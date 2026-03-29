---
phase: 30-sandbox-lifecycle-commands-km-pause-km-lock-km-unlock
plan: "01"
subsystem: cli
tags: [cobra, ec2, hibernate, eventbridge, s3, metadata]

# Dependency graph
requires:
  - phase: 26-live-operations-hardening-bootstrap-init-create-destroy-ttl-auto-destroy-idle-detection-sidecar-fixes-proxy-enforcement-cli-polish
    provides: RemoteCommandPublisher interface, stop/extend/destroy --remote pattern
provides:
  - km pause command with --remote EventBridge dispatch and EC2 hibernate (Hibernate=true)
  - SandboxMetadata.Locked and SandboxMetadata.LockedAt fields for km lock/unlock (plan 02)
affects:
  - 30-02 (lock/unlock commands use Locked/LockedAt fields added here)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - NewXxxCmdWithPublisher constructor pattern for --remote testability (mirrors stop/extend)
    - TDD: test file with failing tests before implementation

key-files:
  created:
    - internal/app/cmd/pause.go
    - internal/app/cmd/pause_test.go
    - internal/app/cmd/help/pause.txt
  modified:
    - pkg/aws/metadata.go
    - internal/app/cmd/root.go

key-decisions:
  - "Hibernate=true passed to StopInstances; EC2 falls back to normal stop if not configured for hibernation"
  - "runPause requires StateBucket to update metadata status — unlike runStop which skips metadata"
  - "SandboxMetadata gains Locked/LockedAt as omitempty fields to maintain backward JSON compat"

patterns-established:
  - "NewPauseCmdWithPublisher follows identical publisher injection pattern as stop/extend"

requirements-completed:
  - PAUSE-01
  - PAUSE-02
  - PAUSE-03

# Metrics
duration: 8min
completed: 2026-03-29
---

# Phase 30 Plan 01: km pause command with EC2 hibernate and SandboxMetadata lock fields Summary

**km pause command dispatching EventBridge "pause" events or hibernating EC2 instances (Hibernate=true) with metadata status update, plus Locked/LockedAt fields added to SandboxMetadata for plan 02**

## Performance

- **Duration:** 8 min
- **Started:** 2026-03-29T04:29:00Z
- **Completed:** 2026-03-29T04:37:22Z
- **Tasks:** 1
- **Files modified:** 5

## Accomplishments
- Extended SandboxMetadata struct with `Locked bool` and `LockedAt *time.Time` fields (omitempty for backward compatibility)
- Implemented `km pause` command following stop.go pattern with `--remote` flag for EventBridge dispatch
- `runPause` calls `StopInstances` with `Hibernate: aws.Bool(true)` and updates metadata status to "paused"
- All 3 unit tests pass: remote publish correct event, remote publish failure, invalid sandbox ID rejected before publisher call

## Task Commits

Each task was committed atomically:

1. **Task 1: Extend SandboxMetadata and create km pause command with tests** - `c1e1dee` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified
- `pkg/aws/metadata.go` - Added Locked bool and LockedAt *time.Time fields to SandboxMetadata
- `internal/app/cmd/pause.go` - km pause command with NewPauseCmd/NewPauseCmdWithPublisher/runPause
- `internal/app/cmd/pause_test.go` - Unit tests for --remote path (3 tests)
- `internal/app/cmd/help/pause.txt` - Embedded help text
- `internal/app/cmd/root.go` - Registered NewPauseCmd(cfg) in command tree

## Decisions Made
- Used `Hibernate: aws.Bool(true)` in StopInstances — EC2 silently falls back to normal stop if hibernation not configured, which is acceptable behavior
- `runPause` requires `cfg.StateBucket` (unlike `runStop`) because it must write metadata status after stopping
- Locked/LockedAt fields added with `omitempty` so existing metadata.json files without these fields remain valid

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- SandboxMetadata struct ready for Plan 02 lock/unlock commands
- km pause registered in CLI and fully testable via --remote path
- Pattern established for Plan 02 lock/unlock commands (same NewXxxCmdWithPublisher injection pattern)

---
*Phase: 30-sandbox-lifecycle-commands-km-pause-km-lock-km-unlock*
*Completed: 2026-03-29*
