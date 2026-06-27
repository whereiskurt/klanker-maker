# Action Quotas & Freeze Quarantine

**Phase 121.** An external, **agent-untrusted** rate limit on high-impact *outbound* actions
(opening PRs, sending email, posting to Slack/HackerOne). Limits are enforced **outside the
sandbox** — at the network-enforcement boundary (the MITM http-proxy) and in the bridge Lambdas
— so a misbehaving or compromised agent cannot raise, disable, or evade its own quota. A breach
can alert, block, or **freeze** the sandbox into quarantine.

## Mental model: a safety rail, not a throttle

Action quotas are a **circuit breaker, like the [budget](../CLAUDE.md#budget-metering-coverage)
is for spend** — they exist to contain the rare, expensive failure, not to pace normal work.
The failure modes they protect against:

- an agent stuck in a **loop** that keeps opening PRs / firing emails / spamming a channel;
- an **offensive-breakout or red-team experiment** that *accidentally succeeds* and starts
  taking real outbound actions against real systems;
- any "it ran away while I wasn't watching" blast-radius event.

Because of that, there are really two usage modes:

| Mode | Typical setting | Who |
|---|---|---|
| **Guardrail** (the default posture) | high caps (e.g. `100`) or omitted (unlimited), `onBreach: freeze` or `warn` | most sandboxes — you never expect to hit it; it's there to catch the runaway |
| **Contained experiment** | tight caps (e.g. `github_pr: lifetime: 1`, `slack_post: perDay: 20`), `onBreach: block`/`freeze` | a deliberately bounded run — "this box may open exactly one PR and send 20 Slack messages, then stop" |

When in doubt, set generous caps with `onBreach: freeze` — you get a hard blast-radius ceiling
and an operator alert the instant anything goes sideways, with effectively zero friction on a
well-behaved agent.

Dormant by default: a profile/install with no `limits:` block behaves byte-identically to
pre-Phase-121.

---

## The action taxonomy

| Action | What it counts | Enforced at |
|---|---|---|
| `github_pr` | opening a pull request (`POST /repos/.../pulls`) | **http-proxy** (sandbox-side MITM) |
| `github_review` | submitting a PR review (`.../pulls/.../reviews`) | **http-proxy** |
| `github_comment` | issue/PR comments (`.../issues/.../comments`) | **http-proxy** |
| `email_send` | sending email (SESv2 `SendEmail`) | **http-proxy** |
| `slack_post` | a sandbox post to Slack (via `km-slack`) | **km-slack-bridge** Lambda |
| `h1_comment` | a HackerOne report comment | **km-h1-bridge** Lambda |

The proxy is the chokepoint for everything that leaves the box over HTTPS to GitHub/SES; the
bridges are the chokepoint for chat posts (which are signed envelopes the sandbox hands to the
bridge, never a direct API call). In all cases the counter lives in DynamoDB and the decision is
made by code the agent cannot reach.

---

## Windows

Each action can carry up to three independent windows. A window is counted only if you set it.

| Window | Resets | Bucket key |
|---|---|---|
| `lifetime` | never | one row per sandbox+action |
| `perHour` | top of each clock hour (UTC) | `floor(epoch / 3600)` |
| `perDay` | UTC midnight | `floor(epoch / 86400)` |

Hour/day windows are **fixed calendar buckets**, not rolling windows. `perHour: 10` means "≤10 in
the current UTC hour bucket"; at the hour boundary the count resets to zero (the new bucket is a
fresh DynamoDB row with a short TTL). `lifetime` never resets for the life of the sandbox.

The trip test is `count > limit` on the **post-increment** count, so `lifetime: 5` allows exactly
5 and trips on the 6th attempt.

---

## Breach policies (`onBreach`)

| Policy | Effect on the tripping action | Sandbox state |
|---|---|---|
| `warn` (default) | **action still flows** — alert only | unchanged |
| `block` | action **denied** (proxy drops it / bridge returns 429) | unchanged — later windows can still pass once they reset |
| `freeze` | action denied **and** the sandbox is latched into quarantine | `action_frozen = true` — *all* subsequent outbound actions and bridge turns refused until an operator releases it |

