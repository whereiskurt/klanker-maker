---
phase: 80-km-cluster-cross-account-irsa-for-k8s-integrations
plan: 02
subsystem: infra
tags: [terraform, terragrunt, iam, iam-policy, module-extraction, moved-blocks]

requires:
  - phase: 80-km-cluster-cross-account-irsa-for-k8s-integrations
    provides: "80-01 test scaffold for cluster-irsa; confirms module interfaces"

provides:
  - "km-operator-policy/v1.0.0 shared Terraform module with 14 aws_iam_role_policy resources"
  - "create-handler/v1.0.0 refactored to consume shared module via module.km_operator_policy"
  - "14 moved {} blocks in create-handler mapping old addresses to module addresses"
  - "Zero-net-diff verified via terragrunt plan -detailed-exitcode (exit 0, 14 address-only moves)"
  - "Terragrunt // double-slash source pattern established for cross-module references"

affects:
  - 80-03-cluster-irsa
  - any future module that references km-operator-policy

tech-stack:
  added: []
  patterns:
    - "Terraform module extraction with moved {} blocks for zero-net-diff IAM refactor"
    - "Terragrunt // double-slash source notation to include parent directory in cache (enables cross-module local references)"

key-files:
  created:
    - infra/modules/km-operator-policy/v1.0.0/main.tf
    - infra/modules/km-operator-policy/v1.0.0/variables.tf
    - infra/modules/km-operator-policy/v1.0.0/outputs.tf
  modified:
    - infra/modules/create-handler/v1.0.0/main.tf
    - infra/live/use1/create-handler/terragrunt.hcl

key-decisions:
  - "cloudwatch_logs policy intentionally NOT extracted — stays inline in create-handler (Lambda-specific log group patterns not needed by IRSA role)"
  - "Terragrunt // double-slash source path used in live/use1/create-handler/terragrunt.hcl so Terragrunt copies infra/modules/ into cache, making ../../km-operator-policy/v1.0.0 resolvable"
  - "km-operator-policy module exposes 8 variables: role_id, resource_prefix, artifact_bucket_arn, state_bucket, dynamodb_table_name, dynamodb_budget_table_arn, sandbox_table_name, identities_table_name"

patterns-established:
  - "Cross-module Terraform reference within this repo requires // in the Terragrunt source path — NOT bare path to the child module. Pattern: source = ${local.repo_root}/infra/modules//child-module/v1.0.0"

requirements-completed: [operator-feature-80]

duration: 20min (continuation agent; tasks 1-2 from prior session, task 3 fix in this session)
completed: 2026-05-11
---

# Phase 80 Plan 02: km-operator-policy Shared Module Extraction Summary

**14 inline IAM policies extracted from create-handler into km-operator-policy/v1.0.0 shared module; zero-net-diff verified (terragrunt plan exit 0, 14 address-only moves, 0 destroy+create)**

## Performance

- **Duration:** ~20 min total (multi-session: tasks 1-2 in prior session, task 3 fix in continuation session)
- **Started:** 2026-05-11T19:33:00Z (tasks 1-2)
- **Completed:** 2026-05-11T19:51:10Z (gate passed)
- **Tasks:** 3 (including continuation task 3)
- **Files modified:** 5

## Accomplishments

- New shared module `infra/modules/km-operator-policy/v1.0.0/` with 14 `aws_iam_role_policy` resources and 8 input variables
- `create-handler/v1.0.0/main.tf` refactored: 14 inline policy blocks replaced with single `module "km_operator_policy"` call + 14 `moved {}` blocks
- Root cause of Terragrunt cache path resolution identified and fixed via `//` double-slash source notation
- `terragrunt plan -detailed-exitcode` exits 0 with 14 address-only moves and 0 IAM policy destroy+create operations

## Task Commits

1. **Task 1: Create km-operator-policy/v1.0.0 module** - `be10872` (feat)
2. **Task 2: Refactor create-handler to consume shared module** - `479dabe` (feat)
3. **Task 3: Fix module path resolution + pass zero-net-diff gate** - `fe17322` (task)

