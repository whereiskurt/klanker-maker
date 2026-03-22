---
phase: 03-sidecar-enforcement-lifecycle-management
plan: "03"
subsystem: observability
tags: [otel, opentelemetry, mlflow, s3, tracing, experiment-tracking, awss3exporter]

requires:
  - phase: 02-core-provisioning-security-baseline
    provides: S3 bucket infrastructure and AWS SDK patterns used by MLflow S3 run logging
  - phase: 03-00-PLAN
    provides: stub files (mlflow_test.go, scheduler_test.go) and pkg/aws package structure

provides:
  - OTel Collector Contrib sidecar config (sidecars/tracing/config.yaml) — OTLP receiver + awss3exporter
  - MLflow S3-backed run logging package (pkg/aws/mlflow.go) — WriteMLflowRun / FinalizeMLflowRun
  - S3RunAPI interface for narrow, testable S3 access in MLflow operations

affects:
  - 03-01-PLAN (DNS proxy sidecar — shares sidecars/ directory structure)
  - 03-02-PLAN (HTTP proxy sidecar — shares sidecars/ directory structure)
  - 03-04-PLAN (audit log sidecar — shares pkg/aws patterns)
  - 03-05-PLAN (lifecycle management — calls WriteMLflowRun/FinalizeMLflowRun from km create/destroy)

tech-stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/s3 v1.97.1
  patterns:
    - Narrow S3RunAPI interface (PutObject + GetObject) for mock-based unit testing without AWS credentials
    - MLflowRun pointer field (*int for ExitStatus) to preserve zero value exit_status=0 through JSON omitempty
    - OTel Collector Contrib v0.118.0+ config format with env-var substitution for runtime values

key-files:
  created:
    - sidecars/tracing/config.yaml
    - pkg/aws/mlflow.go
  modified:
    - pkg/aws/mlflow_test.go (replaced stub with real test logic)
    - go.mod (added aws-sdk-go-v2/service/s3)
    - go.sum (updated)

key-decisions:
  - "ExitStatus stored as *int in MLflowRun so exit_status=0 (success) is preserved through JSON omitempty serialization"
  - "S3RunAPI interface is narrow (PutObject + GetObject only) — callers needing full s3.Client can satisfy it directly since *s3.Client implements both"
  - "OTel config uses env-var substitution (${AWS_REGION}, ${OTEL_S3_BUCKET}, ${SANDBOX_ID}) — zero runtime configuration code needed"

patterns-established:
  - "S3RunAPI narrow interface pattern: define minimal interface per feature, mock struct in _test.go file"
  - "MLflow file store as JSON: single meta.json with params+metrics, written at create and updated at finalize"

requirements-completed:
  - OBSV-08
  - OBSV-09

duration: 7min
completed: 2026-03-22
---

# Phase 03 Plan 03: OTel Tracing Sidecar Config and MLflow S3 Run Logging Summary

**OTel Collector sidecar config (OTLP gRPC/HTTP -> awss3exporter with otlp_json) and S3-backed MLflow run logging (WriteMLflowRun/FinalizeMLflowRun) using narrow S3RunAPI interface, zero infrastructure required**

## Performance

- **Duration:** ~7 min
- **Started:** 2026-03-22T04:44:58Z
- **Completed:** 2026-03-22T04:52:00Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments

- Created OTel Collector Contrib sidecar config for the tracing sidecar — OTLP receivers on 4317/4318, awss3exporter with otlp_json marshaler, batch processor, env-var substitution for region/bucket/sandbox-id
- Implemented MLflow S3 run logging package: S3RunAPI interface, MLflowRun struct with params (set at create) and metrics (set at finalize), WriteMLflowRun writes meta.json at creation, FinalizeMLflowRun reads-modifies-writes at teardown
- All 4 TDD tests pass with mock S3 client: key path verification, JSON field validation, metrics update, error wrapping

## Task Commits

1. **Task 1: OTel Collector sidecar configuration (OBSV-08)** - `6abf41d` (feat)
2. **Task 2 RED: Failing tests for MLflow S3 run logging (OBSV-09)** - `9fa4b11` (test)
3. **Task 2 GREEN: MLflow S3 run logging implementation** - `dd61fe3` (feat)

**Plan metadata:** (docs commit — see state updates)

_Note: TDD task has two commits (test RED -> feat GREEN). No refactor needed._

## Files Created/Modified

- `sidecars/tracing/config.yaml` - OTel Collector Contrib config with OTLP receiver + awss3exporter
- `pkg/aws/mlflow.go` - S3RunAPI interface, MLflowRun struct, WriteMLflowRun, FinalizeMLflowRun
- `pkg/aws/mlflow_test.go` - Real test logic replacing stubs: mockS3RunAPI + 4 test functions
- `go.mod` / `go.sum` - Added aws-sdk-go-v2/service/s3 v1.97.1

## Decisions Made

- ExitStatus as `*int` so exit_status=0 (success exit code) is not dropped by JSON `omitempty`
- S3RunAPI kept narrow (2 methods) — real `*s3.Client` satisfies it without any adapter
- OTel config relies entirely on env-var substitution — no Go config parsing needed for the sidecar

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed exit_status=0 (success) dropped by JSON omitempty**
- **Found during:** Task 2 GREEN (TestFinalizeMLflowRun_UpdatesMetrics failed)
- **Issue:** `ExitStatus int` with `omitempty` serializes exit_status=0 as absent; test checked for float64(0) but key was nil
- **Fix:** Changed ExitStatus to `*int` — nil means not-yet-finalized, &0 means success, &nonzero means failure
- **Files modified:** pkg/aws/mlflow.go
- **Verification:** TestFinalizeMLflowRun_UpdatesMetrics passes, all 4 tests green
- **Committed in:** dd61fe3 (Task 2 implementation commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - bug)
**Impact on plan:** Required for correctness — exit_status=0 is the most common success value and must be persisted. No scope creep.

## Issues Encountered

- Pre-existing build failure in `sidecars/audit-log/cmd/main.go` (references `kmaws.CWLogsAPI`, `kmaws.EnsureLogGroup`, etc. not yet implemented). Logged to `deferred-items.md`. Not caused by this plan's changes.
- Scheduler stub tests (`TestCreateTTLSchedule_*`, `TestDeleteTTLSchedule_*`) remain failing — these are pre-existing stubs from plan 03-00, to be implemented in a later plan.

## User Setup Required

None — no external service configuration required. OTel config uses env-var substitution at runtime. MLflow tests use mock S3 client.

## Next Phase Readiness

- `sidecars/tracing/config.yaml` is ready to be embedded in EC2 user-data or ECS task definition
- `WriteMLflowRun` / `FinalizeMLflowRun` are ready to be called from `km create` / `km destroy` (Plan 03-05)
- S3RunAPI can be satisfied by wrapping any `*s3.Client` from `pkg/aws/client.go`
- Pre-existing audit-log build errors need resolution before `go build ./...` passes cleanly

---
*Phase: 03-sidecar-enforcement-lifecycle-management*
*Completed: 2026-03-22*

## Self-Check: PASSED

- sidecars/tracing/config.yaml: FOUND
- pkg/aws/mlflow.go: FOUND
- pkg/aws/mlflow_test.go: FOUND
- 03-03-SUMMARY.md: FOUND
- Commit 6abf41d (OTel config): FOUND
- Commit 9fa4b11 (RED tests): FOUND
- Commit dd61fe3 (GREEN implementation): FOUND
