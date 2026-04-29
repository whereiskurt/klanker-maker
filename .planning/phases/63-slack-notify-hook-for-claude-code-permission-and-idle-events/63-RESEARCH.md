# Phase 63: Slack-Notify Hook — Research

**Researched:** 2026-04-28
**Domain:** Slack Web API, Go Lambda (Function URL, auth=NONE), Ed25519 signing trust model extension, profile schema, compiler heredoc extension, DynamoDB schema addition
**Confidence:** HIGH (codebase patterns), MEDIUM (Slack API specifics from official docs)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Profile schema — five new fields under `spec.cli`:**

| Field | Type | Default | Effect |
|---|---|---|---|
| `notifyEmailEnabled` | bool | `true` | Skip email dispatch when `false`. Backward-compat default. |
| `notifySlackEnabled` | bool | `false` | Enable Slack delivery for events that pass existing gates. |
| `notifySlackPerSandbox` | bool | `false` | Create `#sb-{id}` at create, archive at destroy. |
| `notifySlackChannelOverride` | string | unset | Hard-pin channel ID; overrides shared and per-sandbox modes. |
| `slackArchiveOnDestroy` | bool | `true` | Per-sandbox channels only. `false` preserves trail post-teardown. |

**Validation rules:**
- `notifySlackPerSandbox: true` AND `notifySlackChannelOverride: <set>` → validation error.
- `notifySlackPerSandbox: true` AND `notifySlackEnabled: false` → validation warning.
- `slackArchiveOnDestroy` set without `notifySlackPerSandbox: true` → validation warning.
- `notifySlackChannelOverride` not matching `^C[A-Z0-9]+$` → validation error.
- `notifyEmailEnabled: false` AND `notifySlackEnabled: false` → validation warning.

Schema changes in `pkg/profile/types.go` and `pkg/profile/schemas/sandbox_profile.schema.json`. No CLI flag overrides in v1.

**Trust model:** Reuses existing Ed25519 plumbing. Sandbox keys at `/sandbox/{id}/signing-key` (SSM, private). Operator key at `/sandbox/operator/signing-key` (SSM, private). Bridge Lambda fetches public keys from DynamoDB `km-identities` table (NOT from a separate SSM public-key path — see Critical Findings below).

**`km slack init` — one-time bootstrap:**
1. Prompt/store bot token at `/km/slack/bot-token` (SSM SecureString, KMS-encrypted).
2. Store workspace metadata at `/km/slack/workspace`.
3. Store invite email at `/km/slack/invite-email`.
4. Create shared `#km-notifications` via `conversations.create`, invite via `conversations.inviteShared`, store channel ID at `/km/slack/shared-channel-id`.
5. Deploy `km-slack-bridge` Lambda, store Function URL at `/km/slack/bridge-url`.

**Per-sandbox channel lifecycle:** Shared mode reads SSM, per-sandbox mode calls `conversations.create` + `conversations.inviteShared`. Channel ID stored in DynamoDB `km_sandboxes.slack_channel_id`. Injected into sandbox env via `/etc/profile.d/km-notify-env.sh`. Failure during per-sandbox channel creation or override validation aborts `km create`.

**At `km destroy`:** Per-sandbox + `slackArchiveOnDestroy: true` → final post + `conversations.archive` via bridge Lambda. Failure logs warning, does not block destroy.

**Hook extension:** Extends the Phase 62 inline heredoc in `pkg/compiler/userdata.go` (the `KM_NOTIFY_HOOK_EOF` heredoc). Adds `sent_any` variable, parallel email+Slack dispatch paths, cooldown only updates when `sent_any == 1`.

**`km-slack` binary:** Go binary at `/opt/km/bin/km-slack`. Built and uploaded as a sidecar artifact alongside dns-proxy/http-proxy/audit-log. Body file required (no stdin, same rationale as km-send). 40 KB body cap. Retry 3 attempts on 5xx/503/network, backoff 1s/2s/4s.

**Bridge Lambda envelope:** JSON discriminated on `action: post | archive | test`. `version: 1` field. Replay protection: timestamp ±5 min + nonce table `km_slack_bridge_nonces` with 10 min TTL. Channel-mismatch authorization: sandbox `post` must match `slack_channel_id` in DynamoDB metadata.

**`km doctor` additions:** Slack token validity check, stale per-sandbox channels check.

**No CLI flag overrides in v1.** All five fields are profile-only.

### Claude's Discretion

- Slack API client implementation: `slack-go/slack` third-party SDK vs. thin HTTP client (no SDK currently in go.mod).
- `ValidationError` severity distinction: how to represent "warning" vs "error" given `ValidationError` struct has no severity field currently.
- How `km slack init` triggers Terraform apply for the bridge Lambda module (standalone or adding to `regionalModules()`).
- Plan breakdown and wave assignment within the suggested decomposition.

### Deferred Ideas (OUT OF SCOPE)

- Closed-loop reply ingestion (Slack reply → agent).
- Slack interactive features (slash commands, buttons, modals).
- Block Kit / rich formatting beyond bold subject header.
- DM delivery, multiple invite recipients, Slack-to-email bridging.
- Retroactive Slack support on existing sandboxes.
- `km pause` / `km resume` Slack notifications.
- CLI flag overrides for the five new profile fields.
- `thread_ts` wired but unused by v1 hook.
</user_constraints>

<phase_requirements>
## Phase Requirements

No IDs registered yet in REQUIREMENTS.md. Following Phase 62 pattern, planner should register the following IDs under a new "Slack Notifications" group:

| Recommended ID | Description | Research Support |
|----------------|-------------|-----------------|
| SLCK-01 | Profile schema adds five `spec.cli` fields with validation rules (error + warning tiers) | `pkg/profile/types.go` CLISpec extension; `ValidateSemantic` pattern |
| SLCK-02 | Compiler extends km-notify-hook heredoc for multi-channel dispatch; emits `KM_SLACK_*` env vars into `/etc/profile.d/km-notify-env.sh` | Phase 62 heredoc at `userdata.go:354`; NotifyEnv template at line 432 |
| SLCK-03 | `km-slack` Go binary: signs payload with sandbox Ed25519 key, POSTs to bridge Lambda URL, 3-attempt retry with backoff | Mirrors `km-send` signing pattern; Go binary added to sidecar Makefile |
| SLCK-04 | `km-slack-bridge` Lambda: Ed25519 verify, replay protection, channel-mismatch auth, Slack API dispatch | New Lambda module `infra/modules/lambda-slack-bridge/`; Function URL auth=NONE (first in codebase) |
| SLCK-05 | `km slack init/test/status` operator commands; one-time bootstrap flow | New `internal/app/cmd/slack.go`; SSM `/km/slack/*` pattern |
| SLCK-06 | `km create` provisions Slack channel (shared/per-sandbox/override), writes `slack_channel_id` to DynamoDB and env file | `create.go` extension; `SandboxMetadata` struct addition |
| SLCK-07 | `km destroy` posts final message + archives per-sandbox channels via bridge Lambda | `destroy.go` extension; non-blocking on Slack failure |
| SLCK-08 | `km doctor` adds Slack token validity and stale-channel health checks | `doctor.go` extension; bridge Lambda `auth.test` invocation |
</phase_requirements>

---

## Summary

Phase 63 extends Phase 62's operator-notify mechanism with parallel Slack delivery. The design reuses the existing Ed25519 trust model, DynamoDB sandbox metadata schema, SSM parameter convention, and profile compiler patterns — it adds new surface (bridge Lambda, `km-slack` binary, `km slack` CLI subcommand) while touching existing surface in a narrowly additive way.

The most important architectural finding is a **spec discrepancy**: the CONTEXT.md design says the bridge Lambda fetches public keys from SSM at `/sandbox/{id}/signing-public-key`, but that SSM path does not exist in this codebase. Public keys are stored in the DynamoDB `km-identities` table. The bridge Lambda must use `FetchPublicKey()` from `pkg/aws/identity.go` (same as the email verification path), not SSM.

A second important finding: Lambda Function URLs with `auth_type = NONE` are not yet used anywhere in this codebase. The bridge Lambda will be the first. The Terraform resource `aws_lambda_function_url` needs to be introduced.

**Primary recommendation:** Follow the email-handler Lambda module as the exact Terraform template, add `aws_lambda_function_url` resource, use DynamoDB for public key lookups rather than the SSM paths described in the spec, and write a thin HTTP client for Slack (no SDK) consistent with the codebase's `pkg/aws/` patterns.

---

## Codebase Inventory

### Phase 62 hook heredoc — the extension point

**File:** `pkg/compiler/userdata.go`
**Lines 354–428:** The `km-notify-hook` bash script is inlined as a heredoc between `KM_NOTIFY_HOOK_EOF` delimiters. Phase 63 extends this exact heredoc — it does NOT extract the script to a separate file.

Current Phase 62 dispatch (lines 418–427):
```bash
to_args=()
[[ -n "${KM_NOTIFY_EMAIL:-}" ]] && to_args=(--to "$KM_NOTIFY_EMAIL")
if /opt/km/bin/km-send ${to_args[@]+"${to_args[@]}"} --subject "$subject" --body "$body_file"; then
  date +%s > "$last_file"
fi
rm -f "$body_file"
exit 0
```

Phase 63 replaces this with the multi-channel `sent_any` dispatch from the CONTEXT.md design.

**Lines 432–448:** The `KM_NOTIFY_ENV_EOF` block writes `/etc/profile.d/km-notify-env.sh` conditionally on `.NotifyEnv`. Phase 63 adds four new env var keys to this map: `KM_NOTIFY_EMAIL_ENABLED`, `KM_NOTIFY_SLACK_ENABLED`, `KM_SLACK_CHANNEL_ID`, `KM_SLACK_BRIDGE_URL`.

**Reason for profile.d vs /etc/environment:** The Phase 62 RESEARCH.md and SUMMARY.md explicitly document that `/etc/profile.d/` is used (not `/etc/environment`) because Amazon Linux 2 SSM sessions reliably source `profile.d`. The spec originally said `/etc/environment` but the codebase uses `profile.d`. Do NOT switch to `/etc/environment`.

**Template data struct** (`UserDataParams` at line 2202): The `NotifyEnv map[string]string` field drives the env block. Phase 63 extends the population logic at line 2425 to add the new keys.

### Profile schema — CLISpec extension point

**File:** `pkg/profile/types.go`, lines 354–395
**`CLISpec` struct** currently ends at `NotificationEmailAddress string`. Phase 63 appends five new fields. No existing fields change.

**File:** `pkg/profile/schemas/sandbox_profile.schema.json`
Channel ID regex is `^C[A-Z0-9]+$`. This must be added as a JSON Schema `pattern` on `notifySlackChannelOverride`.

**File:** `pkg/profile/validate.go`
`ValidateSemantic()` at line 213 is where the five new rules go. Current struct `ValidationError` has no severity field — it carries only `Path` and `Message`. The CONTEXT.md uses "validation warning" to mean rules that should not cause `km validate` to exit non-zero; the planner must decide whether to add an `IsWarning bool` field to `ValidationError` or handle warning conditions differently. Existing code emits eBPF+ECS combination as a `ValidationError` labelled as a "warn" in a comment but treated as a blocking error by `km validate`. Consistent handling is an open question (see Open Questions).

### Signing key infrastructure — CRITICAL FINDING

**Private key path:** SSM at `/sandbox/{id}/signing-key` (SecureString, KMS-encrypted). Function: `signingKeyPath()` at `pkg/aws/identity.go:92`. This is what `km-slack` fetches on the sandbox.

**Public key storage:** DynamoDB `km-identities` table, `public_key` attribute (base64 Ed25519 public key). Retrieved via `FetchPublicKey()` at `pkg/aws/identity.go:231`. This is what the bridge Lambda uses for signature verification.

**THE SPEC DISCREPANCY:** The CONTEXT.md says the bridge Lambda fetches `/sandbox/{id}/signing-public-key` from SSM. This SSM path **does not exist**. There is no code anywhere in this codebase that writes public keys to SSM. Public keys go exclusively to DynamoDB. The bridge Lambda MUST use `FetchPublicKey(ctx, identityClient, tableName, sandboxID)` — not SSM.

**Operator public key:** Same pattern — operator identity at DynamoDB `km-identities` table with `sandbox_id = "operator"`. Fetched via `FetchPublicKey(ctx, identityClient, tableName, "operator")`.

