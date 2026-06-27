# Phase 121: Action Quota + Freeze Quarantine — Research

**Researched:** 2026-06-27
**Domain:** Go proxy MITM extension, DynamoDB atomic counters + Streams, Lambda management, per-sandbox DDB row, CLI verbs
**Confidence:** HIGH (all findings grounded in actual source files)

## Summary

Phase 121 adds an **external, agent-untrusted quota layer** on high-impact outbound actions
enforced at chokepoints the sandbox agent cannot bypass. The two chokepoints are:

1. **MITM http-proxy sidecar** (`sidecars/http-proxy/`) — already MITMs GitHub and SES traffic.
   The proxy has a budget enforcement extension pattern (`WithBudgetEnforcement`) in
   `sidecars/http-proxy/httpproxy/proxy.go` that is the exact template for a new
   `WithActionQuota` option. The proxy receives its config via env vars (`KM_BUDGET_TABLE`,
   `KM_BUDGET_ENABLED`) via systemd drop-ins written by the compiler's userdata.

2. **Bridge Lambdas** (Slack, H1) — hold the bot tokens; sandboxes never do. The Slack bridge
   handler dispatches `ActionPost`/`ActionUpload` at `pkg/slack/bridge/handler.go:273–475`. The
   `FetchByChannel` adapter already reads per-sandbox attrs from `km-sandboxes` and returns a
   `SandboxRoutingInfo` struct — the natural home for `action_limits` + `action_frozen`.

The new `pkg/quota` package (one entry point: `Record(ctx, sandboxID, action, limits) → Decision`)
follows the exact same `aws.IncrementAISpend` pattern (`pkg/aws/budget.go:65`) — atomic `ADD` via
`UpdateItem`, returns updated count for synchronous WARN/BLOCK/FREEZE decisions.

**Primary recommendation:** Follow the proxy budget-enforcement extension pattern (`WithBudgetEnforcement`)
for the proxy chokepoint; follow the `FetchByChannel` + `SandboxRoutingInfo` pattern for bridge
chokepoints. The freeze attrs follow the `Locked`/`LockedAt` DynamoDB field pattern. All four deploy-surface
footguns from CONTEXT.md §8 are verified to be real — see §8 of this document for exact file:line.

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
1. Breach response: **alert now, block later** (design both; enforce by flag later).
2. v1 metered actions: **GitHub writes + email send + Slack/H1 posts** (NOT sandbox lifecycle).
3. Quota key: **per (sandbox, action)** (actor as metadata, not key).
4. Multi-window limits: **lifetime + perHour + perDay**, any subset, fixed buckets, "any window trips ⇒ action trips."
5. Limit config home: **per-profile (`spec.limits`) + install defaults (`km-config.yaml`)**, profile wins per-window.
6. Slack trip notice: **dual — in-thread (bridge, chat trips) + channel-level (alerter, proxy trips)**.
7. Quarantine strength: **latch actions only, box keeps running** (halt/destroy = separate human escalation).
8. Freeze triggers: **auto-on-breach + operator CLI `km freeze`**; release **`km unlock` only**; no Slack trigger in v1.

### Claude's Discretion
(None specified — all key decisions were locked by user.)

### Deferred Ideas (OUT OF SCOPE)
- Per-actor keying (per Slack-user / GitHub-login)
- Sandbox-lifecycle / cold-create storm limits
- Out-of-band OTEL/audit sweeper
- True sliding windows (v1 = fixed buckets)
- Allowlisted-Slack-operator freeze trigger
- `!replace` config directive for narrowing limits
</user_constraints>

---

## 1. Metering Substrate to Extend

### Current Pattern: `WithBudgetEnforcement` in the proxy

**File:** `sidecars/http-proxy/httpproxy/proxy.go`
**Pattern:** A `ProxyOption` functional option adds MITM intercept handlers:
```go
// proxy.go:65-75 — WithBudgetEnforcement adds handlers BEFORE general CONNECT
func WithBudgetEnforcement(client aws.BudgetAPI, tableName string, ...) ProxyOption {
    return func(_ *goproxy.ProxyHttpServer, cfg *proxyConfig) {
        cfg.budget = &budgetEnforcementOptions{ client, tableName, ... }
    }
}
```

The `proxyConfig` struct (proxy.go:52-57) accumulates options. All handlers that need budget access
are registered inside `if cfg.budget != nil { ... }` blocks in `NewProxy`. The same pattern applies
for `WithActionQuota`.

**DynamoDB write pattern:** `pkg/aws/budget.go:65` — `IncrementAISpend` uses:
```go
UpdateItem( ... ADD spentUSD :cost ... ReturnValues: ALL_NEW ... )
```
`pkg/quota.Record` will use the same atomic `ADD count 1` with `ReturnValues: ALL_NEW` to get
the post-increment count for synchronous WARN/BLOCK/FREEZE decisions.

**DDB client wiring:** `sidecars/http-proxy/main.go:47-73` — the proxy reads `KM_BUDGET_ENABLED`,
`KM_BUDGET_TABLE` from env, creates a `*dynamodb.Client`, and passes it to `WithBudgetEnforcement`.
The new `WithActionQuota` follows the exact same env-var + client setup.

