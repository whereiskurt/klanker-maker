---
phase: 43-regional-efs-shared-filesystem-cross-sandbox-persistent-storage-via-km-init-provisioning-and-profile-driven-mount
plan: "01"
subsystem: efs-infrastructure
tags: [efs, terraform, terragrunt, profile, km-init]
dependency_graph:
  requires: []
  provides: [efs-module, efs-live-config, profile-efs-fields, km-init-efs-registration]
  affects: [pkg/compiler, infra/live/use1/efs]
tech_stack:
  added: [aws_efs_file_system, aws_efs_mount_target, aws_security_group (efs)]
  patterns: [terragrunt-network-outputs-pattern, tdd-red-green, output-capture-pattern]
key_files:
  created:
    - infra/modules/efs/v1.0.0/main.tf
    - infra/modules/efs/v1.0.0/variables.tf
    - infra/modules/efs/v1.0.0/outputs.tf
    - infra/live/use1/efs/terragrunt.hcl
    - pkg/profile/types_efs_test.go
  modified:
    - pkg/profile/types.go
    - internal/app/cmd/init.go
    - internal/app/cmd/init_test.go
decisions:
  - "NFS ingress restricted to sandbox_sg_id security group reference (not CIDR) to prevent lateral movement from non-sandbox hosts"
  - "EFS entry placed immediately after network in regionalModules() since terragrunt.hcl reads network/outputs.json at parse time"
  - "Exported RegionalModules()/RegionalModule type added as thin wrapper over internal regionalModules() to enable external test introspection without exporting internal struct fields"
  - "EFSMountPoint defaults to empty string (Go zero value); compiler/userdata (Plan 02) will apply /shared default when field is empty"
metrics:
  duration: 160s
  completed_date: "2026-04-03"
  tasks_completed: 2
  files_changed: 8
requirements_satisfied: [EFS-01, EFS-03, EFS-05]
---

# Phase 43 Plan 01: EFS Terraform Module, Profile Fields, and km init Registration Summary

**One-liner:** EFS Terraform module (encrypted, elastic throughput) with mount targets per AZ, SG-scoped NFS access, and `MountEFS`/`EFSMountPoint` profile fields wired into km init output capture.

## What Was Built

### Task 1: EFS Terraform Module and Terragrunt Config

**infra/modules/efs/v1.0.0/main.tf** — Three resources:
- `aws_efs_file_system.shared`: encrypted=true, throughput_mode=elastic, performance_mode=generalPurpose, creation_token keyed on region_label
- `aws_security_group.efs`: ingress on TCP 2049 from `var.sandbox_sg_id` (SG-to-SG reference, not CIDR), standard egress allow-all
- `aws_efs_mount_target.shared`: count=length(subnet_ids), one per AZ subnet, attached to efs SG

**infra/modules/efs/v1.0.0/variables.tf** — Five variables: vpc_id, subnet_ids (list), sandbox_sg_id, km_label, region_label

**infra/modules/efs/v1.0.0/outputs.tf** — Two outputs: filesystem_id, security_group_id

**infra/live/use1/efs/terragrunt.hcl** — Mirrors network/terragrunt.hcl pattern exactly:
- Reads repo_root, site_vars, region_config locals
- Reads `network/outputs.json` via jsondecode(file()) for vpc_id, public_subnets, sandbox_mgmt_sg_id
- Remote state key: `{tf_state_prefix}/{region_label}/efs/terraform.tfstate`

### Task 2: Profile Fields, km init Registration, Output Capture

**pkg/profile/types.go** — Added two fields to RuntimeSpec after AMI:
- `MountEFS bool` with `yaml:"mountEFS,omitempty" json:"mountEFS,omitempty"`
- `EFSMountPoint string` with `yaml:"efsMountPoint,omitempty" json:"efsMountPoint,omitempty"`

**internal/app/cmd/init.go** — Three changes:
1. Added `efs` entry to `regionalModules()` immediately after `network` (required for ordering)
2. Added EFS output capture block after apply: writes `efs/outputs.json`, prints filesystem ID
3. Added exported `RegionalModule` type and `RegionalModules()` function for test introspection

**Tests (TDD):**
- `TestRegionalModulesIncludesEFS`: verifies efs is in regionalModules() and appears after network
- `TestEFSProfileFields`: YAML round-trip with mountEFS=true and efsMountPoint=/data
- `TestEFSProfileFieldsOmitted`: verifies zero values when EFS fields absent

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check

Verifying artifacts exist and commits are present.
