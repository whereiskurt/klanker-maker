---
phase: 33-ec2-storage-and-ami
plan: 03
subsystem: infra
tags: [terraform, ec2, ebs, user-data, compiler, hcl, additional-volume, auto-mount]

# Dependency graph
requires:
  - phase: 33-ec2-storage-and-ami-plan-02
    provides: ec2HCLParams with RootVolumeSizeGB/HibernationEnabled/AMISlug, Terraform module with root_block_device and ami_slug variable

provides:
  - aws_ebs_volume.additional and aws_volume_attachment.additional Terraform resources (count conditional on additional_volume_size_gb > 0)
  - additional_volume_size_gb and additional_volume_encrypted variables in ec2spot/variables.tf
  - ec2HCLParams.AdditionalVolumeSizeGB, AdditionalVolumeEncrypted, AdditionalVolumeMountPoint fields
  - ec2ServiceHCLTemplate emitting additional_volume_size_gb and additional_volume_encrypted in module_inputs
  - userDataTemplate section 2.6: conditional EBS device probe loop, idempotent mkfs.ext4, fstab UUID mount, chown
  - userDataParams.AdditionalVolumeMountPoint field populated from profile.Spec.Runtime.AdditionalVolume

affects: [ec2spot-terraform-module, compiler-hcl, compiler-userdata, phase-33-storage]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Additional EBS volume lifecycle decoupled from instance via aws_ebs_volume + aws_volume_attachment (not ebs_block_device)"
    - "Device probe loop with root-device guard: /dev/xvdf (AL2023 udev symlink), /dev/sdf, /dev/nvme1n1, /dev/nvme2n1"
    - "Idempotent mkfs: blkid check before mkfs.ext4 -F to prevent data loss on redeploy"
    - "fstab UUID entry with nofail option for persistence across reboots without blocking boot if detached"

key-files:
  created: []
  modified:
    - infra/modules/ec2spot/v1.0.0/variables.tf
    - infra/modules/ec2spot/v1.0.0/main.tf
    - pkg/compiler/service_hcl.go
    - pkg/compiler/ec2_storage_test.go
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go

key-decisions:
  - "Used aws_volume_attachment (not ebs_block_device inside aws_instance) for additional volume: survives instance replacement"
  - "Volume attachment instance_id conditional: length(local.ec2spot_map) > 0 to select spot vs on-demand instance key"
  - "Device probe order: /dev/xvdf first (AL2023 udev symlink from /dev/sdf), then /dev/sdf, then NVMe device names for Ubuntu"
  - "Root-device guard in probe loop: checks lsblk PKNAME against root mount to avoid targeting root volume on Nitro"
  - "AdditionalVolumeMountPoint placed in userDataParams (not additional volume size) since user-data only needs mount point"

patterns-established:
  - "Additional EBS volume pattern: aws_ebs_volume + aws_volume_attachment with count conditional on size_gb > 0"
  - "User-data EBS mount: probe loop 30 iterations x 2s = 60s max wait, then warning if not found"

requirements-completed: [P33-02, P33-08]

# Metrics
duration: 4min
completed: 2026-04-03
---

# Phase 33 Plan 03: Additional EBS Volume - Terraform Resources, Compiler HCL, and User-Data Auto-Mount Summary

**Additional EBS volume support: aws_ebs_volume + aws_volume_attachment Terraform resources, compiler HCL emission, and user-data idempotent mkfs/fstab auto-mount section with 60s device probe loop**

## Performance

- **Duration:** 4 min
- **Started:** 2026-04-03T00:12:42Z
- **Completed:** 2026-04-03T00:16:10Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments

- Terraform module now creates `aws_ebs_volume.additional` and `aws_volume_attachment.additional` with conditional count, `/dev/sdf` device name, `force_detach = true`, and sandbox tags
- Compiler HCL correctly emits `additional_volume_size_gb` and `additional_volume_encrypted` in module_inputs (0/false when no additional volume)
- User-data template section 2.6 conditionally probes AL2023 udev symlinks and NVMe device names, formats with idempotent mkfs.ext4, adds fstab UUID entry with nofail, and chowns to sandbox user

## Task Commits

Each task was committed atomically:

1. **Task 1: Terraform resources and compiler HCL for additional EBS volume** - `43cd549` (feat)
2. **Task 2: User-data auto-mount section for additional EBS volume** - `bb36c79` (feat)

**Plan metadata:** (docs commit below)

_Note: Both tasks used TDD (RED then GREEN)_

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/infra/modules/ec2spot/v1.0.0/variables.tf` - Added additional_volume_size_gb and additional_volume_encrypted variables
- `/Users/khundeck/working/klankrmkr/infra/modules/ec2spot/v1.0.0/main.tf` - Added aws_ebs_volume.additional and aws_volume_attachment.additional resources
- `/Users/khundeck/working/klankrmkr/pkg/compiler/service_hcl.go` - Added 3 fields to ec2HCLParams; updated ec2ServiceHCLTemplate; populated params in generateEC2ServiceHCL
- `/Users/khundeck/working/klankrmkr/pkg/compiler/ec2_storage_test.go` - Added TestAdditionalVolumeInHCL and TestAdditionalVolumeAbsentInHCL
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go` - Added AdditionalVolumeMountPoint to userDataParams; added section 2.6 to template; populated param in generateUserData
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata_test.go` - Added 4 TDD tests for additional volume user-data section

## Decisions Made

- Used `aws_volume_attachment` (not `ebs_block_device` inside `aws_instance`) for additional volume — survives instance replacement, per RESEARCH.md recommendation
- Instance ID conditional in `aws_volume_attachment`: `length(local.ec2spot_map) > 0` selects spot vs on-demand map, then `keys(...)[0]` picks the instance key — matches the pattern used in existing `aws_ec2_tag` resources
- Device probe order in user-data: `/dev/xvdf` first (AL2023 udev auto-creates this symlink from `/dev/sdf`), then `/dev/sdf`, then `/dev/nvme1n1`, `/dev/nvme2n1` for Ubuntu where udev symlinks may not be created
- Root-device guard using `lsblk -no PKNAME` prevents accidentally targeting root volume on Nitro instances where NVMe index 0 could be root

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None - all tests passed after first implementation attempt.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Phase 33 storage customization is complete: root volume sizing (Plan 02) + additional EBS volume (Plan 03) + hibernation (Plan 02) + AMI selection (Plan 02) all implemented
- Additional volume feature is end-to-end: profile schema (Plan 01) → compiler validation (Plan 02) → HCL emission (Plan 03) → Terraform resources (Plan 03) → user-data auto-mount (Plan 03)
- Ready for Phase 34 or any phase that builds on additional EBS volume support

---
*Phase: 33-ec2-storage-and-ami*
*Completed: 2026-04-03*

## Self-Check: PASSED

- main.tf: FOUND
- variables.tf: FOUND
- service_hcl.go: FOUND
- userdata.go: FOUND
- Commit 43cd549: FOUND
- Commit bb36c79: FOUND
