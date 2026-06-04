# Phase 94: km doctor leaked per-sandbox debris cleanup — Research

**Researched:** 2026-06-04
**Domain:** Go CLI extension — `km doctor` check/cleanup, AWS CW Logs, DynamoDB, S3 lifecycle
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **Architecture:** Approach A — three new checks mirroring `checkStale*`/`checkOrphaned*` contract.
  list → group by sandbox-id → diff vs `SandboxLister` active set → WARN with `use --delete-X` hint → delete only under `--dry-run=false --delete-X`.
- **Files:**
  - `internal/app/cmd/doctor_log_groups.go` (new) — `checkStaleLogGroups`; flags `--delete-logs`, `--set-log-retention`.
  - `internal/app/cmd/doctor_ddb_rows.go` (new) — `checkOrphanedDDBRows`; flag `--delete-ddb-rows`.
  - `internal/app/cmd/doctor_artifacts.go` (extend) — `checkS3LifecyclePolicy`; flag `--set-s3-lifecycle`.
- **New mocked-API interfaces:** `CWLogsCleanupAPI` (DescribeLogGroups, DeleteLogGroup, PutRetentionPolicy), `DDBScanDeleteAPI` (Scan, BatchWriteItem/DeleteItem), `S3LifecycleAPI` (Get/PutBucketLifecycleConfiguration).
- **Detection rules:**
  - Log groups: enumerate three name templates, extract `{id}`, orphan = id not in active sandboxes.
  - DDB rows: scan four tables, extract sandbox-id; AI-model rows preserved; sandbox rows guarded by `status=failed` check.
  - S3 lifecycle: WARN if no expiry rule on transient prefixes.
- **Remediation:** `--delete-logs` / `--delete-ddb-rows` bulk delete; `--set-log-retention` and `--set-s3-lifecycle` are idempotent guardrails. `--with-deletes` extended to imply `--delete-logs` + `--delete-ddb-rows`.
- **Config knobs:** `doctor_log_retention_days` + `doctor_s3_expire_days`, both via the five-touchpoint pattern.
- **Safety:** AI-model budget rows never deleted; `status=failed` guard for sandbox rows; pagination required; `--ignore-prefix` honored.
- **Testing:** Table-driven mocked-API tests mirroring `doctor_*_test.go` style; required cases include AI-model budget row preservation and `status=failed` guard.
- **Deployment:** operator-side binary only; no Lambda/terragrunt deploy; `make build` and run.

### Claude's Discretion

- Plan/wave decomposition (e.g. one plan per family, or shared deps + three checks).
- Exact mock-interface method signatures and test fixture shapes.
- Whether `checkS3LifecyclePolicy` is a new file or extension within `doctor_artifacts.go`.
- Naming of the guardrail-application helper functions.

### Deferred Ideas (OUT OF SCOPE)

- Orphan EBS snapshot detection/deletion (manual operator backups — risky).
- Changing teardown (`km destroy` / ttl-handler) to clean up at destroy-time.
- TTL attributes on DDB tables as a native expiry mechanism (schema change).
- Per-install configurable S3 transient-prefix list (YAGNI until needed).
</user_constraints>

---

## Summary

Phase 94 extends `km doctor` with three new checks that detect, reclaim, and prevent orphaned
per-sandbox debris left behind by ~90 destroyed sandboxes: CloudWatch log groups (the main
offender at ~271 orphaned), DynamoDB rows across four tables (~394 leaked rows), and an
absent S3 lifecycle rule on transient artifact prefixes.

All three checks follow the established `checkStale*`/`checkOrphaned*` contract and wire into
`runDoctor` via `buildChecks`. Two new `km-config.yaml` knobs follow the five-touchpoint
config pattern. No Lambda or Terraform deploy is needed — this is a pure operator-binary change.

**Primary recommendation:** Copy `checkStaleSSMParameters` and `checkOrphanedArtifacts` as
structural templates; replace the list/delete API calls with the CW Logs, DynamoDB, and S3
lifecycle equivalents documented below.

---

## MANDATORY RESEARCH ITEM 1: Exact CloudWatch Log Group Name Templates

Derived from source code — verified, HIGH confidence.

### Lambda log group families (per-sandbox Lambdas)

Both per-sandbox Lambda modules hardcode `km-` as the prefix in the CloudWatch log group
name, regardless of the `resource_prefix` variable passed to Terraform:

**budget-enforcer** (`infra/modules/budget-enforcer/v1.0.0/main.tf` line 260):
```
/aws/lambda/km-budget-enforcer-{sandbox_id}
```

**github-token-refresher** (`infra/modules/github-token/v1.0.0/main.tf` line 159):
```
/aws/lambda/km-github-token-refresher-{sandbox_id}
```

Note: the Lambda function itself is named `${var.resource_prefix}-budget-enforcer-{id}` and
`${var.resource_prefix}-github-token-refresher-{id}`, but the log group name is HARDCODED
with `km-` — a known inconsistency (TODO comment in `service_hcl.go` line 355 references this).
For the `kph` install the Lambda log group names are:
- `/aws/lambda/km-budget-enforcer-{id}` (not `/aws/lambda/kph-budget-enforcer-{id}`)
- `/aws/lambda/km-github-token-refresher-{id}` (not `/aws/lambda/kph-github-token-refresher-{id}`)

The `destroy.go` and `ttl-handler` attempt to delete using the dynamic prefix
(`prefix + "-budget-enforcer-" + sandboxID`), so their deletes silently miss the actual groups
on non-default-prefix installs. This is why 271 groups leaked on the `kph` install.

