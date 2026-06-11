---
phase: 105-scoped-km-init-for-bridge-config
plan: "03"
subsystem: cmd/init
tags: [tdd, wave-2, scoped-apply, single-module, dry-run, ssm-side-effects]
dependency_graph:
  requires: [INIT-SCOPED-FLAG, INIT-SCOPED-ALIASES, INIT-SCOPED-GUARD]
  provides: [INIT-SCOPED-IMPL]
  affects: [internal/app/cmd/init.go, internal/app/cmd/init_scoped_test.go]
tech_stack:
  added: []
  patterns: [package-level-var-seam, tdd-red-green, exported-runner-pattern, ssm-side-effects-gating]
key_files:
  created: []
  modified:
    - internal/app/cmd/init.go
    - internal/app/cmd/init_scoped_test.go
decisions:
  - RunInitScopedWithRunner exported as plain function (not var) — consistent with RunInitWithRunner/RunInitPlanWithRunner; mockRunner satisfies InitRunner directly so no var seam needed
  - scopedGateFunc package var defaults to passthrough no-op in Plan 03; Plan 04 rebinds to real plan+destroy-class-gate path for Tier-2 (ses)
  - envReqs check placed AFTER directory existence check and AFTER scopedGateFunc so the error message is always actionable (directory must exist before envReqs can pass)
  - runInitScopedFunc rebind from stub to real runInitScoped by changing the var initializer in place (no init() needed)
  - ExportTerragruntEnvVars tested directly in TestScopedEnvVarsExported rather than via a var seam — the contract is clear (runInitScoped calls it first) and the test is a direct behavioral assertion
metrics:
  duration: 380s
  completed: 2026-06-11T17:00:37Z
  tasks_completed: 2
  files_created: 0
  files_modified: 2
---

# Phase 105 Plan 03: RunInitScopedWithRunner + runInitScoped Implementation Summary

**One-liner:** Tier-1 scoped apply core — RunInitScopedWithRunner filters regionalModules to one module, respects dry-run, runs per-module timeout and envReqs; runInitScoped production wrapper sequences ExportTerragruntEnvVars + SSM side effects before applying.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 (RED) | Failing tests for RunInitScopedWithRunner | 1f2876dd | internal/app/cmd/init_scoped_test.go |
| 1 (GREEN) | RunInitScopedWithRunner + scopedGateFunc production code | 7f7b29b4 | internal/app/cmd/init.go |
| 2 | TestScopedEnvVarsExported real assertion + runInitScoped + rebind | 7f7b29b4 | internal/app/cmd/init.go, init_scoped_test.go |

## Verification

- `go test ./internal/app/cmd/ -run TestScoped -v` → 7 PASS (Wave 1+2), 3 SKIP (Plan 04)
- `make build` → exit 0, km v0.4.927
- `km init --github` (dry-run default) → prints "Would apply: lambda-github-bridge [tier-1] (run with --dry-run=false to apply)" without applying

## Production Symbols Introduced

| Symbol | Type | Notes |
|--------|------|-------|
| `scopedGateFunc` | `var func(ctx, runner, m, acceptDestroys) error` | Plan 04 injection point; passthrough no-op in Plan 03 |
| `RunInitScopedWithRunner(runner, repoRoot, region, module, dryRun, acceptDestroys)` | func (exported) | Testable core: filter → envReqs → gate → dry-run branch → Apply with timeout → email-handler ARN capture |
| `runInitScoped(cfg, awsProfile, region, verbose, module, dryRun, acceptDestroys)` | func (unexported) | Production wrapper: AWS config → ExportTerragruntEnvVars → EnsureSlackBotUserIDFromSSM → gated SSM publishes → RunInitScopedWithRunner |
| `runInitScopedFunc` (rebound) | package-level var | Was stub returning "not implemented"; now points to real `runInitScoped` |

## Test Results (Wave 2)

| Test | Status | Coverage |
|------|--------|----------|
| TestScopedModuleResolution | PASS | Wave 1 (unchanged) |
| TestScopedModuleRejection | PASS | Wave 1 (unchanged) |
| TestScopedAliases | PASS | Wave 1 (unchanged) |
| TestScopedMutualExclusion | PASS | Wave 1 (unchanged) |
| TestScopedDryRun | PASS | Wave 2: dry-run=true → 0 Apply calls, nil return |
| TestScopedApply | PASS | Wave 2: dry-run=false → exactly 1 Apply call; module-not-found error; missing-env-req names the var |
| TestScopedEnvVarsExported | PASS | Wave 2: ExportTerragruntEnvVars sets KM_ARTIFACTS_BUCKET from cfg |
| TestScopedTier2Gate | SKIP | Plan 04 |
| TestScopedTier2GateBlocked | SKIP | Plan 04 |
| TestScopedSesPreflight | SKIP | Plan 04 |

## Key Behavioral Contracts Established

1. **Dry-run gate**: When `dryRun=true` (CLI default), `RunInitScopedWithRunner` prints one line and returns nil — never calls `runner.Apply`. The operator must explicitly pass `--dry-run=false`.

2. **SSM side-effect ordering** (mirrors `runInit` exactly):
   - `ExportTerragruntEnvVars(cfg)` FIRST — sets all `KM_*` env vars; required before envReqs check and before terragrunt reads `get_env()`
   - `EnsureSlackBotUserIDFromSSM(...)` unconditionally (non-fatal; no-op when not needed)
   - `PublishGitHubCommandsToSSM(...)` ONLY when `module == "lambda-github-bridge"` AND `len(cfg.Github.Commands) > 0`
   - `PublishH1CommandsToSSM(...)` ONLY when `module == "lambda-h1-bridge"` AND `len(cfg.H1.Programs) > 0`

3. **Tier-2 hook**: `scopedGateFunc` is a package-level var (passthrough now; Plan 04 replaces with plan+destroy-class-gate for `ses`). The `isScopedGated(module)` branch is live code but the gate itself is a no-op until Plan 04.

4. **email-handler ARN capture**: Post-apply `runner.Output()` call captures `lambda_function_arn` → sets `KM_EMAIL_HANDLER_ARN` env var + prints to stdout. Informational only in scoped context (ses does not apply in the same run).

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- [x] `internal/app/cmd/init.go` contains `func RunInitScopedWithRunner`
- [x] `internal/app/cmd/init.go` contains `var scopedGateFunc`
- [x] `internal/app/cmd/init.go` contains `func runInitScoped`
- [x] `internal/app/cmd/init.go`: `runInitScopedFunc = runInitScoped` (real impl, not stub)
- [x] `internal/app/cmd/init_scoped_test.go` contains `func TestScopedDryRun` (real assertions)
- [x] `internal/app/cmd/init_scoped_test.go` contains `func TestScopedApply` (real assertions)
- [x] `internal/app/cmd/init_scoped_test.go` contains `func TestScopedEnvVarsExported` (real assertions)
- [x] Commit 1f2876dd exists (RED tests)
- [x] Commit 7f7b29b4 exists (GREEN implementation)
- [x] `go test ./internal/app/cmd/ -run TestScoped -v` → 7 PASS, 3 SKIP
- [x] `make build` exits 0 (km v0.4.927)
