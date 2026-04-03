---
phase: 44-km-at-schedule-eventbridge-scheduler-command-for-deferred-and-recurring-sandbox-operations
plan: "02"
subsystem: aws
tags: [dynamodb, eventbridge-scheduler, scheduler, config]

requires:
  - phase: 11-sandbox-auto-destroy-metadata-wiring
    provides: DynamoDB patterns (SandboxMetadataAPI, explicit attribute marshalling)
  - phase: 44-01
    provides: natural language time parser for EventBridge expressions

provides:
  - Extended SchedulerAPI interface with ListSchedules and GetSchedule
  - CreateAtSchedule and DeleteAtSchedule helper functions
  - ScheduleRecord DynamoDB CRUD (PutSchedule, GetScheduleRecord, ListScheduleRecords, DeleteScheduleRecord)
  - Config fields SchedulesTableName and CreateHandlerLambdaARN

affects:
  - 44-03 (km at CLI command — will call these helpers)
  - 44-04 (E2E integration tests)

tech-stack:
  added: []
  patterns:
    - Manual DynamoDB attribute marshalling (AttributeValueMemberS/BOOL) — no MarshalMap
    - Idempotent delete (ResourceNotFoundException swallowed)
    - (nil, nil) not-found sentinel from GetScheduleRecord

key-files:
  created:
    - pkg/aws/schedules_dynamo.go
    - pkg/aws/schedules_dynamo_test.go
  modified:
    - pkg/aws/scheduler.go
    - pkg/aws/scheduler_test.go
    - internal/app/config/config.go

key-decisions:
  - "SchedulerAPI extended (not replaced) — ListSchedules/GetSchedule added alongside existing Create/Delete"
  - "sandbox_id omitted from DynamoDB item when empty (create commands have no target sandbox)"
  - "SchedulesTableName defaults to km-schedules matching Terraform infra naming"
  - "ListScheduleRecords sorts descending by CreatedAt (newest first) for CLI list display"

patterns-established:
  - "Idempotent delete: ResourceNotFoundException swallowed in DeleteAtSchedule (same as DeleteTTLSchedule)"
  - "Not-found sentinel: GetScheduleRecord returns (nil, nil) — callers check for nil pointer"
  - "Manual DynamoDB marshalling: all schedules_dynamo.go uses explicit AttributeValueMemberS/BOOL per project convention"

requirements-completed: [SCHED-STATE, SCHED-INFRA]

duration: 3min
completed: "2026-04-03"
---

# Phase 44 Plan 02: AWS Infrastructure Layer Extension Summary

**Extended SchedulerAPI to 4 methods and added DynamoDB CRUD for km-schedules table with manual attribute marshalling, config fields, and full unit test coverage**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-04-03T06:53:56Z
- **Completed:** 2026-04-03T06:56:42Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments
- Extended SchedulerAPI interface with ListSchedules and GetSchedule (scheduler.Client satisfies all 4 automatically)
- Added CreateAtSchedule (always-non-nil input wrapper) and DeleteAtSchedule (idempotent, swallows ResourceNotFoundException)
- Created schedules_dynamo.go with ScheduleRecord CRUD using explicit DynamoDB attribute marshalling per project convention
- Added SchedulesTableName (default "km-schedules") and CreateHandlerLambdaARN to Config with viper Load() wiring

## Task Commits

Each task was committed atomically:

1. **Task 1: Extend SchedulerAPI and add CreateAtSchedule/DeleteAtSchedule** - `06b108d` (feat)
2. **Task 2: DynamoDB schedule metadata CRUD and config fields** - `5b73060` (feat)

## Files Created/Modified
- `pkg/aws/scheduler.go` - Extended SchedulerAPI interface; added CreateAtSchedule, DeleteAtSchedule
- `pkg/aws/scheduler_test.go` - Added mock stubs for ListSchedules/GetSchedule; tests for new helpers
- `pkg/aws/schedules_dynamo.go` - ScheduleRecord struct + PutSchedule, GetScheduleRecord, ListScheduleRecords, DeleteScheduleRecord
- `pkg/aws/schedules_dynamo_test.go` - Unit tests for all CRUD ops with mockSandboxMetadataAPI
- `internal/app/config/config.go` - Added SchedulesTableName and CreateHandlerLambdaARN fields with defaults and km-config.yaml wiring

## Decisions Made
- Omit sandbox_id from DynamoDB item when empty (create commands) — prevents storing empty string attribute that would waste space and could confuse GSI queries
- ListScheduleRecords sorts descending (newest first) — matches expected UX for `km at list`
- GetScheduleRecord returns (nil, nil) for not-found — consistent with sandbox_dynamo.go pattern; callers check nil before dereferencing

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None — TDD RED/GREEN cycle worked as expected. Both tasks passed on first attempt after implementation.

## User Setup Required

None - no external service configuration required. DynamoDB table must exist (provisioned by Terraform in prior phase).

## Next Phase Readiness
- `km at` CLI command (44-03) can now call CreateAtSchedule/DeleteAtSchedule and PutSchedule/GetScheduleRecord/ListScheduleRecords/DeleteScheduleRecord
- Config fields SchedulesTableName and CreateHandlerLambdaARN ready for CLI wiring
- All existing tests in pkg/aws/ continue to pass

---
*Phase: 44-km-at-schedule-eventbridge-scheduler-command-for-deferred-and-recurring-sandbox-operations*
*Completed: 2026-04-03*
