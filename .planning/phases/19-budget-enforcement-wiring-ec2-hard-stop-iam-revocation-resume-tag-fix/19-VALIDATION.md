---
phase: 19
slug: budget-enforcement-wiring-ec2-hard-stop-iam-revocation-resume-tag-fix
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-24
---

# Phase 19 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib) |
| **Config file** | none — standard `go test ./...` |
| **Quick run command** | `go test ./pkg/compiler/... ./internal/app/cmd/... -run TestBudget -v` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/compiler/... ./internal/app/cmd/... -run TestBudget -v`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 19-01-01 | 01 | 1 | BUDG-07 | unit | `go test ./pkg/compiler/... -run TestBudgetEnforcer` | ❌ W0 | ⬜ pending |
| 19-01-02 | 01 | 1 | BUDG-08 | unit | `go test ./internal/app/cmd/... -run TestBudget` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Add `dependency "sandbox"` assertions to `pkg/compiler/budget_enforcer_hcl_test.go`
- [ ] Add `TestResumeEC2Sandbox_UsesCorrectTagKey` to `internal/app/cmd/budget_test.go`
- [ ] Add `iam_role_arn` output to `infra/modules/ec2spot/v1.0.0/outputs.tf`

*(Framework install not needed — Go stdlib `testing` already in use)*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Budget-enforcer Lambda stops EC2 instance | BUDG-07 | Requires real AWS + running sandbox | Create sandbox with budget, wait for enforcement, verify instance stopped |
| km budget add resumes stopped sandbox | BUDG-08 | Requires real AWS + stopped sandbox | Stop sandbox via budget, run `km budget add`, verify instance started |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
