# Phase 116: km check — Serverless Check Runner - Research

**Researched:** 2026-06-17
**Domain:** AWS Lambda SDK provisioning, Python packaging, EventBridge dispatch, alias-targeted sandbox resume-or-cold-create, config plumbing
**Confidence:** HIGH (all findings grounded in live codebase reads)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- **Lambda-per-snippet** execution model; each check is its own AWS Lambda.
- **SDK-managed provisioning** (`lambda:CreateFunction`/`UpdateFunctionCode`), tracked in `{prefix}-checks` DDB table. No terragrunt per check.
- **Shared baseline role** `{prefix}-check-runner` (not per-check roles).
- **TWO scaffolding modules only:** `infra/modules/dynamodb-checks/v1.0.0/` and `infra/modules/check-runner-role/v1.0.0/`.
- **Snippet contract:** author writes plain Python program; a km-authored bootstrap (`_km_check_bootstrap.py`) is the actual Lambda handler.
- **Bootstrap per invocation:** env build → subprocess exec (capture stdout) → write to S3 → if JSON, evaluate baked-in trigger → on match emit ONE `CheckDispatch` event.
- **`checks.triggers:` config block** mirrors `github.events:`; decision in CONFIG not snippet.
- **`when_py` condition language:** Python predicate block, `out` bound, `def _pred(out): <body>`, must `return bool or (bool, reason)`. Inline or `@file`.
- **`prompt` also accepts inline or `@file`.** `@file` resolved operator-side at deploy/sync, baked into `KM_CHECK_TRIGGER` env var.
- **Stage A (Python bootstrap):** eval `when_py`; on truthy emit `CheckDispatch` event to `km.sandbox` bus.
- **Stage B (Go, ttl-handler):** new `CheckDispatch` EventBridge rule → new case in `eventType` switch → `pkg/dispatch.ResumeOrCreate(alias, prompt, profile, onAbsent)`.
- **`pkg/dispatch` factored from GitHub + H1 bridge logic** — single source of truth shared by bridges and check path.
- **Dispatch host = `ttl-handler`** (no new fleet Lambda).
- **All three invocation modes:** scheduled (EventBridge Scheduler), manual/remote (`km check run`), event-driven pre-filter (`check:` on `github.events` rule).
- **Network egress: open (no VpcConfig)** — v1.
- **Zip + requirements.txt default** (`pip install --platform manylinux2014_aarch64 --only-binary=:all:`), arm64. `--image` container opt-in with lazy SDK-created ECR repo `{prefix}-checks`.
- **Runtime python3.13, arm64. Defaults: 256 MB / 30 s.**
- **CLI:** `km check deploy|run|ls|get|logs|schedule|sync|rm`.
- **Config plumbing ritual:** struct + tags; add `checks` to v2→v merge-list; `YAMLDefaults` snapshot; per-check `KM_CHECK_TRIGGER` env-baking at deploy/sync.
- **km doctor additions:** `{prefix}-checks` table, orphan Lambdas, stale schedules, trigger drift.
- **Example checks:** `profiles/checks/qotd.py` (QOTD) + `profiles/checks/wiz_threat_intel.py` (simulated Wiz) in `profiles/checks/`.
- **Deploy surface:** `make build` + `km init --dry-run=false` for scaffolding + CheckDispatch rule + widened ttl-handler IAM; `make build-lambdas` + `km init --dry-run=false` for ttl-handler code change; `km check deploy`/`km check sync` for per-check (pure SDK).

### Claude's Discretion
- Internal Go package layout, struct/field naming, exact pip/zip build mechanics, bootstrap's precise eval-wrapping, test file organization, precise CloudFormation/terragrunt resource wiring — follow existing repo patterns.

### Deferred Ideas (OUT OF SCOPE)
- Slack/email event-driven pre-filters (GitHub first).
- Non-Python runtimes.
- Per-check least-privilege IAM roles.
- A separate scheduled re-evaluator.
- A dedicated `check-dispatcher` Lambda.
- Per-check VPC-attach + egress filtering.
</user_constraints>

---

## Summary

Phase 116 adds a `km check` family that runs small Python snippets as per-check AWS Lambdas (SDK-managed, not terragrunt), captures stdout to S3, and conditionally dispatches an alias-targeted sandbox prompt. The platform already provides all four moving parts at file-level anchors: the SDK provisioning pattern (mirrors `km create`), the EventBridge/S3/SQS infrastructure, the alias resolver + EC2 resumer logic in `pkg/github/bridge/aws_adapters.go`, and the `ttl-handler` dispatch switch. The new work is: (1) Lambda CRUD via SDK (net-new, no existing `lambda.CreateFunction` wrapper), (2) Python bootstrap + zip packaging (net-new — all current Lambdas are Go), (3) factoring the 3-way resume-or-cold-create into `pkg/dispatch`, (4) a new `CheckDispatch` EventBridge rule in the ttl-handler TF module, and (5) config plumbing for `checks.triggers:`.

**Primary recommendation:** Build `pkg/dispatch` first from the GitHub bridge logic, add the ttl-handler `check-dispatch` + `check-run` cases, then add the per-check Lambda SDK wrapper in a new `pkg/check/` package mirroring `pkg/aws/`, then the CLI.

---

## 1. Per-check Lambda provisioning via SDK

**Finding:** There is NO existing `lambda.CreateFunction`/`UpdateFunctionCode` wrapper in `pkg/aws/` or anywhere else in the codebase. The only `lambdapkg.Client` usage is read-only: `lambdapkg.GetFunction` (to discover TTL Lambda ARN at resume time, `cmd/ttl-handler/main.go:467`). The Makefile `build-lambdas` target cross-compiles Go binaries; no Python packaging anywhere.

**AWS config pattern (HIGH confidence):**
- `pkg/aws/client.go:24` — `LoadAWSConfig(ctx, profile string) (aws.Config, error)` — thin wrapper hardcoded to `us-east-1`.
- `pkg/aws/client.go:40` — `LoadAWSConfigInRegion(ctx, profile, region string)` — named profile respected on operator workstation; in Lambda (`AWS_LAMBDA_FUNCTION_NAME` set) profile is ignored, default credential chain used.
- From that `aws.Config`, SDK clients are constructed: `lambdapkg.NewFromConfig(awsCfg)` (pattern in `cmd/ttl-handler/main.go:466`).
- `AWS_PROFILE=klanker-application` is the operator profile (from project memory).