**IAM:** `infra/modules/ec2spot/v1.1.0/main.tf:348-369` — the per-sandbox EC2 role already has
`dynamodb:GetItem`, `dynamodb:UpdateItem`, `dynamodb:Query` on the `{prefix}-budgets` table.
Phase 121 adds the same permissions for `{prefix}-action-quota`.

---

## 2. Proxy Request Classification

### GitHub writes — already MITM'd

**File:** `sidecars/http-proxy/httpproxy/github.go`
The proxy registers a `HandleConnectFunc` (MitmConnect) for `githubHostsRegex`:
```
^(github\.com|api\.github\.com|raw\.githubusercontent\.com|codeload\.githubusercontent\.com)(:\d+)?$
```
(proxy.go:575-610). After MITM, `OnRequest` handlers can inspect the full URL path.

**`ExtractRepoFromPath`** (github.go:39-78) already parses `api.github.com` paths:
- `POST /repos/{owner}/{repo}/pulls` → extract `owner/repo` → action `github_pr`
- `POST /repos/{owner}/{repo}/issues/{n}/comments` → action `github_comment`
- `POST /repos/{owner}/{repo}/pulls/{n}/reviews` → action `github_review`

**Classification hook point:** `proxy.go:586-609` — the existing `DoFunc` OnRequest handler for
`githubHostsRegex` currently calls `IsRepoAllowed`. The new quota classification runs **in the same
handler** (or a new `OnRequest(ReqHostMatches(githubHostsRegex))` registered after). Match on
`req.Method == "POST"` + URL path pattern to identify the action.

### SES email send — NOT currently MITM'd; needs explicit MITM

**Critical finding:** `km-send` invokes `aws sesv2 send-email` (compiler/userdata.go:3165). The AWS
CLI uses `https://email.us-east-1.amazonaws.com` (SESv2 endpoint). The proxy's general CONNECT
handler passes this through as `OkConnect` (no MITM, no inspection) because it's an `.amazonaws.com`
host in the allowedHosts list. There is **no existing `OnResponse` or `OnRequest` handler for SES
endpoints** — this host is currently allowed but opaque to the proxy.

**What's needed:** A new MITM host regex for `email.*.amazonaws.com` (SESv2 regional endpoints).
The proxy already has the pattern for adding new MITM hosts (`bedrockHostRegex`, `anthropicHostRegex`,
`openaiHostRegex`). Match `POST /v2/email/outbound-emails` (SESv2 SendEmail) in `OnRequest` to
classify `email_send`. Since quota counting only fires on `OnRequest` (before forwarding), a
`metering_reader`-style response capture is NOT needed — count the request, not the response.

**Registration order:** New SES MITM handler MUST be registered BEFORE the general CONNECT handler
(same goproxy first-match semantics as Bedrock/Anthropic/OpenAI handlers).

**Risk flag:** SES traffic currently transits the proxy as OkConnect (HTTPS CONNECT passthrough).
Making it MITM requires the custom CA cert to be trusted for `email.us-east-1.amazonaws.com`. The
custom CA is already installed system-wide at boot (`update-ca-certificates`), so this is safe for
`aws cli` which respects the system CA store. **The CA cert is added via the `KM_PROXY_CA_CERT`
drop-in** (compiler/userdata.go:4773-4778).

---

## 3. Bridge Action Sites

### Slack bridge dispatch

**File:** `pkg/slack/bridge/handler.go`
**Action dispatch switch:** handler.go:273 —
```go
switch env.Action {
case slack.ActionPost, slack.ActionTest:  // line 274 — post/test
case slack.ActionUpload:                   // line 319 — file upload
```
Both `ActionPost` and `ActionUpload` are high-impact outbound actions. The quota Record call
goes at the **top of each case**, before the `Slack.Post` / `FileUploader.Upload` call.

**Channel→sandbox resolution:** `handler.go` calls `Channels.FetchByChannel` (via `handler.go`
`Handler.Channels` field, type `ChannelOwnershipFetcher`). The concrete implementation is
`DDBSandboxByChannel.FetchByChannel` at `pkg/slack/bridge/aws_adapters.go:1040`.

**`SandboxRoutingInfo` struct** (`pkg/slack/bridge/events_interfaces.go:106-131`) — currently holds
`SandboxID`, `QueueURL`, `Paused`, `ReactAlways`, `MentionOnly`, `Allow`. Phase 121 extends this
with `ActionLimits` (resolved JSON string from `action_limits` attr) and `ActionFrozen` (bool from
`action_frozen` attr), both read from the DDB item in `FetchByChannel` (aws_adapters.go:1040+).

**Freeze gate for inbound dispatch:** The events handler (`events_handler.go`) dispatches inbound
turns; it also calls `FetchByChannel` (aws_adapters.go:1293). The freeze gate goes there too: if
`info.ActionFrozen` is true, skip dispatch and post the in-thread control-plane notice.

### H1 bridge dispatch

