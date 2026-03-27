---
phase: 22-remote-sandbox-creation
plan: 03
subsystem: infra
tags: [lambda, terraform, dockerfile, ses, eventbridge, ecs, makefile, terragrunt]

# Dependency graph
requires:
  - phase: 22-01
    provides: create-handler Lambda binary (cmd/create-handler/) and km binary for container bundling
  - phase: 22-02
    provides: email-create-handler Lambda ARN referenced in SES module and live config
  - phase: 04-lifecycle-hardening-artifacts-email
    provides: SES module base (aws_ses_receipt_rule_set, s3 bucket policy pattern)
  - phase: 09-live-infrastructure-operator-docs
    provides: Terragrunt live hierarchy pattern (site.hcl, region.hcl, root.hcl)

provides:
  - Terraform module infra/modules/create-handler/v1.0.0/ with Lambda, IAM, EventBridge
  - Container-image Lambda arm64 with 0-retry EventBridge rule for SandboxCreate events
  - SES conditional create-inbound receipt rule routing create@ to mail/create/ prefix
  - Makefile targets: build-create-handler, build-email-create-handler, push-create-handler
  - infra/live/use1/create-handler/terragrunt.hcl ready for operator deployment

affects:
  - Phase 10 SCP: operator must add lambda_role_arn output to trusted_arns_iam list
  - Phase 22-02: email-create-handler ARN wired via KM_EMAIL_CREATE_HANDLER_ARN env var
  - Any downstream operator deploying sandboxes via email (create@ flow)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - Container-image Lambda (package_type = "Image") — different from zip-based Lambdas elsewhere
    - SES receipt rule ordering via `after` attribute for rule priority within a rule set
    - Conditional Terraform resources via `count = var.x != "" ? 1 : 0` for optional features
    - Multi-stage Dockerfile: amazonlinux:2023 downloader stage + provided:al2023-arm64 runtime

# Key files
key-files:
  created:
    - infra/modules/create-handler/v1.0.0/main.tf
    - infra/modules/create-handler/v1.0.0/variables.tf
    - infra/modules/create-handler/v1.0.0/outputs.tf
    - cmd/create-handler/Dockerfile
    - infra/live/use1/create-handler/terragrunt.hcl
  modified:
    - infra/modules/ses/v1.0.0/main.tf
    - infra/modules/ses/v1.0.0/variables.tf
    - Makefile

# Decisions
decisions:
  - "0 retries on EventBridge target: SandboxCreate events route to a 0-retry EventBridge target because km create is NOT idempotent — a retry after partial provisioning could corrupt the sandbox"
  - "Container image over zip Lambda: create-handler bundles terraform + terragrunt binaries (~500MB) which exceed zip Lambda limits; container image is the only viable packaging approach"
  - "SES rule ordering via `after` attribute: create-inbound (position 1) is referenced by sandbox-inbound via `after` when email_create_handler_arn is set, ensuring correct rule priority"
  - "Conditional SES resources: email_create_handler_arn defaults to empty string; create-inbound rule and S3 notification only created when ARN is provided, making the SES module safe to redeploy before email-create-handler exists"
  - "SCP note documented in outputs.tf: lambda_role_arn output description instructs operator to add it to Phase 10 SCP trusted_arns_iam — not auto-modified to avoid unintended infrastructure changes"

# Metrics
metrics:
  duration: 208s
  completed_date: "2026-03-27"
  tasks_completed: 2
  files_changed: 8
---

# Phase 22 Plan 03: Create-Handler Infrastructure Summary

Container-image Lambda Terraform module + Dockerfile bundling km, terraform, and terragrunt for remote sandbox creation via EventBridge SandboxCreate events, with SES create@ email routing and Makefile build pipeline.

## Tasks Completed

| Task | Name | Commit | Key Files |
|------|------|--------|-----------|
| 1 | Create-handler Terraform module + Dockerfile | 3584e21 | infra/modules/create-handler/v1.0.0/{main,variables,outputs}.tf, cmd/create-handler/Dockerfile |
| 2 | SES receipt rule + Makefile targets + live config | 1408f7e | infra/modules/ses/v1.0.0/{main,variables}.tf, Makefile, infra/live/use1/create-handler/terragrunt.hcl |

