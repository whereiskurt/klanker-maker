# Slack Notifications Guide

Klanker extends the operator-notify hook with parallel Slack delivery.
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

Klanker provides Slack delivery alongside the existing email notification path:

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

2. **Custom Slack App** installed in the workspace with the full bot-scope set (14 scopes today). The canonical, version-current scope list is rendered by `km slack manifest`; paste the output into Slack admin → Apps → Build → New App → From manifest. Maintaining a hand-curated scope list in docs invariably drifts — see § Security Model § Complete bot scope inventory for the audit-friendly per-scope justification.

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
  --invite-email "kurt.hundeck@example.com" \
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
| `notifyEmailEnabled` | `bool*` | `true` | Set `false` to skip email dispatch when Slack is on. Omitting the field preserves the default (email always fires). |
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

Sandboxes provisioned before Slack was configured do **not** get these variables retroactively. Destroy and recreate to pick up the km-slack binary and env vars.

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

This section is the integration's security-audit reference. It answers the
two questions an external reviewer typically asks first — *"how does this work
end-to-end?"* and *"does it follow Slack's stated best practices?"* — by
enumerating scopes, secrets, trust boundaries, IAM scoping, replay defenses,
data retention, and the threat model. Subsections are independent so an
auditor can jump to the bit they care about.

### Quick reference (notify-hook signing chain)

**Signing:** every sandbox has an Ed25519 key pair. The private key lives in
SSM at `/sandbox/{id}/signing-key` (SecureString, accessible only to the
sandbox's own IAM role). The `km-slack` binary signs the canonical JSON
envelope before sending it to the bridge.

**Verification chain (bridge Lambda):**
1. Parse JSON envelope.
2. Reject if `timestamp` is more than ±5 minutes from current UTC (replay protection).
3. Conditional `PutItem` on the nonce table: reject if `nonce` was seen within the replay window (dedup).
4. Fetch sender public key from DynamoDB `km-identities` using `sender_id`.
5. Verify the Ed25519 signature over the canonical JSON bytes.
6. Assert channel ownership: sandbox `sender_id` must match the `channel`'s owning sandbox in DynamoDB `km-sandboxes` (prevents sandbox A posting to sandbox B's channel).
7. Assert action authorization: sandbox identity may only perform `post` / `upload` / `permalink` / `update`; `archive` and `test` require operator identity.

**Bot token isolation:** `/km/slack/bot-token` is a KMS-encrypted SSM
SecureString. Only the bridge Lambda's IAM role and the operator's IAM
identity have `ssm:GetParameter` permission on it. Sandbox IAM roles cannot
read it.

**Slack Connect:** the klankermaker.ai workspace sends Connect invites to the
operator's email. The operator accepts from their own workspace. No
credentials are shared; Slack Connect is a federated channel-sharing protocol.

### Trust boundaries

```
┌──────────────────────┐    Ed25519-signed envelope    ┌────────────────────┐
│  Sandbox EC2 / Docker│  ────────────────────────────▶│  Bridge Lambda     │
│  - own private key   │     POST  (HTTPS, no IAM)     │  - signature       │
│    (SSM, per-sandbox)│                                │    verification    │
│  - no bot token      │  ◀────── HTTP status ────────│  - SSM token cache │
└──────────────────────┘                                │    (15-min TTL)    │
                                                        └─────────┬──────────┘
                                                                  │ Slack Web API
                                                                  │ chat.* / conversations.* / files.*
                                                                  ▼
┌──────────────────────┐                              ┌─────────────────────┐
│  Operator workstation│  read/write /km/slack/*      │  Slack Web API      │
│  - AWS SSO/profile   │  via SSM (KMS SecureString)  │                     │
│  - km CLI            │                              └──────────┬──────────┘
└──────────────────────┘                                         │ Events API
                                                                 │ (Slack signs with /km/slack/signing-secret)
                                                                 ▼
                                                       ┌─────────────────────┐
                                                       │  Bridge /events     │
                                                       │  - HMAC-SHA256      │
                                                       │  - ±5min window     │
                                                       │  - subtype allow-list│
                                                       └──────────┬──────────┘
                                                                  │ sqs:SendMessage
                                                                  ▼
                                                       ┌─────────────────────┐
                                                       │  Per-sandbox        │
                                                       │  SQS FIFO queue     │
                                                       └──────────┬──────────┘
                                                                  ▼
                                                       ┌─────────────────────┐
                                                       │  Sandbox poller     │
                                                       │  (systemd, own IAM) │
                                                       └─────────────────────┘
```

Three independent authentication domains, each with its own secret material:

1. **Sandbox → bridge:** Ed25519 signature over canonical JSON envelope.
2. **Slack → bridge (inbound events):** HMAC-SHA256 with the Slack signing secret + 5-min timestamp window.
3. **Bridge → Slack Web API:** the bot token (`xoxb-…`), fetched from SSM on cold start, cached in-process for 15 minutes.

The sandbox never sees the bot token. The bridge never sees a sandbox's
Ed25519 private key. Slack never sees the per-sandbox Ed25519 material. A
compromise of any one domain does not automatically compromise the others.

### Complete bot scope inventory

Render the canonical list at any time — this is the single source of truth:

```bash
km slack manifest | jq -r '.oauth_config.scopes.bot[]'
```

Audit-friendly per-scope table (14 scopes as of Phase 75 + Phase 72 + the
`groups:read` follow-up):

| Scope | Slack API methods used | Why klanker needs it | Notes |
|---|---|---|---|
| `chat:write` | `chat.postMessage`, `chat.update` | All notification, transcript, reply, and operator-test messages | Primary path — no alternative |
| `channels:manage` | `conversations.create`, `conversations.archive`, `conversations.invite` (public) | Per-sandbox public channel lifecycle and operator invite at `km create` | Required for `notifySlackPerSandbox: true` and `km slack invite` |
| `channels:join` | `conversations.join` | Self-rescue when the bot is ejected from a public channel during an app reinstall | Avoids requiring a human `/invite` after every token rotation |
| `channels:read` | `conversations.info`, `conversations.list` | `km doctor` channel-name resolution; `km slack invite` channel lookup; bridge channel-membership probes | Read-only metadata on public channels |
| `channels:history` | Events `message.channels` delivery; paginated reads | Inbound chat from public channels (poller consumes from per-sandbox SQS) | Pairs with the `message.channels` event subscription |
| `groups:write` | `conversations.create` (private), `conversations.archive` (private), `conversations.invite` (private) | Per-sandbox private channel lifecycle when operator pre-creates private channels | |
| `groups:read` | `conversations.info`, `conversations.list?types=private_channel` | `km doctor` and `km slack invite` against private and Slack Connect channels | Added as a Phase 72 follow-up after a reinstall produced `channel_not_found` for the shared Connect channel |
| `groups:history` | Events `message.groups` delivery; paginated reads | Inbound chat from private channels | Pairs with the `message.groups` event subscription |
| `conversations.connect:write` | `conversations.inviteShared` | Slack Connect invites for external operators and the auto-invite list when `useSlackConnect: true` | Requires Pro workspace tier; gracefully fails open on free tier |
| `reactions:read` | (future) reaction-triggered session fork | Forward-compatibility seam for the planned reaction-fork feature; not actively consumed today | Removing this scope today blocks only the future fork feature |
| `reactions:write` | `reactions.add` | 👀 ACK on every accepted inbound Slack message (user feedback that the sandbox saw the message) | Independent of message delivery — failure logged but does not block reply |
| `files:write` | `files.getUploadURLExternal`, `files.completeUploadExternal` | End-of-response transcript upload (gzipped JSONL) into the per-sandbox thread | Required only for `notifySlackTranscriptEnabled: true` |
| `files:read` | `files.info`; private-URL download with the bot token | Download user-attached files from inbound posts into `/workspace/.km-slack/attachments/` | Required for inbound file-attachment support (Phase 75) |
| `users:read.email` | `users.lookupByEmail` | Auto-detect whether an invite address is a workspace member (regular invite) or external (Slack Connect); used by `km slack init`, `km slack invite`, and the `km create` auto-invite loop | Strictly narrower than `users:read` — does not enumerate the workspace directory |

#### Scopes deliberately NOT requested

A "negative-scope" inventory — adjacent-looking scopes that are absent from
the manifest, with the rationale:

