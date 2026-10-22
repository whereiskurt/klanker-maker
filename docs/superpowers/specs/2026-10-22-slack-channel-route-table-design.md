# Slack channel route table — design

**Date:** 2026-10-22
**Status:** approved in brainstorm; spec for review
**Builds on:** `docs/slack-default-route.md` (the default route and its ladder),
`docs/superpowers/specs/2026-10-17-slack-react-quiet-and-email-allowlists-design.md`
(the `*@<domain>` grammar and the `allowEmails` override rule)

## 1. Problem

An operator who knows ahead of time which Slack channels should talk to which
klanker has no way to say so. Today a channel reaches a specific sandbox only
through ownership (`notification.slack.perSandbox: true` plus
`notification.slack.inbound.enabled: true`, the channel born at `km create` or
seeded with `km slack adopt`), which is strictly one channel to one sandbox
instance. `slack.default_route` sends every unowned conversation to one alias
for the whole install. `notification.slack.channelOverride` carries
notifications only; `inbound.enabled: true` beside it is a validation error.

Wanted: five known channels, two or three klankers, chosen per channel by
name, several channels allowed to share one klanker, cold-create on first use,
the same deny-by-default email gate the default route has, and a change to the
mapping that costs one `km init --slack` and no sandbox recreate.

## 2. Decisions

| Decision | Choice | Why |
|---|---|---|
| Where the mapping lives | `km-config.yaml` `slack.routes`, beside `default_route` | Which bot answers which channel is an install-level routing decision; the bridge needs the whole table at event time anyway. Declaring it per profile would make `km init` harvest every profile (rejected approach C). |
| Mechanism | Generalise the default route: the ladder runs against "the route for this message" = `table[channel]`, else `default_route` | The bridge already threads one `DefaultRoute` through continuity, the mention gate, alias resolve, cold-create and the email gate. Ownership (`slack_channel_id` GSI) is untouched (rejected approach B: multi-channel ownership binds a sandbox instance, not an alias, so recreate and cold-create cannot follow it). |
| Channel identity in config | `#name` or `C…` id; names resolve to ids once at `km init` on the laptop | Slack events carry ids, not names. Resolution reuses the bounded `FindChannelByName` scan and the `channel-id-by-name` SSM cache `km create` already keeps. Never a scan inside the Lambda (Phase 104). |
| Transport to the bridge | One JSON SSM parameter `/{prefix}/slack/routes`, read at cold start; its SHA-256 in the Lambda env | The bridge env is already large and Lambda caps the block at 4 KB (the H1 bridge hit it). The hash in the env is what makes a changed table cold-start the Lambda on `km init --slack`. |
| Email gate | Per route: own `allow_emails` replaces; else the install list (`default_route.allow_emails`); else the route profile's `inbound.allowEmails`; all empty ⇒ `km init` error | Same two-list rule the default route uses, plus one optional narrower list. Deny-by-default stays absolute. |
| Mention | Required by default; per-route `mention_only: false` opt-out for top-level posts | A dedicated channel may want every post dispatched; a shared one must not. Thread follow-ups keep the box's `threadBypass` behaviour. |
| Uninvited channels | Out of scope (separate phase) | Needs `app_mention` + `chat:write.public`, a manifest reinstall, and Slack's delivery semantics for unjoined public channels are unverified. |
| Several channels, one alias | Allowed | Routing is by alias; the alias's box warm-enqueues from every channel that names it. |
| Duplicate channel | `km init` error, by resolved id | Two routes for one channel would be two answers to one message. |

## 3. Config and validation

```yaml
slack:
  routes:
    - channel: "#security-alerts"      # "#name" (lowercase, Slack-legal) or "C0AB12CD3"
      alias: ir-bot
      profile: profiles/ir-bot.yaml
      allow_emails: ["*@example.com"]  # optional; absent ⇒ install list, then profile list
      mention_only: false              # optional; absent ⇒ true
    - channel: "#platform-eng"
      alias: platform-bot
      profile: profiles/platform.yaml
  default_route:                       # optional when routes is present
    alias: ir-bot
    profile: profiles/ir-bot.yaml
    allow_emails: ["ops@example.com"]
```

`internal/app/config`:

- New `SlackRoute{Channel, Alias, Profile, AllowEmails []string, MentionOnly *bool}`
  and `SlackConfig.Routes []SlackRoute` (`mapstructure:"routes"`). **`slack.routes`
  goes into the v2→v merge-list** with a regression test, the
  `project_config_key_merge_list` footgun.
