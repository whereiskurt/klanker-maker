# Phase 63: Slack-notify hook for Claude Code permission and idle events — Context

**Gathered:** 2026-04-29
**Status:** Ready for planning
**Source:** PRD Express Path (`docs/superpowers/specs/2026-04-29-slack-notify-hook-design.md`)

<domain>
## Phase Boundary

This phase extends Phase 62's operator-notify mechanism (signed email
on Claude Code `Notification` permission prompts and `Stop` idle
events) with a parallel Slack delivery channel. Same triggers, same
gates (`notifyOnPermission`, `notifyOnIdle`, cooldown). New transport:
a Slack workspace owned by klankermaker.ai, with the operator invited
to channels via Slack Connect from their own workspace.

**v1 scope:** post-only Slack delivery alongside email. Both channels
run in parallel through the existing `km-notify-hook` (extended for
multi-channel dispatch). Bot token never leaves AWS — sandboxes call a
new `km-slack-bridge` Lambda with Ed25519-signed payloads (same trust
model as `km-send` from Phase 45). Channel lifecycle (create at
`km create`, archive at `km destroy`) handled operator-side via direct
Slack Web API calls. Hybrid channel mode: default `#km-notifications`
shared across all sandboxes, opt-in per-sandbox `#sb-{id}` channels
via `notifySlackPerSandbox`.

