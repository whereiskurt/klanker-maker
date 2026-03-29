---
phase: quick
plan: 3
subsystem: compiler
tags: [budget-enforcer, terragrunt, hcl, dependency-block]
dependency_graph:
  requires: []
  provides: [budget-enforcer-instance-id-wiring]
  affects: [pkg/compiler/budget_enforcer_hcl.go, pkg/compiler/service_hcl.go]
tech_stack:
  added: []
  patterns: [terragrunt-dependency-block, mock-outputs]
key_files:
  created: []
  modified:
    - pkg/compiler/budget_enforcer_hcl.go
    - pkg/compiler/budget_enforcer_hcl_test.go
    - pkg/compiler/service_hcl.go
decisions:
  - Wire instance_id via try(values(dependency.sandbox.outputs.ec2spot_instances)[0].instance_id, "") matching working reference in sb-e38fb038
  - Use mock_outputs_allowed_on_destroy = true to allow destroy without live module outputs
  - Remove empty role_arn/instance_id placeholders from EC2 service.hcl to avoid confusion
metrics:
  duration: 8m
  completed: 2026-03-28
  tasks_completed: 2
  files_modified: 3
---

# Quick Task 3: Fix Budget Enforcer Instance ID Summary

**One-liner:** Budget enforcer terragrunt.hcl now wires instance_id and role_arn from ec2spot module outputs via Terragrunt dependency block, matching the working sb-e38fb038 reference pattern.

## What Was Done

### Task 1: Add dependency block and output-based wiring to budget enforcer HCL template

Added a `dependency "sandbox"` block to `budgetEnforcerHCLTemplate` in `budget_enforcer_hcl.go` pointing to `${get_terragrunt_dir()}/..` (the parent sandbox module). The dependency block includes `mock_outputs` for `ec2spot_instances`, `iam_instance_profile_name`, and `iam_role_arn`, plus `mock_outputs_allowed_on_destroy = true`.

Replaced the static IAM role ARN construction:
```hcl
role_arn = "arn:aws:iam::....:role/km-ec2spot-ssm-${local.be_inputs.sandbox_id}-..."
```

With output-based wiring:
```hcl
instance_id = try(values(dependency.sandbox.outputs.ec2spot_instances)[0].instance_id, "")
role_arn    = dependency.sandbox.outputs.iam_role_arn
```

Updated `budget_enforcer_hcl_test.go` to assert presence of the dependency block and wiring patterns, and to assert absence of the old `km-ec2spot-ssm-` static ARN pattern.

### Task 2: Remove stale empty placeholders from EC2 service.hcl template

Removed the two misleading empty placeholder lines from the `budget_enforcer_inputs` block in `ec2ServiceHCLTemplate`:
```hcl
role_arn       = "" # populated at apply time: IAM role ARN from ec2spot module output
instance_id    = "" # populated at apply time: EC2 instance ID from ec2spot module output
```

These fields are now owned entirely by the dependency block in `budget_enforcer_hcl.go`. The ECS template's `task_arn`, `cluster_arn`, and `role_arn` placeholders were left as-is per the plan (ECS substrate is not the focus of this fix).

## Verification

All compiler tests pass:
```
ok  github.com/whereiskurt/klankrmkr/pkg/compiler  0.318s
```

## Deviations from Plan

None - plan executed exactly as written.

## Commits

- `c88525f` feat(quick-3): wire instance_id and role_arn via dependency block in budget enforcer HCL
- `8c6dda7` fix(quick-3): remove stale empty instance_id and role_arn placeholders from EC2 service.hcl

## Self-Check

- [x] pkg/compiler/budget_enforcer_hcl.go modified and verified
- [x] pkg/compiler/budget_enforcer_hcl_test.go modified and verified
- [x] pkg/compiler/service_hcl.go modified and verified
- [x] All compiler tests pass
- [x] Commits c88525f and 8c6dda7 exist in git log
