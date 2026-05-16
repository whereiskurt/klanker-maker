# Phase 83: km event operator-controlled EventBridge — Research

**Researched:** 2026-05-16
**Domain:** EventBridge Rules (custom bus + archive), Terragrunt CLI pattern, Go Lambda packaging, km-config.yaml schema extension
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Architecture — Shared bus + singleton runner**
- Single shared bus `{prefix}-operator-events` (custom EventBridge bus, NOT default bus). Clean isolation + per-bus archive/replay + scoped IAM.
- Archive enabled by default at bus level. `--no-archive` per-rule opt-out. 7-day replay window, no `km event replay` CLI in v1.
- Singleton `km-runner` Lambda with `km` binary baked in. Reads `command` from event input. Execs `km <command>`. Output to CloudWatch.
- km-runner IAM = same 14-policy bundle from `infra/modules/km-operator-policy/v1.0.0/`.

**CLI — Modeled on Phase 80 `km cluster add`**
- `km event add '<NL when>' <km-cmd> --name <slug>` — NL parse → cron/at expression, generates `events/<slug>.yaml`, runs terragrunt apply, persists to `km-config.yaml`.
- `km event apply events/*.yaml` — for hand-edited manifests.
- `km event list` — reads `km-config.yaml` + thin DescribeRule for status.
- `km event rm <name> [--yes]` — terragrunt destroy + prune config. Idempotent.
- `--target-km '<cmd>'` shorthand or `--target <arn>` (repeatable).
- Optional: `--input <json>`, `--dlq <sqs-arn>`, `--description`, `--no-archive`.
- `--hcl <file>` escape hatch for anything richer than flag coverage.
- `--dry-run=true` default (plan only); `--dry-run=false` to apply.

**Target types (v1)**
- `arn`: EventBridge target wired to existing ARN; IAM derived from service prefix (Lambda → `lambda:InvokeFunction`, SQS → `sqs:SendMessage`, SNS → `sns:Publish`).
- `km`: EventBridge target wired to singleton km-runner Lambda; command goes into target `Input` JSON.

**Manifest schema**
- YAML: `events/<name>.yaml` with fields: `name`, `schedule`/`event_pattern`, `target.type`, `target.command`/`target.arns`, `input`, `dlq`, `description`, `archive`.
- Typed Go struct in new package `pkg/events/manifest.go` (NOT `pkg/profile/`).
- JSON Schema generated from struct → checked into repo. Mirror SandboxProfile pattern.
- Validation: exactly one of `schedule`/`event_pattern`; `target.type` matches companion field; name is kebab-case slug.

**`km-config.yaml` schema** — new `events:` key, absent = empty slice, no migration required.

**NL parser sharing** — `pkg/at/parser.go` shared between commands.
- `km at` enforces `IsRecurring=false` after parse (error pointing at `km event`).
- `km event` allows both. One-off on custom bus is legal.

**Doctor check** — `operator_event_rules_healthy`: every `events:` entry verified via DescribeRule. WARN (not ERROR).

**Rollout** — `make build` only. No `--sidecars`, no `km init --lambdas`. But `operator-event-bus/v1.0.0/` folded into `km init` so existing regions auto-pick-up.

**Idempotency** — `km event add --name foo`: no-op if already in config. Partial failure: log warning, user runs `km event rm <name>` to clean up.

**`km at --cron` hard cut** — strip the flag, error on recurring NL expressions, point error message at `km event`. No deprecation.

### Claude's Discretion

- Exact internal layout of `pkg/events/` (single file vs split).
- Wave/dependency structure of the plan.
- CloudWatch log group structure for km-runner Lambda.
- Idempotency token strategy for terragrunt apply.
- Exact error message wording for `km at` recurring-rejection.
- Whether `--event-pattern <file.json>` flag exists in v1 CLI flags (recommendation: yes, include it).
- DLQ default sizing/retention if `--dlq` not specified (recommendation: no default DLQ).

### Deferred Ideas (OUT OF SCOPE)

- `type: code` target.
- `km event replay <name> --from <ts> --to <ts>` CLI.
- `km at` deprecation period for `--cron` (hard cut, no grace period).
- Multi-target IAM derivation beyond Lambda/SQS/SNS.
- Event-pattern rules with complex content-based filters (use `--hcl`).
- Per-rule custom retry policies beyond single SQS DLQ ARN.
- Cross-region rule fanout.
</user_constraints>

---

## Summary

Phase 83 adds a second EventBridge lane for durable, declarative, operator-controlled work. The key technical decisions are already locked: custom EventBridge bus with archive (using EventBridge Rules, not Scheduler), a singleton `km-runner` Lambda, and the `km cluster add` pattern from Phase 80 used verbatim for CLI mechanics. The planner needs to know exactly how the existing code is shaped so tasks don't reinvent anything already present.

