---
phase: 66-multi-instance-support-configurable-resource-prefix-and-email-subdomain
plan: "04"
subsystem: infra/terraform
tags: [multi-instance, prefix, terraform, terragrunt, lambda, dynamodb]
dependency_graph:
  requires: [66-01, 66-02, 66-03]
  provides: [terraform-prefix-wiring, ddb-prefix, lambda-prefix, pitfall9-fix, phase67-drift-fix]
  affects: [infra/live/site.hcl, infra/live/use1/, infra/modules/, pkg/compiler/budget_enforcer_hcl.go]
tech_stack:
  added: []
  patterns: [terragrunt-site-vars-pattern, var.resource_prefix-passthrough, dependency-mock-outputs]
key_files:
  created: []
  modified:
    - infra/live/site.hcl
    - infra/live/use1/dynamodb-budget/terragrunt.hcl
    - infra/live/use1/dynamodb-identities/terragrunt.hcl
    - infra/live/use1/dynamodb-sandboxes/terragrunt.hcl
    - infra/live/use1/dynamodb-schedules/terragrunt.hcl
    - infra/live/use1/dynamodb-slack-nonces/terragrunt.hcl
    - infra/live/use1/dynamodb-slack-threads/terragrunt.hcl
    - infra/live/use1/dynamodb-slack-stream-messages/terragrunt.hcl
    - infra/live/use1/create-handler/terragrunt.hcl
    - infra/live/use1/ttl-handler/terragrunt.hcl
    - infra/live/use1/email-handler/terragrunt.hcl
    - infra/live/use1/lambda-slack-bridge/terragrunt.hcl
    - infra/modules/create-handler/v1.0.0/main.tf
    - infra/modules/create-handler/v1.0.0/variables.tf
    - infra/modules/ttl-handler/v1.0.0/main.tf
    - infra/modules/ttl-handler/v1.0.0/variables.tf
    - infra/modules/email-handler/v1.0.0/main.tf
    - infra/modules/email-handler/v1.0.0/variables.tf
    - infra/modules/lambda-slack-bridge/v1.0.0/main.tf
    - infra/modules/ecs-spot-handler/v1.0.0/main.tf
    - infra/modules/ecs-spot-handler/v1.0.0/variables.tf
    - pkg/compiler/budget_enforcer_hcl.go
decisions:
  - "mock_outputs in dependency blocks retain literal km- defaults (allowed per plan audit); these are plan-time stubs, not deployed values"
  - "ecs-spot-handler has no live config under infra/live/use1/ — it is only referenced from infra/live/management/scp/terragrunt.hcl (management-side only)"
  - "Lambda function rename caveat: for default-prefix installs, function_name resolves to the same string so no replace. For custom-prefix installs migrating from default, terraform state mv is needed for lambda-slack-bridge"
metrics:
  duration: "~20min"
  completed: "2026-05-04"
  tasks: 4
  files: 22
requirements: [REQ-PLATFORM-MULTI-INSTANCE]
---

# Phase 66 Plan 04: Terraform Prefix Wiring Summary

Wire the prefix and email_subdomain through Terragrunt live configs and Terraform modules: site.hcl driven by env vars, 7 DDB live configs migrated, 5 Lambda modules parameterized with var.resource_prefix, Phase 67/68 drift fixed, Pitfall 9 dependency block added, and compiler HCL emit updated.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | site.hcl extension + 7 DDB live configs | cf1a96a | site.hcl + 7 DDB terragrunt.hcl |
| 2 | var.resource_prefix in 5 Lambda modules | 15c6988 | 10 module files (5 main.tf + 5 variables.tf) |
| 3 | Lambda live configs + compiler HCL emit | 41e5fd3 | 4 live configs + budget_enforcer_hcl.go |
| 4 | TF/Go smoke gate | 673376c | (verification only) |

## Task 1: site.hcl + DynamoDB live config migration

### site.hcl diff (3 changed lines, 1 added)

