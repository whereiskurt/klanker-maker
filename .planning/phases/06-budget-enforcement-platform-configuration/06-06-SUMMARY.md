---
phase: 06-budget-enforcement-platform-configuration
plan: "06"
subsystem: cli-budget-management
tags: [cli, budget, dynamodb, ec2, iam, cobra, tdd]
dependency_graph:
  requires: [06-02, 06-05]
  provides: [km-budget-add, km-status-budget, km-create-budget-init]
  affects: [internal/app/cmd/budget.go, internal/app/cmd/status.go, internal/app/cmd/create.go]
tech_stack:
  added: []
  patterns:
    - DI interfaces for EC2StartAPI, IAMAttachAPI, SandboxMetaFetcher, BudgetFetcher
    - Additive budget top-up with GetBudget + SetBudgetLimits
    - EC2 auto-resume via DescribeInstances tag filter + StartInstances
    - Bedrock IAM restore via ListAttachedRolePolicies + AttachRolePolicy
    - ANSI color-coded percentages (TTY detection via Stat ModeCharDevice)
    - Graceful degradation: budget fetch errors do not fail km status
key_files:
  created:
    - internal/app/cmd/budget.go
    - internal/app/cmd/budget_test.go
  modified:
    - internal/app/cmd/status.go
    - internal/app/cmd/status_test.go
    - internal/app/cmd/create.go
    - internal/app/cmd/root.go
decisions:
  - EC2 auto-resume uses DescribeInstances with sandbox-id tag filter rather than stored instance ID in metadata — avoids metadata schema changes and handles multi-instance sandboxes
  - Bedrock IAM restore uses sandboxRoleName(sandboxID) convention (km-sandbox-{id}-role) — matches compiler output from Phase 02
  - BudgetFetcher is a parallel DI interface to SandboxFetcher — allows independent testing and graceful degradation without touching existing SandboxFetcher interface
  - NewStatusCmdWithFetcher preserved as backward-compatible wrapper over NewStatusCmdWithFetchers — no test breakage
  - Budget section omitted silently when DynamoDB fetch fails or returns zero limits — km status should not fail for sandboxes without budget
metrics:
  duration: 374s
  completed_date: "2026-03-22"
  tasks_completed: 2
  files_changed: 6
---

# Phase 06 Plan 06: CLI Budget Management Summary

**One-liner:** km budget add with EC2/IAM auto-resume, km status with per-model budget breakdown, km create with DynamoDB budget init

## What Was Built

### Task 1: km budget add command with auto-resume (TDD)

New command `km budget add <sandbox-id> [--compute <amount>] [--ai <amount>]` implemented in `internal/app/cmd/budget.go`.

**Logic:**
1. Read current limits via `GetBudget` (DynamoDB Query)
2. Add top-up amounts to existing limits (additive, not replace)
3. Write new limits via `SetBudgetLimits` (DynamoDB SET)
4. Auto-resume: if substrate=ec2 and instance stopped, call `StartInstances`
5. IAM restore: if Bedrock policy missing from role, call `AttachRolePolicy`
6. Print summary: `Budget updated: compute $X.XX/$Y.YY, AI $X.XX/$Y.YY. Sandbox resumed.`

**DI interfaces exported for testing:**
- `EC2StartAPI` — DescribeInstances + StartInstances
- `IAMAttachAPI` — ListAttachedRolePolicies + AttachRolePolicy
- `SandboxMetaFetcher` — FetchSandboxMeta from S3 metadata
- `NewBudgetCmdWithDeps(cfg, budgetClient, ec2Client, iamClient, metaFetcher)` constructor

Registered in `root.go` as `km budget` subcommand.

### Task 2: km status budget display + km create budget init

**km status extended (`internal/app/cmd/status.go`):**
- New `BudgetFetcher` interface with `FetchBudget(ctx, sandboxID)` method
- `NewStatusCmdWithFetchers(cfg, fetcher, budgetFetcher)` constructor added
- `NewStatusCmdWithFetcher` preserved as backward-compatible wrapper
- Budget section printed when DynamoDB has limits set:
  ```
  Budget:
    Compute: $1.23 / $5.00 (24.6%)
    AI:      $3.45 / $10.00 (34.5%)
      anthropic.claude-haiku-3:     $1.35 (500K in / 200K out)
      anthropic.claude-sonnet-4:    $2.10 (150K in / 45K out)
    Warning threshold: 80%
  ```
- ANSI color-coding for percentages: green (<80%), yellow (80-99%), red (>=100%)
- Colors disabled when output is not a TTY (file/pipe detection via `Stat ModeCharDevice`)
- Budget fetch errors silently omit section (graceful degradation)

**km create updated (`internal/app/cmd/create.go`):**
- Step 12b added after EventBridge TTL schedule, before SES provisioning
- When `profile.Spec.Budget != nil`, creates DynamoDB client and calls `SetBudgetLimits`
- Uses `cfg.BudgetTableName` (defaults to `km-budgets`)
- Extracts `computeLimit`, `aiLimit` from profile; defaults `warningThreshold` to 0.80
- Non-fatal: sandbox provisioned even if DynamoDB write fails
- Prints: `Budget limits set: compute $X.XX, AI $X.XX, warning at 80%`

## Tests

All 35 existing cmd tests pass plus 9 new tests:

**Budget tests (5):**
- `TestBudgetAdd_UpdatesDynamoDBLimits` — UpdateItem called, "Budget updated" in output
- `TestBudgetAdd_AIOnlyUpdate` — AI-only --ai flag works
- `TestBudgetAdd_ResumesStoppedEC2` — StartInstances called for stopped instance, "resumed" in output
- `TestBudgetAdd_RestoresBedrockIAM` — AttachRolePolicy called when Bedrock policy missing
- `TestBudgetAdd_RequiresSandboxID` — error returned when sandbox-id arg missing

**Status tests (4 new):**
- `TestStatusCmd_BudgetDisplayed` — Budget section with per-model breakdown shown
- `TestStatusCmd_BudgetOmittedWhenNoBudget` — No Budget: section when budgetFetcher=nil
- `TestStatusCmd_BudgetGracefulDegradation` — Budget fetch error doesn't fail status command

## Deviations from Plan

### Auto-fixed Issues

None.

### Design Adjustments

**1. [Rule 1 - Design] EC2 auto-resume uses tag filter instead of stored instance ID**
- **Found during:** Task 1 implementation
- **Issue:** `SandboxMetadata` has no `InstanceID` field; adding it would require schema change
- **Fix:** Use `DescribeInstances` with `tag:sandbox-id` filter — handles multi-instance scenarios and avoids metadata schema changes
- **Files modified:** `internal/app/cmd/budget.go`

## Self-Check: PASSED

- `/Users/khundeck/working/klankrmkr/internal/app/cmd/budget.go` — FOUND
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/budget_test.go` — FOUND
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/status.go` — FOUND (extended)
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/create.go` — FOUND (extended)
- Commit `e8dec61` (test RED) — FOUND
- Commit `f3c906c` (feat GREEN) — FOUND
- Commit `a71703f` (feat Task 2) — FOUND
- All 35 tests pass: `go test ./internal/app/cmd/... -count=1` PASS