**File:** `pkg/h1/bridge/` — mirrors the Slack bridge exactly. The `WebhookHandler` dispatches
comment actions. Same pattern: call `pkg/quota.Record` in the comment-dispatch code path.

---

## 4. New DynamoDB Table `{prefix}-action-quota`

### Terraform module pattern

**Reference:** `infra/modules/dynamodb-slack-channels/v1.0.0/main.tf` — minimal PAY_PER_REQUEST
table without Streams. `infra/modules/dynamodb-budget/v1.0.0/main.tf:12-38` — same pattern WITH
Streams enabled. Phase 121 needs Streams.

**New module:** `infra/modules/dynamodb-action-quota/v1.0.0/main.tf`
```hcl
resource "aws_dynamodb_table" "action_quota" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "PK"  # "{sandbox}#{action}"
  range_key    = "SK"  # "lifetime" | "hour#{bucket}" | "day#{bucket}"

  stream_enabled   = true
  stream_view_type = "NEW_AND_OLD_IMAGES"

  ttl { attribute_name = "ttl"; enabled = true }
  server_side_encryption { enabled = true }
  ...
}
output "stream_arn" { value = aws_dynamodb_table.action_quota.stream_arn }
```

### Where the new module must be registered (COMPLETE CHECKLIST)

1. **`regionalModules()`** — `internal/app/cmd/init.go:511`
   Add a new `regionalModule` entry with `name: "dynamodb-action-quota"` before `"ses"`.
   Position: after `dynamodb-checks` and before `check-runner-role` or before `ses` is fine;
   no upstreamOutputs dependency.

2. **`init_plan_test.go:439`** — hardcoded count `24` → bump to `26` (this phase adds BOTH
   `dynamodb-action-quota` AND `lambda-quota-alerter`).
   **File:** `internal/app/cmd/init_plan_test.go` line 439:
   `if len(mods) != 24 {` → `if len(mods) != 26 {` (table + alerter)

3. **`infra/live/use1/dynamodb-action-quota/terragrunt.hcl`** — live unit.
   Pattern: copy `infra/live/use1/dynamodb-slack-channels/terragrunt.hcl`, change source,
   table_name input, state key.

4. **`make build` before `km init`** — the binary must carry the new `regionalModules()` entry
   or `km init` silently skips it (`project_make_build_precedes_km_init` footgun).

---

## 5. New Lambda `km-quota-alerter` — Complete Deploy Checklist

The alerter Lambda is triggered by the DDB Stream on the quota table. It is NOT a per-sandbox
Lambda (unlike `budget-enforcer`) — it is a **shared regional management Lambda** like `lambda-slack-bridge`.

### All Four Required Registration Points

**1. TF module:** `infra/modules/lambda-quota-alerter/v1.0.0/main.tf`
   Pattern: `infra/modules/lambda-slack-bridge/v1.0.0/main.tf`. Key differences:
   - Event source: `aws_lambda_event_source_mapping` with `event_source_arn = var.quota_stream_arn`
     (`starting_position = "LATEST"`, `batch_size = 100`, `bisect_batch_on_function_error = true`).
   - IAM: CloudWatch Logs + SSM bot-token read + SESv2 SendEmail + `dynamodb:GetItem` on
     `km-sandboxes` (for sandbox→channel resolution) + `dynamodb:UpdateItem` on `km-action-quota`
     (to set `alert_sent` flag) + DDB Streams read (`dynamodb:GetRecords`,
     `dynamodb:GetShardIterator`, `dynamodb:DescribeStream`, `dynamodb:ListStreams`) on
     `quota_stream_arn`.

**2. Live terragrunt unit:** `infra/live/use1/lambda-quota-alerter/terragrunt.hcl`
   Must declare `dependency "quota_table" { config_path = "../dynamodb-action-quota" }` and
   pass `quota_stream_arn = dependency.quota_table.outputs.stream_arn`.

**3. `init.go` curated module list:** `internal/app/cmd/init.go:511` (`regionalModules()`)
   Add entry after `dynamodb-action-quota`:
   ```go
   { name: "lambda-quota-alerter", dir: ..., envReqs: []string{"KM_ARTIFACTS_BUCKET"} }
   ```
   `envReqs: ["KM_ARTIFACTS_BUCKET"]` matches other Lambda entries.

**4. `lambdaBuilds()` list:** `internal/app/cmd/init.go:2944`
   Add:
   ```go
   {name: "km-quota-alerter", srcDir: "cmd/km-quota-alerter"},
   ```
   **Critical:** missing this entry means the zip is never built even if the TF module and live
   unit both exist (`project_km_init_skips_existing_lambda_zips` footgun).

### Alerter notification paths

**SES email (operator alert):** `pkg/aws/ses.go:41` — `SendLifecycleNotification` is the reference
pattern. The alerter calls `sesv2.SendEmail` via the `SESV2API` interface (`pkg/aws/ses.go:22-26`).
Operator email address: from env `KM_OPERATOR_EMAIL` (same pattern as TTL handler / budget enforcer).

