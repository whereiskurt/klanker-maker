# Slack Default Router — Design Spec

**Date:** 2026-06-05
**Status:** Approved (brainstorm complete)
**Phase:** 96
**Branch:** `phase-96-slack-default-router`
**Builds on:** Phase 95 (federated bridge relay)

## Problem

After Phase 95, an @-mention of the shared bot in a channel that **no install
owns** (no sandbox bound to that channel) is silently dropped — the relay
broadcasts, every install misses, and the message dies with a
`slack_relay_no_owner` log line. A human who @-mentions the bot gets no response.
The most common case: a channel the bot was invited to, or a `#sb-*` channel
whose sandbox was destroyed without archiving the channel.

## Goal

When the bot is @-mentioned in a channel that is a true orphan (no install owns
it), have a single designated **default-router** install post a helpful threaded
reply: explain that no sandbox is bound, show the channel naming convention
(`#sb-{alias}-{profile}`), and list the currently-running sandbox channels across
**all** installs so the human can join one.

## Key architectural facts (from research)

1. **One App ⇒ only the front door receives raw Slack events.** Peers only see
   *relayed* copies the front door forwards. So "does any install own this
   channel?" is a question only the front door can answer — by broadcasting and
   collecting answers.
2. **`slack.default_router` is effectively a front-door capability toggle.** Only
   the front door sees raw events and the gather result, so it is the only node
   that can both detect a true orphan and reply. Setting the flag on a
   non-front-door install is a no-op.
3. **Member-channel-only (v1).** Slack delivers `message.channels` /
   `message.groups` events only for channels the bot is a member of, so the
   trigger is "bot is in the channel but no sandbox is bound." In-channel reply
   uses `chat:write` — **no new Slack scopes, no `app_mention`, no manifest
   change.**
4. **The bot cannot DM** (no `im:write`) — irrelevant for v1 (we reply
   in-channel).

## Non-goals (deferred)

- **Agentic self-serve create** ("@bot spin me up a `profiles/patch.yaml` bot" →
  bridge triggers a real `km create` via EventBridge). The north star, but a
  large separate feature (NL intent parsing, profile allowlist, auth/abuse
  controls, cost gating, async provisioning UX). Captured as a documented
  follow-on phase, **not built here.**
- **Non-member channels** (`app_mention` event + `chat:write.public` scope +
  manifest re-install + uncertain Slack delivery behavior).
- **DM fallback** (`im:write`).
- Any change to `km slack init`, the SandboxProfile schema, or sandbox
  provisioning. Existing sandboxes need **no recreate**.

## Architecture

### Core: broadcast → claim-aware scatter-gather

Phase 95's relay broadcasts and ignores the result. Phase 96 upgrades it so the
front door learns the outcome.

**Peer response contract changes** from `200 "ok"` to `200 {json}` for a
*relayed* request:

```json
{ "claimed": true }
{ "claimed": false, "channels": [ {"id":"C123","alias":"orc","profile":"patch"}, ... ] }
```

- A peer that **owns** the channel processes it (exactly as today) and returns
  `claimed: true`.
- A peer that does **not** own it returns `claimed: false` plus its
  currently-running sandbox channels (a local `km-sandboxes` query:
  `state=running` AND `slack_channel_id` present).
