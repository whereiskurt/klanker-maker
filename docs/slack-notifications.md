# Slack Notifications Guide

> **App permissions:** for a per-scope breakdown of the Slack bot token scopes and
> events km requests (and what breaks without each), see
> `docs/slack-app-permissions.md`.

> **NOTE — Phase 92 (2026-05-31):** All `spec.cli.notify*` fields moved under the
> structured `spec.notification:` block (`notification.events.*`,
> `notification.email.*`, `notification.slack.*` with `slack.inbound`,
> `slack.transcript`, `slack.invites` sub-blocks). `spec.cli.vscodeEnabled` moved
> to `spec.runtime.vscode.enabled`. **Sandbox-side env var names are UNCHANGED** —
> only the YAML surface changed. See `92-CONTEXT.md` § Phase Boundary for the full
> field-by-field mapping.

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
15. [Phase 91: Polite-bot](#phase-91-polite-bot----mention-only-mode-for-sharedoverride-channels)
16. [Phase 95: Federated relay](#phase-95-federated-bridge-relay--one-slack-app-many-km-installs)
17. [Phase 96: Default router](#phase-96--default-router-orphan-channel--mention-reply)
18. [Phase 99.1: Inbound DLQ](#phase-991--inbound-poison-message-dlq-fifo-wedge-fix)
19. [Phase 104: O(1) channel resolution](#phase-104--o1-channel-resolution-on-alias-reuse)

---

## Overview

Klanker provides Slack delivery alongside the existing email notification path:

- **Same triggers:** `Notification` (Claude Code permission prompt) and `Stop` (idle timeout) events, gated by `notification.events.onPermission` and `notification.events.onIdle`.
- **Parallel channels:** email and Slack run simultaneously unless you explicitly disable one via `notification.email.enabled: false`.
- **Signed payloads:** the `km-slack` binary on the sandbox constructs an Ed25519-signed envelope and POSTs it to the bridge Lambda Function URL. The Lambda verifies the signature using the sandbox's public key from DynamoDB before forwarding to the Slack Web API.
- **Bot token isolation:** the Slack bot token is stored in SSM as a SecureString (KMS-encrypted). Only the bridge Lambda and the operator (via `km slack init` / `km slack status`) can read it.
- **Operator channels via Slack Connect:** klankermaker.ai owns the Slack workspace. The operator is invited to the notification channel(s) via `conversations.inviteShared` (Slack Connect). The operator accepts the invite from their own Slack workspace — no workspace credential sharing required.

---

## Channel Modes

| Mode | When | Channel name | Lifecycle |
|------|------|-------------|-----------|
| **Shared (default)** | `notification.slack.enabled: true`, neither per-sandbox nor override set | `#km-notifications` (or the name set during `km slack init`) | Permanent; shared across all sandboxes |
| **Per-sandbox** | `notification.slack.perSandbox: true` | `#sb-{alias}` (or `#sb-{sandbox-id}`), sanitized | Created at `km create`; archived at `km destroy` when `notification.slack.archiveOnDestroy: true` |
| **Per-sandbox, custom name** | `notification.slack.perSandbox: true` + `notification.slack.channelName: <name>` | `<name>` verbatim (sanitized), **no `sb-` prefix**; `{alias}`/`{id}` tokens supported | Same lifecycle as per-sandbox |
| **Override** | `notification.slack.channelOverride: "C..."` | Any existing channel the bot has been invited to | Unmanaged — operator is responsible for channel lifecycle |

Modes are mutually exclusive: `notification.slack.perSandbox: true` and `notification.slack.channelOverride: <set>` at the same time is a validation error.

**Custom per-sandbox channel name.** By default the per-sandbox channel is force-prefixed `sb-` (`#sb-{alias}`). Set `notification.slack.channelName` to choose your own name — used verbatim after sanitization (lowercase, non-`[a-z0-9_]`→`-`, ≤80 chars) with **no `sb-` prefix**, so you own the namespacing. It supports `{profile}` (the profile's `metadata.name`), `{alias}`, and `{id}` token substitution (`{alias}` falls back to the sandbox ID when no `--alias` is set):

```yaml
spec:
  notification:
    slack:
      enabled: true
      perSandbox: true
      channelName: "sb-{profile}-{alias}"   # → #sb-desktop-myml   (the shipped profiles use this)
      # other forms: "proj-{alias}" → #proj-myml ; literal "acme-desktops" → #acme-desktops
```

The built-in `profiles/*.yaml` that enable `perSandbox` set `channelName: "sb-{profile}-{alias}"` so channels are named after the profile *and* stay unique per sandbox (preserving 1:1 inbound routing and archive-on-destroy). A `sb-{profile}`-only name would put every sandbox of a profile in one shared channel — convenient for a single box, but it breaks inbound 1:1 routing and archive-on-destroy when more than one runs at once.

`channelName` requires `perSandbox: true` (warning otherwise) and is mutually exclusive with `channelOverride` (error). The channel ID is persisted at `km create`, so archive/lookup on `km destroy` work regardless of the chosen name.

---

## Prerequisites

1. **Pro Slack workspace** at klankermaker.ai (or a test workspace). Slack Connect (`conversations.inviteShared`) requires Pro tier or higher; the free tier returns `not_allowed_token_type`.

2. **Custom Slack App** installed in the workspace with the full bot-scope set (15 scopes today). The canonical, version-current scope list is rendered by `km slack manifest`; paste the output into Slack admin → Apps → Build → New App → From manifest. Maintaining a hand-curated scope list in docs invariably drifts — see § Security Model § Complete bot scope inventory for the audit-friendly per-scope justification.

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

All new fields are under `spec.notification` (Phase 92; formerly `spec.cli`). All are optional with the defaults shown.

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `notification.email.enabled` | `bool*` | `true` | Set `false` to skip email dispatch when Slack is on. Omitting the field preserves the default (email always fires). |
| `notification.slack.enabled` | `bool*` | `false` | Enable Slack delivery for events already gated by `notification.events.onPermission` / `notification.events.onIdle`. |
| `notification.slack.perSandbox` | `bool*` | `false` | Create `#sb-{sandbox-id}` at `km create`; archive at `km destroy`. Ignored when `notification.slack.enabled` is false. |
| `notification.slack.channelOverride` | `string` | `""` | Hard-pin notifications to an existing Slack channel ID (format: `^C[A-Z0-9]+$`). Overrides both shared and per-sandbox modes. The bot must be a member. |
| `notification.slack.channelName` | `string` | `""` | Custom name for the auto-created per-sandbox channel (requires `perSandbox: true`). Verbatim (sanitized), no `sb-` prefix; supports `{profile}`/`{alias}`/`{id}` tokens. Empty = default `sb-{alias}`. Mutually exclusive with `channelOverride`. |
| `notification.slack.archiveOnDestroy` | `bool*` | `true` | Per-sandbox channels only. Set `false` to preserve the channel and its history after `km destroy`. |

`bool*` indicates the field is a pointer (`*bool`) in the schema, allowing three states: unset (nil → default), `true`, `false`. Omitting the field is different from `false` for `notification.email.enabled` (omit = email on; `false` = email off).

---

## Validation Rules

`km validate` enforces these rules. Errors exit 1; warnings exit 0 with a `WARN:` prefix.

| Rule | Condition | Severity | Message |
|------|-----------|----------|---------|
| Mutual exclusion | `notification.slack.perSandbox: true` AND `notification.slack.channelOverride` set | **Error** | "slack.perSandbox and slack.channelOverride are mutually exclusive" |
| Dead per-sandbox | `notification.slack.perSandbox: true` AND `notification.slack.enabled: false` | Warning | "slack.perSandbox has no effect when slack.enabled is false" |
| Dead archive | `notification.slack.archiveOnDestroy` set AND `notification.slack.perSandbox: false` | Warning | "slack.archiveOnDestroy has no effect unless slack.perSandbox is true" |
| Channel ID format | `notification.slack.channelOverride` does not match `^C[A-Z0-9]+$` | **Error** | "slack.channelOverride must match C[A-Z0-9]+" |
| channelName + override | `notification.slack.channelName` AND `notification.slack.channelOverride` both set | **Error** | "channelName and channelOverride are mutually exclusive" |
| Dead channelName | `notification.slack.channelName` set AND `notification.slack.perSandbox: false` | Warning | "channelName is only meaningful when perSandbox: true" |
| No delivery channel | `notification.slack.enabled: true` AND all three mode fields absent/false/empty AND shared channel not provisioned | Warning | "slack.enabled is true but no delivery channel configured" |

---

## Example Profiles

### Shared mode (default): notify all sandboxes to one channel

```yaml
spec:
  notification:
    events:
      onIdle: true
      cooldownSeconds: 60
    email:
      enabled: false   # email off; Slack only
    slack:
      enabled: true
```

### Per-sandbox channel with archive on destroy

```yaml
spec:
  notification:
    events:
      onPermission: true
      onIdle: true
      cooldownSeconds: 0
    email:
      enabled: false
    slack:
      enabled: true
      perSandbox: true
      archiveOnDestroy: true   # default; explicit here for clarity
```

### Override mode: pin to an existing channel

```yaml
spec:
  notification:
    events:
      onIdle: true
    slack:
      enabled: true
      channelOverride: "C01ABC1234DEF"   # bot must be invited
```

---

## Architecture

The bridge Lambda is the only component that ever holds the bot token. It serves
**two HTTPS paths** on a single Function URL, each authenticated with a different
secret:

- `POST /`        — outbound from sandboxes (Ed25519 envelope, see `pkg/slack/payload.go`)
- `POST /events`  — inbound from Slack Events API (HMAC-SHA256 over `/km/slack/signing-secret`)

End-to-end flow, with every Lambda-side step that has shipped through Phase 91:

```
                                       ┌──────────────────────────────────────┐
                                       │  Slack workspace                     │
                                       │   #km-notifications | #sb-{id} |     │
                                       │   operator-override channel          │
                                       └───────────┬──────────────────────────┘
                                                   │ ▲
                                                   │ │ chat.postMessage / chat.update
                                            Events │ │ reactions.add(👀)
                                            API    │ │ files.getUploadURLExternal
                                                   │ │ files.completeUploadExternal
                                                   │ │ conversations.{create,archive,invite,inviteShared,join}
                                                   │ │ users.lookupByEmail
                                                   ▼ │
┌────────────────────────────┐          ┌────────────────────────────────────────────────────-──┐
│  Sandbox (EC2 / Docker)    │          │   {prefix}-slack-bridge   Lambda                      │
│                            │          │   (provided.al2023, arm64, 1024MB, 60s)               │
│  ┌──────────────────────┐  │  HTTPS   │   Function URL — AuthType: NONE (signature is auth)   │
│  │ km-notify-hook       │  │   POST   │                                                       │
│  │ (Notification/Stop)  │  │  ──────▶ │  ┌────────────────────────────────────────────────┐   │
│  └─────────┬────────────┘  │   /      │  │ POST /     (outbound from sandbox)             │   │
│            │               │          │  │   1. Parse JSON envelope                       │   │
│  ┌─────────▼────────────┐  │          │  │   2. ±5 min timestamp window                   │   │
│  │ /opt/km/bin/km-slack │  │          │  │   3. PutItem km-slack-nonces  (replay dedup)   │   │
│  │  - Ed25519 sign      │  │          │  │   4. GetItem km-identities    (sender pubkey)  │   │
│  │  - retry 1/2/4s      │  │          │  │   5. ed25519.Verify(canonical bytes)           │   │
│  └─────────┬────────────┘  │          │  │   6. Channel ownership vs km-sandboxes         │   │
│            │               │          │  │   7. Action authz (sandbox: post/upload/       │   │
│            │               │          │  │      permalink/update; operator: archive/test) │   │
│ ┌──────────▼─────────────┐ │          │  │   8. GetParameter bot-token (15-min cache)     │   │
│ │ PostToolUse hook       │ │          │  │   9. Dispatch → Slack Web API                  │   │
│ │  → stream lines        │ │          │  │      (chat.postMessage / files.* / etc.)       │   │
│ │  → record offsets in   │ │          │  └────────────────────────────────────────────────┘   │
│ │    km-slack-stream-msg │ │          │                                                       │
│ │  → upload at Stop      │ │          │  Slack ──HTTPS POST──▶                                │
│ │    via S3 + bridge     │ │          │                                                       │
│ └────────────────────────┘ │          │  ┌────────────────────────────────────────────────┐   │
│            ▲               │          │  │ POST /events   (inbound from Slack)            │   │
│            │ SQS long-poll │          │  │   1. HMAC-SHA256 vs /km/slack/signing-secret   │   │
│            │ ReceiveMessage│          │  │   2. ±5 min window                             │   │
│ ┌──────────┴─────────────┐ │          │  │   3. Subtype allow-list (""/thread_broadcast)  │   │
│ │ km-slack-inbound-      │ │   SQS    │  │   4. bot_user_id self-message filter           │   │
│ │ poller (systemd)       │ │  FIFO    │  │   5. POLITE-BOT mention scan ────────────────► │   │
│ │  - dispatch claude/    │ │  ◀────── │  │      ┌─ Mode 1 shared       → require @bot  │  │   │
│ │    codex per agent_    │ │ Send-    │  │      ├─ Mode 2 #sb-{id}     → every message │  │   │
│ │    type in DDB row     │ │ Message  │  │      ├─ Mode 3 override     → require @bot  │  │   │
│ │  - KM_SLACK_THREAD_TS  │ │          │  │      └─ thread-bypass: bot-engaged threads  │  │   │
│ │  - PostToolUse stream  │ │          │  │         (Phase 91.3) always route through   │  │   │
│ │  - mirror /events      │ │          │  │   6. Channel → sandbox (km-sandboxes GSI       │   │
│ │    files to /workspace │ │          │  │      slack_channel_id-index)                   │   │
│ │    /.km-slack/attach/  │ │          │  │   7. UpdateItem km-slack-threads               │   │
│ └────────────────────────┘ │          │  │      (channel, thread_ts, agent_type,          │   │
│                            │          │  │       session_id, last_assistant_msg[:500])    │   │
│  ─ private keys ─          │          │  │   8. SendMessage → per-sandbox FIFO            │   │
│  /sandbox/{id}/signing-key │          │  │   9. (file_share) GET files.info + PutObject   │   │
│  (KMS SecureString;        │          │  │      → s3://artifacts/slack-inbound/{id}/...   │   │
│   sandbox IAM role only)   │          │  │  10. reactions.add(👀) — gated on              │   │
│                            │          │  │      KM_SLACK_REACT_ALWAYS (install default)   │   │
└────────────────────────────┘          │  │      ◀ per-sandbox slack_react_always row on   │   │
                                        │  │        km-sandboxes wins (Phase 91.5)          │   │
                                        │  └────────────────────────────────────────────────┘   │
                                        └──────┬────────────────┬───────────────────┬───────────┘
                                               │                │                   │
                              ┌────────────────▼─┐ ┌────────────▼──────┐ ┌──────────▼───────────┐
                              │ SSM (KMS)        │ │ DynamoDB          │ │ S3 artifacts bucket  │
                              │  /{prefix}/slack │ │  km-identities    │ │  transcripts/{id}/   │
                              │   bot-token      │ │  km-sandboxes     │ │  slack-inbound/{id}/ │
                              │   signing-secret │ │  km-slack-nonces  │ │   (30-day lifecycle) │
                              │   bot-user-id    │ │  km-slack-threads │ └──────────────────────┘
                              │   bridge-url     │ │  km-slack-stream- │
                              │   workspace      │ │   messages        │
                              │   shared-channel-│ └───────────────────┘
                              │   id, invite-    │
                              │   email          │
                              └──────────────────┘
```

### Lambda environment block

Set by the `infra/modules/lambda-slack-bridge/v1.0.0/` module on every `km init`
(full terragrunt apply — `km init --sidecars` only refreshes the zip):

| Env var | Source | Purpose |
|---------|--------|---------|
| `KM_RESOURCE_PREFIX` | `km-config.yaml` | Multi-install isolation prefix |
| `KM_IDENTITIES_TABLE` | terragrunt | DDB table for sender pubkey lookup |
| `KM_SANDBOX_TABLE_NAME` | terragrunt | DDB table for channel→sandbox + react-always |
| `KM_NONCE_TABLE` | terragrunt | DDB table for replay dedup (TTL ~5 min) |
| `KM_SLACK_THREADS_TABLE` | terragrunt | DDB table for thread→session map (Phase 67) |
| `KM_BOT_TOKEN_PATH` | terragrunt | SSM path (default `/{prefix}/slack/bot-token`) |
| `KM_SIGNING_SECRET_PATH` | terragrunt | SSM path for inbound HMAC verification |
| `KM_ARTIFACTS_BUCKET` | terragrunt | S3 bucket for transcripts + inbound files |
| `KM_SLACK_ACK_EMOJI` | terragrunt | Reaction name (default `eyes`; no colons) |
| `KM_SLACK_MENTION_ONLY` | `km-config.yaml` → `slack.mention_only` (Phase 91.1) | Install-level polite-bot default |
| `KM_SLACK_BOT_USER_ID` | SSM `{prefix}/slack/bot-user-id` (auto-read by `km init`) | Polite-bot mention scan target |
| `KM_SLACK_REACT_ALWAYS` | `km-config.yaml` → `slack.react_always` (Phase 91.4) | Install-level 👀-on-replies default |
| `TOKEN_ROTATION_TS` | bumped by `km slack rotate-token` | Forces cold-start to invalidate 15-min cache |

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
| `KM_NOTIFY_EMAIL_ENABLED` | profile `spec.notification.email.enabled` | `1` or `0`; controls email dispatch in `km-notify-hook` |
| `KM_NOTIFY_SLACK_ENABLED` | profile `spec.notification.slack.enabled` | `1` or `0`; controls whether `km-slack` is invoked |
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
| `km create` fails with `name_taken` (per-sandbox mode) | Another channel has the same sanitized name | Use `--alias` to change the sandbox name, or set `notification.slack.channelOverride` to an existing channel |
| Override mode: "bot is not a member" | Bot was not invited to the override channel | In Slack: open the channel → Add people → invite the km bot app |
| Hook fires, no Slack message appears | Bridge Lambda not deployed | Run `km init` then `km slack init`; check `/km/slack/bridge-url` via `km slack status` |
| Hook fires on an existing sandbox, no Slack | Existing sandboxes lack `km-slack` binary and env vars | Run `km destroy` then `km create` to reprovision with the binary |
| `km doctor` reports stale Slack channel | A destroyed sandbox left a non-archived channel | Archive manually in the Slack UI, or remove the `slack_channel_id` attribute from the DynamoDB sandbox record |
| `km slack test` returns 401 | Bot token expired or revoked | Rotate: `km slack rotate-token --bot-token "$NEW_TOKEN"` (or legacy `km slack init --force --bot-token`) |
| Bridge Lambda returns 403 | Signature verification failed (clock skew > 5 min, wrong key) | Ensure sandbox system clock is synced (chronyc / timedatectl); verify `km-identities` DynamoDB record for the sandbox exists |
| Rate limit: bridge returns 503 | Slack returned 429 (rate limit) | Reduce notification frequency via `notification.events.cooldownSeconds` in the profile |
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

Audit-friendly per-scope table (15 scopes as of Phase 75 + Phase 72 + the
`groups:read` follow-up):

| Scope | Slack API methods used | Why klanker needs it | Notes |
|---|---|---|---|
| `chat:write` | `chat.postMessage`, `chat.update` | All notification, transcript, reply, and operator-test messages | Primary path — no alternative |
| `channels:manage` | `conversations.create`, `conversations.archive`, `conversations.invite` (public) | Per-sandbox public channel lifecycle and operator invite at `km create` | Required for `notification.slack.perSandbox: true` and `km slack invite` |
| `channels:join` | `conversations.join` | Self-rescue when the bot is ejected from a public channel during an app reinstall | Avoids requiring a human `/invite` after every token rotation |
| `channels:read` | `conversations.info`, `conversations.list` | `km doctor` channel-name resolution; `km slack invite` channel lookup; bridge channel-membership probes | Read-only metadata on public channels |
| `channels:history` | Events `message.channels` delivery; paginated reads | Inbound chat from public channels (poller consumes from per-sandbox SQS) | Pairs with the `message.channels` event subscription |
| `groups:write` | `conversations.create` (private), `conversations.archive` (private), `conversations.invite` (private) | Per-sandbox private channel lifecycle when operator pre-creates private channels | |
| `groups:read` | `conversations.info`, `conversations.list?types=private_channel` | `km doctor` and `km slack invite` against private and Slack Connect channels | Added as a Phase 72 follow-up after a reinstall produced `channel_not_found` for the shared Connect channel |
| `groups:history` | Events `message.groups` delivery; paginated reads | Inbound chat from private channels | Pairs with the `message.groups` event subscription |
| `conversations.connect:write` | `conversations.inviteShared` | Slack Connect invites for external operators and the auto-invite list when `notification.slack.invites.useConnect: true` | Requires Pro workspace tier; gracefully fails open on free tier |
| `reactions:read` | (future) reaction-triggered session fork | Forward-compatibility seam for the planned reaction-fork feature; not actively consumed today | Removing this scope today blocks only the future fork feature |
| `reactions:write` | `reactions.add` | 👀 ACK on every accepted inbound Slack message (user feedback that the sandbox saw the message) | Independent of message delivery — failure logged but does not block reply |
| `files:write` | `files.getUploadURLExternal`, `files.completeUploadExternal` | End-of-response transcript upload (gzipped JSONL) into the per-sandbox thread | Required only for `notification.slack.transcript.enabled: true` |
| `files:read` | `files.info`; private-URL download with the bot token | Download user-attached files from inbound posts into `/workspace/.km-slack/attachments/` | Required for inbound file-attachment support (Phase 75) |
| `users:read.email` | `users.lookupByEmail` | Auto-detect whether an invite address is a workspace member (regular invite) or external (Slack Connect); used by `km slack init`, `km slack invite`, and the `km create` auto-invite loop | Resolves explicit operator-provided emails only — never enumerates the workspace directory |
| `users:read` | (companion to `users:read.email`) | **Required companion** of `users:read.email` — Slack treats `.email` as an add-on, so `users.lookupByEmail` only works when both are granted and the manifest must list both | Without it, the email lookup fails even though `users:read.email` appears granted (see `docs/slack-app-permissions.md`) |

#### Scopes deliberately NOT requested

A "negative-scope" inventory — adjacent-looking scopes that are absent from
the manifest, with the rationale:

| Scope not requested | Reasoning |
|---|---|
| Any User Token Scope | Integration is purely server-to-server; no user-impersonation. The manifest declares only Bot Token Scopes. |
| Legacy `bot` scope | Deprecated by Slack — granular bot scopes only. |
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
| Slack channels `#sb-{id}` | Per-turn streaming, final transcript upload, operator chat | Per-workspace retention policy; channel archived (not deleted) at `km destroy` unless `notification.slack.archiveOnDestroy: false` | Slack-side |

⚠ **Transcripts contain whatever Claude saw.** Operator-side compliance
(HIPAA, PCI, SOC 2 evidence handling) requires owner review of the artifacts
bucket lifecycle and the Slack workspace retention policy. Klanker does not
redact transcripts; do not enable `notification.slack.transcript.enabled` for sandboxes
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

Add to your profile under `spec.notification`:

```yaml
spec:
  notification:
    email:
      enabled: false
    slack:
      enabled: true
      perSandbox: true
      archiveOnDestroy: true
      inbound:
        enabled: true
```

`notification.slack.inbound.enabled: true` requires `notification.slack.enabled: true` AND
`notification.slack.perSandbox: true`. It is **incompatible with**
`notification.slack.channelOverride` — channel-to-sandbox routing requires 1:1 mapping
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
| `notification.slack.transcript.enabled` | bool | `false` | Per-turn streaming + final upload to per-sandbox Slack thread |

**Validation rules:**
- Requires `notification.slack.enabled: true` AND `notification.slack.perSandbox: true`
- Incompatible with `notification.slack.channelOverride`

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
`notification.slack.perSandbox: true` are shared with the operator via Slack
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
  `notification.slack.channelOverride` to a host-workspace channel ID) — note
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
`spec.notification.slack.inbound.enabled: true`.

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
  `spec.agent.default: codex`, verifies the installed Codex binary supports
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
| Profile-driven auto-invite | `spec.notification.slack.invites.emails: [<email>, ...]` |
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

### Profile fields: `notification.slack.invites.emails` + `notification.slack.invites.useConnect`

The **primary operator** (the address set at `km slack init`, stored in SSM
`{prefix}/slack/invite-email`) is ALWAYS invited to each per-sandbox `#sb-{id}` channel at
`km create` time — a native workspace member via regular invite, an external operator via Slack
Connect. This is unchanged from prior behavior (just now auto-detected).

`notification.slack.invites.emails` adds MORE people beyond the primary operator.
`notification.slack.invites.useConnect` (default `true`) controls whether external addresses in
that list are auto-invited via Slack Connect or skipped.

```yaml
spec:
  notification:
    slack:
      enabled: true
      perSandbox: true
      invites:
        useConnect: true            # default true; omit for the same behavior
        emails:
          - alice@example.com   # workspace member → regular invite
          - bob@external.com    # not in workspace → auto Slack Connect (useConnect true)
```

Behavior of the additional-folks list:
- Internal members: regular invite (silent success).
- External addresses, `invites.useConnect: true` (default): auto-invited via Slack Connect, no
  warning. A Connect failure (e.g. free-tier workspace) is logged as a fail-soft warning.
- External addresses, `invites.useConnect: false`: skipped with a stderr warning; `km create`
  continues (fail-soft). Follow up with
  `km slack invite --external bob@external.com --channel sb-{id}`.
- Empty/unset list: no-op (the primary operator is still invited).

Validation: `notification.slack.invites.emails` requires `notification.slack.enabled: true`
(validation rule SE1). `invites.useConnect` has no validation rule — it is inert when the list
is empty.

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
| `km create` shows `[warn] bob@external.com is not a member ... (useConnect: false)` | Expected when `invites.useConnect: false` and the address isn't a workspace member. Either set `notification.slack.invites.useConnect: true` (default) to auto-Connect, or run `km slack invite --external` afterward. |
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
| 3 | Operator override (`notification.slack.channelOverride`) | `true` | Operator controls the channel; assume shared context |

The effective value is: `notification.slack.inbound.mentionOnly` (if explicitly set) else the
mode default above.

### Profile field reference

```yaml
spec:
  notification:
    slack:
      enabled: true
      inbound:
        # Tri-state *bool: nil = use mode default (table above), true = force polite, false = force chatty.
        # Omit the field entirely to accept the per-mode smart default (recommended).
        mentionOnly: true
```

Field: `spec.notification.slack.inbound.mentionOnly` (`*bool`, optional). Sibling of
`notification.slack.enabled`, `notification.slack.perSandbox`,
`notification.slack.channelOverride`. Validation: any bool is accepted; no semantic error for
Mode 2 + `true` (the operator is intentionally making a per-sandbox channel polite, e.g.
multiple operators sharing one sandbox).

### Operator override examples

**Default — accept per-mode smart defaults (no field needed):**

```yaml
spec:
  notification:
    slack:
      enabled: true
      perSandbox: true
      # inbound.mentionOnly: nil → Mode 2 default false → every-message behaviour
```

**Force polite in a per-sandbox channel (rare — useful when multiple operators share a
sandbox and want to reduce chatter):**

```yaml
spec:
  notification:
    slack:
      enabled: true
      perSandbox: true
      inbound:
        mentionOnly: true   # override Mode 2 default
```

**Force chatty in a shared channel (testing / single-operator installs only):**

```yaml
spec:
  notification:
    slack:
      enabled: true
      channelOverride: "#km-test-${KM_RESOURCE_PREFIX}"
      inbound:
        mentionOnly: false  # override Mode 3 default
```

**Polite-mode on an operator-override channel (Mode 3, accepting the default):**

```yaml
spec:
  notification:
    slack:
      enabled: true
      channelOverride: "#km-notifications"
      # inbound.mentionOnly: nil → Mode 3 default true → polite-bot behaviour
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

**Per-profile override** (`notification.slack.inbound.mentionOnly`) still wins over the install default
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

> **Phase 105 scoped shortcut (config-key-only edit):** for a pure `slack.*` config key
> change with no code change needed:
> ```bash
> km init --slack --dry-run=false   # applies lambda-slack-bridge only (env+IAM)
> ```
> The scoped apply derives from the same `km-config.yaml → KM_* → terragrunt` pipeline;
> a subsequent `km init --plan` shows the bridge as a no-op. For code changes, use the
> full `km init --dry-run=false`.

No `export KM_SLACK_*` shell incantations are required after Phase 91.1 — both env vars flow
through `km init` automatically:

1. `slack.mention_only` from `km-config.yaml` → `KM_SLACK_MENTION_ONLY` (drift-WARN aware).
2. `{prefix}slack/bot-user-id` from SSM → `KM_SLACK_BOT_USER_ID` (env-wins; first-install skips).

### Either ordering works (init-first or slack-init-first)

The rollout sequence above runs `km slack init --force` BEFORE `km init` so SSM is populated
before the auto-read fires. But the inverse also works — Phase 91.1's auto-read is non-fatal
when SSM is empty, and the bridge falls back to a runtime `auth.test` on cold-start when the
Lambda env's `KM_SLACK_BOT_USER_ID` is empty:

| Ordering | `km init` line | `km doctor` Phase 91 check | Lambda `KM_SLACK_BOT_USER_ID` | Runtime fallback |
|----------|----------------|----------------------------|-------------------------------|------------------|
| `km slack init --force` → `km init` | (silent — SSM populated, value injected) | ✓ OK | `U…` (live value) | not needed |
| `km init` → `km slack init --force` | `[info] KM_SLACK_BOT_USER_ID not auto-set` | ⚠ WARN (until slack init runs) | `""` (until next `km init` re-runs) | one `auth.test` call per cold-start |

After flipping the order, re-running `km init` once more closes the loop — the auto-read now
finds the populated SSM param and injects it into the Lambda env on the next terragrunt
apply.

### First-install observed behavior

When `km init` runs against a workspace where `km slack init` hasn't yet been run since
Phase 91 shipped, expect this exact line near the top of the output:

```
  [info] KM_SLACK_BOT_USER_ID not auto-set (SSM /km/slack/bot-user-id unavailable:
  operation error SSM: GetParameter, ... StatusCode: 400, RequestID: …,
  ParameterNotFound: )
```

This is `EnsureSlackBotUserIDFromSSM` doing its non-fatal fallback — not an error, not a
warning, just an info line. The init proceeds; the bridge Lambda's `KM_SLACK_BOT_USER_ID`
env var is set to `""` (terragrunt fallback) and the bridge will use a runtime `auth.test`
on each cold-start.

Then `km doctor` (after init) shows:

```
⚠ Slack bot-user-id cache  /km/slack/bot-user-id not cached — bridge mention-scan will
                           fall back to live auth.test on every cold-start
  → Run `km slack init --force` (or `km slack rotate-token --bot-token <token>`) to
    re-capture and cache the bot user_id.
```

Resolution: `km slack init --force` populates SSM; re-run `km init --dry-run=false` to flow
the value into the Lambda env block; `km doctor` then shows the check as `✓ OK`.

`KM_SLACK_MENTION_ONLY` is install-level: it controls the Lambda default for sandboxes that
don't set `notification.slack.inbound.mentionOnly` explicitly. Setting it to `"true"` makes
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
| Bot ignores a thread reply (no @-mention) when the bot is already engaged in that thread | **Phase 91.3** added bypass for this case. If you upgraded to Phase 91 but not Phase 91.3 (km v0.3.772+), the @-mention is still required on every reply. Upgrade `km` and re-run `km init --dry-run=false`. |
| Bot 👀-reacts to unrelated thread chatter in a shared channel | **Phase 91.3** thread-bypass keys on (channel, thread\_ts) — once the bot dispatches a turn in a thread, EVERY subsequent reply in that thread routes to the sandbox. If the thread root was a non-bot conversation that the bot was mistakenly mentioned in once, the entire thread is now bot-owned. Slack-side fix: start a new top-level thread. |
| Want the bot to skip 👀 on thread replies (acknowledge top-level engagement only) | **Phase 91.4 (km v0.3.773+).** Set `slack.react_always: false` in `km-config.yaml`, run `km init --dry-run=false`. Top-level messages still get 👀 to signal "I saw you"; thread replies dispatch silently. |
| Want some sandboxes chatty and others first-only | **Phase 91.5 (km v0.3.776+).** Set `notification.slack.inbound.reactAlways: true \| false` per profile. `km create` writes `slack_react_always` to the sandbox's `km-sandboxes` row and the bridge prefers the per-sandbox value over `slack.react_always` in `km-config.yaml`. Profile field omitted → install default applies. Existing sandboxes need `km destroy --remote --yes && km create` to pick up profile changes. |
| Mention-scan never fires; cold-start is slow | `KM_SLACK_BOT_USER_ID` is empty in Lambda env. Confirm `km slack init` has been run (writes SSM) — `km init` auto-reads SSM on next apply. |
| Mode 2 (`#sb-{id}`) channel is polite when it should be chatty | Profile has `notification.slack.inbound.mentionOnly: true` set explicitly. Remove the field to restore the Mode 2 every-message default. |
| `km init --sidecars` doesn't change the polite/chatty behaviour | Expected: `--sidecars` only rebuilds km + sidecar binaries and forces a Lambda cold-start. The Lambda environment block (where `KM_SLACK_MENTION_ONLY` lives) only updates on full terragrunt apply: `km init --dry-run=false`. |

### Out of scope

The following capabilities are explicitly deferred to a future phase:

- **Per-channel runtime override via slash command** (e.g. `/km mention-only on`)
- **Display-name mention detection** (`@klankermaker` typed without Slack canonicalising to
  `<@U...>`) — Slack canonicalises on send so this is not expected to be a gap in practice
- **Reactions-as-actions integration** — different phase

---

## Phase 95: Federated bridge relay — one Slack App, many km installs

**Problem:** Slack requires one canonical Request URL per App. With multiple km installs
(different `resource_prefix` values) in a single AWS account/region, each install has its
own bridge Lambda URL. Only one install can be the Slack App's registered "front door"
— messages for sandboxes owned by the other installs are silently dropped at the front door.

**Solution:** A static relay list. The front-door install broadcasts any unknown-channel event
to sibling install bridges. Each peer processes the event if it owns the channel; drops it
(logging `slack_relay_no_owner`) if not. A single `X-KM-Relayed: 1` header is the complete
loop guard — relayed requests are terminal and are never re-broadcast.

### How it works

```
Slack App  →  install-A bridge (front door)
                 ├── channel owned by A?  → process locally (today's path)
                 └── channel unknown?    → broadcast to [B, C, ...]
                                              ├── B owns it?  → process + enqueue + 👀 react
                                              └── C owns it?  → process + enqueue + 👀 react
```

- **Single-hop only.** The `X-KM-Relayed: 1` header prevents any peer from re-relaying.
  A relayed request that arrives at a bridge that doesn't own the channel is dropped with
  `event_type=slack_relay_no_owner` in CloudWatch.
- **Synchronous, bounded.** The front-door bridge POSTs to all peers in parallel inside a
  ~2.5-second context window before returning `200` to Slack. Slack's 3-second ack window
  is honoured. A failing peer is logged (WARN) and non-fatal — the front door always returns
  200 regardless.
- **Verbatim forwarding.** Body, `X-Slack-Signature`, and `X-Slack-Request-Timestamp` are
  forwarded unchanged. Each peer validates the Slack HMAC with the shared signing secret
  (same credential stored per-install in its own SSM prefix).

### Config: `slack.peer_bridges`

Each install lists the `/events` URLs of SIBLING installs (not itself) in `km-config.yaml`.
For symmetry, set the list on every install so bi-directional routing works regardless of
which install gets the message.

```yaml
# Install A: km-config.yaml (the Slack App Request URL points here — A is the front door)
slack:
  peer_bridges:
    - https://def456.lambda-url.us-east-1.on.aws/events   # install B
    - https://ghi789.lambda-url.us-east-1.on.aws/events   # install C (if applicable)

# Install B: km-config.yaml
slack:
  peer_bridges:
    - https://abc123.lambda-url.us-east-1.on.aws/events   # install A (front door)
    - https://ghi789.lambda-url.us-east-1.on.aws/events   # install C (if applicable)
```

**Key:** `slack.peer_bridges` is a list of strings. Absent or empty means federation is off
— the install behaves exactly as before Phase 95 (unknown channels return `200` locally;
no relay occurs). `KM_SLACK_PEER_BRIDGES` is the comma-joined Lambda env var produced by
`km init`.

### Operator setup flow

1. **Create ONE Slack App.** Obtain its `xoxb-...` bot token and signing secret.

2. **`km slack init` on every install.** Paste the SAME `xoxb-...` + signing secret into
   each install's `km slack init`. Credentials are stored per-install in each install's own
   SSM prefix — no shared SSM paths; no coordination between installs.

3. **Set the App's Request URL to the front-door install's bridge `/events` URL.**
   (`api.slack.com/apps → Event Subscriptions → Request URL`)

4. **Set `slack.peer_bridges` in `km-config.yaml` for each install** (omit self, list
   siblings; see YAML example above).

5. **Deploy the env change on each install:**
   ```bash
   make build-lambdas    # clean rebuild (avoids stale zip pitfall)
   km init --dry-run=false
   ```
   **Use `km init --dry-run=false`, NOT `km init --sidecars`.** The `--sidecars` flag only
   rebuilds binaries and forces a Lambda cold-start — it does NOT update the Lambda
   `environment.variables` Terraform block where `KM_SLACK_PEER_BRIDGES` lives. A full
   `km init` (terragrunt apply) is required.

   > **Phase 105 scoped shortcut (config-key-only edit):** once the relay is already
   > deployed and you only need to update `slack.peer_bridges` (no code change):
   > ```bash
   > km init --slack --dry-run=false   # applies lambda-slack-bridge only (env+IAM)
   > ```
   > Code changes still need `make build-lambdas` + full `km init --dry-run=false`.

6. **Verify the env var reached the Lambda:**
   ```bash
   aws lambda get-function-configuration \
     --function-name {prefix}-slack-bridge \
     --query 'Environment.Variables.KM_SLACK_PEER_BRIDGES' \
     --output text
   ```

7. **Run `km doctor`** — the peer-bridge check (`slack peer bridges`) reports `OK` when
   the list is non-empty, well-formed, and contains no self-loops.

### Correctness invariants

- **Channel/alias uniqueness is required.** Each sandbox's per-sandbox `#sb-{id}` channel
  is owned by exactly one install (the one that created the sandbox). Per-sandbox channel
  names are auto-generated from the sandbox ID and are safe by construction.
- **Shared `#km-notifications` (or any manually named channel) must be registered only
  once.** Do not configure the same shared channel alias in multiple installs — routing
  to the wrong sandbox is undefined behaviour.
- **`url_verification` events are never relayed.** Slack sends `url_verification` once
  during App setup; it is handled immediately by the front-door bridge before reaching the
  relay injection point. Peer bridges never see it.

### `km doctor` peer-bridge checks

`km doctor` runs `slack peer bridges` as a WARN-level check (never hard-fails doctor):

| Condition | Result |
|-----------|--------|
| `slack.peer_bridges` absent/empty | SKIPPED — federation off |
| Any entry fails URL parse | WARN — malformed URL |
| Any entry equals this install's own bridge URL | WARN — self-loop detected |
| All entries valid and distinct | OK |

Remediation for WARN: edit `km-config.yaml slack.peer_bridges`, then `km init --dry-run=false`.

### Troubleshooting

| Symptom | Fix |
|---------|-----|
| `km doctor` reports `slack peer bridges` WARN (malformed) | Check `slack.peer_bridges` entries in `km-config.yaml` — each must be a full `https://` URL ending in `/events`. Run `km init --dry-run=false` after fixing. |
| `km doctor` reports `slack peer bridges` WARN (self-loop) | Remove this install's own bridge URL from `slack.peer_bridges`. The list must contain only sibling install URLs. |
| Front-door CloudWatch shows `slack_relay_no_owner` | A relayed event arrived at a peer that doesn't own the channel. Check that the sandbox's owning install has been created with `km create` and the channel row exists in DynamoDB. |
| `KM_SLACK_PEER_BRIDGES` absent in Lambda env | Ran `km init --sidecars` instead of `km init --dry-run=false`. Re-deploy: `make build-lambdas && km init --dry-run=false`. |
| Relay is slow (>3s) and Slack retries | Peer bridge is unhealthy or unreachable. Check peer CloudWatch logs. Failing peers are non-fatal (logged WARN) but add latency to the front-door response. |
| Messages delivered twice | A channel alias is configured on more than one install. Ensure channel/alias uniqueness across all installs. |

---

## Phase 96 — Default router (orphan-channel @-mention reply)

### What it does

When a user @-mentions the shared bot in a Slack channel that **no install owns**
(a true orphan channel), the designated front-door install posts a single threaded
reply explaining the naming convention and listing currently running sandbox channels
aggregated from all installs:

```
No sandbox is bound to this channel. To work with a bot, join one of its channels —
they're named `#sb-{alias}-{profile}`. Currently running:
• <#C012345ABC> — orc (patch)
• <#C678901DEF> — wrkr (hardened)
```

When no sandboxes are running the reply uses a guidance-only variant (no channel
mentions, just the naming convention and a prompt to ask an operator).

### Enabling it

Only the **front-door install** (the one that receives raw Slack events) should
enable this feature. Add one line to `km-config.yaml`:

```yaml
slack:
  default_router: true   # Enable orphan-channel reply on this install only.
```

Leave it absent or `false` on every other install — setting it on a non-front-door
install is a no-op because only the front door receives raw Slack events.

### How it works: claim-aware scatter-gather

1. The front door calls `FetchByChannel` — the channel has no owner locally.
2. `Broadcast` fans the event out to all peer installs (Phase 95 relay).
3. Each peer returns `{ claimed: false, channels: [...] }` listing its running
   sandboxes (or `{ claimed: true }` if it owns the channel).
4. **Claim tally**: if ANY peer returns `claimed:true`, the owner handled it — no
   router reply is posted.
5. **Zero claims = true orphan**. The reply gates then apply in order:
   - `default_router: true` on the front door.
   - The message must @-mention the bot (`<@{bot_user_id}>` in text).
   - The per-channel cooldown must be clear (3600 s via the nonces table).
6. The reply is posted synchronously (not in a goroutine — Lambda freeze safety).

**Rollout safety (mixed-version fleets):** a Phase-95 peer returns a legacy plain
`"ok"` body. The front door treats any non-JSON / HTTP-error response as
`claimed:true` (conservative). A mixed fleet never produces a false orphan reply;
the only downside is that legacy peers' running channels are absent from the list
until all installs are upgraded.

### Cooldown

Each orphan channel gets a per-channel cooldown of **3600 s (1 h)** from the first
reply. The cooldown reuses the `km-slack-bridge-nonces` DynamoDB table with a
`router-cooldown:{channel_id}` TTL key — no new infrastructure or IAM grants
required (the bridge Lambda already has write access to the nonces table).

A second @-mention within the cooldown window is silently ignored. After the TTL
expires the bot will reply again.

### Scope and limitations

- **Member channels only.** The bot can only reply to channels it has been invited
  to (`chat:write` scope, no `chat:write.public`). Non-member channels are silently
  ignored (Slack returns `not_in_channel`).
- **No new Slack scopes.** Phase 96 requires no additional OAuth scopes beyond
  those already granted in Phase 91.
- **No SandboxProfile schema change.** No sandbox recreate is needed. The feature
  is purely a Lambda-side change.

### Deploy

> **Important:** `KM_SLACK_DEFAULT_ROUTER` is a Lambda `environment.variables`
> entry. `km init --sidecars` rebuilds and cold-starts the Lambda binary but does
> **NOT** update the `environment` block via terragrunt. You must use
> `make build-lambdas` (clean) + `km init --dry-run=false` on every install.

For a two-install setup (e.g. `km` and `km2` in `us-east-1`):

```bash
# On the front-door install (e.g. the km install):
# 1. Edit km-config.yaml:
#    slack:
#      default_router: true

# 2. Rebuild and redeploy ALL installs (peers need their response shape upgraded
#    from Phase 95's plain "ok" to the JSON claim response):
make build-lambdas   # clean rebuild — do NOT skip; stale zips are NOT redeployed
km init --dry-run=false

# On each sibling install (e.g. km2):
make build-lambdas
km init --dry-run=false

# Verify:
km doctor            # confirms KM_SLACK_DEFAULT_ROUTER visible in Lambda env
```

> **Phase 105 scoped shortcut (config-key-only edit):** once the relay is already
> deployed and you only need to update `slack.default_router` (no code change):
> ```bash
> km init --slack --dry-run=false   # applies lambda-slack-bridge only (env+IAM)
> ```
> Code changes still need `make build-lambdas` + full `km init --dry-run=false`.

Deploy all installs **before** relying on cross-install channel lists in the reply
(see Pitfall 6 in the Phase 96 research notes: a mixed fleet omits legacy peers'
channels from the aggregate list, but is otherwise safe due to the rollout-safety
rule).

### Deferred items (out of scope)

- **Agentic self-serve create**: instead of just naming the convention, have the
  router spin up a new sandbox on behalf of the requester via EventBridge.
- **Non-member channels** (`app_mention` + `chat:write.public` scope).
- **DM fallback** (`im:write` scope).
- **Reply caching**: cache the running-channel list to avoid a DynamoDB Scan on
  every @-mention. The per-channel cooldown already bounds frequency to at most
  once per hour per channel.

### `km doctor` check

| Condition | Result |
|-----------|--------|
| `KM_SLACK_DEFAULT_ROUTER=true` present in Lambda env | OK — router active |
| `KM_SLACK_DEFAULT_ROUTER` absent or `false` | SKIPPED — router dormant (default) |

### Troubleshooting

| Symptom | Fix |
|---------|-----|
| No router reply appears after @-mention | Check `KM_SLACK_DEFAULT_ROUTER=true` in the front-door Lambda env tab (AWS console). If absent, ran `--sidecars` — redo with `make build-lambdas && km init --dry-run=false`. |
| Reply lists channels from this install but not from peers | Peers are still on Phase 95 code (returning legacy `"ok"`). Upgrade all installs: `make build-lambdas && km init --dry-run=false` on each. |
| Two replies in the same channel | Two installs both have `default_router: true`. Only the designated front-door install should enable it. |
| No reply even though cooldown should have expired | DynamoDB TTL deletion is eventually consistent (can lag by 48 h). Wait for TTL sweep, or contact AWS support. This is the DynamoDB TTL guarantee — not a bug. |

---

## Phase 99.1 — Inbound poison-message DLQ (FIFO wedge fix)

The per-sandbox slack-inbound FIFO queue (`{prefix}-slack-inbound-{sandbox-id}.fifo`)
delivers inbound Slack turns to the sandbox poller, which dispatches an agent turn. Before
Phase 99.1, a *poison message* — an envelope whose agent turn fails every time — would
**head-of-line-block** its FIFO message group **forever**: SQS will not deliver a later
message in the same group until the failed one is acknowledged, and a poller restart does
not clear it (only a queue purge does). Surfaced in the Phase 99 UAT.

**Fix:** a shared **per-install FIFO dead-letter queue** plus a `RedrivePolicy` on the
source queues:

- **Shared DLQs** (one pair per install, not per sandbox), created at `km init` by the
  `sqs-inbound-dlq` Terraform module:
  - `{prefix}-slack-inbound-dlq.fifo`
  - `{prefix}-github-inbound-dlq.fifo`
- **RedrivePolicy** on every per-sandbox inbound FIFO queue:
  `maxReceiveCount = 3` → after 3 failed receives, SQS auto-evicts the poison envelope to
  the matching DLQ, **unblocking the FIFO group** so subsequent turns flow.
- DLQs are FIFO (a FIFO source queue can only redrive to a FIFO DLQ) with **14-day
  retention** so an operator has time to inspect or redrive poison messages.
- No SandboxProfile schema change; no poller-shell change; the source-queue `RedrivePolicy`
  is injected purely at the SQS-attribute layer.

### km doctor — inbound DLQ depth

`km doctor` reports an **Inbound DLQ depth** check:

| State | Condition |
|---|---|
| **SKIP** | No SQS client configured, OR neither shared DLQ exists (dormant — inbound never provisioned). |
| **OK** | One or both DLQs present and **empty** (no poison messages). |
| **WARN** | At least one DLQ holds **> 0** messages — names the count and points at `aws sqs receive-message` / `purge-queue` / redrive. |

```bash
# Inspect a poison message before deciding to redrive or purge:
aws sqs receive-message --queue-url \
  https://sqs.<region>.amazonaws.com/<account>/<prefix>-slack-inbound-dlq.fifo
```

### Deploy sequence (Phase 99.1)

```bash
# 1. Rebuild the km binary (carries the new regionalModules() entry — a stale km
#    silently skips the sqs-inbound-dlq module). Memory: project_make_build_precedes_km_init.
make build

# 2. Clean-rebuild the Lambda zips (avoids the km-init-skips-existing-zips trap).
make build-lambdas

# 3. Full terragrunt apply — creates the two shared DLQs; the source-queue RedrivePolicy
#    is applied to NEW queues on the next km create.
#    NOT --sidecars: a new Terraform module + IAM require a full apply (env-block/IAM
#    changes are invisible to --sidecars). Memory: feedback_km_init_full_apply.
km init --dry-run=false

# 4. Existing sandboxes do NOT gain the RedrivePolicy retroactively (no silent backfill) —
#    recreate to attach redrive:
km destroy <sandbox-id> --remote --yes && km create <profile> --alias <alias>
```

**No `cmd/create-handler/main.go` change was required:** the create-handler Lambda only
*drains* envelopes into a pre-existing queue — it never creates the queue, so the
RedrivePolicy injection happens entirely in the `km create` warm path + the shared module.

**IAM:** no new grant. The existing `{prefix}-slack-inbound-*.fifo` /
`{prefix}-github-inbound-*.fifo` operator-policy wildcards already match `-dlq.fifo`
(Create/Delete/GetQueueAttributes/SetQueueAttributes/ListQueues/TagQueue). The sandbox EC2
role needs **no** DLQ grant — the poller never reads the DLQ (SQS moves poison messages
there automatically).

### Dormant invariant (99.1)

When no inbound integration is configured, the shared DLQs are never provisioned, no source
queue carries a `RedrivePolicy`, the doctor check **SKIPs**, and runtime is **byte-identical**
to pre-Phase-99.1.

---

## Phase 104 — O(1) channel resolution on alias reuse

### Background: the 900s wedge incident

`km create` on a profile with `notification.slack.perSandbox: true` and
`notification.slack.archiveOnDestroy: false` calls `conversations.create` on every
recreate. When the channel already exists Slack returns `name_taken` and the resolver
previously fell back to `conversations.list` — an unbounded walk of every channel in the
workspace (1 000 items per page, freshly-created channels sort last). On large corporate
workspaces with tens of thousands of channels this walk can exhaust the create-handler's
900 s Lambda timeout and strand the sandbox in `starting`.

**Root cause (confirmed against code):** `resolveExistingChannelID` in
`internal/app/cmd/create_slack.go` gated an SSM by-name cache hit on
`conversations.info(cachedID) == nil-err`. ANY transient info error — a momentary
`ratelimited`, 5xx, or network blip — fell through to `FindChannelByName`, an unbounded
`for{}` with `limit:1000` and no page cap. The SSM cache value was present and correct
during the incident; the defeater was the transient-info fall-through, not a cache miss.

Phase 104 closes the incident with three layers of defence:

- **P0 — wall-clock budget:** the entire per-sandbox channel resolution step runs in a
  sub-context bounded by `KM_SLACK_RESOLVE_BUDGET` (default **45 s**). The create-handler
  Lambda can never wedge beyond this ceiling.
- **P1 — lookup-first with transient-error classification:** the resolver reads the stored
  channel ID (DDB then SSM) before calling `conversations.create`. `conversations.info`
  errors are classified: only a definitive `channel_not_found` invalidates the stored
  mapping. Every other error (ratelimited, 5xx, network, context) is **transient** and
  never triggers a scan — after two bounded retries the stored ID is used optimistically.
- **P2 — durable authoritative store:** a dedicated `km-slack-channels` DynamoDB table
  (hash key `alias`, no TTL) makes the O(1) mapping survive across `km destroy`/`km
  create` cycles. The existing SSM by-name cache is kept as a back-compat read/write
  fallback; both stores are written through on every successful resolution.

### Env knobs

| Env var | Default | Effect |
|---------|---------|--------|
| `KM_SLACK_RESOLVE_BUDGET` | `45` (seconds) | Wall-clock ceiling for Mode-2 channel resolution at `km create` time. Increase for very slow workspaces. |
| `KM_SLACK_MAX_SCAN_PAGES` | `0` (scan **off**) | Max pages for the `conversations.list` fallback scan. `0` = fail-fast (return `scan_capped`/`failfast` with no HTTP call). Set `>0` only as a temporary migration aid — the bounded-lookup-first path makes a scan unnecessary for correctly populated stores. |

These env vars are read by the **operator-side `km` binary** (the create-handler Lambda
derives the table name from `cfg.GetSlackChannelsTableName()`, not from an env var).

### km-slack-channels DynamoDB table

A dedicated `{prefix}-slack-channels` DynamoDB table stores the authoritative
`alias → channel_id` mapping:

| Attribute | Type | Notes |
|-----------|------|-------|
| `alias` (PK) | String | The `--alias` value passed to `km create`. Key is absent for alias-less sandboxes — those channels never collide. |
| `channel_id` | String | The Slack channel ID (e.g. `C012ABCDE3F`). |
| `channel_name` | String | Recorded for observability only. |

**No TTL.** Mappings must survive destroy/recreate cycles. Stale rows (channel deleted or
bot removed) self-heal: `conversations.info` returns a definitive `channel_not_found` →
the row is re-populated on the next `km create`.

The table is **not scanned** by any km code path — unlike `km-sandboxes`, adding a
synthetic alias row would never pollute `km list`. `km doctor` checks for table existence
only (NOT orphan-row scan — alias rows are not per-sandbox and must never be auto-deleted).

### Observability: `slack_resolve` log line

The create-handler emits exactly one `INFO`-level log per Mode-2 resolution:

```
slack_resolve path=cache_hit ms=12 id=C012ABCDE3F channel=sb-github-review-myalias
```

| `path` value | Meaning |
|--------------|---------|
| `cache_hit` | Stored ID confirmed live via `conversations.info` — O(1), no API scan. |
| `cache_optimistic` | Stored ID present; `conversations.info` returned a transient error after retries — used optimistically (never scan). |
| `created` | No stored mapping; `conversations.create` succeeded — mapping written to DDB + SSM. |
| `scan_capped` | `KM_SLACK_MAX_SCAN_PAGES>0` scan ran and hit the page cap without finding the channel. |
| `failfast` | `KM_SLACK_MAX_SCAN_PAGES=0` (default); no scan attempted. |

On the first `km create` after deploying Phase 104 the path will be `created`. On every
subsequent recreate with the same `--alias` it will be `cache_hit` (or `cache_optimistic`
on a transient blip) — never an unbounded scan.

### channelOverride — zero-lookup manual escape (unchanged)

`notification.slack.channelOverride: "C012ABCDE3F"` (Mode 3) is the zero-lookup escape
hatch: the resolver validates the channel ID format and bot membership via a single
`conversations.info` call and returns immediately. No DDB read, no SSM read, no scan.
Suitable for channels that are not named after an alias (e.g. a shared team channel that
predates km).

### km slack adopt — seed the store for orphaned channels

When a channel was created outside km (or before Phase 104 was deployed) and the
bot is already a member, use `km slack adopt` to write the `alias → channel_id` mapping
into the DDB store without running `km create`:

```bash
km slack adopt <alias> <channelID>
```

**Example:**
```bash
# Find the channel ID: open the channel in Slack → channel name (top bar) → About → Channel ID
km slack adopt github-bot C012ABCDE3F
# Output: ✓ adopted alias=github-bot channel_id=C012ABCDE3F
```

`km slack adopt` validates:

1. `channelID` matches `^C[A-Z0-9]+$` — rejects IDs in wrong format.
2. Bot is a **member** of the channel — rejects if not (`conversations.info is_member`).
   Fix: `/invite @km-bot` in Slack, then re-run.
3. Writes through to **both** the DDB store (authoritative) and the SSM by-name cache
   (back-compat).

**After adopting**, the next `km create --alias <alias>` resolves O(1) via `cache_hit`
instead of running `conversations.create` (which would fail with `name_taken`) or
triggering a scan.

**Negative cases:**
- `km slack adopt github-bot not-an-id` → rejected: `channel ID must match ^C[A-Z0-9]+$`
- Adopting a channel the bot is not in → rejected: `bot is not a member of C… — /invite the bot first, then re-run km slack adopt`

### Deploy sequence (Phase 104)

**CRITICAL build order:** `make build` the `km` binary BEFORE `km init`. The binary
carries the `regionalModules()` entry for `dynamodb-slack-channels`; a stale binary
silently skips the module and `km init` bakes in a mock ARN (`000000000000`), causing
`AccessDenied` at runtime. See memory `project_make_build_precedes_km_init`.

```bash
# 1. Rebuild the km binary (picks up the new dynamodb-slack-channels module registration).
make build

# 2. Clean-rebuild the Lambda zips.
make build-lambdas

# 3. Preview the apply — confirm dynamodb-slack-channels + create-handler IAM show as ADDs;
#    no destroy-class trips expected.
AWS_PROFILE=klanker-application km init --plan

# 4. Full apply — creates the km-slack-channels table + create-handler IAM policy.
#    NOT --sidecars: a new Terraform module + IAM require a full terragrunt apply.
AWS_PROFILE=klanker-application km init --dry-run=false

# 5. Verify table is reachable.
AWS_PROFILE=klanker-application km doctor 2>&1 | grep -i slack-channels
# Expected: ✓ slack-channels table: <prefix>-slack-channels
```

**No SandboxProfile schema change.** No `km init --sidecars`. Existing sandboxes are
unaffected — Phase 104 is a create-time fix; running sandboxes do not need to be recreated.

**No `lambda-slack-bridge` change.** The bridge is not a consumer of the `km-slack-channels`
table. Only the `km` binary (operator-side) and the create-handler Lambda (IAM grant)
interact with the table.

### Troubleshooting (Phase 104)

| Symptom | Fix |
|---------|-----|
| `km create` still wedges for ~900 s | Binary is stale — `make build` was NOT run before `km init`. Rebuild and redeploy: `make build && make build-lambdas && km init --dry-run=false`. |
| `slack_resolve path=failfast` on every create | `KM_SLACK_MAX_SCAN_PAGES` defaults to 0 (scan off). This is correct for the bounded-lookup path. If you expected `cache_hit`, the DDB mapping may be missing — run `km slack adopt <alias> <channelID>`. |
| `km slack adopt` rejected: "not a member" | Invite the bot to the channel in Slack (`/invite @km-bot`), then re-run `km slack adopt`. |
| `km doctor` reports slack-channels table missing | `make build` (stale binary skipped the module) OR `km init` not run after Phase 104. Follow the deploy sequence above. |
| `path=cache_optimistic` on every create | `conversations.info` is returning transient errors (ratelimited or 5xx). The stored ID is being used optimistically — check Slack API status; the behaviour is correct (no scan triggered). |
| CloudWatch shows `events: router: orphan reply post failed` | The bot is not a member of the orphan channel. Invite it with `/invite @km-bot`. |

---

## Phase 111 — Rich Slack rendering: markdown and table blocks (opt-in `blocks-rich`)

Phase 111 adds a new opt-in **Tier-3** Slack render mode (`blocks-rich`) that uses Slack's GA
`markdown` block (Feb 2025) and `table` block (Aug 2025) for richer, native rendering of Claude's
GFM output. The default render mode stays `blocks` (Tier-2) — flip deferred to Phase 112 after
real-workspace UAT.

### Render mode tiers

| Tier | Mode | Output | Default? |
|------|------|--------|----------|
| 1 | `plain` | Literal text — no formatting | No |
| 1 | `mrkdwn` | CommonMark → Slack mrkdwn reflow | No |
| 2 | `blocks` | Block Kit sections/header/context/divider | **Yes** |
| 3 | `blocks-rich` | `markdown` + `table` blocks (Phase 111 opt-in) | No |

### What `blocks-rich` does

**Prose → `markdown` block.** GFM is passed verbatim to `{"type":"markdown","text":"<GFM>"}`.
The `markdown` block renders native bold/italic/lists/code spans and, critically, renders
`[label](url)` as clickable anchor links without any Slack-syntax conversion. Mrkdwnify is
intentionally NOT run — running it would double-convert `[l](u)` links to `<u|l>` syntax.

**Leading H1 → `header` block.** The first `# Heading` in each segment is promoted to a Slack
`header` block for visual hierarchy, identical to the Tier-2 renderer.

**GFM pipe tables → `table` block.** GFM pipe tables are parsed and emitted as
`{"type":"table","column_settings":[...],"rows":[[cell,…],…]}` blocks instead of monospace
fenced grids:
- Column alignment from the delimiter row (`:--` left, `:-:` center, `--:` right).
- Header row → `rich_text` bold cells (wrapped in the required `rich_text_section`; a flat
  element list is rejected by Slack with `invalid_blocks`).
- Body cells → `raw_text` (all of them). Numeric columns still right-align via
  `column_settings`; the `raw_number` cell type is deferred (its value-field schema is
  undocumented and was rejected in live UAT).
- Ragged rows are padded to the column count with empty `raw_text` cells.
- Guards: >20 columns or >100 rows trigger the monospace `fencePipeTables` fallback instead.

**Tool lines (🔧) → `context` block.** Unchanged from Tier-2.

**AI-disclaimer footer.** When `KM_SLACK_AI_FOOTER=true` (default off), a trailing
`_Generated by AI — verify before sharing._` context block is appended. Set this
per-profile in `/etc/km/notify.env` — the compiler does NOT emit it.

**12K cumulative markdown cap.** Slack caps the total markdown-block text per
`chat.postMessage` call at ~12,000 characters across all `markdown` blocks. When this is
exceeded, `blocks-rich` returns `ok=false` and the caller falls back to Tier-2.

**50-block cap.** Same as Tier-2 — returns `ok=false` on overflow, triggering Tier-2 fallback.

### Fallback chain

```
blocks-rich (Tier-3)
    ok=false → blocks (Tier-2)
                   ok=false → mrkdwn (Tier-1)
```

The fallback is whole-message: if `blocks-rich` fails, the entire message re-renders via
`RenderBlocks` (Tier-2), then `Mrkdwnify` (Tier-1). No partial Tier-3 output is emitted on failure.

### Surface caveats

| Caveat | Detail |
|--------|--------|
| Heading hierarchy flattens | `markdown` blocks render all heading levels at one size. Only the promoted leading H1 gets the larger `header` block weight. |
| Table cells are not markdown | Code spans, nested lists, and multi-line content inside a table cell degrade to `raw_text` — they are not further parsed. |
| No inline images | Images inside `markdown` blocks render as link text (unchanged from Tier-2). |
| Email / search / push | Only the plain-text `text:` fallback is delivered via email notification, search index, or push. Tables don't render in email — same as today. |
| Over-limit tables | Tables >20 cols or >100 rows fall back to the monospace `fencePipeTables` reflow, emitted as a `markdown` block with a ``` fence. |

### `KM_SLACK_AI_FOOTER`

Opt-in AI disclaimer footer. When set to `"true"`, appends a trailing context block:

> _Generated by AI — verify before sharing._

Set per-profile by adding to `/etc/km/notify.env` (the compiler does NOT emit this variable —
it is operator-controlled):

```bash
echo 'KM_SLACK_AI_FOOTER=true' | sudo tee -a /etc/km/notify.env
```

The footer fires only in `blocks-rich` mode — Tier-2 and Tier-1 are unaffected.

### Activating `blocks-rich`

Set `KM_SLACK_RENDER=blocks-rich` on the sandbox. The streaming hook (`_km_stream_drain`) and
the inbound poller reply both respect `KM_SLACK_RENDER`, so setting it in `/etc/km/notify.env`
affects all outbound Slack output on that sandbox:

```bash
echo 'KM_SLACK_RENDER=blocks-rich' | sudo tee -a /etc/km/notify.env
```

Or set it in the SandboxProfile's `spec.execution.configFiles` to pre-seed it on create.

### Deploy

**No SandboxProfile schema change. No new Terraform resource. No DynamoDB column.**

```bash
make build                # operator-side binary (km)
make build-lambdas        # sidecar zip carries new km-slack binary
km init --sidecars        # upload new km-slack binary to S3 + cold-start Lambda
```

Existing sandboxes pick up `blocks-rich` only after `km destroy && km create` — the sidecar
binary is baked into the sandbox at create time.

---

## Phase 114 — Slack bridge auto-resume

**What it does:** When an inbound Slack message targets a sandbox in `paused` or `stopped` state
AND that message would otherwise be dispatched (it already passed the mention-only / thread-bypass
filter and was enqueued to the per-sandbox FIFO), the `km-slack-bridge` Lambda calls
`ec2:StartInstances` to wake the EC2 instance. Once the instance is up, the on-box inbound poller
boots, drains the already-enqueued message, and the agent replies in the thread as normal.

This is the Slack analog of the GitHub/H1 Phase-109 resume-or-cold-create path.
**Resume-only** — there is no cold-create path for Slack (Slack has no `SandboxCreate`
EventBridge publisher).

### Trigger gate

Resume fires **only at the existing step-9 paused branch** — i.e., only after the message
passed the mention-only / thread-bypass filter (step 5b) and was enqueued to SQS (step 8).
Idle channel chatter that would not dispatch **never** wakes the box. The dispatch and
enqueue behavior for running sandboxes is byte-identical to pre-Phase-114.

### Wake UX

The bridge posts a threaded hint immediately after triggering the resume:

| Path | Hint text |
|------|-----------|
| Resume triggered (nominal) | "Sandbox is waking up — your message is queued and will be answered shortly." |
| Orphan / degraded (instance gone, row still paused) | "Couldn't auto-resume this sandbox (the instance is gone). Ask an operator to recreate it with `km create`." |

Both hints use a 1-hour cooldown at the `km-sandboxes` row level — whichever fires first
suppresses the other for that window (correct behavior for the pair).

### Synchronous design

`StartInstances` and the DDB status flip (`SetStatusRunning`) run **synchronously** inside
`Handle`, not in a goroutine. This mirrors the Phase 75.2 lesson: goroutines mid-flight when
`Handle` returns have their context elapse during the Lambda freeze. The 3-second Slack ack
window is protected by the step-6 `event_id` dedup — if `StartInstances` pushes past 3 seconds,
Slack's retry hits the dedup and returns 200 immediately.

The resume context uses a 15-second sub-timeout from the request context, staying well within
the Lambda's 60-second timeout.

### Back-compat invariant

`h.Resumer == nil` (pre-deploy Lambda image) → byte-identical to pre-Phase-114: the
`PauseHinter` fires as before, posting the old "message queued" hint. No behavior change
until `make build-lambdas` + `km init --slack` deploys the new image.

### IAM

One additive policy (`aws_iam_role_policy.ec2_resume`) on the `km-slack-bridge` Lambda role:

| Statement | Action | Resource | Condition |
|-----------|--------|----------|-----------|
| `EC2DescribeInstances` | `ec2:DescribeInstances` | `*` (Describe has no resource conditions) | — |
| `EC2StartInstances` | `ec2:StartInstances` | `arn:aws:ec2:{region}:{account}:instance/*` | `aws:ResourceTag/km:resource-prefix == {prefix}` |

`dynamodb:UpdateItem` on `km-sandboxes` (for `SetStatusRunning`) is already granted by the
existing `dynamodb_sandboxes_pause_hint` policy — **no new DDB grant**.

### Deploy surface

```bash
make build-lambdas          # rebuild the bridge zip carrying the new EC2 client wiring
km init --slack             # tier-1 env+IAM fast-path: applies the new ec2_resume policy
# OR
km init --dry-run=false     # full apply (also applies ec2_resume policy)
```

**NOT `--sidecars`** — `--sidecars` rebuilds sidecar binaries and cold-starts the Lambda, but
does NOT update the Lambda's IAM role. The EC2 client in the binary is harmless without the
grant (it logs `UnauthorizedOperation` and falls through to the already-enqueued message —
fail-soft), but the feature does not work until the IAM policy is applied via a full
`km init --slack` or `km init --dry-run=false`.

**No SandboxProfile schema change. No DynamoDB schema change. No sandbox recreate required.**
Existing paused sandboxes gain resume-on-message immediately after the bridge deploy.

### E2E UAT

Perform the following after deploying:

1. **Paused sandbox resume:** `km pause <id>` a running Slack-bound sandbox. Post a message
   in its channel. Verify the bridge logs `event_type=resume_triggered` and the sandbox wakes.
   The agent replies to the message.

2. **Stopped sandbox resume:** `km stop <id>` a running sandbox. Post a message. Verify
   `StartInstances` is called and the sandbox comes up.

3. **Orphan / degraded path:** Manually terminate the EC2 instance (without `km destroy`)
   leaving the DDB row in `paused` state. Post a message. Verify the bridge posts the
   "Couldn't auto-resume" orphan hint and the row is left in place.

4. **Warm regression:** Post a message to a running sandbox. Verify behavior is byte-identical
   to pre-Phase-114 (SQS enqueue, 👀 reaction, no resume path triggered).

5. **Mention-only guard:** With `KM_SLACK_MENTION_ONLY=true`, post a message without
   `@bot-mention` to a paused sandbox. Verify the message is dropped before step-9 —
   `StartInstances` is NOT called.

### Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `UnauthorizedOperation` on `StartInstances` in bridge logs | `km init --slack` (or `--dry-run=false`) not yet run; `ec2_resume` IAM policy absent | Run `km init --slack --dry-run=false` |
| Bridge posts pause hint, no resume | Old Lambda image (binary predates Phase 114) | Run `make build-lambdas` then `km init --slack --dry-run=false` |
| Sandbox stays stopped after hint | On-box inbound poller not yet running (instance still starting) | Wait 30–60s for boot; the already-enqueued SQS message has a visibility timeout and will be drained on poller start |
| "Couldn't auto-resume" posted for a live sandbox | EC2 instance terminated out-of-band (row is an orphan) | `km create <profile> --alias <alias>` to recreate; existing DDB row left in place (no cold-create in Phase 114) |
