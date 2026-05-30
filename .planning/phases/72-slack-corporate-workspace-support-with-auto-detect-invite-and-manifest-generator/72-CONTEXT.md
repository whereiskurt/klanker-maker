# Phase 72: Slack corporate-workspace support — Context

**Gathered:** 2026-05-06
**Status:** Ready for planning
**Source:** Brainstorming dialogue (Q1–Q4 + Approach 1 user-approved)

<domain>
## Phase Boundary

Support installing the KlankerMaker Slack app into a corporate workspace (e.g., the operator's
Corporate workspace) where most invitees are native members of that workspace. Today the
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
  a Corporate install runs alongside the PoC under a different `resource_prefix`).
- Bridge Lambda, signing, sidecars, Connect transport, existing channel/SSM keys (no changes).
- DM / mpim flows, multi-workspace `org_deploy_enabled`, OAuth flows beyond manifest install.
- Slack Enterprise Grid features, multi-team installs, workspace migration tooling.

</domain>

<decisions>
## Implementation Decisions

### Detection Strategy (Q1: B — auto-detect with fallback)

- **Primary path:** `users.lookupByEmail(email)` → on success, `conversations.invite(channelID, userID)`.
- **Fallback path:** on `users_not_found`, the orchestrator chooses based on `EnsureMemberOpts`:
  - `ForceExternal` → Connect directly, no prompt (the `km slack invite --external` path).
  - `Interactive` + `Prompter` set → prompt the operator:
    `"User not found in workspace. Send Slack Connect invite (requires Pro)? [y/N]"`. If yes,
    `conversations.inviteShared(channelID, email)`; if no, `SkippedExternal`.
  - `AutoConnect` true → Connect directly, no prompt (the `km create` path when
    `spec.cli.useSlackConnect` is true; see Q3).
  - otherwise → `SkippedExternal` so the caller can warn and proceed.
- **Connect failure:** if Connect returns `not_allowed_token_type` (free tier), surface the
  existing Pro-tier error message — don't swallow it (caller maps to `Failed`).

### Where Invites Happen (Q2: C+D)

Four call sites, all routed through one orchestrator:
1. **`km slack init`** — keeps existing `--invite-email` single-recipient invite to the shared
   channel. Refactored to call the orchestrator instead of `InviteShared` directly. Behavior
   unchanged for existing PoC installs (operator email is external → falls back to Connect).
2. **New `km slack invite <email> [--channel <name|id>] [--external] [--dry-run]`** — ad-hoc
   command for adding people to any channel anytime. Default channel is the SSM-stored shared
   channel. `--external` skips the lookup and goes straight to Connect (no prompt). `--dry-run`
   does the read-only lookup and prints the action it WOULD take (native invite vs Connect vs
   not-a-member) without sending anything — a zero-side-effect probe for validating the
   auto-detect against a live workspace without a sandbox or any writes.
