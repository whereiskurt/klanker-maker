# Phase 94: km doctor leaked per-sandbox debris cleanup - Context

**Gathered:** 2026-06-04
**Status:** Ready for planning
**Source:** Design spec (brainstorming) — `docs/superpowers/specs/2026-06-04-km-doctor-debris-cleanup-design.md`

<domain>
## Phase Boundary

Enhance the existing `km doctor` command to detect, reclaim, and prevent the
orphaned per-sandbox debris that teardown (`km destroy` / ttl-handler) never
reclaims. Discovered in a live read-only crawl of the `kph` install (account
850919910932, us-east-1): the compute plane was clean (0 EC2/EBS/AMIs) but ~90
destroyed sandboxes left a trail of unbounded-growth debris.

**Three resource families IN scope:**
1. **CloudWatch log groups** (~271 orphaned, the main offender) — three name
   families, none with a retention policy:
   - `/aws/lambda/{prefix}-budget-enforcer-{id}` (~91)
   - `/aws/lambda/{prefix}-github-token-refresher-{id}` (~100)
   - the per-sandbox sandbox log-group family (`/…/sandboxes/{id}/…`, ~90)
   AWS never deletes a Lambda's log group when the Lambda is deleted (classic leak).
2. **DynamoDB row leaks** — rows not purged on teardown:
   - `{prefix}-budgets` (~251 incl. per-sandbox rows + AI-model rows)
   - `{prefix}-identities` (~85, ~1/sandbox)
   - `{prefix}-slack-threads` (~53)
   - `{prefix}-sandboxes` `status=failed` rows with no `instance_id` (failed creates)
3. **S3 lifecycle** — artifacts bucket has no expiry rule on transient prefixes
   (`logs/`, `remote-create/`, `agent-runs/`, `slack-inbound/`).

**ALSO IN scope (added 2026-06-04 — root-cause source fix):** finish the
`resource_prefix` migration so the leak stops at the source, not just the doctor
safety net. The per-sandbox Lambda log groups and the sandbox/sidecar audit log
groups are CREATED with a hardcoded `km-`/`/km/` prefix while teardown
(`destroy.go`/`ttl-handler`/`pkg/aws/cloudwatch.go`) DELETES using the dynamic
`{resource_prefix}` — so on any non-default-prefix install (e.g. `kph`) the names
never match and teardown silently leaks every group. This is the
`project_teardown_prefix_asymmetry` issue. See the "Root-cause prefix fix" section
in <decisions>.

**OUT of scope:** orphan EBS snapshots (manual operator backups, too risky to
auto-touch). Adding NEW destroy-time deletion logic — the source fix makes the
EXISTING teardown code match (by fixing creation names); it does not add new
cleanup paths.

**Deployment surface:** TWO parts. (1) The doctor checks are operator-side binary
only (`make build`, run `km doctor`). (2) The root-cause prefix fix touches TF
modules (budget-enforcer, github-token, create-handler) + the compiler, so it
requires `make build-lambdas` (clean) + `km init --dry-run=false` to deploy, and
existing sandboxes do NOT retroactively rename (they keep their legacy `km-`
groups, which the doctor check still reclaims). The prefix fix is a **no-op on the
default `km` install** (`km`→`km`); it only changes behavior for non-default
multi-installs. Overlaps the known teardown gaps in the
`project_ttl_handler_ignores_retain_and_lock` and `project_teardown_prefix_asymmetry`
memories.
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Architecture — Approach A (chosen)
- Three new checks mirroring the established `checkStale*`/`checkOrphaned*`
  contract used across the 25 `doctor_*.go` files: **list resources → group by
  sandbox-id → diff against the `SandboxLister` active set → `WARN` with a
  `use --delete-X` hint → reclaim only under `--dry-run=false --delete-X`.**
- Rejected B (one combined "leaked debris" mega-check — breaks one-check-per-file
  convention + collapses per-family delete granularity).
- Rejected C (split detection in `km doctor` from reclamation in a new
  `km reap`/`km gc` command — new surface, diverges from the trusted
  "doctor both detects and reclaims under explicit opt-in flags" model).

### Files & checks
- `internal/app/cmd/doctor_log_groups.go` (new) — `checkStaleLogGroups`;
  flags `--delete-logs` (cleanup) + `--set-log-retention` (guardrail).
- `internal/app/cmd/doctor_ddb_rows.go` (new) — `checkOrphanedDDBRows`;
  flag `--delete-ddb-rows`.
- `internal/app/cmd/doctor_artifacts.go` (extend) — `checkS3LifecyclePolicy`;
  flag `--set-s3-lifecycle` (guardrail).
- Each wired into `runDoctor`'s check slice, run via `runChecks`, returning the
  standard `CheckResult` (`CheckOK`/`CheckWarn`/`CheckSkipped`).
