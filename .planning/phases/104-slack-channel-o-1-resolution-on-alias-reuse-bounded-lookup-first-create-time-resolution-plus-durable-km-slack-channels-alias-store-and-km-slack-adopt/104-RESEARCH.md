# Phase 104: Slack Channel O(1) Resolution on Alias Reuse — Research

**Researched:** 2026-06-10
**Domain:** Slack channel resolution, DynamoDB store, create-handler IAM, km-operator-policy plumbing, config merge-list, doctor checks
**Confidence:** HIGH — spec + plan authored by pairing session; codebase validated against actual files

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**P0 — Bound resolution (safety net)**
- Wrap per-sandbox Slack channel resolution (create Step 6c / Mode-2 of `resolveSlackChannel`) in a wall-clock sub-context. Budget default **45s** via `KM_SLACK_RESOLVE_BUDGET` (seconds).
- `FindChannelByName` gains a **max-pages cap** and honors `ctx` cancellation per page; returns a typed `ErrScanCapExceeded` (distinct from `*SlackAPIError{Code:"ratelimited"}`) so callers emit adopt/channelOverride guidance, not a retry-shortly hint.
- Page cap default **OFF**: `KM_SLACK_MAX_SCAN_PAGES=0` => `FindChannelByName` returns `ErrScanCapExceeded` with NO HTTP call => fail-fast. Opt-in `>0` runs a bounded scan.

**P1 — Lookup-first O(1) hot path**
- Look up the stored channel ID **before** `conversations.create`.
- Classify `conversations.info` errors via a new `IsChannelNotFound(err)` helper: ONLY a definitive `channel_not_found` invalidates the stored mapping (=> recreate). Every other error (ratelimited, 5xx, network, context) is transient.
- Transient info error policy: **bounded-retry 2x (500ms backoff) then optimistically USE the stored ID** — never enumerate.

**P2 — Durable authoritative store**
- New dedicated **`km-slack-channels` DynamoDB table**, hash_key `alias`, **no TTL** (mapping must persist across destroy/recreate; stale rows self-heal via the `channel_not_found` recreate path), PAY_PER_REQUEST, SSE on.
- Read **first** (authoritative) during resolution; the existing SSM by-name cache stays as a back-compat read/write fallback. Write-through to BOTH on create/resolve.
- **Rejected: storing on `km-sandboxes`** — destroy DELETES that row and `ListAllSandboxesByDynamo` SCANS the table, so a synthetic alias item would pollute `km list`. A dedicated table avoids both.
- DDB lookup keyed on `alias`; skip when alias is empty.

**C — Operator escape hatch**
- `km slack adopt <alias> <channelID>`: validate `^C[A-Z0-9]+$` + confirm bot membership (`conversations.info` is_member) + write-through to BOTH the DDB store and the SSM by-name cache.

**Observability**
- Single INFO log per resolution: `slack_resolve path=cache_hit|cache_optimistic|created|scan_capped|failfast ms=... id=...`.

**Deploy surface**
- TF module `infra/modules/dynamodb-slack-channels/v1.0.0` + live unit `infra/live/use1/dynamodb-slack-channels/terragrunt.hcl` + `init.go` regionalModules entry.
- create-handler IAM: km-operator-policy var (`slack_channels_table_name`, default "") + IAM policy (GetItem/PutItem/DescribeTable, count-gated) + create-handler var + wiring + live-unit dependency + input.
- Config: `GetSlackChannelsTableName()` getter + v2->v merge-list entry.
- Go: `pkg/aws.SlackChannelStore` (GetByAlias/UpsertByAlias) + wire into `resolveSlackChannel` and `km create`.
- `km doctor`: table-existence check only (NOT orphan-row scan).
- Build order: `make build` BEFORE `km init`. Deploy = `make build-lambdas` + `km init --dry-run=false`. No `--sidecars`. No SandboxProfile schema change.

### Claude's Discretion
- Exact internal helper factoring (e.g. `lookupStoredChannelID` / `storeChannelMapping` / `validateStoredChannel`), test-fake shapes, and the `slackResolveSleep` ctx-aware sleep var.
- Precise doctor check wording/placement.
- Whether the bounded-scan opt-in surfaces as env-only or also a km-config.yaml key (lean env-only to avoid schema churn).

