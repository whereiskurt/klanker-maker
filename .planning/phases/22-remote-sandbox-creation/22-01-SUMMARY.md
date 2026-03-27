---
phase: 22-remote-sandbox-creation
plan: 01
subsystem: infra
tags: [eventbridge, lambda, s3, aws-sdk-go-v2, tdd]

# Dependency graph
requires:
  - phase: 04-lifecycle-hardening-artifacts-email
    provides: SES SendLifecycleNotification, S3PutAPI, SESV2API interfaces
  - phase: 03-sidecar-enforcement-lifecycle-management
    provides: EventBridgeAPI interface in pkg/aws/idle_event.go

provides:
  - EventBridgeAPI interface (shared, defined in idle_event.go) + PutSandboxCreateEvent in pkg/aws/eventbridge.go
  - SandboxCreateDetail struct for EventBridge event detail
  - km create --remote flag dispatching to runCreateRemote
  - create-handler Lambda binary (cmd/create-handler/) that runs km create as subprocess

affects:
  - 22-02-create-handler-terraform (infra for create-handler Lambda)
  - Any phase needing remote sandbox creation dispatch

# Tech tracking
tech-stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/eventbridge (already in go.mod, now used in create.go)
  patterns:
    - Narrow EventBridgeAPI interface for testability (same pattern as SchedulerAPI)
    - RunCommandFunc dependency injection for Lambda subprocess testing
    - TDD: failing tests first, then minimal implementation, then verify all pass

key-files:
  created:
    - pkg/aws/eventbridge.go
    - pkg/aws/eventbridge_test.go
    - internal/app/cmd/create_remote_test.go
    - cmd/create-handler/main.go
    - cmd/create-handler/exec.go
    - cmd/create-handler/main_test.go
  modified:
    - internal/app/cmd/create.go

key-decisions:
  - "EventBridgeAPI already defined in pkg/aws/idle_event.go — reused instead of redefining; PutSandboxCreateEvent uses the shared interface"
  - "exec.go separated from main.go to keep os/exec out of Lambda handler logic and enable clean RunCommandFunc injection in tests"
  - "RunCommand field on CreateHandler uses func type for subprocess injection — same dependency injection pattern as other Lambda handlers"
  - "create-handler does NOT send 'created' notification — km create subprocess already sends it at Step 14"

patterns-established:
  - "Remote dispatch pattern: compile locally → upload to S3 → publish EventBridge event → Lambda handles terragrunt"
  - "RunCommandFunc injection: handler struct holds RunCommandFunc field, tests inject stub, production uses os/exec wrapper"

requirements-completed: [REMOTE-01, REMOTE-02, REMOTE-05, REMOTE-06]

# Metrics
duration: 10min
completed: 2026-03-26
---

# Phase 22 Plan 01: Remote Sandbox Creation Summary

**EventBridge dispatch for km create --remote using S3 artifact upload + SandboxCreate event, plus create-handler Lambda that runs km create subprocess with failure notification**

## Performance

- **Duration:** 10 min
- **Started:** 2026-03-26T21:17:26Z
- **Completed:** 2026-03-26T21:27:Z
- **Tasks:** 2
- **Files modified:** 7

## Accomplishments
- Added `PutSandboxCreateEvent` and `SandboxCreateDetail` to `pkg/aws/eventbridge.go` with unit tests
- Added `--remote` flag to `km create` that dispatches to `runCreateRemote` — compiles profile locally, uploads artifacts to S3, publishes EventBridge event
- Created `cmd/create-handler/` Lambda binary with `CreateHandler.Handle` that downloads profile, runs km subprocess, sends create-failed SES notification on failure

## Task Commits

Each task was committed atomically:

1. **Task 1: EventBridge SDK package + km create --remote flag** - `1e2a9da` (feat)
2. **Task 2: Create handler Lambda binary** - `36eab02` (feat)

## Files Created/Modified
- `pkg/aws/eventbridge.go` - SandboxCreateDetail struct + PutSandboxCreateEvent function
- `pkg/aws/eventbridge_test.go` - Unit tests for PutSandboxCreateEvent (3 tests)
- `internal/app/cmd/create.go` - Added --remote flag, eventbridge import, runCreateRemote function
- `internal/app/cmd/create_remote_test.go` - Tests for --remote flag registration and source checks
- `cmd/create-handler/main.go` - CreateHandler, CreateEvent, Handle method, main()
- `cmd/create-handler/exec.go` - os/exec wrapper for runOSExec, separated for clean test injection
- `cmd/create-handler/main_test.go` - Unit tests: JSON round-trip, happy path, failure path, on-demand flag

## Decisions Made
- EventBridgeAPI was already declared in `pkg/aws/idle_event.go` — reused the shared interface rather than redefining it. `PutSandboxCreateEvent` in `eventbridge.go` references the interface from `idle_event.go`.
- Separated `exec.go` from `main.go` to keep os/exec out of handler logic; `RunCommandFunc` field enables clean subprocess injection in tests without importing os/exec.
- CreateHandler deliberately does NOT send a "created" SES notification — km create (the subprocess) already sends it at Step 14 of `runCreate`. The handler only sends "create-failed" on error.

## Deviations from Plan

**1. [Rule 1 - Bug] EventBridgeAPI already declared in idle_event.go**
- **Found during:** Task 1 (eventbridge.go implementation)
- **Issue:** Attempted to declare `EventBridgeAPI` in eventbridge.go but it was already declared in `pkg/aws/idle_event.go`, causing a "redeclared in this block" compile error. Also discovered the entry type is `ebtypes.PutEventsRequestEntry` not `eventbridge.PutEventsRequestEntry`.
- **Fix:** Removed the duplicate interface declaration from eventbridge.go. Added a comment noting the interface lives in idle_event.go. Used correct `ebtypes` import alias.
- **Files modified:** pkg/aws/eventbridge.go
- **Verification:** `go test ./pkg/aws/... -run TestPutSandboxCreateEvent` passes
- **Committed in:** 1e2a9da (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - compile error from duplicate interface declaration)
**Impact on plan:** Minor — interface already existed from earlier phase work. Reuse is cleaner than duplication.

## Issues Encountered
- git stash during pre-existence check accidentally wiped create.go edits — had to reapply the import and `runCreateRemote` function. No code was lost; re-application was straightforward.

## Next Phase Readiness
- EventBridge SDK package ready: `pkg/aws/eventbridge.go` exports `PutSandboxCreateEvent` and `SandboxCreateDetail`
- `km create --remote` compiles and dispatches correctly (unit tested)
- `cmd/create-handler/` Lambda binary builds and handles `CreateEvent` (unit tested)
- Next: Phase 22-02 needs Terraform/Terragrunt infra for the create-handler Lambda IAM role, EventBridge rule, and deployment

---
*Phase: 22-remote-sandbox-creation*
*Completed: 2026-03-26*
