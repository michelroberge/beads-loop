GREENFIELD PROJECT
Create a simple go application. I want to be able to run it from anywhere on this system.

The goal is to launch claude.exe in a loop to implement all beads without supervision.


When I run it, it should

1. launch command 'bd ready' to see if there are any beads ready.

example of output:

michelroberge@Mac agiler3 % bd ready
○ ag-uxb ● P1 Implement AiSkillsService and AiGatesService CRUD

--------------------------------------------------------------------------------
Ready: 1 issues with no active blockers

Status: ○ open  ◐ in_progress  ● blocked  ✓ closed  ❄ deferred

If it is empty, you can launch 'bd list' to see all beads.

example of output: 
michelroberge@Mac agiler3 % bd list
○ ag-4sc [● P2] [task] - Create useAiSkill hook (blocked by: ag-n2n)
○ ag-7s5 [● P3] [task] - Add example AI widget integration to task module (blocked by: ag-n2n, ag-va8)

If a bead is in progress, that is the bead that should be picked up.

2. Claim the bead.

3. Launch claude.exe OR claude SDK - whichever works best - to implement the bead (prompt: implement bead bd-123).

4. This should stream the response to console like in claude.

5. This should auto-approve everything.

6. If limit is reached, it should pick up the until when it is blocked, and wait until then before re-try.

7. When all beads are closed, it should end up with a "ALL DONE!" and exit.

8. This must be idempotent - if for some reason it exists, it should be able to resume on the bead it was working on. I suggest to track in-progress in a json file with a time-stamp; if an item is not updated for more than 5 minutes, we can assume it can be picked up.