| Scope not requested | Reasoning |
|---|---|
| Any User Token Scope | Integration is purely server-to-server; no user-impersonation. The manifest declares only Bot Token Scopes. |
| Legacy `bot` scope | Deprecated by Slack — granular bot scopes only. |
| `users:read` | Klanker only resolves *explicit* email addresses provided by the operator; it never enumerates the workspace directory. `users:read.email` is the narrower scope sufficient for `lookupByEmail`. |
| `chat:write.public` | The bot must be explicitly invited to channels before posting. This is an intentional guardrail — if a channel ID drifts, the bot fails with `not_in_channel` rather than silently posting somewhere else. |
| `chat:write.customize` | The bot posts under its installed display name and avatar uniformly. No per-message identity override. |
| `im:read` / `im:write` / `im:history` | No DMs. All interaction happens in named channels (shared or per-sandbox). |
| `mpim:*` | No multi-party DMs. |
| `links:read` / `links:write` | No link unfurling; klanker does not register an unfurl domain. |
| `app_mentions:read` | No `@klanker` mention handling. Sandbox routing is by channel, not by mention. |
| `pins:*`, `bookmarks:*`, `usergroups:*`, `team:read`, `dnd:read` | No surface uses these. |
| `admin.*` (Enterprise) | Klanker operates within a single workspace; no admin/Enterprise Grid scopes required. |
| `commands` | No slash commands. (`km` is the operator CLI; no `/km` Slack command.) |
| Socket Mode | `socket_mode_enabled: false` in the manifest. Bridge is reached over HTTPS at the Lambda Function URL. |
| OAuth token rotation | `token_rotation_enabled: false`. Rotation is operator-driven via `km slack rotate-token` so the cold-start window is controlled. |
| Interactive components (modals, buttons, shortcuts) | No interactivity today. Outbound Block Kit (Phase 74) is presentation-only. |

#### Events API subscriptions

Two bot events — no workspace-wide subscription pattern:

| Event | Why |
|---|---|
| `message.channels` | Inbound chat from public per-sandbox channels |
| `message.groups` | Inbound chat from private per-sandbox channels |

The bridge filters every event through an **allow-list** at receipt:

- `subtype == ""` (real human message) — forwarded
- `subtype == "thread_broadcast"` (reply with broadcast) — forwarded
- Every other subtype (`channel_join`, `channel_leave`, `channel_topic`, `pinned_item`, `bot_message`, `message_changed`, `me_message`, file_share system messages, etc.) — dropped at the bridge with `events: subtype filter dropped subtype=…` (debug log)

A second-line `bot_user_id` filter drops any message authored by the bot
itself, defending against a future Slack subtype slipping past the allow-list.

### Secrets inventory

| Secret | Storage | Encryption | Read access | Rotation command |
|---|---|---|---|---|
| Slack bot token (`xoxb-…`) | SSM `/km/slack/bot-token` | KMS SecureString (account-default AWS-managed key) | Bridge Lambda role + operator's local AWS identity | `km slack rotate-token --bot-token <new>` |
| Slack signing secret | SSM `/km/slack/signing-secret` | KMS SecureString | Bridge Lambda role + operator | `km slack rotate-signing-secret --signing-secret <new>` |
| Per-sandbox Ed25519 private key | SSM `/sandbox/{id}/signing-key` | KMS SecureString | Sandbox's own IAM role only (resource-ARN-scoped to its own ID) | Sandbox lifetime; rotated by `km destroy && km create` |
| Per-sandbox Ed25519 public key | DynamoDB `km-identities` | At-rest AWS-managed KMS encryption | Bridge Lambda role | Paired with the private key |

Nothing related to Slack is stored in profiles, environment variables, source
files, or git history.

### IAM scoping

**Sandbox IAM role** (per-sandbox; least privilege):
- `ssm:GetParameter` only on `/sandbox/{own-id}/signing-key` (resource ARN includes the sandbox's own ID; cannot read peers')
- `sqs:ReceiveMessage`, `sqs:DeleteMessage`, `sqs:ChangeMessageVisibility` only on its own queue ARN
- `s3:PutObject` on `transcripts/{own-id}/*` only
- **Cannot** read the bot token, the signing secret, peer sandboxes' keys, or peer queues

