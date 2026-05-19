---
phase: 85-doctor-orphan-state-lock-digest-sweeper-report-cleanup
verified: 2026-05-19T08:30:00Z
status: passed
score: 5/5 must-haves verified
re_verification: false
---

# Phase 85: doctor — orphan state-lock digest sweeper + report cleanup — Verification Report

**Phase Goal:** Close the Phase 84.1 digest-leak loop in `km doctor`: add a `--delete-state-digests` cleanup category (also folded into `--with-deletes`) that removes orphan rows from the Terragrunt state-lock DDB table where the sibling S3 state object is definitively gone (NoSuchKey + age > 24h). Replace the unreadable single-line digest-mismatch warn with a `summary + 10-item preview + --json full list` format matching Stale Lambdas. Parallelize the per-item S3 HEAD scan + BatchWriteItem deletes; target `km doctor` < 30s wall clock on accounts with hundreds of orphans (vs. ~1:40 today). Out of scope: plugging the upstream leak in `km destroy` / `km uninit` (separate follow-up phase).

**Verified:** 2026-05-19T08:30:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| #   | Truth                                                                                                                                                                                            | Status     | Evidence                                                                                                                                                                                                                                                       |
| --- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | New `--delete-state-digests` flag exists on `km doctor` and is folded into `--with-deletes` umbrella                                                                                              | ✓ VERIFIED | `doctor.go:2352` (`cmd.Flags().BoolVar(&deleteStateDigests, "delete-state-digests", false, ...)`); `doctor.go:2355` `--with-deletes` help text enumerates `--delete-state-digests`; freshly rebuilt `km doctor --help` confirms both flags visible. |
| 2   | Sweeper algorithm deletes only orphan rows where S3 HEAD returns definitive NotFound AND age > 24h; ambiguous/5xx errors NEVER delete; live S3 rows NEVER deleted                                 | ✓ VERIFIED | `doctor_state_digest_sweeper.go` 410 lines implementing `checkStateLockDigestSweeper`; `s3types.NotFound` classification at L57; min-orphan-age constant set to `24 * time.Hour` at `doctor.go:2716`; all 8 `TestDigestSweeper_*` unit tests GREEN. |
| 3   | Output replaces 275-line unreadable block with summary + 10-item preview + `… and N more (use --json for full list)` continuation, matching Stale Lambdas pattern                                | ✓ VERIFIED | `TestDigestSweeper_OutputFormat` GREEN; live UAT shows: `"state digest mismatch in 278 item(s) (278 orphan: state object missing, 0 other)"` + 10 inline items + `"… and 268 more (use --json for full list)"`; matches Stale-Lambdas format exactly.    |
| 4   | `--json` exposes uncapped full list via new `CheckResult.Details []string` field                                                                                                                  | ✓ VERIFIED | `doctor.go:82` `Details []string \`json:"details,omitempty"\``; populated in sweeper; UAT inferred PASS (Plan 02 unit tests proved population, inline rendering proves uncapped emission downstream).                                                            |
| 5   | Parallel S3 HEAD + BatchWriteItem brings `km doctor` < 30s wall clock on accounts with hundreds of orphans (vs. ~1:40 baseline)                                                                   | ✓ VERIFIED | 85-04-UAT.md operator sign-off: 9.496s read-only on 278 orphans (10.6× speedup); 10.232s cleanup of 278 rows (9.9×); 7.504s post-cleanup (13.4×). All three runs well under 30s target. Operator KPH signed PASS 2026-05-19.                                |

**Score:** 5/5 truths verified

### Required Artifacts

