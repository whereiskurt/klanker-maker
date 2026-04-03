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

## E2E Validation Loop

EFS touches the full provisioning pipeline: `km init` (Terraform module), `km create` (compiler + userdata), runtime (mount). Expect ~2 iterations.

### Loop procedure

```
# 1. Build and upload toolchain
make build && km init

# 2. Verify EFS was created by km init
aws efs describe-file-systems --profile klanker-terraform --region us-east-1 \
  --query 'FileSystems[?Name==`km-shared-efs`].[FileSystemId,LifeCycleState]'

# 3. Create a sandbox with mountEFS enabled (use goose profile)
km create profiles/goose.yaml --on-demand

# 4. Verify via shell
km shell <sandbox-id>
  # Inside the sandbox:
  df -h /shared                    # EFS should be mounted
  mount | grep efs                 # confirm EFS mount type
  touch /shared/test-from-$(hostname)  # write a test file
  ls -la /shared/                  # verify file exists

# 5. (Optional) Cross-sandbox test — create second sandbox
km create profiles/goose-ebpf.yaml --on-demand
km shell <sandbox-id-2>
  ls -la /shared/                  # should see test file from first sandbox

# 6. Tear down
km destroy <sandbox-id> --remote --yes
km destroy <sandbox-id-2> --remote --yes
# NOTE: km destroy does NOT remove EFS — it persists
```

### Post-E2E: Wire up goose profiles

After E2E passes, add `mountEFS: true` and `efsMountPoint: /shared` to:
- `profiles/goose.yaml`
- `profiles/goose-ebpf.yaml`
- `profiles/goose-ebpf-gatekeeper.yaml`

Commit and push so future `km create` invocations automatically get the shared filesystem.

### Expect iterations

- **Round 1:** `km init` Terraform errors (missing dependency on network outputs, SG wiring)
- **Round 2:** Userdata mount fails (missing `amazon-efs-utils` install, wrong EFS ID, TLS cert issue)
- **Round 3:** Clean run — mount works, cross-sandbox visibility confirmed

Budget ~3 create/destroy cycles and ~20 minutes. EFS itself costs ~$0.00 until data is written.

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 5s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
