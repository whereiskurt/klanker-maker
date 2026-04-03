---
phase: 43-regional-efs-shared-filesystem-cross-sandbox-persistent-storage-via-km-init-provisioning-and-profile-driven-mount
verified: 2026-04-03T02:14:25Z
status: passed
score: 12/12 must-haves verified
re_verification: false
---

# Phase 43: Regional EFS Shared Filesystem Verification Report

**Phase Goal:** `km init` provisions a Regional EFS filesystem with mount targets in each AZ, and sandboxes with `mountEFS: true` in their profile automatically mount the shared filesystem at a configurable path — enabling cross-sandbox artifact sharing without S3
**Verified:** 2026-04-03T02:14:25Z
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | km init provisions a Regional EFS filesystem with encryption and Elastic throughput | VERIFIED | `infra/modules/efs/v1.0.0/main.tf`: `encrypted=true`, `throughput_mode="elastic"`, `performance_mode="generalPurpose"` |
| 2 | EFS mount targets are created in every AZ subnet of the shared VPC | VERIFIED | `aws_efs_mount_target.shared` with `count=length(var.subnet_ids)`, one per subnet |
| 3 | An EFS security group allows NFS port 2049 ingress only from sandbox SGs | VERIFIED | `aws_security_group.efs` ingress: `from_port=2049`, `to_port=2049`, `security_groups=[var.sandbox_sg_id]` — no CIDR used |
| 4 | Profile schema accepts mountEFS and efsMountPoint fields on RuntimeSpec | VERIFIED | `pkg/profile/types.go` lines 155-160: `MountEFS bool` and `EFSMountPoint string` with correct yaml/json tags |
| 5 | km init applies the efs module after network (dependency order) | VERIFIED | `regionalModules()` in `init.go` places `efs` immediately after `network` with comment explaining dependency |
| 6 | LoadEFSOutputs reads filesystem_id from efs/outputs.json | VERIFIED | `LoadEFSOutputs()` at line 494-511 of `init.go`; returns `("", nil)` when file missing |
| 7 | NetworkConfig carries EFSFilesystemID from init outputs to the compiler | VERIFIED | `service_hcl.go` line 570-572: `EFSFilesystemID string` field on `NetworkConfig` |
| 8 | create.go populates EFSFilesystemID on NetworkConfig before calling Compile() | VERIFIED | `create.go` lines 351-358: calls `LoadEFSOutputs`, assigns `network.EFSFilesystemID = efsID` |
| 9 | create.go errors when profile has mountEFS:true but EFS is not initialized | VERIFIED | `create.go` line 358: returns descriptive error if `MountEFS && efsID == ""` |
| 10 | Userdata installs amazon-efs-utils and mounts EFS with TLS + _netdev,nofail when mountEFS is true | VERIFIED | Template section 2.7 (lines 113-131 of `userdata.go`): `yum install -y amazon-efs-utils`, fstab entry with `_netdev,nofail,tls`, `mountpoint -q` verification |
| 11 | Userdata omits EFS block entirely when mountEFS is false or EFSFilesystemID is empty | VERIFIED | Template guard `{{- if .EFSFilesystemID }}` at line 113; `generateUserData` only sets params when `MountEFS && network != nil && network.EFSFilesystemID != ""` |
| 12 | km destroy does not reference or teardown EFS resources | VERIFIED | `destroy.go` has zero EFS references (confirmed by `TestDestroyNoEFSReference` passing) |

**Score:** 12/12 truths verified

---

## Required Artifacts