**What must be NEW in `pkg/check/` (or `internal/app/cmd/check.go`):**
```go
// Pseudocode — net-new Lambda CRUD wrapper needed
lambdaClient := lambdapkg.NewFromConfig(awsCfg)

// CreateFunction or UpdateFunctionCode based on whether function exists
_, err := lambdaClient.CreateFunction(ctx, &lambdapkg.CreateFunctionInput{
    FunctionName: aws.String(prefix + "-check-" + name),
    Role:         aws.String(roleARN), // {prefix}-check-runner ARN
    Runtime:      "python3.13",
    Architectures: []lambdatypes.Architecture{lambdatypes.ArchitectureArm64},
    Handler:      aws.String("_km_check_bootstrap.handler"),
    Code: &lambdatypes.FunctionCode{
        ZipFile: zipBytes, // <50MB direct; S3 key for >50MB
    },
    Environment: &lambdatypes.Environment{
        Variables: map[string]string{
            "KM_CHECK_TRIGGER": triggerJSON,
            "KM_ARTIFACTS_BUCKET": bucket,
            // static --env K=V pairs
        },
    },
    MemorySize: aws.Int32(256),
    Timeout:    aws.Int32(30),
    Tags: map[string]string{"km:component": "check", "km:resource-prefix": prefix},
})
```

For existing functions use `UpdateFunctionCode` + `UpdateFunctionConfiguration` (two-call pattern; Lambda requires them separately).

**S3 path for >50MB zip upload:** `s3://{artifacts}/checks/{name}/package.zip` — upload first, then pass `Code: &FunctionCode{S3Bucket, S3Key}`.

**ECR repo for `--image`:** `ecr.CreateRepository("{prefix}-checks")` + SetRepositoryPolicy granting `lambda.amazonaws.com` pull. Lazy on first `--image` deploy (SDK call, not terragrunt).

---

## 2. Resume-or-cold-create logic to factor into `pkg/dispatch`

**Finding:** The exact logic lives in `pkg/github/bridge/aws_adapters.go` and `pkg/github/bridge/webhook_handler.go`. The H1 bridge (`pkg/h1/bridge/`) has a parallel copy (Phase 109 ported to both; Phase 116 will create one more consumer via `pkg/dispatch`).

### Key types and file:line anchors

**`pkg/github/bridge/interfaces.go`:**
- `SandboxAliasResolverWithStatus` (line 69) — `ResolveByAliasWithStatus(ctx, alias) (sandboxID, status string, err error)`
- `SandboxResumer` (line 38) — `StartSandbox(ctx, sandboxID string) error`
- `SandboxStatusWriter` (line 51) — `SetStatusRunning` + `DeleteSandboxRow`
- `EventBridgePublisher` (line 111) — `PutSandboxCreate(ctx, alias, profile, githubEnvelopeJSON string) error`
- `SQSSender` (line 119) — `Send(ctx, queueURL, body, groupID, deduplicationID string) error`

**`pkg/github/bridge/aws_adapters.go`:**
- `ErrNoResumableInstance` sentinel (line 92) — terminal: instance gone, orphaned alias row.
- `DynamoAliasResolver.ResolveByAliasWithStatus` (line 273) — queries alias-index GSI, reads `status` attr (NOTE: attr is `status` NOT `state` — the Slack `state`/`status` bug memory).
- `DynamoAliasResolver.GitHubQueueURL` (line 315) — fetches `github_inbound_queue_url` from km-sandboxes row.
- `EC2Resumer.StartSandbox` (line 521) — DescribeInstances filter `tag:km:sandbox-id` + state `stopped/stopping`; wraps `ErrNoResumableInstance` on `len(found)==0`; transient errors are NOT wrapped.
- `EC2Resumer.sandboxIDTagKey()` (line 496) — always returns `"km:sandbox-id"` regardless of resource_prefix (the Phase-109 sandbox-id-tag-always-km-namespace fix).
- `EventBridgeAdapter.PutSandboxCreate` (line 445) — emits `source:"km.sandbox"`, `detail-type:"SandboxCreate"`.

**`pkg/github/bridge/webhook_handler.go` (the 3-way dispatch block, lines 529–596):**
```
status == "stopped" || "paused" && Resumer != nil:
    err = Resumer.StartSandbox(ctx, sandboxID)
    if errors.Is(err, ErrNoResumableInstance):
        StatusWriter.DeleteSandboxRow(ctx, sandboxID)   // clear orphaned row
        Publisher.PutSandboxCreate(ctx, alias, profile, envJSON)  // cold-create
    else (success or transient):
        StatusWriter.SetStatusRunning(ctx, sandboxID)
        SQS.Send(ctx, queueURL, envJSON, groupID, dedupID)
resolveErr != nil (alias absent):
    Publisher.PutSandboxCreate(ctx, alias, profile, envJSON)
running (or stopped without Resumer):
    SQS.Send(ctx, queueURL, envJSON, groupID, dedupID)
```

**`pkg/dispatch` factoring design:**
The `pkg/dispatch` package should re-declare the same interfaces (`SandboxAliasResolverWithStatus`, `SandboxResumer`, `SandboxStatusWriter`, `EventBridgePublisher`, `SQSSender`) or import them from a shared location. The bridges' concrete adapters (`DynamoAliasResolver`, `EC2Resumer`, `EventBridgeAdapter`, `DynamoGitHubNonceStore`) satisfy these interfaces — the bridges can wire the same concrete types into `pkg/dispatch.ResumeOrCreate`. This avoids duplication of the SDK adapter layer.

**`pkg/dispatch.ResumeOrCreate` signature (proposed):**
```go
func ResumeOrCreate(
    ctx context.Context,
    alias, prompt, profile, onAbsent string,
    resolver SandboxAliasResolverWithStatus,
    resumer SandboxResumer,
    statusWriter SandboxStatusWriter,
    publisher EventBridgePublisher,
    sqsSender SQSSender,
    nonces NonceStore, // for cooldown check — same DynamoGitHubNonceStore
    cooldownKey string,
    cooldownSeconds int,
    log *slog.Logger,
) error
```

The cooldown nonce key for checks: `"check-trigger:{name}"` — same `CheckAndStore` interface used by `DynamoGitHubNonceStore`.

For the check path, the SQS queue to enqueue to is the **per-alias sandbox's `slack_inbound_queue_url` or `github_inbound_queue_url`** (whichever is provisioned). The design doc says "SQS enqueue to the sandbox's inbound FIFO". The check dispatch should target the per-alias inbound queue. Since the check is a new trigger source, a new `check_inbound_queue_url` DDB attribute on km-sandboxes would be cleanest — OR the `pkg/dispatch` implementation can reuse the `slack_inbound_queue_url` (Slack inbound is broader). **Clarification needed for planner:** the spec says "SQS enqueue prompt to the sandbox's inbound FIFO" but doesn't specify WHICH inbound FIFO. Given the design reuses bridge self-heal logic, the most conservative approach is to add a generic `inbound_queue_url` field or use the existing slack inbound. The planner must decide and it should be consistent with what the on-box check poller reads.

