---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: 04
subsystem: email
tags: [ses, configure, operator-email, resource-prefix, km-configure]

# Dependency graph
requires:
  - phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
    plan: 01
    provides: "Wave 0 test stubs W0-01/02/03 in configure_test.go (cmd_test package)"

provides:
  - "deriveOperatorEmail(prefix, emailSubdomain, domain) helper in configure.go"
  - "runConfigure integration: auto-derives operator_email from prefix when blank"
  - "preserve-on-rerun: loads existing operator_email from disk when not blank"
  - "--reset-prefix clears operator_email so next run re-derives from new default prefix"
  - "W0-01, W0-02, W0-03 test stubs GREEN"

affects:
  - plan 84-07 (km bootstrap reads pc.OperatorEmail as single source of truth)
  - plan 84-05 (userdata KM_OPERATOR_EMAIL env var traces back to this derivation)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "deriveOperatorEmail helper function with empty-input guard (returns '' on any blank input)"
    - "preserve-on-rerun: read existing config from disk before prompting, skip existingOperatorEmail on --reset-prefix"
    - "non-interactive derivation: auto-fill blank operator_email unless resetPrefix=true"
    - "interactive derivation: derived address used as prompt default when blank"

key-files:
  created:
    - "internal/app/cmd/configure_84_test.go — internal package (cmd) test stubs for W0-01/02/03"
  modified:
    - "internal/app/cmd/configure.go — deriveOperatorEmail helper + runConfigure integration"

key-decisions:
  - "--reset-prefix clears operator_email to empty string (omitted from YAML via omitempty); the SAME run does NOT re-derive — the operator must re-run configure to get the new derived value"
  - "Non-interactive path derives operator_email only when !resetPrefix; interactive path also gates derivation on !resetPrefix so the user sees an empty default on reset"
  - "Preserve-on-rerun: existingOperatorEmail from disk only used when incoming operatorEmail flag is blank AND !resetPrefix — explicit --operator-email flag always wins"

patterns-established:
  - "Empty-input guard pattern: return '' when any deriveOperatorEmail input is blank — callers handle fallback"
  - "Preserve-on-rerun gating: all state loaded from existing config is gated on !resetPrefix consistently"

requirements-completed:
  - SES-CONFIGURE-WIRING
  - SES-PREFIX-ADDRESS

# Metrics
duration: 13min
completed: 2026-05-16
---

# Phase 84 Plan 04: km configure Operator-Email Derivation Summary

**`deriveOperatorEmail` helper added to configure.go; `runConfigure` auto-derives `operator-{prefix}@{subdomain}.{domain}` when `operator_email` is blank; `--reset-prefix` clears it; W0-01/02/03 GREEN**

## Performance

- **Duration:** 13 min
- **Started:** 2026-05-16T20:09:12Z
- **Completed:** 2026-05-16T20:22:22Z
- **Tasks:** 1 (TDD: RED + GREEN + fix)
- **Files modified:** 3

## Accomplishments
- Added `deriveOperatorEmail(resourcePrefix, emailSubdomain, domain string) string` helper in `configure.go`
- Integrated derivation into `runConfigure`: both non-interactive and interactive paths auto-derive when `operator_email` is blank and `!resetPrefix`
- Implemented preserve-on-rerun: existing `operator_email` from disk is loaded and used as default on re-run (when not blank and no explicit `--operator-email` flag)
- `--reset-prefix` clears `operator_email` to empty; same-run derivation is skipped; next `km configure` call re-derives from the new default prefix
- All three Wave 0 configure stubs (W0-01, W0-02, W0-03) now GREEN — both internal (package cmd) and external (package cmd_test via binary) variants

## Task Commits

1. **TDD RED: W0-01/02/03 stubs (internal pkg cmd)** - `f0ec960` (test)
2. **TDD GREEN: deriveOperatorEmail + runConfigure integration** - `01fe519` (feat)
3. **Fix: align internal test reset-prefix assertion with canonical spec** - `7f0fe90` (fix)

## Files Created/Modified
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/configure.go` - deriveOperatorEmail helper + existingOperatorEmail load + non-interactive and interactive derivation paths
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/configure_84_test.go` - internal package stubs for W0-01/02/03 (supplement to cmd_test binary-invocation stubs)

## Decisions Made
- `--reset-prefix` path does NOT re-derive in the same run. The test spec (W0-03 in `configure_test.go`) requires `operator_email` to be absent after reset; re-derivation happens on the next `km configure` invocation.
- Internal test file `configure_84_test.go` created to enable direct unit testing of `deriveOperatorEmail` and `runConfigure` without spawning the binary. Supplements the cmd_test binary-invocation tests from Plan 84-01.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Added checkSESRules stub and SESReceiptRuleAPI interface to doctor.go**
- **Found during:** Task 1 (GREEN implementation)
- **Issue:** W0-06/W0-07 stubs in `doctor_test.go` reference `checkSESRules` which didn't exist; caused the entire `cmd` package to fail to compile, blocking go test for configure tests
- **Fix:** Confirmed `7d424c2` (Plan 84-01 commit) had already added the stub; the error was from a stale build state. No action needed — pre-existing stub from Plan 84-01 was sufficient.
- **Files modified:** None (already in doctor.go from Plan 84-01)
- **Committed in:** 7d424c2 (prior plan)

**2. [Rule 1 - Bug] Fixed internal test W0-03 assertion (configure_84_test.go)**
- **Found during:** Post-GREEN verification
- **Issue:** Internal package test (configure_84_test.go) had contradictory assertion — expected `operator-km@sandboxes.example.com` to be present after `--reset-prefix`, but the canonical spec (configure_test.go W0-03 from Plan 84-01) and the implementation both specify: after reset, operator_email is empty/omitted
- **Fix:** Updated assertion to check that `operator_email:` is absent from YAML after `--reset-prefix`
- **Files modified:** internal/app/cmd/configure_84_test.go
- **Verification:** go test passes for all six test instances (3 internal + 3 external)
- **Committed in:** 7f0fe90

---

**Total deviations:** 2 (1 already-fixed by prior plan, 1 auto-fixed Rule 1 bug)
**Impact on plan:** Both issues identified and resolved without scope creep. No production behavior changed.

## Issues Encountered
- Plans 84-01 through 84-06 were partially or fully executed before this plan ran (non-sequential execution). The Wave 0 stubs were already committed in `7d424c2`. The RED phase commit `f0ec960` (creating `configure_84_test.go`) thus created supplemental internal-package stubs that serve as additional test coverage.
- Internal and external (binary-invocation) tests for W0-03 initially had contradictory expectations. Resolved by aligning to the canonical spec.

## Self-Check

Checking committed files exist:
- configure.go: tracked in `01fe519`
- configure_84_test.go: tracked in `f0ec960`, updated in `7f0fe90`

## Next Phase Readiness
- `deriveOperatorEmail` is available for use by Plan 84-07 (km bootstrap reads pc.OperatorEmail)
- `pc.OperatorEmail` is now a reliable single source of truth derived from prefix
- W0-01, W0-02, W0-03 GREEN — Plan 84-04 verification complete

---
*Phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix*
*Completed: 2026-05-16*

## Phase 84.1 drift

No code drift for Plan 84-04 itself. However, the `--reset-prefix` clearing path (operator_email cleared on reset, then re-derived on next configure) was NOT exercised in Phase 84 UAT (existing value was preserved). Plan 84.1-05 UAT exercises this path explicitly.