- The front door, after the bounded (~2.5 s) gather:
  - **any `claimed: true` ⇒ done** — the owner handled it (today's behavior).
  - **zero claims ⇒ true orphan** — proceed to the router reply (if enabled).
- The front door also includes **its own** running sandbox channels in the
  aggregate list.

**Rollout safety:** a peer still on Phase-95 code returns the legacy plain
`"ok"` (not JSON). The front door treats any unparseable/legacy/HTTP-error
response as `claimed: true` (conservative — never post a false "no sandbox here"
when ownership is uncertain). This makes a mixed-version fleet safe during
deploy.

### The router reply

- **Config:** `slack.default_router: true` in `km-config.yaml`, plumbed exactly
  like `slack.mention_only` (struct field + v2→v merge-list + tri-state
  population + `init.go` env export with drift WARN → terragrunt `get_env` → TF
  var → Lambda env `KM_SLACK_DEFAULT_ROUTER`). Default **false** ⇒ today's
  behavior (orphan mentions dropped/logged). Meaningful only on the front-door
  install.
- **Trigger (all three required):**
  1. the message **@-mentions the bot** (reuse the existing mention detection —
     `<@{bot_user_id}>` scan, same as Phase 91 mention-only), AND
  2. `FetchByChannel` misses locally on the front door, AND
  3. the scatter-gather returns **zero claims**.
- **Reply:** a **threaded** reply on the triggering message (`thread_ts` =
  message ts), e.g.:
  > No sandbox is bound to this channel. To work with a bot, join one of its
  > channels — they're named `#sb-{alias}-{profile}`. Currently running:
  > • `<#C123>` — orc (patch)  • `<#C456>` — wrkr (hardened) …

  Channels are rendered as `<#CHANNELID>` mentions so Slack applies per-viewer
  visibility. If the aggregate list is empty, the reply omits the list and just
  gives guidance + how to get a sandbox.
- **Cooldown:** per-channel, reusing the existing pause-hint cooldown mechanism
  (default 3600 s) so a busy orphan channel gets one reply, not one per mention.
  Tracked the same way the pause hint tracks `last_pause_hint_ts` (a per-channel
  / per-row timestamp + cooldown compare).
- **No new Slack scopes.** In-channel `chat:write` (bot is a member).

### Anti-loop

The bot's reply is itself a message in the channel. The existing self-message /
`isBotLoop` filter (events_handler.go ~176-180) already drops the bot's own
messages, so the reply cannot re-trigger the router.

## Data flow

```
Slack App → front-door bridge (@-mention in unowned channel)
   ├─ FetchByChannel hit?            → process locally (today)
   └─ miss → scatter-gather to peers:
        ├─ a peer claims (owns it)   → that peer processes; front door: done
        └─ zero claims (orphan):
             └─ default_router=true & mention & member
                  → post threaded reply listing running channels
                    (front door's own + all peers' from the gather),
                    subject to per-channel cooldown
```

## Components / files (implementation surface)

| File | Change |
|---|---|
| `internal/app/config/config.go` | `SlackConfig.DefaultRouter *bool` + merge-list `"slack.default_router"` + tri-state population |
| `internal/app/config/config_test.go` | round-trip + merge-list regression + drift tests |
| `internal/app/cmd/init.go` | export `KM_SLACK_DEFAULT_ROUTER` (bool) with drift WARN (mirror MentionOnly) |
| `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` | `slack_default_router = get_env("KM_SLACK_DEFAULT_ROUTER", "false")` |
| `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` | `slack_default_router` string var (default "false") |
| `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` | `KM_SLACK_DEFAULT_ROUTER = var.slack_default_router` Lambda env |
| `pkg/slack/bridge/relayer.go` | scatter-gather: change `Broadcast` to return per-peer claim results (`[]PeerClaimResult`), parse `200 {json}`, legacy/error → claimed:true |
| `pkg/slack/bridge/relayer_test.go` | gather tally + legacy-response + bounded-timeout tests |
| `pkg/slack/bridge/events_handler.go` | relayed-request handler returns `{claimed,channels}`; front-door miss path consumes the gather; orphan→reply (gated on default_router + mention + cooldown) |
| `pkg/slack/bridge/events_interfaces.go` | extend relayer interface; add a "list running sandbox channels" interface + a cooldown store interface (or reuse pause-hint store) |
| `pkg/slack/bridge/aws_adapters.go` | DDB adapter: list running sandbox channels (`state=running` + `slack_channel_id`), returning {id,alias,profile}; resolve cross-install aggregation in handler |
| `pkg/slack/bridge/events_handler_test.go` | orphan-detection, mention gate, cooldown, reply formatting, default_router=false silent, claim short-circuit |
| `cmd/km-slack-bridge/main.go` | read `KM_SLACK_DEFAULT_ROUTER`; wire the router (default-off, nil-safe) |
| `internal/app/cmd/doctor.go` / `doctor_slack.go` | (optional) WARN if `default_router:true` but install isn't the Slack Request-URL host |
| `docs/slack-notifications.md` | § Phase 96 default router |
| `CLAUDE.md` | Phase 96 note |

## Deploy / rollout

- Lambda env-block change ⇒ `make build-lambdas` (clean) + `km init
  --dry-run=false` (NOT `--sidecars`), same constraint as `slack.mention_only`.
- **Deploy order matters** because the peer response contract changes: deploy
  **all installs** before relying on cross-install channel lists. The legacy→
  `claimed:true` fallback keeps a mixed fleet correct (no false orphan replies)
  during the rollout window.
- No SandboxProfile schema change ⇒ existing sandboxes need no recreate.

## Correctness invariants

- Inherits Phase 95's channel-name uniqueness invariant (a channel is owned by
  at most one install).
- The router reply fires **only** on a true orphan (zero claims) — never when any
  install owns the channel. Uncertain ownership (legacy/error response) is
  treated as owned, so the bot stays silent rather than posting a wrong reply.
- `default_router` defaults false ⇒ Phase 96 is fully dormant until opted in;
  with it off, behavior is byte-identical to Phase 95.

## Testing

- **Scatter-gather tally:** mixed peer responses → correct claimed/unclaimed
  aggregation; legacy `"ok"` and HTTP error both count as `claimed:true`;
  bounded-timeout honored.
- **Orphan detection:** zero claims + bot-mention + member channel ⇒ reply;
  any claim ⇒ no reply; non-mention message in orphan channel ⇒ no reply.
- **Cooldown:** second mention within window ⇒ suppressed; after window ⇒ replies.
- **Reply formatting:** aggregates front-door + peer channels; renders
  `<#CID>`; empty list ⇒ guidance-only variant.
- **Config:** `slack.default_router` round-trips (merge-list regression);
  `default_router:false`/absent ⇒ silent (Phase 95 behavior); drift WARN on
  `KM_SLACK_DEFAULT_ROUTER` mismatch.
- **Anti-loop:** the bot's own reply is filtered by the existing self-message
  guard (regression assertion).

## Manual UAT (cannot be automated)

Two installs + one Slack App in one account: invite the bot to a channel with no
bound sandbox; @-mention it; confirm exactly one threaded reply listing running
channels from both installs; @-mention again within the cooldown → no second
reply; bind a sandbox to that channel (or have an install own it) → @-mention →
no router reply (owner handles it).