---

## 3. ttl-handler operation dispatch

**`cmd/ttl-handler/main.go:214-276` — `HandleTTLEvent` switch:**
```go
switch event.EventType {
case "stop":         return h.handleStop(ctx, event)
case "resume":       return h.handleResume(ctx, event)
case "extend":       return h.handleExtend(ctx, event)
case "budget-add":   return h.handleBudgetAdd(ctx, event)
case "agent-run":    return h.handleAgentRun(ctx, event)
case "schedule-create": return h.handleScheduleCreate(ctx, event)
default:  // "ttl", "idle", "destroy", "" → handleDestroy
```

**New cases to add:**
```go
case "check-dispatch":
    return h.handleCheckDispatch(ctx, event)
case "check-run":
    return h.handleCheckRun(ctx, event)
```

**`TTLEvent` struct (lines 52-80) needs new fields for check operations:**
```go
// check-dispatch fields
CheckName  string `json:"check_name,omitempty"`
Alias      string `json:"alias,omitempty"`   // already present
Prompt     string `json:"prompt,omitempty"`  // already present
Profile    string `json:"profile_name,omitempty"` // already present
OnAbsent   string `json:"on_absent,omitempty"`
Reason     string `json:"reason,omitempty"`

// check-run fields (manual lambda:Invoke relay)
CheckName  string // already covered above
```

**`Alias` and `ProfileName` already exist on `TTLEvent` (lines 78-79).** Only `OnAbsent`, `Reason`, and `CheckName` are net-new fields.

**How EventBridge rule → ttl-handler is wired (from `infra/modules/ttl-handler/v1.0.0/main.tf`):**
- `aws_cloudwatch_event_rule.sandbox_idle` (line 397): matches `source=["km.sandbox"], detail-type=["SandboxIdle"]`.
- `aws_cloudwatch_event_rule.sandbox_command` (line 446): matches `detail-type=["SandboxIdle"]` with `event_type != idle`.
- New `CheckDispatch` rule must match `source=["km.sandbox"], detail-type=["CheckDispatch"]`.
- Target: `aws_cloudwatch_event_target` → `ttl-handler` Lambda ARN.
- `input_path = "$.detail"` (pass full detail so the TTLEvent struct fields unmarshal correctly).
- Lambda permission: `aws_lambda_permission` with `principal = "events.amazonaws.com"`.

**This new rule goes INTO the existing `infra/modules/ttl-handler/v1.0.0/main.tf`** — the ttl-handler module owns all rules targeting the ttl-handler Lambda.

**`handleScheduleCreate` pattern (lines 742-819)** is the template for `handleCheckRun` (scheduled one-shot via `km at '...' check run`). `handleCheckDispatch` uses `pkg/dispatch.ResumeOrCreate` directly.

---

## 4. EventBridge SandboxCreate path and new CheckDispatch event

**`pkg/aws/eventbridge.go:20-68`:**
```go
type SandboxCreateDetail struct {
    SandboxID      string `json:"sandbox_id"`
    ArtifactBucket string `json:"artifact_bucket"`
    ArtifactPrefix string `json:"artifact_prefix"`
    OperatorEmail  string `json:"operator_email,omitempty"`
    OnDemand       bool   `json:"on_demand"`
    CreatedBy      string `json:"created_by,omitempty"`
    Alias          string `json:"alias,omitempty"`
    ScheduleTime   string `json:"schedule_time,omitempty"`
    GithubEnvelope string `json:"github_envelope,omitempty"`
}
```

`PutSandboxCreateEvent` (line 45): emits `source:"km.sandbox"`, `detail-type:"SandboxCreate"`.

The `pkg/github/bridge/aws_adapters.go:EventBridgeAdapter.PutSandboxCreate` (line 445) is the bridge's concrete implementation — it sets `ArtifactPrefix:"github-profiles/{slug}"` and generates a `gh-` prefixed sandbox ID. For check cold-creates, the `pkg/dispatch.ResumeOrCreate` path should emit similarly but with `created_by:"check"` and a `check-` prefix sandbox ID.

**New `CheckDispatch` event (emitted by Python bootstrap, consumed by ttl-handler):**
```json
{
  "source": "km.sandbox",
  "detail-type": "CheckDispatch",
  "detail": {
    "event_type": "check-dispatch",
    "check": "<check-name>",
    "alias": "<alias>",
    "prompt": "<expanded-prompt>",
    "profile_name": "<profile-or-empty>",
    "on_absent": "cold-create|skip",
    "reason": "<predicate-reason-string>"
  }
}
```

The Python bootstrap emits this via `boto3.client('events').put_events(...)`. The ttl-handler EventBridge rule matches `detail-type=["CheckDispatch"]` and routes to ttl-handler; `input_path="$.detail"` passes the inner dict which maps to `TTLEvent` fields.

---

## 5. S3 artifact bucket helpers

**`pkg/aws/artifacts.go`:** `UploadArtifacts`, `S3PutAPI`, `ArtifactSkippedEvent` — Go-side helpers for sandbox artifact upload. NOT used by check Lambda bootstrap (which uses Python boto3 directly).

**Bucket env var in Lambda context:** `KM_ARTIFACTS_BUCKET` (seen in `cmd/ttl-handler/main.go:724` inside `buildAgentRunScript`; also `init.go:556,563` as an `envReqs` key). The check Lambda bootstrap must receive `KM_ARTIFACTS_BUCKET` as an env var baked at deploy time.

**S3 output key for checks:** `check-runs/<name>/<ts>/output.json` — analogous to `agent-runs/<sandbox>/<runid>/output.json`. The bucket name is `{prefix}-artifacts-{account_id}` (from `ArtifactsBucket` in config; the `{prefix}-artifacts-{account}` naming convention).

**Python boto3 write pattern (bootstrap):**
```python
import boto3, json, os, time
s3 = boto3.client('s3')
bucket = os.environ['KM_ARTIFACTS_BUCKET']
ts = time.strftime('%Y%m%dT%H%M%SZ')
key = f"check-runs/{check_name}/{ts}/output.json"
s3.put_object(Bucket=bucket, Key=key, Body=stdout_bytes)
```