**Bridge Lambda execution role:**
- `ssm:GetParameter` on `/km/slack/*` (bot token + signing secret only — not sandbox keys)
- `dynamodb:GetItem` on `km-identities`, `km-sandboxes`, `km-slack-threads`; `dynamodb:PutItem` on `km-slack-nonces` (conditional, for replay dedup)
- `sqs:SendMessage` on the queue-name pattern `{prefix}-slack-inbound-*.fifo` (cannot `ReceiveMessage` — write-only into sandbox queues)
- `s3:GetObject` on `transcripts/*` (read; for upload) and `slack-inbound/*` (read; for the poller's file mirror)
- **Cannot** call `ec2:*`, `iam:*`, `sts:AssumeRole`, or any other AWS service

**Operator IAM identity** (your local AWS profile):
- Full read/write `/km/*` SSM (covers all rotations + initial bootstrap)
- `lambda:UpdateFunctionConfiguration` on the bridge (for forced cold-start during rotation)
- Read on operational DynamoDB tables (for `km doctor` / `km status`)
- Gated by your org's AWS SSO / federation policy

### Authentication chains

**Outbound (sandbox → Slack):**

```
1. Hook fires in sandbox (notify-hook or systemd poller)
2. km-slack builds canonical JSON envelope:
     { sender_id, channel, action, body, timestamp, nonce, blocks? }
3. Loads Ed25519 private key from SSM /sandbox/{id}/signing-key (cached in process)
4. Signs canonical bytes; POSTs to https://{bridge-fn-url}/ with X-KM-Signature
5. Bridge:
     a. Parse envelope
     b. Reject if |now - timestamp| > 5 min (replay window)
     c. Conditional PutItem on km-slack-nonces — reject if nonce already seen (TTL ~5 min)
     d. GetItem from km-identities for sender_id → public key
     e. ed25519.Verify(pub, canonical, sig) — reject on failure
     f. Channel-ownership assertion (sandbox can post only to its own channel,
        cross-checked against km-sandboxes)
     g. Action-authorization assertion (sandboxes: post/upload/permalink/update;
        operator-only: archive/test)
     h. GetParameter for /km/slack/bot-token (15-min in-process cache)
     i. Dispatch to Slack Web API
6. Slack response code returned to sandbox over HTTPS
```

**Inbound (Slack → sandbox):**

```
1. User posts in #sb-{id}
2. Slack POSTs to https://{bridge-fn-url}/events with:
     X-Slack-Signature: v0=<HMAC-SHA256 over "v0:{ts}:{body}">
     X-Slack-Request-Timestamp: <unix-seconds>
3. Bridge /events:
     a. Reject if |now - ts| > 5 min (per Slack guidance)
     b. HMAC-SHA256 verify with /km/slack/signing-secret — reject on mismatch
     c. Parse event; auto-ack url_verification challenges
     d. Subtype allow-list: drop anything outside { "", thread_broadcast }
     e. Bot user_id filter: drop self-messages
     f. Resolve channel → sandbox via km-sandboxes GSI on slack_channel_id
     g. sqs:SendMessage to {prefix}-slack-inbound-{sandbox-id}.fifo
     h. reactions.add(👀) on the originating message (best-effort, bounded retry, fail-soft)
4. Sandbox poller (systemd):
     a. ReceiveMessage from own queue (long-poll)
     b. Export KM_SLACK_THREAD_TS into Claude's environment
     c. Invoke claude -p (Bedrock or direct API)
     d. On success: post Claude's reply via km-slack (re-enters outbound chain),
        then SQS DeleteMessage
     e. On failure: do not delete — SQS redelivers after visibility timeout
```

The Function URL is `AuthType: NONE` **deliberately**. The signature *is* the
auth — adding IAM auth on the Function URL would require the sandbox to assume
credentials reachable from Slack's HMAC verification flow, coupling the two
inbound paths. Keeping the URL open and verifying the signature inside the
handler preserves the separation of the three authentication domains.

### Replay & timing-attack defenses

- ±5-minute timestamp window on **both** the inbound Slack HMAC and the outbound Ed25519 envelope
- DynamoDB `km-slack-nonces` table with TTL — single-use envelope nonces enforced via conditional PutItem (`attribute_not_exists(nonce)`)
- Ed25519 + HMAC-SHA256 — both constant-time-comparable primitives in their reference implementations
- Subtype allow-list (positive, not deny) — new Slack message subtypes fail closed
- `bot_user_id` second-line filter prevents self-message loops

### Data classification & retention

| Surface | Data | Retention | Encryption |
|---|---|---|---|
| `s3://{artifacts-bucket}/transcripts/{sandbox-id}/` | Full Claude transcripts (JSONL.gz; includes Bash output, file reads, tool inputs) | Bucket lifecycle (operator-configured; apply per your compliance regime) | SSE-S3 (default) |
| `s3://{artifacts-bucket}/slack-inbound/{sandbox-id}/{thread_ts}/` | User-uploaded files from Slack drag-drop | **30-day lifecycle expiration** | SSE-S3 |
| DynamoDB `km-slack-threads` | `(channel_id, thread_ts, agent_type, session_id, last_assistant_msg[:500])` | Per-row TTL | At-rest AWS-managed KMS |
| DynamoDB `km-slack-nonces` | Nonce bytes only | ~5-minute TTL | At-rest AWS-managed KMS |
| DynamoDB `km-identities` | Per-sandbox public keys + metadata | Sandbox lifetime | At-rest AWS-managed KMS |
| SQS `{prefix}-slack-inbound-{sandbox-id}.fifo` | Inbound message bodies | 14-day SQS max; consumed within seconds in normal operation | At-rest AWS-managed KMS |
| Slack channels `#sb-{id}` | Per-turn streaming, final transcript upload, operator chat | Per-workspace retention policy; channel archived (not deleted) at `km destroy` unless `slackArchiveOnDestroy: false` | Slack-side |

⚠ **Transcripts contain whatever Claude saw.** Operator-side compliance
(HIPAA, PCI, SOC 2 evidence handling) requires owner review of the artifacts
bucket lifecycle and the Slack workspace retention policy. Klanker does not
redact transcripts; do not enable `notifySlackTranscriptEnabled` for sandboxes
that process regulated data without explicit owner sign-off.

### Slack security best-practices alignment

| Slack platform guidance | Klanker implementation |
|---|---|
| Verify request signatures on every Events API call | HMAC-SHA256 with `/km/slack/signing-secret` inside bridge `handler.go`; ±5-min timestamp window |
| Use granular Bot Token Scopes, not legacy `bot` | Manifest declares only granular scopes; no `bot`, no User Token Scopes |
| Subscribe only to events you need | Two bot events (`message.channels`, `message.groups`); zero workspace-wide subscriptions |
| Don't store bot tokens in source / CI / env vars | Stored only in SSM SecureString; never in code, environment variables, or git |
| Provide a documented rotation procedure | `km slack rotate-token` (validates → persists → cold-starts bridge → smoke tests) plus a documented incident-response cycle |
| Provide signing-secret rotation | `km slack rotate-signing-secret` |
| Use least-privilege channel access | Per-sandbox channels; bot must be invited; bridge cross-checks that sandbox `sender_id` matches the channel's owning sandbox in DDB |
| Don't enable OAuth token rotation unless needed | `token_rotation_enabled: false` — operator manages rotation explicitly so cold-start window is controlled |
| No Socket Mode unless required | `socket_mode_enabled: false` — HTTPS Function URL only |
| No interactive components unless needed | No modals, buttons, slash commands, or shortcuts |
| Validate the team / channel in Events payloads | `team_id` checked; channel→sandbox lookup against own DDB before SQS dispatch |
| Provide audit visibility | CloudWatch log group `/aws/lambda/km-slack-bridge` with structured `key=value` logs of every signature step, dispatched call, and error path |
| Don't request `chat:write.public` unless required | Not requested — bot membership is a deliberate guardrail |

### Audit & observability

CloudWatch log group: **`/aws/lambda/km-slack-bridge`**. Every request logs
structured `key=value` pairs:

| Field | What an auditor learns |
|---|---|
| `action` | `post` / `archive` / `test` / `upload` / `permalink` / `update` |
| `sender_id` | Sandbox ID or `operator` |
| `channel` | Slack channel ID (target of the call) |
| `nonce_prefix` | First 8 chars of the request nonce (cross-reference handle) |
| `step` | Which verification step failed (`nonce` / `signature` / `token_fetch` / `dispatch`) |
| `slack_error` | Slack API error code on dispatch failure (`not_in_channel`, `invalid_auth`, `channel_not_found`, …) |
| `status` | HTTP status returned to caller |
| `attempt=N` | Retry attempt count on reaction-add failures |

Operator-side audit tools:

- `km doctor` — checks every Slack-related secret, scope (cached), channel membership, table existence, and queue health
- `km slack status` — current SSM state at a glance
- DynamoDB `km-slack-nonces` TTL gives a forensic window for nonce-replay investigation

### Threat model: out of scope

Klanker's Slack integration explicitly does **not** defend against:

- **Compromise of the operator's AWS identity** — gives full access to the bot token via SSM. Mitigation lives in your AWS identity provider's MFA + session controls, not in klanker.
- **Compromise of a Slack workspace admin** — they can reinstall the app, exfiltrate the bot token from the Slack admin UI, change app scopes, etc.
- **Insider with sandbox shell access** — they can read the sandbox's own Ed25519 private key from SSM via the sandbox's IAM role (by design: the sandbox needs it). Lateral movement from the sandbox to other surfaces is bounded by per-sandbox channel ownership + per-sandbox queue scoping.
- **Slack platform compromise** — Slack's TLS termination, data residency, and admin UI security are out of scope and deferred to Slack.
- **Bot token leak via Slack's own incident response** — if Slack reports the token compromised, the operator must rotate via `km slack rotate-token` per Slack's playbook.

For the **in-scope** threats — sandbox impersonation, replay, cross-sandbox
lateral movement, inbound message forgery, exfiltration via crafted envelopes
— the layered controls above (Ed25519 signatures, per-sandbox IAM,
channel-ownership assertion, subtype allow-list, nonce dedup, signing-secret
HMAC, S3 prefix enforcement) are the defense.

---

## See Also

- `docs/multi-agent-email.md` — email protocol (signing model reused here)
- `docs/superpowers/specs/2026-04-29-slack-notify-hook-design.md` — full Slack notify-hook design spec
- `CLAUDE.md` — CLI quick reference, env var and SSM path conventions

---

## Inbound chat

Slack messages in a per-sandbox channel become Claude turns inside that sandbox.
The same `#sb-{id}` channel is a bidirectional chat surface.

### Prerequisites

- Slack notifications already configured: bridge Lambda deployed, bot token persisted at
  `/km/slack/bot-token`, shared channel created.
- Slack App has these additional scopes (add via Slack App config → OAuth & Permissions):
  - `channels:history` — read messages in public channels
  - `groups:history` — read messages in private channels
  - `reactions:write` — post the 👀 ACK reaction on accepted messages
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
    notifySlackInboundEnabled: true
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

### ACK reaction

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

#### ACK reaction retry behavior

The ACK reaction uses a bounded retry loop inside `SlackReactorAdapter.Add`. Transient Slack API
failures — HTTP 429 (rate limit), HTTP 5xx, network errors, and
Slack JSON errors `internal_error` / `service_unavailable` /
`fatal_error` / `request_timeout` — now trigger up to 2 retries
with exponential backoff (200ms then 600ms, each with ±25% jitter
to de-correlate retries across many sandboxes during a Slack
incident). On HTTP 429, the loop honors the `Retry-After` header if
its value fits within the remaining 10-second budget; otherwise it
returns the typed `ErrSlackRateLimited` immediately.

Error classification follows three buckets:

- **Success** (no retry): HTTP 200 + `ok:true`, or `already_reacted`
  (idempotency for double-delivered events).
- **Terminal — no retry, Error log**: `invalid_auth`, `not_authed`,
  `account_inactive`, `token_revoked`, `missing_scope`,
  `token_expired`, plus related auth codes. These require operator
  action (rotate bot token, re-install app for missing scope).
- **Terminal — no retry, Warn log via handler**: `bad_timestamp`,
  `message_not_found`, `channel_not_found`, `not_reactable`,
  `thread_locked`, `invalid_name`, plus related bad-input codes.
- **Transient — retry**: everything in the transient list above
  PLUS any unknown error string (default-unknown→transient policy
  — safer than hard-failing on an error code Slack adds tomorrow).

Operator observability: the existing `events: reaction failed` Warn
line in CloudWatch is preserved on final retry exhaustion, now with
a new `attempt=N` structured field. Intermediate retries log at
Debug (silent at the default Lambda log level; visible when
`KM_LOG_LEVEL=debug`).

The handler goroutine's context timeout was bumped from 5s to 10s
to accommodate the retry budget (worst case: ~800ms of sleeps + 3
HTTP round-trips, comfortably under 10s for normal Slack latency
with headroom for incident-mode slowness).

Bridge-only change. Deploy: `make build && km init --lambdas`.
Rollback: PR revert + `km init --lambdas`. No sandbox redeploy.
See `docs/superpowers/specs/2026-05-14-slack-ack-reaction-bounded-retry-design.md`
for the full design spec.

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
- Permission-prompt round-trip via Slack reply.

(Block Kit / rich formatting for outbound replies is described in § Slack Block Kit rendering below.)

## Slack transcript streaming

Per-turn streaming of Claude assistant text + tool one-liners to a per-sandbox
Slack thread, plus a final gzipped JSONL transcript uploaded as a Slack file
when the response ends. Replaces the single idle-ping for sandboxes that opt in.

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

3. **Auto-thread-parent:** Operator-initiated runs (no inbound thread context) post a parent message `🤖 [sb-X] turn started — {prompt}` and cache its ts so all turns of the response thread under it.

### Security model

- **Audience containment:** transcripts only land in per-sandbox channels (validation rejects shared channel + override combinations)
- **Cross-sandbox isolation:** bridge enforces S3 prefix `transcripts/{envelope.sender_id}/` before GetObject; one sandbox cannot upload another's transcript via crafted envelope
- **Trust boundary:** sandbox holds Ed25519 signing key; bridge holds Slack bot token

⚠️ **Transcripts contain whatever Claude saw.** Bash output, file reads, env dumps, API responses — all visible in the channel and the uploaded file. Do NOT enable for sandboxes processing sensitive data without operator awareness. Transcript redaction is not supported.

### Known limitations

#### Slack Connect externally-shared channels reject file uploads

Per-sandbox channels created via `km create` with
`notifySlackPerSandbox: true` are shared with the operator via Slack
Connect (`is_ext_shared: true`). UAT discovered that Slack's modern
3-step file upload API (`files.completeUploadExternal`) silently
returns `internal_error` when the target channel is externally shared,
even when:

- The bot is a full member of the channel
- The bot has `files:write` scope (verified by cold-start probe)
- Steps 1+2 of the upload (URL request + PUT) succeed
- Other API calls like `chat.postMessage` work fine in the same channel

**Effect:** the per-turn `🔧 ToolName: …` chat lines, auto-thread
parents, and DDB `record-mapping` rows all work correctly. Only the
final `claude-transcript-{session_id}.jsonl.gz` attachment at Stop is
affected — the upload silently fails and the operator gets no file.

**Workarounds today:**
- Pull transcripts directly from S3:
  `aws s3 ls s3://<artifacts-bucket>/transcripts/<sandbox-id>/`
- Use a non-Connect internal Slack channel (set
  `notifySlackChannelOverride` to a host-workspace channel ID) — note
  this loses per-sandbox isolation

**Known fix path (planned):** detect channel type at `km create`,
fall back to posting an S3 presigned-URL message in Connect channels
instead of a native Slack file attachment.

### Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `km doctor` flags `slack_transcript_table_exists` WARN | DDB table not provisioned | `km init` (terraform apply) |
| `km doctor` flags `slack_files_write_scope` WARN | Bot lacks files:write | Re-auth Slack App with files:write scope |
| Per-turn chat lines appear but no .jsonl.gz file at Stop | Channel is Slack Connect (`is_ext_shared: true`) | Known limitation; pull from S3 directly. A presigned-URL fallback is planned. |
| Streaming works but file upload missing AND channel is internal | files:write missing on bot | Re-auth Slack App with files:write; bridge returns 400 scope_missing |
| Bridge logs show `s3_key_prefix_mismatch` | Sandbox attempted upload with wrong prefix | Should never happen in normal flow; investigate sandbox compromise |
| Bridge logs show `s3_get_failed` 403 AccessDenied | Bridge IAM missing `s3:GetObject` on `transcripts/*` | Confirm `KM_ARTIFACTS_BUCKET` is set in bridge env (`aws lambda get-function-configuration`); re-run `km init` if missing |
| Bridge logs show `upload_failed: internal_error` | Slack Connect channel limitation (see Known Limitations above) | Pull from S3 directly |
| `km agent run` produces no transcript activity | `claude -p` (print mode) skips PostToolUse hooks per Claude Code platform | Use interactive `km shell` instead |
| Multiple top-level "turn started" messages for one task | Subagent fan-out — each Task-tool spawn has its own session_id | Expected behavior for subagent parallelism |
| Lambda timeout / OOM during upload | Transcript >100 MB | Out of scope; current cap 100 MB |
| Slack thread shows gaps during heavy runs | Slack rate limit | By design — file upload at Stop has the full record |
| `km doctor` flags `slack_transcript_stale_objects` WARN | S3 has transcripts for destroyed sandboxes | Cleanup advisory; configure bucket lifecycle policy or `aws s3 rm s3://<bucket>/transcripts/<sandbox-id>/ --recursive` |

### Operator runbook: enabling files:write scope

1. Slack admin → App → OAuth & Permissions
2. Bot Token Scopes → Add scope → `files:write`
3. Re-install app (top of page)
4. New token issued; rotate via `km slack rotate-token --bot-token <new>`
5. Verify: `km doctor` should show `slack_files_write_scope` OK
6. (Optional) Force bridge cold-start to pick up cached scope state: `km slack rotate-token` does this automatically

### Inbound chat and transcript streaming interaction

Inbound chat and transcript streaming compose cleanly. When BOTH are enabled:
  - Inbound message arrives → poller dispatches `km agent run` with `KM_SLACK_THREAD_TS` set to the inbound thread parent
  - PostToolUse hooks stream into THAT thread (no auto-parent created — inbound thread is used)
  - Stop hook uploads the transcript into the same thread
- Inbound off + transcript on:
  - PostToolUse auto-creates a thread parent in the per-sandbox channel
  - All turns + final upload thread under it

### Future: reaction-triggered session fork

The DynamoDB stream-messages table is the integration seam for a future "reaction-triggered session fork": an operator reaction (e.g. 🍴) on a streamed message would mint a new Claude session forked at that transcript offset. The table is written but has no consumer yet.

## Slack Block Kit rendering

Two-tier markdown renderer that turns Claude's CommonMark output into
valid Slack mrkdwn (Tier 1) or structured Block Kit (Tier 2). Eliminates
literal `***heading***` asterisks, dropped `# headings`, and broken
pipe-tables in outbound replies. Code blocks pass through byte-for-byte
(`**p = nil` stays intact), the tokenizer is idempotent and fail-soft,
and a 50-block cap automatically falls back from Tier 2 → Tier 1.

### Render modes

`km-slack post --render <mode>` selects the output:

| Mode | Output | Default user |
|---|---|---|
| `plain` | Literal markdown — no transformation | notify-hook (no `--render` flag passed) |
| `mrkdwn` | Tier 1: tokenized markdown → Slack mrkdwn `text` field | Operators who want rendering without Block Kit |
| `blocks` | Tier 2: Block Kit `blocks` field + Tier 1 mrkdwn fallback in `text` for mobile push previews; auto-falls-back to `mrkdwn` if the response exceeds 50 blocks | Inbound poller reply + streaming hook |

### Where Block Kit rendering is wired

Two paths in `pkg/compiler/userdata.go` use `blocks` rendering:

- `_km_stream_drain` — per-turn streaming posts (interactive `km shell` / transcript streaming path)
- `km-slack-inbound-poller` reply — final reply for Slack-inbound chat

Both lines pass `--render "${KM_SLACK_RENDER:-blocks}"`, so the env
override (below) takes precedence.

Idle-pings and permission-prompt notifications stay on
`plain` (the notify hook constructs envelopes without `--render`),
so the existing email/Slack idle path is byte-identical.

### Operator safety valve

A per-sandbox env var downgrades the renderer without a redeploy:

```bash
km shell <sandbox-id>
echo 'KM_SLACK_RENDER=plain' | sudo tee -a /etc/km/notify.env
# Next outbound post → falls back to literal markdown
```

Valid values: `plain` | `mrkdwn` | `blocks`. Unset → defaults to the
userdata template's hard-coded fallback (`blocks` for both
Block-Kit-emitting paths).