The sandbox ID is the trailing component after the last `-` ... but sandbox IDs have the form
`sb-{8hex}`, so the correct extraction is: strip the known prefix from the group name,
yielding the sandbox ID. E.g.:
- `/aws/lambda/km-budget-enforcer-sb-a1b2c3d4` → sandbox ID = `sb-a1b2c3d4`
- `/aws/lambda/km-github-token-refresher-sb-a1b2c3d4` → sandbox ID = `sb-a1b2c3d4`

**Detection strategy:** enumerate all log groups matching prefix `/aws/lambda/km-budget-enforcer-`
and `/aws/lambda/km-github-token-refresher-` using `DescribeLogGroups` with a
`logGroupNamePrefix` filter (does NOT require `resource_prefix` since the prefix is hardcoded `km-`).

### Per-sandbox sandbox audit log group

The audit-log sidecar creates log groups at runtime using `CW_LOG_GROUP` baked into the
systemd unit by userdata (`userdata.go` line 1197):
```
/km/sandboxes/{sandbox_id}/
```

Note: the userdata hardcodes `/km/` (not dynamic prefix) — same hardcoding bug as the
Lambda modules. The ECS path (service_hcl.go line 355) also hardcodes `/km/sandboxes/{id}/`.

For the `kph` install the actual groups are `/km/sandboxes/{id}/` (not `/kph/sandboxes/{id}/`).

The `pkg/aws/cloudwatch.go` `DeleteSandboxLogGroup` and `ExportSandboxLogs` use the dynamic
prefix (`"/" + prefix + "/sandboxes/" + sandboxID + "/"`) — another inconsistency, meaning
destroy-time deletion misses these groups on non-km-prefix installs too.

`doctor_presence.go` comment (line 13) acknowledges: log group convention is
`/{resource_prefix}/sandboxes/{sandbox_id}/` but the actual sidecar hardcodes `/km/sandboxes/`.

**Detection strategy:** enumerate all log groups matching prefix `/km/sandboxes/` using
`DescribeLogGroups` with `logGroupNamePrefix`. Extract sandbox ID as the component between
the second and third `/`, e.g. `/km/sandboxes/sb-a1b2c3d4/` → `sb-a1b2c3d4`.

### ECS sidecar log group (separate from audit log)

The ECS path also emits `service_hcl.go` line 361:
```
/km/sidecars/{sandbox_id}
```
This is a fourth log group family for ECS substrates (not present for EC2 substrates).
Include in the check: extract sandbox ID from `/km/sidecars/{id}` similarly.

### Summary of log group name templates (for detection code)

| Family | Template | Prefix filter for DescribeLogGroups | Extract sandbox-id from |
|--------|----------|--------------------------------------|-------------------------|
| budget-enforcer Lambda | `/aws/lambda/km-budget-enforcer-{id}` | `/aws/lambda/km-budget-enforcer-` | strip prefix |
| github-token-refresher Lambda | `/aws/lambda/km-github-token-refresher-{id}` | `/aws/lambda/km-github-token-refresher-` | strip prefix |
| audit-log sidecar | `/km/sandboxes/{id}/` | `/km/sandboxes/` | 3rd path component |
| ECS sidecars | `/km/sidecars/{id}` | `/km/sidecars/` | 3rd path component |

Note: the profile-declared `observability.commandLog.logGroup: /klanker-maker/sandboxes` and
`networkLog.logGroup: /klanker-maker/network` are passed to the eBPF enforcer (ECS task
definition only, EC2 uses the sidecar). These are SHARED log groups (not per-sandbox-suffixed)
and are NOT orphaned by sandbox teardown — exclude them from the check.

### Management Lambda log groups (retain, do NOT delete)

These are single log groups (not per-sandbox). The `--set-log-retention` guardrail should
set retention on them if missing, but never delete them:
- `/aws/lambda/{prefix}-create-handler`
- `/aws/lambda/{prefix}-ttl-handler`
- `/aws/lambda/{prefix}-email-handler` (or `/aws/lambda/{prefix}-email-handler-{region}`)
- `/aws/lambda/{prefix}-slack-bridge`

---

## MANDATORY RESEARCH ITEM 2: Exact DynamoDB Key Schemas

Derived from `pkg/aws/` source — HIGH confidence.

### `{prefix}-budgets` table

**Source:** `pkg/aws/budget.go` (top comment + code)

```
PK = "SANDBOX#{sandboxID}"   (S, partition key)
SK = "BUDGET#compute"        (S, sort key) — compute spend row
SK = "BUDGET#limits"         (S, sort key) — budget limits configuration
SK = "BUDGET#ai#{modelID}"   (S, sort key) — per-model AI spend row (NEVER DELETE)
```

No separate hash key — composite PK+SK. Sandbox ID is encoded in PK as `SANDBOX#{id}`.

**Extraction for scan:** Scan → for each item, read `PK` attribute, strip `SANDBOX#` prefix
to get sandbox-id. Skip items where `SK` starts with `BUDGET#ai#` (preserve AI-model rows).
Also skip `SK = "BUDGET#limits"` if you want to be conservative — those are configuration
rows and don't grow unboundedly (1 per sandbox max). Safe to delete all rows for orphaned
sandbox IDs except `BUDGET#ai#{modelID}` rows.

Wait — looking at the design spec again: the spec says "only per-sandbox rows; AI-model rows
(`BUDGET#ai#{modelID}`) are explicitly preserved." This means per-sandbox rows that ARE budget
rows (compute + limits) may be deleted, but AI-model rows stay. The discrimination is:
- Delete if `SK = "BUDGET#compute"` or `SK = "BUDGET#limits"` and sandbox-id is orphaned
- PRESERVE if `SK` starts with `BUDGET#ai#` regardless

### `{prefix}-identities` table

**Source:** `pkg/aws/identity.go` (comment line 14 + `PublishIdentity` code)

```
sandbox_id (S) — sole hash key (no sort key)
```

One row per sandbox. The sandbox ID is the hash key itself (`"sandbox_id"` attribute).

