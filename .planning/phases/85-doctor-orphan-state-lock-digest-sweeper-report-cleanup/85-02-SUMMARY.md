---
phase: 85-doctor-orphan-state-lock-digest-sweeper-report-cleanup
plan: 02
subsystem: infra
tags: [go, aws-sdk-v2, dynamodb, s3, doctor, sweeper, tdd]

# Dependency graph
requires:
  - phase: 85
    plan: 01
    provides: "S3StateHeadAPI / LockDigestDeleterAPI interfaces, function signature, mocks, RED test stubs"
provides:
  - "checkStateLockDigestSweeper full implementation (paginated Scan → parallel HEAD → classify → age guard → optional BatchWriteItem → output formatter)"
  - "parseSandboxIDFromLockID helper (sandbox-scoped vs shared-module classification)"
  - "renderSweeperMessage helper (summary + 10-inline + 'use --json for full list' + uncapped fullList for Details)"
  - "batchDeleteLockItems helper (25-item batching with UnprocessedItems surfacing)"
  - "CheckResult.Details []string field (json:omitempty) — Phase 85 ACCEPT-READ contract"
  - "All 8 TestDigestSweeper_* tests GREEN (5 TDD + OutputFormat + UnprocessedItems + SharedModuleLockID_NoSandboxID)"
affects:
  - "phase 85 plan 03 (DoctorDeps wiring, --delete-state-digests flag, buildChecks swap rely on this contract)"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Parallel scan with bounded concurrency: goroutine pool + buffered semaphore channel + select-on-ctx.Done() for cancellation safety"
    - "Partial-success AWS API handling: BatchWriteItem err==nil with non-empty UnprocessedItems map; surface as failed-with-lockID, not silently dropped"
    - "Narrow CheckResult shape extension: omitempty new field preserves JSON compatibility for every existing --json consumer"
    - "Cross-reference age guard pattern: live-sandbox set built from SandboxLister; rows whose LockID embeds a live sandbox-id are skipped from deletion"
    - "Shared-module fallback: rows with no /sandboxes/ segment bypass the lister cross-reference and fall through to the strict NotFound-only delete path"

key-files:
  created: []
  modified:
    - "internal/app/cmd/doctor.go (+6 lines: CheckResult.Details field with godoc)"
    - "internal/app/cmd/doctor_state_digest_sweeper.go (replaced stubs with 410-line implementation)"
    - "internal/app/cmd/doctor_state_digest_test.go (turned 8 RED stubs into real test bodies)"
    - ".planning/phases/85-.../deferred-items.md (logged pre-existing TestUnlockCmd failure)"

key-decisions:
  - "Implemented the full batchDeleteLockItems body inside Task 1's source rev so the integration shape is testable at the Task 1 boundary; Task 2 only flips the 3 remaining RED test stubs to GREEN"
  - "minOrphanAge accepted on signature but currently UNUSED — no per-row creation timestamp exists in Terragrunt lock table schema; age guard implemented via SandboxLister cross-reference instead"
  - "Shared-module LockIDs (no /sandboxes/ segment: ses, vpc, scp, management/*) bypass lister cross-reference and fall to strict NotFound-only path with delete gate"
  - "CheckResult.Details is populated with the FULL uncapped list ONLY when status is non-OK; OK status sets Details=nil so omitempty drops the field entirely from --json (no regression for existing JSON consumers)"
  - "Pre-existing TestUnlockCmd_RequiresStateBucket failure is OUT OF SCOPE (km unlock test, unrelated to sweeper); logged to deferred-items.md"

patterns-established:
  - "Phase 85 Plan 02 → Plan 03 contract: CheckResult.Details + sweeper signature locked; Plan 03 only swaps buildChecks registration and adds DoctorDeps fields"
  - "Pitfall remediations are cited in source comments by pitfall number (#1 NotFound vs NoSuchKey, #2 UnprocessedItems, #4 ctx.Done() on semaphore acquire) — makes reviewer cross-reference trivial"

requirements-completed:
  - IN-SCOPE-1
  - IN-SCOPE-3
  - IN-SCOPE-4
  - IN-SCOPE-5
  - IN-SCOPE-6
  - IN-SCOPE-7
  - ACCEPT-READ
  - ACCEPT-WRITE
  - ACCEPT-TEST