## Module Variable Interface (consumed by Plan 80-03)

The `km-operator-policy/v1.0.0` module exposes exactly these 8 input variables (Plan 80-03's cluster-irsa module MUST use these same names):

```hcl
variable "role_id"                   { type = string }
variable "resource_prefix"           { type = string }
variable "artifact_bucket_arn"       { type = string }
variable "state_bucket"              { type = string }
variable "dynamodb_table_name"       { type = string }
variable "dynamodb_budget_table_arn" { type = string }
variable "sandbox_table_name"        { type = string }
variable "identities_table_name"     { type = string }
```

No outputs — policies are attached to `var.role_id` directly.

## cloudwatch_logs Intentional Non-extraction

`aws_iam_role_policy.cloudwatch_logs` remains inline in `create-handler/v1.0.0/main.tf`. This policy references `/aws/lambda/${var.resource_prefix}-*` log group patterns that are Lambda-specific. The cluster IRSA role (Plan 80-03) does not run Lambda functions and does not need these permissions. This is intentional divergence from "same surface"; the policy table in RESEARCH.md Open Question 1 deliberately excludes `cloudwatch_logs` from the 14-policy extraction list.

## Terragrunt Plan Output (zero-diff gate audit trail)

Gate run with `AWS_PROFILE=klanker-application`, `KM_ARTIFACTS_BUCKET=km-artifacts-12345`, `KM_OPERATOR_EMAIL=whereiskurt+km@gmail.com`:

```
Plan: 0 to add, 0 to change, 0 to destroy.
```

14 address-only moves confirmed:
- `aws_iam_role_policy.dynamodb` has moved to `module.km_operator_policy.aws_iam_role_policy.dynamodb`
- `aws_iam_role_policy.dynamodb_sandboxes` has moved to `module.km_operator_policy.aws_iam_role_policy.dynamodb_sandboxes`
- `aws_iam_role_policy.ec2_provisioning` has moved to `module.km_operator_policy.aws_iam_role_policy.ec2_provisioning`
- `aws_iam_role_policy.ecs_provisioning` has moved to `module.km_operator_policy.aws_iam_role_policy.ecs_provisioning`
- `aws_iam_role_policy.iam_sandbox` has moved to `module.km_operator_policy.aws_iam_role_policy.iam_sandbox`
- `aws_iam_role_policy.kms` has moved to `module.km_operator_policy.aws_iam_role_policy.kms`
- `aws_iam_role_policy.lambda_budget` has moved to `module.km_operator_policy.aws_iam_role_policy.lambda_budget`
- `aws_iam_role_policy.s3_artifacts` has moved to `module.km_operator_policy.aws_iam_role_policy.s3_artifacts`
- `aws_iam_role_policy.scheduler` has moved to `module.km_operator_policy.aws_iam_role_policy.scheduler`
- `aws_iam_role_policy.ses_send` has moved to `module.km_operator_policy.aws_iam_role_policy.ses_send`
- `aws_iam_role_policy.sqs_slack_inbound` has moved to `module.km_operator_policy.aws_iam_role_policy.sqs_slack_inbound`
- `aws_iam_role_policy.ssm` has moved to `module.km_operator_policy.aws_iam_role_policy.ssm`
- `aws_iam_role_policy.ssm_send_command` has moved to `module.km_operator_policy.aws_iam_role_policy.ssm_send_command`
- `aws_iam_role_policy.terraform_state` has moved to `module.km_operator_policy.aws_iam_role_policy.terraform_state`

No `~ aws_iam_role.create_handler` modifications. No destroy+create pairs. Gate PASSED.

## Files Created/Modified

- `infra/modules/km-operator-policy/v1.0.0/main.tf` — 14 aws_iam_role_policy resources with `role = var.role_id`
- `infra/modules/km-operator-policy/v1.0.0/variables.tf` — 8 input variables
- `infra/modules/km-operator-policy/v1.0.0/outputs.tf` — no outputs (empty, policies attach to role_id directly)
- `infra/modules/create-handler/v1.0.0/main.tf` — replaced 14 inline policies with module call + 14 moved blocks
- `infra/live/use1/create-handler/terragrunt.hcl` — updated source from `.../create-handler/v1.0.0` to `.../infra/modules//create-handler/v1.0.0`

## Decisions Made

**Terragrunt // double-slash path:** The original source `${local.repo_root}/infra/modules/create-handler/v1.0.0` caused Terragrunt to copy only the `create-handler/v1.0.0` directory into its cache. The relative path `../../km-operator-policy/v1.0.0` in `main.tf` then failed to resolve during `terraform init` (no parent `infra/modules/` directory in cache). Changed to `${local.repo_root}/infra/modules//create-handler/v1.0.0` — the `//` tells Terragrunt to use `infra/modules/` as the root of the copied tree, placing `create-handler/v1.0.0` at `create-handler/v1.0.0` within the cache alongside `km-operator-policy/v1.0.0`. This is a first use of cross-module local references in this codebase.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Terragrunt cache path resolution for cross-module reference**
- **Found during:** Task 3 (zero-net-diff gate)
- **Issue:** Terragrunt copies only the specified module directory into cache, so `../../km-operator-policy/v1.0.0` relative path from `main.tf` could not be resolved. Error: "Unable to evaluate directory symlink: lstat ../../km-operator-policy: no such file or directory"
- **Fix:** Changed `terragrunt.hcl` source from `${local.repo_root}/infra/modules/create-handler/v1.0.0` to `${local.repo_root}/infra/modules//create-handler/v1.0.0`. The `//` double-slash causes Terragrunt to copy `infra/modules/` into the cache, making sibling modules accessible.
- **Files modified:** `infra/live/use1/create-handler/terragrunt.hcl`
- **Verification:** `terragrunt plan -detailed-exitcode` exits 0, 14 address-only moves
- **Committed in:** `fe17322`

---

**Total deviations:** 1 auto-fixed (Rule 3 - blocking)
**Impact on plan:** Necessary fix for the novel cross-module reference pattern; no scope creep.

## Issues Encountered

The `//` double-slash Terragrunt path notation was needed to support a cross-module local reference — the first such reference in this codebase. All other modules are standalone. The scp module's terragrunt.hcl already uses this pattern (`${repo_root}/infra/modules/scp//v1.0.0`), which served as the reference for the correct approach.

Note: The initial plan run (without KM env vars set) showed a spurious `s3_artifacts` policy diff because `artifact_bucket_arn` was evaluated as empty ARN. Re-running with `KM_ARTIFACTS_BUCKET` set confirmed the gate passes cleanly.

## Next Phase Readiness

- Plan 80-03 (cluster-irsa module) is UNBLOCKED
- `km-operator-policy/v1.0.0/` is ready to be consumed as `module "km_operator_policy"` in cluster-irsa with the same 8-variable interface
- The `//` double-slash pattern must be used in `infra/live/{region}/cluster-{name}/terragrunt.hcl` as well, since cluster-irsa will also reference km-operator-policy as a local module

---
*Phase: 80-km-cluster-cross-account-irsa-for-k8s-integrations*
*Completed: 2026-05-11*

## Self-Check: PASSED

Files verified:
- `infra/modules/km-operator-policy/v1.0.0/main.tf` — FOUND
- `infra/modules/km-operator-policy/v1.0.0/variables.tf` — FOUND
- `infra/modules/km-operator-policy/v1.0.0/outputs.tf` — FOUND
- `infra/modules/create-handler/v1.0.0/main.tf` — FOUND (module call + 14 moved blocks)
- `infra/live/use1/create-handler/terragrunt.hcl` — FOUND (// source)

Commits verified:
- `be10872` — FOUND (km-operator-policy module creation)
- `479dabe` — FOUND (create-handler refactor)
- `fe17322` — FOUND (terragrunt.hcl fix)
