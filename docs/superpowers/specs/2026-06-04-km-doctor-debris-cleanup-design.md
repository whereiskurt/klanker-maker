# km doctor — leaked per-sandbox debris cleanup (log groups, DDB rows, S3 lifecycle)

**Date:** 2026-06-04
**Status:** Design approved, pending implementation plan
**Author:** operator + Claude

## Problem

A read-only crawl of a live install (prefix `kph`, account 850919910932, us-east-1)
found the compute plane genuinely clean — 0 EC2, 0 EBS volumes, 0 self-owned AMIs —
but a clear trail of orphaned per-sandbox debris that teardown never reclaims, left
behind by ~90 destroyed sandboxes. None of it is expensive, but most of it grows
unbounded with every create/destroy cycle and clutters both the AWS console and
`km doctor` output.

Three families of leak (one set per sandbox ever created):

1. **CloudWatch log groups — the main offender.** 277 log groups, ~271 orphaned,
   none with a retention policy (they live forever). Three families:
   - `/aws/lambda/{prefix}-budget-enforcer-{id}` — 91 groups, ~124 MB
   - `/aws/lambda/{prefix}-github-token-refresher-{id}` — ~100 groups
   - the per-sandbox sandbox log-group family (`/…/sandboxes/{id}/…`) — ~90 groups, many 0-byte

   The per-sandbox Lambdas themselves are correctly deleted on teardown (only the 4
   management Lambdas remain), but AWS never deletes a Lambda's log group when the
   Lambda goes away — the classic CloudWatch leak.

2. **DynamoDB rows leaking on destroy.** Tables are management-owned (fine) but rows
   aren't purged on teardown:
   - `{prefix}-budgets` — 251 items (per-sandbox budget rows + AI-model rows)
   - `{prefix}-identities` — 85 items (~1 per sandbox ever created)
   - `{prefix}-slack-threads` — 53 items (thread mappings for dead sandboxes)
   - `{prefix}-sandboxes` — orphaned `status=failed` rows with no `instance_id`/`created_at`
     (failed creates that never got far enough to make log groups)

3. **S3 artifacts bucket — no lifecycle expiry.** Per-sandbox transient prefixes
   accumulate with no expiry rule: `logs/`, `remote-create/`, `agent-runs/`,
   `slack-inbound/`. (Build artifacts `toolchain/`, `sidecars/`, `rsync/` are current
   and expected.)

Out of scope: orphan EBS snapshots (those are manual operator backups, not
platform-generated — too risky to auto-touch).

This overlaps the known teardown gaps in `project_ttl_handler_ignores_retain_and_lock`.
This spec addresses **detection + reclamation + recurrence-prevention from the
`km doctor` side**; it does not change the teardown (`km destroy` / ttl-handler) path.

## Approach (chosen: A)

Three new checks that mirror the established `checkStale*` / `checkOrphaned*`
contract already used across the 25 `doctor_*.go` files
(`checkStaleSSMParameters`, `checkOrphanedArtifacts`, etc.):

> list resources → group by sandbox-id → diff against the `SandboxLister` active set
> → `WARN` with a `use --delete-X` hint → reclaim only under `--dry-run=false --delete-X`.

Rejected alternatives:
- **B — one combined "leaked debris" mega-check.** Breaks the one-check-per-file
  convention and collapses per-family delete granularity (couldn't purge DDB rows
  without also touching log groups).
- **C — split detection (`km doctor`) from reclamation (`km reap`/`km gc`).** Cleaner
  separation for the bulk delete, but a new command surface that diverges from the
  established "doctor both detects and reclaims under explicit opt-in flags" model
  already trusted for EBS/SSM/S3.

## Detailed design

### Files & checks

| File | Check | Cleanup flag | Guardrail flag |
|---|---|---|---|
| `internal/app/cmd/doctor_log_groups.go` (new) | `checkStaleLogGroups` | `--delete-logs` | `--set-log-retention` |
| `internal/app/cmd/doctor_ddb_rows.go` (new) | `checkOrphanedDDBRows` | `--delete-ddb-rows` | — |
| `internal/app/cmd/doctor_artifacts.go` (extend) | `checkS3LifecyclePolicy` | — | `--set-s3-lifecycle` |

