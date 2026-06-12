---
phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests
plan: 05
subsystem: testing
tags: [go-test, source-grep, create-command, docker-compose, budget-override]

# Dependency graph
requires:
  - phase: 107-01
    provides: baseline failure inventory and test hygiene scope
provides:
  - TestCreateDockerWritesComposeFile passing with PLACEHOLDER_SIDECAR_ROLE_ARN replacing stale PLACEHOLDER_OPERATOR_KEY
  - TestApplyLifecycleOverrides_RunCreateRemoteSignature passing with full current runCreateRemote signature including computeBudgetOverride/aiBudgetOverride
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Source-grep tests must be reconciled whenever create.go signature or token names change"

key-files:
  created: []
  modified:
    - internal/app/cmd/create_docker_test.go
    - internal/app/cmd/create_override_test.go

key-decisions:
  - "Replaced stale PLACEHOLDER_OPERATOR_KEY check with PLACEHOLDER_SIDECAR_ROLE_ARN (a placeholder that actually exists in create.go) to preserve coverage intent"
  - "Updated runCreateRemote signature string verbatim from create.go:2074 to include computeBudgetOverride float64, aiBudgetOverride float64 before clonedFromOverride"

patterns-established: []

requirements-completed: [TEST-HYGIENE-CREATE]

# Metrics
duration: 3min
completed: 2026-06-12
---

# Phase 107 Plan 05: Create Source-Grep Tests Summary

**Docker-compose placeholder check updated to PLACEHOLDER_SIDECAR_ROLE_ARN and runCreateRemote signature assertion extended with budget override params — both source-grep tests now green**

## Performance

- **Duration:** 3 min
- **Started:** 2026-06-12T02:00:00Z
- **Completed:** 2026-06-12T02:03:00Z
- **Tasks:** 1
- **Files modified:** 2

## Accomplishments
- Removed stale `PLACEHOLDER_OPERATOR_KEY` check from `TestCreateDockerWritesComposeFile`; replaced with `PLACEHOLDER_SIDECAR_ROLE_ARN` to keep placeholder-coverage intent
- Updated `TestApplyLifecycleOverrides_RunCreateRemoteSignature` to assert the full current `runCreateRemote` signature, adding `computeBudgetOverride float64, aiBudgetOverride float64` before `clonedFromOverride ...string`
- Both targeted tests green (`ok` + EXIT=0); no production file touched

## Task Commits

1. **Task 1: Reconcile docker-compose placeholder grep + runCreateRemote signature grep** - `a4c4b0c5` (fix)

## Files Created/Modified
- `internal/app/cmd/create_docker_test.go` - Replaced PLACEHOLDER_OPERATOR_KEY with PLACEHOLDER_SIDECAR_ROLE_ARN in checks slice
- `internal/app/cmd/create_override_test.go` - Extended runCreateRemote signature assertion with budget override params

## Decisions Made
- Replaced the removed `PLACEHOLDER_OPERATOR_KEY` entry with `PLACEHOLDER_SIDECAR_ROLE_ARN` rather than just deleting it, to maintain one placeholder coverage check beyond the sandbox role ARN already present — preserves the spirit of the test.
- Signature string copied verbatim from create.go:2074 per plan instruction.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Plans 107-01 through 107-05 complete; remaining stale tests in the phase (agent_auth, email, list, status, uninit) handled in other plans.

---
*Phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests*
*Completed: 2026-06-12*