**Slack channel-level notice (sandbox user notice):** The alerter resolves the sandbox's channel
from the `km-sandboxes` row (`slack_channel_id` attr) via a `GetItem` on the sandboxes table, then
calls the Slack API `chat.postMessage` via the bot token from SSM (`/{prefix}/slack/bot-token`).
This mirrors how `pkg/slack/client.go` `PostMessage` works. The alerter reuses `pkg/slack.Client`
from the existing `cmd/km-slack-bridge` code, or calls the Slack API directly via an HTTP client.

**Idempotency:** Set `alert_sent = :now` with a `ConditionExpression = attribute_not_exists(alert_sent)`
on the quota row, AFTER sending, to guarantee exactly-one alert per (sandbox, action, window).

---

## 6. Per-Sandbox DDB Row Attrs + SandboxMetadata Round-Trip

### New attrs on `km-sandboxes`

Four new attrs written to `km-sandboxes` via `UpdateItem` (NOT full-row PutItem):
- `action_limits` (S) — resolved JSON limits map (at `km create` time)
- `action_frozen` (BOOL) — latched freeze flag
- `frozen_reason` (S) — human-readable reason
- `frozen_at` (S) — RFC3339 timestamp
- `frozen_by` (S) — "auto:{action}:{window}" or "operator:{sandbox_id}"

### `SandboxMetadata` struct — `pkg/aws/metadata.go:11`

Must add:
```go
ActionLimits string     `json:"action_limits,omitempty"`  // resolved JSON map
ActionFrozen bool       `json:"action_frozen,omitempty"`
FrozenReason string     `json:"frozen_reason,omitempty"`
FrozenAt     *time.Time `json:"frozen_at,omitempty"`
FrozenBy     string     `json:"frozen_by,omitempty"`
```

### Round-trip — ALL FOUR write sites

The lossy-round-trip footgun means new attrs MUST be threaded through all four files:

1. **`pkg/aws/sandbox_dynamo.go:334` (`marshalSandboxItem`)** — emit new attrs in the item map
2. **`pkg/aws/sandbox_dynamo.go:147` (`unmarshalSandboxItem` / new unmarshalFrozenFields func)** — read new attrs
3. **`pkg/aws/sandbox_dynamo.go:69` (`toSandboxMetadata`)** — assign from `sandboxItemDynamo`
   NOTE: the `sandboxItemDynamo` struct (line 47) also needs new fields for the frozen attrs.
4. **`pkg/aws/sandbox_dynamo.go:520` (`ListAllSandboxesByDynamo`)** — calls `unmarshalFrozenFields`
5. **`pkg/aws/sandbox_dynamo.go:459` (`ReadSandboxMetadataDynamo`)** — calls `unmarshalFrozenFields`

The `action_limits` attr is a resolved JSON string set at `km create` time. It can be written with
`UpdateSandboxStringAttrDynamo` (sandbox_dynamo.go:820) immediately after the row is first written.

The `action_frozen` + frozen_* attrs are written by the new `FreezeSandboxDynamo` function (mirrors
`LockSandboxDynamo`, sandbox_dynamo.go:644) using an `UpdateItem` with `SET action_frozen = :t,
frozen_reason = :reason, frozen_at = :now, frozen_by = :by`.

### `FetchByChannel` extension (bridge side)

`pkg/slack/bridge/aws_adapters.go:1040` — add reads for `action_limits` (S) and `action_frozen`
(BOOL) from the DDB item and set them on `SandboxRoutingInfo`. This is the bridge's synchronous
lookup; it must happen within the existing GSI query result.

---

## 7. Profile Schema + Config

### Profile schema — `spec.limits`

**File:** `pkg/profile/types.go:71` — `Spec` struct. Add:
```go
Limits *LimitsSpec `yaml:"limits,omitempty" json:"limits,omitempty"`
```
New `LimitsSpec` and `ActionLimitSpec` types in the same file:
```go
type LimitsSpec struct {
    GithubPR      *ActionLimitSpec `yaml:"github_pr,omitempty"`
    GithubComment *ActionLimitSpec `yaml:"github_comment,omitempty"`
    GithubReview  *ActionLimitSpec `yaml:"github_review,omitempty"`
    EmailSend     *ActionLimitSpec `yaml:"email_send,omitempty"`
    SlackPost     *ActionLimitSpec `yaml:"slack_post,omitempty"`
    H1Comment     *ActionLimitSpec `yaml:"h1_comment,omitempty"`
}
type ActionLimitSpec struct {
    Lifetime  *int64 `yaml:"lifetime,omitempty"`
    PerHour   *int64 `yaml:"perHour,omitempty"`
    PerDay    *int64 `yaml:"perDay,omitempty"`
    OnBreach  string `yaml:"onBreach,omitempty"` // "warn" | "block" | "freeze"
}
```

**JSON schema file:** `pkg/profile/schemas/` — the `additionalProperties: false` JSON schema
must also gain the `limits` block. No `apiVersion` bump (additive field, consistent with Phase 118/119).

**`km validate` extension:** Add validation in `pkg/profile/validate.go` — validate `onBreach` enum
values; validate that `lifetime`/`perHour`/`perDay` are positive when set.

### Config (`km-config.yaml`) `limits:` key