Each is wired into `runDoctor`'s check slice and run via `runChecks`, returning the
standard `CheckResult` (`CheckOK` / `CheckWarn` / `CheckSkipped`). New mocked-API
interfaces are added to the doctor deps, matching how `SSMDeleterAPI` /
`S3CleanupAPI` are structured:
- `CWLogsCleanupAPI` — `DescribeLogGroups`, `DeleteLogGroup`, `PutRetentionPolicy`
- `DDBScanDeleteAPI` — `Scan`, `BatchWriteItem` / `DeleteItem`
- `S3LifecycleAPI` — `GetBucketLifecycleConfiguration`, `PutBucketLifecycleConfiguration`

### Detection logic

**Log groups** — enumerate the three name templates, extract `{id}`, orphan = id ∉
active sandboxes. Lambda families are clearly prefixed; the sandbox log-group base is
an **open research item** (the crawl shows `/km/sandboxes/`, the profile declares
`/klanker-maker/sandboxes`) — planning derives the exact templates from the
compiler / create-handler rather than hardcoding a guess.

**DDB rows** — scan four tables, extract sandbox-id, orphan = id ∉ active set:
- `{prefix}-budgets` — **only per-sandbox rows.** AI-model rows (`BUDGET#ai#{modelID}`,
  the shape from `BUDGET#ai#{modelID}` metering, Phase 88) are explicitly preserved.
- `{prefix}-identities` — ~1 row/sandbox.
- `{prefix}-slack-threads` — sandbox-id is an **attribute** (`sandbox_id`), not the key.
- `{prefix}-sandboxes` — purge only rows with `status=failed` **and** missing
  `instance_id` (matches the crawl; avoids nuking in-flight creates).

Exact key schema per table is an **open research item** — derived from the `pkg/aws`
table definitions during planning, not assumed.

**S3 lifecycle** — `GetBucketLifecycleConfiguration` on the artifacts bucket; `WARN`
if no expiry rule covers the transient prefixes `logs/`, `remote-create/`,
`agent-runs/`, `slack-inbound/`. Build-artifact prefixes (`toolchain/`, `sidecars/`,
`rsync/`) are never expired.

### Remediation & guardrails

- **`--delete-logs` / `--delete-ddb-rows`** — bulk delete orphans, paginated, report
  `deleted/failed` counts. Identical safety model to `--delete-ssm`: report-only
  unless BOTH `--dry-run=false` AND the per-family flag are set.
- **`--set-log-retention`** — set `retentionInDays` (default 30) on management +
  live-sandbox groups that currently have no retention. Idempotent (no-op if already
  set). Does not delete anything.
- **`--set-s3-lifecycle`** — `PutBucketLifecycleConfiguration` expiring the transient
  prefixes after N days (default 30). Idempotent; preserves any existing unrelated rules.
- **`--with-deletes`** — extended to imply `--delete-logs` and `--delete-ddb-rows`
  alongside the existing delete flags.

### Config knobs (`internal/app/config/config.go`)

Two new keys, each requiring the full five-touchpoint pattern (field + `SetDefault`
+ **merge-list entry** + accessor + clamp), per `project_config_key_merge_list`:

- `doctor_log_retention_days` (default 30) → `DoctorLogRetentionDays`
- `doctor_s3_expire_days` (default 30) → `DoctorS3ExpireDays`

The transient-prefix list for S3 is hardcoded (YAGNI — no knob until a second
install needs a different set).

### Safety / edge cases

- **AI-model budget rows preserved** — never treated as per-sandbox orphans.
- **In-flight-create race** — matched to existing checks (point-in-time diff against
  the active set); the `status=failed && no instance_id` guard further protects
  in-progress sandbox rows.
- **Multi-install** — every resource name is `resource_prefix`-scoped already;
  `--ignore-prefix` / `doctor_ignore_prefixes` honored so sibling installs aren't touched.
- **Pagination** — full pagination on all scans/describes (log groups, DDB scans).
- **Idempotent guardrails** — re-running `--set-log-retention` / `--set-s3-lifecycle`
  is a no-op when already correct.

