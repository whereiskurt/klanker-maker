# Phase 72: Slack corporate-workspace support — Context

**Gathered:** 2026-05-06
**Status:** Ready for planning
**Source:** Brainstorming dialogue (Q1–Q4 + Approach 1 user-approved)

<domain>
## Phase Boundary

Support installing the KlankerMaker Slack app into a corporate workspace (e.g., the operator's
Greenhouse workspace) where most invitees are native members of that workspace. Today the
install model assumes the operator is **external** to the bot's workspace and must receive a
Slack Connect invite — that assumption is hard-coded into `km slack init`. After this phase,
the same install supports **both** patterns transparently: native workspace members get a
regular invite (`conversations.invite`), external collaborators get the existing Slack Connect
flow (`conversations.inviteShared`), and the system auto-detects which path to use.

The phase also delivers a `km slack manifest` command that renders a deployment-specific app
manifest for copy-paste into the Slack admin "From manifest" UI. This unblocks the install in a
new corporate workspace and ensures scope additions (notably `users:read.email`) ship as code,
not tribal knowledge.

**Out of scope:**
- Migration of existing PoC installs (multi-instance support already covers per-prefix isolation —
  a Greenhouse install runs alongside the PoC under a different `resource_prefix`).
- Bridge Lambda, signing, sidecars, Connect transport, existing channel/SSM keys (no changes).
- DM / mpim flows, multi-workspace `org_deploy_enabled`, OAuth flows beyond manifest install.
- Slack Enterprise Grid features, multi-team installs, workspace migration tooling.

</domain>

<decisions>
## Implementation Decisions

### Detection Strategy (Q1: B — auto-detect with fallback)

- **Primary path:** `users.lookupByEmail(email)` → on success, `conversations.invite(channelID, userID)`.
- **Fallback path:** on `users_not_found`, prompt the operator (interactive only):
  `"User not found in workspace. Send Slack Connect invite (requires Pro)? [y/N]"`. If yes,
  call `conversations.inviteShared(channelID, email)`. If no or non-interactive, return a
  `SkippedExternal` result so the caller can warn and proceed.
- **Connect failure:** if Connect returns `not_allowed_token_type` (free tier), surface the
  existing Pro-tier error message — don't swallow it.

### Where Invites Happen (Q2: C+D)

Three call sites, all routed through one orchestrator:
1. **`km slack init`** — keeps existing `--invite-email` single-recipient invite to the shared
   channel. Refactored to call the orchestrator instead of `InviteShared` directly. Behavior
   unchanged for existing PoC installs (operator email is external → falls back to Connect).
2. **New `km slack invite <email> [--channel <name|id>] [--external]`** — ad-hoc command for
   adding people to any channel anytime. Default channel is the SSM-stored shared channel.
   `--external` skips the lookup and goes straight to Connect (no prompt).
3. **`km create` profile field** — new `spec.cli.notifySlackInviteEmails: []string` runs the
   orchestrator for each email after the per-sandbox channel is created. **Fail-soft**: skip+warn
   on Connect-needed addresses (since `km create` may run from `km at`/scheduled), do not block
   the create.

### Connect Fallback UX (Q3: B — prompt before fallback)

- Interactive (TTY): prompt before Connect, default `N`.
- Non-interactive (piped, scheduled, `km create`): no prompt — return `SkippedExternal` and emit
  a stderr warning telling the operator to follow up with `km slack invite --external <email>`.

### Manifest Handling (Q4: A — `km slack manifest` generates)

- New standalone command `km slack manifest` reads the bridge URL from SSM
  (`{ssm_prefix}/slack/bridge-url`) and the resource_prefix from config, then renders an embedded
  JSON template to stdout.
- Template lives in code (Go embedded string or `embed.FS`); single source of truth for scopes.
- Template based on the production manifest at `/Users/khundeck/Downloads/km-personal.json`:
  - `display_information.name` parameterized: `KlankerMakerNotification` → derived from
    resource_prefix (e.g., `KlankerMaker-greenhouse`). Free choice — must remain a valid Slack
    app name (≤35 chars, alphanumeric + spaces + hyphens).
  - `bot_user.display_name` mirrors the app name.
  - `oauth_config.scopes.bot` — full union of currently-used scopes PLUS the new
    `users:read.email`. Final list:
    `["chat:write", "channels:manage", "channels:join", "channels:read", "channels:history",
      "groups:write", "groups:history", "conversations.connect:write", "reactions:read",
      "reactions:write", "files:write", "users:read.email"]`.
  - `settings.event_subscriptions.request_url` filled with the Lambda Function URL +
    `/events` path.
  - `settings.event_subscriptions.bot_events` retains `["message.channels", "message.groups"]`.
  - `socket_mode_enabled`, `org_deploy_enabled`, `token_rotation_enabled`, `is_mcp_enabled`,
    `pkce_enabled` all stay `false` (matching the production manifest).
