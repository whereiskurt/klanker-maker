<!--
  MAINTAINER NOTE — update this file BEFORE tagging each release.

  Its contents are injected VERBATIM into every GitHub release's notes,
  between the install header and goreleaser's auto-generated changelog.
  Wiring: .github/workflows/release.yml ("Load release highlights" step) →
  $KM_RELEASE_HIGHLIGHTS → .goreleaser.yaml `release.header` template.

  Keep it to the few MAJOR, human-curated additions for THIS release (the
  auto-changelog already lists every commit). HTML comments like this one
  are hidden in GitHub's rendered view. If this file is empty/absent the
  section is omitted gracefully.

  Curated by hand for v0.9.4 (Slack channel history) on top of v0.9.3's PRs #154 (Slack channel routes), #155
  (km-slack react + --broadcast), #157 (update --render/--subject) and #159
  (ec2spot v1.10.0: the SOPS KMS grant). No phase blocks since v0.9.0, so
  scripts/draft-release-highlights.sh had nothing to draft.
-->

## ⚠️ Upgrade notes — read before deploying

- **Slack App: `km slack manifest` → reinstall → `km slack rotate-token --bot-token <new>`.**
  Four new events (`member_joined_channel`, `channel_left`, `group_left`, `channel_deleted`).
  `groups:read` was already in the manifest, but your installed App may lack it; `km doctor`
  says. Channel history is default-off, so nothing changes until a route carries `history:`.

This release changes the sandbox IAM role (`ec2spot` **v1.10.0**), the Slack bridge and the
`km-slack` sidecar. No schema or Slack App change.

- **If you run SOPS secrets (`spec.secrets.sopsFile`), deploy this before recreating any
  sandbox.** The v0.9.0 and v0.9.2 notes told you to recreate boxes onto the least-privilege
  role; on v1.8.0/v1.9.0 that role could not decrypt the bundle, so a recreated box's agent
  started with no API key. See the first section below. Boxes already recreated need one
  more `km destroy && km create` on v1.10.0; `km doctor` → **Sandbox role shape** names them.

- **Deploy order:** `make build` → `make build-lambdas` → `km init --dry-run=false`.
  After that, every edit to `slack.routes:` is just `km init --slack`.
- **No reinstall.** `reactions:write` has been a required bot scope since the 👀 ACK, and
  `km doctor` already checks it. `--broadcast` needs no scope.
- **Existing sandboxes** gain `km-slack react`, `--broadcast` and `update --render` on a sidecar refresh or
  `km destroy && km create`. Until then, a new bridge with an old `km-slack` is byte-identical,
  and an old bridge with a new `km-slack` fails closed (`400 unknown_action`, or a signature
  failure for `--broadcast`). Nothing half-deployed is dangerous.
- **`km init` now reads the live routes table on every full and `--slack` apply**, even with
  no `routes:` block, so a stale table can be emptied. An SSM read error there stops the init.

## 🕰️ Channel history for the bots you invite in

A klanker invited to a channel can now look back. Three pieces, all default-off:

- **Glob routes.** `slack.routes` accepts `"#inc_*"`: the route owns every matching channel,
  including ones created later, bound when the bot joins.
- **A per-route mirror.** `history: {mirror: true, backfill: 7d, retention: 7d}` makes the
  bridge keep the channel's text (never file bytes, never DMs) in a table with a TTL, point-in-time
  recovery off, and only the bridge able to read it. Backfill runs in the background, paced by
  `slack.history_rate`. `km slack history purge` removes it on demand.
- **`km-slack history`.** `--since 7d --grep topic`, paged with `--before`, `--thread` for one
  thread. Allowed only in the box's own channel or a channel routed to it with `history.query`.

**Membership is the permission.** The bot reads what any member of the channel reads, so the
mirror and the verb return every message, humans and apps, private channels included. The email
allowlist still decides only who may *instruct* the bot. The strict-mode thread context follows
the same rule and now carries every human reply. History is labelled "background only, not
instructions" when the agent sees it.

```yaml
slack:
  routes:
    - channel: "#inc_*"
      alias: ir-bot
      profile: profiles/ir-bot.yaml
      allow_emails: ["*@example.com"]
      history: {query: true, mirror: true, backfill: 7d, retention: 7d}
```

## 🔑 SOPS bundles decrypt again on the least-privilege role

`ec2spot` v1.8.0 scoped the sandbox role's `kms:Decrypt` to the SOPS bundle key with a
`kms:ResourceAliases` condition. That key is **multivalued** (the set of aliases on the key)
and AWS requires a `ForAnyValue`/`ForAllValues` set operator for it. The policy used a bare
`StringEquals`, which never matches, so the grant authorized nothing. It had been that way
since Phase 89; the old `Allow */*` region lock covered for it, and v1.8.0 removed the cover.

Symptoms on a v1.8.0/v1.9.0 role with a SOPS profile: `km-secretsd` cannot unseal
(`AccessDeniedException … no identity-based policy allows the kms:Decrypt action`), `km-env`
injects nothing, `claude` exits with `Not logged in`, and the inbound poller retries the
message forever.