- `SlackRoute.Validate()` (no disk, no Slack): `channel`, `alias`, `profile`
  required; `channel` is `#` + a Slack-legal channel name (lowercase letters,
  digits, `-`, `_`, 1–80 chars) or matches the existing `^C[A-Z0-9]+$`; each
  `allow_emails` entry passes `ValidateEmailEntry` (exact address or exactly
  `*@<domain>`); `mention_only` any bool.
- `SlackConfig.ValidateRoutes()`: two routes with the same literal `channel`
  are rejected here; the by-resolved-id duplicate check happens at `km init`
  (§4) because two names cannot collide but a name and an id can.
- `default_route` is optional when `routes` is present. With `routes` and no
  `default_route`, an unmapped channel gets the Phase 96 behaviour exactly as
  an install with neither has today.

`km init` (and `km init --slack`), extending `prepareSlackDefaultRoute`:

- Each route's profile resolves through the same locator the default route
  uses (`locateSlackDefaultRouteProfile`), must have Slack inbound enabled
  (`notification.slack.inbound.enabled: true`), and is flattened and pre-staged
  to `slack-profiles/{slug}/.km-profile.yaml` by the same uploader
  (`prestageSlackDefaultRoute` generalised over N routes). Two routes naming one
  profile stage it once.
- The effective allow list per route is computed here, in this order, and the
  first non-empty wins: the route's `allow_emails`; `default_route.allow_emails`;
  the route profile's `inbound.allowEmails`. All three empty ⇒ error naming the
  route. The chosen list is what the table carries; the bridge never merges.

## 4. Name resolution and transport (`km init`)

Runs on the laptop in the lambda-slack-bridge hook (full apply and `--slack`),
after `ExportTerragruntEnvVars`, before the terragrunt apply. Fail-closed:
any error below stops `km init` before any apply.

1. **Id given.** Taken verbatim; one `conversations.info` confirms the channel
   exists and the bot is a member.
2. **Name given.** Read the cache `/{prefix}/slack/channel-id-by-name/<name>`
   (the key `cacheSlackChannelIDByName` writes). On a hit, one
   `conversations.info` confirms the id still carries that name and the bot is
   a member; a name mismatch is a rename or a reuse (§4 step 4). On a miss,
   `FindChannelByName(ctx, name, SlackMaxScanPages)` over
   `types=public_channel,private_channel` (scopes `channels:read` and
   `groups:read` are already in the manifest), then cache the result. A scan
   that exceeds the page cap or is rate-limited fails with a message telling
   the operator to put the `C…` id in the config.
3. **Membership.** The bot not being a member is an error naming the channel
   and the invite step. Slack delivers `message.channels` / `message.groups`
   only for channels the app is in; a route to a channel the bridge cannot
   hear from is a misconfiguration.
4. **Renames.** Ids are stable, so a renamed channel keeps routing. When the
   cached id's current name differs from the configured name, the resolver
   treats the configured name as authoritative: it scans for the configured
   name; if found, that is the route and the cache entry is rewritten (WARN:
   "`#old` is now `#new`; `#old` resolved to `C…`"); if not found, error.
5. **Duplicates.** Two routes resolving to one id ⇒ error naming both entries.

Transport:

- `km init` writes `/{prefix}/slack/routes` (String, not SecureString: it holds
  channel ids, aliases, profile slugs and allow lists, none secret) with the
  canonical JSON below, keys sorted, routes ordered by channel id, so the same
  config yields the same bytes and the same hash.

  ```json
  {"v":1,"routes":[{"channel":"C0AB12CD3","alias":"ir-bot","profile":"ir-bot",
    "allow":["*@example.com"],"mention_only":false}]}
  ```

  `profile` is the pre-stage slug (`SlackDefaultRouteProfileSlug`), the value
  the cold-create event needs, not the laptop path.
- `KM_SLACK_ROUTES_SHA256` = hex SHA-256 of those bytes is exported for
  terragrunt and lands in the bridge env (`var.slack_routes_sha256`, default
  `""`). A changed table changes the env, the apply updates the function, and
  the Lambda cold-starts and re-reads. `routes:` absent ⇒ the parameter is
  deleted if present and the env value is `""`, byte-identical to today.
- The operator policy already grants `ssm:PutParameter`/`DeleteParameter` on
  `parameter/{prefix}/*`; no change there. The bridge module
  (`infra/modules/lambda-slack-bridge/v1.0.0`, edited in place, additive) gains
  an exact-ARN `ssm:GetParameter` on `parameter/{prefix}/slack/routes`, beside
  the bot-token and signing-secret statements.
- The sandbox role is not involved: routing is entirely bridge-side.