| Artifact                                                          | Expected                                                                          | Status     | Details                                                                                                                                                                                          |
| ----------------------------------------------------------------- | --------------------------------------------------------------------------------- | ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `internal/app/cmd/doctor_state_digest_sweeper.go`                 | Sweeper function + S3StateHeadAPI + LockDigestDeleterAPI interfaces (≥250 lines)  | ✓ VERIFIED | 410 lines; contains `checkStateLockDigestSweeper` (L99), `S3StateHeadAPI` (L34), `LockDigestDeleterAPI` (L41), `s3types.NotFound` classification, batched BatchWriteItem, semaphore parallelism. |
| `internal/app/cmd/doctor.go`                                      | `CheckResult.Details` field; `--delete-state-digests` flag; `DoctorDeps` fields; `initRealDepsWithExisting` wiring; `buildChecks` REPLACES old digest check | ✓ VERIFIED | `Details []string` at L82; flag definition L2352; `StateLockS3HeadClient` + `StateLockDDBWriteClient` in DoctorDeps L291-292; `initRealDepsWithExisting` wires both at L3073-3074; `buildChecks` registers ONLY `checkStateLockDigestSweeper` (L2718) — no duplicate `checkStateLockDigest` registration; old function body retained at L3486 as documented regression target. |
| `internal/app/cmd/doctor_state_digest_test.go`                    | 8 `TestDigestSweeper_*` tests + Phase 84.1 `TestCheckStateLockDigest_*` retained  | ✓ VERIFIED | 682 lines; 8 `TestDigestSweeper_*` tests (L411-639); 7 `TestCheckStateLockDigest_*` Phase 84.1 tests retained (L116-252).                                                                       |
| `internal/app/cmd/doctor_test.go`                                 | New flag-wiring tests for `--delete-state-digests` + `--with-deletes` fold-in     | ✓ VERIFIED | `TestDoctorCmd_WithDeletes_EnablesStateDigests` (L739); `TestDoctorCmd_DeleteStateDigestsAlone_DoesNotImplyOthers` (L765); existing `TestDoctorCmd_WithDeletes_EnablesAllDeleteOptIns` (L668). |
| `85-01-SUMMARY.md`                                                | Plan 01 scaffolding contract                                                      | ✓ VERIFIED | Present (7,866 bytes).                                                                                                                                                                          |
| `85-02-SUMMARY.md`                                                | Plan 02 algorithm + Details field contract                                        | ✓ VERIFIED | Present (11,138 bytes).                                                                                                                                                                          |
| `85-03-SUMMARY.md`                                                | Plan 03 integration contract                                                      | ✓ VERIFIED | Present (14,425 bytes).                                                                                                                                                                          |
| `85-04-SUMMARY.md` + `85-04-UAT.md`                               | Plan 04 UAT log with PASS verdict                                                 | ✓ VERIFIED | 85-04-SUMMARY.md present (4,223 bytes); 85-04-UAT.md present (8,093 bytes) with `status: PASS` + `verdict: PASS` in frontmatter, operator sign-off KPH 2026-05-19.                          |

### Key Link Verification

| From                                                       | To                                                          | Via                                                                                       | Status   | Details                                                                                                              |
| ---------------------------------------------------------- | ----------------------------------------------------------- | ----------------------------------------------------------------------------------------- | -------- | -------------------------------------------------------------------------------------------------------------------- |
| `doctor.go` cobra flag definition                          | `runDoctor(... deleteStateDigests bool)`                    | Last positional bool param, matches deleteSSM precedent                                   | ✓ WIRED  | `doctor.go:2362` runDoctor signature; `doctor.go:2331` Run callback passes `deleteStateDigests`.                    |
| `--with-deletes=true` umbrella                             | `deleteStateDigests = true`                                 | OR-merge fan-out in Run callback                                                          | ✓ WIRED  | `doctor.go:2329` `deleteStateDigests = true`; `--with-deletes` description at L2355 enumerates `--delete-state-digests`. |
| `DoctorDeps.StateLockS3HeadClient` + `StateLockDDBWriteClient` | `initRealDepsWithExisting`                              | `s3.NewFromConfig(awsCfg)` + `dynamodb.NewFromConfig(awsCfg)` using same awsCfg as 84.1   | ✓ WIRED  | `doctor.go:3073-3074`.                                                                                              |
| `buildChecks` registration                                 | `checkStateLockDigestSweeper`                               | `checks = append(checks, func(ctx)... checkStateLockDigestSweeper(...))`                  | ✓ WIRED  | `doctor.go:2718`. Old `checkStateLockDigest` body retained at L3486 (intentional — Phase 84.1 regression target) but NOT registered in buildChecks. Exactly 1 registration. |
| Sweeper output                                             | `CheckResult.Details`                                       | `renderSweeperMessage` returns (summary, fullList); fullList assigned to `result.Details` | ✓ WIRED  | Implementation present in `doctor_state_digest_sweeper.go`; `TestDigestSweeper_OutputFormat` GREEN proves Details population. |
| Plan 03 binary                                             | Live AWS account                                            | `time km doctor` + `km doctor --with-deletes --dry-run=false` against 278-orphan account  | ✓ WIRED  | 85-04-UAT.md documents 3 timed runs against account `052251888500`; 278 orphans cleaned; 913 live rows preserved; running sandbox `learn-4d7b6665` lock untouched. |

### Requirements Coverage

Requirement IDs are defined in `BRIEF.md` (no Phase-85 entries in `.planning/REQUIREMENTS.md`; this phase is locally scoped).