`onBreach` is omitted ⇒ `warn`. A bare `lifetime: 5` with no `onBreach` **alerts but never
blocks** — you must say `block`/`freeze` to actually stop the action.

### Zero = hard deny

`0` is a valid limit (the floor is `>= 0`, negatives rejected). Because `count > limit` trips on
the first attempt (`1 > 0`):

| Config | Behavior |
|---|---|
| `lifetime: 0` + `onBreach: block` (or `freeze`) | **hard deny** — the action is never allowed |
| `lifetime: 0` (default `warn`) | **tripwire** — alerts on *any* use, action still flows |
| field omitted | **unlimited** — window not counted (the default) |

"Unlimited" and "0" are different knobs: unlimited = leave the field out; 0 = trip on the first
attempt.

---

## Configuring quotas

Two layers, resolved per `(action, window)`: **profile value → install default → unlimited**.
`onBreach`: profile wins if set, else install default, else `warn`. A profile that sets only
`perHour` for an action still inherits the install default's `lifetime`/`perDay`.

### Per-profile — `spec.limits`

```yaml
spec:
  limits:
    github_pr:
      lifetime: 20          # ≤20 PRs over the sandbox's whole life
      perDay: 5             # …and ≤5 per UTC day
      onBreach: block       # deny the 6th-per-day / 21st-lifetime
    email_send:
      perHour: 10
      onBreach: warn        # alert at 11/hour but keep sending
    slack_post:
      lifetime: 0
      onBreach: block       # hard deny — this box may never post to Slack
```

### Install-level default — `km-config.yaml`

```yaml
limits:
  github_pr:
    perDay: 10
    onBreach: block
  email_send:
    perHour: 25
    onBreach: warn
```

Applies to every sandbox on the install unless a profile overrides the specific
`(action, window)`. (`limits:` must be in the config v2→v merge-list — it is.)

`km validate <profile>` enforces the rules: `onBreach ∈ {warn,block,freeze}` or empty; window
values `>= 0`; negatives rejected.

---

## Freeze quarantine

`onBreach: freeze` is the panic policy. When a window trips with `freeze`, the chokepoint:

1. denies the action, **and**
2. writes `action_frozen = true` (+ `frozen_reason`, `frozen_at`, `frozen_by = auto:<action>:<window>`)
   to the sandbox's `km-sandboxes` row.

Once latched, the **frozen-dispatch gate** in the bridges refuses every subsequent inbound turn
(it posts a `🛑 This sandbox is frozen…` control-plane notice and stops), and the proxy/bridge
chokepoints deny further outbound actions. The sandbox keeps running (you can still inspect it) —
it just can't *act* until released.

### Operator controls

```bash
km freeze  <sandbox> [--reason "..."]   # manual panic button — latch action_frozen=true now
km unlock  <sandbox>                    # release: clears BOTH the safety-lock and the freeze latch
```

`km freeze` is idempotent and the box keeps running. `km unlock` reports what it cleared and is
backward-compatible with non-frozen sandboxes.

---

## Visibility

### `km status <sandbox>`

Shows a **Quotas** section (configured limit + live usage per window + policy) and a **Frozen**
section:

```
Quotas:
  github_pr   perDay    3/5    block
  email_send  perHour   8/10   warn
  slack_post  lifetime  1/0    block  (hard-deny)

Frozen:      YES — outbound actions blocked
  Reason:    quota exceeded: slack_post (lifetime window)
  Since:     2026-06-27 11:59:08 AM EDT
  By:        auto:slack_post:lifetime
```

Rows are ordered by the action taxonomy; `used/limit` is read live from the
`{prefix}-action-quota` table for the current buckets. Usage is fail-soft — if the table read
fails the section still renders the configured limits with `?/limit`. `(hard-deny)` is appended
when the limit is `0`.

### `km list`

A configured-quota marker (`⚖Q`) appears alongside the `🔒` lock and `🧊FROZEN` markers (in
both narrow and `--wide` output) whenever a sandbox has any `spec.limits` configured.

### `km doctor`

- warns per frozen sandbox (names it, with reason + duration),
- checks the `{prefix}-action-quota` table exists.