The most important technical finding: **EventBridge Rules do NOT support the `at()` one-off syntax** — that is Scheduler-only. Rules support only `cron(...)` and `rate(...)`. This means `km event`'s one-off support must generate a `cron(...)` expression that fires at a specific datetime (e.g., `cron(30 14 16 5 ? 2026)`) rather than an `at()` expression. The existing `pkg/at/parser.go` produces `at(...)` for one-off inputs, which is not directly usable for EventBridge Rules; a conversion step is needed.

The existing codebase uses EventBridge Rules on the **default bus** for `SandboxCreate`, `SandboxIdle`, and `SandboxCommand`. The `km-operator-policy/v1.0.0/` module's `EventBridgePutEvents` policy is scoped to `event-bus/default`. Phase 83 adds `event-bus/{prefix}-operator-events` to that scope (additive change to the policy), so km-runner can emit events to the operator bus if needed.

**Primary recommendation:** Follow `cluster.go` mechanically for event.go. The pattern is battle-tested, has full unit tests with injected seams, and covers every edge case (idempotency, rollback, dry-run, region.hcl bootstrap).

---

## Standard Stack

### Core
| Library/Tool | Version | Purpose | Why Standard |
|---|---|---|---|
| `aws-lambda-go` | (existing in go.mod) | Lambda runtime, `events.CloudWatchEvent` type | Used by all other Lambdas |
| `github.com/aws/aws-sdk-go-v2/service/cloudwatchevents` | v2 | DescribeRule for doctor check | Already in project |
| `github.com/spf13/cobra` | (existing) | CLI command tree | Standard for all commands |
| `gopkg.in/yaml.v3` | (existing) | Manifest YAML parse/write | Used by cluster.go and profile/ |
| Terraform `aws_cloudwatch_event_bus` | latest provider | Custom bus resource | Verified in Terraform registry |
| Terraform `aws_cloudwatch_event_archive` | latest provider | Bus-level archive | Verified in Terraform registry |
| Terraform `aws_cloudwatch_event_rule` | latest provider | Per-rule schedule/pattern | Used in create-handler, ttl-handler |
| Terraform `aws_cloudwatch_event_target` | latest provider | Rule→Lambda wiring | Used in existing modules |

### Supporting
| Library | Version | Purpose | When to Use |
|---|---|---|---|
| `pkg/at/parser.go` | (internal) | NL→ScheduleSpec for `km event add` | Shared, not forked |
| `pkg/terragrunt` | (internal) | Runner with Plan/Apply/Destroy/Output | Same seam as cluster.go |
| `infra/modules/km-operator-policy/v1.0.0/` | (internal) | 14-policy IAM bundle for km-runner | Reused verbatim |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|---|---|---|
| EventBridge Rules on custom bus | EventBridge Scheduler | Scheduler lacks archive/replay, rules use IAM-derived targeting |
| Singleton km-runner Lambda | Per-rule Lambdas | Singleton is simpler, km-runner is the pattern; per-rule is deferred `type:code` |

**Installation:** No new dependencies. All tooling is already in `go.mod` and the Terraform AWS provider.

---

## Architecture Patterns

### Recommended Project Structure

```
infra/modules/
  operator-event-bus/v1.0.0/    # Bus + archive + km-runner IAM + Lambda
    main.tf
    variables.tf
    outputs.tf
  operator-event-rule/v1.0.0/   # Per-rule: rule + target + target-role IAM
    main.tf
    variables.tf
    outputs.tf

infra/live/{region-label}/
  operator-event-bus/            # km init applies this once per region
    terragrunt.hcl               # generated by km init bootstrap code

cmd/km-runner/
  main.go                        # Lambda: reads event.detail.command, runs km <cmd>

pkg/events/
  manifest.go                    # EventManifest struct + JSON Schema embed
  validate.go                    # Validation logic (schedule/pattern XOR, slug check)

internal/app/cmd/
  event.go                       # km event add/list/rm/apply + EventRunner interface
  event_test.go                  # unit tests with mockEventRunner

events/                          # git-tracked operator manifests
  nightly-doctor.yaml            # example
```

### Pattern 1: Runtime-generated terragrunt.hcl (from cluster.go)

**What:** `km event add` generates a per-rule `terragrunt.hcl` into `infra/live/{region-label}/event-rule-{name}/` using string template substitution, then invokes `terragrunt.Runner.Apply()`.

**When to use:** Any time a CLI operation needs to provision persistent infra via Terragrunt.

**Key mechanics from `cluster.go`:**

