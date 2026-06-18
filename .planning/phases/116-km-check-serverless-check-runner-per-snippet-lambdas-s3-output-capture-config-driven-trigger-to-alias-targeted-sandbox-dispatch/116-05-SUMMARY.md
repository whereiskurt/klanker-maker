---
phase: 116-km-check-serverless-check-runner
plan: 05
subsystem: infra
tags: [lambda, dynamodb, ecr, s3, python, km-check, serverless, check-runner, pkg-check]

# Dependency graph
requires:
  - phase: 116-03
    provides: ChecksConfig/CheckTrigger structs in internal/app/config/config.go
  - phase: 116-04
    provides: _km_check_bootstrap.py Lambda handler + KM_CHECK_TRIGGER schema

provides:
  - pkg/check/ package: Lambda CRUD, zip packaging, ECR lazy-create, DDB row CRUD, trigger bake + sourceHash
  - internal/app/cmd/check.go: km check deploy|run|ls|get|logs|schedule|sync|rm CLI
  - Embedded bootstrap (BootstrapBytes()) with byte-identity assertion

affects: [116-06, 116-07, 116-08]

# Tech tracking
tech-stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/ecr v1.58.4 (lazy ECR repo creation)
  patterns:
    - "TDD (RED then GREEN): check_test.go written first with failing imports, then pkg/check/trigger.go to pass"
    - "UpdateItem-never-PutItem on existing DDB rows (project_sandboxmetadata_lossy_roundtrip)"
    - "go:embed for Python bootstrap bytes (BootstrapBytes() + AssertBootstrapByteIdentity())"
    - "BakeTrigger(@file resolution): @prefix signals operator-side file read at deploy/sync time"
    - "sourceHash = SHA-256 hex of resolved KM_CHECK_TRIGGER JSON for drift detection"

key-files:
  created:
    - pkg/check/trigger.go
    - pkg/check/bootstrap.go
    - pkg/check/_km_check_bootstrap.py
    - pkg/check/lambda.go
    - pkg/check/package.go
    - pkg/check/ecr.go
    - pkg/check/ddb.go
    - internal/app/cmd/check.go
    - internal/app/cmd/check_test.go
  modified:
    - internal/app/cmd/root.go (NewCheckCmd registration near NewH1Cmd)
    - go.mod / go.sum (added ecr dependency)

key-decisions:
  - "Lambda env vars set on every check: KM_CHECK_NAME, KM_ARTIFACTS_BUCKET, KM_CHECK_TRIGGER, KM_CHECK_SECRET_PATHS (+ user --env K=V)"
  - "DDB row uses UpdateItem (never PutItem) on existing rows to prevent lossy round-trip overwrites"
  - "sourceHash covers the fully resolved (post-@file) KM_CHECK_TRIGGER JSON, not raw config text"
  - "on_absent defaults to cold-create when empty (matches 116-04 schema default)"
  - "ECR dependency added to go.mod; not previously in the module"
  - "EventBridge Scheduler create/delete stubs for compile-time completeness; live deploy tested in 116-08"

patterns-established:
  - "BakeTrigger as the single resolve+marshal+hash point for KM_CHECK_TRIGGER (CLI and sync both call it)"
  - "checkRowInputFromRow helper converts CheckRow back to CheckRowInput for UpdateCheckRow (preserves attrs)"
  - "drift detection in km check ls: current bake hash vs stored source_hash in DDB"

requirements-completed: []

# Metrics
duration: 8min
completed: 2026-06-18
---

# Phase 116 Plan 05: km check CLI + pkg/check Package Summary

**`km check` CLI family + `pkg/check/` package: per-check Lambda CRUD via SDK, arm64 zip packaging with pip wheels, lazy ECR repo for --image, DDB row CRUD with UpdateItem-safe round-trips, and KM_CHECK_TRIGGER bake (inline + @file) with sourceHash drift detection**

## Performance

- **Duration:** 8 min
- **Started:** 2026-06-18T00:55:17Z
- **Completed:** 2026-06-18T01:03:15Z
- **Tasks:** 3 (TDD task 1, SDK task 2, CLI task 3)
- **Files modified:** 11

