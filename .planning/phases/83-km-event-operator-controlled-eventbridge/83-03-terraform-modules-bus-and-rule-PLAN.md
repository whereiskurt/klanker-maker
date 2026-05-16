---
phase: 83-km-event-operator-controlled-eventbridge
plan: 03
type: execute
wave: 1
depends_on: [01]
files_modified:
  - infra/modules/operator-event-bus/v1.0.0/main.tf
  - infra/modules/operator-event-bus/v1.0.0/variables.tf
  - infra/modules/operator-event-bus/v1.0.0/outputs.tf
  - infra/modules/operator-event-rule/v1.0.0/main.tf
  - infra/modules/operator-event-rule/v1.0.0/variables.tf
  - infra/modules/operator-event-rule/v1.0.0/outputs.tf
autonomous: true
requirements:
  - operator-feature-83
must_haves:
  truths:
    - "infra/modules/operator-event-bus/v1.0.0/ exists with main.tf provisioning aws_cloudwatch_event_bus + aws_cloudwatch_event_archive + the km-runner Lambda + its IAM role consuming km-operator-policy/v1.0.0/ (the bus + Lambda are co-located so a single module call provisions the singleton infra)"
    - "infra/modules/operator-event-rule/v1.0.0/ exists with main.tf provisioning aws_cloudwatch_event_rule + aws_cloudwatch_event_target + the events.amazonaws.com IAM role + aws_lambda_permission allowing the rule to invoke km-runner"
    - "Both modules pass `terraform init -backend=false && terraform validate` with exit 0"
    - "operator-event-bus consumes km-operator-policy/v1.0.0/ via relative `../km-operator-policy/v1.0.0` path (with // double-slash source pattern documented for terragrunt callers)"
    - "operator-event-rule supports BOTH schedule_expression AND event_pattern (XOR enforced by variable validation: exactly one of var.schedule, var.event_pattern is non-empty)"
    - "operator-event-rule derives target_role IAM action from the target ARN service prefix: arn:aws:lambda:* → lambda:InvokeFunction, arn:aws:sqs:* → sqs:SendMessage, arn:aws:sns:* → sns:Publish; any other service errors out at terraform plan via local.derived_action lookup"
    - "Lambda function name = ${var.resource_prefix}-km-runner, role = aws_iam_role.km_runner, runtime = provided.al2023, arm64, timeout=900, memory=1536, ephemeral_storage=10240"
    - "aws_lambda_function has lifecycle { replace_triggered_by = [aws_iam_role.km_runner] } (per project_lambda_role_replace_trigger memory)"
  artifacts:
    - path: "infra/modules/operator-event-bus/v1.0.0/main.tf"
      provides: "Singleton resources: aws_cloudwatch_event_bus, aws_cloudwatch_event_archive (7-day retention), aws_lambda_function (km-runner), aws_iam_role (km_runner) → km-operator-policy module call, CloudWatch log group /aws/lambda/{prefix}-km-runner"
      contains: "aws_cloudwatch_event_bus"
    - path: "infra/modules/operator-event-rule/v1.0.0/main.tf"
      provides: "Per-rule resources: aws_cloudwatch_event_rule (schedule or pattern), aws_cloudwatch_event_target, aws_iam_role + policy for events.amazonaws.com, aws_lambda_permission (when target is Lambda)"
      contains: "aws_cloudwatch_event_rule"
    - path: "infra/modules/operator-event-bus/v1.0.0/outputs.tf"
      provides: "bus_name, bus_arn, km_runner_lambda_arn, km_runner_role_arn"
      contains: "output \"bus_arn\""
    - path: "infra/modules/operator-event-rule/v1.0.0/outputs.tf"
      provides: "rule_arn, role_arn (the events.amazonaws.com target-role)"
      contains: "output \"rule_arn\""
  key_links:
    - from: "infra/modules/operator-event-bus/v1.0.0/main.tf"
      to: "infra/modules/km-operator-policy/v1.0.0/"
      via: "module \"km_operator_policy\" { source = \"../km-operator-policy/v1.0.0\" }"
      pattern: "km-operator-policy"
    - from: "infra/modules/operator-event-rule/v1.0.0/main.tf"
      to: "aws_lambda_function.km_runner (provisioned by operator-event-bus)"
      via: "var.target_arn passed in by the per-rule terragrunt.hcl; rule's aws_lambda_permission gives the rule's ARN permission to invoke that Lambda"
      pattern: "aws_lambda_permission"
