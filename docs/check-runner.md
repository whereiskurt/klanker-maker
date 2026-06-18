# km check — Serverless Check Runner Guide

> **Phase 116 (2026-06-17) — complete.**
> Checks are workloads, not control-plane infrastructure: each check is its own
> AWS Lambda, provisioned imperatively via `km check deploy`, tracked in a
> `{prefix}-checks` DynamoDB table. Two scaffolding Terraform modules
> (`dynamodb-checks`, `check-runner-role`) are the only control-plane additions.

## Table of Contents

1. [Overview](#overview)
2. [SECURITY: Open-Egress Posture](#security-open-egress-posture)
3. [The Snippet Contract](#the-snippet-contract)
4. [The KM_CHECK_TRIGGER Model](#the-km_check_trigger-model)
5. [Two-Stage Firing Path](#two-stage-firing-path)
6. [CLI Reference](#cli-reference)
7. [Packaging: zip vs. --image](#packaging-zip-vs---image)
8. [Invocation Modes](#invocation-modes)
9. [The github.events check: Pre-filter](#the-githubevents-check-pre-filter)
10. [km doctor Check Group](#km-doctor-check-group)
11. [Deploy Surface](#deploy-surface)
12. [Example: QOTD](#example-qotd)
13. [Example: Wiz Threat Intel](#example-wiz-threat-intel)
14. [Troubleshooting](#troubleshooting)
15. [See Also](#see-also)

---

## Overview

`km check` runs small Python "check" snippets serverlessly, captures their JSON
output to the S3 artifact bucket, and — under a config-driven Python predicate —
fires an alias-targeted sandbox prompt (resume the paused/stopped box for that
alias, or cold-create one). A check is:

1. A plain Python program (no Lambda handler signature) — the author's snippet.
2. A km bootstrap handler (`_km_check_bootstrap.py`) shipped in every Lambda zip
   that execs the snippet, captures its stdout to S3, and evaluates the trigger.
3. A `checks.triggers:` config block in `km-config.yaml` that owns the dispatch
   decision — the snippet itself stays decision-free.

Checks reuse the existing km rails: EventBridge for scheduling, the S3 artifact
bucket for output capture, the nonces table for cooldown, and `ttl-handler` for
warm/cold sandbox dispatch.

---

## SECURITY: Open-Egress Posture

> **WARNING — Read before deploying checks.**

Check Lambdas are provisioned **without** a `VpcConfig`. They run in AWS's managed
Lambda network with full NAT'd outbound internet access:

- A check can call any external service (Wiz, Slack, PagerDuty, arbitrary SaaS
  APIs) with no IP/domain allowlist.
- Check Lambdas are **NOT** subject to the km sandbox eBPF/proxy network
  enforcement — that mechanism is EC2-userdata-based and does not apply to Lambda.
- This is intentional for the first use case (calling external monitoring and
  security SaaS platforms) but means a check is a less network-constrained
  surface than an EC2 sandbox.

**Mitigation plan (v2, deferred):** per-check VPC-attach + AWS Network Firewall
FQDN allowlist for production hardening. Not required for current use cases.

The shared baseline execution role (`{prefix}-check-runner`) limits blast radius:

- CloudWatch Logs write (the check's own log group).
- `s3:PutObject` on `s3://{artifacts}/check-runs/*` (output capture).
- `events:PutEvents` on the `km.sandbox` bus (dispatch a sandbox prompt).
- `ssm:GetParameter` on `{prefix}/checks/*` (per-check secrets).
- `dynamodb` read on `{prefix}-checks`.

A check Lambda does **not** have EC2, SQS, or resume permissions — those live in
`ttl-handler`, which is the stage-B dispatcher.

---

## The Snippet Contract

A check is a plain Python program. No Lambda handler signature is required.

```python
# my_check.py
import json
import sys

result = {"status": "ok", "count": 42}
print(json.dumps(result))
sys.exit(0)
```

The km bootstrap handler (`_km_check_bootstrap.py`) is the actual Lambda
entrypoint, shipped in every zip alongside the snippet as `snippet.py`. Per
invocation the bootstrap:

1. **Builds the env:** static Lambda env → SSM secrets (`{prefix}/checks/*`) →
   per-run `event['env']` overrides (later wins).
2. **Execs `snippet.py`** as a subprocess capturing stdout.
3. **Writes stdout verbatim** to `s3://{artifacts}/check-runs/<name>/<ts>/output.json`.
4. If stdout parses as JSON and `KM_CHECK_TRIGGER` has `when_py`, **evaluates
   the predicate** (Stage A). On truthy → emits one `CheckDispatch` event.
5. **Returns** `{"triggered": bool, "reason": "...", "output_key": "..."}` as the
   Lambda invocation result (readable by `km check run` and the github bridge
   pre-filter).

**Non-JSON stdout** is still captured to S3 but can never trigger
(`check_output_not_json` logged). Write `print(json.dumps({...}))` in your
snippet.

---

## The KM_CHECK_TRIGGER Model

The dispatch decision lives in **config, never in the snippet**. Add a
`checks.triggers:` block to `km-config.yaml`:

```yaml
checks:
  triggers:
    - check: wiz-intel                   # name of the deployed check Lambda
      when_py: |                         # inline or @file path
        return out.get("max_affected", 0) > 100
      alias: wiz-triage-box              # REQUIRED — target sandbox alias
      prompt: |                          # inline or @file; supports {{vars}}
        Wiz check fired: {{reason}}
        Affected systems: {{out.max_affected}}
      on_absent: cold-create             # or "skip"
      cooldown_seconds: 3600             # Stage B enforces via nonces table
```

> **Key rule — snake_case everywhere.** Viper lowercases config keys at load
> time. Use `on_absent`, `cooldown_seconds` — NOT `onAbsent`, `cooldownSeconds`.

### `when_py`

A Python predicate **body** wrapped at runtime as:

```python
def _pred(out):
    <body>
```

- `out` is the parsed JSON output dict.
- Must `return` either a `bool` or a `(bool, reason)` tuple.
- Standard library is importable inside the predicate.
- Exception → `triggered=false` (fail-closed for dispatch safety).

### `prompt`

Template delivered to the sandbox agent. Substitution vars:

| Token | Value |
|-------|-------|
| `{{reason}}` | The predicate's reason string (second element of a tuple return) |
| `{{out.<field>}}` | `str(out['<field>'])` for top-level output dict keys |

### `@file` resolution

Both `when_py` and `prompt` accept `@file` paths (e.g. `@predicates/triage.py`).
The CLI resolves and inlines the file **operator-side at `km check deploy` /
`km check sync` time**. The Lambda env var always contains the resolved inline
string. Editing a `@file` requires `km check sync` to re-bake.

### `sourceHash`

`km check deploy` and `km check sync` compute a SHA-256 of the resolved trigger
JSON and store it in the DDB row as `source_hash`. `km doctor` compares the
baked hash against the current config to flag drift and nudge `km check sync`.

---

## Two-Stage Firing Path

### Stage A — Python evaluation (in the check Lambda)

The bootstrap evaluates `when_py` against the parsed output dict. On truthy:

```json
{
  "event_type":       "check-dispatch",
  "check_name":       "wiz-intel",
  "alias":            "wiz-triage-box",
  "prompt":           "<expanded prompt>",
  "profile_name":     "github-review",
  "on_absent":        "cold-create",
  "reason":           "153 systems affected by top advisory",
  "cooldown_seconds": 3600,
  "auto_start":       true
}
```

Emitted to the `km.sandbox` EventBridge bus. The check role only needs
`events:PutEvents`.

### Stage B — Go dispatch (in `ttl-handler`)

A `CheckDispatch` EventBridge rule routes to `ttl-handler`, which:

1. **Cooldown check** — nonces table key `check-trigger:{name}`. If the
   `cooldown_seconds` window is still active, drops the event (idempotent).
2. **Alias resolution** — `DynamoAliasResolver.ResolveByAliasWithStatus`.
   - Sandbox **exists** (running/paused/stopped) → auto-resume + SSM
     `SendCommand` the prompt into the agent (canonical command builder, same
     as `km agent run`). No per-sandbox FIFO queue. No on-box poller change.
     **Existing sandboxes receive check dispatches without recreate.**
   - Sandbox **absent** + `on_absent=cold-create` → `PutSandboxCreate` with
     the profile and expanded prompt.
   - Sandbox **absent** + `on_absent=skip` → log `check_dispatch_skip`, drop.

---

## CLI Reference

| Command | Action |
|---------|--------|
| `km check deploy <file.py> [--name] [--env K=V] [--secret <ssm-path>] [--sops <file>] [--memory MB] [--timeout s] [--schedule "expr"] [--requirements] [--image]` | Package + CreateFunction/UpdateFunctionCode; write DDB row; re-bake KM_CHECK_TRIGGER; `--sops` unpacks an encrypted secrets file to per-check SSM params |
| `km check run <name> [--env K=V] [--wait]` | Synchronous invoke; print output + trigger/dispatch result |
| `km check ls [--json]` | List checks (name, schedule, last-run, drift flag) |
| `km check get <name>` | Detail: ARN, env keys, secret paths, schedule, trigger summary, sourceHash |
| `km check logs <name> [--follow]` | Tail the check Lambda's CloudWatch logs |
| `km check schedule <name> "<expr>"` (or `--off`) | Change/pause the EventBridge Scheduler entry |
| `km check sync [<name>] [--sops <file>]` | Re-resolve @file predicates/prompts + re-bake KM_CHECK_TRIGGER; `--sops <file> <name>` re-unpacks one check's secrets without re-zipping |
| `km check rm <name>` | Delete the Lambda + schedule + DDB row + per-check SSM secret params |

### Scheduling expressions

```
rate(1 hour)              # every hour
rate(30 minutes)          # every 30 minutes
cron(0 9 * * ? *)         # 09:00 UTC daily
```

Scheduler group: `{prefix}-checks`. Entries reference the specific check Lambda.

---

## Secrets

A check snippet reads secrets as **environment variables**. The check Lambda's
role grants `ssm:GetParameter` on `{prefix}/checks/*`, and the bootstrap fetches
each declared secret path `WithDecryption` at invoke time, exposing it as an env
var keyed by the **last path segment, UPPERCASED**:

```
/km/checks/wiz-audit/wiz_token   ⇒   $WIZ_TOKEN
```

There is **no KMS-decrypt path inside the Lambda** — values are always read from
SSM SecureString params that already exist.

### Individual secrets — `--secret`

Point a check at SSM params you manage yourself:

```bash
aws ssm put-parameter --name /km/checks/wiz-audit/wiz_token \
  --type SecureString --value "$TOKEN" --overwrite
km check deploy wiz-audit.py --secret /km/checks/wiz-audit/wiz_token
# snippet sees $WIZ_TOKEN
```

`km check get <name>` lists the declared `secret_paths` (paths only — never values).

### Bulk secrets from a SOPS file — `--sops`

For many keys at once, keep them in a SOPS-encrypted **flat** YAML/JSON file and
unpack them at deploy time:

```yaml
# secrets.enc.yaml  (flat, top-level scalars only)
WIZ_TOKEN: super-secret-token
SLACK_HOOK: https://hooks.slack.com/services/xxx
RETRIES: 5
```

```bash
km check deploy wiz-audit.py --sops secrets.enc.yaml
#   unpacked 3 SOPS secret(s) → /km/checks/wiz-audit/*
# snippet sees $WIZ_TOKEN, $SLACK_HOOK, $RETRIES
```

At deploy time `km` (operator-side, where `sops` + the KMS key already work):

1. Decrypts the file (`sops decrypt --output-type json`).
2. Writes each value to `/{prefix}/checks/{check}/{key}` as a **SecureString**
   (`Overwrite=true`).
3. Appends those paths to the check's secret-path list, merged with any explicit
   `--secret` paths (deduplicated).

Constraints (validated at deploy with a clear error):

- **Flat scalars only.** Nested maps, arrays, and `null` are rejected. Numbers and
  bools are coerced to their string form.
- **Keys must be valid env var names** (`[A-Za-z_][A-Za-z0-9_]*`) — no dashes,
  dots, spaces, or leading digits. The bootstrap UPPERCASES the key, so `apiKey`
  becomes `$APIKEY`. Name your keys in `UPPER_SNAKE_CASE` to avoid surprises.
- Requires the `sops` binary on the operator's PATH and local access to the KMS
  key. Secrets transit the operator machine + SSM (KMS-encrypted at rest) — the
  same trust model as `km bootstrap` secret handling. No new IAM is needed.

### Rotating / refreshing SOPS secrets — `km check sync --sops`

To push new values (or add/remove keys) without re-zipping the snippet:

```bash
km check sync wiz-audit --sops secrets.enc.yaml
```

This re-unpacks the file (overwriting the SSM values), rebuilds the check's
secret-path list from the freshly decrypted key set (so keys removed from the file
are dropped), and refreshes `KM_CHECK_SECRET_PATHS` on the Lambda. A pure value
rotation (keys unchanged) needs no env change — the Lambda reads the new value on
its next invoke.

> **Note — `--sops` and `sourceHash`/drift.** The SOPS key set is **not** folded
> into the trigger `sourceHash` (that hash is derived from `km-config.yaml` only,
> which `km check ls` recomputes for drift detection; folding SOPS keys in would
> make every `ls` report false drift). Refresh secrets explicitly with
> `km check deploy --sops` / `km check sync --sops`.

### Teardown

`km check rm <name>` deletes the per-check SSM namespace `/{prefix}/checks/{name}/*`
(paginated `GetParametersByPath` → `DeleteParameters`) alongside the Lambda,
schedule, and DDB row, so SOPS-derived secrets don't leak after teardown.
Externally-managed `--secret` params outside that namespace are left untouched.

---

## Packaging: zip vs. --image

### Default: zip + `requirements.txt`

```
km check deploy audit.py --name audit --requirements
```

`km check deploy` builds an arch-correct zip at deploy time:

```bash
pip install --platform manylinux2014_aarch64 --only-binary=:all: \
    --target <build-dir> -r requirements.txt
```

Cap: ~250 MB unzipped. Pure-Python wheels and common compiled wheels (requests,
boto3, etc.) work. Heavy system libs or missing arm64 wheels → use `--image`.

### Opt-in: container Lambda (`--image`)

```
km check deploy audit.py --name audit --image
```

Requires Docker on the operator host. Uses a sibling `Dockerfile`:

```dockerfile
FROM public.ecr.aws/lambda/python:3.13
COPY requirements.txt .
RUN pip install -r requirements.txt
COPY snippet.py _km_check_bootstrap.py ./
CMD ["_km_check_bootstrap.handler"]
```

The ECR repo `{prefix}-checks` is **lazily SDK-created** on the first `--image`
deploy (not a Terraform module). Up to 10 GB — any dep, any system lib.

---

## Invocation Modes

### 1. Scheduled (EventBridge Scheduler)

```bash
km check deploy snippet.py --name qotd --schedule "rate(1 hour)"
# or change the schedule later:
km check schedule qotd "rate(6 hours)"
km check schedule qotd --off     # pause without deleting
```

### 2. Manual / Remote

```bash
km check run qotd                       # synchronous; prints output + trigger
km check run qotd --env DEBUG=1         # per-run env override
km at '5pm' check run qotd             # deferred one-shot (km at / ttl-handler)
```

### 3. Event-driven Pre-filter (github.events check:)

See next section.

---

## The github.events check: Pre-filter

A `check:` field on a `github.events` rule gates sandbox dispatch on a check
Lambda result. The bridge invokes the check synchronously **before** dispatching:

```yaml
github:
  events:
    - on: repository
      actions: [created]
      match: "my-org/*"
      check: wiz-intel          # pre-filter: only dispatch if wiz-intel triggers
      alias: onboarding-bot
      prompt: "New repo created in my-org: {{repo}}"
```

**Behavior:**

- `check: wiz-intel` → bridge calls `{prefix}-check-wiz-intel` synchronously.
  - `triggered=true` → proceed with sandbox dispatch (cold/warm per alias).
  - `triggered=false` → drop the dispatch (`github_check_prefilter_skipped` logged).
  - **Invoke error → FAIL-CLOSED** (no dispatch). A check that errors must not
    silently fire a sandbox.
- `check:` absent (empty) → byte-identical to Phase 115 behavior (no filter).

**IAM:** `lambda:InvokeFunction` on `{prefix}-check-*` added to the GitHub bridge
role at `km init --dry-run=false` (in-place edit to `lambda-github-bridge/v1.1.0`).

**Scope:** GitHub bridge only (Phase 116). Slack and email pre-filters are deferred.

---

## km doctor Check Group

`km doctor` reports four sub-checks for the check runner (skipped silently on
dormant installs where the `{prefix}-checks` table does not exist):

| Check | Condition | Severity |
|-------|-----------|----------|
| Checks Table (`{prefix}-checks`) | DDB table absent or inaccessible | WARN |
| Orphan Check Lambdas | `{prefix}-check-*` Lambda not in DDB table | WARN |
| Orphan Check Schedules | Schedule targeting a check Lambda not in DDB | WARN |
| Check Trigger Drift | `source_hash` in DDB != current `km-config.yaml` hash | WARN |

The drift check compares the SHA-256 of the baked `KM_CHECK_TRIGGER` JSON (stored
in the DDB row at deploy/sync time) against re-baking from the current config. A
mismatch means the config changed without a `km check sync`.

**Remediation:**

```bash
km doctor                      # run all checks
km check sync                  # re-bake all triggers from current config
km check sync wiz-intel        # re-bake one check
km check rm ghost-check        # remove orphan Lambda + DDB row
```

---

## Deploy Surface

One-time scaffolding (first install of Phase 116):

```bash
make build                     # BEFORE km init — binary carries new regionalModules entries
make build-lambdas             # rebuild ttl-handler (CheckDispatch + check-run cases)
km init --dry-run=false        # full apply: dynamodb-checks + check-runner-role +
                               # CheckDispatch EventBridge rule + widened ttl-handler IAM
                               # NOT --sidecars (env/IAM require full terragrunt apply)
```

Per check (no Terraform after initial setup):

```bash
km check deploy snippet.py --name my-check  # SDK: CreateFunction + DDB row + KM_CHECK_TRIGGER
km check deploy snippet.py --sops secrets.enc.yaml  # + unpack SOPS → per-check SSM SecureString params
km check sync my-check                       # after editing km-config.yaml triggers
km check sync my-check --sops secrets.enc.yaml      # rotate/refresh secrets without re-zipping
km check rm my-check                         # SDK: DeleteFunction + schedule + DDB row + SSM secret params
```

`--sops` is a **pure operator-side `km` change** (decrypt + `ssm:PutParameter`).
No Lambda/Terraform/IAM change: the per-check `/{prefix}/checks/{name}/*` namespace
already matches the `check-runner` role's `ssm:GetParameter` grant. Requires the
`sops` binary + local KMS access on the operator's machine. Rebuild the operator
binary with `make build` to pick up the flag.

GitHub bridge pre-filter (after adding `check:` to a `github.events` rule):

```bash
make build-lambdas
km init --github               # or km init --dry-run=false to refresh IAM + env
```

---

## Example: QOTD

`profiles/checks/qotd/snippet.py` fetches a random inspirational quote from the
public Quotable API and emits it as JSON. Demonstrates:

- **Open internet egress** from a check Lambda.
- **requirements.txt** packaging (`requests` wheel).
- Zero configuration needed to run (no secrets, no predicate required).

```bash
# Deploy
km check deploy profiles/checks/qotd/snippet.py --name qotd \
  --schedule "rate(1 hour)"

# Test manually
km check run qotd
# Output: {"quote": "...", "author": "...", "category": "..."}

# View logs
km check logs qotd --follow
```

For a dispatch trigger on a specific category, add to `km-config.yaml`:

```yaml
checks:
  triggers:
    - check: qotd
      when_py: |
        return out.get("category") == "inspirational"
      alias: my-coding-sandbox
      prompt: "Today's quote: {{out.quote}} — {{out.author}}"
      cooldown_seconds: 86400   # once per day
```

---

## Example: Wiz Threat Intel

`profiles/checks/wiz-intel/snippet.py` emits a SIMULATED Wiz advisory payload
(replace with a live Wiz GraphQL API call in production using `--secret` for
credentials). Demonstrates:

- **Structured JSON output** with computed aggregate fields (`max_affected`).
- A `when_py` threshold predicate that fires when affected systems exceed 100.
- Cold-create sandbox dispatch for an absent alias.

See `profiles/checks/wiz-intel/checks.triggers.example.yaml` for the full
`checks.triggers:` config snippet to paste into `km-config.yaml`.

```bash
# Deploy (simulated — no credentials needed)
km check deploy profiles/checks/wiz-intel/snippet.py --name wiz-intel \
  --schedule "rate(1 hour)"

# Test manually
km check run wiz-intel
# Output: {"advisories": [...], "total_advisories": 4, "max_affected": 153}
# Trigger: "153 systems affected by top advisory" (exceeds threshold of 100)

# Real production version — inject Wiz API credentials via SSM:
# km check deploy profiles/checks/wiz-intel/snippet.py --name wiz-intel \
#   --secret /km/checks/wiz-client-id \
#   --secret /km/checks/wiz-client-secret \
#   --schedule "rate(1 hour)"
```

Pair with a `github.events` rule to gate new repo onboarding on Wiz posture:

```yaml
github:
  events:
    - on: repository
      actions: [created]
      match: "my-org/*"
      check: wiz-intel           # only onboard if Wiz threat level is high
      alias: security-auditor
      prompt: "New repo {{repo}} in my-org — please review security posture."
```

---

## Troubleshooting

### check Lambda invocation fails with AccessDenied

The `{prefix}-check-runner` role policy may be stale. Re-run `km init --dry-run=false`
(or `km check deploy` to re-register with the latest role ARN).

### km doctor reports "Checks Table not found"

The `{prefix}-checks` DDB table hasn't been provisioned yet.

```bash
make build && km init --dry-run=false
```

### km doctor reports trigger drift

Config changed without syncing the Lambda environment.

```bash
km check sync              # re-bake all triggers
km check sync <name>       # re-bake one check
```

### check triggered but sandbox not dispatched

Check `km check logs <name>` for `check_dispatch_skip` (cooldown active) or
`predicate error:` messages. Also verify `alias` in `checks.triggers` matches
a running/stopped sandbox or `on_absent: cold-create` is set.

### github bridge pre-filter dropped despite check triggering

The `CheckInvoker` field on `WebhookHandler` must be wired at bridge cold-start
(requires `km init --github` or `km init --dry-run=false` to pick up the IAM
grant). If the bridge logs `check pre-filter configured but CheckInvoker is nil`,
the Lambda cold-start is using a stale code zip — run `make build-lambdas` +
`km init --dry-run=false`.

---

## See Also

- `profiles/checks/qotd/snippet.py` — QOTD example check
- `profiles/checks/wiz-intel/snippet.py` — Wiz Threat Intel example check
- `profiles/checks/wiz-intel/checks.triggers.example.yaml` — trigger config example
- `profiles/checks/_bootstrap/_km_check_bootstrap.py` — Lambda bootstrap handler
- `profiles/checks/_bootstrap/KM_CHECK_TRIGGER.schema.md` — trigger schema
- `pkg/check/` — Go CLI helpers (BakeTrigger, DDB CRUD, Lambda CRUD, packaging)
- `docs/github-bridge.md` § Phase 115 — github.events router
- `OPERATOR-GUIDE.md` — full operator runbook