**Existing Python Lambda precedent:** The only Python Lambda in the repo is `infra/modules/ecs-spot-handler/v1.0.0/lambda/handler.py` (ECS Fargate spot interruption handler). It is deployed inline via Terraform `archive_file` + `aws_lambda_function`, NOT via `buildLambdaZips`. The check Lambda bootstrap is the FIRST Python Lambda managed by the km CLI itself (not Terraform) — net-new tooling.

---

## 6. github.events router for `check:` pre-filter

**`pkg/github/bridge/event_router.go`:**
- `EventRule` struct (line 14): fields `On, Actions, Match, Exclude, Profile, Alias, Agent, CooldownSeconds, Prompt`.
- `MatchEventRule(eventType, payload, rules)` (line 87): exact-before-glob, first-match.
- `ExpandEventTemplate(tmpl, payload, eventType)` (line 128): simple `strings.Replacer` for `{{repo}}, {{event}}, {{action}}, {{sender}}, {{default_branch}}, {{html_url}}`.

**Adding `check:` pre-filter to an `EventRule`:**
1. Add `Check string` field to `EventRule` (bridge side) and `GithubEventRule` (config side) with `mapstructure:"check"` tag.
2. In `handleEventRoute` (webhook_handler.go:724), after matching the rule but before dispatching, if `rule.Check != ""`: synchronously `lambda.Invoke` the `{prefix}-check-{name}` Lambda; only proceed to dispatch if the bootstrap returns a triggered result.
3. The bridge Lambda needs `lambda:InvokeFunction` IAM on `{prefix}-check-*` — a new IAM statement in `infra/modules/lambda-github-bridge/v1.1.0/`.
4. Template vars for check-prompt expansion: extend `ExpandEventTemplate` with `{{reason}}` and `{{out.<field>}}` dotted-access (the check path uses the same substitution but with richer variables). The `ExpandEventTemplate` function is pure Go string replacement — extend it or provide a sibling `ExpandCheckTemplate` function.

**`internal/app/config/config.go:152-186` — `GithubEventRule` struct** is the canonical config pattern. The new `CheckTrigger` struct for `checks.triggers:` should mirror the `mapstructure` + `yaml` + `json` tag discipline.

---

## 7. Config plumbing

### v2→v merge-list (the CRITICAL site)

**`internal/app/config/config.go:707-768`** — the merge-list `for _, key := range []string{...}` block. Adding `checks` as a key is required for `checks.triggers:` to not be silently dropped (the `project_config_key_merge_list` footgun).

The `github` block (line 760) is the exact precedent — the entire block decoded atomically via `v.UnmarshalKey("github", &cfg.Github)`. The new entry:
```go
"checks",  // Phase 116: checks.triggers list-of-objects — decoded atomically via UnmarshalKey
```

Then in the `cfg :=` block (line 794+), add:
```go
// Phase 116: decode checks config
if err := v.UnmarshalKey("checks", &cfg.Checks); err != nil {
    // non-fatal; absent checks: block → zero value → dormant
}
```

### `Config` struct additions

**`internal/app/config/config.go:364` — `Config struct`** needs:
```go
Checks ChecksConfig `mapstructure:"checks" yaml:"checks,omitempty"`
```

New types:
```go
type ChecksConfig struct {
    Triggers []CheckTrigger `mapstructure:"triggers" yaml:"triggers,omitempty"`
}

type CheckTrigger struct {
    Check           string `mapstructure:"check" yaml:"check"`
    WhenPy          string `mapstructure:"when_py" yaml:"when_py,omitempty"`
    Alias           string `mapstructure:"alias" yaml:"alias"`
    Prompt          string `mapstructure:"prompt" yaml:"prompt,omitempty"`
    OnAbsent        string `mapstructure:"on_absent" yaml:"onAbsent,omitempty"`
    CooldownSeconds int    `mapstructure:"cooldown_seconds" yaml:"cooldownSeconds,omitempty"`
}
```

### `YAMLDefaults` snapshot (line 782-791)

