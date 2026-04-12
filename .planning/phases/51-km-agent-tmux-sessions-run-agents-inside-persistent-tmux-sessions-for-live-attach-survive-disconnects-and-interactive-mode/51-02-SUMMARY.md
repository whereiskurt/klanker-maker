---
phase: 51-km-agent-tmux-sessions
plan: 02
subsystem: agent
tags: [tmux, ssm, agent, attach, interactive, session-persistence]

# Dependency graph
requires:
  - phase: 51-km-agent-tmux-sessions-01
    provides: "BuildAgentShellCommands returns ([]string, string) with RUN_ID and tmux wrapping"
provides:
  - "km agent attach subcommand for connecting to live tmux sessions"
  - "km agent run --interactive flag for create-and-attach in one step"
  - "Mutual exclusivity validation between --interactive and --wait"
affects: [51-03, km-agent-workflows]

# Tech tracking
tech-stack:
  added: []
  patterns: [ssm-start-session-tmux-attach, interactive-tmux-create-attach]

key-files:
  created: []
  modified:
    - internal/app/cmd/agent.go
    - internal/app/cmd/agent_test.go

key-decisions:
  - "attach targets latest km-agent-* session via tmux list-sessions grep/tail pattern"
  - "--interactive sends only script-write commands via SendCommand, then uses SSM start-session for tmux new-session (no -d)"
  - "2-second sleep between script write and tmux session creation to ensure disk landing"

patterns-established:
  - "SSM start-session with tmux attach-session for live agent observation"
  - "Two-phase interactive: write script via SendCommand, attach via start-session"

requirements-completed: [TMUX-03, TMUX-04]

# Metrics
duration: 6min
completed: 2026-04-12
---

# Phase 51 Plan 02: Agent Attach and Interactive Mode Summary

**km agent attach subcommand and --interactive flag for live tmux session connection via SSM start-session**

## Performance

- **Duration:** 6 min
- **Started:** 2026-04-12T14:17:44Z
- **Completed:** 2026-04-12T14:23:16Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments
- Added `km agent attach <sandbox>` subcommand that resolves sandbox to instance ID and opens SSM session with tmux attach-session targeting latest km-agent-* session
- Added `--interactive` flag to `km agent run` that writes script to disk via SendCommand then opens SSM session with tmux new-session (attached, no -d flag)
- Added mutual exclusivity validation between --interactive and --wait flags
- All 17 agent tests pass including 4 new tests

## Task Commits

Each task was committed atomically (TDD: RED then GREEN):

1. **Task 1: Add km agent attach subcommand** - `3047cc4` (test: RED), `7399367` (feat: GREEN)
2. **Task 2: Add --interactive flag to km agent run** - `abdbd88` (test: RED), `85e0177` (feat: GREEN)

## Files Created/Modified
- `internal/app/cmd/agent.go` - Added newAgentAttachCmd, --interactive flag, interactive path in runAgentNonInteractive
- `internal/app/cmd/agent_test.go` - Added TestAgentAttach, TestAgentAttach_StoppedSandbox, TestAgentInteractive, TestAgentInteractive_MutuallyExclusiveWithWait

## Decisions Made
- attach uses `tmux list-sessions -F '#{session_name}' | grep km-agent | tail -1` to find the latest agent session rather than requiring an explicit session name
- --interactive sends only the first 2 commands (script write + chmod) via SendCommand, then uses SSM start-session for the tmux session creation so the operator gets an attached terminal immediately
- 2-second delay between script write and tmux session to ensure the script lands on disk before tmux tries to execute it

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- attach and --interactive are ready for use with any tmux-wrapped agent sessions created by Plan 01
- Plan 03 (if any) can build on these capabilities for session management and monitoring

---
*Phase: 51-km-agent-tmux-sessions*
*Completed: 2026-04-12*
