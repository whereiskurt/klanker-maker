---
phase: 10
slug: scp-sandbox-containment-org-level-ec2-breakout-prevention
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-22
---

# Phase 10 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — standard Go test infrastructure |
| **Quick run command** | `go test ./infra/modules/scp/... ./internal/app/cmd/...` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~30 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./infra/modules/scp/... ./internal/app/cmd/...`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 10-01-01 | 01 | 1 | SCP module | unit | `go test ./infra/modules/scp/...` | ❌ W0 | ⬜ pending |
| 10-01-02 | 01 | 1 | SCP JSON | unit | `go test ./infra/modules/scp/...` | ❌ W0 | ⬜ pending |
| 10-02-01 | 02 | 1 | bootstrap wiring | unit | `go test ./internal/app/cmd/...` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `infra/modules/scp/v1.0.0/main.tf` — SCP Terraform module
- [ ] SCP JSON policy validation via `terraform validate` or plan output inspection

*Existing Go test infrastructure covers bootstrap command testing.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| SCP blocks sandbox role from SG mutation | SG deny | Requires real AWS Organizations + multi-account | Deploy SCP, assume sandbox role, attempt `ec2:AuthorizeSecurityGroupIngress` — expect AccessDenied |
| SCP allows provisioner role through | Carve-outs | Requires real AWS SSO role assumption | Assume provisioner role, run `km create` — expect success |
| Region lock blocks out-of-region actions | Region deny | Requires multi-region AWS setup | Assume sandbox role, attempt `ec2:DescribeInstances` in non-allowed region — expect AccessDenied |

*SCP enforcement is an AWS Organizations primitive — unit tests verify JSON structure, but real enforcement requires multi-account deployment.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
