---
phase: 33
slug: ec2-storage-and-ami-selection
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-29
---

# Phase 33 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test |
| **Config file** | none — standard Go testing |
| **Quick run command** | `go test ./pkg/profile/... ./pkg/compiler/... -run "RootVolume\|AdditionalVolume\|Hibernat\|AMI" -count=1` |
| **Full suite command** | `go test ./pkg/profile/... ./pkg/compiler/... -count=1` |
| **Estimated runtime** | ~8 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick command
- **After every plan wave:** Run full suite command
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 8 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 33-01-01 | 01 | 1 | root-vol-size | unit | `go test ./pkg/profile/... -run RootVolume -count=1` | ❌ W0 | ⬜ pending |
| 33-01-02 | 01 | 1 | additional-vol | unit | `go test ./pkg/profile/... -run AdditionalVolume -count=1` | ❌ W0 | ⬜ pending |
| 33-01-03 | 01 | 1 | hibernation | unit | `go test ./pkg/profile/... -run Hibernat -count=1` | ❌ W0 | ⬜ pending |
| 33-01-04 | 01 | 1 | ami-spec | unit | `go test ./pkg/profile/... -run AMI -count=1` | ❌ W0 | ⬜ pending |
| 33-02-01 | 02 | 2 | terraform-hcl | unit | `go test ./pkg/compiler/... -run "RootVolume\|EBS\|Hibernat\|AMI" -count=1` | ❌ W0 | ⬜ pending |
| 33-02-02 | 02 | 2 | userdata-mount | unit | `go test ./pkg/compiler/... -run AdditionalVolume -count=1` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Profile schema test stubs for new RuntimeSpec fields
- [ ] Compiler test stubs for HCL generation with storage/AMI inputs
- [ ] Userdata test stubs for auto-mount script generation

*Existing Go test infrastructure covers all phase requirements.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| EBS volume actually mounts | additional-vol | Requires real EC2 instance | `km create` with additionalVolume, SSH in, check `df -h /data` |
| Hibernate preserves RAM | hibernation | Requires real on-demand instance | `km pause` on-demand sandbox, `km budget add` to restart, verify state |
| AMI resolves per-region | ami-spec | Requires real AWS API | `km create` with `ami: ubuntu-24.04` in different regions |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 8s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