**Implication for bridge Lambda IAM:** The spec lists `ssm:GetParameter` on `/sandbox/*/signing-public-key` as a Lambda IAM permission. This is wrong. The bridge Lambda needs `dynamodb:GetItem` on `km-identities` instead. It already needs `dynamodb:GetItem` on `km-sandboxes` for channel-mismatch check.

### DynamoDB sandbox metadata schema

**File:** `pkg/aws/metadata.go` — `SandboxMetadata` struct (lines 11–27).
**File:** `pkg/aws/sandbox_dynamo.go` — `marshalSandboxItem()` / `unmarshalSandboxItem()` / `WriteSandboxMetadataDynamo()`.

`slack_channel_id` is not yet in the schema. Phase 63 adds:
- `SlackChannelID string \`json:"slack_channel_id,omitempty"\`` to `SandboxMetadata`
- `SlackPerSandbox bool \`json:"slack_per_sandbox,omitempty"\`` (needed to determine destroy behavior)
- Corresponding marshal/unmarshal entries in `sandbox_dynamo.go`

The DynamoDB table `km-sandboxes` uses `sandbox_id` as hash key, no sort key. Adding new string attributes is a no-op at the table level (DynamoDB is schemaless beyond key definitions). No Terraform change needed for `km-sandboxes` itself.

### Lambda deploy pipeline

**Build:** `buildLambdaZips()` at `init.go:861` — cross-compiles Lambda entry points from `cmd/` to `build/bootstrap`, zips to `build/{name}.zip`. Phase 63 adds `{name: "slack-bridge", srcDir: "cmd/km-slack-bridge"}` to the `lambdas` slice.

**`km init --lambdas`:** Rebuilds all Lambda ZIPs and forces create-handler cold start. Does NOT run Terraform apply. The bridge Lambda module needs Terraform apply to be deployed initially.

**Full `km init`:** `RunInitWithRunner()` at `init.go:558` iterates `regionalModules()`. Phase 63 adds `lambda-slack-bridge` to this list, after `email-handler` (no ordering dependency needed). Alternatively, `km slack init` can run `runner.Apply(ctx, modulePath)` itself (matches `km slack init` being the bootstrap entry point).

**Terragrunt module pattern:** Follow `infra/modules/email-handler/v1.0.0/` exactly:
- `aws_iam_role` with `replace_triggered_by = [aws_iam_role.slack_bridge]` on the Lambda (CLAUDE.md requirement — avoids stale KMS grants)
- `aws_lambda_function` with `runtime = "provided.al2023"`, `architectures = ["arm64"]`
- **New: `aws_lambda_function_url`** with `authorization_type = "NONE"` and `cors` block — first Function URL in this codebase
- Output the Function URL for storage to SSM

**`infra/live/{region}/lambda-slack-bridge/terragrunt.hcl`** follows `email-handler/terragrunt.hcl` pattern: find repo root, read site.hcl and region.hcl, S3 backend, source the module, pass inputs.

### `km-slack` Go binary — sidecar delivery