- Output destination: stdout. No file written by default. Operator runs
  `km slack manifest > manifest.json` if they want a file.

### Architecture (Approach 1 — unified invite primitive + thin commands)

**New low-level methods in `pkg/slack/client.go`:**
- `LookupUserByEmail(ctx, email) (userID string, found bool, err error)` — wraps
  `users.lookupByEmail`. Returns `(empty, false, nil)` on `users_not_found`. Returns error for
  any other Slack error.
- `InviteUserToChannel(ctx, channelID, userID string) error` — wraps
  `conversations.invite`. Idempotent: treats `already_in_channel` as success.
- Keep `InviteShared(ctx, channelID, email)` unchanged.

**New orchestrator file `pkg/slack/invite.go`:**
- `EnsureMemberByEmail(ctx, channelID, email string, opts EnsureMemberOpts) (EnsureMemberResult, error)`
- `EnsureMemberOpts { ForceExternal bool; Interactive bool; Prompter Prompter }`
- `EnsureMemberResult` is a typed enum:
  - `InvitedDirect`   — looked up, regular invited
  - `InvitedConnect`  — fell back to Connect (and Connect succeeded)
  - `AlreadyMember`   — `conversations.invite` returned `already_in_channel`
  - `SkippedExternal` — non-interactive or operator declined Connect
  - `Failed`          — caller logs/handles; orchestrator returns the underlying error too

**Three callers reuse the orchestrator:**
- `internal/app/cmd/slack.go` (existing `km slack init`) — replace direct `InviteShared` call.
- `internal/app/cmd/slack_invite.go` (NEW) — cobra command wiring.
- `internal/app/cmd/create_slack.go` (existing) — extend to read profile field after channel
  creation; call orchestrator with `Interactive=false` for each email.

**Profile schema additions:**
- `pkg/profile/types.go` — add `NotifySlackInviteEmails []string \`yaml:"notifySlackInviteEmails,omitempty" json:"notifySlackInviteEmails,omitempty"\`` to the `CLI` struct.
- `pkg/profile/schemas/sandbox_profile.schema.json` — mirror the field with type `array`,
  items `string`, `format: email`.
- `pkg/profile/validate.go` — validate each entry with the same email-format check used
  elsewhere; reject if `notifySlackEnabled` is false.

**Manifest command:**
- `internal/app/cmd/slack_manifest.go` (NEW) — cobra command, registered under the
  `km slack` subcommand group.
- `internal/app/cmd/slack_manifest_template.json` (NEW, embedded via `//go:embed`) — the
  parameterized template.

### What Does NOT Change

- Bridge Lambda code (`cmd/km-slack-bridge/`, `pkg/slack/bridge/`) — no signing changes,
  no new actions.
- Sidecar binaries (`cmd/km-slack/`).
- DynamoDB tables, SSM key paths, IAM, Terragrunt modules.
- Existing channels (`#km-notifications`, `#sb-{id}`).
- Phase 67 inbound dispatch, Phase 67.1 ACK reaction, Phase 68 transcript streaming —
  all continue to work unchanged.

### Claude's Discretion

- Exact UX of the Connect-fallback prompt wording (must mention Pro requirement, confirm with
  default-N).
- Whether to short-circuit to `AlreadyMember` proactively via `conversations.members`
  (probably not — `conversations.invite` already returns `already_in_channel`; one fewer round-trip).
- File layout for the embedded template — single JSON file with Go template-style placeholders
  (`{{.AppName}}`, `{{.BridgeURL}}`) is the cleanest; `text/template` rendering keeps it simple.
- Test seam strategy: define a `Prompter` interface in `pkg/slack/invite.go` for unit-testable
  Connect-fallback prompts; wire stdin/stdout impl from the cmd layer.
