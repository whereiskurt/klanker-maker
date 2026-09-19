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

  Drafted from CLAUDE.md phase blocks since v0.8.21 via
  scripts/draft-release-highlights.sh, then curated.
-->

## 👋 A sandbox reply can @-mention people — and it actually pings them

Ask a sandbox over Slack to "@ me back" and it used to post the literal text `<@U0ABC…>`
(or `@Kurt`), visible to everyone and notifying no one. Three km-side causes, all fixed:
the inbound poller dropped the sender's Slack id (so "me" was unknowable), the renderer
HTML-escaped `<@U…>` into `&lt;@U…&gt;`, and nothing told the agent the token form. Now
every Slack turn starts with `[Slack] From: <@U…>` (also `$KM_SLACK_SENDER_ID`), the
renderer preserves exactly `<@U…>`/`<#C…>`/`<!here>`/`<!channel>`, and
`km-slack post|reply --mention U…[,U…|here|channel]` is the explicit form. Verified live:
Slack's markdown block resolves inline mentions natively. Names are rejected by design —
the bot never enumerates the workspace directory; the ids come from the sender and from
whatever they @-typed. Plugin `0.4.16` carries the skill guidance.

## 🪦 H1 inbound was non-functional since Phase 103: the DLQ nothing created

`pkg/aws.H1InboundDLQName` named `{prefix}-h1-inbound-dlq.fifo`; no module ever provisioned
it, and SQS validates a RedrivePolicy target at `CreateQueue` — not at redrive — so the first
`km create` of any `notification.h1.inbound.enabled` profile failed *after* the EC2 apply,
leaving a running instance on a `failed` row. `sqs-inbound-dlq/v1.2.0` creates it; a
name-agnostic guard now pairs every `*InboundDLQName` helper with a resource in the pinned
module, and `km doctor`'s DLQ-depth check probes the webhook and h1 DLQs too.

## 🔐 `km h1 init` no longer fails its own first run, or echo the API token

With no `--bridge-url` (the documented first run, before the Lambda exists) init wrote an
empty SSM Value, which SSM rejects — after the other three params had landed, so the minted
webhook secret was never shown and the retry needed `--force`. `km github init` had the
identical defect. Both skip the write. The `HackerOne API token:` prompt reads without echo.

**Deploy:** `make build` → `make build-lambdas` → `km init --dry-run=false` — unsplittable
(poller userdata in the create-handler zip, `km-slack` in sidecars, the DLQ needs a
terraform apply). Existing sandboxes gain the renderer fix and `--mention` on a sidecar
refresh; the `[Slack] From:` preamble needs `km destroy && km create`.
