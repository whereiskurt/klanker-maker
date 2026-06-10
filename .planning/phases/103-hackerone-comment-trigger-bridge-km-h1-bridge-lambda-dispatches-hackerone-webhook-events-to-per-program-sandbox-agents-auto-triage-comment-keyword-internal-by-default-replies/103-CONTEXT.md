# Phase 103: HackerOne comment-trigger bridge — Context

**Gathered:** 2026-06-09
**Status:** Ready for planning
**Source:** Live discussion (operator) + research (HackerOne webhooks/API + GitHub-bridge architecture map)

<domain>
## Phase Boundary

Build `km-h1-bridge` — a HackerOne analog of the GitHub comment-trigger bridge (Phases 97–102).
A single Lambda Function URL receives HackerOne **program webhooks**, verifies + dedupes them, resolves
the report's program to one-or-more sandbox targets, and dispatches a sandbox agent turn. The sandbox
agent reads the report and posts back to HackerOne via a new `cmd/km-h1` helper.

**The whole hard pipeline already exists** in `pkg/github/bridge` and ports over with header/field renames:
HMAC verify, GUID dedup, config-driven resolve, 3-way warm/cold/resume dispatch, thread continuity. The
genuinely new work is: the HackerOne webhook payload shape, the Basic-Auth back-channel, the two trigger
models, config-driven event→prompt mapping, multi-target fanout, and the internal/external reply guard.

### In scope
- `km-h1-bridge` Lambda (`cmd/km-h1-bridge`) behind a Function URL (auth NONE; app-layer HMAC + nonce).
- `pkg/h1/bridge` — ported from `pkg/github/bridge`.
- New `h1:` block in km-config.yaml: `programs:` (per-program routing + targets + allow + events + commands),
  a configurable comment trigger `@handle`, an `event→prompt` mapping surface.
- TWO trigger models: (1) opt-in lifecycle-event **auto-triage**, (2) configurable **@-handle comment-keyword**.
- `/command` parsing that overrides a default prompt; `/claude` `/codex` agent verbs (Phase 102 analog).
- **Multi-target fanout**: one trigger fans the same prompt to MULTIPLE sandbox targets (envs/profiles).
- Reply visibility: **INTERNAL by default**; `/reply_to_researcher` posts researcher-visible, allowlist-gated.
- `cmd/km-h1` sandbox helper: `comment` (internal/public), `state`, `read` — HackerOne customer API Basic Auth.
- New CLI: `km h1 init`, `km h1 status`.
- New TF module `infra/modules/lambda-h1-bridge`, wired into `regionalModules()` + `lambdaBuilds()`.
- New lean H1 profile (analog of `profiles/github-review.yaml`).

### Out of scope (deferred)
- **Federated relay** (GitHub Phase 100/101 analog: one front-door relaying to peer install bridges).
  NOT NEEDED here the way it is for GitHub — each HackerOne program's webhook can point directly at a
  specific install's Function URL (GitHub forces one App = one URL across all repos; HackerOne does not).
  Note: the operator's "fanout" intent is satisfied by **multi-target dispatch** (in scope), NOT relay.
- HackerOne report **attachment** download/upload (defer unless trivial).
</domain>

<decisions>
## Implementation Decisions (LOCKED unless noted)

### Webhook ingestion (port from pkg/github/bridge)
- **HMAC verify:** `X-H1-Signature` header is `sha256=<hexdigest>`, HMAC-SHA256 of the raw request body
  keyed by the program's webhook secret — the SAME scheme as GitHub's `X-Hub-Signature-256`. Reuse the
  constant-time `VerifyGitHubSignature()` logic with a header-name swap. Bad/absent sig → 401; internal
  error → 200 (mirror GitHub bridge Pitfall 3).
- **Dedup:** `X-H1-Delivery` GUID → shared `{prefix}-slack-bridge-nonces` table with an `h1-delivery:`
  key prefix, 24h TTL (analog of `github-delivery:`).
- **Event type:** read from `X-H1-Event` header (not duplicated in JSON body).
- **Payload:** top-level `data` object → `data.activity` (event metadata) + `data.report` (full report incl.
  `id` and program-handle relationship). Parse program handle from `data.report` relationships.

### Routing / config (`h1.programs:` — analog of `github.repos:`)
- Resolve the report's **program handle** → `{targets[], allow[], events[], commands{}, default_command}`.
  Program handle is the routing key (GitHub used `owner/repo`).
- `allow:` is a login allowlist, **deny-by-default** (which HackerOne usernames may trigger / be honored).
- New config structs in `internal/app/config/config.go` (mirror `GithubRepoEntry`/`GithubCommandEntry`/
  `GithubConfig`). Remember the **v2→v merge-list** requirement (memory `project_config_key_merge_list`) —
  new top-level `h1:` key must be added to the merge-list in `config.Load()`, not just struct+getter.