No scalar top-level key to snapshot for `checks` (it's a list-of-objects). Skip — or snapshot a `checks.triggers` count for drift detection.

### Per-check env-baking

**Pattern:** `internal/app/config/config.go:1688-1711` — the `KM_GITHUB_EVENTS` baking block.

For checks, env-baking is NOT at `km init` time (not a global Lambda env var). It happens at `km check deploy` / `km check sync` time: resolve `@file` refs → marshal the specific check's trigger to JSON → call `lambda.UpdateFunctionConfiguration` to set `KM_CHECK_TRIGGER` on THAT check Lambda only.

```go
type CheckTriggerBaked struct {
    WhenPy  string `json:"when_py"`  // resolved (inline or @file content)
    Alias   string `json:"alias"`
    Prompt  string `json:"prompt"`   // resolved (inline or @file content)
    OnAbsent string `json:"on_absent"`
    CooldownSeconds int `json:"cooldown_seconds"`
}
```

### `ExportTerragruntEnvVars` (init.go:1688)

No KM_ global env var for checks (no bridge Lambda consumed them). The only init.go change is adding `dynamodb-checks` and `check-runner-role` to `regionalModules()`.

### `regionalModules()` (init.go:511-678)

Add two new entries (before `ses` which must remain last):
```go
{
    name: "dynamodb-checks",
    dir:  filepath.Join(regionDir, "dynamodb-checks"),
    envReqs: nil,
},
{
    name: "check-runner-role",
    dir:  filepath.Join(regionDir, "check-runner-role"),
    envReqs: []string{"KM_ARTIFACTS_BUCKET"},
},
```

The ttl-handler module (`regionalModules()[5]` at line 566) also gets a new variable input (`checks_table_name`) once the `CheckDispatch` rule is added to its module.

### `lambdaBuilds()` (init.go:2906-2922)

**NO new entry** for check Lambdas (they are SDK-managed, not fleet Lambdas). The `ttl-handler` entry already exists: `{name: "ttl-handler", srcDir: "cmd/ttl-handler"}`. When `handleCheckDispatch` is added to ttl-handler, it rebuilds automatically via the existing entry.

### Makefile `build-lambdas`

**NO change** needed for check Lambda packaging. The check Lambdas are Python, packaged by `km check deploy` (Go operator-side logic), not by `make build-lambdas`. The Makefile is Go-only.

---

## 8. Terraform module conventions

### Template: `infra/modules/dynamodb-checks/v1.0.0/`

Based on `infra/modules/dynamodb-github-threads/v1.0.0/main.tf`:
```hcl
resource "aws_dynamodb_table" "checks" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "name"   # hash_key only (not composite PK — name is unique)

  attribute {
    name = "name"
    type = "S"
  }

  server_side_encryption {
    enabled = true
  }

  tags = merge(var.tags, {
    Name      = var.table_name
    Component = "km-check"
  })
}
```

Variables: `table_name string` (default `"km-checks"`), `tags map(string)`.

### Template: `infra/modules/check-runner-role/v1.0.0/`

IAM role `{prefix}-check-runner` (Lambda trust policy) with inline policies:
- CloudWatch Logs: `logs:CreateLogGroup`, `logs:CreateLogStream`, `logs:PutLogEvents`.
- S3 read: `s3:GetObject` on `s3://{artifacts}/checks/*`.
- S3 write: `s3:PutObject` on `s3://{artifacts}/check-runs/*`.
- EventBridge: `events:PutEvents` on the `km.sandbox` event bus ARN.
- SSM: `ssm:GetParameter` on `arn:aws:ssm:*:*:parameter/{prefix}/checks/*`.
- DynamoDB: `dynamodb:GetItem`, `dynamodb:Query` on `{prefix}-checks` table ARN.

**Critical rule:** Module MUST NOT declare `required_providers` (from `project_terragrunt_providers_in_root` memory — root.hcl's `generate "provider"` stanza is the single source).

### Live unit template: `infra/live/use1/dynamodb-checks/terragrunt.hcl`

Based on `infra/live/use1/dynamodb-github-threads/terragrunt.hcl` (exact pattern read above):
```hcl
locals {
  repo_root     = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars     = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
}

include "root" {
  path = find_in_parent_folders("root.hcl")
}

remote_state { ... key = ".../dynamodb-checks/terraform.tfstate" }

terraform {
  source = "${local.repo_root}/infra/modules/dynamodb-checks/v1.0.0"
}

inputs = {
  table_name = "${local.site_vars.locals.site.label}-checks"
  tags = { "km:component" = "km-check", "km:managed" = "true" }
}
```

**mock_outputs note:** modules with `dependency` blocks need `mock_outputs_allowed_terraform_commands = ["show"]` (from `project_terragrunt_show_needs_mocks` memory). The `dynamodb-checks` and `check-runner-role` modules have no inter-module dependencies (no `dependency` blocks), so no mocks needed.

---

## 9. Cobra command structure for `km check`

**Pattern: `internal/app/cmd/github.go` — `NewGithubCmd`:**
```go
// NewCheckCmd creates the "km check" parent command
func NewCheckCmd(cfg *config.Config) *cobra.Command {
    parent := &cobra.Command{
        Use:          "check",
        Short:        "Manage serverless Python check Lambdas",
        SilenceUsage: true,
    }
    parent.AddCommand(newCheckDeployCmd(cfg))
    parent.AddCommand(newCheckRunCmd(cfg))
    parent.AddCommand(newCheckLsCmd(cfg))
    parent.AddCommand(newCheckGetCmd(cfg))
    parent.AddCommand(newCheckLogsCmd(cfg))
    parent.AddCommand(newCheckScheduleCmd(cfg))
    parent.AddCommand(newCheckSyncCmd(cfg))
    parent.AddCommand(newCheckRmCmd(cfg))
    return parent
}
```

**`root.go` registration** (after `NewDesktopCmd`, before `NewClusterCmd` — alphabetical near "c"):
```go
root.AddCommand(NewCheckCmd(cfg))
```

**`km at` extension for `check-run`:** Add `"check-run"` to `schedulableCommands` map in `at.go:40-50`:
```go
"check-run": {targetARNField: "ttl", eventType: "check-run"},
```

---

## 10. Python packaging — net-new tooling

**Current state:** ALL Lambdas in `lambdaBuilds()` are Go. The only Python Lambda (`ecs-spot-handler`) is managed by Terraform's `archive_file`, NOT by the km CLI. There is zero Python packaging infrastructure in `make build-lambdas` or `buildLambdaZips`.

**What `km check deploy` must implement (Go code calling pip/zip):**

**Zip + requirements.txt path:**
```bash
# arch-correct wheel install (arm64 lambda, manylinux2014)
pip install \
  --platform manylinux2014_aarch64 \
  --only-binary=:all: \
  --target /tmp/km-check-deploy/{name}/pkg \
  -r requirements.txt

# copy snippet + bootstrap into the package dir
cp {file.py} /tmp/km-check-deploy/{name}/pkg/snippet.py
cp {_km_check_bootstrap.py} /tmp/km-check-deploy/{name}/pkg/_km_check_bootstrap.py

# zip everything
cd /tmp/km-check-deploy/{name}/pkg && zip -r /tmp/km-check-deploy/{name}/package.zip .
```

If `requirements.txt` is absent, skip pip and just zip snippet + bootstrap.

**Go implementation:** Use `os/exec` to call `pip` and `zip` (or the stdlib `archive/zip` for the zip step). Detect `pip3` vs `pip`. This requires Python + pip on the operator's PATH — document as a prerequisite.

**S3 upload for >50MB:** After zip, check size; if >50MB, `s3.PutObject` to `checks/{name}/package.zip` then use `Code: &lambdatypes.FunctionCode{S3Bucket, S3Key}`.

**`--image` container path:**
```bash
docker build \
  --platform linux/arm64 \
  -t {ecr-repo-uri}/{prefix}-check-{name}:latest \
  -f Dockerfile .
docker push {ecr-repo-uri}/{prefix}-check-{name}:latest
```

Requires Docker on operator host. ECR repo `{prefix}-checks` created lazily via `ecr.CreateRepository` before first push.

**`_km_check_bootstrap.py`** is a static file shipped as part of the km CLI binary (embed via `go:embed` or write to a temp file from a string constant). It is the actual Lambda handler and must be identical across all deployed checks.

**Build artifacts land in** a temp dir (not `build/` which is Go-only). Clean up after deploy.

---

## Architecture Patterns

### Recommended project structure additions
```
cmd/
├── ...                         # existing
internal/app/cmd/
├── check.go                    # km check command + subcommands
├── check_test.go               # unit tests for config/deploy plumbing
pkg/
├── check/
│   ├── bootstrap.go            # _km_check_bootstrap.py embedded string + deploy helpers
│   ├── lambda.go               # Lambda CRUD wrappers (CreateFunction, UpdateFunctionCode, Invoke)
│   ├── ecr.go                  # ECR repo lazy-create helper
│   └── package.go              # zip + pip build helpers
├── dispatch/
│   ├── dispatch.go             # ResumeOrCreate factored from bridge logic
│   └── dispatch_test.go        # unit tests (mock interfaces)
infra/modules/
├── dynamodb-checks/v1.0.0/     # {prefix}-checks table
│   ├── main.tf
│   ├── variables.tf
│   └── outputs.tf
└── check-runner-role/v1.0.0/   # {prefix}-check-runner IAM role
    ├── main.tf
    ├── variables.tf
    └── outputs.tf
infra/live/use1/
├── dynamodb-checks/terragrunt.hcl
└── check-runner-role/terragrunt.hcl
profiles/checks/
├── qotd.py                     # QOTD example check
└── wiz_threat_intel.py         # simulated Wiz example
```

### Pattern: `pkg/dispatch.ResumeOrCreate`
**What:** Factored 3-way alias dispatch (absent→cold-create, stopped/paused→resume+enqueue, running→enqueue).
**When to use:** Both bridge `handleEventRoute` and ttl-handler `handleCheckDispatch` call this.
**Concrete adapter reuse:** `pkg/github/bridge/aws_adapters.go` concrete types (`DynamoAliasResolver`, `EC2Resumer`, `EventBridgeAdapter`, `GitHubSQSAdapter`) already satisfy the interfaces — the bridge can wire them into `pkg/dispatch` without code changes, preserving byte-identical behavior.

### Pattern: per-check DDB row
```
name            S (hash key)
arn             S
runtime         S ("python3.13")
packageType     S ("zip"|"image")
imageUri        S (if image)
memory          N
timeout         N
schedule        S (EventBridge Scheduler expression or empty)
env             M (map of non-secret K=V)
secretPaths     SS (set of SSM param paths)
sourceHash      S (SHA256 of resolved when_py + prompt)
triggerSummary  S (human-readable: "alias=nightly-auditor cooldown=3600s")
createdAt       S (ISO8601)
updatedAt       S (ISO8601)
```

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Alias→sandbox resolution | Custom DDB scan | `pkg/github/bridge/aws_adapters.go:DynamoAliasResolver` | Handles ambiguous-alias trap, GSI query |
| EC2 stopped instance detection | Custom DescribeInstances | `EC2Resumer.StartSandbox` (aws_adapters.go:521) | Handles stopping→stopped race, `ErrNoResumableInstance` sentinel |
| Orphaned alias self-heal | Custom delete logic | `SandboxStatusWriter.DeleteSandboxRow` + `pkg/dispatch` | Phase 109 pattern — deletes stale row then cold-creates |
| EventBridge SandboxCreate | Custom event format | `pkg/aws/eventbridge.go:PutSandboxCreateEvent` + `SandboxCreateDetail` | Create-handler expects exact field shapes |
| Cooldown / dedup | Custom timer | `DynamoGitHubNonceStore.CheckAndStore` | Shared nonces table already deployed |
| Python wheel cross-compilation | `pip` without `--platform` | `pip install --platform manylinux2014_aarch64 --only-binary=:all:` | Wheels built for operator OS won't run on Lambda arm64 |
| Lambda >50MB deploy | Direct zip upload | S3 pre-upload → `Code: {S3Bucket, S3Key}` | Lambda API rejects direct uploads >50MB |

---

## Common Pitfalls

### Pitfall 1: SandboxID tag namespace
**What goes wrong:** Using `{prefix}:sandbox-id` tag key (e.g. `sec:sandbox-id`) instead of `km:sandbox-id`.
**Why it happens:** Resource prefix is in a SEPARATE `km:resource-prefix` tag; `km:sandbox-id` is always `km:` regardless of prefix.
**How to avoid:** `EC2Resumer.sandboxIDTagKey()` always returns `"km:sandbox-id"` — reuse this logic in `pkg/dispatch`.
**See:** `project_sandbox_id_tag_always_km_namespace` memory.

### Pitfall 2: DDB attr name `status` not `state`
**What goes wrong:** Reading `state` attribute from km-sandboxes row; `info.Paused` always false.
**Root cause:** km writes `status` attr but old code read `state`.
**How to avoid:** `DynamoAliasResolver.ResolveByAliasWithStatus` reads `status` (aws_adapters.go:307) — use the existing resolver.
**See:** `project_slack_bridge_inbound_e2e_and_status_attr` memory.

### Pitfall 3: ErrNoResumableInstance must NOT be wrapped for transient errors
**What goes wrong:** A transient `DescribeInstances` API error wrapped as `ErrNoResumableInstance` → false orphan delete + cold-create.
**How to avoid:** Only the terminal `len(found)==0` path wraps the sentinel; API errors return plain `fmt.Errorf`. `pkg/dispatch` must mirror this.

### Pitfall 4: Missing merge-list entry for `checks`
**What goes wrong:** `checks.triggers:` in `km-config.yaml` silently ignored.
**How to avoid:** Add `"checks"` to the merge-list in `config.go:707` AND call `v.UnmarshalKey("checks", &cfg.Checks)`.
**See:** `project_config_key_merge_list` memory.

### Pitfall 5: stale km binary before `km init`
**What goes wrong:** km binary without the new `regionalModules()` entries silently skips `dynamodb-checks` and `check-runner-role` → `AccessDenied` at runtime.
**How to avoid:** `make build` BEFORE `km init --dry-run=false`.
**See:** `project_make_build_precedes_km_init` memory.

### Pitfall 6: Python wheels built for wrong arch
**What goes wrong:** `pip install` without `--platform` installs wheels for the operator's host OS (macOS/x86) → Lambda runtime error on arm64 linux.
**How to avoid:** Always `pip install --platform manylinux2014_aarch64 --only-binary=:all: --target <dir> -r requirements.txt`.

### Pitfall 7: SandboxMetadata lossy round-trip on `{prefix}-checks` rows
**What goes wrong:** PutItem on a partial struct strips unrecognised attributes.
**How to avoid:** Use UpdateItem (never PutItem) for updates to existing check rows, mirroring the sandboxes table pattern.
**See:** `project_sandboxmetadata_lossy_roundtrip` memory.

### Pitfall 8: Lambda `required_providers` in new modules
**What goes wrong:** Modules that declare `required_providers` conflict with root.hcl's generated provider stanza.
**How to avoid:** New `dynamodb-checks` and `check-runner-role` modules MUST NOT declare `required_providers`.
**See:** `project_terragrunt_providers_in_root` memory.

### Pitfall 9: `when_py` and `prompt` @file resolved at deploy, not runtime
**What goes wrong:** Editing a `@file` predicate or prompt file and expecting it to take effect on the next check run.
**How to avoid:** `km check sync` re-resolves and re-bakes `KM_CHECK_TRIGGER`. Document in CLI help text.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` (standard library) |
| Config file | none (module-level test flags) |
| Quick run command | `go test ./pkg/dispatch/... ./internal/app/cmd/ -count=1 -timeout 60s` |
| Full suite command | `go test ./... -count=1 -timeout 600s` |

**Note:** Always check exit code, not pipe (`feedback_check_go_test_exit_not_pipe` memory): `go test ./... > /tmp/test.out 2>&1; echo $?`.

### Phase Requirements → Test Map

No pre-existing CHCK-* requirement IDs in REQUIREMENTS.md (confirmed by full read). No pre-existing requirement IDs apply — net-new feature.

| Behavior | Test Type | Automated Command | File Exists? |
|----------|-----------|-------------------|-------------|
| `pkg/dispatch.ResumeOrCreate` — absent alias → cold-create | unit (mock interfaces) | `go test ./pkg/dispatch/ -run TestResumeOrCreate_AbsentAlias` | No — Wave 0 |
| `pkg/dispatch.ResumeOrCreate` — stopped alias → resume + enqueue | unit | `go test ./pkg/dispatch/ -run TestResumeOrCreate_StoppedAlias` | No — Wave 0 |
| `pkg/dispatch.ResumeOrCreate` — orphaned stopped row → delete + cold-create | unit | `go test ./pkg/dispatch/ -run TestResumeOrCreate_OrphanedRow` | No — Wave 0 |
| `pkg/dispatch.ResumeOrCreate` — running alias → enqueue only | unit | `go test ./pkg/dispatch/ -run TestResumeOrCreate_RunningAlias` | No — Wave 0 |
| Config merge: `checks` key in merge-list populates `ChecksConfig` | unit | `go test ./internal/app/config/ -run TestChecksConfigMerge` | No — Wave 0 |
| `KM_CHECK_TRIGGER` JSON bake: inline `when_py` → JSON | unit | `go test ./internal/app/cmd/ -run TestCheckTriggerBake` | No — Wave 0 |
| `KM_CHECK_TRIGGER` JSON bake: `@file` resolved at deploy | unit | `go test ./internal/app/cmd/ -run TestCheckTriggerBakeAtFile` | No — Wave 0 |
| `regionalModules()` count (update after adding 2 new modules) | unit | `go test ./internal/app/cmd/ -run TestRunInitPlan_ModuleOrder` | Yes (must bump count from 22 to 24) |
| Python bootstrap `when_py` wrapping: truthy → `CheckDispatch` | Python unit (manual) or live UAT | manual | No |
| Python bootstrap non-JSON stdout → no trigger | Python unit (manual) | manual | No |
| E2E: deploy QOTD check, `km check run qotd`, S3 output present | live AWS | manual | No |
| E2E: Wiz check `when_py` fires → `CheckDispatch` → sandbox resume | live AWS | manual | No |
| `github.events` `check:` pre-filter: check triggers → sandbox dispatched | live AWS | manual | No |
| `github.events` `check:` pre-filter: check does not trigger → no sandbox dispatch | live AWS | manual | No |

### Sampling Rate
- **Per task commit:** `go test ./pkg/dispatch/... ./internal/app/cmd/ -count=1 -timeout 120s`
- **Per wave merge:** `go test ./... -count=1 -timeout 600s`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/dispatch/dispatch_test.go` — 4 unit tests covering 3-way dispatch (mock interfaces)
- [ ] `pkg/dispatch/dispatch.go` — the factored `ResumeOrCreate` function
- [ ] `internal/app/config/config_check_test.go` — config merge test
- [ ] `internal/app/cmd/check_test.go` — trigger-bake unit tests
- [ ] Update `TestRunInitPlan_ModuleOrder` hardcoded count: 22 → 24 (2 new modules)

---

## Code Examples

### Existing nonce cooldown pattern (reuse for check-trigger cooldown)
```go
// Source: pkg/github/bridge/aws_adapters.go:200
func (s *DynamoGitHubNonceStore) CheckAndStore(ctx context.Context, key string, ttlSeconds int) (bool, error) {
    ttlExpiry := time.Now().Unix() + int64(ttlSeconds)
    _, err := s.Client.PutItem(ctx, &dynamodb.PutItemInput{
        TableName: awssdk.String(s.TableName),
        Item: map[string]dynamodbtypes.AttributeValue{
            "nonce":      &dynamodbtypes.AttributeValueMemberS{Value: key},
            "ttl_expiry": &dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(ttlExpiry, 10)},
        },
        ConditionExpression: awssdk.String("attribute_not_exists(nonce)"),
    })
    // ConditionalCheckFailedException → already seen (alreadySeen=true, err=nil)
}
```

Check cooldown key: `"check-trigger:{check_name}"`.

### EventBridge rule pattern in TF module
```hcl
# Source: infra/modules/ttl-handler/v1.0.0/main.tf:394-479
resource "aws_cloudwatch_event_rule" "check_dispatch" {
  name = "${var.resource_prefix}-check-dispatch"
  event_pattern = jsonencode({
    source      = ["km.sandbox"]
    detail-type = ["CheckDispatch"]
  })
}

resource "aws_cloudwatch_event_target" "check_dispatch_to_ttl" {
  rule      = aws_cloudwatch_event_rule.check_dispatch.name
  target_id = "${var.resource_prefix}-ttl-handler-check-dispatch"
  arn       = aws_lambda_function.ttl_handler.arn
  input_path = "$.detail"   # full detail passthrough → TTLEvent fields
}

resource "aws_lambda_permission" "eventbridge_check_dispatch" {
  statement_id  = "AllowEventBridgeCheckDispatchInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.ttl_handler.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.check_dispatch.arn
}
```

### Python bootstrap skeleton (to implement as embedded string in Go)
```python
# _km_check_bootstrap.py — Lambda handler (embedded in km binary, shipped in every check zip)
import os, sys, json, subprocess, time
import boto3

def handler(event, context):
    name    = os.environ.get('KM_CHECK_NAME', 'unknown')
    bucket  = os.environ['KM_ARTIFACTS_BUCKET']
    trigger = json.loads(os.environ.get('KM_CHECK_TRIGGER', '{}'))

    # Build env for subprocess: static env → SSM secrets → per-run overrides
    env = dict(os.environ)
    for ssm_path in json.loads(os.environ.get('KM_CHECK_SECRET_PATHS', '[]')):
        val = boto3.client('ssm').get_parameter(Name=ssm_path, WithDecryption=True)['Parameter']['Value']
        key = ssm_path.split('/')[-1].upper()
        env[key] = val
    if event.get('env'):
        env.update(event['env'])  # per-run overrides from km check run --env K=V

    # Exec snippet capturing stdout
    result = subprocess.run(
        [sys.executable, 'snippet.py'],
        env=env, capture_output=True, text=True, timeout=context.get_remaining_time_in_millis()/1000 - 5
    )
    stdout = result.stdout

    # Write to S3
    ts = time.strftime('%Y%m%dT%H%M%SZ', time.gmtime())
    key = f"check-runs/{name}/{ts}/output.json"
    boto3.client('s3').put_object(Bucket=bucket, Key=key, Body=stdout.encode())

    # Evaluate trigger
    if not trigger.get('when_py'):
        return {'triggered': False, 'reason': 'no trigger configured'}

    try:
        out = json.loads(stdout)
    except json.JSONDecodeError:
        print(f"check_output_not_json: {name}")
        return {'triggered': False, 'reason': 'non-JSON output'}

    # Wrap and exec when_py predicate
    pred_src = "def _pred(out):\n" + "\n".join("    " + l for l in trigger['when_py'].splitlines())
    globs = {}
    exec(pred_src, globs)
    result = globs['_pred'](out)
    triggered, reason = (result, '') if isinstance(result, bool) else result

    if triggered:
        # Expand {{reason}} and {{out.<field>}} in prompt template
        prompt = trigger.get('prompt', '')
        prompt = prompt.replace('{{reason}}', reason)
        for k, v in out.items():
            prompt = prompt.replace(f'{{{{out.{k}}}}}', str(v))

        # Emit CheckDispatch
        boto3.client('events').put_events(Entries=[{
            'Source': 'km.sandbox',
            'DetailType': 'CheckDispatch',
            'Detail': json.dumps({
                'event_type': 'check-dispatch',
                'check': name,
                'alias': trigger['alias'],
                'prompt': prompt,
                'profile_name': trigger.get('profile', ''),
                'on_absent': trigger.get('on_absent', 'cold-create'),
                'reason': reason,
            })
        }])
        return {'triggered': True, 'reason': reason, 'output_key': key}

    return {'triggered': False, 'reason': reason, 'output_key': key}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| All Lambdas are Go, managed by km init / terragrunt | Per-check Python Lambdas managed by km CLI SDK | Phase 116 (new) | Net-new tooling: pip, zip, ECR, lambda.CreateFunction all new |
| Bridge resume-or-cold-create duplicated in github + h1 | Factored into `pkg/dispatch` | Phase 116 (new) | Bridges refactored to call shared helper |
| `github.events` autonomous routing | + `check:` pre-filter field | Phase 116 (new) | Bridge synchronously invokes check Lambda before dispatching |

---

## Open Questions

1. **Which inbound FIFO queue for check dispatch enqueue (warm path)?**
   - What we know: design says "SQS enqueue prompt to the sandbox's inbound FIFO"; km-sandboxes has both `slack_inbound_queue_url` and `github_inbound_queue_url`.
   - What's unclear: which queue URL does `pkg/dispatch` use for the warm enqueue? Using `slack_inbound_queue_url` is the broadest (almost all sandboxes have Slack inbound); using `github_inbound_queue_url` requires github inbound provisioned; a new `check_inbound_queue_url` is cleanest but requires schema change.
   - Recommendation: Introduce a new `inbound_queue_url` DDB attribute that is populated by WHICHEVER inbound sources are provisioned (a generic alias for the "primary inbound" queue). OR: document that check dispatch requires Slack inbound provisioned (cleaner for v1 since Slack inbound is the dominant use case). **Planner must decide.**

2. **`km check run` result format — synchronous invoke response**
   - What we know: `lambda:InvokeFunction` returns the bootstrap's return value.
   - What's unclear: how to surface S3 output URL, trigger/dispatch result, and reason to the operator CLI.
   - Recommendation: The bootstrap's return dict (`{'triggered': bool, 'reason': str, 'output_key': str}`) maps cleanly to CLI output. `km check run` should print output + trigger status + S3 URI.

3. **`sourceHash` computation for trigger drift detection**
   - What we know: `sourceHash` is the SHA256 of resolved `when_py` + `prompt` (post `@file` expansion).
   - What's unclear: whether to include `alias`, `onAbsent`, `cooldownSeconds` in the hash or only predicate + prompt.
   - Recommendation: Hash the full `KM_CHECK_TRIGGER` JSON value (includes all fields). Drift = current hash != deployed Lambda env hash.

---

## Sources

### Primary (HIGH confidence)
- Live codebase reads: `pkg/github/bridge/aws_adapters.go`, `pkg/github/bridge/webhook_handler.go`, `pkg/github/bridge/interfaces.go`, `pkg/github/bridge/event_router.go`, `cmd/ttl-handler/main.go`, `pkg/aws/eventbridge.go`, `pkg/aws/artifacts.go`, `pkg/aws/client.go`, `internal/app/config/config.go`, `internal/app/cmd/init.go`, `internal/app/cmd/github.go`, `internal/app/cmd/at.go`, `internal/app/cmd/root.go`, `infra/modules/dynamodb-github-threads/v1.0.0/main.tf`, `infra/modules/ttl-handler/v1.0.0/main.tf`, `infra/live/use1/dynamodb-github-threads/terragrunt.hcl`, `infra/live/use1/ttl-handler/terragrunt.hcl`, `Makefile`, `infra/modules/ecs-spot-handler/v1.0.0/lambda/handler.py`.
- Design spec: `docs/superpowers/specs/2026-06-17-km-check-serverless-runner-design.md`.
- CONTEXT.md: `.planning/phases/116-.../116-CONTEXT.md`.

### Secondary (MEDIUM confidence)
- Project memory items (cross-verified against codebase): `project_sandbox_id_tag_always_km_namespace`, `project_config_key_merge_list`, `project_sandboxmetadata_lossy_roundtrip`, `project_make_build_precedes_km_init`, `project_module_order_test_count_debt`, `project_terragrunt_show_needs_mocks`, `project_terragrunt_providers_in_root`, `feedback_check_go_test_exit_not_pipe`.

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries/patterns confirmed in live code.
- Architecture: HIGH — file:line anchors provided for all key touch points.
- Pitfalls: HIGH — all sourced from project memory items + live code verification.
- Python packaging: MEDIUM — `pip --platform` flag documented behavior; no existing precedent in this repo.

**Research date:** 2026-06-17
**Valid until:** 2026-07-17 (stable domain; AWS SDK + Python Lambda packaging stable)

---

## Requirement ID Assignment

No pre-existing CHCK-* or other requirement IDs in REQUIREMENTS.md apply to this phase. No pre-existing requirement IDs apply — net-new feature.