**Extraction for scan:** Scan → for each item, read `sandbox_id` attribute (S type) directly.

### `{prefix}-slack-threads` table

**Source:** `pkg/slack/bridge/aws_adapters.go` `DDBThreadStore` (lines 864-946)

```
channel_id (S) — partition key
thread_ts  (S) — sort key
sandbox_id (S) — NON-KEY attribute (written at Upsert time)
```

The sandbox ID is NOT part of the key — it is a regular attribute `sandbox_id` written by
`DDBThreadStore.Upsert`. Rows also have a `ttl_expiry` (N) attribute set to 30 days from
creation — many rows may already be expired (AWS TTL cleanup runs async).

**Extraction for scan:** Scan → for each item, read `sandbox_id` attribute (S type). Items
where `sandbox_id` is absent (e.g. legacy rows before Phase 91.3) should be skipped.

**Deletion key:** requires both `channel_id` and `thread_ts` for DeleteItem.

### `{prefix}-sandboxes` table

**Source:** `pkg/aws/sandbox_dynamo.go` (`sandboxItemDynamo` struct + `unmarshalSandboxItem`)

```
sandbox_id (S) — sole hash key (no sort key)
```

Key attributes scanned for this check:
- `sandbox_id` (S) — the hash key and sandbox identifier
- `status` (S) — "running", "stopped", "paused", "failed", "nocap", "starting", etc.
- `failure_reason` (S) — present only on failed rows
- `failed_at` (S, RFC3339) — present only on failed rows

**No `instance_id` in the DDB schema.** The design spec's "missing `instance_id`" guard
is NOT a literal DDB attribute check. The `instance_id` is stored in S3 Terraform state
(the `spot_instance_id` Terraform output), not in DynamoDB. The practical guard is:

> Purge only rows where `status` is `"failed"` or `"nocap"`.

These are sandboxes that failed during `km create` — the create-handler Lambda called
`UpdateSandboxStatusAndReasonDynamo` with `"failed"` or `"nocap"`. A row with any other
status (`"running"`, `"stopped"`, `"paused"`, `"starting"`, `"destroyed"`, etc.) represents
a sandbox that made it further in the lifecycle and should NOT be deleted as "debris."

In-flight creates have `status = "starting"` — these are excluded by the guard.

**Safe deletion criterion:** `sandbox_id` not in the active-set returned by `SandboxLister`
AND (`status == "failed"` OR `status == "nocap"`). This is narrower than the broader
"orphaned" definition used for the other tables, as the design intends.

---

## Existing Pattern Ground Truth

### `checkStaleSSMParameters` canonical shape

**Source:** `internal/app/cmd/doctor.go` line 1608.

Pattern copied verbatim for the three new checks:

```go
func checkStaleXxx(ctx context.Context, client XxxAPI, lister SandboxLister,
    dryRun bool, deleteXxx bool, prefix string) CheckResult {
    name := "Stale Xxx"
    if client == nil {
        return CheckResult{Name: name, Status: CheckSkipped, Message: "client not available"}
    }
    // 1. List resources, group by sandbox-id
    // 2. Diff against SandboxLister active set
    // 3. If no orphans: CheckOK
    // 4. If dryRun || !deleteXxx: CheckWarn + hint
    // 5. Else: delete, report deleted/failed counts
    // 6. Return CheckWarn if any failed, CheckOK if all succeeded
}
```

Key details from `checkStaleSSMParameters`:
- `ListSandboxes(ctx, false)` call for the active-set (false = include all statuses)
- `activeSandboxes[r.SandboxID] = true` map build
- Hint string pattern: `"use --dry-run=false --delete-ssm to delete"` / `"use --delete-ssm to delete"`
- Returns `CheckWarn` even after successful deletion (preserves audit trail in output)

### `checkOrphanedArtifacts` canonical shape

**Source:** `internal/app/cmd/doctor_artifacts.go`

The lifecycle check in `doctor_artifacts.go` will be a NEW function added to that file
(alongside `checkOrphanedArtifacts`). Pattern for guardrail checks (no deletion, idempotent
set operation):

```go
func checkS3LifecyclePolicy(ctx context.Context, client S3LifecycleAPI,
    bucket string, expireDays int32, dryRun bool, setLifecycle bool) CheckResult {
    // 1. GetBucketLifecycleConfiguration
    // 2. Check if any existing rule covers the transient prefixes
    // 3. If missing: WARN with hint "use --set-s3-lifecycle"
    // 4. If dryRun || !setLifecycle: return WARN
    // 5. Else: PutBucketLifecycleConfiguration (preserve existing rules)
    // 6. Return OK
}
```

### `SandboxLister` interface

```go
type SandboxLister interface {
    ListSandboxes(ctx context.Context, wide bool) ([]kmaws.SandboxRecord, error)
}
```

`ListSandboxes(ctx, false)` is the canonical call used by all existing stale checks.
Returns `[]SandboxRecord` where each record has `.SandboxID` and `.Status`.

### `--ignore-prefix` / `doctor_ignore_prefixes` integration

Honored at the resource level in existing checks: log group names and DDB table names are
already `resource_prefix`-scoped, so scoping the scan/filter by prefix handles multi-install
isolation. The CW log groups have the `km-` hardcoded prefix bug (see Research Item 1), but
the sandbox IDs extracted from group names are globally unique, so diffing against the active
set from the local install naturally excludes sibling installs' sandboxes.

For the guardrail checks (S3 lifecycle, log retention), these operate on the local install's
artifacts bucket and log groups, so no additional prefix filtering is needed.

### `DoctorDeps` struct additions required

**Source:** `internal/app/cmd/doctor.go` lines 263–432 (existing struct)