**Out of scope (v2 forward-compatible):** closed-loop reply ingestion
(operator's Slack reply → agent), interactive Slack features
(slash commands, buttons, modals), Block Kit / rich formatting beyond
bold subject header, DM delivery, multiple invite recipients,
Slack-to-email bridging, retroactive Slack support on existing
sandboxes. The v1 design preserves forward compatibility — `thread_ts`
field wired but unused; `action` discriminator allows v2 to add
`react`/`update_message`/`dm` without breaking envelopes; `version: 1`
field on every payload enables co-existing rollouts; subject metadata
is structured (separate field, not concatenated) for future Block Kit.

**Dependencies (all complete):**
- Phase 14 — sandbox identity / Ed25519 signed email (signing keys
  reused at `/sandbox/{id}/signing-key`).
- Phase 23 — credential rotation (covers Slack bot token rotation via
  SSM update).
- Phase 27 — Claude Code OTEL integration (extended for Slack-bridge
  observability).
- Phase 39 — DynamoDB sandbox metadata (where `slack_channel_id`
  lands).
- Phase 45 — `km-send`/`km-recv` and operator email CLI (signing
  precedent, hook-script base; operator identity at
  `/sandbox/operator/signing-key`).
- Phase 62 — operator-notify hook (extended in this phase with
  multi-channel dispatch and `notifyEmailEnabled` toggle).

**Out-of-band operator setup (not part of phase scope):** operator
provisions a Pro Slack workspace at klankermaker.ai, creates a custom
Slack App inside it with bot scopes (`chat:write`, `channels:manage`,
`conversations.connect:write`, `groups:write`), installs to workspace,
captures bot token. Phase 63 work begins from there.

</domain>

<decisions>
## Implementation Decisions (LOCKED — from spec)

### Profile Schema (under `spec.cli`)

Five new fields. All optional. Defaults are implementation-defined
below:

| Field | Type | Default | Effect |
|---|---|---|---|
| `notifyEmailEnabled` | bool | `true` | When `false`, skip email dispatch in hook even if event gates fire. Backward-compat with phase 62 (default keeps email on). |
| `notifySlackEnabled` | bool | `false` | Enable Slack delivery for whatever events `notifyOn*` already gates |
| `notifySlackPerSandbox` | bool | `false` | Create `#sb-{id}` at `km create`, archive at `km destroy`. Ignored if `notifySlackEnabled: false` |
| `notifySlackChannelOverride` | string | unset → `/km/slack/shared-channel-id` | Hard-pin to a specific channel ID; overrides both shared and per-sandbox modes |
| `slackArchiveOnDestroy` | bool | `true` | Per-sandbox channels only. Set `false` to preserve trail post-teardown |

**Validation rules:**
- `notifySlackPerSandbox: true` AND `notifySlackChannelOverride: <set>`
  → validation error (mutual exclusion).
- `notifySlackPerSandbox: true` AND `notifySlackEnabled: false`
  → validation warning (no-op).
- `slackArchiveOnDestroy` set without `notifySlackPerSandbox: true`
  → validation warning (no-op).
- `notifySlackChannelOverride` not matching `^C[A-Z0-9]+$`
  → validation error.
- `notifyEmailEnabled: false` AND `notifySlackEnabled: false`
  → validation warning (no notification channels).

Schema additions go in `pkg/profile/types.go` and
`pkg/profile/schemas/sandbox_profile.schema.json`.

**No CLI flag overrides in v1.** All five fields are profile-only.

### Trust Model (Reuses Existing Ed25519 Plumbing)

- **Sandbox-side signing:** existing keys at `/sandbox/{id}/signing-key`
  (Phase 14). The new `km-slack` binary signs payloads with the same
  key already used by `km-send`.
- **Operator-side signing:** existing key at
  `/sandbox/operator/signing-key` (provisioned by `km init`, used by
  `km email send --from operator`). Operator-driven actions in this
  phase (`km slack init`, `km slack test`, `km destroy` archive) are
  signed with this key.
- **Bridge Lambda accepts BOTH** sandbox and operator signatures, with
  action-level authorization distinguishing what each is allowed to do.

### One-Time Operator Bootstrap: `km slack init`

Interactive command, idempotent. New `internal/app/cmd/slack.go`
sub-command tree. Steps:

1. Prompt for bot token (`xoxb-...`). Validate via `auth.test`. Store
   at `/km/slack/bot-token` (SSM SecureString, KMS-encrypted with
   existing platform key).
2. Store workspace metadata (workspace ID, team name) at
   `/km/slack/workspace`.
3. Prompt for default invite recipient email. Store at
   `/km/slack/invite-email`.
4. Create default shared channel `#km-notifications` (override via
   `--shared-channel`) using `conversations.create`. Send Slack Connect
   invite via `conversations.inviteShared` to invite-email. Store
   channel ID at `/km/slack/shared-channel-id`.
5. Deploy `km-slack-bridge` Lambda if not already present. Store its
   Function URL at `/km/slack/bridge-url`.

**Idempotence:** re-running with same workspace updates token and
re-deploys Lambda but does not recreate shared channel if
`/km/slack/shared-channel-id` already populated and channel still
exists.

**Flags:**
- `--bot-token <token>` (skip prompt; for CI)
- `--invite-email <addr>` (skip prompt)
- `--shared-channel <name>` (override default `km-notifications`)
- `--force` (recreate shared channel and re-send invite even if
  existing)

**Companion commands:**
- `km slack test` — posts test message to shared channel via bridge
  Lambda using operator signing key. Confirms end-to-end: SSM →
  Lambda → Slack API → channel.
- `km slack status` — shows current Slack config (workspace, channel
  ID, invite-email, Lambda URL, last-test timestamp).

### Per-Sandbox Channel Lifecycle

**At `km create`:** When resolved profile has `notifySlackEnabled: true`,
after sandbox infra is provisioned but before user-data finalizes, the
operator-side CLI:

- **Shared mode (default):** read `/km/slack/shared-channel-id`, write
  to sandbox metadata as `slack_channel_id`. No Slack API call.
- **Per-sandbox mode (`notifySlackPerSandbox: true`):** call
  `conversations.create` for `#sb-{id}` (or `#sb-{alias}` if `--alias`
  set). Call `conversations.inviteShared` to `/km/slack/invite-email`.
  Store new channel ID in metadata. **Failure here aborts `km create`**
  and tears down any partially-created infra.
- **Override mode (`notifySlackChannelOverride: "C0123ABC"`):**
  validate channel exists and bot is a member; write to metadata.
  Failure aborts `km create`.

Channel ID is then injected into sandbox's
`/etc/profile.d/km-notify-env.sh` (NOT `/etc/environment` — Phase 62
chose `profile.d` because Amazon Linux 2 SSM sessions reliably source
it; Phase 63 uses the same file) as `KM_SLACK_CHANNEL_ID`. Bridge
Lambda's URL goes in as `KM_SLACK_BRIDGE_URL`. `KM_NOTIFY_SLACK_ENABLED`
and `KM_NOTIFY_EMAIL_ENABLED` join the existing Phase 62 vars.

**At `km destroy`:** Read metadata. If sandbox was provisioned in
per-sandbox mode AND `slackArchiveOnDestroy: true`:

1. Post final message: "Sandbox `sb-abc123` destroyed at `<timestamp>`."
2. Call `conversations.archive` via bridge Lambda (operator-signed).
3. Archive failure → log warning, continue with destroy. Don't block
   teardown on Slack API issues.

For `slackArchiveOnDestroy: false`, channel persists; "destroyed;
channel preserved per profile" line is still posted. For
shared-channel sandboxes, no cleanup — channel persists.

For `km pause` / `km resume`: no Slack action in v1.

### `km doctor` Additions

Two new health checks:

- **Slack token validity** — calls `auth.test` via Lambda; flags
  expired/revoked tokens.
- **Stale per-sandbox channels** — for each DynamoDB sandbox record
  with `slack_channel_id`, verify sandbox still exists; flag channels
  whose sandbox was destroyed but archive failed (only when
  `slackArchiveOnDestroy: true` was originally set).

### Sandbox-Side: `km-slack` Binary

Single Go binary at `/opt/km/bin/km-slack`. Parallels `km-send` from
Phase 45. v1 needs one command shape:

```
km-slack post --channel <id> --subject <text> --body <file> [--thread <ts>]
```

| Flag | Required | Notes |
|---|---|---|
| `--channel <id>` | yes | Slack channel ID (`C0123ABC`); must match `KM_SLACK_CHANNEL_ID` |
| `--subject <text>` | yes | Used by Lambda for bold header line |
| `--body <file>` | yes | Path to plain-text body. **File only**, no stdin (OpenSSL 3.5+ rationale, same as `km-send`) |
| `--thread <ts>` | no | Thread reply parent ts. Wired but unused by v1 hook (reserved for v2) |

**Behavior:**
1. Read body file. **Cap at 40 KB** (Slack's `chat.postMessage` text
   limit). Refuse early on overflow.
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
3. Sign canonical JSON (sorted keys, no whitespace) with sandbox
   Ed25519 private key from `/sandbox/{id}/signing-key`. Cache for
   process lifetime.
4. POST to `$KM_SLACK_BRIDGE_URL` with headers:
   - `X-KM-Sender-ID: sb-abc123`
   - `X-KM-Signature: <base64 ed25519 sig>`
   - `Content-Type: application/json`
5. **Retry policy:** 3 attempts on HTTP 5xx / 503 / network error,
   backoff 1s/2s/4s. HTTP 4xx → exit 1 immediately. 2xx → exit 0.

**Why a Go binary, not bash:** crypto + retry + JSON canonicalization
cleaner and more testable. Matches `km-send` precedent. Static binary
deployed via existing sidecar pipeline (`km init --sidecars`).

### Hook Integration (Extends Phase 62)

The Phase 62 hook is **inlined as a heredoc in
`pkg/compiler/userdata.go`** (NOT in a separate `assets/*.sh` file).
Phase 63 extends the same heredoc with multi-channel dispatch:

```bash
# ... gate check (KM_NOTIFY_ON_*), cooldown, build $subject + $body_file
# (existing Phase 62 logic, unchanged)

sent_any=0

# Email path — existing Phase 62 logic gated by KM_NOTIFY_EMAIL_ENABLED.
# Default "1" preserves Phase 62 backward compat.
if [[ "${KM_NOTIFY_EMAIL_ENABLED:-1}" == "1" ]]; then
  to_args=()
  [[ -n "${KM_NOTIFY_EMAIL:-}" ]] && to_args=(--to "$KM_NOTIFY_EMAIL")
  /opt/km/bin/km-send "${to_args[@]+"${to_args[@]}"}" \
    --subject "$subject" --body "$body_file" \
    && sent_any=1 \
    || true
fi

# Slack path — new in Phase 63.
if [[ "${KM_NOTIFY_SLACK_ENABLED:-0}" == "1" && -n "${KM_SLACK_CHANNEL_ID:-}" ]]; then
  /opt/km/bin/km-slack post \
    --channel "$KM_SLACK_CHANNEL_ID" \
    --subject "$subject" \
    --body "$body_file" \
    && sent_any=1 \
    || true
fi

# Cooldown updates iff at least one channel succeeded
[[ $sent_any -eq 1 ]] && date +%s > "$last_file"

exit 0
```

**Invariants preserved from Phase 62:**
- Hook **never blocks Claude.** Always exits 0 even if both fail.
- **Cooldown is per-sandbox, shared across both events AND both channels.**
  A Notification within the cooldown window emits to neither channel.
- **Body file is unchanged.** Same content goes to email and Slack.
  Email gets a subject line; Slack uses structured `subject` field for
  bold header.

**Slack message format (rendered by bridge Lambda):**
```
*[sb-abc123]* needs permission

Claude needs your permission to use Bash

---
Attach:  km agent attach sb-abc123
Results: km agent results sb-abc123
```

`*...*` is Slack's bold mrkdwn. `unfurl_links: false` and
`unfurl_media: false` are set on the API call.

### Bridge Lambda: `km-slack-bridge`

Go Lambda, deployed via `km init --lambdas`. Lambda Function URL with
auth mode `NONE` — application-layer Ed25519 signatures provide auth.
Same model as existing operator email Lambda.

**Request envelope** (single discriminated shape):

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

**Verification flow (per request):**

1. **Parse envelope.** Reject malformed JSON / missing required fields
   with HTTP 400.
2. **Replay protection.**
   - Verify `timestamp` within ±5 minutes of Lambda clock. Older → 401.
   - Verify `nonce` not in `km_slack_bridge_nonces` DynamoDB table
     (TTL = 10min). Conditional-write to insert; collision → 401.
3. **Signature verification.**
   - `sender_id == "operator"` → fetch
     `/sandbox/operator/signing-public-key` from SSM.
   - `sender_id == "sb-*"` → fetch
     `/sandbox/{id}/signing-public-key` from SSM.
   - Verify Ed25519 over canonical JSON (sorted keys, no whitespace).
     Mismatch → 401. Sender not found → 404.
4. **Action authorization.**
   - `archive` or `test` from a sandbox sender → 403.
   - `post` from a sandbox sender to a channel that doesn't match the
     sandbox's metadata `slack_channel_id` → 403. (Compromised sandbox
     can't post to channels other than its own.)
   - `post` from operator → any channel allowed.
5. **Bot token fetch.** Read `/km/slack/bot-token` from SSM (cached
   for Lambda warm lifetime, ~15min).
6. **Execute against Slack Web API.**
   - `post` → `chat.postMessage` with formatted bold header + body.
   - `archive` → `conversations.archive`.
   - `test` → `chat.postMessage` to shared channel with fixed test
     text.
7. **Response.**
   - Slack 2xx → `{"ok": true, "ts": "<message_ts>"}` HTTP 200.
   - Slack 429 → HTTP 503 with `Retry-After`.
   - Slack other error → HTTP 502 with Slack error code surfaced.

**Channel-mismatch authorization (the meaningful blast-radius
reducer):** sandbox `sb-abc123` posts with `channel: C09FOO123`.
Lambda reads `km_sandboxes.sb-abc123.slack_channel_id` from DynamoDB.
If mismatch → 403. If sandbox has no `slack_channel_id` → 403.
Compromised sandbox in per-sandbox mode can only spam its own channel.
In shared-channel mode, all sandboxes have same `slack_channel_id`, so
check passes for any sandbox (correct — shared is shared).

**IAM execution role:**
- `ssm:GetParameter` on `/km/slack/*`, `/sandbox/*/signing-public-key`,
  `/sandbox/operator/signing-public-key`
- `kms:Decrypt` on platform KMS key alias (for bot token SecureString)
- `dynamodb:PutItem`, `GetItem` on `km_slack_bridge_nonces`
- `dynamodb:GetItem` on existing sandbox metadata table
- `logs:*` for CloudWatch

**What this Lambda does NOT do:**
- Channel creation (stays operator-side; sync error handling fits
  poorly in async Lambda).
- Connect-invite sending (same reason).
- Interactive Slack features.
- Retry from Lambda side (`km-slack` retries; Lambda is single-shot).

**Operational concerns:**
- Cold start: 200–400ms hit on operator calls; sandbox calls are async
  so don't care.
- Bot token rotation: update SSM, Lambda picks up on next cold start
  or after 15min cache TTL. No sandbox redeploy.
- Signing key rotation: handled by Phase 23.
- Observability: CloudWatch structured JSON logs. Surfaced via
  `km otel <sandbox-id>` (extends Phase 27) for sandbox-side calls
  and `km slack status` for aggregate metrics.

### Test Surface

**Profile schema tests (`pkg/profile/validate_test.go`):**
- All five validation rules above (mutual exclusion, no-op warnings,
  channel-ID regex, no-channels warning).

**Compiler unit tests (`pkg/compiler/compiler_test.go`):**
- Profile with `notifySlackEnabled: false` → no `KM_NOTIFY_SLACK_*`
  lines in `/etc/profile.d/km-notify-env.sh`, no `km-slack` invocation
  in hook script.
- Profile with `notifySlackEnabled: true` shared mode →
  `KM_NOTIFY_SLACK_ENABLED=1`, `KM_SLACK_CHANNEL_ID=<shared-id>`,
  `KM_SLACK_BRIDGE_URL=<url>` written.
- Profile with `notifySlackPerSandbox: true` → metadata
  `slack_channel_id` injected, not the shared one.
- Profile with `notifySlackChannelOverride: "C0123ABC"` → that channel
  ID injected verbatim.
- Profile with `notifyEmailEnabled: false` →
  `KM_NOTIFY_EMAIL_ENABLED=0` written, hook skips `km-send`.
- Profile with `notifyEmailEnabled` unset →
  `KM_NOTIFY_EMAIL_ENABLED` not emitted (hook default `1` takes
  effect — Phase 62 backward compat).
- User-supplied `settings.json` via `configFiles` → still gets
  Phase 62's hook entries merged (regression check).

**`km-slack` binary tests:**
- `pkg/slack/payload_test.go` — canonical-JSON construction, signature
  determinism, body size enforcement (40 KB cap).
- `pkg/slack/client_test.go` — HTTP retry behavior with stub server:
  200 → exit 0; 429 → backoff and retry; persistent 5xx → exit 1
  after 3 attempts; network error → backoff.
- `cmd/km-slack/main_test.go` — end-to-end with stub Lambda Function
  URL, real Ed25519 keys, verify request shape and headers.

**`km-notify-hook` script tests (extending Phase 62 harness):**
- `KM_NOTIFY_SLACK_ENABLED=0` + `KM_NOTIFY_EMAIL_ENABLED=1` → only
  `km-send` invoked.
- `KM_NOTIFY_SLACK_ENABLED=1` + `KM_NOTIFY_EMAIL_ENABLED=0` → only
  `km-slack` invoked.
- Both enabled → both invoked; cooldown updates iff at least one
  succeeded.
- `km-slack` returns 1 + `km-send` returns 0 → cooldown updates,
  hook exits 0.
- Both fail → cooldown does not update, hook exits 0.
- `KM_NOTIFY_EMAIL_ENABLED` unset (Phase 62 backward compat) → email
  path runs.
- Stubs for `km-send` and `km-slack` via PATH override.

**Bridge Lambda tests (`pkg/slack/bridge/handler_test.go`):**
- Valid sandbox `post` to its own channel → Slack `chat.postMessage`
  called with expected args.
- Valid sandbox `post` to non-matching channel → 403.
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

**CLI command tests (`internal/app/cmd/`):**
- `km slack init` happy path with mocked SSM/Slack/Lambda — verify
  all four SSM params written, Lambda deployed, shared channel
  created, invite sent.
- `km slack init --force` with existing config — recreates channel,
  re-sends invite.
- `km slack test` — invokes bridge Lambda, parses response, prints
  test message timestamp.
- `km slack status` — reads SSM, validates token via Lambda
  `auth.test`, prints summary.
- `km create` with `notifySlackEnabled: true` per-sandbox mode —
  Slack channel created before infra, channel ID in metadata,
  `KM_SLACK_*` injected.
- `km create` failure during channel creation → infra rollback (test
  unwind path).
- `km destroy` with per-sandbox + archive enabled → final post +
  archive call sequence.
- `km destroy` with archive disabled → final post only, no archive.
- `km destroy` archive failure → warning logged, destroy continues.

**`km doctor` test additions:**
- Slack token expired → reported as failed health check.
- Sandbox destroyed but channel not archived → reported as stale
  resource.

**E2E (manual / opt-in CI):**
- Real klankermaker.ai workspace, real
  `kurt.hundeck@greenhouse.io` invite acceptance done out-of-band
  once.
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

### Implementation Footprint (Files Touched)

| Area | Files |
|---|---|
| Profile schema | `pkg/profile/types.go`, `pkg/profile/schemas/sandbox_profile.schema.json`, `pkg/profile/validate.go` |
| Compiler | `pkg/compiler/userdata.go` (extends inlined Phase 62 hook heredoc), `pkg/compiler/compiler_test.go` |
| Sandbox binary | `cmd/km-slack/main.go`, `pkg/slack/client.go`, `pkg/slack/payload.go`, sidecar `Makefile` |
| Bridge Lambda | `cmd/km-slack-bridge/main.go`, `pkg/slack/bridge/*`, `infra/modules/lambda-slack-bridge/` |
| Operator CLI | `internal/app/cmd/slack.go` (new — `init`/`test`/`status`), `internal/app/cmd/create.go`, `internal/app/cmd/destroy.go`, `internal/app/cmd/doctor.go` |
| Infra wiring | `infra/live/.../management/lambdas/terragrunt.hcl`, new DynamoDB nonce table |
| Documentation | `docs/slack-notifications.md` (new), `CLAUDE.md` (new commands and env vars) |

**No** new sidecars in the network-enforcement sense (the bridge is a
management-plane Lambda, not a per-sandbox sidecar).

### Suggested Wave Decomposition (Hint to Planner)

This is a hint, not a hard contract — the planner can repartition.

**Wave 1 — Foundations (parallelizable, no cross-deps):**
- 1A: Profile schema (`pkg/profile/types.go`, schema JSON, validation
  rules + tests).
- 1B: Slack payload + client packages (`pkg/slack/payload.go`,
  `pkg/slack/client.go` — pure library code, no consumers yet).
- 1C: Bridge Lambda handler skeleton (`pkg/slack/bridge/handler.go`
  with verification logic, mocked Slack client; unit tests pass
  without deploying).

**Wave 2 — Integration points (depends on Wave 1):**
- 2A: Compiler env-var + hook-script changes (depends on 1A; extends
  inline `km-notify-hook` heredoc in `pkg/compiler/userdata.go` for
  parallel dispatch and adds `KM_SLACK_*` vars to
  `/etc/profile.d/km-notify-env.sh` template).
- 2B: `km-slack` binary (depends on 1B; `cmd/km-slack/main.go`).
- 2C: Bridge Lambda Terraform module + deploy wiring (depends on 1C;
  `infra/modules/lambda-slack-bridge/`, DynamoDB nonce table,
  `km init --lambdas` integration).

**Wave 3 — Operator surface (depends on Wave 2):**
- 3A: `km slack init` / `test` / `status` (depends on 2C; new
  `internal/app/cmd/slack.go`).
- 3B: `km create` channel provisioning (depends on 1A + 2C; extends
  `create.go` with shared/per-sandbox/override branches).
- 3C: `km destroy` archive flow (depends on 1A + 2C; extends
  `destroy.go` with final-post + archive).
- 3D: `km doctor` health checks (depends on 3A).

**Wave 4 — End-to-end + docs (depends on Wave 3):**
- 4A: E2E test harness (opt-in CI flag, real workspace credentials in
  test environment).
- 4B: `docs/slack-notifications.md` (operator guide).
- 4C: `CLAUDE.md` updates (new commands, env vars, key locations).

</decisions>

<specifics>
## Specific References

- **Spec source:** `docs/superpowers/specs/2026-04-29-slack-notify-hook-design.md`
  (committed in `5dbde15`, with option (ii) email-toggle decision
  locked in).
- **Predecessor spec:** `docs/superpowers/specs/2026-04-26-operator-notify-hook-design.md`
  (Phase 62 — email-only).
- **Existing pattern — `km-send`:** `internal/app/cmd/email.go` and
  related code; signs messages with sandbox Ed25519 key from
  `/sandbox/{id}/signing-key`. Phase 63's `km-slack` reuses the same
  signing key per sandbox.
- **Existing pattern — operator identity:**
  `internal/app/cmd/init.go` provisions
  `/sandbox/operator/signing-key` at `km init` time. Phase 63 reuses
  this for operator-side signed actions.
- **Existing pattern — Phase 62 hook:** inlined heredoc in
  `pkg/compiler/userdata.go` (search for "Phase 62 — HOOK-01" comment,
  ~line 343). Phase 63 extends this same heredoc; does NOT extract it
  to a separate asset file.
- **Existing pattern — env file:**
  `/etc/profile.d/km-notify-env.sh` written by compiler when
  `NotifyEnv` template data is non-empty (~line 432 in
  `userdata.go`). Phase 63 adds `KM_NOTIFY_SLACK_ENABLED`,
  `KM_SLACK_CHANNEL_ID`, `KM_SLACK_BRIDGE_URL`,
  `KM_NOTIFY_EMAIL_ENABLED` to the same emission.
- **Slack Web API endpoints used:**
  - `auth.test` — token validity check (used in `km slack init` and
    `km doctor`).
  - `chat.postMessage` — primary post action.
  - `conversations.create` — per-sandbox channel creation.
  - `conversations.inviteShared` — Slack Connect invite to external
    email.
  - `conversations.archive` — channel cleanup on destroy.
- **Slack channel ID regex:** `^C[A-Z0-9]+$` (channel IDs always
  start with `C`).
- **Slack `chat.postMessage` text limit:** 40 KB. Enforce client-side
  in `km-slack` to fail early.
- **OpenSSL 3.5+ signing constraint:** body file required, not stdin
  (per `CLAUDE.md` and `docs/multi-agent-email.md`). Applies to
  `km-slack` the same as `km-send`.
- **Required Slack App bot scopes** (operator sets up out-of-band):
  `chat:write`, `channels:manage`, `conversations.connect:write`,
  `groups:write` (for private channels if needed).

</specifics>

<deferred>
## Deferred Ideas (v2+ — Out of Scope)

Explicitly out of scope for this phase, but the v1 design preserves
forward compatibility:

- **Closed-loop reply ingestion** — operator's Slack reply → agent.
  Same v2 deferral as Phase 62's email closed-loop. `thread_ts` field
  in `km-slack` payload is wired but unused by v1 hook; v2 will use it
  to thread per-turn `Notification` events under a root message.
- **`action` discriminator in bridge envelope** allows v2 to add
  `react`, `update_message`, `dm` actions without breaking v1.
- **`version: 1` field** on every payload enables breaking-change
  rollouts (Lambda accepts both during transition).
- **Subject metadata is structured** (separate `subject` field, not
  concatenated into body) so v2 Block Kit formatting can use it
  without touching the hook.
- **Channel-mismatch auth assumes single `slack_channel_id`.** v2
  multi-channel support would make this a set — additive schema
  change.
- **Slack interactive features** — slash commands, buttons, modals.
- **Block Kit / rich formatting** beyond bold subject header.
- **Filtering by tool name** (e.g., "only notify on Bash permissions")
  — same as Phase 62, all events fire if gate is on.
- **Per-event recipient routing.** One channel per sandbox.
- **Multiple invite recipients** — one platform-wide invite email; add
  humans via Slack UI post-acceptance.
- **DM delivery** — would require `im:write` scope and per-user
  resolution.
- **Slack-to-email bridging** — Slack and email are independent
  channels; no automatic forwarding either direction.
- **Retroactive Slack support on existing sandboxes** — same
  constraint as Phase 62. Requires `km destroy && km create`.
- **CLI flag overrides for the five new fields** — profile-only in v1
  (matches Phase 62's precedent of profile-only cooldown / recipient).
- **`km pause` / `km resume` Slack notifications** — silent in v1.
  Optional polish for v2.

</deferred>

---

*Phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events*
*Context gathered: 2026-04-29 via PRD Express Path*
*Source spec: docs/superpowers/specs/2026-04-29-slack-notify-hook-design.md (5dbde15)*
