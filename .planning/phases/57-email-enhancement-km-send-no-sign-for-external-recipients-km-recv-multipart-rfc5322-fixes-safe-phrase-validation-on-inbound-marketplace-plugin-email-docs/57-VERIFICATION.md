---
phase: 57-email-enhancement-km-send-no-sign-for-external-recipients-km-recv-multipart-rfc5322-fixes-safe-phrase-validation-on-inbound-marketplace-plugin-email-docs
verified: 2026-04-27T00:00:00Z
status: passed
score: 15/15 must-haves verified
re_verification: false
---

# Phase 57: External Email Enhancement Verification Report

**Phase Goal:** Fix km-send and km-recv bash scripts to handle external (non-sandbox) email. km-send gets `--no-sign` flag that skips Ed25519 SSM key fetch and X-KM-* headers, enabling plain email to Gmail/external addresses. km-recv gets RFC 5322 folded header parsing, multipart/alternative body extraction (Gmail HTML emails), and `--from-external` display hint (implemented as automatic detection). Inbound external emails must contain the configured safe phrase (from km configure) to be accepted — enforced in km-mail-poller (SES receipt rule is a pure S3 action; no Lambda). Update the marketplace plugin/skill docs.

**Verified:** 2026-04-27
**Status:** passed
**Re-verification:** No — initial verification

## Architectural Deviations (Intentional — NOT Gaps)

Two documented deviations from the roadmap text are intentional and confirmed present:

1. `--from-external` is implemented as **automatic detection** (no CLI flag) based on `X-KM-Sender-ID` absence. Plan 57-02 formalized this. The `[EXTERNAL]` display and `"external": true/false` JSON field deliver the behavior.
2. "SES receipt rule Lambda" does not exist. The `sandbox_inbound` SES rule is a pure S3 action (`infra/modules/ses/v1.0.0/main.tf:126`). Safe-phrase enforcement is in `km-mail-poller`. Plan 57-03 documents this explicitly in its objective.

---

## Observable Truths

| #  | Truth | Status | Evidence |
|----|-------|--------|----------|
| 1  | RED test stubs compile and all 15 Phase 57 tests existed at wave-0 scaffold | VERIFIED | `pkg/compiler/userdata_phase57_test.go` contains exactly 15 `TestUserData_*` functions; file compiles cleanly |
| 2  | 4 .eml fixtures exist on disk and are loadable | VERIFIED | All 4 present in `pkg/compiler/testdata/phase57/`; CRLF line endings confirmed (`\r\n` in od -c output); correct MIME structures verified |
| 3  | km-send accepts `--no-sign` flag without error | VERIFIED | `TestUserData_KmSend_NoSignFlag` PASS; `--no-sign) NO_SIGN=true; shift ;;` at line 759; `NO_SIGN=false` init at line 740 |
| 4  | `--no-sign` skips SSM fetch, openssl sign, and X-KM-* header emission | VERIFIED | `TestUserData_KmSend_NoSignOmitsHeaders` PASS; `if ! $NO_SIGN; then` gate at line 915; `[[ -n "$sender_id" ]] && printf 'X-KM-Sender-ID:` conditional at emit_headers |
| 5  | `--no-sign` does NOT suppress KM-AUTH operator-inbox safe-phrase append | VERIFIED | `TestUserData_KmSend_NoSignKeepsAuthPhrase` PASS; KM-AUTH block at line ~797 precedes NO_SIGN gate at line 915; unconditional |
| 6  | km-recv unfolds RFC 5322 folded headers before parsing | VERIFIED | `TestUserData_KmRecv_FoldedHeaders` PASS; `unfold_headers()` awk function present in userdata.go (1 occurrence) |
| 7  | km-recv extracts text/plain from multipart/alternative (Gmail layout) | VERIFIED | `TestUserData_KmRecv_MultipartAlternative` PASS; `multipart/alternative` substring present (7 occurrences) |
| 8  | km-recv handles nested multipart/mixed wrapping multipart/alternative | VERIFIED | `TestUserData_KmRecv_NestedMultipart` PASS; `alt_boundary=` variable present (6 occurrences) for second-pass scan |
| 9  | km-recv appends `[EXTERNAL]` to human output for unsigned external senders | VERIFIED | `TestUserData_KmRecv_ExternalDisplay` PASS; `[EXTERNAL]` literal present in userdata.go |
| 10 | km-recv JSON output includes `"external": true/false` field | VERIFIED | `TestUserData_KmRecv_ExternalJSONField` PASS; `"external":%s` format token present in userdata.go |
| 11 | km-mail-poller fetches safe phrase from SSM at startup, cached, fail-open | VERIFIED | `TestUserData_MailPoller_FetchesSafePhrase` + `TestUserData_MailPoller_SsmFailOpen` PASS; `KM_SAFE_PHRASE_CACHED` at lines 634-643; `2>/dev/null \|\| true` + WARN log present |
| 12 | sender_id/sender_email extracted unconditionally (hoisted out of allowlist block) | VERIFIED | `TestUserData_MailPoller_ExtractsSenderIdUnconditionally` PASS; comment `# Always extract sender_id for external validation` at line 658 |
| 13 | External email without safe phrase is rejected to MAIL_DIR/skipped | VERIFIED | `TestUserData_MailPoller_DropsExternalNoPhrase` PASS; rejection block at lines 700-703; log includes `rejected: missing/invalid safe phrase` |
| 14 | External email with safe phrase is accepted and logged | VERIFIED | `TestUserData_MailPoller_DeliversExternalWithPhrase` PASS; acceptance log `safe phrase OK` at line 705 |
| 15 | Sandbox-to-sandbox email (sender_id non-empty) bypasses safe-phrase gate | VERIFIED | `TestUserData_MailPoller_SkipsCheckForSandbox` PASS; gate guard `[ -z "$sender_id" ] && [ -n "${KM_SAFE_PHRASE_CACHED:-}" ]` at line 696 |
| 16 | Phrase match uses `grep -qF` (fixed string, not regex) | VERIFIED | `TestUserData_MailPoller_PhraseMatchUsesFixedString` PASS; `grep -qF "KM-AUTH: ${KM_SAFE_PHRASE_CACHED}"` at line 698 |
| 17 | skills/email/SKILL.md documents --no-sign, external field, safe-phrase, absolute paths | VERIFIED | All 6 grep assertions pass: `--no-sign`, `safe phrase`, `/opt/km/bin/km-send`, `/opt/km/bin/km-recv`, `"external"`, `[EXTERNAL]` all present |
| 18 | skills/sandbox/SKILL.md documents external email workflow | VERIFIED | All 3 grep assertions pass: `external email\|external recipient`, `--no-sign`, `KM-AUTH` all present |
| 19 | skills/operator/SKILL.md and skills/user/SKILL.md NOT modified | VERIFIED | `git log -- skills/operator/SKILL.md skills/user/SKILL.md` — last touch is commit `68887dd` (Phase <57 refactor); Phase 57 commits only touch `skills/email/SKILL.md` and `skills/sandbox/SKILL.md` |

