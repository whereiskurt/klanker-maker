# Slack-Notify Hook — Design

**Date:** 2026-04-29
**Status:** Approved for implementation (v1 = post-only Slack delivery alongside email)
**Predecessor:** `2026-04-26-operator-notify-hook-design.md` (phase 62)

## Goal

Extend the operator-notify mechanism delivered in phase 62 with a Slack
delivery channel. Same triggers (`Notification` permission prompts and
`Stop` idle events from Claude Code), same gates (`notifyOnPermission`,
`notifyOnIdle`, cooldown). New transport: a Slack workspace owned by
klankermaker.ai, with the operator invited to channels via Slack Connect
from their own workspace.

v1 is one-way Slack delivery. v2 (out of scope here, but the design must
remain compatible) closes the loop: operator replies in Slack, the agent
picks it up, resumes.

## Non-Goals

- Closed-loop reply ingestion. Designed-around but not built in v1.
- Slack interactive features — slash commands, buttons, modals.
- Block Kit / rich formatting. Plain text + bold subject header only.
- DM delivery. Channels only.
- Per-event recipient routing. One channel per sandbox.
- Multiple invite recipients. One platform-wide invite email; humans
  add themselves to the channel post-acceptance via Slack's UI.
- Retroactive Slack support on existing sandboxes (`km destroy && km create`
  required, same constraint as phase 62).

## Architecture Overview

### Trust model

Same Ed25519 signing model the platform already uses for `km-send`. Each
sandbox has a signing key in SSM at `/sandbox/{id}/signing-key`. The new
`km-slack` binary on the sandbox signs an HTTP request payload with that
key. A new Lambda `km-slack-bridge` verifies the signature, looks up the
workspace bot token from SSM, and calls Slack's Web API. **The bot token
never leaves AWS**, eliminating the workspace-takeover risk that direct
sandbox-to-Slack would carry.

The operator side reuses the existing operator Ed25519 identity provisioned
by `km init` at `/sandbox/operator/signing-key` (created in phase 45 / used
by `km email send --from operator`). The bridge accepts signatures from
both sandbox keys *and* the operator key, with action-level authorization
distinguishing what each can do.

### One-time bootstrap (per platform)

Operator runs `km slack init` once. This:

1. Prompts for the bot token (`xoxb-...`) and stores it in SSM at
   `/km/slack/bot-token` (SecureString, KMS-encrypted with the existing
   platform key).
2. Stores workspace metadata (workspace ID, team name) at
   `/km/slack/workspace`.
3. Stores the default invite recipient email at `/km/slack/invite-email`.
4. Creates the default shared channel `#km-notifications` via
   `conversations.create`, sends a Slack Connect invite via
   `conversations.inviteShared` to the invite-email, stores the channel
   ID at `/km/slack/shared-channel-id`. Operator clicks accept in their
   email — channel is now live in their Slack workspace.
5. Deploys the `km-slack-bridge` Lambda if not already present, stores
   its Function URL at `/km/slack/bridge-url`.

This is the moment that "feels like" a GitHub-app install — one
interactive flow, never repeated.

### Per-sandbox provisioning

`km create` already creates DynamoDB metadata. New step: if the resolved
profile has `notifySlackEnabled: true`, the operator-side CLI:

- **Shared mode** (default): looks up `/km/slack/shared-channel-id`,
  writes it into the sandbox metadata as `slack_channel_id`. No new
  channel created.
- **Per-sandbox mode** (`notifySlackPerSandbox: true`): calls
  `conversations.create` for `#sb-{id}` (or `#sb-{alias}` if `--alias`
  set), sends a Connect invite to `/km/slack/invite-email`, stores the
  new channel ID in metadata.
- **Override mode** (`notifySlackChannelOverride: "C0123ABC"`):
  validates the channel exists and the bot is a member; writes that
  channel ID to metadata. Failure aborts `km create` (no orphaned
  sandboxes pointing at non-existent channels).

