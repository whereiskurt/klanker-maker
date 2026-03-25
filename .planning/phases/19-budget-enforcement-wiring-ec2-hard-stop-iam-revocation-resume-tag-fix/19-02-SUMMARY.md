---
phase: 19-budget-enforcement-wiring-ec2-hard-stop-iam-revocation-resume-tag-fix
plan: "02"
subsystem: budget
tags: [ec2, tag-filter, budget, resume, aws-sdk]

# Dependency graph
requires:
  - phase: 19-budget-enforcement-wiring-ec2-hard-stop-iam-revocation-resume-tag-fix
    provides: EC2 stop/start wiring and resumeEC2Sandbox function
provides:
  - Corrected tag:km:sandbox-id filter in resumeEC2Sandbox DescribeInstances call
  - Source-level test that locks in the correct tag key going forward
affects:
  - km budget add EC2 resume path
  - BUDG-08 requirement closure

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Source-level verification: os.ReadFile + strings.Contains to assert implementation details (Phase 07-02 pattern)"

key-files:
  created: []
  modified:
    - internal/app/cmd/budget.go
    - internal/app/cmd/budget_test.go

key-decisions:
  - "Used source-level string assertion (not behavioral mock) because DescribeInstances filter value is invisible to fake — the fake ignores filter args entirely"
  - "Checked for exact broken pattern awssdk.String(\"tag:sandbox-id\") rather than naive substring to avoid false negative (km:sandbox-id contains sandbox-id as substring)"

patterns-established:
  - "Source-level tag key verification: when AWS SDK filter keys must match infra tag keys exactly, a source-level test locks the contract without requiring AWS connectivity"

requirements-completed: [BUDG-08]

# Metrics
duration: 1min
completed: 2026-03-25
---

# Phase 19 Plan 02: EC2 Resume Tag Filter Fix Summary

**Single-line fix: corrected DescribeInstances filter key from tag:sandbox-id to tag:km:sandbox-id in resumeEC2Sandbox, enabling km budget add to find and start stopped EC2 sandboxes**

## Performance

- **Duration:** ~1 min
- **Started:** 2026-03-25T02:35:33Z
- **Completed:** 2026-03-25T02:37:07Z
- **Tasks:** 1 (TDD: 2 commits — RED test then GREEN fix)
- **Files modified:** 2

## Accomplishments

- Fixed root cause of BUDG-08 partial status: stopped EC2 sandboxes were silently unfindable on budget add
- Added TestResumeEC2Sandbox_UsesCorrectTagKey to lock in correct tag key contract at the source level
- All 8 budget tests pass after the single-line fix

## Task Commits

TDD task with two commits (RED → GREEN):

1. **RED — failing tag key test** - `0a6ba7c` (test)
2. **GREEN — fix tag filter key** - `59b34ea` (fix)

**Plan metadata:** (docs commit below)

_Note: TDD tasks have two commits (test → feat). No refactor phase was needed._

## Files Created/Modified

- `internal/app/cmd/budget.go` — Changed `"tag:sandbox-id"` to `"tag:km:sandbox-id"` on line 226 in resumeEC2Sandbox
- `internal/app/cmd/budget_test.go` — Added TestResumeEC2Sandbox_UsesCorrectTagKey source-level verification test

## Decisions Made

- Used source-level verification (os.ReadFile + strings.Contains) rather than a behavioral mock extension, because the existing fakeEC2StartAPI ignores DescribeInstances filter args entirely — there was no way to catch the wrong tag key at the behavior level without rewriting the fake. Source-level test is the same pattern already established in TestBudgetAdd_ECSSourceLevelVerification.
- Negative assertion checks for the exact broken Go string literal `awssdk.String("tag:sandbox-id")` — not the bare substring `tag:sandbox-id` — because `tag:km:sandbox-id` contains `tag:sandbox-id` as a substring and a naive check would produce a false negative.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- BUDG-08 requirement is now fully closed: the tag filter matches the tag key applied by infra/modules/ec2spot/v1.0.0/main.tf
- Budget resume path for EC2 sandboxes is now correct end-to-end
- No blockers for remaining Phase 19 plans

---
*Phase: 19-budget-enforcement-wiring-ec2-hard-stop-iam-revocation-resume-tag-fix*
*Completed: 2026-03-25*

## Self-Check: PASSED

- internal/app/cmd/budget.go: FOUND
- internal/app/cmd/budget_test.go: FOUND
- 19-02-SUMMARY.md: FOUND
- Commit 0a6ba7c (RED test): FOUND
- Commit 59b34ea (GREEN fix): FOUND
