---
phase: 4
slug: lifecycle-hardening-artifacts-email
status: draft
nyquist_compliant: false
wave_0_complete: false
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

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 04-01-01 | 01 | 0 | OBSV-04 | unit | `go test ./pkg/compiler/... -run TestFilesystem -count=1` | ❌ W0 | ⬜ pending |
| 04-01-02 | 01 | 0 | OBSV-05 | unit | `go test ./pkg/aws/... -run TestUploadArtifacts -count=1` | ❌ W0 | ⬜ pending |
| 04-01-03 | 01 | 0 | OBSV-05 | unit | `go test ./pkg/profile/... -run TestArtifacts -count=1` | ❌ W0 | ⬜ pending |
| 04-01-04 | 01 | 0 | OBSV-06 | unit | `go test ./pkg/compiler/... -run TestReplication -count=1` | ❌ W0 | ⬜ pending |
| 04-01-05 | 01 | 0 | OBSV-07 | unit | `go test ./sidecars/audit-log/... -run TestRedact -count=1` | ❌ W0 | ⬜ pending |
| 04-01-06 | 01 | 0 | OBSV-07 | unit | `go test ./sidecars/audit-log/... -run TestRedactLiteral -count=1` | ❌ W0 | ⬜ pending |
| 04-01-07 | 01 | 0 | OBSV-07 | unit | `go test ./sidecars/audit-log/... -run TestRedactStructural -count=1` | ❌ W0 | ⬜ pending |
| 04-01-08 | 01 | 0 | PROV-13 | unit | `go test ./pkg/compiler/... -run TestSpotPollLoop -count=1` | ❌ W0 | ⬜ pending |
| 04-01-09 | 01 | 0 | MAIL-02 | unit | `go test ./pkg/aws/... -run TestProvisionSandboxEmail -count=1` | ❌ W0 | ⬜ pending |
| 04-01-10 | 01 | 0 | MAIL-03 | unit | `go test ./pkg/compiler/... -run TestSESIAM -count=1` | ❌ W0 | ⬜ pending |
| 04-01-11 | 01 | 0 | MAIL-04 | unit | `go test ./pkg/aws/... -run TestSendLifecycle -count=1` | ❌ W0 | ⬜ pending |
| 04-XX-XX | XX | X | MAIL-01 | manual | manual — Terraform plan inspection | N/A | ⬜ pending |
| 04-XX-XX | XX | X | MAIL-05 | manual | manual — SES receipt rule S3 verification | N/A | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `sidecars/audit-log/redact_test.go` — stubs for OBSV-07 redaction tests
- [ ] `pkg/aws/artifacts_test.go` — stubs for OBSV-05 upload logic
- [ ] `pkg/aws/ses_test.go` — stubs for MAIL-02, MAIL-04
- [ ] `pkg/compiler/filesystem_test.go` — stubs for OBSV-04 compiler output
- [ ] `pkg/compiler/spot_test.go` — stubs for PROV-13 spot poll loop
- [ ] `pkg/compiler/ses_iam_test.go` — stubs for MAIL-03 SES IAM policy

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| SES domain identity Terraform module creates domain identity + DKIM records | MAIL-01 | Terraform infra — no Go code path | Run `terraform plan` on SES module, verify domain identity and DKIM resources present |
| SES receipt rule routes inbox mail to correct S3 prefix | MAIL-05 | End-to-end SES delivery requires live AWS | Send test email to sandbox address, verify S3 object arrives at expected prefix |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
