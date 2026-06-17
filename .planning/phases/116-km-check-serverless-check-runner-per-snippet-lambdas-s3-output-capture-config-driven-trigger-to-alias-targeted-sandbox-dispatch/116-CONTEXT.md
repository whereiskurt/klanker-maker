# Phase 116: km check — Serverless Check Runner - Context

**Gathered:** 2026-06-17
**Status:** Ready for planning
**Source:** Brainstorming session (approved design spec) — `docs/superpowers/specs/2026-06-17-km-check-serverless-runner-design.md`

<domain>
## Phase Boundary

Deliver a `km check` family that runs small Python "check" snippets serverlessly,
captures their JSON output to the S3 artifact bucket, and — under a config-driven
predicate — fires an alias-targeted sandbox prompt (resume the paused/stopped box
for that alias, else cold-create). Checks are **workloads** (like sandboxes),
provisioned imperatively via the AWS SDK — NOT control-plane infra; there is no
terragrunt apply per check.

**In scope:** the two scaffolding terragrunt modules, the per-check Lambda
provisioning + bootstrap, output capture, the `checks.triggers` config block +
`when_py` predicate, the `CheckDispatch` event + `ttl-handler` consumer reusing a
factored `pkg/dispatch`, scheduled/manual/`github.events` invocation, zip +
container packaging, the `km check` CLI, `km doctor` checks, and two live example
checks in `profiles/checks/` (QOTD + simulated Wiz Threat Intel).

**Authoritative source of truth:** the design spec named above. Every decision
below is locked from it.
</domain>

<decisions>
## Implementation Decisions (LOCKED)

### Execution model
- **Lambda-per-snippet** — each check is its own AWS Lambda (full isolation).
- **SDK-managed provisioning** — `lambda:CreateFunction` / `UpdateFunctionCode`
  directly (the `km create` sandbox pattern), tracked in a `{prefix}-checks` DDB
  table. No terragrunt apply per check.
- **Shared baseline execution role** `{prefix}-check-runner` (NOT per-check roles):
  CloudWatch Logs; read `s3://{artifacts}/checks/*`; write
  `s3://{artifacts}/check-runs/*`; `events:PutEvents` on the `km.sandbox` bus;
  `ssm:GetParameter` on `{prefix}/checks/*`; read `{prefix}-checks`. The check role
  gets NO EC2/SQS/resume perms (those live in Stage B / ttl-handler).

### Scaffolding (terragrunt — TWO modules only)
- `infra/modules/dynamodb-checks/v1.0.0/` → `{prefix}-checks` table (hash key
  `name`, PAY_PER_REQUEST, SSE).
- `infra/modules/check-runner-role/v1.0.0/` → shared `{prefix}-check-runner` role.
- Both registered ONCE in `regionalModules()` (init.go). No third always-on module.

### Snippet contract
- Author writes a plain Python program — NO Lambda handler signature. A km-authored
  bootstrap (`_km_check_bootstrap.py`) is the actual Lambda handler, shipped in
  every zip/image.
- Bootstrap per invocation: build env (static → SSM secrets → per-run overrides,
  later wins) → subprocess-exec the snippet capturing stdout → write stdout verbatim
  to `s3://{artifacts}/check-runs/<name>/<ts>/output.json` → if JSON, evaluate the
  baked-in trigger inline (Stage A) → on match emit ONE `CheckDispatch` event.
- Non-JSON stdout is captured but never triggers (log `check_output_not_json`).

### Trigger (decoupled, config-driven — mirrors `github.events:`)
- `checks.triggers:` block in `km-config.yaml`. Each rule:
  `check, when_py, alias (REQUIRED), prompt, onAbsent (cold-create|skip),
  cooldownSeconds`.
- The decision lives in CONFIG, never in the snippet.
- **Condition language = Python predicate block** `when_py`: wrapped as
  `def _pred(out): <body>`, `out` = parsed output dict, stdlib importable, must
  `return` a `bool` or `(bool, reason)` tuple. `eval/exec` of operator config in the
  operator's own Lambda — same trust boundary as the snippet.