The channel ID is then injected into the sandbox's compiler-managed env
file (`/etc/profile.d/km-notify-env.sh` — phase 62 chose `profile.d` over
`/etc/environment` because Amazon Linux 2 SSM sessions reliably source the
former) as `KM_SLACK_CHANNEL_ID`. The bridge Lambda's URL goes in as
`KM_SLACK_BRIDGE_URL`. Both join the existing phase 62 `KM_NOTIFY_*` vars
in the same file.

### Per-event runtime path

```
Claude hits Notification/Stop event
  → ~/.claude/settings.json fires /opt/km/bin/km-notify-hook
  → hook checks KM_NOTIFY_ON_* gates (existing phase 62 logic)
  → hook checks KM_NOTIFY_SLACK_ENABLED (new)
  → if enabled: hook calls /opt/km/bin/km-slack post \
       --channel "$KM_SLACK_CHANNEL_ID" \
       --subject "$subject" --body /tmp/body
  → km-slack signs payload with sandbox Ed25519 key,
    POSTs to KM_SLACK_BRIDGE_URL
  → km-slack-bridge Lambda verifies signature, action, and
    channel-ownership; fetches bot token from SSM;
    calls Slack chat.postMessage
  → message lands in operator's Slack
```

The phase 62 email path runs in parallel if `notificationEmailAddress`
is also configured. Both channels share the cooldown window (per-sandbox,
not per-channel — the rate-limit is about *operator attention*, not per-
transport volume).

### Per-sandbox cleanup

`km destroy sb-abc123` reads metadata. If the sandbox was provisioned in
per-sandbox mode AND `slackArchiveOnDestroy: true`:

1. Posts a final message to the channel: "Sandbox `sb-abc123` destroyed
   at `<timestamp>`."
2. Calls `conversations.archive` on the channel via the bridge Lambda.
3. Archive failure → log warning, continue with destroy. Don't block
   teardown on Slack API issues.

For `slackArchiveOnDestroy: false`, the channel persists; a "destroyed;
channel preserved per profile" line is still posted. For shared-channel
sandboxes, no cleanup — channel persists.

## Profile Schema Additions

New optional fields under `spec.cli` (extends phase 62's pattern):

```yaml
spec:
  cli:
    # existing (phase 62)
    notifyOnPermission: true
    notifyOnIdle: true
    notifyCooldownSeconds: 60
    notificationEmailAddress: "team@example.com"

    # new (this phase)
    notifyEmailEnabled: true             # default true; set false for Slack-only profiles
    notifySlackEnabled: true
    notifySlackPerSandbox: false
    notifySlackChannelOverride: "C0123ABC"
    slackArchiveOnDestroy: true
```

| Field | Type | Default | Effect |
|---|---|---|---|
| `notifyEmailEnabled` | bool | `true` | When `false`, skip the email dispatch in the hook even if event gates fire. Backward-compatible with phase 62 (default keeps email on). |
| `notifySlackEnabled` | bool | `false` | Enable Slack delivery for whatever events `notifyOn*` already gates |
| `notifySlackPerSandbox` | bool | `false` | Create `#sb-{id}` at `km create`, archive at `km destroy`. Ignored if `notifySlackEnabled: false` |
| `notifySlackChannelOverride` | string | unset → `/km/slack/shared-channel-id` | Hard-pin to a specific channel ID; overrides both shared and per-sandbox modes |
| `slackArchiveOnDestroy` | bool | `true` | Per-sandbox channels only. Set `false` to preserve the trail post-teardown |

### Validation rules

- `notifySlackPerSandbox: true` AND `notifySlackChannelOverride: <set>`
  → validation error (mutual exclusion).
- `notifySlackPerSandbox: true` AND `notifySlackEnabled: false`
  → validation warning (no-op).
- `slackArchiveOnDestroy` set without `notifySlackPerSandbox: true`
  → validation warning (no-op).
- `notifySlackChannelOverride` not matching `^C[A-Z0-9]+$`
  → validation error.
- `notifyEmailEnabled: false` AND `notifySlackEnabled: false`
  → validation warning (no notification channels — gates fire into a void).

### What's *not* a field

- `notifySlackInviteEmail` — the invite recipient is platform-wide
  (`/km/slack/invite-email`), not per-profile.
- `notifySlackBotToken` — platform secret, not a profile knob.

### CLI overrides

None in v1. All four fields are profile-only. Phase 62 added flags only
for the boolean gates `notifyOnPermission`/`notifyOnIdle`; recipient and
cooldown stayed profile-only. Same precedent.

## Operator CLI

### `km slack init`

Interactive command. Idempotent: re-running with the same workspace
updates the token and re-deploys the Lambda but doesn't recreate the
shared channel if `/km/slack/shared-channel-id` is already populated and
the channel still exists.

Flags:

- `--bot-token <token>` (skip prompt; pull from arg for CI)
- `--invite-email <addr>` (skip prompt)
- `--shared-channel <name>` (override default `km-notifications`)
- `--force` (recreate shared channel and re-send invite even if existing)

### `km slack test`

Posts a test message to the shared channel via the bridge Lambda using
the operator's Ed25519 key (`/sandbox/operator/signing-key` — already exists,
used by operator-sent email). Confirms end-to-end: SSM → Lambda → Slack
API → channel.

