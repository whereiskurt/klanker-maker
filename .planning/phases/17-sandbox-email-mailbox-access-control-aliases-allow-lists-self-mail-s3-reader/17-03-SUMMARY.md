---
phase: 17-sandbox-email-mailbox-access-control-aliases-allow-lists-self-mail-s3-reader
plan: "03"
subsystem: cli-commands, aws-identity
tags: [email, alias, allowedSenders, km-status, km-create, identity, tdd]
dependency_graph:
  requires:
    - "17-01: EmailSpec.Alias and AllowedSenders fields in pkg/profile/types.go"
    - "17-02: Extended PublishIdentity with alias/allowedSenders params; Extended IdentityRecord with Alias/AllowedSenders fields; create.go auto-wired as Rule 3 fix"
  provides:
    - Tests confirming create.go wires alias and allowedSenders from EmailSpec to PublishIdentity
    - km status displays Alias line (when non-empty) in Identity section
    - km status displays Allowed Senders as comma-joined list or 'not configured' when empty
    - Full Phase 17 feature complete end-to-end
  affects:
    - "Future plans using km status output parsing (Alias and Allowed Senders now present)"
tech_stack:
  added: []
  patterns:
    - TDD (RED->GREEN) for status display — tests written before implementation
    - Source-level verification pattern for create.go wiring (os.ReadFile + strings.Contains checks)
    - strings.Join for comma-separated allow-list display
    - Conditional line omission pattern (Alias line omitted when empty, consistent with existing TTL omission)
key_files:
  created: []
  modified:
    - internal/app/cmd/create_test.go
    - internal/app/cmd/status.go
    - internal/app/cmd/status_test.go
key-decisions:
  - "[Phase 17-03]: Alias line conditionally omitted (identity.Alias == '') — consistent with TTL Expiry conditional display pattern already established in printSandboxStatus"
  - "[Phase 17-03]: Allowed Senders always shown (either joined list or 'not configured') — more useful than omitting entirely; operator needs to know if allow-list is configured"
  - "[Phase 17-03]: create.go wiring was already done by Plan 02 Rule 3 auto-fix; Task 1 provides source-level verification tests to document and lock that behavior"

patterns-established:
  - "Conditional display in printSandboxStatus: omit line when value is empty string; always show summary line for slice fields"
  - "Source-level verification for non-DI-injectable wiring: os.ReadFile(sourcefile) + strings.Contains pattern"

requirements-completed: [MAIL-STATUS-DISPLAY, MAIL-CREATE-WIRING]

duration: 184s
completed: "2026-03-23"
---

# Phase 17 Plan 03: CLI Wiring — alias/allowedSenders in km create and km status — Summary

**Wired alias and allowedSenders from profile EmailSpec through km create (PublishIdentity call confirmed with source-level tests) and km status (Alias conditional line + Allowed Senders always-shown), completing Phase 17 end-to-end.**

## Performance

- **Duration:** 184s (~3 min)
- **Started:** 2026-03-23T05:23:26Z
- **Completed:** 2026-03-23T05:26:30Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments
- Confirmed create.go correctly threads alias and allowedSenders from EmailSpec to PublishIdentity (source-level tests)
- Extended printSandboxStatus to display Alias line (when non-empty) and Allowed Senders (comma-joined or "not configured")
- Full `go test ./...` passes across all 15 packages with zero failures
- `go vet ./...` passes cleanly
- Phase 17 feature complete: alias and allow-list support works end-to-end

## Task Commits

Each task was committed atomically:

1. **Task 1 (RED): create.go alias wiring tests** - `8b6d736` (feat)
2. **Task 2 (RED): Failing tests for km status alias display** - `6afc9f4` (test)
3. **Task 2 (GREEN): status.go Alias and Allowed Senders display** - `ba37639` (feat)

**Plan metadata:** (to be added)

_Note: TDD tasks have multiple commits (test RED -> feat GREEN). Task 1 was GREEN immediately since create.go was already wired by Plan 02 auto-fix._

## Files Created/Modified
- `internal/app/cmd/create_test.go` - Added TestRunCreate_PublishIdentityAliasWiring and TestRunCreate_PublishIdentityAliasBackwardCompat source-level verification tests
- `internal/app/cmd/status.go` - Added `strings` import; added Alias conditional line and Allowed Senders line after Encryption in Identity section
- `internal/app/cmd/status_test.go` - Added TestStatus_EmailAlias (alias + allowedSenders populated) and TestStatus_EmailAlias_Empty (empty alias, nil allowedSenders)

## Decisions Made
- Alias line is conditionally omitted when empty (consistent with existing TTL Expiry conditional pattern in printSandboxStatus)
- Allowed Senders always shows when Identity section is shown — either the joined list or "not configured"; this is more informative than omitting the line
- Task 1 source-level tests use the existing os.ReadFile + strings.Contains pattern (same as TestRunCreate_GitHubToken, TestRunCreate_MLflow, etc.) — consistent with codebase conventions

## Deviations from Plan

None - plan executed exactly as written. The create.go wiring was already completed by Plan 02's Rule 3 auto-fix; Task 1 confirmed that by adding source-level verification tests. The plan anticipated this, stating "Update any existing tests in create_test.go that mock or assert on PublishIdentity to account for the new parameters."

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Phase 17 is fully complete: schema (Plan 01), business logic (Plan 02), and CLI wiring (Plan 03) are all implemented and tested.
- All Phase 17 requirements satisfied: MAIL-STATUS-DISPLAY and MAIL-CREATE-WIRING.
- No blockers for subsequent work.

---
*Phase: 17-sandbox-email-mailbox-access-control-aliases-allow-lists-self-mail-s3-reader*
*Completed: 2026-03-23*

## Self-Check: PASSED

Files verified:
- internal/app/cmd/create_test.go — FOUND (TestRunCreate_PublishIdentityAliasWiring, TestRunCreate_PublishIdentityAliasBackwardCompat added)
- internal/app/cmd/status.go — FOUND (Alias and Allowed Senders display added to printSandboxStatus)
- internal/app/cmd/status_test.go — FOUND (TestStatus_EmailAlias, TestStatus_EmailAlias_Empty added)
- .planning/phases/17-.../17-03-SUMMARY.md — FOUND

Commits verified:
- 8b6d736 — feat(17-03): wire alias and allowedSenders tests for create.go PublishIdentity
- 6afc9f4 — test(17-03): failing tests for alias and allowedSenders display in km status
- ba37639 — feat(17-03): extend km status to display Alias and Allowed Senders
