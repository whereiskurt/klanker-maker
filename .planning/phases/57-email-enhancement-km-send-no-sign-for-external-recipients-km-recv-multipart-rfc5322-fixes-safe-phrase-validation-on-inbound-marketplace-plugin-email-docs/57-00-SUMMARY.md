---
phase: 57-email-enhancement-km-send-no-sign-for-external-recipients-km-recv-multipart-rfc5322-fixes-safe-phrase-validation-on-inbound-marketplace-plugin-email-docs
plan: "00"
subsystem: testing
tags: [go-test, tdd, red-scaffold, mime, email, km-send, km-recv, km-mail-poller]

requires:
  - phase: 45-email-km-send-km-recv
    provides: km-send/km-recv bash scripts embedded in userdata.go
  - phase: 46-email-ai-command
    provides: safe phrase SSM path and km-mail-poller allowlist enforcement

provides:
  - 15 RED test stubs in pkg/compiler/userdata_phase57_test.go locking the Phase 57 contract
  - 4 MIME .eml fixtures under pkg/compiler/testdata/phase57/ for UAT reference

affects:
  - phase: 57-01 (km-send --no-sign must turn TestUserData_KmSend_* GREEN)
  - phase: 57-02 (km-recv RFC5322+multipart must turn TestUserData_KmRecv_* GREEN)
  - phase: 57-03 (km-mail-poller safe phrase must turn TestUserData_MailPoller_* GREEN)

tech-stack:
  added: []
  patterns:
    - "RED-test scaffold: all 15 Phase 57 tests assert substrings not yet present in userdata.go"
    - "renderUD57() helper: stable test helper calling generateUserData(emailProfile(), 'sb-p57', ...)"
    - "Structural assertion with strings.Index ordering check for NoSignKeepsAuthPhrase"
    - "vet-safe assertion: want := backtick + '%s' concatenation avoids go vet Printf-directive false positive"

key-files:
  created:
    - pkg/compiler/userdata_phase57_test.go
    - pkg/compiler/testdata/phase57/gmail_multipart_alternative.eml
    - pkg/compiler/testdata/phase57/folded_headers.eml
    - pkg/compiler/testdata/phase57/external_no_sender_id.eml
    - pkg/compiler/testdata/phase57/external_with_safe_phrase.eml
  modified: []

key-decisions:
  - "TestUserData_MailPoller_SkipsCheckForSandbox asserts combined guard '[ -z $sender_id ] && [ -n ${KM_SAFE_PHRASE_CACHED:-}' not bare '[ -z $sender_id ]' (pre-existing in km-recv would have caused false PASS)"
  - "go vet Printf-directive false positive on t.Error('...%s...') fixed by string concatenation not nolint"
  - "CRLF enforced via Python bytes join — Write tool would strip carriage returns"

requirements-completed:
  - PHASE57-WAVE0

duration: 3min
completed: 2026-04-28
---

# Phase 57 Plan 00: RED Scaffold Summary

**Wave-0 nyquist scaffold: 15 RED unit-test stubs + 4 MIME fixtures locking the Phase 57 email-enhancement contract before any implementation begins**

## Performance

- **Duration:** 3 min
- **Started:** 2026-04-28T20:21:34Z
- **Completed:** 2026-04-28T20:24:58Z
- **Tasks:** 2
- **Files created:** 5

## Accomplishments
- Created 4 RFC 5322-compliant MIME .eml fixtures with proper CRLF line endings for UAT and future integration tests
- Created `pkg/compiler/userdata_phase57_test.go` with `renderUD57()` helper and all 15 RED test stubs
- All 15 new Phase 57 tests fail with descriptive assertion messages; pre-existing `TestKmSend*` / `TestKmRecv*` suite remains green
- `go vet` and `go build` clean

## Task Commits

1. **Task 1: Create 4 .eml MIME fixtures** - `78ba19c` (test)
2. **Task 2: Create RED test scaffold userdata_phase57_test.go** - `c38ffb8` (test)

## Test Functions (all 15, all RED)

### km-send --no-sign (Plan 57-01)
1. `TestUserData_KmSend_NoSignFlag` — expects `--no-sign` flag + `NO_SIGN=false` init
2. `TestUserData_KmSend_NoSignOmitsHeaders` — expects conditional `[[ -n "$sender_id" ]]` guard on X-KM-* headers
3. `TestUserData_KmSend_NoSignKeepsAuthPhrase` — expects KM-AUTH block present + ordered before `if ! $NO_SIGN` gate