### `km slack status`

Shows current Slack config — workspace, channel ID, invite-email, Lambda
URL, last-test timestamp.

### `km doctor` additions

- **Slack token validity** — calls `auth.test` via the Lambda; flags
  expired/revoked tokens.
- **Stale per-sandbox channels** — for each DynamoDB sandbox record with
  a `slack_channel_id`, verify the sandbox still exists; flag channels
  whose sandbox was destroyed but the archive failed (only when
  `slackArchiveOnDestroy: true` was originally set).

## `km-slack` Sandbox Binary

Single Go binary at `/opt/km/bin/km-slack`. Parallels `km-send`. Single
command shape v1 needs:

```
km-slack post --channel <id> --subject <text> --body <file> [--thread <ts>]
```

| Flag | Required | Notes |
|---|---|---|
| `--channel <id>` | yes | Slack channel ID (`C0123ABC`); must match `KM_SLACK_CHANNEL_ID` |
| `--subject <text>` | yes | Used by Lambda for the bold header line |
| `--body <file>` | yes | Path to plain-text body. **File only**, no stdin (OpenSSL 3.5+ rationale, same as `km-send`) |
| `--thread <ts>` | no | Thread reply parent ts. Wired but unused by v1 hook (reserved for v2) |

### Behavior

1. Read body file. Cap at 40 KB (Slack's `chat.postMessage` text limit).
2. Construct signed envelope:
   ```json
   {
     "version": 1,
     "action": "post",
     "sender_id": "sb-abc123",
     "channel": "C0123ABC",
     "subject": "[sb-abc123] needs permission",
     "body": "<message text>",
     "thread_ts": null,
     "timestamp": 1714280400,
     "nonce": "<128-bit random>"
   }
   ```
3. Sign canonical JSON (sorted keys, no whitespace) with sandbox's
   Ed25519 private key from `/sandbox/{id}/signing-key` (cached for
   process lifetime).
4. POST to `$KM_SLACK_BRIDGE_URL` with headers:
   - `X-KM-Sender-ID: sb-abc123`
   - `X-KM-Signature: <base64 ed25519 sig>`
   - `Content-Type: application/json`
5. **Retry policy**: 3 attempts on HTTP 5xx / 503 / network error,
   backoff 1s/2s/4s. HTTP 4xx → exit 1 immediately (caller decides
   whether to swallow). 2xx → exit 0.

### Why a binary, not bash

Crypto + retry + JSON canonicalization is enough surface that a Go
binary is cleaner and more testable. Matches `km-send` precedent. Single
static binary deployed via the existing sidecar pipeline
(`km init --sidecars`).

## Hook Integration

The phase 62 `km-notify-hook` script becomes multi-channel. After the
existing gate + cooldown logic (untouched), the dispatch section grows:

```bash
# ... gate check (KM_NOTIFY_ON_*), cooldown, build $subject + $body_file
# (existing phase 62 logic, unchanged)

sent_any=0

# Email path (existing phase 62 logic, now gated by KM_NOTIFY_EMAIL_ENABLED).
# Default is "1" so existing phase 62 profiles continue to send email.
# Set notifyEmailEnabled: false in profile for Slack-only delivery.
if [[ "${KM_NOTIFY_EMAIL_ENABLED:-1}" == "1" ]]; then
  to_args=()
  [[ -n "${KM_NOTIFY_EMAIL:-}" ]] && to_args=(--to "$KM_NOTIFY_EMAIL")
  /opt/km/bin/km-send "${to_args[@]+"${to_args[@]}"}" --subject "$subject" --body "$body_file" \
    && sent_any=1 \
    || true
fi

# Slack path (new)
if [[ "${KM_NOTIFY_SLACK_ENABLED:-0}" == "1" && -n "${KM_SLACK_CHANNEL_ID:-}" ]]; then
  /opt/km/bin/km-slack post \
    --channel "$KM_SLACK_CHANNEL_ID" \
    --subject "$subject" \
    --body "$body_file" \
    && sent_any=1 \
    || true
fi

# Cooldown only updates if at least one channel succeeded
[[ $sent_any -eq 1 ]] && date +%s > "$last_file"

exit 0
```

### Email enable semantics

Phase 62's hook unconditionally sent email once the event gate fired —
no separate enable/disable toggle. This phase introduces
`notifyEmailEnabled: bool` (default `true`) for symmetry with
`notifySlackEnabled`. Compiler emits `KM_NOTIFY_EMAIL_ENABLED=1` (or `0`)
into `/etc/profile.d/km-notify-env.sh`; the hook gates the email
dispatch path on it. Default `true` keeps existing phase 62 profiles
unchanged. Setting `notifyEmailEnabled: false` together with
`notifySlackEnabled: true` enables clean Slack-only delivery.

### Invariants preserved from phase 62

- **Hook never blocks Claude.** Always exits 0, even if both channels fail.
- **Cooldown is per-sandbox, shared across both events AND both channels.**
  A Notification within the cooldown window emits to neither channel.
- **Body file is unchanged.** Same content goes to email and Slack. Email
  gets a subject line; Slack uses the structured `subject` field for a
  bold header.

### Slack message format (what lands in the channel)

Plain text. The bridge Lambda formats the bold header from the `subject`
field; the hook is agnostic to Slack-specific formatting.

```
*[sb-abc123]* needs permission

Claude needs your permission to use Bash

---
Attach:  km agent attach sb-abc123
Results: km agent results sb-abc123
```

`*...*` is Slack's bold mrkdwn. `unfurl_links: false` and `unfurl_media:
false` are set on the API call to keep notifications compact.

## `km-slack-bridge` Lambda

Go Lambda, deployed via `km init --lambdas`. Reachable at a Lambda
Function URL (auth mode `NONE` — application-layer Ed25519 signatures
provide auth). Same model as the existing operator email Lambda.

### Request envelope

```json
{
  "version": 1,
  "action": "post" | "archive" | "test",
  "sender_id": "sb-abc123" | "operator",
  "channel": "C0123ABC",
  "subject": "...",
  "body": "...",
  "thread_ts": null,
  "timestamp": 1714280400,
  "nonce": "<128-bit random>"
}
```

| Action | Allowed senders | Effect |
|---|---|---|
| `post` | sandbox, operator | `chat.postMessage` |
| `archive` | operator only | `conversations.archive` (used by `km destroy`) |
| `test` | operator only | post to shared channel; used by `km slack test` |

### Verification flow (per request)

1. **Parse envelope.** Reject malformed JSON or missing required fields
   with HTTP 400.
2. **Replay protection.**
   - Verify `timestamp` within ±5 minutes of Lambda clock. Older → 401.
   - Verify `nonce` not in `km_slack_bridge_nonces` DynamoDB table
     (TTL = 10min). Conditional-write to insert; collision → 401.
3. **Signature verification.**
   - `sender_id == "operator"` → fetch `/sandbox/operator/signing-public-key`.
   - `sender_id == "sb-*"` → fetch `/sandbox/{id}/signing-public-key`.
   - Verify Ed25519 over canonical JSON (sorted keys, no whitespace).
     Mismatch → 401. Sender not found → 404.
4. **Action authorization.**
   - `archive` or `test` from a sandbox sender → 403.
   - `post` from a sandbox sender to a channel that doesn't match the
     sandbox's metadata `slack_channel_id` → 403. (Compromised sandbox
     can't post to channels other than its own.)
   - `post` from operator → any channel allowed.
5. **Bot token fetch.** Read `/km/slack/bot-token` from SSM (cached for
   the Lambda's warm lifetime, ~15min).
6. **Execute against Slack Web API.**
   - `post` → `chat.postMessage` with formatted bold header + body.
   - `archive` → `conversations.archive`.
   - `test` → `chat.postMessage` to shared channel with fixed test text.
7. **Response.**
   - Slack 2xx → `{"ok": true, "ts": "<message_ts>"}` HTTP 200.
   - Slack 429 → HTTP 503 with `Retry-After`.
   - Slack other error → HTTP 502 with Slack error code surfaced.

### Channel-mismatch authorization detail

Step 4's "post from sandbox to non-matching channel" check is the
meaningful blast-radius reducer. The flow:

- Sandbox `sb-abc123` posts with `channel: C09FOO123`.
- Lambda reads `km_sandboxes.sb-abc123.slack_channel_id` from DynamoDB.
- If the metadata channel ID doesn't equal the request channel ID → 403.
- If the sandbox has no `slack_channel_id` (Slack disabled) → 403.

Compromised sandbox in per-sandbox mode can only spam its own channel.
In shared-channel mode, all sandboxes legitimately have the same
`slack_channel_id`, so the check passes for any sandbox — that's correct
(shared is shared).

### IAM

The Lambda execution role gets:

- `ssm:GetParameter` on `/km/slack/*`, `/sandbox/*/signing-public-key`,
  `/sandbox/operator/signing-public-key`.
- `kms:Decrypt` on the platform KMS key alias (for the bot token
  SecureString).
- `dynamodb:PutItem`, `GetItem` on `km_slack_bridge_nonces`.
- `dynamodb:GetItem` on the existing sandbox metadata table.
- `logs:*` for CloudWatch.

No internet egress restriction needed (only external endpoint is
`slack.com`).

### What this Lambda does *not* do

- Channel creation. Stays operator-side in `km create` (synchronous
  error handling fits poorly in async Lambda).
- Connect-invite sending. Same reason — operator-side at `km slack init`
  and `km create`.
- Interactive Slack features. Out of scope for v1.
- Retry from Lambda side. `km-slack` retries; Lambda is single-shot.

### Operational concerns

- **Cold start latency.** Sandbox-driven calls are async; operator-driven
  calls (`km slack test`, `km destroy` archive) take a 200–400ms hit on
  cold starts. Acceptable.
- **Bot token rotation.** Operator updates SSM via `km slack init
  --bot-token <new>` or `aws ssm put-parameter`. Lambda picks it up on
  next cold start (or after 15min cache TTL on warm). No sandbox
  redeploy needed — that was the point of approach 2.
- **Signing key rotation.** Already handled by phase 23. Nothing new.
- **Lambda observability.** CloudWatch logs structured JSON. Surfaced
  via `km otel <sandbox-id>` for sandbox-side calls (extends phase 27)
  and `km slack status` for aggregate metrics.

## Test Surface

### Profile schema tests (`pkg/profile/validate_test.go`)

- `notifySlackEnabled: true` + `notifySlackPerSandbox: true` +
  `notifySlackChannelOverride: "C0123ABC"` → validation error.
- `notifySlackEnabled: false` + `notifySlackPerSandbox: true` →
  validation warning.
- `slackArchiveOnDestroy` set without `notifySlackPerSandbox` →
  validation warning.
- `notifySlackChannelOverride` not matching `^C[A-Z0-9]+$` →
  validation error.

### Compiler unit tests (`pkg/compiler/compiler_test.go`)

- Profile with `notifySlackEnabled: false` → no `KM_NOTIFY_SLACK_*` lines
  in `/etc/profile.d/km-notify-env.sh`, no `km-slack` invocation in hook
  script.
- Profile with `notifyEmailEnabled: false` →
  `KM_NOTIFY_EMAIL_ENABLED=0` written, hook skips `km-send`.
- Profile with `notifyEmailEnabled` unset → `KM_NOTIFY_EMAIL_ENABLED` not
  emitted (hook default of `1` takes effect — phase 62 backward compat).
- Profile with `notifySlackEnabled: true` shared mode →
  `KM_NOTIFY_SLACK_ENABLED=1`, `KM_SLACK_CHANNEL_ID=<shared-id>`,
  `KM_SLACK_BRIDGE_URL=<url>` written to `/etc/profile.d/km-notify-env.sh`.
- Profile with `notifySlackPerSandbox: true` → metadata's
  `slack_channel_id` injected, not the shared one.
- Profile with `notifySlackChannelOverride: "C0123ABC"` → that channel
  ID injected verbatim.
- User-supplied `settings.json` via `configFiles` → still gets phase 62's
  hook entries merged (regression check).

### `km-slack` binary tests

- `pkg/slack/payload_test.go` — canonical-JSON construction, signature
  determinism, body size enforcement (40 KB cap).
- `pkg/slack/client_test.go` — HTTP retry behavior with stub server:
  200 → exit 0; 429 → backoff and retry; persistent 5xx → exit 1 after
  3 attempts; network error → backoff.
- `cmd/km-slack/main_test.go` — end-to-end with a stub Lambda Function
  URL, real Ed25519 keys, verify request shape and headers.

### `km-notify-hook` script tests (extending phase 62 harness)

- `KM_NOTIFY_SLACK_ENABLED=0` + `KM_NOTIFY_EMAIL_ENABLED=1` (or unset) →
  only `km-send` invoked, `km-slack` never called.
- `KM_NOTIFY_SLACK_ENABLED=1` + `KM_NOTIFY_EMAIL_ENABLED=0` → only
  `km-slack` invoked, `km-send` never called.
- Both enabled → both invoked; cooldown updates iff at least one
  succeeded.
- `km-slack` returns 1 + `km-send` returns 0 → cooldown updates, hook
  exits 0.
- Both fail → cooldown does not update, hook exits 0.
- `KM_NOTIFY_EMAIL_ENABLED` unset (phase 62 backward compat) → email
  path runs (default of `1` takes effect).
- Stubs for `km-send` and `km-slack` via PATH override.

### Bridge Lambda tests (`pkg/slack/bridge/handler_test.go`)

- Valid sandbox `post` to its own channel → Slack `chat.postMessage`
  called with expected args.
- Valid sandbox `post` to a non-matching channel → 403, no Slack call.
- Sandbox `archive` → 403.
- Operator `archive` → Slack `conversations.archive` called.
- Operator `test` → posts to shared channel.
- Stale timestamp (>5min) → 401.
- Replayed nonce → 401.
- Bad signature → 401.
- Unknown sender_id → 404.
- Slack 429 from upstream → 503 + `Retry-After` propagated.
- Slack 5xx → 502 with code surfaced.
- DynamoDB unavailable for nonce write → 500 (fail closed).
- Bot token missing from SSM → 500 with clear log line.

### CLI command tests (`internal/app/cmd/`)

- `km slack init` happy path with mocked SSM/Slack/Lambda — verify all
  four SSM params written, Lambda deployed, shared channel created,
  invite sent.
- `km slack init --force` with existing config — recreates channel,
  re-sends invite.
- `km slack test` — invokes bridge Lambda, parses response, prints test
  message timestamp.
- `km slack status` — reads SSM, validates token via Lambda `auth.test`,
  prints summary.
- `km create` with `notifySlackEnabled: true` per-sandbox mode — Slack
  channel created before infra, channel ID in metadata, `KM_SLACK_*`
  injected.
- `km create` failure during channel creation → infra rollback (test the
  unwind path).
- `km destroy` with per-sandbox + archive enabled → final post + archive
  call sequence.
- `km destroy` with archive disabled → final post only, no archive.
- `km destroy` archive failure → warning logged, destroy continues.

### `km doctor` test additions

- Slack token expired → reported as failed health check.
- Sandbox destroyed but channel not archived → reported as stale resource.

### E2E (manual / opt-in CI)

Real klankermaker.ai workspace, real `kurt.hundeck@greenhouse.io` invite
acceptance done out-of-band once.

- Create sandbox with `notifySlackEnabled: true` shared mode +
  `notifyOnIdle: true`. Run `km agent run --prompt "What's 2+2?"`.
  Confirm message in `#km-notifications` with subject header.
- Same with `notifyOnPermission: true` and a permission-triggering
  prompt.
- Per-sandbox mode: `km create profiles/p.yaml --alias=demo`. Confirm
  `#sb-demo` appears in operator's Slack via Connect invite. Confirm
  hook posts there.
- `km destroy` sandbox with `slackArchiveOnDestroy: true` → confirm
  archive in Slack UI.
- `km destroy` sandbox with `slackArchiveOnDestroy: false` → channel
  persists, "destroyed" message visible.

## v2 Forward-Compatibility (Deliberate v1 Choices)

- `thread_ts` field in `km-slack` payload is wired but unused in v1. v2
  closed-loop case threads each `Notification` event under a per-turn
  root message.
- `action` discriminator in the bridge payload is wired so v2 can add
  `react`, `update_message`, `dm` actions without breaking v1 envelopes.
- `version: 1` field on every payload means breaking-change rollouts can
  co-exist (Lambda accepts both during a transition).
- Subject metadata is structured (separate `subject` field), so v2 Block
  Kit formatting can use it without touching the hook.
- Channel-mismatch auth assumes `slack_channel_id` is a single value. v2
  multi-channel support would make this a set — additive schema change.

## Implementation Footprint

| Area | Files |
|---|---|
| Profile schema | `pkg/profile/types.go`, `pkg/profile/schemas/sandbox_profile.schema.json`, `pkg/profile/validate.go` |
| Compiler | `pkg/compiler/userdata.go` (the hook script is inlined as a heredoc in this file — phase 62 did not extract it; this phase extends the same heredoc), `pkg/compiler/compiler_test.go` |
| Sandbox binary | `cmd/km-slack/main.go`, `pkg/slack/client.go`, `pkg/slack/payload.go`, sidecar `Makefile` |
| Bridge Lambda | `cmd/km-slack-bridge/main.go`, `pkg/slack/bridge/*`, `infra/modules/lambda-slack-bridge/` |
| Operator CLI | `internal/app/cmd/slack.go` (new — `init`/`test`/`status`), `internal/app/cmd/create.go`, `internal/app/cmd/destroy.go`, `internal/app/cmd/doctor.go` |
| Infra wiring | `infra/live/.../management/lambdas/terragrunt.hcl`, new DynamoDB nonce table |
| Documentation | `docs/slack-notifications.md` (new), `CLAUDE.md` (new commands and env vars) |

No new sidecars in the network-enforcement sense (the bridge is a
management-plane Lambda, not a per-sandbox sidecar).

## Suggested Wave Decomposition

This is a hint to `/gsd:plan-phase`, not a hard contract — the planner
can repartition. The seams below maximize parallelism while keeping
dependencies clean.

### Wave 1 — Foundations (parallelizable)

- **1A: Profile schema** — `pkg/profile/types.go`, schema JSON,
  validation rules + tests.
- **1B: Slack payload + client packages** — `pkg/slack/payload.go`,
  `pkg/slack/client.go` with no consumers yet (pure library code).
- **1C: Bridge Lambda handler skeleton** — `pkg/slack/bridge/handler.go`
  with verification logic, mocked Slack client. Unit tests pass without
  deploying.

### Wave 2 — Integration points (depends on Wave 1)

- **2A: Compiler env-var + hook-script changes** — depends on 1A.
  Extends the inline `km-notify-hook` heredoc in `pkg/compiler/userdata.go`
  for parallel dispatch and adds `KM_SLACK_*` vars to the
  `/etc/profile.d/km-notify-env.sh` template.
- **2B: `km-slack` binary** — depends on 1B. `cmd/km-slack/main.go`
  wiring CLI to the client.
- **2C: Bridge Lambda Terraform module + deploy wiring** — depends on
  1C. `infra/modules/lambda-slack-bridge/`, DynamoDB nonce table,
  `km init --lambdas` integration.

### Wave 3 — Operator surface (depends on Wave 2)

- **3A: `km slack init`/`test`/`status`** — depends on 2C (Lambda must
  be deployable). New `internal/app/cmd/slack.go`.
- **3B: `km create` channel provisioning** — depends on 1A + 2C.
  Extends create.go with shared/per-sandbox/override branches.
- **3C: `km destroy` archive flow** — depends on 1A + 2C. Extends
  destroy.go with final-post + archive.
- **3D: `km doctor` health checks** — depends on 3A.

### Wave 4 — End-to-end + docs (depends on Wave 3)

- **4A: E2E test harness** — opt-in CI flag, real workspace credentials
  in test environment.
- **4B: `docs/slack-notifications.md`** — operator guide.
- **4C: `CLAUDE.md` updates** — new commands, env vars, key locations.

### Why this decomposition

- Wave 1 is fully parallel — three independent packages, no cross-deps.
- Wave 2 unblocks consumers of Wave 1 outputs but tasks within the wave
  are still parallel.
- Wave 3 is where things become serial-by-CLI-surface (a single
  `slack.go` file owns init/test/status, but create/destroy/doctor live
  elsewhere and can run alongside).
- Wave 4 is verification + documentation, last because it depends on
  everything.

## Out of Scope (Explicitly)

- Slack interactive features — slash commands, buttons, modals.
- Closed-loop reply ingestion (operator's Slack reply → agent).
- Block Kit / rich formatting beyond bold subject header.
- Filtering by tool name (same as phase 62 — all Notification events
  fire if the gate is on).
- Per-event recipient routing.
- Multiple invite recipients.
- DM delivery.
- Slack-to-email bridging.
- Retroactive Slack support on existing sandboxes.

## Dependencies (All Complete)

- Phase 14 — sandbox identity / Ed25519 signed email (signing keys
  reused).
- Phase 23 — credential rotation (covers Slack bot token rotation via
  SSM update).
- Phase 27 — Claude Code OTEL integration (extended for Slack-bridge
  observability).
- Phase 39 — DynamoDB sandbox metadata (where `slack_channel_id` lands).
- Phase 45 — `km-send`/`km-recv` and operator email CLI (signing
  precedent, hook-script base).
- Phase 62 — operator-notify hook for permission and idle events
  (extended in this phase).

---

*Source-of-truth for phase planning. To register on the roadmap, run
`/gsd:add-phase` and reference this spec in the phase description.*
