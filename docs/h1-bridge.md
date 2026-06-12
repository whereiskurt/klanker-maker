# HackerOne Bridge Guide

The **HackerOne comment-trigger bridge** (`km-h1-bridge`, Phase 103) lets a HackerOne
program webhook drive a sandbox agent turn the same way the GitHub bridge (Phases 97–102)
turns a PR comment into an agent run. A single Lambda Function URL HMAC-verifies the
incoming webhook, dedupes it, resolves the report's **program handle** to one-or-more
sandbox targets, and dispatches an agent turn. The agent reads the report and posts back
through the `km-h1` helper using the HackerOne customer API.

It is the direct analog of `docs/github-bridge.md`; this guide only documents the
HackerOne-specific surface. Where behavior matches the GitHub bridge, that is called out.

> **Status (Phase 103):** code-complete and goal-verified (16/17 requirements). The one
> remaining item is the live reply-visibility UAT against a HackerOne **Sandbox** program
> (see [UAT](#uat)). Federated relay (the GitHub Phase 100/101 analog) is **out of scope** —
> each HackerOne program's webhook points directly at one install's Function URL.

---

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Webhook signature & headers](#webhook-signature--headers)
- [Config surface — `h1.programs`](#config-surface--h1programs)
- [Trigger models](#trigger-models)
- [Commands & agent verbs](#commands--agent-verbs)
- [Reply visibility (safety-critical)](#reply-visibility-safety-critical)
- [Multi-target fanout](#multi-target-fanout)
- [The h1-triage profile](#the-h1-triage-profile)
- [CLI reference](#cli-reference)
- [Deploy sequence](#deploy-sequence)
- [Dormant invariant](#dormant-invariant)
- [Thread continuity](#thread-continuity)
- [UAT](#uat)
- [Troubleshooting](#troubleshooting)
- [Out of scope](#out-of-scope)

---

## Overview

| | |
|---|---|
| **Trigger** | A HackerOne program webhook (`report_created`, `report_comment_created`, …) |
| **Auth in** | HMAC-SHA256 of the raw body, `X-H1-Signature: sha256=<hex>` (same scheme as GitHub's `X-Hub-Signature-256`) |
| **Auth back** | HackerOne customer API **HTTP Basic Auth** (API username + token) — no App-JWT, no token refresher |
| **Routing key** | the report's **program handle** (`report.relationships.program.data.attributes.handle`) |
| **Dispatch** | warm FIFO enqueue / cold EventBridge `SandboxCreate` / resume — per target |
| **Reply** | `km-h1 comment` → `POST /reports/{id}/activities`, **internal by default** |
| **Activation** | add an `h1:` block to `km-config.yaml` — absent ⇒ bridge is dormant |

Two trigger models, both opt-in:

1. **Auto-triage** — fire on the lifecycle events a program lists (e.g. `report_created`).
2. **Comment-keyword** — fire on `report_comment_created` when the comment contains the
   configured `bot_handle` (e.g. `@km`). HackerOne internal comments have no bot user to
   @-mention, so the handle is a literal substring match.

---

## Architecture

```
HackerOne program webhook ──POST──▶  km-h1-bridge Lambda (Function URL, auth NONE)
                                       │  1. HMAC-verify X-H1-Signature over raw bytes
                                       │  2. dedupe X-H1-Delivery (nonces table)
                                       │  3. parse data.activity + data.report
                                       │  4. Resolve(program handle) → targets/allow/events/commands
                                       │  5. trigger gate (auto-triage event | @handle comment)
                                       │  6. authorize sender (allow list, deny-by-default)
                                       │  7. parse /commands + /claude /codex + /reply_to_researcher
                                       │  8. fan out to N targets:
                                       ▼
            warm  → SQS  {prefix}-h1-inbound-{sandbox}.fifo  (DLQ-protected)
            cold  → EventBridge SandboxCreate
            resume→ EC2 StartInstances + enqueue
                                       │
                                       ▼
            sandbox: km-h1-inbound-poller (userdata) → agent turn (claude|codex)
                                       │
                                       ▼
            km-h1 comment → POST /reports/{id}/activities  (internal by default)
```

Source map (ported from `pkg/github/bridge`):

| Component | Path |
|---|---|
| Lambda entry | `cmd/km-h1-bridge/main.go` |
| Handle() flow + adapters | `pkg/h1/bridge/webhook_handler.go`, `aws_adapters.go` |
| Resolve / payload / commands | `pkg/h1/bridge/{resolve,payload,commands}.go` |
| Sandbox helper | `cmd/km-h1/main.go` |
| Terraform module | `infra/modules/lambda-h1-bridge/v1.0.0/` |
| Continuity table | `infra/.../dynamodb-h1-threads` (`{prefix}-h1-threads`, PK `report_id`, SK `target`) |
| CLI | `internal/app/cmd/h1.go` |
| Config | `internal/app/config/config.go` (`H1Config`/`H1ProgramEntry`/…) |
| Poller | `pkg/compiler/userdata.go` (`km-h1-inbound-poller`, gated on `notification.h1.inbound.enabled`) |
| Profile | `profiles/h1-triage.yaml` |

---

## Webhook signature & headers

HackerOne delivers an HTTP POST with:

| Header | Use |
|---|---|
| `X-H1-Signature` | `sha256=<hexdigest>` — HMAC-SHA256 of the **raw** request body, key = the program's webhook secret. Verified constant-time. Bad/absent ⇒ **401**; internal error ⇒ **200** (don't make HackerOne retry on our bug). |
| `X-H1-Delivery` | GUID — deduped in the shared `{prefix}-slack-bridge-nonces` table under the `h1-delivery:` prefix (24h TTL). |
| `X-H1-Event` | event name (e.g. `report_created`). |

Payload shape: top-level `data` → `data.activity` (event metadata, actor, `internal` flag,
`message`) + `data.report` (full report incl. `id` and the program relationship). The parser
is **wrapper-tolerant** (single-`data` and JSON:API double-`data`) and fails safe when the
program handle is absent.

> **Field-path provenance:** the report-object inner paths (`relationships.program…handle`,
> `activity.attributes.internal`, `activity.relationships.actor…username`, `attributes.message`)
> were confirmed against the live customer API. The exact webhook **envelope wrapper** is
> pinned during the live UAT capture; until then the parse is intentionally tolerant.

---

## Config surface — `h1.programs`

Add an `h1:` block to `km-config.yaml`. Absent/empty ⇒ `KM_H1_PROGRAMS` unset ⇒ the bridge
silent-drops every event (byte-identical to a pre-Phase-103 install).

```yaml
h1:
    # Install-wide comment trigger token (literal substring match in the comment body).
    bot_handle: "@km"
    # Fallback SandboxProfile when a target sets no profile.
    default_profile: profiles/h1-triage.yaml
    programs:
        - handle: prodsec_klanker_maker_test_h1b   # routing key — exact match, deny-by-default
          # HackerOne usernames allowed to TRIGGER via comment AND to use
          # /reply_to_researcher. Deny-by-default.
          allow:
            - your-hackerone-username
          # bot_handle: "@km"                       # optional per-program override
          targets:                                  # multi-target fanout — one trigger → N sandboxes
            - alias: h1-test-a
              profile: profiles/h1-triage.yaml
            # - alias: h1-test-b                     # uncomment to exercise fanout
            #   profile: profiles/h1-triage.yaml
          # AUTO-TRIAGE map. Present ⇒ opt-in on those lifecycle events. Remove ⇒
          # comment-keyword-only (auto-triage dormant).
          events:
            report_created:
                prompt: '@profiles/h1.report_created.prompt.txt'
          # COMMENT-context /commands. /claude, /codex, /reply_to_researcher are RESERVED
          # built-ins — do not redefine them here.
          commands:
            triage:
                description: Re-run triage on this report (internal)
                prompt: '@profiles/h1.triage.prompt.txt'
            summarize:
                description: Summarize the report and its activity (internal)
                prompt: '@profiles/h1.summarize.prompt.txt'
          # Dispatched when a comment carries the bot handle but no /command. Names a Commands key.
          default_command: triage
```

| Field | Meaning |
|---|---|
| `bot_handle` | comment trigger token; per-program override allowed |
| `default_profile` | fallback profile for cold create |
| `programs[].handle` | program handle, the deny-by-default routing key |
| `programs[].targets[]` | `{alias, profile}` fanout list (each = one sandbox + its own thread row) |
| `programs[].allow[]` | HackerOne usernames permitted to trigger + use `/reply_to_researcher` |
| `programs[].events{}` | event-type → prompt (auto-triage); absent ⇒ dormant |
| `programs[].commands{}` | `/command` name → `{description, prompt}` |
| `programs[].default_command` | command dispatched on a bare-handle comment |

### `@file` prompts

Both `events[*].prompt` and `commands[*].prompt` accept the `@file` convention:

- `@profiles/h1.triage.prompt.txt` — explicit path (relative to `km-config.yaml`).
- `@h1.triage.prompt.txt` — bare name resolves to `profiles/h1.triage.prompt.txt`.
- `@@literal` — escape: yields a literal leading `@`, no file read.

`@file` content is **inlined at `km init` time** (the Lambda has no filesystem) — command
prompts into SSM, event prompts into `KM_H1_PROGRAMS`. A missing `@file` is a **hard error**
at `km init`. Prompts may reference `{{report_id}}`, `{{title}}`, `{{state}}`, `{{program}}`,
and `{{args}}`.

---

## Trigger models

| Model | Fires on | Authorization | Default posture |
|---|---|---|---|
| **Auto-triage** | the lifecycle events in `events:` | event-listing is the opt-in (no `allow` gate) | **dormant** — empty/absent `events:` ⇒ off |
| **Comment-keyword** | `report_comment_created` containing `bot_handle` | `allow:` list, deny-by-default | active whenever `bot_handle` matches |

A comment in a **known thread** (a report the bridge has already dispatched into) bypasses
the handle requirement — follow-up comments continue the conversation (GitHub thread-bypass
analog).

---

## Commands & agent verbs

Parsed from the comment body (GitHub Phase 99/102 parser, ported):

- **`/command`** — runs the program's `commands[name].prompt`. **Exactly one** distinct
  command per comment; two distinct commands ⇒ error reply, no dispatch. No command ⇒
  `default_command` (or a free-form prompt).
- **`/claude` / `/codex`** — select the agent for the turn (reserved built-ins). Absent ⇒
  the sandbox's default agent (`spec.agent.default`). `/codex` needs a Codex-capable profile.
- **`/reply_to_researcher`** — reserved built-in; see below. Composes with a `/command`.

---

## Reply visibility (safety-critical)

A HackerOne comment is either **internal** (team-only) or **researcher-visible** (the hacker
sees it). The bridge is **internal by default at every layer** to prevent accidentally
messaging researchers:

1. **Envelope** — `ReplyToResearcher` zero-value = internal.
2. **Handler gate** — `ComputeReplyToResearcher` returns researcher-visible **only** when the
   triggering comment carries `/reply_to_researcher` **AND** the actor is in the program's
   `allow:` list. Command present but actor not allowlisted ⇒ **downgraded to internal**
   (never silently sent externally).
3. **Helper** — `km-h1 comment` posts `internal: true` unless `--reply-to-researcher` is
   passed; the default is enforced at the JSON-marshalling layer.
4. **Poller** — the agent is only told it *may* reply externally when the envelope flag is set.

Under [fanout](#multi-target-fanout), **only the primary target (`targets[0]`) may post a
researcher-visible reply** — N targets never produce N external replies.

> ⚠️ A non-allowlisted `/reply_to_researcher` that produces a researcher-visible reply is a
> **P0 safety bug**. UAT step 5 exists to confirm it is blocked/downgraded.

---

## Multi-target fanout

`programs[].targets` is a list — one trigger fans the **same** prompt to every target. Each
target:

- dispatches independently (warm/cold/resume) with a distinct SQS dedup ID
  (`{delivery-guid}-h1-{report_id}-{alias}`),
- gets its own `{prefix}-h1-threads` continuity row keyed `(report_id, target)` — so N
  targets never collide,
- posts its own **internal** analysis; only `targets[0]` may reply externally.

This is the HackerOne-specific capability with no GitHub precedent ("fan out to other envs").

---

## The h1-triage profile

`profiles/h1-triage.yaml` — a lean inbound profile (mirror of `github-review.yaml`):

- spot `t3.medium`, short TTL/idle,
- `sourceAccess: none`, network allowlist includes `api.hackerone.com`,
- `notification.h1.inbound.enabled: true` (provisions the per-sandbox `h1-inbound` FIFO queue
  + the `km-h1-inbound-poller`),
- `apiVersion: klankermaker.ai/v1alpha2`.

`notification.h1.inbound.enabled` defaults **false** everywhere else — that is the dormancy
gate (see below).

---

## CLI reference

### `km h1 init`

Mints a 32-byte hex webhook secret, captures the Basic-Auth creds, and writes SSM
(SecureString for secret + token):

```
/{prefix}/config/h1/webhook-secret   # the webhook HMAC secret (paste into H1 UI)
/{prefix}/config/h1/api-username     # customer-API Basic Auth username
/{prefix}/config/h1/api-token        # customer-API Basic Auth token (the bridge + km-h1 read this)
/{prefix}/config/h1/bridge-url       # the Lambda Function URL (informational)
```

Prints the **Function URL + webhook secret** to paste into the program's HackerOne Webhooks
UI. There is **no App manifest** — HackerOne has no App-install model; webhooks are configured
per-program in the UI (Engagements → Program → Settings → Automation → Webhooks).

### `km h1 status`

Prints the SSM-backed config (webhook secret + API token **redacted**) plus the parsed
`h1.programs` (handle, targets, allow, events, commands, default_command). Dormant-safe.

### `km-h1` (sandbox-side helper)

Runs inside the sandbox; reads Basic-Auth creds from `KM_H1_API_USER`/`KM_H1_API_TOKEN` or SSM
`/{prefix}/config/h1/api-{username,token}`. Has 429/5xx backoff.

```bash
# Post a comment — INTERNAL by default
km-h1 comment --report 12345 --body @analysis.md
# Post a RESEARCHER-VISIBLE reply (explicit; gated upstream by allowlist)
km-h1 comment --report 12345 --body @reply.md --reply-to-researcher
# Read a report as JSON
km-h1 read --report 12345
# Change report state (best-effort; KM_H1_STATE_ENDPOINT override available)
km-h1 state --report 12345 --state triaged
```

---

## Deploy sequence

```bash
# 0. Configure km-config.yaml: add the h1: block (see Config surface) and set allow:
#    to your real HackerOne username.

# 1. Build the km binary FIRST — it adds a regionalModules() entry; a stale km
#    silently skips the new module (memory: project_make_build_precedes_km_init).
make build

# 2. CLEAN rebuild of all Lambda zips (km-h1-bridge is in the hardcoded lambdaBuilds()
#    list; make build alone does NOT rebuild it — memory: project_km_init_skips_existing_lambda_zips).
make build-lambdas

# 3. Full apply — new module (lambda-h1-bridge + dynamodb-h1-threads), KM_H1_* env block, IAM.
#    NOT --sidecars: env-block changes require a full apply (memory: feedback_km_init_full_apply).
km init --dry-run=false

# 4. Deploy the notification.h1.inbound schema field so create-handler accepts h1-triage.
km init --sidecars

# 5. Mint the webhook secret + store Basic-Auth creds in SSM; prints the Function URL.
km h1 init --api-username <user> --api-token <token>

# 6. In the HackerOne program's Webhooks UI, set Payload URL = the Function URL and
#    Secret = the printed webhook secret; subscribe to report_created + report_comment_created
#    (or "send everything"). Click "Test request" → expect 200 in Recent Deliveries.

# 7. Create a sandbox (or let auto-triage cold-create one on first event).
km create profiles/h1-triage.yaml
```

Existing sandboxes need `km destroy && km create` to gain the `h1-inbound` queue + poller.

> **Phase 105 scoped shortcut (config-key-only edits after initial deploy):**
> for a pure `h1.*` config key change (e.g. adding a program, updating `h1.allow`,
> changing event prompts) with no code change needed:
> ```bash
> km init --h1 --dry-run=false   # applies lambda-h1-bridge only (env+IAM)
> ```
> Scoped apply refreshes the env block + IAM in seconds. It does NOT rebuild the code
> zip (still `make build-lambdas` + full `km init`) and does NOT provision new resources.
> A subsequent `km init --plan` shows the bridge as a no-op (zero drift).

---

## Dormant invariant

With no `h1:` block in `km-config.yaml` and `notification.h1.inbound.enabled` false (the
default):

- `KM_H1_PROGRAMS` is unset ⇒ the bridge logs "dormant" and silent-drops every event;
- no H1 poller heredoc renders in userdata — guarded by `TestUserdataH1ByteIdentity`, which
  asserts an H1-free profile's userdata is **byte-identical** before/after Phase 103;
- `km doctor` and existing behavior are unaffected.

The bridge Lambda + module are deployed regardless (so the Function URL exists for `km h1
init`), but they do nothing until a program is configured.

---

## Thread continuity

`{prefix}-h1-threads` (PK `report_id`, SK `target`) stores `{sandbox_id, agent_session_id,
agent_type}` per `(report, target)`. The first dispatch creates the row; follow-up comments in
that report bypass the handle requirement and resume the same agent session. `agent_type` is
schema-on-write (no migration). DDB writes use `UpdateItem` (not full-row `PutItem`) to avoid
the lossy-round-trip footgun (memory: `project_sandboxmetadata_lossy_roundtrip`).

---

## UAT

Run against a **free HackerOne Sandbox program** (create at
<https://hackerone.com/teams/new/sandbox>) — almost all features, you submit your own
fake/test reports, full webhooks + API, zero contact with real researchers. **Never run the
comment-posting UAT against a production program.**

After the [deploy sequence](#deploy-sequence):

1. **Webhook accepted** — fire "Test request" → 200 in Recent Deliveries.
2. **Auto-triage** — submit a test report → an **internal** triage comment appears (not
   researcher-visible).
3. **Comment-keyword** — comment `@km /triage` as an allowlisted user → a reply.
4. **Allowlisted external** — `@km /reply_to_researcher …` as an allowlisted user → a
   researcher-visible reply, posted **exactly once** even with multiple targets.
5. **Non-allowlisted external** — `@km /reply_to_researcher …` as a non-allowlisted user →
   **downgraded to internal / blocked** (researcher-visible here = P0 bug).
6. **Loop guard** — the bridge does not react to its own comments; no repeated identical
   internal comments.

A `RUN_H1_E2E=1`-gated harness lives at `test/e2e/h1/e2e_test.go` (skips clean when unset).

---

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Webhook shows non-200 in Recent Deliveries | secret mismatch (re-run `km h1 init`, re-paste) or malformed body — check Lambda logs |
| Event accepted (200) but no sandbox/agent | program not in `h1.programs`, or comment-keyword without `bot_handle`, or auto-triage event not in `events:` (dormant) |
| `@km` comment ignored | commenter not in the program's `allow:` list (deny-by-default) |
| Agent prompt literally contains `@profiles/…txt` | event prompt not inlined — re-run `km init` (the exporter inlines `@file`; a missing file hard-errors) |
| Researcher saw a reply unexpectedly | check the `allow:` list + that the comment really carried `/reply_to_researcher`; verify only `targets[0]` replied externally |
| `KM_H1_PROGRAMS` drift WARN at `km init` | a shell `KM_H1_PROGRAMS` env var overrides `km-config.yaml`; unset it (yaml wins by default) |
| Remote `km create profiles/h1-triage.yaml` rejects the schema | `km init --sidecars` not run after deploy (the `notification.h1.inbound` field) |

> `km doctor` does not yet have HackerOne checks — a candidate fast-follow (mirroring the
> Slack/GitHub doctor groups).

---

## Out of scope

- **Federated relay** (one webhook → front-door → peer bridges; GitHub Phase 100/101 analog) —
  unnecessary for HackerOne, which lets each program target a specific install's Function URL
  directly. The operator "fan out" need is met by [multi-target fanout](#multi-target-fanout).
- **Orphan-program helpful reply** (Phase 101 analog).
- **Report attachment** download/upload through the bridge/helper.
- **`km doctor` HackerOne checks.**

---

## Phase 106 — Resume-hint on bridge replies (post-on-mint)

> **Phase 106 (2026-06-11) — Session-resume hint on HackerOne bridge replies (post-on-mint).**
>
> After a bridge agent turn completes, the sandbox-side H1 poller posts ONE additional
> collapsed `<details>` comment carrying the operator resume handle. The hint fires only
> when the session id is new or changed (post-on-mint semantics), so a stable thread
> produces exactly one hint comment — typically on the first turn.

### INTERNAL-only safety property

The hint is posted via `km-h1 comment --report "$REPORT_ID" --body "$HINT_BODY"` — the
**bare form, without `--reply-to-researcher`**. `km-h1 comment` defaults `internal:true`,
which means the activity is posted on the **internal / team** comment track in HackerOne.

**The hint is never visible to the external researcher.** This is a hard safety property:
the hint contains the sandbox id and the session id; exposing either to a researcher would
be a security boundary violation. The `--reply-to-researcher` flag must not be added to
the hint call.

### What the hint contains

The collapsed `<details>` fold includes:

- **Sandbox id** (`$SANDBOX_ID`) — for internal context.
- **Run-from directory: `/workspace`** — the session transcript lives at
  `/home/sandbox/.claude/projects/-workspace/<session-id>.jsonl`, but `--resume` keys
  off the current working directory, so the resume command **must** be run from
  `/workspace` (not `/home/sandbox`).
- **Agent-correct resume command** — branched on `EFFECTIVE_AGENT`:
  - Claude: `claude --resume <session-id>`
  - Codex: `codex exec resume <session-id>`
- **The minted session id** — the freshly issued or resumed id from this turn.

### Post-on-mint semantics

The hint block is gated by a comparison of `NEW_H1_SESSION` against the previously
stored session id (`${H1_SESSION:-}`). It fires only when the value is non-empty
**and** differs from the stored value — i.e., on:

1. **First turn** — no stored session id; `NEW_H1_SESSION` is always new.
2. **Gap-E cross-box re-mint** — a new sandbox was cold-created for the same alias;
   the session id changes.

Common case: exactly **one** hint comment per thread. Subsequent turns on the same
sandbox produce no additional hint.

### Robustness

The hint post is best-effort — it runs as:

```bash
/opt/km/bin/km-h1 comment --report "$REPORT_ID" --body "$HINT_BODY" || true
```

The `|| true` guard ensures a transient API error never blocks the SQS ack.

### Deploy surface

Phase 106 is a `pkg/compiler/userdata.go` change embedded in the **create-handler
Lambda zip**. There are no new Terraform resources, no SandboxProfile schema changes,
no new DDB columns, and no changes to bridge Lambdas or IAM.

```bash
# 1. Rebuild the create-handler Lambda zip (userdata.go is embedded here).
#    NOT --sidecars (--sidecars only re-uploads sidecar binaries, not the create-handler zip).
#    NOT km init --h1 (that refreshes bridge env+IAM only, not the create-handler zip).
make build-lambdas

# 2. Full terragrunt apply — uploads the new create-handler zip.
km init --dry-run=false

# 3. Existing sandboxes must be recreated to gain the new poller.
km destroy <sandbox-id> --remote --yes
km create profiles/h1-triage.yaml --alias h1-myprogram

# 4. Verify.
km doctor
```

**Bridge Lambdas / IAM / Terraform:** UNAFFECTED. No scoped `km init --h1` step required.

**Slack poller:** EXCLUDED — byte-identical to pre-Phase-106.

## Phase 109 — Self-heal orphaned `stopped` alias rows (resume-or-cold-create)

> **Phase 109 (2026-06-12) — H1 bridge self-heals an orphaned `stopped` row.**
>
> The HackerOne port of the GitHub-bridge fix (`docs/github-bridge.md` § Phase 109).
> When a `{prefix}-sandboxes` row lingers with `status=stopped` but its EC2 instance
> is **gone**, the resume path used to log a non-fatal error and enqueue to a per-sandbox
> h1-inbound FIFO with **no live poller** — the comment stranded. The fix: detect "no
> resumable instance", delete the stale row, and **cold-create** instead.

### The fix

`EC2Resumer.StartSandbox` wraps the exported sentinel `ErrNoResumableInstance` on the
terminal `len(found)==0` path (a transient `DescribeInstances`/`StartInstances` API error
is **not** wrapped — it keeps log-non-fatal + enqueue). `dispatchTarget` branches the
resume failure with `errors.Is`:

- **`ErrNoResumableInstance`** → `StatusWriter.DeleteSandboxRow(sandboxID)` (clears the
  stale row so the alias becomes absent — avoiding the **ambiguous-alias trap**), then
  `Publisher.PutSandboxCreate(...)`. **No enqueue, no thread upsert.**
- **any other (transient) error** → unchanged: log non-fatal, enqueue, FIFO redelivers.
- **success** → unchanged: `SetStatusRunning` + enqueue.

Multi-target fanout is unaffected: each target dispatches independently through
`dispatchTarget`, so one target self-healing does not touch the others.

### Files

- `pkg/h1/bridge/aws_adapters.go` — `ErrNoResumableInstance` sentinel + wrap in
  `StartSandbox`; `DynamoSandboxStatusWriter.DeleteSandboxRow` (single `DeleteItem` keyed
  by `sandbox_id`; `DynamoUpdateItemClient` widened with `DeleteItem`).
- `pkg/h1/bridge/interfaces.go` — `SandboxStatusWriter` extended with `DeleteSandboxRow`.
- `pkg/h1/bridge/webhook_handler.go` — the `dispatchTarget` resume-branch `errors.Is` fork.
- `infra/modules/lambda-h1-bridge/v1.0.0/main.tf` — `dynamodb:DeleteItem` added to the
  `DDBSandboxesUpdateItem` statement (still no `PutItem`).

### Deploy sequence (Phase 109)

Pure bridge-Lambda code + one IAM statement. **No SandboxProfile schema change → no
sandbox recreate.**

```bash
make build-lambdas           # rebuild the H1 bridge Lambda zip
km init --dry-run=false      # full apply — picks up the new DeleteItem IAM statement
                             # (km init --h1 also covers the env+IAM change)
km doctor
```

### Verification

- **Unit:** `EC2Resumer.StartSandbox` with no matching instances returns an error for which
  `errors.Is(err, ErrNoResumableInstance)` is true; a `DescribeInstances` API error does
  **not** match. The handler test asserts the orphan path deletes the row + cold-creates and
  does **not** enqueue/upsert; the transient-error path still enqueues. Covered in
  `pkg/h1/bridge/aws_adapters_phase109_test.go` and
  `pkg/h1/bridge/webhook_handler_phase109_test.go`.
- **Manual:** orphan a test alias (terminate its instance, leave `status=stopped`), trigger
  the program webhook → the bridge logs `orphaned stopped row … cold-creating`, a new
  instance is created, and no message strands in the old FIFO.
