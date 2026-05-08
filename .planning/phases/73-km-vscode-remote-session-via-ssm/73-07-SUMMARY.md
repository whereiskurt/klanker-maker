---
phase: 73-km-vscode-remote-session-via-ssm
plan: 07
subsystem: cli
tags: [vscode, ssh, destroy, cleanup, operator-state]

# Dependency graph
requires:
  - phase: 73-km-vscode-remote-session-via-ssm
    plan: 03
    provides: RemoveHost function in sshconfig.go
provides:
  - cleanupVSCodeState helper in destroy.go called from both runDestroy and runDestroyDocker
  - km destroy removes Host km-<id> block from ~/.ssh/config after successful teardown
  - km destroy deletes ~/.km/keys/<id> and <id>.pub after successful teardown
affects:
  - 73-km-vscode-remote-session-via-ssm plan 09 (UAT — manual smoke test covers this cleanup)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Cleanup helper extracted as cleanupVSCodeState(sandboxID) and called from both destroy paths"
    - "Unconditional cleanup post-AWS-teardown: no profile flag, idempotent on missing files"
    - "Non-fatal warn logging for cleanup errors: flaky cleanup never fails the destroy command"

key-files:
  created: []
  modified:
    - internal/app/cmd/destroy.go
    - internal/app/cmd/destroy_test.go

key-decisions:
  - "Factored cleanup into cleanupVSCodeState helper to avoid duplicating code in runDestroy and runDestroyDocker"
  - "Cleanup runs unconditionally (no DDB metadata read) — missing files are non-errors, safe for pre-Phase-73 sandboxes"
  - "Cleanup runs AFTER successful AWS teardown so retries still find keys on disk"

patterns-established:
  - "Phase 73 cleanup pattern: RemoveHost + os.Remove with os.IsNotExist guard, warn-level errors"

requirements-completed:
  - GOAL-6

# Metrics
duration: 6min
completed: 2026-05-08
---

# Phase 73 Plan 07: VSCode Destroy Cleanup Summary

**km destroy now removes Host km-<id> SSH config block and ~/.km/keys/<id>* key files after successful AWS teardown via a new cleanupVSCodeState helper**

## Performance

- **Duration:** 6 min
- **Started:** 2026-05-08T00:02:14Z
- **Completed:** 2026-05-08T00:08:36Z
- **Tasks:** 1 (TDD: test + implementation commits)
- **Files modified:** 2

## Accomplishments
- Added `cleanupVSCodeState(sandboxID string)` helper that removes the `Host km-<id>` block from `~/.ssh/config` via `RemoveHost` and deletes `~/.km/keys/<id>` and `~/.km/keys/<id>.pub` via `os.Remove`
- Called cleanup in both `runDestroy` (EC2/remote path, line ~566) and `runDestroyDocker` (line ~745) after successful AWS teardown
- Cleanup is unconditional, idempotent, and non-fatal — missing files or missing ssh-config entries are silently ignored, so pre-Phase-73 sandboxes are handled cleanly

## Task Commits

TDD task with test-then-implementation flow:

1. **RED: test(73-07): add failing test for Phase 73 vscode cleanup** - `7fc66b7` (test)
2. **GREEN: feat(73-07): add Phase 73 vscode cleanup to km destroy** - `fca27b8` (feat)

## Files Created/Modified
- `internal/app/cmd/destroy.go` - Added `cleanupVSCodeState` helper and two call sites (one per destroy path)
- `internal/app/cmd/destroy_test.go` - Added `TestRunDestroy_Phase73VSCodeCleanup` source-level verification test

## Decisions Made
- Factored cleanup into a shared `cleanupVSCodeState` helper rather than duplicating the block in both paths — cleaner, easier to test
- Used source-level verification test pattern (matches existing tests in destroy_test.go) that checks for key patterns in the source file

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
- Pre-existing test failures in `TestRunDestroy_GitHubTokenCleanup` and others were present before this plan and are out of scope (confirmed via `git stash` verification)

## Next Phase Readiness
- km destroy cleanup complete; operator state is fully tidied on destroy
- Phase 73-09 UAT can exercise the full flow: create sandbox → verify ~/.km/keys/sb-X exists → destroy → verify files gone + ssh-config block removed

---
*Phase: 73-km-vscode-remote-session-via-ssm*
*Completed: 2026-05-08*
