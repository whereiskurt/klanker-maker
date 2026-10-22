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

  Curated by hand for v0.9.2 from PRs #154 (Slack channel routes), #155
  (km-slack react + --broadcast) and #157 (update --render/--subject). No phase blocks since v0.9.0, so
  scripts/draft-release-highlights.sh had nothing to draft.
-->

## ⚠️ Upgrade notes — read before deploying

This release changes the Slack bridge and the `km-slack` sidecar. No IAM, no schema, no
Slack App change.

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
