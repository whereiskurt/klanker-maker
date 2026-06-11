---
phase: 105-scoped-km-init-for-bridge-config
plan: "02"
subsystem: cmd/init
tags: [tdd, wave-1, flags, validation, dispatch-guard]
dependency_graph:
  requires: [INIT-SCOPED-TESTS]
  provides: [INIT-SCOPED-FLAG, INIT-SCOPED-ALIASES, INIT-SCOPED-GUARD]
  affects: [internal/app/cmd/init.go, internal/app/cmd/init_scoped_test.go]
tech_stack:
  added: []
  patterns: [package-level-var-seam, cobra-flags, table-driven-tests, exported-test-helper]
key_files:
  created: []
  modified:
    - internal/app/cmd/init.go
    - internal/app/cmd/init_scoped_test.go
decisions:
  - Exported ResolveScopedModule (not unexported) so cmd_test external package can call it directly in unit tests — mirrors RegionalModules export pattern
  - runInitScopedFunc stub var uses same package-level-var-seam pattern as RunInitPlanFunc/BuildLambdaZipsFunc so Plan 03 can replace the body without editing dispatch code
  - isScopedGated() left unexported (only consumed internally in Plan 03 tier-2 path)
  - Dispatch guard placed before --plan check so scoped+plan yields the scoped error, not the plan mutual-exclusion error (consistent UX)
metrics:
  duration: 191s
  completed: 2026-06-11T17:01:26Z
  tasks_completed: 1
  files_created: 0
  files_modified: 2
---

# Phase 105 Plan 02: Scoped km init Flags + ResolveScopedModule Summary

**One-liner:** Flag surface and resolution/guard logic for scoped km init — `--only`/`--github`/`--slack`/`--h1`/`--email` with allowlist validation and mutual-exclusion guard, independently testable stub seam.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Allowlist slices + ResolveScopedModule + flags | 5734ef79 | internal/app/cmd/init.go, internal/app/cmd/init_scoped_test.go |

## Verification

- `go test ./internal/app/cmd/ -run TestScoped -v` → 4 PASS (Wave 1), 6 SKIP (Wave 2)
- `make build` → exit 0, km v0.4.925
- `km init --help` shows --only, --github, --slack, --h1, --email
- `km init --only bogus` → error listing allowed set
- `km init --github --plan` → error containing "cannot be combined"

## Production Symbols Introduced

| Symbol | Type | Notes |
|--------|------|-------|
| `scopedCheapAllowlist` | `[]string` (unexported) | Tier-1: lambda-github-bridge, lambda-slack-bridge, lambda-h1-bridge, email-handler |
| `scopedGatedAllowlist` | `[]string` (unexported) | Tier-2: ses |
| `isScopedGated(name string) bool` | func (unexported) | Membership check on Tier-2 list |
| `ResolveScopedModule(onlyVal string, github, slack, h1, email bool) (string, error)` | func (exported) | Maps flags → canonical module name; validates allowlist; at-most-one guard |
| `runInitScopedFunc` | `var func(...) error` (package-level) | Stub returning "not implemented"; Plan 03 replaces body |
| `--only`, `--github`, `--slack`, `--h1`, `--email` | Cobra flags | Registered on `km init` |

## Test Results (Wave 1)

| Test | Status | Coverage |
|------|--------|----------|
| TestScopedModuleResolution | PASS | 6 subtests: all Tier-1 modules, ses (Tier-2), empty (no-op) |
| TestScopedModuleRejection | PASS | 5 subtests: bogus-module, network, dynamodb-sandboxes, create-handler, totally-fake |
| TestScopedAliases | PASS | 4 subtests: --github, --slack, --h1, --email |
| TestScopedMutualExclusion | PASS | 7 subtests: two-alias via resolver, --only+alias via resolver, 5 dispatch-guard combos |
| TestScopedDryRun | SKIP | Wave 2 (Plan 03) |
| TestScopedApply | SKIP | Wave 2 (Plan 03) |
| TestScopedEnvVarsExported | SKIP | Wave 2 (Plan 03) |
| TestScopedTier2Gate | SKIP | Wave 2/3 (Plan 03) |
| TestScopedTier2GateBlocked | SKIP | Wave 2/3 (Plan 03) |
| TestScopedSesPreflight | SKIP | Wave 2/3 (Plan 03) |

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- [x] `internal/app/cmd/init.go` contains `scopedCheapAllowlist`
- [x] `internal/app/cmd/init.go` contains `ResolveScopedModule`
- [x] `internal/app/cmd/init.go` contains `runInitScopedFunc` stub
- [x] `internal/app/cmd/init_scoped_test.go` contains `func TestScopedModuleResolution`
- [x] Commit 5734ef79 exists in git log
- [x] `go test ./internal/app/cmd/ -run TestScoped -v` → 4 PASS, 6 SKIP
- [x] `make build` exits 0
