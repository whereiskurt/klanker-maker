# Phase 103: HackerOne comment-trigger bridge (km-h1-bridge) - Research

**Researched:** 2026-06-09
**Domain:** AWS Lambda webhook bridge (Go), HackerOne customer API + program webhooks, porting an existing GitHub-bridge pipeline
**Confidence:** HIGH (architecture port — every analog source file read and characterized in-repo) / MEDIUM (HackerOne payload internals — two exact field paths flagged for live-webhook confirmation)

## Summary

Phase 103 is overwhelmingly a **port**, not new architecture. The hard webhook pipeline (HMAC verify, GUID dedup, deny-by-default allowlist, config-driven resolve, 3-way warm/cold/resume dispatch, thread continuity, command/agent-verb parsing) already exists and is battle-tested across GitHub Phases 97–102 in `pkg/github/bridge/*`, `cmd/km-github-bridge/main.go`, `cmd/km-github/main.go`, `internal/app/cmd/github.go`, `internal/app/cmd/init.go`, `internal/app/cmd/create_github_inbound.go`, `infra/modules/lambda-github-bridge/v1.1.0/`, and the userdata `km-github-inbound-poller` (`pkg/compiler/userdata.go:2080+`). The genuinely new work is narrow and well-scoped: (1) the HackerOne webhook **payload shape** + header names, (2) the **Basic-Auth back-channel** (much simpler than GitHub's App-JWT/installation-token dance — no token refresher), (3) **two trigger models** (opt-in lifecycle auto-triage + configurable `@handle` literal scan), (4) **config-driven event→prompt mapping**, (5) **multi-target fanout** (one trigger → N sandbox targets, the single thing with no GitHub precedent), and (6) the safety-critical **internal-by-default reply guard**.

The HackerOne side is friendlier than GitHub in three ways that simplify the port: signature scheme is **byte-identical to GitHub's** (`X-H1-Signature: sha256=<hexdigest>` HMAC-SHA256 of the raw body — reuse `VerifyGitHubSignature` with a header-name swap), there is **no App-install model** (so no manifest generator, no JWT, no per-installation token), and **federation/relay is out of scope** (each HackerOne program webhook points directly at a specific install's Function URL, unlike GitHub's one-App-one-URL constraint). The reply path is the only place where HackerOne is *more* dangerous than GitHub: posting to the wrong visibility messages an external researcher, so internal-by-default must be enforced at every layer.

**Primary recommendation:** Near-verbatim copy `pkg/github/bridge` → `pkg/h1/bridge` and `cmd/km-github-bridge` → `cmd/km-h1-bridge` and `cmd/km-github` → `cmd/km-h1`, **sharing** the stateless AWS adapters (nonce store, SQS, EventBridge, EC2 resume) by either reuse-in-place or thin H1 wrappers, and **forking** the payload/resolve/handler/envelope/thread-store/reply layers. Drop federation entirely. Add `h1:` config + `lambda-h1-bridge` module with the full deploy-surface checklist from Phase 97's hard-won 7-gap UAT. Enforce internal-by-default in the helper, the envelope, the allowlist gate, AND the fanout (single external reply target).

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions (verbatim)

**Webhook ingestion (port from pkg/github/bridge)**
- **HMAC verify:** `X-H1-Signature` header is `sha256=<hexdigest>`, HMAC-SHA256 of the raw request body keyed by the program's webhook secret — the SAME scheme as GitHub's `X-Hub-Signature-256`. Reuse the constant-time `VerifyGitHubSignature()` logic with a header-name swap. Bad/absent sig → 401; internal error → 200 (mirror GitHub bridge Pitfall 3).
- **Dedup:** `X-H1-Delivery` GUID → shared `{prefix}-slack-bridge-nonces` table with an `h1-delivery:` key prefix, 24h TTL (analog of `github-delivery:`).
- **Event type:** read from `X-H1-Event` header (not duplicated in JSON body).
- **Payload:** top-level `data` object → `data.activity` (event metadata) + `data.report` (full report incl. `id` and program-handle relationship). Parse program handle from `data.report` relationships.

**Routing / config (`h1.programs:` — analog of `github.repos:`)**
- Resolve the report's **program handle** → `{targets[], allow[], events[], commands{}, default_command}`. Program handle is the routing key (GitHub used `owner/repo`).
- `allow:` is a login allowlist, **deny-by-default** (which HackerOne usernames may trigger / be honored).
- New config structs in `internal/app/config/config.go` (mirror `GithubRepoEntry`/`GithubCommandEntry`/`GithubConfig`). Remember the **v2→v merge-list** requirement (memory `project_config_key_merge_list`) — new top-level `h1:` key must be added to the merge-list in `config.Load()`, not just struct+getter.

**Trigger model 1 — auto-triage (event-driven)**
- **Opt-in per program / DORMANT by default.** A program auto-triages ONLY the lifecycle events it explicitly lists (e.g. `events: [report_created]`). Absent/empty `events:` ⇒ comment-keyword only. (Mirrors the "dormant by default" posture used across Slack/GitHub federation phases.)
- On a listed event, dispatch the **event→prompt mapping** for that event type.

**Trigger model 2 — comment-keyword (@-handle)**
- The comment trigger is a **configurable @-handle** set in km-config.yaml (e.g. `h1.bot_handle: "@km"`), the HackerOne analog of the GitHub @-mention scan (HackerOne internal comments have no bot user to @-mention, so the handle is a literal string match in the comment body).
- Fires on `report_comment_created`; the comment body must contain the @-handle to trigger.
- A **default prompt** applies when no `/command` is present; `/command`(s) **override** the default.
- **`/commands`**: parse `/command` tokens from the body (reuse the GitHub Phase 99 command-parser model). Commands may differ between the comment context and the auto-triage (report-generation) context — i.e. separate command/prompt sets keyed by context.
- **Multiple distinct `/commands` in one comment ⇒ error reply, no dispatch** (mirror GitHub's ≤1-verb rule).
- **Agent verbs `/claude` `/codex`** (Phase 102 analog): select the agent for the turn; absent ⇒ the box's default agent (`spec.agent.default`). Reserved tokens; composes with template commands.

**Event→prompt mapping (config-driven)**
- km-config.yaml maps an H1 event type (e.g. `report_created`) and/or a `/command` name → a prompt kind/template (a string template; may reference report fields / `{{args}}` like GitHub commands).
- Potentially **different `/command` sets for the comment context vs the auto-triage context** — design the config so a program can declare both an `events:`→prompt map and a `commands:`→prompt map.

**Multi-target fanout (IN SCOPE)**
- One trigger (auto-triage event OR @-handle comment) can fan the SAME prompt out to **multiple sandbox targets** (envs/profiles) at once — `targets:` is a list per program (each target = alias/profile, the GitHub model had exactly one).
- Each target gets its own dispatch + its own **report-id-keyed thread continuity row** (keyed by `report_id` + target, so N targets don't collide).
- **Fanout interaction with `/reply_to_researcher`:** an external researcher-visible reply must NOT be posted N times by N targets. Planner/research must resolve this (candidate: `/reply_to_researcher` is single-target only, or only the primary/first target may post externally). Flagged as a design risk.

**Dispatch (port 3-way from pkg/github/bridge)**
- **Warm** (target alias running): SQS FIFO enqueue to `{prefix}-h1-inbound-{sandbox_id}.fifo` (groupID per report; dedupID `{deliveryGUID}-{groupID}`).
- **Cold** (alias absent): EventBridge `SandboxCreate` with the H1 envelope + profile artifact prefix.
- **Resume** (alias stopped/paused): `StartInstances` + enqueue.
- New `H1Envelope` (analog of `GitHubEnvelope`): `source="hackerone"`, `program`, `report_id`, `kind` (`report_created` | `report_comment_created` | …), `activity_id`, `report_url`, `actor`/`sender`, `body` (extracted prompt), `agent`, `reply_target` flags.

**Thread continuity**
- New `{prefix}-h1-threads` DDB table (analog of `km-github-threads`), keyed by **report id** (+ target). Stores `sandbox_id`, agent session id, `agent_type`. Follow-up comments in a known report bypass the @-handle requirement (analog of GitHub thread-bypass). `agent_type` schema-on-write (no TF migration).

**Reply path (the safety-critical part)**
- **INTERNAL by default.** Every agent reply is a HackerOne **internal** comment unless explicitly marked researcher-visible. This is the default to avoid accidentally messaging external hackers.
- **`/reply_to_researcher`** is the ONLY way to post a researcher-visible (non-internal) reply, and it is **gated by Command + allowlist**: the triggering commenter must be in the program's `allow:` list (deny-by-default, the same authz gate as dispatch). Command-present alone is NOT sufficient.
- `cmd/km-h1 comment` supports BOTH an internal flag and a public/`--reply-to-researcher` flag; **default is internal**. The public path must be explicit at every layer.

**Back-channel (HackerOne customer API — simpler than GitHub)**
- **HTTP Basic Auth** (API username + API token), stored in SSM. NO App-JWT / installation-token dance, NO per-sandbox token refresher (the big GitHub simplification).
- Endpoints: `POST /reports/{id}/comments` (with `internal` flag — NOTE: confirm `/activities` vs `/comments`, see Open Questions), `PATCH /reports/{id}/state`, `GET /reports/{id}`. `cmd/km-h1` subcommands: `comment` / `state` / `read`.

**CLI surface**
- **`km h1 init`**: mint a 32-byte hex webhook secret + store Basic-Auth creds, all in SSM under `/{prefix}/config/h1/*`; print the Function URL + secret for the operator to paste into the HackerOne program's **Webhooks UI** (Engagements → Program → Settings → Automation → Webhooks). NO App-manifest generator (HackerOne has no App-install model — config is per-program in the UI).
- **`km h1 status`**: print SSM-backed H1 config (secret redacted), programs, targets, handle, bridge-url.
- (Optional, planner discretion) `km doctor` checks for the H1 group, dormant when unconfigured.

**Deploy wiring (memory-critical)**
- Add `lambda-h1-bridge` to `regionalModules()` in `init.go` AND a `cmd/km-h1-bridge` entry to `lambdaBuilds()`. Per memory `project_make_build_precedes_km_init`: **`make build` the km binary BEFORE `km init`** when adding a `regionalModules()` entry, or a stale km silently skips the new module. Per `project_new_lambda_needs_live_unit_and_init_list`: the Lambda needs a TF module AND a live terragrunt.hcl unit AND the init.go list entry. Per `project_km_init_skips_existing_lambda_zips`: the build list is hardcoded — a Lambda missing from `lambdaBuilds()` is silently never built.
- Harden the new `{prefix}-h1-inbound-*.fifo` queues with the shared per-install DLQ + RedrivePolicy (memory `project_inbound_poller_fifo_poison_wedge`, resolved Phase 99.1) — reuse the warm helper.

### Claude's Discretion (verbatim)
- Exact `pkg/h1/bridge` file decomposition (mirror `pkg/github/bridge`: interfaces / aws_adapters / resolve / payload / commands / webhook_handler).
- Whether to share or fork the nonce/SQS/EventBridge/resume adapters vs. the GitHub ones (DRY vs. coupling).
- The precise YAML shape of `h1.programs[].events:` and `h1.programs[].commands:` (event→prompt + command→prompt).
- Wave/plan decomposition and TDD test boundaries.
- Whether `km doctor` H1 checks land this phase or a fast-follow.

### Deferred Ideas (OUT OF SCOPE)
- **Federated relay** (one H1 webhook → front-door install → peer bridges; GitHub Phase 100/101 analog). Not needed for HackerOne the way it is for GitHub (per-program webhook URLs). Operator's fanout intent is met by in-scope multi-target dispatch. Revisit only if a true one-webhook-many-installs case appears.
- **Orphan-program helpful reply** (Phase 101 analog).
- **Report attachment** download/upload through the bridge/helper.
- **`km doctor` H1 deep checks** — may land this phase (planner discretion) or as a fast-follow.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| H1-BRIDGE-HMAC | Verify `X-H1-Signature: sha256=<hex>` HMAC-SHA256 of raw body | Reuse `VerifyGitHubSignature` (webhook_handler.go:612) verbatim with header-name swap — scheme is byte-identical to GitHub. Raw-body gotcha: decode base64 Function URL body BEFORE HMAC (main.go:336-344). |
| H1-BRIDGE-DEDUP | Dedup `X-H1-Delivery` GUID in nonces table | Reuse `DynamoGitHubNonceStore` (aws_adapters.go:182) + `CheckAndStore`; new prefix const `H1DeliveryNoncePrefix = "h1-delivery:"`, TTL 86400 (webhook_handler.go:16-22). |
| H1-RESOLVE-PROGRAM | Resolve report's program handle → `{targets[],allow[],events[],commands{}}` | Fork `Resolve()` (resolve.go:51) — routing key is program handle (string) not `owner/repo`; returns `[]target` not single alias. Program handle path: see Open Question 1. |
| H1-TRIGGER-AUTOTRIAGE | Opt-in lifecycle event auto-triage, dormant by default | New: gate on `X-H1-Event` ∈ program.events. No GitHub analog (GitHub is comment-only). Event→prompt map drives the prompt. |
| H1-TRIGGER-MENTION | Configurable `@handle` literal-scan on report_comment_created | Fork `ContainsMention`/`ExtractMentionBody` (resolve.go:96-118) — bot_handle is config string, not SSM bot-login. No "Bot"-type loop guard; skip bot's own comments by actor username. |
| H1-COMMAND-PARSE | `/command` parsing, ≤1 distinct command, override default | Reuse `ParseCommands`/`RunCommandPass`/`ExpandTemplate` (commands.go) near-verbatim. MultiError → reply-no-dispatch already implemented. |
| H1-AGENT-VERB | `/claude` `/codex` per-turn agent select | Reuse Phase 102 agent-verb logic (commands.go:241-252, webhook_handler.go:355-450). Carry to `H1Envelope.Agent`; poller precedence block (userdata.go:2259-2272) ports directly. |
| H1-EVENT-PROMPT-MAP | Config maps event type and/or command → prompt template | New config surface: `programs[].events: {report_created: {prompt: ...}}` + `commands: {...}`. Two parallel maps; `ExpandTemplate` reused for `{{args}}` / field refs. |
| H1-FANOUT-MULTITARGET | One trigger → N sandbox targets | NEW (no GitHub precedent). Loop the 3-way dispatch over `targets[]`; thread-row key = `report_id`+target. See Pattern 4. |
| H1-DISPATCH-3WAY | Warm/cold/resume dispatch per target | Reuse `WebhookHandler` dispatch block (webhook_handler.go:472-582) + adapters; wrap in a per-target loop. SQS/EventBridge/EC2Resume adapters reusable as-is. |
| H1-THREAD-CONTINUITY | `{prefix}-h1-threads` keyed by report id (+target) | Fork `DynamoGitHubThreadStore` (aws_adapters.go:705) + `GitHubThreadStore` iface (interfaces.go:158); PK=report_id, SK=target. Poller session-resume logic ports from userdata.go:2179-2197. |
| H1-REPLY-INTERNAL-DEFAULT | Every reply internal unless explicitly public | NEW safety layer. `km-h1 comment` default `internal:true`; envelope `reply_target` flags; enforce at helper + poller + gate. |
| H1-REPLY-RESEARCHER-GATED | `/reply_to_researcher` gated by command + allowlist | Reuse `CommandAllowed` inner-allow gate (commands.go:406) + outer `isInAllowlist` (webhook_handler.go:640). Public path requires BOTH gates pass. |
| H1-HELPER-KM-H1 | `cmd/km-h1` comment/state/read via Basic Auth | Fork `cmd/km-github/main.go` subcommand dispatch (main.go:75-94); swap App-token loader for SSM Basic-Auth creds; 3 verbs not 4. |
| H1-CLI-INIT-STATUS | `km h1 init`, `km h1 status` | Fork `internal/app/cmd/github.go` (RunGitHubInit/RunGitHubStatus); drop `manifest`; add Basic-Auth cred capture. SSM under `/{prefix}/config/h1/*`. |
| H1-DEPLOY-WIRING | regionalModules + lambdaBuilds + live unit + env block + DLQ + profile | See Deploy Surface section. Cross-references 6 memory footguns. |
| H1-E2E | End-to-end live verification | See Validation Architecture. Live HackerOne program webhook test request + recent-deliveries inspector; byte-identity baseline for userdata. |
</phase_requirements>

---

## Standard Stack

### Core (all already in the repo — this phase adds zero new dependencies)
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-lambda-go` | (repo-pinned) | Lambda Function URL handler (`events.LambdaFunctionURLRequest`) | Already used by km-github-bridge, km-slack-bridge |
| `github.com/aws/aws-sdk-go-v2/*` | (repo-pinned) | DynamoDB, SQS, EventBridge, EC2, SSM clients | Exact same client set as km-github-bridge (main.go:44-49) |
| `crypto/hmac` + `crypto/sha256` (stdlib) | — | HMAC-SHA256 webhook verify | `VerifyGitHubSignature` reuses these verbatim |
| `net/http` (stdlib) | — | HackerOne customer API Basic-Auth client | `cmd/km-h1` uses stdlib http like `cmd/km-github` |
| `github.com/spf13/cobra` + `viper` | (repo-pinned) | `km h1` CLI + `h1:` config unmarshal | Same as `km github` / `GithubConfig` |

### Supporting
| Component | Purpose | When to Use |
|-----------|---------|-------------|
| `pkg/aws/sqs.go` helpers | `GitHubInboundQueueName`/`CreateGitHubInboundQueue`/`GitHubInboundDLQName` | Clone to `H1Inbound*` variants OR parameterize the existing helpers with a "source" arg |
| `pkg/aws/identity.go` `SandboxParameterPath` | SSM param path builder for per-sandbox queue URL | Reuse as-is (`h1-inbound-queue-url` suffix) |
| `pkg/aws/eventbridge.go` `sandboxCreateDetail` | Cold-create event carrier | Extend to carry the H1 envelope (or add an `h1_envelope` field alongside `github_envelope`) |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Fork `pkg/github/bridge` → `pkg/h1/bridge` | A shared `pkg/webhookbridge` generic core | DRY but high-risk refactor of 6 shipped GitHub phases; CONTEXT favors a clean fork (decisions name `pkg/h1/bridge` explicitly). **Recommend fork.** |
| Separate `h1-threads` DDB table | Reuse `km-github-threads` with a source-prefixed key | Reuse couples two products + risks key collision; CONTEXT locks a new `{prefix}-h1-threads` table. **Recommend new table.** |
| New `{prefix}-h1-bridge-nonces` table | Reuse shared `{prefix}-slack-bridge-nonces` | CONTEXT locks reuse of the shared table with `h1-delivery:` prefix — no new infra. **Recommend reuse.** |

**Installation:** No `npm`/`go get` — all dependencies already vendored. New code only.

---

## Architecture Patterns

### Recommended file decomposition (mirror `pkg/github/bridge`)
```
pkg/h1/bridge/
├── interfaces.go        # fork: SecretFetcher, DeliveryNonceStore (REUSE GitHub's),
│                        #       SandboxAliasResolver(+WithStatus), SandboxResumer,
│                        #       EventBridgePublisher, SQSSender, H1ThreadStore,
│                        #       H1Commenter (replaces CommentPoster/Reactor),
│                        #       NO PeerRelayer (federation out of scope)
├── payload.go           # NEW: H1WebhookPayload (data.activity + data.report), H1Envelope
├── resolve.go           # fork: ProgramEntry, Resolve(handle), ContainsHandle/ExtractBody
├── commands.go          # REUSE near-verbatim: ParseCommands, RunCommandPass, ExpandTemplate,
│                        #       agent verbs, + add /reply_to_researcher reserved token
├── aws_adapters.go      # SHARE stateless adapters or thin H1 wrappers (nonce/SQS/EB/EC2);
│                        #       fork DynamoH1ThreadStore, SSM Basic-Auth fetchers, H1Commenter
└── webhook_handler.go   # fork: Handle() — the 11→~10-step flow, per-target fanout loop

cmd/km-h1-bridge/main.go # fork cmd/km-github-bridge/main.go (drop App-cred/JWT, relay, router)
cmd/km-h1/main.go        # fork cmd/km-github/main.go: comment|state|read (Basic Auth)
internal/app/cmd/h1.go   # fork internal/app/cmd/github.go: init|status (no manifest)
internal/app/cmd/create_h1_inbound.go  # fork create_github_inbound.go
infra/modules/lambda-h1-bridge/v1.0.0/ # fork lambda-github-bridge/v1.1.0
infra/live/use1/lambda-h1-bridge/terragrunt.hcl  # fork the live unit
profiles/h1-triage.yaml  # fork profiles/github-review.yaml
```

### Port Map (file-by-file — answers Research Question 1)

| GitHub-bridge file | Disposition | What changes for H1 |
|--------------------|-------------|---------------------|
| `pkg/github/bridge/webhook_handler.go` (31KB, 11-step Handle) | **(b) near-verbatim fork** | Header swaps (`x-h1-*`); drop PR-only filter (step 4) and federation/relay/router (steps 4.5 relay branch); replace single dispatch with per-target fanout loop; add auto-triage branch; loop guard by actor-username not Bot-type. See Handle-flow delta below. |
| `pkg/github/bridge/interfaces.go` | **(b) fork, mostly reuse** | Keep `SecretFetcher`, `DeliveryNonceStore`, `SandboxAliasResolver(+WithStatus)`, `SandboxResumer`, `SandboxStatusWriter`, `EventBridgePublisher`, `SQSSender`. Drop `PeerRelayer`, `BotLoginFetcher`, `GitHubReactor`. Fork `GitHubThreadStore`→`H1ThreadStore` (key=report_id+target). Rename `CommentPoster`→`H1Commenter` (adds `internal bool`). |
| `pkg/github/bridge/aws_adapters.go` (45KB) | **(a) share stateless / (c) fork stateful** | **Reusable as-is (share or trivially wrap):** `DynamoGitHubNonceStore`, `DynamoAliasResolver` (alias-index GSI — identical), `DynamoSandboxStatusWriter`, `GitHubSQSAdapter`, `EventBridgeAdapter`, `EC2Resumer`, `SSMSecretFetcher`. **Fork:** `DynamoGitHubThreadStore` (new key schema), `SSMBotLoginFetcher` (→ config bot_handle, can delete), `InstallationReactor`/`InstallationCommenter` (→ Basic-Auth `H1Commenter`, no JWT). |
| `pkg/github/bridge/resolve.go` | **(b) fork** | `RepoEntry`→`ProgramEntry`; `Resolve(fullName)`→`Resolve(handle)` returning `[]Target`; `defaultAlias("gh-"+...)`→`"h1-"+handle`; `ContainsMention(botLogin)`→`ContainsHandle(botHandle)`. |
| `pkg/github/bridge/payload.go` | **(c) new logic** | `IssueCommentPayload`→`H1WebhookPayload` (data.activity + data.report JSON:API shape); `GitHubEnvelope`→`H1Envelope`. |
| `pkg/github/bridge/commands.go` (21KB) | **(a) reuse near-verbatim** | Add `/reply_to_researcher` as a reserved token alongside `/claude`,`/codex`,`/help`. Everything else (StripCode, ParseCommands, ExpandTemplate, allow gates) is generic. |
| `pkg/github/bridge/relayer.go`, `orphan_reply.go`, `webhook_handler_phase100/101_test.go` | **drop entirely** | Federation + orphan-router are out of scope. |
| `cmd/km-github-bridge/main.go` | **(b) fork** | Drop App-cred read (`readAppCredentials`), JWT reactor, relayer/router env parsing. Add Basic-Auth SSM read for the bridge's own reply path if the bridge ever replies (it does NOT — replies come from the sandbox helper). Keep base64-body + lowercase-headers normalization. |
| `cmd/km-github/main.go` (sandbox helper) | **(b) fork, simpler** | `comment`/`review`/`check`/`pr` (4 verbs) → `comment`/`state`/`read` (3 verbs). Swap GitHub App-token loader for SSM Basic-Auth creds. `comment` gains `--internal` (default true) / `--reply-to-researcher`. |
| `infra/modules/lambda-github-bridge/v1.1.0/{main,variables,outputs}.tf` | **(b) fork** | Drop App-cred/JWT SSM params, peer_bridges, default_router vars. Add Basic-Auth SSM read grant + `h1-threads` table grant + `h1-inbound` SQS grants. Function URL auth=NONE unchanged. |
| `internal/app/cmd/github.go` | **(b) fork** | `RunGitHubInit`/`RunGitHubStatus` → `RunH1Init`/`RunH1Status`. Drop `manifest`. Add Basic-Auth (api-username + api-token) capture. |
| `internal/app/config/config.go` (GithubConfig) | **(b) fork** | New `H1Config`/`H1ProgramEntry`/`H1CommandEntry`. **CRITICAL: add `"h1"` to the merge-list** (config.go:579 region) + `v.UnmarshalKey("h1", &cfg.H1)` (config.go:703 region). |
| `internal/app/cmd/init.go` | **(b) extend** | Add `lambda-h1-bridge` to `regionalModules()` (init.go:327 region) + `cmd/km-h1-bridge` to `lambdaBuilds()` (init.go:2123) + `KM_H1_*` exports to `ExportTerragruntEnvVars` (init.go:1092 region) + H1 profile pre-staging (init.go:343 region) + publish H1 commands to SSM (init.go:617 region). |
| `internal/app/cmd/create_github_inbound.go` | **(b) fork** | `provisionGitHubInboundQueue`→`provisionH1InboundQueue`; same DLQ/RedrivePolicy threading (DLQArn). |
| userdata `km-github-inbound-poller` (`pkg/compiler/userdata.go:2080+`) | **(b) fork** | New `km-h1-inbound-poller` heredoc + systemd unit. Envelope fields rename; preamble teaches `km-h1 comment` (internal default); session/agent-type continuity logic (2179-2272) ports directly; report_id+target key. |
| `profiles/github-review.yaml` | **(b) fork** | `profiles/h1-triage.yaml`: `notification.h1.inbound.enabled: true`; HackerOne API egress suffix (`api.hackerone.com`); lean spot t3.medium 2h/20m-idle. |

### Pattern 1: Handle() flow delta (answers Research Question 2)

Enumerate the GitHub `Handle()` steps (webhook_handler.go:177) in order and what changes:

| # | GitHub step | H1 change |
|---|-------------|-----------|
| 1 | Verify `x-hub-signature-256`, fetch secret from SSM | Header → `x-h1-signature`. Reuse `VerifyGitHubSignature`. Bad/absent → 401; secret-fetch error → 200 (Pitfall 3 unchanged). |
| 1b | `x-github-event != "issue_comment"` → drop | Read `x-h1-event`. ACCEPT `report_comment_created` (comment trigger) AND any event in some program's `events:` (auto-triage). Else 200 drop. |
| 2 | Parse `IssueCommentPayload`; `action != "created"` → drop | Parse `H1WebhookPayload` (data.activity + data.report). No "action" field — the event type IS the discriminator. |
| 3 | Loop guard: `comment.user.type == "Bot"` OR `login == botLogin` → drop | **CHANGE:** no Bot type in HackerOne. Skip the bot's OWN comments by `activity.relationships.actor...username == h1.api_username` (the Basic-Auth identity). Prevents the agent's own internal replies from re-triggering. |
| 4 | PR-only filter (`issue.pull_request == nil` → drop) | **DELETE** — N/A for HackerOne. |
| 4.5 | Resolve `owner/repo` (+ federation relay branch) | Resolve **program handle** → `[]Target`+allow+events+commands. **DELETE federation branch** — no relay; a miss is a silent 200 drop (today's dormant GitHub behavior). |
| 4b | Known-thread bypass (`km-github-threads` lookup) | Lookup `{report_id}` in `h1-threads` (any target row known → bypass handle requirement). |
| 5 | `@bot-login` mention check | **For comment events:** `ContainsHandle(body, program.bot_handle)` (literal config string) unless thread-known. **For auto-triage events:** skip the handle check entirely (event presence in `events:` is the trigger). |
| 6 | Authorize: `sender.login in allow` else 200 silent | `actor username in program.allow` else 200 silent. Deny-by-default. (Auto-triage: actor is the reporter — decide whether auto-triage bypasses allow or requires the reporter be allowed; recommend auto-triage does NOT gate on allow since it is opt-in per-event by the operator.) |
| 7 | Dedupe `x-github-delivery` GUID | `x-h1-delivery` GUID, `h1-delivery:` prefix. |
| 8/9 | Command pass + agent-verb + 3-way dispatch (single target) | Command pass reused (add `/reply_to_researcher` reserved token). **Dispatch wrapped in `for _, target := range targets` fanout loop** (Pattern 4). Each target: warm/cold/resume + per-(report_id,target) thread upsert. |
| 10 | Post 👀 reaction synchronously | **CHANGE:** HackerOne has no comment-reaction API. ACK options: (a) post a one-line internal "🤖 on it" comment (visible only to the team — safe), or (b) no ack at all (the eventual agent reply is the ack). **Recommend (a) internal-only ack** via `H1Commenter` with `internal:true`. Synchronous before return (Pitfall 3). |
| — | Phase 101 claim-emit (`jsonClaim`) | **DELETE** — no federation. |

### Pattern 2: Internal-by-default reply (the safety-critical layer)
**What:** Every reply HackerOne posts is `internal: true` unless an explicit, allowlist-gated `/reply_to_researcher` overrides it.
**Defense in depth (enforce at all 4 layers):**
1. **Config/parse:** `/reply_to_researcher` is a reserved token (commands.go ParseCommands); presence sets `H1Envelope.ReplyToResearcher = true`.
2. **Allowlist gate:** the public path requires BOTH the outer `program.allow` gate (already gating dispatch) AND the inner command gate — `command-present alone is NOT sufficient` (CONTEXT). If actor ∉ allow, strip the public flag and fall back to internal.
3. **Envelope:** `H1Envelope.ReplyTarget` carries the decision to the poller; default zero-value = internal.
4. **Helper:** `km-h1 comment` flag `--internal` defaults **true**; `--reply-to-researcher` must be passed explicitly AND the poller only passes it when the envelope flag is set. The JSON body sends `attributes.internal: true` by default.
**Example (HackerOne customer API body):**
```json
// Source: https://api.hackerone.com/customer-resources/ (POST /reports/{id}/activities)
{ "data": { "type": "activity-comment",
            "attributes": { "message": "...", "internal": true } } }
```

### Pattern 3: Config-driven event→prompt + command→prompt (two parallel maps)
**Recommended YAML (answers Research Question 5):**
```yaml
h1:
  bot_handle: "@km"            # install-wide default; program may override
  default_profile: h1-triage
  programs:
    - handle: acme-corp        # routing key = data.report program handle
      targets:                 # multi-target fanout (each = alias+profile)
        - alias: h1-acme-triage
          profile: h1-triage
        - alias: h1-acme-dupe-check
          profile: h1-triage
      allow: [alice, bob]      # HackerOne usernames, deny-by-default
      bot_handle: "@acmebot"   # optional per-program override
      events:                  # auto-triage map (DORMANT if absent/empty)
        report_created:
          prompt: "Triage new report {{report_id}}: {{title}}. Read it with km-h1 read."
      commands:                # comment-context command map
        dupe:
          description: "Check for duplicates"
          prompt: "Search prior reports for duplicates of {{args}}"
      default_command: ""      # comment with @handle + no /command → free-form
```
- `events:` and `commands:` are **separate maps** (CONTEXT: "different /command sets for the comment context vs the auto-triage context"). The handler picks `events[eventType].prompt` for auto-triage, `RunCommandPass(commands)` for comment-keyword.
- `{{args}}` reuses `ExpandTemplate` (commands.go:357). Report-field refs (`{{report_id}}`, `{{title}}`) are a small extension — either pre-expand in the handler from `data.report.attributes`, or leave to the agent (the preamble already carries report context). **Recommend pre-expanding a small fixed set** (`report_id`, `title`, `state`, `program`) to keep templates terse.

### Pattern 4: Multi-target fanout (answers Research Question 4)
**Design:** wrap the existing single-target dispatch (webhook_handler.go:472-582) in `for _, target := range targets`. Each iteration:
- runs the full 3-way warm/cold/resume against `target.alias`/`target.profile`;
- enqueues with `groupID = "h1-{report_id}-{target.alias}"`, `dedupID = "{deliveryGUID}-{groupID}"` (dedupID must include target so N targets aren't deduped to one);
- upserts a thread row keyed **(report_id, target.alias)** — `h1-threads` PK=report_id, SK=target.alias. N targets ⇒ N rows, no collision (CONTEXT lock).
**`/reply_to_researcher` fanout safety (the flagged risk — RESOLVED):** A researcher-visible reply must be posted **exactly once**. Recommend: **only the FIRST target in `targets[]` is the "primary" and is the only one permitted to post externally.** Concretely:
- The envelope for `targets[0]` carries `ReplyTarget=researcher` (when the command+allow gates pass); every other target's envelope is forced to `ReplyTarget=internal` regardless.
- Internal comments from non-primary targets are safe (team-only) and give the operator the N parallel analyses without spamming the researcher.
- This is simpler and safer than coordination/locking and needs no new infra. Document it: "`/reply_to_researcher` is honored only by the primary (first) target; other targets reply internally."

### Anti-Patterns to Avoid
- **HMAC over the base64 body.** Lambda Function URLs base64-encode bodies; you MUST decode before HMAC (main.go:336-344 does this — port it). Verifying over the encoded body silently fails all signatures.
- **Returning 5xx on internal errors.** HackerOne (like GitHub) retries 5xx with a NEW `X-H1-Delivery` GUID, bypassing dedup → duplicate dispatch. Return 200 on internal errors (Pitfall 3).
- **`internal` defaulting to false anywhere.** A single layer defaulting public = an external message to a hacker. Default true at config struct, envelope zero-value, helper flag, and JSON body.
- **Fanning `/reply_to_researcher` to all targets.** N external replies to the researcher. Primary-target-only (Pattern 4).
- **PutItem on the thread/sandbox row.** Use UpdateItem — `SandboxMetadata lossy round-trip` footgun (interfaces.go:46-56, 173-185).
- **Adding `h1` struct+getter without the merge-list entry.** Silently dropped (`project_config_key_merge_list`).

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| HMAC webhook verify | Custom sig check | `VerifyGitHubSignature` (header-swapped) | Constant-time compare, format validation, battle-tested |
| Command/agent-verb parsing | New parser | `ParseCommands`/`RunCommandPass`/`StripCode`/`ExpandTemplate` (commands.go) | Code-fence stripping, dedup, MultiError, `{{args}}`, reserved tokens all done |
| GUID replay dedup | New table/logic | `DynamoGitHubNonceStore.CheckAndStore` + shared nonces table | Conditional-write atomicity, TTL — no new infra |
| Alias→sandbox resolution + status | New GSI query | `DynamoAliasResolver` / `...WithStatus` | alias-index GSI + status-aware 3-way dispatch already correct |
| FIFO queue + poison protection | New queue plumbing | `CreateGitHubInboundQueue(...,dlqARN)` + shared DLQ + RedrivePolicy | `project_inbound_poller_fifo_poison_wedge` already solved (Phase 99.1) |
| Cold-create dispatch | New EventBridge schema | `EventBridgeAdapter` + `sandboxCreateDetail` | create-handler already drains the carried envelope post-provision |
| EC2 resume | New StartInstances logic | `EC2Resumer` + `DynamoSandboxStatusWriter` | resume-then-enqueue + status write-back already handle the race |

**Key insight:** The entire stateless AWS-adapter layer is product-agnostic. The ONLY genuinely new code is the HackerOne payload parse, the Basic-Auth helper, the two-trigger gate, the event→prompt map, and the fanout loop. Resist re-implementing anything in the table above.

---

## Common Pitfalls (answers Research Question 7)

### Pitfall 1: base64 Function URL body breaks HMAC
**What goes wrong:** All signatures fail verification (→ 401 on every legit webhook).
**Why:** Lambda Function URLs set `IsBase64Encoded`; the raw body bytes you HMAC must be the decoded bytes.
**How to avoid:** Port `decodeBase64Body` + the `IsBase64Encoded` branch (main.go:336-344) and HMAC the DECODED bytes. Add a unit test with a base64-encoded body.
**Warning signs:** "signature mismatch" on the HackerOne webhook "Recent deliveries" inspector despite a correct secret.

### Pitfall 2: 5xx → duplicate dispatch (synchronous-ACK budget)
**What goes wrong:** An SQS/DDB transient error returns 500; HackerOne retries with a new `X-H1-Delivery`; dedup misses; the report is triaged twice.
**Why:** Dedup keys on the delivery GUID, which changes on retry.
**How to avoid:** Return 200 on ALL internal errors (port the `200 Body:"ok"` pattern from every error branch). The webhook ACK budget is short — do the minimum synchronous work (verify, dedupe, enqueue, optional internal ack) and let the poller do the slow agent turn. HackerOne webhook timeout is not documented precisely; treat it like GitHub's ~10s and keep `Handle()` fast. (Open Question 4.)
**Warning signs:** Duplicate internal comments / duplicate agent runs for one report.

### Pitfall 3: internal-vs-public reply accident
**What goes wrong:** An agent reply meant for the team reaches the external researcher.
**Why:** Any single layer defaulting `internal:false`, or fanning `/reply_to_researcher` to all targets, or the bridge's own ACK comment being public.
**How to avoid:** Pattern 2 (4-layer internal-default) + Pattern 4 (primary-target-only external) + internal-only ACK comment. Add a test asserting the helper's default JSON body has `attributes.internal == true` and that a non-allowlisted actor's `/reply_to_researcher` is downgraded to internal.
**Warning signs:** Researcher receives a comment they shouldn't; `internal:false` in a body you didn't intend.

### Pitfall 4: bot's own comment re-triggers the bridge (loop)
**What goes wrong:** The agent posts an internal comment containing the `@handle` (or the program auto-triages on `report_comment_created`), HackerOne fires `report_comment_created`, the bridge dispatches again → infinite loop.
**Why:** No "Bot" actor type in HackerOne to filter on (GitHub's step-3 loop guard relied on it).
**How to avoid:** Skip events where `activity.relationships.actor...username == h1.api_username` (the Basic-Auth identity that the helper posts as). Store/derive the bot's HackerOne username at cold start (from SSM Basic-Auth username). This is the H1 analog of the Bot-type loop guard.
**Warning signs:** A report accrues repeated identical internal comments.

### Pitfall 5: HackerOne API rate limits (429)
**What goes wrong:** Bursty fanout (N targets × read+comment) or a chatty agent hits the write limit → 429.
**Why:** HackerOne write ops are limited (MEDIUM-confidence community figure: ~25 writes / 20s; reads ~600/min, report pages ~300/min). 429 returns on overflow.
**How to avoid:** `km-h1` should backoff/retry on 429 (mirror `km-github`'s 5xx retry). Fanout multiplies API calls — N targets each reading+commenting. Keep auto-triage prompts from spamming comments. Confirm current limits with HackerOne (Open Question 5).
**Warning signs:** HTTP 429 from `km-h1`; missing replies under load.

### Pitfall 6: deploy-surface gaps (see Deploy Surface — bit Phase 97 seven times)
The single highest-risk area. Enumerated below.

---

## Deploy Surface (answers Research Question 6 — CRITICAL)

Cross-referenced against the six memory footguns. Every touchpoint:

| # | Touchpoint | File / location | Footgun if missed |
|---|-----------|-----------------|-------------------|
| 1 | `lambda-h1-bridge` in `regionalModules()` | `init.go:327` region (after `lambda-github-bridge`) | `project_new_lambda_needs_live_unit_and_init_list` — module in `infra/modules/` is invisible to `km init` without this entry |
| 2 | Live terragrunt unit | `infra/live/use1/lambda-h1-bridge/terragrunt.hcl` | same memory — needs module AND live unit AND init.go entry (all three) |
| 3 | `cmd/km-h1-bridge` in `lambdaBuilds()` | `init.go:2123` | `project_km_init_skips_existing_lambda_zips` — hardcoded list; missing → zip never built → `filebase64sha256(missing)` aborts apply under `set -e` |
| 4 | Makefile `build-lambdas` target | `Makefile:235` region | Artifact lockstep (feedback memory #2) — must match `lambdaBuilds()` |
| 5 | `cmd/km-h1` in `sidecarBuilds()` | (mirror `km-github` sidecar entry) | helper binary userdata downloads but `km init` never uploads → 404 → bootstrap aborts (feedback memory #2) |
| 6 | `make build` BEFORE `km init` | runbook | `project_make_build_precedes_km_init` — a stale km binary silently skips the new `regionalModules()` entry → dependents bake mock ARNs (000000000000) → runtime AccessDenied |
| 7 | `KM_H1_*` env-block vars exported | `ExportTerragruntEnvVars` (init.go:1092 region) + terragrunt.hcl `get_env` | env-block change needs **`km init --dry-run=false`, NOT `--sidecars`** (`feedback_km_init_full_apply`, `project_km_init_lambdas_doesnt_deploy`) |
| 8 | `"h1"` in config merge-list + `UnmarshalKey("h1",...)` | `config.go:579` + `:703` regions | `project_config_key_merge_list` — struct+getter alone → yaml silently ignored |
| 9 | SSM params under `/{prefix}/config/h1/*` | `km h1 init` writes; bridge reads | feedback memory #7 — a runtime SSM read is inert unless the deploy path writes it; grep each read back to a writer |
| 10 | Shared DLQ + RedrivePolicy on `{prefix}-h1-inbound-*.fifo` | `sqs-inbound-dlq` module + `create_h1_inbound.go` DLQArn | `project_inbound_poller_fifo_poison_wedge` — poison envelope head-of-line-blocks the FIFO group forever without maxReceiveCount=3 redrive. Add `H1InboundDLQName` + extend the shared DLQ module (or reuse the github DLQ — decide) |
| 11 | H1 profile pre-staging to S3 | `preStageGitHubProfiles` analog (init.go:343 region) | cold-create needs the profile artifact at `{bucket}/h1-profiles/{slug}/.km-profile.yaml` |
| 12 | Publish `h1.commands`+events to SSM | init.go:617 region (`{prefix}/config/h1/commands`) | bridge reads the command set at cold start (SSMCommandsFetcher analog) |
| 13 | IAM grants on the bridge role | `lambda-h1-bridge` main.tf | feedback memory #3 — bridge needs: nonces RW, sandboxes-table read (+alias-index), h1-threads RW, h1-inbound SQS send, EventBridge PutEvents, EC2 StartInstances, SSM read (`/config/h1/*`). |
| 14 | IAM grants on create-handler + ec2spot sandbox role | those modules | create-handler: SQS create/delete/send on `h1-inbound-*`; sandbox role: SQS receive/delete + DDB on h1-threads + SSM read for Basic-Auth creds. |
| 15 | SQS visibility timeout > longest agent turn | `create_h1_inbound.go` / poller `change-message-visibility` | feedback memory #4 — Phase 97 used 1800s after 300s caused dup-review loops; port 1800s. |
| 16 | Existing sandboxes: `km destroy && km create` | runbook | sandboxes need the new `km-h1-inbound-poller` + h1-inbound queue + env vars — a new SandboxProfile field (`notification.h1.inbound`) means `km init --sidecars` to refresh management Lambdas, then recreate boxes. |

**Deploy command sequence (the runbook):**
```
make build                       # CRITICAL: refresh km BEFORE init (footgun #6)
make build-lambdas               # clean rebuild incl. km-h1-bridge zip + km-h1 sidecar
km init --dry-run=false          # new module + live unit + KM_H1_* env block + IAM (NOT --sidecars)
km init --sidecars               # refresh mgmt Lambdas for the new schema field
km h1 init ...                   # mint webhook secret + Basic-Auth creds → SSM; print Function URL
# paste Function URL + secret into HackerOne program Webhooks UI
km destroy <id> && km create profiles/h1-triage.yaml   # existing boxes gain the poller
```

---

## Code Examples (verified from sources)

### HMAC verify (reuse verbatim, header-swapped)
```go
// Source: pkg/github/bridge/webhook_handler.go:612 (VerifyGitHubSignature)
// H1: identical scheme — X-H1-Signature: sha256=<hex(HMAC-SHA256(rawBody, secret))>
func VerifyH1Signature(secret, sigHeader string, rawBody []byte) error {
    if !strings.HasPrefix(sigHeader, "sha256=") {
        return fmt.Errorf("h1-bridge: missing/wrong-format signature %q", sigHeader)
    }
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(rawBody)
    expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
    if !hmac.Equal([]byte(expected), []byte(sigHeader)) {
        return fmt.Errorf("h1-bridge: signature mismatch")
    }
    return nil
}
```

### HackerOne webhook payload (parse target)
```jsonc
// Source: https://api.hackerone.com/webhooks/
// Headers: X-H1-Event, X-H1-Delivery (GUID), X-H1-Signature (sha256=<hex>)
{
  "data": {
    "activity": {
      "type": "activity-comment",            // e.g. for report_comment_created
      "id": "1337",
      "attributes": { "message": "...", "internal": false,
                      "created_at": "...", "updated_at": "..." },
      "relationships": {
        "actor": { "data": { "attributes": { "username": "alice" } } }  // sender
      }
    },
    "report": {
      "id": "2468",
      "type": "report",
      "attributes": { "title": "...", "state": "new", "vulnerability_information": "..." },
      "relationships": {
        "program": { "data": { "attributes": { "handle": "acme-corp" } } }  // ROUTING KEY (confirm path — OQ1)
      }
    }
  }
}
```

### HackerOne customer API reply (internal default)
```bash
# Source: https://api.hackerone.com/customer-resources/  (Basic Auth: api_username:api_token)
# Base URL: https://api.hackerone.com/v1
curl -u "$KM_H1_API_USER:$KM_H1_API_TOKEN" \
  -X POST "https://api.hackerone.com/v1/reports/2468/activities" \
  -H 'Content-Type: application/json' \
  -d '{"data":{"type":"activity-comment","attributes":{"message":"...","internal":true}}}'
# GET report:    GET  /reports/{id}
# change state:  POST /reports/{id}/state_changes  (confirm exact path — OQ2)
```

---

## State of the Art

| Old (GitHub bridge) | New (H1 bridge) | Why different |
|---------------------|-----------------|---------------|
| App JWT → installation token → per-sandbox token refresher Lambda | Static Basic-Auth (api_username:api_token) in SSM | HackerOne has no App-install model; massive simplification — delete the refresher entirely |
| One App = one webhook URL → needs federated relay (Phase 100/101) | Per-program webhook URL → relay NOT needed | HackerOne webhooks are configured per-program in the UI; point each at the owning install directly |
| App manifest generator (`km github manifest`) | None | No App to install — config is per-program UI |
| 👀 reaction ACK | Internal-only "on it" comment (or no ACK) | HackerOne has no comment-reaction API |
| `owner/repo` routing key, single alias | program `handle`, multi-target fanout | The one genuinely new capability |

**Deprecated/outdated for H1:** `BotLoginFetcher` (replaced by config `bot_handle`), `InstallationReactor`, `github-token-refresher` Lambda, `PeerRelayer`/`orphan_reply.go`, the PR-only filter, App-credential SSM params (client-id/private-key/installation-id).

---

## Open Questions

1. **Exact program-handle path in the webhook payload.** Docs confirm `data.report.relationships` exists and `data.report.attributes` carries title/state, but do NOT print the precise program-handle path. Likely `data.report.relationships.program.data.attributes.handle`.
   - What we know: program identification lives in `data.report` relationships (CONTEXT + docs).
   - What's unclear: exact JSON path / whether handle is on `attributes` vs a separate field.
   - **Recommendation:** Wave 0 — fire a HackerOne **Test request** (Webhooks UI has a built-in test + recent-deliveries inspector) against a logging endpoint, capture one real `report_created` and one `report_comment_created` body, and pin the struct tags from the actual bytes. Make the parse tolerant (try `relationships.program.data.attributes.handle`, fall back to any `handle`).

2. **Exact state-change endpoint + body.** Docs show `POST /reports/{id}/activities` for comments and reference state changes, but the precise state-change endpoint is unconfirmed (candidates: `POST /reports/{id}/state_changes` with `type:"state-change"` + `attributes.state`, vs `PATCH /reports/{id}`).
   - **Recommendation:** confirm in `customer-reference` for the live program before implementing `km-h1 state`; it is the least-critical verb (read+comment are the core path) and can ship in a fast-follow if needed.

3. **Does auto-triage gate on `allow`?** The comment trigger gates on the commenter's allowlist membership. For auto-triage (event-driven), the "actor" is the reporter (external). Gating auto-triage on `allow` would block all auto-triage (reporters aren't in `allow`).
   - **Recommendation:** auto-triage is opt-in per-event by the operator (`events:` list) and therefore does NOT gate on `allow` — the operator's choice to list the event IS the authorization. The `allow` gate applies to comment-keyword triggers and to `/reply_to_researcher`. Document explicitly.

4. **HackerOne webhook timeout / retry policy.** Not precisely documented. The "recent deliveries" inspector implies retries on failure.
   - **Recommendation:** treat like GitHub (~10s budget, retry on non-2xx with a new GUID). Keep `Handle()` fast; return 200 on internal error. Confirm during Wave-0 live test.

5. **Current write/read rate limits.** Community sources (MEDIUM confidence): ~25 writes/20s, ~600 reads/min, report pages ~300/min, 429 on overflow.
   - **Recommendation:** implement 429 backoff in `km-h1`; confirm live numbers in `api.hackerone.com/getting-started` for the program before heavy fanout.

---

## Validation Architecture

> nyquist_validation is enabled (config.json `workflow.nyquist_validation: true`).

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` (table-driven), as used across `pkg/github/bridge/*_test.go` |
| Config file | none (Go modules) |
| Quick run command | `go test ./pkg/h1/... ./internal/app/cmd/... -run H1 -count=1` |
| Full suite command | `make test` (or `go test ./... -count=1`) |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| H1-BRIDGE-HMAC | sha256 verify of raw + base64 body; 401 on mismatch | unit | `go test ./pkg/h1/bridge -run TestVerifyH1Signature` | ❌ Wave 0 |
| H1-BRIDGE-DEDUP | replay GUID → 200 no-dispatch | unit (mock nonce store) | `go test ./pkg/h1/bridge -run TestHandle_Dedup` | ❌ Wave 0 |
| H1-RESOLVE-PROGRAM | handle → targets/allow/events/commands; miss → drop | unit (pure, table) | `go test ./pkg/h1/bridge -run TestResolve` | ❌ Wave 0 |
| H1-TRIGGER-AUTOTRIAGE | listed event dispatches; unlisted → drop; dormant when empty | unit | `go test ./pkg/h1/bridge -run TestHandle_AutoTriage` | ❌ Wave 0 |
| H1-TRIGGER-MENTION | `@handle` present → dispatch; absent+unknown-thread → drop; thread-bypass | unit | `go test ./pkg/h1/bridge -run TestHandle_Mention` | ❌ Wave 0 |
| H1-COMMAND-PARSE | ≤1 distinct command; MultiError → reply-no-dispatch | unit | `go test ./pkg/h1/bridge -run TestParseCommands` | partial (port GitHub) |
| H1-AGENT-VERB | `/claude`/`/codex` select; conflict → reply; carried to envelope | unit | `go test ./pkg/h1/bridge -run TestAgentVerb` | partial (port GitHub) |
| H1-EVENT-PROMPT-MAP | event→prompt + command→prompt expansion incl `{{args}}`/field refs | unit | `go test ./pkg/h1/bridge -run TestExpandTemplate` | partial |
| H1-FANOUT-MULTITARGET | N targets → N enqueues + N thread rows; distinct dedupIDs | unit (mock SQS/threads) | `go test ./pkg/h1/bridge -run TestHandle_Fanout` | ❌ Wave 0 |
| H1-DISPATCH-3WAY | warm/cold/resume per target | unit (mock resolver-with-status) | `go test ./pkg/h1/bridge -run TestHandle_Dispatch` | ❌ Wave 0 |
| H1-THREAD-CONTINUITY | (report_id,target) upsert; poller resumes session | unit + manual (poller bash) | `go test ./pkg/h1/bridge -run TestThreadStore` | ❌ Wave 0 |
| H1-REPLY-INTERNAL-DEFAULT | helper default body `internal:true`; envelope zero=internal | unit | `go test ./cmd/km-h1 -run TestCommentInternalDefault` | ❌ Wave 0 |
| H1-REPLY-RESEARCHER-GATED | public only when actor∈allow AND `/reply_to_researcher`; else downgraded | unit | `go test ./pkg/h1/bridge -run TestReplyGate` | ❌ Wave 0 |
| H1-HELPER-KM-H1 | comment/state/read build correct Basic-Auth requests; 429 retry | unit (httptest server) | `go test ./cmd/km-h1 -run TestHelper` | ❌ Wave 0 |
| H1-CLI-INIT-STATUS | init writes SSM `/config/h1/*`; status redacts secret | unit (mock SSM) | `go test ./internal/app/cmd -run TestH1Init` | ❌ Wave 0 |
| H1-DEPLOY-WIRING | guard tests: h1-bridge in lambdaBuilds + LambdaBuildNames; km-h1 in sidecarBuilds; `h1` in merge-list | unit | `go test ./internal/app/cmd -run TestLambdaBuilds` + config load test | ❌ Wave 0 (mirror `TestLambdaBuildsIncludesGitHubBridge`) |
| H1-DEPLOY-WIRING | userdata byte-identity: an unrelated profile renders IDENTICAL userdata pre/post (dormancy) | golden | `go test ./pkg/compiler -run ByteIdentity` | mirror `userdata_phase92_byte_identity_test.go` |
| H1-E2E | live HackerOne program: test webhook → internal triage comment; comment `@handle /command` → reply | E2E (live, gated) | `RUN_H1_E2E=1 go test ./test/e2e/h1/...` | ❌ Wave 0 (manual UAT acceptable) |

### Mockable vs needs-live
- **Fully mockable (unit):** HMAC, dedup, resolve, fanout, dispatch (mock SQS/EventBridge/EC2/threads adapters — same interface-injection pattern as `pkg/github/bridge/*_test.go`), command/agent-verb parse, reply-gate logic, helper HTTP (httptest), CLI init/status (mock SSM).
- **Needs live HackerOne (E2E/UAT):** exact payload-shape confirmation (Wave 0 capture), webhook delivery + signature acceptance in the Recent-Deliveries inspector, internal-vs-public reply visibility, state-change endpoint, rate-limit behavior. Treat as documented UAT (gated `RUN_H1_E2E=1`), per the Phase 97/SLCK-09 precedent.
- **Byte-identity baseline (compiler/userdata):** capture a golden of an unrelated profile's userdata BEFORE adding the H1 poller block, assert it renders byte-identical AFTER (dormancy invariant — the new poller heredoc must only appear when `notification.h1.inbound.enabled:true`). Mirror `pkg/compiler/userdata_phase92_byte_identity_test.go` (capture path + verify path drive identical inputs).

### Sampling Rate
- **Per task commit:** `go test ./pkg/h1/... ./cmd/km-h1/... ./internal/app/cmd/... -run H1 -count=1`
- **Per wave merge:** `make test` (full suite green)
- **Phase gate:** full suite green + the deploy-surface guard tests pass + Wave-0 live payload capture done, before `/gsd:verify-work`.

### Wave 0 Gaps
- [ ] `pkg/h1/bridge/webhook_handler_test.go` — HMAC, dedup, two-trigger, fanout, dispatch, reply-gate (port from `pkg/github/bridge/*_test.go`)
- [ ] `pkg/h1/bridge/resolve_test.go` — program-handle resolution table
- [ ] `pkg/h1/bridge/commands_test.go` — command/agent-verb + `/reply_to_researcher` reserved token (port)
- [ ] `pkg/h1/bridge/payload_test.go` — parse a CAPTURED real webhook body (Wave 0 live capture is a prerequisite)
- [ ] `cmd/km-h1/main_test.go` — Basic-Auth request shape, internal-default, 429 retry (httptest)
- [ ] `internal/app/cmd/h1_test.go` — init/status SSM
- [ ] `internal/app/cmd/init_test.go` additions — guard tests (h1-bridge in lambdaBuilds, km-h1 in sidecarBuilds, `h1` in merge-list, h1-bridge in regionalModules)
- [ ] `pkg/compiler/userdata_h1_byte_identity_test.go` — dormancy golden
- [ ] **Live capture (Wave 0, blocking Open Question 1/2):** one real `report_created` + one `report_comment_created` body via the HackerOne Webhooks Test request → pin payload struct tags.

---

## Sources

### Primary (HIGH confidence)
- In-repo (read directly): `pkg/github/bridge/{webhook_handler,interfaces,resolve,payload,commands}.go`, `cmd/km-github-bridge/main.go`, `cmd/km-github/main.go` (structure), `internal/app/cmd/{github,init}.go`, `internal/app/cmd/create_github_inbound.go`, `internal/app/config/config.go`, `infra/modules/lambda-github-bridge/v1.1.0/variables.tf`, `pkg/compiler/userdata.go` (github-inbound poller 2080-2300), `profiles/github-review.yaml`, `pkg/compiler/userdata_phase92_byte_identity_test.go`, `pkg/aws/sqs.go` + `identity.go` helper signatures.
- Memory files: `feedback_verify_deploy_surface_not_just_code`, `project_config_key_merge_list`, `project_new_lambda_needs_live_unit_and_init_list`, `project_make_build_precedes_km_init`, `project_km_init_skips_existing_lambda_zips`, `project_inbound_poller_fifo_poison_wedge`.
- HackerOne webhook spec — headers (`X-H1-Event`/`X-H1-Delivery`/`X-H1-Signature`), HMAC-SHA256 `sha256=<hex>`, event list, `data.activity`+`data.report` shape, actor username path: https://api.hackerone.com/webhooks/
- HackerOne customer API — Basic Auth, base URL `https://api.hackerone.com/v1`, `POST /reports/{id}/activities` with `attributes.internal`, `GET /reports/{id}`: https://api.hackerone.com/customer-resources/

### Secondary (MEDIUM confidence)
- HackerOne rate limits (~25 writes/20s, ~600 reads/min, 429 on overflow): WebSearch aggregate of api.hackerone.com getting-started — confirm live.
- Webhooks UI location (Engagements → Program → Settings → Automation → Webhooks; Test request + Recent deliveries): https://docs.hackerone.com/en/articles/8588351-webhooks (CONTEXT-corroborated).

### Tertiary (LOW confidence — flagged in Open Questions)
- Exact program-handle JSON path (likely `data.report.relationships.program.data.attributes.handle`) — NOT printed in docs; confirm via Wave-0 live capture.
- Exact state-change endpoint (`POST /reports/{id}/state_changes` vs `PATCH /reports/{id}`) — confirm in customer-reference for the live program.

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — zero new deps; every analog adapter read in-repo.
- Architecture / port map: HIGH — every GitHub-bridge source file read and characterized; the delta is mechanical except fanout (clearly specified) and the reply guard (clearly specified).
- HackerOne API surface: MEDIUM — headers/HMAC/auth/comment-endpoint/internal-flag are HIGH (docs-confirmed); program-handle path + state endpoint + exact rate limits + webhook timeout are MEDIUM/LOW (flagged, Wave-0 capture resolves).
- Deploy surface: HIGH — enumerated against six concrete memory footguns + the Phase-97 7-gap post-mortem.
- Pitfalls: HIGH — derived from shipped GitHub-bridge code comments + the deploy-surface feedback memory.

**Research date:** 2026-06-09
**Valid until:** ~2026-07-09 for the in-repo port map (stable); ~2026-06-23 for HackerOne API specifics (verify the two LOW-confidence paths against a live program before/at Wave 0).
