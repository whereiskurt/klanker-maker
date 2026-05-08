# Phase 72: Slack corporate-workspace support — Research

**Researched:** 2026-05-06
**Domain:** Slack Web API (users.lookupByEmail, conversations.invite, conversations.inviteShared) + Go cobra CLI integration + JSON manifest templating
**Confidence:** HIGH (Slack API verified via official docs; existing in-repo patterns read directly from source)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Detection Strategy (Q1: B — auto-detect with fallback)**
- Primary: `users.lookupByEmail(email)` → on success, `conversations.invite(channelID, userID)`.
- Fallback (interactive only): on `users_not_found`, prompt operator: `"User not found in workspace. Send Slack Connect invite (requires Pro)? [y/N]"`. If yes, call `conversations.inviteShared(channelID, email)`. If no or non-interactive, return `SkippedExternal`.
- Connect failure: if `not_allowed_token_type` (free tier), surface existing Pro-tier error message (don't swallow).

**Where Invites Happen (Q2: C+D — three call sites, single orchestrator)**
1. `km slack init` — refactor existing `--invite-email` to call orchestrator instead of `InviteShared` directly. Behavior unchanged for existing PoC installs.
2. `km slack invite <email> [--channel <name|id>] [--external]` — NEW ad-hoc command. Default channel = SSM-stored shared channel. `--external` skips lookup.
3. `km create` profile field `spec.cli.notifySlackInviteEmails: []string` — runs orchestrator for each email after per-sandbox channel is created. **Fail-soft** with `Interactive=false`.

**Connect Fallback UX (Q3: B — prompt before fallback)**
- Interactive (TTY): prompt before Connect, default `N`.
- Non-interactive (`km create`, scheduled, piped): no prompt; return `SkippedExternal` and emit stderr warning telling operator to follow up with `km slack invite --external <email>`.

**Manifest Handling (Q4: A — `km slack manifest` generates)**
- New `km slack manifest` reads bridge URL from SSM (`{ssm_prefix}slack/bridge-url`) and resource_prefix from config; renders embedded JSON template to stdout.
- Template lives in code (single source of truth for scopes); based on `/Users/khundeck/Downloads/km-personal.json`.
- New scope: `users:read.email` added to oauth_config.scopes.bot.
- Final scope list: `["chat:write", "channels:manage", "channels:join", "channels:read", "channels:history", "groups:write", "groups:history", "conversations.connect:write", "reactions:read", "reactions:write", "files:write", "users:read.email"]`.
- App name parameterized: `KlankerMaker-{resource_prefix}` (≤35 chars, alphanumeric+spaces+hyphens), via `--app-name` override.
- `request_url` = bridge Lambda Function URL + `/events`.
- `bot_events`: `["message.channels", "message.groups"]`.
- All flags (`socket_mode_enabled`, `org_deploy_enabled`, `token_rotation_enabled`, `is_mcp_enabled`, `pkce_enabled`) stay `false`.
- Output: stdout. No file written by default.

**Architecture (Approach 1 — unified invite primitive)**
- New low-level methods in `pkg/slack/client.go`:
  - `LookupUserByEmail(ctx, email) (userID string, found bool, err error)` — wraps `users.lookupByEmail`. Returns `(empty, false, nil)` on `users_not_found`. Errors otherwise.
  - `InviteUserToChannel(ctx, channelID, userID string) error` — wraps `conversations.invite`. Idempotent: treats `already_in_channel` as success.
  - Keep existing `InviteShared(ctx, channelID, email)` unchanged.
- New orchestrator file `pkg/slack/invite.go`:
  - `EnsureMemberByEmail(ctx, channelID, email string, opts EnsureMemberOpts) (EnsureMemberResult, error)`
  - `EnsureMemberOpts { ForceExternal bool; Interactive bool; Prompter Prompter }`
  - `EnsureMemberResult` typed enum: `InvitedDirect`, `InvitedConnect`, `AlreadyMember`, `SkippedExternal`, `Failed`.
- Three callers reuse the orchestrator:
  - `internal/app/cmd/slack.go` (existing init) — replace direct `InviteShared` call.
  - `internal/app/cmd/slack_invite.go` (NEW) — cobra wiring.
  - `internal/app/cmd/create_slack.go` (existing) — extend after channel creation.
- Profile schema additions:
  - `pkg/profile/types.go` — add `NotifySlackInviteEmails []string` to `CLISpec`.
  - `pkg/profile/schemas/sandbox_profile.schema.json` — mirror with `type: array, items: {type: string, format: email}`.
  - `pkg/profile/validate.go` — validate emails; reject when `notifySlackEnabled` is false.
- Manifest command:
  - `internal/app/cmd/slack_manifest.go` (NEW).
  - `internal/app/cmd/slack_manifest_template.json` (NEW, embedded via `//go:embed`).

**What does NOT change**
- Bridge Lambda (`cmd/km-slack-bridge/`, `pkg/slack/bridge/`) — no signing changes, no new actions.
- `cmd/km-slack/` sidecar binary.
- DynamoDB tables, SSM key paths, IAM, Terragrunt modules.
- Existing channels (`#km-notifications`, `#sb-{id}`).
- Phase 67/67.1/68 inbound, ACK reaction, transcript streaming — all unchanged.

### Claude's Discretion

- Exact UX wording of Connect-fallback prompt (must mention Pro requirement, default-N).
- Whether to short-circuit to `AlreadyMember` proactively via `conversations.members` (probably NO — `conversations.invite` already returns `already_in_channel`; one fewer round-trip).
- Embedded template file layout — single JSON file with Go `text/template` placeholders (`{{.AppName}}`, `{{.BridgeURL}}`).
- Test seam strategy: define `Prompter` interface in `pkg/slack/invite.go` for unit-testable fallback prompts; wire stdin/stdout impl from cmd layer.
- Whether `km slack init` warns when `users:read.email` is missing from bot scopes (probably YES — same `VerifyEventsAPIScopes` pattern as Phase 67).

### Deferred Ideas (OUT OF SCOPE)

- `km slack invite --bulk @file` (read emails from file).
- Profile field at platform-config level (km-config.yaml) for global default invitees.
- `reaction_added` event subscription (deferred to "reactions-as-actions" later).
- Manifest diff/upgrade tool comparing rendered template against installed app.
- Slack Connect retraction / un-invite (Slack API doesn't expose this).
- Multi-workspace install fan-out (Org Grid feature).
</user_constraints>

<phase_requirements>
## Phase Requirements

Phase 72 has no formal REQ-IDs in REQUIREMENTS.md; CONTEXT.md decisions form the requirement set. Each row maps a CONTEXT.md decision to research findings that enable implementation.

| Decision | Research Support |
|----------|------------------|
| **D1: `LookupUserByEmail` wraps `users.lookupByEmail`** | Slack API verified: GET, requires `users:read.email` bot scope, Tier 3 rate limit (50+/min), returns `users_not_found` error code on miss; HIGH confidence (official docs). |
| **D2: `InviteUserToChannel` wraps `conversations.invite` idempotently** | Slack API verified: POST, takes `channel` + `users` (comma-list, up to 100). Error codes: `already_in_channel`, `cant_invite_self`, `user_is_restricted`, `user_not_found`, `channel_not_found`, `not_in_channel`, `missing_scope`. Tier 3. Bot scopes: `channels:manage` (already in spec), `groups:write` (already in spec). HIGH confidence. |
| **D3: Keep `InviteShared` unchanged** | Existing in `pkg/slack/client.go:343` (`conversations.inviteShared`). Phase 63 retains free-tier failure pattern (`isSlackProWorkspaceError` matches `not_allowed_token_type` / `org_login_required`). |
| **D4: `EnsureMemberByEmail` orchestrator with 5 result states** | Existing patterns: `SlackAPIError` typed errors (`pkg/slack/client.go:69`); fail-soft pattern in `create_slack.go:201-210` (warn-and-continue); `Prompter` interface pattern in `internal/app/cmd/slack.go:67` (`SlackPrompter`). |
| **D5: New `km slack invite` cobra subcommand** | Pattern verified: `newSlackInitCmd` / `newSlackTestCmd` / `newSlackStatusCmd` / `newSlackRotateTokenCmd` (`internal/app/cmd/slack.go:120-132`). Each registered via `slackCmd.AddCommand(...)`. `SlackCmdDeps` struct is the test seam. |
| **D6: `km slack manifest` cobra subcommand** | Same pattern as D5; embed via `//go:embed` (existing usages: `pkg/profile/schema.go:13`, `internal/app/cmd/help.go:5`, `pkg/profile/builtins.go:11`). |
| **D7: Profile field `notifySlackInviteEmails []string`** | Existing CLISpec slice fields: `ClaudeArgs []string`, `CodexArgs []string` (`pkg/profile/types.go:366,371`). Validation pattern with `notifySlackEnabled` gating: see Rule SI1/ST1 in `pkg/profile/validate.go:330-336`. |
| **D8: Schema mirror for new field** | Pattern: existing fields at `pkg/profile/schemas/sandbox_profile.schema.json:517-543`. Array-of-strings with `format: email`: standard JSON Schema draft 7+ syntax. |
| **D9: Three callers refactor (init, invite, create)** | Existing call sites: `RunSlackInit` calls `api.InviteShared(ctx, chID, inv)` at `internal/app/cmd/slack.go:289` (replace with orchestrator). `create_slack.go:201` calls `api.InviteShared(ctx, chID, inviteEmail)` (extend with profile-driven email loop). |
| **D10: `users:read.email` scope warning at `km slack init`** | Existing: `VerifyEventsAPIScopes(scopes []string)` at `internal/app/cmd/slack.go:840` — pattern accepts a `required []string` slice. Production wiring: `fetchSlackBotScopes(ctx, botToken)` in `internal/app/cmd/doctor_slack.go:493` reads `X-OAuth-Scopes` header from `auth.test`. |
| **D11: Manifest reads bridge URL from SSM** | Verified: `internal/app/cmd/slack.go:329` reads `d.SsmPrefix+"slack/bridge-url"` via `d.SSM.Get(ctx, ..., false)`. Same `SlackSSMStore` interface. |
| **D12: Resource prefix from config** | Verified: `cfg.GetResourcePrefix()` at `internal/app/config/config.go:363` returns configured value or fallback "km". `cfg.GetSsmPrefix()` returns `/{prefix}/`. |
</phase_requirements>

## Summary

Phase 72 adds three new capabilities to the existing klankermaker Slack integration:

1. **A unified invite primitive** (`pkg/slack/invite.go::EnsureMemberByEmail`) that auto-detects whether a target email belongs to a native workspace member or external user, and dispatches `conversations.invite` or `conversations.inviteShared` accordingly. Three call sites (init, new ad-hoc command, profile-driven `km create`) share this primitive.

2. **A new `km slack invite <email>` ad-hoc command** for inviting people to channels post-install, plus a new profile field `spec.cli.notifySlackInviteEmails` that auto-invites a list of operators to per-sandbox channels at `km create` time.

3. **A new `km slack manifest` command** that generates a deployment-specific Slack App manifest (JSON) for paste into Slack admin "From manifest" UI. The manifest pins the full scope set including the new `users:read.email` scope required for D1.

**Primary recommendation:** Mirror existing `pkg/slack` patterns (httptest-driven unit tests, `*SlackAPIError` typed errors, idempotent `JoinChannel`-style methods) and existing `internal/app/cmd/slack*.go` cobra/deps patterns (`SlackCmdDeps`, injectable `Prompter`, deps-with-defaults). The Phase 67 / Phase 68 surface is the gold standard for testability and operator UX in this domain — copy it verbatim.

The work is greenfield in terms of new Slack API methods (no `users.lookupByEmail` or `conversations.invite` calls exist anywhere in the codebase) but follows established patterns. Risk surface is small: the bridge Lambda is untouched, nonce/signing model is untouched, and the orchestrator is purely operator-side (runs on the workstation, not in sandboxes or Lambdas).

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `pkg/slack` (in-repo) | n/a | Slack Web API client (`*Client`) and orchestrator (new `invite.go`) | Existing pattern; tests use `httptest.NewServer` + `c.SetBaseURL` to override Slack base URL |
| `github.com/spf13/cobra` | v1.8+ (as used) | CLI subcommand wiring | Already used by every `km` subcommand |
| `text/template` (stdlib) | Go 1.21+ | Manifest JSON template rendering | No third-party templating; manifest is small, placeholders are simple |
| `embed` (stdlib) | Go 1.16+ | Embed manifest template at compile time | Existing in-repo usage: `pkg/profile/schema.go`, `pkg/profile/builtins.go`, `internal/app/cmd/help.go`, `cmd/configui/main.go` |
| `encoding/json` (stdlib) | Go 1.21+ | Decode Slack responses; not needed for manifest output (template renders directly to stdout) | Existing pattern in `pkg/slack/client.go` |
| `net/http` (stdlib) | Go 1.21+ | Slack Web API HTTP calls (already in use via `*Client`) | n/a |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `aws-sdk-go-v2/service/ssm` | (as in repo) | Read bridge URL + bot token from SSM | Already used by `slack.go::buildSlackCmdDeps` |
| `goccy/go-yaml` | (as in repo) | Profile YAML parse | Only relevant if validation tests need YAML fixtures |
| `httptest` (stdlib) | Go 1.21+ | Mock Slack API in unit tests | Established pattern: `pkg/slack/client_test.go:27` (`newClientAgainstServer`) |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `text/template` | Pure `strings.Replace` | `text/template` provides escape-safety and clean placeholder syntax; at this size either works, but template is more conventional for JSON generation. Use `text/template` with `delims` if `{{` collides with JSON. |
| `text/template` | `encoding/json.Marshal` of a Go struct | Marshal would produce a deterministic but harder-to-eyeball output; the production manifest at `/Users/khundeck/Downloads/km-personal.json` has a specific field ordering. Template preserves ordering trivially. |
| Single batch `users.lookupByEmail` | Per-email loop | Slack has no batch endpoint for `users.lookupByEmail` — you call once per email. Tier 3 (50+/min) is more than enough for `notifySlackInviteEmails: [a, b, c]`-sized lists. |
| Pre-check via `conversations.members` | Trust `conversations.invite`'s `already_in_channel` | Pre-check costs an extra paginated call; `already_in_channel` is the documented idempotency signal. Use `already_in_channel` (matches CONTEXT discretion call). |

**Installation:** No new Go module dependencies. All work is in-repo + stdlib + existing AWS SDK.

## Architecture Patterns

### Recommended Project Structure
```
pkg/slack/
├── client.go           # ADD LookupUserByEmail, InviteUserToChannel
├── client_test.go      # ADD httptest tests for new methods
├── invite.go           # NEW: EnsureMemberByEmail orchestrator + Prompter interface
└── invite_test.go      # NEW: orchestrator tests (mock client + mock prompter)

internal/app/cmd/
├── slack.go            # MODIFY: register new subcommands; refactor RunSlackInit to call orchestrator
├── slack_invite.go     # NEW: cobra command for `km slack invite`
├── slack_invite_test.go    # NEW
├── slack_manifest.go   # NEW: cobra command for `km slack manifest`
├── slack_manifest_test.go  # NEW
├── slack_manifest_template.json  # NEW: //go:embed-ed template
├── create_slack.go     # MODIFY: after channel creation, loop NotifySlackInviteEmails
└── create_slack_test.go    # MODIFY: tests for new email loop

pkg/profile/
├── types.go            # MODIFY: add NotifySlackInviteEmails to CLISpec
├── validate.go         # MODIFY: add validation rule (require notifySlackEnabled, format-check emails)
└── schemas/
    └── sandbox_profile.schema.json  # MODIFY: mirror new field
```

### Pattern 1: Slack API client method
Each new client method follows the existing `callJSON`-based pattern at `pkg/slack/client.go:79`. Method takes `(ctx, ...)`, returns typed result + `error`. On non-OK Slack responses, `callJSON` returns `*SlackAPIError{Method, Code}` so callers can `errors.As` to inspect the upstream code.

```go
// Source: existing pattern in pkg/slack/client.go (CreateChannel, InviteShared)

// LookupUserByEmail wraps users.lookupByEmail. Returns (empty, false, nil) on
// users_not_found so callers can branch on a typed boolean rather than match
// error codes. Other Slack errors are returned untouched as *SlackAPIError.
//
// Requires the bot's `users:read.email` scope. Slack returns "missing_scope"
// via SlackAPIError if not granted; callers should surface that as an
// actionable scope-warning at km slack init time (see VerifyEventsAPIScopes
// pattern in internal/app/cmd/slack.go).
//
// Rate limit: Tier 3 (50+/min). Sufficient for notifySlackInviteEmails-size lists.
func (c *Client) LookupUserByEmail(ctx context.Context, email string) (string, bool, error) {
    resp, err := c.callJSON(ctx, "users.lookupByEmail", map[string]any{"email": email})
    if err != nil {
        var apierr *SlackAPIError
        if errors.As(err, &apierr) && apierr.Code == "users_not_found" {
            return "", false, nil
        }
        return "", false, err
    }
    // Note: SlackAPIResponse needs a User field added (or a new dedicated struct).
    // The existing SlackAPIResponse has Channel{} — add User{ ID string `json:"id"` }
    // following the same pattern, or define a method-specific decode struct.
    return resp.User.ID, true, nil
}
```

**Note on `SlackAPIResponse`:** The existing struct at `pkg/slack/client.go:41-57` has fields tailored for current methods. Add a `User struct{ ID string `json:"id"` }` field (mirroring the `Channel` shape at line 45-49) — additive change, no risk to existing decoders.

### Pattern 2: Idempotent wrapper (treat already_in_channel as success)
Mirror `JoinChannel` at `pkg/slack/client.go:291` which is "idempotent — Slack returns ok=true when the bot is already a member, so callers can call this unconditionally."

```go
// Source: pattern from JoinChannel (pkg/slack/client.go:291)

// InviteUserToChannel wraps conversations.invite. Idempotent: treats
// already_in_channel as success (matching JoinChannel's contract).
//
// Single-user invocation only. The Slack API supports a comma-list of up to
// 100 user IDs in `users`, but Phase 72 wires this method as a one-at-a-time
// primitive — bulk invites are explicitly deferred (see CONTEXT.md).
//
// Bot scopes required (already in km Slack App): channels:manage,
// channels:write.invites (for public channels), groups:write (for private).
//
// Common error codes (returned via *SlackAPIError):
//   - already_in_channel: TREATED AS SUCCESS; method returns nil.
//   - user_is_restricted: guest user — caller may want a typed warning.
//   - cant_invite_self: bot trying to invite itself.
//   - not_in_channel: the BOT is not in the channel — caller must JoinChannel first.
//   - channel_not_found / user_not_found: invalid IDs.
//   - missing_scope: scope drift — surface remediation pointing at km slack manifest.
func (c *Client) InviteUserToChannel(ctx context.Context, channelID, userID string) error {
    _, err := c.callJSON(ctx, "conversations.invite", map[string]any{
        "channel": channelID,
        "users":   userID, // Slack accepts a single ID or comma-list; single is fine.
    })
    if err != nil {
        var apierr *SlackAPIError
        if errors.As(err, &apierr) && apierr.Code == "already_in_channel" {
            return nil // idempotent
        }
        return err
    }
    return nil
}
```

### Pattern 3: Orchestrator + Prompter interface (test seam)
Mirror the `SlackPrompter` interface at `internal/app/cmd/slack.go:67` and the existing pattern of struct-based deps that callers swap for fakes in tests.

```go
// File: pkg/slack/invite.go (NEW)

package slack

import (
    "context"
    "errors"
    "fmt"
)

// Prompter collects yes/no input for the Connect-fallback flow. Production
// implementations live in internal/app/cmd; tests provide a recording fake.
type Prompter interface {
    // ConfirmConnect returns true if the operator confirms sending a Slack
    // Connect invite to email. The implementation is responsible for showing
    // the email and Pro-tier requirement in the prompt text.
    ConfirmConnect(email string) (bool, error)
}

// InviteAPI is the narrow Slack client surface needed by EnsureMemberByEmail.
// *Client satisfies it. Tests inject a fake.
type InviteAPI interface {
    LookupUserByEmail(ctx context.Context, email string) (userID string, found bool, err error)
    InviteUserToChannel(ctx context.Context, channelID, userID string) error
    InviteShared(ctx context.Context, channelID, email string) error
}

// EnsureMemberOpts controls EnsureMemberByEmail behavior.
type EnsureMemberOpts struct {
    // ForceExternal: skip lookup, go straight to InviteShared (matches
    // `km slack invite --external` semantics). Operator confirmed via flag.
    ForceExternal bool
    // Interactive: when true and lookup misses, call Prompter.ConfirmConnect
    // before falling back to Connect. When false (km create, scheduled, piped),
    // returns SkippedExternal with no prompt.
    Interactive bool
    // Prompter is required when Interactive=true; ignored otherwise.
    Prompter Prompter
}

// EnsureMemberResult is a typed enum.
type EnsureMemberResult int

const (
    InvitedDirect EnsureMemberResult = iota + 1
    InvitedConnect
    AlreadyMember
    SkippedExternal
    Failed
)

func (r EnsureMemberResult) String() string {
    switch r {
    case InvitedDirect:
        return "InvitedDirect"
    case InvitedConnect:
        return "InvitedConnect"
    case AlreadyMember:
        return "AlreadyMember"
    case SkippedExternal:
        return "SkippedExternal"
    case Failed:
        return "Failed"
    default:
        return "Unknown"
    }
}

// EnsureMemberByEmail is the unified invite primitive. Three callers reuse it:
//   - km slack init (single email, interactive)
//   - km slack invite (single email, interactive or --external)
//   - km create (loop over notifySlackInviteEmails, non-interactive)
//
// On success returns one of (InvitedDirect, InvitedConnect, AlreadyMember,
// SkippedExternal) with err == nil. On failure returns Failed and the
// underlying error. Callers decide warn vs fail behavior based on result.
//
// Detection: lookupByEmail → invite. On users_not_found, fallback to
// (interactive prompt → InviteShared) OR SkippedExternal (non-interactive).
//
// ForceExternal=true skips lookup and invokes InviteShared directly.
func EnsureMemberByEmail(ctx context.Context, api InviteAPI, channelID, email string, opts EnsureMemberOpts) (EnsureMemberResult, error) {
    if opts.ForceExternal {
        if err := api.InviteShared(ctx, channelID, email); err != nil {
            return Failed, err
        }
        return InvitedConnect, nil
    }

    userID, found, err := api.LookupUserByEmail(ctx, email)
    if err != nil {
        return Failed, fmt.Errorf("lookup %s: %w", email, err)
    }
    if found {
        if err := api.InviteUserToChannel(ctx, channelID, userID); err != nil {
            // already_in_channel is already absorbed by InviteUserToChannel.
            // Other errors (cant_invite_self, user_is_restricted, missing_scope)
            // bubble up here as Failed so caller can warn.
            return Failed, fmt.Errorf("invite %s (%s) to %s: %w", email, userID, channelID, err)
        }
        return InvitedDirect, nil
    }

    // Lookup miss → external path.
    if !opts.Interactive {
        return SkippedExternal, nil
    }
    if opts.Prompter == nil {
        return Failed, errors.New("interactive mode requires a Prompter")
    }
    confirmed, err := opts.Prompter.ConfirmConnect(email)
    if err != nil {
        return Failed, fmt.Errorf("prompt for %s: %w", email, err)
    }
    if !confirmed {
        return SkippedExternal, nil
    }
    if err := api.InviteShared(ctx, channelID, email); err != nil {
        return Failed, err
    }
    return InvitedConnect, nil
}
```

### Pattern 4: Cobra subcommand wiring + DI
Mirror exactly `newSlackInitCmd` at `internal/app/cmd/slack.go:138` and `newSlackTestCmd` at `:433`. Pattern:
1. Local var declarations for flag values.
2. `&cobra.Command{Use, Short, SilenceUsage, RunE}`.
3. RunE: get `ctx` from `cmd.Context()` (fallback `context.Background()`); resolve `deps := sharedDeps`; call exported `RunSlackXxx(ctx, deps, opts)`.
4. `c.Flags().StringVar(&v, "name", "", "help")` for each flag.

```go
// File: internal/app/cmd/slack_invite.go (NEW)

func newSlackInviteCmd(cfg *config.Config, sharedDeps *SlackCmdDeps) *cobra.Command {
    var (
        channelArg string
        external   bool
    )
    c := &cobra.Command{
        Use:          "invite <email>",
        Short:        "Invite an email address to a Slack channel (auto-detects native vs Connect)",
        Args:         cobra.ExactArgs(1),
        SilenceUsage: true,
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := cmd.Context()
            if ctx == nil {
                ctx = context.Background()
            }
            deps := sharedDeps
            if deps == nil {
                var err error
                deps, err = buildSlackCmdDeps(cfg)
                if err != nil {
                    return err
                }
            }
            return RunSlackInvite(ctx, deps, SlackInviteOpts{
                Email:    args[0],
                Channel:  channelArg,
                External: external,
            })
        },
    }
    c.Flags().StringVar(&channelArg, "channel", "", "Channel name (e.g. km-notifications) or ID (e.g. C012ABCDE3F); default: SSM-stored shared channel")
    c.Flags().BoolVar(&external, "external", false, "Skip lookup; send Slack Connect invite directly (no prompt)")
    return c
}
```

Then register in `newSlackCmdInternal` at `slack.go:120`:
```go
slackCmd.AddCommand(newSlackInviteCmd(cfg, deps))
slackCmd.AddCommand(newSlackManifestCmd(cfg, deps))
```

### Pattern 5: Manifest template + embed
Use `//go:embed` for a single JSON file with `text/template` placeholders. Match existing in-repo embed usage at `internal/app/cmd/help.go:5` (`//go:embed help/*.txt`).

```go
// File: internal/app/cmd/slack_manifest.go (NEW)

package cmd

import (
    _ "embed"
    "fmt"
    "io"
    "text/template"
)

//go:embed slack_manifest_template.json
var slackManifestTemplate string

type slackManifestData struct {
    AppName   string
    BridgeURL string // already includes /events suffix when callers append it
    EventsURL string // bridgeURL + "/events"
}

// RenderSlackManifest renders the embedded template to w with the given data.
// Exported for testability and golden-file diff.
func RenderSlackManifest(w io.Writer, data slackManifestData) error {
    t, err := template.New("slack-manifest").Parse(slackManifestTemplate)
    if err != nil {
        return fmt.Errorf("parse manifest template: %w", err)
    }
    return t.Execute(w, data)
}
```

Template placeholder syntax (`{{.AppName}}`) does not collide with JSON; `text/template` evaluates and emits literal JSON text.

### Pattern 6: Profile field + validation rule (mirror Phase 67/68)
The CLISpec already has `NotifyEmailEnabled *bool`, `NotifySlackEnabled *bool`, `NotifySlackInboundEnabled bool`, `NotifySlackTranscriptEnabled bool`. Add a slice field next to them.

```go
// File: pkg/profile/types.go (MODIFY CLISpec)

// NotifySlackInviteEmails is a list of email addresses to auto-invite to the
// per-sandbox Slack channel after km create succeeds. Each address is run
// through the EnsureMemberByEmail orchestrator with Interactive=false:
// native workspace members get a regular conversations.invite; non-members
// emit a stderr warning instructing the operator to follow up with
// `km slack invite --external <email>`.
//
// Requires notifySlackEnabled=true. No-op when notifySlackEnabled is unset
// or false. Profile-only (no CLI flag override) for v1.
//
// Default: empty.
NotifySlackInviteEmails []string `yaml:"notifySlackInviteEmails,omitempty" json:"notifySlackInviteEmails,omitempty"`
```

Validation rule (insert in `pkg/profile/validate.go` near line 320, mirroring SI1/ST1):

```go
// Rule SE1 (error): invite-emails requires outbound Slack enabled.
if len(cli.NotifySlackInviteEmails) > 0 && !slackOn {
    errs = append(errs, ValidationError{
        Path:    "spec.cli.notifySlackInviteEmails",
        Message: "notifySlackInviteEmails requires notifySlackEnabled: true",
    })
}
// Rule SE2 (error): each entry must be a syntactically valid email.
for i, e := range cli.NotifySlackInviteEmails {
    if !isValidEmail(e) {
        errs = append(errs, ValidationError{
            Path:    fmt.Sprintf("spec.cli.notifySlackInviteEmails[%d]", i),
            Message: fmt.Sprintf("invalid email %q", e),
        })
    }
}
```

`isValidEmail` likely already exists; if not, use a simple regex (`^[^@\s]+@[^@\s]+\.[^@\s]+$`). Reusing whatever the rest of the codebase does keeps consistency — search `pkg/profile/validate.go` for an existing email validator before adding a new one.

JSON schema mirror:
```json
"notifySlackInviteEmails": {
  "type": "array",
  "items": { "type": "string", "format": "email" },
  "default": [],
  "description": "Emails to auto-invite to per-sandbox channel after km create. Requires notifySlackEnabled. Native workspace members get conversations.invite; non-members emit a stderr warning. Out-of-workspace emails follow up with km slack invite --external."
}
```

### Anti-Patterns to Avoid

- **Anti-pattern: Pre-checking membership via `conversations.members`.** Costs an extra paginated call. Slack's `conversations.invite` already returns `already_in_channel` — use that as the idempotency signal. Matches the existing `JoinChannel` pattern.

- **Anti-pattern: Catching every Slack error code in the orchestrator.** The orchestrator should branch on `users_not_found` (the routing decision) only. All other errors (`cant_invite_self`, `user_is_restricted`, `missing_scope`, `not_in_channel`) bubble up as `Failed` so callers can present audience-specific guidance. Don't swallow `not_in_channel` — that's an operational signal that the bot was kicked.

- **Anti-pattern: Aborting `km create` on a single bad invite.** CONTEXT.md explicitly mandates fail-soft: warn-and-continue, sandbox provisioning is the primary operation. Mirror the existing `inviteShared` non-fatal pattern at `create_slack.go:201-210` (logs warning, continues).

- **Anti-pattern: Hardcoding the bridge URL or scope list in the manifest template.** Both are moving targets across deployments — bridge URL changes per `resource_prefix`, scope list grows over phases. Both must be parameterized via the template.

- **Anti-pattern: Writing the manifest to a file by default.** stdout output enables shell piping (`km slack manifest > app.json`), is friendlier for CI, and matches existing `km slack status` ergonomics. The operator opts in to a file via shell redirection.

- **Anti-pattern: Calling Slack from inside `km create`'s critical path before terragrunt apply.** The existing channel-creation already happens before terragrunt at `create.go:480-510` and is the right gate. The new invite loop runs **after** the channel creation succeeds (still pre-terragrunt or post-channel-creation depending on placement; CONTEXT calls it "after the per-sandbox channel is created"). It must remain non-fatal because `notifySlackInviteEmails` failures are warnings, not errors.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Slack API error parsing | Custom string-prefix matching | `errors.As(&SlackAPIError{})` then inspect `.Code` | Pattern already exists; covers all Slack error formats consistently |
| HTTP client + auth | Custom `http.Client` with retry | Existing `*Client` constructed via `NewClient(token, nil)` | Sets timeout, baseURL, Authorization header. Tests use `SetBaseURL` for httptest |
| Email validation | Custom regex per-feature | Whatever helper `pkg/profile/validate.go` already uses (search before adding) | Consistency with existing rules |
| YAML/JSON schema sync | Update one and forget the other | Edit both in same plan; tests at `pkg/profile/validate_test.go` validate fixtures against schema | Schema drift causes ConfigUI / external tooling breakage |
| Cobra command/test wiring | New cobra patterns | Copy structure of `newSlackInitCmd` + `RunSlackInit` exactly (DI via `*SlackCmdDeps`) | The whole slack.go file is the canonical example |
| JSON manifest construction | Hand-build with `fmt.Sprintf` | `text/template` over an embedded fixture | Production manifest at `/Users/khundeck/Downloads/km-personal.json` is the source of truth; template diffs cleanly against it |
| Stdin prompt for yes/no | New `bufio.NewReader` per call | Reuse `SlackPrompter` interface; add `ConfirmConnect` method to it | Existing pattern; tests inject fake prompter |
| Bot-scope verification | New `auth.test` invocation | Existing `fetchSlackBotScopes` (`internal/app/cmd/doctor_slack.go:493`) returns `[]string` from `X-OAuth-Scopes` header | Reuse for `users:read.email` check at `km slack init` |

**Key insight:** Phase 72 should look like a thin extension of Phase 63/67/68. If the diff introduces patterns that don't already exist in `pkg/slack/` or `internal/app/cmd/slack*.go`, you've gone off-pattern.

## Common Pitfalls

### Pitfall 1: Missing `users:read.email` scope on existing installs
**What goes wrong:** `LookupUserByEmail` returns `*SlackAPIError{Code: "missing_scope"}` and the orchestrator treats this as `Failed`. Operators who installed the Slack App before Phase 72 don't have this scope.
**Why it happens:** Slack App scopes are install-time; adding a scope requires re-installing the app and rotating the bot token.
**How to avoid:** At `km slack init` (and `km slack invite`), check the bot's scopes via `fetchSlackBotScopes` and emit a structured warning when `users:read.email` is missing. Direct operator to:
1. Run `km slack manifest` to see the current scope list.
2. Update Slack App config → OAuth & Permissions → Bot Token Scopes.
3. Reinstall the app.
4. Run `km slack rotate-token --bot-token <new-token>`.
**Warning signs:** First invite after rolling out Phase 72 fails with a cryptic "lookup: slack users.lookupByEmail: missing_scope" message.

### Pitfall 2: `not_in_channel` on `conversations.invite`
**What goes wrong:** Slack returns `not_in_channel` because the BOT itself is not a member of the target channel. Common after Slack App reinstall (drops bot from previously-joined channels).
**Why it happens:** Slack App reinstall semantics — every reinstall is a fresh install for membership purposes.
**How to avoid:** Mirror the existing `JoinChannel`-before-`InviteShared` pattern at `slack.go:306-323` and `create_slack.go:176-189`. The orchestrator should accept that the bot is already in the channel (callers like `km create` already join after channel creation); for `km slack invite` operating on operator-named channels, prepend a `JoinChannel` call before `InviteUserToChannel` if `not_in_channel` is returned.
**Warning signs:** `km slack invite` works the first time after `km slack init` but fails after the operator rotates the Slack App.

### Pitfall 3: Treating `cant_invite_self` as success
**What goes wrong:** When the bot's own email (or operator email tied to the bot user) is in `notifySlackInviteEmails`, `conversations.invite` returns `cant_invite_self`. Naive "treat all errors as warning" code would silently skip the bot itself, masking a config bug.
**Why it happens:** `cant_invite_self` looks similar to `already_in_channel` — both are "user is in the channel" outcomes.
**How to avoid:** Only treat `already_in_channel` as success in `InviteUserToChannel`. Surface `cant_invite_self` as `Failed` so the caller can emit a clear warning ("can't invite the bot itself").
**Warning signs:** Operator configures notifySlackInviteEmails with the bot's own email and gets no warning.

### Pitfall 4: Manifest template URL drift across instances
**What goes wrong:** Operator copies a manifest from one install (`resource_prefix=km`) and pastes into another (`resource_prefix=corporate`); the bridge `request_url` points at the wrong Lambda Function URL.
**Why it happens:** Multi-instance support per CLAUDE.md — each `resource_prefix` has its own bridge Lambda with its own Function URL.
**How to avoid:** `km slack manifest` MUST read bridge URL from SSM (`{ssm_prefix}slack/bridge-url`) and resource_prefix from `cfg.GetResourcePrefix()`, never hardcode either. Print a banner above the manifest output indicating which install the manifest is for: `# Slack App manifest for resource_prefix={prefix}, region={region}`.
**Warning signs:** Slack `/events` 401s in the second install; bridge logs show no traffic.

### Pitfall 5: Free-tier workspace + Connect invite
**What goes wrong:** Operator runs `km slack invite alice@external.com` with `--external` (or `users.lookupByEmail` returns `users_not_found` → fallback prompt → operator says yes), and `conversations.inviteShared` returns `not_allowed_token_type`. Default error message is unhelpful ("not_allowed_token_type").
**Why it happens:** Slack Connect requires a Pro Slack workspace (per CLAUDE.md and existing Phase 63 pattern).
**How to avoid:** Reuse the existing `isSlackProWorkspaceError` helper at `internal/app/cmd/slack.go:417` so the error message explicitly mentions Pro tier. The existing `RunSlackInit` already does this at `:290-294`; the new `EnsureMemberByEmail` callers must do the same when surfacing `Failed` from a `InvitedConnect` attempt.
**Warning signs:** First operator on a free-tier workspace tries the new flow and gets a confusing error.

### Pitfall 6: `users.lookupByEmail` with case-sensitive emails
**What goes wrong:** Slack matches on the email Slack stores in the user's profile, which can differ in case from what the operator provides (`Alice@Example.com` vs `alice@example.com`).
**Why it happens:** Email matching in Slack's API is case-sensitive in some scenarios (per slackapi/node-slack-sdk#1523).
**How to avoid:** Lowercase the email before lookup. If lowercase miss but exact-case might work, the cost of double-lookup is negligible. Document the behavior in the orchestrator GoDoc.
**Warning signs:** `users.lookupByEmail` returns `users_not_found` for an email the operator can see in the workspace user directory.

### Pitfall 7: Schema field added to types.go but not schema.json
**What goes wrong:** `km validate` accepts a profile with `notifySlackInviteEmails`, but ConfigUI (which validates against the JSON schema) rejects it.
**Why it happens:** Two sources of truth (Go types and JSON schema) drift.
**How to avoid:** Treat both files as one atomic change. Add a regression test that loads a fixture profile with `notifySlackInviteEmails` set and runs both `Parse` + `ValidateAgainstSchema` (if such a helper exists; pattern: `pkg/profile/schema.go` is the embed point).
**Warning signs:** ConfigUI tests fail after the change merges.

### Pitfall 8: `text/template` JSON delimiter collision
**What goes wrong:** Manifest template uses `{{.AppName}}`, but JSON syntax includes `{` and `}` heavily. Template parser can mis-tokenize or render unexpected escapes.
**Why it happens:** `text/template` default delimiters are `{{ }}`. They don't actually collide with JSON's `{` `}` because templates require double-brace, but operators editing the template might write `{name}` thinking it's a placeholder.
**How to avoid:** Stick with `{{ }}` (double-brace) — JSON uses single `{` `}` so no collision. If you need to render literal `{{` for some reason, use `{{"{{"}}`. Add a golden-file test that runs the template and compares against a fixture with placeholders bound to known values.
**Warning signs:** Manifest renders with broken JSON; Slack admin UI rejects with "Manifest is not valid JSON."

## Code Examples

Verified patterns from official Slack docs and existing in-repo code.

### `users.lookupByEmail` request/response (HIGH confidence — official Slack docs)
```bash
# Request — actually a POST in Go via callJSON, but Slack docs show GET form
POST https://slack.com/api/users.lookupByEmail
Authorization: Bearer xoxb-...
Content-Type: application/json; charset=utf-8

{ "email": "alice@example.com" }

# Success response
HTTP 200
{
  "ok": true,
  "user": {
    "id": "W012A3CDE",
    "team_id": "T012AB3C4",
    "name": "alice",
    "deleted": false,
    "real_name": "Alice Anderson",
    "profile": { "email": "alice@example.com", ... },
    "is_admin": false,
    "is_bot": false
  }
}

# Miss response
HTTP 200
{ "ok": false, "error": "users_not_found" }

# Scope drift
HTTP 200
{ "ok": false, "error": "missing_scope" }
```
Source: https://docs.slack.dev/reference/methods/users.lookupByEmail/

### `conversations.invite` request/response (HIGH confidence)
```bash
POST https://slack.com/api/conversations.invite
Authorization: Bearer xoxb-...
Content-Type: application/json; charset=utf-8

{ "channel": "C012AB3CD", "users": "W012A3CDE" }

# Success
HTTP 200
{ "ok": true, "channel": { "id": "C012AB3CD", ... } }

# Idempotent — already a member
HTTP 200
{ "ok": false, "error": "already_in_channel" }

# Bot needs to be in the channel first
HTTP 200
{ "ok": false, "error": "not_in_channel" }
```
Source: https://docs.slack.dev/reference/methods/conversations.invite/

### Existing httptest unit-test pattern (verbatim from `pkg/slack/client_test.go:51-83`)
```go
func TestClient_AuthTest_NotOK(t *testing.T) {
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.Write(slackErr("invalid_auth"))
    }))
    defer ts.Close()

    c := newClientAgainstServer(ts)
    err := c.AuthTest(context.Background())
    if err == nil { t.Fatal("expected error, got nil") }
    apiErr, ok := err.(*slack.SlackAPIError)
    if !ok { t.Fatalf("expected *SlackAPIError, got %T: %v", err, err) }
    if apiErr.Code != "invalid_auth" {
        t.Errorf("Code = %q; want %q", apiErr.Code, "invalid_auth")
    }
}
```

### Existing fail-soft pattern in create_slack.go (verbatim from line 201-210)
```go
if inviteErr := api.InviteShared(ctx, chID, inviteEmail); inviteErr != nil {
    // Invite failure is non-fatal: the channel is live, the bot is in
    // it, sandbox notifications will still flow. The cross-workspace
    // invite is a convenience for the operator's external Slack;
    // failing here used to abort sandbox provisioning, which was
    // disproportionate (the failure typically means the operator
    // already accepted the invite, the workspace isn't on Pro tier,
    // or the email already has a connection).
    log.Warn().Err(inviteErr).Str("channel", chID).Str("email", inviteEmail).Msg("Slack Connect invite failed (non-fatal — channel and bot are healthy; manually re-invite if needed)")
}
```
This is the idiom to mirror for the `notifySlackInviteEmails` loop in `km create`.

### Embedded template skeleton (sketch — adapt from production manifest)
```json
{
  "display_information": {
    "name": "{{.AppName}}",
    "description": "Get notifications from KlankerMaker sandboxes",
    "background_color": "#000000"
  },
  "features": {
    "bot_user": {
      "display_name": "{{.AppName}}",
      "always_online": false
    }
  },
  "oauth_config": {
    "scopes": {
      "bot": [
        "chat:write",
        "channels:manage",
        "channels:join",
        "channels:read",
        "channels:history",
        "groups:write",
        "groups:history",
        "conversations.connect:write",
        "reactions:read",
        "reactions:write",
        "files:write",
        "users:read.email"
      ]
    },
    "pkce_enabled": false
  },
  "settings": {
    "event_subscriptions": {
      "request_url": "{{.EventsURL}}",
      "bot_events": ["message.channels", "message.groups"]
    },
    "org_deploy_enabled": false,
    "socket_mode_enabled": false,
    "token_rotation_enabled": false,
    "is_mcp_enabled": false
  }
}
```
Source: derived from `/Users/khundeck/Downloads/km-personal.json` per CONTEXT.md.

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `files.upload` | `files.getUploadURLExternal` + `files.completeUploadExternal` (3-step) | 2024 (Slack deprecation) | Already in `pkg/slack/client.go::UploadFile` — Phase 68 work; not relevant to Phase 72 |
| Single-tier Slack workspace assumption (external operators only) | Native + Slack Connect support via auto-detection | Phase 72 itself | The whole point of this phase |
| Manifest as tribal knowledge / one-off file | Generated by `km slack manifest` | Phase 72 | Scope drift becomes a code change; operators in new workspaces have a clear install path |

**Deprecated / outdated:**
- `users.list` for finding individual users by email — `users.lookupByEmail` is the current best practice. Don't fall back to enumerating users.

## Open Questions

1. **Should `EnsureMemberByEmail` self-join the channel on `not_in_channel`?**
   - What we know: existing call sites (init, create) already join the channel after creation/discovery. The new `km slack invite` command targets arbitrary channels (potentially ones the bot has never joined).
   - What's unclear: whether `km slack invite --channel <name-of-channel-bot-isnt-in>` should auto-join (silently) or fail with actionable guidance.
   - Recommendation: have `km slack invite` (the cobra command) call `JoinChannel` defensively before `EnsureMemberByEmail`. Keep the orchestrator pure — it operates on a channel where the bot is already a member. This pushes the join concern up to the caller, matching how existing call sites handle it.

2. **Does `format: email` in JSON schema actually validate?**
   - What we know: JSON Schema draft 7 supports `format: email` but most validators treat it as advisory.
   - What's unclear: whether the in-repo schema validator (whatever `pkg/profile` uses for schema enforcement) honors `format: email`. The Go-side `isValidEmail` regex check in `validate.go` is the authoritative gate.
   - Recommendation: include `format: email` in the JSON schema for ConfigUI hints, but rely on the Go-side validator for correctness. Add a unit test that asserts a malformed email is rejected by `Validate()`.

3. **Connect-fallback prompt wording — explicit confirmation per email or batch?**
   - What we know: CONTEXT.md says interactive prompt, default-N. CONTEXT.md doesn't specify behavior when MULTIPLE emails miss in a row (km create is non-interactive, but `km slack invite` always operates on one email).
   - What's unclear: km create runs non-interactive, so this doesn't apply there. `km slack invite` takes one email at a time. This is a non-issue.
   - Recommendation: per-email prompt is the only mode that exists. Move on.

4. **Is `users:read.email` part of the existing klankermaker.ai workspace install?**
   - What we know: production manifest at `/Users/khundeck/Downloads/km-personal.json` does NOT include `users:read.email`. CONTEXT.md says it's the new scope.
   - What's unclear: rollout sequence — does Phase 72 require a Slack App reinstall on the existing klankermaker workspace before merge, or only at the new corporate workspace?
   - Recommendation: **the existing klankermaker workspace also needs the scope added and the app reinstalled** for `km slack invite` to work there. Plan should include a one-time UAT step: "Operator updates klankermaker Slack App scopes via manifest, reinstalls, runs `km slack rotate-token`. Validate `km slack invite` works against the existing PoC install."

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (test files: `*_test.go` colocated with source) |
| Config file | `go.mod` (no separate test config) |
| Quick run command | `go test ./pkg/slack/... ./pkg/profile/... ./internal/app/cmd/...` |
| Full suite command | `go test ./...` |
| E2E gating | `RUN_SLACK_E2E=1` env var (existing pattern in `test/e2e/slack/`) |

### Phase Requirements → Test Map

| Decision | Behavior | Test Type | Automated Command | File Exists? |
|----------|----------|-----------|-------------------|-------------|
| D1: `LookupUserByEmail` returns `(id, true, nil)` on success | unit | unit | `go test ./pkg/slack -run TestClient_LookupUserByEmail_Found` | Wave 0 (file `pkg/slack/client_test.go` exists; add new test func) |
| D1: returns `(empty, false, nil)` on `users_not_found` | unit | unit | `go test ./pkg/slack -run TestClient_LookupUserByEmail_NotFound` | Wave 0 |
| D1: returns error on `missing_scope` | unit | unit | `go test ./pkg/slack -run TestClient_LookupUserByEmail_MissingScope` | Wave 0 |
| D2: `InviteUserToChannel` succeeds | unit | unit | `go test ./pkg/slack -run TestClient_InviteUserToChannel_OK` | Wave 0 |
| D2: idempotent on `already_in_channel` | unit | unit | `go test ./pkg/slack -run TestClient_InviteUserToChannel_AlreadyMember` | Wave 0 |
| D2: surfaces `cant_invite_self` as error | unit | unit | `go test ./pkg/slack -run TestClient_InviteUserToChannel_CantInviteSelf` | Wave 0 |
| D2: surfaces `not_in_channel` as error | unit | unit | `go test ./pkg/slack -run TestClient_InviteUserToChannel_NotInChannel` | Wave 0 |
| D4: `EnsureMemberByEmail` returns `InvitedDirect` on lookup hit | unit | unit | `go test ./pkg/slack -run TestEnsureMemberByEmail_Direct` | Wave 0 (NEW file `pkg/slack/invite_test.go`) |
| D4: returns `AlreadyMember` when invite returns already_in_channel | unit | unit | `go test ./pkg/slack -run TestEnsureMemberByEmail_AlreadyMember` | Wave 0 |
| D4: returns `InvitedConnect` on miss + interactive YES | unit | unit | `go test ./pkg/slack -run TestEnsureMemberByEmail_InvitedConnect` | Wave 0 |
| D4: returns `SkippedExternal` on miss + non-interactive | unit | unit | `go test ./pkg/slack -run TestEnsureMemberByEmail_SkippedNonInteractive` | Wave 0 |
| D4: returns `SkippedExternal` on miss + interactive NO | unit | unit | `go test ./pkg/slack -run TestEnsureMemberByEmail_SkippedInteractiveNo` | Wave 0 |
| D4: ForceExternal=true skips lookup | unit | unit | `go test ./pkg/slack -run TestEnsureMemberByEmail_ForceExternal` | Wave 0 |
| D4: returns `Failed` + Pro-tier error on free-tier Connect | unit | unit | `go test ./pkg/slack -run TestEnsureMemberByEmail_FreeTierConnect` | Wave 0 |
| D5: `km slack invite` cobra cmd wiring | unit (cmd-level) | unit | `go test ./internal/app/cmd -run TestSlackInvite_HappyPath` | Wave 0 (NEW file `internal/app/cmd/slack_invite_test.go`) |
| D5: `--external` flag bypasses lookup | unit | unit | `go test ./internal/app/cmd -run TestSlackInvite_ExternalFlag` | Wave 0 |
| D5: `--channel <name>` resolves via FindChannelByName | unit | unit | `go test ./internal/app/cmd -run TestSlackInvite_ChannelByName` | Wave 0 |
| D5: default channel = SSM shared-channel-id | unit | unit | `go test ./internal/app/cmd -run TestSlackInvite_DefaultChannelFromSSM` | Wave 0 |
| D6: `km slack manifest` renders golden output | unit | unit | `go test ./internal/app/cmd -run TestSlackManifest_Golden` | Wave 0 (NEW file `internal/app/cmd/slack_manifest_test.go` + fixture) |
| D6: `--app-name` overrides default name | unit | unit | `go test ./internal/app/cmd -run TestSlackManifest_AppNameOverride` | Wave 0 |
| D6: bridge URL read from SSM | unit | unit | `go test ./internal/app/cmd -run TestSlackManifest_BridgeURLFromSSM` | Wave 0 |
| D6: includes `users:read.email` in scopes | unit | unit | `go test ./internal/app/cmd -run TestSlackManifest_ScopesIncludeUsersReadEmail` | Wave 0 |
| D7: profile parses `notifySlackInviteEmails` | unit | unit | `go test ./pkg/profile -run TestParse_NotifySlackInviteEmails` | Wave 0 (NEW test in existing `pkg/profile/profile_test.go` or similar) |
| D7: validate rejects when `notifySlackEnabled=false` | unit | unit | `go test ./pkg/profile -run TestValidate_InviteEmails_RequiresSlackEnabled` | Wave 0 (NEW test in `pkg/profile/validate_test.go`) |
| D7: validate rejects malformed emails | unit | unit | `go test ./pkg/profile -run TestValidate_InviteEmails_InvalidEmail` | Wave 0 |
| D8: schema accepts valid array; rejects non-string items | unit | unit | `go test ./pkg/profile -run TestSchema_InviteEmails` | Wave 0 |
| D9: `RunSlackInit` calls orchestrator (not `InviteShared` directly) | unit | unit | `go test ./internal/app/cmd -run TestSlackInit_UsesOrchestrator` | Wave 0 (modify existing `internal/app/cmd/slack_test.go` test) |
| D9: `km create` loops `notifySlackInviteEmails` after channel creation | unit (integration-style) | unit | `go test ./internal/app/cmd -run TestCreateSlack_InvitesEmails` | Wave 0 (NEW test in existing `internal/app/cmd/create_slack_test.go`) |
| D9: km create warning on SkippedExternal email | unit | unit | `go test ./internal/app/cmd -run TestCreateSlack_WarnsOnSkippedExternal` | Wave 0 |
| D10: `km slack init` warns on missing `users:read.email` | unit | unit | `go test ./internal/app/cmd -run TestSlackInit_WarnsOnMissingUsersReadEmail` | Wave 0 |
| E2E: `km slack invite alice@native.com` (live workspace) | e2e | manual | `RUN_SLACK_E2E=1 KM_SLACK_E2E_BOT_TOKEN=... go test ./test/e2e/slack -run TestE2ESlackInvite_NativeMember` | Wave 0 (NEW test in `test/e2e/slack/slack_e2e_test.go`) |
| E2E: `km slack invite --external bob@external.com` | e2e | manual | `RUN_SLACK_E2E=1 ... go test ./test/e2e/slack -run TestE2ESlackInvite_ExternalConnect` | Wave 0 |
| E2E: `km slack manifest` produces installable JSON | manual UAT | manual | (operator pastes into Slack admin "From manifest" UI) | UAT only |
| Manual UAT: corporate workspace install via manifest | manual UAT | manual | (operator follows runbook in `docs/slack-notifications.md`) | UAT only |
| Manual UAT: scope drift detection at `km slack init` | manual UAT | manual | (operator removes `users:read.email`, reinstalls, runs `km slack init`, sees warning) | UAT only |

### Sampling Rate

- **Per task commit:** `go test ./pkg/slack/... ./pkg/profile/... ./internal/app/cmd/...` (covers all unit-level changes; ≤30s on this codebase)
- **Per wave merge:** `go test ./...` (full suite green; includes downstream packages that depend on `pkg/profile` and `pkg/slack`)
- **Phase gate:** Full suite green + at least one E2E run with `RUN_SLACK_E2E=1` against the klankermaker.ai workspace before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/slack/invite.go` — orchestrator implementation file (NEW)
- [ ] `pkg/slack/invite_test.go` — orchestrator unit tests with fake `InviteAPI` + fake `Prompter` (NEW)
- [ ] `internal/app/cmd/slack_invite.go` — cobra wiring (NEW)
- [ ] `internal/app/cmd/slack_invite_test.go` — cmd-level tests (NEW)
- [ ] `internal/app/cmd/slack_manifest.go` — cobra wiring + RenderSlackManifest (NEW)
- [ ] `internal/app/cmd/slack_manifest_template.json` — embedded template (NEW)
- [ ] `internal/app/cmd/slack_manifest_test.go` — golden-file test (NEW)
- [ ] `internal/app/cmd/testdata/slack_manifest_golden.json` — golden output for fixed inputs (NEW)
- [ ] No additional framework install needed — Go stdlib `testing` + existing `httptest` pattern covers everything.
- [ ] No additional shared fixtures needed — fakes are constructed in-test (matching `create_slack_test.go::fakeSlackAPI` pattern).

## Sources

### Primary (HIGH confidence)
- `pkg/slack/client.go` (in-repo) — `*Client` patterns: `callJSON`, `*SlackAPIError`, `JoinChannel` idempotency, `InviteShared`, `SlackAPIBase`. Verified by direct read.
- `pkg/slack/client_test.go` (in-repo) — httptest mock pattern (`newClientAgainstServer`, `slackOK`, `slackErr` helpers). Verified by direct read.
- `internal/app/cmd/slack.go` (in-repo) — `SlackCmdDeps` struct, cobra subcommand wiring pattern (`newSlackInitCmd`/`newSlackTestCmd`/...), `SlackPrompter` interface, `VerifyEventsAPIScopes`, `isSlackProWorkspaceError`. Verified by direct read.
- `internal/app/cmd/create_slack.go` (in-repo) — `resolveSlackChannel`, fail-soft pattern at line 201-210, `SlackAPI` interface. Verified by direct read.
- `internal/app/cmd/doctor_slack.go` (in-repo) — `fetchSlackBotScopes` returning `[]string` from `X-OAuth-Scopes` header. Verified by direct read.
- `internal/app/cmd/doctor_slack_transcript.go` (in-repo) — `checkSlackFilesWriteScope` pattern for scope-existence checks. Verified by direct read.
- `pkg/profile/types.go` (in-repo) — `CLISpec` struct, existing `*bool` and slice patterns. Verified by direct read.
- `pkg/profile/validate.go` (in-repo) — Phase 67/68 validation rule patterns (SI1/SI2/SI3, ST1/ST2/ST3). Verified by direct read.
- `pkg/profile/schemas/sandbox_profile.schema.json` (in-repo) — JSON Schema mirror for CLISpec fields. Verified by direct read.
- [Slack `users.lookupByEmail` API reference](https://docs.slack.dev/reference/methods/users.lookupByEmail/) — required scopes (`users:read.email`), Tier 3 rate limit, error codes (`users_not_found`, `missing_scope`, `invalid_auth`, `ratelimited`, etc.), JSON request/response shape.
- [Slack `conversations.invite` API reference](https://docs.slack.dev/reference/methods/conversations.invite/) — required scopes, Tier 3 rate limit, error codes (`already_in_channel`, `cant_invite_self`, `user_is_restricted`, `user_not_found`, `channel_not_found`, `not_in_channel`).
- [Slack Web API rate limits](https://docs.slack.dev/apis/web-api/rate-limits/) — Tier 3 = 50+/min.

### Secondary (MEDIUM confidence)
- [slackapi/node-slack-sdk #1523](https://github.com/slackapi/node-slack-sdk/issues/1523) — `users.lookupByEmail` case-sensitivity edge case. Cross-referenced with WebSearch results.
- `/Users/khundeck/Downloads/km-personal.json` (referenced by CONTEXT.md, not directly read in this research) — production manifest reference. Confidence in field shape comes from CONTEXT.md inline JSON quote.

### Tertiary (LOW confidence)
- None. All claims in this research either reference verified in-repo code or official Slack docs.

## Metadata

**Confidence breakdown:**
- Standard stack: **HIGH** — every library/pattern is already in-repo and verified by direct read.
- Architecture: **HIGH** — all five new files mirror existing files (`client.go`, `slack.go`, `create_slack.go`, `validate.go`, schema.json). Discovered via direct file inspection.
- Pitfalls: **HIGH** — derived from Slack docs (Tier 3, error codes), existing in-repo Phase 63/67/68 lessons (scope drift, fail-soft, multi-instance bridge URLs), and the explicit pattern at `slack.go:417` for Pro-tier handling.
- Slack API specifics: **HIGH** — Slack official docs verified directly via WebFetch.

**Research date:** 2026-05-06
**Valid until:** 2026-06-06 (30 days — Slack API surface is stable; in-repo patterns may evolve as parallel phases land but won't regress the patterns documented here)

**Notes for planner:**
- Phase has no formal REQ-IDs; CONTEXT.md decisions D1–D12 are the requirement set (mapped in `<phase_requirements>` section above).
- Wave 0 should land the new files as stubs (interfaces + skeleton tests) before any wiring touches `RunSlackInit` or `create_slack.go::resolveSlackChannel`-adjacent code, so the orchestrator's contract is locked before three callers depend on it.
- Recommend a single-task plan for the Slack App scope rollout: (1) operator runs `km slack manifest` against existing klankermaker.ai install, (2) updates Slack App scopes (adds `users:read.email`), (3) reinstalls, (4) `km slack rotate-token`. This is UAT, not automated.
- The `make build` requirement (memory: feedback_rebuild_km) applies to every plan that touches CLI source — every Phase 72 task except pure-data/schema changes.