**File:** `internal/app/config/config.go`
**The v2→v merge-list** (config.go:830 region) — add `"limits"` to the slice at config.go:874:
```go
"limits",  // Phase 121: install-level action quota defaults
```
**Without this entry**, the `limits:` block in `km-config.yaml` is silently dropped
(`project_config_key_merge_list` footgun — verified against actual code at line 874).

**Config struct:** Add `Limits LimitsConfig` field (mirrors `Checks`, `H1`, `Github` pattern).
Add `GetLimitsConfig()` getter.

### Compiler delivery to chokepoints

**Proxy delivery (userdata):**
`pkg/compiler/userdata.go` writes env vars into the systemd drop-in at line 4773. Add:
```
Environment=KM_QUOTA_TABLE={{ .QuotaTable }}
Environment=KM_ACTION_LIMITS={{ .ActionLimitsJSON }}
```
The template data struct (`userDataParams` near line 4941) gains `QuotaTable string` and
`ActionLimitsJSON string`. The compiler resolves per-profile limits → merges with install defaults →
JSON-encodes → injects as `KM_ACTION_LIMITS`. The proxy reads this at startup.

**Bridge delivery (DDB attr):**
At `km create` time, the resolved limits JSON is written to `km-sandboxes.action_limits` via
`UpdateSandboxStringAttrDynamo`. `FetchByChannel` reads it at dispatch time. This mirrors how
`slack_allow` is stored as a comma-joined S attr and read in FetchByChannel (aws_adapters.go:1108).

---

## 8. CLI Surface

### `km lock` / `km unlock` — reference pattern

**`km lock`:** `internal/app/cmd/lock.go` — calls `awspkg.LockSandboxDynamo` (sandbox_dynamo.go:644),
uses `ConditionExpression = attribute_exists(sandbox_id) AND (attribute_not_exists(locked) OR locked = :f)`.

**`km unlock`:** `internal/app/cmd/unlock.go:63` — calls `awspkg.UnlockSandboxDynamo` (sandbox_dynamo.go:675),
uses `ConditionExpression = attribute_exists(sandbox_id) AND locked = :t`.

### New `km freeze <sandbox> [--reason ...]`

**New file:** `internal/app/cmd/freeze.go` (mirrors lock.go structure).
Calls new `FreezeSandboxDynamo(ctx, client, tableName, sandboxID, reason, by)` in `pkg/aws/sandbox_dynamo.go`.
The DynamoDB write:
```
SET action_frozen = :t, frozen_reason = :reason, frozen_at = :now, frozen_by = :by
```
No `ConditionExpression` guard — `km freeze` is idempotent (re-freezing an already-frozen sandbox
is fine; it updates reason/timestamp).

### `km unlock` — latch-aware extension

**File:** `internal/app/cmd/unlock.go:63` (`runUnlock`). After `UnlockSandboxDynamo` succeeds (or
separately), also check `action_frozen` on the row and clear it:
```
SET action_frozen = :f REMOVE frozen_reason, frozen_at, frozen_by
```
The implementation can either:
a. Do a `GetItem` first to see if frozen, then report what was cleared; OR
b. Always issue the remove expression (idempotent — removes absent attrs is a no-op).

**`UnlockSandboxDynamo` in sandbox_dynamo.go:675** currently only clears `locked`/`locked_at`.
Either extend the function signature or add a new `UnfreezeSandboxDynamo` function called from
`runUnlock` after the safety-lock clear.

### `km list` / `km status` — FROZEN marker

**`km list` (`internal/app/cmd/list.go:545`):** Add `case "frozen": return "🧊 froz"` to
`statusDisplay`. Alternatively, overlay `FROZEN` as a column flag (check `meta.ActionFrozen` in
the row render loop). The `SandboxRecord` struct (`pkg/aws/sandbox.go:22`) needs `ActionFrozen bool`.

**`km status`:** Add a `FROZEN` section in the detailed output showing `frozen_reason`, `frozen_at`,
`frozen_by`.

### `km doctor` frozen-sandbox check

**Pattern:** `internal/app/cmd/doctor.go` — existing checks scan DDB. A new check scans all
`km-sandboxes` rows for `action_frozen = true` and surfaces them as WARN (with reason + duration).
The check also verifies `{prefix}-action-quota` table existence (table health check, like the
existing checks for other tables).

---

## 9. Open Questions / Risks

### Risk 1: Double-counting via proxy + bridge POST

**Issue:** The `km-slack` sidecar on the sandbox calls `pkg/slack/client.go` which POSTs an
Ed25519-signed envelope to the bridge Function URL (`KM_SLACK_BRIDGE_URL`). This POST transits
through the HTTPS proxy (port 443, the MITM proxy catches it). The bridge Function URL is
`*.lambda-url.us-east-1.on.aws` which resolves to an HTTPS endpoint. **If the proxy also counts
`slack_post` at the proxy level**, each Slack post would be counted TWICE — once by the proxy
intercepting the bridge Function URL POST, and once by the bridge itself.

