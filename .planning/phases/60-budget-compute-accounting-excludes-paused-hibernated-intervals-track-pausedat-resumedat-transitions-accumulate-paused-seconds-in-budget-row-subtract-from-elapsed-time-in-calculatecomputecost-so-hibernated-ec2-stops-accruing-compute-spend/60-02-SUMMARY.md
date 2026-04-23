---
phase: 60-budget-compute-accounting-excludes-paused-hibernated-intervals
plan: 02
subsystem: budget-pause-hooks
tags: [budget, pause, resume, ec2, ttl-handler, compute-accounting]
depends_on:
  requires: [60-01]
  provides: [60-03]
  affects: [internal/app/cmd/pause.go, internal/app/cmd/resume.go, internal/app/cmd/budget.go, cmd/ttl-handler/main.go]
tech-stack:
  added: []
  patterns: [warn-and-continue, non-fatal hook, exported helper for testability]
key-files:
  created: []
  modified:
    - internal/app/cmd/pause.go
    - internal/app/cmd/resume.go
    - internal/app/cmd/budget.go
    - internal/app/cmd/pause_test.go
    - cmd/ttl-handler/main.go
decisions:
  - "Extracted RecordPauseForEC2 as an exported helper in pause.go for testability (runPause constructs AWS clients internally, making direct unit testing impractical)"
  - "Extended resumeEC2Sandbox signature with budgetClient+budgetTable (single caller, cleaner than post-call hook)"
  - "All budget hooks are non-fatal: DynamoDB error logs Warn and lifecycle continues"
  - "BudgetClient in TTLHandler reuses existing dynamoClient — no second DynamoDB client construction"
  - "TTL-handler hooks guarded by nil-check on BudgetClient+BudgetTable for backward compatibility with existing tests"
metrics:
  duration: 743s
  completed: 2026-04-22
  tasks: 2
  files: 5
---

# Phase 60 Plan 02: Pause/Resume Budget Hook Wiring Summary

**One-liner:** Non-fatal RecordPauseStart/RecordResumeClose hooks wired at all 6 EC2 pause/resume transition sites (km pause, km resume, km budget add, ttl-handler handleStop/handleResume/handleAgentRun).

## What Was Built

Plan 02 is the glue plan — no business logic, only hook wiring. Every EC2 pause/resume path now calls the Plan 01 helpers to maintain accurate paused-interval accounting in the km-budgets DynamoDB table.

### Pause hooks wired (RecordPauseStart)

| Site | File | Placement |
|------|------|-----------|
| `km pause` (EC2 path) | `internal/app/cmd/pause.go:runPause` | After StopInstances loop, before UpdateSandboxStatusAndClearTTL |
| TTL-handler idle-hibernate | `cmd/ttl-handler/main.go:handleStop` | After stop loop, when status=="paused", before UpdateSandboxStatusAndClearTTL |

### Resume hooks wired (RecordResumeClose)

| Site | File | Placement |
|------|------|-----------|
| `km resume` (EC2 path) | `internal/app/cmd/resume.go:runResume` | After StartInstances loop, before UpdateSandboxStatusDynamo |
| `km budget add` auto-resume | `internal/app/cmd/budget.go:resumeEC2Sandbox` | After StartInstances succeeds, before return true |
| TTL-handler handleResume | `cmd/ttl-handler/main.go:handleResume` | After StartInstances loop, before UpdateSandboxStatusDynamo |
| TTL-handler handleAgentRun auto-start | `cmd/ttl-handler/main.go:handleAgentRun` | After instance reaches running state, before UpdateSandboxStatusDynamo |

### Substrate gating

- Docker substrate: excluded (pause.go returns early before EC2 path)
- ECS substrate: excluded (budget.go has separate ECS branch that does not call resumeEC2Sandbox)
- TTL-handler handleStop: EC2-only by construction (handleStop uses EC2 DescribeInstances directly)

### Test added

`TestPauseRecordsTimestamp` in `internal/app/cmd/pause_test.go`: verifies that `RecordPauseForEC2` (exported helper in pause.go) issues a DynamoDB UpdateItem with `SET pausedAt = if_not_exists(pausedAt, :now)` using a fake `BudgetAPI`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing critical functionality] Exported RecordPauseForEC2 helper for testability**
- **Found during:** Task 1
- **Issue:** `runPause` constructs AWS clients internally (not injectable), making it impossible to unit test the pause hook without DI surgery
- **Fix:** Extracted `RecordPauseForEC2(ctx, client, table, sandboxID)` as an exported helper in pause.go; `runPause` calls the helper; `TestPauseRecordsTimestamp` tests the helper directly via a fake BudgetAPI
- **Files modified:** `internal/app/cmd/pause.go`, `internal/app/cmd/pause_test.go`
- **Commit:** 809d54f

None other — plan executed as specified.

## Self-Check: PASSED

All files exist:
- FOUND: internal/app/cmd/pause.go
- FOUND: internal/app/cmd/resume.go
- FOUND: internal/app/cmd/budget.go
- FOUND: internal/app/cmd/pause_test.go
- FOUND: cmd/ttl-handler/main.go

All commits exist:
- FOUND: 809d54f (feat(60-02): wire pause/resume - cmd package)
- FOUND: 1174aee (feat(60-02): wire pause/resume - ttl-handler)
