---
phase: 124-platform-wide-az-failover-and-capacity-feasibility-for-ec2-launches
plan: "05"
subsystem: capacity
tags: [capacity, wait-for-capacity, doctor, gpu-quota, az-sweep]
dependency_graph:
  requires: [124-04]
  provides: [km create --wait-for-capacity outer backoff, km doctor capacity-table + GPU-quota checks]
  affects: [internal/app/cmd/create.go, internal/app/cmd/doctor.go]
tech_stack:
  added: []
  patterns: [TDD RED/GREEN, NoOptDefVal flag with optional =value, outer-backoff retry loop, doctor check functions]
key_files:
  created:
    - internal/app/cmd/create_wait_capacity_test.go
    - internal/app/cmd/doctor_capacity_test.go
  modified:
    - internal/app/cmd/create.go (--wait-for-capacity flag + outer re-sweep backoff loop; operator-only, never forwarded to Lambda subprocess)
    - internal/app/cmd/doctor.go (capacity table existence check + GPU vCPU quota L-DB2E81BA=0 WARN)
    - internal/app/cmd/doctor_test.go (test wiring)
    - internal/app/cmd/clone.go (incidental 1-line adjustment)
decisions:
  - "--wait-for-capacity uses NoOptDefVal=30m: bare flag = 30m default, =Nm overrides; duration validated early before any I/O"
  - "--wait-for-capacity is operator-only — never appended to the cold-create Lambda subprocess args (verified)"
  - "GPU vCPU quota (L-DB2E81BA) == 0 → CheckWarn with remediation link; >0 passes"
metrics:
  duration: "~1490s (agent context overflowed at docs step; both code tasks committed cleanly)"
  completed: "2026-06-28"
  tasks: 2
  files: 5
---

# Phase 124 Plan 05: Operator Wrapper — --wait-for-capacity + km doctor Capacity Checks Summary

Opt-in `km create --wait-for-capacity[=30m]` outer backoff that re-sweeps all AZs until a deadline (default off = fail-fast), plus `km doctor` visibility into the new `{prefix}-capacity` table and a GPU vCPU quota (L-DB2E81BA) = 0 WARN.

## What Was Built

**Task 1 — `km create --wait-for-capacity` (commit `e0c48761`):**
- New `--wait-for-capacity` flag on `km create` with `NoOptDefVal = "30m"` (bare flag → 30m; `=Nm` overrides). Duration is validated early (before any I/O).
- Outer backoff loop wraps the AZ sweep: when set, re-sweeps all AZs on a backoff until the deadline instead of failing fast on a transient capacity dry-spell.
- Operator-only: the flag is **never** forwarded to the cold-create Lambda subprocess (explicitly documented + verified — no subprocess arg append).
- `create_wait_capacity_test.go` (166 lines) covers the flag default-off, the duration parse/validation, and the outer-backoff behavior.

**Task 2 — `km doctor` capacity checks (commit `b09b037b`):**
- `km doctor` now checks the `{prefix}-capacity` DynamoDB table existence (via `cfg.GetCapacityTableName()` + `checkDynamoTable`).
- New GPU vCPU quota check: queries `capacity.GetGPUVCPUQuota`; a value of 0 (the L-DB2E81BA default-0 footgun) → `CheckWarn` with the remediation link; >0 passes.
- `doctor_capacity_test.go` (196 lines) covers both the table check and the quota=0 WARN path.

## Verification

- `go build ./...` clean.
- `go test ./internal/app/cmd/` green (includes `create_wait_capacity_test.go` + `doctor_capacity_test.go`).
- All three plan `must_haves.truths` confirmed in committed code:
  1. `--wait-for-capacity[=30m]` registered + outer re-sweep on backoff ✓
  2. defaults off (fail-fast), never passed to Lambda subprocess ✓
  3. doctor surfaces `{prefix}-capacity` table + WARNs on GPU vCPU quota (L-DB2E81BA) = 0 ✓

## Note

The gsd-executor agent completed both code tasks with atomic commits and a clean working tree, then exhausted its context window ("Prompt is too long") at the documentation step. This SUMMARY and the STATE/ROADMAP updates were written by the orchestrator after spot-checking all committed work (build + tests green, must_haves verified). No code work was lost.
