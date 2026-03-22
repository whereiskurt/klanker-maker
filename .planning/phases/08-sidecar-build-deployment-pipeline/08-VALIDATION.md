---
phase: 08
slug: sidecar-build-deployment-pipeline
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-22
---

# Phase 08 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test + make |
| **Config file** | Makefile (created in this phase) |
| **Quick run command** | `go build ./sidecars/... && go test ./pkg/compiler/... -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~35 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick command
- **After every plan wave:** Run full suite
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 35 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 08-01-01 | 01 | 1 | NETW-02, NETW-03, OBSV-01, OBSV-02 | build | `make sidecars && ls -la build/` | ❌ (created) | ⬜ pending |
| 08-01-02 | 01 | 1 | NETW-02, NETW-03, OBSV-01, OBSV-02 | build | `make docker-build` | ❌ (created) | ⬜ pending |
| 08-02-01 | 02 | 2 | PROV-10 | unit | `go test ./pkg/compiler/... -run TestECSImage -v -count=1` | ❌ (TDD) | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

*Existing infrastructure covers all phase requirements. All plans use TDD inline or build verification.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| EC2 sidecar download at boot | NETW-02, OBSV-01 | Requires live EC2 sandbox | `km create` EC2 profile, SSH in, verify sidecar processes running |
| ECS sidecar container pull | PROV-10 | Requires ECR + ECS cluster | `km create` ECS profile, check ECS task events for successful image pulls |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 35s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
