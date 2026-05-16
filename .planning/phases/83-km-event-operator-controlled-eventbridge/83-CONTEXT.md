# Phase 83: Add km event command for operator-controlled EventBridge — Context

**Gathered:** 2026-05-16
**Status:** Ready for planning
**Source:** Synthesized from design conversation (locked decisions before /gsd:add-phase 83)

<domain>
## Phase Boundary

**Adds:** A second EventBridge lane for **durable, declarative, operator-controlled** scheduled work — `km event add/list/rm/apply` — alongside today's ad-hoc `km at`.

**Two-lane model after this phase:**

| Lane | Use case | Mechanism | Storage |
|---|---|---|---|
| `km at '<when>' <cmd>` | "Happen once, soon, minimum ceremony" | EventBridge SDK call + DDB row | DynamoDB `{prefix}-schedules` (existing) |
| `km event '<when>' <cmd>` | "Durable platform configuration" | Manifest in `events/*.yaml` → Terragrunt apply | Git-tracked HCL + `km-config.yaml` |

**`km at` cleanup (in scope):** strip the existing `--cron` flag and reject NL parser results with `IsRecurring=true`. Hard cut, no deprecation period.

**Deliverables:**
1. New CLI subcommands: `km event add`, `km event list`, `km event rm`, `km event apply`
2. Two new Terraform modules: `operator-event-bus/v1.0.0/` (one-time bus + km-runner Lambda) and `operator-event-rule/v1.0.0/` (per-rule, reused per `km event add`)
3. New singleton `km-runner` Lambda — `km` binary baked in, takes command from event input, execs it
4. Typed manifest schema (`pkg/events/manifest.go`) → JSON Schema for IDE completion
5. New `events:` key in `km-config.yaml` (absent = empty, no migration)
6. New `km doctor` check: `operator_event_rules_healthy`
7. Hard cut on `km at --cron` and recurring NL expressions

**Out of scope (deferred):** `type: code` target, `km event replay` CLI, multi-target IAM derivation beyond Lambda/SQS/SNS, `km at` deprecation period.

</domain>

<decisions>
## Implementation Decisions

### Architecture — Shared bus + singleton runner

- **Single shared bus** `{prefix}-operator-events` (custom EventBridge bus, NOT default bus). Custom bus gives clean isolation + per-bus archive/replay + scoped IAM. Trade-off accepted: anything firing into it needs explicit `events:PutEvents`.
- **Archive enabled by default** at bus level. `--no-archive` per-rule opt-out exists but archive default is ON. Cost (~$0.10/GB/mo) accepted in exchange for zero-maintenance replay capability.
- **7-day replay window** provisioned on the archive, but **no `km event replay` CLI in v1**. Replay is rare enough to defer to console/SDK; revisit if used in anger.
- **Singleton `km-runner` Lambda** has the `km` binary baked in (mirroring `create-handler` userdata bake), reads `command` string from event input, execs `km <command>`, captures stdout+stderr to CloudWatch.
- **km-runner IAM = same 14-policy bundle from `infra/modules/km-operator-policy/v1.0.0/`** (the module Phase 80 just consolidated). Reusing keeps the trust surface coherent — km-runner can do anything operator can do because that's the point.

### CLI — Modeled on Phase 80 `km cluster add`

- `km event add '<NL when>' <km-cmd> --name <slug>` — NL → cron/at expression via `pkg/at/parser.go`, generates `events/<slug>.yaml`, runs `terragrunt apply`, persists to `km-config.yaml` under new `events:` key. Idempotent.
- `km event apply events/*.yaml` — for hand-edited manifests (richer-than-flag-soup rules).
- `km event list` — reads `km-config.yaml` + thin EventBridge `DescribeRule` for status.
- `km event rm <name> [--yes]` — runs `terragrunt destroy` then prunes config entry. Idempotent (no-op if rule already gone).
- Target flags: **`--target-km '<cmd>'`** (shorthand, common case) OR **`--target <arn>`** (repeatable for fanout).
- Optional flags: `--input <json>` (static target input override), `--dlq <sqs-arn>`, `--description <text>`, `--no-archive`.
- **Escape hatch:** `--hcl <file>` drops raw HCL into the live tree for anything richer than flag coverage (multi-target, input transformers, custom retry policies).
- `--dry-run=true` default (terragrunt plan only); `--dry-run=false` to apply. Matches `km cluster add` ergonomics.

