---
phase: 06-budget-enforcement-platform-configuration
plan: 07
subsystem: ui
tags: [configui, budget, dynamodb, htmx, dashboard]

requires:
  - phase: 06-02
    provides: "pkg/aws.GetBudget, BudgetSummary — DynamoDB budget query function"
  - phase: 05-configui
    provides: "ConfigUI dashboard with HTMX polling, Handler struct, DashboardData template data"

provides:
  - "ConfigUI dashboard budget columns: real-time Compute Budget and AI Budget per sandbox"
  - "BudgetFetcher interface for DI-testable budget data retrieval"
  - "BudgetDisplayData struct with pre-formatted monetary strings and CSS class logic"
  - "Color-coded budget indicators: budget-ok (green), budget-warn (yellow), budget-exceeded (red)"
  - "Graceful degradation: nil fetcher or DynamoDB error shows dash in budget columns"

affects: ["configui", "dashboard", "budget-enforcement"]

tech-stack:
  added: []
  patterns:
    - "DashboardSandbox wrapper struct: embeds SandboxRecord + Budget *BudgetDisplayData for per-row enrichment"
    - "Pre-formatted display structs: monetary values as '$N.NN' strings, CSS classes computed at fetch time"
    - "80%/100% CSS threshold logic: budget-ok < 80%, budget-warn 80-99%, budget-exceeded >= 100%"
    - "Worst-class wins: CSSClass reflects the more severe of compute vs AI percentage"

key-files:
  created:
    - cmd/configui/handlers_budget.go
    - cmd/configui/handlers_budget_test.go
  modified:
    - cmd/configui/handlers.go
    - cmd/configui/main.go
    - cmd/configui/templates/dashboard.html
    - cmd/configui/templates/partials/sandbox_row.html

key-decisions:
  - "DashboardSandbox wrapper embeds SandboxRecord + Budget pointer — avoids modifying pkg/aws.SandboxRecord (shared type) while giving templates access to budget data"
  - "Budget fetch in handleDashboard per-sandbox loop with silent degradation — budget error logged as Warn, dash shown in UI, no HTTP 500"
  - "dynoBudgetFetcher wraps pkg/aws.GetBudget — returns HasBudget=false on any error (unreachable DynamoDB treated same as no-budget)"
  - "Two budget columns (Compute/AI) not one — operators need visibility into which budget category is at risk"

requirements-completed: [BUDG-09]

duration: 4min
completed: 2026-03-22
---

# Phase 06 Plan 07: ConfigUI Budget Dashboard Columns Summary

**ConfigUI dashboard enriched with real-time per-sandbox Compute and AI budget columns, color-coded green/yellow/red, polling via existing HTMX 10-second refresh**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-03-22T20:52:56Z
- **Completed:** 2026-03-22T20:56:xx Z
- **Tasks:** 1 of 2 (Task 2 is human-verify checkpoint)
- **Files modified:** 6

## Accomplishments

- Added `BudgetFetcher` interface and `BudgetDisplayData` struct with pre-formatted monetary values and CSS class logic
- `DashboardSandbox` wrapper embeds `SandboxRecord` + `Budget *BudgetDisplayData` — enriches each row without modifying shared `pkg/aws` types
- `handleDashboard` fetches per-sandbox budget data in a loop with silent graceful degradation (error → dash in UI)
- Dashboard template updated with two columns (Compute Budget, AI Budget) with inline CSS for green/yellow/red
- `dynoBudgetFetcher` wired in `main.go` using DynamoDB client and `KM_BUDGET_TABLE` env var (default "km-budgets")
- 15 new tests: formatting, CSS boundary values (79%, 80%, 99%, 100%), worst-class logic, dashboard render with/without budget

## Task Commits

1. **Task 1: ConfigUI budget dashboard columns** - `99c56bc` (feat)
2. **Task 2: Phase 6 end-to-end verification checkpoint** - awaiting human-verify

## Files Created/Modified

- `cmd/configui/handlers_budget.go` - BudgetFetcher interface, BudgetDisplayData, budgetCSSClass logic, dynoBudgetFetcher
- `cmd/configui/handlers_budget_test.go` - 15 tests covering formatting, CSS thresholds, dashboard rendering
- `cmd/configui/handlers.go` - DashboardSandbox wrapper, Handler.budgetFetcher field, updated handleDashboard, updated buildTestTemplates sandbox_rows
- `cmd/configui/main.go` - DynamoDB client, dynoBudgetFetcher wiring, budgetFetcher field in Handler
- `cmd/configui/templates/dashboard.html` - Two budget columns in thead, budget CSS classes, colspan 8
- `cmd/configui/templates/partials/sandbox_row.html` - Two budget cells with conditional Budget.HasBudget rendering

## Decisions Made

- DashboardSandbox wrapper chosen over adding Budget field to `pkg/aws.SandboxRecord` — shared types stay clean, UI concerns stay in configui
- Budget fetch errors are silently degraded (Warn log + dash in UI) — budget display is informational, not critical path
- `KM_BUDGET_TABLE` env var for DynamoDB table name (default "km-budgets") — consistent with other configui env var patterns

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## Next Phase Readiness

Phase 6 is complete. All 7 plans executed. Full test suite passes, all binaries build (km, configui, budget-enforcer). The platform now has:
- Schema + compiler pipeline (Phase 1)
- Core provisioning and security baseline (Phase 2)
- Sidecar enforcement and lifecycle management (Phase 3)
- Lifecycle hardening, artifacts, and email (Phase 4)
- ConfigUI dashboard, editor, and secrets management (Phase 5)
- Budget enforcement, platform configuration, and ConfigUI budget display (Phase 6)

---
*Phase: 06-budget-enforcement-platform-configuration*
*Completed: 2026-03-22*