### One-time operator setup

Block Kit rendering is a code-only change — no new SSM params, no new DynamoDB
tables, no new Slack scopes.

```bash
make build
km init --sidecars        # ships new km-slack binary + new userdata template
km init --dry-run=false   # deploys updated bridge Lambda (PostMessageBlocks dispatch)
```

Existing sandboxes do NOT get Block Kit rendering retroactively (their
userdata is baked at create time). `km destroy && km create` to
provision a sandbox with the new template.

### Verify the deploy

After deploying, chat in `#sb-<sandbox-id>` from Slack:

> Show me a Go function and explain it. Use a heading.

Expect:

- A bold/large header block (not literal `# Heading`)
- A section block with monospaced code (not surrounded by triple-backticks in plain text)
- No literal `**bold**`, `***italic***`, or `# heading` in the rendered text

### Architecture

`pkg/slack/payload.go` adds a `Blocks string` field to `SlackEnvelope`
(alphabetical position between `Action` and `Body`, so the canonical
JSON ordering used for Ed25519 signing stays deterministic). When
non-empty, `pkg/slack/bridge/handler.go` type-asserts the configured
`SlackPoster` to a `BlockPoster`
(`pkg/slack/bridge/interfaces.go`) and dispatches to
`PostMessageBlocks`, which posts BOTH the rendered blocks AND the
Tier 1 `mrkdwn` text as the `text` fallback for Slack's mobile
push previews and notification surfaces.