### km-recv RFC 5322 + multipart + [EXTERNAL] (Plan 57-02)
4. `TestUserData_KmRecv_FoldedHeaders` — expects `unfold_headers()` bash function
5. `TestUserData_KmRecv_MultipartAlternative` — expects literal `multipart/alternative` in extract_body
6. `TestUserData_KmRecv_NestedMultipart` — expects `alt_boundary=` second-pass variable
7. `TestUserData_KmRecv_ExternalDisplay` — expects `[EXTERNAL]` literal in human-readable output
8. `TestUserData_KmRecv_ExternalJSONField` — expects `"external":%s` in JSON printf format

### km-mail-poller safe phrase gate (Plan 57-03)
9. `TestUserData_MailPoller_ExtractsSenderIdUnconditionally` — expects marker comment `# Always extract sender_id for external validation`
10. `TestUserData_MailPoller_FetchesSafePhrase` — expects SSM path `/km/config/remote-create/safe-phrase` + `KM_SAFE_PHRASE_CACHED=`
11. `TestUserData_MailPoller_DropsExternalNoPhrase` — expects `external email` + `safe phrase` rejection log + `MAIL_DIR/skipped`
12. `TestUserData_MailPoller_DeliversExternalWithPhrase` — expects `safe phrase OK` acceptance log
13. `TestUserData_MailPoller_SkipsCheckForSandbox` — expects combined `[ -z "$sender_id" ] && [ -n "${KM_SAFE_PHRASE_CACHED:-}`
14. `TestUserData_MailPoller_SsmFailOpen` — expects `2>/dev/null || true` + `safe phrase not available` WARN log
15. `TestUserData_MailPoller_PhraseMatchUsesFixedString` — expects `grep -qF` for phrase match

## .eml Fixtures

| File | MIME Shape | Purpose |
|------|-----------|---------|
| `gmail_multipart_alternative.eml` | multipart/mixed (outer) -> multipart/alternative (inner) -> text/plain + text/html + octet-stream attachment | Plan 02 UAT: nested multipart body extraction |
| `folded_headers.eml` | single text/plain; Subject folded 3 lines; X-KM-Signature folded at char 900 (~1100-char value) | Plan 02 UAT: RFC 5322 folded header parsing |
| `external_no_sender_id.eml` | plain text/plain; no X-KM-* headers; no KM-AUTH body line | Plan 03 UAT: external email rejected (no phrase) |
| `external_with_safe_phrase.eml` | plain text/plain; no X-KM-* headers; body line `KM-AUTH: open-sesame-phrase` | Plan 03 UAT: external email accepted (phrase match) |

## Decisions Made
- `TestUserData_MailPoller_SkipsCheckForSandbox` was tightened from `[ -z "$sender_id" ]` to the combined guard because the bare string already appears in the km-recv block at line 1163 of userdata.go, causing a false PASS. The combined condition is the actual Plan 03 code shape.
- `go vet` flags `t.Error("...%s...")` as a Printf-directive issue. Fixed by building the want string via concatenation (`want := \`"external":\` + \`%s\``) rather than using a nolint comment, which go vet ignores.
- MIME fixtures written via Python `bytes` join with `b"\r\n"` separator because the Write tool normalizes line endings.

## Deviations from Plan
None — plan executed exactly as written (minor vet fix applied inline during Task 2 without scope creep).

## Issues Encountered
- `TestUserData_MailPoller_SkipsCheckForSandbox` was initially PASS because `[ -z "$sender_id" ]` already existed in km-recv. Fixed assertion to use the full combined condition unique to Plan 03's gate.
- `go vet` flagged `t.Error(...)` with `%s` literal as a Printf directive. Fixed with string concatenation pattern.

## Next Phase Readiness
- Plans 57-01, 57-02, 57-03 can now execute against a locked test contract
- Each plan turns its 3-5-7 tests GREEN; the RED count decreases to 0 after Plan 03
- No production source was modified — only test scaffold + testdata

---
*Phase: 57-email-enhancement*
*Completed: 2026-04-28*