---

<objective>
Create the two new Terraform modules that make EventBridge custom-bus rules + the singleton km-runner Lambda
declarable from Terragrunt. These are pure infrastructure modules — no Go code. Plan 83-04 fills in the Lambda
binary; this plan provisions the cloud resources that will host it.

Purpose: Establish the IaC contract that Plan 83-05's `km event add` generates a terragrunt.hcl against. By
splitting into two modules (bus = singleton, applied once per region by km init; rule = many, one per `km event
add`), we get the Phase 80 `km cluster add` pattern: shared module + dynamic per-rule stack.

Output:
- infra/modules/operator-event-bus/v1.0.0/ — three .tf files (main, variables, outputs)
- infra/modules/operator-event-rule/v1.0.0/ — three .tf files (main, variables, outputs)
- Both modules validate clean against `terraform validate`
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-CONTEXT.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-RESEARCH.md
@.planning/phases/83-km-event-operator-controlled-eventbridge/83-VALIDATION.md
@infra/modules/create-handler/v1.0.0/main.tf
@infra/modules/km-operator-policy/v1.0.0/main.tf
@infra/modules/cluster-irsa/v1.0.0/main.tf
@infra/modules/ttl-handler/v1.0.0/main.tf

<interfaces>
Pattern for the bus module — consumes km-operator-policy/v1.0.0/ exactly like create-handler does:

```hcl
# infra/modules/operator-event-bus/v1.0.0/main.tf (abbreviated)

resource "aws_cloudwatch_event_bus" "operator" {
  name = "${var.resource_prefix}-operator-events"
  tags = { "km:component" = "operator-event-bus" }
}

resource "aws_cloudwatch_event_archive" "operator" {
  name             = "${var.resource_prefix}-operator-events-archive"
  event_source_arn = aws_cloudwatch_event_bus.operator.arn
  retention_days   = 7
}

resource "aws_iam_role" "km_runner" {
  name = "${var.resource_prefix}-km-runner"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

module "km_operator_policy" {
  source = "../km-operator-policy/v1.0.0"
  role_id                   = aws_iam_role.km_runner.id
  resource_prefix           = var.resource_prefix
  artifact_bucket_arn       = var.artifact_bucket_arn
  state_bucket              = var.state_bucket
  dynamodb_table_name       = var.dynamodb_table_name
  dynamodb_budget_table_arn = var.dynamodb_budget_table_arn
  sandbox_table_name        = var.sandbox_table_name
  identities_table_name     = var.identities_table_name
}

resource "aws_cloudwatch_log_group" "km_runner" {
  name              = "/aws/lambda/${var.resource_prefix}-km-runner"
  retention_in_days = 7
}

# Inline CloudWatch logs policy (km-operator-policy intentionally excludes this — see Phase 80 memory)
resource "aws_iam_role_policy" "km_runner_logs" {
  role = aws_iam_role.km_runner.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "logs:CreateLogStream",
        "logs:PutLogEvents",
      ]
      Resource = "${aws_cloudwatch_log_group.km_runner.arn}:*"
    }]
  })
}

resource "aws_lambda_function" "km_runner" {
  function_name    = "${var.resource_prefix}-km-runner"
  role             = aws_iam_role.km_runner.arn
  filename         = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  timeout          = 900
  memory_size      = 1536
  ephemeral_storage { size = 10240 }
  environment {
    variables = {
      KM_RESOURCE_PREFIX   = var.resource_prefix
      KM_REGION_LABEL      = var.region_label
      KM_STATE_BUCKET      = var.state_bucket
      KM_ARTIFACTS_BUCKET  = var.artifact_bucket_name
      KM_OPERATOR_EMAIL    = var.operator_email
    }
  }
  lifecycle {
    replace_triggered_by = [aws_iam_role.km_runner]
  }
  depends_on = [aws_cloudwatch_log_group.km_runner]
}
```

Pattern for the rule module:

