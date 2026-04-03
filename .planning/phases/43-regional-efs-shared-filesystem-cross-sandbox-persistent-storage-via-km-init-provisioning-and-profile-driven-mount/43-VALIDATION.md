---
phase: 43
slug: regional-efs-shared-filesystem
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-02
---

# Phase 43 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — standard Go testing |
| **Quick run command** | `go test ./pkg/compiler/... -run TestEFS -count=1 -v` |
| **Full suite command** | `go test ./pkg/profile/... ./pkg/compiler/... ./internal/app/cmd/... -count=1` |
| **Estimated runtime** | ~5 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick command
- **After every plan wave:** Run full suite command
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 5 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 43-01-01 | 01 | 1 | EFS-01, EFS-05 | smoke | `ls infra/modules/efs/v1.0.0/main.tf` | ❌ W0 | ⬜ pending |
| 43-01-02 | 01 | 1 | EFS-03 | unit | `go test ./pkg/profile/... -run TestEFS -count=1 -v` | ❌ W0 | ⬜ pending |
| 43-02-01 | 02 | 2 | EFS-02 | unit | `go test ./internal/app/cmd/... -run TestLoadEFSOutputs -count=1 -v` | ❌ W0 | ⬜ pending |
| 43-02-02 | 02 | 2 | EFS-04, EFS-06 | unit | `go test ./pkg/compiler/... -run TestUserDataEFS -count=1 -v` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/compiler/efs_userdata_test.go` — stubs for EFS-04 (userdata mount block)
- [ ] `infra/modules/efs/v1.0.0/main.tf` — EFS-01, EFS-05 (Terraform module)

*Existing Go test infrastructure covers all phase requirements.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| EFS mounts at /shared on real EC2 | EFS-04 | Requires real AWS infra | `km init && km create` with `mountEFS: true`, `km shell`, check `df -h /shared` |
| Cross-sandbox file visibility | EFS-01 | Requires 2 running sandboxes | Create 2 sandboxes with `mountEFS: true`, write file in one, read in other |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 5s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
