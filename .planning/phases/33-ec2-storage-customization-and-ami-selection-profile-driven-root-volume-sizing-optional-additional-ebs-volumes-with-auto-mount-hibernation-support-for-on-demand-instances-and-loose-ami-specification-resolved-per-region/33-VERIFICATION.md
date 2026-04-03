---
phase: 33-ec2-storage-and-ami
verified: 2026-04-02T01:00:00Z
status: passed
score: 20/20 must-haves verified
re_verification: false
---

# Phase 33: EC2 Storage Customization and AMI Selection Verification Report

**Phase Goal:** Profiles can specify root volume sizing, optional additional EBS volumes with auto-mount, hibernation for on-demand instances, and loose AMI slugs resolved per-region -- extending the EC2 provisioning pipeline from schema through compiler to Terraform
**Verified:** 2026-04-02T01:00:00Z
**Status:** passed
**Re-verification:** No -- initial verification

## Goal Achievement

### Observable Truths

| #  | Truth                                                                                              | Status     | Evidence                                                                                 |
|----|---------------------------------------------------------------------------------------------------|------------|------------------------------------------------------------------------------------------|
| 1  | RuntimeSpec has RootVolumeSize, AdditionalVolume, Hibernation, AMI fields                         | VERIFIED   | pkg/profile/types.go lines 148-154 -- all four fields present with omitempty             |
| 2  | AdditionalVolumeSpec type exists with Size, MountPoint, Encrypted fields                          | VERIFIED   | pkg/profile/types.go lines 128-135 -- type fully defined                                 |
| 3  | JSON schema accepts rootVolumeSize, additionalVolume, hibernation, ami under runtime               | VERIFIED   | sandbox_profile.schema.json lines 193-229 -- all four properties in runtime object       |
| 4  | JSON schema rejects rootVolumeSize < 0                                                             | VERIFIED   | schema has minimum: 0; TestSchemaRootVolumeValidation passes                             |
| 5  | Compiler rejects hibernation: true + spot: true with clear error                                  | VERIFIED   | validateEC2StorageFields() at service_hcl.go:594; TestHibernationSpotConflict passes     |
| 6  | Compiler rejects additionalVolume on ECS substrate with clear error                               | VERIFIED   | validateEC2StorageFields(); TestAdditionalVolumeECSConflict passes                       |
| 7  | Compiler rejects hibernation on ECS substrate with clear error                                    | VERIFIED   | validateEC2StorageFields(); TestHibernationECSConflict passes                            |
| 8  | Profile with rootVolumeSize: 50 generates HCL with root_volume_size_gb = 50                       | VERIFIED   | TestRootVolumeSizeInHCL passes; template line 70 confirmed                               |
| 9  | Profile with hibernation: true generates HCL with hibernation_enabled = true                      | VERIFIED   | TestHibernationEnabledInHCL passes; template line 71 confirmed                          |
| 10 | Profile with ami: ubuntu-24.04 generates HCL with ami_slug = "ubuntu-24.04"                       | VERIFIED   | TestAMISlugExplicitInHCL passes; template line 72 confirmed                             |
| 11 | Empty ami field generates HCL with ami_slug = "amazon-linux-2023"                                 | VERIFIED   | TestAMISlugDefaultInHCL passes; generateEC2ServiceHCL defaults AMI when empty           |
| 12 | Terraform module accepts root_volume_size_gb, hibernation_enabled, ami_slug variables              | VERIFIED   | variables.tf lines 89-105 -- all three variables present with correct types/defaults     |
| 13 | On-demand instance has dynamic root_block_device with encrypted=true when hibernation enabled      | VERIFIED   | main.tf lines 516-527: dynamic block for_each on size>0 or hibernation; encrypted=var.hibernation_enabled |
| 14 | Spot instance has dynamic root_block_device for sizing only (no encryption, no hibernation)        | VERIFIED   | main.tf lines 433-444: separate dynamic block, no encrypted, no hibernation attribute   |
| 15 | data.aws_ami uses locals map keyed by ami_slug for AMI filter resolution                           | VERIFIED   | main.tf lines 16-30: ami_filters locals map; lines 139-148 use ami_filters[resolved_ami_slug] |
| 16 | Profile with additionalVolume generates Terraform aws_ebs_volume and aws_volume_attachment         | VERIFIED   | main.tf lines 540-558: both resources with count conditional on additional_volume_size_gb > 0 |
| 17 | Terraform additional volume uses /dev/sdf device name and conditional count                        | VERIFIED   | main.tf line 555: device_name = "/dev/sdf"; line 554: count = var.additional_volume_size_gb > 0 ? 1 : 0 |
| 18 | Compiler HCL emits additional_volume_size_gb, additional_volume_encrypted                          | VERIFIED   | TestAdditionalVolumeInHCL passes; service_hcl.go template lines 75-76 confirmed         |
| 19 | User-data includes EBS device probe, idempotent mkfs, fstab UUID mount when additionalVolume set   | VERIFIED   | userdata.go lines 70-110: device probe loop, mkfs.ext4, /etc/fstab; all 4 TDD tests pass |
| 20 | User-data omits additional volume section when additionalVolume is nil                             | VERIFIED   | TestUserDataAdditionalVolumeAbsent passes; template gated on {{- if .AdditionalVolumeMountPoint }} |

