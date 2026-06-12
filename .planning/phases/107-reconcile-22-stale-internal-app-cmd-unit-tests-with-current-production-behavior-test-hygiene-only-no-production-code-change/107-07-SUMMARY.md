---
phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests
plan: 07
subsystem: testing
tags: [shell, preflight, cobra, error-handling]

# Dependency graph
requires:
  - phase: 107-plans-01-through-06
    provides: "stale test reconciliation for all non-production files; shell test assertions already correct"
provides:
  - "preflightError sentinel type in shell.go distinguishing pre-flight from session-exit errors"
  - "NewShellCmdWithFetcher RunE returns pre-flight errors, swallows only session-exit errors"
  - "TestShellCmd_StoppedSandbox, _UnknownSubstrate, _MissingInstanceID all pass"
affects: [km-shell-ux, test-hygiene-phase-107]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Sentinel wrapper type (preflightError) to distinguish error categories across a call boundary"
    - "errors.As discrimination in RunE: propagate pre-flight errors, swallow session-exit errors"

key-files:
  created: []
  modified:
    - internal/app/cmd/shell.go

key-decisions:
  - "Use a preflightError wrapper struct (not a sentinel var) so Unwrap() preserves the original error message for substring assertions in tests"
  - "Wrap all five pre-flight return sites in runShellWithSSM (fetch error, stopped, missing EC2 instance, missing ECS cluster/task, unsupported substrate default)"
  - "Leave newShellCmdWithSSM (test-injection constructor) untouched — it already returns runErr directly"
  - "learn path in NewShellCmdWithFetcher left untouched — it returns runErr already"

patterns-established:
  - "Shell pre-flight vs session-exit error discrimination pattern: tag at source, check at RunE boundary"

requirements-completed: [TEST-HYGIENE-SHELL-FIX]

# Metrics
duration: 5min
completed: 2026-06-12
---

# Phase 107 Plan 07: Shell Pre-flight Error Fix Summary

**`preflightError` sentinel type added to shell.go so `km shell` on a stopped/unknown-substrate/no-instance sandbox returns a meaningful error instead of silently exiting 0**

## Performance

- **Duration:** ~5 min
- **Started:** 2026-06-12T02:04:00Z
- **Completed:** 2026-06-12T02:09:18Z
- **Tasks:** 1
- **Files modified:** 1 (internal/app/cmd/shell.go) + VERSION (auto-bumped by make build)

## Accomplishments

- Added `preflightError` wrapper struct with `Error()` and `Unwrap()` methods to shell.go
- Wrapped all five pre-flight return sites in `runShellWithSSM` with `&preflightError{...}` (fetch error, stopped sandbox, missing EC2 instance ARN, missing ECS cluster/task ARN, unsupported substrate)
- Replaced unconditional `return nil` in `NewShellCmdWithFetcher` RunE with `errors.As` discrimination: pre-flight errors propagate, session-exit errors from execFn are still swallowed
- Added `"errors"` to import block (was not previously imported)
- `TestShellCmd_StoppedSandbox`, `_UnknownSubstrate`, `_MissingInstanceID` all pass
- `TestShellCmd_EC2`, `_EC2_Root`, `_ECS`, `_MissingSSMDoc` all remain green
- `TestShellDocker*` unaffected

## Task Commits

1. **Task 1: Tag pre-flight errors and return them from NewShellCmdWithFetcher RunE** - `8cefdf13` (fix)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/internal/app/cmd/shell.go` - Added preflightError type; wrapped pre-flight returns; discriminating RunE

## Decisions Made

- Used a struct type `preflightError` (not `errors.New` sentinel) so `Unwrap()` preserves the original fmt.Errorf message verbatim — the escalation tests do substring checks on exact strings like "stopped" and "k8s"
- Wrapped ECS cluster and ECS task ARN errors (lines ~398-403) as pre-flight in addition to the explicitly-listed three targets, for completeness and consistency
- Left `newShellCmdWithSSM` (test-injection constructor, lines 178-211) completely untouched — its existing `return runErr` already propagates everything correctly

## Deviations from Plan

None — plan executed exactly as written. The recommended `preflightError` struct approach was applied verbatim.

## Issues Encountered

None. `make build` succeeded on the first attempt. All targeted tests green on the first run.

## Next Phase Readiness

Phase 107 now has all 22 stale tests reconciled. The full `go test ./internal/app/cmd/ -count=1` suite should be clean. Plans 01-06 handled the 19 stale-test-only reconciliations; Plan 07 delivers the one approved production fix.

---
*Phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests*
*Completed: 2026-06-12*
