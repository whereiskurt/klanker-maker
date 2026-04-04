---
phase: 46
slug: ai-email-to-command
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-03
---

# Phase 46 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — existing test infra |
| **Quick run command** | `go test ./cmd/email-create-handler/... -v` |
| **Full suite command** | `go test ./cmd/email-create-handler/... ./pkg/aws/... -v` |
| **Estimated runtime** | ~5 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./cmd/email-create-handler/... -v`
- **After every plan wave:** Run `go test ./cmd/email-create-handler/... ./pkg/aws/... -v`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 10 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 46-01-01 | 01 | 1 | Bedrock client | unit | `go test ./cmd/email-create-handler/... -run TestBedrock` | ❌ W0 | ⬜ pending |
| 46-02-01 | 02 | 2 | Intent extraction | unit | `go test ./cmd/email-create-handler/... -run TestIntent` | ❌ W0 | ⬜ pending |
| 46-02-02 | 02 | 2 | Confirmation template | unit | `go test ./cmd/email-create-handler/... -run TestConfirmation` | ❌ W0 | ⬜ pending |
| 46-03-01 | 03 | 2 | Conversation state | unit | `go test ./cmd/email-create-handler/... -run TestConversation` | ❌ W0 | ⬜ pending |
| 46-04-01 | 04 | 3 | Reply handling | unit | `go test ./cmd/email-create-handler/... -run TestReply` | ❌ W0 | ⬜ pending |
| 46-04-02 | 04 | 3 | Command dispatch | unit | `go test ./cmd/email-create-handler/... -run TestDispatch` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Test stubs for Bedrock InvokeModel mock
- [ ] Conversation state S3 fixtures

*Existing email-create-handler test infra covers most needs.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Real Haiku response quality | Intent extraction accuracy | LLM output non-deterministic | Send test emails, verify confirmation templates |
| SES reply threading | In-Reply-To header chain | Requires real email client | Send email, reply, verify Lambda picks up thread |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 10s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
