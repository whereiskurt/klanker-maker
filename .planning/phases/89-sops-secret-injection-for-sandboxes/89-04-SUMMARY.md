---
phase: 89-sops-secret-injection-for-sandboxes
plan: "04"
subsystem: infra
tags: [kms, bootstrap, uninit, sops, phase-89, terragrunt]

# Dependency graph
requires:
  - phase: 89-02
    provides: sandbox-secrets-key Terraform module + terragrunt.hcl live wiring
  - phase: 84
    provides: runBootstrapSharedSES pattern + RunBootstrapSharedSESFunc seam + destroy-class gate
  - phase: 84-3
    provides: runBootstrapAll + RunBootstrapAllFunc seam + --all flag
provides:
  - km bootstrap --shared-secrets-key flag (idempotent KMS key provisioning)
  - km bootstrap --shared-secrets-key --plan (routes through Phase 84.2 destroy-class gate)
  - km bootstrap --all now chains foundation → shared-ses → shared-secrets-key
  - km uninit deletes own-prefix sandbox-secrets alias + schedules 7-day key deletion
  - KMSAliasLister + KMSAliasDeleter interfaces (mockable in tests)
  - RunBootstrapSharedSecretsKeyFunc package-level test seam
  - deleteOwnSecretsKMSAlias + scanOrphanedSecretsKey (orphan-key recovery)
affects:
  - 89-07 (UAT: km bootstrap --shared-secrets-key --plan output verification)
  - 89-05 (create.go SOPS bundle upload wires KM_REGISTER_SECRETS_KEY)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "6-stage bootstrap subflow pattern (loadBootstrapConfig + ExportTerragruntEnvVars + ensureRegionHCL + AWS-client-with-dry-run-degrade + auto-detect + os.Setenv + ApplyTerragrunt)"
    - "Two-stage KMS key discovery: DescribeKey(alias) primary, ListKeys+ListResourceTags tag-based fallback for orphan recovery"
    - "Package-level test seam var (RunBootstrap*Func pattern) for testable cobra command routing"

key-files:
  created:
    - internal/app/cmd/bootstrap_secrets_test.go
  modified:
    - internal/app/cmd/bootstrap.go
    - internal/app/cmd/uninit.go
    - infra/live/use1/sandbox-secrets-key/terragrunt.hcl

key-decisions:
  - "KMSAliasDeleter interface defined in uninit.go (not bootstrap.go) since deleteOwnSecretsKMSAlias lives there; KMSAliasLister defined in bootstrap.go with runBootstrapSharedSecretsKey"
  - "Two-stage KMS key discovery for deleteOwnSecretsKMSAlias: alias-via-DescribeKey primary; tag-based ListKeys+ListResourceTags fallback for orphan-key recovery (BLOCKER 2 Option (a) revision)"
  - "7-day pending window for ScheduleKeyDeletion in uninit (vs 30-day module default) because uninit implies intentional teardown"
  - "Tasks 2 and 3 committed together (same commit b27ad46) because the test file references both runBootstrapSharedSecretsKey (bootstrap.go) and deleteOwnSecretsKMSAlias (uninit.go) — package must compile as a whole"
  - "Removed pre-existing 89-04 stubs (KMSAliasDeleter + deleteOwnSecretsKMSAlias stub + runBootstrapSharedSecretsKey stub) from bootstrap.go; real implementations replace them"

patterns-established:
  - "Pattern: Bootstrap subflow mirror — new bootstrap subflows follow the 6-stage shape of runBootstrapSharedSES (bootstrap.go); copy-paste-substitute pattern is documented in 89-RESEARCH.md"
  - "Pattern: Orphan-key recovery — two-stage KMS discovery (DescribeKey primary + tag-scan fallback) allows uninit to recover from partial-destroy states where alias was deleted but key leaked"

requirements-completed:
  - SOPS-05-BOOTSTRAP-FLAG
  - SOPS-06-BOOTSTRAP-PLAN
  - SOPS-07-BOOTSTRAP-ALL-CHAIN
  - SOPS-21-UNINIT-CLEANUP

# Metrics
duration: 12min
completed: 2026-05-27
---

# Phase 89 Plan 04: Bootstrap CLI + Uninit Cleanup Summary

**`km bootstrap --shared-secrets-key` flag wired to sandbox-secrets-key KMS module via 6-stage SES-mirror pattern; --all chain extended to foundation → shared-ses → shared-secrets-key; uninit deletes own-prefix alias + schedules 7-day key deletion with two-stage orphan-key recovery**

## Performance

- **Duration:** 12 min
- **Started:** 2026-05-27T20:40:23Z
- **Completed:** 2026-05-27T20:52:43Z
- **Tasks:** 3 (Tasks 2+3 committed together due to test-package compile dependency)
- **Files modified:** 4

## Accomplishments

- New `runBootstrapSharedSecretsKey` function mirrors `runBootstrapSharedSES` line-by-line (6-stage pattern): config load, env export, region.hcl ensure, KMS auto-detect via `detectSharedSecretsKeyState`, `os.Setenv("KM_REGISTER_SECRETS_KEY")`, ApplyTerragruntFunc; dry-run degrades gracefully when AWS is unavailable
- `runBootstrapAll` extended: foundation → shared-ses → shared-secrets-key (exact order enforced by `TestRunBootstrapAllChain_Phase89`); `--all` ↔ `--shared-secrets-key` mutex enforced in cobra RunE
- `deleteOwnSecretsKMSAlias` in uninit.go with two-stage key discovery: DescribeKey(alias) primary round trip + tag-based ListKeys+ListResourceTags fallback for orphan-key recovery; 7-day pending window; sibling-install prefix collision guard (exact alias name match, not substring)
- `km:resource_prefix` tag added to sandbox-secrets-key/terragrunt.hcl inputs (required for the tag-based orphan-key fallback predicate in uninit)
- All 9 Phase 89 tests GREEN; existing bootstrap + uninit regression tests unaffected; `go vet` clean; `make build` produces km v0.3.722