The `BlockPoster` interface is optional — existing fakes that only
implement `SlackPoster` keep working (additive change, BRDG-01).
Any caller that omits the `Blocks` field hits the original
`PostMessage` path; notify-hook callers do not set it.

### Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Slack reply shows literal `# heading` or `**bold**` | Sandbox was provisioned BEFORE the new userdata template landed | `km init --sidecars` (refreshes the create-handler toolchain), then `km destroy && km create` |
| Reply renders as Tier 1 mrkdwn (bold/italic work, no header blocks) on a very long Claude response | Response exceeded the 50-block Block Kit cap → automatic Tier 1 fallback | Working as designed; trim or split the response to land in Block Kit |
| Reply renders as plain markdown despite Block Kit being configured | `KM_SLACK_RENDER=plain` in `/etc/km/notify.env` | Remove the override and reload systemd units (`sudo systemctl daemon-reload`), or pass `--render blocks` explicitly |
| Bridge returns 400 or `unknown action:` from Slack Web API when blocks are present | Bridge Lambda predates the BRDG-02 dispatch wrap | `km init --dry-run=false` to redeploy. Verify: `aws lambda get-function-configuration --function-name km-slack-bridge` shows a recent `LastModified` |
| Block Kit appears in `#sb-<id>` but NOT in the shared channel | Shared-channel notify-hook callers intentionally stay on `plain` | Working as designed; pass `--render blocks` from a custom caller if needed |
| `km-slack-inbound-poller` log: `WARN: agent run failed (exit 1)` and `output.json` shows `api_error_status: 401` | Anthropic OAuth token in the sandbox is stale (only affects `noBedrock: true` profiles) | `km shell <sandbox-id>` then `claude login` to refresh `~/.claude/.credentials.json` |

### Authoritative source

Plan files and verification: `.planning/phases/74-slack-mrkdwn-…/`
(`74-01-PLAN.md`, `74-02-PLAN.md`, `74-VERIFICATION.md`).

## Slack inbound file attachments

Users can drag-and-drop files (images, PDFs, etc.) into a per-sandbox
`#sb-{sandbox-id}` channel. The bridge Lambda downloads each file from
Slack using the bot token, stages it to S3, and the sandbox-side poller
mirrors each file to `/workspace/.km-slack/attachments/<thread_ts>/`.
A natural-language master-prompt wrapper is prepended to the Claude
turn enumerating absolute paths and MIME types — Claude reads each file
with its Read tool when relevant to the question.

**Profile field:** No separate field. Gated on the existing
`spec.cli.notifySlackInboundEnabled: true`.

**Caps:**

- 25 files per message — over-cap files dropped with thread-reply warning
- 100 MB per file — oversize files dropped with thread-reply warning

**One-time operator setup:**

1. Add `files:read` to the Slack App's bot scopes (App config → OAuth & Permissions)
2. Re-install the app to your workspace (admin approval may be required)
3. Rotate the bot token to pick up the new scope:
   ```bash
   km slack rotate-token --bot-token <new-token-from-Slack-App-admin>
   ```
4. Rebuild + redeploy the bridge:
   ```bash
   make build && km init --dry-run=false
   ```
   **Critical:** `km init` defaults to `--dry-run=true`. Without
   `--dry-run=false` the command only prints what *would* deploy and
   exits — no Terraform applies, no zip uploads. UAT 2026-05-15 lost
   ~30 minutes to this. Also use full `km init`, NOT `km init --lambdas`
   (lambdas-only builds the zip but never uploads it; see
   `project_km_init_lambdas_doesnt_deploy` in operator memory).
5. Verify the deploy actually landed:
   ```bash
   aws --profile klanker-application --region us-east-1 \
     lambda get-function-configuration --function-name km-slack-bridge \
     --query '{MemorySize:MemorySize, Timeout:Timeout, Vars:Environment.Variables}'
   ```
   Expected: `MemorySize=1024`, `Timeout=60`, `Vars` contains
   `KM_ARTIFACTS_BUCKET` plus the rest of the inbound env var set. If
   `Vars` only has `TOKEN_ROTATION_TS`, the last `km slack rotate-token`
   blew away the env vars and Terraform hasn't replaced them — re-run
   `km init --dry-run=false`.
6. Verify scopes via `km doctor` — `slack_app_events_subscription` should
   report `(channels:history, groups:history, reactions:write, files:read)`.

**Sandbox provisioning:** Existing sandboxes do NOT get file-attachment
userdata changes retroactively (the poller bash is baked into userdata
at create time). Run `km destroy && km create` on any sandbox that
needs file-attachment support. **The sandbox MUST be created AFTER
`km init --dry-run=false` runs** — otherwise the create-handler Lambda
will use its stale bundled `km` toolchain and generate outdated
userdata even though your local binary is current.

**S3 staging layout:**

- Key format: `slack-inbound/<sandbox-id>/<thread_ts>/<file_id>-<sanitized_name>`
- `<file_id>` is the Slack `F012345` identifier — guarantees uniqueness
  even when two files in the same thread share a name
- `<sanitized_name>` strips path-unsafe characters and truncates to 255 bytes
- **30-day lifecycle expiration** on the `slack-inbound/` prefix (matches
  the `km-slack-threads` DDB TTL)

**Sandbox-side layout:**

- Directory: `/workspace/.km-slack/attachments/<thread_ts>/`
- Files persist for the sandbox lifetime (cleaned by `km destroy`
  taking the EBS volume); subsequent turns in the same thread don't
  re-download

**Troubleshooting:**

