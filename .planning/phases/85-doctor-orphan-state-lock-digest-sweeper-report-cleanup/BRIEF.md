# Phase 85 — doctor: orphan state-lock digest sweeper + report cleanup

> **Note on numbering:** Originally brainstormed as "Phase 84.5" but landed as Phase 85
> because `/gsd:add-phase` allocates integer phases. Roadmap position is equivalent
> (last phase in the v1.0 milestone, after 84.4.1). Use `/gsd:insert-phase` next time
> if a decimal slot is wanted.

> **Source:** brainstorming session 2026-05-18 (km doctor cleanup UX). Operator
> reported ~275 orphan state-lock digest entries accumulated across 84.x
> multi-install testing and asked to extend `--with-deletes` to handle them.

## Motivation

Phase 84.1 added `checkStateLockDigest` (in `internal/app/cmd/doctor.go`) — it
detects DDB lock rows in the Terragrunt state-lock table whose sibling S3 state
object has been deleted. On the operator's current account, **~275 such orphans
have accumulated** from a mix of:

1. **`km destroy` leaks** — the destroy path deletes the S3 state object but
   leaves the `terraform.tfstate-md5` DDB row behind.
2. **Manual S3 cleanup** during 84.x rebuilds and multi-install testing —
   operators emptied state-bucket prefixes without touching the lock table.

Today the check has **no cleanup path** — remediation is hand-running
`aws dynamodb update-item` for each row. The warn message dumps all 275 items
into a single unreadable line that obscures every other warning.

Observed runtime: `km doctor` takes ~1:41 wall clock on this account; the
digest check (sequential S3 HEAD per item) dominates.

## In scope

1. **New cleanup category** — orphan rows in the Terragrunt state-lock DDB
   table where the matching S3 state object is definitively gone.

2. **Gate (matches existing per-category pattern):**
   - New flag: `--delete-state-digests`.
   - Also enabled by the `--with-deletes` umbrella (which today turns on
     `--delete-{ebs,sqs,s3,lambdas,ssh,ssm}`).
   - `--dry-run=true` (default) still reports without deleting.

3. **Detection safety (the sensitive part — this touches Terraform's lock
   table; a false positive could corrupt a live state lock):**
   A digest row is deletable iff **both** of the following hold:
   - S3 HEAD on the sibling `terraform.tfstate` returns a **definitive
     NoSuchKey** (a generic error — 5xx, timeout, throttling — must NOT
     delete; log as "could not verify").
   - The DDB row's age exceeds the configurable threshold (default **24h**).
     Matches the existing in-flight-create age guard pattern elsewhere in
     doctor.

4. **Deletion path:** DynamoDB `BatchWriteItem` (25 items per batch). No
   re-HEAD immediately before delete (explicitly chosen over maximum-paranoia
   option during brainstorming).

5. **Output format** (replaces the 275-line one-liner):
   - Top-line warning summary:
     `state digest mismatch in N item(s) (M orphan: state object missing, K other)`
   - Inline block: first **10** items, formatted to match the Stale Lambdas
     check.
   - `… and N more (use --json for full list)` continuation line.
   - `--json` output continues to emit every item (no cap).

6. **Performance:**
   - Parallelize the per-item S3 HEAD scan with a worker pool (target ~10
     concurrent HEADs).
   - Use `BatchWriteItem` for deletes (25 per batch).
   - **Target:** full `km doctor` run < 30s on the operator's current account
     (vs. ~1:40 today).

7. **Tests (TDD, in `internal/app/cmd/doctor_state_digest_test.go`):**
   - orphan + age-passes → row deleted.
   - orphan + age-fails → row skipped (never deleted within guard window).
   - S3 HEAD returns network/5xx error → row skipped (never deletes on
     ambiguous error).
   - Non-orphan mismatch type (live S3 object + stale MD5) → never deleted
     by this sweeper.
   - Batch of 26 items → splits into 2 BatchWriteItem calls correctly.

## Out of scope — flagged as follow-up phases

- **Plug the underlying leak.** `km destroy` / `km uninit` should delete the
  `terraform.tfstate-md5` DDB row alongside the S3 state object so new
  orphans stop accumulating. This is a preventative fix at the destruction
  path, not the doctor sweeper path. Separate phase.

- **Sweeper for other digest-mismatch types.** Rows where the S3 object
  EXISTS but the MD5 doesn't match. These are reported by the check today
  and will continue to be reported, but auto-deletion is not safe without
  more design work (the live S3 object means there's an active state lock
  intent).

## Acceptance

- **Read path:** `km doctor` on the operator's current account completes in
  **≤30s wall clock**. The state-digest warning summarizes to one readable
  line plus a 10-item inline preview. `--json` still emits the full list.

- **Write path:** `km doctor --with-deletes --dry-run=false` cleans the ~275
  orphan rows. Any live-state mismatches (S3 object present, MD5 stale) are
  **untouched**. The running sandbox's lock row is **untouched**.

- **Test path:** all 5 TDD cases above pass.

## Design decisions captured during brainstorming

| Decision | Choice | Reasoning |
|---|---|---|
| Orphan source | both leak + manual cleanup | Operator confirmed both contribute; sweeper handles accumulation, leak fix is separate phase. |
| Gate shape | `--with-deletes` umbrella + per-category flag | Operator chose umbrella over conservative-only-per-category; per-category flag preserved for surgical use. |
| Safety guards | strict 404 + age guard | Avoid mass-delete from a network blip; avoid racing in-flight km create/destroy. Re-HEAD at delete time was offered but declined. |
| Output format | summary + cap at 10 inline | Avoids the 275-line wall-of-text; full list still in `--json`. |
| Phase placement | new integer phase (landed as 85) | Multi-install thesis (84.4.x) is unrelated; doctor cleanup deserves its own slot. |

## Suggested next move

Run `/gsd:plan-phase 85` — the planner agent will produce
`.planning/phases/85-…/RESEARCH.md` and `.planning/phases/85-…/PLAN.md`
from this brief.