```go
// Template pattern — verbatim from cluster.go lines 96-143
const eventRuleTerragruntHCLTemplate = `locals {
  repo_root     = dirname(find_in_parent_folders("CLAUDE.md"))
  ...
}
include "root" {
  path = find_in_parent_folders("root.hcl")
}
remote_state {
  backend = "s3"
  ...key = ".../${local.region_label}/event-rule-{RULE_NAME}/terraform.tfstate"
}
terraform {
  # Use // so Terragrunt copies infra/modules/ into cache, making sibling
  # modules (like km-operator-policy/v1.0.0) resolvable via relative paths.
  source = "${local.repo_root}/infra/modules//operator-event-rule/v1.0.0"
}
inputs = {
  rule_name   = "{RULE_NAME}"
  schedule    = "{SCHEDULE_EXPRESSION}"
  target_type = "{TARGET_TYPE}"
  command     = "{COMMAND}"
  ...
}`

// Idempotency check first (from RunClusterAdd lines 349-353)
for _, e := range cfg.Events {
    if e.Name == name {
        fmt.Printf("Event %q already registered: %s\n", name, e.RuleARN)
        return nil
    }
}

// ExportConfigEnvVars before ANY terragrunt call (mandatory — prevents 403 on non-default prefix)
ExportConfigEnvVars(cfg)

// region.hcl bootstrap if missing (from RunClusterAdd lines 405-415)
// Apply → Output → extract rule_arn + role_arn → append to cfg.Events
// PersistEventsConfigFunc — if fails, log warning, user runs km event rm
```

### Pattern 2: km-runner Lambda — bake km binary, read command from event detail

