---
phase: 33-ec2-storage-and-ami
plan: 02
subsystem: infra
tags: [terraform, ec2, ebs, ami, hibernation, hcl-compiler, go]

requires:
  - phase: 33-01
    provides: RuntimeSpec fields (RootVolumeSize, Hibernation, AMI, AdditionalVolume) in pkg/profile/types.go and validateEC2StorageFields in compiler

provides:
  - ec2HCLParams struct with RootVolumeSizeGB, HibernationEnabled, AMISlug fields
  - ec2ServiceHCLTemplate emitting root_volume_size_gb, hibernation_enabled, ami_slug in module_inputs
  - generateEC2ServiceHCL populates new params from profile, defaulting AMI to amazon-linux-2023
  - Terraform variables: root_volume_size_gb, hibernation_enabled, ami_slug with correct types/defaults
  - AMI locals map (ami_filters) keyed by resolved_ami_slug for AL2023, Ubuntu 24.04, Ubuntu 22.04
  - dynamic root_block_device on aws_instance.ec2_ondemand (encrypted when hibernation, gp3)
  - hibernation = var.hibernation_enabled attribute on aws_instance.ec2_ondemand
  - dynamic root_block_device on aws_spot_instance_request.ec2spot (sizing only, no encryption)

affects: [33-03, infra/modules/ec2spot]

tech-stack:
  added: []
  patterns:
    - "AMI resolution via locals map keyed by ami_slug avoids plan-time errors from for_each on data sources with variable keys"
    - "dynamic root_block_device with for_each conditional ([1] or []) is idiomatic Terraform for optional nested blocks"
    - "TDD RED-GREEN commit pair: failing test committed first, then implementation brings tests to green"

key-files:
  created:
    - pkg/compiler/ec2_storage_test.go (extended with 5 new HCL output tests)
  modified:
    - pkg/compiler/service_hcl.go
    - infra/modules/ec2spot/v1.0.0/variables.tf
    - infra/modules/ec2spot/v1.0.0/main.tf

key-decisions:
  - "AMI defaults to amazon-linux-2023 when profile ami field is empty (compiler responsibility, not schema)"
  - "Spot instances get root_block_device for sizing only - no encrypted, no hibernation (km pause explicitly rejects spot)"
  - "On-demand: encrypted = var.hibernation_enabled in root_block_device (encrypted iff hibernation needed, not always)"
  - "locals ami_filters map approach chosen over multiple data.aws_ami blocks to avoid plan-time variable key errors"

patterns-established:
  - "Pattern: AMI resolution via locals map (ami_filters[resolved_ami_slug]) - use this approach for all future AMI variants"
  - "Pattern: dynamic root_block_device with for_each = condition ? [1] : [] for optional EBS configuration"

requirements-completed: [P33-01, P33-03, P33-05, P33-06]

duration: 3min
completed: 2026-04-03
---

# Phase 33 Plan 02: Compiler HCL template + Terraform module for EC2 storage and AMI selection

**Compiler emits root_volume_size_gb, hibernation_enabled, ami_slug into module_inputs; Terraform module resolves AMI via locals map and configures dynamic root_block_device with hibernation support on on-demand instances.**

## Performance

- **Duration:** 3 min
- **Started:** 2026-04-03T00:05:58Z
- **Completed:** 2026-04-03T00:08:38Z
- **Tasks:** 1 (TDD: RED commit + GREEN commit)
- **Files modified:** 4

## Accomplishments
- Extended ec2HCLParams struct and ec2ServiceHCLTemplate to emit three new Terraform module inputs
- Replaced hardcoded `data "aws_ami"` block with a locals map supporting amazon-linux-2023, ubuntu-24.04, ubuntu-22.04
- Added dynamic root_block_device on both instance types (encrypted + hibernation on on-demand, sizing-only on spot)
- All 8 compiler tests pass (5 new + 3 pre-existing)

## Task Commits

Each task was committed atomically (TDD pattern):

1. **RED - Failing tests** - `b0907d6` (test)
2. **GREEN - Implementation** - `6828800` (feat)

## Files Created/Modified
- `pkg/compiler/ec2_storage_test.go` - Added 5 new HCL output tests (TestRootVolumeSizeInHCL, TestRootVolumeSizeZeroInHCL, TestHibernationEnabledInHCL, TestAMISlugExplicitInHCL, TestAMISlugDefaultInHCL)
- `pkg/compiler/service_hcl.go` - Added RootVolumeSizeGB/HibernationEnabled/AMISlug to ec2HCLParams, extended template, populated in generateEC2ServiceHCL
- `infra/modules/ec2spot/v1.0.0/variables.tf` - Added root_volume_size_gb, hibernation_enabled, ami_slug variables
- `infra/modules/ec2spot/v1.0.0/main.tf` - Added ami_filters locals map, updated data.aws_ami, added dynamic root_block_device and hibernation to both instance types

## Decisions Made
- AMI defaults to "amazon-linux-2023" in the Go compiler when profile `ami` field is empty — keeps profile simple and avoids requiring users to specify the common case
- Spot instances only get sizing in root_block_device (no encryption, no hibernation) because `km pause` already rejects spot instances with a clear error
- `encrypted = var.hibernation_enabled` — root volume is only encrypted when hibernation is enabled, not always, to avoid surprising users who don't need hibernation
- Used `locals ami_filters` map pattern (as documented in RESEARCH.md Pattern 4) instead of multiple `data "aws_ami"` blocks to avoid Terraform plan-time errors

## Deviations from Plan
None - plan executed exactly as written.

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Plan 03 (additional EBS volume + auto-mount user-data) can now proceed; it builds on the same variable pattern established here
- Terraform module is ready to accept root_volume_size_gb, hibernation_enabled, ami_slug from generated service.hcl

---
*Phase: 33-ec2-storage-and-ami*
*Completed: 2026-04-03*