### Trigger model 1 — auto-triage (event-driven)
- **Opt-in per program / DORMANT by default.** A program auto-triages ONLY the lifecycle events it
  explicitly lists (e.g. `events: [report_created]`). Absent/empty `events:` ⇒ comment-keyword only.
  (Mirrors the "dormant by default" posture used across Slack/GitHub federation phases.)
- On a listed event, dispatch the **event→prompt mapping** for that event type (see below).

### Trigger model 2 — comment-keyword (@-handle)
- The comment trigger is a **configurable @-handle** set in km-config.yaml (e.g. `h1.bot_handle: "@km"`),
  the HackerOne analog of the GitHub @-mention scan (HackerOne internal comments have no bot user to
  @-mention, so the handle is a literal string match in the comment body).
- Fires on `report_comment_created`; the comment body must contain the @-handle to trigger.
- A **default prompt** applies when no `/command` is present; `/command`(s) **override** the default.
- **`/commands`**: parse `/command` tokens from the body (reuse the GitHub Phase 99 command-parser model).
  Commands may differ between the comment context and the auto-triage (report-generation) context — i.e.
  separate command/prompt sets keyed by context.
- **Multiple distinct `/commands` in one comment ⇒ error reply, no dispatch** (mirror GitHub's ≤1-verb rule).
- **Agent verbs `/claude` `/codex`** (Phase 102 analog): select the agent for the turn; absent ⇒ the box's
  default agent (`spec.agent.default`). Reserved tokens; composes with template commands.

### Event→prompt mapping (config-driven)
- km-config.yaml maps an H1 event type (e.g. `report_created`) and/or a `/command` name → a prompt
  kind/template (a string template; may reference report fields / `{{args}}` like GitHub commands).
- Potentially **different `/command` sets for the comment context vs the auto-triage context** — design
  the config so a program can declare both an `events:`→prompt map and a `commands:`→prompt map.

### Multi-target fanout (IN SCOPE)
- One trigger (auto-triage event OR @-handle comment) can fan the SAME prompt out to **multiple sandbox
  targets** (envs/profiles) at once — `targets:` is a list per program (each target = alias/profile, the
  GitHub model had exactly one).
- Each target gets its own dispatch + its own **report-id-keyed thread continuity row** (keyed by
  `report_id` + target, so N targets don't collide).
- **Fanout interaction with `/reply_to_researcher`:** an external researcher-visible reply must NOT be
  posted N times by N targets. Planner/research must resolve this (candidate: `/reply_to_researcher` is
  single-target only, or only the primary/first target may post externally). Flagged as a design risk.

### Dispatch (port 3-way from pkg/github/bridge)
- **Warm** (target alias running): SQS FIFO enqueue to `{prefix}-h1-inbound-{sandbox_id}.fifo`
  (groupID per report; dedupID `{deliveryGUID}-{groupID}`).
- **Cold** (alias absent): EventBridge `SandboxCreate` with the H1 envelope + profile artifact prefix.
- **Resume** (alias stopped/paused): `StartInstances` + enqueue.
- New `H1Envelope` (analog of `GitHubEnvelope`): `source="hackerone"`, `program`, `report_id`, `kind`
  (`report_created` | `report_comment_created` | …), `activity_id`, `report_url`, `actor`/`sender`,
  `body` (extracted prompt), `agent`, `reply_target` flags.

### Thread continuity
- New `{prefix}-h1-threads` DDB table (analog of `km-github-threads`), keyed by **report id** (+ target).
  Stores `sandbox_id`, agent session id, `agent_type`. Follow-up comments in a known report bypass the
  @-handle requirement (analog of GitHub thread-bypass). `agent_type` schema-on-write (no TF migration).

### Reply path (the safety-critical part)
- **INTERNAL by default.** Every agent reply is a HackerOne **internal** comment unless explicitly marked
  researcher-visible. This is the default to avoid accidentally messaging external hackers.
- **`/reply_to_researcher`** is the ONLY way to post a researcher-visible (non-internal) reply, and it is
  **gated by Command + allowlist**: the triggering commenter must be in the program's `allow:` list
  (deny-by-default, the same authz gate as dispatch). Command-present alone is NOT sufficient.
- `cmd/km-h1 comment` supports BOTH an internal flag and a public/`--reply-to-researcher` flag; **default
  is internal**. The public path must be explicit at every layer.

### Back-channel (HackerOne customer API — simpler than GitHub)
- **HTTP Basic Auth** (API username + API token), stored in SSM. NO App-JWT / installation-token dance,
  NO per-sandbox token refresher (the big GitHub simplification).
- Endpoints: `POST /reports/{id}/comments` (with `internal` flag), `PATCH /reports/{id}/state`,
  `GET /reports/{id}`. `cmd/km-h1` subcommands: `comment` / `state` / `read`.

### CLI surface
- **`km h1 init`**: mint a 32-byte hex webhook secret + store Basic-Auth creds, all in SSM under
  `/{prefix}/config/h1/*`; print the Function URL + secret for the operator to paste into the HackerOne
  program's **Webhooks UI** (Engagements → Program → Settings → Automation → Webhooks). NO App-manifest
  generator (HackerOne has no App-install model — config is per-program in the UI).
- **`km h1 status`**: print SSM-backed H1 config (secret redacted), programs, targets, handle, bridge-url.
- (Optional, planner discretion) `km doctor` checks for the H1 group, dormant when unconfigured.

### Deploy wiring (memory-critical)
- Add `lambda-h1-bridge` to `regionalModules()` in `init.go` AND a `cmd/km-h1-bridge` entry to
  `lambdaBuilds()`. Per memory `project_make_build_precedes_km_init`: **`make build` the km binary BEFORE
  `km init`** when adding a `regionalModules()` entry, or a stale km silently skips the new module.
  Per `project_new_lambda_needs_live_unit_and_init_list`: the Lambda needs a TF module AND a live
  terragrunt.hcl unit AND the init.go list entry. Per `project_km_init_skips_existing_lambda_zips`:
  the build list is hardcoded — a Lambda missing from `lambdaBuilds()` is silently never built.
- Harden the new `{prefix}-h1-inbound-*.fifo` queues with the shared per-install DLQ + RedrivePolicy
  (memory `project_inbound_poller_fifo_poison_wedge`, resolved Phase 99.1) — reuse the warm helper.

### Claude's Discretion
- Exact `pkg/h1/bridge` file decomposition (mirror `pkg/github/bridge`: interfaces / aws_adapters /
  resolve / payload / commands / webhook_handler).
- Whether to share or fork the nonce/SQS/EventBridge/resume adapters vs. the GitHub ones (DRY vs. coupling).
- The precise YAML shape of `h1.programs[].events:` and `h1.programs[].commands:` (event→prompt + command→prompt).
- Wave/plan decomposition and TDD test boundaries.
- Whether `km doctor` H1 checks land this phase or a fast-follow.
</decisions>

<specifics>
## Specific references

- **HackerOne webhooks:** configured per-program (Engagements → Program → Settings → Automation → Webhooks);
  payload URL + secret; "send everything" or per-event; built-in Test request + recent-deliveries inspector.
  Headers: `X-H1-Event`, `X-H1-Delivery` (GUID), `X-H1-Signature` (`sha256=<hex>` HMAC-SHA256 of raw body).
  35+ events incl. `report_created`, `report_comment_created`, `report_triaged`, `report_needs_more_info`,
  `report_reopened`, `report_bounty_awarded`, etc. Payload: `data.activity` + `data.report`.
  Docs: https://docs.hackerone.com/en/articles/8588351-webhooks , https://api.hackerone.com/webhooks/
- **HackerOne customer API:** HTTP Basic Auth (API username + token). `GET /reports/{id}`,
  `POST /reports/{id}/comments` (internal flag), `PATCH /reports/{id}/state`.
  Docs: https://api.hackerone.com/customer-resources/

- **GitHub-bridge files to clone/adapt** (canonical map):
  - Lambda entry: `cmd/km-github-bridge/main.go`
  - Core handler (11-step flow): `pkg/github/bridge/webhook_handler.go`
  - Interfaces: `pkg/github/bridge/interfaces.go`
  - AWS adapters (SSM/DDB nonce/alias/thread, SQS, EventBridge, EC2 resume): `pkg/github/bridge/aws_adapters.go`
  - Resolve: `pkg/github/bridge/resolve.go`
  - Envelope/payload: `pkg/github/bridge/payload.go`
  - Commands / agent-verbs: `pkg/github/bridge/commands.go`
  - Sandbox helper: `cmd/km-github/main.go`
  - TF module: `infra/modules/lambda-github-bridge/v1.1.0/{main,variables,outputs}.tf`
  - CLI: `internal/app/cmd/github.go`
  - Config structs: `internal/app/config/config.go`
  - Init orchestration (module list + build list + profile pre-stage): `internal/app/cmd/init.go`
  - Per-sandbox inbound queue provisioning: `internal/app/cmd/create_github_inbound.go`
  - Profile: `profiles/github-review.yaml`
</specifics>

<deferred>
## Deferred Ideas

- **Federated relay** (one H1 webhook → front-door install → peer bridges; GitHub Phase 100/101 analog).
  Not needed for HackerOne the way it is for GitHub (per-program webhook URLs). Operator's fanout intent
  is met by in-scope multi-target dispatch. Revisit only if a true one-webhook-many-installs case appears.
- **Orphan-program helpful reply** (Phase 101 analog).
- **Report attachment** download/upload through the bridge/helper.
- **`km doctor` H1 deep checks** — may land this phase (planner discretion) or as a fast-follow.
</deferred>

---

*Phase: 103-hackerone-comment-trigger-bridge*
*Context gathered: 2026-06-09 via live discussion + research*