### Target types (v1)

| Type | Operator writes | Module emits |
|---|---|---|
| `arn` | `--target arn:aws:lambda:...` (repeatable) | EventBridge target wired to existing ARN; target-role IAM derived from service prefix (Lambda → `lambda:InvokeFunction`, SQS → `sqs:SendMessage`, SNS → `sns:Publish`) |
| `km` | `--target-km 'doctor --all-regions'` | EventBridge target wired to singleton km-runner Lambda; command goes into target `Input` JSON |

`type: code` (operator-supplied Python/Go compiled to Lambda) **explicitly out of scope** — sandboxes are the code-execution substrate. `type: km` can invoke `km create profiles/job.yaml` to run arbitrary code in a sandbox.

### Manifest schema

```yaml
# events/<name>.yaml
name: nightly-doctor
schedule: cron(0 9 * * ? *)        # OR event_pattern: <json>
target:
  type: km                         # or "arn"
  command: doctor --all-regions    # for type: km
  arns: ["arn:aws:lambda:..."]     # for type: arn (list, fanout)
input: '{"foo":"bar"}'             # optional, static
dlq: arn:aws:sqs:...               # optional
description: "Nightly platform sweep"
archive: true                      # default true
```

- Typed Go struct in **new package `pkg/events/manifest.go`** (NOT inside `pkg/profile/` — keep event config separate from sandbox profile config).
- JSON Schema generated from struct → checked into repo for IDE/editor completion. Mirror the SandboxProfile pattern.
- Validation rules: exactly one of `schedule`/`event_pattern`; `target.type` matches its companion field (`command` for `km`, `arns` for `arn`); name is kebab-case slug.

### `km-config.yaml` schema

New top-level `events:` key:

```yaml
events:
  - name: nightly-doctor
    type: schedule              # or "pattern"
    expression: "cron(0 9 * * ? *)"
    target_type: km             # or "arn"
    targets: ["arn:aws:lambda:..."]   # the km-runner ARN for type:km
    rule_arn: "arn:aws:events:..."
    role_arn: "arn:aws:iam::..."
```

Absent `events:` key = empty slice — existing installs need no migration. Same pattern as Phase 80's `clusters:`.

### NL parser sharing

- Existing `pkg/at/parser.go` (303 lines, both one-off `at(...)` and recurring `cron(...)`/`rate(...)` paths) is **shared** between commands.
- `km at` enforces `IsRecurring=false` after parse — error message: `"recurring expressions not supported by km at; use km event '<when>' <cmd> for durable schedules"`.
- `km event` allows both. One-off events with rich targets (DLQ, multi-target) are rare but legal.

### Doctor check

- New: `operator_event_rules_healthy` — for every entry in `km-config.yaml`'s `events:`, verify the corresponding EventBridge rule exists and is `ENABLED`. WARN (not ERROR) for missing rules — matches the "opt-in feature can't be hard failure" pattern from Slack inbound checks.

### Rollout — Operator-applied terragrunt, NO `--sidecars` propagation

- **No sandbox-side code changes**, no management Lambda changes. `make build` only (no `km init --sidecars`, no `km init`).
- Operator runs `terragrunt apply` from workstation as part of `km event add`/`apply` (same as `km cluster add`).
- **Exception:** the one-time `operator-event-bus/v1.0.0/` module needs to be applied once per region by the operator before any `km event add` works. Suggest wiring this into `km init` so existing regions pick it up. Decision: yes, fold into `km init` because it's regional infrastructure like ttl-handler and create-handler.
- Existing sandboxes: unaffected (no userdata changes).

### Idempotency & rollback

- `km event add --name foo`: if `foo` already exists in `km-config.yaml`, return existing rule ARN (no-op). Safe to re-run after partial failure.
- If `terragrunt apply` succeeds but `km-config.yaml` write fails: log warning; user can re-run `km event rm <name>` to clean up by name (since terraform state has the rule). Same rollback story as Phase 80 `km cluster add`.

### Claude's Discretion