3. **`km create` primary operator invite** — the existing per-sandbox operator invite
   (`create_slack.go` Mode 2: read `{prefix}/slack/invite-email` from SSM, then invite to
   `#sb-{id}`) is refactored from raw `InviteShared` to the orchestrator with `Interactive=false`
   and **`AutoConnect=true` unconditionally**. The primary operator is therefore ALWAYS invited
   (preserving today's guarantee): a native workspace member gets `conversations.invite`; an
   external operator still gets a Slack Connect invite. This fixes the corporate case where the
   operator is a native member and the old Connect-only call would fail, leaving them out of the
   per-sandbox channel. **NOT gated by `useSlackConnect`** — that flag governs only the additional
   folks in call-site 4. Fail-soft (warn, never block).
4. **`km create` additional-folks list** — new `spec.cli.notifySlackInviteEmails: []string` runs
   the orchestrator for each ADDITIONAL email after the per-sandbox channel is created and the
   primary operator has been invited. Purely additive — does NOT replace call-site 3. New
   companion field `spec.cli.useSlackConnect` (`*bool`, **default true**) gates the Connect
   fallback for THIS list only (see Q3). **Fail-soft** regardless: a failed Connect or invite
   emits a stderr warning, never blocks the create (since `km create` may run from
   `km at`/scheduled).

### Connect Fallback UX (Q3: prompt for ad-hoc; profile-gated auto-Connect for create)

- **Ad-hoc `km slack invite` (interactive/TTY):** prompt before Connect, default `N`. Unchanged.
- **`km create` (non-interactive, profile-driven):** governed by `spec.cli.useSlackConnect`
  (`*bool`, default true; nil ⇒ true):
  - `useSlackConnect: true` (default) → auto-Connect external addresses with **no prompt**
    (`AutoConnect=true`). Result `InvitedConnect` on success; `Failed` (fail-soft warning) if
    Connect errors, e.g. free-tier `not_allowed_token_type`.
  - `useSlackConnect: false` → do **not** Connect; return `SkippedExternal` and emit a stderr
    warning telling the operator to follow up with
    `km slack invite --external <email> --channel sb-{id}`.
- **Scope:** `useSlackConnect` only gates the additional-folks `notifySlackInviteEmails` loop
  (Q2 call-site 4). It does NOT govern: `km slack invite` (call-site 2, interactive prompt +
  `--external`), `km slack init` (call-site 1, operator invite), or the `km create` **primary
  operator invite** (call-site 3, always `AutoConnect=true` so the operator is always invited).
- **Rationale for default true:** an operator who lists external collaborators in
  `notifySlackInviteEmails` almost always wants them invited; requiring a manual follow-up per
  external address was friction. `useSlackConnect: false` preserves the prior skip+warn behavior
  for operators who want to keep create-time invites workspace-internal only.

### Manifest Handling (Q4: A — `km slack manifest` generates)

- New standalone command `km slack manifest` reads the bridge URL from SSM
  (`{ssm_prefix}/slack/bridge-url`) and the resource_prefix from config, then renders an embedded
  JSON template to stdout.
- Template lives in code (Go embedded string or `embed.FS`); single source of truth for scopes.
- Template based on the production manifest at `/Users/khundeck/Downloads/km-personal.json`:
  - `display_information.name` parameterized: `KlankerMakerNotification` → derived from
    resource_prefix (e.g., `KlankerMaker-corporate`). Free choice — must remain a valid Slack
    app name (≤35 chars, alphanumeric + spaces + hyphens).
  - `bot_user.display_name` mirrors the app name.
  - `oauth_config.scopes.bot` — full union of currently-used scopes PLUS the new
    `users:read.email`. Final list (13 scopes):
    `["chat:write", "channels:manage", "channels:join", "channels:read", "channels:history",
      "groups:write", "groups:history", "conversations.connect:write", "reactions:read",
      "reactions:write", "files:write", "files:read", "users:read.email"]`.
    NOTE: `files:read` is REQUIRED by Phase 75 (inbound file attachments — `files.info` +
    download from `files.slack.com`) and is verified by `km doctor`'s inbound-scope check
    (`internal/app/cmd/doctor_slack.go` `required = [channels:history, groups:history,
    reactions:write, files:read]`). The reference manifest at
    `/Users/khundeck/Downloads/km-personal.json` predates Phase 75 and is MISSING `files:read` —
    the Phase 72 manifest MUST include it so a freshly-installed app passes `km doctor` and can
    process inbound files. `users:read.email` is the Phase-72-specific addition.
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
- `EnsureMemberOpts { ForceExternal bool; Interactive bool; AutoConnect bool; Prompter Prompter }`
  - `AutoConnect`: non-interactive auto-fallback to Connect on lookup miss (no prompt). Set by
    the `km create` loop from `spec.cli.useSlackConnect`. Ignored when `ForceExternal` or a
    `Prompter`-driven interactive prompt already handled the fallback.
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
- `pkg/profile/types.go` — add to the `CLI` struct:
  - `NotifySlackInviteEmails []string \`yaml:"notifySlackInviteEmails,omitempty" json:"notifySlackInviteEmails,omitempty"\``
  - `UseSlackConnect *bool \`yaml:"useSlackConnect,omitempty" json:"useSlackConnect,omitempty"\`` — pointer so nil ⇒ default true.
- `pkg/profile/schemas/sandbox_profile.schema.json` — mirror `notifySlackInviteEmails` (type
  `array`, items `string`, `format: email`) and `useSlackConnect` (type `boolean`, default
  `true`).
- `pkg/profile/validate.go` — validate each `notifySlackInviteEmails` entry with the same
  email-format check used elsewhere; reject if `notifySlackEnabled` is false (Rule SE1).
  `useSlackConnect` needs no new reject rule — it is inert when the invite list is empty.

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

The Phase 72 manifest's `oauth_config.scopes.bot` is the full 13-scope union. Versus the
reference manifest above (11 scopes, May 2026) it adds TWO: `files:read` (Phase 75 inbound file
attachments — already shipped; the reference manifest is stale) and `users:read.email` (the
Phase 72 addition). All other fields parameterized by deployment.

### CLI surface (new)

```
km slack invite <email> [--channel <name|id>] [--external] [--dry-run]
  --channel  channel name (e.g. "km-notifications") or ID (e.g. "C012ABCDE3F")
             default: SSM-stored shared channel
  --external skip lookup, send Slack Connect invite directly
  --dry-run  read-only: look up the email and print whether it would be invited
             natively or via Slack Connect; send nothing (safe probe for a live
             workspace, no sandbox required)

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
    useSlackConnect: true          # default true; omit for the same behavior
    notifySlackInviteEmails:
      - alice@example.com
      - bob@example.com
```

When `km create` provisions this sandbox (with `useSlackConnect` true/unset):
1. Per-sandbox channel `#sb-{id}` is created (existing flow).
2. For each email: orchestrator runs with `Interactive=false`, `AutoConnect=true`.
3. Internal users land via regular invite (`InvitedDirect`); external users get an automatic
   Slack Connect invite (`InvitedConnect`) — no prompt, no manual follow-up.
4. If Connect fails (e.g. free-tier workspace) the email is logged as a fail-soft warning and
   the create proceeds.

With `useSlackConnect: false`, step 3 instead skips external users and emits a stderr warning:
`[warn] bob@external.com is not a member of the Slack workspace; not sending Connect invite.`
`  To send one: km slack invite --external bob@external.com --channel sb-{id}`

### Test surface

- Unit tests for `LookupUserByEmail`, `InviteUserToChannel` (mock HTTP in pattern of existing
  `client_test.go`).
- Unit tests for `EnsureMemberByEmail` covering all five result paths (mock client + mock
  prompter), including the non-interactive `AutoConnect=true` → `InvitedConnect` path and the
  `AutoConnect=false` non-interactive → `SkippedExternal` path.
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
