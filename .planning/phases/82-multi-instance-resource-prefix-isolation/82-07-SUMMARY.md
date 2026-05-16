---
phase: 82-multi-instance-resource-prefix-isolation
plan: "07"
subsystem: infra/email-handler
tags: [terraform, iam, email-handler, multi-instance, state-prefix]
dependency_graph:
  requires: [82-01, 82-02, 82-03, 82-04, 82-05]
  provides: [email-handler-state-prefix-parameterized]
  affects: [infra/modules/email-handler/v1.0.0, infra/live/use1/email-handler]
tech_stack:
  added: []
  patterns: [terraform-variable-parameterization, state-prefix-convention]
key_files:
  created: []
  modified:
    - infra/modules/email-handler/v1.0.0/variables.tf
    - infra/modules/email-handler/v1.0.0/main.tf
    - infra/live/use1/email-handler/terragrunt.hcl
decisions:
  - "Add standalone state_prefix variable rather than overloading resource_prefix — keeps IAM policy concern (state-bucket path) separate from resource-naming concern"
  - "Default state_prefix='tf-km' ensures zero Terraform diff for existing km install"
metrics:
  duration: 30s
  completed_date: "2026-05-16"
  tasks_completed: 1
  files_modified: 3
---

# Phase 82 Plan 07: Email-handler state_prefix parameterization Summary

**One-liner:** Added `state_prefix` Terraform variable (default `"tf-km"`) to email-handler module and replaced the hardcoded `tf-km` literal in the S3 IAM ARN with `${var.state_prefix}` interpolation.

## What Was Built

Fixes infrastructure blocker B2: `infra/modules/email-handler/v1.0.0/main.tf` line 75 hardcoded the literal `tf-km/` in the S3 IAM policy resource ARN used for sandbox metadata reads. A second install (e.g. `resource_prefix=rg`) would have its email-handler scoped to the wrong state bucket, unable to read sandbox metadata.

**Three-file change:**

1. **`variables.tf`** — New `variable "state_prefix"` added with `default = "tf-km"` and a clear description explaining its purpose (IAM policy scope for state-bucket reads, distinct from resource naming).

2. **`main.tf`** — The single `tf-km` literal at line 75 replaced:
   - Before: `"arn:aws:s3:::${var.state_bucket}/tf-km/sandboxes/*/metadata.json"`
   - After:  `"arn:aws:s3:::${var.state_bucket}/${var.state_prefix}/sandboxes/*/metadata.json"`

3. **`infra/live/use1/email-handler/terragrunt.hcl`** — `state_prefix = "tf-${get_env("KM_RESOURCE_PREFIX", "km")}"` added to inputs block, following the same convention as `site.hcl`'s `tf_state_prefix` local.

## Verification

All grep-based checks passed:
- `var.state_prefix` present in `main.tf`
- `variable "state_prefix"` declared in `variables.tf`
- `state_prefix = "tf-...` present in `terragrunt.hcl`
- No `tf-km` literal remaining in any IAM ARN in `main.tf`

For the existing `km` install: `KM_RESOURCE_PREFIX=km` evaluates `state_prefix` to `"tf-km"` — identical to the previous hardcoded value. Zero Terraform diff expected (confirmed in Plan 10 dry-run gate per VALIDATION.md row 1).

## Commits

| Task | Description | Hash | Files |
|------|-------------|------|-------|
| 1 | Add state_prefix variable and replace tf-km literal | 6558c27 | variables.tf, main.tf, terragrunt.hcl |

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- `infra/modules/email-handler/v1.0.0/variables.tf` — FOUND, contains `variable "state_prefix"`
- `infra/modules/email-handler/v1.0.0/main.tf` — FOUND, contains `${var.state_prefix}`, no `tf-km` literal in ARN
- `infra/live/use1/email-handler/terragrunt.hcl` — FOUND, contains `state_prefix = "tf-..."`
- Commit `6558c27` — FOUND in git log