```hcl
# infra/modules/operator-event-rule/v1.0.0/main.tf (abbreviated)

variable "schedule" { type = string; default = "" }       # cron(...) or rate(...) — exactly one of {schedule, event_pattern}
variable "event_pattern" { type = string; default = "" }  # JSON string

locals {
  is_schedule = var.schedule != ""
  is_pattern  = var.event_pattern != ""

  # Derive target IAM action from service prefix in target_arn
  arn_parts = split(":", var.target_arn)
  target_service = local.arn_parts[2]   # arn:aws:lambda:..., arn:aws:sqs:..., arn:aws:sns:...
  service_actions = {
    "lambda" = "lambda:InvokeFunction"
    "sqs"    = "sqs:SendMessage"
    "sns"    = "sns:Publish"
  }
  derived_action = local.service_actions[local.target_service]   # plan-time error for unsupported service
}

# Validation: exactly one of schedule / event_pattern
resource "null_resource" "validation" {
  lifecycle {
    precondition {
      condition     = (local.is_schedule && !local.is_pattern) || (!local.is_schedule && local.is_pattern)
      error_message = "Exactly one of var.schedule and var.event_pattern must be non-empty."
    }
  }
}

resource "aws_cloudwatch_event_rule" "rule" {
  name                = "${var.resource_prefix}-event-${var.rule_name}"
  event_bus_name      = var.event_bus_name           # the custom bus NAME (not ARN — Pitfall 1)
  description         = var.description
  schedule_expression = local.is_schedule ? var.schedule : null
  event_pattern       = local.is_pattern ? var.event_pattern : null
  state               = "ENABLED"
}

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
      Action   = [local.derived_action]
      Resource = var.target_arn
    }]
  })
}

resource "aws_cloudwatch_event_target" "target" {
  rule           = aws_cloudwatch_event_rule.rule.name
  event_bus_name = var.event_bus_name
  target_id      = "${var.resource_prefix}-${var.rule_name}"
  arn            = var.target_arn
  role_arn       = aws_iam_role.target.arn
  input          = var.input_json
}

# Conditional: aws_lambda_permission only when target is Lambda
resource "aws_lambda_permission" "rule" {
  count         = local.target_service == "lambda" ? 1 : 0
  statement_id  = "AllowEventBridgeRule-${var.rule_name}"
  action        = "lambda:InvokeFunction"
  function_name = var.target_arn
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.rule.arn
}
```

Existing modules to mirror:
- create-handler/v1.0.0/main.tf — Lambda function block (same runtime/arch/timeout/memory), CloudWatch log group, km-operator-policy consumption
- cluster-irsa/v1.0.0/main.tf — consumes km-operator-policy module via relative path
- ttl-handler/v1.0.0/main.tf — EventBridge rule + target + aws_lambda_permission on default bus
</interfaces>
</context>

<tasks>

