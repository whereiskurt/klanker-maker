# Slack Notifications Guide

Phase 63 extends the Phase 62 operator-notify hook with parallel Slack delivery.
The same `Notification` (permission prompt) and `Stop` (idle) events that trigger
email also post to a Slack channel — shared or per-sandbox — via an Ed25519-signed
bridge Lambda. The bot token never leaves AWS; sandboxes call the bridge with
signed payloads and the Lambda forwards to the Slack Web API.

## Table of Contents

1. [Overview](#overview)
2. [Channel Modes](#channel-modes)
3. [Prerequisites](#prerequisites)
4. [One-Time Setup](#one-time-setup)
5. [Profile Fields](#profile-fields)
6. [Validation Rules](#validation-rules)
7. [Example Profiles](#example-profiles)
8. [Architecture](#architecture)
9. [SSM Parameters Reference](#ssm-parameters-reference)
10. [Sandbox Environment Variables](#sandbox-environment-variables)
11. [Bot Token Rotation](#bot-token-rotation)
12. [Troubleshooting](#troubleshooting)
13. [Security Model](#security-model)
14. [See Also](#see-also)

---

## Overview

Phase 63 adds Slack delivery alongside the existing email notification path:

- **Same triggers:** `Notification` (Claude Code permission prompt) and `Stop` (idle timeout) events, gated by `notifyOnPermission` and `notifyOnIdle`.
- **Parallel channels:** email and Slack run simultaneously unless you explicitly disable one via `notifyEmailEnabled: false`.
- **Signed payloads:** the `km-slack` binary on the sandbox constructs an Ed25519-signed envelope and POSTs it to the bridge Lambda Function URL. The Lambda verifies the signature using the sandbox's public key from DynamoDB before forwarding to the Slack Web API.
- **Bot token isolation:** the Slack bot token is stored in SSM as a SecureString (KMS-encrypted). Only the bridge Lambda and the operator (via `km slack init` / `km slack status`) can read it.
- **Operator channels via Slack Connect:** klankermaker.ai owns the Slack workspace. The operator is invited to the notification channel(s) via `conversations.inviteShared` (Slack Connect). The operator accepts the invite from their own Slack workspace — no workspace credential sharing required.

---

## Channel Modes

| Mode | When | Channel name | Lifecycle |
|------|------|-------------|-----------|
| **Shared (default)** | `notifySlackEnabled: true`, neither per-sandbox nor override set | `#km-notifications` (or the name set during `km slack init`) | Permanent; shared across all sandboxes |
| **Per-sandbox** | `notifySlackPerSandbox: true` | `#sb-{sandbox-id}` (sanitized) | Created at `km create`; archived at `km destroy` when `slackArchiveOnDestroy: true` |
| **Override** | `notifySlackChannelOverride: "C..."` | Any existing channel the bot has been invited to | Unmanaged — operator is responsible for channel lifecycle |

Modes are mutually exclusive: `notifySlackPerSandbox: true` and `notifySlackChannelOverride: <set>` at the same time is a validation error.

---

## Prerequisites

1. **Pro Slack workspace** at klankermaker.ai (or a test workspace). Slack Connect (`conversations.inviteShared`) requires Pro tier or higher; the free tier returns `not_allowed_token_type`.

2. **Custom Slack App** installed in the workspace with these bot scopes:
   - `chat:write` — post messages
   - `channels:manage` — create and archive public channels
   - `conversations.connect:write` — send Slack Connect invites
   - `groups:write` — create and archive private channels

3. **Bot token** (`xoxb-...`) captured from the Slack App's OAuth & Permissions page.

4. **Operator's separate Slack workspace** able to receive Slack Connect invites (any tier). This is where the operator sees notifications.

5. **AWS account** with the platform initialized (`km init` run at least once) and SSM, DynamoDB, and Lambda accessible in the primary region.

---

## One-Time Setup

Run these once per AWS account/region before creating sandboxes with Slack notifications:

```bash
# Step 1: Always rebuild km after editing CLI source.
# Memory: feedback_rebuild_km — use make build, not bare go build.
make build

# Step 2: Upload km-slack sidecar binary to S3 (management Lambda needs it to
# provision new sandboxes). Required after schema-driven changes ship.
km init --sidecars

# Step 3: Deploy bridge Lambda and DynamoDB nonce table.
km init

# Step 4: Bootstrap Slack integration (interactive, or pass flags to skip prompts).
km slack init \
  --bot-token "$SLACK_BOT_TOKEN" \
  --invite-email "operator@example.com" \
  --shared-channel "km-notifications"
```

km slack init \
  --bot-token "xoxb-REDACTED-EXAMPLE-TOKEN" \
  --invite-email "kurt.hundeck@greenhouse.io" \
  --shared-channel "km-notifications"

`km slack init` does the following:
1. Validates the bot token via `auth.test`.
2. Writes the token to SSM `/km/slack/bot-token` (SecureString, KMS-encrypted).
3. Creates the shared channel via `conversations.create`.
4. Sends a Slack Connect invite to the provided email via `conversations.inviteShared`.
5. Stores invite email in `/km/slack/invite-email`.
6. Applies the `dynamodb-slack-nonces` and `lambda-slack-bridge` Terragrunt modules.
7. Reads the bridge Function URL from Terraform output and stores it in `/km/slack/bridge-url`.

After init, check configuration:

```bash
km slack status
```

Expected output shows five SSM paths populated (none `(unset)`):

```
/km/slack/workspace                           {"team_id":"T...","team_name":"..."}
/km/slack/shared-channel-id                   C01ABC123
/km/slack/invite-email                        operator@example.com
/km/slack/bridge-url                          https://....lambda-url.us-east-1.on.aws/
/km/slack/last-test-timestamp                 (unset)
```

Then accept the Slack Connect invite: open your email (`operator@example.com`), click the invite link, and accept it from your separate Slack workspace. The `#km-notifications` channel will appear there.

Run an end-to-end smoke test:

```bash
km slack test
```

If successful, `#km-notifications` shows: "If you see this, the bridge is wired."

---

## Profile Fields

All new fields are under `spec.cli`. All are optional with the defaults shown.

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `notifyEmailEnabled` | `bool*` | `true` | Phase 62 backward compat: set `false` to skip email dispatch when Slack is on. Omitting the field preserves Phase 62 behavior (email always fires). |
| `notifySlackEnabled` | `bool*` | `false` | Enable Slack delivery for events already gated by `notifyOnPermission` / `notifyOnIdle`. |
| `notifySlackPerSandbox` | `bool` | `false` | Create `#sb-{sandbox-id}` at `km create`; archive at `km destroy`. Ignored when `notifySlackEnabled` is false. |
| `notifySlackChannelOverride` | `string` | `""` | Hard-pin notifications to an existing Slack channel ID (format: `^C[A-Z0-9]+$`). Overrides both shared and per-sandbox modes. The bot must be a member. |
| `slackArchiveOnDestroy` | `bool*` | `true` | Per-sandbox channels only. Set `false` to preserve the channel and its history after `km destroy`. |

`bool*` indicates the field is a pointer (`*bool`) in the schema, allowing three states: unset (nil → default), `true`, `false`. Omitting the field is different from `false` for `notifyEmailEnabled` (omit = email on; `false` = email off).

---

## Validation Rules

`km validate` enforces these rules. Errors exit 1; warnings exit 0 with a `WARN:` prefix.

| Rule | Condition | Severity | Message |
|------|-----------|----------|---------|
| Mutual exclusion | `notifySlackPerSandbox: true` AND `notifySlackChannelOverride` set | **Error** | "notifySlackPerSandbox and notifySlackChannelOverride are mutually exclusive" |
| Dead per-sandbox | `notifySlackPerSandbox: true` AND `notifySlackEnabled: false` | Warning | "notifySlackPerSandbox has no effect when notifySlackEnabled is false" |
| Dead archive | `slackArchiveOnDestroy` set AND `notifySlackPerSandbox: false` | Warning | "slackArchiveOnDestroy has no effect unless notifySlackPerSandbox is true" |
| Channel ID format | `notifySlackChannelOverride` does not match `^C[A-Z0-9]+$` | **Error** | "notifySlackChannelOverride must match C[A-Z0-9]+" |
| No delivery channel | `notifySlackEnabled: true` AND all three mode fields absent/false/empty AND shared channel not provisioned | Warning | "notifySlackEnabled is true but no delivery channel configured" |

---

## Example Profiles

### Shared mode (default): notify all sandboxes to one channel

```yaml
spec:
  cli:
    notifyOnIdle: true
    notifyCooldownSeconds: 60
    notifyEmailEnabled: false   # email off; Slack only
    notifySlackEnabled: true
```

### Per-sandbox channel with archive on destroy

```yaml
spec:
  cli:
    notifyOnPermission: true
    notifyOnIdle: true
    notifyCooldownSeconds: 0
    notifyEmailEnabled: false
    notifySlackEnabled: true
    notifySlackPerSandbox: true
    slackArchiveOnDestroy: true   # default; explicit here for clarity
```

### Override mode: pin to an existing channel

```yaml
spec:
  cli:
    notifyOnIdle: true
    notifySlackEnabled: true
    notifySlackChannelOverride: "C01ABC1234DEF"   # bot must be invited
```

---

## Architecture

```
sandbox EC2 instance
  │
  │  km-notify-hook (bash, fires on Notification/Stop events)
  │    │
  │    └── /opt/km/bin/km-slack  (Go binary, Ed25519 key from /sandbox/{id}/signing-key)
  │          │
  │          │  POST https://{function-url}/  (JSON envelope + X-KM-Signature header)
  │          │  Retry on 5xx: 1s → 2s → 4s backoff
  │          ▼
  │        km-slack-bridge Lambda (Function URL, no auth URL — signature is the auth)
  │          │
  │          │  1. Parse envelope
  │          │  2. Verify timestamp (±5 min window)
  │          │  3. Check DynamoDB nonce table (replay prevention)
  │          │  4. Fetch sender public key from DynamoDB km-identities
  │          │  5. Verify Ed25519 signature
  │          │  6. Assert channel ownership (sandbox can only post to its own channel)
  │          │  7. Assert action authorization (sandbox: post only; operator: post+archive+test)
  │          │  8. Fetch bot token from SSM SecureString (15-min in-memory cache)
  │          │  9. POST to Slack Web API (chat.postMessage or conversations.archive)
  │          │
  │          └── Slack Web API ──► #km-notifications (or #sb-{id})
  │                                    │
  │                                    └── Slack Connect ──► operator's workspace
  │
operator workstation
  km slack init / km slack test / km slack status
    │
    └── SSM: /km/slack/{bot-token,workspace,invite-email,shared-channel-id,bridge-url}
```

---

## SSM Parameters Reference

All parameters live in the primary AWS region. The bot token is a `SecureString` (KMS-encrypted); others are `String`.

| Parameter | Type | Set by | Purpose |
|-----------|------|--------|---------|
| `/km/slack/bot-token` | SecureString | `km slack init` | Slack bot token (`xoxb-...`); read only by bridge Lambda and `km slack init --force` |
| `/km/slack/workspace` | String | `km slack init` | JSON: `{"team_id":"...","team_name":"..."}` |
| `/km/slack/invite-email` | String | `km slack init` | Email address for Slack Connect invites |
| `/km/slack/shared-channel-id` | String | `km slack init` | Slack channel ID (e.g. `C01ABC1234`) for the default shared notification channel |
| `/km/slack/bridge-url` | String | `km slack init` | Lambda Function URL for the bridge |
| `/km/slack/last-test-timestamp` | String | `km slack test` | RFC3339 timestamp of the last successful smoke test |

---

## Sandbox Environment Variables

These are injected into the sandbox at `km create` time by the compiler.

| Variable | Source | Purpose |
|----------|--------|---------|
| `KM_NOTIFY_EMAIL_ENABLED` | profile `spec.cli.notifyEmailEnabled` | `1` or `0`; controls email dispatch in `km-notify-hook` |
| `KM_NOTIFY_SLACK_ENABLED` | profile `spec.cli.notifySlackEnabled` | `1` or `0`; controls whether `km-slack` is invoked |
| `KM_SLACK_CHANNEL_ID` | runtime, resolved at `km create` | Slack channel ID to send notifications to (shared, per-sandbox, or override) |
| `KM_SLACK_BRIDGE_URL` | runtime, from `/km/slack/bridge-url` | Lambda Function URL for the `km-slack` binary to POST to |

Variables are exported in the sandbox's `/etc/profile.d/km.sh` by the user-data script.

Existing sandboxes provisioned before Phase 63 do **not** get these variables retroactively. Destroy and recreate to pick up the km-slack binary and env vars.

---

## Bot Token Rotation

To rotate the bot token end-to-end (revoke compromised → reissue new → propagate to bridge):

### Quick path (recommended): `km slack rotate-token`

The single-command flow validates the new token, persists it to SSM, force-cold-starts the bridge Lambda to invalidate the 15-min in-process token cache, and runs a smoke test.

```bash
# Step 1: In Slack App admin UI (api.slack.com/apps/<your-app-id>):
#         OAuth & Permissions → "Reinstall to Workspace" or "Regenerate Token"
#         Copy the new xoxb-... token.

# Step 2: Rotate locally:
km slack rotate-token --bot-token "$NEW_BOT_TOKEN"
```

Expected output:
```
km slack rotate-token: validated new token (auth.test ok)
km slack rotate-token: persisted token to /km/slack/bot-token
km slack rotate-token: forced km-slack-bridge cold start (cache invalidated)
km slack rotate-token: complete.
```

The smoke test posts a `[rotation]` message to `#km-notifications`. If it fails, the token is still persisted and the cold start may still be in progress — retry `km slack test` after 60 seconds.

### Full revoke-and-rotate cycle (security incident response)

When responding to a leaked or compromised token:

```bash
# 1. REVOKE in Slack App admin UI:
#    api.slack.com/apps → your app → OAuth & Permissions → Revoke Token.
#    All bridge requests using the old token will fail with invalid_auth.

# 2. WAIT for the bridge cache to invalidate. The bridge caches the token
#    in-memory for up to 15 minutes (per-Lambda-instance). Two options:
#
#    a) Wait 15 min, OR
#    b) Force cold-start NOW (preferred for emergencies):
aws lambda update-function-configuration \
    --function-name km-slack-bridge \
    --environment "Variables={TOKEN_ROTATION_TS=$(date +%s)}"

# 3. ISSUE a new token in Slack App admin UI:
#    Same OAuth & Permissions page → Install to Workspace → copy xoxb-... token.

# 4. ROTATE via the single command:
km slack rotate-token --bot-token "$NEW_BOT_TOKEN"

# 5. VERIFY:
km slack test           # expect: km slack test: posted ts=...
km doctor               # expect: ✓ Slack bot token: test message delivered
```

### Manual (legacy) path: `km slack init --force`

The pre-rotate-token workflow still works:
```bash
km slack init --force --bot-token "$NEW_BOT_TOKEN"
```

`--force` overwrites SSM `/km/slack/bot-token`, re-applies the bridge Lambda Terraform, and reuses the existing shared channel. It does NOT force a Lambda cold start — the new token activates after the 15-min cache TTL expires OR the next deployment recycles the Lambda execution environment.

### Cache TTL caveat

The bridge Lambda caches `/km/slack/bot-token` in-process for 15 minutes (see `pkg/slack/bridge/aws_adapters.go` `SSMBotTokenFetcher`). After revoking a token in Slack:
- The old token is in SSM but the Lambda may continue serving requests using the cached value
- If the old token is revoked before the TTL expires, bridge requests fail with `invalid_auth`

Use `km slack rotate-token` (which force-cold-starts the bridge Lambda) to make the new token effective immediately.

Verify rotation:
```bash
km slack test     # expect: km slack test: posted ts=...
km doctor         # expect: ✓ Slack bot token: test message delivered (ts=...)
```

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| `km slack init` returns `not_allowed_token_type` | Workspace is free tier; Slack Connect requires Pro | Upgrade workspace to Pro at api.slack.com/pricing |
| `km create` fails with `name_taken` (per-sandbox mode) | Another channel has the same sanitized name | Use `--alias` to change the sandbox name, or set `notifySlackChannelOverride` to an existing channel |
| Override mode: "bot is not a member" | Bot was not invited to the override channel | In Slack: open the channel → Add people → invite the km bot app |
| Hook fires, no Slack message appears | Bridge Lambda not deployed | Run `km init` then `km slack init`; check `/km/slack/bridge-url` via `km slack status` |
| Hook fires on an existing sandbox, no Slack | Existing sandboxes lack `km-slack` binary and env vars | Run `km destroy` then `km create` to reprovision with the binary |
| `km doctor` reports stale Slack channel | A destroyed sandbox left a non-archived channel | Archive manually in the Slack UI, or remove the `slack_channel_id` attribute from the DynamoDB sandbox record |
| `km slack test` returns 401 | Bot token expired or revoked | Rotate: `km slack rotate-token --bot-token "$NEW_TOKEN"` (or legacy `km slack init --force --bot-token`) |
| Bridge Lambda returns 403 | Signature verification failed (clock skew > 5 min, wrong key) | Ensure sandbox system clock is synced (chronyc / timedatectl); verify `km-identities` DynamoDB record for the sandbox exists |
| Rate limit: bridge returns 503 | Slack returned 429 (rate limit) | Reduce notification frequency via `notifyCooldownSeconds` in the profile |
| Bridge returns 502 with `not_in_channel` | Bot is not a member of the channel | Add the bot to the channel in Slack UI, OR if the bot has `channels:join` scope it can self-rescue: `km slack test` after re-adding should succeed |

---

### Bridge error observability

**CloudWatch log group:** `/aws/lambda/km-slack-bridge`

The bridge Lambda logs all requests and every error path to stderr (shipped to CloudWatch automatically). Each log line is structured text with `key=value` pairs. Key fields:

| Field | What it tells you |
|-------|------------------|
| `action` | `post`, `archive`, or `test` |
| `sender_id` | sandbox ID or `operator` |
| `channel` | Slack channel ID |
| `nonce_prefix` | First 8 chars of the request nonce (for cross-referencing) |
| `step` | Which verification step failed (e.g. `nonce`, `signature`, `token_fetch`, `dispatch`) |
| `slack_error` | Full Slack API error code when the bridge's Slack call fails (e.g. `not_in_channel`, `invalid_auth`) |
| `status` | HTTP status the bridge returned |

**Diagnosing "smoke test fails but rotation appeared to work":**

If `km slack rotate-token` or `km slack test` returns an error after token rotation, use this sequence:

```bash
# 1. Verify the new token is valid (direct Slack API call — no bridge involved)
curl -s -H "Authorization: Bearer $NEW_BOT_TOKEN" https://slack.com/api/auth.test | jq .

# 2. Check CloudWatch for the real error
aws logs tail /aws/lambda/km-slack-bridge --since 5m --format short

# 3. Look for slack_error= in the log output
# Common codes:
#   not_in_channel — bot needs to join the channel (see channels:join scope note below)
#   invalid_auth   — token rotation did not propagate (wait 60s or re-run km slack rotate-token)
#   channel_not_found — channel ID is stale or wrong
```

**channels:join scope and bot channel membership:**

If the bot's Slack App does not have `channels:join` scope, the bot cannot self-join channels it was removed from. During token rotation via "Reinstall to Workspace" in the Slack App admin UI, the bot may lose channel membership for non-shared channels.

Diagnosis: `km slack test` returns `not_in_channel` but `auth.test` succeeds directly.

Fix options:
1. **Add bot to channel manually** — in Slack, open `#km-notifications` → Add people → invite the bot.
2. **Add `channels:join` scope** — in Slack App admin → OAuth & Permissions → add `channels:join` to Bot Token Scopes → reinstall → the bot can self-rescue via `conversations.join`.

**"rotation vs channel" diagnostic rule:** if `auth.test` succeeds but `km slack test` fails with `not_in_channel` or a bridge 502, the rotation itself succeeded — the issue is channel membership, not the token.

---

## Security Model

**Signing:** every sandbox has an Ed25519 key pair. The private key lives in SSM at `/sandbox/{id}/signing-key` (SecureString, accessible only to the sandbox's IAM role). The `km-slack` binary signs the canonical JSON envelope before sending it to the bridge.

**Verification chain (bridge Lambda):**
1. Parse JSON envelope.
2. Reject if `timestamp` is more than ±5 minutes from current UTC (replay protection).
3. Check DynamoDB nonce table: reject if `nonce` was seen within the replay window (dedup).
4. Fetch sender public key from DynamoDB `km-identities` table using `sender_id`.
5. Verify the Ed25519 signature over the canonical JSON bytes.
6. Assert channel ownership: sandbox `sender_id` must match the `channel` field's owning sandbox in DynamoDB `km-sandboxes` (prevents sandbox A posting to sandbox B's channel).
7. Assert action authorization: sandbox identity may only perform `post`; `archive` and `test` require operator identity.

**Bot token isolation:** `/km/slack/bot-token` is a KMS-encrypted SSM SecureString. Only the bridge Lambda's IAM role and the operator's IAM identity (for `km slack init`) have `ssm:GetParameter` permission on it. Sandbox IAM roles cannot read it.

**Slack Connect:** the klankermaker.ai Slack workspace sends invites to the operator's email. The operator accepts from their own workspace. No credentials are shared; Slack Connect is a federated channel sharing protocol.

---

## See Also

- `docs/multi-agent-email.md` — Phase 45 email protocol (signing model reused here)
- `docs/superpowers/specs/2026-04-29-slack-notify-hook-design.md` — full Phase 63 design spec
- `CLAUDE.md` — CLI quick reference, env var and SSM path conventions
- Phase 62 email notification hook (predecessor to Phase 63 Slack delivery)

---

## Inbound chat (Phase 67)

Phase 67 closes the loop opened by Phase 63: Slack messages in a per-sandbox
channel become Claude turns inside that sandbox. The same `#sb-{id}` channel
becomes a bidirectional chat surface.

### Prerequisites

- Phase 63 already configured: bridge Lambda deployed, bot token persisted at
  `/km/slack/bot-token`, shared channel created.
- Slack App has these additional scopes (add via Slack App config → OAuth & Permissions):
  - `channels:history` — read messages in public channels
  - `groups:history` — read messages in private channels
  - `reactions:write` — post the 👀 ACK reaction on accepted messages (Phase 67.1)
  After adding scopes, **reinstall the app** to your workspace.
- Slack signing secret captured. Get it from Slack App config → **Basic
  Information → App Credentials → Signing Secret**.

### One-time setup

```bash
km slack init --force --signing-secret <signing-secret-from-slack-app-config>
```

This persists the secret to `/km/slack/signing-secret` (KMS-encrypted) and
prints the **Events API URL** to paste into the Slack App config.

In Slack App config → **Event Subscriptions** → enable, then paste:

- Request URL: `https://<bridge-fn-url>/events`
- Subscribe to bot event: `message.channels` (and optionally `message.groups`
  for private channels).

Slack will hit the URL with a `url_verification` challenge — the bridge
auto-acks. You should see "Verified" in the Slack App config.

### Per-sandbox enablement

Add to your profile under `spec.cli`:

```yaml
spec:
  cli:
    notifyEmailEnabled: false
    notifySlackEnabled: true
    notifySlackPerSandbox: true
    notifySlackInboundEnabled: true   # Phase 67
    slackArchiveOnDestroy: true
```

`notifySlackInboundEnabled: true` requires `notifySlackEnabled: true` AND
`notifySlackPerSandbox: true`. It is **incompatible with**
`notifySlackChannelOverride` — channel-to-sandbox routing requires 1:1 mapping
in v1.

### Behavior

- After `km create`, the bridge posts: "Sandbox `sb-abc123` ready. Reply here
  or in any thread to give it a task." Reply directly to that message to
  start a fresh Claude session.
- Top-level posts in the channel start new conversations. Each thread is its
  own Claude session (resumed via `claude --resume <session-id>` keyed by
  `(channel_id, thread_ts)`).
- Claude's replies land in the same thread as the user's message.
- `km pause` doesn't drop messages — the SQS queue retains for 14 days. Run
  `km resume` to drain.
- `km destroy` drains in-flight turns up to 30s, posts a final "destroyed"
  message, deletes the SQS queue, and archives the channel.

### ACK reaction (Phase 67.1)

When the bridge accepts an inbound message and successfully writes it to the
sandbox's SQS queue, it adds a 👀 emoji reaction to the originating Slack
message within ~1 second. This gives the user immediate visual confirmation
that the sandbox saw their message — even before the agent boots, before any
paused-sandbox hint posts, before any reply.

The 👀 means "we accepted this for processing" — not just "we received the
HTTP request". If the SQS write fails, no reaction is added (and the
operator sees the failure in CloudWatch logs).

**Required scope:** `reactions:write` (Bot Token Scope, added via Slack App
config → OAuth & Permissions; reinstall the app after adding).

**Bridge env var:** `KM_SLACK_ACK_EMOJI` (default `eyes`). Set on the Lambda
to override the emoji workspace-wide. Always omit the surrounding colons
(`hourglass_flowing_sand`, NOT `:hourglass_flowing_sand:`).

**Deploying:** `make build && km init --lambdas`. Existing inbound-enabled
sandboxes pick up the change automatically — no `km destroy/create` needed
because this is a bridge-only change.

**Failure modes** (all logged at WARN, none block delivery):
- Missing `reactions:write` scope → `events: reaction failed err=missing_scope`. Add scope and reinstall app.
- Bot kicked from channel → `events: reaction failed err=channel_not_found`. Re-invite the bot.
- Slack delivered the same event twice (cold-start replay) → `already_reacted`. Treated as idempotent success — NOT logged at WARN.

### Inspecting

```bash
km status sb-abc123          # queue depth + active thread count
km list --wide               # column shows active threads per sandbox
km doctor                    # three new checks: queue exists, stale queues,
                             # Slack App scopes
```

### Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Slack message disappears, no Claude reply | Bot doesn't have `channels:history` scope | Add scope, reinstall app, run `km slack rotate-token` |
| `url_verification` failed in Slack App config | Signing secret not configured | `km slack init --force --signing-secret <value>` |
| Duplicate Claude responses (one real reply + one `(no recent assistant text)`) | Stop hook posting alongside the poller. The Stop hook gates on `KM_SLACK_THREAD_TS` (set by the poller BEFORE Claude launches); if you see this, the gate is broken or the env file is not loading | Confirm `/etc/profile.d/km-notify-env.sh` is sourced into Claude's bash (`echo $KM_SLACK_THREAD_TS` mid-turn). Older builds gated on `KM_SLACK_INBOUND_REPLY_HANDLED` which is set AFTER Claude exits — `make build && km init --sidecars && km destroy + km create` to pick up the fix. |
| `(no recent assistant text)` appears in Slack instead of Claude's actual reply | Same root cause as above — Stop hook running its fallback because the poller-driven reply was suppressed by an old gate | Same fix as above |
| Duplicate Claude responses (two real replies) | VisibilityTimeout race | Already mitigated (poller extends to 300s); if it persists, check `journalctl -u km-slack-inbound-poller` |
| Poller logs `AWS_REGION not set` and `km-slack post` returns no usable error | systemd's `EnvironmentFile=` directive does NOT accept the shell-style `export VAR=val` lines used by `/etc/profile.d/*.sh`; every entry is silently rejected | The poller writes a parallel systemd-format file `/etc/km/notify.env` (no `export` prefix) and the systemd unit's `EnvironmentFile=` points there. Confirm the file exists on the sandbox and `systemctl show km-slack-inbound-poller -p Environment` lists `AWS_REGION`/`KM_SLACK_*`. If missing, the userdata template is stale — `km destroy + km create` after `make build && km init --sidecars`. |
| Channel-join / channel-topic / pinned-item / other Slack system events trigger Claude turns and burn Bedrock spend | Old deny-list `isBotLoop` filter in the bridge | Bridge now uses allow-list semantics — only `subtype == ""` (real human turn) or `thread_broadcast` reaches SQS. Redeploy the bridge: `cd infra/live/management/lambda-slack-bridge && terragrunt apply` (or wait for the next `km init` cycle). |
| Channel-join from Slack Connect invite acceptance triggers a Claude turn | Same as above | Same fix |
| Claude doesn't continue session across turns | `--resume` interaction with session map | Check `~/.claude/projects/<cwd>/` exists on sandbox; check session_id appears in km-slack-threads DDB row |
| `km destroy` hangs | Drain timeout exceeded — agent run still active at 30s | Drain is bounded; `km destroy` proceeds anyway. Check `journalctl` for "drain: agent-run still active" |
| Claude reply lands on the wrong (previous) message instead of the latest | FIFO ordering vs `KM_SLACK_THREAD_TS` re-use across rapid back-to-back posts | Open: tracked as gap G15 in `.planning/phases/67-.../UAT-2-HANDOFF.md`. Workaround: pause briefly between rapid posts; investigate via `journalctl -u km-slack-inbound-poller \| grep THREAD_TS`. |
| Fresh `--remote` create needs `claude login` inside the sandbox before inbound replies work | Local rsync of `~/.claude` does not apply to remote creates; OAuth credentials don't ride the wire | Open: tracked as gap G12. Workaround: `km shell <id>` once after create, run `claude login`, retry the Slack message. |
| No 👀 reaction appears within 1-2s of a Slack post (but Claude still replies) | Bot is missing `reactions:write` scope, OR `KM_SLACK_ACK_EMOJI` is set to an invalid emoji name (with colons, or a name Slack does not recognize) | `km doctor` will FAIL with `reactions:write` listed missing → add scope in Slack App config → OAuth & Permissions → reinstall app → `make build && km init --lambdas`. For invalid emoji, check `KM_SLACK_ACK_EMOJI` does NOT have surrounding colons (`eyes` not `:eyes:`). No token rotation needed — bot token is unchanged by reinstall. |

### How replies flow (validated end-to-end)

```
Slack post → Bridge /events (HMAC-verified, allow-list filtered) →
SQS FIFO {prefix}-slack-inbound-{sandbox-id}.fifo →
sandbox systemd poller (km-slack-inbound-poller.service) →
poller exports KM_SLACK_THREAD_TS into Claude's env →
claude -p (Bedrock or OAuth) →
output.json .result captured by poller →
poller calls /opt/km/bin/km-slack post --thread $KM_SLACK_THREAD_TS →
SQS DeleteMessage (only after successful post) →
Bridge re-issues chat.postMessage → reply lands in same Slack thread
```

- The **Stop hook** is gated on `KM_SLACK_THREAD_TS` (which the poller exports
  BEFORE launching Claude). When the poller is driving the turn, the Stop
  hook's Slack branch is suppressed — exactly one bot post per turn.
- Failure mode is **silent in Slack** — if Claude exits non-zero or `.result`
  is empty, no fallback string is posted; the SQS message returns to the
  queue and SQS redelivers. Operators diagnose via
  `journalctl -u km-slack-inbound-poller` and `km agent list <sandbox>`.

### Security model

- **Signing secret** verifies that incoming `/events` requests are from Slack
  (HMAC-SHA256 with a 5-minute timestamp window).
- **Allow-list subtype filter** in the bridge: only `subtype == ""` (real
  human turn) and `subtype == "thread_broadcast"` reach SQS. Every other
  subtype (`channel_join`, `channel_leave`, `channel_topic`, `pinned_item`,
  `bot_message`, `message_changed`, `me_message`, etc.) is dropped at the
  bridge with a debug log line `events: subtype filter dropped subtype=...`.
  Forensic CloudWatch query:
  ```
  fields @timestamp, subtype, channel
  | filter @message like /subtype filter dropped/
  | stats count() by subtype
  ```
- **Bot user_id filter** is the second-line defence under the allow-list
  (drops self-messages even if a future Slack subtype slips through).
- **Per-sandbox IAM**: each sandbox can only `ReceiveMessage` from its own
  queue ARN.
- **Cross-sandbox isolation**: bridge's `sqs:SendMessage` permission is
  scoped to the queue-name pattern; cannot write to a sandbox's queue without
  knowing the channel-to-sandbox mapping (DDB GSI).

### Signing secret rotation

```bash
km slack init --force --signing-secret <new-secret>
```

Then manually force-cold-start the bridge Lambda (touch its env var or
redeploy) so the `SSMSigningSecretFetcher` cache invalidates within 15 minutes.

### Limitations (deferred to later phases)

- Mention-based sandbox spawning (`@km-bot create profile=foo prompt="..."`).
- Slack interactive features (Block Kit buttons, slash commands, modals).
- Auto-resume of paused sandboxes on inbound activity.
- Inbound on shared channel or override-mode channels.
- DM delivery, multi-recipient routing.
- Block Kit / rich formatting for outbound replies.
- Permission-prompt round-trip via Slack reply.

## Slack transcript streaming (Phase 68)

Per-turn streaming of Claude assistant text + tool one-liners to a per-sandbox
Slack thread, plus a final gzipped JSONL transcript uploaded as a Slack file
when the response ends. Replaces the Phase 63 single idle-ping for sandboxes
that opt in.

### One-time operator setup

```bash
# 1. Add `files:write` scope to the Slack App
#    (Slack admin → App → OAuth & Permissions → add `files:write` → re-install)

# 2. Provision the new DDB table + bridge code + sidecar binary
make build
km init --sidecars   # uploads new km-slack binary + bridge zip
km init              # provisions {prefix}-slack-stream-messages DynamoDB table

# 3. Verify
km doctor            # slack_transcript_table_exists / slack_files_write_scope green
```

### Profile field

| Field | Type | Default | Effect |
|---|---|---|---|
| `notifySlackTranscriptEnabled` | bool | `false` | Per-turn streaming + final upload to per-sandbox Slack thread |

**Validation rules:**
- Requires `notifySlackEnabled: true` AND `notifySlackPerSandbox: true`
- Incompatible with `notifySlackChannelOverride`

### CLI overrides

```bash
km agent run sb-X --prompt "..." --transcript-stream      # force-enable for this run
km agent run sb-X --prompt "..." --no-transcript-stream   # force-disable for this run
km shell sb-X --transcript-stream
km shell sb-X --no-transcript-stream
```

Sets `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=1`/`=0` in the SSM session env, taking precedence over the profile default.

### How it works

1. **PostToolUse hook (per Claude tool call):**
   - Reads new transcript JSONL entries from byte offset
   - Renders assistant text + `🔧 ToolName: input` one-liners
   - Posts to per-sandbox channel thread
   - Records `(channel_id, slack_ts) → transcript_offset` in DynamoDB

2. **Stop hook (end of Claude response):**
   - Drains any unstreamed text
   - `gzip` transcript, `aws s3 cp` to `s3://${KM_ARTIFACTS_BUCKET}/transcripts/{sandbox-id}/{session-id}.jsonl.gz`
   - Calls bridge `upload` action; bridge fetches from S3 (streamed), uploads to Slack via 3-step files API

3. **Auto-thread-parent:** Operator-initiated runs (no Phase 67 inbound thread context) post a parent message `🤖 [sb-X] turn started — {prompt}` and cache its ts so all turns of the response thread under it.

### Security model

- **Audience containment:** transcripts only land in per-sandbox channels (validation rejects shared channel + override combinations)
- **Cross-sandbox isolation:** bridge enforces S3 prefix `transcripts/{envelope.sender_id}/` before GetObject; one sandbox cannot upload another's transcript via crafted envelope
- **Trust boundary:** unchanged from Phase 63/67 — sandbox holds Ed25519 signing key; bridge holds Slack bot token

⚠️ **Transcripts contain whatever Claude saw.** Bash output, file reads, env dumps, API responses — all visible in the channel and the uploaded file. Do NOT enable for sandboxes processing sensitive data without operator awareness. Transcript redaction is OUT OF SCOPE for Phase 68.

### Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `km doctor` flags `slack_transcript_table_exists` WARN | DDB table not provisioned | `km init` (terraform apply) |
| `km doctor` flags `slack_files_write_scope` WARN | Bot lacks files:write | Re-auth Slack App with files:write scope |
| Streaming works but file upload missing | files:write missing on bot | Same as above; bridge returns 400 scope_missing |
| Bridge logs show `s3_key_prefix_mismatch` | Sandbox attempted upload with wrong prefix | Should never happen in normal flow; investigate sandbox compromise |
| Lambda timeout / OOM during upload | Transcript >100 MB | Out of scope; current cap 100 MB |
| Slack thread shows gaps during heavy runs | Slack rate limit | By design — file upload at Stop has the full record |
| `km doctor` flags `slack_transcript_stale_objects` WARN | S3 has transcripts for destroyed sandboxes | Cleanup advisory; bucket lifecycle eventually reaps |

### Operator runbook: enabling files:write scope

1. Slack admin → App → OAuth & Permissions
2. Bot Token Scopes → Add scope → `files:write`
3. Re-install app (top of page)
4. New token issued; rotate via `km slack rotate-token --bot-token <new>`
5. Verify: `km doctor` should show `slack_files_write_scope` OK
6. (Optional) Force bridge cold-start to pick up cached scope state: `km slack rotate-token` does this automatically

### Phase 68 ↔ Phase 67 interaction

- Inbound (Phase 67) and transcript streaming (Phase 68) compose cleanly. When BOTH are on:
  - Inbound message arrives → poller dispatches `km agent run` with `KM_SLACK_THREAD_TS` set to the inbound thread parent
  - PostToolUse hooks stream into THAT thread (no auto-parent created — Phase 67 thread used)
  - Stop hook uploads the transcript into the same thread
- Inbound off + transcript on:
  - PostToolUse auto-creates a thread parent in the per-sandbox channel
  - All turns + final upload thread under it

### Phase B preview (deferred, not part of Phase 68)

The DynamoDB stream-messages table written by Phase 68 is the integration seam for a future "reaction-triggered session fork" phase: an operator reaction (e.g. 🍴) on a streamed message would mint a new Claude session forked at that transcript offset. Phase 68 has no consumer for the table — it just writes.
