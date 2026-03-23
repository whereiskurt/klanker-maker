---
phase: 18-loose-ends
plan: "02"
subsystem: cli-commands
tags: [km-uninit, regional-infra, cobra, terragrunt, tdd]
dependency_graph:
  requires:
    - "pkg/terragrunt.Runner (Destroy method)"
    - "internal/app/cmd.SandboxLister (ListSandboxes)"
    - "pkg/compiler.RegionLabel"
    - "pkg/aws.LoadAWSConfig, ValidateCredentials"
  provides:
    - "km uninit command (NewUninitCmd)"
    - "RunUninitWithDeps (testable DI entry point)"
    - "UninitRunner interface"
    - "SSMGetPutAPI interface (create.go)"
    - "ErrGitHubNotConfigured sentinel (create.go)"
  affects:
    - "internal/app/cmd/root.go (uninit registered)"
    - "internal/app/cmd/create.go (SSMGetPutAPI interface)"
tech_stack:
  added: []
  patterns:
    - "TDD (RED-GREEN): failing tests before implementation"
    - "Dependency injection via interface for testability (UninitRunner, SandboxLister)"
    - "Reverse-order destroy loop with non-fatal error handling"
    - "Active sandbox guard with --force escape hatch"
key_files:
  created:
    - internal/app/cmd/uninit.go
    - internal/app/cmd/uninit_test.go
    - internal/app/cmd/help/uninit.txt
  modified:
    - internal/app/cmd/root.go
    - internal/app/cmd/create.go
decisions:
  - "Non-fatal destroy errors: uninit warns and continues rather than stopping on first module failure — partial teardown is better than none"
  - "Require --force when state_bucket empty: cannot safely check active sandboxes without state bucket config"
  - "Defined SSMGetPutAPI interface in create.go: unblocks pre-existing create_github_test.go stubs that were blocking compilation"
  - "ErrGitHubNotConfigured defined in create.go: typed sentinel for ParameterNotFound detection rather than string matching"
metrics:
  duration: "~12 minutes"
  completed: "2026-03-23"
  tasks_completed: 2
  files_changed: 5
---

# Phase 18 Plan 02: km uninit Command Summary

**One-liner:** `km uninit` tears down all 6 regional Terraform modules in reverse dependency order with active-sandbox safety guard, accepting `UninitRunner`/`SandboxLister` interfaces for full unit test coverage.

## What Was Built

The `km uninit` command reverse-mirrors `km init`. It is the operator-facing teardown for decommissioning a region. Key behaviors:

- Destroys 6 modules in reverse dependency order: ttl-handler → s3-replication → ses → dynamodb-identities → dynamodb-budget → network
- Refuses teardown when active (running) sandboxes exist in the target region — unless `--force` is passed
- Requires `--force` when `state_bucket` is not configured (can't verify active sandboxes)
- Skips module directories that don't exist (warning message, continues)
- Continues past failed Destroy calls (warning, non-fatal)
- Registered in root command tree and appears in `km --help`

## Task Results

### Task 1: Create km uninit command with active-sandbox guard (TDD)

**Commit:** ba2b6c3

**TDD Flow:**
- RED: Created `uninit_test.go` with 11 tests covering all behaviors — confirmed build failure (RunUninitWithDeps/NewUninitCmd undefined)
- GREEN: Created `uninit.go` with `UninitRunner` interface, `RunUninitWithDeps`, `NewUninitCmd`
- Also created `help/uninit.txt` (required at compile time by embedded FS) to complete the GREEN phase

**Tests passing (11/11):**
- `TestUninitDestroyOrder` — reverse order verified (ttl-handler first, network last)
- `TestUninitRefusesWithActiveSandboxes` — error returned, no Destroy called
- `TestUninitProceedsWithForce` — force bypasses sandbox check
- `TestUninitProceedsNoActiveSandboxes` — proceeds when region has no running sandboxes
- `TestUninitSkipsMissingModuleDirectory` — 0 Destroy calls for non-existent region
- `TestUninitContinuesPastModuleErrors` — all 6 modules attempted even with errors
- `TestUninitRequiresForceWhenStateBucketEmpty` — error without --force
- `TestUninitRequiresForceWhenStateBucketEmptyProceedsWithForce` — force allows proceeding
- `TestUninitCmdRegistered` — command flags verified
- `TestUninitOnlyCountsRegionSandboxes` — cross-region and stopped sandboxes don't block
- `TestUninitActiveSandboxErrorMessage` — error message mentions count and --force

### Task 2: Register uninit in root command and add help text

**Commit:** 5f5b3e3 (executed as part of 18-03 wave — root.go already had NewUninitCmd added)

`root.AddCommand(NewUninitCmd(cfg))` added adjacent to `NewInitCmd(cfg)`. Binary builds. Full suite passes.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Pre-existing test stubs in create_github_test.go blocked compilation**
- **Found during:** Task 1 RED phase
- **Issue:** `create_github_test.go` (untracked, from previous agent) referenced `ErrGitHubNotConfigured` and called `generateAndStoreGitHubToken` with `SSMGetPutAPI` interface — but create.go still used `*ssm.Client` and had no sentinel
- **Fix:** Extracted `SSMGetPutAPI` interface in `create.go`, defined `ErrGitHubNotConfigured` sentinel, added `ParameterNotFound` detection in all three SSM GetParameter calls
- **Files modified:** `internal/app/cmd/create.go`
- **Commit:** ba2b6c3

**2. [Rule 2 - Missing functionality] uninit.txt help file needed at compile time**
- **Found during:** Task 1 GREEN phase
- **Issue:** `helpText("uninit")` in `NewUninitCmd` panics at compile time if `help/uninit.txt` doesn't exist (embedded FS). Creating the test for `TestUninitCmdRegistered` would always panic.
- **Fix:** Created `help/uninit.txt` with proper help content as part of Task 1 (Task 2 specified this file but it was needed to complete Task 1's tests)
- **Files modified:** `internal/app/cmd/help/uninit.txt`
- **Commit:** ba2b6c3

## Self-Check: PASSED

| Item | Status |
|------|--------|
| `internal/app/cmd/uninit.go` | FOUND |
| `internal/app/cmd/uninit_test.go` | FOUND |
| `internal/app/cmd/help/uninit.txt` | FOUND |
| Commit ba2b6c3 | FOUND |
| All 11 TestUninit* tests pass | VERIFIED |
| Binary `km` builds | VERIFIED |
