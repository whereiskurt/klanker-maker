---
phase: 09-live-infrastructure-operator-docs
plan: 01
subsystem: infra
tags: [terragrunt, terraform, lambda, dynamodb, ses, arm64, graviton]

# Dependency graph
requires:
  - phase: 04-lifecycle-hardening-artifacts-email
    provides: ttl-handler and ses Terraform modules (infra/modules/ttl-handler, infra/modules/ses)
  - phase: 06-budget-enforcement-platform-configuration
    provides: dynamodb-budget Terraform module and budget-enforcer Lambda binary (infra/modules/dynamodb-budget, cmd/budget-enforcer)
provides:
  - Makefile build-lambdas target producing arm64 Lambda deployment zips
  - Terragrunt live config for TTL Handler Lambda (infra/live/use1/ttl-handler)
  - Terragrunt live config for DynamoDB budget table (infra/live/use1/dynamodb-budget)
  - Terragrunt live config for SES email domain verification (infra/live/use1/ses)
affects: [10-scp-sandbox-containment, operator-runbooks]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Lambda arm64 build: GOOS=linux GOARCH=arm64, binary named bootstrap inside zip"
    - "Terragrunt live config pattern: locals (repo_root, site_vars, region_config) + include root + remote_state S3 + terraform source + inputs"

key-files:
  created:
    - infra/live/use1/ttl-handler/terragrunt.hcl
    - infra/live/use1/dynamodb-budget/terragrunt.hcl
    - infra/live/use1/ses/terragrunt.hcl
  modified:
    - Makefile

key-decisions:
  - "Lambda binaries use GOARCH=arm64 (not amd64) matching architectures=[arm64] in Terraform modules — mismatch causes exec format error"
  - "Binary inside Lambda zip must be named bootstrap (not package name) — provided.al2023 runtime requirement"
  - "KM_ROUTE53_ZONE_ID referenced inline in ses/terragrunt.hcl only — not added to site.hcl (per plan spec)"
  - "dynamodb-budget starts with replica_regions=[] — operators add replicas post-deployment when multi-region needed"

patterns-established:
  - "All live configs use get_env() for runtime values (KM_ARTIFACTS_BUCKET, KM_OPERATOR_EMAIL, KM_ROUTE53_ZONE_ID) — no hardcoded account or domain values"
  - "artifact_bucket_arn computed inline as arn:aws:s3:::${get_env('KM_ARTIFACTS_BUCKET', '')} — avoids data source lookups"

requirements-completed: [PROV-05, BUDG-02, MAIL-01]

# Metrics
duration: 20min
completed: 2026-03-22
---

# Phase 9 Plan 1: Live Infrastructure Deployment Configs Summary

**arm64 Lambda build target and Terragrunt live configs for ttl-handler, dynamodb-budget, and SES — completing the deployment pipeline for three shared infrastructure services**

## Performance

- **Duration:** ~20 min
- **Started:** 2026-03-22T22:49:00Z
- **Completed:** 2026-03-22T23:08:59Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments

- `make build-lambdas` cross-compiles ttl-handler and budget-enforcer for linux/arm64 and produces correctly-named bootstrap zips
- Three Terragrunt live configs created in `infra/live/use1/`, each following the established network/terragrunt.hcl pattern
- All required module variables supplied via `get_env()` calls or computed from `site_vars` — no hardcoded values
- `build/` already in `.gitignore`; no changes needed

## Task Commits

1. **Task 1: Add build-lambdas target to Makefile** - `7e9ebd8` (feat)
2. **Task 2: Create Terragrunt live configs for ttl-handler, dynamodb-budget, and ses** - `ab9ec97` (feat)

## Files Created/Modified

- `Makefile` - Added `build-lambdas` target and `build-lambdas` to `.PHONY`
- `infra/live/use1/ttl-handler/terragrunt.hcl` - Terragrunt config for TTL Handler Lambda; inputs: artifact_bucket, email_domain, operator_email, lambda_zip_path
- `infra/live/use1/dynamodb-budget/terragrunt.hcl` - Terragrunt config for DynamoDB budget table; km-budgets, no replicas, component tags
- `infra/live/use1/ses/terragrunt.hcl` - Terragrunt config for SES domain verification; KM_ROUTE53_ZONE_ID inline

## Decisions Made

- Lambda arm64 build uses explicit `GOOS=linux GOARCH=arm64` (not Makefile top-level `GOARCH := amd64`) — sidecars use amd64, Lambdas use arm64 for Graviton cost savings
- Binary inside zip named `bootstrap` (not `ttl-handler` or `budget-enforcer`) per AWS Lambda `provided.al2023` runtime requirement
- `KM_ROUTE53_ZONE_ID` referenced inline in `ses/terragrunt.hcl` only — plan explicitly forbids modifying `site.hcl` for this value
- `dynamodb-budget` starts with `replica_regions = []` — single-region deployment; operators add replicas when needed

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

Before running `terragrunt apply` on any of these configs, operators must export:

- `KM_ARTIFACTS_BUCKET` — S3 bucket for Lambda zips and sidecar artifacts
- `KM_ROUTE53_ZONE_ID` — Route53 hosted zone ID for the sandboxes subdomain (ses config only)
- `KM_OPERATOR_EMAIL` — Operator notification address (ttl-handler config; empty string is valid)

Run `make build-lambdas` before deploying ttl-handler to produce `build/ttl-handler.zip`.

## Next Phase Readiness

- Live infrastructure deployment configs are complete for Phase 9 Plan 1 scope
- Operators can now `cd infra/live/use1/ttl-handler && terragrunt apply` to deploy TTL lifecycle management
- Operators can now `cd infra/live/use1/dynamodb-budget && terragrunt apply` to deploy the budget tracking table
- Operators can now `cd infra/live/use1/ses && terragrunt apply` to deploy SES email domain verification
- Phase 9 Plan 2 (operator runbook) can reference these exact deployment commands

---
*Phase: 09-live-infrastructure-operator-docs*
*Completed: 2026-03-22*
