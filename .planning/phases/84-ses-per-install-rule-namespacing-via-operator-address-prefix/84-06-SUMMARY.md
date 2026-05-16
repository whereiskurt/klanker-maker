---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: "06"
subsystem: email
tags: [ses, lambda, email-handler, recipient-verification, resource-prefix, multi-install]

# Dependency graph
requires:
  - phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
    provides: resourcePrefix() helper already present in email-create-handler/main.go (from prior plans)

provides:
  - "Recipient verification gate in email-create-handler Handle() — silently drops foreign-prefix emails before allowlist/safe-phrase analysis"
  - "Prefix-aware outbound reply From address: operator-${prefix}@${domain} instead of bare operator@${domain}"
  - "W0-04 and W0-05 unit tests GREEN"

affects:
  - 84-07-km-bootstrap-shared-ses-and-doctor-check
  - 84-09-docs-claude-md-operator-guide-and-roadmap
  - 84-10-operator-uat-checkpoint

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "msg.Header.AddressList(\"To\") for MIME To: extraction in Lambda handlers"
    - "Case-insensitive address comparison via strings.ToLower before equality check"
    - "captureStderr() test helper using os.Pipe for stderr capture in unit tests"
    - "buildPlainEmailWithTo() test helper for email builder when To: needs explicit control"

key-files:
  created: []
  modified:
    - cmd/email-create-handler/main.go
    - cmd/email-create-handler/main_test.go

key-decisions:
  - "Verification gate inserted BEFORE allowlist and safe-phrase checks for cheap dispatch — foreign emails dropped without S3/SSM round-trips"
  - "Test handler Domain changed to sandboxes.example.com (full KM_EMAIL_DOMAIN value) so resourcePrefix()+Domain produces a valid matchable address"
  - "Existing test builders updated to use operator-km@sandboxes.example.com matching the default prefix (km) and new domain field"

patterns-established:
  - "Lambda recipient gate: extract To: with AddressList, compare ToLower against operator-${resourcePrefix()}@${h.Domain}"

requirements-completed:
  - SES-HANDLER-LOOKUP
  - SES-PREFIX-ADDRESS

# Metrics
duration: 3min
completed: "2026-05-16"
---

# Phase 84 Plan 06: Email Handler Recipient Verification Summary

**Recipient verification gate in email-create-handler drops foreign-prefix operator emails (operator-rg@domain) before allowlist or safe-phrase analysis, and fixes outbound reply From to use operator-${prefix}@${domain}**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-05-16T~T
- **Completed:** 2026-05-16
- **Tasks:** 1 (TDD: RED + GREEN commits)
- **Files modified:** 2

## Accomplishments

- Added Phase 84 recipient verification gate in `Handle()` at line ~247: extracts `To:` header via `msg.Header.AddressList`, computes `operator-${resourcePrefix()}@${h.Domain}` as expected address, silently drops mismatches with an INFO-level stderr log line
- Fixed `sendReplyGetID` outbound `From` address: was `operator@%s`, now `operator-%s@%s` using `resourcePrefix()` — no bare `operator@` literal remains in production code
- W0-04 (`TestHandle_OperatorAddress_OwnPrefix`) and W0-05 (`TestHandle_OperatorAddress_ForeignPrefix_Drops`) now GREEN; all 50 email-handler tests pass

## Task Commits

TDD flow with two commits:

1. **RED — W0-04/W0-05 failing stubs** - `82b9f9a` (test)
2. **GREEN — Recipient verification gate + From fix + test fixture updates** - `7b1496f` (feat)

_TDD: RED commit first, then GREEN implementation._

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/cmd/email-create-handler/main.go` — Added 21-line recipient verification gate in Handle(); fixed sendReplyGetID From literal
- `/Users/khundeck/working/klankrmkr/cmd/email-create-handler/main_test.go` — Added W0-04/W0-05 tests, buildPlainEmailWithTo helper, captureStderr helper; updated email builder To: headers and handler Domain field to sandboxes.example.com

## Decisions Made

- Used `msg.Header.AddressList("To")` rather than `extractEmail(msg.Header.Get("To"))` for correct RFC 5322 address parsing (handles display-name + angle-bracket format)
- Verification gate returns `nil` (not an error) for foreign-prefix emails — consistent with the allowlist and safe-phrase silent-drop convention
- Updated test handler `Domain` from `"example.com"` to `"sandboxes.example.com"` to reflect reality (`KM_EMAIL_DOMAIN` in production is the full email subdomain+domain). All test email `To:` headers updated correspondingly.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Test fixtures used pre-prefix email address causing gate regressions**
- **Found during:** Task 1 GREEN phase (after implementing verification gate)
- **Issue:** Existing test email builders hardcoded `To: operator@sandboxes.example.com` and `newTestHandler`/`newTestHandlerWithAI` used `Domain: "example.com"`. After the gate landed, the computed expected address `operator-km@example.com` didn't match `operator@sandboxes.example.com`, causing 9 existing tests to fail
- **Fix:** Updated `buildMIMEEmailWithSubject`, `buildPlainEmailWithSubject`, `buildPlainEmailWithHeaders` To: headers to `operator-km@sandboxes.example.com`; updated both test handler constructors to `Domain: "sandboxes.example.com"`
- **Files modified:** cmd/email-create-handler/main_test.go
- **Verification:** All 50 email-handler tests pass
- **Committed in:** `7b1496f` (GREEN task commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 — test fixture mismatch with new gate)
**Impact on plan:** Essential correctness fix. No scope creep — no production code beyond what plan specified.

## Issues Encountered

None beyond the test fixture mismatch above.

## Next Phase Readiness

- Lambda is structurally ready for shared rule set deployment: cross-install email contamination bounded
- W0-04 and W0-05 GREEN — these stubs are now available for plan 84-10 UAT verification
- `cmd/email-create-handler/main.go` has no bare `operator@` literals in production paths

---
*Phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix*
*Completed: 2026-05-16*
