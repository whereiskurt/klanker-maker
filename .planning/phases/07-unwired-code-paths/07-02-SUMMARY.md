---
phase: 07-unwired-code-paths
plan: "02"
subsystem: observability
tags: [mlflow, s3, terragrunt, site-hcl, audit, profile-inheritance, builtins]

# Dependency graph
requires:
  - phase: 03-sidecar-enforcement-lifecycle-management
    provides: WriteMLflowRun and FinalizeMLflowRun functions in pkg/aws/mlflow.go
  - phase: 06-budget-enforcement-platform-configuration
    provides: Config.Load() with KM_ACCOUNTS_* env var mapping via viper AutomaticEnv

provides:
  - MLflow session tracking wired into km create (WriteMLflowRun at Step 11a)
  - MLflow session finalization wired into km destroy (FinalizeMLflowRun at Step 8a)
  - site.hcl exposes account IDs via get_env() for Terragrunt configs
  - SCHM-04 and SCHM-05 formally verified with passing tests

affects:
  - phase: 09-live-infra (site.hcl accounts block consumed by multi-account Terragrunt configs)
  - phase: 08-onwards (MLflow runs now captured for every sandbox lifecycle event)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Non-fatal S3 audit write: log.Warn + continue, never return err — consistent with metadata and TTL schedule pattern"
    - "Source-level verification tests: os.ReadFile(source_file) + strings.Contains for call site presence checks"
    - "site.hcl get_env() pattern extended to accounts block; env var names match viper SetEnvPrefix('KM')+AutomaticEnv() mapping"

key-files:
  created:
    - internal/app/cmd/create_test.go (TestRunCreate_MLflow added)
    - internal/app/cmd/destroy_test.go (TestRunDestroy_MLflow added)
  modified:
    - internal/app/cmd/create.go (Step 11a: WriteMLflowRun wired after s3Client construction)
    - internal/app/cmd/destroy.go (Step 8a: FinalizeMLflowRun wired after ExecuteTeardown)
    - infra/live/site.hcl (accounts block with KM_ACCOUNTS_MANAGEMENT/TERRAFORM/APPLICATION)
    - .planning/REQUIREMENTS.md (SCHM-04 and SCHM-05 marked complete)

key-decisions:
  - "MLflow writes placed after s3Client declaration to avoid nil pointer — same s3Client used for profile YAML storage"
  - "FinalizeMLflowRun placed after ExecuteTeardown (not after CleanupSandboxDir) — ensures audit record written before local cleanup"
  - "ExitStatus=0 hardcoded in destroy finalize — destroy is only called on success path (error returns early before Step 8a)"
  - "site.hcl accounts use empty string defaults — modules consuming account IDs not yet deployed in live (Phase 9 concern)"
  - "SCHM-04/SCHM-05 require no code changes — tests exist and pass; Phase 7 simply verifies and marks complete"

patterns-established:
  - "TDD source-level verification: write test that reads .go source and checks for call site patterns before wiring production code"
  - "Non-fatal audit path: all S3 audit writes (MLflow, metadata, profile) follow log.Warn pattern — sandbox lifecycle never blocked by audit failures"

requirements-completed: [OBSV-09, CONF-03, SCHM-04, SCHM-05]

# Metrics
duration: 3min
completed: 2026-03-22
---

# Phase 07 Plan 02: Unwired Code Paths — MLflow Wiring + site.hcl Account IDs Summary

**MLflow session tracking wired into km create/destroy via WriteMLflowRun/FinalizeMLflowRun, site.hcl extended with KM_ACCOUNTS_* env var block, and SCHM-04/SCHM-05 formally verified via existing inherit_test.go and builtins_test.go**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-03-22T22:20:54Z
- **Completed:** 2026-03-22T22:24:11Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments

- Wired `WriteMLflowRun` into `create.go` Step 11a — every `km create` now records an MLflow run to S3 with sandbox metadata (OBSV-09)
- Wired `FinalizeMLflowRun` into `destroy.go` Step 8a — every successful `km destroy` closes the MLflow run with ExitStatus=0
- Added `accounts` block to `site.hcl` with `get_env()` calls for KM_ACCOUNTS_MANAGEMENT, KM_ACCOUNTS_TERRAFORM, KM_ACCOUNTS_APPLICATION (CONF-03)
- Marked SCHM-04 (profile inheritance) and SCHM-05 (four built-in profiles) as complete in REQUIREMENTS.md after verifying all tests pass

## Task Commits

Each task was committed atomically:

1. **Task 1 RED: Failing MLflow tests** - `4bd6d95` (test)
2. **Task 1 GREEN: Wire MLflow into create.go and destroy.go** - `1c073c5` (feat)
3. **Task 2: site.hcl accounts + SCHM-04/SCHM-05 verification** - `98ca2d0` (feat)

**Plan metadata:** (docs commit — follows)

_Note: TDD task has separate RED (test) and GREEN (feat) commits_

## Files Created/Modified

- `internal/app/cmd/create.go` - Added Step 11a: WriteMLflowRun call (non-fatal) after s3Client construction
- `internal/app/cmd/destroy.go` - Added Step 8a: FinalizeMLflowRun call (non-fatal) after ExecuteTeardown
- `internal/app/cmd/create_test.go` - Added TestRunCreate_MLflow source-level verification test
- `internal/app/cmd/destroy_test.go` - Added TestRunDestroy_MLflow source-level verification test
- `infra/live/site.hcl` - Added accounts block with three KM_ACCOUNTS_* env var reads
- `.planning/REQUIREMENTS.md` - SCHM-04 and SCHM-05 marked [x] complete in checklist and traceability table

## Decisions Made

- MLflow calls are non-fatal throughout — consistent with existing metadata write and TTL schedule patterns in the codebase; sandbox lifecycle must not be blocked by observability failures
- Source-level verification test pattern used for MLflow wiring — reads .go source file and checks for call site patterns; actual S3 write behavior tested in pkg/aws/mlflow_test.go
- ExitStatus=0 hardcoded in FinalizeMLflowRun call — destroy only reaches Step 8a on the success path (errors return before that point)
- site.hcl account IDs default to empty string — consuming modules not yet deployed (Phase 9), empty string is intentional placeholder

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None — all tests passed on first run after wiring. The `go test -run TestMLflow` pattern initially showed "no tests to run" due to Go test filtering behavior with external test packages; running `TestRunCreate_MLflow|TestRunDestroy_MLflow` resolved it.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- MLflow session tracking now active for all sandbox lifecycle events
- site.hcl ready for Phase 9 live infra deployment — operators set KM_ACCOUNTS_* env vars in their environment
- SCHM-04/SCHM-05 requirements closed; profile inheritance and builtins fully verified

---
*Phase: 07-unwired-code-paths*
*Completed: 2026-03-22*