**km-send is a bash script** (userdata.go lines 838–1083), not a Go binary. `km-slack` is specified as Go for crypto + retry + JSON canonicalization reasons. This is new: no other sandbox-side Go binary currently exists (sidecars are Go but they're long-running daemons, not one-shot commands).

**Build location:** `cmd/km-slack/main.go` (new directory).

**Makefile changes required:**
1. Add build target to `build-sidecars` and `sidecars` targets:
   ```make
   GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build ... -o build/km-slack ./cmd/km-slack/
   aws s3 cp build/km-slack s3://$(KM_ARTIFACTS_BUCKET)/sidecars/km-slack
   ```
2. Add to `buildAndUploadSidecars()` in `init.go` (the `--sidecars` path).

**User-data download:** Add to the sidecar download block in `userdata.go` (lines 459–469):
```bash
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-slack" /opt/km/bin/km-slack
chmod +x /opt/km/bin/km-slack
```

**After `km init --sidecars`:** Existing sandboxes do NOT get the binary retroactively (user-data is baked at provision time). Only new sandboxes provisioned after the sidecar upload will have `km-slack`.

### Operator CLI — new `slack.go`

**File:** `internal/app/cmd/slack.go` (new).
Pattern: new Cobra command tree attached to root, similar to `ami.go` for `km ami list/bake/copy/delete`.

**`km slack init` flow** calls Slack Web API directly from the operator machine (not via the bridge Lambda, because the bridge isn't deployed yet at bootstrap time):
1. `auth.test` to validate bot token.
2. SSM `PutParameter` for `/km/slack/bot-token`, `/km/slack/workspace`, `/km/slack/invite-email`.
3. `conversations.create` for `#km-notifications`.
4. `conversations.inviteShared` to operator email.
5. SSM `PutParameter` for `/km/slack/shared-channel-id`.
6. Terraform apply for `infra/live/{region}/lambda-slack-bridge/`.
7. Capture Lambda Function URL output, SSM `PutParameter` for `/km/slack/bridge-url`.

The operator-side Slack API calls use the bot token fetched directly (not through the bridge). The bridge Lambda is only for sandbox-to-Slack and operator-signed bridge calls post-init.

### `doctor.go` health check pattern

**`CheckResult` struct** (line 58): `Name`, `Status` (OK/WARN/ERROR/SKIPPED), `Message`, `Remediation`.

**Check functions** (line 225 onward): standalone pure functions, DI-injected clients via narrow interfaces.

Two new checks:
- `checkSlackTokenValidity`: calls `auth.test` via bridge Lambda (uses operator signing key), returns WARN on invalid/expired token, SKIPPED if no `/km/slack/bot-token` configured.
- `checkStaleSlackChannels`: scans `km-sandboxes` DynamoDB, finds records with `slack_channel_id` where sandbox no longer exists, returns WARN listing stale channels.

---

## External Dependencies — Slack API

### Slack API surface for Phase 63

| Method | Rate Limit | Notes |
|--------|-----------|-------|
| `auth.test` | Special — "hundreds of req/min" | Use for token validation in `km slack init`, `km doctor`. |
| `chat.postMessage` | Special — "1 msg/sec per channel, burst allowed" | Primary post action. `unfurl_links: false`, `unfurl_media: false`. Max text 40 KB. |
| `conversations.create` | Tier 2 — 20+ req/min | Per-sandbox channel creation at `km create`. Called operator-side, not via Lambda. |
| `conversations.inviteShared` | Tier 2 — 20+ req/min | Slack Connect invite. One email/user per request despite array signature. |
| `conversations.archive` | Tier 2 — 20+ req/min | Called at `km destroy` via bridge Lambda. |

**`chat.postMessage` 40 KB limit:** Enforce client-side in `km-slack` before signing. Bridge Lambda should also enforce to prevent abusive payloads.

**`conversations.inviteShared` constraints (HIGH confidence from official docs):**
- Creates a Slack Connect invite to an external workspace user via email.
- `external_limited: true` (default) — invitee can only send messages, cannot invite others, export, or change settings.
- Only one email per call.
- Channel must NOT be archived, an MPDM, DM, or mandatory channel.
- Workspace settings may restrict whether apps can send invitations.
- The channel becomes a Slack Connect channel upon use of this method.

**Slack rate limit on `chat.postMessage`:** 1 message/second per channel with burst. For Phase 63, the hook's cooldown mechanism (default 60s from Phase 62) prevents runaway posting well within this limit. The bridge Lambda is single-shot (no Lambda-side retry); `km-slack` retries on 5xx/network, not on 429. On 429, bridge returns HTTP 503 + `Retry-After` header; `km-slack` should respect it in the retry loop.

### Slack Go SDK vs. thin HTTP client

No Slack SDK (`slack-go/slack`) is in `go.mod`. The codebase consistently uses thin HTTP wrapper clients (see `pkg/aws/` pattern: each service gets a narrow interface + AWS SDK). Recommendation: write a thin HTTP client in `pkg/slack/` that calls the Slack Web API directly over HTTPS using `net/http`. This avoids a new dependency and matches the codebase's style. The API surface needed is small: `auth.test`, `chat.postMessage`, `conversations.create`, `conversations.inviteShared`, `conversations.archive`.

Slack Web API base URL: `https://slack.com/api/{method}`, POST, `application/x-www-form-urlencoded` or `application/json` for some methods. Bearer token in `Authorization: Bearer {bot-token}` header.

### Lambda Function URL (auth=NONE) — new pattern for this codebase

No existing `aws_lambda_function_url` resource exists in any Terraform module in this codebase (confirmed by exhaustive grep of `infra/modules/`). Phase 63 introduces it.

**Terraform resource:**
```hcl
resource "aws_lambda_function_url" "slack_bridge" {
  function_name      = aws_lambda_function.slack_bridge.function_name
  authorization_type = "NONE"
}
```

**Security model:** `auth_type = NONE` means Lambda is publicly reachable. Application-layer Ed25519 + replay protection provide auth. This matches the CONTEXT.md design and the spec's "same model as existing operator email Lambda" claim — though in practice the email Lambda uses SES-triggered invocation (not a Function URL). The bridge Lambda is the first *publicly-addressable* Lambda in this codebase.

**IAM note:** With `auth_type = NONE`, no `aws_lambda_permission` resource for the URL is needed (AWS handles it automatically). A `aws_lambda_permission` would be needed for SES/EventBridge triggers, not for Function URL invocations.

---

## Architecture Patterns

### Recommended Package Structure

```
cmd/
  km-slack/
    main.go               # CLI binary: km-slack post ...
  km-slack-bridge/
    main.go               # Lambda handler entry point
pkg/
  slack/
    client.go             # thin Slack Web API HTTP client
    payload.go            # envelope construction, canonical JSON, signing
    payload_test.go
    client_test.go
    bridge/
      handler.go          # Lambda handler: verify + dispatch
      handler_test.go
infra/
  modules/
    lambda-slack-bridge/
      v1.0.0/
        main.tf           # IAM role, Lambda function, Function URL
        variables.tf
        outputs.tf        # function_url output
  live/
    use1/
      lambda-slack-bridge/
        terragrunt.hcl
internal/
  app/
    cmd/
      slack.go            # km slack init/test/status
```

### Pattern: Compiler env var emission

**Current `NotifyEnv` population** (userdata.go ~line 2425):
```go
notifyEnv := map[string]string{}
if p.Spec.CLI.NotifyOnPermission { notifyEnv["KM_NOTIFY_ON_PERMISSION"] = "1" }
// ...
params.NotifyEnv = notifyEnv
```

Phase 63 extends this block. `KM_NOTIFY_EMAIL_ENABLED` is only emitted when `notifyEmailEnabled` is explicitly set (the field is `bool` with default true, but in Go `omitempty` on bool means the zero value `false` is omitted; the default `true` is NOT zero — the compiler must explicitly check whether the field was set in the profile YAML vs. left at Go default). Recommendation: make `notifyEmailEnabled` a `*bool` (pointer) in `CLISpec` so unset is distinguishable from `false`.

### Pattern: Sidecar binary in user-data

All existing sidecar binaries are downloaded at provision time (lines 459–469). `km-slack` follows the same pattern:
```bash
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-slack" /opt/km/bin/km-slack
chmod +x /opt/km/bin/km-slack
```
The binary is arm64 (sandbox EC2 runs arm64; see Makefile `GOARCH := amd64` — NOTE: Makefile currently uses amd64 for EC2 sidecars but arm64 for Lambdas; verify which GOARCH the EC2 sidecar line uses before writing the Makefile target for `km-slack`).

### Pattern: Bridge Lambda verification flow

The bridge must verify signatures. The public key lookup uses `FetchPublicKey(ctx, dynamoClient, "km-identities", senderID)` from `pkg/aws/identity.go`. The Lambda needs `dynamodb:GetItem` on `km-identities`, NOT `ssm:GetParameter` for public keys.

Canonical JSON for signing: sorted keys, no whitespace. Use `encoding/json` with a map for deterministic serialization — `json.Marshal(map[string]interface{}{...})` does NOT guarantee key order. Use a custom canonical serializer or the `encoding/json` + sorted-key approach. Recommendation: define a fixed Go struct for the envelope; `json.Marshal` of a Go struct uses field order (defined at compile time), which is deterministic. Alternatively, build a `map[string]interface{}` with sorted keys using `sort.Strings`. The `km-slack` binary and bridge Lambda must use identical canonicalization.

### Pattern: Nonce DynamoDB table

Model after `dynamodb-budget` or `dynamodb-identities` TTL pattern:
```hcl
resource "aws_dynamodb_table" "nonces" {
  name         = "km-slack-bridge-nonces"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "nonce"
  ttl { attribute_name = "ttl_expiry"; enabled = true }
}
```
`ttl_expiry` = Unix epoch seconds (Number type). Set to `now + 600` (10 min). DynamoDB TTL is eventually consistent (items may survive a few minutes past TTL) — the ±5-min timestamp window already provides replay protection; the nonce table is defense-in-depth.

**Conditional write for nonce:** Use `ConditionExpression: "attribute_not_exists(nonce)"` in `PutItem`. If the condition fails (nonce exists), DynamoDB returns `ConditionalCheckFailedException` → return 401.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead |
|---------|-------------|-------------|
| Ed25519 signature verification | Custom crypto | `crypto/ed25519` from stdlib (already used in `pkg/aws/identity.go`) |
| Canonical JSON | Custom serializer | Fixed struct with `encoding/json` (struct field order is deterministic); or explicit key sort |
| Slack API HTTP calls | Third-party SDK | Thin `net/http` client in `pkg/slack/client.go` — only 5 methods needed |
| Replay protection | Custom time-window logic | DynamoDB conditional write with TTL (matches `dynamodb-identities` pattern) |
| Lambda IAM KMS grant stability | Manual policy updates | `replace_triggered_by = [aws_iam_role.slack_bridge]` on Lambda resource (CLAUDE.md requirement) |
| Public key retrieval | SSM paths | `FetchPublicKey()` from `pkg/aws/identity.go` — DynamoDB is the source of truth |

---

## Common Pitfalls

### Pitfall 1: Wrong public key source for bridge Lambda

**What goes wrong:** Following the spec's reference to `/sandbox/{id}/signing-public-key` SSM path. That path doesn't exist; the SSM `GetParameter` call returns a 404, bridge returns 500, all sandbox Slack posts fail.

**Root cause:** The CONTEXT.md spec describes a public-key SSM path that was designed aspirationally but never implemented. The actual store is DynamoDB `km-identities.public_key`.

**How to avoid:** Use `pkg/aws/FetchPublicKey()` in the bridge Lambda. Add `dynamodb:GetItem` on `km-identities` to the Lambda IAM role instead of `ssm:GetParameter` on public-key paths.

### Pitfall 2: Slack channel names with dots or uppercase

**What goes wrong:** Operator uses `--alias "research.team-a"` → channel `#sb-research.team-a` — Slack rejects with `invalid_name` (dots not allowed in channel names).

**Root cause:** Sandbox `--alias` is free-form. Slack channel names allow only lowercase letters, numbers, hyphens, underscores (no dots, no spaces, no uppercase).

**How to avoid:** In `km create`, before calling `conversations.create`, sanitize the alias: replace dots and spaces with hyphens, lowercase. Document the transformation. Cap channel name at 80 chars. Validate and warn if the sanitized name differs from the alias.

**Channel name collision:** If two sandboxes use the same alias at different times, `#sb-{alias}` will conflict. `conversations.create` returns `name_taken` error — handle with a suffixed fallback (`#sb-{alias}-{id-suffix}`) or abort `km create` with a clear error.

### Pitfall 3: Lambda Function URL — first in codebase, no precedent

**What goes wrong:** Terraform `aws_lambda_function_url` resource not configured correctly. Missing `authorization_type = "NONE"` or missing CORS block causes Lambda URL to behave unexpectedly.

**Root cause:** No existing example in this repo. The `replace_triggered_by` pattern on IAM role must also be applied — creating the Function URL after role replacement without this trigger can result in stale Lambda environment KMS grants.

**How to avoid:** Add `aws_lambda_function_url` after the `aws_lambda_function` in the same Terraform module. Include `replace_triggered_by = [aws_iam_role.slack_bridge]` on the `aws_lambda_function` resource (per CLAUDE.md). Output the URL and store in SSM at `/km/slack/bridge-url`.

### Pitfall 4: `notifyEmailEnabled` — bool vs *bool ambiguity

**What goes wrong:** `notifyEmailEnabled` is a `bool` field with default `true`. In Go, an unset bool is `false`, not `true`. If the compiler checks `p.Spec.CLI.NotifyEmailEnabled == true` to emit `KM_NOTIFY_EMAIL_ENABLED=1`, unset profiles will emit `KM_NOTIFY_EMAIL_ENABLED=0`, breaking Phase 62 backward compat.

**Root cause:** Go `bool` zero value is `false`. YAML `omitempty` on bool omits `false` from marshaling. When the field is absent from YAML, the Go struct gets `false` — not `true`.

**How to avoid:** Use `*bool` (pointer to bool) for `NotifyEmailEnabled` in `CLISpec`. Nil pointer = unset = don't emit the env var (hook's `${KM_NOTIFY_EMAIL_ENABLED:-1}` default takes effect). Non-nil `true` = emit `=1`. Non-nil `false` = emit `=0`. Same pattern applies to `NotifySlackEnabled`.

### Pitfall 5: ValidationError has no severity field

**What goes wrong:** CONTEXT.md distinguishes "validation error" (km validate exits 1) from "validation warning" (km validate prints but continues). Current `ValidationError` struct has only `Path` and `Message` — no severity. Implementing "warning" rules as blocking errors breaks the no-op cases.

**Root cause:** ValidationError was designed as error-only; warnings weren't needed until Phase 63.

**How to avoid:** Add `IsWarning bool` to `ValidationError`. Update `km validate`'s `validateFile()` to separate warnings from errors: print warnings to stderr with "WARN:" prefix but don't count them toward `anyFailed`. Update all existing "warn" comments in validate.go to actually use `IsWarning: true` (consistent across codebase).

### Pitfall 6: `conversations.inviteShared` is Slack Connect — requires Pro workspace

**What goes wrong:** `conversations.inviteShared` fails if the klankermaker.ai Slack workspace is on a free plan. Slack Connect (external channel sharing) requires Pro or higher.

**Root cause:** Slack Connect is a paid-tier feature.

**How to avoid:** CONTEXT.md establishes the klankermaker.ai workspace as Pro — this is an operator setup prerequisite, documented in `docs/slack-notifications.md`. The `km slack init` command should call `auth.test` to validate token before attempting anything; the workspace tier won't be surfaced by `auth.test` alone but a failed `conversations.inviteShared` with `not_allowed_token_type` or `org_login_required` is a clear signal.

### Pitfall 7: Sidecar binary GOARCH — arm64 for Lambda, check for EC2

**What goes wrong:** Building `km-slack` binary for wrong architecture. Lambda Lambdas are arm64 (see `buildLambdaZips()` setting `GOARCH=arm64`). EC2 sidecar binaries are built with `GOARCH := amd64` in the Makefile. If `km-slack` (a sandbox-side binary) uses arm64, it won't run on amd64 EC2 instances.

**Root cause:** The Makefile `GOARCH := amd64` applies to EC2 sidecars. Lambda binaries use arm64 (`architectures = ["arm64"]`). `km-slack` is a sandbox binary (EC2), so it should be amd64.

**How to avoid:** Add `km-slack` to the EC2 sidecar `build-sidecars` target with `GOARCH=amd64`. Confirm by checking the existing dns-proxy/http-proxy entries in `Makefile`.

### Pitfall 8: DynamoDB nonce TTL — eventually consistent, don't rely solely on TTL

**What goes wrong:** DynamoDB TTL deletion is eventually consistent — items can survive 48h past their TTL. Relying on TTL alone for replay protection means old nonces may not be deleted in time.

**Root cause:** DynamoDB TTL is a background process, not real-time.

**How to avoid:** The conditional write (`attribute_not_exists(nonce)`) is the actual replay protection mechanism; TTL only cleans up old entries. The timestamp ±5 min window independently rejects stale requests. Use both defense layers as designed.

### Pitfall 9: `km init --lambdas` doesn't deploy the bridge Lambda

**What goes wrong:** Operator runs `km init --lambdas` after code change, expects bridge Lambda to be updated, but `km init --lambdas` only rebuilds ZIPs and does NOT run Terraform apply. The Lambda function code only updates on Terraform apply.

**Root cause:** `km init --lambdas` calls `buildLambdaZips()` + `forceLambdaColdStart()`. It does not call `runner.Apply()` for any Lambda module.

**How to avoid:** Document clearly that deploying/updating the bridge Lambda requires either: (a) running `km slack init` (which should call `runner.Apply()`), or (b) running full `km init` (if added to `regionalModules()`). The `km init --lambdas` flag path only rebuilds the ZIP; a subsequent `km slack init` or full `km init` is needed for Terraform to pick up the new ZIP via `source_code_hash = filebase64sha256(var.lambda_zip_path)`.

---

## Code Examples

### Canonical JSON signing (Go)

```go
// Source: pkg/aws/identity.go VerifyEmailSignature() pattern (adapted)
// For Slack envelope: build a struct, marshal with encoding/json.
// Struct field order in Go JSON serialization is deterministic (alphabetical by json tag).

type SlackEnvelope struct {
    Action    string `json:"action"`
    Body      string `json:"body"`
    Channel   string `json:"channel"`
    Nonce     string `json:"nonce"`
    SenderID  string `json:"sender_id"`
    Subject   string `json:"subject"`
    ThreadTS  string `json:"thread_ts"`
    Timestamp int64  `json:"timestamp"`
    Version   int    `json:"version"`
}
// Note: field tags MUST be sorted alphabetically for canonical JSON.
// json.Marshal of a struct emits fields in tag-order, which here = sorted.
canonical, _ := json.Marshal(env)
sig := ed25519.Sign(privKey, canonical)
```

### Fetching private key from SSM (pattern from pkg/aws/identity.go:554)

```go
// signingKeyPath returns "/sandbox/{id}/signing-key"
keyPath := fmt.Sprintf("/sandbox/%s/signing-key", senderID)
ssmOut, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
    Name:           aws.String(keyPath),
    WithDecryption: aws.Bool(true),
})
privKeyB64 := *ssmOut.Parameter.Value
privBytes, _ := base64.StdEncoding.DecodeString(privKeyB64)
// privBytes is 64-byte Ed25519 seed||public; first 32 bytes is seed
privKey := ed25519.NewKeyFromSeed(privBytes[:32])
```

### Fetching public key from DynamoDB (existing function)

```go
// Source: pkg/aws/identity.go:231
record, err := kmaws.FetchPublicKey(ctx, identityClient, "km-identities", senderID)
pubKeyBytes, _ := base64.StdEncoding.DecodeString(record.PublicKeyB64)
pubKey := ed25519.PublicKey(pubKeyBytes)
ok := ed25519.Verify(pubKey, canonical, sig)
```

### DynamoDB conditional nonce write

```go
// Fail closed: DynamoDB unavailable → 500 (not 200)
_, err = dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
    TableName: aws.String("km-slack-bridge-nonces"),
    Item: map[string]types.AttributeValue{
        "nonce":      &types.AttributeValueMemberS{Value: nonce},
        "ttl_expiry": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", time.Now().Unix()+600)},
    },
    ConditionExpression: aws.String("attribute_not_exists(nonce)"),
})
if err != nil {
    var ccf *types.ConditionalCheckFailedException
    if errors.As(err, &ccf) { return 401, "replayed nonce" }
    return 500, "nonce check unavailable"
}
```

### Lambda Function URL Terraform (new pattern)

```hcl
resource "aws_lambda_function_url" "slack_bridge" {
  function_name      = aws_lambda_function.slack_bridge.function_name
  authorization_type = "NONE"

  cors {
    allow_origins = ["*"]
    allow_methods = ["POST"]
    allow_headers = ["content-type", "x-km-sender-id", "x-km-signature"]
  }
}

output "function_url" {
  value = aws_lambda_function_url.slack_bridge.function_url
}
```

---

## Implementation Sequencing

The suggested wave decomposition in CONTEXT.md is sound. One refinement:

**Wave 1 (parallelizable foundations):**
- 1A: Profile schema (`types.go`, schema JSON, `ValidateSemantic` rules). Requires `*bool` decision for `notifyEmailEnabled` / `notifySlackEnabled`. Requires `IsWarning` field decision on `ValidationError`.
- 1B: `pkg/slack/payload.go` + `pkg/slack/client.go`. Pure library, no consumers yet. No external dependencies (thin HTTP client).
- 1C: `pkg/slack/bridge/handler.go` skeleton. Verification + dispatch logic, mocked Slack client. Unit tests pass without Lambda deploy.

**Wave 2 (integration, depends on Wave 1):**
- 2A: Compiler extension (`userdata.go` heredoc + env map). Depends on 1A.
- 2B: `cmd/km-slack/main.go`. Depends on 1B. Add to Makefile `build-sidecars` + S3 upload. Add sidecar download line to user-data.
- 2C: Bridge Lambda module + deploy wiring. Depends on 1C. `infra/modules/lambda-slack-bridge/v1.0.0/`, `infra/live/{region}/lambda-slack-bridge/`, add to `buildLambdaZips()`, add `km_slack_bridge_nonces` DynamoDB table.

**Wave 3 (operator surface, depends on Wave 2):**
- 3A: `km slack init/test/status` (`internal/app/cmd/slack.go`). Depends on 2C.
- 3B: `km create` channel provisioning. Depends on 1A + 2C (bridge URL from SSM).
- 3C: `km destroy` archive flow. Depends on 3A (bridge Lambda must be deployed first to be callable).
- 3D: `km doctor` health checks. Depends on 3A.

**Wave 4 (docs + E2E):**
- 4A: E2E test harness (opt-in CI).
- 4B: `docs/slack-notifications.md`.
- 4C: `CLAUDE.md` updates.

**Sequencing invariant:** 2C (bridge Lambda module) must exist before 3A (`km slack init` runs the apply). 2B (km-slack sidecar) must be in S3 before new sandboxes can use it — but existing sandboxes are unaffected.

---

## Validation Architecture

Nyquist validation is enabled (`workflow.nyquist_validation: true` in `.planning/config.json`).

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (`go test ./...`) |
| Config file | none — standard Go test runner |
| Quick run command | `go test ./pkg/profile/... ./pkg/compiler/... ./pkg/slack/... -count=1` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| SLCK-01 | Schema validation rules (5 new fields, error/warning tiers) | unit | `go test ./pkg/profile/... -run TestSlack -count=1` | ❌ Wave 1 |
| SLCK-01 | Backward compat: existing profiles parse without Slack fields | unit | `go test ./pkg/profile/... -run TestValidate -count=1` | ✅ (existing tests cover) |
| SLCK-02 | Compiler env-var emission for Slack vars | unit | `go test ./pkg/compiler/... -run TestUserDataSlack -count=1` | ❌ Wave 2 |
| SLCK-02 | Hook heredoc extended with Slack path | unit | `go test ./pkg/compiler/... -run TestNotifyHookSlack -count=1` | ❌ Wave 2 |
| SLCK-02 | Phase 62 backward compat: existing profiles still emit email only | unit | `go test ./pkg/compiler/... -run TestUserDataNotify -count=1` | ✅ (existing; extend) |
| SLCK-03 | Payload canonical JSON construction + determinism | unit | `go test ./pkg/slack/... -run TestPayload -count=1` | ❌ Wave 1 |
| SLCK-03 | km-slack HTTP retry behavior (stub server) | unit | `go test ./pkg/slack/... -run TestClient -count=1` | ❌ Wave 1 |
| SLCK-03 | km-slack end-to-end with stub Lambda URL | unit | `go test ./cmd/km-slack/... -count=1` | ❌ Wave 2 |
| SLCK-04 | Bridge: valid sandbox post to own channel | unit | `go test ./pkg/slack/bridge/... -run TestHandler -count=1` | ❌ Wave 1 |
| SLCK-04 | Bridge: channel-mismatch → 403 | unit | `go test ./pkg/slack/bridge/... -run TestChannelMismatch -count=1` | ❌ Wave 1 |
| SLCK-04 | Bridge: stale timestamp → 401 | unit | `go test ./pkg/slack/bridge/... -run TestTimestamp -count=1` | ❌ Wave 1 |
| SLCK-04 | Bridge: replayed nonce → 401 | unit | `go test ./pkg/slack/bridge/... -run TestNonce -count=1` | ❌ Wave 1 |
| SLCK-04 | Bridge: bad signature → 401 | unit | `go test ./pkg/slack/bridge/... -run TestSig -count=1` | ❌ Wave 1 |
| SLCK-04 | Bridge: Slack 429 → 503 + Retry-After | unit | `go test ./pkg/slack/bridge/... -run TestSlack429 -count=1` | ❌ Wave 1 |
| SLCK-05 | km slack init SSM writes + idempotence | unit | `go test ./internal/app/cmd/... -run TestSlackInit -count=1` | ❌ Wave 3 |
| SLCK-06 | km create with Slack enabled: channel ID in DynamoDB + env file | unit | `go test ./internal/app/cmd/... -run TestCreateSlack -count=1` | ❌ Wave 3 |
| SLCK-06 | km create: channel creation failure → infra rollback | unit | `go test ./internal/app/cmd/... -run TestCreateSlackFailure -count=1` | ❌ Wave 3 |
| SLCK-07 | km destroy: final post + archive sequence | unit | `go test ./internal/app/cmd/... -run TestDestroySlack -count=1` | ❌ Wave 3 |
| SLCK-07 | km destroy: archive failure → warning, destroy continues | unit | `go test ./internal/app/cmd/... -run TestDestroySlackArchiveFailure -count=1` | ❌ Wave 3 |
| SLCK-08 | km doctor: Slack token expired → WARN | unit | `go test ./internal/app/cmd/... -run TestDoctorSlack -count=1` | ❌ Wave 3 |
| Hook script | KM_NOTIFY_SLACK_ENABLED=1 + email=0 → only km-slack invoked | integration (shell) | `go test ./pkg/compiler/... -run TestNotifyHookScript -count=1` | ❌ Wave 2 (extend existing harness) |
| Hook script | both fail → cooldown not updated, hook exits 0 | integration (shell) | `go test ./pkg/compiler/... -run TestNotifyHookBothFail -count=1` | ❌ Wave 2 |
| E2E | Real Slack workspace: shared mode idle notification | e2e (manual) | `KM_SLACK_E2E=1 go test ./... -run TestE2ESlack -count=1` | ❌ Wave 4 |

### Sampling Rate

- **Per task commit:** `go test ./pkg/profile/... ./pkg/slack/... -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/slack/` — new package, no files exist
- [ ] `pkg/slack/bridge/` — new package, no files exist
- [ ] `cmd/km-slack/` — new directory, no files exist
- [ ] `cmd/km-slack-bridge/` — new directory, no files exist
- [ ] `infra/modules/lambda-slack-bridge/v1.0.0/` — new module, no files exist
- [ ] `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` — new, no file exists
- [ ] Bridge nonce table Terraform module — no files exist

---

## Open Questions

1. **`ValidationError` warning semantics**
   - What we know: `ValidationError` struct has no severity field. The `km validate` command treats all `ValidationError` results as blocking errors (exit 1).
   - What's unclear: Should "validation warning" conditions (no-op combinations) be blocking or non-blocking? The Phase 62 eBPF+ECS rule in the code comment says "warn" but emits a `ValidationError` that blocks.
   - Recommendation: Add `IsWarning bool` to `ValidationError`. Update `validateFile()` to print WARN-prefixed messages but not count them toward `anyFailed`. Apply retroactively to the eBPF+ECS rule for consistency. This is a small additive change with low risk.

2. **`km slack init` Terraform apply approach**
   - What we know: `km slack init` must deploy the bridge Lambda. `km init --lambdas` only rebuilds ZIPs. Terraform apply is needed.
   - What's unclear: Should `km slack init` call `runner.Apply()` directly on the bridge Lambda module, OR should the bridge Lambda be added to `regionalModules()` so full `km init` deploys it?
   - Recommendation: Add to `regionalModules()` for consistency (all platform Lambdas deploy via `km init`). But also have `km slack init` check if the Lambda exists and apply the module if it doesn't. This gives operators two paths: initial setup via `km slack init --deploy` and standard infra management via `km init`.

3. **`*bool` vs `bool` for `notifyEmailEnabled` and `notifySlackEnabled`**
   - What we know: Go `bool` zero value is `false`. `notifyEmailEnabled` default is `true`. Unset must be distinguishable from explicitly `false`.
   - Recommendation: Use `*bool` for both `notifyEmailEnabled` and `notifySlackEnabled` in `CLISpec`. Nil = unset. The `omitempty` tag on `*bool` omits the field when nil. The compiler emits env vars only when non-nil. This is additive and doesn't change existing fields.

4. **Channel name collision handling at `km create`**
   - What we know: `conversations.create` returns `name_taken` if channel exists. Slack channel names must be lowercase, no dots.
   - What's unclear: Should `km create` fail hard on collision or use a suffixed fallback (`#sb-{alias}-{hex}`)?
   - Recommendation: Fail hard with a clear error message on collision. Document that aliases used with `notifySlackPerSandbox: true` should be unique. The operator can use `notifySlackChannelOverride` if they want to reuse an existing channel.

5. **Bridge Lambda deployment for `km destroy` archive at `km destroy` time**
   - What we know: `km destroy` should call `conversations.archive` via bridge Lambda. The bridge Lambda URL is at SSM `/km/slack/bridge-url`.
   - What's unclear: What happens if `km destroy` runs on a sandbox with `notifySlackPerSandbox: true` but the bridge Lambda is not deployed (e.g., `km slack init` was never run)?
   - Recommendation: `km destroy` should check if `/km/slack/bridge-url` is populated before attempting the archive. If missing, log a warning and skip the Slack archive (don't fail the destroy).

---

## Sources

### Primary (HIGH confidence — codebase)

- `pkg/compiler/userdata.go` — Phase 62 hook heredoc (lines 354–448), sidecar download block, NotifyEnv emission
- `pkg/profile/types.go` — `CLISpec` struct (lines 354–395)
- `pkg/profile/validate.go` — `ValidateSemantic()` pattern (lines 209–268)
- `pkg/aws/identity.go` — `signingKeyPath()`, `FetchPublicKey()`, `VerifyEmailSignature()`, `SendSignedEmail()`
- `pkg/aws/metadata.go` — `SandboxMetadata` struct
- `pkg/aws/sandbox_dynamo.go` — `WriteSandboxMetadataDynamo()`, DynamoDB column conventions
- `internal/app/cmd/init.go` — `buildLambdaZips()`, `regionalModules()`, operator identity provisioning
- `infra/modules/email-handler/v1.0.0/main.tf` — Lambda module template (IAM, function, `replace_triggered_by`)
- `infra/live/use1/email-handler/terragrunt.hcl` — Terragrunt live wiring pattern
- `internal/app/cmd/doctor.go` — `CheckResult`, check function pattern
- `Makefile` — Sidecar build/upload targets
- `pkg/compiler/notify_hook_script_test.go` — Hook script test harness pattern

### Secondary (MEDIUM confidence — official Slack docs)

- Slack docs `conversations.create` — Tier 2 (20+ req/min)
- Slack docs `conversations.archive` — Tier 2 (20+ req/min)
- Slack docs `conversations.inviteShared` — Tier 2 (20+ req/min); Slack Connect; one email per call; `external_limited: true` default
- Slack docs `auth.test` — Special tier ("hundreds of req/min")
- Slack docs `chat.postMessage` — Special tier (1 msg/sec per channel, burst allowed, 40 KB text limit)

### Tertiary (LOW confidence — needs validation)

- Slack Pro workspace requirement for Slack Connect (`conversations.inviteShared`) — confirmed from Slack Connect documentation but workspace-specific settings may further restrict.

---

## Metadata

**Confidence breakdown:**
- Codebase inventory (hook heredoc, signing pattern, Lambda deploy): HIGH — direct code inspection
- Slack API rate limits and constraints: MEDIUM — official Slack docs
- Lambda Function URL behavior (first in codebase): HIGH — Terraform AWS provider documentation is standard
- Pitfalls: HIGH — derived from direct codebase contradictions with spec

**Research date:** 2026-04-28
**Valid until:** 2026-05-28 (stable codebase; Slack API tier changes possible but unlikely at Tier 2)
