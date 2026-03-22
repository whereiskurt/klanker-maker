---
phase: 4
slug: lifecycle-hardening-artifacts-email
status: draft
nyquist_compliant: true
wave_0_complete: true
created: 2026-03-22
---

# Phase 4 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing stdlib (no external framework) |
| **Config file** | none — `go test ./...` from repo root |
| **Quick run command** | `go test ./sidecars/audit-log/... ./pkg/aws/... ./pkg/profile/... ./pkg/compiler/... -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./sidecars/audit-log/... ./pkg/aws/... ./pkg/profile/... ./pkg/compiler/... -count=1`
- **After every plan wave:** Run `go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Nyquist Approach

All plans use **TDD inline** (`tdd="true"` on tasks). Each task's `<behavior>` block defines test expectations, and the task creates both tests and implementation in a single pass. No separate Wave 0 stub plan is needed because:

1. Every `<automated>` verify command references real test functions created by the task itself
2. TDD tasks write failing tests first (RED), then implement (GREEN) within the same execution
3. Test files are listed in each task's `<files>` field alongside production code

This is the standard TDD-inline approach — Wave 0 stubs are only needed when tasks lack `tdd="true"` and reference test files that don't exist yet.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | Status |
|---------|------|------|-------------|-----------|-------------------|--------|
| 04-01-T1 | 01 | 1 | OBSV-05, OBSV-07 | unit (TDD) | `go test ./sidecars/audit-log/... ./pkg/profile/... -run "TestRedact\|TestArtifact" -count=1` | pending |
| 04-01-T2 | 01 | 1 | OBSV-05 | unit (TDD) | `go test ./pkg/aws/... -run TestUploadArtifact -count=1` | pending |
| 04-02-T1 | 02 | 1 | MAIL-01, MAIL-05 | infra | `terraform -chdir=infra/modules/ses/v1.0.0 init -backend=false && terraform -chdir=infra/modules/ses/v1.0.0 validate` | pending |
| 04-02-T2 | 02 | 1 | MAIL-02, MAIL-03, MAIL-04 | unit (TDD) | `go test ./pkg/aws/... -run TestSES -count=1` | pending |
| 04-03-T1 | 03 | 2 | OBSV-04, PROV-13 | unit (TDD) | `go test ./pkg/compiler/... -run "TestFilesystem\|TestSpotPoll\|TestReadonly\|TestBindMount\|TestArtifactUploadScript\|TestIMDSToken" -count=1` | pending |
| 04-03-T2 | 03 | 2 | OBSV-05 | unit (TDD) | `go test ./pkg/lifecycle/... -count=1` | pending |
| 04-04-T1 | 04 | 3 | MAIL-02, MAIL-03, MAIL-04, MAIL-05 | integration | `go build ./cmd/km/ && go test ./internal/app/cmd/... ./pkg/compiler/... -count=1` | pending |
| 04-04-T2 | 04 | 3 | OBSV-06 | infra | `terraform -chdir=infra/modules/s3-replication/v1.0.0 init -backend=false && terraform -chdir=infra/modules/s3-replication/v1.0.0 validate` | pending |

*Status: pending / green / red / flaky*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| SES domain identity Terraform module creates domain identity + DKIM records | MAIL-01 | Terraform infra — no Go code path | Run `terraform plan` on SES module, verify domain identity and DKIM resources present |
| SES receipt rule routes inbox mail to correct S3 prefix | MAIL-05 | End-to-end SES delivery requires live AWS | Send test email to sandbox address, verify S3 object arrives at expected prefix |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify commands (TDD inline creates tests within task)
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] No separate Wave 0 needed — TDD inline is the adopted approach
- [x] No watch-mode flags
- [x] Feedback latency < 15s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