Three new fields to add to `DoctorDeps`:
```go
// Phase 94 — log group cleanup (checkStaleLogGroups)
CWLogsCleanupClient CWLogsCleanupAPI
// Phase 94 — DDB row cleanup (checkOrphanedDDBRows)
DDBScanDeleteClient DDBScanDeleteAPI
// Phase 94 — S3 lifecycle guardrail (checkS3LifecyclePolicy)
S3LifecycleClient S3LifecycleAPI
// Phase 94 — deletion flags
DeleteLogs      bool
DeleteDDBRows   bool
SetLogRetention bool
SetS3Lifecycle  bool
```

### `initRealDepsWithExisting` additions required

**Source:** `internal/app/cmd/doctor.go` lines 3131–3458

The real clients are straightforward — `cloudwatchlogs.NewFromConfig(awsCfg)`,
`dynamodb.NewFromConfig(awsCfg)`, and `s3.NewFromConfig(awsCfg)` are ALL already
imported and instantiated elsewhere in `initRealDepsWithExisting`.

### `NewDoctorCmdWithDeps` flag block pattern

**Source:** `internal/app/cmd/doctor.go` lines 2398–2424

New flags to add, following the same pattern as `--delete-ssm`:

```go
cmd.Flags().BoolVar(&deleteLogs, "delete-logs", false,
    "With --dry-run=false, delete orphaned CloudWatch log groups for destroyed sandboxes "+
    "(budget-enforcer, github-token-refresher, and sandbox audit-log groups).")
cmd.Flags().BoolVar(&deleteDDBRows, "delete-ddb-rows", false,
    "With --dry-run=false, delete DynamoDB rows in budgets/identities/slack-threads for "+
    "sandboxes whose record is gone from km-sandboxes, and status=failed rows in km-sandboxes.")
cmd.Flags().BoolVar(&setLogRetention, "set-log-retention", false,
    "With --dry-run=false, set a retention policy (default 30 days) on management and "+
    "sandbox log groups that currently have no retention. Idempotent no-op if already set.")
cmd.Flags().BoolVar(&setS3Lifecycle, "set-s3-lifecycle", false,
    "With --dry-run=false, install an S3 lifecycle rule expiring transient artifact "+
    "prefixes (logs/, remote-create/, agent-runs/, slack-inbound/) after N days. Idempotent.")
```

The `--with-deletes` shortcut (line 2384–2394) must also be extended:
```go
if withDeletes {
    deleteEBS = true
    // ... existing flags ...
    deleteLogs = true     // new
    deleteDDBRows = true  // new
}
```

### `runDoctor` signature extension

**Source:** `internal/app/cmd/doctor.go` line 2428

The `runDoctor` function signature will need the four new booleans added, and each wired
into `deps.DeleteLogs`, `deps.DeleteDDBRows`, `deps.SetLogRetention`, `deps.SetS3Lifecycle`.

---

## Standard Stack

### Core (all already vendored)

| Library | Version | Purpose | Import path |
|---------|---------|---------|-------------|
| aws-sdk-go-v2/service/cloudwatchlogs | v1.64.1 | DescribeLogGroups, DeleteLogGroup, PutRetentionPolicy | `github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs` |
| aws-sdk-go-v2/service/dynamodb | v1.57.0 | Scan (paginated), BatchWriteItem, DeleteItem | `github.com/aws/aws-sdk-go-v2/service/dynamodb` + `dynamodb/types` |
| aws-sdk-go-v2/service/s3 | v1.97.1 | GetBucketLifecycleConfiguration, PutBucketLifecycleConfiguration | `github.com/aws/aws-sdk-go-v2/service/s3` + `s3/types` |

All three are already in `go.mod` and used elsewhere. No new dependencies required.

### SDK API signatures (verified from go.mod version)

**CloudWatch Logs:**
- `DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{LogGroupNamePrefix: ..., NextToken: ...})` → paginated with `NextToken`
- `DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{LogGroupName: ...})` → already used in `destroy.go` and `ttl-handler`
- `PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{LogGroupName: ..., RetentionInDays: ...})` → already used in `pkg/aws/cloudwatch.go` `EnsureLogGroup`

**DynamoDB:**
- `Scan(ctx, &dynamodb.ScanInput{TableName: ..., ExclusiveStartKey: ..., ProjectionExpression: "PK, SK"})` → paginated with `LastEvaluatedKey`
- `DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: ..., Key: map[...]})` → already used in `CleanupSandboxIdentity`
- `BatchWriteItem` for bulk deletion (25 items max per call, the DynamoDB batch limit)

**S3 lifecycle:**
- `GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: ...})` → returns existing rules (or `NoSuchLifecycleConfiguration` error when none)
- `PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{Bucket: ..., LifecycleConfiguration: &s3types.BucketLifecycleConfiguration{Rules: [...]}})` → replaces ALL rules atomically, so must merge with existing

---

## Architecture Patterns

### New interface definitions

The three new API interfaces follow the narrow-interface pattern:

```go
// CWLogsCleanupAPI covers CloudWatch Logs operations for doctor_log_groups.go.
// The real *cloudwatchlogs.Client satisfies this interface.
type CWLogsCleanupAPI interface {
    DescribeLogGroups(ctx context.Context, input *cloudwatchlogs.DescribeLogGroupsInput,
        optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DescribeLogGroupsOutput, error)
    DeleteLogGroup(ctx context.Context, input *cloudwatchlogs.DeleteLogGroupInput,
        optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.DeleteLogGroupOutput, error)
    PutRetentionPolicy(ctx context.Context, input *cloudwatchlogs.PutRetentionPolicyInput,
        optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.PutRetentionPolicyOutput, error)
}

// DDBScanDeleteAPI covers DynamoDB operations for doctor_ddb_rows.go.
// The real *dynamodb.Client satisfies this interface.
type DDBScanDeleteAPI interface {
    Scan(ctx context.Context, input *dynamodb.ScanInput,
        optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
    DeleteItem(ctx context.Context, input *dynamodb.DeleteItemInput,
        optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
    BatchWriteItem(ctx context.Context, input *dynamodb.BatchWriteItemInput,
        optFns ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
}

// S3LifecycleAPI covers S3 lifecycle operations for checkS3LifecyclePolicy.
// The real *s3.Client satisfies this interface.
type S3LifecycleAPI interface {
    GetBucketLifecycleConfiguration(ctx context.Context, input *s3.GetBucketLifecycleConfigurationInput,
        optFns ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error)
    PutBucketLifecycleConfiguration(ctx context.Context, input *s3.PutBucketLifecycleConfigurationInput,
        optFns ...func(*s3.Options)) (*s3.PutBucketLifecycleConfigurationOutput, error)
}
```