### 4.1 Refinements made while planning (2026-10-22)

- **Resolution uses `users.conversations`, not `conversations.list`.** It lists
  only the channels the bot is a member of (public and private, scopes
  `channels:read` + `groups:read`), so it is bounded by the bot's membership,
  needs no scan cap, and makes "the bot must be a member" structural: a name
  or id absent from that list is unresolvable. Steps 2–4 above collapse to one
  listing per `km init`; the by-name SSM cache is not consulted. A name that
  is not found fails with the bot's current channel names in the message, so a
  rename shows as "`#old` not found; bot channels: `#new`, …".
- **No routes ⇒ the parameter is overwritten with the empty document
  (`{"v":1,"routes":[]}`) only when it already exists; it is never deleted.**
  Same dormancy (the bridge treats an empty table and a missing parameter
  alike), no SSM delete capability added to the operator-side store.
- **The hash is a cold-start trigger only.** The bridge reads the parameter,
  logs the hash it computed beside `KM_SLACK_ROUTES_SHA256`, and never fails
  on a mismatch. `km init --plan` exports the hash of the live parameter and
  neither resolves nor writes; `km doctor` reports config-vs-table drift.

## 5. Bridge (`pkg/slack/bridge`, `cmd/km-slack-bridge`)

### 5.1 Table

`cmd/km-slack-bridge/main.go` wires, after `wireDefaultRoute`, a
`wireChannelRoutes(ctx, ssm, prefix)`: `GetParameter` on `/{prefix}/slack/routes`,
decode, build `map[string]*DefaultRoute` keyed by channel id through
`NewDefaultRoute(alias, profile, allow, nil)` plus the `MentionOnly` field. A
missing parameter is an empty table (Info log). A read or decode error logs
once at WARN with the error and leaves the table empty: the default route
still works and no event ever fails on it. The table is read once per cold
start and never refreshed; the env hash is the invalidation.

`DefaultRoute` gains `MentionOnly bool` (true for the default route and for a
route without the key). No second type: a channel route *is* a default route
with a narrower audience.

### 5.2 Selection

In `EventsHandler.Handle`, on the ownership miss (`info.SandboxID == "" ||
info.QueueURL == ""`), before ladder step 1:

```go
route := h.DefaultRoute
if r, ok := h.Routes[msg.Channel]; ok { route = r }
```

A DM's `D…` channel never matches, so DMs keep going to the default route.
`route == nil` with no table match means "no route configured" and the
existing Phase 96 branch runs. The selected route is carried in
`routeContinuity` and `routeDecision` and replaces every `h.DefaultRoute` read
in `resolveDefaultRoute`, `routeTarget`, `routeAllows`, `deliver`'s cold-create
publish, and continuity's own-box check.

### 5.3 Precedence (unchanged in order, one new rung)

1. Owned `#sb-` channel (`FetchByChannel` hit): untouched.
2. Routed-thread continuity (`km-slack-threads` row): a thread started under
   one mapping stays with its box after the table changes; rebind only when
   that box is gone, to the *current* route of the channel.
3. Peer claim (Phase 95 relay): a peer that owns the channel still wins over a
   mapping, so one message is never processed on two installs.
4. `table[channel]`.
5. `default_route`.
6. Phase 96 orphan reply / silent drop.

### 5.4 Gates

