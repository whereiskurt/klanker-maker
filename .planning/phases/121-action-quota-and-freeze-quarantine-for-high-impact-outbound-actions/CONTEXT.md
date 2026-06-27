# Phase 121 — Action quota + freeze quarantine: design context

> Captured from a brainstorming session (2026-06-27). This is the agreed design that
> `/gsd:plan-phase 121` should break into plans. Every decision below was an explicit
> user choice during the session — they are settled, not open questions.

## 1. Goal & threat model

An **external, agent-untrusted** quota layer on high-impact outbound actions. A sandbox that's
been manipulated into a tight loop — a malicious actor chatting with the bot **or** a
jailbroken/runaway agent (the mechanism is intent-agnostic) — trips a limit at a chokepoint it
**cannot bypass**, because enforcement lives outside agent-controllable code:

- **http-proxy** (the network-enforcement boundary / MITM sidecar) — already MITMs egress and
  meters AI spend (Phase 88). `km-github` posts **directly** to `api.github.com` with the
  sandbox-held token, so GitHub writes transit the proxy. SES email send transits it too.
- **bridge Lambdas** (Slack, H1) — they hold the bot/API tokens; the sandbox never does. The
  sandbox's `km-slack` signs an Ed25519 envelope and POSTs to the bridge Function URL.

Phase 1 = **alert-now**; the same counters later **block** (flip a flag). See §6.

## 2. Core mechanism — `pkg/quota` + multi-window counter table

New package, one entry point: `Record(ctx, sandboxID, action, limits) → Decision`. For each
**configured** window of that action it does an atomic `UpdateItem … ADD count 1` and compares
the returned count to that window's limit.

A single action carries **several limits at once**, each its own window; the action trips if
**any** window is exceeded:

| Window | Resets | Example | Row TTL |
|---|---|---|---|
| `lifetime` | never (whole sandbox life) | 100 PRs | none — deleted on `km destroy` |
| `hour` | fixed hourly bucket (`epoch/3600`) | 15 PRs/hr | ~2h |
| `day` | fixed calendar day, UTC midnight (`epoch/86400`) | 50 PRs/day | ~2d |

Table `{prefix}-action-quota`:
- `PK = {sandbox}#{action}`, `SK = <window>` (`lifetime` | `hour#<bucket>` | `day#<bucket>`)
- attrs: `count` (number), `ttl` (none for `lifetime`), `breached_at`, `alert_sent`
- **Streams ENABLED** (drives the alerter, §5)

`Decision` carries per-window `{count, limit, exceeded}` + overall `tripped` + `worstWindow` +
the resolved `onBreach` policy. Fixed buckets give "resets daily/hourly" for free and stay atomic
+ TTL-friendly. **Fixed buckets, not sliding** — slight burst tolerance at boundaries is
acceptable for a safety net (true sliding deferred to v2).

`lifetime` rows have no TTL → deleted in the `km destroy` teardown path (alongside the sandbox
row); otherwise harmless orphans swept by `km doctor`.

## 3. Chokepoints & action taxonomy