**Score:** 20/20 truths verified

### Required Artifacts

| Artifact                                             | Expected                                            | Status     | Details                                                                              |
|------------------------------------------------------|-----------------------------------------------------|------------|--------------------------------------------------------------------------------------|
| `pkg/profile/types.go`                               | RuntimeSpec with new fields, AdditionalVolumeSpec   | VERIFIED   | Lines 128-155: AdditionalVolumeSpec + 4 RuntimeSpec fields with correct tags         |
| `pkg/profile/schemas/sandbox_profile.schema.json`    | JSON schema with new runtime fields                 | VERIFIED   | Lines 193-229: rootVolumeSize, additionalVolume, hibernation, ami all present        |
| `pkg/profile/schema_storage_test.go`                 | Schema validation tests (TestSchemaRootVolume)      | VERIFIED   | 3 test functions, 7 sub-tests; all pass                                              |
| `pkg/compiler/ec2_storage_test.go`                   | Semantic + HCL output tests (TestHibernationSpotConflict) | VERIFIED | 12 test functions covering validation, HCL output, additional volume; all pass      |
| `pkg/compiler/service_hcl.go`                        | ec2HCLParams with new fields; template with module_inputs | VERIFIED | Lines 400-406: 6 new fields; lines 70-76: 5 new template lines; validateEC2StorageFields at 592 |
| `infra/modules/ec2spot/v1.0.0/variables.tf`          | New Terraform variables                             | VERIFIED   | Lines 89-117: root_volume_size_gb, hibernation_enabled, ami_slug, additional_volume_size_gb, additional_volume_encrypted |
| `infra/modules/ec2spot/v1.0.0/main.tf`               | AMI locals map, dynamic root_block_device, aws_ebs_volume | VERIFIED | Lines 16-30: ami_filters map; lines 433-527: dynamic root_block_device on both instance types; lines 540-558: aws_ebs_volume + aws_volume_attachment |
| `pkg/compiler/userdata.go`                           | AdditionalVolumeMountPoint field + mount template   | VERIFIED   | Lines 921-923: field; lines 70-110: conditional template section 2.6                |
| `pkg/compiler/userdata_test.go`                      | 4 TDD tests for additional volume user-data         | VERIFIED   | Lines 805-880: TestUserDataAdditionalVolumeWaitMessage, MkfsAndFstab, Mkdir, Absent -- all pass |

### Key Link Verification

| From                             | To                                           | Via                                                          | Status  | Details                                                                                    |
|----------------------------------|----------------------------------------------|--------------------------------------------------------------|---------|--------------------------------------------------------------------------------------------|
| `pkg/profile/types.go`           | `sandbox_profile.schema.json`                | Go struct fields match JSON schema properties                | WIRED   | All four fields have matching schema properties with correct types                         |
| `pkg/compiler/service_hcl.go`   | `infra/modules/ec2spot/v1.0.0/variables.tf`  | HCL template emits module_inputs matching Terraform variables | WIRED   | root_volume_size_gb, hibernation_enabled, ami_slug, additional_volume_size_gb, additional_volume_encrypted all present in both |
| `infra/modules/ec2spot/v1.0.0/main.tf` | `data.aws_ami.base_ami`               | locals.ami_filters map keyed by var.ami_slug                 | WIRED   | Line 30: resolved_ami_slug; lines 144/148: ami_filters[local.resolved_ami_slug].owner/.name_pattern |
| `infra/modules/ec2spot/v1.0.0/main.tf` | `aws_ebs_volume.additional`           | count conditional on additional_volume_size_gb > 0           | WIRED   | Line 541: count = var.additional_volume_size_gb > 0 ? 1 : 0; line 554: matching count on attachment |
| `pkg/compiler/userdata.go`       | user-data script                             | Go template conditionally emits mount section                | WIRED   | Line 70: {{- if .AdditionalVolumeMountPoint }}; populated at line 1054 from AdditionalVolume.MountPoint |

