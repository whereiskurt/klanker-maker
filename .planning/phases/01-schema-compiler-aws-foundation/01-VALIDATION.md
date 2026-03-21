---
phase: 1
slug: schema-compiler-aws-foundation
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-21
---

# Phase 1 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — Wave 0 installs |
| **Quick run command** | `go test ./pkg/profile/... ./pkg/compiler/...` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~5 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/profile/... ./pkg/compiler/...`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 10 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 01-01-01 | 01 | 1 | SCHM-01 | unit | `go test ./pkg/profile/... -run TestSchemaValid` | ❌ W0 | ⬜ pending |
| 01-01-02 | 01 | 1 | SCHM-02 | unit | `go test ./pkg/profile/... -run TestAllSections` | ❌ W0 | ⬜ pending |
| 01-01-03 | 01 | 1 | SCHM-03 | unit | `go test ./pkg/profile/... -run TestValidateErrors` | ❌ W0 | ⬜ pending |
| 01-01-04 | 01 | 1 | SCHM-04 | unit | `go test ./pkg/profile/... -run TestInheritance` | ❌ W0 | ⬜ pending |
| 01-01-05 | 01 | 1 | SCHM-05 | unit | `go test ./pkg/profile/... -run TestBuiltinProfiles` | ❌ W0 | ⬜ pending |
| 01-02-01 | 02 | 1 | INFR-08 | integration | `ls infra/modules/network infra/modules/ec2spot` | ❌ W0 | ⬜ pending |
| 01-03-01 | 03 | 2 | INFR-01 | manual | N/A — AWS console verification | N/A | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `go.mod` — initialize Go module
- [ ] `pkg/profile/profile_test.go` — test stubs for SCHM-01 through SCHM-05
- [ ] `pkg/compiler/compiler_test.go` — test stubs for compiler output
- [ ] Test fixtures: valid and invalid YAML profiles in `testdata/`

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| AWS multi-account setup | INFR-01 | Requires AWS console access | Verify management, terraform, application accounts exist with SSO |
| Route53 delegation | INFR-03 | Requires DNS propagation | Verify NS records delegate from management to application |
| KMS key provisioning | INFR-04 | Requires AWS access | Verify KMS key exists and is usable for SOPS |
| S3 bucket setup | INFR-05 | Requires AWS access | Verify artifact bucket with lifecycle policies |

*AWS infrastructure verifications are manual because they require authenticated AWS access and real account state.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 10s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
