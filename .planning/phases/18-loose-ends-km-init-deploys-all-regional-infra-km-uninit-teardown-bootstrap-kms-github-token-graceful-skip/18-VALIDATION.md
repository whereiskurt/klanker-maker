---
phase: 18
slug: loose-ends-km-init-deploys-all-regional-infra-km-uninit-teardown-bootstrap-kms-github-token-graceful-skip
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-23
---

# Phase 18 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib) |
| **Config file** | none — standard `go test ./...` |
| **Quick run command** | `go test ./internal/app/cmd/... -v` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/... -v`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 18-01-01 | 01 | 1 | km init all modules | unit | `go test ./internal/app/cmd/... -run TestInit` | ❌ W0 | ⬜ pending |
| 18-02-01 | 02 | 1 | km uninit refuses w/o --force | unit | `go test ./internal/app/cmd/... -run TestUninit` | ❌ W0 | ⬜ pending |
| 18-02-02 | 02 | 1 | km uninit reverse order | unit | `go test ./internal/app/cmd/... -run TestUninitDestroyOrder` | ❌ W0 | ⬜ pending |
| 18-03-01 | 03 | 1 | github-token graceful skip | unit | `go test ./internal/app/cmd/... -run TestCreateGitHubSkip` | ❌ W0 | ⬜ pending |
| 18-04-01 | 04 | 1 | km configure state_bucket | unit | `go test ./internal/app/cmd/... -run TestConfigureStateBucket` | ❌ W0 | ⬜ pending |
| 18-04-02 | 04 | 1 | km doctor checks all infra | unit | `go test ./internal/app/cmd/... -run TestDoctorLambda` | ❌ W0 | ⬜ pending |
| 18-05-01 | 05 | 1 | km bootstrap KMS e2e | manual | n/a — requires real AWS | n/a | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/uninit_test.go` — stubs for uninit command (mock runner + lister)
- [ ] Additional test cases in `internal/app/cmd/init_test.go` for multi-module expansion
- [ ] Additional test cases in `internal/app/cmd/create_test.go` for github-token skip path
- [ ] Additional test cases in `internal/app/cmd/configure_test.go` for state_bucket prompt
- [ ] Additional test cases in `internal/app/cmd/doctor_test.go` for TTL Lambda check

*(Framework install not needed — Go stdlib `testing` already in use across all cmd tests)*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| km bootstrap KMS key creation | KMS alias/km-platform | Requires real AWS account with IAM permissions | Run `km bootstrap` in test account, verify `aws kms describe-key --key-id alias/km-platform` returns key |
| km init full region deploy | All 6 modules apply | Requires live AWS + Terragrunt | Run `km init --region us-east-1`, verify all resources created |
| km uninit full teardown | Reverse dependency order | Requires live AWS + deployed infra | Deploy, then `km uninit --region us-east-1`, verify clean teardown |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