| Action | Chokepoint | Match |
|---|---|---|
| `github_pr` | http-proxy | `POST api.github.com /repos/*/pulls` |
| `github_comment` | http-proxy | `POST …/issues/*/comments` |
| `github_review` | http-proxy | `POST …/pulls/*/reviews` |
| `email_send` | http-proxy | SES `SendEmail` / `SendRawEmail*` (endpoint already MITM'd) |
| `slack_post` | slack-bridge | `ActionPost` / `ActionUpload` envelope |
| `h1_comment` | h1-bridge | comment dispatch |

Proxy owns GitHub + email (it sees that egress, already has DDB-write IAM). Bridges own Slack/H1
where the action is unambiguous. All write the same table via `pkg/quota`.

**Sandbox-lifecycle / cold-create storm limits are OUT of v1** (different mechanism: bridge /
EventBridge layer). Deferred — see §9.

## 4. Config & resolution — per-profile + install defaults

- **Install default:** new `limits:` block in `km-config.yaml`. MUST be added to the config
  **v2→v merge-list** in `config.Load()` or the file value is silently dropped
  (known footgun — see memory `project_config_key_merge_list`).
- **Per-profile:** new `spec.limits` block in the SandboxProfile JSON schema
  (`additionalProperties:false`). **Additive → no `apiVersion` bump** (consistent with Phase
  118/119 additive fields).
- **Resolution, per (action, window):** profile value if set → else install default if set →
  else **unlimited**. Per-window precedence (a profile that sets only `perHour` still inherits
  the default `lifetime`).
- Example config shape:
  ```yaml
  limits:
    github_pr:      { lifetime: 100, perHour: 15, perDay: 50, onBreach: freeze }
    github_comment: { perHour: 60, perDay: 300, onBreach: warn }
    email_send:     { lifetime: 200, perHour: 10, perDay: 40, onBreach: block }
    slack_post:     { perHour: 120, onBreach: warn }
    h1_comment:     { perHour: 60, onBreach: warn }
  ```
- **Runtime delivery of the resolved map** (source of truth at runtime):
  - **proxy:** written into **userdata** at create time (compiler renders it into the sidecar
    env/config), like other per-sandbox knobs.
  - **bridges:** written onto the **`km-sandboxes` row** as `action_limits` (resolved JSON);
    bridges read it via `FetchByChannel`. MUST round-trip through `SandboxMetadata`
    (pause/resume/extend/ttl) or it gets stripped on full-row PutItem (memory
    `project_sandboxmetadata_lossy_roundtrip`).
- **Dormancy:** omit a window ⇒ that window not enforced; omit the action ⇒ no counting ⇒
  byte-identical to today.

## 5. Notification model — dual streams, both control-plane

Two independent notice streams. **Both are control-plane: never counted against any quota, and
never posted by the (possibly throttled) sandbox agent** — they come from the token holder, so a
tripped `slack_post` can never silence its own "you've been throttled" message.

| Trip origin | User-facing notice | Operator notice |
|---|---|---|
| `slack_post` / `h1_comment` (the chat loop) | **Bridge** replies **in the active thread**, instantly | alerter → email (+ optional control channel) |
| `github_pr` / `github_comment` / `github_review` / `email_send` (proxy) | **alerter Lambda** posts to the sandbox's **main channel** | alerter → email (+ optional control channel) |

- **Operator alert path:** DDB Stream on the quota table → new **`km-quota-alerter`** Lambda.
  On the MODIFY where a window first breaches (`breached_at` just set, `alert_sent` absent): send
  the operator an **SES email** (+ optional Slack control channel if configured), then set
  `alert_sent` conditionally → **exactly one alert per (sandbox, action, window)**. Decoupled
  from the request hot path.
- **User-facing notice (the "post a warning in the channel" requirement):**
  - **Bridge, in-thread** for chat trips — immediate, threaded, where the person is talking.
  - **Alerter, channel-level** for proxy trips (GitHub/email) — the alerter resolves the
    sandbox→channel mapping from the `km-sandboxes` row and posts via the bot token (needs SSM
    bot-token read IAM + the channel id).
  - Both are **enforce-aware**:
    - WARN mode: *"⚠️ Quota reached: `github_pr` hit 15/15 this hour. WARN mode — actions still
      flowing; heads-up."*
    - BLOCK/FREEZE: *"🛑 Quota exceeded: `github_pr` (15/hr). Further PRs blocked until the window
      resets at HH:00 UTC."* / frozen variant (§7).
  - **Idempotent** (one per sandbox/action/window via the `breached_at`/`alert_sent` guard — a
    hard loop of hundreds of blocked attempts still yields one notice).
  - **Best-effort:** only when the sandbox has an associated Slack channel; else operator
    email/log only.

## 6. Breach policy — three tiers (phased enforcement)

Per-limit `onBreach: warn | block | freeze`:
- **`warn`** (default) — count + alert + user notice; action still flows. This is the entire
  **alert-now** phase.
- **`block`** — chokepoint already holds `(count, limit)` synchronously → returns **429/deny**
  for *that action* until the window resets. No new components.
- **`freeze`** — latches the **whole sandbox** into quarantine (§7). Natural for `lifetime` caps
  / panic thresholds.

Ships dormant (effective default `warn` everywhere) so Phase 1 is pure observation; graduate
individual limits to `block`/`freeze` once real baselines are known.

**User decision: "alert now, block later"** — design for both; enable enforcement later by config.

## 7. Quarantine latch — the "stop"

A **latched quarantine** with *asymmetric* control: easy to trigger (auto or manual CLI),
releasable **only** by an operator at the CLI — deliberately outside the Slack/GitHub attack
surface, so nothing a chatter says can clear it.

- **State:** `action_frozen=true` (+ `frozen_reason`, `frozen_at`, `frozen_by`) on the
  `km-sandboxes` row. Atomic DynamoDB write. **Round-trips through `SandboxMetadata`** so it
  survives stop/resume/extend/ttl — a restart can't shake it loose (delivers "can't be restarted
  from another Slack message").
- **Containment strength (user decision): latch actions, box keeps running.** No `km stop`. The
  EC2 box stays up (agent can think/read; nothing escapes). Halting/destroying is a separate,
  deliberate human escalation the operator may choose after being alerted (auto-freeze is the
  floor, not power-off).
- **What it blocks:** every chokepoint (proxy + bridges) denies **all** high-impact actions;
  **inbound bridges refuse to dispatch new turns** and post the in-thread control-plane notice:
  *"🛑 This sandbox is frozen (reason). No further actions or replies until your operator releases
  it."*
- **Triggers (v1, user decision):**
  1. **Auto-on-breach** — any limit with `onBreach: freeze`, when its window is exceeded.
  2. **Operator CLI panic button** — new verb `km freeze <sandbox> [--reason …]`.
  - **No Slack trigger** (allowlisted-Slack-operator trigger deferred to v2 — keeps the trigger
    surface off Slack entirely).
- **Release:** **`km unlock <sandbox>`** only — AWS-authenticated, out-of-band, `--yes` to
  confirm. Box was never stopped → actions resume immediately, no `km resume` needed.
- **`km unlock` overloading (flagged for plan-phase):** today `km lock`/`km unlock` is the
  destroy/stop/pause safety lock. Make `km unlock` **latch-aware** — it clears the quarantine
  freeze (and reports what it cleared); the existing safety-lock behavior stays intact. `km freeze`
  is the new trigger verb pairing with `km unlock` as the release (per user's explicit `./km unlock`
  request).
- **Visibility:** `km list` / `km status` show a `FROZEN` marker; `km doctor` surfaces frozen
  sandboxes.

## 8. New infrastructure & deploy surface

(Deploy-surface verification is mandatory — code-green ≠ deployable; memory
`feedback_verify_deploy_surface_not_just_code`.)

- **DynamoDB `{prefix}-action-quota`** (+ Streams): new TF module; **added to `regionalModules()`**
  (bump the module-order test count — `project_module_order_test_count_debt`) and to `init.go`'s
  curated module list. `make build` the binary **before** `km init` or a stale `km` silently
  skips the new module (`project_make_build_precedes_km_init`).
- **Lambda `km-quota-alerter`** (DDB-Stream triggered): needs **all four** or it's invisible to
  `km init` — TF module **+** live terragrunt unit **+** `init.go` list entry **+**
  `lambdaBuilds()` entry (`project_new_lambda_needs_live_unit_and_init_list`,
  `project_km_init_skips_existing_lambda_zips`).
- **IAM:** quota-table write added to the ec2spot per-sandbox role (proxy) + slack/h1 bridge
  roles; alerter gets stream-read + SES-send + SSM bot-token read + `km-sandboxes` read.
- **`km-sandboxes` attrs:** `action_limits` + `action_frozen`/`frozen_*` — schema-on-write,
  round-trip through `SandboxMetadata`.
- **Config:** `limits:` key added to the v2→v merge-list.
- **Profile schema:** `spec.limits` block (additive, no `apiVersion` bump).
- **Deploy sequence:** `make build` (binary carries the new regionalModules entry — before
  `km init`) → `make build-lambdas` → `km init --dry-run=false` (table + Streams + Lambda + IAM +
  env) → `make build && km init --sidecars` (proxy sidecar + `spec.limits` schema). Existing
  sandboxes need `km destroy && km create` to gain the new userdata + per-sandbox attrs.

## 9. Out of scope (v2)

- **Per-actor keying** (per Slack-user / GitHub-login) — needs bridge-only actor identity; proxy
  can't see who triggered the turn. v1 keys per (sandbox, action); actor captured as alert
  metadata only.
- **Sandbox-lifecycle / cold-create storm limits** (bridge looping `km create`, budget top-ups).
- **Out-of-band OTEL/audit sweeper** (defense-in-depth backstop; detection-only, after-the-fact).
- **True sliding windows** (v1 = fixed buckets).
- **Allowlisted-Slack-operator freeze trigger** (v1 trigger = auto-on-breach + `km freeze` CLI).
- **`!replace` config directive** for narrowing limits.

## Decisions log (all user-confirmed this session)

1. Breach response: **alert now, block later** (design both; enforce by flag later).
2. v1 metered actions: **GitHub writes + email send + Slack/H1 posts** (NOT sandbox lifecycle).
3. Quota key: **per (sandbox, action)** (actor as metadata, not key).
4. Multi-window limits: **lifetime + perHour + perDay**, any subset, fixed buckets, "any window
   trips ⇒ action trips."
5. Limit config home: **per-profile (`spec.limits`) + install defaults (`km-config.yaml`)**,
   profile wins per-window.
6. Slack trip notice: **dual — in-thread (bridge, chat trips) + channel-level (alerter, proxy
   trips)**.
7. Quarantine strength: **latch actions only, box keeps running** (halt/destroy = separate human
   escalation).
8. Freeze triggers: **auto-on-breach + operator CLI `km freeze`**; release **`km unlock` only**;
   no Slack trigger in v1.