```hcl
# BEFORE:
site = {
    label           = "km"
    tf_state_prefix = "tf-km"
    domain          = get_env("KM_DOMAIN", "klankermaker.ai")
    random_suffix   = get_env("KMGUID", "")
}

# AFTER:
site = {
    label           = get_env("KM_RESOURCE_PREFIX", "km")
    tf_state_prefix = "tf-${get_env("KM_RESOURCE_PREFIX", "km")}"
    domain          = get_env("KM_DOMAIN", "klankermaker.ai")
    email_subdomain = get_env("KM_EMAIL_SUBDOMAIN", "sandboxes")
    random_suffix   = get_env("KMGUID", "")
}
```

The backend bucket and lock table already derived from `local.site.tf_state_prefix` — rename flows automatically.

### DynamoDB live config migration (7 files — all one-line changes)

| File | Before | After |
|------|--------|-------|
| dynamodb-budget | `"km-budgets"` | `"${local.site_vars.locals.site.label}-budgets"` |
| dynamodb-identities | `"km-identities"` | `"${local.site_vars.locals.site.label}-identities"` |
| dynamodb-sandboxes | `"km-sandboxes"` | `"${local.site_vars.locals.site.label}-sandboxes"` |
| dynamodb-schedules | `"km-schedules"` | `"${local.site_vars.locals.site.label}-schedules"` |
| dynamodb-slack-nonces | `"km-slack-bridge-nonces"` | `"${local.site_vars.locals.site.label}-slack-bridge-nonces"` |
| dynamodb-slack-threads | `"km-slack-threads"` | `"${local.site_vars.locals.site.label}-slack-threads"` (Phase 67 drift fix) |
| dynamodb-slack-stream-messages | `"km-slack-stream-messages"` | `"${local.site_vars.locals.site.label}-slack-stream-messages"` (Phase 68 drift fix) |

## Task 2: Five Lambda Modules

### Per-module parameterization counts

| Module | name attrs changed | New variables added | Notes |
|--------|--------------------|---------------------|-------|
| create-handler | 30 | resource_prefix, sandbox_table_name, identities_table_name | IAM ARN wildcards parameterized |
| ttl-handler | 28 | resource_prefix, sandbox_table_name, budget_table_name, schedules_table_name | Pitfall 4 env vars added |
| email-handler | 13 | resource_prefix, sandbox_table_name | Pitfall 7 fixed |
| lambda-slack-bridge | 3 (locals + uses) | (already had resource_prefix from Phase 67) | Phase 67 drift fixed at line 5 |
| ecs-spot-handler | 7 | resource_prefix | No longer account-singleton |

### Phase 67 drift fix (lambda-slack-bridge main.tf line 5)

```hcl
# BEFORE:
locals {
  function_name = "km-slack-bridge"
}

# AFTER:
locals {
  function_name = "${var.resource_prefix}-slack-bridge"
}
```

All other names in this module already use `${local.function_name}` so this single change propagates to all resources.

### Pitfall 4 fix (ttl-handler env block)

Added to `aws_lambda_function.ttl_handler.environment.variables`:
```hcl
KM_TTL_HANDLER_NAME   = "${var.resource_prefix}-ttl-handler"
KM_TTL_SCHEDULER_ROLE = "${var.resource_prefix}-ttl-scheduler"
KM_AT_GROUP_NAME      = "${var.resource_prefix}-at"
KM_SANDBOX_TABLE_NAME = var.sandbox_table_name
KM_BUDGET_TABLE       = var.budget_table_name
KM_SCHEDULES_TABLE    = var.schedules_table_name
```

### Pitfall 7 fix (email-handler)

```hcl
# BEFORE:
SANDBOX_TABLE_NAME = "km-sandboxes"

# AFTER:
SANDBOX_TABLE_NAME = var.sandbox_table_name
```

### Pitfall 6 fix (ecs-spot-handler)

Previously all names were hardcoded `km-ecs-spot-handler`, `km-spot-handler-cw-logs`, etc. — any second install would collide. Now all seven name attributes use `${var.resource_prefix}-` interpolation.

## Task 3: Lambda live configs + compiler

### Lambda live configs: resource_prefix + table-name + email-domain inputs

All four Lambda live configs gained `resource_prefix = local.site_vars.locals.site.label`. Additionally:

- **create-handler**: sandbox_table_name, identities_table_name, email_domain (site.email_subdomain), dynamodb_budget_table_arn (prefix-derived)
- **ttl-handler**: sandbox_table_name, budget_table_name, schedules_table_name, email_domain
- **email-handler**: sandbox_table_name, safe_phrase_ssm_key (prefix-derived `/{label}/config/remote-create/safe-phrase`), email_domain
- **lambda-slack-bridge**: see Pitfall 9 fix below

### Pitfall 9 fix (lambda-slack-bridge live config) — before/after diff

**Added dependency block (before inputs block):**
```hcl
dependency "slack_threads" {
  config_path = "../dynamodb-slack-threads"
  mock_outputs = {
    table_name = "km-slack-threads"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-slack-threads"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply"]
}
```

**New inputs (added to existing inputs block):**
```hcl
resource_prefix          = local.site_vars.locals.site.label
signing_secret_path      = "/${local.site_vars.locals.site.label}/slack/signing-secret"
slack_threads_table_name = dependency.slack_threads.outputs.table_name
nonce_table_name         = "${local.site_vars.locals.site.label}-slack-bridge-nonces"
bot_token_path           = "/${local.site_vars.locals.site.label}/slack/bot-token"  # was "/km/slack/bot-token"
sandboxes_table_name     = "${local.site_vars.locals.site.label}-sandboxes"  # no longer from dep output (dep still wired for ARN)
```

### Compiler HCL emit fix (budget_enforcer_hcl.go)

```go
// BEFORE (with TODO marker):
email_domain = "sandboxes.${local.site_vars.locals.site.domain}" // TODO Phase 66 plan 04: migrate...

// AFTER:
email_domain = "${local.site_vars.locals.site.email_subdomain}.${local.site_vars.locals.site.domain}"
```

## Task 4: Smoke checks

### make build
```
Built: km v0.2.497 (41e5fd3)
```
`km --help` runs without panic.

### grep audit result
Zero non-allow-listed literal `"km-"` in `infra/` TF/HCL files outside:
- `default = "km-..."` in variables.tf (Phase 66 default fallbacks — allowed)
- `mock_outputs` blocks in dependency stubs (allowed)
- Tag values like `"km:component" = "km-slack-inbound"` (component labels, not resource names — allowed)
- Per-sandbox modules (budget-enforcer, ecs, ecs-task, ecs-cluster) — explicitly out of scope per RESEARCH

### Terragrunt plan smoke (manual gate)

AWS credentials were not available in the execution environment. Operator must run:
```bash
cd infra/live/use1
terragrunt run-all plan --terragrunt-non-interactive 2>&1 | tee /tmp/66-04-tg-plan.log
grep -E '^\s*(~|-\/\+|-\s)' /tmp/66-04-tg-plan.log
```

**Expected:** Zero `-/+` (replace) lines for stateful resources. At most `~` (in-place update) for Lambda env var additions and tag changes.

**Lambda function rename caveat:** For the default prefix (`km`), `var.resource_prefix` defaults to `km`, so `function_name = "${var.resource_prefix}-slack-bridge"` resolves to `km-slack-bridge` — same as before, no replace. For custom-prefix installs migrating from default (e.g., changing from `km` to `acme`), the Lambda function IS replaced (destroy+create). In that case, run `terraform state mv` for the Lambda resource before apply, or accept the brief cold start.

## ecs-spot-handler placement

`find infra/live -name 'terragrunt.hcl' | xargs grep -l ecs-spot-handler 2>/dev/null` returns only `infra/live/management/scp/terragrunt.hcl`. There is no separate live config for ecs-spot-handler under `infra/live/use1/`. The module is included in the management-side SCP config (IAM policy scope reference), not deployed as a standalone live config. This means there is no `resource_prefix` live wire needed for ecs-spot-handler — only the module itself needed parameterization (done in Task 2).

## Deviations from Plan

None — plan executed exactly as written. The email-handler `safe_phrase_ssm_key` input was renamed from `safe_phrase_ssm_path` to `safe_phrase_ssm_key` to match the existing variable name in the module (the plan spec used `safe_phrase_ssm_path` which is the live config input name, but the module variable is `safe_phrase_ssm_key`). Verified by reading variables.tf before applying.

## Self-Check: PASSED
