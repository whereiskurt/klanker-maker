---
phase: 44-km-at-schedule-eventbridge-scheduler-command-for-deferred-and-recurring-sandbox-operations
verified: 2026-04-03T07:15:13Z
status: human_needed
score: 14/14 must-haves verified
re_verification: false
human_verification:
  - test: "Run 'km at --help' and 'km schedule --help' in a built binary"
    expected: "Both show usage with time-expr + command syntax and list/cancel subcommands"
    why_human: "Cobra alias behavior (Aliases field) cannot be fully verified via grep — need to confirm km schedule --help shows schedule-specific help and subcommands propagate"
  - test: "Run 'km at list' against a real or mocked AWS environment"
    expected: "Table renders correctly with NAME | COMMAND | TARGET | SCHEDULE | STATUS | CREATED columns; empty list prints 'No scheduled operations.'"
    why_human: "Tabwriter column alignment is a visual check"
  - test: "Run KM_E2E=1 go test -tags e2e ./internal/app/cmd/ -run TestAtE2E -timeout 15m -v"
    expected: "Full scheduling lifecycle completes: create fires, pause/resume/extend dispatch, cancel works, kill destroys sandbox"
    why_human: "Requires real AWS infrastructure with EventBridge Scheduler, Lambda handlers, and DynamoDB provisioned"
---

# Phase 44: km at/schedule Verification Report

**Phase Goal:** Operators can schedule any remote-capable sandbox command (create, destroy, stop, pause, resume, extend) for deferred or recurring execution via EventBridge Scheduler, using natural language time expressions or raw cron. Includes schedule listing and cancellation.
**Verified:** 2026-04-03T07:15:13Z
**Status:** human_needed (all automated checks passed; 3 items need human/live AWS verification)
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | One-time natural language expressions like '10pm tomorrow' resolve to a valid at() EventBridge expression | VERIFIED | `pkg/at/parser.go` uses `olebedev/when` for one-time parsing; `Parse()` emits `at(YYYY-MM-DDThh:mm:ss)`; all parser tests pass |
| 2 | Recurring expressions like 'every thursday at 3pm' resolve to a valid cron() EventBridge expression | VERIFIED | Custom recurring parser in `parser.go` maps day names to EventBridge DOW (thu=5); `TestAtCmd_RecurringKill` passes |
| 3 | Raw --cron input is validated and passed through unchanged | VERIFIED | `ValidateCron()` in parser.go; `--cron` flag in at.go calls `atpkg.ValidateCron`; `TestAtCmd_CronFlag` passes |
| 4 | Schedule names are sanitized to EventBridge constraints (64-char max, [0-9a-zA-Z-_.] only) | VERIFIED | `SanitizeScheduleName()` and `GenerateScheduleName()` exported from parser.go (289 lines); called in at.go line 193 |
| 5 | Day-of-week mapping uses EventBridge convention (1=SUN, 5=THU) not unix convention | VERIFIED | `ebDOW` map in parser.go explicitly documents and implements 1=SUN through 7=SAT |
| 6 | SchedulerAPI interface includes ListSchedules and GetSchedule methods alongside existing CreateSchedule/DeleteSchedule | VERIFIED | scheduler.go lines 14-25: 4-method interface; mock in scheduler_test.go updated with ListSchedules and GetSchedule stubs |
| 7 | DynamoDB CRUD for km-schedules table supports put, get, list (scan), and delete operations | VERIFIED | schedules_dynamo.go exports PutSchedule, GetScheduleRecord, ListScheduleRecords, DeleteScheduleRecord; all 6 DynamoDB tests pass |
| 8 | Config struct has SchedulesTableName and CreateHandlerLambdaARN fields | VERIFIED | config.go lines 121-128: both fields present with comments; wired to viper in Load() at lines 260-261 |
| 9 | Operator can run 'km at' to schedule one-time and recurring commands | VERIFIED | at.go (455 lines) implements full RunE; `TestAtCmd_OneTimeCreate` and `TestAtCmd_RecurringKill` pass |
| 10 | Operator can run 'km at list' to see all scheduled operations from DynamoDB | VERIFIED | `NewAtListCmd`/`NewAtListCmdWithDeps` in at.go; calls `awspkg.ListScheduleRecords`; `TestAtList_Empty` and `TestAtList_WithRecords` pass |
| 11 | Operator can run 'km at cancel <name>' to remove a schedule from both EventBridge and DynamoDB | VERIFIED | `NewAtCancelCmd`/`NewAtCancelCmdWithDeps` in at.go lines 349-400; calls `DeleteAtSchedule` + `DeleteScheduleRecord`; `TestAtCancel` passes |
| 12 | 'km schedule' works as an alias for 'km at' | VERIFIED | root.go line 85: `atCmd.Aliases = []string{"schedule"}`; subcommands added before alias registration |
| 13 | Recurring create schedules warn at CLI time if sandbox count is at or above max_sandboxes limit | VERIFIED | at.go lines 176-187: guardrail block; `TestAtCmd_RecurringCreateAtLimit` (blocks), `TestAtCmd_RecurringCreateBelowLimit` (proceeds), `TestAtCmd_RecurringCreateUnlimited` (MaxSandboxes=0 skips) all pass |
| 14 | E2E integration test exists with build tag e2e and KM_E2E gate | VERIFIED | at_e2e_test.go: `//go:build e2e` line 1; `KM_E2E` env var check lines 116-117; go vet -tags e2e passes |

