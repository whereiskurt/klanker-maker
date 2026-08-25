# Generic webhook ingress bridge (`km-webhook-bridge`), Wiz first source

**Date:** 2026-08-25
**Status:** Design approved, not yet planned
**Branch:** `feat/wiz-webhook-bridge`
**Target release:** v0.8.2

## Summary

A third-party SaaS POSTs a payload to a km-owned Lambda. The bridge authenticates
it, drops replays, matches it against operator-declared rules, and dispatches a
prompt to a named sandbox alias — warm via an SQS FIFO queue, cold via an
EventBridge `SandboxCreate`. The sandbox agent runs the turn.

The first source is **Wiz**: a Wiz Automation Rule fires on an Issue or Threat
and the `ir-bot` sandbox runs `/triage` against the payload.

The facility is generic — a `webhooks:` block in `km-config.yaml` declares N named
sources over one Lambda, one queue, one poller, and one profile field. Source #2
costs a config entry, not an infrastructure phase.

**Dormant by default.** Absent `webhooks:` ⇒ env unset ⇒ the bridge drops
everything and `km doctor` skips the group silently.

**No CLI verb.** Configuration is `km-config.yaml` only, by explicit decision.
This is the opposite of `km slack` / `km github` / `km h1` and is deliberate:
those exist because of App manifests, OAuth, and token rotation. A webhook has a
URL and a shared secret, and neither needs a verb.

## Why this shape

The repo already has a **pull** ingress to exactly this destination:
`checks.triggers` (Phase 116) polls an API on a schedule and dispatches
`alias` + `prompt` + `profile` + `on_absent` + `cooldown_seconds`. There is even
an existing `profiles/checks/wiz-intel/` check that polls Wiz.

This phase builds the **push** counterpart. Same destination, same vocabulary,
different ingress. The config keys are lifted verbatim from `checks.triggers` so
operators learn one dialect, not two.

## The load-bearing discovery: Wiz payloads are ours to define

Wiz webhook payloads are **not a fixed Wiz schema**. The operator configures an
Automation Rule (Policies → Automation Rules → New Rule), sets a trigger and
filters, and authors the **request body** as a Mustache template using variables
Wiz exposes:

```
{{issue.id}} {{issue.severity}} {{issue.status}} {{issue.createdAt}} {{issue.updatedAt}}
{{issue.entitySnapshot.type}} {{issue.entitySnapshot.cloudPlatform}}
{{issue.enrichedMainDetection.rule.name}} {{issue.enrichedThreatActors}}
{{triggerSource}} {{triggerType}} {{ruleId}} {{ruleName}} {{changedFields}} {{wizDomain}}
```

Block sections `{{#array}}…{{/array}}` iterate; inverted sections
`{{^field}}…{{/field}}` supply fallbacks.

Three design consequences follow, and all three simplify the work:

1. **No schema reverse-engineering.** We publish a canonical template; the
   operator pastes it into Wiz; Wiz POSTs our shape. A wrong leaf variable is
   fixed in the Wiz UI (which lists available variables), not in a Go patch.
2. **A semantically correct idempotency key.** Wiz sends no delivery-id header,
   so instead of hashing the body we ask Wiz to fill
   `"delivery_key": "{{issue.id}}:{{triggerType}}:{{issue.updatedAt}}"`.
   Same issue, same update ⇒ same key, independent of formatting.
3. **Filtering moves left.** Wiz Automation Rules filter by severity, trigger
   type, project, and cloud platform before sending. Our `match:` demotes to a
   second layer for multi-rule routing and defence in depth.

### Confirmed Wiz behaviour

| Property | Finding |
|---|---|
| Setup path | Settings → Integrations → Add Integration → SIEM & Automation Tools → Webhook |
| Auth options | None, Basic (user/pass), **Token (Bearer)**, plus arbitrary custom headers ("Add Header") |
| Signature / HMAC | **None offered** |
| Delivery-id header | **None documented** across several third-party integration guides |
| Client certificate | Offered by Wiz — **unusable for us**: a Lambda Function URL cannot terminate mTLS (would need an ALB or API Gateway in front) |
| Payload | Operator-authored Mustache template; default format "JSON: Generic" |
| Compression | One source (Cribl) describes gzip-compressed bodies; unconfirmed for the generic webhook |

Because Wiz offers no signature, the bearer token is the entire auth boundary and
the template-injected `delivery_key` is the load-bearing replay defence.

