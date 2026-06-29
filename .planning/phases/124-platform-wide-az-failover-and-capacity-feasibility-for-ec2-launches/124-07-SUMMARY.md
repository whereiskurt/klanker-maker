---
phase: 124-platform-wide-az-failover-and-capacity-feasibility-for-ec2-launches
plan: "07"
subsystem: capacity-store-write-back
tags: [capacity, dynamodb, iam, tdd, gap-closure]
dependency_graph:
  requires: [124-02, 124-04, 124-05]
  provides: [capacity-store-write-path, create-handler-capacity-iam]
  affects: [internal/app/cmd/create.go, infra/modules/km-operator-policy, infra/modules/create-handler, infra/live/use1/create-handler]
tech_stack:
  added: []
  patterns:
    - best-effort write-back with 5s bounded context and log.Warn on error
    - hoisted var pattern to share state between ranking block and sweep loop
    - terraform count-gated conditional IAM policy (back-compat empty-default)
key_files:
  created: []
  modified:
    - internal/app/cmd/create.go
    - internal/app/cmd/create_az_sweep_test.go
    - infra/modules/km-operator-policy/v1.0.0/main.tf
    - infra/modules/km-operator-policy/v1.0.0/variables.tf
    - infra/modules/create-handler/v1.0.0/main.tf
    - infra/modules/create-handler/v1.0.0/variables.tf
    - infra/live/use1/create-handler/terragrunt.hcl
    - CLAUDE.md
decisions:
  - "RecordICE fires on ClassICE only (not SpotPrice/Quota/Auth/Unknown) to keep the signal precise"
  - "bestEffortRecordCapacity uses a 5s bounded context so a slow DDB never delays the create result"
  - "capacity_table_name defaults to '' in all variables.tf (back-compat for pre-Phase-124 installs)"
  - "Static string in terragrunt.hcl (no dependency block) mirrors the slack_threads/slack_channels pattern"
metrics:
  duration: 4 minutes
  completed: 2026-06-29
  tasks_completed: 3
  files_modified: 8
---

# Phase 124 Plan 07: Capacity Store Write-Back Gap Closure Summary

Wired the capacity store write path into the `km create` AZ sweep loop and granted the create-handler Lambda role the DynamoDB permissions it needs to call that path.

## One-Liner

Closed the Phase 124 feedback loop: `RecordSuccess`/`RecordICE` now fire from the `km create` sweep loop (both operator-side and Lambda cold-create paths), and the create-handler Lambda role has `GetItem/UpdateItem` on `{prefix}-capacity` so the writes succeed.

## What Was Done

### Task 1: Wire RecordSuccess/RecordICE into the sweep loop (TDD)

**RED (test before implementation):**
- Added `fakeCapacityStore` struct (implements `capacity.CapacityStore`) to `create_az_sweep_test.go`
- Added `TestCapacityWriteBack` with 5 sub-tests: success calls RecordSuccess, ICE calls RecordICE, non-ICE failures call neither, nil store is no-op, store error is best-effort (never propagates)
- Confirmed test fails with `undefined: bestEffortRecordCapacity`

**GREEN (implementation):**
- Added `bestEffortRecordCapacity(ctx, store, instanceType, az, class, success)` helper in `create.go` after `sweepDecision` (lines ~83-112)
- Hoisted `var capacityStore capacity.CapacityStore` and `var rankInstanceType string` to scope before the ranking block (so they survive into the sweep loop)
- Changed the ranking block from `:=` to `=` assignments for those two variables
- Wired `bestEffortRecordCapacity(..., ClassSuccess, true)` on the success branch (before the `break`)
- Wired `bestEffortRecordCapacity(..., class, false)` immediately after `ClassifyError` on the failure branch

The helper uses a 5-second bounded context, logs `Warn` on error, and returns immediately — a DDB outage never blocks or delays a create.

### Task 2: Grant create-handler Lambda role dynamodb write on {prefix}-capacity

Three-layer IAM wiring (mirrors the established `slack_threads_table_name` and `slack_channels_table_name` patterns):

1. **`km-operator-policy/v1.0.0/variables.tf`**: Added `capacity_table_name` variable (default `""`)
2. **`km-operator-policy/v1.0.0/main.tf`**: Added `aws_iam_role_policy "dynamodb_capacity"` (count-gated on `capacity_table_name != ""`) granting `GetItem/PutItem/UpdateItem/DescribeTable` on the table ARN
3. **`create-handler/v1.0.0/variables.tf`**: Added `capacity_table_name` passthrough variable (default `""`)
4. **`create-handler/v1.0.0/main.tf`**: Threaded `capacity_table_name = var.capacity_table_name` into the `module "km_operator_policy"` block
5. **`infra/live/use1/create-handler/terragrunt.hcl`**: Added `capacity_table_name = "${local.site_vars.locals.site.label}-capacity"` (static string, no dependency block needed — IAM grant needs only the table name)

`terraform fmt` clean on all edited `.tf` files.

### Task 3: Document the closed feedback loop in CLAUDE.md

Added a Phase 124.07 bullet to the Phase 124 block in CLAUDE.md stating: feedback loop closed, deploy = `make build-lambdas` + `km init --dry-run=false`.

## Deviations from Plan

None — plan executed exactly as written.

## Feedback Loop Closure

Before this plan:
- `RankAZs` reads the `{prefix}-capacity` store to produce preference ordering
- The sweep loop never wrote to the store
- RankAZs always read an empty store → ranking was inert (no sticky-success, no ICE deprioritization)

After this plan:
- Successful apply → `RecordSuccess` → next RankAZs call prefers that AZ (sticky)
- ICE failure → `RecordICE` (TTL 45min) → next RankAZs call deprioritizes that AZ
- The cold-create Lambda path is covered too (Lambda shells out to the bundled `km create` subprocess, so `create.go` changes apply to both runtime paths)

## Deploy

NO terragrunt apply was run (per plan — deploy is a separate operator checkpoint).

To deploy: `make build-lambdas` + `km init --dry-run=false` (the IAM grant is in the create-handler Terraform module, not `--sidecars`).

## Self-Check: PASSED

- FOUND: internal/app/cmd/create.go
- FOUND: internal/app/cmd/create_az_sweep_test.go
- FOUND: infra/modules/km-operator-policy/v1.0.0/main.tf
- FOUND: infra/live/use1/create-handler/terragrunt.hcl
- FOUND commit 12a2b717 (Task 1: wire sweep loop)
- FOUND commit 7970d176 (Task 2: IAM grant)
- FOUND commit a254def4 (Task 3: docs)
