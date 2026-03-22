---
phase: 09-live-infrastructure-operator-docs
plan: "02"
subsystem: compiler
tags: [budget-enforcer, compiler, terragrunt, km-create, km-destroy]
dependency_graph:
  requires:
    - "06-05: budget-enforcer Lambda module (infra/modules/budget-enforcer/v1.0.0)"
    - "06-06: budget_enforcer_inputs block in service_hcl.go"
  provides:
    - "per-sandbox budget-enforcer/terragrunt.hcl generator"
    - "BudgetEnforcerHCL field in CompiledArtifacts"
    - "km create Step 12c: budget-enforcer deploy"
    - "km destroy Step 7b: budget-enforcer pre-destroy"
  affects:
    - "pkg/compiler/compiler.go"
    - "internal/app/cmd/create.go"
    - "internal/app/cmd/destroy.go"
tech_stack:
  added: []
  patterns:
    - "text/template for Terragrunt HCL generation (matches existing compiler pattern)"
    - "Non-fatal deployment step (matches Phase 06-06 budget init pattern)"
    - "TDD: RED commit then GREEN commit for compiler work"
key_files:
  created:
    - pkg/compiler/budget_enforcer_hcl.go
    - pkg/compiler/budget_enforcer_hcl_test.go
  modified:
    - pkg/compiler/compiler.go
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go
decisions:
  - "Budget enforcer terragrunt.hcl reads budget_enforcer_inputs from sibling service.hcl via read_terragrunt_config — template is substrate-agnostic since substrate comes from service.hcl at apply time"
  - "GenerateBudgetEnforcerHCL exported (uppercase) so external callers (future plans) can generate the HCL directly without going through Compile()"
  - "budget_table_arn constructed at HCL eval time from site_vars accounts.application + region — avoids hardcoding account IDs in compiled output"
  - "budget-enforcer destroy fires before main sandbox destroy — Lambda depends on sandbox IAM role and instance ID which main module manages"
metrics:
  duration: "128s"
  completed_date: "2026-03-22"
  tasks_completed: 2
  files_changed: 5
---

# Phase 09 Plan 02: Budget Enforcer HCL Generator + km create/destroy Wiring Summary

**One-liner:** Per-sandbox budget-enforcer Terragrunt HCL generator using text/template reading inputs from sibling service.hcl, with non-fatal km create/destroy wiring.

## What Was Built

### Task 1: Budget enforcer HCL generator + compiler integration (TDD)

Added `BudgetEnforcerHCL string` field to `CompiledArtifacts` in `compiler.go` with comment explaining empty-when-no-budget semantics.

Created `pkg/compiler/budget_enforcer_hcl.go`:
- `budgetEnforcerHCLTemplate`: Terragrunt HCL template that reads `budget_enforcer_inputs` from `../service.hcl` via `read_terragrunt_config`. Uses `find_in_parent_folders("CLAUDE.md")` to locate repo root. State key: `tf-km/sandboxes/{sandbox-id}/budget-enforcer/terraform.tfstate`. Module source: `budget-enforcer/v1.0.0`.
- `GenerateBudgetEnforcerHCL(sandboxID string) (string, error)`: exported function executing the template.
- `generateBudgetEnforcerHCL`: unexported alias for internal compiler use.

Updated `compileEC2()` and `compileECS()` in `compiler.go` to call `generateBudgetEnforcerHCL(sandboxID)` when `p.Spec.Budget != nil`, storing result in `artifacts.BudgetEnforcerHCL`.

### Task 2: Wire km create to deploy per-sandbox budget-enforcer

Updated `internal/app/cmd/create.go` Step 12c (after budget limits DynamoDB write):
- If `artifacts.BudgetEnforcerHCL != ""`: creates `{sandboxDir}/budget-enforcer/`, writes `terragrunt.hcl`, runs `runner.Apply(ctx, budgetEnforcerDir)`
- Non-fatal at every step: mkdir failure, write failure, apply failure all log warnings and continue

Updated `internal/app/cmd/destroy.go` Step 7b (before main sandbox destroy):
- Checks if `{sandboxDir}/budget-enforcer/` exists via `os.Stat`
- If present: runs `runner.Destroy(ctx, budgetEnforcerDir)` — non-fatal, main destroy proceeds regardless
- Enforcer destroyed first because the budget-enforcer Lambda's IAM conditions reference the sandbox IAM role managed by the main module

## Tests Written (TDD)

`pkg/compiler/budget_enforcer_hcl_test.go`:
- `TestGenerateBudgetEnforcerHCL_EC2`: verifies HCL contains `budget-enforcer/v1.0.0`, `lambda_zip_path`, `budget_table_arn`, `read_terragrunt_config`, `remote_state`, per-sandbox state key
- `TestGenerateBudgetEnforcerHCL_ECS`: verifies template is substrate-agnostic (same template serves both EC2 and ECS)
- `TestCompileBudgetEnforcerHCL_WithBudget`: `Compile()` with `ec2-with-budget.yaml` returns non-empty `BudgetEnforcerHCL`
- `TestCompileBudgetEnforcerHCL_NoBudget`: `Compile()` with `ec2-basic.yaml` returns empty `BudgetEnforcerHCL`

## Deviations from Plan

None - plan executed exactly as written.

## Self-Check: PASSED

- `/Users/khundeck/working/klankrmkr/pkg/compiler/budget_enforcer_hcl.go` — FOUND
- `/Users/khundeck/working/klankrmkr/pkg/compiler/budget_enforcer_hcl_test.go` — FOUND
- All 4 BudgetEnforcer tests pass
- `go build ./cmd/km/` succeeds
- Commits: ab85fd0 (RED tests), 3762a3e (GREEN impl), fa5d418 (Task 2 wiring)