# Metrics
duration: 13 min
completed: 2026-05-19
---

# Phase 85 — Plan 02 Summary (Wave 1)

**Sweeper implementation + tests + the one-field `CheckResult.Details` exception — all 8 TDD tests GREEN; Plan 03 can wire the flag and swap buildChecks against this locked contract.**

## Scope

Sweeper algorithm + tests + the one-field CheckResult.Details exception.

## doctor.go diff (this plan's contribution)

EXACTLY one addition: a fifth field on the `CheckResult` struct.

```go
type CheckResult struct {
    Name        string      `json:"name"`
    Status      CheckStatus `json:"status"`
    Message     string      `json:"message"`
    Remediation string      `json:"remediation,omitempty"`
    Details     []string    `json:"details,omitempty"` // NEW (Phase 85)
}
```

Rationale: ACCEPT-READ requires `--json` to emit the FULL uncapped list of
orphan items. The existing JSON-output path
(`json.NewEncoder(out).Encode(results)`) automatically picks this up; no other
doctor.go changes needed in Plan 02. The remaining doctor.go work (DoctorDeps
fields, flag wiring, buildChecks swap) belongs to Plan 03 — no merge conflict.

## checkStateLockDigestSweeper — final shape

- Scan paginator → 10-worker goroutine pool with semaphore + ctx-cancel-safe acquire.
- HeadObject classification: `s3types.NotFound` → orphan; other errors → cant-verify; success → live.
- Age guard via `SandboxLister` cross-reference. Shared-module LockIDs (no `/sandboxes/` segment) bypass the guard and fall to the strict-NotFound-only path.
- Optional `BatchWriteItem` path gated on `dryRun==false && deleteStateDigests==true`. UnprocessedItems surfaced as failed via the failure map.
- Returns `CheckResult` with `Details []string` populated when mismatches exist (uncapped); `nil` on CheckOK so JSON omitempty drops the field.
- Destructive runs append `\n  → N deleted, M failed` to the WARN message and name up to 5 failed lockIDs inline (the rest go in `Details`).

## parseSandboxIDFromLockID — semantics for Plan 03

