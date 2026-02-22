# beads-loop

Autonomous agent loop that implements [beads](https://github.com/steveyegge/beads) issues using Claude Code without supervision.

> **Note:** The `bd` / beads issue tracker used here is [Steve Yegge's beads](https://github.com/steveyegge/beads). This project runs on top of it.

## What it does

`beads-loop` runs a continuous loop that:

1. Finds the next ready bead (`bd ready` / `bd list`)
2. Claims it (`bd update --status=in_progress`)
3. Launches `claude` to implement it, streaming output to the console
4. Handles rate limits — parses the reset time and waits automatically
5. Repeats until all beads are closed, then exits with `ALL DONE!`

It is **idempotent**: if interrupted and restarted, it resumes the in-progress bead automatically. A bead is considered stale (and re-evaluated) if it hasn't been touched in 5 minutes.

## Prerequisites

- [Go](https://go.dev/) 1.21+
- [bd](https://github.com/steveyegge/beads) — Steve Yegge's beads CLI on your `$PATH`
- [claude](https://claude.ai/claude-code) — Claude Code CLI on your `$PATH`
- A beads-tracked project directory (`.beads/` present)

## Install

```bash
git clone https://github.com/michelroberge/beads-loop
cd beads-loop
make install          # builds and copies to /usr/local/bin
```

To uninstall:

```bash
make uninstall
```

## Usage

Run from inside any beads-tracked project:

```bash
cd /your/project
beads-loop
```

The loop streams Claude's output to the terminal, including tool calls (shown dimmed) and per-turn cost.

### Stop it

Press `Ctrl+C` — state is preserved so it can resume cleanly on the next run.

Or use the helper script:

```bash
./kill-beads-loop.sh
```

## How it works

```
┌─────────────────────────────────────────────────────┐
│                     beads-loop                      │
│                                                     │
│  load state ──► resume in-progress bead?            │
│                         │ yes                       │
│                         ▼                           │
│              run claude (implement bead-id)         │
│                         │                           │
│                 ┌───────┴────────┐                  │
│            rate limit?      completed?              │
│                 │                │                  │
│            wait & retry     clear state             │
│                                  │                  │
│                             find next bead          │
│                          (bd list / bd ready)       │
│                                  │                  │
│                    ┌─────────────┼─────────────┐    │
│               all done?    bead found?    none ready │
│                  │              │               │   │
│              exit 0         claim it        wait 30s │
└─────────────────────────────────────────────────────┘
```

State is persisted to `.beads-loop-state.json` in the project directory. The state timestamp is refreshed every 2 minutes while Claude is running.

## Rate limit handling

When Claude hits a usage limit, `beads-loop` parses the reset time from the error message and sleeps until it expires. Supported message formats include:

- `limit will reset on <time>`
- `rate limited until <time>`
- `try again at <time>`
- `retry after <time>`
- `resets at <time>`

If the time cannot be parsed, it backs off for 1 hour.

## Build from source

```bash
make build       # produces ./beads-loop binary
make install     # build + install to /usr/local/bin
make clean       # remove local binary
```

## State file

`.beads-loop-state.json` is written to the project root while a bead is in progress:

```json
{
  "in_progress_id": "bd-abc",
  "started_at": "2026-02-22T10:00:00Z",
  "last_updated": "2026-02-22T10:02:00Z"
}
```

The file is removed automatically when the bead completes. It is safe to delete manually if you want to force a fresh start.

## License

MIT