### Deferred Ideas (OUT OF SCOPE)
- `archiveOnDestroy: true` default-flip hygiene — separate ticket (archiving doesn't avoid `name_taken`: Slack reserves the name ~30 days).
- Bridge-side channel-name resolution — N/A (bridge is not a consumer).
- A km-config.yaml knob for the scan cap / budget — lean env-only for now (no schema churn); revisit if operators need per-install tuning.
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| SLACK-CHAN-BOUND | `FindChannelByName` bounded: page cap + ctx cancellation + `ErrScanCapExceeded`; `KM_SLACK_MAX_SCAN_PAGES=0` = fail-fast default | Task 1 in plan: adds `maxPages int` param, typed error var, zero-cap fast-fail |
| SLACK-CHAN-LOOKUP | Lookup-first state machine: DDB->SSM read before `conversations.create`; sub-context deadline `KM_SLACK_RESOLVE_BUDGET=45s`; `IsChannelNotFound` classifier; bounded-retry-then-optimistic for transient errors | Tasks 2+3 in plan: classifier + resolver restructure |
| SLACK-CHAN-INFO-CLASS | `IsChannelNotFound(err) bool` — only `channel_not_found` invalidates stored mapping; all other errors are transient | Task 2 in plan |
| SLACK-CHAN-STORE | `km-slack-channels` DDB table (PK=alias, no TTL, PAY_PER_REQUEST, SSE) + `SlackChannelStore` Go helper + config getter + merge-list + IAM + live unit + init.go entry | Tasks 4–10 in plan |
| SLACK-CHAN-ADOPT | `km slack adopt <alias> <channelID>`: format validate + membership confirm + DDB+SSM write-through | Task 11 in plan |
| SLACK-CHAN-DEPLOY | Full deploy surface: `make build` + `make build-lambdas` + `km init --dry-run=false`; doctor table-existence check; docs | Tasks 12+13 in plan |
| SLACK-CHAN-E2E | Live UAT on large workspace: `km create` reused alias resolves in ~2 min; `slack_resolve path=` log confirms O(1) branch; cold orphan via `km slack adopt` | Final verification checklist in plan |
</phase_requirements>

---

## Summary

The Phase 104 spec and implementation plan were authored in a pairing session and represent a fully-resolved design. The codebase validation below confirms all line-number references are accurate, the reference implementations are correctly identified, and no drift exists between the plan and the current state of the code.

**The critical finding from codebase validation:** `FindChannelByName` in `pkg/slack/client.go` at line 605 is currently a 2-argument function (`ctx context.Context, name string`) with an unbounded `for {}` loop and no page cap — exactly as described in the spec. The plan adds a `maxPages int` third argument which will break one existing call site in `resolveExistingChannelID`. No other production callers exist (confirmed by grep).

**The `slack_threads_table_name` env-var pattern:** `create.go:504` does `os.Setenv("KM_SLACK_THREADS_TABLE", cfg.GetSlackThreadsTableName())` so the compiler can read it. The `km-slack-channels` store is read by Go code in `resolveSlackChannel` directly (not by the compiler/userdata), so a `KM_SLACK_CHANNELS_TABLE` env export is NOT required. The config getter's `{prefix}-slack-channels` derivation is sufficient since `cfg` is fully loaded before `resolveSlackChannel` runs.

**Primary recommendation:** Implement all 13 tasks in the plan in strict TDD order. The deploy surface is complete and validated — no gaps exist beyond what the plan covers.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | v2 (already a dep) | DDB GetItem/PutItem for `SlackChannelStore` | Used by `DDBThreadStore`, `DynamoNonceStore`, all existing DDB adapters |
| `pkg/slack` (internal) | current | `FindChannelByName`, `ChannelInfo`, `IsChannelNotFound`, `ErrScanCapExceeded` | The existing client already wires all Slack API calls for create |
| `pkg/aws` (internal) | current | `SlackChannelStore` struct (new file `slack_channels.go`) | Mirrors `DDBThreadStore` in `pkg/slack/bridge/aws_adapters.go` |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/aws/aws-sdk-go-v2/service/ssm` | v2 (already a dep) | SSM by-name cache write (existing `cacheSlackChannelIDByName`) | Back-compat write-through; keep existing pattern |
| `errors` (stdlib) | — | `errors.As`, `errors.Is`, `errors.New` for `ErrScanCapExceeded` | Already used in `client.go` for `SlackAPIError` detection |
| `context` (stdlib) | — | `context.WithTimeout` for `SlackResolveBudget` sub-context | Already used throughout resolve path |

---

## Architecture Patterns

### Recommended Project Structure for New Files

```
pkg/slack/
├── client.go              # MODIFY: FindChannelByName (3-arg), IsChannelNotFound, ErrScanCapExceeded
├── client_test.go         # MODIFY: update 2-arg call sites; add 3 new test cases

pkg/aws/
├── slack_channels.go      # NEW: SlackChannelStore struct + GetByAlias/UpsertByAlias
├── slack_channels_test.go # NEW: fake DDB tests

internal/app/cmd/
├── create_slack.go        # MODIFY: SlackChannelStore interface, vars block, state machine
├── create_slack_test.go   # MODIFY: extend fakeSlackAPI; add 5 resolver tests
├── create.go              # MODIFY: build SlackChannelStore, wire into resolveSlackChannel
├── slack_adopt.go         # NEW: runSlackAdopt + Cobra command
├── slack.go               # MODIFY: register adopt subcommand

internal/app/config/
├── config.go              # MODIFY: SlackChannelsTableName field, setter, getter, merge-list, SetDefault

infra/modules/
├── dynamodb-slack-channels/v1.0.0/main.tf       # NEW: table module
├── dynamodb-slack-channels/v1.0.0/variables.tf  # NEW
├── dynamodb-slack-channels/v1.0.0/outputs.tf    # NEW
├── km-operator-policy/v1.0.0/variables.tf       # MODIFY: add slack_channels_table_name var
├── km-operator-policy/v1.0.0/main.tf            # MODIFY: add dynamodb_slack_channels IAM resource
├── create-handler/v1.0.0/variables.tf           # MODIFY: add slack_channels_table_name var
├── create-handler/v1.0.0/main.tf                # MODIFY: pass to km_operator_policy

infra/live/use1/
├── dynamodb-slack-channels/terragrunt.hcl        # NEW: live unit
├── create-handler/terragrunt.hcl                 # MODIFY: add dependency + input

internal/app/cmd/init.go                          # MODIFY: regionalModules entry
internal/app/cmd/doctor.go                        # MODIFY: add checkDynamoTable for slack-channels
docs/slack-notifications.md                       # MODIFY: O(1) resolution section
CLAUDE.md                                         # MODIFY: phase note + where-to-look row
```

### Pattern 1: Reference DDB Store (DDBThreadStore)

The canonical pattern to mirror for `SlackChannelStore` is `DDBThreadStore` in `pkg/slack/bridge/aws_adapters.go` (lines 861-946). Key structural differences for `SlackChannelStore`:

- PK is `alias` (string), not a composite `(channel_id, thread_ts)` key
- Two attributes: `alias` (PK) + `channel_id` + `updated_at` (audit)
- **No TTL** — must not expire (unlike threads which have `ttl_expiry`)
- **No ConditionExpression** on PutItem — this is a write-through upsert, always overwrites
- Interface is `SlackChannelGetPutAPI` (GetItem + PutItem only; no Query needed)

```go
// Source: pkg/slack/bridge/aws_adapters.go:861 (DDBThreadStore as reference)
// New file: pkg/aws/slack_channels.go
type SlackChannelGetPutAPI interface {
    GetItem(ctx context.Context, in *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
    PutItem(ctx context.Context, in *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
}

type SlackChannelStore struct {
    Client    SlackChannelGetPutAPI
    TableName string
}
```

### Pattern 2: km-operator-policy Conditional IAM Resource (slack_threads precedent)

The `dynamodb_slack_threads` IAM resource in `infra/modules/km-operator-policy/v1.0.0/main.tf` (lines 99-129) is the exact mirror for the new `dynamodb_slack_channels` resource. Key pattern:

```hcl
# Source: infra/modules/km-operator-policy/v1.0.0/main.tf:109
resource "aws_iam_role_policy" "dynamodb_slack_threads" {
  count = var.slack_threads_table_name != "" ? 1 : 0
  name  = "${var.resource_prefix}-create-handler-dynamodb-slack-threads"
  role  = var.role_id
  # ... GetItem, PutItem, UpdateItem on table ARN
}
```

The `dynamodb_slack_channels` resource follows the same `count = var.slack_channels_table_name != "" ? 1 : 0` pattern, granting GetItem/PutItem/DescribeTable (no UpdateItem needed — upsert is a full PutItem).

### Pattern 3: create-handler module km_operator_policy pass-through

`infra/modules/create-handler/v1.0.0/main.tf` has a single `module "km_operator_policy"` block (line 56) where all operator policy variables are passed. The `slack_threads_table_name` wire (line 67) is the exact pattern to replicate:

```hcl
# Source: infra/modules/create-handler/v1.0.0/main.tf:67
module "km_operator_policy" {
  source = "../../km-operator-policy/v1.0.0"
  # ... other vars ...
  slack_threads_table_name  = var.slack_threads_table_name
  # ADD: slack_channels_table_name = var.slack_channels_table_name
}
```

Note: the create-handler Lambda environment block does **NOT** pass `KM_SLACK_THREADS_TABLE` — that is set by `create.go:504` via `os.Setenv("KM_SLACK_THREADS_TABLE", cfg.GetSlackThreadsTableName())`. The `km-slack-channels` table name follows the same pattern: no Lambda env var needed. `cfg.GetSlackChannelsTableName()` derives from `ResourcePrefix` which is already loaded at create time.

### Pattern 4: Config merge-list (mandatory or silently ignored)

`internal/app/config/config.go` (line 667) shows the merge-list loop. `slack_threads_table_name` is at line 667. The new `slack_channels_table_name` must be added in the same block:

```go
// Source: internal/app/config/config.go:667 (merge-list loop)
"slack_threads_table_name",          // existing
"slack_stream_messages_table_name",  // existing
// ADD: "slack_channels_table_name",
```

Without this entry, the yaml value `slack_channels_table_name:` in `km-config.yaml` is silently ignored (the known `project_config_key_merge_list` footgun — memory note).

### Pattern 5: live terragrunt unit (slack-threads)

`infra/live/use1/dynamodb-slack-threads/terragrunt.hcl` is the exact template to mirror. Key values to change for `dynamodb-slack-channels`:

- `key` path: `dynamodb-slack-channels/terraform.tfstate`
- `source`: `infra/modules/dynamodb-slack-channels/v1.0.0`
- `table_name`: `"${local.site_vars.locals.site.label}-slack-channels"`
- `tags["km:component"]`: `"km-slack-channels"`

The `locals` block uses: `repo_root`, `site_vars`, `region_config`, `region_label`, `region_full` — all present in the threads unit and must be replicated identically.

### Pattern 6: init.go regionalModules placement

`internal/app/cmd/init.go` lines 270–299 show the DynamoDB Slack modules clustered together. Insert `dynamodb-slack-channels` immediately after `dynamodb-slack-threads` (line 282):

```go
// Source: internal/app/cmd/init.go:279
{
    name:    "dynamodb-slack-threads",
    dir:     filepath.Join(regionDir, "dynamodb-slack-threads"),
    envReqs: nil,
},
// ADD AFTER:
{
    name:    "dynamodb-slack-channels",
    dir:     filepath.Join(regionDir, "dynamodb-slack-channels"),
    envReqs: nil,
},
```

### Pattern 7: FindChannelByName current signature (CONFIRMED DRIFT)

The **current** signature in `pkg/slack/client.go:605` is:

```go
func (c *Client) FindChannelByName(ctx context.Context, name string) (string, error)
```

The plan adds a `maxPages int` third argument. The **only** production caller is `resolveExistingChannelID` in `create_slack.go:166` — this call site must be updated as part of Task 3 (the plan notes this). Test call sites in `client_test.go` that call the 2-arg form must also be updated to pass a generous cap (e.g. 100) per plan step 4.

### Pattern 8: `SlackAPI` interface in create_slack.go (must be updated)

`internal/app/cmd/create_slack.go:87` defines `SlackAPI` interface with:

```go
FindChannelByName(ctx context.Context, name string) (string, error)
```

This must be updated to the 3-arg signature to match the new `client.go` implementation. The `*slack.Client` concrete type satisfies the interface at compile time; updating both together keeps them in sync.

### Pattern 9: doctor checkDynamoTable reuse

`doctor.go:716-742` has a ready-made `checkDynamoTable(ctx, client, tableName, checkName)` helper. The slack-channels check follows the same pattern as the identity table check (lines 3443-3454): call `checkDynamoTable`, optionally demote `CheckError` to `CheckWarn`. The planner should decide on WARN vs ERROR severity (lean WARN — the table is optional for installs without Slack, though the IAM grant means it should exist).

### Anti-Patterns to Avoid

- **Adding `KM_SLACK_CHANNELS_TABLE` to the Lambda env block**: Not needed. `cfg.GetSlackChannelsTableName()` derivation is sufficient. The `KM_SLACK_THREADS_TABLE` env set (create.go:504) is for the compiler/userdata path — `SlackChannelStore` reads at Go runtime from `cfg`, not from env.
- **Adding `slack-channels` to `checkOrphanedDDBRows`**: Explicitly out of scope. Alias rows are not per-sandbox and must not be auto-deleted. The table is NOT passed to `checkOrphanedDDBRows`.
- **Required providers block in new TF module**: root.hcl's generate stanza owns the provider declaration (memory `project_terragrunt_providers_in_root`). Do not add `required_providers` to `dynamodb-slack-channels/v1.0.0/*.tf`.
- **TTL on the km-slack-channels table**: The mapping must persist across destroy/recreate cycles — no TTL. Contrast with `km-slack-threads` which has a 30-day `ttl_expiry` attribute.
- **ConditionExpression on PutItem in SlackChannelStore.UpsertByAlias**: This is a write-through upsert — always overwrite. Unlike `DDBThreadStore.Upsert` which uses `attribute_not_exists(channel_id)` to avoid overwriting poller-written session IDs.
- **`--sidecars` for deploy**: The `km-slack-channels` addition involves a new module, IAM changes, and env-block additions — all require `km init --dry-run=false` (full terragrunt apply). `--sidecars` rebuilds binaries and cold-starts but does NOT update the env block or apply new modules.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| DDB GetItem/PutItem helper | Custom marshaling/unmarshal | Mirror `DDBThreadStore` raw attribute map pattern | Consistency; already battle-tested in bridge; no struct tags needed for 3-field table |
| `conversations.info` error classification | if/else string matching inline | `IsChannelNotFound(err)` helper function | Reusable across adopt + resolve; testable in isolation |
| Ctx-aware sleep for retry | `time.Sleep(d)` (not ctx-aware) | `select { case <-ctx.Done(): ...; case <-t.C: }` | Budget timeout must propagate through retry loop |
| Page cap error type | reuse `*SlackAPIError` | `var ErrScanCapExceeded = errors.New(...)` | Distinct from rate-limit errors so callers can give adopt/channelOverride guidance |

---

## Common Pitfalls

### Pitfall 1: Stale km binary skips the new module
**What goes wrong:** `km init --plan` silently shows no `dynamodb-slack-channels` entry; the table is never deployed.
**Why it happens:** `init.go` regionalModules list is compiled into the `km` binary. If the operator runs `km init` with the old binary, the new entry is invisible (memory `project_make_build_precedes_km_init`).
**How to avoid:** `make build` (with ldflags, not bare `go build`) before `km init`. Task 6 step 2 gates on this.
**Warning signs:** `km init --plan` output does not mention `dynamodb-slack-channels`.

### Pitfall 2: merge-list omission silently drops config value
**What goes wrong:** `slack_channels_table_name:` in `km-config.yaml` is ignored; `GetSlackChannelsTableName()` always returns the `{prefix}-slack-channels` derivation, never a custom value.
**Why it happens:** `config.go` merge loop (line 650-703) only merges explicitly listed keys from v2 (km-config.yaml viper) into v (env viper). Unlisted keys are silently dropped (memory `project_config_key_merge_list`).
**How to avoid:** Add `"slack_channels_table_name"` to the merge-list at line ~668 alongside `"slack_threads_table_name"`.
**Warning signs:** `TestGetSlackChannelsTableName` passes but a real install with a custom `slack_channels_table_name:` in yaml uses the wrong table.

### Pitfall 3: FindChannelByName caller-site mismatch
**What goes wrong:** Compilation error: `too many arguments in call to api.FindChannelByName`.
**Why it happens:** `SlackAPI` interface at create_slack.go:87 still has the 2-arg signature after `client.go` is updated to 3-arg.
**How to avoid:** Update the interface declaration in `create_slack.go` AND update `fakeSlackAPI` in tests in the same task (Tasks 1+3 overlap here).
**Warning signs:** `go test ./pkg/slack/` passes but `go test ./internal/app/cmd/` fails to compile.

### Pitfall 4: No dependency block in create-handler live unit
**What goes wrong:** `terraform plan` on `create-handler` during `km init --plan` sees `dependency.slack_channels.outputs.table_name` as `""` (mock value) and the IAM grant is skipped.
**Why it happens:** Missing `mock_outputs_allowed_terraform_commands = ["validate", "plan", "show", "init", "destroy"]` in the dependency block (memory `project_terragrunt_show_needs_mocks`). The destroy-class gate calls `terragrunt show` which needs mocks.
**How to avoid:** Include `"show"` in the allowed commands list per the plan (Task 7, step 4).
**Warning signs:** `km init --plan` emits a terragrunt parse error on `create-handler` unit.

### Pitfall 5: Alias-rows appear in km doctor orphan scan
**What goes wrong:** `km doctor` deletes valid alias→channel mappings when their corresponding sandbox is destroyed, breaking the next recreate.
**Why it happens:** `checkOrphanedDDBRows` is passed `slackThreadsTable` but must NOT receive the `slack-channels` table (alias rows are not per-sandbox).
**How to avoid:** The `checkOrphanedDDBRows` signature takes explicit table name params. Do not add `slackChannelsTable` to its call site (doctor.go:3794). Add a separate `checkDynamoTable` call for existence-only check.
**Warning signs:** `km doctor --delete-ddb-rows` deletes alias rows and the next `km create --alias github-bot` hangs again.

### Pitfall 6: `KM_SLACK_RESOLVE_BUDGET` and `KM_SLACK_MAX_SCAN_PAGES` not recognized
**What goes wrong:** env vars set by operator have no effect; budget stays at 45s default; scan remains off.
**Why it happens:** The `init()` block reading these env vars in `create_slack.go` was not added.
**How to avoid:** Add the `init()` block as the first step in Task 3 (before any behavior change).
**Warning signs:** Setting `KM_SLACK_MAX_SCAN_PAGES=5` doesn't enable the bounded scan path in tests.

---

## Codebase Validation Results (CONFIRMED against actual files)

### 1. Current function signatures (confirmed vs plan line numbers)

| Function | File | Current Signature | Plan Modification |
|----------|------|-------------------|-------------------|
| `FindChannelByName` | `pkg/slack/client.go:605` | `(ctx, name string) (string, error)` | Add `maxPages int` 3rd arg |
| `ChannelInfo` | `pkg/slack/client.go:749` | `(ctx, channelID string) (int, bool, error)` | No change; `IsChannelNotFound` added as new func |
| `resolveExistingChannelID` | `internal/app/cmd/create_slack.go:152` | Current 5-arg form | Replaced by lookup-first state machine in Task 3 |
| `resolveSlackChannel` | `internal/app/cmd/create_slack.go:236` | `(ctx, p, sandboxID, alias, api, ssmStore, ssmPrefix)` | Add `store SlackChannelStore` arg |
| `slack.NewClient(token, nil)` | `internal/app/cmd/create.go:597` | Correct; no nil http.Client arg drift | Wire `channelStore` constructed alongside |

**Key drift confirmed:** `SlackAPI` interface (create_slack.go:87) declares `FindChannelByName(ctx context.Context, name string) (string, error)` — 2-arg. Both this interface AND `client.go` must be updated to 3-arg in the same task.

### 2. Reference implementation verified (DDBThreadStore)

`DDBThreadStore` in `pkg/slack/bridge/aws_adapters.go:861-946` is the confirmed reference. The `DynamoGetPutter` interface (line 61) is the basis — `SlackChannelGetPutAPI` will be a pkg/aws-local copy of the same pattern (GetItem + PutItem only).

### 3. Config merge-list mechanism confirmed

The merge-list loop is at `internal/app/config/config.go` lines 650-703. The `slack_threads_table_name` entry is at line 667. The new `slack_channels_table_name` must be added in the same block. The `SetDefault` call is at line 582, the struct field population at line 755 — both need corresponding entries.

### 4. create-handler Lambda does NOT use env vars for table names

Confirmed: `infra/modules/create-handler/v1.0.0/main.tf` environment block (lines 95-113) does NOT include `KM_SLACK_THREADS_TABLE` — that is set programmatically by `create.go:504`. The `km-slack-channels` table name follows the same pattern: no new env var in the TF module. The IAM grant (via km-operator-policy) is what matters at runtime.

### 5. create-handler live unit has no dependency blocks

Confirmed: `infra/live/use1/create-handler/terragrunt.hcl` has no `dependency {}` blocks — inputs are all derived from `site_vars`, env vars, or static values. The `slack_threads_table_name` input (line 63) is derived as `"${local.site_vars.locals.site.label}-slack-threads"` (static string, no dependency block needed). The `slack_channels_table_name` follows the same pattern: `"${local.site_vars.locals.site.label}-slack-channels"` — no dependency block in the create-handler live unit itself. (The `dynamodb-slack-channels` live unit stands alone with its own remote_state; create-handler just needs the name string, not an output reference.)

**Correction to plan Task 7, Step 4:** The plan's sample code shows a `dependency "slack_channels"` block in `create-handler/terragrunt.hcl`. This is NOT needed — the input can be a static string derivation matching the pattern at line 63. A dependency block would introduce unnecessary coupling and break `km init --plan` on fresh installs where the DDB table unit hasn't run yet. Use the static string approach instead.

### 6. doctor orphan-row scan — confirmed exclusion requirement

`doctor_ddb_rows.go:34-58` and call site at `doctor.go:3793-3795`: `checkOrphanedDDBRows` takes explicit table name parameters `(budgetsTable, identitiesTable, slackThreadsTable, sandboxesTable)`. The `km-slack-channels` table is NOT passed here — confirmed safe.

The `checkDynamoTable` helper at `doctor.go:716` is the correct place for the existence check. Pattern to follow: the identity table check (lines 3443-3454) which demotes `CheckError` to `CheckWarn`.

---

## Code Examples

### FindChannelByName new signature with ErrScanCapExceeded

```go
// Source: pkg/slack/client.go (plan Task 1 implementation)
var ErrScanCapExceeded = errors.New("slack: conversations.list scan exceeded page cap")

func (c *Client) FindChannelByName(ctx context.Context, name string, maxPages int) (string, error) {
    if maxPages <= 0 {
        return "", ErrScanCapExceeded
    }
    cursor := ""
    for page := 0; page < maxPages; page++ {
        if err := ctx.Err(); err != nil {
            return "", err
        }
        // ... existing body with cursor, callJSON, range resp.Channels ...
        if resp.ResponseMetadata.NextCursor == "" {
            return "", nil
        }
        cursor = resp.ResponseMetadata.NextCursor
    }
    return "", ErrScanCapExceeded
}
```

### IsChannelNotFound classifier

```go
// Source: pkg/slack/client.go (plan Task 2)
func IsChannelNotFound(err error) bool {
    var apierr *SlackAPIError
    return errors.As(err, &apierr) && apierr.Code == "channel_not_found"
}
```

### SlackChannelStore DDB helper

```go
// Source: pkg/aws/slack_channels.go (new file, plan Task 9)
// Mirrors DDBThreadStore in pkg/slack/bridge/aws_adapters.go:861
type SlackChannelStore struct {
    Client    SlackChannelGetPutAPI
    TableName string
}

func (s *SlackChannelStore) GetByAlias(ctx context.Context, alias string) (string, error) {
    out, err := s.Client.GetItem(ctx, &dynamodb.GetItemInput{
        TableName: awssdk.String(s.TableName),
        Key: map[string]ddbtypes.AttributeValue{
            "alias": &ddbtypes.AttributeValueMemberS{Value: alias},
        },
    })
    // ... return out.Item["channel_id"] or "" on miss
}

func (s *SlackChannelStore) UpsertByAlias(ctx context.Context, alias, channelID string) error {
    _, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{
        TableName: awssdk.String(s.TableName),
        Item: map[string]ddbtypes.AttributeValue{
            "alias":      &ddbtypes.AttributeValueMemberS{Value: alias},
            "channel_id": &ddbtypes.AttributeValueMemberS{Value: channelID},
            "updated_at": &ddbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
        },
        // NO ConditionExpression — always overwrite (unlike DDBThreadStore.Upsert)
    })
    return err
}
```

### Config getter (mirrors GetSlackThreadsTableName)

```go
// Source: internal/app/config/config.go (plan Task 8; mirrors lines 1034-1046)
func (c *Config) GetSlackChannelsTableName() string {
    if c == nil {
        return "km-slack-channels"
    }
    if c.SlackChannelsTableName != "" {
        return c.SlackChannelsTableName
    }
    return c.GetResourcePrefix() + "-slack-channels"
}
```

### km-operator-policy IAM resource (mirrors dynamodb_slack_threads)

```hcl
# Source: infra/modules/km-operator-policy/v1.0.0/main.tf (mirrors line 109)
resource "aws_iam_role_policy" "dynamodb_slack_channels" {
  count = var.slack_channels_table_name != "" ? 1 : 0
  name  = "${var.resource_prefix}-create-handler-dynamodb-slack-channels"
  role  = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid    = "SlackChannelsTableAccess"
      Effect = "Allow"
      Action = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:DescribeTable"]
      Resource = [
        "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.slack_channels_table_name}",
      ]
    }]
  })
}
```

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` (stdlib) + `httptest` |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./pkg/slack/ ./internal/app/cmd/ ./pkg/aws/ ./internal/app/config/ -run 'TestFindChannel\|TestIsChannel\|TestResolvePerSandbox\|TestSlackAdopt\|TestSlackChannelStore\|TestGetSlackChannels' -v` |
| Full suite command | `go test ./... 2>&1 \| tail -30` |

### Phase Requirements -> Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| SLACK-CHAN-BOUND | `FindChannelByName` stops at page cap and returns `ErrScanCapExceeded`; zero cap = fail-fast with no HTTP; ctx cancel mid-scan | unit | `go test ./pkg/slack/ -run TestFindChannelByName -v` | Wave 0: add 3 new test cases in `client_test.go` |
| SLACK-CHAN-BOUND | `KM_SLACK_RESOLVE_BUDGET` / `KM_SLACK_MAX_SCAN_PAGES` env parsing in `init()` | unit | `go test ./internal/app/cmd/ -run TestSlackResolveBudget -v` | Wave 0 |
| SLACK-CHAN-INFO-CLASS | `IsChannelNotFound` returns true only for `channel_not_found`; false for ratelimited/nil/network | unit | `go test ./pkg/slack/ -run TestIsChannelNotFound -v` | Wave 0 |
| SLACK-CHAN-LOOKUP | Stored-live: no create, no scan (O(1) happy path) | unit | `go test ./internal/app/cmd/ -run TestResolvePerSandbox_StoredID_Live_NoScan -v` | Wave 0 |
| SLACK-CHAN-LOOKUP | Stored + transient info error: no scan (today's bug) | unit | `go test ./internal/app/cmd/ -run TestResolvePerSandbox_StoredID_TransientInfo_NoScan -v` | Wave 0 |
| SLACK-CHAN-LOOKUP | Stored + `channel_not_found`: invalidate + recreate | unit | `go test ./internal/app/cmd/ -run TestResolvePerSandbox_StoredID_NotFound_Recreates -v` | Wave 0 |
| SLACK-CHAN-LOOKUP | name_taken + no mapping + scan off: fail-fast with `km slack adopt` guidance | unit | `go test ./internal/app/cmd/ -run TestResolvePerSandbox_NameTaken_NoMapping_FailFast -v` | Wave 0 |
| SLACK-CHAN-LOOKUP | Fresh alias, name free: create + write-through | unit | `go test ./internal/app/cmd/ -run TestResolvePerSandbox_FreshCreate_WritesStore -v` | Wave 0 |
| SLACK-CHAN-STORE | `GetByAlias` miss returns `""/nil` | unit | `go test ./pkg/aws/ -run TestSlackChannelStore_GetMiss -v` | Wave 0: new file |
| SLACK-CHAN-STORE | `UpsertByAlias` + `GetByAlias` round-trip | unit | `go test ./pkg/aws/ -run TestSlackChannelStore_UpsertThenGet -v` | Wave 0: new file |
| SLACK-CHAN-STORE | Config getter default derivation: nil-safe, explicit override, prefix-derived | unit | `go test ./internal/app/config/ -run TestGetSlackChannelsTableName -v` | Wave 0 |
| SLACK-CHAN-ADOPT | Rejects non-`^C[A-Z0-9]+$` channel ID | unit | `go test ./internal/app/cmd/ -run TestSlackAdopt_RejectsBadChannelID -v` | Wave 0: new file |
| SLACK-CHAN-ADOPT | Requires bot membership | unit | `go test ./internal/app/cmd/ -run TestSlackAdopt_RequiresBotMembership -v` | Wave 0: new file |
| SLACK-CHAN-ADOPT | Writes through to DDB + SSM | unit | `go test ./internal/app/cmd/ -run TestSlackAdopt_WritesThrough -v` | Wave 0: new file |
| SLACK-CHAN-DEPLOY | `km slack adopt --help` renders | smoke | `make build && ./km slack adopt --help` | after Task 11 |
| SLACK-CHAN-DEPLOY | `km init --plan` shows `dynamodb-slack-channels` ADD | smoke | `make build && AWS_PROFILE=klanker-application ./km init --plan 2>&1 \| grep -i slack-channels` | after Task 6 |
| SLACK-CHAN-DEPLOY | `scripts/validate-all-profiles.sh` passes (no schema change) | smoke | `scripts/validate-all-profiles.sh` | existing: confirm no regression |
| SLACK-CHAN-E2E | `km create --alias github-bot` completes in ~2 min; log shows `slack_resolve path=cache_hit\|created` | live UAT | operator: `km create profiles/github-review.yaml --alias github-bot --wait` | manual |
| SLACK-CHAN-E2E | Cold orphan resolved via `km slack adopt` | live UAT | operator: `km slack adopt github-bot C0B91RA9CPR` then `km create ...` | manual |

### Sampling Rate
- **Per task commit:** quick run command (relevant test package subset)
- **Per wave merge:** `go test ./... 2>&1 | tail -30`
- **Phase gate:** full suite green + `scripts/validate-all-profiles.sh` green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/slack/client_test.go` — 3 new `TestFindChannelByName_*` tests; update existing 2-arg call sites
- [ ] `pkg/slack/client_test.go` — `TestIsChannelNotFound` (4 cases)
- [ ] `internal/app/cmd/create_slack_test.go` — extend `fakeSlackAPI` with `findShouldPanic`/`channelInfoErr`/`createCalls`; add `fakeChannelStore`; add 5 `TestResolvePerSandbox_*` tests
- [ ] `pkg/aws/slack_channels.go` — new file (SlackChannelStore + interface)
- [ ] `pkg/aws/slack_channels_test.go` — new file (2 tests: UpsertThenGet + GetMiss)
- [ ] `internal/app/config/config_test.go` — `TestGetSlackChannelsTableName` (3 cases)
- [ ] `internal/app/cmd/slack_adopt.go` — new file (runSlackAdopt + cobra command)
- [ ] `internal/app/cmd/slack_adopt_test.go` — new file (3 tests)

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Unbounded `for {}` in `FindChannelByName` | Bounded with page cap + ctx-per-page + `ErrScanCapExceeded` | Phase 104 | 15-min wedge -> <1-min fail-fast |
| Any `conversations.info` error falls through to O(N) scan | Transient -> bounded-retry+optimistic; `channel_not_found` only invalidates | Phase 104 | Eliminates the incident's root cause |
| SSM by-name cache is the only durable mapping | DDB `km-slack-channels` table (alias->channel_id) is authoritative; SSM remains fallback | Phase 104 | Mapping survives destroy/recreate cycles |
| No `km slack adopt` | `km slack adopt <alias> <channelID>` for operator-seeded orphaned channels | Phase 104 | Eliminates the manual `channelOverride` YAML edit for orphaned channels |

---

## Open Questions

1. **`km slack adopt` SSM write-through requires channel NAME**
   - What we know: `cacheSlackChannelIDByName` takes `channelName` (e.g. `sb-github-bot-sec`), not `alias`. In `runSlackAdopt`, we know `alias` and `channelID` but not `channelName`.
   - What's unclear: Can `runSlackAdopt` derive `channelName = "sb-" + sanitizeChannelName(alias)` reliably? Yes for the default derivation; no for profiles with `notification.slack.channelName` template.
   - Recommendation: For the SSM write-through in `km slack adopt`, derive `channelName` as `"sb-" + sanitizeChannelName(alias)` (the common case). Document that profiles with custom `channelName` templates must use `channelOverride` instead of `adopt`. The DDB-by-alias write-through is always authoritative and doesn't need the channel name.

2. **`SlackChannelStore` interface in `create_slack.go` vs `pkg/aws`**
   - What we know: The plan defines `SlackChannelStore` as an interface in `create_slack.go` and the concrete `SlackChannelStore` struct in `pkg/aws/slack_channels.go`. The concrete type satisfies the interface.
   - What's unclear: Naming collision — both the interface (in cmd package) and the struct (in pkg/aws) are named `SlackChannelStore`. Go allows this across packages, but it may confuse readers.
   - Recommendation: Follow the existing pattern — `DDBThreadStore` in `pkg/slack/bridge` is both the interface name in the bridge package and the concrete struct name. Acceptable at project scale.

---

## Sources

### Primary (HIGH confidence)
- Codebase direct reads: `internal/app/cmd/create_slack.go`, `pkg/slack/client.go`, `internal/app/cmd/create.go`, `pkg/slack/bridge/aws_adapters.go`, `internal/app/config/config.go`, `internal/app/cmd/init.go`, `internal/app/cmd/doctor.go`, `internal/app/cmd/doctor_ddb_rows.go`
- `infra/modules/km-operator-policy/v1.0.0/main.tf` and `variables.tf`
- `infra/modules/create-handler/v1.0.0/main.tf` and `variables.tf`
- `infra/live/use1/dynamodb-slack-threads/terragrunt.hcl`
- `infra/live/use1/create-handler/terragrunt.hcl`
- `.planning/phases/104-*/104-CONTEXT.md`
- `docs/superpowers/specs/2026-06-10-slack-channel-reuse-o1-resolution-spec.md`
- `docs/superpowers/plans/2026-06-10-slack-channel-o1-resolution.md`

### Secondary (MEDIUM confidence)
- Memory notes: `project_config_key_merge_list`, `project_make_build_precedes_km_init`, `project_terragrunt_providers_in_root`, `project_terragrunt_show_needs_mocks`, `feedback_verify_deploy_surface_not_just_code`

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all dependencies already in use; no new external packages
- Architecture: HIGH — directly validated against live source files; reference implementations confirmed
- Pitfalls: HIGH — 5 of 6 pitfalls are directly derived from verified memory notes + code validation (not hypothesis)
- Deploy surface: HIGH — all 7 deploy surface items validated against actual TF/Go files

**Research date:** 2026-06-10
**Valid until:** 2026-07-10 (stable — no fast-moving dependencies)

**Critical correction to plan Task 7, Step 4:** The plan shows a `dependency "slack_channels"` block in `create-handler/terragrunt.hcl`. This is unnecessary and adds fragility. Use the static string derivation `"${local.site_vars.locals.site.label}-slack-channels"` (same pattern as `slack_threads_table_name` at line 63 of the existing unit). The IAM grant uses the table name string, not the table ARN, so no output reference is needed.