**Honest limitation:** a static bearer token is replayable. The nonce closes
retries and accidental double-sends within its TTL; it cannot stop a deliberate
replay after the TTL lapses. Only a signed timestamp would, and Wiz does not
offer one.

## Architecture

One Lambda behind a Function URL. Source is resolved from the **URL path**, so a
single endpoint serves every source and adding one never touches Terraform.

```
Wiz ──POST /wiz──▶ km-webhook-bridge  (Function URL, auth_type NONE at AWS layer)
                     │
                     ├ 1. resolve source from path segment      → config entry
                     ├ 2. decompress if Content-Encoding: gzip
                     ├ 3. auth: bearer (constant-time) | hmac    → SSM SecureString
                     ├ 4. parse envelope (km_schema v1 | path fallback | raw)
                     ├ 5. replay nonce on delivery_key
                     ├ 6. first-match rule on normalized fields
                     ├ 7. cooldown (group_by) → rate ceiling → action quota
                     └ 8. resolve alias "ir-bot" via alias-index GSI
                          ├ running  → SQS enqueue          ─┐
                          ├ stopped  → resume + enqueue       │
                          └ absent   → EventBridge SandboxCreate(--prompt)
                                                              │
   ir-bot:  km-webhook-inbound-poller ◀── {prefix}-{id}-webhook-inbound.fifo
              └ "[Webhook Trigger] wiz / issue" + prompt → agent turn → /triage
                 └ optional: post result to Slack via km-slack
```

### Reused, not rebuilt

- Nonces table + `CheckAndStore` (`pkg/github/bridge/aws_adapters.go:200`) — a
  conditional `PutItem` with TTL, already shared by all three bridges.
- `alias-index` GSI on `{prefix}-sandboxes` for alias → sandbox resolution.
- Shared inbound DLQ pattern (`infra/modules/sqs-inbound-dlq`, Phase 99.1).
- EventBridge `SandboxCreate` cold path.
- `WireActionQuota` (Phase 121) — the circuit-breaker with `onBreach: freeze`.
- Phase 109 self-heal: a terminal `ErrNoResumableInstance` deletes the stale
  DynamoDB row and falls through to cold-create, so an ir-bot terminated out
  from under km does not shadow creation forever.

### Deliberately NOT built: a threads table

GitHub and H1 each carry one because they reply into a conversation. Wiz is
one-way — **confirmed by the operator, 2026-08-25** — so there is nothing to
reply to and no continuity to persist. This drops a DynamoDB table, its
Terraform module, and a `regionalModules()` entry. The agent reports out via
`km-slack` instead.

## Config surface

```yaml
webhooks:
  rate_limit:                      # install-wide storm breaker
    max_dispatches: 20
    window_seconds: 600
  sources:
    - name: wiz                    # ← URL path segment: POST /wiz
      auth:
        type: bearer               # bearer | hmac
        header: Authorization
        secret_path: /km/config/webhooks/wiz/token
      replay_ttl_seconds: 900
      field_paths:                 # fallback only, when km_schema absent
        id: "$.id"                 # doubles as the replay key when set
        type: "$.objectType"
        severity: "$.severity"
        group: "$.entity.cloud_id"
      rules:
        - match:
            type: [issue, threat]
            severity: [CRITICAL, HIGH]
          alias: ir-bot
          profile: ir-bot
          on_absent: cold-create   # cold-create | skip
          cooldown_seconds: 900
          group_by: "{{entity.cloud_id}}"
          prompt: "@file profiles/prompts/wiz.triage.prompt.txt"
```

`alias` / `profile` / `on_absent` / `cooldown_seconds` / `prompt` match
`checks.triggers` exactly, including `@file` prompt loading.

### Config plumbing requirements

Three known-silent failure modes, all mandatory:

1. **`webhooks` MUST be added to the v2→v merge-list** in `config.Load()`
   (`internal/app/config/config.go:973`). It is a new top-level key and does not
   piggyback on any parent entry. Omitting it silently discards the whole block.
   Add a regression-guard test, as `slack.allow` has.
2. **Every struct field needs a `mapstructure` tag.** Viper's `UnmarshalKey`
   silently ignores untagged fields.
3. **Keys stay snake_case.** Viper lowercases config keys at load time.

The block is exported to the Lambda as `KM_WEBHOOK_SOURCES` (JSON) plus
`KM_WEBHOOK_RATE_LIMIT`, mirroring `KM_H1_PROGRAMS`.

## Payload contract

### Canonical km envelope (v1)

