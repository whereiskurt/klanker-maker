---
phase: 44-km-at-schedule-eventbridge-scheduler-command-for-deferred-and-recurring-sandbox-operations
plan: "03"
subsystem: cli
tags: [cobra, eventbridge-scheduler, dynamodb, km-at, scheduling, guardrail]

# Dependency graph
requires:
  - phase: 44-01
    provides: "at.Parse, ValidateCron, GenerateScheduleName, ScheduleSpec struct"
  - phase: 44-02
    provides: "CreateAtSchedule, DeleteAtSchedule, ScheduleRecord, PutSchedule, ListScheduleRecords, DeleteScheduleRecord, config fields"

provides:
  - "km at '<time-expr>' <command> [args...] CLI command"
  - "km at list subcommand with tabwriter table output"
  - "km at cancel <name> subcommand (idempotent)"
  - "km schedule alias (Cobra Aliases field) with identical behavior"
  - "SCHED-GUARDRAIL: CLI-side recurring create warning when count >= max_sandboxes"

affects:
  - "44-04-E2E integration tests (km at end-to-end flows)"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Dependency injection via WithDeps constructors for testability (schedClient, dynamo, countActiveSandboxes)"
    - "Lazy AWS client init in RunE — nil check + LoadAWSConfig when deps not injected"
    - "Cobra alias via Aliases []string field — inherits all flags and subcommands"
    - "ErrOrStderr() for user-facing warnings so Cobra captures them in tests"

key-files:
  created:
    - "internal/app/cmd/at.go"
    - "internal/app/cmd/at_test.go"
  modified:
    - "internal/app/cmd/root.go"

key-decisions:
  - "Use Cobra Aliases field for km schedule alias (not a separate command) — inherits all flags and subcommands automatically"
  - "SCHED-GUARDRAIL writes to cmd.ErrOrStderr() not os.Stderr — allows Cobra to capture warning in tests"
  - "RetryPolicy removed from CreateScheduleInput — field doesn't exist in this SDK version"
  - "countActiveSandboxes injected as closure for testability, real path uses ListAllSandboxesByS3"

patterns-established:
  - "WithDeps constructor pattern: NewAtCmdWithDeps(cfg, sched, dynamo, counter) for test injection"
  - "Lazy init pattern: nil check in RunE, init from LoadAWSConfig when not injected"

requirements-completed:
  - SCHED-CMD
  - SCHED-LIST
  - SCHED-CANCEL
  - SCHED-GUARDRAIL

# Metrics
duration: 5min
completed: 2026-04-03
---

# Phase 44 Plan 03: km at Command Summary

**`km at` Cobra command wiring EventBridge Scheduler + DynamoDB with NL time parsing, list/cancel subcommands, km schedule alias, and CLI-side recurring-create guardrail**

## Performance

- **Duration:** 5 min (318s)
- **Started:** 2026-04-03T06:59:09Z
- **Completed:** 2026-04-03T07:04:27Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

- Implemented `km at '<time-expr>' <command> [args...]` wiring at.Parse → EventBridge Scheduler → DynamoDB with TDD approach (11 tests)
- Implemented `km at list` showing scheduled operations from DynamoDB in tabwriter table
- Implemented `km at cancel <name>` deleting from EventBridge and DynamoDB (idempotent)
- Registered `km schedule` as Cobra alias inheriting all flags and subcommands
- SCHED-GUARDRAIL: recurring create schedules blocked CLI-side with warning when count >= max_sandboxes

## Task Commits

Each task was committed atomically:

1. **Task 1: km at command with list, cancel, guardrail** - `a464f1d` (feat)
2. **Task 2: Register in root.go with km schedule alias** - `bd7c9ab` (feat)

**Plan metadata:** (see final commit)

_Note: Task 1 used TDD — tests written first (RED), then implementation (GREEN)_

## Files Created/Modified

- `internal/app/cmd/at.go` — km at command, km at list, km at cancel with dependency injection
- `internal/app/cmd/at_test.go` — 11 unit tests with mock scheduler, DynamoDB, and sandbox counter
- `internal/app/cmd/root.go` — km at registration with km schedule alias via Cobra Aliases field

## Decisions Made

- **Cobra Aliases for km schedule:** Used `atCmd.Aliases = []string{"schedule"}` rather than a separate command with copied RunE. The alias approach inherits all flags (`--cron`, `--name`, `--group`) and subcommands automatically — no duplication.
- **cmd.ErrOrStderr() for guardrail warning:** Using `os.Stderr` directly bypasses Cobra's output capture, causing test assertions to fail. Switching to `cmd.ErrOrStderr()` allows test output capture.
- **RetryPolicy field removed:** `scheduler.CreateScheduleInput` in this AWS SDK version doesn't have a `RetryPolicy` field — removed from the plan's action spec (Rule 1 auto-fix).
- **countActiveSandboxes as closure:** Injected as `func(ctx context.Context) (int, error)` for testability; real implementation calls `ListAllSandboxesByS3` and filters non-destroyed records.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Removed non-existent RetryPolicy field from CreateScheduleInput**
- **Found during:** Task 1 (at.go implementation)
- **Issue:** Plan specified `RetryPolicy: &schedulertypes.RetryPolicy{MaximumRetryAttempts: aws.Int32(0)}` but this field does not exist in the aws-sdk-go-v2 scheduler.CreateScheduleInput struct
- **Fix:** Removed the RetryPolicy field; EventBridge Scheduler defaults to no retry for km-at schedules
- **Files modified:** `internal/app/cmd/at.go`
- **Verification:** `go build ./internal/app/cmd/` succeeds

**2. [Rule 1 - Bug] Fixed guardrail warning to use cmd.ErrOrStderr() instead of os.Stderr**
- **Found during:** Task 1 (TestAtCmd_RecurringCreateAtLimit test failure)
- **Issue:** Warning written to `os.Stderr` bypassed Cobra's stderr capture, causing test assertion to fail
- **Fix:** Changed to `cmd.ErrOrStderr()` so warning is captured by Cobra in tests
- **Files modified:** `internal/app/cmd/at.go`
- **Verification:** `TestAtCmd_RecurringCreateAtLimit` passes

---

**Total deviations:** 2 auto-fixed (2 Rule 1 - bug)
**Impact on plan:** Both fixes required for correctness. No scope creep.

## Issues Encountered

None — both issues were caught during compilation/test execution and auto-fixed.

## Self-Check

Files exist:
- `internal/app/cmd/at.go` ✓
- `internal/app/cmd/at_test.go` ✓
- `internal/app/cmd/root.go` ✓

Commits exist:
- `a464f1d` ✓ (feat(44-03): km at command)
- `bd7c9ab` ✓ (feat(44-03): root.go registration)

## Next Phase Readiness

- km at command is ready for E2E testing (Phase 44-04)
- All SCHED requirements (CMD, LIST, CANCEL, GUARDRAIL) implemented
- Lambda-side enforcement tested separately (Plans 01/02 tests cover the AWS layer)

---
*Phase: 44-km-at-schedule-eventbridge-scheduler-command-for-deferred-and-recurring-sandbox-operations*
*Completed: 2026-04-03*
