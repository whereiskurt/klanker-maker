---
phase: 124-platform-wide-az-failover-and-capacity-feasibility-for-ec2-launches
plan: "06"
subsystem: capacity-feasibility
tags: [docs, test-fix, az-failover, capacity, uninit, operational-gotchas]

requires:
  - phase: 124-02
    provides: classify-and-retry AZ sweep loop
  - phase: 124-03
    provides: capacity DDB store + classifier
  - phase: 124-04
    provides: RankAZs + km capacity command + km create wiring
  - phase: 124-05
    provides: km doctor capacity-table + GPU-quota checks + --wait-for-capacity

provides: Phase 124 full test gates (with uninit count fix), operational docs, CLAUDE.md summary block, and deploy instructions; checkpoint at live deploy gate

affects:
  - internal/app/cmd/uninit_test.go
  - docs/operational-gotchas.md
  - CLAUDE.md

dependency_graph:
  provides: [124-06-docs, 124-06-test-fix]
  requires: [124-02, 124-03, 124-04, 124-05]
  affects: [CLAUDE.md, docs/operational-gotchas.md]

tech_stack:
  added: []
  patterns:
    - uninit module count tracking (add entry to wantOrder + bump const when regionalModules grows)

key_files:
  created: []
  modified:
    - internal/app/cmd/uninit_test.go
    - docs/operational-gotchas.md
    - CLAUDE.md

decisions:
  - id: uninit-count-fix
    summary: "Phase 124 dynamodb-capacity raised regionalModules 26->27; updated wantOrder + count checks in 3 uninit tests"
  - id: known-8-env-failures
    summary: "8 pre-existing cmd suite environmental failures (2 bootstrap, 3 cluster, 3 configure) require live AWS creds / valid SSO session; confirmed NOT Phase 124 regressions; documented but not fixed"
  - id: checkpoint-at-deploy
    summary: "Stopped at Task 2 (checkpoint:human-action): operator must run make build -> make build-lambdas -> km init --dry-run=false -> km doctor"

metrics:
  duration: "1040s"
  completed_date: "2026-06-28"
  tasks_completed: 1
  tasks_total: 3
  files_changed: 3
---

# Phase 124 Plan 06: Ship Plan — Full Test Gates + Docs Summary

One-liner: Phase 124 docs + uninit count regression fix; AZ-failover + capacity gotchas documented; stopped at live-deploy checkpoint.

## What Was Done

### Task 1: Full test gates + Phase 124 docs (complete — commit 87369b6c)

**Test gate results:**

- `go test ./...` — all packages green except the pre-existing "known-8 environmental" in `internal/app/cmd` (2 bootstrap + 3 cluster tests need live AWS creds via fast-fail seam; 3 configure tests need a valid SSO session in subprocess). These are NOT Phase 124 regressions.
- Phase 124 regression found and fixed: `dynamodb-capacity` added in Phase 124 raised `regionalModules()` from 26 to 27 but `uninit_test.go` still checked for 26. Fixed `TestUninitDestroyOrder` (wantOrder slice), `TestUninitContinuesPastModuleErrors` (const wantCalls), and `TestUninitDetectsBackendDrift` (inline count).
- `scripts/validate-all-profiles.sh` — 20/20 profiles valid.

**Documentation:**

- `docs/operational-gotchas.md`: added "AZ failover + capacity feasibility (Phase 124)" section covering:
  - The AZ sweep: `RankAZs` + classify-and-retry with error class taxonomy table
  - GPU quota wall: `L-DB2E81BA` vs ICE — structural distinction, account-default=0 gotcha
  - Bounded spot waiter: `ClassWaiterTimeout` as iterate-class (not fail-fast)
  - `km capacity` verdicts table (likely / recently-dry / not-offered / quota-blocked / unknown)
  - `km create --wait-for-capacity`: outer 5-min re-sweep loop, default 30m deadline
  - `{prefix}-capacity` DDB table: TTL semantics, last_ice_at + last_success_at
  - Deploy-surface order: `make build` BEFORE `km init --dry-run=false` (load-bearing)