- New mocked-API interfaces in the doctor deps, mirroring `SSMDeleterAPI`/
  `S3CleanupAPI`: `CWLogsCleanupAPI` (DescribeLogGroups, DeleteLogGroup,
  PutRetentionPolicy), `DDBScanDeleteAPI` (Scan, BatchWriteItem/DeleteItem),
  `S3LifecycleAPI` (Get/PutBucketLifecycleConfiguration).

### Root-cause prefix fix (Wave 5 — added 2026-06-04)
Finish the `resource_prefix` migration for the per-sandbox CloudWatch log groups so
new sandboxes are correctly namespaced and the existing teardown code matches them.
Everything is ALREADY dynamic except these spots (change `km-`/`/km/` → dynamic):
- `infra/modules/budget-enforcer/v1.0.0/main.tf:260` — `/aws/lambda/km-budget-enforcer-${var.sandbox_id}` → `/aws/lambda/${var.resource_prefix}-budget-enforcer-${var.sandbox_id}`
- `infra/modules/github-token/v1.0.0/main.tf:159` — `/aws/lambda/km-github-token-refresher-...` → `${var.resource_prefix}-...` (and :14 KMS description, cosmetic)
- `pkg/compiler/userdata.go:1197` — `CW_LOG_GROUP=/km/sandboxes/{{ .SandboxID }}/` → `/{{ .ResourcePrefix }}/sandboxes/{{ .SandboxID }}/` (`.ResourcePrefix` already in scope, used at userdata.go:286)
- `pkg/compiler/service_hcl.go:355` — `/km/sandboxes/{{ .SandboxID }}/` → dynamic (has existing `TODO(plan-04)`)
- `pkg/compiler/service_hcl.go:361` — `/km/sidecars/{{ .SandboxID }}` → dynamic
- `infra/modules/create-handler/v1.0.0/main.tf:41-42` — IAM log-group ARNs `/km/sandboxes/*` → `/${var.resource_prefix}/sandboxes/*` **(MUST change in lockstep or the create-handler loses log-write permission after the path moves)**
- COUPLING TO VERIFY in the plan: the SCP module references role-name patterns
  `km-budget-enforcer-*` / `km-github-token-refresher-*` (scp/v1.0.0/main.tf:23,31).
  The Lambda ROLE names are already `${var.resource_prefix}-...`, so those SCP
  patterns are a SEPARATE pre-existing `km`-hardcode — note it, fix only if the
  plan confirms it's load-bearing for non-default installs; do not silently widen scope.
- Guarantee: a no-op diff on the default `km` install (`km`→`km`). Verify by
  asserting compiled output byte-identity for a `km`-prefix fixture.

### Detection rules
- **Log groups:** enumerate the name templates, extract `{id}`, orphan = id ∉
  active sandboxes. **Match BOTH the legacy literal `km-`/`/km/` names (existing
  orphans on non-default installs) AND the new `{resource_prefix}-`/`/{prefix}/`
  names (post-fix sandboxes), deduped by log-group name** — on the default install
  both collapse to the same `km` set. Four families: `…/km-budget-enforcer-{id}`,
  `…/km-github-token-refresher-{id}`, `/km/sandboxes/{id}/`, `/km/sidecars/{id}`
  (plus their `{prefix}-`/`/{prefix}/` twins). Management Lambda groups
  (`/aws/lambda/{prefix}-{create-handler,email-handler,slack-bridge,ttl-handler}`)
  are NEVER deleted — only eligible for `--set-log-retention`.
- **DDB rows:** scan four tables, extract sandbox-id, orphan = id ∉ active set.
  - `{prefix}-budgets`: **only per-sandbox rows.** AI-model rows
    (`BUDGET#ai#{modelID}`, the Phase 88 metering shape) are explicitly preserved.
  - `{prefix}-identities`: ~1 row/sandbox.
  - `{prefix}-slack-threads`: sandbox-id is an **attribute** (`sandbox_id`), not the key.
  - `{prefix}-sandboxes`: purge only rows with `status=failed` **AND** missing
    `instance_id` (avoids nuking in-flight creates).
- **S3 lifecycle:** GetBucketLifecycleConfiguration on the artifacts bucket; WARN
  if no expiry rule covers the transient prefixes. Build artifacts
  (`toolchain/`, `sidecars/`, `rsync/`) are never expired.

### Remediation & guardrails
- `--delete-logs` / `--delete-ddb-rows`: bulk delete orphans, paginated, report
  `deleted/failed` counts. Report-only unless BOTH `--dry-run=false` AND the
  per-family flag are set (same safety model as `--delete-ssm`).