Shipped as `docs/webhook-templates/wiz.issue.v1.json` for pasting into the Wiz
Automation Rule request body:

```json
{
  "km_schema": "v1",
  "source": "wiz",
  "delivery_key": "{{issue.id}}:{{triggerType}}:{{issue.updatedAt}}",
  "type": "issue",
  "id": "{{issue.id}}",
  "severity": "{{issue.severity}}",
  "status": "{{issue.status}}",
  "title": "{{issue.control.name}}",
  "trigger": { "type": "{{triggerType}}", "rule": "{{ruleName}}" },
  "entity": {
    "type": "{{issue.entitySnapshot.type}}",
    "name": "{{issue.entitySnapshot.name}}",
    "cloud_platform": "{{issue.entitySnapshot.cloudPlatform}}",
    "cloud_id": "{{issue.entitySnapshot.externalId}}"
  },
  "url": "https://{{wizDomain}}/issues#~(issue~'{{issue.id}})"
}
```

A sibling `wiz.threat.v1.json` sets `"type": "threat"` and sources its title from
`{{issue.enrichedMainDetection.rule.name}}`, adding actors and detection ids
(variable names confirmed against PagerDuty's published reference payloads).

`entitySnapshot.name` and `entitySnapshot.externalId` are conventional and **not
confirmed by a primary source**. This is low-risk precisely because the template
is operator-authored: the Wiz UI lists the real variables, and a correction is a
paste, not a release.

### Parsing precedence

```
if body.km_schema == "v1":   typed fields drive match / group_by / delivery_key
elif source.field_paths:     declared JSON paths drive them; field_paths.id
                             doubles as the replay key
else:                        no routing fields — only rules with an empty
                             `match` block can fire

replay key precedence:  delivery_key → field_paths.id → sha256(raw body)
always:                 the full raw body reaches the prompt as {{raw}}
unparseable JSON:       200 + log webhook_unparseable, no dispatch
```

A rule whose `match` names a field the envelope does not carry **never fires** —
it is treated as non-matching, not as a wildcard. This fails closed on purpose:
a typo'd path silently dispatching every payload to a sandbox is worse than one
that dispatches nothing and shows up in `km doctor`.

**Never return 4xx.** A non-200 makes the sender redeliver — for GitHub and H1
with a fresh delivery id that walks straight past dedup. Every path returns 200,
including internal errors. This is the H1 Pitfall-2 rule and it is not optional.

### Template-syntax collision (documentation hazard, not a bug)

Wiz templates use `{{…}}` and so do km `prompt:` templates. They render at
different times in different systems — Wiz renders its variables before sending;
km expands `{{severity}}`, `{{entity.name}}`, `{{raw}}` against the already-rendered
envelope. There is no collision, but the docs must say so explicitly.

## Storm control

Four layers, cheapest first. The FIFO queue already removes the concurrency
hazard — turns queue rather than trample each other — so these govern **backlog
and spend**, not correctness.

| Layer | Key | On error |
|---|---|---|
| Replay nonce | `wh:{source}:{delivery_key}`, TTL `replay_ttl_seconds` | closed (drop) |
| Cooldown | `wh-cd:{source}:{rule_idx}:{group_by}`, TTL `cooldown_seconds` | **open** |
| Rate ceiling | `wh-rate:{floor(now/window)}` atomic counter | **open** |
| Action quota | Phase 121 `WireActionQuota`, `onBreach: freeze` | closed |

**`group_by` is the coalescing answer.** Fifty findings on one host with
`group_by: "{{entity.cloud_id}}"` ⇒ the first dispatches and forty-nine are
suppressed for the window. This is dedup-by-grouping, not true batching — one
config field instead of a buffer and a tumbling window. True batching is
explicitly out of scope.

**The rate ceiling is the only new mechanism.** An atomic
`UpdateExpression: ADD n :one` with `ReturnValues: UPDATED_NEW` on a
time-bucketed nonce key, TTL'd to the bucket. Over the cap ⇒ drop and log
`webhook_rate_ceiling_tripped`.

The ceiling is **install-wide across all sources**, not per-source: it exists to
protect one shared fleet of sandboxes and one shared AI budget, so a Wiz storm
and a future PagerDuty storm draw down the same allowance. A per-source ceiling
is a straightforward later addition (`sources[].rate_limit` overriding the
install default) and is not built now.

Counting happens **after** the replay and cooldown gates, so suppressed
duplicates do not consume ceiling budget — only real dispatches do.

Cooldown and ceiling **fail open**, matching existing bridge semantics: never
strand a real alert on a transient DynamoDB blip. The action quota is what
actually stops runaway spend, and it is a circuit-breaker, not a throttle —
generous caps with `onBreach: freeze`.

Poison envelopes evict to `{prefix}-webhook-inbound-dlq.fifo` at
`maxReceiveCount=3`, avoiding the Phase 99.1 head-of-line wedge.

**`MessageGroupId` = the sandbox id**, i.e. fully serial. Phase 119 grouped by
thread to gain parallelism, but its own `/workspace` shared-mutation caveat is
exactly the IR case: two triage turns racing in one workspace is a bug, not
throughput. A `maxConcurrentThreads` analog can follow if volume demands it.

## Sandbox side

### Profile field

```yaml
spec:
  notification:
    webhook:
      inbound:
        enabled: true
```

`NotificationWebhookInboundSpec`, tri-state `*bool`, nil ⇒ false — a direct copy
of `NotificationGitHubInboundSpec` (`pkg/profile/types.go:211`). Provisions the
FIFO, writes SSM `/{prefix}/sandbox/{id}/webhook-inbound-queue-url`, and sets a
`webhook_inbound_queue_url` DynamoDB attribute. Lifecycle helpers clone
`create_github_inbound.go` / `destroy_github_inbound.go`.

### Two traps this walks directly into

1. **`SandboxMetadata` round-trip.** The new DynamoDB attribute must round-trip
   through the struct + marshal + unmarshal chokepoint, or `km pause` / `resume`
   / `extend` silently strip it and the poller loses its queue URL after the
   first resume.
2. **Poller emission is gated on `Spec.CLI != nil`.** A profile with
   `webhook.inbound.enabled` but no `cli:` block provisions a queue that nothing
   drains — messages accumulate and everything looks green.

### Poller

`km-webhook-inbound-poller` in `pkg/compiler/userdata.go`, modelled on
`km-github-inbound-poller`. Reads `KM_WEBHOOK_INBOUND_QUEUE_URL`, falling back to
the SSM path with retry/backoff. Renders a `[Webhook Trigger] wiz / issue`
preamble plus the expanded prompt, runs one agent turn, acks after completion.
No reply-back leg. When `KM_SLACK_*` is present, posts the result to the
sandbox's channel.

### Prompt

`profiles/prompts/wiz.triage.prompt.txt`:

```
A Wiz {{type}} fired: {{title}}
Severity: {{severity}}   Status: {{status}}
Entity: {{entity.name}} ({{entity.type}}, {{entity.cloud_platform}})
{{url}}

Run the /triage skill with this payload in mind.

Raw payload:
{{raw}}
```

`/triage` is assumed to already exist on the ir-bot box. This phase stops at the
sandbox boundary; if the skill is absent the first live turn will say so plainly.
Authoring a `klanker:triage` skill is explicitly out of scope.

## Secret delivery without a CLI verb

`km init` mints a 32-byte random token into the SSM SecureString when
`auth.secret_path` names a parameter that does not exist, and prints it **once**
for pasting into the Wiz integration's Authorization header. Idempotent: an
existing parameter is never overwritten, so re-running `km init` does not rotate
the token out from under a live Wiz integration. Rotation is a deliberate
`aws ssm put-parameter --overwrite` plus a Wiz-side paste, documented in the
runbook.

## Deploy surface

Order is load-bearing.

1. **`make build` FIRST** — the binary carries the new `regionalModules()`
   entries. A stale binary silently skips them, leaving `km doctor` green while
   nothing was provisioned.
2. `make build-lambdas` — the new bridge zip, **and** the create-handler, which
   must be refreshed for the new profile schema field and the new poller userdata.
3. `km init --dry-run=false` — **not `--sidecars`**. The Lambda, Function URL,
   IAM, and env block need a full terragrunt apply.
4. Existing `ir-bot` needs **one** `km destroy && km create` to gain the queue and
   poller.

Add a `--webhooks` sugar alias to the Phase 105 scoped-init set
(`internal/app/cmd/init.go:133`) for env+IAM-only refreshes.

### Registration points that fail silently if missed

| Location | What breaks |
|---|---|
| `regionalModules()` (`init.go:525`) | module never applied; doctor green, nothing exists |
| `infra/live/*/lambda-webhook-bridge/terragrunt.hcl` | module defined but never deployed |
| `buildLambdaZips` list (`init.go:3165`) | zip never built; stale or missing code |
| `TestRunInitPlan_ModuleOrder` count | test fails on the new module count — must be bumped |
| destroy-class gate list (`init.go:506`) | new module skips the safety gate |

The Terraform module declares **no `required_providers`** (they live in
`root.hcl`), and its `terragrunt.hcl` needs `"show"` in
`mock_outputs_allowed_terraform_commands`.

New modules: `infra/modules/lambda-webhook-bridge/v1.0.0` (modelled on
`lambda-h1-bridge/v1.0.0`, minus the threads table and the API back-channel) and
a `webhook-inbound` variant of the shared inbound DLQ.

## `km doctor`

A new group, silently skipped with zero AWS calls when `webhooks:` is absent:

- Each source is structurally valid (known `auth.type`, non-empty `name`, at
  least one rule, `name` is URL-path-safe).
- Each `auth.secret_path` resolves to an existing SSM parameter.
- The Function URL is recorded in SSM and reachable.
- Each rule's `profile` file exists on disk and each `alias` either resolves or
  has `on_absent: cold-create`.
- DLQ depth — the early warning on a poison wedge.
- `rate_limit` is sane (`max_dispatches > 0`, `window_seconds > 0`).

## Testing

Unit coverage leans on negative paths, because that is where fail-open versus
fail-closed actually gets pinned:

- Bearer constant-time compare; wrong token, missing header, empty configured
  secret (must fail closed, never match-empty).
- HMAC verifier with timestamp skew, for the pluggable path.
- Envelope parsing: `km_schema v1`, `field_paths` fallback, unparseable JSON
  (⇒ 200 + log, no dispatch), gzip-encoded body.
- Rule first-match ordering; `match` on `type` and `severity`; non-matching drop.
- `group_by` expansion including a missing field (must not produce a key that
  collides across distinct entities).
- Replay nonce hit and miss; cooldown hit and miss; rate ceiling at, below, and
  above the cap; **nonce-store error ⇒ dispatch proceeds** (fail-open pinned).
- Dispatch branches: running ⇒ enqueue; stopped ⇒ resume + enqueue; absent ⇒
  cold-create; `on_absent: skip` ⇒ no dispatch; terminal resume failure ⇒ row
  deleted then cold-create (Phase 109 self-heal).
- Golden: poller emitted when `webhook.inbound.enabled`, absent when not.
- Config: `webhooks` survives `Load()` (the merge-list regression guard).
- `SandboxMetadata` round-trip of `webhook_inbound_queue_url`.

**Live UAT** drives a synthetic bearer-authenticated POST at the Function URL —
the technique from the Slack bridge UAT — then follows queue → poller → agent
turn. Prior phases are unambiguous that live UAT finds classes of bug the unit
suite cannot; Phase 126 found twelve.

## Open items

Verify against the real Wiz tenant during UAT, not before:

1. Whether Wiz sends any per-delivery id header. If one exists, prefer it over
   the template-injected `delivery_key`. Designed so this is a one-line change.
2. Whether the generic webhook gzip-compresses bodies. Handled transparently
   either way; the test exists regardless.
3. The exact `entitySnapshot` leaf names in your tenant's variable picker.
   A template paste, not a code change.
4. Whether `/triage` is present on ir-bot.

## Explicitly out of scope

- True batching / tumbling windows (`group_by` covers the practical case).
- A threads table or any reply-back channel to Wiz.
- mTLS / client-certificate auth. Impossible on a Lambda Function URL, and
  **confirmed never required** by the operator (2026-08-25) — so the Function URL
  stays the endpoint and no ALB/API Gateway fronting is contemplated.
- Vulnerability-finding normalization (falls through the `field_paths` escape
  hatch).
- Authoring a `klanker:triage` skill.
- Any `km webhook` CLI verb.

## Sources

- [PagerDuty — Wiz automation rule reference payloads](https://github.com/PagerDuty/wiz-pagerduty-automation-rules/blob/main/README.md)
- [RunReveal — Wiz Threats source](https://docs.runreveal.com/sources/source-types/wiz/threats)
- [Cribl — Getting started with the Wiz Webhook Source](https://cribl.io/blog/getting-started-with-the-wiz-webhook-source-in-cribl-stream/)
- [Scanner — Wiz data ingestion](https://docs.scanner.dev/scanner/using-scanner-complete-feature-reference/data-ingestion/sources/wiz)
- [Elastic — Wiz integration](https://www.elastic.co/docs/reference/integrations/wiz)
