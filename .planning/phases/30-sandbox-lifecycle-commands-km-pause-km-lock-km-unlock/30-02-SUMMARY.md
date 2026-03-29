---
phase: 30-sandbox-lifecycle-commands-km-pause-km-lock-km-unlock
plan: "02"
subsystem: cli
tags: [cobra, s3, metadata, lock, unlock, eventbridge, tdd]

# Dependency graph
requires:
  - phase: 30-sandbox-lifecycle-commands-km-pause-km-lock-km-unlock
    plan: "01"
    provides: SandboxMetadata.Locked/LockedAt fields, km pause command, NewXxxCmdWithPublisher pattern
provides:
  - km lock command with --remote EventBridge dispatch and S3 metadata write
  - km unlock command with --yes flag, confirmation prompt, --remote dispatch
  - CheckSandboxLock helper for fail-open lock enforcement in destroy/stop/pause
  - Lock guards in runDestroy, runStop, runPause
  - pause/lock/unlock registered in root.go
  - README.md, docs/user-manual.md, CLAUDE.md updated with new commands
affects:
  - CLI help output (km --help shows pause, lock, unlock)
  - destroy/stop/pause all blocked when sandbox is locked

# Tech tracking
tech-stack:
  added: []
  patterns:
    - NewLockCmdWithPublisher / NewUnlockCmdWithPublisher follows stop/extend publisher injection pattern
    - CheckSandboxLock fail-open pattern: nil StateBucket, AWS config failure, missing metadata all return nil
    - TDD: failing tests written before implementation for all lock/unlock behaviors

key-files:
  created:
    - internal/app/cmd/lock.go
    - internal/app/cmd/lock_test.go
    - internal/app/cmd/unlock.go
    - internal/app/cmd/unlock_test.go
    - internal/app/cmd/help/lock.txt
    - internal/app/cmd/help/unlock.txt
  modified:
    - internal/app/cmd/stop.go
    - internal/app/cmd/pause.go
    - internal/app/cmd/destroy.go
    - internal/app/cmd/root.go
    - README.md
    - docs/user-manual.md
    - CLAUDE.md

key-decisions:
  - "CheckSandboxLock is fail-open: returns nil if StateBucket empty, AWS config fails, or metadata missing — prevents lock check from blocking commands when metadata is unavailable"
  - "runStop signature changed from (ctx, sandboxID) to (ctx, cfg, sandboxID) for consistency with pause/extend and to enable lock guard"
  - "Lock guard in runDestroy placed after credential validation but before tag discovery — avoids expensive AWS API calls on locked sandboxes"
  - "NewUnlockCmdWithPublisher: --remote path skips confirmation prompt (publisher handles the event), --yes flag skips local confirmation"

requirements-completed:
  - LOCK-01
  - LOCK-02
  - LOCK-03
  - UNLOCK-01
  - UNLOCK-02

# Metrics
duration: 6min
completed: 2026-03-29
---

# Phase 30 Plan 02: km lock and km unlock commands with lock guards Summary

**km lock/unlock commands with S3 metadata write, --remote EventBridge dispatch, and fail-open CheckSandboxLock guard wired into destroy/stop/pause**

## Performance

- **Duration:** 6 min
- **Started:** 2026-03-29T04:39:32Z
- **Completed:** 2026-03-29T04:45:44Z
- **Tasks:** 3
- **Files modified:** 13

## Accomplishments

- Implemented `km lock` command: sets `meta.Locked=true` and `meta.LockedAt`, errors on empty StateBucket, follows NewXxxCmdWithPublisher injection pattern
- Implemented `km unlock` command: sets `meta.Locked=false`/`meta.LockedAt=nil`, requires y/N confirmation unless `--yes`, --remote path via EventBridge
- Created `CheckSandboxLock` helper: fail-open (returns nil) when StateBucket is empty, AWS config unavailable, or metadata missing
- Added lock guard at top of `runStop` (signature changed to accept cfg), `runPause`, and `runDestroy` (after credential validation)
- Registered `NewLockCmd` and `NewUnlockCmd` in root.go (NewPauseCmd already registered by plan 01)
- All 7 unit tests pass (including fail-open guard test)
- `make build` succeeds; `./km --help` shows pause, lock, unlock

## Task Commits

Each task was committed atomically:

1. **Task 1: Create km lock and km unlock commands with tests** - `22366b1` (feat)
2. **Task 2: Add lock guards to destroy/stop/pause and register all commands in root.go** - `fdf2805` (feat)
3. **Task 3: Update README, user manual, and CLAUDE.md with new commands** - `2db6d79` (docs)

## Files Created/Modified

- `internal/app/cmd/lock.go` - km lock command with NewLockCmd/NewLockCmdWithPublisher/runLock/CheckSandboxLock
- `internal/app/cmd/lock_test.go` - 4 tests: remote publish, invalid ID, state bucket guard, fail-open behavior
- `internal/app/cmd/unlock.go` - km unlock command with NewUnlockCmd/NewUnlockCmdWithPublisher/runUnlock
- `internal/app/cmd/unlock_test.go` - 3 tests: remote publish, invalid ID, state bucket guard
- `internal/app/cmd/help/lock.txt` - Embedded help text for km lock
- `internal/app/cmd/help/unlock.txt` - Embedded help text for km unlock
- `internal/app/cmd/stop.go` - runStop signature extended to (ctx, cfg, sandboxID), lock guard added
- `internal/app/cmd/pause.go` - CheckSandboxLock guard added after StateBucket check
- `internal/app/cmd/destroy.go` - CheckSandboxLock guard added as Step 2b after credential validation
- `internal/app/cmd/root.go` - NewLockCmd and NewUnlockCmd registered
- `README.md` - km pause/lock/unlock rows added to sandbox lifecycle command table
- `docs/user-manual.md` - Full sections added for km pause, km lock, km unlock with flags, behavior, examples, ToC updated
- `CLAUDE.md` - km pause/lock/unlock added to CLI quick-reference section

## Decisions Made

- CheckSandboxLock is fail-open by design: a sandbox without reachable metadata should not be permanently blocked from destroy/stop/pause. This is intentional and consistent with "missing metadata shouldn't block the operator."
- Lock guard in destroy goes AFTER credential validation (Step 2b) but BEFORE tag discovery and resource operations — avoids expensive API calls on locked sandboxes while still requiring valid credentials to query lock state.
- runStop signature was updated to accept cfg (Option A from plan), maintaining consistency with pause/extend pattern.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Phase 30 complete: km pause, km lock, km unlock all working and registered
- Lock/unlock state persisted to S3 metadata.json (same file used by km list/status)
- All commands available via --remote EventBridge dispatch path

---
*Phase: 30-sandbox-lifecycle-commands-km-pause-km-lock-km-unlock*
*Completed: 2026-03-29*
