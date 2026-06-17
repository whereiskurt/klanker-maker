# `km check` — Serverless Check Runner

**Status:** Design (approved in brainstorming)
**Date:** 2026-06-17
**Author:** operator (whereiskurt) + Claude

## Problem

Operators want to run small Python snippets/programs serverlessly — each with
injected environment variables — capture their output to S3, and **conditionally
kick off a sandbox prompt only under certain circumstances**. They want a generic
way to author a snippet, push it as a Lambda, remote-execute it, and capture its
output, in a way that reuses klanker-maker's existing rails rather than fighting
them.

## Summary

Introduce a `km check` family that treats checks as **workloads** (like sandboxes),
not control-plane infrastructure (like the bridges). Each check is its own AWS
Lambda, provisioned imperatively via the SDK — **no terragrunt apply per check**.
A check runs a plain Python program, captures its stdout to S3, and inline-evaluates
a config-driven Python predicate; on a match it emits one dispatch event that a Go
handler turns into **resume-or-cold-create** of a sandbox handed the expanded prompt.

The platform already provides three of the four moving parts: EventBridge-driven
Lambdas, the S3 artifact bucket, and the `github.events` event→prompt→sandbox router.
The only genuinely new layer is **generic snippet packaging + remote execution +
output capture**.

## Decisions (locked in brainstorming)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Execution model | **Lambda-per-snippet** (full isolation per check) |
| 2 | Provisioning | **SDK-managed** (`CreateFunction`/`UpdateFunctionCode`), **shared baseline role**; 2 terragrunt scaffolding modules only |
| 3 | Trigger contract | **Decoupled** — snippet stays pure; a config-driven `checks.triggers:` block owns the fire decision (mirrors `github.events:`) |
| 4 | Evaluator placement | **Inline** at the tail of each check run (in the bootstrap); one component |
| 5 | Invocation | **Scheduled** (EventBridge Scheduler) + **manual/remote** (`km check run`) + **event-driven pre-filter** (`check:` on a `github.events` rule; github first) |
| 6 | Condition language | **Python predicate block** (`when_py:`), `out` bound; accepts **inline or `@file`** |
| 7 | Firing | **Alias-targeted resume-or-cold-create** (Stage A Python bootstrap emits `CheckDispatch`; Stage B Go reuses the bridge self-heal) |
| 8 | Dispatch host | **`ttl-handler`** (no new fleet Lambda); shared `pkg/dispatch` helper |
| 9 | Network egress | **Open egress (no VPC)** for v1 — checks reach any internet service (Wiz, Slack, etc.); NOT under sandbox eBPF/proxy enforcement |
| 10 | Packaging | **Zip + `requirements.txt` by default** (arch-correct wheels) + **`--image` container opt-in** (up to 10 GB; lazy SDK-created ECR repo) |

## Architecture

Two layers, mirroring the sandbox model.

### Control-plane scaffolding (terragrunt, provisioned once — 2 modules total)

- `infra/modules/dynamodb-checks/v1.0.0/` → `{prefix}-checks` table.
  Hash key `name`, `PAY_PER_REQUEST`, SSE on. The check registry, exactly like
  `{prefix}-sandboxes`. Added once to `regionalModules()` (init.go).
- `infra/modules/check-runner-role/v1.0.0/` → one shared Lambda execution role
  `{prefix}-check-runner` with a baseline policy:
  - CloudWatch Logs (create/put).
  - `s3:GetObject` on `s3://{artifacts}/checks/*` (snippet source, if read at runtime).
  - `s3:PutObject` on `s3://{artifacts}/check-runs/*` (output capture).
  - `events:PutEvents` on the `km.sandbox` bus (emit `CheckDispatch`).
  - `ssm:GetParameter` on `{prefix}/checks/*` (per-check secrets).
  - `dynamodb` read on `{prefix}-checks`.

  Note the **thin blast radius**: the check role does **not** get EC2/SQS/resume
  perms. All sandbox-touching power lives in Stage B (`ttl-handler`), which already
  holds it.

These two modules are the **only** control-plane fleet growth. Adding/editing/removing
an actual check never touches terragrunt.

### Data-plane per-check Lambdas (SDK-managed, no terragrunt)

`km check deploy <file.py>` packages and calls `lambda:CreateFunction` /
`UpdateFunctionCode` directly — the same imperative pattern `km create` uses to
provision a sandbox. Each check Lambda is:

- Named `{prefix}-check-<name>`.
- Runtime `python3.13`.
- Role = the shared `{prefix}-check-runner` baseline.
- Memory/timeout from `--memory` / `--timeout` flags (sane defaults: 256 MB / 30 s).
- Handler = a km-authored bootstrap (`_km_check_bootstrap.py`) shipped in every zip.

