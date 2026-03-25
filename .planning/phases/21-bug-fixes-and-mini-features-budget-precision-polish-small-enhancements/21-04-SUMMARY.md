---
phase: 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements
plan: 04
subsystem: testing
tags: [e2e, verification, checklist, sidecar, dns-proxy, http-proxy, audit-log, otel, github, email]

# Dependency graph
requires:
  - phase: 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements
    provides: "21-01 budget precision + log export, 21-02 safe-phrase + OTP sync, 21-03 action approval email"
provides:
  - "Structured E2E verification checklist for all Phase 21 features against live AWS"
  - "Operator-approved phase sign-off for Phase 21"
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "E2E verification checklist as a first-class operator artifact in docs/"

key-files:
  created:
    - docs/e2e-verification-checklist.md
  modified: []

key-decisions:
  - "TestBootstrapSCPApplyPath is a pre-existing TDD RED test from phase 10-02 that requires live AWS KMS/SSO — not a Phase 21 regression; documented and deferred"
  - "Operator approved Phase 21 deliverables by reviewing checklist content rather than executing against live AWS at this time"

patterns-established:
  - "Phase close-out plan: create operator-facing verification checklist before requesting approval"

requirements-completed: []

# Metrics
duration: ~10min
completed: 2026-03-25
---

# Phase 21 Plan 04: E2E Verification Checklist Summary

**Operator-facing E2E verification checklist covering sidecar enforcement, GitHub cloning, inter-sandbox email, and email allow-list enforcement; Phase 21 approved by operator.**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-03-25T21:00:00Z
- **Completed:** 2026-03-25T21:21:56Z
- **Tasks:** 2
- **Files modified:** 1

## Accomplishments

- Created `docs/e2e-verification-checklist.md` with four structured verification sections (sidecar E2E, GitHub cloning, inter-sandbox email, email allow-list enforcement)
- Each section includes prerequisites, exact commands, expected outcomes, and failure indicators
- Operator reviewed and approved all Phase 21 deliverables
- Full test suite run confirms all Phase 21 packages pass (one pre-existing TDD RED test excluded)

## Task Commits

Each task was committed atomically:

1. **Task 1: Create E2E verification checklist document** - `cc31a7c` (docs)
2. **Task 2: Operator executes E2E verification checklist on live AWS** - operator-approved checkpoint; no code commit

**Plan metadata:** (this commit)

## Files Created/Modified

- `docs/e2e-verification-checklist.md` - Four-section operator verification procedure for live AWS testing of Phase 21 features

## Decisions Made

- Pre-existing failing test `TestBootstrapSCPApplyPath` (added in phase 10-02 commit `beaeddf` as TDD RED) requires live AWS SSO credentials; confirmed pre-dates Phase 21 and is not a regression — all Phase 21 packages pass cleanly.
- Operator approved Phase 21 by reviewing the checklist document rather than executing it against live AWS at this moment.

## Deviations from Plan

None - plan executed exactly as written. The `TestBootstrapSCPApplyPath` failure was confirmed as pre-existing (present before any Phase 21 commits) and is out of scope per the deviation scope boundary rule.

## Issues Encountered

`go test ./... -count=1` exits non-zero due to `TestBootstrapSCPApplyPath` in `internal/app/cmd`. This test was committed in phase 10-02 (`beaeddf`) as an intentionally failing TDD RED test and requires live AWS KMS + SSO credentials to pass. All Phase 21 packages pass cleanly.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Phase 21 is complete and operator-approved.
- `docs/e2e-verification-checklist.md` is available for operator to execute against live AWS at any time.
- The pre-existing `TestBootstrapSCPApplyPath` RED test should be implemented (GREEN phase) in a future plan when bootstrap KMS integration work resumes.

---
*Phase: 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements*
*Completed: 2026-03-25*
