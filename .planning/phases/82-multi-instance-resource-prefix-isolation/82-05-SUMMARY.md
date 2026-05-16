---
phase: 82-multi-instance-resource-prefix-isolation
plan: "05"
subsystem: infra
tags: [aws, tagging, dynamodb, resourcegroupstaggingapi, doctor, multi-instance]

requires:
  - phase: 82-03
    provides: km:resource-prefix tag written at AMI bake time
  - phase: 82-04
    provides: checkOrphanedEC2 filter + checkStaleAMIs prefix param (parallel wave-2 plan)

provides:
  - "`km doctor --backfill-tags` command for retrofitting km:resource-prefix tag onto pre-Phase-82 resources"
  - "BackfillTaggingAPI and BackfillDDBAPI interfaces for testable cross-install safety guard"
  - "runBackfillTags() with paginated GetResources + batched TagResources + DDB cross-reference"
  - "TestBackfillTags_CrossInstallGuard and TestBackfillTags_Idempotent test coverage"

affects:
  - "82-06 through 82-10 (downstream Wave-2 plans consuming same doctor.go)"
  - "Wave-3 manual UAT (Plan 10)"

tech-stack:
  added: []
  patterns:
    - "BackfillTaggingAPI/BackfillDDBAPI narrow interfaces for tagging operations (same DI pattern as doctor EC2InstanceAPI)"
    - "Manual pagination loop over PaginationToken (avoids paginator type in interface, keeps mocks simple)"
    - "Batch-20 TagResources with per-ARN FailedResourcesMap error handling"
    - "DDB GetItem cross-install safety guard: only tag resources whose sandbox_id is in this install's DDB"

key-files:
  created:
    - internal/app/cmd/doctor_backfill_tags.go
    - internal/app/cmd/doctor_backfill_tags_test.go
  modified:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go

key-decisions:
  - "Use manual PaginationToken loop (not SDK paginator) in BackfillTaggingAPI so mocks don't need to implement the paginator constructor interface"
  - "DDB cross-reference is the safety guard: skip any resource whose sandbox_id is absent from this install's DDB (covers foreign installs AND true orphans)"
  - "SkippedForeignPrefix covers resources that already have a km:resource-prefix but from a different install; SkippedUnknownSandbox covers no-prefix resources whose sandbox_id is not in this DDB"
  - "Default --dry-run=true matches km init UX; operators must pass --dry-run=false to commit tags"
  - "Fixed doctor_test.go checkStaleAMIs call signatures (Rule 3): 82-04 parallel plan added currentPrefix param, breaking 9 test call sites"

requirements-completed: []

duration: 10min
completed: "2026-05-16"
---

# Phase 82 Plan 05: km doctor --backfill-tags Cross-Install Safety Guard Summary

**`km doctor --backfill-tags` with mandatory DDB cross-reference guard using resourcegroupstaggingapi GetResources/TagResources, idempotent re-run semantics, and default --dry-run=true**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-05-16T12:59:13Z
- **Completed:** 2026-05-16T13:09:30Z
- **Tasks:** 2 (combined Task 1 + Task 2 in single commit)
- **Files modified:** 4

## Accomplishments

- New `internal/app/cmd/doctor_backfill_tags.go` implementing `runBackfillTags()` with paginated GetResources sweep, DDB cross-install guard, and batched TagResources (up to 20 ARNs per call)
- `BackfillTaggingAPI` (GetResources + TagResources) and `BackfillDDBAPI` (GetItem) interfaces enable full unit-test isolation without real AWS calls
- `--backfill-tags` and `--dry-run` flags registered on the doctor cobra command; `--backfill-tags` early-branches before normal doctor checks
- `TestBackfillTags_CrossInstallGuard` and `TestBackfillTags_Idempotent` both pass GREEN — cover the RESEARCH.md Pitfall 4 requirement and re-run safety

## Task Commits

1. **Task 1+2: Backfill implementation + tests + flag registration** - `a9c7a5d` (feat)

## Files Created/Modified

- `internal/app/cmd/doctor_backfill_tags.go` — BackfillTaggingAPI/BackfillDDBAPI interfaces, BackfillReport struct, runBackfillTags(), applyTags() batch helper
- `internal/app/cmd/doctor_backfill_tags_test.go` — mockBackfillTaggingClient (mutates in-memory state on TagResources calls), mockBackfillDDBClient (knownIDs set), TestBackfillTags_CrossInstallGuard, TestBackfillTags_Idempotent
- `internal/app/cmd/doctor.go` — `--backfill-tags` flag + `--dry-run` early-branch in RunE; import `resourcegroupstaggingapi`
- `internal/app/cmd/doctor_test.go` — Fixed 9 `checkStaleAMIs` call sites (Rule 3 fix for 82-04 parallel plan's new `currentPrefix string` parameter)

## Decisions Made

- Manual PaginationToken loop preferred over SDK's `NewGetResourcesPaginator` so the `BackfillTaggingAPI` interface stays minimal (only `GetResources` + `TagResources`) and mocks don't need a paginator constructor. The trade-off is a slightly longer loop body; simplicity won.
- Two distinct skip categories for resources without `km:resource-prefix`: `SkippedForeignPrefix` (has a different prefix already) vs `SkippedUnknownSandbox` (no prefix, sandbox_id not in this DDB). This distinction helps operators understand why a resource was skipped without requiring them to cross-reference DDB manually.
- `applyTags()` is a separate helper rather than inline logic so it is reusable in future plans (e.g., Plan 06 which will add resource-specific tagging paths).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Fixed checkStaleAMIs call signatures broken by 82-04 parallel plan**
- **Found during:** Task 2 (running tests)
- **Issue:** Plan 82-04 (running in parallel in Wave 2) added a new `currentPrefix string` parameter to `checkStaleAMIs`. The existing `doctor_test.go` had 9 call sites using the old 6-arg signature, causing compile failures that blocked test execution.
- **Fix:** Added `"km"` as the 7th argument to all 9 `checkStaleAMIs` call sites in `doctor_test.go`.
- **Files modified:** `internal/app/cmd/doctor_test.go`
- **Verification:** `go build ./... && go vet ./internal/app/cmd/...` clean; TestBackfillTags tests pass.
- **Committed in:** a9c7a5d (combined task commit)

---

**Total deviations:** 1 auto-fixed (1 Rule 3 - blocking)
**Impact on plan:** Necessary to unblock test execution. The fix is mechanical (add missing argument matching the 82-04 default value `"km"`). No scope creep.

## Issues Encountered

None beyond the 82-04 parallel-plan signature collision handled above.

## Next Phase Readiness

- `km doctor --backfill-tags --dry-run=true` is ready for operator use against the existing `km` install
- Plan 10's Wave 3 manual UAT can verify zero SkippedForeignPrefix and expected Tagged count
- Plans 82-06 through 82-10 can proceed independently (no changes to interfaces they consume)

---
*Phase: 82-multi-instance-resource-prefix-isolation*
*Completed: 2026-05-16*