### Testing

Mirror the existing `doctor_*_test.go` table-driven style with mocked APIs:
- orphan-detected → WARN with hint
- all-active → OK
- dry-run (or flag unset) → WARN, no mutation
- `--dry-run=false --delete-X` → correct `deleted/failed` counts
- **AI-model budget row preservation** (regression guard)
- **`status=failed && no instance_id` guard** for sandbox rows
- guardrail-already-set → idempotent no-op
- pagination across multiple pages

## Open research items (resolved during planning, not guessed)

1. Exact CloudWatch log-group name templates — especially the per-sandbox sandbox
   log-group base — derived from the compiler / create-handler.
2. Exact DynamoDB key schemas for `{prefix}-budgets`, `-identities`,
   `-slack-threads`, `-sandboxes` — derived from `pkg/aws` table definitions.

## Root-cause source fix (added 2026-06-04 — scope expansion)

The doctor checks above are a *safety net*; they reclaim the leak but don't stop
it. Research for this phase found the leak's root cause: the per-sandbox
CloudWatch log groups are **created** with a hardcoded `km-`/`/km/` prefix while
teardown (`destroy.go` / `ttl-handler` / `pkg/aws/cloudwatch.go`) **deletes** using
the dynamic `{resource_prefix}`. On the default `km` install the two match and
teardown works; on any non-default-prefix install (e.g. `kph`) they never match,
so every group leaks. This is the `project_teardown_prefix_asymmetry` issue —
everything else already migrated to `resource_prefix`; these are the stragglers.

**The fix (finish the migration — no-op on the default `km` install):**

| Location | Hardcoded | → |
|---|---|---|
| `infra/modules/budget-enforcer/v1.0.0/main.tf:260` | `/aws/lambda/km-budget-enforcer-${var.sandbox_id}` | `${var.resource_prefix}-` |
| `infra/modules/github-token/v1.0.0/main.tf:159` (+:14 cosmetic) | `/aws/lambda/km-github-token-refresher-…` | `${var.resource_prefix}-` |
| `pkg/compiler/userdata.go:1197` | `CW_LOG_GROUP=/km/sandboxes/{{ .SandboxID }}/` | `/{{ .ResourcePrefix }}/sandboxes/…` |
| `pkg/compiler/service_hcl.go:355,361` | `/km/sandboxes/…`, `/km/sidecars/…` | dynamic (existing `TODO(plan-04)`) |
| `infra/modules/create-handler/v1.0.0/main.tf:41-42` | IAM log-group ARN `/km/sandboxes/*` | `/${var.resource_prefix}/sandboxes/*` (lockstep — else create-handler loses log-write perm) |

**Interaction with detection:** `checkStaleLogGroups` must match BOTH the legacy
`km-`/`/km/` names (existing orphans) AND the new `{prefix}-`/`/{prefix}/` names,
deduped by name. On the default install both collapse to the same `km` set.

**Coupling to verify, not assume:** the SCP module references role-name patterns
`km-budget-enforcer-*` / `km-github-token-refresher-*` (`scp/v1.0.0/main.tf`). The
Lambda *role* names are already `${var.resource_prefix}-…`, so those SCP patterns
are a separate pre-existing `km`-hardcode — the plan notes it and fixes it only if
confirmed load-bearing for non-default installs; no silent scope creep.

**Safety guarantee:** byte-identical compiled output for a `km`-prefix fixture
(asserted in tests) so the default install is provably unaffected.

## Rollout

**Two parts.**

1. **Doctor checks** — operator-side binary only: `make build`, then `km doctor`
   to detect, `km doctor --dry-run=false --delete-logs --delete-ddb-rows` to
   reclaim, and `km doctor --dry-run=false --set-log-retention --set-s3-lifecycle`
   to install the guardrails.
2. **Root-cause prefix fix** — touches TF modules + the compiler, so it needs a
   deploy: `make build-lambdas` (clean) + `km init --dry-run=false`. Existing
   sandboxes do NOT retroactively rename (they keep legacy `km-` groups, which the
   doctor check still reclaims on teardown). No-op on the default `km` install.
