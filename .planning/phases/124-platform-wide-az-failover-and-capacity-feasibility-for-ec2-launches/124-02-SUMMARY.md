---
phase: 124-platform-wide-az-failover-and-capacity-feasibility-for-ec2-launches
plan: "02"
subsystem: ec2-launch / capacity-error-handling
tags: [capacity, az-failover, error-classification, tdd, create-handler, km-create]
dependency_graph:
  requires: [124-01]
  provides: [classify-and-retry-sweep, nocap-taxonomy-classifier]
  affects: [cmd/create-handler, internal/app/cmd/create.go]
tech_stack:
  added: []
  patterns: [pkg/capacity.ClassifyError, sweepDecision-helper, failStatusForSubprocess-helper]
key_files:
  created:
    - internal/app/cmd/create_az_sweep_test.go
  modified:
    - cmd/create-handler/main.go
    - cmd/create-handler/main_test.go
    - internal/app/cmd/create.go
decisions:
  - "sweepDecision helper factored out of runCreate loop to enable unit-testing without a real Terragrunt runner"
  - "ClassUnknown falls through to (retry=false, failFast=false): generic error message, not a fail-fast — preserves original behaviour for unrecognised errors"
  - "failStatusForSubprocess helper in create-handler main.go allows direct unit-testing without mocking the full Lambda handler"
  - "Pre-existing cmd suite failures (TestBootstrapSCPApplyPath, TestClusterAdd, TestConfigureWizard*, TestUninit*) confirmed pre-date Phase 124 and are out of scope"
metrics:
  duration: 458s
  completed: "2026-06-28"
  tasks: 2
  files: 4
---

# Phase 124 Plan 02: Classify-and-Retry AZ-Sweep + Lambda nocap Taxonomy Summary

Both capacity-error matchers now consume the single `pkg/capacity.ClassifyError` taxonomy. Quota walls fail fast with named L-DB2E81BA remediation; ICE/spot errors iterate across AZs; success breaks.

## Tasks Completed

| # | Task | Commit | Files |
|---|------|--------|-------|
| 1 | Refactor create-handler nocap classifier onto pkg/capacity.ClassifyError | ec837e89 | cmd/create-handler/main.go, main_test.go |
| 2 | Upgrade km create AZ-sweep loop to classify-and-retry with fail-fast | 33e1fc50 | internal/app/cmd/create.go, create_az_sweep_test.go |

## What Was Built

**Task 1 — create-handler nocap classifier:**

Replaced the 4-string substring block in `cmd/create-handler/main.go:341-346` with a call to `capacity.ClassifyError(outStr, runErr).ShouldIterate()`. Extracted into `failStatusForSubprocess(outStr, runErr) string` helper to keep the logic testable. Key behavioural difference: `capacity-not-available` (ICE subvariant) now correctly maps to `"nocap"` — the old block only checked `InsufficientInstanceCapacity` / `MaxSpotInstanceCountExceeded` / `SpotMaxPriceTooLow` / `no Spot capacity`.

**Task 2 — km create AZ-sweep loop:**

Upgraded `internal/app/cmd/create.go:844-868` from a 2-string `isSpotCapacity` check to `class := capacity.ClassifyError(stderrStr, applyErr)` + `sweepDecision(class, attempt, maxAttempts)`. Key changes:
- `sweepDecision` returns `(retry bool, failFast bool)` — decouples the loop decision from the loop body and makes it unit-testable without a real Terragrunt runner.
- `ClassQuota` → `failFast=true` → prints `L-DB2E81BA` + Service Quotas increase URL, returns error immediately.
- `ClassAuth` / `ClassInvalid` → `failFast=true` → generic error message, no further AZ attempts.
- `ShouldIterate()` with remaining AZs → `retry=true` → `CleanupSandboxDir` + rotate + `continue` (unchanged position).
- `ShouldIterate()` exhausted → both false → existing "unavailable in all N AZs / use --on-demand" message.
- `ClassUnknown` → both false → generic error message (preserves prior fallback).
- `on-demand maxAttempts=1` path unchanged.

## TDD Flow

Both tasks followed strict RED→GREEN TDD:

**Task 1:**
- RED: `TestNocapClassifier` added to `main_test.go` calling non-existent `failStatusForSubprocess` → build error
- GREEN: helper added + inline block replaced → 9 subtests pass

**Task 2:**
- RED: `create_az_sweep_test.go` created with `TestAZSweepLoop` calling non-existent `sweepDecision` → build error
- GREEN: `sweepDecision` helper + loop upgrade → 11 subtests pass

## Verification Results

```
go test ./cmd/create-handler/... -run TestNocapClassifier -timeout 30s     PASS (9 subtests)
go test ./internal/app/cmd/... -run TestAZSweepLoop -timeout 60s          PASS (11 subtests)
go test ./cmd/create-handler/... -timeout 120s                              PASS (no regressions)
```

## Deviations from Plan

None — plan executed exactly as written.

## Out-of-Scope Items

Pre-existing test failures in `./internal/app/cmd/` (TestBootstrapSCPApplyPath, TestClusterAdd, TestConfigureWizard*, TestUninit*) were confirmed to pre-date Phase 124 via `git worktree` check against commit `1f42c674` (last Phase 122 commit). Logged to deferred-items, not fixed.

## Self-Check: PASSED

- FOUND: cmd/create-handler/main.go
- FOUND: internal/app/cmd/create.go
- FOUND: internal/app/cmd/create_az_sweep_test.go
- FOUND commit ec837e89 (Task 1 GREEN)
- FOUND commit 33e1fc50 (Task 2 GREEN)
