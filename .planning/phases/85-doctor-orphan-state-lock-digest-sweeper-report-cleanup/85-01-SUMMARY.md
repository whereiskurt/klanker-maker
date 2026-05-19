---
phase: 85-doctor-orphan-state-lock-digest-sweeper-report-cleanup
plan: 01
subsystem: infra
tags: [go, aws-sdk-v2, dynamodb, s3, doctor, tdd, scaffolding]

# Dependency graph
requires:
  - phase: 84.1
    provides: "checkStateLockDigest function, S3StateReader / LockDigestReader interfaces, parseLockID helper (all reused unchanged by the Phase 85 sweeper)"
provides:
  - "S3StateHeadAPI narrow interface (HeadObject) — distinct from existing S3StateReader (GetObject)"
  - "LockDigestDeleterAPI narrow interface (BatchWriteItem) — Phase 85 delete path seam"
  - "checkStateLockDigestSweeper function signature (Wave 0 stub returning CheckSkipped)"
  - "renderSweeperMessage + batchDeleteLockItems helper stubs (Plan 02 implements)"
  - "8 red-state TDD test stubs locked by name (5 core + OutputFormat + UnprocessedItems + SharedModuleLockID)"
  - "mockS3StateHead + mockLockDigestDeleter test mocks satisfying the new interfaces"
affects:
  - "phase 85 plan 02 (implementation — turns the 8 RED tests GREEN against this locked contract)"
  - "phase 85 plan 03 (DoctorDeps wiring, --delete-state-digests flag, buildChecks swap)"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Narrow per-API-surface interface seams (S3StateHeadAPI HeadObject-only vs existing S3StateReader GetObject-only) — avoids widening Phase 84.1 contracts"
    - "Compile-time interface assertions (`var _ Iface = (*Impl)(nil)`) — locks the SDK client method-set drift at build time"
    - "RED-state TDD stub pattern: function exists with placeholder return + tests t.Fatalf(\"RED — ...\") for Plan 02 to drive GREEN"

key-files:
  created:
    - "internal/app/cmd/doctor_state_digest_sweeper.go"
    - ".planning/phases/85-doctor-orphan-state-lock-digest-sweeper-report-cleanup/85-01-SUMMARY.md"
  modified:
    - "internal/app/cmd/doctor_state_digest_test.go (appended 8 RED test stubs + 2 mocks; Phase 84.1 tests untouched)"

key-decisions:
  - "Locked checkStateLockDigestSweeper signature with separate S3StateHeadAPI / LockDigestDeleterAPI seams — Phase 84.1 S3StateReader stays narrow"
  - "Wave 0 stub returns CheckSkipped so callers compile cleanly while tests stay RED"
  - "renderSweeperMessage + batchDeleteLockItems helpers stubbed in the source file so Plan 02 only fills in bodies"
  - "fakeSandboxLister (from doctor_ebs_test.go) reused as-is — no duplicate stub introduced"
  - "doctor.go remains byte-identical in Plan 01; Plan 02 has a documented exception to add CheckResult.Details for --json full-list plumbing; Plan 03 swaps buildChecks"

patterns-established:
  - "Phase 85 sweeper file naming: internal/app/cmd/doctor_state_digest_sweeper.go matches the test target doctor_state_digest_test.go (single underscore convention)"
  - "TDD test names locked at Wave 0 so Plan 02 implementation and 85-VALIDATION.md verification harness reference the same identifiers"
  - "Shared-module LockID fallback (no /sandboxes/ segment) gets a dedicated test — parseSandboxIDFromLockID ok=false branch is exercised explicitly to prevent silent accumulation of orphan ses/vpc/scp rows"

requirements-completed:
  - IN-SCOPE-1
  - IN-SCOPE-7
  - ACCEPT-TEST

# Metrics
duration: 3 min
completed: 2026-05-19
---

# Phase 85 — Plan 01 Summary (Wave 0)

**RED-state scaffolding for the orphan state-lock digest sweeper: new file with interface stubs + 8 failing TDD tests + locked function signature for Plans 02/03.**

## Scope

Wave 0 scaffolding only: new file with stubs + 8 red-state test stubs + 2 new
test mocks. Zero runtime logic. doctor.go is byte-identical.

## Locked interface contract (DO NOT CHANGE without updating Plans 02 + 03)

### Function signature

```go
func checkStateLockDigestSweeper(
    ctx context.Context,
    s3Head S3StateHeadAPI,
    ddbRead LockDigestReader,
    ddbWrite LockDigestDeleterAPI,
    lister SandboxLister,
    lockTableName string,
    dryRun bool,
    deleteStateDigests bool,
    minOrphanAge time.Duration,
) CheckResult
```

