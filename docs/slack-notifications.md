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
| Rate limit: bridge returns 503 | Slack returned 429; all retries exhausted | Reduce notification frequency via `notifyCooldownSeconds` in the profile; the bridge retries 1s→2s→4s before surfacing 503 |

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