- `CLAUDE.md`: added Phase 124 summary block and "Where to look" table row

### Tasks 2-3: Checkpoint (not yet executed)

- **Task 2 (checkpoint:human-action):** Deploy the capacity table + refactored classifier — operator must run `make build` → `make build-lambdas` → `km init --dry-run=false` → `km doctor`
- **Task 3 (checkpoint:human-verify):** Live UAT gates — G1/G2 (no quota needed), G3-G5 (GPU quota gated)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed Phase 124 uninit test count regression (26→27)**
- **Found during:** Task 1 test gate run
- **Issue:** Phase 124 added `dynamodb-capacity` to `regionalModules()` (raising the count from 26 to 27) but `uninit_test.go` still hardcoded 26 in three tests. The `TestRunInitPlan_ModuleOrder` test had already been updated to 27 by a prior Phase 124 commit.
- **Fix:** Updated `TestUninitDestroyOrder` (added `dynamodb-capacity` to `wantOrder` between `dynamodb-schedules` and `dynamodb-sandboxes`, matching the actual destroy order from the test output), `TestUninitContinuesPastModuleErrors` (const from 26→27), `TestUninitDetectsBackendDrift` (inline count from 26→27)
- **Files modified:** `internal/app/cmd/uninit_test.go`
- **Commit:** 87369b6c

### Documented Environmental Failures (out-of-scope, not fixed)

8 pre-existing cmd suite failures remain in the environment:
- 2 bootstrap tests (`TestBootstrapSCPApplyPath`, `TestBootstrapSCPSkipped_OrganizationBlank`): fail via LoadAWSConfig fast-fail seam (no live AWS creds)
- 3 cluster tests (`TestClusterAdd`, `TestClusterRm`, `TestClusterAddPersistFailure`): same fast-fail seam
- 3 configure tests (`TestConfigureWizardWritesResourcePrefixAndEmailSubdomain`, `TestConfigureWizardDefaultsApply`, `TestConfigureInteractivePromptsUseNewNames`): subprocess HeadBucket fails with `InvalidGrantException` (expired SSO session)

These are the "known-8 environmental set" acknowledged in the June 28 LoadAWSConfig seam commit (`6aa1398d`). Not caused by Phase 124.

## Checkpoint State — RESOLVED

**Task 2 (deploy) — DONE.** Operator ran `make build && make build-lambdas && km init --dry-run=false`
in order; the full apply created the `km-capacity` table (ACTIVE, `(instanceType HASH, az RANGE)`,
PAY_PER_REQUEST, TTL on `ttl`) and re-applied the 27-module fleet to clean exit. Orchestrator verified
via `describe-table` + `km doctor` (Capacity Table row present; GPU vCPU quota = 64 vCPUs).

**Task 3 (live UAT) — PARTIAL (G1/G2 passed, G3–G5 deferred).** Evidence captured in `124-UAT.md`:
- G1 — 4 distinct AZ subnets (1a/1b/1c/1d) in vpc-027ba3e68c2e32549 ✓
- G2 — `km capacity --type g6e.12xlarge` honest per-AZ verdict table; 1e/1f `not-offered`, never "available" ✓
- G3–G5 — DEFERRED (live GPU launch cost + us-east-1 g6e capacity-hang risk; not authorized for this
  unattended session). GPU quota is now present (64 vCPUs) so an operator can run them attended, per the
  Phase 122 live-UAT pattern.

## Self-Check: PASSED (final)

- FOUND: 124-UAT.md (deploy + G1 + G2 evidence; G3–G5 quota-deferred note)
- VERIFIED: km-capacity table ACTIVE in us-east-1
- VERIFIED: km doctor Capacity Table + GPU vCPU quota checks live

## Self-Check: PASSED

- FOUND: docs/operational-gotchas.md
- FOUND: CLAUDE.md
- FOUND: internal/app/cmd/uninit_test.go
- FOUND: 124-06-SUMMARY.md
- FOUND commit 87369b6c (feat(124-06): full test gates + Phase 124 docs)
