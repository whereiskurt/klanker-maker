# Phase 96: Slack default router — Context

**Gathered:** 2026-06-05
**Status:** Ready for planning
**Source:** Brainstorm + approved design spec (`docs/superpowers/specs/2026-06-05-slack-default-router-design.md`)

<domain>
## Phase Boundary

**Delivers:** When the shared bot is @-mentioned in a channel no install owns,
a designated `slack.default_router` install posts one helpful threaded reply
(naming convention + running sandbox channels across all installs) instead of
silently dropping (`slack_relay_no_owner`). Built on Phase 95.

**In scope:**
- `slack.default_router *bool` config + plumbing to `KM_SLACK_DEFAULT_ROUTER`
  (mirror `slack.mention_only`).
- Upgrade Phase 95 relay broadcast → claim-aware scatter-gather.
- Orphan detection + threaded reply + per-channel cooldown.
- Cross-install running-channel aggregation via the gather.
- Docs + CLAUDE.md note; optional doctor WARN (default_router on non-front-door).

**Out of scope (deferred, LOCKED):**
- Agentic self-serve create (`@bot spin me up profiles/patch.yaml` → EventBridge
  km create). The north star; a separate future phase.
- Non-member channels (`app_mention` + `chat:write.public` + manifest re-install).
- DM fallback (`im:write`).
- Any change to `km slack init`, SandboxProfile schema, or sandbox provisioning.
  Existing sandboxes need NO recreate.
</domain>

<decisions>
## Implementation Decisions (LOCKED from brainstorm)

### Why only the front door
- One App ⇒ only the front-door bridge receives raw Slack events; peers only see
  relayed copies. So only the front door can detect a true orphan and reply.
  `slack.default_router` is effectively a front-door capability toggle; setting
  it on a non-front-door install is a no-op.

### Core: broadcast → claim-aware scatter-gather
- A relayed-request handler returns `200 {json}` instead of `200 "ok"`:
  - owner: `{ "claimed": true }`
  - non-owner: `{ "claimed": false, "channels": [{"id","alias","profile"}, …] }`
    where channels = this peer's running sandboxes (`km-sandboxes` `state=running`
    AND `slack_channel_id` present).
- Front door includes its OWN running channels in the aggregate.
- Tally: any `claimed:true` ⇒ owner handled it ⇒ NO router reply. Zero claims ⇒
  true orphan.
- ROLLOUT SAFETY (critical): a peer on Phase-95 code returns legacy plain `"ok"`.
  Front door treats any unparseable/legacy/HTTP-error response as `claimed:true`
  (conservative — never post a false "no sandbox here"). Mixed fleet safe.

### Trigger (ALL three required)
1. message @-mentions the bot (reuse Phase 91 `<@{bot_user_id}>` detection), AND
2. front-door `FetchByChannel` misses locally, AND
3. scatter-gather returns zero claims.

### Reply
- Threaded on the triggering message (`thread_ts` = msg ts).
- Content: "no sandbox bound" + convention `#sb-{alias}-{profile}` + running
  channels (front door + all peers) rendered as `<#CID>` mentions (Slack handles
  per-viewer visibility). Empty list ⇒ guidance-only variant.
- Per-channel cooldown: reuse the existing pause-hint cooldown (default 3600s).
- In-channel `chat:write` only — NO new Slack scopes, NO `app_mention`.

### Config plumbing (mirror slack.mention_only EXACTLY)
- `internal/app/config/config.go`: `SlackConfig.DefaultRouter *bool`
  (`mapstructure:"default_router"`); add `"slack.default_router"` to v2→v
  merge-list (footgun: `project_config_key_merge_list`); populate via
  `v.GetBool` when `v.IsSet`.
- `internal/app/cmd/init.go`: export `KM_SLACK_DEFAULT_ROUTER` (bool) with
  env-wins drift WARN (mirror MentionOnly/ReactAlways block).
- `terragrunt.hcl`: `slack_default_router = get_env("KM_SLACK_DEFAULT_ROUTER", "false")`.
- TF module variables.tf + main.tf: `slack_default_router` var → Lambda env.
- `cmd/km-slack-bridge/main.go`: read env; wire router (default-off, nil-safe).

### Anti-loop
- The bot's reply is filtered by the existing self-message / `isBotLoop` guard
  (events_handler.go ~176-180); it cannot re-trigger the router.
</decisions>

<specifics>
## Specific Ideas / References

- Channel naming convention to show users: `#sb-{alias}-{profile}` — matches the
  `channelName` token substitution (`pkg/profile/types.go:127`, default
  `sb-{alias}`/`sb-{id}`; derivation in `create_slack.go:429`).
- Relay engine to upgrade: `pkg/slack/bridge/relayer.go` (`HTTPPeerRelayer`,
  Phase 95). Decision/miss site: `events_handler.go:197-234`.
- Cooldown precedent: `DDBPauseHinter` + `last_pause_hint_ts` cooldown
  (`aws_adapters.go`; CooldownSeconds 3600). Reuse the pattern.
- Running-channel enumeration: `km-sandboxes` `state=running` + `slack_channel_id`
  (+ `alias`, `profile_name`). FetchByChannel uses GSI `slack_channel_id-index`;
  the new list is a Scan/Query the other direction.
- Mention detection precedent: Phase 91 mention-only `<@{bot_user_id}>` scan
  (events_handler.go ~270).
- Deploy: `make build-lambdas` (clean) + `km init --dry-run=false` (NOT
  `--sidecars`); deploy ALL installs before relying on cross-install lists
  (`project_km_init_lambdas_doesnt_deploy`,
  `project_km_init_skips_existing_lambda_zips`).
</specifics>

<deferred>
## Deferred Ideas

- Agentic self-serve create via EventBridge (north star; separate phase).
- Non-member channel handling (`app_mention` + `chat:write.public`).
- DM fallback (`im:write`).
- Caching the running-channel enumeration (only if perf shows a need; cooldown
  already bounds frequency).
</deferred>

---

*Phase: 96-slack-default-router-orphan-channel-mention-reply*
*Context gathered: 2026-06-05 from approved design spec*