### Plan 01 Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `infra/modules/efs/v1.0.0/main.tf` | EFS filesystem, mount targets, security group | VERIFIED | Contains `aws_efs_file_system`, `aws_security_group`, `aws_efs_mount_target` |
| `infra/modules/efs/v1.0.0/variables.tf` | Module input variables | VERIFIED | Declares `vpc_id`, `subnet_ids`, `sandbox_sg_id`, `km_label`, `region_label` |
| `infra/modules/efs/v1.0.0/outputs.tf` | filesystem_id and security_group_id outputs | VERIFIED | Exports both outputs correctly |
| `infra/live/use1/efs/terragrunt.hcl` | Terragrunt config wiring EFS module to network outputs | VERIFIED | References `modules/efs/v1.0.0`, reads `network/outputs.json` |
| `pkg/profile/types.go` | MountEFS and EFSMountPoint fields on RuntimeSpec | VERIFIED | Both fields present with correct omitempty tags |
| `internal/app/cmd/init.go` | efs entry in regionalModules() + output capture + LoadEFSOutputs | VERIFIED | All three present |

### Plan 02 Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/cmd/init.go` | LoadEFSOutputs() function | VERIFIED | Lines 494-511; correct IsNotExist guard |
| `pkg/compiler/service_hcl.go` | EFSFilesystemID field on NetworkConfig | VERIFIED | Line 570-572 |
| `pkg/compiler/userdata.go` | EFS mount block + EFSFilesystemID/EFSMountPoint on userDataParams | VERIFIED | Template section 2.7 + params at lines 945-948 |
| `internal/app/cmd/create.go` | EFS output loading and NetworkConfig population | VERIFIED | Lines 351-358 |
| `pkg/compiler/compiler.go` | network passed to generateUserData | VERIFIED | Line 112: `generateUserData(p, sandboxID, secretPaths, artifactsBucket, useSpot, network, ...)` |

---

## Key Link Verification

### Plan 01 Key Links

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `infra/live/use1/efs/terragrunt.hcl` | `infra/modules/efs/v1.0.0` | terraform source | WIRED | `source = "${local.repo_root}/infra/modules/efs/v1.0.0"` |
| `infra/live/use1/efs/terragrunt.hcl` | `infra/live/use1/network/outputs.json` | jsondecode(file()) | WIRED | `jsondecode(file("${get_terragrunt_dir()}/../network/outputs.json"))` |
| `internal/app/cmd/init.go` | `infra/live/{regionLabel}/efs` | regionalModules() slice entry | WIRED | `name: "efs"` after `name: "network"` in slice |