- **`when_py` AND `prompt` accept inline OR `@file`.** `@file` resolved
  operator-side at `km check deploy` / `km check sync` time and baked into the
  Lambda's `KM_CHECK_TRIGGER` env var (Lambda has no operator-filesystem access at
  runtime). Editing a `@file` requires `km check sync`. `sourceHash` covers the
  resolved predicate + prompt for drift detection.
- Prompt template: `{{reason}}` + `{{out.<field>}}` substitution (the
  `ExpandEventTemplate` mechanism).

### Firing path (two stages)
- **Stage A (Python, in the check Lambda bootstrap):** evaluate `when_py`; on truthy
  emit `CheckDispatch` to the `km.sandbox` bus:
  `{alias, prompt(expanded), profile, onAbsent, reason, check}`. Check role needs
  only `events:PutEvents`.
- **Stage B (Go, in `ttl-handler`):** a new `CheckDispatch` EventBridge rule → new
  case in the `eventType` switch → `pkg/dispatch.ResumeOrCreate(alias, prompt,
  profile, onAbsent)`: running → SQS enqueue; stopped/paused → `EC2Resumer.
  StartSandbox` → enqueue; absent (`ErrNoResumableInstance`) → `onAbsent`
  (cold `PutSandboxCreate` | skip). Cooldown enforced HERE via the nonces table
  (`check-trigger:{name}`).
- **`pkg/dispatch` is factored from the EXISTING GitHub + H1 bridge resume-or-cold-
  create logic** and shared by both bridges (parity preserved) AND the check path —
  one source of truth; NO Python boto3 reimplementation of the sandbox-id-tag /
  `ErrNoResumableInstance` / ambiguous-alias footguns.
- `ttl-handler` IAM widened with SQS-send + cold `PutSandboxCreate`.
- **Dispatch host = `ttl-handler`** (no new fleet Lambda).

### Invocation (all three)
- **Scheduled:** `km check deploy --schedule "rate(1h)"|"cron(...)"` → EventBridge
  Scheduler entry targeting the check Lambda (reuse `handleScheduleCreate`).
  `km check schedule <name> "<expr>"` / `--off`.
- **Manual / remote:** `km check run <name>` → synchronous `lambda:Invoke`, prints
  output + trigger/dispatch result. `km at '...' check run <name>` one-shot
  (new `check-run` op in the ttl-handler/`km at` switch). `--env K=V` per-run.
- **Event-driven pre-filter:** a `check: <name>` field on a `github.events` rule —
  the bridge synchronously invokes the check first; only dispatches the sandbox if
  the check triggers. **GitHub first**; Slack/email deferred.

### Network egress (v1)
- **Open egress (no VpcConfig)** — checks reach any internet service (Wiz, Slack,
  etc.). Explicitly NOT under the sandbox eBPF/proxy enforcement; document the
  posture prominently. Per-check VPC-attach + egress filtering deferred.

### Packaging
- **Default: zip + `requirements.txt`** resolved at deploy with arch-correct wheels
  (`pip install --platform manylinux2014_aarch64 --only-binary=:all: --target …`,
  arm64). Cap ~250 MB unzipped (upload via S3 over 50 MB).
- **Opt-in: `--image`** container Lambda (sibling `Dockerfile`,
  `FROM public.ecr.aws/lambda/python:3.13`, up to 10 GB). ECR repo `{prefix}-checks`
  **lazily SDK-created** on first `--image` deploy with a repo policy granting
  `lambda.amazonaws.com` pull — NOT a third terragrunt module.
- Runtime `python3.13`, arm64. Defaults: 256 MB / 30 s.

### CLI surface
- `km check deploy <file.py> [--name --env --secret --memory --timeout --schedule
  --requirements --image]`, `run`, `ls`, `get`, `logs`, `schedule`, `rm`, `sync`.
- DDB row: `name, arn, runtime, packageType (zip|image), imageUri, memory, timeout,
  schedule, env (non-secret keys), secretPaths, sourceHash, triggerSummary,
  createdAt, updatedAt`.