## Accomplishments
- `pkg/check/trigger.go`: `BakeTrigger()` resolves `@file` references operator-side at deploy/sync time and marshals the canonical KM_CHECK_TRIGGER JSON per the 116-04 schema; `sourceHash` = SHA-256 hex of the resolved JSON for drift detection
- `pkg/check/lambda.go`: `DeployFunction` (create if absent, else UpdateFunctionCode + UpdateFunctionConfiguration two-call); `UpdateTriggerEnv`, `InvokeFunction`, `DeleteFunction` via `lambdapkg.NewFromConfig`
- `pkg/check/ddb.go`: `PutCheckRow`/`UpdateCheckRow`(UpdateItem)/`GetCheckRow`/`ListCheckRows`/`DeleteCheckRow` on `{prefix}-checks`; full row schema with JSON-encoded env/secretPaths
- `pkg/check/ecr.go`: `EnsureECRRepo` — idempotent `CreateRepository` + `SetRepositoryPolicy` granting `lambda.amazonaws.com` pull access
- `pkg/check/package.go`: `BuildZip` with `pip install --platform manylinux2014_aarch64 --only-binary :all:` for arm64; `MaybeUploadLargeZip` (>50 MB → S3 pre-upload)
- `pkg/check/bootstrap.go`: `BootstrapBytes()` via `go:embed`; `AssertBootstrapByteIdentity()` for test-time byte-identity verification
- `km check` CLI: all 8 subcommands registered and verified with `km check --help`

## Lambda Environment Variables Set

Every check Lambda receives these env vars at `km check deploy`:

| Var | Value |
|-----|-------|
| `KM_CHECK_NAME` | check name (e.g. `qotd`) |
| `KM_ARTIFACTS_BUCKET` | `cfg.ArtifactsBucket` (e.g. `km-artifacts-123456789`) |
| `KM_CHECK_TRIGGER` | resolved KM_CHECK_TRIGGER JSON (from `BakeTrigger`, empty when no trigger configured) |
| `KM_CHECK_SECRET_PATHS` | JSON list of SSM paths from `--secret` flags (e.g. `["/km/checks/api-key"]`) |
| user `--env K=V` | operator-supplied static env overrides |

`KM_CHECK_TRIGGER` is absent when no matching `checks.triggers` entry exists for the check (capture-only mode, trigger never fires). `KM_CHECK_SECRET_PATHS` is absent when no `--secret` flags are set.

## DDB Row Schema (`{prefix}-checks`)

| Attribute | DynamoDB type | Description |
|-----------|--------------|-------------|
| `name` | S (hash key) | Check name (e.g. `qotd`) |
| `arn` | S | Lambda function ARN |
| `runtime` | S | `python3.13` (zip); empty for image |
| `package_type` | S | `zip` or `image` |
| `image_uri` | S | ECR image URI (image checks only) |
| `memory` | N | Lambda memory in MB |
| `timeout` | N | Lambda timeout in seconds |
| `schedule` | S | EventBridge Scheduler expression or empty |
| `env` | S | JSON map of non-secret `--env K=V` pairs |
| `secret_paths` | S | JSON list of SSM paths (no values, ever) |
| `source_hash` | S | SHA-256 of resolved KM_CHECK_TRIGGER JSON |
| `trigger_summary` | S | Human-readable trigger (e.g. `alias=nightly-auditor cooldown=3600s`) |
| `created_at` | S | ISO-8601 creation timestamp |
| `updated_at` | S | ISO-8601 last-update timestamp |

**UpdateItem on existing rows** (never PutItem) preserves any attributes we don't own, preventing the `project_sandboxmetadata_lossy_roundtrip` data-loss footgun.

## @file Resolution

`BakeTrigger(t config.CheckTrigger)` resolves `@file` references in both `when_py` and `prompt`:
- A value starting with `@` is treated as a filesystem path (e.g. `@predicates/critical.py`)
- The file is read operator-side at `km check deploy` / `km check sync` time
- The Lambda env var always contains the resolved inline string — never a path
- Editing a `@file` source requires `km check sync` to re-bake and re-push

`sourceHash` covers the resolved (post-@file) KM_CHECK_TRIGGER JSON. `km check ls` shows `DRIFT (run km check sync)` when the current bake hash differs from the stored `source_hash`.

## Task Commits

1. **Task 1: pkg/check trigger bake + sourceHash (TDD red→green)** - `f066d689` (feat)
2. **Task 2: pkg/check Lambda CRUD + packaging + ECR + DDB** - `30d0e898` (feat)
3. **Task 3: internal/app/cmd/check.go CLI + root registration** - `fa9b5ff5` (feat)

