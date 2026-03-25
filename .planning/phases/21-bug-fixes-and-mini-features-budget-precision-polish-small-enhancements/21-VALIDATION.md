---
phase: 21
slug: bug-fixes-and-mini-features-budget-precision-polish-small-enhancements
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-25
---

# Phase 21 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — standard Go test toolchain |
| **Quick run command** | `go test ./pkg/... ./internal/... ./sidecars/...` |
| **Full suite command** | `go test -count=1 ./...` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/... ./internal/...`
- **After every plan wave:** Run `go test -count=1 ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| TBD — populated during planning | | | | | | | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Existing infrastructure covers all phase requirements — go test toolchain is already configured.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| E2E sidecar verification (DNS, HTTP, audit, OTel) | Scope item 3 | Requires live AWS sandbox | Deploy sandbox, verify each sidecar produces expected artifacts |
| GitHub repo cloning | Scope item 4 | Requires live GitHub App + AWS | Deploy sandbox with repo config, verify clone succeeds |
| Inter-sandbox email | Scope item 5 | Requires two live sandboxes | Deploy two sandboxes, send email between them |
| Email allow-list | Scope item 6 | Requires live SES + external Gmail | Send from allowed/blocked addresses, verify filtering |
| Safe phrase override | Scope item 7 | Requires live email flow | Send email with safe phrase, verify override triggers |
| Action approval email | Scope item 8 | Requires live email + operator Gmail | Trigger approval request, reply from Gmail, verify action |
| OTP sync | Scope item 9 | Requires live SSM + sandbox | Deploy sandbox with OTP config, verify secret available |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