**Resolution (design is locked — flag for implementation):** The proxy must NOT count `slack_post`
actions. Only the **bridge Lambdas** own Slack/H1 quota metering. The proxy only counts GitHub
writes (`api.github.com`) and SES email sends (`email.*.amazonaws.com`). CONTEXT.md §3 already
describes this split correctly — this risk is about an implementation mistake to avoid.

**Verification:** The km-slack binary POSTs to the bridge URL which is an `.on.aws` subdomain.
That host IS in the proxy allowedHosts list (it must be for Slack posting to work at all). An
`OnRequest(ReqHostMatches(.*lambda-url.*.on.aws))` check for `slack_post` counting in the proxy
would be double-counting. **The proxy's quota handlers must be scoped ONLY to `api.github.com`
and `email.*.amazonaws.com`.**

### Risk 2: SES endpoint requires new MITM registration

**Issue:** `email.*.amazonaws.com` is currently allowed as OkConnect (passthrough). Adding MITM
for this host is new. The AWS CLI for `aws sesv2` respects the system CA store (which already
trusts the km CA at boot). However, if `AWS_CA_BUNDLE` is set to something that doesn't include
the km CA, CLI calls will fail.

**Verification needed at UAT:** Verify `aws sesv2 send-email` works through MITM proxy on a live
sandbox before ship.

### Risk 3: No existing `aws_lambda_event_source_mapping` in the codebase

**Issue:** There is no existing TF pattern for wiring a Lambda to a DDB Stream via
`aws_lambda_event_source_mapping` in this codebase. The budget-enforcer uses EventBridge instead.
The planner must create this pattern from scratch for the quota-alerter.

**Confirmed:** `grep -r "aws_lambda_event_source_mapping" infra/` returns empty. The TF resource is
well-documented (AWS provider), but the planner needs to add it to the alerter module's main.tf,
including the IAM grant for `dynamodb:GetRecords`, `dynamodb:GetShardIterator`,
`dynamodb:DescribeStream`, `dynamodb:ListStreams` on the stream ARN.

### Risk 4: `km-send` in userdata uses root-exempt DNAT

**Issue:** `iptables -t nat -A OUTPUT -p tcp --dport 443 -m owner ! --uid-owner km-sidecar -j REDIRECT --to-ports 3128` (userdata.go:4458) redirects the sandbox user's traffic but NOT root. The `aws sesv2 send-email` command in `km-send` (userdata.go:3165) runs as the **sandbox user**, so SES traffic from `km-send` DOES transit the proxy. However, the lifecycle notification `aws sesv2 send-email` call in the userdata bootstrap (line 4698) runs as root — this is NOT proxied and cannot be quota-counted. This is acceptable (lifecycle notifications are not high-impact agent actions), but the planner should note it.

### Risk 5: Proxy receives quota limits as env var JSON — size limit

**Issue:** If `KM_ACTION_LIMITS` JSON is rendered as a systemd drop-in `Environment=` line, there
is a systemd environment variable size limit (~32KB per env block). For the Phase 121 config (6 actions
× 3 windows × a few fields each), the JSON will be well under 1KB. Not a practical risk.

### Risk 6: `km-send` via `aws sesv2` CLI — request classification