- Exact internal layout of `pkg/events/` (single file vs split manifest.go + parser.go + validate.go)
- Wave/dependency structure of the plan (likely: Wave 1 = module + Go schema, Wave 2 = CLI + km-runner, Wave 3 = doctor + km at cleanup + docs)
- CloudWatch log group structure for km-runner Lambda (default `/aws/lambda/{prefix}-km-runner` follows convention)
- Idempotency token strategy for terragrunt apply (likely `terragrunt apply --auto-approve` with the rule name as natural idempotency key — same as `km cluster add`)
- Exact error message wording for `km at` recurring-rejection — should point at `km event` and include a one-line "translate this command" hint
- Whether to generate `event_pattern: <json>` support in v1 CLI flags or leave that to `--hcl` escape hatch. Lean: `--event-pattern <file.json>` flag exists in v1 even though most use cases are `--schedule`.
- DLQ default sizing/retention if `--dlq` not specified — likely no default DLQ at all (operator must opt in explicitly).

</decisions>

<specifics>
## Specific Ideas

- **Precedent: Phase 80 `km cluster add`** is the exact pattern to copy — `infra/modules/{name}/v1.0.0/` + `infra/live/{region}/{name}/terragrunt.hcl` generated at runtime + `terragrunt apply` from CLI + persist metadata to `km-config.yaml` + idempotent add/rm + `km doctor` check.
- **Reuse Phase 80's `km-operator-policy/v1.0.0/`** for km-runner Lambda's IAM. No new policy bundle.
- **Reuse `pkg/at/parser.go`** for NL time parsing in both `km at` and `km event`. Don't fork.
- **Reuse `pkg/profile/` pattern** for typed Go struct → JSON Schema. New package `pkg/events/` mirrors the shape, doesn't share code.
- **Current `km at --cron` is at `internal/app/cmd/at.go:317`** — that's the flag to strip.
- **Current `pkg/aws/scheduler.go` (90 lines)** wraps EventBridge Scheduler SDK for `km at`. `km event` should NOT use this — it goes through Terragrunt, not direct SDK.
- **km-runner Lambda runtime:** Go (consistent with all other platform Lambdas — `ttl-handler`, `budget-enforcer`, `create-handler`, `email-create-handler`, `km-actions-runner-token`).
- **CloudWatch log group + KMS** for km-runner — match existing Lambda module conventions in `infra/modules/`.

</specifics>

<deferred>
## Deferred Ideas

- **`type: code` target** — operator-supplied Python/Go compiled to per-target Lambda. Deferred indefinitely. Rationale: sandboxes are already the platform's isolated, policy-governed compute substrate. `type: km` with `km create profiles/job.yaml` gives operators arbitrary code execution without building a parallel Lambda-build pipeline. Revisit only if there's a use case sandboxes can't serve (e.g., sub-second latency, sub-cent cost, or stateless event transformations).
- **`km event replay <name> --from <ts> --to <ts>`** — replay CLI surface. Bus archive is provisioned (7-day window), but the CLI to drive replay is deferred. Operators can replay via console/SDK if needed in v1. Revisit if replay becomes a common operation.
- **`km at` deprecation period for `--cron`** — explicitly declined by operator decision. Hard cut: strip `--cron`, error on recurring NL expressions, point at `km event` in the error message. No grace period.
- **Multi-target IAM derivation beyond Lambda/SQS/SNS** — if an operator names a target ARN with a service prefix the module doesn't recognize (Kinesis, Step Functions, ECS Run Task), v1 errors with "unsupported target service; use --hcl escape hatch". Extend the derived-IAM table later if demand emerges.
- **Event-pattern rules with rich filters** — basic `--event-pattern <file.json>` works in v1; complex pattern composition (multiple sources, content-based filters) goes through `--hcl`.
- **Per-rule custom retry policies and dead-letter routing semantics beyond a single SQS DLQ ARN** — defer to `--hcl` escape hatch.
- **Cross-region rule fanout** — v1 is per-region; operator runs `km event add` once per region they want the rule in. No special "global" mode.

</deferred>

---

*Phase: 83-km-event-operator-controlled-eventbridge*
*Context gathered: 2026-05-16 from design conversation*