**Score:** 15/15 test truths verified (19/19 including docs truths)

---

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/compiler/userdata_phase57_test.go` | 15 RED-then-GREEN test stubs | VERIFIED | 15 `TestUserData_*` functions; all 15 PASS; compiles cleanly |
| `pkg/compiler/testdata/phase57/gmail_multipart_alternative.eml` | Gmail multipart/mixed+alternative fixture | VERIFIED | Present; `boundary="outer"` and `boundary="inner"` confirmed; CRLF line endings |
| `pkg/compiler/testdata/phase57/folded_headers.eml` | RFC 5322 folded Subject + X-KM-Signature fixture | VERIFIED | Present; `X-KM-Sender-ID:` header present; CRLF endings |
| `pkg/compiler/testdata/phase57/external_no_sender_id.eml` | External email without KM headers or safe phrase | VERIFIED | Present; 0 occurrences of `X-KM-Sender-ID:` confirmed |
| `pkg/compiler/testdata/phase57/external_with_safe_phrase.eml` | External email with `KM-AUTH: open-sesame-phrase` | VERIFIED | Present; `KM-AUTH: open-sesame-phrase` confirmed; CRLF endings |
| `pkg/compiler/userdata.go` (km-send block) | `--no-sign` flag, NO_SIGN gate, conditional headers | VERIFIED | `NO_SIGN=false` init; `--no-sign) NO_SIGN=true` case arm; `if ! $NO_SIGN; then` SSM gate; `[[ -n "$sender_id" ]]` conditional emission |
| `pkg/compiler/userdata.go` (km-recv block) | `unfold_headers()`, `alt_boundary`, `[EXTERNAL]`, `"external":%s` | VERIFIED | All four substrings confirmed present |
| `pkg/compiler/userdata.go` (km-mail-poller block) | `KM_SAFE_PHRASE_CACHED`, unconditional sender_id, `grep -qF`, rejection/acceptance logs | VERIFIED | All confirmed at specific line numbers |
| `skills/email/SKILL.md` | `--no-sign` table row, External Recipients section, JSON external field, safe-phrase note, absolute paths | VERIFIED | All 6 grep checks pass; table row at line 53; dedicated section from line 67; JSON example at line 116 |
| `skills/sandbox/SKILL.md` | External email workflow note in Step 4 and Identity Summary | VERIFIED | All 3 grep checks pass; Step 4 note at line 73; Identity Summary bullet at line 85 |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| km-send arg-parse | `NO_SIGN=true` | `--no-sign) NO_SIGN=true; shift ;;` | WIRED | Line 759 |
| km-send signing block | `NO_SIGN` gate | `if ! $NO_SIGN; then` | WIRED | Line 915 |
| km-send build_mime site | SENDER_ID clearance | `if $NO_SIGN; then SENDER_ID=""` | WIRED | Line 937 |
| emit_headers() | Conditional X-KM-* | `[[ -n "$sender_id" ]] &&` guards | WIRED | Both printf lines guarded |
| process_messages() header assembly | unfold_headers() pipeline | `raw_for_parse=...unfold_headers` before `parse_headers` | WIRED | Confirmed by FoldedHeaders test PASS |
| extract_body() first-pass | `alt_boundary` second-pass | `alt_boundary=` capture triggers inner scan | WIRED | Confirmed by NestedMultipart test PASS |
| km-mail-poller pre-loop | SSM safe-phrase fetch | `aws ssm get-parameter --name /km/config/remote-create/safe-phrase ... 2>/dev/null \|\| true` | WIRED | Lines 635-643 |
| per-file processing | unconditional sender_id | After `mv "$tmp_file" "$local_file"`, before allowlist block | WIRED | Line 658 comment + extraction |
| external safe-phrase gate | `grep -qF` phrase match | `echo "$body_check" \| grep -qF "KM-AUTH: ${KM_SAFE_PHRASE_CACHED}"` | WIRED | Line 698 |
| skills/email/SKILL.md | `--no-sign` table row | `\| \`--no-sign\` \|` in Send Flags Reference table | WIRED | Line 53 |
| skills/email/SKILL.md | `"external":` JSON example | JSON code block with `"external": false` | WIRED | Line 116 |
| skills/sandbox/SKILL.md | external email note | Step 4 paragraph + Identity Summary bullet | WIRED | Lines 73, 85 |

