---
phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests
plan: 04
subsystem: testing
tags: [go, unit-tests, dynamodb, state-bucket, lock, unlock, list, status]

# Dependency graph
requires:
  - phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests
    provides: Phase 107 context — stale test reconciliation to DynamoDB-primary production behavior
provides:
  - list_test.go empty-bucket assertion reconciled to DynamoDB-primary (no legacy guard fires)
  - status_test.go empty-bucket assertion broadened to accept "sandbox not found" from DynamoDB lookup
  - lock_test.go state-bucket guard assertion replaced with DynamoDB-first assertion
  - unlock_test.go state-bucket guard assertion replaced with DynamoDB-first assertion
affects: [107-reconcile-22-stale-internal-app-cmd-unit-tests]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "State-bucket guard tests: assert the guard does NOT fire on DynamoDB-primary path (negative assertion); guard only fires on S3 fallback after ResourceNotFoundException"
    - "Status error broadening: OR-chain accepted substrings for DynamoDB-first error messages alongside legacy S3 messages"

key-files:
  created: []
  modified:
    - internal/app/cmd/list_test.go
    - internal/app/cmd/status_test.go
    - internal/app/cmd/lock_test.go
    - internal/app/cmd/unlock_test.go

key-decisions:
  - "TestListCmd_EmptyStateBucketError renamed to TestListCmd_EmptyStateBucketNoLongerErrors with inverted negative assertion — DynamoDB-primary means empty bucket no longer triggers the old guard at all"
  - "TestStatusCmd_EmptyStateBucketError broadened (not renamed): keep err==nil fail guard but accept 'sandbox not found' as a valid DynamoDB-first error alongside legacy messages"
  - "TestLock/UnlockCmd_RequiresStateBucket renamed to TestLock/UnlockCmd_EmptyStateBucketUsesDynamo — assertions flip from 'must mention state bucket' to 'must NOT mention state bucket'"
  - "No production code edited: list.go/status.go/lock.go/unlock.go all unchanged"

patterns-established:
  - "When production behavior changes from guard-triggers to guard-bypasses, invert the test assertion rather than removing the test entirely — this documents the intentional design change"

requirements-completed: [TEST-HYGIENE-STATEBUCKET]

# Metrics
duration: 4min
completed: 2026-06-12
---

# Phase 107 Plan 04: State-Bucket Guard Test Reconciliation Summary

**Four stale state-bucket-guard tests reconciled to DynamoDB-primary behavior: negative assertions replace legacy guard expectations across list, status, lock, and unlock command tests**

## Performance

- **Duration:** 4 min
- **Started:** 2026-06-12T01:59:43Z
- **Completed:** 2026-06-12T02:03:43Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments

- Renamed `TestListCmd_EmptyStateBucketError` to `TestListCmd_EmptyStateBucketNoLongerErrors`; assertion now confirms the DynamoDB-primary path does NOT fire the legacy bucket-guard message
- Broadened `TestStatusCmd_EmptyStateBucketError` accepted-error-substring OR-chain to include "sandbox not found" (DynamoDB-first lookup result in unit environments)
- Renamed `TestLockCmd_RequiresStateBucket` to `TestLockCmd_EmptyStateBucketUsesDynamo`; assertion flipped from "error must mention state bucket" to "error must NOT mention state bucket"
- Renamed `TestUnlockCmd_RequiresStateBucket` to `TestUnlockCmd_EmptyStateBucketUsesDynamo`; same symmetric inversion as lock

## Task Commits

1. **Task 1: Reconcile list + status empty-bucket assertions** - `f81906cf` (test)
2. **Task 2: Reconcile lock + unlock state-bucket assertions** - `248a3458` (test, bundled with 107-02 plan)

**Plan metadata:** committed with final doc commit

## Files Created/Modified

- `internal/app/cmd/list_test.go` - Renamed + inverted TestListCmd_EmptyStateBucketError test; added DynamoDB-primary rationale comment
- `internal/app/cmd/status_test.go` - Broadened error OR-chain in TestStatusCmd_EmptyStateBucketError to accept "sandbox not found"
- `internal/app/cmd/lock_test.go` - Renamed + inverted TestLockCmd_RequiresStateBucket to TestLockCmd_EmptyStateBucketUsesDynamo
- `internal/app/cmd/unlock_test.go` - Renamed + inverted TestUnlockCmd_RequiresStateBucket to TestUnlockCmd_EmptyStateBucketUsesDynamo

## Decisions Made

- Inverted assertions rather than deleting tests: keeps documented evidence of the intentional DynamoDB-primary behavioral shift (the guard now lives only on the S3 fallback path after ResourceNotFoundException)
- TestStatusCmd_EmptyStateBucketError kept its name (behavior is still "returns an error") with only the accepted-substring broadened — contrast with list where nil is now a valid return, warranting a full rename
- No production files touched: list.go, status.go, lock.go, unlock.go are all unchanged

## Deviations from Plan

None - plan executed exactly as written. The lock/unlock test changes were found already committed as part of the 107-02 execution (bundled commit `248a3458`); the current file state matched the required edits exactly, and all target tests passed.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required. Test-hygiene only.

## Next Phase Readiness

- All 4 state-bucket-guard tests green against DynamoDB-primary behavior
- Phase 107 state-bucket cluster (Plan 04) complete — no remaining guard-related stale tests
- Full suite baseline: go test ./internal/app/cmd/ -run 'Test(List|Lock|Unlock|Status)Cmd|TestCheckSandboxLock' passes at EXIT=0

---
*Phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests*
*Completed: 2026-06-12*
