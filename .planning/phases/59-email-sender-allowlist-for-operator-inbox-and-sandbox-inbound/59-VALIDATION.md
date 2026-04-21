---
phase: 59
slug: email-sender-allowlist-for-operator-inbox-and-sandbox-inbound
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-21
---

# Phase 59 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — existing test infrastructure |
| **Quick run command** | `go test ./pkg/aws/... ./internal/app/cmd/... -run TestAllowList -count=1` |
| **Full suite command** | `go test ./pkg/aws/... ./internal/app/cmd/... ./cmd/email-create-handler/... -count=1` |
| **Estimated runtime** | ~5 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/aws/... -run TestAllowList -count=1`
- **After every plan wave:** Run full suite command
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 5 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 59-01-01 | 01 | 1 | Email pattern matching in MatchesAllowList | unit | `go test ./pkg/aws/... -run TestAllowList` | ❌ W0 | ⬜ pending |
| 59-01-02 | 01 | 1 | Config struct EmailAllowedSenders field | unit | `go test ./internal/app/config/...` | ✅ | ⬜ pending |
| 59-02-01 | 02 | 1 | Lambda sender allowlist enforcement | unit | `go test ./cmd/email-create-handler/...` | ❌ W0 | ⬜ pending |
| 59-02-02 | 02 | 1 | km-recv bash sender filtering | manual | SSH + send test email | N/A | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/aws/identity_test.go` — add tests for email pattern matching in MatchesAllowList
- [ ] `cmd/email-create-handler/main_test.go` — add tests for sender allowlist check

*Existing infrastructure covers framework and fixtures.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| km-recv rejects non-allowed sender | Bash script enforcement | Shell script, no Go test harness | Send email from non-allowed address, verify rejection in km-recv output |
| Lambda rejects non-allowed sender | Live SES integration | Requires actual email delivery | Send email from non-allowed address to operator@, verify no processing in CloudWatch |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 5s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
