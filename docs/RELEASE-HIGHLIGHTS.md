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

  Curated by hand for v0.9.0 from PRs #146–#153 (no phase blocks since
  v0.8.24, so scripts/draft-release-highlights.sh had nothing to draft).
-->

## ⚠️ Upgrade notes — read before deploying

This release changes the sandbox IAM role twice (`ec2spot` v1.8.0 → **v1.9.0**), the
Slack bridge, the pollers, and the Slack App's scopes.

- **Deploy order:** `make build` → `make build-lambdas` → `km init --dry-run=false`.
  `--sidecars` alone is not enough.
- **Slack App:** run `km slack manifest`, reinstall the App, then
  `km slack rotate-token --bot-token <new>`. The new scopes are `im:history`,
  `mpim:history`, `im:write`, and the `message.im`/`message.mpim` events.
- **Existing sandboxes** keep their old role and poller until `km destroy && km create`.
  `km doctor` → **Sandbox role shape** lists the boxes still on the old role. Recreate a
  canary first.
- **Behaviour change:** operator `aws` calls inside `initCommands` (SSM, S3, DynamoDB
  outside the sandbox's own scope) now fail under `set -e`. `km validate` warns on them.
  Use `iam.allowedSecretPaths` for SSM.

## 🔒 The sandbox role is least-privilege

Before this release, any sandbox could read every SSM parameter in the account and write
every DynamoDB table in its region. Two grants caused it:
- the region lock was an **`Allow *` conditioned on region**
- the managed `AmazonSSMManagedInstanceCore` policy granted `ssm:GetParameter*` on `*`

**`ec2spot` v1.8.0** replaces both with a region **Deny** and one explicit grant per concern.
Every data-bearing grant (S3, DynamoDB, SSM, SQS, logs, SES) is scoped to the sandbox id or
the install prefix, and KMS decrypt is pinned to the parameter it serves.

**v1.9.0** removes the sandbox role's last access to the Slack, GitHub and HackerOne threads
tables. Pollers now keep agent-session state in `/var/lib/km/sessions/`. They report it
through a new signed `km-slack session` action, and the bridge writes the row only if it
names the sandbox making the call. That closes the cross-thread prompt-injection residual
that v1.8.0 left open.

## 💬 Talk to the bot from any channel or DM

- **`slack.default_route` routes conversations no install owns.** Invite the bot to a channel
  or DM it, @-mention it, and the reply lands in a thread right there. It goes to one alias:
  warm enqueue, resume, or cold-create.
- **Deny by default.** Senders are gated by email allowlist. Entries are exact addresses or
  `*@domain`, and a profile's `allowEmails` replaces the install list for that sandbox.
- **Strict threads (`threadBypass: false`).** Every turn needs an @-mention, and the agent
  receives the thread's human messages since its last reply.
- **One reply per turn.** Turns used to post twice: once when the agent replied with
  `km-slack`, once when the poller posted its closing line.
- **`km-slack dm --to U…`** lets an agent DM someone on the allowlist. Replies route back to
  the sandbox.
- **Quiet 👀.** Thread replies react only after 180 s of quiet (on by default; `0` disables).
  Top-level messages always react.

See `docs/slack-notifications.md` § Default route.

## 🧑‍💻 `km t3` — T3 Code on a sandbox, in the background

`km t3 start <sb>` installs [T3 Code](https://github.com/pingdotgg/t3code) and runs it as a
background service on the box's `127.0.0.1`. `km t3 <sb>` connects your browser or the t3
desktop app through an auto-reconnecting SSM tunnel and prints a single-use pairing link.
`stop` and `status` complete the set.

The idle timer keeps the box awake while a browser is connected or an agent turn is running.
A started server nobody is using still idles out: t3's long-lived agent processes no longer
hold the box up by name. The verbs work on running sandboxes with just `make build`. The idle
signal ships with the `km-presence` sidecar and reaches existing boxes on recreate.
See `docs/t3code.md`.

## ⏱️ A cold-created sandbox answers its first message on time

The inbound pollers started before `initCommands` had installed claude or codex. The first
dispatched turn exited 127, and the message sat invisible for the queue's 30-minute
timeout. Pollers now wait for the boot's `init-done` marker, and an exit-127 turn retries in
60 s.

## ✨ Small things

- `km doctor` gains a **Slack default route** group, plus **Sandbox role shape**, which also
  inspects stopped and paused boxes.
- GitHub and HackerOne thread rows with no owner are reclaimed by the next bridge write, and
  `km doctor` counts them.
- `km-send` and `km-recv` are on `/usr/local/bin` like every other helper.
- Resource names derived from `KM_RESOURCE_PREFIX` fail loudly when it is unset, instead of
  silently targeting `km-*`.
- New doc: a code walkthrough of the bridge Lambdas (`docs/lambda-bridge-internals.md`).