**Score:** 14/14 truths verified

### Required Artifacts

| Artifact | Min Lines | Actual Lines | Status | Details |
|----------|-----------|-------------|--------|---------|
| `pkg/at/parser.go` | 80 | 289 | VERIFIED | Exports Parse, ValidateCron, SanitizeScheduleName, GenerateScheduleName, ScheduleSpec |
| `pkg/at/parser_test.go` | 100 | 347 | VERIFIED | Comprehensive TDD tests; `go test ./pkg/at/... -count=1` passes |
| `pkg/aws/scheduler.go` | 60 | 90 | VERIFIED | 4-method SchedulerAPI interface; CreateAtSchedule, DeleteAtSchedule helpers |
| `pkg/aws/schedules_dynamo.go` | 100 | 207 | VERIFIED | ScheduleRecord CRUD using explicit DynamoDB attribute marshalling per project convention |
| `pkg/aws/schedules_dynamo_test.go` | 80 | 215 | VERIFIED | 6 unit tests, all pass |
| `internal/app/config/config.go` | — | — | VERIFIED | SchedulesTableName and CreateHandlerLambdaARN fields added and wired to viper |
| `internal/app/cmd/at.go` | 220 | 455 | VERIFIED | Full km at command, list/cancel subcommands, guardrail, dependency injection |
| `internal/app/cmd/at_test.go` | 180 | 409 | VERIFIED | 11 unit tests covering all specified scenarios, all pass |
| `internal/app/cmd/at_e2e_test.go` | 250 | 319 | VERIFIED | E2E test with build tag, KM_E2E gate, 12-step lifecycle, safety cleanup |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/at/parser.go` | `github.com/olebedev/when` | import for one-time date parsing | WIRED | Lines 10-11: import + used in Parse() at line 168 |
| `pkg/aws/schedules_dynamo.go` | `pkg/aws/sandbox_dynamo.go` | SandboxMetadataAPI interface for DynamoDB | WIRED | All 4 CRUD functions use SandboxMetadataAPI parameter |
| `pkg/aws/scheduler.go` | `aws-sdk-go-v2/service/scheduler` | SchedulerAPI interface wrapping SDK client | WIRED | Lines 10-11: import; comment at line 15 confirms `*scheduler.Client` satisfies interface |
| `internal/app/cmd/at.go` | `pkg/at/parser.go` | at.Parse and GenerateScheduleName calls | WIRED | Import alias `atpkg` at line 23; `atpkg.Parse` at line 162, `atpkg.GenerateScheduleName` at line 193, `atpkg.ValidateCron` at line 153 |
| `internal/app/cmd/at.go` | `pkg/aws/scheduler.go` | CreateAtSchedule and DeleteAtSchedule | WIRED | `awspkg.CreateAtSchedule` at line 227; `awspkg.DeleteAtSchedule` at line 388 |
| `internal/app/cmd/at.go` | `pkg/aws/schedules_dynamo.go` | PutSchedule, ListScheduleRecords, DeleteScheduleRecord | WIRED | Lines 242, 323, 393 |
| `internal/app/cmd/at.go` | `pkg/aws/s3.go` | ListAllSandboxesByS3 for guardrail | WIRED | Line 427 in countActiveSandboxes closure |
| `internal/app/cmd/root.go` | `internal/app/cmd/at.go` | NewAtCmd registration | WIRED | Lines 84-88: NewAtCmd called, subcommands added, alias set, registered |
| `internal/app/cmd/at_e2e_test.go` | `internal/app/cmd/at.go` | e2e tag, invokes km binary via exec.Command | WIRED | Line 32: exec.CommandContext with km binary; line 1: `//go:build e2e` |

### Requirements Coverage