<task type="auto">
  <name>Task 1: Create infra/modules/operator-event-bus/v1.0.0/ (bus + archive + km-runner Lambda + IAM)</name>
  <files>infra/modules/operator-event-bus/v1.0.0/main.tf, infra/modules/operator-event-bus/v1.0.0/variables.tf, infra/modules/operator-event-bus/v1.0.0/outputs.tf</files>
  <action>
    1. Read infra/modules/create-handler/v1.0.0/main.tf for the Lambda block + CloudWatch log group + km-operator-policy consumption — copy patterns verbatim.
    2. Read infra/modules/cluster-irsa/v1.0.0/main.tf for the km-operator-policy module-call pattern (same source path style).
    3. Read infra/modules/create-handler/v1.0.0/variables.tf for the variable shape (types + defaults + descriptions).
    4. Create infra/modules/operator-event-bus/v1.0.0/variables.tf with these variables:
       - `resource_prefix` (string, required)
       - `region_label` (string, required)
       - `state_bucket` (string, required)
       - `artifact_bucket_arn` (string, required)
       - `artifact_bucket_name` (string, required)
       - `dynamodb_table_name` (string, required)
       - `dynamodb_budget_table_arn` (string, required)
       - `sandbox_table_name` (string, required)
       - `identities_table_name` (string, required)
       - `operator_email` (string, required)
       - `lambda_zip_path` (string, required — path to km-runner.zip; produced by Plan 83-04's make build-lambdas)
    5. Create infra/modules/operator-event-bus/v1.0.0/main.tf per the <interfaces> abbreviated example. Full content:
       - aws_cloudwatch_event_bus.operator (name="${prefix}-operator-events", tags)
       - aws_cloudwatch_event_archive.operator (event_source_arn = bus.arn, retention_days = 7)
       - aws_iam_role.km_runner (lambda.amazonaws.com trust)
       - module "km_operator_policy" {} consuming ../km-operator-policy/v1.0.0 (single-slash source — the // double-slash is only on the terragrunt.hcl source path, not in Terraform modules; see Phase 80 memory)
       - aws_cloudwatch_log_group.km_runner (retention 7 days)
       - aws_iam_role_policy.km_runner_logs (inline CloudWatch logs grant — km-operator-policy intentionally excludes logs)
       - aws_lambda_function.km_runner with the exact settings from <interfaces>, including lifecycle.replace_triggered_by
       - data.aws_caller_identity.current (if needed for tagging; otherwise omit)
    6. Create infra/modules/operator-event-bus/v1.0.0/outputs.tf with:
       - output "bus_name" { value = aws_cloudwatch_event_bus.operator.name }
       - output "bus_arn" { value = aws_cloudwatch_event_bus.operator.arn }
       - output "km_runner_lambda_arn" { value = aws_lambda_function.km_runner.arn }
       - output "km_runner_role_arn" { value = aws_iam_role.km_runner.arn }
    7. Run `cd infra/modules/operator-event-bus/v1.0.0 && terraform init -backend=false`. Should download providers via the project-level lock if present, else download fresh.
    8. Run `cd infra/modules/operator-event-bus/v1.0.0 && terraform validate`. Must exit 0.
    9. Note: Per project memory "Terragrunt provider declarations live in root.hcl", the module MUST NOT declare a `terraform { required_providers { ... } }` block. The root.hcl generates that for terragrunt-driven applies.

    AVOID:
    - Declaring `terraform { required_providers { ... } }` in main.tf — root.hcl is the single source (per project_terragrunt_providers_in_root memory)
    - Using a slash // in the module's km-operator-policy source path — that // pattern is only for terragrunt.hcl source paths. Inside Terraform modules, `source = "../km-operator-policy/v1.0.0"` (single slash) is correct.
    - Adding the aws.Provider declaration explicitly — root.hcl handles it
    - Creating a separate module just for the km-runner Lambda — co-locating bus + Lambda in operator-event-bus is intentional (single km init application, fewer state files)
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02/infra/modules/operator-event-bus/v1.0.0 && terraform init -backend=false > /dev/null 2>&1 && terraform validate 2>&1 && grep -c "aws_cloudwatch_event_bus\|aws_cloudwatch_event_archive\|aws_lambda_function" main.tf</automated>
  </verify>
  <done>
    Directory infra/modules/operator-event-bus/v1.0.0/ contains main.tf, variables.tf, outputs.tf. `terraform validate` exits 0. main.tf references aws_cloudwatch_event_bus, aws_cloudwatch_event_archive, aws_lambda_function, aws_iam_role, module "km_operator_policy". outputs.tf exposes bus_name, bus_arn, km_runner_lambda_arn, km_runner_role_arn.
  </done>
</task>

<task type="auto">
  <name>Task 2: Create infra/modules/operator-event-rule/v1.0.0/ (per-rule: rule + target + IAM + lambda permission)</name>
  <files>infra/modules/operator-event-rule/v1.0.0/main.tf, infra/modules/operator-event-rule/v1.0.0/variables.tf, infra/modules/operator-event-rule/v1.0.0/outputs.tf</files>
  <action>
    1. Read infra/modules/ttl-handler/v1.0.0/main.tf for the existing pattern of EventBridge rule + target + IAM role + aws_lambda_permission (note: ttl-handler is on the default bus; this module is on a custom bus).
    2. Create infra/modules/operator-event-rule/v1.0.0/variables.tf with these variables:
       - `resource_prefix` (string, required)
       - `rule_name` (string, required — the kebab-case slug)
       - `event_bus_name` (string, required — the NAME of the custom bus, not ARN, per Pitfall 1)
       - `schedule` (string, default "" — cron(...) or rate(...); empty if using event_pattern)
       - `event_pattern` (string, default "" — JSON string; empty if using schedule)
       - `target_arn` (string, required — the destination ARN: km-runner Lambda, or operator-supplied Lambda/SQS/SNS)
       - `input_json` (string, default null — static target input override; null means EventBridge passes the full event)
       - `description` (string, default "")
    3. Create infra/modules/operator-event-rule/v1.0.0/main.tf per the <interfaces> abbreviated example. Key resources:
       - locals: derive `arn_parts`, `target_service`, `service_actions` map, `derived_action`. The `local.derived_action = local.service_actions[local.target_service]` will fail at plan time for unsupported services (lookup on missing key) — that's the v1 contract from CONTEXT.md.
       - null_resource.validation with a precondition asserting XOR(schedule, event_pattern)
       - aws_cloudwatch_event_rule.rule (with conditional schedule_expression OR event_pattern via ternary on locals.is_schedule)
       - aws_iam_role.target (events.amazonaws.com trust)
       - aws_iam_role_policy.invoke (uses local.derived_action)
       - aws_cloudwatch_event_target.target (rule + bus + role + target_arn + input)
       - aws_lambda_permission.rule (count = 1 when local.target_service == "lambda" else 0)
    4. Create infra/modules/operator-event-rule/v1.0.0/outputs.tf with:
       - output "rule_arn" { value = aws_cloudwatch_event_rule.rule.arn }
       - output "role_arn" { value = aws_iam_role.target.arn }
       - output "rule_name" { value = aws_cloudwatch_event_rule.rule.name }
    5. Run `cd infra/modules/operator-event-rule/v1.0.0 && terraform init -backend=false`.
    6. Run `cd infra/modules/operator-event-rule/v1.0.0 && terraform validate`. Must exit 0.

    AVOID:
    - Declaring `terraform { required_providers { ... } }` (same reason as Task 1).
    - Setting `event_bus_name = var.event_bus_arn` — use NAME not ARN per Pitfall 1.
    - Hardcoding service_actions outside locals — it must be a local map so Plan 83-05 / future plans can extend it via a single PR.
    - Skipping the null_resource.validation block — XOR enforcement happens at plan time; helps operators catch malformed manifests before apply.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/.claude/worktrees/phase-74-02/infra/modules/operator-event-rule/v1.0.0 && terraform init -backend=false > /dev/null 2>&1 && terraform validate 2>&1 && grep -c "aws_cloudwatch_event_rule\|aws_cloudwatch_event_target\|aws_lambda_permission\|service_actions" main.tf</automated>
  </verify>
  <done>
    Directory infra/modules/operator-event-rule/v1.0.0/ contains main.tf, variables.tf, outputs.tf. `terraform validate` exits 0. main.tf references aws_cloudwatch_event_rule, aws_cloudwatch_event_target, aws_iam_role.target, aws_lambda_permission. The locals block contains a `service_actions` map with at least lambda/sqs/sns keys. outputs.tf exposes rule_arn, role_arn, rule_name.
  </done>
</task>

</tasks>

<verification>
- `terraform validate` exits 0 in both module directories
- Module files exist at the expected paths
- operator-event-bus/v1.0.0/main.tf consumes km-operator-policy via `../km-operator-policy/v1.0.0`
- operator-event-rule/v1.0.0/main.tf derives target IAM action from ARN service prefix (lambda/sqs/sns)
- Neither module declares a `terraform { required_providers }` block (root.hcl is single source)
</verification>

<success_criteria>
- Both new Terraform modules exist and validate clean
- The bus module co-locates bus + archive + km-runner Lambda + IAM in a single state
- The rule module supports both schedule and event_pattern (XOR), and derives target IAM from service prefix
- Plan 83-04 has a target zip path variable to fill (`lambda_zip_path`)
- Plan 83-05 has stable module ARN outputs to read via `terragrunt output -json`
</success_criteria>

<output>
After completion, create `.planning/phases/83-km-event-operator-controlled-eventbridge/83-03-SUMMARY.md` documenting:
- The exact list of variables in each module (Plan 83-05 must pass these in via the runtime-generated terragrunt.hcl inputs block)
- The exact list of outputs in each module (Plan 83-05 reads bus.name from bus module and rule_arn/role_arn from rule module)
- Whether terraform validate produced any deprecation warnings (not errors) the planner should note
- Decisions taken on the service_actions map: which services are supported in v1 (lambda/sqs/sns confirmed); document the plan-time error path for unsupported services
</output>
</content>
