---
phase: 82-multi-instance-resource-prefix-isolation
plan: "06"
subsystem: infra
tags: [terraform, ses, receipt-rule-set, resource-prefix, multi-instance]

requires:
  - phase: 82-multi-instance-resource-prefix-isolation
    provides: "82-01 through 82-05 established resource_prefix parameterisation for DynamoDB, CloudWatch, SNS, S3/KMS, and EC2 modules"

provides:
  - "SES module accepts resource_prefix variable with default 'km'"
  - "aws_ses_receipt_rule_set.km_sandbox rule_set_name parameterised via var.resource_prefix"
  - "infra/live/use1/ses/terragrunt.hcl wired to KM_RESOURCE_PREFIX env var"

affects: [82-10, km-init, ses, email-handler]

tech-stack:
  added: []
  patterns:
    - "resource_prefix variable with default 'km' added to module — same pattern as 82-01 through 82-05"
    - "Terragrunt live file wires get_env(\"KM_RESOURCE_PREFIX\", \"km\") to inputs block"

key-files:
  created: []
  modified:
    - infra/modules/ses/v1.0.0/variables.tf
    - infra/modules/ses/v1.0.0/main.tf
    - infra/live/use1/ses/terragrunt.hcl

key-decisions:
  - "No moved{} block: for the existing 'km' install the evaluated name is identical to the old literal so Terraform sees no diff — zero downtime"
  - "Variable placed after existing variables.tf entries in alphabetical order (resource_prefix after email_create_handler_arn, route53_zone_id)"

patterns-established:
  - "SES receipt rule set name pattern: ${var.resource_prefix}-sandbox-email"

requirements-completed: []

duration: 39s
completed: "2026-05-16"
---

# Phase 82 Plan 06: SES Receipt Rule-Set Resource Prefix Isolation Summary

**SES receipt rule-set name parameterised from literal `km-sandbox-email` to `${var.resource_prefix}-sandbox-email`, enabling per-install isolation with zero Terraform diff for the existing km install**

## Performance

- **Duration:** 39s
- **Started:** 2026-05-16T13:13:24Z
- **Completed:** 2026-05-16T13:14:03Z
- **Tasks:** 1
- **Files modified:** 3

## Accomplishments

- Replaced the only hardcoded `"km-sandbox-email"` literal in `main.tf` with `"${var.resource_prefix}-sandbox-email"` — no other occurrences existed
- Added `variable "resource_prefix"` with `default = "km"` and a descriptive multi-instance comment to `variables.tf`
- Wired `resource_prefix = get_env("KM_RESOURCE_PREFIX", "km")` into the `inputs` block of `infra/live/use1/ses/terragrunt.hcl`, matching the existing site.hcl pattern

## Task Commits

Each task was committed atomically:

1. **Task 1: Add resource_prefix variable to SES module + replace literal + wire terragrunt** - `bf7abf5` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified

- `infra/modules/ses/v1.0.0/variables.tf` — appended `variable "resource_prefix"` with default `"km"`
- `infra/modules/ses/v1.0.0/main.tf` — replaced literal `"km-sandbox-email"` with `"${var.resource_prefix}-sandbox-email"` on line 62
- `infra/live/use1/ses/terragrunt.hcl` — added `resource_prefix = get_env("KM_RESOURCE_PREFIX", "km")` to inputs block

## Decisions Made

- No `moved {}` block introduced (per RESEARCH.md § Q1): for the existing `km` install `var.resource_prefix = "km"` evaluates to the identical string `"km-sandbox-email"`, so Terraform shows zero diff on `aws_ses_receipt_rule_set.km_sandbox`. For a second install with `resource_prefix = "rg"` a new rule set is created from scratch.
- The `aws_ses_active_receipt_rule_set` resource already references `aws_ses_receipt_rule_set.km_sandbox.rule_set_name` (not a literal), so it automatically inherits the parameterised value with no further edits.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required. The new `resource_prefix` input will be consumed by `km init --dry-run=false` in Plan 82-10's Wave 3 apply checkpoint.

## Next Phase Readiness

- SES module is now install-aware; for `resource_prefix='km'` (existing install) Terraform plan shows no changes
- Plan 82-10 (operator checkpoint) will apply all Wave 1–3 infra changes together — SES is ready for that apply step
- Plans 82-07 (email-handler module) and 82-08 (ecs-task/ecs/ecs-cluster) are running in parallel in Wave 3 and do not overlap with SES

## Self-Check: PASSED

All files confirmed on disk and commit bf7abf5 present in git log.

---
*Phase: 82-multi-instance-resource-prefix-isolation*
*Completed: 2026-05-16*
