package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	stateFile    = ".beads-loop-state.json"
	staleTimeout = 5 * time.Minute
	retryDelay   = 30 * time.Second
)

// State tracks the currently claimed bead across restarts.
type State struct {
	InProgressID string    `json:"in_progress_id,omitempty"`
	StartedAt    time.Time `json:"started_at,omitempty"`
	LastUpdated  time.Time `json:"last_updated,omitempty"`
}

type beadStatus string

const (
	statusOpen       beadStatus = "open"
	statusInProgress beadStatus = "in_progress"
	statusBlocked    beadStatus = "blocked"
	statusClosed     beadStatus = "closed"
	statusDeferred   beadStatus = "deferred"
)

type bead struct {
	ID     string
	Status beadStatus
}

var (
	beadLineRe = regexp.MustCompile(`^[○◐●✓❄]\s+(\S+)`)

	rateLimitPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)limit will reset on (.+?)(?:\n|$|\|)`),
		regexp.MustCompile(`(?i)rate limited until (.+?)(?:\n|$|\|)`),
		regexp.MustCompile(`(?i)try again(?:\s+at)? (.+?)(?:\n|$|\|)`),
		regexp.MustCompile(`(?i)retry after (.+?)(?:\n|$|\|)`),
		regexp.MustCompile(`(?i)resets? at (.+?)(?:\n|$|\|)`),
	}

	timeFormats = []string{
		"Mon, Jan 2, 2006, 3:04 PM",
		"Mon, Jan 2, 2006 at 3:04 PM",
		"Monday, January 2, 2006 at 3:04 PM MST",
		"January 2, 2006 at 3:04 PM",
		"January 2, 2006, 3:04 PM",
		"Jan 2, 2006, 3:04 PM",
		"Jan 2, 2006 at 3:04 PM",
		time.RFC1123,
		time.RFC1123Z,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z07:00",
	}
)

func main() {
	fmt.Println("beads-loop v1.0 — autonomous bead implementer")
	fmt.Printf("[%s] starting loop\n\n", ts())

	// Handle Ctrl+C: exit cleanly but keep state so work can resume.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Printf("\n[%s] interrupted — state preserved for resume\n", ts())
		os.Exit(0)
	}()

	for {
		state := loadState()

		// Resume a recently active in-progress bead.
		if state.InProgressID != "" {
			age := time.Since(state.LastUpdated)
			if age < staleTimeout {
				fmt.Printf("[%s] resuming bead %s (last active %s ago)\n",
					ts(), state.InProgressID, age.Round(time.Second))
				retryAt, _ := implement(state.InProgressID)
				if retryAt != nil {
					waitRateLimit(retryAt)
					continue
				}
				clearState()
				continue
			}
			fmt.Printf("[%s] stale state for %s (%s old), re-evaluating\n",
				ts(), state.InProgressID, age.Round(time.Second))
			clearState()
		}

		beadID, waitUntil, allDone := findWork()

		if allDone {
			fmt.Printf("\n[%s] ALL DONE!\n", ts())
			os.Exit(0)
		}

		if waitUntil != nil {
			waitRateLimit(waitUntil)
			continue
		}

		if beadID == "" {
			fmt.Printf("[%s] no beads ready — waiting %s\n", ts(), retryDelay)
			time.Sleep(retryDelay)
			continue
		}

		fmt.Printf("[%s] claiming %s\n", ts(), beadID)
		if err := claimBead(beadID); err != nil {
			fmt.Printf("[%s] claim failed: %v\n", ts(), err)
			time.Sleep(5 * time.Second)
			continue
		}

		saveState(beadID)

		fmt.Printf("\n%s\n[%s] implementing %s\n%s\n\n",
			strings.Repeat("─", 60), ts(), beadID, strings.Repeat("─", 60))

		retryAt, produced := implement(beadID)
		if retryAt != nil {
			waitRateLimit(retryAt)
			// Keep state — will resume this bead after the limit expires.
			continue
		}

		clearState()

		if !produced {
			// claude exited immediately without output (e.g. bad flag, config
			// error). Back off to avoid a hot retry loop.
			fmt.Printf("[%s] claude produced no output — backing off 15s\n", ts())
			time.Sleep(15 * time.Second)
		}
	}
}

// findWork returns the next bead to work on.
// Priority: in_progress (resume) > ready (start new).
// Returns allDone=true when nothing is open, in_progress, or blocked.
func findWork() (beadID string, waitUntil *time.Time, allDone bool) {
	// Check the full list first so we can resume an in_progress bead.
	listOut, listErr := run("bd", "list")
	if listErr == nil {
		beads := parseBeads(listOut)
		hasActive := false
		for _, b := range beads {
			switch b.Status {
			case statusOpen, statusInProgress, statusBlocked:
				hasActive = true
			}
			if b.Status == statusInProgress {
				return b.ID, nil, false
			}
		}
		// allDone when list is empty or every bead is closed/deferred.
		if !hasActive {
			return "", nil, true
		}
	}

	// Look for beads that are ready to start.
	readyOut, readyErr := run("bd", "ready")
	if readyErr == nil {
		for _, line := range strings.Split(readyOut, "\n") {
			m := beadLineRe.FindStringSubmatch(strings.TrimSpace(line))
			if m != nil {
				return m[1], nil, false
			}
		}
	}

	// If bd list also errored, we can't determine state.
	if listErr != nil {
		fmt.Printf("[%s] bd list error: %v\n", ts(), listErr)
	}

	return "", nil, false
}

// parseBeads parses `bd list` output into a slice of beads.
func parseBeads(output string) []bead {
	var beads []bead
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var status beadStatus
		switch {
		case strings.HasPrefix(line, "○"):
			status = statusOpen
		case strings.HasPrefix(line, "◐"):
			status = statusInProgress
		case strings.HasPrefix(line, "●"):
			status = statusBlocked
		case strings.HasPrefix(line, "✓"):
			status = statusClosed
		case strings.HasPrefix(line, "❄"):
			status = statusDeferred
		default:
			continue
		}
		m := beadLineRe.FindStringSubmatch(line)
		if m != nil {
			beads = append(beads, bead{ID: m[1], Status: status})
		}
	}
	return beads
}

func claimBead(id string) error {
	_, err := run("bd", "update", id, "--status=in_progress")
	return err
}

// implement runs claude to implement the given bead, streaming output to stdout.
// Returns (retryAt, produced): retryAt is non-nil when rate-limited;
// produced is true when claude emitted at least one character of output.
func implement(beadID string) (*time.Time, bool) {
	cmd := exec.Command("claude",
		"--print",
		"--verbose",
		"--dangerously-skip-permissions",
		"--output-format", "stream-json",
		"--include-partial-messages",
		fmt.Sprintf("implement bead %s", beadID),
	)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Printf("[%s] pipe error: %v\n", ts(), err)
		return nil, false
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("[%s] start error: %v\n", ts(), err)
		return nil, false
	}

	// Keep the state timestamp fresh while claude is running.
	stopTick := make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				touchState(beadID)
			case <-stopTick:
				return
			}
		}
	}()

	var retryAt *time.Time
	sr := &streamer{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)

	for scanner.Scan() {
		rt, text := sr.event(scanner.Text())
		if rt != nil {
			retryAt = rt
		}
		if text != "" {
			fmt.Print(text)
		}
	}

	close(stopTick)
	cmd.Wait()
	if !sr.atLineStart {
		fmt.Println()
	}
	return retryAt, sr.hadOutput
}

// streamer holds formatting state across stream-json events.
type streamer struct {
	atLineStart bool // true when the cursor is at column 0
	inTool      bool // true while inside a tool_use content block
	hadOutput   bool // true once any text has been printed
}

// nl returns "\n" if the cursor is not already at the start of a line.
func (sr *streamer) nl() string {
	if sr.atLineStart {
		return ""
	}
	sr.atLineStart = true
	return "\n"
}

// event processes one line of stream-json output and returns text to print.
func (sr *streamer) event(line string) (*time.Time, string) {
	if line == "" {
		return nil, ""
	}

	var ev map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return nil, ""
	}

	var typ string
	json.Unmarshal(ev["type"], &typ)

	switch typ {

	case "content_block_start":
		var cbs struct {
			ContentBlock struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if json.Unmarshal([]byte(line), &cbs) != nil {
			return nil, ""
		}
		if cbs.ContentBlock.Type == "tool_use" {
			sr.inTool = true
			// Always start the tool label on its own line.
			prefix := sr.nl()
			label := fmt.Sprintf("\033[2m▶ %s\033[0m\n", cbs.ContentBlock.Name)
			sr.atLineStart = true
			return nil, prefix + label
		}
		// text block starting — no output needed
		sr.inTool = false
		return nil, ""

	case "content_block_stop":
		if sr.inTool {
			sr.inTool = false
			// Ensure subsequent text starts on a fresh line.
			out := sr.nl()
			return nil, out
		}
		return nil, ""

	case "content_block_delta":
		if sr.inTool {
			return nil, "" // skip tool input JSON chunks
		}
		var d struct {
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(line), &d) != nil || d.Delta.Type != "text_delta" || d.Delta.Text == "" {
			return nil, ""
		}
		sr.hadOutput = true
		sr.atLineStart = strings.HasSuffix(d.Delta.Text, "\n")
		return nil, d.Delta.Text

	case "message_start":
		// A new assistant turn is beginning (after tool results).
		// Always separate turns with a blank line for readability.
		if sr.hadOutput {
			var out string
			if sr.atLineStart {
				out = "\n" // already on fresh line, just add blank line
			} else {
				out = "\n\n" // end current line + blank line
			}
			sr.atLineStart = true
			return nil, out
		}
		return nil, ""

	case "assistant":
		// Complete assistant turn (fallback when partial streaming isn't active).
		var a struct {
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &a) != nil {
			return nil, ""
		}
		var sb strings.Builder
		for _, c := range a.Message.Content {
			if c.Type == "text" {
				sb.WriteString(c.Text)
			}
		}
		text := sb.String()
		if text != "" {
			sr.hadOutput = true
			sr.atLineStart = strings.HasSuffix(text, "\n")
		}
		return nil, text

	case "result":
		var r struct {
			Subtype string  `json:"subtype"`
			Error   string  `json:"error"`
			Cost    float64 `json:"total_cost_usd"`
		}
		json.Unmarshal([]byte(line), &r)
		if r.Subtype == "error" && r.Error != "" {
			fmt.Printf("%s\033[31m[error: %s]\033[0m\n", sr.nl(), r.Error)
			sr.atLineStart = true
			if rt := parseRateLimit(r.Error); rt != nil {
				return rt, ""
			}
		}
		if r.Cost > 0 {
			fmt.Printf("%s\033[2m[cost: $%.4f]\033[0m\n", sr.nl(), r.Cost)
			sr.atLineStart = true
		}
	}

	return nil, ""
}

// parseRateLimit looks for a rate limit reset time in a string.
func parseRateLimit(s string) *time.Time {
	for _, pat := range rateLimitPatterns {
		m := pat.FindStringSubmatch(s)
		if len(m) < 2 {
			continue
		}
		timeStr := strings.TrimSpace(m[1])
		for _, format := range timeFormats {
			t, err := time.ParseInLocation(format, timeStr, time.Local)
			if err == nil {
				return &t
			}
		}
		// Could not parse the time string; fall back to waiting 1 hour.
		t := time.Now().Add(time.Hour)
		return &t
	}
	return nil
}

func waitRateLimit(t *time.Time) {
	d := time.Until(*t)
	if d <= 0 {
		return
	}
	fmt.Printf("[%s] rate limited — waiting until %s (%s)\n",
		ts(), t.Format("15:04:05"), d.Round(time.Second))
	time.Sleep(d)
	fmt.Printf("[%s] rate limit expired, resuming\n", ts())
}

// ── state helpers ────────────────────────────────────────────────────────────

func loadState() State {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return State{}
	}
	var s State
	json.Unmarshal(data, &s)
	return s
}

func saveState(id string) {
	s := State{
		InProgressID: id,
		StartedAt:    time.Now(),
		LastUpdated:  time.Now(),
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(stateFile, data, 0644)
}

// touchState updates LastUpdated without changing anything else.
func touchState(id string) {
	s := loadState()
	if s.InProgressID != id {
		return
	}
	s.LastUpdated = time.Now()
	data, _ := json.MarshalIndent(s, "", "  ")
	os.WriteFile(stateFile, data, 0644)
}

func clearState() {
	os.Remove(stateFile)
}

// ── utilities ────────────────────────────────────────────────────────────────

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

func ts() string {
	return time.Now().Format("15:04:05")
}