## Task Commits

1. **Task 1: RED tests for runBootstrapSharedSecretsKey + deleteOwnSecretsKMSAlias** - `6424369` (test)
2. **Tasks 2+3: Implementation — bootstrap.go + uninit.go + terragrunt.hcl** - `b27ad46` (feat)

## Files Created/Modified

- `internal/app/cmd/bootstrap_secrets_test.go` (NEW) — 9 subtests: 3 dry-run (no-alias, existing-alias, AWS-unavailable-graceful), 1 chain-order, 1 mutex, 4 uninit (own-prefix delete, no-op, prefix collision guard, orphan recovery)
- `internal/app/cmd/bootstrap.go` — Added KMSAliasLister interface, RunBootstrapSharedSecretsKeyFunc seam, runBootstrapSharedSecretsKey, detectSharedSecretsKeyState, runBootstrapSharedSecretsKeyPlan, runBootstrapSharedSecretsKeyPlanWithWriter; extended runBootstrapAll to subflow 3; added --shared-secrets-key cobra flag + mutex; removed Phase 89-04 stubs
- `internal/app/cmd/uninit.go` — Added KMSAliasDeleter interface, deleteOwnSecretsKMSAlias, scanOrphanedSecretsKey; wired into runUninit before RunUninitWithDeps; added kms + kmstypes + errors imports
- `infra/live/use1/sandbox-secrets-key/terragrunt.hcl` — Added `"km:resource_prefix" = local.resource_prefix` to tags block

## Decisions Made

- `KMSAliasDeleter` defined in uninit.go (not bootstrap.go) since `deleteOwnSecretsKMSAlias` lives in uninit.go; `KMSAliasLister` stays in bootstrap.go with `runBootstrapSharedSecretsKey`
- Tasks 2+3 committed together because the test file (`package cmd`) references both `runBootstrapSharedSecretsKey` (bootstrap.go) and `deleteOwnSecretsKMSAlias` (uninit.go) — Go package must compile as a unit
- Pre-existing 89-04 stubs (planted before 89-04 by earlier planning work) removed and replaced with real implementations
- `--shared-ses` and `--shared-secrets-key` allowed simultaneously (NOT mutex) per plan recommendation — each calls its own subflow; only `--all` is mutex with each

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Removed duplicate 89-04 stubs from bootstrap.go**
- **Found during:** Task 2 (bootstrap.go implementation)
- **Issue:** bootstrap.go already contained placeholder stubs for `KMSAliasDeleter`, `deleteOwnSecretsKMSAlias`, and `RunBootstrapSharedSecretsKeyFunc` planted by earlier planning work; these caused `redeclared in this block` compile errors when the real implementations were added
- **Fix:** Removed all pre-existing stubs; real implementations replace them
- **Files modified:** internal/app/cmd/bootstrap.go
- **Committed in:** b27ad46

**2. [Rule 2 - Missing Critical] Added km:resource_prefix tag to sandbox-secrets-key/terragrunt.hcl**
- **Found during:** Task 3 (uninit.go — tag-based fallback predicate)
- **Issue:** terragrunt.hcl tags block had `km:component=sandbox-secrets-key` but missing `km:resource_prefix`; the `scanOrphanedSecretsKey` fallback predicate requires BOTH tags to identify own-prefix keys unambiguously
- **Fix:** Added `"km:resource_prefix" = local.resource_prefix` to tags block
- **Files modified:** infra/live/use1/sandbox-secrets-key/terragrunt.hcl
- **Verification:** `grep -E 'km:resource_prefix|km:component'` shows both tags present
- **Committed in:** b27ad46

---

**Total deviations:** 2 auto-fixed (1 bug — duplicate stubs causing compile error; 1 missing critical — tag needed for orphan recovery predicate)
**Impact on plan:** Both fixes necessary for correctness. No scope creep.

## Issues Encountered

- `aws.ToBool(out.Truncated)` called on `bool` not `*bool` in `detectSharedSecretsKeyState` (KMS `ListAliasesOutput.Truncated` is a plain bool unlike SES equivalents) — fixed by removing the `aws.ToBool()` wrapper
- `strPtr` helper redeclared in test file (already defined in `email.go`) — removed the local redeclaration and used `aws.String()` throughout

## User Setup Required

None - no external service configuration required. Operator must run:
```
make build && km bootstrap --shared-secrets-key --plan   # verify fresh-install plan output
km bootstrap --shared-secrets-key                         # apply after plan review
```

## Next Phase Readiness

- 89-05 (create.go SOPS bundle upload) can now call `km bootstrap --shared-secrets-key` to pre-provision the KMS key before sandbox creation
- 89-07 (UAT) should verify `km bootstrap --shared-secrets-key --plan` shows ~3 creates + 0 destroys on a fresh install; escape-hatch via `--i-accept-destroys` documented in function comment
- km init --lambdas is recommended after this plan ships (CLAUDE.md note: schema additions require Lambda toolchain refresh)

---
*Phase: 89-sops-secret-injection-for-sandboxes*
*Completed: 2026-05-27*
