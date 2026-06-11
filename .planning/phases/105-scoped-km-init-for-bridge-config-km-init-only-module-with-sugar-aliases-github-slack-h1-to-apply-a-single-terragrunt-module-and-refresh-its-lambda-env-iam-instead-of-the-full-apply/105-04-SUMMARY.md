---
phase: 105-scoped-km-init-for-bridge-config
plan: "04"
subsystem: cmd/init
tags: [tdd, wave-3, tier-2-gate, ses, destroy-class, preflight, reconfigure]
dependency_graph:
  requires: [INIT-SCOPED-IMPL]
  provides: [INIT-SCOPED-GUARD]
  affects: [internal/app/cmd/init.go, internal/app/cmd/init_scoped_test.go]
tech_stack:
  added: []
  patterns: [package-level-var-seam, tdd-red-green, destroy-class-gate, spy-pattern]
key_files:
  created: []
  modified:
    - internal/app/cmd/init.go
    - internal/app/cmd/init_scoped_test.go
decisions:
  - scopedGateFunc rebound in-place (var initializer replaced, no init() needed) — planModule + planreport.Evaluate for single-module pre-apply gate; NOT RunInitPlanFunc (which plans all 27 modules and never applies)
  - Gate placed BEFORE dryRun branch so dry-run --only ses still surfaces destroy-class trips without applying
  - ses preflight + Reconfigure replicated from RunInitWithRunner:1845-1870 inside scopedGateFunc (not after) so both fire before Apply regardless of dry-run
  - mockReconfigurePlanRunner introduced in test file to record Reconfigure calls — embeds mockPlanRunner and overrides Reconfigure to append dir; minimal change, no new test infrastructure
  - InitSESPreflight spy replaces the package var temporarily via t.Cleanup restore pattern (standard for package var seams)
metrics:
  duration: 480s
  completed: 2026-06-11T17:10:00Z
  tasks_completed: 1
  files_created: 0
  files_modified: 2
---

# Phase 105 Plan 04: Tier-2 Gate (scopedGateFunc) + SES Preflight Summary

**One-liner:** Real destroy-class gate for `--only ses` via `planModule` + `planreport.Evaluate` + `InitSESPreflight` + `runner.Reconfigure` before apply; all 10 TestScoped* tests pass with no SKIPs.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 (GREEN) | Real scopedGateFunc + tier-2 tests | 71b79969 | internal/app/cmd/init.go, init_scoped_test.go |

## Verification

- `go test ./internal/app/cmd/ -run TestScoped -v` → 10 PASS (no SKIPs)
- `make build` → exit 0, km v0.4.928

## Production Symbols Changed

| Symbol | Type | Change |
|--------|------|--------|
| `scopedGateFunc` | `var func(ctx, runner, m, acceptDestroys) error` | Replaced no-op with real plan+evaluate+ses-preflight path |

## Test Results (Wave 3)

| Test | Status | Coverage |
|------|--------|----------|
| TestScopedModuleResolution | PASS | Wave 1 (unchanged) |
| TestScopedModuleRejection | PASS | Wave 1 (unchanged) |
| TestScopedAliases | PASS | Wave 1 (unchanged) |
| TestScopedMutualExclusion | PASS | Wave 1 (unchanged) |
| TestScopedDryRun | PASS | Wave 2 (unchanged) |
| TestScopedApply | PASS | Wave 2 (unchanged) |
| TestScopedEnvVarsExported | PASS | Wave 2 (unchanged) |
| TestScopedTier2Gate | PASS | Wave 3: plan invoked before apply for ses; clean plan → apply runs |
| TestScopedTier2GateBlocked | PASS | Wave 3: destroy plan → blocked (no apply); acceptDestroys=true → apply runs |
| TestScopedSesPreflight | PASS | Wave 3: InitSESPreflight spy fired; Reconfigure recorded; Apply called after |

## Gate Logic Implemented (scopedGateFunc)

```
planModule(ctx, runner, m, verbose=false)
→ planreport.Evaluate([report], acceptDestroys)
  → result.Blocked → printTripBlock + return "destroy-class gate tripped" (apply skipped)
  → result.Trips + acceptDestroys → printTripBlock + "(override active)" + continue
  → clean → continue
→ (ses only) InitSESPreflight(ctx)   [replicate RunInitWithRunner:1845]
→ (ses only) runner.Reconfigure(ctx, m.dir)  [replicate RunInitWithRunner:1860]
→ return nil  (caller proceeds to dryRun branch → Apply)
```

## Key Behavioral Contracts Established

1. **Single-module gate:** `scopedGateFunc` plans ONLY the target module (not all 27). `planModule` + `planreport.Evaluate` reuses the same curated destroy-class gate as `RunInitPlanWithRunner` but for one module only.

2. **Gate order:** Gate runs BEFORE the `dryRun` early-return so `km init --only ses` (dry-run=true, the default) still surfaces destroy/replace trips without applying — matching the spirit of the full `--plan` gate.

3. **ses preflight + Reconfigure:** Both `InitSESPreflight` and `runner.Reconfigure` are called inside `scopedGateFunc` for `m.name == "ses"`. This replicates the `RunInitWithRunner:1845-1870` ses branch. Reconfigure is required because the ses module source moved v1→v2; omitting it silently wedges the backend.

4. **acceptDestroys override:** `planreport.Evaluate(reports, acceptDestroys)` respects the `--i-accept-destroys` flag exactly as `RunInitPlanWithRunner` does. Trips are printed but apply proceeds when overridden.

## Deferred Issues

- **TestRunInitPlan_ModuleOrder** (pre-existing, not introduced by Plan 04): expects 17 modules in `allModuleNames` test constant but `regionalModules()` now returns 22. Logged to `deferred-items.md`. No action taken per scope boundary rule.

## Deviations from Plan

None — plan executed exactly as written. `scopedGateFunc` rebound as var initializer replacement (no `init()` needed per Plan 03 pattern). All three tier-2 tests implemented and passing.

## Self-Check: PASSED

- [x] `internal/app/cmd/init.go` `var scopedGateFunc` contains `planreport.Evaluate`
- [x] `internal/app/cmd/init.go` `var scopedGateFunc` contains `InitSESPreflight`
- [x] `internal/app/cmd/init.go` `var scopedGateFunc` contains `runner.Reconfigure`
- [x] `internal/app/cmd/init_scoped_test.go` contains `func TestScopedTier2Gate` (real assertions, no t.Skip)
- [x] `internal/app/cmd/init_scoped_test.go` contains `func TestScopedTier2GateBlocked` (real assertions, no t.Skip)
- [x] `internal/app/cmd/init_scoped_test.go` contains `func TestScopedSesPreflight` (real assertions, no t.Skip)
- [x] Commit 71b79969 exists
- [x] `go test ./internal/app/cmd/ -run TestScoped -v` → 10 PASS, 0 SKIP
- [x] `make build` exits 0 (km v0.4.928)
