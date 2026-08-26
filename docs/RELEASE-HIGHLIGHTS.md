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
-->
## ✨ Major additions highlighted

This release is about **letting the outside world wake a sandbox.** Until now every inbound path
was a conversation — a GitHub comment, a Slack mention, a HackerOne report. This one is a
machine: a security platform fires an alert at a webhook, and a sandbox agent picks it up and
starts working.

### 🔔 Generic webhook ingress bridge — Wiz first source

A third-party SaaS POSTs to a new km-owned Lambda behind a Function URL. The bridge
authenticates the request, drops replays, matches it against operator-declared rules, and
dispatches a prompt to a sandbox — **warm** via that sandbox's SQS FIFO queue, or **cold** by
creating one. The first source is Wiz: an Automation Rule fires on an Issue or a Threat
Detection, and an `ir-bot` sandbox runs `/triage` against the payload.

```yaml
webhooks:
  rate_limit:
    max_dispatches: 20
    window_seconds: 600
  sources:
    - name: wiz                    # ← the URL path segment: POST /wiz
      auth:
        type: bearer
        secret_path: /km/config/webhooks/wiz/token
      rules:
        - match:
            type: [issue, threat]
            severity: [CRITICAL, HIGH]
          alias: ir-bot
          profile: ir-bot
          on_absent: cold-create
          cooldown_seconds: 900
          group_by: "{{entity.cloud_id}}"
          prompt: "@file profiles/prompts/wiz.triage.prompt.txt"
```

**It is generic, not Wiz-shaped.** One Lambda, one queue, one poller, one profile field serve
N named sources; the source is resolved from the URL path, so adding a second one is a config
entry rather than another bridge. **No CLI verb, by design** — a webhook has a URL and a shared
secret, and neither needs a command.

**km publishes the payload contract instead of reverse-engineering one.** Wiz webhook bodies are
operator-authored Mustache templates, so this release ships the template you paste into your
tenant (`docs/webhook-templates/`) and the bridge parses what it asked for. That also buys a
real idempotency key — `{{issue.id}}:{{triggerType}}:{{issue.updatedAt}}` — which matters
because Wiz sends no signature and no delivery-id header, making that key the load-bearing
replay defence rather than a nicety.

**Four layers stand between an alert storm and an overloaded sandbox:** a replay nonce, a
per-group cooldown, an install-wide rate ceiling, and the Phase 121 action quota with
`onBreach: freeze`. The first three fail **open** — a transient DynamoDB fault must never strand
a real alert — while authentication and unparseable payloads fail **closed**. `group_by`
coalesces rather than batches: the first alert of a burst wins and the rest are suppressed for
the window.

**Dormant by default.** No `webhooks:` block means no environment variables, no doctor output,
no AWS calls, and byte-identical sandbox userdata.

Deploy order is load-bearing — `make build` **first** (the binary carries the new module
entries; a stale one skips them silently while `km doctor` stays green), then
`make build-lambdas`, then `km init --dry-run=false` (**not** `--sidecars`), then one
`km destroy && km create` on the target sandbox. Full runbook: `docs/webhook-ingress.md`.

> **Status:** code-complete and unit-tested end to end; **live UAT against a real Wiz tenant is
> still pending.** Four things are deliberately left to verify there: whether your tenant sends
> a delivery-id header, whether the generic webhook gzip-compresses bodies, the exact
> `entitySnapshot` leaf names in your variable picker, and whether `/triage` exists on the
> target box. None change the architecture.

### 🔁 AZ sweep fixes that were quietly costing launches

Three capacity and teardown fixes also land here, all found the hard way:

- **A fresh ICE now outranks a stale success.** `rankScore` checked `LastSuccessAt` before the
  fresh-ICE branch and returned unconditionally — and success rows carry no TTL, so an AZ that
  ever succeeded out-ranked every other AZ *forever*, however often it later hit
  `InsufficientInstanceCapacity`. `km capacity` disagreed with the ranker while the launch path
  kept picking the dead AZ.
- **The on-demand launch waiter is bounded.** Terraform retried ICE internally, so km never saw
  the error, the sweep could not rotate, and the Lambda died at 900s leaving a stale lock.
- **`km doctor` sees untagged linked-account orphans and leaked volumes** — a create killed
  mid-apply leaks its 300GB volume, and `km destroy` reported success because the volume never
  reached state.