- Whether `km slack init` warns when `users:read.email` is missing from the workspace's bot
  scopes (probably yes — same `VerifyEventsAPIScopes` pattern as Phase 67).

</decisions>

<specifics>
## Specific Ideas

### Reference manifest (current production)

`/Users/khundeck/Downloads/km-personal.json`:

```json
{
  "display_information": {
    "name": "KlankerMakerNotification",
    "description": "Get notifications from KlankerMaker sandboxes",
    "background_color": "#000000"
  },
  "features": {
    "bot_user": {
      "display_name": "KlankerMakerNotification",
      "always_online": false
    }
  },
  "oauth_config": {
    "scopes": {
      "bot": [
        "files:write", "channels:history", "channels:join", "channels:manage",
        "channels:read", "chat:write", "conversations.connect:write",
        "groups:history", "groups:write", "reactions:read", "reactions:write"
      ]
    },
    "pkce_enabled": false
  },
  "settings": {
    "event_subscriptions": {
      "request_url": "https://4meahsvr5yumhng37ovxxwuapq0krlnf.lambda-url.us-east-1.on.aws/events",
      "bot_events": ["message.channels", "message.groups"]
    },
    "org_deploy_enabled": false,
    "socket_mode_enabled": false,
    "token_rotation_enabled": false,
    "is_mcp_enabled": false
  }
}
```

The Phase 72 manifest adds `users:read.email` to `oauth_config.scopes.bot`. All other fields
parameterized by deployment.

### CLI surface (new)

```
km slack invite <email> [--channel <name|id>] [--external]
  --channel  channel name (e.g. "km-notifications") or ID (e.g. "C012ABCDE3F")
             default: SSM-stored shared channel
  --external skip lookup, send Slack Connect invite directly

km slack manifest [--app-name <name>]
  --app-name override the auto-derived app name (default: "KlankerMaker-{resource_prefix}")
  output: parameterized JSON to stdout
```

### Profile field example

```yaml
spec:
  cli:
    notifySlackEnabled: true
    notifySlackPerSandbox: true
    notifySlackInviteEmails:
      - alice@greenhouse.io
      - bob@greenhouse.io
```

When `km create` provisions this sandbox:
1. Per-sandbox channel `#sb-{id}` is created (existing flow).
2. For each email: orchestrator runs with `Interactive=false`.
3. Internal users land via regular invite. External users emit a stderr warning:
   `[warn] alice@external.com not in workspace; run \`km slack invite --external alice@external.com --channel sb-{id}\` to send a Connect invite.`

### Test surface

- Unit tests for `LookupUserByEmail`, `InviteUserToChannel` (mock HTTP in pattern of existing
  `client_test.go`).
- Unit tests for `EnsureMemberByEmail` covering all five result paths (mock client + mock prompter).
- Unit test for manifest rendering (golden-file compare against fixture).
- Cmd-level test for `km slack invite` (mock orchestrator; verify flag wiring + exit codes).
- Cmd-level test for `km slack manifest` (golden output for known SSM stub).
- Profile schema test for `notifySlackInviteEmails` (valid emails, validation when
  `notifySlackEnabled=false`).
- E2E: extend `test/e2e/slack/slack_e2e_test.go` to cover the new `km slack invite` happy path
  if/when token & test workspace are available; otherwise gate behind existing
  `KM_SLACK_E2E_TOKEN` env guard.

</specifics>

<deferred>
## Deferred Ideas

- **`km slack invite --bulk @file`** — read emails from a file. YAGNI for v1; one-at-a-time
  works.
- **Profile field at the platform-config level** (km-config.yaml) for global default invitees.
  YAGNI; per-profile is the right scope for now.
- **`reaction_added` event subscription** for the future "reactions-as-actions" feature
  (deferred from Phase 68; tracked separately in memory). NOT a Phase 72 deliverable.
- **Manifest diff/upgrade tool** that compares the rendered template against an installed app's
  scopes via `auth.test`. Out of scope; the existing scope-warning path in `km slack init`
  already covers drift detection.
- **Slack Connect retraction / un-invite** — Slack API doesn't expose this; out of scope.
- **Multi-workspace install fan-out** — single workspace per `resource_prefix`. Multi-tenant
  is an Org Grid feature.

</deferred>

---

*Phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator*
*Context gathered: 2026-05-06 via brainstorming dialogue*