**What:** Like `create-handler`, km-runner is a zip Lambda (`provided.al2023`, `arm64`). It reads `event.detail.command` and runs `km` as a subprocess. Unlike create-handler, km-runner does NOT need to download the km binary at cold start — it is baked into the zip directly (simpler than create-handler's container + S3-download pattern).

**Lambda packaging:**

```makefile
# Add to build-lambdas target in Makefile
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/km ./cmd/km/
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/bootstrap ./cmd/km-runner/
cd build && zip -j km-runner.zip bootstrap km && rm bootstrap km
```

**cmd/km-runner/main.go pattern:**

```go
// Source: cmd/create-handler/main.go lines 135-140 (Handle pattern)
type RunnerEvent struct {
    Command string `json:"command"`  // e.g. "doctor --all-regions"
}

func (h *KMRunner) Handle(ctx context.Context, ebEvent events.CloudWatchEvent) error {
    var ev RunnerEvent
    if err := json.Unmarshal(ebEvent.Detail, &ev); err != nil {
        return fmt.Errorf("km-runner: unmarshal detail: %w", err)
    }
    // os.exec km binary with ev.Command split into args
    // stdout+stderr captured; non-zero exit returns error to Lambda runtime
}
```

**Module wiring:** `operator-event-bus/v1.0.0/main.tf` provisioned km-runner Lambda (zip from var.lambda_zip_path), CloudWatch log group `/aws/lambda/{prefix}-km-runner`, and the IAM role consuming `km-operator-policy/v1.0.0/`.

### Pattern 3: EventBridge Rules on Custom Bus (NOT Scheduler)

**Critical distinction:**

| Feature | EventBridge Rules | EventBridge Scheduler |
|---|---|---|
| Custom bus | YES | NO (only default bus targets) |
| Archive/Replay | YES (bus-level archive) | NO |
| Cron expressions | YES | YES |
| Rate expressions | YES | YES |
| One-time `at(ISO8601)` | **NO** | YES |
| IAM execution role | NO (Lambda permission) | YES (scheduler role) |

**One-off support via Rules:** EventBridge Rules do not support `at(...)` expressions. To fire once, use a cron expression pinned to a specific date: `cron(M H D Mon ? YYYY)`. The planner must ensure that when `pkg/at/parser.go` returns a `ScheduleSpec` with `IsRecurring=false` (i.e., an `at(...)` expression), `km event add` converts it to an equivalent `cron(...)` with year pinned.

Conversion logic:
```go
// at(2026-05-20T14:30:00) → parse datetime → cron(30 14 20 5 ? 2026)
func atExprToCron(atExpr string) (string, error) {
    // strip "at(" prefix and ")" suffix
    // parse ISO8601 datetime
    // return fmt.Sprintf("cron(%d %d %d %d ? %d)", min, hour, day, month, year)
}
```

**Terraform resource shape for custom bus + archive:**

```hcl
# operator-event-bus/v1.0.0/main.tf
resource "aws_cloudwatch_event_bus" "operator" {
  name = "${var.resource_prefix}-operator-events"
  tags = { "km:component" = "operator-event-bus" }
}

resource "aws_cloudwatch_event_archive" "operator" {
  name             = "${var.resource_prefix}-operator-events-archive"
  event_source_arn = aws_cloudwatch_event_bus.operator.arn
  retention_days   = 7
}

# km-runner Lambda (zip from var.lambda_zip_path)
resource "aws_lambda_function" "km_runner" {
  function_name = "${var.resource_prefix}-km-runner"
  role          = aws_iam_role.km_runner.arn
  filename      = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)
  handler       = "bootstrap"
  runtime       = "provided.al2023"
  architectures = ["arm64"]
  timeout       = 900
  memory_size   = 1536
  ephemeral_storage { size = 10240 }
  environment { variables = { KM_RESOURCE_PREFIX = var.resource_prefix, ... } }
  lifecycle { replace_triggered_by = [aws_iam_role.km_runner] }
}
```

```hcl
# operator-event-rule/v1.0.0/main.tf
resource "aws_cloudwatch_event_rule" "rule" {
  name           = "${var.resource_prefix}-event-${var.rule_name}"
  event_bus_name = var.event_bus_name   # NAME (not ARN) of the shared custom bus — see Pitfall 1
  schedule_expression = var.schedule    # cron(...) or rate(...)
  # OR: event_pattern = var.event_pattern (for non-schedule rules)
  state       = "ENABLED"
}

resource "aws_cloudwatch_event_target" "target" {
  rule           = aws_cloudwatch_event_rule.rule.name
  event_bus_name = var.event_bus_name
  target_id      = "${var.resource_prefix}-${var.rule_name}"
  arn            = var.target_arn   # km-runner ARN for type:km
  role_arn       = aws_iam_role.target.arn  # events.amazonaws.com role
  input          = var.input_json   # static input override
}

# IAM role for EventBridge to invoke Lambda (events.amazonaws.com)
resource "aws_iam_role" "target" {
  name = "${var.resource_prefix}-event-rule-${var.rule_name}-target"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "events.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "invoke" {
  role = aws_iam_role.target.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["lambda:InvokeFunction"]   # or sqs:SendMessage, sns:Publish
      Resource = var.target_arn
    }]
  })
}

# Lambda permission: allow EventBridge to invoke km-runner from this rule
resource "aws_lambda_permission" "rule" {
  statement_id  = "AllowEventBridgeRule-${var.rule_name}"
  action        = "lambda:InvokeFunction"
  function_name = var.target_arn
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.rule.arn
}
```

### Pattern 4: Config Persistence (from cluster.go)

```go
// PersistEventsConfig — mirrors PersistClustersConfig exactly (lines 214-247)
func PersistEventsConfig(configPath string, events []config.EventConfig) error {
    data, err := os.ReadFile(configPath)
    // yaml.Unmarshal into raw map[string]interface{}
    // set raw["events"] = list
    // yaml.Marshal → os.WriteFile with header
    // No fsync, no rename-based atomic write — matches existing pattern
}
```

No atomic rename or fsync is used in the existing pattern — simple `os.WriteFile` with 0o600 permission. Match this exactly.

### Pattern 5: JSON Schema embed (from pkg/profile/schema.go)

```go
// pkg/events/manifest.go
package events

import _ "embed"

//go:embed schemas/event_manifest.schema.json
var eventManifestSchemaJSON []byte

func SchemaJSON() []byte { return eventManifestSchemaJSON }
```

The JSON Schema is hand-authored or generated from the Go struct and checked in. The `schema.go` pattern in `pkg/profile/` uses a compiled jsonschema validator; `pkg/events/` should mirror this.

### Anti-Patterns to Avoid

- **Using EventBridge Scheduler for km event:** Scheduler does not support custom buses or archive/replay. Use EventBridge Rules.
- **Using the default bus for km event rules:** Existing rules on the default bus (`SandboxCreate`, `SandboxIdle`, `SandboxCommand`) could match if patterns overlap. The custom `{prefix}-operator-events` bus is isolated.
- **Updating `km-operator-policy` for the new bus without a `moved {}` block:** The policy module already has an `EventBridgePutEvents` policy scoped to `event-bus/default` (line 546 of main.tf). Adding `event-bus/{prefix}-operator-events` to that resource list is additive and safe, but requires a new policy resource, not modification of the existing one (to avoid YAML drift in the moved{} chain).
- **Calling `PersistEventsConfigFunc` before checking for partial failure:** The rollback contract from Phase 80 is: if apply succeeds but config write fails, log warning and tell the user to run `km event rm <name>`. Do NOT auto-destroy.
- **km-runner reading the km binary from S3 at cold start (create-handler pattern):** km-runner is simpler — bake km directly into the zip alongside `bootstrap`. No toolchain download needed.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---|---|---|---|
| NL→cron expression | Custom time parser | `pkg/at/parser.go` (shared) | Already handles "every monday at 1pm" → `cron(0 13 ? * 2 *)` |
| Terragrunt invocation | Direct subprocess | `pkg/terragrunt.Runner` + seam interface | Tested, handles output capture, verbose mode |
| YAML safe write | Custom atomic write | Match `PersistClustersConfig` (`os.WriteFile`) | Project convention; no atomic rename in any existing code |
| EventBridge one-off | Custom scheduling | `at(...)` → `cron(M H D Mon ? YYYY)` conversion | EventBridge Rules don't support `at()` |
| IAM derivation for target | Per-service logic | Derive from ARN prefix: `lambda:` → `lambda:InvokeFunction`, `sqs:` → `sqs:SendMessage`, `sns:` → `sns:Publish` | v1 supports only these 3; others error with `--hcl` hint |

**Key insight:** Phase 80's `cluster.go` is the exact template. Copy its structure (interface, factory var, seam, exported RunX functions, test file) rather than reinventing any of it.

---

## Common Pitfalls

### Pitfall 1: EventBridge Rules `event_bus_name` requires ARN, not name, for custom buses in some SDK calls

**What goes wrong:** The Terraform resource `aws_cloudwatch_event_rule` accepts the bus name (string) in `event_bus_name`. But the AWS SDK `DescribeRule` call for doctor checks requires the bus name (not ARN). These are different from `PutEvents` which takes the bus ARN. Mixing them causes 400 errors.

**How to avoid:** Use bus name (string, not ARN) consistently in Terraform resources. Use bus name in SDK `DescribeRule` calls. Use bus ARN in `aws_cloudwatch_event_archive.event_source_arn` and in `events:PutEvents` IAM policies.

### Pitfall 2: `at(...)` from parser.go is not valid for EventBridge Rules

**What goes wrong:** `pkg/at/parser.go` returns `at(2026-05-20T14:30:00)` for one-off inputs. Passing this directly to `aws_cloudwatch_event_rule.schedule_expression` causes Terraform apply to fail with "InvalidParameterException: Invalid schedule expression".

**How to avoid:** In `km event add`, after calling `atpkg.Parse(...)`, check `spec.IsRecurring`. If false (one-off), convert the `at(...)` expression to a pinned cron expression: parse the ISO8601 datetime from the `at(...)` string and emit `cron(MIN HOUR DAY MONTH ? YEAR)`.

**Warning signs:** `apply failed: InvalidParameterException: Invalid schedule expression` with `at(...)` in the expression.

### Pitfall 3: Missing `ExportConfigEnvVars` before terragrunt

**What goes wrong:** Non-default `resource_prefix` installs (e.g., `kpf`, `stg`) hit 403 HeadBucket on the state bucket because `KM_RESOURCE_PREFIX` is not set in the environment.

**How to avoid:** Call `ExportConfigEnvVars(cfg)` as step 3 in `RunEventAdd`, before writing the HCL or calling `runner.Apply()`. This is documented in the existing `RunClusterAdd` code comment at line 397.

### Pitfall 4: km-runner Lambda needs km binary that matches the bus region

**What goes wrong:** The km binary baked into km-runner.zip is region-specific if `km-config.yaml` has `region:` set. Running `km create` or `km doctor` from inside the Lambda in a different region causes "state bucket not found" failures.

**How to avoid:** Pass `KM_REGION_LABEL`, `KM_STATE_BUCKET`, `KM_RESOURCE_PREFIX`, `KM_ARTIFACTS_BUCKET`, and `KM_OPERATOR_EMAIL` as Lambda environment variables (mirroring create-handler's pattern). km-runner does NOT need the full toolchain — no Terraform/Terragrunt download, just the km binary which handles its own AWS SDK calls.

### Pitfall 5: km-operator-policy's EventBridgePutEvents scoped to default bus only

**What goes wrong:** km-runner and `km event add` CLI need to put events on `{prefix}-operator-events` (custom bus). The existing `km-operator-policy/v1.0.0/main.tf` line 546 scopes `events:PutEvents` to `event-bus/default` only. km-runner will get AccessDenied if it tries to emit events to the custom bus.

**How to avoid:** Add the custom bus ARN to the `EventBridgePutEvents` resource list in `km-operator-policy/v1.0.0/main.tf`. This is an additive change (no `moved {}` needed since we're adding to an existing policy's Resource list, not moving a resource address). The module already exists and is referenced by both create-handler and the new km-runner module.

### Pitfall 6: `aws_cloudwatch_event_archive` must reference bus ARN, not name

**What goes wrong:** The `event_source_arn` argument in `aws_cloudwatch_event_archive` expects the full ARN of the event bus. Passing the bus name causes `InvalidParameterException`.

**How to avoid:** Use `aws_cloudwatch_event_bus.operator.arn` (Terraform attribute), not `.name`.

### Pitfall 7: region.hcl bootstrap for non-primary regions

**What goes wrong:** `km event add` with `--region us-west-2` on a fresh operator machine may not have `infra/live/usw2/region.hcl`. The cluster.go pattern already handles this (lines 405-415).

**How to avoid:** Copy the `region.hcl` bootstrap block from `RunClusterAdd` verbatim into `RunEventAdd`.

### Pitfall 8: Double-slash source path for module sibling resolution

**What goes wrong:** If the terragrunt.hcl source path uses single slash (`infra/modules/operator-event-rule/v1.0.0`), Terragrunt only copies the leaf module directory into cache — sibling modules like `km-operator-policy/v1.0.0/` are not accessible via relative `../../km-operator-policy/v1.0.0` paths inside `operator-event-rule/v1.0.0/main.tf`.

**How to avoid:** Use the double-slash pattern in the source: `"${local.repo_root}/infra/modules//operator-event-rule/v1.0.0"`. This is explicitly documented in the existing `clusterTerragruntHCLTemplate` comment at line 95.

---

## Code Examples

### km cluster add flow — exact precedent for km event add

```go
// Source: internal/app/cmd/cluster.go RunClusterAdd (lines 344-499)
// Steps for km event add (copy this flow exactly):
//
// 1. Idempotency: if name in cfg.Events → print existing ARN → return nil
// 2. Load AWS config + ValidateCredentials
// 3. ExportConfigEnvVars(cfg)   ← MANDATORY before any terragrunt call
// 4. Compute regionLabel, dirs; bootstrap region.hcl if missing
// 5. os.MkdirAll(stackDir); generate HCL from template; os.WriteFile
// 6. If dryRun: runner.Plan → print note → return (no state mutation)
// 7. runner.Apply → runner.Output → extract rule_arn, role_arn
// 8. cfg.Events = append(cfg.Events, ...); PersistEventsConfigFunc
//    If PersistEventsConfigFunc fails: return error with "km event rm" hint
//    Do NOT call runner.Destroy
// 9. Print confirmation (event name + rule ARN)
```

### PersistClustersConfig — mirror for PersistEventsConfig

```go
// Source: internal/app/cmd/cluster.go PersistClustersConfig (lines 214-247)
// Pattern: read file → unmarshal to raw map → set key → marshal → write
func PersistEventsConfig(configPath string, events []config.EventConfig) error {
    data, _ := os.ReadFile(configPath)
    var raw map[string]interface{}
    yaml.Unmarshal(data, &raw)
    if raw == nil { raw = make(map[string]interface{}) }
    list := make([]interface{}, len(events))
    for i, e := range events {
        list[i] = map[string]interface{}{
            "name": e.Name, "type": e.Type, "expression": e.Expression,
            "target_type": e.TargetType, "targets": e.Targets,
            "rule_arn": e.RuleARN, "role_arn": e.RoleARN,
        }
    }
    raw["events"] = list
    newData, _ := yaml.Marshal(raw)
    header := "# km-config.yaml — generated by km configure\n# Add this file to .gitignore\n\n"
    return os.WriteFile(configPath, append([]byte(header), newData...), 0o600)
}
```

### config.EventConfig — mirror ClusterConfig shape

```go
// Source: internal/app/config/config.go ClusterConfig (lines 20-26)
// Pattern to follow exactly for EventConfig:
type EventConfig struct {
    Name        string   `mapstructure:"name"         yaml:"name"`
    Type        string   `mapstructure:"type"         yaml:"type"`         // "schedule" or "pattern"
    Expression  string   `mapstructure:"expression"   yaml:"expression"`
    TargetType  string   `mapstructure:"target_type"  yaml:"target_type"`  // "km" or "arn"
    Targets     []string `mapstructure:"targets"      yaml:"targets"`      // km-runner ARN for type:km
    RuleARN     string   `mapstructure:"rule_arn"     yaml:"rule_arn"`
    RoleARN     string   `mapstructure:"role_arn"     yaml:"role_arn"`
}
// Add to Config struct:
//   Events []EventConfig `mapstructure:"events" yaml:"events"`
// Add to Load():
//   v.SetDefault("events", []interface{}{})
// Add UnmarshalKey("events", &cfg.Events) in load block
```

### at-to-cron conversion for one-off EventBridge Rules

```go
// EventBridge Rules do NOT support at() — must convert to pinned cron
// Source: verified via AWS docs https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-scheduled-rule-pattern.html
func atExprToCronForRules(atExpr string) (string, error) {
    // atExpr like "at(2026-05-20T14:30:00)"
    inner := strings.TrimPrefix(strings.TrimSuffix(atExpr, ")"), "at(")
    t, err := time.Parse("2006-01-02T15:04:05", inner)
    if err != nil {
        return "", fmt.Errorf("cannot parse at() expression %q: %w", atExpr, err)
    }
    // EventBridge Rules cron: min hour day-of-month month day-of-week(?) year
    return fmt.Sprintf("cron(%d %d %d %d ? %d)", t.Minute(), t.Hour(), t.Day(), t.Month(), t.Year()), nil
}
```

### Doctor check pattern — mirror presence daemon check

```go
// Source: internal/app/cmd/doctor.go checkPresenceDaemonHealthy pattern (line ~2714)
// For operator_event_rules_healthy:
func checkOperatorEventRulesHealthy(ctx context.Context, ebClient EventRulesAPI, events []config.EventConfig, busName string) CheckResult {
    if len(events) == 0 {
        return CheckResult{Name: "operator_event_rules_healthy", Status: StatusOK, Message: "no event rules configured"}
    }
    var stale []string
    for _, e := range events {
        out, err := ebClient.DescribeRule(ctx, &eventbridgepkg.DescribeRuleInput{
            Name:         aws.String(e.RuleARN),  // or derived name
            EventBusName: aws.String(busName),
        })
        if err != nil || *out.State != "ENABLED" {
            stale = append(stale, e.Name)
        }
    }
    if len(stale) > 0 {
        return CheckResult{Name: "operator_event_rules_healthy", Status: StatusWarn,
            Message: fmt.Sprintf("%d rule(s) not ENABLED: %s", len(stale), strings.Join(stale, ", "))
        }
    }
    return CheckResult{Name: "operator_event_rules_healthy", Status: StatusOK}
}
```

### Lambda build entry in Makefile

```makefile
# Add to build-lambdas target (line 151)
# km-runner: bake km binary directly into zip (no S3 cold-start download needed)
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/km-runner-km ./cmd/km/
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o build/bootstrap ./cmd/km-runner/
cd build && cp km-runner-km km && zip -j km-runner.zip bootstrap km && rm bootstrap km km-runner-km
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|---|---|---|---|
| EventBridge legacy rules for one-off | EventBridge Scheduler `at()` for one-off | ~2021 when Scheduler launched | km at uses Scheduler; km event must use Rules with pinned cron for one-offs |
| Per-entity IAM policies in create-handler | Shared `km-operator-policy/v1.0.0/` module | Phase 80 (2026-05) | km-runner reuses this module — no new policy bundle needed |
| All rules on default bus | Custom bus per use-case | EventBridge custom buses GA | km event uses custom bus for isolation + archive |

**Deprecated/outdated:**
- `km at --cron`: HARD CUT in this phase. Strip the flag and the `cronFlag` branch in `at.go`.
- Recurring expressions via `km at`: error with redirect to `km event`.

---

## Open Questions

1. **`operator-event-bus` module and `km init --lambdas` flag**
   - What we know: `km init` has a `--lambdas` flag (line 549) that applies a narrow set of Lambdas. CONTEXT says fold bus into `km init`.
   - What's unclear: Does `km init --lambdas` also need to build/deploy km-runner.zip, or only full `km init`?
   - Recommendation: `km init` (full) deploys the bus module including km-runner zip upload. `km init --lambdas` also deploys km-runner (consistent with it deploying slack-bridge). Add `km-runner` to the `lambdaBinaries` slice in `init.go` (around line 983).

2. **`km-operator-policy` EventBridgePutEvents scope addition**
   - What we know: Current policy at line 546 scopes to `event-bus/default` only.
   - What's unclear: Does km-runner need to put events back to the custom bus (e.g., for audit events)?
   - Recommendation: Yes, add `event-bus/{prefix}-operator-events` to the resource list. This is additive; no `moved {}` needed.

3. **One-off rule cleanup after firing**
   - What we know: EventBridge Rules do not auto-delete after firing (unlike Scheduler's `ActionAfterCompletion=DELETE`).
   - What's unclear: Should km event automatically set a state=DISABLED after one-off rule fires, or leave it ENABLED permanently?
   - Recommendation: Leave ENABLED for v1 (simpler Terraform state). Document that one-off rules remain in state after firing; operator can `km event rm <name>` to clean up. Revisit with a Lambda trigger in v2.

---

## Validation Architecture

### Test Framework
| Property | Value |
|---|---|
| Framework | Go `testing` package (standard) + `cmd_test` package pattern |
| Config file | none (no separate config file; tests run via `go test ./...`) |
| Quick run command | `go test ./internal/app/cmd/ -run TestEvent -v` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command | File Exists? |
|----------|-----------|-------------------|-------------|
| `km event add` idempotency (name already in config → no-op) | unit | `go test ./internal/app/cmd/ -run TestEventAdd_Idempotent -v` | Wave 0 |
| `km event add --dry-run=true` calls runner.Plan, no state mutation | unit | `go test ./internal/app/cmd/ -run TestEventAdd_DryRun -v` | Wave 0 |
| `km event add --dry-run=false` calls Apply/Output/Persist | unit | `go test ./internal/app/cmd/ -run TestEventAdd_Apply -v` | Wave 0 |
| `km event add` apply success but persist fails → returns error with `km event rm` hint | unit | `go test ./internal/app/cmd/ -run TestEventAdd_PersistFailure -v` | Wave 0 |
| `km event rm` dry-run calls Plan not Destroy | unit | `go test ./internal/app/cmd/ -run TestEventRm_DryRun -v` | Wave 0 |
| `km event rm` not-found → clear error | unit | `go test ./internal/app/cmd/ -run TestEventRm_NotFound -v` | Wave 0 |
| GenerateEventHCL produces correct substitutions | unit | `go test ./internal/app/cmd/ -run TestGenerateEventHCL -v` | Wave 0 |
| PersistEventsConfig reads-modifies-writes YAML preserving other keys | unit | `go test ./internal/app/cmd/ -run TestPersistEvents -v` | Wave 0 |
| `pkg/events` manifest validation: exactly one of schedule/event_pattern | unit | `go test ./pkg/events/ -run TestValidateManifest -v` | Wave 0 |
| `pkg/events` manifest validation: target.type matches companion field | unit | `go test ./pkg/events/ -run TestValidateManifest -v` | Wave 0 |
| `atExprToCronForRules` converts at() correctly to pinned cron | unit | `go test ./internal/app/cmd/ -run TestAtExprToCron -v` | Wave 0 |
| `km at` with recurring NL returns error pointing at `km event` | unit | `go test ./internal/app/cmd/ -run TestAtRecurringRejected -v` | Wave 0 |
| `km at --cron` flag removed → cobra returns "unknown flag" | unit | `go test ./internal/app/cmd/ -run TestAtCronFlagRemoved -v` | Wave 0 |
| `operator_event_rules_healthy` check: empty events → OK | unit | `go test ./internal/app/cmd/ -run TestOperatorEventRulesHealthy_Empty -v` | Wave 0 |
| `operator_event_rules_healthy` check: rule not ENABLED → WARN | unit | `go test ./internal/app/cmd/ -run TestOperatorEventRulesHealthy_Stale -v` | Wave 0 |
| config.EventConfig round-trips through km-config.yaml YAML | unit | `go test ./internal/app/config/ -run TestEventConfig -v` | Wave 0 |
| km-runner Lambda handles valid event.detail.command | unit | `go test ./cmd/km-runner/ -run TestKMRunner_ValidCommand -v` | Wave 0 |
| km-runner Lambda returns error on missing command field | unit | `go test ./cmd/km-runner/ -run TestKMRunner_MissingCommand -v` | Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/cmd/ -run TestEvent -v` and `go test ./pkg/events/ -v`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `internal/app/cmd/event_test.go` — covers all RunEventAdd/RunEventRm/GenerateEventHCL/PersistEvents/atExprToCron tests + mockEventRunner
- [ ] `pkg/events/validate_test.go` — covers manifest validation rules
- [ ] `cmd/km-runner/main_test.go` — covers KMRunner Handle tests
- [ ] `internal/app/config/event_config_test.go` — covers EventConfig YAML round-trip

---

## Sources

### Primary (HIGH confidence)
- `internal/app/cmd/cluster.go` — Phase 80 precedent, full mechanistic detail (all 609 lines reviewed)
- `pkg/at/parser.go` — NL parser: `ScheduleSpec{Expression, IsRecurring, HumanExpr}`, confirmed `at(...)` for one-off, `cron(...)`/`rate(...)` for recurring
- `pkg/aws/scheduler.go` — EventBridge Scheduler SDK wrapper (km at path); km event must NOT use this
- `internal/app/cmd/at.go` lines 73, 317 — `cronFlag string` at line 73, `--cron` registered at line 317; cron path at lines 124-191
- `infra/modules/create-handler/v1.0.0/main.tf` — Lambda pattern: `provided.al2023`, `arm64`, `zip`, `module "km_operator_policy"` call, CloudWatch log group, EventBridge rule/target on default bus
- `infra/modules/km-operator-policy/v1.0.0/main.tf` — 14-policy bundle; `EventBridgePutEvents` at line 546 scoped to default bus only
- `internal/app/cmd/init.go` `regionalModules()` lines 83-183 — where new `operator-event-bus` module slots in
- `internal/app/config/config.go` `ClusterConfig` (lines 20-26), `Clusters` field (line 192), `Load()` (lines 238-239, 364) — exact pattern to mirror for `EventConfig` and `Events`
- [AWS EventBridge Rules schedule expressions](https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-scheduled-rule-pattern.html) — confirmed: Rules support `cron(...)` and `rate(...)` only; **`at()` is Scheduler-only**
- [Terraform aws_cloudwatch_event_archive](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/cloudwatch_event_archive) — `event_source_arn` requires bus ARN; `retention_days` optional
- [Terraform aws_cloudwatch_event_bus](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/cloudwatch_event_bus) — custom bus resource confirmed

### Secondary (MEDIUM confidence)
- [AWS docs: Archive and replay events](https://docs.aws.amazon.com/eventbridge/latest/userguide/eb-archive.html) — archive is bus-level, replay targets same bus
- [EventBridge Scheduler schedule types](https://docs.aws.amazon.com/scheduler/latest/UserGuide/schedule-types.html) — confirms `at()` is Scheduler-only (one-time schedule type)

### Tertiary (LOW confidence)
- None

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries already in project; Terraform resources verified via registry
- Architecture: HIGH — cluster.go is the exact precedent, read line by line
- at()-to-cron conversion: HIGH — verified via official AWS docs that Rules don't support at()
- Pitfalls: HIGH — drawn from actual code (ExportConfigEnvVars comment, double-slash comment, policy scope)
- Validation: HIGH — mirrors existing cluster_test.go pattern

**Research date:** 2026-05-16
**Valid until:** 2026-07-16 (stable APIs; EventBridge Rules API is mature)
