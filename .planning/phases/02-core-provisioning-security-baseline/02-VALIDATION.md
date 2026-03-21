---
phase: 2
slug: core-provisioning-security-baseline
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-21
---

# Phase 2 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — existing go.mod |
| **Quick run command** | `go test ./pkg/compiler/... ./pkg/terragrunt/... ./pkg/aws/...` |
| **Full suite command** | `go test ./...` |
| **Estimated runtime** | ~5 seconds (unit), ~60s (integration with AWS) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/compiler/... ./pkg/terragrunt/... ./pkg/aws/...`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 10 seconds (unit tests)

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 02-01-01 | 01 | 1 | PROV-01 | unit | `go test ./pkg/compiler/... -run TestCompile` | ❌ W0 | ⬜ pending |
| 02-01-02 | 01 | 1 | PROV-09 | unit | `go test ./pkg/compiler/... -run TestSubstrate` | ❌ W0 | ⬜ pending |
| 02-02-01 | 02 | 1 | PROV-01 | unit | `go test ./internal/app/cmd/... -run TestCreate` | ❌ W0 | ⬜ pending |
| 02-02-02 | 02 | 1 | PROV-02 | unit | `go test ./internal/app/cmd/... -run TestDestroy` | ❌ W0 | ⬜ pending |
| 02-03-01 | 03 | 2 | NETW-01 | integration | `go test ./... -run TestSGEgress -tags integration` | ❌ W0 | ⬜ pending |
| 02-03-02 | 03 | 2 | PROV-08 | integration | `go test ./... -run TestTagging -tags integration` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/compiler/compiler_test.go` — test stubs for compilation output
- [ ] `pkg/terragrunt/runner_test.go` — test stubs for Terragrunt execution
- [ ] `pkg/aws/ec2_test.go` — test stubs for tag-based lookup and instance termination

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| EC2 spot instance provisions | PROV-11 | Requires real AWS spot capacity | `km create profiles/open-dev.yaml` → verify instance in console |
| ECS Fargate Spot task runs | PROV-12 | Requires real ECS cluster | `km create` with ecs substrate → verify task in console |
| IMDSv2 enforced | NETW-05 | Requires SSH/SSM into instance | From inside: `curl http://169.254.169.254/latest/meta-data/` should fail without token |
| Secrets injected as env vars | NETW-06 | Requires running instance with SSM params | SSM into instance, check env vars |
| SOPS decrypt at boot | NETW-07 | Requires KMS + SSM integration | Verify decrypted value matches SSM parameter |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 10s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