### Requirements Coverage

| Requirement | Source Plan | Description (inferred)                                   | Status    | Evidence                                                    |
|-------------|-------------|----------------------------------------------------------|-----------|-------------------------------------------------------------|
| P33-01      | 33-01, 33-02 | RuntimeSpec fields and HCL emission for root volume size | SATISFIED | types.go + template line 70 + TestRootVolumeSizeInHCL       |
| P33-02      | 33-01, 33-03 | AdditionalVolumeSpec schema and user-data auto-mount     | SATISFIED | types.go + schema + userdata.go section 2.6 + 4 user-data tests |
| P33-03      | 33-02       | Terraform module AMI resolution via locals map           | SATISFIED | main.tf ami_filters locals + data.aws_ami use               |
| P33-04      | 33-01       | JSON schema validates new runtime fields                 | SATISFIED | schema.json + TestSchemaRootVolumeValidation                |
| P33-05      | 33-01, 33-02 | Compiler semantic validation: hibernation+spot, ECS conflicts | SATISFIED | validateEC2StorageFields + 3 error tests                   |
| P33-06      | 33-01, 33-02 | HCL emission for hibernation_enabled                     | SATISFIED | template line 71 + TestHibernationEnabledInHCL              |
| P33-07      | 33-01       | HCL emission for ami_slug with default fallback          | SATISFIED | template line 72 + TestAMISlugDefaultInHCL                  |
| P33-08      | 33-03       | Terraform aws_ebs_volume + aws_volume_attachment resources | SATISFIED | main.tf lines 540-558 + TestAdditionalVolumeInHCL           |

**Note on REQUIREMENTS.md mapping:** Phase 33 uses plan-internal requirement IDs (P33-xx). These are not currently entered in `.planning/REQUIREMENTS.md` traceability table (which uses global IDs like PROV-xx, NETW-xx, etc). This is consistent with how the roadmap uses phase-local identifiers -- no orphaned requirements were found because REQUIREMENTS.md has no P33 entries to cross-reference.

### Anti-Patterns Found

| File                  | Line | Pattern                    | Severity | Impact                                                     |
|-----------------------|------|----------------------------|----------|------------------------------------------------------------|
| `service_hcl.go`      | 435  | PLACEHOLDER_ECR reference  | Info     | Pre-existing pattern name for ECR registry default -- not Phase 33, not a stub |

No blockers or warnings in Phase 33 modified code. The PLACEHOLDER_ECR on line 435 and 734 is a pre-existing fallback value name (string used when KM_ACCOUNTS_APPLICATION env var is unset), not a stub implementation from this phase.

### Human Verification Required

#### 1. Actual EC2 provisioning with hibernation enabled

**Test:** Run `km create` with a profile containing `hibernation: true`, `spot: false`, instance type t3.medium. Verify EC2 instance launches with hibernation enabled.
**Expected:** Instance shows hibernation = enabled in AWS console; can be hibernate-stopped and resumed.
**Why human:** Requires live AWS account with EC2 quota; can't verify Terraform plan execution programmatically.

#### 2. AMI resolution per-region for ubuntu-24.04

**Test:** Run `km create` with `ami: ubuntu-24.04` targeting us-west-2. Verify the provisioned instance uses the canonical Ubuntu 24.04 AMI for that region.
**Expected:** `data.aws_ami.base_ami` resolves to an Ubuntu 24.04 image owned by Canonical (099720109477).
**Why human:** Requires live Terraform plan/apply against real AWS region.

#### 3. Additional EBS volume auto-mount in user-data

**Test:** Run `km create` with a profile containing `additionalVolume: {size: 50, mountPoint: /data}`. SSH or SSM into the instance after bootstrap. Verify `/data` is mounted and owned by sandbox user.
**Expected:** `df -h /data` shows a 50GB ext4 volume; `/etc/fstab` contains the UUID entry; `ls -la /` shows `/data` owned by sandbox.
**Why human:** Requires live EC2 instance with user-data execution.

### Gaps Summary

No gaps. All 20 truths verified. Phase 33 goal is fully achieved: the EC2 provisioning pipeline has been extended end-to-end from profile schema (types.go + JSON schema) through compiler validation (validateEC2StorageFields) and HCL emission (service_hcl.go template) to Terraform module variables and resources (variables.tf + main.tf), plus user-data auto-mount for the additional EBS volume (userdata.go). All 21 automated tests pass with no regressions in the full profile and compiler test suites.

---

_Verified: 2026-04-02T01:00:00Z_
_Verifier: Claude (gsd-verifier)_