| Symptom | Likely cause | Fix |
|---|---|---|
| 👀 appears but Claude doesn't read the file | `files:read` scope missing | Re-install app + `km slack rotate-token`; verify with `km doctor` |
| 👀 appears, Claude replies as text-only ("I don't see any file path attached") | (a) Sandbox provisioned before file-attachment support was deployed, **or** (b) sandbox created via `--remote` BEFORE `km init --dry-run=false` ran (stale create-handler toolchain) | `km init --dry-run=false` first, then `km destroy && km create` |
| Sandbox journal: `KM_ARTIFACTS_BUCKET: unbound variable` from km-slack-inbound-poller | Pre-75.3 userdata — the poller systemd unit doesn't set `KM_ARTIFACTS_BUCKET` and bash `set -u` fires on first file_share | `km init --dry-run=false` to refresh the create-handler toolchain, then `km destroy && km create` |
| Bridge logs `Get "": unsupported protocol scheme ""` | Modern Slack workspaces deliver stub file objects in event payloads (only `id` populated). The bridge must call `files.info` to enrich. Pre-75.1 bridges issued `http.Get("")` on the empty URL field. | Deploy ≥ 75.1: `make build && km init --dry-run=false` |
| Bridge logs `Client.Timeout exceeded while awaiting headers` on `files.slack.com` | Pre-75.2 bridge used a goroutine that outlived the handler return. AWS Lambda freezes the runtime once the 200 ships, and the in-flight HTTP deadline elapses during freeze. 75.2 made `file_share` handling synchronous. | Deploy ≥ 75.2 + redeploy bridge; verify Lambda `Timeout` ≥ 60s and bridge logs `events: enqueued (files-sync)` not `(files-fork)` |
| Bridge logs `files:read scope may be missing` | Same as "👀 appears but Claude doesn't read the file" | Same fix |
| Bridge logs `request body offset reset failed` | Lambda memory_size < 1024 (pre-bump) | Verify `terragrunt plan` and re-apply `infra/live/use1/lambda-slack-bridge/` |
| First 25 of N files attached; rest skipped | 25-file cap by design | Split the upload across multiple messages |
| Skipped foo.png (>100 MB cap) | 100MB cap by design | Trim or split the file |

**Verify the full pipeline on a running sandbox:**

```bash
# Bridge enqueued via the synchronous file path (note the suffix):
aws --profile klanker-application --region us-east-1 logs tail /aws/lambda/km-slack-bridge \
  --since 10m | grep 'enqueued (files-sync)'

# Poller mirrored attachments to the per-thread dir on the box:
km shell <sandbox-id> 'ls -laR /workspace/.km-slack/attachments/ 2>/dev/null | head -20'
```

**Authoritative design:** `docs/superpowers/specs/2026-05-15-slack-inbound-file-attachments-design.md` —
full PRD with failure-handling matrix, Pitfall catalog, and rollback procedure.

---

## Prefix routing & agent switching (Phase 70)

Phase 70 adds two related features to the Slack inbound flow:

1. **Per-message prefix routing** — a Slack message starting with `claude:` or
   `codex:` selects the agent for that turn (overriding the profile default).
2. **Cross-agent mid-thread switching** — a prefix in an existing thread that
   names the *other* agent spawns a new top-level message with a clean handoff
   post in the original thread.

For the full design, mechanism details, and troubleshooting matrix, see
`docs/codex-parity.md`. This section is the Slack-scoped quick reference.

### Prefix grammar

Regex: `^([Cc][Ll][Aa][Uu][Dd][Ee]|[Cc][Oo][Dd][Ee][Xx]):[[:space:]]?`

- Case-insensitive on agent name (`claude`, `Claude`, `CLAUDE` all match)
- Anchored at message start — mid-sentence `claude:` is ignored
- Exactly zero or one space after the colon
- No tolerance for spaces before the colon (`claude :` does not match)

### Behavior matrix

| Scenario | Result |
|---|---|
| No prefix, existing thread | Dispatch the row's agent (Phase 67 unchanged) |
| `codex: ...` on a fresh top-level in a claude-default sandbox | Codex dispatched; new DDB row with `agent_type=codex`; profile `KM_AGENT` on disk unchanged |
| `claude: ...` inside an existing claude-rooted thread | No-op continuation: strip prefix, same dispatch, same thread, no handoff |
| `codex: ...` inside an existing claude-rooted thread | Cross-agent switch: new top-level + handoff post + new DDB row + new Codex first turn |

### Cross-agent switch artifacts

In the **OLD** thread, after a switch:

```
Switching to codex → continuing in this thread.
https://workspace.slack.com/archives/C12345/p1716393742000300
```

In the **NEW** top-level message (posted first, before the handoff):

```
Codex will continue from https://workspace.slack.com/archives/C12345/p1716393640000200

Previous assistant (claude) said:
> {first 500 chars of last_assistant_msg from the old DDB row}
```

The new agent's prompt seed (passed to the agent CLI, NOT posted to Slack) is:

```
{stripped_prompt}

--- Context from prior thread (agent: claude) ---
{up to 2000 chars of last_assistant_msg}
```

No placeholder string is ever sent to Slack. The OLD thread's permalink is
fetched first (THREAD_TS is already known from the SQS event), so the new
top-level body embeds it at post-time. `chat.update` is not used in the
critical path.

### km-slack sidecar additions (Phase 70-04)

Three new surfaces added to the existing `km-slack` binary:

- `km-slack post --new-message` — omits `thread_ts`; returns `ts=...` to
  stdout (used to capture `NEW_TOP_TS` in step 3 of the switch sequence).
- `km-slack permalink --channel C --ts T` — wraps `chat.getPermalink`.
- `km-slack update --channel C --ts T --text "..."` — wraps `chat.update`
  (subject to Slack's 10-minute bot edit window; not in the cross-agent
  critical path).

All three go through the bridge Lambda via signed Ed25519 envelopes; sandboxes
never touch the raw Slack bot token.

### km doctor checks (Phase 70)

- **`codex_version_supports_jsonl`** — for each sandbox with
  `spec.cli.agent: codex`, verifies the installed Codex binary supports
  `--json` output (JSONL stream). WARN on mismatch.
- **`agent_type_consistency`** — for each `km-slack-threads` row with
  `agent_type` set, confirms the corresponding profile still declares the same
  agent. WARN on drift (catches post-create profile flips).

### See also

`docs/codex-parity.md` — full operator guide including JSONL stream mechanism,
SC-3 rationale, full switch sequence, and Phase 70 deferrals.

---

## Phase 72: Corporate workspace support — auto-detect invite + manifest generator

Phase 72 adds three new capabilities for installing klankermaker into a corporate Slack workspace
where most invitees are native workspace members (not external collaborators reachable only via
Slack Connect).

### What's new

| Capability | Command / Surface |
|---|---|
| Render install manifest | `km slack manifest [--app-name <name>]` |
| Ad-hoc invite by email | `km slack invite <email> [--channel <name|id>] [--external]` |
| Profile-driven auto-invite | `spec.cli.notifySlackInviteEmails: [<email>, ...]` |
| Scope drift diagnostic | `km doctor` adds `slack_users_read_email_scope` |

### Scopes carried by the manifest

Phase 72 requires the bot to have the `users:read.email` scope (for `users.lookupByEmail`). The
`km slack manifest` output is the full 13-scope union and also includes `files:read` — required
by Phase 75 (inbound file attachments) and enforced by `km doctor`'s inbound check. Installing or
reinstalling from this manifest is the clean way to pick up BOTH scopes at once. EXISTING
klankermaker installs provisioned before these phases do NOT have `users:read.email` (and may
also be missing `files:read` if never updated for Phase 75) and MUST be reinstalled with the
updated manifest.

### Installing into a new corporate workspace (cross-account / cross-machine)

Ordering matters — `km slack manifest` reads the bridge Lambda URL from SSM, which only exists
after `km init` deploys the regional infrastructure into the target account. So you cannot render
a usable manifest before the account is initialized, and you cannot finish the Slack app before
you have the manifest. The sequence resolves the chicken-and-egg:

> `km` reads all account state from SSM/AWS, so you can run these from any machine that has AWS
> credentials for the target account — it does NOT need to be the machine the code was built on.
> Just make sure that machine has a `km` binary built from this version (`make build`).

```bash
# 0. Point km at the target account (AWS creds/profile for the NEW account).
eval $(km env --aws-profile <your-new-account-profile>)   # or export AWS_PROFILE

# 1. Initialize regional infrastructure — this deploys the bridge Lambda and writes
#    {prefix}/slack/bridge-url to SSM. Required BEFORE the manifest can be rendered.
km init

# 2. Render the manifest (now that bridge-url exists in SSM).
km slack manifest > /tmp/km-app.json
python3 -m json.tool < /tmp/km-app.json   # sanity-check valid JSON

# 3. In Slack admin: Apps → Build → Create New App → From an app manifest →
#    select your workspace → paste /tmp/km-app.json → Next → Create.
#    Slack creates the app with all 13 scopes (incl. files:read + users:read.email).

# 4. Install the app to your workspace from the app config UI; copy the Bot User OAuth Token.

# 5. Initialize klankermaker's Slack integration with the token:
km slack init --bot-token xoxb-... --invite-email <your-email-in-the-corp-workspace>
# The orchestrator detects that --invite-email is a workspace member and uses a
# regular conversations.invite (no Slack Connect needed).

# 6. Verify:
km doctor       # all slack_* checks should pass (incl. slack_users_read_email_scope + files:read)
km slack test
```

