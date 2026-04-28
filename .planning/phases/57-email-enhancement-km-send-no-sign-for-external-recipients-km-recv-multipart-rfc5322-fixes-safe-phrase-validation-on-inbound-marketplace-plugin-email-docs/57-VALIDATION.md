---
phase: 57
slug: email-enhancement-km-send-no-sign-for-external-recipients-km-recv-multipart-rfc5322-fixes-safe-phrase-validation-on-inbound-marketplace-plugin-email-docs
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-28
---

# Phase 57 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (with embedded bash heredoc fixtures via testdata/) |
| **Config file** | none — go.mod / go test conventions |
| **Quick run command** | `go test ./pkg/compiler/... -run "Phase57\|KmSend\|KmRecv\|MailPoller" -count=1` |
| **Full suite command** | `make test` |
| **Estimated runtime** | ~30 seconds (quick); ~3 minutes (full) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/compiler/... -run "Phase57\|KmSend\|KmRecv\|MailPoller" -count=1`
- **After every plan wave:** Run `make test`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds (quick); 180 seconds (full)

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 57-01-01 | 01 | 1 | km-send --no-sign skips SSM/openssl | unit | `go test ./pkg/compiler/... -run TestUserData_KmSend_NoSignFlag` | ❌ W0 | ⬜ pending |
| 57-01-02 | 01 | 1 | km-send --no-sign omits X-KM-* headers | unit | `go test ./pkg/compiler/... -run TestUserData_KmSend_NoSignOmitsHeaders` | ❌ W0 | ⬜ pending |
| 57-01-03 | 01 | 1 | km-send --no-sign keeps KM-AUTH safe phrase | unit | `go test ./pkg/compiler/... -run TestUserData_KmSend_NoSignKeepsAuthPhrase` | ❌ W0 | ⬜ pending |
| 57-02-01 | 02 | 1 | km-recv RFC5322 unfolds folded headers | unit | `go test ./pkg/compiler/... -run TestUserData_KmRecv_FoldedHeaders` | ❌ W0 | ⬜ pending |
| 57-02-02 | 02 | 1 | km-recv extracts text/plain from multipart/alternative | unit | `go test ./pkg/compiler/... -run TestUserData_KmRecv_MultipartAlternative` | ❌ W0 | ⬜ pending |
| 57-02-03 | 02 | 1 | km-recv handles nested multipart/mixed → multipart/alternative | unit | `go test ./pkg/compiler/... -run TestUserData_KmRecv_NestedMultipart` | ❌ W0 | ⬜ pending |
| 57-02-04 | 02 | 1 | km-recv shows external sender hint when X-KM-Sender-ID absent | unit | `go test ./pkg/compiler/... -run TestUserData_KmRecv_ExternalDisplay` | ❌ W0 | ⬜ pending |
| 57-02-05 | 02 | 1 | km-recv adds "external": true to JSON output for external senders | unit | `go test ./pkg/compiler/... -run TestUserData_KmRecv_ExternalJSONField` | ❌ W0 | ⬜ pending |
| 57-03-01 | 03 | 1 | km-mail-poller extracts sender_id unconditionally (after To-match) | unit | `go test ./pkg/compiler/... -run TestUserData_MailPoller_ExtractsSenderIdUnconditionally` | ❌ W0 | ⬜ pending |
| 57-03-02 | 03 | 1 | km-mail-poller fetches safe phrase from SSM at startup | unit | `go test ./pkg/compiler/... -run TestUserData_MailPoller_FetchesSafePhrase` | ❌ W0 | ⬜ pending |
| 57-03-03 | 03 | 1 | km-mail-poller drops external email missing safe phrase | unit | `go test ./pkg/compiler/... -run TestUserData_MailPoller_DropsExternalNoPhrase` | ❌ W0 | ⬜ pending |
| 57-03-04 | 03 | 1 | km-mail-poller delivers external email with safe phrase | unit | `go test ./pkg/compiler/... -run TestUserData_MailPoller_DeliversExternalWithPhrase` | ❌ W0 | ⬜ pending |
| 57-03-05 | 03 | 1 | km-mail-poller skips safe phrase check for sandbox-to-sandbox | unit | `go test ./pkg/compiler/... -run TestUserData_MailPoller_SkipsCheckForSandbox` | ❌ W0 | ⬜ pending |
| 57-03-06 | 03 | 1 | km-mail-poller fail-open when SSM unreachable, logs warning | unit | `go test ./pkg/compiler/... -run TestUserData_MailPoller_SsmFailOpen` | ❌ W0 | ⬜ pending |
| 57-03-07 | 03 | 1 | km-mail-poller uses grep -F (fixed-string) for phrase match | unit | `go test ./pkg/compiler/... -run TestUserData_MailPoller_PhraseMatchUsesFixedString` | ❌ W0 | ⬜ pending |
| 57-04-01 | 04 | 2 | skills/email/SKILL.md documents --no-sign flag | manual | `grep -q -- "--no-sign" skills/email/SKILL.md` | ❌ W0 | ⬜ pending |
| 57-04-02 | 04 | 2 | skills/email/SKILL.md documents safe phrase requirement | manual | `grep -q -i "safe phrase" skills/email/SKILL.md` | ❌ W0 | ⬜ pending |
| 57-04-03 | 04 | 2 | skills/email/SKILL.md documents /opt/km/bin/ paths | manual | `grep -q "/opt/km/bin/km-send" skills/email/SKILL.md` | ❌ W0 | ⬜ pending |
| 57-04-04 | 04 | 2 | skills/sandbox/SKILL.md documents external email workflow | manual | `grep -q -i "external email\|external recipient" skills/sandbox/SKILL.md` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/compiler/userdata_phase57_test.go` — new test file containing all 15 RED unit-test stubs (57-01-* through 57-03-*)
- [ ] `pkg/compiler/testdata/phase57/gmail_multipart_alternative.eml` — Gmail multipart/mixed → multipart/alternative fixture
- [ ] `pkg/compiler/testdata/phase57/folded_headers.eml` — RFC 5322 folded header (e.g., long X-KM-Signature) fixture
- [ ] `pkg/compiler/testdata/phase57/external_no_sender_id.eml` — external email without X-KM-Sender-ID
- [ ] `pkg/compiler/testdata/phase57/external_with_safe_phrase.eml` — external email containing the configured KM-AUTH safe phrase
- [ ] No new framework install — `go test` already configured

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end Gmail → sandbox delivery with safe phrase | Phase 57 goal | Requires real SES + real Gmail account | 1) `km create profiles/learn.yaml` 2) From Gmail, send to `<sandbox-id>@sandboxes.klankermaker.ai` with safe phrase in body 3) `km email read <sandbox-id>` 4) Verify email appears with `[EXTERNAL]` hint and HTML body extracted as text |
| End-to-end Gmail → sandbox blocked without safe phrase | Phase 57 goal | Requires real SES + real Gmail account | 1) Send Gmail to sandbox WITHOUT safe phrase 2) `km email read <sandbox-id>` 3) Verify email is dropped (not in mailbox) 4) Check sandbox cloud-init / km-mail-poller log for drop record |
| End-to-end sandbox → Gmail with --no-sign | Phase 57 goal | Requires real Gmail inbox to verify deliverability | 1) On sandbox: `km-send --no-sign --to whereiskurt@gmail.com --subject test --body /tmp/msg.txt` 2) Check Gmail inbox 3) Verify NO X-KM-Sender-ID / X-KM-Signature headers in raw source 4) Verify subject and body deliver correctly 5) Verify KM-AUTH phrase still present in body (operator-bound flow) |
| Sandbox-to-sandbox signed email regression | Phases 45/46 | Requires two live sandboxes | 1) `km create` two sandboxes 2) `km-send` from one to the other (no --no-sign) 3) `km-recv` on receiver verifies signature, shows X-KM-Sender-ID, no `[EXTERNAL]` hint |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s (quick); 180s (full)
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