### Plan 02 Key Links

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/app/cmd/create.go` | `internal/app/cmd/init.go` | LoadEFSOutputs() call | WIRED | `efsID, err := LoadEFSOutputs(repoRoot, regionLabel)` |
| `internal/app/cmd/create.go` | `pkg/compiler/service_hcl.go` | network.EFSFilesystemID assignment | WIRED | `network.EFSFilesystemID = efsID` |
| `pkg/compiler/userdata.go` | `pkg/compiler/service_hcl.go` | userDataParams.EFSFilesystemID from NetworkConfig | WIRED | `params.EFSFilesystemID = network.EFSFilesystemID` at line 1087 |
| `pkg/compiler/userdata.go` | `pkg/profile/types.go` | p.Spec.Runtime.MountEFS check | WIRED | `if p.Spec.Runtime.MountEFS && network != nil && network.EFSFilesystemID != ""` |

---

## Requirements Coverage

Note: EFS-01 through EFS-06 are defined in ROADMAP.md and RESEARCH.md for this phase. They do NOT appear as standalone entries in REQUIREMENTS.md (the formal requirements document covers other subsystem IDs). The EFS IDs are phase-local requirement labels documented in the RESEARCH.md requirements table.

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| EFS-01 | 43-01 | km init provisions encrypted EFS with Elastic throughput, mount targets per AZ | SATISFIED | `main.tf`: `encrypted=true`, `throughput_mode="elastic"`, `count=length(var.subnet_ids)` |
| EFS-02 | 43-02 | EFS filesystem ID stored so km create can reference it | SATISFIED | `LoadEFSOutputs()` reads `efs/outputs.json`; `create.go` calls it before Compile() |
| EFS-03 | 43-01 | Profile fields `mountEFS` and `efsMountPoint` on RuntimeSpec | SATISFIED | Both fields in `pkg/profile/types.go` with yaml/json tags, tests pass |
| EFS-04 | 43-02 | EC2 userdata installs amazon-efs-utils, mounts with TLS + _netdev,nofail | SATISFIED | Template section 2.7 confirmed; `TestUserDataEFSMount` and `TestUserDataNoEFSMount` pass |
| EFS-05 | 43-01 | Security group allowing NFS port 2049 from sandbox SGs | SATISFIED | `aws_security_group.efs` with `from_port=2049`, `security_groups=[var.sandbox_sg_id]` |
| EFS-06 | 43-02 | km destroy does NOT remove EFS | SATISFIED | `destroy.go` has zero EFS references; `TestDestroyNoEFSReference` passes |

---

## Anti-Patterns Found

None detected in Phase 43 files.

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| — | — | — | — | — |

---

## Test Results

All Phase 43 tests pass:

- `TestRegionalModulesIncludesEFS` — PASS
- `TestLoadEFSOutputs_Success` — PASS
- `TestLoadEFSOutputs_NotExist` — PASS
- `TestLoadEFSOutputs_MalformedJSON` — PASS
- `TestEFSProfileFields` — PASS
- `TestEFSProfileFieldsOmitted` — PASS
- `TestUserDataEFSMount` — PASS
- `TestUserDataNoEFSMount` — PASS
- `TestUserDataEFSCustomMountPoint` — PASS
- `TestUserDataEFSMountWithNoNetwork` — PASS
- `TestDestroyNoEFSReference` — PASS
- `go build ./cmd/km/` — SUCCESS

Pre-existing failures in `internal/app/cmd` (`TestCreateDockerWritesComposeFile`, `TestShellDockerContainerName`, `TestShellDockerNoRootFlag`, `TestUnlockCmd_RequiresStateBucket`, `TestLockCmd_RequiresStateBucket`, `TestListCmd_EmptyStateBucketError`, `TestStatusCmd_EmptyStateBucketError`) are confirmed pre-existing from before Phase 43 commits — `shell_docker_test.go` existed at the parent of the first Phase 43 commit and these tests fail on the same assertions. Not introduced by this phase.

---

## Human Verification Required

The following items cannot be verified programmatically:

### 1. EFS Mount at EC2 Boot

**Test:** Provision an EC2 sandbox with `mountEFS: true` (after `km init` creates EFS in the region). SSH into the instance once running.
**Expected:** `/shared` (or configured `efsMountPoint`) exists and is mounted; `df -h` shows the EFS filesystem; files written on sandbox A are visible on sandbox B.
**Why human:** Requires live AWS infrastructure — EFS filesystem, EC2 instance boot, and NFS connectivity between the mount target SG and the sandbox instance SG cannot be exercised in unit tests.

### 2. Cross-Sandbox Artifact Sharing

**Test:** Create two sandboxes in the same region with `mountEFS: true`. Write a file from sandbox A at `/shared/test.txt`, read it from sandbox B.
**Expected:** Both sandboxes share the same EFS filesystem; the file is visible across sandboxes.
**Why human:** Requires two live EC2 instances and a provisioned EFS filesystem with mount targets.

### 3. Mount Failure Graceful Handling

**Test:** Provision a sandbox with `mountEFS: true` but intentionally block NFS (e.g., modify the SG rule). Observe boot behavior.
**Expected:** Instance boots and reaches a usable state despite EFS mount failure; `/var/log/km-bootstrap.log` shows the WARNING message.
**Why human:** Requires live infra and intentional SG misconfiguration.

---

## Summary

Phase 43 goal is fully achieved. All 12 observable truths are verified against the codebase. The complete data path from `km init` (EFS provisioning) through `LoadEFSOutputs` → `NetworkConfig.EFSFilesystemID` → `userdata.go` template section 2.7 (install + fstab + mount + verify) is wired and tested. All 6 EFS requirement IDs are satisfied. The binary builds cleanly. No blockers or anti-patterns were found.

The only gaps are items requiring live AWS infrastructure, which are explicitly flagged for human verification above.

---

_Verified: 2026-04-03T02:14:25Z_
_Verifier: Claude (gsd-verifier)_
