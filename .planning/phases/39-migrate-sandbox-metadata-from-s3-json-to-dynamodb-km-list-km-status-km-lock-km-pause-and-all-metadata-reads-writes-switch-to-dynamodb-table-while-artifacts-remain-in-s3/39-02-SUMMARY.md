---
phase: 39-migrate-sandbox-metadata-s3-to-dynamodb
plan: 02
subsystem: infrastructure
tags: [dynamodb, terraform, iam, lambda, init]
dependency_graph:
  requires: []
  provides: [dynamodb-sandboxes-module, km-sandboxes-iam, sandbox-table-name-config]
  affects: [ttl-handler, email-handler, create-handler, km-init, km-config]
tech_stack:
  added: [dynamodb-sandboxes terraform module v1.0.0]
  patterns: [PAY_PER_REQUEST DynamoDB, GSI alias-index, TTL on ttl_expiry, inline IAM role policies]
key_files:
  created:
    - infra/modules/dynamodb-sandboxes/v1.0.0/main.tf
    - infra/modules/dynamodb-sandboxes/v1.0.0/variables.tf
    - infra/modules/dynamodb-sandboxes/v1.0.0/outputs.tf
    - infra/live/use1/dynamodb-sandboxes/terragrunt.hcl
  modified:
    - infra/modules/ttl-handler/v1.0.0/main.tf
    - infra/modules/email-handler/v1.0.0/main.tf
    - infra/modules/create-handler/v1.0.0/main.tf
    - internal/app/cmd/init.go
    - internal/app/config/config.go
decisions:
  - "No replica_regions variable in dynamodb-sandboxes (v1.0.0 is single-region; can be added in v1.1.0 if multi-region needed)"
  - "dynamodb-sandboxes placed after dynamodb-identities and before s3-replication in init ordering (ensures table exists before Lambda handlers)"
  - "Hardcoded km-sandboxes ARN in Lambda IAM policies (consistent with how km-budgets table ARN is referenced in create-handler)"
metrics:
  duration: 131s
  tasks_completed: 2
  files_changed: 9
  completed_date: "2026-03-28"
---

# Phase 39 Plan 02: DynamoDB Sandboxes Infrastructure Summary

**One-liner:** km-sandboxes DynamoDB table module with sandbox_id PK, alias-index GSI, ttl_expiry TTL, and GetItem/PutItem/UpdateItem/DeleteItem/Scan/Query IAM grants on all 3 Lambda execution roles.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Terraform module + Terragrunt live config for DynamoDB sandboxes table | 4d969b2 | main.tf, variables.tf, outputs.tf, terragrunt.hcl |
| 2 | IAM permissions for Lambda roles + km init ordering + config field | e65f107 | 3x main.tf, init.go, config.go |

## What Was Built

### Task 1: DynamoDB Sandboxes Module

Created `infra/modules/dynamodb-sandboxes/v1.0.0/` mirroring the dynamodb-identities v1.1.0 pattern:

- `aws_dynamodb_table.sandboxes`: `sandbox_id` (S) hash key, PAY_PER_REQUEST billing
- `alias-index` GSI: `alias` (S) hash key, ALL projection — enables lookup by alias without full scan
- TTL on `ttl_expiry` attribute — automatic cleanup after sandbox destroy grace period
- `variables.tf`: `table_name` (default `km-sandboxes`), `tags`
- `outputs.tf`: `table_name`, `table_arn`
- `infra/live/use1/dynamodb-sandboxes/terragrunt.hcl`: mirrors dynamodb-identities, state key at `dynamodb-sandboxes/terraform.tfstate`

No `replica_regions` variable added — single-region v1.0.0 design is simpler; can be added in v1.1.0.

### Task 2: IAM Permissions, Init Ordering, Config Field

**IAM policies (all 3 Lambda roles):**

Each Lambda module received a new `aws_iam_role_policy.dynamodb_sandboxes` granting:
- Actions: `GetItem`, `PutItem`, `UpdateItem`, `DeleteItem`, `Scan`, `Query`
- Resources: `arn:aws:dynamodb:*:${account_id}:table/km-sandboxes` + `table/km-sandboxes/index/alias-index`

**km init ordering:**

`dynamodb-sandboxes` inserted into `regionalModules()` after `dynamodb-identities` and before `s3-replication` — ensures the table is provisioned before ttl-handler and create-handler Lambda deployments that reference it.

**Config field:**

`SandboxTableName string` added to `Config` struct in `internal/app/config/config.go`:
- Default: `"km-sandboxes"` via `v.SetDefault("sandbox_table_name", "km-sandboxes")`
- Merged from `km-config.yaml` key `sandbox_table_name`
- Populated via `v.GetString("sandbox_table_name")`

## Verification

- `terraform fmt -check infra/modules/dynamodb-sandboxes/v1.0.0/` — PASS
- `terraform fmt -check infra/modules/ttl-handler/v1.0.0/ infra/modules/email-handler/v1.0.0/ infra/modules/create-handler/v1.0.0/` — PASS
- `make build` — PASS (km v0.0.67 built with ldflags)
- `grep "dynamodb-sandboxes" internal/app/cmd/init.go` — PASS
- `grep "SandboxTableName" internal/app/config/config.go` — PASS
- `grep "km-sandboxes" infra/modules/ttl-handler/v1.0.0/main.tf` — PASS
- `grep "km-sandboxes" infra/modules/email-handler/v1.0.0/main.tf` — PASS
- `grep "km-sandboxes" infra/modules/create-handler/v1.0.0/main.tf` — PASS

## Deviations from Plan

None — plan executed exactly as written.

Auto-formatting was applied by `terraform fmt` to pre-existing alignment differences in ttl-handler and create-handler (unrelated attribute ordering). These are cosmetic changes not introduced by this plan's edits.

## Self-Check: PASSED