If `km slack manifest` errors with "run km slack init first", you skipped step 1 — the bridge URL
isn't in SSM yet. Run `km init` (it's idempotent) and retry the manifest.

### Updating an existing PoC install (klankermaker workspace)

The existing PoC workspace was provisioned with the Phase 63 scope set; `users:read.email` is
absent. To enable Phase 72 features against the existing install:

```bash
# 1. Get the updated manifest
km slack manifest > /tmp/km-app.json

# 2. In Slack admin → Apps → existing klankermaker app → App Manifest tab →
#    paste new manifest → Save Changes.
#    Slack will require an "Update Permissions" / app reinstall.

# 3. After reinstall, copy the new Bot User OAuth Token.

# 4. Rotate the token in klankermaker:
km slack rotate-token --bot-token xoxb-NEW-TOKEN

# 5. Verify:
km doctor
```

### `km slack invite` reference

Invite a user to a Slack channel by email. Auto-detects whether the email is a workspace member:
- **Native member** (in the workspace): `conversations.invite` (regular invite).
- **External** (not in workspace): in interactive sessions, prompts before sending Slack Connect
  (default N); in non-interactive sessions, returns `SkippedExternal` and exits with code 2 plus
  a follow-up command hint.

| Flag | Description |
|---|---|
| `--channel <name\|id>` | Channel name (`km-notifications`) or ID (`C012ABCDE3F`). Default: SSM-stored shared channel. |
| `--external` | Skip lookup; force Slack Connect invite (no prompt). |
| `--dry-run` | Read-only probe: look up the email and print whether it would be invited natively or via Slack Connect. Sends nothing, joins nothing, never prompts. Safe to run against a live workspace; needs no sandbox. |

Exit codes:
- `0` — InvitedDirect / InvitedConnect / AlreadyMember / any `--dry-run`
- `1` — Failed (Slack API error)
- `2` — SkippedExternal (non-interactive miss)

`--dry-run` is the recommended first check after install — it exercises the same auto-detect
orchestrator that `km create` uses, so you can confirm "is this address seen as a native member
or an outsider?" without sending invites or provisioning a sandbox. Example:

```bash
km slack invite teammate@newcorp.com --dry-run   # [dry-run] ... would invite via conversations.invite
km slack invite outsider@gmail.com  --dry-run    # [dry-run] ... NOT a workspace member — would require Slack Connect
```

### `km slack manifest` reference

Renders a deployment-specific Slack App manifest to stdout. The manifest is parameterized by
the install's `resource_prefix` and bridge Lambda Function URL.

| Flag | Description |
|---|---|
| `--app-name <name>` | Override the auto-derived name (default: `KlankerMaker-{resource_prefix}`; max 35 chars). |

Pipe to a file: `km slack manifest > app.json`. Paste into Slack admin → Apps → Build → New App
→ From manifest.

### Profile fields: `notifySlackInviteEmails` + `useSlackConnect`

The **primary operator** (the address set at `km slack init`, stored in SSM
`{prefix}/slack/invite-email`) is ALWAYS invited to each per-sandbox `#sb-{id}` channel at
`km create` time — a native workspace member via regular invite, an external operator via Slack
Connect. This is unchanged from prior behavior (just now auto-detected).

`notifySlackInviteEmails` adds MORE people beyond the primary operator. `useSlackConnect`
(default `true`) controls whether external addresses in that list are auto-invited via Slack
Connect or skipped.

```yaml
spec:
  cli:
    notifySlackEnabled: true
    notifySlackPerSandbox: true
    useSlackConnect: true            # default true; omit for the same behavior
    notifySlackInviteEmails:
      - alice@example.com   # workspace member → regular invite
      - bob@external.com    # not in workspace → auto Slack Connect (useSlackConnect true)
```

Behavior of the additional-folks list:
- Internal members: regular invite (silent success).
- External addresses, `useSlackConnect: true` (default): auto-invited via Slack Connect, no
  warning. A Connect failure (e.g. free-tier workspace) is logged as a fail-soft warning.
- External addresses, `useSlackConnect: false`: skipped with a stderr warning; `km create`
  continues (fail-soft). Follow up with
  `km slack invite --external bob@external.com --channel sb-{id}`.
- Empty/unset list: no-op (the primary operator is still invited).

Validation: `notifySlackInviteEmails` requires `notifySlackEnabled: true` (validation rule SE1).
`useSlackConnect` has no validation rule — it is inert when the list is empty.

### `km doctor` new check: `slack_users_read_email_scope`

| Status | Meaning |
|---|---|
| OK | Bot has the scope |
| WARN | Scope missing — run `km slack manifest`, update Slack App scopes, reinstall, rotate token |
| SKIP | Slack not configured |

### Troubleshooting

| Symptom | Fix |
|---|---|
| `km slack invite` returns `missing_scope` | Bot doesn't have `users:read.email`. Run `km doctor`; remediation in WARN message. |
| `km create` shows `[warn] bob@external.com is not a member ... (useSlackConnect: false)` | Expected when `useSlackConnect: false` and the address isn't a workspace member. Either set `useSlackConnect: true` (default) to auto-Connect, or run `km slack invite --external` afterward. |
| `km slack manifest` says "run km slack init first" | SSM `{prefix}/slack/bridge-url` is unset. Run `km init` once first. |
| Manifest pasted into Slack admin but app rejected | Confirm the JSON is valid (`python3 -m json.tool`). Confirm `display_information.name` ≤ 35 chars. |
| `km slack invite` against private channel returns `not_in_channel` | Bot was kicked or never joined. The command auto-joins; if it still fails, manually `/invite @KlankerMaker` from Slack first. |
| `km doctor` shows `channel_not_found` / 502 for shared channel right after reinstall | Expected. When reinstalling the app from an updated manifest, Slack ejects the bot from all pre-existing channels. Re-invite the bot with `/invite @KlankerMaker` from Slack in each channel, or run `km slack init --force` to restore the bridge. |

### Known reinstall consequence: bot ejected from channels

When you reinstall the Slack app from an updated manifest (the Phase 72 manifest generator path), Slack ejects the bot from every pre-existing channel it was a member of — including the shared channel stored at `km slack init` time. After the reinstall and token rotation, run `/invite @KlankerMaker` in your shared channel from Slack (or re-run `km slack init --force`) to restore bridge posting. This is also why `km doctor` may transiently report a 502 `channel_not_found` on the `slack_bot_in_shared_channel` check immediately after a reinstall + rotate-token cycle; it resolves once the bot is re-invited.

---

## Phase 91: Polite-bot — @-mention-only mode for shared/override channels

### Overview

Before Phase 91, the km-slack bridge reacted to and dispatched every inbound message in every
channel the bot was a member of. In a corporate Slack workspace where the bot shares a team
channel like `#km-notifications` with many participants, this made the bot noisy — it reacted
with 👀 to every message, even those not intended for it. Phase 91 introduces polite-bot mode:
for shared (Mode 1) and operator-controlled override (Mode 3) channels, the bridge only reacts
and dispatches when the message text contains `<@{bot_user_id}>` (a native Slack @-mention of
the bot). Per-sandbox `#sb-{id}` channels (Mode 2) keep the existing every-message behaviour
because the bot is the primary participant in those single-purpose channels. A new profile knob
lets operators override the smart per-mode default in either direction.

### Per-mode defaults

| Mode | Channel | Default `mention_only` | Why |
|------|---------|------------------------|-----|
| 1 | Shared (e.g. `#km-notifications`) | `true` | Reduce noise in multi-participant corporate channels |
| 2 | Per-sandbox `#sb-{id}` | `false` | Bot is the primary participant; every message is relevant |
| 3 | Operator override (`notifySlackChannelOverride`) | `true` | Operator controls the channel; assume shared context |

The effective value is: `notifySlackInboundMentionOnly` (if explicitly set) else the mode
default above.

### Profile field reference

```yaml
spec:
  cli:
    notifySlackEnabled: true
    # Tri-state *bool: nil = use mode default (table above), true = force polite, false = force chatty.
    # Omit the field entirely to accept the per-mode smart default (recommended).
    notifySlackInboundMentionOnly: true
```