---

## Requirements Coverage

| Requirement ID | Source Plan | Description | Status |
|----------------|-------------|-------------|--------|
| PHASE57-WAVE0 | 57-00 | RED test scaffold + 4 .eml fixtures | SATISFIED |
| PHASE57-KMSEND-NOSIGN | 57-01 | km-send `--no-sign` flag implementation | SATISFIED |
| PHASE57-KMRECV-RFC5322 | 57-02 | RFC 5322 folded header unfolding | SATISFIED |
| PHASE57-KMRECV-MULTIPART | 57-02 | multipart/alternative two-level body extraction | SATISFIED |
| PHASE57-KMRECV-EXTERNAL | 57-02 | [EXTERNAL] display hint + JSON external field | SATISFIED |
| PHASE57-MAILPOLLER-SAFEPHRASE | 57-03 | Safe-phrase SSM fetch, gate, fail-open | SATISFIED |
| PHASE57-MAILPOLLER-SENDERID-SCOPING | 57-03 | Unconditional sender_id extraction (hoisted) | SATISFIED |
| PHASE57-MAILPOLLER-FIXEDSTRING | 57-03 | `grep -F` fixed-string phrase match | SATISFIED |
| PHASE57-DOCS-EMAIL | 57-04 | skills/email/SKILL.md updated | SATISFIED |
| PHASE57-DOCS-SANDBOX | 57-04 | skills/sandbox/SKILL.md updated | SATISFIED |

---

## Anti-Patterns Found

None blocking. No TODO/FIXME/placeholder comments introduced in phase 57 files. No empty implementations. The `2>/dev/null || true` pattern in the SSM fetch is intentional fail-open design, not a suppressed error.

---

## Human Verification Required

### 1. End-to-End `--no-sign` Outbound Send

**Test:** From inside a live sandbox, run `km-send --no-sign --to whereiskurt@gmail.com --subject "test" --body /tmp/msg.txt` and confirm the email arrives in Gmail with no `X-KM-Sender-ID` or `X-KM-Signature` headers.

**Expected:** Email delivered to Gmail; no X-KM-* headers visible in Gmail's "Show original".

**Why human:** Requires live AWS SES + real Gmail mailbox; cannot be verified by grep on source.

### 2. Inbound Safe-Phrase Gate (Live Poller)

**Test:** Send an external email to a sandbox address from a non-sandbox address, without the safe phrase. Verify the message does NOT appear in `km-recv` output and lands in `/var/mail/km/skipped/`.

**Expected:** Message silently dropped to skipped; `km-recv` shows no new mail; km-mail-poller log shows rejection message.

**Why human:** Requires live SES inbound delivery + running km-mail-poller systemd service on an EC2 sandbox.

### 3. RFC 5322 Folded Header Display

**Test:** Send an email from a real Gmail account to a sandbox address that has a Subject line long enough to be SES-folded. Read it with `km-recv` and verify Subject displays as a single unfolded line.

**Expected:** Subject appears as one line in km-recv output, not truncated at the first physical fold.

**Why human:** Requires live SES inbound with actual header folding behavior; CRLF fixture tests unit behavior but not the full pipeline.

---

## Gaps Summary

No gaps. All 15 Phase 57 test functions pass GREEN. All key implementation patterns verified in source. All skill documentation verified by grep. operators/user skills untouched. Architectural deviations are intentional, pre-documented, and the correct implementation choices given codebase reality.

---

_Verified: 2026-04-27_
_Verifier: Claude (gsd-verifier)_