## What Was Built

### Terraform Module: infra/modules/create-handler/v1.0.0/

The create-handler module provisions:

- **IAM role** `km-create-handler` with scoped policies for: EC2 provisioning, IAM role/instance-profile management, ECS cluster/task management, S3 (artifact + state buckets), DynamoDB (state lock + budget tables), EventBridge Scheduler, SSM Parameter Store, SES send, Lambda (budget-enforcer functions only), KMS, CloudWatch Logs
- **Lambda function** `km-create-handler`: container image, arm64, 900s timeout, 1536MB RAM, 2048MB ephemeral storage; env vars wired for KM_ARTIFACTS_BUCKET, KM_EMAIL_DOMAIN, KM_OPERATOR_EMAIL, KM_STATE_BUCKET, KM_STATE_PREFIX, KM_REGION_LABEL
- **EventBridge rule** `km-sandbox-create`: matches `source=km.sandbox, detail-type=SandboxCreate`
- **EventBridge target** with `retry_policy { maximum_retry_attempts = 0 }` — create is not idempotent
- **Lambda permission** for events.amazonaws.com to invoke the Lambda
- **CloudWatch Log Group** `/aws/lambda/km-create-handler` with 30-day retention
- **Outputs**: `lambda_function_arn`, `lambda_role_arn` (with SCP note in description), `event_rule_arn`

### Dockerfile: cmd/create-handler/Dockerfile

Multi-stage arm64 container image:
- Stage 1 (downloader): amazonlinux:2023, downloads terraform 1.9.8 + terragrunt 0.67.16 arm64 binaries
- Stage 2 (runtime): provided:al2023-arm64, bundles bootstrap (create-handler binary), km binary, terraform, terragrunt, and the entire infra/ directory

### SES Module Updates

The SES module gains conditional resources (gated on `email_create_handler_arn != ""`):
- `aws_ses_receipt_rule.create_inbound`: routes `create@{domain}` to `mail/create/` S3 prefix, position 1
- `aws_lambda_permission.s3_email_create`: allows S3 to invoke the email-create-handler Lambda
- `aws_s3_bucket_notification.email_create`: S3 event notification on `mail/create/*` prefix
- `sandbox_inbound` rule updated with `after = create_inbound[0].name` when handler is set

### Makefile Targets

| Target | Description |
|--------|-------------|
| `build-create-handler` | Compile `km-create-handler` and `km` binaries (arm64) for Docker bundling |
| `build-email-create-handler` | Compile and zip email-create-handler Lambda binary |
| `push-create-handler` | `docker buildx build --platform linux/arm64 ... --push` to ECR |
| `ecr-repos` | Updated to include `km-create-handler` repository |
| `build-lambdas` | Updated to include email-create-handler zip packaging |

### Live Config: infra/live/use1/create-handler/terragrunt.hcl

Standard pattern: includes root.hcl, reads site.hcl and region.hcl, remote state at `tf-km/use1/create-handler/terraform.tfstate`. Inputs: ECR image URI derived from account ID, artifact bucket from env, DynamoDB budget table ARN hardcoded to `km-budgets` table name, `email_create_handler_arn` from `KM_EMAIL_CREATE_HANDLER_ARN` env var.

## Deviations from Plan

None — plan executed exactly as written.

## Operator Notes

1. **SCP update required**: After deploying, add the `lambda_role_arn` output to the Phase 10 SCP `trusted_arns_iam` list to permit the create-handler role to perform provisioning actions outside the SCP deny boundary.

2. **Deployment order**: Deploy `email-create-handler` (Phase 22-02) first to get its Lambda ARN, then set `KM_EMAIL_CREATE_HANDLER_ARN` before deploying this module to enable the create@ email flow.

3. **ECR setup**: Run `make ecr-repos` to create the `km-create-handler` ECR repository before the first `make push-create-handler`.

## Self-Check: PASSED

All 5 required files verified on disk. Both task commits (3584e21, 1408f7e) verified in git log.