### Config plumbing
- `checks.triggers:` follows the new-config-key ritual: struct + tags; ADD `checks`
  to the v2→v merge-list in `config.Load()` (list-of-objects — omitting silently
  drops it); `YAMLDefaults` snapshot for drift WARN; **per-check env-baking** —
  bake the resolved trigger into THAT check Lambda's `KM_CHECK_TRIGGER`
  (the `KM_GITHUB_EVENTS` pattern, scoped per Lambda) at deploy / sync.

### km doctor
- `{prefix}-checks` table exists; orphan `{prefix}-check-*` Lambdas not in DDB
  (WARN); schedules referencing a missing check Lambda (WARN); per-check
  `KM_CHECK_TRIGGER` drift vs current config (nudge `km check sync`).

### Example checks (profiles/checks/)
- **QOTD** — fetches a quote-of-the-day off the internet (proves open egress +
  requirements packaging), prints JSON.
- **Wiz Threat Intel (simulated)** — emits a list of Wiz advisories + affected-
  system counts as JSON; paired with a `checks.triggers` `when_py` that fires a
  sandbox when affected counts cross a threshold (proves the full trigger → dispatch
  path end to end).

### Claude's Discretion
- Internal Go package layout, struct/field naming, exact `pip`/zip build mechanics,
  the bootstrap's precise eval-wrapping, test file organization, and the precise
  CloudFormation/terragrunt resource wiring — follow existing repo patterns
  (`pkg/aws`, `cmd/ttl-handler`, `infra/modules/*`, `internal/app/cmd`).
</decisions>

<specifics>
## Specific Ideas / Constraints (from project memory + CLAUDE.md)

- **Deploy surface (CRITICAL):** scaffolding modules + `CheckDispatch` rule +
  widened `ttl-handler` IAM/env require `make build` (binary carries new
  `regionalModules()` entries — stale binary silently skips new modules →
  AccessDenied) THEN `km init --dry-run=false` (NOT `--sidecars`, which doesn't
  update IAM/env). `ttl-handler` code change also needs `make build-lambdas`.
- **New Lambda checklist applies to ttl-handler change only** (the per-check Lambdas
  are SDK, not fleet). Any new fleet module needs: TF module + live terragrunt unit +
  `regionalModules()` entry + `lambdaBuilds()` entry (for code Lambdas) — but here we
  add ZERO new fleet Lambdas (dispatch hosted in ttl-handler).
- **DDB attr names:** verify against a real row, not a test mock (the Slack
  `state`/`status` bug). New per-sandbox/per-check attrs must round-trip through
  struct+marshal+unmarshal (SandboxMetadata lossy round-trip lesson).
- **Verify deploy surface, not just code** (feedback memory): new Lambda/queue/IAM/
  agent-prompt work needs a deploy-surface verification pass.
- **Check `go test` exit code, not the pipe** (feedback memory).
- `AWS_PROFILE=klanker-application` for AWS ops. `km destroy` uses `--remote --yes`.
- Rebuild with `make build` (ldflags), not bare `go build`.
- This is the default `km` install (`resource_prefix: km`); but all naming MUST use
  `{prefix}-*` and the `km:sandbox-id` tag namespace (constant regardless of prefix).
</specifics>

<deferred>
## Deferred Ideas (explicit out-of-scope for v1)

- Slack / email event-driven pre-filters (GitHub first).
- Non-Python runtimes.
- Per-check least-privilege IAM roles (shared baseline chosen).
- A separate scheduled re-evaluator (inline-at-run only).
- A dedicated `check-dispatcher` Lambda (dispatch hosted in `ttl-handler`).
- Per-check VPC-attach + egress filtering (open egress only in v1).
- Warm-alias firing is NOT deferred — it is core (resume-or-cold-create).
</deferred>

---

*Phase: 116-km-check-serverless-check-runner*
*Context gathered: 2026-06-17 from approved brainstorming design spec*
