---
phase: 12-ecs-budget-topup-s3-replication
plan: 01
subsystem: budget
tags: [ecs, fargate, budget, s3, compiler, terragrunt, profile]

# Dependency graph
requires:
  - phase: 11-sandbox-auto-destroy-metadata-wiring
    provides: SandboxMetadata and FetchSandboxMeta wiring used in budget substrate detection
  - phase: 06-budget-enforcement-platform-configuration
    provides: runBudgetAdd EC2 branch pattern; BudgetAPI; SandboxMetaFetcher interface
  - phase: 04-lifecycle-hardening-artifacts-email
    provides: artifacts/{sandbox-id}/.km-profile.yaml S3 storage pattern used in re-provisioning
provides:
  - reprovisionECSSandbox function in budget.go (S3 download -> parse -> compile -> apply)
  - ECS substrate branch in runBudgetAdd (substrate == "ecs" detection)
  - ArtifactsBucket and AWSProfile fields in Config struct
  - Three new ECS budget tests covering substrate detection, missing bucket warning, and source-level verification
affects:
  - phase 12-02 (s3-replication config — same phase, different plan)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "ECS re-provisioning reuses existing sandboxID — never generates new — so Terraform state maps to existing cluster"
    - "reprovisionECSSandbox not injected via interface — calls compiler.Compile + runner.Apply directly; tests use source-level verification (Phase 07-02 pattern)"
    - "ArtifactsBucket in Config sourced from KM_ARTIFACTS_BUCKET; empty bucket produces actionable warning, never fatal budget failure"

key-files:
  created: []
  modified:
    - internal/app/cmd/budget.go
    - internal/app/cmd/budget_test.go
    - internal/app/config/config.go

key-decisions:
  - "reprovisionECSSandbox is a real implementation function (not DI-injectable) — calls compiler.Compile and runner.Apply directly; unit tests verify via source-level inspection (strings.Contains) following Phase 07-02 pattern"
  - "ArtifactsBucket added to Config struct (KM_ARTIFACTS_BUCKET env var) — required for ECS re-provisioning path to avoid tight coupling to os.Getenv inside budget command"
  - "AWSProfile added to Config struct — extracted from hard-coded klanker-terraform default so the ECS branch can pass the profile to reprovisionECSSandbox without re-reading env vars"
  - "ECS re-provisioning failure is non-fatal (Warning + continue) — budget limits are updated regardless; operator visibility via warning message"

patterns-established:
  - "Source-level verification test pattern for non-injectable functions: os.ReadFile(source_file) + strings.Contains checks"

requirements-completed: [BUDG-08]

# Metrics
duration: 4min
completed: 2026-03-23
---

# Phase 12 Plan 01: ECS Budget Top-Up Summary

**ECS Fargate re-provisioning in km budget add via S3-stored profile download, compiler.Compile with existing sandboxID, and terragrunt apply**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-03-23T02:31:10Z
- **Completed:** 2026-03-23T02:35:00Z
- **Tasks:** 1 (TDD: test commit + implementation commit)
- **Files modified:** 3

## Accomplishments

- Added `reprovisionECSSandbox` function to budget.go: downloads `.km-profile.yaml` from S3, parses and resolves the profile, loads network outputs, calls `compiler.Compile` with the existing `sandboxID`, writes artifacts via `terragrunt.CreateSandboxDir`/`PopulateSandboxDir`, and runs `runner.Apply`
- Added ECS substrate branch in `runBudgetAdd`: detects `substrate == "ecs"`, emits actionable warning when `cfg.ArtifactsBucket` is empty, otherwise calls `reprovisionECSSandbox` (non-fatal on failure)
- Added `ArtifactsBucket` and `AWSProfile` fields to `Config` struct with viper/env-var wiring (`KM_ARTIFACTS_BUCKET`, `KM_AWS_PROFILE`)
- All 8 budget tests pass including 3 new ECS tests

## Task Commits

Each task was committed atomically:

1. **TDD RED — Failing ECS tests** - `5c3e8a6` (test)
2. **TDD GREEN — ECS implementation** - `ab76b3e` (feat)

## Files Created/Modified

- `internal/app/cmd/budget.go` — Added `reprovisionECSSandbox` function, ECS branch in `runBudgetAdd`, `awsProfile` variable extraction, new imports (io, compiler, profile, terragrunt)
- `internal/app/cmd/budget_test.go` — Added `TestBudgetAdd_ECSSubstrate`, `TestBudgetAdd_ECSMissingArtifactBucket`, `TestBudgetAdd_ECSSourceLevelVerification`; added `os` import
- `internal/app/config/config.go` — Added `ArtifactsBucket` and `AWSProfile` fields to Config struct; added viper defaults and km-config.yaml merge keys

## Decisions Made

- `reprovisionECSSandbox` uses existing `sandboxID` directly — never calls `compiler.GenerateSandboxID()`. This is the critical correctness constraint: generating a new ID would create duplicate ECS clusters and break the budget enforcer Lambda named `km-budget-enforcer-{sandbox-id}`.
- The function is not DI-injectable (calls real compiler and terragrunt) — unit tests verify via source-level inspection following the Phase 07-02 MLflow pattern.
- `ArtifactsBucket` added to Config rather than using raw `os.Getenv` inside the budget command — consistent with how other config values are threaded through the Config struct.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] Added ArtifactsBucket and AWSProfile to Config struct**
- **Found during:** Task 1 (implementing ECS test + reprovisionECSSandbox)
- **Issue:** Plan referenced `cfg.ArtifactsBucket` but Config struct had no such field; tests could not compile
- **Fix:** Added `ArtifactsBucket` and `AWSProfile` fields to Config struct with viper defaults (`artifacts_bucket`, `aws_profile`) and km-config.yaml merge keys; also extracted `awsProfile` variable from hard-coded `"klanker-terraform"` string in production init block
- **Files modified:** `internal/app/config/config.go`
- **Verification:** `go build ./...` succeeds; all tests pass
- **Committed in:** `5c3e8a6` (part of RED test commit)

---

**Total deviations:** 1 auto-fixed (Rule 2 - missing critical config fields)
**Impact on plan:** Required for correctness — the plan explicitly referenced `cfg.ArtifactsBucket` which did not yet exist. No scope creep.

## Issues Encountered

None — all tests passed on first GREEN run after implementing `reprovisionECSSandbox`.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- BUDG-08 requirement fully satisfied: ECS sandboxes can be resumed via `km budget add`
- Phase 12-02 (S3 replication Terragrunt live config) is independent and ready to execute
- No blockers

---
*Phase: 12-ecs-budget-topup-s3-replication*
*Completed: 2026-03-23*

## Self-Check: PASSED

- FOUND: internal/app/cmd/budget.go
- FOUND: internal/app/cmd/budget_test.go
- FOUND: internal/app/config/config.go
- FOUND: .planning/phases/12-ecs-budget-topup-s3-replication/12-01-SUMMARY.md
- FOUND commit: 5c3e8a6 (test RED)
- FOUND commit: ab76b3e (feat GREEN)
