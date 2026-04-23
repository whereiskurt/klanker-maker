---
phase: 60-budget-compute-accounting-excludes-paused-hibernated-intervals
plan: 01
subsystem: budget
tags: [dynamodb, pause-accounting, budget, ec2, hibernate]
dependency_graph:
  requires: []
  provides: [BudgetSummary.PausedSeconds, BudgetSummary.PausedAt, RecordPauseStart, RecordResumeClose]
  affects: [pkg/aws/budget.go, Plan 02 pause/resume call sites, Plan 03 budget enforcer Lambda]
tech_stack:
  added: []
  patterns: [DynamoDB if_not_exists idempotency, ADD+REMOVE atomic expression, non-fatal GetItem swallow]
key_files:
  created: []
  modified:
    - pkg/aws/budget.go
    - pkg/aws/budget_test.go
decisions:
  - RecordResumeClose swallows GetItem errors (non-fatal) to match warn-and-continue convention; callers do not need to handle metering errors
  - Negative interval clamped to 0 to handle clock skew between operator workstation and DynamoDB timestamps
  - Single UpdateItem for resume close (ADD+REMOVE atomically) — no two-step update to avoid race condition
metrics:
  duration: ~10min
  completed_date: "2026-04-22"
  tasks_completed: 2
  files_modified: 2
---

# Phase 60 Plan 01: Budget Pause-Interval Accounting Primitives Summary

**One-liner:** DynamoDB pause/resume accounting primitives using if_not_exists + ADD+REMOVE atomic expressions for EC2 hibernation budget exclusion.

## What Was Built

Extended `pkg/aws/budget.go` with two new exported functions and two new `BudgetSummary` fields to support durable pause-interval tracking for EC2 sandboxes.

### New API Surface

- `BudgetSummary.PausedSeconds int64` — cumulative closed-interval pause time in seconds across all pause/resume cycles
- `BudgetSummary.PausedAt *time.Time` — RFC3339 timestamp of current open pause interval (nil when not paused)
- `RecordPauseStart(ctx, client, tableName, sandboxID, now)` — writes `SET pausedAt = if_not_exists(pausedAt, :now)` on BUDGET#compute; idempotent
- `RecordResumeClose(ctx, client, tableName, sandboxID, now)` — reads pausedAt via GetItem, computes interval, issues `ADD pausedSeconds :interval REMOVE pausedAt` atomically

### Safety Properties

- Double-pause: if_not_exists preserves original timestamp
- Double-resume / legacy sandbox: GetItem returns no pausedAt → no-op return nil
- GetItem error: swallowed → return nil (non-fatal, self-healing on next cycle)
- Negative clock skew: interval clamped to 0
- Backward compat: GetBudget returns PausedSeconds=0 / PausedAt=nil for legacy items with no paused* attrs

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| 1 | f092543 | feat(60-01): extend BudgetSummary and GetBudget with paused-interval state |
| 2 | 9cf349e | feat(60-01): implement RecordPauseStart and RecordResumeClose with full test coverage |

## Tests Added

| Test | Coverage |
|------|----------|
| TestGetBudget_PopulatesPausedFields/with_paused_fields | GetBudget parses pausedSeconds and pausedAt from BUDGET#compute |
| TestGetBudget_PopulatesPausedFields/legacy_no_paused_fields | GetBudget returns zero values for legacy items |
| TestRecordPauseStart_WritesIfNotExists | UpdateExpression contains if_not_exists(pausedAt, :now) with RFC3339 :now |
| TestRecordPauseStart_Idempotent | Second call does not overwrite original pausedAt |
| TestRecordResumeClose_NoPausedAtIsNoop | No pausedAt → 0 UpdateItem calls, nil error |
| TestRecordResumeClose_AccumulatesInterval | 1h interval → :interval=3600, ADD+REMOVE expression |
| TestRecordResumeClose_NegativeIntervalClamped | Clock skew → :interval=0 |
| TestMultiplePauseResumeCycles | 3 cycles sum correctly, pausedAt absent after final resume |
| TestRecordResumeClose_GetItemErrorIsNonFatal | GetItem error → nil returned |

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- pkg/aws/budget.go: modified (PausedSeconds, PausedAt fields + GetBudget extension + RecordPauseStart + RecordResumeClose)
- pkg/aws/budget_test.go: modified (TestGetBudget_PopulatesPausedFields + 7 pause/resume tests + fakePauseBudgetClient)
- Commits f092543 and 9cf349e present in git log
- `go test ./pkg/aws/... -count=1` green
- Pre-existing sidecar vet warning in sidecars/http-proxy/httpproxy/transparent.go (out of scope — confirmed pre-existing)