- Returns `(sandboxID, true)` for keys containing `/sandboxes/<sid>/`.
- Returns `("", false)` for shared-module keys (ses, vpc, scp, management/*).
- Plan 03 does NOT need to call this directly — `buildChecks` just wires the sweeper.

## Locked contract for Plan 03

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

- New `DoctorDeps` fields Plan 03 must add: `StateLockS3HeadClient S3StateHeadAPI`, `StateLockDDBWriteClient LockDigestDeleterAPI`, `DeleteStateDigests bool`.
- New `runDoctor` positional bool Plan 03 must add: `deleteStateDigests` (last position, after `deleteSSM`).
- `buildChecks` REPLACEMENT site (around doctor.go:2594) — Plan 03 swaps `checkStateLockDigest(...)` for `checkStateLockDigestSweeper(...)`.

## Test status

All 8 TestDigestSweeper_* tests GREEN. All 7 Phase 84.1 TestCheckStateLockDigest_* tests still GREEN. `TestParseLockID_KeyWithSlashes` GREEN.

| Test | Status |
|------|--------|
| TestDigestSweeper_OrphanAgePassesDeleted (TDD-1) | PASS |
| TestDigestSweeper_OrphanAgeFailsSkipped (TDD-2) | PASS |
| TestDigestSweeper_S3HeadAmbiguousError_Skipped (TDD-3) | PASS |
| TestDigestSweeper_LiveS3StaleDigest_NotDeletedBySweeper (TDD-4) | PASS |
| TestDigestSweeper_26Items_TwoBatches (TDD-5) | PASS |
| TestDigestSweeper_OutputFormat | PASS |
| TestDigestSweeper_UnprocessedItems | PASS |
| TestDigestSweeper_SharedModuleLockID_NoSandboxID | PASS |

## Pitfall handling — line citations for Plan 03 reviewers

- **Pitfall #1** (s3types.NotFound vs NoSuchKey): commented at the `errors.As(err, &notFound)` site in `doctor_state_digest_sweeper.go` (~line 196). `grep -c "s3types.NotFound"` returns 5.
- **Pitfall #2** (UnprocessedItems partial-success): handled inside `batchDeleteLockItems` (~line 360) — `out.UnprocessedItems[tableName]` mapped to per-row `failed` count and `failures` map. `grep -c "UnprocessedItems"` returns 5.
- **Pitfall #4** (ctx cancellation in semaphore acquire): `select { case sem <- struct{}{}: case <-ctx.Done(): return }` at ~line 175. `grep -c "case <-ctx.Done()"` returns 1 (single instance — only one semaphore acquire path).
- **Pitfall #5** (age guard with no per-row timestamp): implemented as SandboxLister cross-reference (no time.Duration math); minOrphanAge parameter is reserved with `_ = minOrphanAge` and a godoc note.

## Ready-for-Plan-03 checklist

- [x] `go test ./internal/app/cmd/ -run "TestDigestSweeper|TestCheckStateLockDigest|TestParseLockID" -v` — all GREEN
- [x] `CheckResult.Details` field present (`grep -c "Details\s*\[\]string" internal/app/cmd/doctor.go` returns 1)
- [x] `doctor_state_digest_sweeper.go` is 410 lines (≥250)
- [x] No `t.Skip` / no flaky test
- [x] `go build ./internal/app/cmd/...` clean
- [x] `go vet ./internal/app/cmd/...` clean

## Task Commits

1. **Task 0 (CheckResult.Details field)** — `c20fd85` (feat)
2. **Task 1 (sweeper implementation + 5 GREEN tests)** — `204aca8` (feat)
3. **Task 2 (TDD-1, TDD-5, UnprocessedItems GREEN)** — `ef08ae4` (test)
4. **Task 3 (this SUMMARY)** — pending docs commit

## Deviations from Plan

None — plan executed exactly as written. Note that `batchDeleteLockItems`
body was written in Task 1's commit (it's needed for the integration shape
to be testable at the Task 1 boundary); Task 2's commit therefore only
turns the 3 remaining RED test stubs into real test bodies. This is
consistent with the plan's task split (Task 1 owns
"scan + classify + render + Details plumbing"; Task 2 owns the test
flipping for the delete-path tests).

## Deferred Issues

- `TestUnlockCmd_RequiresStateBucket` is failing in the package — this is
  a pre-existing failure on Plan 01 head (`781398c`) too, completely
  unrelated to Phase 85. Logged to `deferred-items.md`. Belongs in a
  separate `km unlock` fix.

## Next Phase Readiness

Plan 03 can begin immediately. The sweeper function signature, helper
shape, mocks, test names, and the `CheckResult.Details` field are all
locked. Plan 03's job is to:

1. Add three fields to `DoctorDeps` (`StateLockS3HeadClient`,
   `StateLockDDBWriteClient`, `DeleteStateDigests`).
2. Add `--delete-state-digests` flag wired via `--with-deletes` umbrella.
3. Add `deleteStateDigests` as the last positional bool to `runDoctor`.
4. Swap the `checkStateLockDigest` registration in `buildChecks` for
   `checkStateLockDigestSweeper`.

## Self-Check: PASSED

- `internal/app/cmd/doctor.go` — exists on disk
- `internal/app/cmd/doctor_state_digest_sweeper.go` — exists on disk (410 lines)
- `internal/app/cmd/doctor_state_digest_test.go` — exists on disk (all 8 TestDigestSweeper_* tests GREEN)
- `.planning/phases/85-.../85-02-SUMMARY.md` — exists on disk (this file, 200+ lines)
- `.planning/phases/85-.../deferred-items.md` — exists on disk
- Task 0 commit `c20fd85` (feat) — present in git log
- Task 1 commit `204aca8` (feat) — present in git log
- Task 2 commit `ef08ae4` (test) — present in git log

---

*Phase: 85-doctor-orphan-state-lock-digest-sweeper-report-cleanup*
*Plan: 02 (Wave 1)*
*Completed: 2026-05-19*