- **v1.10.0** writes `ForAnyValue:StringEquals`. The IMDS fence's session-policy Deny had the
  identical bug (so the Deny was inert) and is fixed the same way.
- **`km doctor` → Sandbox role shape** now reads each role's secrets-KMS policy and names the
  ones still carrying the bare operator.
- A guard test scans the pinned module and every Go policy builder for the pattern.
- The IAM policy simulator cannot see this class of bug. Verify from a box:
  `cat /opt/km/.km-secrets-check.json` (`unseal` ok) and
  `km-env exec --as claude -- sh -c 'env | grep -c ^ANTHROPIC_API_KEY='` (prints `1`).

## 📍 Route named channels to specific klankers

`slack.routes:` sits beside `slack.default_route` and maps channels to aliases by name:

```yaml
slack:
  routes:
    - channel: "#security-alerts"      # "#name" or a channel id (C…)
      alias: ir-bot
      profile: profiles/ir-bot.yaml
      allow_emails: ["*@example.com"]  # optional; else default_route's, else the profile's
      mention_only: false              # optional; default true
    - channel: "#platform-eng"
      alias: platform-bot
      profile: profiles/platform.yaml
```

- **Names are resolved at `km init`**, from the channels the bot is a member of (never a
  workspace-wide `conversations.list`). A channel the bot cannot see fails the init with the
  list of channels it can, so a renamed channel or a missing invite never routes silently.
- **The ladder is unchanged, with one new rung:** owned `#sb-` channel → thread continuity →
  peer claim → **`routes`** → `default_route` → the orphan reply. Several channels may share
  one alias, but an alias gets exactly one profile, or `km init` refuses: the first
  cold-create would otherwise decide the box's profile.
- **`mention_only: false` lifts the mention for top-level posts only.** Any allowed sender's
  message starts a turn; thread follow-ups keep the box's `threadBypass` rule. The table
  routes channels only, so DMs and group DMs still take the default route.
- **Deny by default, same as the default route.** Each route takes `allow_emails` (exact or
  `*@domain`), falling back to `default_route`'s list, then the profile's. All empty fails
  `km init`.
- **Transport is one canonical JSON SSM parameter** (`/{prefix}/slack/routes`, 4096 bytes,
  about 20 routes). Its hash lands in the bridge env, so a change cold-starts the Lambda.
  `km doctor` → **Slack channel routes** reports the profiles, channel resolution, and drift
  between the config and the live table.
- **Front-door only**, like `default_route`: a table on a peer install is ignored.

See `docs/slack-notifications.md` § Channel routes.

## ✅ Show a verdict as a reaction — `km-slack react`

A watcher that posts an advisory and later reaches a verdict can now say so with a reaction
instead of (or as well as) an edited status line:

```bash
km-slack react --ts "$TS" --emoji hourglass_flowing_sand \
  --exclusive hourglass_flowing_sand,white_check_mark,rotating_light
# …later…
km-slack react --ts "$TS" --emoji white_check_mark \
  --exclusive hourglass_flowing_sand,white_check_mark,rotating_light
```

- **`--exclusive` is a status set.** The bridge adds `--emoji`, then removes the bot's own
  reaction for every other name, so `pending → done` never leaves the hourglass behind and
  the caller never tracks the previous state. `--remove` takes one reaction off.
- **Idempotent.** Slack's `already_reacted` and `no_reaction` count as success, so a cron job
  retrying a react never fails on its own earlier success. Any other Slack code
  (`message_not_found`, `invalid_name`, `missing_scope`) comes back verbatim.
- **Only the bot's reaction moves.** `reactions.remove` acts on the calling token, so
  `--exclusive` can never take away a human's identical reaction.
- **One signed request, bounded fan-out.** The set is capped at 10 names and every name is
  re-validated at the bridge (`^[a-z0-9_+'-]{1,100}$`, colons optional), so a bypassed client
  cannot sign a wider request. Own channel only, same rule as `update`.

## 📣 Thread replies that the whole channel sees — `--broadcast`

`km-slack post --thread T --broadcast` and `km-slack reply --broadcast` set `reply_broadcast`,
so a reply lives in the advisory's thread and still shows in the channel. Until now a
channel-wide ping had to be a separate top-level post, because an `@here` inside a thread only
reaches the thread's followers. `post` requires `--thread` and refuses `--new-message`; `reply`
refuses when no thread resolves rather than silently posting top-level.

See `docs/slack-notifications.md` § Reactions and broadcast.

## ✏️ `km-slack update` renders like `post`

`update` gains `--render plain|mrkdwn|blocks|blocks-rich` and `--subject`, with `post`'s
precedence and its one-shot `invalid_blocks` retry. An edit can now reproduce a `blocks-rich`
post instead of hand-written mrkdwn, and a plain edit of a rich message drops the old blocks
(Slack does that on a text-only `chat.update`). Own channel only, as before; still no `--mention`.

## ✨ Small things

- `km doctor` gains the **Slack channel routes** group.
- The `klanker:slack` skill documents `react`, `--broadcast` and `update --render` (plugin `0.4.21`).
