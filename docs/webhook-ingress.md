# Webhook Ingress Guide

> **Phase 127 (2026-08-25) — Generic webhook ingress bridge, Wiz first source (complete):**
> A third-party SaaS POSTs a payload to a km-owned Lambda (`km-webhook-bridge`),
> which authenticates it, drops replays, matches operator-declared rules, and
> dispatches a prompt to a named sandbox alias — warm via a per-sandbox SQS
> FIFO queue, cold via EventBridge `SandboxCreate`. First source: **Wiz** →
> the `ir-bot` sandbox runs `/triage` against the Issue or Threat payload.
> Dormant by default (absent `webhooks:` block ⇒ byte-identical to
> pre-Phase-127). **No CLI verb** — configuration is `km-config.yaml` only.

This is the **push** counterpart to the Phase 116 **pull** ingress
(`checks.triggers`, which polls an API on a schedule). Same destination vocabulary
(`alias` / `profile` / `on_absent` / `cooldown_seconds` / `prompt`, including
`@file` prompt loading) — different direction. If you already run a
`checks.triggers` entry against Wiz (see `profiles/checks/wiz-intel/`), this is
not a replacement for it; it is the same idea run in reverse, and the two can
coexist.

## Table of Contents

1. [What it is](#1-what-it-is)
2. [Wiz setup](#2-wiz-setup)
3. [Automation Rule](#3-automation-rule)
4. [km-config.yaml](#4-km-configyaml)
5. [Storm control](#5-storm-control)
6. [The `{{...}}` non-collision](#6-the--non-collision)
7. [Deploy surface](#7-deploy-surface)
8. [Token rotation](#8-token-rotation)
9. [Troubleshooting](#9-troubleshooting)
10. [Limits](#10-limits)

---

## 1. What it is

`km-webhook-bridge` is a single Lambda behind a Function URL. The source is
resolved from the **URL path** (`POST /wiz` → the `wiz` entry in
`webhooks.sources`), so one endpoint serves every source you configure, and
adding a second source is a config entry, not a new Lambda.

```
Wiz ──POST /wiz──▶ km-webhook-bridge
                     │
                     ├ auth (bearer, constant-time)  → SSM SecureString
                     ├ parse envelope (km_schema v1 | field_paths | raw)
                     ├ replay nonce on delivery_key
                     ├ first-match rule
                     ├ cooldown (group_by) → rate ceiling → action quota
                     └ resolve alias "ir-bot"
                          ├ running  → SQS enqueue
                          ├ stopped  → resume + enqueue
                          └ absent   → cold-create (unless on_absent: skip)

   ir-bot:  km-webhook-inbound-poller ◀── {prefix}-ir-bot-webhook-inbound.fifo
              └ "[Webhook Trigger] wiz / issue" + prompt → one agent turn
```

There is **no reply-back leg and no `km-webhook` sandbox-side helper binary** —
unlike GitHub/Slack/H1, a webhook source is one-way by design (confirmed with
the operator, 2026-08-25): there is nothing to reply to and no thread to
persist. If the sandbox needs to report out, have it post to Slack via
`km-slack` (see the `klanker:slack` skill) from inside the `/triage` prompt.

## 2. Wiz setup

In your Wiz tenant: **Settings → Integrations → Add Integration → SIEM &
Automation Tools → Webhook.**

- **URL:** your bridge's Function URL plus the source path segment —
  `https://<function-url>.lambda-url.<region>.on.aws/wiz`. Get the Function URL
  with `aws ssm get-parameter --name /<prefix>/config/webhooks/bridge-url`
  (see [§7](#7-deploy-surface) for how it gets there).
- **Authentication:** **Token (Bearer)**. Paste the value `km init` printed the
  first time it minted this source's secret (see [§8](#8-token-rotation)).
  Wiz also offers **None**, **Basic**, and a **client certificate** — do not
  use them. A Lambda Function URL cannot terminate mTLS (it would need an ALB
  or API Gateway in front, which this feature does not provision), and the
  operator has confirmed mTLS is never required for this integration.
- **Payload format:** JSON: Generic.
- **Request body:** paste the contents of
  `docs/webhook-templates/wiz.issue.v1.json` (or `wiz.threat.v1.json` for a
  Threats rule — Wiz Issues and Threats are configured as separate
  Automation Rules, each with its own request body). These are **Mustache
  templates you paste verbatim** — km does not reverse-engineer a Wiz schema;
  it publishes the contract and Wiz renders it. If a leaf variable doesn't
  match your tenant (e.g. `entitySnapshot.name`), the Wiz UI's variable picker
  shows the real name — fix it there, not in a Go patch.

The `delivery_key` field in both templates —
`"{{issue.id}}:{{triggerType}}:{{issue.updatedAt}}"` — is the deliberate
replacement for a delivery-id header Wiz does not send. Same issue, same
update ⇒ same key, independent of any formatting differences between
deliveries. See [§10](#10-limits) for what this can and cannot protect against.

## 3. Automation Rule

**Policies → Automation Rules → New Rule.** Set the **trigger** (Issue Created
/ Updated, or Threat Detected) and the **filters** — severity, project, cloud
platform, whatever your rule editor exposes — **here, on the Wiz side**.
Filtering in the Automation Rule means the request is **never sent at all**;
km's own `match:` (§4) is a second layer for multi-rule routing and defence in
depth, not a substitute for filtering upstream. A noisy, unfiltered Automation
Rule forwarding everything to km and relying on `match:` to sort it out works,
but every payload still costs a Lambda invocation and a replay-nonce write.

Attach the pasted body from §2 as this rule's webhook action payload.

## 4. km-config.yaml

```yaml
webhooks:
  rate_limit:                      # install-wide storm breaker, shared by every source
    max_dispatches: 20
    window_seconds: 600
  sources:
    - name: wiz                    # URL path segment: POST /wiz
      auth:
        type: bearer                 # bearer | hmac (hmac exists for a future
                                      # signed source — Wiz offers no signature,
                                      # so Wiz always uses bearer)
        header: Authorization
        secret_path: /km/config/webhooks/wiz/token
      replay_ttl_seconds: 900       # 0 => default 900s
      field_paths:                  # FALLBACK ONLY — ignored when the payload
                                     # carries km_schema: v1, which both shipped
                                     # templates do. Only needed for a source
                                     # whose payload you cannot template.
        id: "$.id"
        type: "$.objectType"
        severity: "$.severity"
        group: "$.entity.cloud_id"
      rules:
        - match:
            type: [issue, threat]
            severity: [CRITICAL, HIGH]
          alias: ir-bot
          profile: ir-bot
          on_absent: cold-create    # cold-create | skip
          cooldown_seconds: 900
          group_by: "{{entity.cloud_id}}"
          prompt: "@file profiles/prompts/wiz.triage.prompt.txt"
```

Key-by-key:

| Key | Meaning |
|---|---|
| `webhooks.rate_limit.max_dispatches` / `window_seconds` | Install-wide dispatch ceiling across **every** configured source — see [§5](#5-storm-control). |
| `sources[].name` | The URL path segment. Must be URL-path-safe; `km doctor` checks this. |
| `sources[].auth.type` | `bearer` or `hmac`. Wiz has no signature option, so use `bearer`. |
| `sources[].auth.header` | Header carrying the credential. Defaults to `Authorization`. |
| `sources[].auth.secret_path` | SSM SecureString holding the token/HMAC key. `km init` mints it once (§8) if the parameter doesn't exist yet. |
| `sources[].replay_ttl_seconds` | Replay-nonce window keyed on `delivery_key`. 0 defaults to 900s. |
| `sources[].field_paths` | Escape hatch for a source that doesn't carry `km_schema: v1`. Ignored entirely once `km_schema` is present — both Wiz templates set it, so this is dead weight for Wiz and exists for a future untemplatable source. `field_paths.id` doubles as the replay key when set. |
| `rules[].match` | Field → allowed-values map, evaluated AND across fields, OR within a field's list. A field the envelope doesn't carry is a **non-match**, never a wildcard — a typo'd path dispatches nothing, not everything, and shows up as `webhook_no_rule_match` in the logs rather than an unwanted agent turn. |
| `rules[].alias` / `profile` | Same semantics as `checks.triggers`: target sandbox alias, and the profile used for cold-create. |
| `rules[].on_absent` | `cold-create` (default) or `skip`. |
| `rules[].cooldown_seconds` / `group_by` | See [§5](#5-storm-control). |
| `rules[].prompt` | The agent's first turn. Supports `@file` and `{{field}}` expansion, including `{{raw}}` for the full envelope. |

## 5. Storm control

Four layers, cheapest first. The FIFO queue already removes the concurrency
hazard (a box drains one message at a time), so these govern **backlog and
spend**, not correctness.

| Layer | Key | On store error |
|---|---|---|
| Replay nonce | `wh:{source}:{delivery_key}` | **fails open** — log and proceed, never strand a real alert on a DynamoDB blip |
| Cooldown (`group_by`) | `wh-cd:{source}:{rule}:{group}` | **fails open** |
| Rate ceiling | `wh-rate:{time-bucket}` | **fails open** |
| Action quota (Phase 121) | per-sandbox `webhook_dispatch` action | **fails closed if it trips** — `onBreach: block` or `freeze` actually stops the dispatch; a store-read failure to *evaluate* the quota still fails open, same as the other three |

**`group_by` coalesces — it does not batch.** Fifty findings on one host with
`group_by: "{{entity.cloud_id}}"` means the **first** dispatches and the other
**forty-nine are suppressed** for the cooldown window. This is dedup-by-grouping,
not a buffer that later delivers all fifty in one digest — true batching is out
of scope for this phase. If you need every finding seen individually, set
`cooldown_seconds: 0` (or omit it) and rely on the rate ceiling instead.

The rate ceiling is counted **after** the replay and cooldown gates, so
suppressed duplicates never consume ceiling budget — only real dispatches do.
It is also **install-wide across all sources**, not per-source: it protects
one shared fleet of sandboxes and one shared AI budget, so a Wiz storm and a
future second source's storm draw down the same allowance.

The action quota (`spec.limits` / install `limits:`, see `docs/action-quotas.md`)
is the actual circuit-breaker against runaway spend — the other three layers
smooth bursts, this one is what stops a sustained flood. It only engages if the
target sandbox's profile declares a `webhook_dispatch` limit; without one, this
layer is a no-op and dispatch proceeds.

## 6. The `{{...}}` non-collision

Wiz's Automation Rule body and km's `prompt:` field **both** use `{{...}}`
syntax, and it is easy to assume they collide. **They don't, because they
render at different times in different systems:**

- **Wiz renders its own Mustache variables first**, on Wiz's side, before the
  HTTP request is ever sent. By the time the POST body reaches
  `km-webhook-bridge`, every `{{issue.id}}`, `{{issue.severity}}`, etc. is
  already a literal string — plain JSON, no Wiz syntax left in it.
- **km then expands its own `{{field}}` syntax** — `{{severity}}`,
  `{{entity.name}}`, `{{raw}}` — against the **already-rendered** JSON
  envelope, when building the agent prompt from `rules[].prompt`.

Nothing in a rendered Wiz payload can accidentally satisfy a km template
variable and vice versa, because the two expansions never see each other's raw
`{{...}}` tokens. Say this explicitly to anyone new to the setup — this is
exactly the kind of thing that costs an hour of confused staring at two
templates that look identical but are consumed a step apart.

## 7. Deploy surface

**Order is load-bearing.** Get this wrong and the failure is silent:
`km doctor` stays green while nothing was actually provisioned.

1. **`make build` FIRST.** The operator binary carries the new
   `regionalModules()` entries (`lambda-webhook-bridge`, and the shared inbound
   DLQ module now covers a `webhook-inbound` variant). A stale binary silently
   **skips** them during `km init` — it doesn't error, it just never applies
   them — so this must run before step 3, not after.
2. **`make build-lambdas`.** Builds the new `km-webhook-bridge` zip, and
   **also refreshes the create-handler**, which is what renders the new
   `notification.webhook.inbound` profile field and the on-box poller
   userdata. Skipping this step leaves cold-created sandboxes unable to gain
   the queue even after step 3 applies the infrastructure.
3. **`km init --dry-run=false`** — **not `--sidecars`**. The Lambda, Function
   URL, IAM, the new DLQ, and the `KM_WEBHOOK_SOURCES` / `KM_WEBHOOK_RATE_LIMIT`
   env block all need a full terragrunt apply. A scoped
   `km init --webhooks` (sugar for `--only lambda-webhook-bridge`) is available
   for a fast env+IAM-only refresh once the module already exists — it does
   not create the module the first time.
4. **One `km destroy && km create`** on the target sandbox (e.g. `ir-bot`) to
   pick up the per-sandbox webhook-inbound queue and the
   `km-webhook-inbound-poller` systemd unit. Existing sandboxes keep their
   baked-in userdata — created before this profile field was set — until
   recreated.

Why step 1 must precede step 3: `km init` decides which Terraform modules to
apply by asking the **compiled binary**, not the source tree. If you run
`km init --dry-run=false` against a binary built before this phase's
`regionalModules()` entries existed, it silently walks past
`lambda-webhook-bridge` as if it were never in scope — no error, no warning,
just an install that looks identical to before. Building first is the only way
`km init` even knows the module exists to apply.

## 8. Token rotation

`km init` mints a fresh 32-byte random bearer token into the configured
`secret_path` **only when that SSM parameter does not already exist**, and
prints it once for you to paste into the Wiz integration. It **never**
overwrites an existing parameter — re-running `km init` (for any reason, on any
schedule) cannot rotate a token out from under a live Wiz integration by
accident.

Deliberate rotation is therefore a two-step, explicit action:

```bash
aws ssm put-parameter \
  --name /km/config/webhooks/wiz/token \
  --type SecureString \
  --value "$(openssl rand -base64 32)" \
  --overwrite
```

then paste the new value into the Wiz integration's Authorization header. The
old token stops working the moment you save the Wiz side; there is no grace
window, so do this during a change window if your Wiz tenant sends a high
volume of issues.

## 9. Troubleshooting

Every log tag below is emitted at INFO/WARN/DEBUG and the request **still
returns HTTP 200** regardless of outcome — including auth failures. This is
deliberate: a non-200 response makes the sender redeliver, and for a source
with no delivery-id header (Wiz), redelivery just repeats the same request
forever with no dedup benefit. Never diagnose this bridge by watching for a
non-200 from Wiz; watch CloudWatch Logs on `km-webhook-bridge` instead.

| Log tag | Meaning | Where to look |
|---|---|---|
| `webhook_unauthorized` | Bearer token missing, malformed, or didn't match — or the configured secret resolved empty (fails closed, never open) | Confirm the Wiz-side Authorization header still has the current token; check `/km/config/webhooks/wiz/token` in SSM |
| `webhook_unparseable` | Body was not valid JSON | Check the Automation Rule's request-body field for a paste error (stray Mustache section left unclosed, wrong quoting) |
| `webhook_no_rule_match` | Envelope parsed, but no rule's `match` fired | Check `rules[].match` values against what the Automation Rule actually renders — remember a missing field is a non-match, not a wildcard |
| `webhook_replay_dropped` | Same `delivery_key` seen within `replay_ttl_seconds` | Expected on Wiz retries; if unexpectedly frequent, `issue.updatedAt` may not be changing between deliveries you expect to be distinct |
| `webhook_cooldown_suppressed` | A `group_by` key repeated within `cooldown_seconds` | Expected during a burst — this is the coalescing behavior from §5, not a failure |
| `webhook_rate_ceiling_tripped` | Install-wide `max_dispatches` exceeded in the current window | Raise `webhooks.rate_limit`, or investigate why one source is suddenly this noisy |
| `webhook_queue_url_missing` | Resolved a running/resumed sandbox but its webhook-inbound queue URL wasn't found | The target sandbox predates `notification.webhook.inbound.enabled` on its profile — needs `km destroy && km create` (§7 step 4) |
| `webhook_cold_create_failed` / `webhook_enqueue_failed` | The dispatch itself failed after passing every gate | Check the create-handler / SQS IAM grants landed via step 3 of §7 |

Also check:

- **DLQ depth** (`{prefix}-webhook-inbound-dlq.fifo`) — `km doctor` reports it
  directly; a growing depth means the poller is receiving messages it can't
  process (agent binary missing, malformed envelope) rather than the bridge
  failing to dispatch.
- **`km doctor`'s webhook group** (silently skipped entirely when `webhooks:`
  is absent) also checks: each source is structurally valid; each
  `auth.secret_path` resolves in SSM; the Function URL is recorded in SSM;
  each rule's `profile` file exists on disk.

## 10. Limits

- **A static bearer token is replayable.** The replay nonce (keyed on
  `delivery_key`, TTL `replay_ttl_seconds`) stops accidental duplicate
  dispatch and retries within its window. It **cannot** stop someone who
  captured a valid request and replays it deliberately after the TTL lapses —
  that would need a signed, timestamped request, and Wiz's webhook integration
  does not offer one (no HMAC, no signature of any kind).
- **Wiz's client-certificate option is not usable here and is not planned.**
  A Lambda Function URL cannot terminate mTLS. Fronting it with an ALB or API
  Gateway to support one is out of scope — the operator has confirmed mTLS is
  never required for this integration.
- **The bearer token is therefore the entire auth boundary.** Treat
  `secret_path`'s SSM parameter with the same care as any other credential:
  it is a SecureString, but anyone who can read it can forge Wiz requests
  until you rotate it (§8).
- **The action-quota gate is warm-path only.** It evaluates against a
  specific sandbox's usage history, so it cannot apply to a cold-create
  dispatch — there is no sandbox yet to have a history. A storm that only
  ever hits `on_absent: cold-create` is bounded by the install-wide rate
  ceiling (§5) alone, not by any per-sandbox quota.
- **No true batching.** `group_by` coalesces a burst down to one dispatch,
  not one *summarizing* dispatch of all fifty findings — see §5.

## See also

- `docs/webhook-templates/wiz.issue.v1.json` / `wiz.threat.v1.json` — the
  operator-pasteable Wiz Automation Rule request bodies referenced in §2.
- `docs/action-quotas.md` — the `onBreach: freeze` circuit-breaker referenced
  in §5.
- `docs/check-runner.md` — the Phase 116 **pull** counterpart
  (`checks.triggers`) that shares this feature's `alias` / `profile` /
  `on_absent` / `cooldown_seconds` / `prompt` vocabulary.
- `docs/github-bridge.md`, `docs/h1-bridge.md` — sibling bridges with a
  reply-back leg and a threads table, which this feature deliberately omits.