- **Mention:** required when `requiresMention(msg.ChannelType)` and either
  the route's `mention_only` is true or the message is a thread reply.
  `mention_only: false` lifts the mention for an allowed sender's top-level
  post only. Thread follow-ups keep the existing `skipMention` logic (the
  box's `threadBypass`), so a strict box still needs a mention in a thread. The bot-user-id fetch still
  fails closed.
- **Email:** `senderAllowedForSandbox` unchanged: the receiving box's row list
  wins when it carries one; otherwise the gate names the route's list
  (`gateRouteCreate` for the route's own box and a cold-create) or the install
  list (`gateRouteInstall` for any other box). The route's compiled list is the
  effective list computed at `km init` (§3), so the install fall-back is
  already folded in. Identity resolution and the no-cached-deny rule are
  untouched.
- **Cold-create:** `PutSandboxCreate(ctx, id, route.Alias, route.Profile, body)`.
  Two routes sharing an alias produce one box; a second mention on the other
  channel while the first create is in flight hits the Phase 109 alias
  resolution exactly as two mentions in one channel do today.

### 5.5 Logs

Every `events: route:` line gains `route=<channel id>` or `route=default`.
`wireChannelRoutes` logs the table size and the hash it loaded at cold start.

## 6. `km doctor`

New group **Slack channel routes** (`checkSlackChannelRoutes`, beside
`checkSlackDefaultRoute`), skipped silently with zero AWS or Slack calls when
`routes:` is absent. One evaluation, three results:

1. Every route's profile resolves through the same locator, has Slack inbound
   enabled, and a strict (`threadBypass: false`) profile is noted; the effective
   allow list per route is printed with its source (route / install / profile).
2. Every route's channel resolves: cache read plus one `conversations.info`
   each (no scan); bot not a member ⇒ WARN naming the channel.
3. The SSM table matches what the config would produce now (recompute the
   canonical JSON from config plus the cache, compare hashes); a mismatch ⇒
   WARN "run `km init --slack`". Parameter absent with `routes:` present ⇒
   WARN the same.

## 7. Tests

- **Config** (`internal/app/config`): `slack.routes` survives the merge-list;
  literal duplicate channels rejected; `#name` / `C…` accepted, `name`,
  `#Name`, `#a b` rejected; `routes` without `default_route` accepted; the
  effective-list fall-back order and the all-empty error, table-driven.
- **Resolution** (`internal/app/cmd`): fake Slack API recording calls. Cache
  hit ⇒ one `conversations.info`, no scan; miss ⇒ one scan and one cache
  write; non-member ⇒ error; rename ⇒ scan for the configured name, cache
  rewritten, WARN text; two routes to one id ⇒ error; canonical JSON is
  byte-stable across map-order changes (same hash); absent `routes:` ⇒ no
  parameter, empty hash; pre-stage covers N routes and stages a shared profile
  once.
- **Bridge** (`pkg/slack/bridge`): a mapped channel dispatches to its alias
  while an unmapped one goes to the default route; two routes sharing an alias
  enqueue to one box; `mention_only: false` dispatches a top-level post without
  a mention and still drops an unlisted sender; continuity keeps a thread on
  its box after the table changes; a peer claim beats a mapping; a DM ignores
  the table; cold-start read failure leaves the default route working; the
  `route` field is on both paths; `wireChannelRoutes` decode of the canonical
  JSON.
- **Terraform** (`pkg/terragrunt`): the bridge module's env block and IAM pair
  the routes parameter (exact ARN) and `slack_routes_sha256` is in the env.
- **Doctor**: three results against fake lookups, including drift and absent
  parameter.
- **Live UAT** (§9) with the synthetic HMAC driver.

## 8. Rollout

- **First deploy:** `make build` → `make build-lambdas` → `km init --dry-run=false`
  (bridge code, the module's SSM grant, the hash env var). No sandbox recreate.
- **Every later change to `routes:`:** `km init --slack`.
- **Half-deploys, all quiet, none dangerous:** new config + old bridge ⇒ the
  parameter exists and nobody reads it; mapped channels fall through to the
  default route or Phase 96. New bridge + no parameter ⇒ empty table,
  byte-identical to today. Table read error at cold start ⇒ WARN once, default
  route only.
- **Docs:** CLAUDE.md bullet; `docs/slack-notifications.md` new section
  "Channel routes" beside "Default route" (config, resolution, gates, doctor,
  deploy, troubleshooting); `docs/slack-default-route.md` ladder updated with
  the new rung; `OPERATOR-GUIDE.md` config reference.

## 9. Live UAT (go/no-go)

On this install, two routes to two aliases (`ir-bot` and a second lean
profile), default route left on `ir-bot`:

1. Mention in channel A ⇒ reply in the originating thread from alias A's box
   (cold-created on first use).
2. Mention in channel B ⇒ reply from alias B's box.
3. Channel B with `mention_only: false`: a top-level post with no mention
   dispatches; the same post from a synthetic unlisted user id is dropped
   silently (no 👀, no reply, Info log).
4. Thread follow-up in channel A without a mention continues on A's box.
5. Mention in an unmapped channel ⇒ `ir-bot` via the default route, unchanged.
6. Edit `routes:` (swap B to alias A), `km init --slack`, confirm the cold
   start log line with the new hash, mention in B ⇒ alias A's box.
7. Rename channel B in Slack, `km init --slack` with the old name ⇒ error
   naming the rename; update the config ⇒ clean.
8. `km doctor` green on every step above; drift WARN between step 6's edit and
   its apply.

## 10. Out of scope

- Uninvited channels (`app_mention` + `chat:write.public`): separate phase.
- Per-route agent selection or prompt preamble: the klanker's profile decides.
- Routing by channel *name pattern* (globs): names resolve to ids once; a
  pattern would need the scan at event time.
- Peer-install routes: a mapping lives on the front door only, like
  `default_route`.