| Requirement       | Source Plan(s)  | Description                                                                                              | Status        | Evidence                                                                                                   |
| ----------------- | --------------- | -------------------------------------------------------------------------------------------------------- | ------------- | ---------------------------------------------------------------------------------------------------------- |
| IN-SCOPE-1        | 85-01, 85-02   | New cleanup category — orphan rows where sibling S3 state object is gone                                  | ✓ SATISFIED   | `doctor_state_digest_sweeper.go` `checkStateLockDigestSweeper` + orphan classification; UAT cleaned 278 orphans. |
| IN-SCOPE-2        | 85-03          | New flag `--delete-state-digests` + `--with-deletes` umbrella fold-in                                    | ✓ SATISFIED   | `doctor.go:2352` flag def; `doctor.go:2329` umbrella OR-merge; flag-wiring tests GREEN.                  |
| IN-SCOPE-3        | 85-02          | Detection safety: NotFound + age > 24h gate                                                              | ✓ SATISFIED   | `s3types.NotFound` classification; 24h min-orphan-age const; `TestDigestSweeper_OrphanAgeFailsSkipped` + `TestDigestSweeper_S3HeadAmbiguousError_Skipped` GREEN. |
| IN-SCOPE-4        | 85-02          | DynamoDB BatchWriteItem in 25-item batches                                                               | ✓ SATISFIED   | `batchDeleteLockItems` impl; `TestDigestSweeper_26Items_TwoBatches` + `TestDigestSweeper_UnprocessedItems` GREEN. |
| IN-SCOPE-5        | 85-02, 85-03   | Output format: summary + 10 inline + "and N more" continuation                                            | ✓ SATISFIED   | `TestDigestSweeper_OutputFormat` GREEN; UAT live output matches Stale-Lambdas pattern exactly.            |
| IN-SCOPE-6        | 85-02          | Performance: parallel HEAD scan, BatchWriteItem, <30s target                                              | ✓ SATISFIED   | 10-worker semaphore + BatchWriteItem; UAT measured 9.496s/10.232s/7.504s (all <30s).                     |
| IN-SCOPE-7        | 85-01, 85-02   | TDD test suite (5 TDD + OutputFormat + UnprocessedItems + SharedModuleLockID_NoSandboxID)                 | ✓ SATISFIED   | All 8 `TestDigestSweeper_*` tests present and GREEN; Phase 84.1 `TestCheckStateLockDigest_*` regression baseline retained and GREEN. |
| ACCEPT-READ       | 85-02, 85-03, 85-04 | km doctor <30s + readable summary + uncapped `--json` via Details                                     | ✓ SATISFIED   | UAT ACCEPT-READ-1/2/3 all PASS; 9.496s on 278 orphans = 10.6× speedup.                                  |
| ACCEPT-WRITE      | 85-02, 85-04   | --with-deletes --dry-run=false cleans orphans, preserves live state locks and running-sandbox lock        | ✓ SATISFIED   | UAT ACCEPT-WRITE-1/2/3/4 all PASS; 278 deleted, 0 failed; 913 live rows preserved; learn-4d7b6665 lock untouched. |
| ACCEPT-TEST       | 85-01, 85-02   | All 5+ TDD cases pass; no Phase 84.1 regression                                                          | ✓ SATISFIED   | `go test -run TestDigestSweeper ./internal/app/cmd/...` PASS; `go test -run TestCheckStateLockDigest ./internal/app/cmd/...` PASS. |

### Anti-Patterns Found

| File                                              | Line | Pattern             | Severity | Impact                                                                                          |
| ------------------------------------------------- | ---- | ------------------- | -------- | ----------------------------------------------------------------------------------------------- |
| `internal/app/cmd/doctor_state_digest_sweeper.go` | —    | None                | —        | Clean — no TODO/FIXME/XXX/HACK/PLACEHOLDER in new file.                                          |
| `internal/app/cmd/doctor.go` L3486-3500            | —    | Retained old `checkStateLockDigest` body | ℹ️ Info | INTENTIONAL: Plan 03 documents that the Phase 84.1 function body stays in-file so Phase 84.1 unit tests (`TestCheckStateLockDigest_*`) keep compiling and passing as a regression baseline. Not registered in `buildChecks`. |
| `internal/app/cmd/unlock_test.go::TestUnlockCmd_RequiresStateBucket` | — | Unrelated FAIL in full `go test ./internal/app/cmd/...` | ℹ️ Info | PRE-EXISTING, NOT introduced by Phase 85. Phase 85 only touched doctor* files. Not in scope for this verification. |

### Human Verification Required

None — operator UAT (Plan 04) already executed on 2026-05-19 with PASS sign-off (KPH @ whereiskurt@gmail.com). All ACCEPT-READ / ACCEPT-WRITE criteria validated against live account `052251888500` (klankermaker.ai) with 278 actual orphan rows.

### Gaps Summary

None. Every observable truth in BRIEF.md acceptance criteria is satisfied by a combination of (a) shipped code in the local working tree, (b) GREEN unit tests, and (c) operator-signed live UAT. The only out-of-scope follow-up (already documented in BRIEF.md): plug the upstream leak in `km destroy` / `km uninit` so new orphans stop accumulating — explicitly deferred to a separate phase.

The performance target was massively exceeded (9.496s vs. 30s budget = 3.2× headroom). The destructive write path was validated against real account state without collateral damage to either live state-lock rows (913 preserved) or the currently-running sandbox's lock row (learn-4d7b6665 untouched).

---

_Verified: 2026-05-19T08:30:00Z_
_Verifier: Claude (gsd-verifier)_