### Config knob five-touchpoint pattern (doctor_stale_ami_days precedent)

**Source:** `internal/app/config/config.go`

Five touchpoints required for each new key:

1. **Struct field** (config.go ~line 172):
   ```go
   DoctorLogRetentionDays int  // km-config.yaml: doctor_log_retention_days, default 30
   DoctorS3ExpireDays     int  // km-config.yaml: doctor_s3_expire_days, default 30
   ```

2. **SetDefault** (config.go ~line 301):
   ```go
   v.SetDefault("doctor_log_retention_days", 30)
   v.SetDefault("doctor_s3_expire_days", 30)
   ```

3. **MERGE-LIST entry** (config.go ~line 375 — CRITICAL, see `project_config_key_merge_list` memory):
   ```go
   "doctor_log_retention_days",
   "doctor_s3_expire_days",
   ```

4. **Config struct initialization** (config.go ~line 442):
   ```go
   DoctorLogRetentionDays: v.GetInt("doctor_log_retention_days"),
   DoctorS3ExpireDays:     v.GetInt("doctor_s3_expire_days"),
   ```

5. **Clamp** (config.go ~line 495, after the DoctorStaleAMIDays clamp):
   ```go
   if cfg.DoctorLogRetentionDays <= 0 {
       cfg.DoctorLogRetentionDays = 30
   }
   if cfg.DoctorS3ExpireDays <= 0 {
       cfg.DoctorS3ExpireDays = 30
   }
   ```

Plus accessors in `DoctorConfigProvider` interface and `appConfigAdapter`:
```go
GetDoctorLogRetentionDays() int
GetDoctorS3ExpireDays() int
```

### S3 lifecycle rule upsert pattern

The `PutBucketLifecycleConfiguration` call REPLACES all rules. The guardrail must:
1. `GetBucketLifecycleConfiguration` — collect existing rules
2. Check if any existing rule already covers the transient prefixes (prefix-level filter match)
3. If missing: build a new rule and MERGE with existing before calling Put

The transient prefixes are hardcoded (CONTEXT.md: YAGNI, no config knob):
- `logs/`
- `remote-create/`
- `agent-runs/`
- `slack-inbound/`

Build-artifact prefixes (`toolchain/`, `sidecars/`, `rsync/`) MUST NOT be expired.

A single S3 lifecycle rule with multiple prefix filters is not valid — each prefix needs its
own rule. Merge all four into one rule using a tag-based approach or four separate rules.
The AWS SDK `s3types.LifecycleRule` supports one `Filter` per rule; use a rule per prefix
OR use an `And` filter with multiple prefixes inside one rule (supported via
`s3types.LifecycleRuleFilterMemberAnd`). Simplest approach: one rule per transient prefix,
giving four new rules (idempotent: check rule IDs before adding).

### DynamoDB batch deletion limit

AWS DynamoDB `BatchWriteItem` accepts at most 25 request items per call. For `{prefix}-budgets`
(251 items across ~83 sandboxes, ~3 rows/sandbox) and `{prefix}-identities` (85 items),
pagination of delete requests into batches of 25 is required. The existing
`checkStaleSSMParameters` uses a simple `DeleteParameter` per item loop. For large sets,
`BatchWriteItem` is more efficient but adds retry complexity. The planner may choose
single-item `DeleteItem` (simpler, matching the SSM pattern) or batched `BatchWriteItem`
(faster for large counts). Both are valid; for this scale (~500 rows total) `DeleteItem`
per-row is acceptable.

### CW Logs DescribeLogGroups API notes

- Paginated via `NextToken` (field name in response: `NextToken`)
- `LogGroupNamePrefix` filter narrows the scan to relevant groups
- Each `LogGroup` item contains: `LogGroupName`, `RetentionInDays` (nil if no retention set),
  `StoredBytes`, `CreationTime`
- The `RetentionInDays` field being nil is the trigger for `--set-log-retention`

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| CW Logs pagination | Manual token loop from scratch | Copy the `for {}` + `NextToken` loop from `checkStaleSSMParameters` | Same pattern, just different field names |
| DynamoDB scan + pagination | Custom scan | Copy `ListAllSandboxesByDynamo` paginated scan pattern | Pitfall: LastEvaluatedKey must be passed as ExclusiveStartKey |
| S3 lifecycle rule merge | Rebuild all rules from scratch | Fetch existing rules, merge, PUT back | AWS replaces ALL rules on PUT — drop existing rules = operator incident |
| Budget row discriminator | Regex on SK | Simple `strings.HasPrefix(sk, "BUDGET#ai#")` | Already documented in `pkg/aws/budget.go` |

---

## Common Pitfalls