A row is written to `{prefix}-checks` (see schema below).

## The snippet contract

The author's snippet stays a **plain program — no Lambda handler signature
required**. The km bootstrap is the actual Lambda handler. Per invocation it:

1. **Builds the env:** static deploy-time env → SSM secrets (`{prefix}/checks/*`) →
   per-run overrides from the invoke payload (later wins).
2. **Execs the snippet** as a subprocess with that env, capturing stdout.
3. **Writes stdout verbatim** to `s3://{artifacts}/check-runs/<name>/<ts>/output.json`.
4. If stdout parses as JSON, **evaluates this check's baked-in trigger** (Stage A,
   below). On match, emits one `CheckDispatch` event.

So a check is just: *read env → do work → `print(json.dumps({...}))`.* Pure-stdlib
snippets zip as-is; a sibling `requirements.txt` is pip-installed into the package
at deploy.

Non-JSON stdout is still captured to S3 but can never trigger (logged as
`check_output_not_json`).

## Runtime capability envelope

### Network egress (open, v1)

Check Lambdas are created **without** a `VpcConfig`, so they run in AWS's managed
network with full NAT'd outbound internet. A check can call any external service —
Wiz, Slack, PagerDuty, arbitrary SaaS APIs — with no allowlist and no NAT cost.

**Explicit posture note:** checks are therefore **not** subject to the sandbox
eBPF/proxy network enforcement (that mechanism is EC2-userdata-based and does not
apply to Lambda). Egress is unrestricted. This is the right default for the use case
(calling external services) but must be documented prominently in the operator guide:
a check is a less network-constrained surface than a sandbox. Per-check VPC-attach +
egress filtering (IP/SG or AWS Network Firewall FQDN allowlist) is a deferred
follow-up (see Out of scope).

### Packaging (zip default, container opt-in)

- **Default — zip + `requirements.txt`.** A sibling `requirements.txt` is resolved at
  `km check deploy` with **arch-correct wheels** for the Lambda runtime, not the
  operator's host:
  `pip install --platform manylinux2014_aarch64 --only-binary=:all: --target <build> -r requirements.txt`
  (arm64 to match the `linux/arm64` build target). The snippet + deps are zipped and
  uploaded (via S3 for packages over the 50 MB direct-upload limit). Covers
  pure-Python + common pre-built wheels. Cap: ~250 MB unzipped; deps with no aarch64
  wheel or heavy system libs won't fit — use `--image`.
- **Opt-in — `--image` container Lambda.** A sibling `Dockerfile`
  (`FROM public.ecr.aws/lambda/python:3.13`) is built and pushed; the check is created
  with `PackageType=Image` (up to 10 GB; any dep, any system lib). Requires Docker on
  the operator host at deploy time; slower cold starts (image checks only).
  - **ECR repo is lazy + SDK-created** (not a third terragrunt module): on the first
    `--image` deploy, km `ecr:CreateRepository` `{prefix}-checks` (idempotent) and sets
    a repo policy granting the Lambda service principal (`lambda.amazonaws.com`) pull.
    Keeps control-plane fleet growth at the two scaffolding modules.

## The trigger (decoupled, config-driven)

In `km-config.yaml`, mirroring `github.events:`:

```yaml
checks:
  triggers:
    - check: s3-audit
      when_py: |                    # inline OR "@predicates/critical.py"
        crit = [i for i in out['items'] if i['sev'] == 'crit']
        return (len(crit) >= 3, f"{len(crit)} critical findings")
      alias: nightly-auditor         # REQUIRED — target sandbox alias
      prompt: "@prompts/triage.tmpl"  # inline OR @file; vars from out + {{reason}}
      onAbsent: cold-create           # or "skip" if no box with that alias exists
      cooldownSeconds: 3600
```

- The decision lives in **config, never in the snippet** (preserves the decoupling).
- `when_py` and `prompt` each accept **inline or `@file`** (consistent with
  `km create --prompt <text-or-@file>`).
- `@file` is resolved **operator-side at `km check deploy` / `km check sync` time**:
  km reads the file and bakes the resolved contents into the Lambda's
  `KM_CHECK_TRIGGER` env var. The Lambda has **no access to the operator's
  filesystem** at runtime. Editing a `@file` therefore requires `km check sync` to
  re-push. Inline and `@file` are otherwise byte-identical downstream.
- The resolved predicate + prompt feed the check's `sourceHash` (DDB row), so
  `km check ls` / `km doctor` can flag *"config drifted — run `km check sync`."*