| Requirement | Source Plans | Description | Status | Evidence |
|-------------|-------------|-------------|--------|---------|
| SCHED-PARSE | 44-01 | Natural language time parsing to EventBridge expressions | SATISFIED | pkg/at/parser.go with Parse, ValidateCron, SanitizeScheduleName, GenerateScheduleName |
| SCHED-STATE | 44-02 | DynamoDB schedule state tracking (km-schedules table CRUD) | SATISFIED | pkg/aws/schedules_dynamo.go with ScheduleRecord CRUD |
| SCHED-INFRA | 44-02 | Extended SchedulerAPI interface and config fields | SATISFIED | scheduler.go 4-method interface; config.go SchedulesTableName + CreateHandlerLambdaARN |
| SCHED-CMD | 44-03, 44-04 | km at command for scheduling sandbox operations | SATISFIED | at.go full implementation; all 11 unit tests pass |
| SCHED-LIST | 44-03, 44-04 | km at list subcommand showing DynamoDB records | SATISFIED | NewAtListCmd in at.go; TestAtList_Empty + TestAtList_WithRecords pass |
| SCHED-CANCEL | 44-03, 44-04 | km at cancel removing schedule from EventBridge and DynamoDB | SATISFIED | NewAtCancelCmd in at.go; TestAtCancel passes |
| SCHED-GUARDRAIL | 44-03, 44-04 | CLI-side max_sandboxes warning for recurring creates | SATISFIED | at.go lines 176-187; three guardrail test cases all pass |

**Note on requirement IDs:** SCHED-PARSE, SCHED-STATE, SCHED-INFRA, SCHED-CMD, SCHED-LIST, SCHED-CANCEL, SCHED-GUARDRAIL are referenced in ROADMAP.md Phase 44 and in PLAN frontmatter, but are not formally catalogued in REQUIREMENTS.md. They appear to be phase-local requirements introduced with Phase 44. This is a documentation gap (no entries in REQUIREMENTS.md coverage table) but does not indicate missing implementation — all 7 IDs map to verified, implemented, and tested code.

### Anti-Patterns Found

No blocker or warning-level anti-patterns detected.

| File | Pattern | Severity | Assessment |
|------|---------|----------|-----------|
| `internal/app/cmd/at.go` | Multiple `return nil` occurrences | Info | All are intentional: guardrail early return (line 186), successful command completion (lines 249, 330, 398) |
| `pkg/aws/scheduler.go` | `ListSchedules` and `GetSchedule` stubs in test mock | Info | Intentional no-op stubs in scheduler_test.go mock — interface compliance, not a production stub |

### Human Verification Required

#### 1. km schedule alias subcommand propagation

**Test:** Build the binary (`make build`) then run `./build/km schedule --help` and `./build/km schedule list --help`
**Expected:** `km schedule --help` shows the at command help. `km schedule list` and `km schedule cancel` subcommands are accessible (Cobra Aliases applies to the parent command's name but subcommands may not propagate automatically via Aliases — this depends on Cobra version behavior)
**Why human:** The alias is implemented via `atCmd.Aliases = []string{"schedule"}` rather than registering a separate command. Cobra's `Aliases` field only aliases the command name — `km schedule list` routing depends on Cobra treating aliases as full command trees. This needs live binary verification.

#### 2. km at list table formatting

**Test:** Create a few schedules and run `km at list`
**Expected:** Aligned tabwriter table with NAME | COMMAND | TARGET | SCHEDULE | STATUS | CREATED columns; no truncation issues on long schedule names
**Why human:** Column alignment and tabwriter output is a visual check that cannot be validated by grep

#### 3. Full E2E scheduling lifecycle against real AWS

**Test:** `KM_E2E=1 go test -tags e2e ./internal/app/cmd/ -run TestAtE2E -timeout 15m -v`
**Expected:** All 12 steps complete: schedule fires and creates sandbox, pause/resume/extend dispatch, cancel removes from list, --cron recurring schedule creates and cancels, kill destroys sandbox
**Why human:** Requires provisioned AWS infrastructure with EventBridge Scheduler group "km-at", Lambda handlers (create-handler and TTL-handler), DynamoDB km-schedules table, and IAM roles

### Build and Test Summary

```
go build ./...                                          PASS (clean)
go vet ./pkg/at/... ./pkg/aws/ ./internal/app/cmd/     PASS (no issues)
go vet -tags e2e ./internal/app/cmd/                   PASS (no issues)
go test ./pkg/at/... -count=1                          PASS (ok)
go test ./pkg/aws/ -run "TestPutSchedule|TestGetSchedule|TestListSchedule|TestDeleteSchedule" -count=1    PASS (6 tests)
go test ./internal/app/cmd/ -run TestAt -v -count=1    PASS (11 tests)
```

All unit tests pass. All artifacts are substantive (well above minimum line counts). All key links are verified wired.

---

_Verified: 2026-04-03T07:15:13Z_
_Verifier: Claude (gsd-verifier)_
