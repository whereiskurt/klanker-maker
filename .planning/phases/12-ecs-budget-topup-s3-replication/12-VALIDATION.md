---
phase: 12
slug: ecs-budget-topup-s3-replication
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-23
---

# Phase 12 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test + terraform validate |
| **Config file** | none — existing infrastructure |
| **Quick run command** | `go test ./internal/app/cmd/... -run TestBudget -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~10 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick command
- **After every plan wave:** Run full suite
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 10 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 12-01-01 | 01 | 1 | BUDG-08 | unit | `go test ./internal/app/cmd/... -run TestBudgetAdd -count=1` | partial | ⬜ pending |
| 12-02-01 | 02 | 1 | OBSV-06 | infra | `terraform validate` on s3-replication live config | ❌ new | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

*Existing test infrastructure covers budget command tests. Terraform validate covers infra.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| ECS task re-provisions from stored S3 profile | BUDG-08 | Requires live ECS cluster + suspended task | Create ECS sandbox, exhaust budget, run `km budget add`, verify task running |
| S3 replication deploys to secondary region | OBSV-06 | Requires live AWS with two regions | Run `terragrunt apply` on s3-replication config, verify replication rule |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 10s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