### Pitfall 1: Hardcoded `km-` prefix in log group names vs dynamic resource_prefix
**What goes wrong:** Searching for `/aws/lambda/{resource_prefix}-budget-enforcer-{id}` finds
zero groups on a `kph` install because the actual groups are named `/aws/lambda/km-budget-enforcer-{id}`.
**Why it happens:** Both Lambda Terraform modules hardcode `km-` in the log group name
(`main.tf` line 260, line 159) — not `${var.resource_prefix}`.
**How to avoid:** Always use literal `km-budget-enforcer-` and `km-github-token-refresher-`
as the prefix for `DescribeLogGroups`, NOT the dynamic resource_prefix. Same for
`/km/sandboxes/` and `/km/sidecars/`.
**Warning signs:** "0 stale log groups" on an install with many destroyed sandboxes.

### Pitfall 2: S3 lifecycle PutBucketLifecycleConfiguration overwrites all rules
**What goes wrong:** Calling `PutBucketLifecycleConfiguration` with only the new rules
deletes any existing operator-defined rules (e.g. archive rules for old snapshots).
**How to avoid:** Always `GetBucketLifecycleConfiguration` first, merge new rules into
the existing list (de-dup by rule ID), then Put.
**Warning signs:** Operator reports S3 archive rules disappearing after `km doctor --set-s3-lifecycle`.

### Pitfall 3: DynamoDB Scan without pagination drops items beyond first page
**What goes wrong:** A DynamoDB table with >1MB of data (easily hit with 251+ items) returns
a partial result when `LastEvaluatedKey` is not handled.
**How to avoid:** Loop until `out.LastEvaluatedKey` is empty, passing it as `ExclusiveStartKey`
on the next call. Copy the pattern from `ListAllSandboxesByDynamo` in `sandbox_dynamo.go`.
**Warning signs:** "0 orphaned rows" reported despite known stale data.

### Pitfall 4: Deleting `BUDGET#ai#{modelID}` rows
**What goes wrong:** Deleting all rows for an orphaned sandbox-id in `{prefix}-budgets`
including the AI-model metering rows (e.g. `BUDGET#ai#claude-opus-4-5-...`), destroying
per-model spend history that the operator may need for cost audit.
**How to avoid:** Filter scan results: only delete rows where `SK` does NOT have prefix
`BUDGET#ai#`. Document this filter prominently in the code.
**Warning signs:** `km otel` / AI spend summary returns zero history for old sandboxes.

### Pitfall 5: In-flight create race for `{prefix}-sandboxes` rows
**What goes wrong:** Deleting a row with `status="starting"` that belongs to a sandbox
that's currently being provisioned by the create-handler Lambda.
**How to avoid:** Only delete rows with `status="failed"` or `status="nocap"`. Never
delete `status="starting"` rows. The active-sandbox-set diff alone is insufficient here
because `ListSandboxes` may not yet return a brand-new sandbox still in "starting" status.
**Warning signs:** `km create` returns "sandbox not found" immediately after a remote create.

### Pitfall 6: `--with-deletes` not extended
**What goes wrong:** `--with-deletes` doesn't imply `--delete-logs` and `--delete-ddb-rows`,
breaking the design intent and existing operator workflow.
**How to avoid:** Extend the `withDeletes` block in `NewDoctorCmdWithDeps` to set
`deleteLogs = true` and `deleteDDBRows = true` alongside existing flags.

### Pitfall 7: config merge-list omission
**What goes wrong:** Adding `DoctorLogRetentionDays` and `DoctorS3ExpireDays` to the
Config struct but omitting them from the v2→v merge-list in `config.Load()` (~line 375).
The yaml value is silently ignored and the default is always used.
**How to avoid:** Add both keys to the merge-list slice (per `project_config_key_merge_list`
memory). This is the most commonly forgotten touchpoint.

---

## Code Examples

### DescribeLogGroups paginated scan

```go
// Source: pkg/aws/cloudwatch.go patterns + cloudwatchlogs SDK
var nextToken *string
for {
    out, err := cwClient.DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
        LogGroupNamePrefix: awssdk.String("/aws/lambda/km-budget-enforcer-"),
        NextToken:          nextToken,
    })
    if err != nil {
        return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("DescribeLogGroups: %v", err)}
    }
    for _, lg := range out.LogGroups {
        if lg.LogGroupName == nil {
            continue
        }
        id := strings.TrimPrefix(*lg.LogGroupName, "/aws/lambda/km-budget-enforcer-")
        groupsBySandbox[id] = append(groupsBySandbox[id], *lg.LogGroupName)
    }
    if out.NextToken == nil {
        break
    }
    nextToken = out.NextToken
}
```

### DynamoDB scan for budgets table (with AI-row filter)

```go
// Source: pkg/aws/sandbox_dynamo.go ListAllSandboxesByDynamo pattern
var lastKey map[string]dynamodbtypes.AttributeValue
for {
    input := &dynamodb.ScanInput{
        TableName:            awssdk.String(budgetsTable),
        ProjectionExpression: awssdk.String("PK, SK"),
    }
    if len(lastKey) > 0 {
        input.ExclusiveStartKey = lastKey
    }
    out, err := ddbClient.Scan(ctx, input)
    if err != nil {
        return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("scan %s: %v", budgetsTable, err)}
    }
    for _, item := range out.Items {
        pk := attrStr(item, "PK")  // e.g. "SANDBOX#sb-a1b2c3d4"
        sk := attrStr(item, "SK")  // e.g. "BUDGET#compute"
        if !strings.HasPrefix(pk, "SANDBOX#") {
            continue
        }
        // Preserve AI-model rows unconditionally
        if strings.HasPrefix(sk, "BUDGET#ai#") {
            continue
        }
        sandboxID := strings.TrimPrefix(pk, "SANDBOX#")
        budgetRowsBySandbox[sandboxID] = append(budgetRowsBySandbox[sandboxID], budgetKey{pk: pk, sk: sk})
    }
    if len(out.LastEvaluatedKey) == 0 {
        break
    }
    lastKey = out.LastEvaluatedKey
}
```