### New interfaces (in `internal/app/cmd/doctor_state_digest_sweeper.go`)

- `S3StateHeadAPI` — narrow `HeadObject`-only surface; returns `s3types.NotFound` (NOT `s3types.NoSuchKey`).
- `LockDigestDeleterAPI` — narrow `BatchWriteItem`-only surface.

Compile-time assertions confirm `*s3.Client` and `*dynamodb.Client` satisfy
the new interfaces:

```go
var _ S3StateHeadAPI = (*s3.Client)(nil)
var _ LockDigestDeleterAPI = (*dynamodb.Client)(nil)
```

### Reused (DO NOT redefine)

- `LockDigestReader` (Scan) — from `doctor.go`.
- `SandboxLister` (ListSandboxes) — from `list.go`.
- `CheckResult`, `CheckOK/Warn/Error/Skipped` — from `doctor.go`.
- `parseLockID` — from `doctor.go` (H8-correct).
- `fakeSandboxLister` (test) — from `doctor_ebs_test.go` (already in-package).

## Locked test names (Plan 02 turns these GREEN)

1. `TestDigestSweeper_OrphanAgePassesDeleted` (TDD-1)
2. `TestDigestSweeper_OrphanAgeFailsSkipped` (TDD-2)
3. `TestDigestSweeper_S3HeadAmbiguousError_Skipped` (TDD-3)
4. `TestDigestSweeper_LiveS3StaleDigest_NotDeletedBySweeper` (TDD-4)
5. `TestDigestSweeper_26Items_TwoBatches` (TDD-5)
6. `TestDigestSweeper_OutputFormat`
7. `TestDigestSweeper_UnprocessedItems`
8. `TestDigestSweeper_SharedModuleLockID_NoSandboxID` (checker-required shared-module fallback test)

All 8 currently fail with `t.Fatalf("RED — ...")` — the expected RED state of TDD.

## doctor.go status

**Untouched in Plan 01.** Plan 02 has a documented exception to add a single
`Details []string` field to `CheckResult` (needed by `--json` full-list
plumbing for ACCEPT-READ). All other doctor.go wiring (DoctorDeps fields,
`--delete-state-digests` flag, buildChecks swap) is Plan 03.

## Mock types added (in `doctor_state_digest_test.go`)

- `mockS3StateHead` — implements `S3StateHeadAPI`. Keys in `missing` return
  `*s3types.NotFound`; non-nil `err` returns that error for ALL calls.
- `mockLockDigestDeleter` — implements `LockDigestDeleterAPI`. Records every
  call; first call optionally echoes `unprocessedLocks` via `UnprocessedItems`
  to exercise the retry path.
- `stubSandboxLister` — NOT added; `fakeSandboxLister` is already in-package
  (`doctor_ebs_test.go`) and reused as-is.

## Ready-for-Plan-02 checklist

- [x] `go build ./internal/app/cmd/...` — green
- [x] `go vet ./internal/app/cmd/...` — clean
- [x] `go test ./internal/app/cmd/ -run TestCheckStateLockDigest -v` — all 7 Phase 84.1 tests GREEN (regression baseline)
- [x] `go test ./internal/app/cmd/ -run TestCheckStaleLambdas -v` — GREEN
- [x] `go test ./internal/app/cmd/ -run TestParseLockID -v` — GREEN
- [x] `go test ./internal/app/cmd/ -run TestDigestSweeper -v` — all 8 RED with `t.Fatalf("RED — ...")`
- [x] `internal/app/cmd/doctor.go` is byte-identical (`git diff --stat` empty)

## Task Commits

1. **Task 1 (sweeper scaffolding)** — `ad8038e` (feat)
2. **Task 2 (RED test stubs + mocks)** — `f541dba` (test)
3. **Task 3 (this SUMMARY + plan metadata)** — pending docs commit

## Deviations from Plan

None — plan executed exactly as written.

## Next Phase Readiness

Plan 02 can begin immediately. The function signature, interface seams, and
test names are locked. Plan 02's job is to replace the Wave 0 stub body of
`checkStateLockDigestSweeper` with the real implementation and drive all 8
RED tests GREEN, with the documented `CheckResult.Details` exception in
`doctor.go` for `--json` full-list plumbing.

## Self-Check: PASSED

- internal/app/cmd/doctor_state_digest_sweeper.go: exists on disk
- internal/app/cmd/doctor_state_digest_test.go: exists on disk
- .planning/phases/85-.../85-01-SUMMARY.md: exists on disk
- Task 1 commit ad8038e: present in git log
- Task 2 commit f541dba: present in git log

---

*Phase: 85-doctor-orphan-state-lock-digest-sweeper-report-cleanup*
*Plan: 01 (Wave 0 scaffolding)*
*Completed: 2026-05-19*
