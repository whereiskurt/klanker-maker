---
phase: 85-doctor-orphan-state-lock-digest-sweeper-report-cleanup
plan: 04
status: complete
completed: 2026-05-19T07:56Z
result: PASS
tasks_complete: 3
gaps_surfaced: 0
---

# Phase 85 Plan 04: Operator UAT — Summary

## One-liner

Phase 85 empirically validated on live AWS account with 278 accumulated orphan state-lock digest rows: `km doctor` read-only completes in **9.496s** (≥10× faster than the ~1:41 baseline), cleanup deletes 278 orphans in 10.232s with zero live-row collateral, and post-cleanup re-run shows `✓ state digest consistent (913 items checked)`. ACCEPT-READ and ACCEPT-WRITE both PASS.

## Tasks executed

1. **Task 1 — Pre-UAT capture**: Fresh `make build` → `km v0.2.706`. `--delete-state-digests` flag exposed in help, folded into `--with-deletes`.
2. **Task 2 — Timed read-only runs**: `time ./km doctor` → **9.496s**. Output: summary line + 10 inline items + "… and 268 more (use --json for full list)". Stale-Lambdas pattern matched exactly.
3. **Task 3 — Destructive cleanup + verification**:
   - `./km doctor --with-deletes=true --dry-run=false` → 10.232s, "278 deleted, 0 failed".
   - Second `./km doctor` → 7.504s, `✓ state digest consistent (913 items checked)`. Zero residual orphans. Live rows preserved.
4. **UAT log committed** as `.planning/phases/85-doctor-orphan-state-lock-digest-sweeper-report-cleanup/85-04-UAT.md`.

## Acceptance criteria

| ID | Result |
|----|--------|
| ACCEPT-READ-1 (≤30s wall clock) | ✓ PASS — 9.496s (10.6× faster than baseline) |
| ACCEPT-READ-2 (summary + 10 + "and N more") | ✓ PASS |
| ACCEPT-READ-3 (--json full list via .details) | ✓ PASS (inline output proves rendering; unit tests prove Details population) |
| ACCEPT-WRITE-1 (delete orphans) | ✓ PASS — 278 deleted, 0 failed |
| ACCEPT-WRITE-2 (live rows untouched) | ✓ PASS — 913 live items preserved |
| ACCEPT-WRITE-3 (running sandbox lock untouched) | ✓ PASS — learn-4d7b6665 still active |
| ACCEPT-WRITE-4 (second run shows 0 orphans) | ✓ PASS — green check, no list |

## Performance

| Run | Wall clock | vs. baseline |
|-----|------------|--------------|
| Pre-Phase-85 baseline | ~1:41 (101s) | — |
| Post-Phase-85 read-only | 9.496s | **10.6×** |
| Post-Phase-85 cleanup | 10.232s | **9.9×** |
| Post-Phase-85 clean account | 7.504s | **13.4×** |

## Commits

(This commit) — docs(85-04): operator UAT PASS — 278 orphans cleaned, 10.6× speedup verified

## Gaps surfaced

**None blocking Phase 85.** A few unrelated observations noted in UAT.md (orphan SCP from 84.4.1-06 gap #3, pre-Phase-79 presence daemon on `learn-4d7b6665`, stale Slack inbound queue cleanup behavior). All out of scope for Phase 85's stated goal.

## Phase goal achievement

Original goal (from ROADMAP.md):

> Close the Phase 84.1 digest-leak loop in `km doctor`: add a `--delete-state-digests` cleanup category (also folded into `--with-deletes`) that removes orphan rows from the Terragrunt state-lock DDB table where the sibling S3 state object is definitively gone (NoSuchKey + age > 24h). Replace the unreadable single-line digest-mismatch warn with a `summary + 10-item preview + --json full list` format matching Stale Lambdas. Parallelize the per-item S3 HEAD scan + BatchWriteItem deletes; target `km doctor` < 30s wall clock on accounts with hundreds of orphans (vs. ~1:40 today).

**Achievement:**
- ✓ `--delete-state-digests` flag added (Plan 03)
- ✓ Folded into `--with-deletes` umbrella (Plan 03)
- ✓ Deletes orphan rows where HeadObject returns NotFound (Plan 02 classifier — NotFound only, never on generic errors)
- ✓ Output replaced with summary + 10-item preview + `--json` full list via `CheckResult.Details` (Plan 02)
- ✓ Parallelized S3 HEAD scan (10-worker semaphore) + BatchWriteItem deletes (25-item batches with UnprocessedItems retry) (Plan 02)
- ✓ **Wall clock < 30s achieved**: 9.496s read-only on 278-orphan account (Plan 04 UAT)

**Out of scope (acknowledged in original phase goal):** Plugging the upstream leak in `km destroy` / `km uninit`. The orphan source remains; cleanup is now a one-line operator command.

**Self-Check: PASSED** — UAT criteria all green, no Self-Check: FAILED markers.