### Condition language: `when_py`

A full Python predicate block, evaluated by the bootstrap. The parsed output is
bound as `out` (a dict). The block is wrapped as `def _pred(out): <body>` and must
`return` either a `bool` or a `(bool, reason)` tuple. Standard library is importable.
The optional `reason` string is logged and exposed to the prompt template as
`{{reason}}`.

Trust boundary: `eval`/`exec` of `when_py` runs the **operator's own config** inside
the **operator's own Lambda** — the same trust as the snippet itself. No additional
sandboxing required.

### Prompt template

Simple `{{...}}` substitution (same mechanism as `ExpandEventTemplate`):
- `{{reason}}` — the predicate's reason string.
- `{{out.<field>}}` — dotted access into the output dict.

## Firing path (two stages, each in its native language)

### Stage A — eval (Python, in the check Lambda bootstrap)

After writing `output.json`, the bootstrap wraps and calls `when_py`. On a truthy
return it emits **one** `CheckDispatch` event to the `km.sandbox` bus:

```json
{ "alias": "...", "prompt": "<expanded>", "profile": "<optional>",
  "onAbsent": "cold-create|skip", "reason": "...", "check": "<name>" }
```

The check Lambda's role therefore needs only `events:PutEvents`.

### Stage B — dispatch (Go, in `ttl-handler`, reusing the bridge self-heal)

A new `CheckDispatch` EventBridge rule targets `ttl-handler`; a new case in its
`eventType` switch calls a shared helper:

```
pkg/dispatch.ResumeOrCreate(alias, prompt, profile, onAbsent)
  resolve alias (ResolveByAliasWithStatus):
    running        -> SQS enqueue prompt to the sandbox's inbound FIFO
    stopped/paused -> EC2Resumer.StartSandbox -> enqueue
    absent (ErrNoResumableInstance):
      onAbsent == cold-create -> PutSandboxCreate(profile, prompt envelope)
      onAbsent == skip         -> log + drop
```

This is the **exact** resume-or-cold-create logic from the Phase 109/114 bridges —
including the sandbox-id-tag namespace handling, `ErrNoResumableInstance`, and the
ambiguous-alias trap. **The bridge logic is factored into `pkg/dispatch`** and shared
by both the bridges and the check path (one source of truth — no Python boto3
reimplementation of those footguns).

Cooldown is enforced **here** via the nonces table (key `check-trigger:{name}`),
the same mechanism as bridge event cooldowns.

`ttl-handler` IAM is widened with SQS-send + cold `PutSandboxCreate` (it already
holds EC2 resume + EventBridge consumption from `handleResume` / `handleAgentRun` /
`handleScheduleCreate`).

## Invocation

1. **Scheduled (cron/rate).** `km check deploy audit.py --schedule "rate(1 hour)"`
   creates an EventBridge Scheduler entry targeting the check Lambda — reusing the
   `handleScheduleCreate` pattern (`cmd/ttl-handler/main.go`). Stored in the DDB
   row; editable via `km check schedule <name> "<expr>" | --off`.
2. **Manual / remote.** `km check run <name>` → synchronous `lambda:Invoke`; prints
   the captured output + whether it triggered + the dispatch outcome.
   `km at '5pm' check run <name>` schedules a one-shot (a new `check-run` op in the
   `km at` / ttl-handler switch). Per-run env via `km check run --env K=V`.
3. **Event-driven pre-filter.** A `check: <name>` field on a `github.events` rule:
   the bridge synchronously invokes the check Lambda first and only dispatches the
   sandbox if the check triggers — i.e. checks become a pre-filter for the existing
   routers. **GitHub first**; Slack/email pre-filters are noted follow-ups.

## Env vars & secrets

- **Static:** `--env K=V` (repeatable) → baked into the Lambda's environment config.
- **Secrets:** `--secret <ssm-path under {prefix}/checks/>` → the baseline role
  already allows the prefix; the bootstrap fetches + injects at run start (mirrors
  sandbox `iam.allowedSecretPaths`). Secret values are never written to the DDB row
  (only the paths).
- **Per-run overrides:** `km check run --env K=V` → passed in the invoke payload;
  bootstrap merges over the static env (per-run wins).

## CLI surface

Mirrors the sandbox verbs:

| Command | Action |
|---------|--------|
| `km check deploy <file.py> [--name] [--env] [--secret] [--memory] [--timeout] [--schedule] [--requirements] [--image]` | Package (zip, or container via `--image`) + `CreateFunction`/`UpdateFunctionCode`; write DDB row; (re)bake `KM_CHECK_TRIGGER`; create/update schedule |
| `km check run <name> [--env K=V] [--wait]` | Synchronous invoke; print output + trigger/dispatch result |
| `km check ls [--json]` | List checks (name, schedule, last-run, drift flag) |
| `km check get <name>` | Detail: arn, env keys, secret paths, schedule, trigger summary, sourceHash |
| `km check logs <name> [--follow]` | Tail the check Lambda's CloudWatch logs |
| `km check schedule <name> "<expr>"` (or `--off`) | Change/pause the EventBridge Scheduler entry |
| `km check sync [<name>]` | Re-resolve `@file` predicates/prompts + re-bake `KM_CHECK_TRIGGER` from current `km-config.yaml`; update sourceHash |
| `km check rm <name>` | Delete the Lambda + schedule + DDB row |

### DDB `{prefix}-checks` row

`name, arn, runtime, packageType (zip|image), imageUri (if image), memory, timeout,
schedule, env (non-secret keys only), secretPaths, sourceHash, triggerSummary,
createdAt, updatedAt`.

## Config plumbing

`checks.triggers:` follows the established new-config-key ritual:

1. Add `Checks` struct (with `Triggers []CheckTrigger`) to the config types,
   `mapstructure` + `yaml` tags.
2. Add `checks` to the **v2→v merge-list** in `config.Load()` (it is a
   list-of-objects; omitting it silently drops the file value).
3. Capture in the `YAMLDefaults` snapshot for drift WARN.
4. **Per-check env-baking** (not a single global env var): at `km check deploy` /
   `km check sync`, km resolves the trigger for that check (including `@file`s) and
   bakes it into **that check Lambda's** `KM_CHECK_TRIGGER` env var — the
   `KM_GITHUB_EVENTS` env-baking pattern, scoped per Lambda. The bootstrap reads
   `KM_CHECK_TRIGGER` at runtime.

## `km doctor` additions

- `{prefix}-checks` table exists.
- Orphan `{prefix}-check-*` Lambdas not present in the DDB table (WARN).
- Schedules referencing a check Lambda that no longer exists (WARN).
- Per-check config drift: `KM_CHECK_TRIGGER` baked value vs. current
  `km-config.yaml` resolved trigger (nudge `km check sync`).

## Deploy surface

- **One-time:** `make build` + `km init --dry-run=false` (the 2 new scaffolding
  modules — table + role — plus the `CheckDispatch` EventBridge rule and the widened
  `ttl-handler` IAM/env require a full terragrunt apply, NOT `--sidecars`).
- **Per check:** `km check deploy` / `km check sync` — pure SDK, seconds, no apply.
  `--image` checks additionally require Docker on the operator host and lazily
  `ecr:CreateRepository` the shared `{prefix}-checks` repo on first use (still no
  terragrunt).
- **`ttl-handler` code change** (the `check-dispatch` + `check-run` cases, shared
  `pkg/dispatch`): `make build-lambdas` + `km init --dry-run=false`.
- `make build` the `km` binary **before** `km init` so the new `regionalModules()`
  entries are present (stale binary silently skips new modules → runtime
  `AccessDenied`).

## Out of scope (v1 / YAGNI)

- Slack/email event-driven pre-filters (GitHub first).
- Non-Python runtimes (Python-only contract for v1).
- Per-check least-privilege IAM roles (shared baseline chosen).
- A separate scheduled re-evaluator (inline-at-run only).
- A dedicated `check-dispatcher` Lambda (dispatch hosted in `ttl-handler`).
- **Per-check VPC-attach + egress filtering** (open egress only in v1; no FQDN
  allowlist / Network Firewall for checks).

## Reuse map (anchors)

| Need | Reuse |
|------|-------|
| Imperative workload provisioning | `km create` sandbox pattern (SDK, DDB-tracked) |
| Cold sandbox create w/ prompt | `pkg/aws/eventbridge.go` `PutSandboxCreate` + create-handler `--prompt` |
| Resume-or-cold-create self-heal | Phase 109/114 bridge logic → factored `pkg/dispatch` |
| Scheduled serverless execution | `cmd/ttl-handler/main.go` `handleScheduleCreate` (EventBridge Scheduler) |
| Event→prompt template expansion | `pkg/github/bridge/event_router.go` `ExpandEventTemplate` |
| Config-driven rule block + env-baking | `github.events:` / `KM_GITHUB_EVENTS` |
| S3 output capture | `{prefix}-artifacts-{account}`, `pkg/aws/artifacts.go` |
| Cooldown / dedup | nonces table (bridge event cooldowns) |
| Secret injection | sandbox `iam.allowedSecretPaths` SSM pattern |