Field: `spec.cli.notifySlackInboundMentionOnly` (`*bool`, optional). Sibling of
`notifySlackEnabled`, `notifySlackPerSandbox`, `notifySlackChannelOverride` (introduced
Phase 72). Validation: any bool is accepted; no semantic error for Mode 2 + `true` (the
operator is intentionally making a per-sandbox channel polite, e.g. multiple operators sharing
one sandbox).

### Operator override examples

**Default — accept per-mode smart defaults (no field needed):**

```yaml
spec:
  cli:
    notifySlackEnabled: true
    notifySlackPerSandbox: true
    # mention_only: nil → Mode 2 default false → every-message behaviour
```

**Force polite in a per-sandbox channel (rare — useful when multiple operators share a
sandbox and want to reduce chatter):**

```yaml
spec:
  cli:
    notifySlackEnabled: true
    notifySlackPerSandbox: true
    notifySlackInboundMentionOnly: true   # override Mode 2 default
```

**Force chatty in a shared channel (testing / single-operator installs only):**

```yaml
spec:
  cli:
    notifySlackEnabled: true
    notifySlackChannelOverride: "#km-test-${KM_RESOURCE_PREFIX}"
    notifySlackInboundMentionOnly: false  # override Mode 3 default
```

**Polite-mode on an operator-override channel (Mode 3, accepting the default):**

```yaml
spec:
  cli:
    notifySlackEnabled: true
    notifySlackChannelOverride: "#km-notifications"
    # mention_only: nil → Mode 3 default true → polite-bot behaviour
```

### Bridge env vars

The resolved effective value is compiled into the bridge Lambda environment block. Both
env vars are populated automatically by `km init` (Phase 91.1):

| Env var | Values | Default | Source |
|---------|--------|---------|--------|
| `KM_SLACK_MENTION_ONLY` | `"true"` / `"false"` | `"false"` | `km-config.yaml` key `slack.mention_only` (Phase 91.1 — formerly required an `export`) |
| `KM_SLACK_BOT_USER_ID` | Slack user ID (e.g. `U03ABCDEF`) | `""` | SSM `{prefix}slack/bot-user-id` (populated by `km slack init`); auto-read by `km init` |

**Polite vs chatty install-level default:**

Add to `km-config.yaml`:

```yaml
slack:
    mention_only: true     # polite-bot default for the whole install
    # mention_only: false  # chatty default — every message routed
    # (omit the slack: block entirely → terragrunt fallback "false" applies)
```

`km init` reads `slack.mention_only` and exports `KM_SLACK_MENTION_ONLY=true|false` into the
terragrunt subprocess. The bridge Lambda's environment block is updated on terragrunt apply.

**env-wins:** if `KM_SLACK_MENTION_ONLY` is already exported when `km init` runs and it
disagrees with `slack.mention_only`, km emits a one-line drift WARN to stderr and the env
value wins. Same semantics as `KM_REGION` / `KM_OPERATOR_EMAIL` / `KM_RESOURCE_PREFIX`.

**`KM_SLACK_BOT_USER_ID` auto-read:** `km init` reads `{prefix}slack/bot-user-id` from SSM and
exports it before invoking terragrunt — no operator `export` needed. First-install (param
not yet written) silently skips with an `[info]` line; the terragrunt fallback (`""`) applies
and the bridge falls back to a runtime `auth.test` call on cold-start, which still works but
is slower.

**Per-profile override** (`notifySlackInboundMentionOnly`) still wins over the install default
at sandbox-create time — profiles can flip polite/chatty per sandbox.

### `km doctor` check: `slack_bot_user_id_cached`

| Status | Meaning |
|--------|---------|
| OK | `{prefix}/slack/bot-user-id` SSM parameter is present and non-empty |
| WARN | Parameter missing or empty AND at least one local profile resolves to `mention_only=true`; bridge would fall back to a runtime `auth.test` call on cold-start |
| SKIP | Slack not configured / no profiles with mention-only enabled |

Remediation: `km slack init --force` — re-runs `auth.test`, writes `bot_user_id` to SSM.

### Rollout sequence

After upgrading `km` to a Phase 91.1 build:

```sh
make build                                                    # rebuild km binary
km slack init --force                                         # re-runs auth.test, caches bot_user_id at SSM
# Edit km-config.yaml — add (or change) the slack block:
#   slack:
#       mention_only: true
km init --dry-run=false                                       # apply terragrunt — Lambda env block picks up KM_SLACK_MENTION_ONLY + KM_SLACK_BOT_USER_ID
km doctor                                                     # verifies slack_bot_user_id_cached OK

# For sandboxes created before Phase 91 (existing #sb-{id} channels):
km destroy <sandbox-id> --remote --yes && km create <profile>  # picks up new schema field
```

No `export KM_SLACK_*` shell incantations are required after Phase 91.1 — both env vars flow
through `km init` automatically:

1. `slack.mention_only` from `km-config.yaml` → `KM_SLACK_MENTION_ONLY` (drift-WARN aware).
2. `{prefix}slack/bot-user-id` from SSM → `KM_SLACK_BOT_USER_ID` (env-wins; first-install skips).

`KM_SLACK_MENTION_ONLY` is install-level: it controls the Lambda default for sandboxes that
don't set `notifySlackInboundMentionOnly` explicitly. Setting it to `"true"` makes
Mode 1/3 channels polite and leaves Mode 2 (`#sb-{id}`) every-message unless the profile
forces otherwise.

**Why `km init` and not `km init --sidecars`:** `--sidecars` only rebuilds the operator-side
km binary plus sidecar zips and forces a Lambda cold-start — it does NOT run terragrunt
apply. The bridge Lambda's environment block is owned by the
`infra/modules/lambda-slack-bridge/v1.0.0/` Terraform module, which only updates on
terragrunt apply (full `km init`). So a flip of `slack.mention_only` in `km-config.yaml`
requires `km init --dry-run=false`, not `km init --sidecars`.

### Troubleshooting

| Symptom | Fix |
|---------|-----|
| Bot still 👀-reacts to non-mention messages in a shared channel | Check `KM_SLACK_MENTION_ONLY` in Lambda env: `aws lambda get-function-configuration --function-name ${KM_RESOURCE_PREFIX}-slack-bridge \| jq .Environment.Variables.KM_SLACK_MENTION_ONLY`. If `"false"` or absent, confirm `slack.mention_only: true` is in `km-config.yaml` and re-run `km init --dry-run=false`. |
| `km doctor` reports `slack_bot_user_id_cached` WARN | Run `km slack init --force` to re-cache the bot user ID; verify with `aws ssm get-parameter --name /${KM_RESOURCE_PREFIX}/slack/bot-user-id --query Parameter.Value --output text`. |
| `km init` prints `WARN: KM_SLACK_MENTION_ONLY=... (env) overrides km-config.yaml slack.mention_only=...` | Operator has an old `export KM_SLACK_MENTION_ONLY=...` in their shell that disagrees with the yaml. Either `unset KM_SLACK_MENTION_ONLY` (yaml wins) or update the yaml to match the export. |
| Bot ignores a message that DOES @-mention it | Confirm the user typed `@KlankerMaker` in the Slack UI (not a literal `<@U...>` string). Slack only canonicalises to `<@U...>` for real app/user mentions in the Slack UI, not for raw text strings. |
| Mention-scan never fires; cold-start is slow | `KM_SLACK_BOT_USER_ID` is empty in Lambda env. Confirm `km slack init` has been run (writes SSM) — `km init` auto-reads SSM on next apply. |
| Mode 2 (`#sb-{id}`) channel is polite when it should be chatty | Profile has `notifySlackInboundMentionOnly: true` set explicitly. Remove the field to restore the Mode 2 every-message default. |
| `km init --sidecars` doesn't change the polite/chatty behaviour | Expected: `--sidecars` only rebuilds km + sidecar binaries and forces a Lambda cold-start. The Lambda environment block (where `KM_SLACK_MENTION_ONLY` lives) only updates on full terragrunt apply: `km init --dry-run=false`. |

### Out of scope

The following capabilities are explicitly deferred to a future phase:

- **Per-channel runtime override via slash command** (e.g. `/km mention-only on`)
- **Display-name mention detection** (`@klankermaker` typed without Slack canonicalising to
  `<@U...>`) — Slack canonicalises on send so this is not expected to be a gap in practice
- **Reactions-as-actions integration** — different phase
