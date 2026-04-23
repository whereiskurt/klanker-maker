---
phase: 60-budget-compute-accounting-excludes-paused-hibernated-intervals
plan: 03
subsystem: budget
tags: [dynamodb, ec2, lambda, budget-enforcer, paused-intervals, cost-accounting]

requires:
  - phase: 60-01
    provides: BudgetSummary.PausedSeconds, BudgetSummary.PausedAt, RecordPauseStart
  - phase: 60-02
    provides: pausedAt written on pause/resume transitions via km commands

provides:
  - calculateComputeCost(event, pausedSeconds) subtracts pause time from elapsed with zero clamp
  - HandleBudgetCheck computes effective pausedSeconds (closed + open interval) before cost calc
  - enforceBudgetCompute calls RecordPauseStart after EC2 StopInstances to stop the billing clock
  - 8 new tests covering all paused-accounting scenarios

affects:
  - budget-enforcer Lambda runtime behavior
  - any phase reading compute cost from BudgetSummary.ComputeSpent

tech-stack:
  added: []
  patterns:
    - "Effective pause = BudgetSummary.PausedSeconds + (now - PausedAt) computed before cost calc"
    - "billableSecs = max(0, elapsedSecs - pausedSecs) — clamp prevents negative costs"
    - "RecordPauseStart uses if_not_exists(pausedAt) — idempotent across repeated Lambda ticks"
    - "Non-fatal hook pattern: log warn + continue when RecordPauseStart DynamoDB write fails"

key-files:
  created: []
  modified:
    - cmd/budget-enforcer/main.go
    - cmd/budget-enforcer/main_test.go

key-decisions:
  - "Reorder HandleBudgetCheck: read budget first (moved up), compute effectivePausedSecs, then calculateComputeCost — ensures paused fields are available before cost calc without a double-read"
  - "billableSecs clamped to >= 0 in calculateComputeCost to handle legacy data or clock skew"
  - "RecordPauseStart hook placed only on EC2 stop path — ECS tasks are ephemeral and do not hibernate"
  - "RecordPauseStart failure is non-fatal (warn + continue) — self-corrects on next operator-driven resume"
  - "Declare var err error at top of HandleBudgetCheck after reorder removes the := first-use"

patterns-established:
  - "Budget read before cost calc: always GetBudget first so pause fields are available"
  - "Effective pause computation: closed PausedSeconds + open (now - PausedAt) interval at call site"

requirements-completed:
  - BUDG-PAUSE-03

duration: 20min
completed: 2026-04-23
---

# Phase 60 Plan 03: Budget Enforcer Paused-Interval Accounting Summary

**calculateComputeCost now accepts pausedSeconds and clamps billableSecs to zero; HandleBudgetCheck threads closed+open pause intervals before computing cost; enforceBudgetCompute records pausedAt after StopInstances so hibernated EC2 stops accruing compute spend**

## Performance

- **Duration:** ~20 min
- **Started:** 2026-04-23T01:29:00Z
- **Completed:** 2026-04-23T01:49:11Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments
- `calculateComputeCost` signature changed to `(event BudgetCheckEvent, pausedSeconds int64) (float64, error)` with `billableSecs = max(0, elapsed - paused)` clamp
- `HandleBudgetCheck` reordered: GetBudget first, then effectivePaused = `PausedSeconds + (now - PausedAt)`, then calculateComputeCost — paused sandbox cost stays flat across Lambda ticks
- `enforceBudgetCompute` EC2 branch calls `RecordPauseStart` after `StopInstances` succeeds, before `UpdateSandboxStatusDynamo` — subsequent Lambda invocations hit `if_not_exists` idempotency and do not add new elapsed time
- 8 new tests: subtract, zero-unchanged, never-negative, open-interval-zero, end-to-end paused spend, multi-cycle, RecordsPauseStart assertion, NonFatal DynamoDB error

## Task Commits

Each task was committed atomically:

1. **Task 1: calculateComputeCost signature + HandleBudgetCheck open-interval adjustment** - `4b15233` (feat)
2. **Task 2: Wire RecordPauseStart into enforceBudgetCompute** - `d4ef956` (feat)

## Files Created/Modified
- `cmd/budget-enforcer/main.go` - calculateComputeCost signature, HandleBudgetCheck reorder, RecordPauseStart hook
- `cmd/budget-enforcer/main_test.go` - 8 new tests for paused-accounting and enforce hook

## Decisions Made
- Reordered HandleBudgetCheck so GetBudget runs before calculateComputeCost; avoids a second GetBudget call and keeps the code path linear
- Declared `var err error` block at the top of the reordered section to fix "undefined: err" that arose when the first := use was removed
- RecordPauseStart hook placed on EC2 branch only (ECS is ephemeral, no hibernation concept)
- RecordPauseStart failure is non-fatal — idempotent if_not_exists means the original pausedAt is preserved on re-invocations

## Deviations from Plan

None - plan executed exactly as written. The only unplanned adjustment was declaring `var err error` explicitly after removing the original `:=` first-use when reordering the GetBudget block (minor Go compiler fix, Rule 1).

## Issues Encountered
- `undefined: err` vet error after reorder — fixed by adding `var (budget *awspkg.BudgetSummary; err error)` declaration block (no logic change)

## Next Phase Readiness
- Paused-interval accounting is complete end-to-end: Plan 01 primitives → Plan 02 transition recording → Plan 03 consumer side
- All 8 new tests pass; `go test ./pkg/aws/... ./cmd/budget-enforcer/... -count=1` green
- Pre-existing failures in `cmd/configui` (schema validation) and `cmd/ttl-handler` (IMDS timeout) are unrelated to Phase 60

---
*Phase: 60-budget-compute-accounting-excludes-paused-hibernated-intervals*
*Completed: 2026-04-23*