**Classification:** SESv2 `SendEmail` API call is `POST /v2/email/outbound-emails` to
`https://email.{region}.amazonaws.com`. Matching: new `sesHostRegex = regexp.MustCompile(...)` in
the proxy, OnRequest match on `req.Method == "POST" && strings.HasPrefix(req.URL.Path, "/v2/email/outbound-emails")`.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` package (standard) |
| Config file | none (per-package, `go test ./...`) |
| Quick run command | `go test ./pkg/quota/... -count=1 -timeout 60s` |
| Full suite command | `go test ./... -count=1 -timeout 600s` |

### Phase Requirements → Test Map

Derived from CONTEXT.md success criteria:

| ID | Behavior | Test Type | Automated Command | File Exists? |
|----|----------|-----------|-------------------|-------------|
| QUO-01 | `pkg/quota.Record` fixed-window bucket math (hourly `epoch/3600`, daily `epoch/86400`) | unit | `go test ./pkg/quota/... -run TestRecord` | ❌ Wave 0 |
| QUO-02 | `Record` resolution: profile value → install default → unlimited (per-window precedence) | unit | `go test ./pkg/quota/... -run TestResolveLimits` | ❌ Wave 0 |
| QUO-03 | `Record` returns `tripped=true` when any window exceeds limit | unit | `go test ./pkg/quota/... -run TestDecision` | ❌ Wave 0 |
| QUO-04 | `Record` is atomic (ADD, not read-modify-write) — verify UpdateItem expression in output | unit | `go test ./pkg/quota/... -run TestAtomicADD` | ❌ Wave 0 |
| QUO-05 | `lifetime` rows have no TTL; `hour`/`day` rows have TTL ~2h/~2d | unit | `go test ./pkg/quota/... -run TestTTL` | ❌ Wave 0 |
| PRX-01 | Proxy classifies `POST api.github.com /repos/*/pulls` → `github_pr` | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestClassifyGitHub` | ❌ Wave 0 |
| PRX-02 | Proxy classifies `POST email.*.amazonaws.com /v2/email/outbound-emails` → `email_send` | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestClassifySES` | ❌ Wave 0 |
| PRX-03 | Proxy does NOT classify bridge Function URL POST (`*.on.aws`) as slack_post | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestNoDoubleCount` | ❌ Wave 0 |
| BRG-01 | Slack bridge calls `quota.Record` for `ActionPost`/`ActionUpload`; returns 429 in BLOCK mode | unit | `go test ./pkg/slack/bridge/... -run TestQuotaRecord` | ❌ Wave 0 |
| BRG-02 | Slack bridge checks `action_frozen` from `SandboxRoutingInfo`; refuses dispatch + posts in-thread notice | unit | `go test ./pkg/slack/bridge/events_handler_test.go -run TestFrozenDispatch` | ❌ Wave 0 |
| BRG-03 | `FetchByChannel` reads `action_limits` and `action_frozen` from DDB item | unit | `go test ./pkg/slack/bridge/... -run TestFetchByChannel` | ❌ Wave 0 |
| META-01 | `SandboxMetadata` round-trip: `ActionFrozen`, `FrozenReason`, `ActionLimits` survive marshal→unmarshal | unit | `go test ./pkg/aws/... -run TestSandboxMetadataRoundTrip` | ❌ Wave 0 |
| META-02 | `marshalSandboxItem` emits `action_frozen`, `frozen_reason`, `action_limits` attrs | unit | `go test ./pkg/aws/... -run TestMarshalFrozen` | ❌ Wave 0 |
| INIT-01 | `regionalModules()` includes `dynamodb-action-quota` (before ses) | unit | `go test ./internal/app/cmd/... -run TestRunInitPlan_ModuleOrder` | ✅ (must update count) |
| INIT-02 | `lambdaBuilds()` includes `km-quota-alerter` | unit | `go test ./internal/app/cmd/... -run TestQuotaAlerterBuildListMembership` | ❌ Wave 0 |
| CFG-01 | `limits:` key in km-config.yaml is NOT silently dropped (v2→v merge-list) | unit | `go test ./internal/app/config/... -run TestLimitsConfigLoaded` | ❌ Wave 0 |
| CLI-01 | `km freeze <sandbox>` writes `action_frozen=true` + frozen_* attrs via atomic UpdateItem | unit | `go test ./internal/app/cmd/... -run TestRunFreeze` | ❌ Wave 0 |
| CLI-02 | `km unlock <sandbox>` clears `action_frozen` alongside safety-lock | unit | `go test ./internal/app/cmd/... -run TestRunUnlockLatchAware` | ❌ Wave 0 |

### Live UAT Items (cannot be covered by Go unit tests alone)

These require a live sandbox + live Slack/GitHub integration:

1. **In-thread trip notice (bridge, chat trip):** Post enough Slack messages to trip `perHour` limit
   → verify the bridge posts a threaded `"⚠️ Quota reached"` notice in the same thread as the
   blocked message. Go unit tests can verify the bridge calls the notifier; only live UAT verifies
   the rendered Slack message appears correctly.

2. **Channel-level notice (alerter, proxy trip):** Send enough GitHub comments via `km-github` to
   trip limit → verify the `km-quota-alerter` Lambda fires from the DDB Stream and posts to the
   sandbox's main Slack channel. DDB Stream lag (usually <1s) must be observed live.

3. **Freeze gate — inbound dispatch refused:** Freeze a sandbox (`km freeze <id>`) → send a Slack
   message → verify the bridge refuses dispatch and posts the control-plane frozen notice.
   Go tests cover the code; live UAT verifies the actual Slack message content.

4. **SES MITM verification:** Send email from inside a live sandbox (`km-send ...`) → verify the
   proxy intercepts it, counts it, and (if limit is set to 1) blocks the second send. This is the
   new SES MITM path which has no existing live test.

5. **`km unlock` clears freeze:** `km freeze <id>` then `km unlock <id>` → verify both the safety
   lock AND the `action_frozen` flag are cleared; verify Slack dispatch resumes.

6. **Alerter idempotency:** Trip a limit twice in rapid succession → verify only ONE operator SES
   email is sent (the `alert_sent` conditional write prevents the second).

### Sampling Rate
- **Per task commit:** `go test ./pkg/quota/... ./pkg/aws/... ./sidecars/http-proxy/httpproxy/... -count=1 -timeout 60s`
- **Per wave merge:** `go test ./... -count=1 -timeout 600s`
- **Phase gate:** Full suite green + live UAT items 1-6 above before `/gsd:verify-work`

### Wave 0 Gaps (test infrastructure that must exist before implementation)

- [ ] `pkg/quota/` — new package, needs `quota.go` + `quota_test.go` covering QUO-01 through QUO-05
- [ ] `pkg/quota/testutil/` — DDB mock implementing the `ADD count 1` operation (or reuse `aws.BudgetAPI` mock pattern from `pkg/aws/budget_test.go`)
- [ ] `internal/app/cmd/init_plan_test.go:439` — bump `!= 24` to `!= 26` (table + alerter) — MUST update before the quota modules are added or the test fails on every run
- [ ] `internal/app/cmd/freeze_test.go` — covering CLI-01 + CLI-02
- [ ] `pkg/slack/bridge/events_handler_frozen_test.go` — covering BRG-02

---

## Standard Stack (Deploy Surface Summary)

### Confirmed Deploy Sequence (from CONTEXT.md §8, grounded in code)

```bash
# Step 1: binary carries new regionalModules() entry (MUST be before km init)
make build

# Step 2: build all Lambda zips including km-quota-alerter (new)
make build-lambdas

# Step 3: full apply — provisions new table, Streams, alerter Lambda, IAM, env blocks
km init --dry-run=false

# Step 4: proxy sidecar carries new WithActionQuota option + spec.limits schema
make build && km init --sidecars

# Step 5: existing sandboxes need recreate for userdata env vars + per-sandbox attrs
km destroy <id> --remote --yes && km create <profile>
```

### Confirmed File Locations for All Deploy-Surface Items

| Footgun | File | Line | What to do |
|---------|------|------|------------|
| `regionalModules()` new entry | `internal/app/cmd/init.go` | 511 (start of `regionalModules()`) | Add `dynamodb-action-quota` + `lambda-quota-alerter` entries before `ses` |
| Module-order test count | `internal/app/cmd/init_plan_test.go` | 439 | Bump `24` → `26` (table + alerter) |
| `lambdaBuilds()` new entry | `internal/app/cmd/init.go` | 2944 (start of `lambdaBuilds()`) | Add `{name: "km-quota-alerter", srcDir: "cmd/km-quota-alerter"}` |
| Config v2→v merge-list | `internal/app/config/config.go` | ~874 (the `for _, key := range []string{...}` block) | Add `"limits"` to the slice |
| `SandboxMetadata` struct | `pkg/aws/metadata.go` | 11 | Add 5 new fields |
| `marshalSandboxItem` | `pkg/aws/sandbox_dynamo.go` | 334 | Emit new attrs |
| `unmarshalSandboxItem` + new helper | `pkg/aws/sandbox_dynamo.go` | 147 + new func | Read new attrs |
| `FetchByChannel` | `pkg/slack/bridge/aws_adapters.go` | 1040 | Read `action_limits`, `action_frozen` |
| `SandboxRoutingInfo` struct | `pkg/slack/bridge/events_interfaces.go` | 106 | Add `ActionLimits`, `ActionFrozen` fields |
| `statusDisplay` (km list) | `internal/app/cmd/list.go` | 545 | Add `frozen` case |
| `SandboxRecord` struct | `pkg/aws/sandbox.go` | 22 | Add `ActionFrozen bool` |

---

## Sources

### Primary (HIGH confidence — grounded in actual source files)
- `sidecars/http-proxy/httpproxy/proxy.go` — proxy MITM extension pattern
- `sidecars/http-proxy/main.go` — proxy env var wiring
- `pkg/aws/budget.go:65` — `IncrementAISpend` atomic ADD pattern
- `pkg/aws/sandbox_dynamo.go` — `marshalSandboxItem`, `unmarshalSandboxItem`, `LockSandboxDynamo`, `UnlockSandboxDynamo`, `UpdateSandboxStringAttrDynamo`
- `pkg/aws/metadata.go` — `SandboxMetadata` struct
- `pkg/slack/bridge/handler.go` — action dispatch switch
- `pkg/slack/bridge/aws_adapters.go:1040` — `FetchByChannel` implementation
- `pkg/slack/bridge/events_interfaces.go:106` — `SandboxRoutingInfo` struct
- `internal/app/cmd/init.go:511` — `regionalModules()` full list (24 entries as of Phase 116)
- `internal/app/cmd/init.go:2944` — `lambdaBuilds()` list
- `internal/app/cmd/init_plan_test.go:439` — hardcoded module count `24`
- `internal/app/config/config.go:874` — v2→v merge-list
- `infra/modules/dynamodb-budget/v1.0.0/main.tf` — DDB Streams pattern
- `infra/modules/dynamodb-slack-channels/v1.0.0/main.tf` — minimal DDB table pattern
- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` — Lambda module pattern
- `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` — live terragrunt unit pattern
- `internal/app/cmd/lock.go` + `unlock.go` — km lock/unlock CLI pattern
- `internal/app/cmd/list.go:545` — `statusDisplay` function
- `pkg/aws/ses.go:41` — `SendLifecycleNotification` SES pattern
- `pkg/compiler/userdata.go:4773` — systemd drop-in env injection pattern

### Secondary (MEDIUM confidence)
- CONTEXT.md — locked design decisions (all grounded in source during research)
- STATE.md — Phase 121 roadmap entry (confirmed matches CONTEXT.md)

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all proxy patterns, DDB patterns, bridge patterns verified in source
- Architecture: HIGH — all file:line anchors verified
- Pitfalls / deploy-surface: HIGH — each footgun verified against actual code locations
- SES MITM: MEDIUM — pattern is clear (mirrors Bedrock MITM) but SES MITM is new and needs live UAT verification

**Research date:** 2026-06-27
**Valid until:** 2026-08-01 (stable architecture; no fast-moving dependencies)
