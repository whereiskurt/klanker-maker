---
phase: 26-live-operations-hardening
plan: "04"
subsystem: testing, cli
tags: [go, testing, eventbridge, remote-commands, list, ansi-color, tdd]
dependency_graph:
  requires:
    - phase: 26-live-operations-hardening
      plan: 02
      provides: Green test baseline and publishRemoteCommand signature with cfg
  provides: [HARD-03, HARD-06]
  affects: [internal/app/cmd]
tech_stack:
  added: []
  patterns:
    - "RemoteCommandPublisher interface for testable EventBridge dispatch (same pattern as SandboxFetcher)"
    - "WithPublisher constructors alongside default constructors (nil = real AWS publisher)"
    - "colorizeListStatus() for ANSI status coloring in table output"
    - "TDD RED-GREEN pattern for all new tests"
key_files:
  created:
    - internal/app/cmd/remote_publisher.go
    - internal/app/cmd/stop_test.go
  modified:
    - internal/app/cmd/destroy.go
    - internal/app/cmd/destroy_test.go
    - internal/app/cmd/extend.go
    - internal/app/cmd/extend_test.go
    - internal/app/cmd/list.go
    - internal/app/cmd/list_test.go
    - internal/app/cmd/stop.go
decisions:
  - "Extracted RemoteCommandPublisher interface and realRemotePublisher from publishRemoteCommand in destroy.go — cleaner than package-level variable override"
  - "WithPublisher constructors follow existing SandboxFetcher/NewShellCmdWithFetcher pattern"
  - "colorizeListStatus applied at print time, not at record level — keeps SandboxRecord data pure"
  - "Removed orphaned runRemoteDestroy and publishRemoteCommand from destroy.go after extraction to remote_publisher.go"
  - "Running/stopped regression test uses ecs substrate to avoid EC2 live status check in unit tests"
metrics:
  duration_minutes: 10
  tasks_completed: 2
  tasks_total: 2
  files_changed: 9
  completed_date: "2026-03-27"
---

# Phase 26 Plan 04: Test and Fix --remote Flag, Failed/Partial Status in km list Summary

**One-liner:** RemoteCommandPublisher interface enables mock-tested --remote paths for destroy/extend/stop; km list now shows failed/partial sandboxes in red/yellow with ANSI color coding.

## What Was Built

### Task 1: --remote flag tests and interface extraction

Added `RemoteCommandPublisher` interface to the `cmd` package with a `realRemotePublisher` implementation that wraps the existing AWS EventBridge dispatch logic. Three "WithPublisher" constructors were added (`NewDestroyCmdWithPublisher`, `NewExtendCmdWithPublisher`, `NewStopCmdWithPublisher`) following the established `NewShellCmdWithFetcher` pattern. The original constructors now delegate to `WithPublisher(nil)`.

Created `stop_test.go` with:
- `TestStopCmd_RemotePublishesCorrectEvent` — verifies sandbox ID and "stop" event type
- `TestStopCmd_RemotePublishFailure` — verifies error propagation from publisher
- `TestStopCmd_RemoteInvalidSandboxID` — verifies invalid IDs rejected before publish

Extended `destroy_test.go` with:
- `TestDestroyCmd_RemotePublishesCorrectEvent` — verifies "destroy" event type and sandbox ID
- `TestDestroyCmd_RemotePublishFailure` — verifies error propagation
- `TestDestroyCmd_RemoteInvalidSandboxID` — verifies format validation before publish

Extended `extend_test.go` with:
- `TestExtendCmd_RemotePublishesCorrectEvent` — verifies "extend" event type, sandbox ID, and duration="2h" in extra params
- `TestExtendCmd_RemotePublishFailure` — verifies error propagation
- `TestExtendCmd_RemoteInvalidSandboxID` — verifies invalid IDs rejected before publish

### Task 2: Failed/partial status in km list

Added `colorizeListStatus()` to `list.go` that applies ANSI color codes to the status column:
- `"failed"` → red (`\033[31m`)
- `"partial"`, `"killed"` → yellow (`\033[33m`)
- `"running"` → green (`\033[32m`)
- others → no color

`printSandboxTable` now uses the colorized status. Failed/partial sandboxes retain their row number (#N) so operators can reference them for `km destroy` cleanup.

Four new tests in `list_test.go`:
- `TestListCmd_FailedSandboxDisplaysRedStatus` — checks ANSI red escape code in output
- `TestListCmd_PartialSandboxDisplaysYellowStatus` — checks ANSI yellow escape code
- `TestListCmd_FailedSandboxIsNumbered` — verifies row number assigned to failed sandbox
- `TestListCmd_RunningAndStoppedNoRegression` — regression test for existing statuses

## Task Commits

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 RED | Failing tests for --remote paths | 7e22e47 | stop_test.go (new), destroy_test.go, extend_test.go |
| 1 GREEN | RemoteCommandPublisher interface and constructors | e439c80 | remote_publisher.go (new), destroy.go, extend.go, stop.go |
| 2 RED | Failing tests for failed/partial list status | 1259ae2 | list_test.go |
| 2 GREEN | Colored status in km list | 81827ff | list.go |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Removed orphaned publishRemoteCommand and runRemoteDestroy from destroy.go**
- **Found during:** Task 1 GREEN (after extracting logic to remote_publisher.go, old functions became unreachable)
- **Issue:** publish logic was duplicated between destroy.go and remote_publisher.go
- **Fix:** Removed `runRemoteDestroy` and `publishRemoteCommand` from destroy.go; also removed the now-unused `eventbridge` import
- **Files modified:** `internal/app/cmd/destroy.go`

**2. [Rule 1 - Bug] Regression test for "running" status used ec2 substrate which triggers live EC2 status check**
- **Found during:** Task 2 RED — `TestListCmd_RunningAndStoppedNoRegression` failed because "running" ec2 records get their status replaced by `checkEC2InstanceStatus`
- **Fix:** Changed substrate from "ec2" to "ecs" in the running sandbox test record — ecs substrate skips the EC2 live check
- **Files modified:** `internal/app/cmd/list_test.go`

None — plan executed with two minor auto-fixes.

## Verification Results

Remote tests:
```
--- PASS: TestDestroyCmd_RemotePublishesCorrectEvent
--- PASS: TestDestroyCmd_RemotePublishFailure
--- PASS: TestDestroyCmd_RemoteInvalidSandboxID
--- PASS: TestExtendCmd_RemotePublishesCorrectEvent
--- PASS: TestExtendCmd_RemotePublishFailure
--- PASS: TestExtendCmd_RemoteInvalidSandboxID
--- PASS: TestStopCmd_RemotePublishesCorrectEvent
--- PASS: TestStopCmd_RemotePublishFailure
--- PASS: TestStopCmd_RemoteInvalidSandboxID
```

List status tests:
```
--- PASS: TestListCmd_FailedSandboxDisplaysRedStatus
--- PASS: TestListCmd_PartialSandboxDisplaysYellowStatus
--- PASS: TestListCmd_FailedSandboxIsNumbered
--- PASS: TestListCmd_RunningAndStoppedNoRegression
```

Full suite: `go test ./... -count=1` — all packages green, no regressions.

## Self-Check: PASSED

Files exist:
- FOUND: internal/app/cmd/remote_publisher.go
- FOUND: internal/app/cmd/stop_test.go

Commits exist:
- FOUND: 7e22e47
- FOUND: e439c80
- FOUND: 1259ae2
- FOUND: 81827ff

---
*Phase: 26-live-operations-hardening*
*Completed: 2026-03-27*