## Files Created/Modified
- `/Users/khundeck/working/klankrmkr/pkg/check/trigger.go` — BakeTrigger, resolveAtFile, TriggerSummary
- `/Users/khundeck/working/klankrmkr/pkg/check/bootstrap.go` — BootstrapBytes() via go:embed, AssertBootstrapByteIdentity
- `/Users/khundeck/working/klankrmkr/pkg/check/_km_check_bootstrap.py` — embedded copy (canonical: profiles/checks/_bootstrap/)
- `/Users/khundeck/working/klankrmkr/pkg/check/lambda.go` — DeployFunction, UpdateTriggerEnv, InvokeFunction, DeleteFunction
- `/Users/khundeck/working/klankrmkr/pkg/check/package.go` — BuildZip (arm64 pip wheels), MaybeUploadLargeZip
- `/Users/khundeck/working/klankrmkr/pkg/check/ecr.go` — EnsureECRRepo (idempotent + lambda pull policy)
- `/Users/khundeck/working/klankrmkr/pkg/check/ddb.go` — CheckRow struct, PutCheckRow/UpdateCheckRow/GetCheckRow/ListCheckRows/DeleteCheckRow
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/check.go` — km check deploy|run|ls|get|logs|schedule|sync|rm
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/check_test.go` — TestCheckTriggerBakeInline, TestCheckTriggerBakeAtFile, TestCheckSourceHash
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/root.go` — NewCheckCmd registration
- `go.mod` / `go.sum` — added github.com/aws/aws-sdk-go-v2/service/ecr v1.58.4

## Decisions Made
- Lambda env vars set: `KM_CHECK_NAME`, `KM_ARTIFACTS_BUCKET`, `KM_CHECK_TRIGGER`, `KM_CHECK_SECRET_PATHS`, plus user `--env K=V` overrides — exactly what the bootstrap reads
- `on_absent` defaults to `"cold-create"` in `BakeTrigger` when empty (matches KM_CHECK_TRIGGER schema default)
- ECR `{prefix}-checks` repo is lazily SDK-created at first `--image` deploy (not a third terragrunt module)
- EventBridge Scheduler create/delete are stubs for compile-time correctness; live implementation and UAT deferred to Plan 116-08
- `go:embed` for the bootstrap bytes keeps the km binary self-contained; `AssertBootstrapByteIdentity` enforces canonical source parity in tests

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Added github.com/aws/aws-sdk-go-v2/service/ecr dependency**
- **Found during:** Task 2 (ECR repo creation)
- **Issue:** ECR SDK package not in go.mod; required for `EnsureECRRepo`
- **Fix:** `go get github.com/aws/aws-sdk-go-v2/service/ecr`
- **Files modified:** go.mod, go.sum
- **Verification:** `go build ./pkg/check/` green
- **Committed in:** `30d0e898` (Task 2 commit)

**2. [Rule 1 - Bug] Removed unused `lambdapkg` import and fixed execCmdRunner type errors**
- **Found during:** Task 3 (CLI build)
- **Issue:** Initial check.go imported lambdapkg as unused; execCmdRunner had undefined Stdout/Stderr fields
- **Fix:** Removed lambdapkg import; replaced custom execCmdRunner with direct `exec.Command` in `runDockerBuildPush`
- **Files modified:** internal/app/cmd/check.go
- **Verification:** `go build ./internal/app/cmd/` clean
- **Committed in:** `fa9b5ff5` (Task 3 commit)

---

**Total deviations:** 2 auto-fixed (1 blocking dependency, 1 compile-time bug)
**Impact on plan:** Both auto-fixes necessary for compilation. No scope creep.

## Issues Encountered
None beyond the two auto-fixed deviations above.

## Next Phase Readiness
- `pkg/check/` package is complete and ready for Plan 116-06 (ttl-handler CheckDispatch consumer, pkg/dispatch)
- `km check deploy|sync` bakes KM_CHECK_TRIGGER correctly; the bootstrap (116-04) reads it at runtime
- EventBridge Scheduler integration stubs are in place; real implementation needed before 116-08 UAT
- `go build ./...` is clean; full test suite still green

---
*Phase: 116-km-check-serverless-check-runner*
*Completed: 2026-06-18*