- `--set-log-retention`: set `retentionInDays` (default 30) on management +
  live-sandbox groups lacking it. Idempotent no-op if already set.
- `--set-s3-lifecycle`: PutBucketLifecycleConfiguration expiring the transient
  prefixes after N days (default 30). Idempotent; preserves unrelated rules.
- `--with-deletes`: extended to imply `--delete-logs` + `--delete-ddb-rows`.

### Config knobs (`internal/app/config/config.go`)
- `doctor_log_retention_days` (default 30) → `DoctorLogRetentionDays`
- `doctor_s3_expire_days` (default 30) → `DoctorS3ExpireDays`
- Each via the **five-touchpoint pattern**: struct field + `SetDefault` +
  **merge-list entry** (per `project_config_key_merge_list` memory — required or
  the yaml value is silently ignored) + `Get*` accessor + clamp (<=0 → default).
- S3 transient-prefix list is hardcoded (YAGNI — no knob yet).

### Safety / edge cases
- AI-model budget rows never treated as per-sandbox orphans.
- In-flight-create race matched to existing checks (point-in-time diff) + the
  `status=failed && no instance_id` guard for sandbox rows.
- Multi-install honored: all names `resource_prefix`-scoped; `--ignore-prefix` /
  `doctor_ignore_prefixes` respected.
- Full pagination on all scans/describes; guardrails idempotent.

### Testing
- Mirror the existing `doctor_*_test.go` table-driven style with mocked APIs.
  Required cases: orphan-detected→WARN, all-active→OK, dry-run/flag-unset→WARN
  no-mutation, `--dry-run=false --delete-X`→correct deleted/failed counts,
  **AI-model budget row preservation** (regression guard), **`status=failed &&
  no instance_id` guard**, guardrail-already-set→idempotent no-op, pagination.

### Claude's Discretion
- Plan/wave decomposition (e.g. one plan per family, or shared deps + three checks).
- Exact mock-interface method signatures and test fixture shapes.
- Whether `checkS3LifecyclePolicy` is a new file or an extension within
  `doctor_artifacts.go` (spec says extend; planner may split if cleaner).
- Naming of the guardrail-application helper functions.
</decisions>

<specifics>
## Specific Ideas

- Follow the exact shape of `checkStaleSSMParameters` (doctor.go) and
  `checkOrphanedArtifacts` (doctor_artifacts.go) — both already implement the
  list→group→diff→WARN→delete-under-flag pattern this phase replicates.
- `SandboxLister.ListSandboxes(ctx, false)` is the active-sandbox source of truth
  (same call the existing stale checks use).
- Config precedent to copy verbatim: `doctor_stale_ami_days` (field
  `DoctorStaleAMIDays`, `SetDefault("doctor_stale_ami_days", 30)`, merge-list
  entry at config.go ~line 375, accessor, clamp at ~line 495).
- Flag-wiring precedent: the `--delete-ebs/-sqs/-s3/-lambdas/-ssh/-ssm` block in
  `NewDoctorCmdWithDeps` (~line 2398) + the `runDoctor` signature threading.
</specifics>

<deferred>
## Deferred / Out of Scope

- Orphan EBS snapshot detection/deletion (manual operator backups — risky).
- Adding NEW destroy-time deletion logic to `km destroy` / ttl-handler (the
  root-cause fix makes the EXISTING teardown match by fixing creation names; no new
  cleanup path is added).
- Retroactively renaming existing sandboxes' `km-` log groups (they keep legacy
  names; doctor reclaims them at teardown via the both-names match).
- TTL attributes on DDB tables as a native expiry mechanism (schema change).
- Per-install configurable S3 transient-prefix list (YAGNI until needed).

</deferred>

---

## Open research items (MUST be resolved by the researcher, NOT guessed)

1. **Exact CloudWatch log-group name templates** — especially the per-sandbox
   sandbox log-group base. The crawl shows `/km/sandboxes/{id}/`; the profile
   declares `logGroup: /klanker-maker/sandboxes`; the prefix is `kph`. Derive the
   real templates (Lambda log groups AND the sandbox/command/network log groups)
   from `pkg/compiler` and the create-handler / Lambda module definitions.
2. **Exact DynamoDB key schemas** for `{prefix}-budgets`, `-identities`,
   `-slack-threads`, `-sandboxes` — derive from the `pkg/aws` table definitions
   and record structs (PK/SK names, the `BUDGET#`/`BUDGET#ai#` row discriminator,
   the `sandbox_id` attribute on slack-threads, the `status`/`instance_id`
   attributes on sandboxes).

---

*Phase: 94-km-doctor-leaked-per-sandbox-debris-cleanup-log-groups-ddb-rows-s3-lifecycle*
*Context derived 2026-06-04 from the approved design spec (brainstorming session)*
