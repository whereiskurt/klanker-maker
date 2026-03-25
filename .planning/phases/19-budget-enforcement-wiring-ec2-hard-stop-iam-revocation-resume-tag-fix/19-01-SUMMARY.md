---
phase: 19-budget-enforcement-wiring-ec2-hard-stop-iam-revocation-resume-tag-fix
plan: 01
subsystem: infra
tags: [terragrunt, budget-enforcer, ec2spot, lambda, dependency-block, iam]

# Dependency graph
requires:
  - phase: 06-budget-enforcement-platform-configuration
    provides: budget-enforcer Lambda module and HCL template compiler
  - phase: 03-sidecar-enforcement-lifecycle-management
    provides: ec2spot Terraform module with IAM role resources
provides:
  - "budget-enforcer HCL template with dependency block reading ec2spot outputs"
  - "ec2spot module iam_role_arn output for dependency consumption"
  - "instance_id and role_arn wired into budget-enforcer inputs at apply time"
affects:
  - budget-enforcer Lambda (receives real instance_id and role_arn via EventBridge payload)
  - EC2 hard-stop and IAM revocation flows (now have required values at runtime)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Terragrunt dependency block with mock_outputs for first-apply safety"
    - "try(values(map)[0].field, '') pattern for empty-map EC2 instance guard"

key-files:
  created: []
  modified:
    - pkg/compiler/budget_enforcer_hcl.go
    - pkg/compiler/budget_enforcer_hcl_test.go
    - infra/modules/ec2spot/v1.0.0/outputs.tf

key-decisions:
  - "Use try(values(dependency.sandbox.outputs.ec2spot_instances)[0].instance_id, '') to handle ECS sandboxes (empty ec2spot_instances map) and mock_outputs during first apply"
  - "mock_outputs_allowed_on_destroy = true prevents destroy failures when sandbox module is already gone"

patterns-established:
  - "Pattern: budget-enforcer wires real runtime values from sibling sandbox module via Terragrunt dependency block, not service.hcl"

requirements-completed: [BUDG-07]

# Metrics
duration: 1min
completed: 2026-03-25
---

# Phase 19 Plan 01: Budget-enforcer dependency block wiring Summary

**Budget-enforcer HCL template now reads real EC2 instance_id and IAM role_arn from ec2spot module at apply time via Terragrunt dependency block, fixing silent skip of StopInstances and IAM revocation**

## Performance

- **Duration:** 1 min
- **Started:** 2026-03-25T02:36:12Z
- **Completed:** 2026-03-25T02:37:23Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

- Added `output "iam_role_arn"` to ec2spot module using `try(aws_iam_role.ec2spot_ssm[0].arn, "")` guard pattern matching existing outputs
- Added `dependency "sandbox"` block with `mock_outputs` for first-apply safety and destroy tolerance
- Wired `instance_id` and `role_arn` inputs from `dependency.sandbox.outputs` into budget-enforcer inputs merge block
- All 57 compiler tests pass with zero regressions

## Task Commits

Each task was committed atomically:

1. **Task 1: Add iam_role_arn output to ec2spot and update compiler tests (RED)** - `9882ee4` (test)
2. **Task 2: Add dependency block to budget-enforcer HCL template (GREEN)** - `7b416c4` (feat)

_Note: TDD tasks have two commits (test RED → feat GREEN)_

## Files Created/Modified

- `infra/modules/ec2spot/v1.0.0/outputs.tf` - Added `iam_role_arn` output with try() guard
- `pkg/compiler/budget_enforcer_hcl.go` - Added dependency "sandbox" block and wired instance_id/role_arn inputs
- `pkg/compiler/budget_enforcer_hcl_test.go` - Added dependency block assertions to EC2 and ECS test cases

## Decisions Made

- Used `try(values(...)[0].instance_id, "")` for instance_id extraction: handles ECS sandboxes (empty ec2spot_instances map gracefully) and mock_outputs during first apply
- Used `mock_outputs_allowed_on_destroy = true` to prevent destroy failures when the sandbox module is already destroyed
- Did not change Go function signatures, struct, or test helpers — purely template string edit

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- BUDG-07 root cause resolved: budget-enforcer EventBridge payload now carries real EC2 instance_id and IAM role_arn
- EC2 StopInstances and IAM policy revocation in budget-enforcer Lambda will receive non-empty values at runtime
- Remaining Phase 19 plans can proceed (EC2 hard-stop, IAM revocation, resume tag fix)

---
*Phase: 19-budget-enforcement-wiring-ec2-hard-stop-iam-revocation-resume-tag-fix*
*Completed: 2026-03-25*