### Slack-threads scan (non-key sandbox_id extraction)

```go
// Source: pkg/slack/bridge/aws_adapters.go DDBThreadStore key design
for _, item := range out.Items {
    channelID := attrStr(item, "channel_id")
    threadTS  := attrStr(item, "thread_ts")
    sandboxID := attrStr(item, "sandbox_id")
    if sandboxID == "" || channelID == "" || threadTS == "" {
        continue // skip legacy rows or incomplete items
    }
    threadRowsBySandbox[sandboxID] = append(threadRowsBySandbox[sandboxID],
        slackThreadKey{channelID: channelID, threadTS: threadTS})
}
```

### Sandbox row deletion guard (status=failed only)

```go
// Source: pkg/aws/sandbox_dynamo.go sandboxItemDynamo fields
for _, item := range out.Items {
    sandboxID := attrStr(item, "sandbox_id")
    status    := attrStr(item, "status")
    if sandboxID == "" {
        continue
    }
    // Only consider rows for sandboxes NOT in the active set
    if activeSandboxes[sandboxID] {
        continue
    }
    // Guard: only delete status=failed or status=nocap rows
    if status != "failed" && status != "nocap" {
        continue
    }
    sandboxFailedRows = append(sandboxFailedRows, sandboxID)
}
```

### S3 lifecycle guardrail (merge-and-put pattern)

```go
// Source: aws-sdk-go-v2/service/s3 API
var existingRules []s3types.LifecycleRule
out, err := s3Client.GetBucketLifecycleConfiguration(ctx,
    &s3.GetBucketLifecycleConfigurationInput{Bucket: awssdk.String(bucket)})
if err != nil {
    var nslc *s3types.NoSuchLifecycleConfiguration
    if !errors.As(err, &nslc) {
        return CheckResult{/* WARN */}
    }
    // No lifecycle config — existingRules stays nil
} else {
    existingRules = out.Rules
}
// Check if transient prefixes already covered
transientPrefixes := []string{"logs/", "remote-create/", "agent-runs/", "slack-inbound/"}
// ... check logic ...
// Merge: add new rules alongside existing, keyed by rule ID
newRules := append(existingRules, buildTransientExpiryRules(transientPrefixes, expireDays)...)
_, err = s3Client.PutBucketLifecycleConfiguration(ctx,
    &s3.PutBucketLifecycleConfigurationInput{
        Bucket: awssdk.String(bucket),
        LifecycleConfiguration: &s3types.BucketLifecycleConfiguration{Rules: newRules},
    })
```

### PutRetentionPolicy guardrail (idempotent)

```go
// Idempotent: check RetentionInDays before calling PutRetentionPolicy
// Source: cloudwatchlogs.LogGroup.RetentionInDays is *int32 (nil = no retention)
for _, lg := range out.LogGroups {
    if lg.RetentionInDays != nil {
        continue // already set — idempotent no-op
    }
    _, err := cwClient.PutRetentionPolicy(ctx, &cloudwatchlogs.PutRetentionPolicyInput{
        LogGroupName:    lg.LogGroupName,
        RetentionInDays: awssdk.Int32(int32(retentionDays)),
    })
    // non-fatal: log warning, continue
}
```

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` package (table-driven, same as all existing `doctor_*_test.go`) |
| Config file | None (Go test infrastructure, `go test ./internal/app/cmd/...`) |
| Quick run command | `go test ./internal/app/cmd/ -run TestDoctor_StaleLogGroups -v -count=1` |
| Full suite command | `go test ./internal/app/cmd/... -count=1` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command | Test file |
|----------|-----------|-------------------|-----------|
| Orphaned log group → WARN | unit | `go test -run TestDoctor_StaleLogGroups_OrphanDetected` | `doctor_log_groups_test.go` |
| All log groups active → OK | unit | `go test -run TestDoctor_StaleLogGroups_AllActive` | `doctor_log_groups_test.go` |
| Dry-run → no mutation | unit | `go test -run TestDoctor_StaleLogGroups_DryRun` | `doctor_log_groups_test.go` |
| `--delete-logs` deletion → counts correct | unit | `go test -run TestDoctor_StaleLogGroups_DeleteFlag` | `doctor_log_groups_test.go` |
| Pagination across 2+ pages | unit | `go test -run TestDoctor_StaleLogGroups_Pagination` | `doctor_log_groups_test.go` |
| Orphaned DDB row → WARN | unit | `go test -run TestDoctor_OrphanedDDBRows_Detected` | `doctor_ddb_rows_test.go` |
| AI-model budget row preserved (REGRESSION) | unit | `go test -run TestDoctor_OrphanedDDBRows_AIModelPreserved` | `doctor_ddb_rows_test.go` |
| `status=failed` guard — other statuses skipped | unit | `go test -run TestDoctor_OrphanedDDBRows_SandboxStatusGuard` | `doctor_ddb_rows_test.go` |
| `status=failed` deleted | unit | `go test -run TestDoctor_OrphanedDDBRows_FailedStatusDeleted` | `doctor_ddb_rows_test.go` |
| Slack-threads: missing sandbox_id → skip | unit | `go test -run TestDoctor_OrphanedDDBRows_SlackThreadNoSandboxID` | `doctor_ddb_rows_test.go` |
| S3 lifecycle missing → WARN | unit | `go test -run TestDoctor_S3LifecyclePolicy_Missing` | `doctor_artifacts_test.go` |
| S3 lifecycle present → OK idempotent | unit | `go test -run TestDoctor_S3LifecyclePolicy_AlreadySet` | `doctor_artifacts_test.go` |
| Log retention: already-set → no-op | unit | `go test -run TestDoctor_StaleLogGroups_RetentionAlreadySet` | `doctor_log_groups_test.go` |
| `--with-deletes` implies `--delete-logs --delete-ddb-rows` | unit | `go test -run TestDoctor_WithDeletesImpliesNewFlags` | `doctor_test.go` |

### Sampling Rate

- **Per task commit:** `go test ./internal/app/cmd/... -run TestDoctor -count=1`
- **Per wave merge:** `go test ./internal/app/cmd/... -count=1`
- **Phase gate:** Full suite green (`go test ./... -count=1`) before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `internal/app/cmd/doctor_log_groups_test.go` — covers all log group check cases (new file)
- [ ] `internal/app/cmd/doctor_ddb_rows_test.go` — covers all DDB row check cases (new file)
- [ ] `internal/app/cmd/doctor_log_groups.go` — new check implementation (new file)
- [ ] `internal/app/cmd/doctor_ddb_rows.go` — new check implementation (new file)
- [ ] S3 lifecycle test cases added to existing `internal/app/cmd/doctor_artifacts_test.go`

---

## State of the Art

| Old Approach | Current Approach | Impact |
|--------------|------------------|--------|
| No orphan log group detection | `checkStaleLogGroups` with explicit `--delete-logs` | Prevents unbounded CW log growth |
| DDB rows accumulate forever | `checkOrphanedDDBRows` scanning 4 tables | Reclaims ~394+ leaked rows on `kph` install |
| No S3 lifecycle rule on artifacts bucket | `checkS3LifecyclePolicy` + `--set-s3-lifecycle` | Prevents unbounded S3 growth on `logs/` etc. |
| `--with-deletes` covers EBS/SQS/S3/Lambda/SSH/SSM | Extended to include `--delete-logs --delete-ddb-rows` | Full cleanup pass with one flag |

---

## Open Questions

1. **ECS sidecar log group family (`/km/sidecars/{id}`)**: The design spec mentions three
   families. The ECS sidecar group (`/km/sidecars/{id}`) was found in `service_hcl.go` line 361.
   The spec's "~90 groups" for the sandbox family may or may not include this family.
   **Recommendation:** Include `/km/sidecars/` prefix scan alongside `/km/sandboxes/`.

2. **`instance_id` guard for sandbox rows**: No `instance_id` attribute exists in the
   `sandboxItemDynamo` schema. The practical guard is `status == "failed"` or `status == "nocap"`.
   **Recommendation:** Use status-based guard (`status in {"failed", "nocap"}`), document
   that this is the implementation of the spec's "missing instance_id" intent.

3. **Budget `BUDGET#limits` rows**: The spec says "only per-sandbox rows; AI-model rows preserved."
   `BUDGET#limits` is also a per-sandbox configuration row (one per sandbox). It may make
   sense to delete it alongside `BUDGET#compute` for orphaned sandboxes.
   **Recommendation:** Delete `BUDGET#compute` and `BUDGET#limits` for orphaned sandboxes;
   preserve ONLY `BUDGET#ai#` rows.