### Operator alert — `km-quota-alerter`

A DynamoDB-Streams-triggered Lambda fires **exactly one** operator alert on the *first* breach of
a window (SES email + a Slack notice), deduped via an `alert_sent` conditional write. You don't
have to be watching `km status` — the first time any sandbox crosses a limit, you get pinged.

---

## How it works (internals)

- **Counter store:** `{prefix}-action-quota` DynamoDB table, Streams enabled, PAY_PER_REQUEST,
  SSE on. Key: PK = `{sandbox}#{action}`, SK = `lifetime` | `hour#{bucket}` | `day#{bucket}`.
  Hour/day rows carry a short TTL; lifetime rows don't.
- **Counting:** each chokepoint calls `quota.Record`, which issues one atomic `ADD #count :1`
  (`ReturnValues: ALL_NEW`) per configured window. No configured windows ⇒ zero DynamoDB calls
  (dormant).
- **Breach metadata:** on a trip (`count > limit`) `Record` also writes `breached_at` +
  `on_breach` via a conditional `if_not_exists` SET — this is what the Streams alerter keys on for
  first-breach detection.
- **Limit delivery:** at `km create` the compiler resolves `ResolveLimits(profile, installDefault)`
  and (a) emits `KM_QUOTA_TABLE` + `KM_ACTION_LIMITS` into the proxy's systemd env (for the
  github/ses chokepoint), and (b) writes the resolved JSON to the sandbox's `action_limits` row
  attr (which the bridges read per-turn via GetItem).

---

## Deploy surface

| Change | Redeploy |
|---|---|
| New install (table + alerter don't exist yet) | `make build` + `make build-lambdas` + `km init --dry-run=false` (provisions `dynamodb-action-quota` + `lambda-quota-alerter`; `regionalModules()` = 26) |
| Bridge quota/freeze code or `quota_table_arn` wiring | `make build-lambdas` + `km init --slack` / `km init --h1` (env+IAM) |
| Proxy chokepoint (sidecar) | `make build && km init --sidecars` |
| `spec.limits` on a profile | resolved at `km create` time — `make build` (validate) + `make build-lambdas` (the create-handler compiles + writes `action_limits`). Existing sandboxes need `km destroy && km create`. |
| Install-level `limits:` | `km init --dry-run=false` (it feeds new creates; running sandboxes keep their baked-in resolved limits) |

> **Footgun (fixed in 121 follow-up):** the bridge enforcement is dormant unless
> `KM_QUOTA_TABLE` is set on the bridge Lambda, which requires the bridge terragrunt unit to pass
> `quota_table_arn` from the `dynamodb-action-quota` dependency. A module variable with a gating
> default is invisible if the live unit omits it — verify the *deployed* Lambda env, not just the
> module's `variables.tf`.

---

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Quota never trips on slack/h1 posts | bridge Lambda has `KM_QUOTA_TABLE=""` — the terragrunt unit isn't passing `quota_table_arn`, or the bridge code predates the `main.go` wiring. Check `aws lambda get-function-configuration --function-name {prefix}-slack-bridge`. |
| Quota never trips on github/email | `KM_QUOTA_TABLE`/`KM_ACTION_LIMITS` not in the proxy env — the sandbox predates the wiring; `km destroy && km create`. |
| `km validate` rejects `lifetime: 0` | the operator `km` binary / create-handler predate the `>= 0` floor — `make build` + `km init --dry-run=false`. |
| Configured `block` but actions still flow | `onBreach` defaulted to `warn` (you didn't set it), or the limit field is absent (unlimited). |
| Sandbox won't act / bridge refuses every turn | it's frozen — `km status` shows the Frozen section; `km unlock <sandbox>` to release. |
| No operator alert on breach | check `alert_sent` on the `{prefix}-action-quota` row and the `km-quota-alerter` Lambda's stream event-source mapping. |

---

## See also

- `klanker:user` skill — operator CLI tour (`km freeze` / `km unlock` / `km status`)
- `docs/operational-gotchas.md` — deploy-surface footguns
- CLAUDE.md § Budget Metering Coverage — the *spend* metering layer (distinct from action quotas)
