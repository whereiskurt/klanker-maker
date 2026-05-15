---
phase: 77-failed-sandbox-discoverability-persist-failure-reason-in-ddb-and-km-logs-lambda-fallback
plan: 4
subsystem: cloudwatch-fallback
tags: [cloudwatchlogs, km-logs, fallback, multi-instance, tdd, resourcenotfoundexception]

# Dependency graph
requires:
  - 77-00  # CWLogsAPI interface + NewLogsCmdWithClient DI seam
  - 77-01  # failure_reason schema (provides context for the empty-state hint)
provides:
  - FilterCreateHandlerLogs helper in pkg/aws/cloudwatch.go
  - ResourceNotFoundException-triggered fallback in runLogs
  - runLogsLambdaFallback private helper in logs.go
  - Four pkg/aws tests for FilterCreateHandlerLogs
  - Four internal/app/cmd/logs tests for fallback paths
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "errors.As(err, &notFound) for typed ResourceNotFoundException detection through %w wrapping"
    - "prefix parameterization via cfg.GetResourcePrefix() → FilterCreateHandlerLogs(prefix, ...) → /aws/lambda/<prefix>-create-handler"

key-files:
  created: []
  modified:
    - pkg/aws/cloudwatch.go
    - pkg/aws/cloudwatch_test.go
    - internal/app/cmd/logs.go
    - internal/app/cmd/logs_test.go

key-decisions:
  - "errors.As used (not errors.Is) for ResourceNotFoundException — errors.Is does NOT unwrap %w-wrapped typed errors (77-RESEARCH.md Pitfall 5)"
  - "--follow short-circuits BEFORE FilterLogEvents call so mock.filterLogEventsInput == nil is a reliable gate"
  - "Pagination intentionally omitted for 24h / single-sandbox-id window (77-RESEARCH.md Open Q1)"

# Metrics
duration: 631s
completed: 2026-05-15
---

# Phase 77 Plan 04: Lambda Log Fallback Summary

**`km logs <id>` falls back to `/aws/lambda/<prefix>-create-handler` when the per-sandbox log group doesn't exist — implemented via `FilterCreateHandlerLogs` helper + `runLogsLambdaFallback` with full multi-instance support and four TDD-driven test scenarios**

## Performance

- **Duration:** 631s (~10 min)
- **Started:** 2026-05-15T00:10:54Z
- **Completed:** 2026-05-15T00:21:25Z
- **Tasks:** 2 (both TDD)
- **Files modified:** 4

## Accomplishments

- Added `FilterCreateHandlerLogs(ctx, client, prefix, sandboxID) ([]LogEvent, error)` to `pkg/aws/cloudwatch.go`
  - Constructs `/aws/lambda/<prefix>-create-handler` log group from the prefix argument
  - Uses 24h time window (`now-24h` to `now`) as UnixMilli timestamps
  - Filter pattern exactly: `` { $.sandbox_id = "<sandboxID>" } `` (backtick-quoted in Go)
  - Returns empty non-nil slice when no events match (matches GetLogEvents convention)
  - Wraps SDK errors so `errors.Is` unwraps to the original sentinel

- Modified `runLogs` in `internal/app/cmd/logs.go` to detect `*ResourceNotFoundException` via `errors.As` and dispatch to `runLogsLambdaFallback`
  - Non-ResourceNotFoundException errors continue to surface as `fmt.Errorf("tail logs for sandbox %s: %w", ...)` — fallback is strictly typed-error gated
  - `errors.As` unwraps correctly through `TailLogs` → `GetLogEvents`'s `fmt.Errorf(...%w...)` wrapper

- Added `runLogsLambdaFallback` private helper to `internal/app/cmd/logs.go`
  - Prints prelude: `── per-sandbox log group not found; falling back to create-handler Lambda ──` (U+2500 box-drawing characters, not ASCII dashes)
  - `--follow` short-circuit: prints note, returns nil, does NOT call FilterLogEvents
  - Empty result: prints `No create-handler activity found for <id> in the last 24h. Try km status <id> for the persisted failure reason.`
  - Populated result: prints `<RFC3339-ts> <raw-Message-field>` for each event chronologically

## Exact Output Strings (for UAT grep)

**Prelude line:**
```
── per-sandbox log group not found; falling back to create-handler Lambda ──
```
(Characters: U+2500 U+2500 space ... space U+2500 U+2500)

**Empty-state hint** (sandboxID = `learn-abc12345`):
```
No create-handler activity found for learn-abc12345 in the last 24h. Try km status learn-abc12345 for the persisted failure reason.
```

**Follow-fallback note** (sandboxID = `learn-abc12345`):
```
--follow is not supported in fallback mode (failure is terminal); use km status learn-abc12345 for the persisted reason.
```

## FilterCreateHandlerLogs Signature

```go
func FilterCreateHandlerLogs(ctx context.Context, client CWLogsAPI, prefix, sandboxID string) ([]LogEvent, error)
```

Located at: `pkg/aws/cloudwatch.go:229` (declaration line)

## Multi-Instance Gate Trace

```
km logs <id>
  → runLogs: cfg.GetResourcePrefix() → "kph" (when KM_RESOURCE_PREFIX=kph)
    → TailLogs returns ResourceNotFoundException
      → runLogsLambdaFallback(cfg=..., follow=false)
        → kmaws.FilterCreateHandlerLogs(ctx, client, "kph", sandboxID)
          → FilterLogEvents: LogGroupName = "/aws/lambda/kph-create-handler"
```

## Fallback Dispatch Line Range

In `internal/app/cmd/logs.go`, the fallback dispatch is at lines ~81-89:
```go
var notFound *cloudwatchlogstypes.ResourceNotFoundException
if errors.As(err, &notFound) {
    return runLogsLambdaFallback(cmd, cfg, cwClient, sandboxID, follow)
}
```

## Task Commits

Each task was committed atomically:

1. **Task 1: FilterCreateHandlerLogs helper in pkg/aws + tests** - `ced58b2` (feat)
2. **Task 2: Wire fallback into runLogs + runLogsLambdaFallback + four logs tests** - `d9c36df` (feat)

## Files Modified

- `pkg/aws/cloudwatch.go` — Added `FilterCreateHandlerLogs` (lines 212–257)
- `pkg/aws/cloudwatch_test.go` — Added `TestFilterCreateHandlerLogs_{HappyPath,Empty,MultiInstancePrefix,PropagatesError}`
- `internal/app/cmd/logs.go` — Added `cloudwatchlogstypes` import + `time` import; modified `runLogs` fallback dispatch; added `runLogsLambdaFallback`
- `internal/app/cmd/logs_test.go` — Added `errors` import; added `TestLogsCmd_{FallbackWithEvents,FallbackBothEmpty,FallbackFollow_NoOp,NonNotFoundError_Surfaces}`

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

All files exist: cloudwatch.go, cloudwatch_test.go, logs.go, logs_test.go, 77-04-SUMMARY.md.
All commits exist: ced58b2 (Task 1), d9c36df (Task 2).

---
*Phase: 77-failed-sandbox-discoverability-persist-failure-reason-in-ddb-and-km-logs-lambda-fallback*
*Completed: 2026-05-15*