4. **Management Lambda log groups retention**: The `--set-log-retention` guardrail should
   optionally set retention on management log groups too (create-handler, ttl-handler,
   email-handler, slack-bridge). These are NOT per-sandbox and should not be deleted.
   **Recommendation:** The `--set-log-retention` pass should cover both management and
   sandbox log groups. Detection of orphaned groups is separate from setting retention.

---

## Sources

### Primary (HIGH confidence)
- Source code: `internal/app/cmd/doctor.go` — `checkStaleSSMParameters`, `DoctorDeps`,
  `NewDoctorCmdWithDeps`, `runDoctor`, `buildChecks`, `initRealDepsWithExisting`
- Source code: `internal/app/cmd/doctor_artifacts.go` — `checkOrphanedArtifacts` pattern
- Source code: `internal/app/cmd/doctor_presence.go` — `CWLogsFilterAPI`, presence log group convention
- Source code: `pkg/aws/budget.go` — budgets table PK/SK schema
- Source code: `pkg/aws/identity.go` — identities table hash-key schema
- Source code: `pkg/aws/sandbox_dynamo.go` — sandboxes table schema, `sandboxItemDynamo`
- Source code: `pkg/slack/bridge/aws_adapters.go` — slack-threads key schema (`DDBThreadStore`)
- Source code: `pkg/aws/cloudwatch.go` — `DeleteSandboxLogGroup`, `PutRetentionPolicy` in `EnsureLogGroup`
- Source code: `pkg/compiler/userdata.go` line 1197 — `CW_LOG_GROUP=/km/sandboxes/{{ .SandboxID }}/`
- Source code: `pkg/compiler/service_hcl.go` lines 355, 361 — `/km/sandboxes/{{ .SandboxID }}/`, `/km/sidecars/{{ .SandboxID }}`
- Source code: `infra/modules/budget-enforcer/v1.0.0/main.tf` line 260 — hardcoded `km-budget-enforcer-`
- Source code: `infra/modules/github-token/v1.0.0/main.tf` line 159 — hardcoded `km-github-token-refresher-`
- Source code: `internal/app/config/config.go` — five-touchpoint pattern, merge-list
- `go.mod` — all AWS SDK v2 packages already vendored at correct versions

### Secondary (MEDIUM confidence)
- `cmd/ttl-handler/main.go` lines 1309, 1357 — confirms log group name construction in cleanup
- `internal/app/cmd/destroy.go` lines 855, 961 — confirms log group name construction in destroy path

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all packages already imported, no new dependencies
- Architecture: HIGH — derived directly from existing source, verified patterns
- Log group name templates: HIGH — derived from actual Terraform module source + sidecar code
- DynamoDB key schemas: HIGH — derived from actual Go struct definitions in `pkg/aws`
- Pitfalls: HIGH — derived from code inconsistencies actually observed in source

**Research date:** 2026-06-04
**Valid until:** 2026-07-04 (stable domain; AWS SDK v2 interface stable)
