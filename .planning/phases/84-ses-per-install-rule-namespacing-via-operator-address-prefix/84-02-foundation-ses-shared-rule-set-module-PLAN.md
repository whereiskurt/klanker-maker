---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: 02
type: execute
wave: 1
depends_on: [01]
files_modified:
  - infra/modules/ses-shared-rule-set/v1.0.0/main.tf
  - infra/modules/ses-shared-rule-set/v1.0.0/variables.tf
  - infra/modules/ses-shared-rule-set/v1.0.0/outputs.tf
  - infra/live/use1/ses-shared-rule-set/terragrunt.hcl
autonomous: true
requirements:
  - SES-SHARED-RULESET

must_haves:
  truths:
    - "A new foundation Terraform module exists at `infra/modules/ses-shared-rule-set/v1.0.0/`"
    - "The module owns the singleton SES rule set, its always-active pointer, the shared `aws_ses_domain_identity` for the email domain, the 3 DKIM CNAMEs, and the route53 MX + SES verification records"
    - "The module accepts `register_shared_rule_set` (bool) and `register_domain_identity` (bool) flags following the Phase 80 cluster-irsa `register_X` precedent — when `false`, the module references the existing resource via a data source instead of creating it"
    - "A new live wiring at `infra/live/use1/ses-shared-rule-set/terragrunt.hcl` calls the module and reads the two flags + the domain inputs from env vars set by `km bootstrap`"
    - "The rule set has `lifecycle.prevent_destroy = true`"
    - "`terragrunt plan` against the live dir succeeds offline (no AWS calls planned for plan-only); plan output references both `aws_ses_receipt_rule_set` and `aws_ses_active_receipt_rule_set` (or their data-source equivalents when register flags are false)"
  artifacts:
    - path: "infra/modules/ses-shared-rule-set/v1.0.0/main.tf"
      provides: "Foundation module Terraform resources"
    - path: "infra/modules/ses-shared-rule-set/v1.0.0/variables.tf"
      provides: "Module inputs (rule_set_name, domain, email_subdomain, register_shared_rule_set, register_domain_identity, hosted_zone_id, etc.)"
    - path: "infra/modules/ses-shared-rule-set/v1.0.0/outputs.tf"
      provides: "rule_set_name (string), domain_identity_arn (string), email_domain (string)"
    - path: "infra/live/use1/ses-shared-rule-set/terragrunt.hcl"
      provides: "Live wiring that calls the module"
  key_links:
    - from: "infra/live/use1/ses-shared-rule-set/terragrunt.hcl"
      to: "infra/modules/ses-shared-rule-set/v1.0.0"
      via: "terraform.source = ...modules/ses-shared-rule-set/v1.0.0"
      pattern: "terraform { source ="
    - from: "infra/modules/ses-shared-rule-set/v1.0.0/main.tf"
      to: "AWS SES singleton state"
      via: "aws_ses_receipt_rule_set.shared with prevent_destroy + aws_ses_active_receipt_rule_set"
      pattern: "rule_set_name = \"sandbox-email-shared\""
---

<objective>
Create the new foundation Terraform module `infra/modules/ses-shared-rule-set/v1.0.0/` plus its live wiring at `infra/live/use1/ses-shared-rule-set/terragrunt.hcl`. The module owns ALL the account-shared SES state that previously lived in the regional `ses/` module: the rule set, the always-active pointer, the domain identity, DKIM CNAMEs, MX record, and SES verification records.

Per `84-RESEARCH.md` Open Question 2: domain identity moves to foundation (locked in CONTEXT.md "Additional Decisions" § SES domain identity). Per Open Question 3: idempotency via `register_X` auto-detect flags (Phase 80 cluster-irsa pattern).

Purpose: Plan 84-03 (regional v2.0.0) consumes the domain identity via data source and only owns the prefix-named rules. Plan 84-07 wires `km bootstrap --shared-ses` to apply this module.

Output:
- 4 new Terraform/Terragrunt files
- 0 Go code changes
- No AWS apply in this plan — `terragrunt plan` smoke-tested only
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/PROJECT.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-CONTEXT.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-RESEARCH.md

@infra/modules/ses/v1.0.0/main.tf
@infra/modules/ses/v1.0.0/variables.tf
@infra/modules/ses/v1.0.0/outputs.tf
@infra/modules/cluster-irsa/v1.0.0/main.tf
@infra/modules/cluster-irsa/v1.0.0/variables.tf
@infra/live/use1/ses/terragrunt.hcl

<interfaces>
<!-- Resources the new module owns. Sourced from 84-RESEARCH.md "Additional Decisions" + existing v1.0.0/main.tf. -->

Foundation module owns (per CONTEXT.md "Additional Decisions" § SES domain identity):
- `aws_ses_receipt_rule_set.shared` — rule set named "sandbox-email-shared" with `lifecycle.prevent_destroy = true`
- `aws_ses_active_receipt_rule_set.shared` — always activates the shared rule set
- `aws_ses_domain_identity.sandbox` — for the email domain (e.g., "sandboxes.example.com")
- `aws_ses_domain_dkim.sandbox` — 3 DKIM CNAMEs
- `aws_route53_record.mx` — MX → SES inbound (inbound-smtp.<region>.amazonaws.com priority 10)
- `aws_route53_record.ses_verification` — DKIM verification CNAMEs (count = 3, one per DKIM token)
- (verification TXT for domain identity if the v1.0.0 module had one — confirm by reading existing main.tf)

Phase 80 cluster-irsa idempotency pattern (from `infra/modules/cluster-irsa/v1.0.0/main.tf:51`):
```hcl
variable "register_oidc_provider" { type = bool, default = true }
resource "aws_iam_openid_connect_provider" "cluster" { count = var.register_oidc_provider ? 1 : 0 ... }
data "aws_iam_openid_connect_provider" "existing" { count = var.register_oidc_provider ? 0 : 1 ... }
```

Replicate this pattern for BOTH the rule set and the domain identity:
- `var.register_shared_rule_set` — gates `aws_ses_receipt_rule_set.shared` (count=1) vs data-source-lookup (count=0). Note: AWS SES has no `aws_ses_receipt_rule_set` data source (per RESEARCH Pitfall 2), so when register=false the module simply does not create the resource AND does not reference it — downstream consumers use the string constant "sandbox-email-shared" directly. The active-rule-set resource also count-gates on this var.
- `var.register_domain_identity` — gates `aws_ses_domain_identity.sandbox` + `aws_ses_domain_dkim.sandbox` + the route53 records (all count=1 when registering, count=0 when not). When not registering, downstream consumers re-derive any needed identifier from inputs (domain, etc.).

S3 bucket policy: NOT in this module. The regional `ses/v1.0.0` module owns the S3 bucket policy for the artifact bucket today. That policy is per-install and stays regional in v2.0.0 (Plan 84-03), keyed on `${var.resource_prefix}/mail/...` prefix.

Outputs:
- `rule_set_name` (string) — always "sandbox-email-shared" (constant when register=true; literal when register=false)
- `domain_identity_arn` (string) — when register_domain_identity=true, `aws_ses_domain_identity.sandbox[0].arn`; when false, derived as `"arn:aws:ses:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:identity/${var.email_domain}"` (or referenced via data source if one exists for SES domain identity)
- `email_domain` (string) — input pass-through for downstream convenience

Live terragrunt.hcl shape (mirror existing `infra/live/use1/ses/terragrunt.hcl` structure):
- `include "root" { path = find_in_parent_folders("root.hcl") }`
- `terraform { source = "${get_repo_root()}/infra/modules/ses-shared-rule-set/v1.0.0" }`
- `inputs = { rule_set_name = "sandbox-email-shared", email_domain = local.email_domain, hosted_zone_id = local.zone_id, register_shared_rule_set = tobool(get_env("KM_REGISTER_SHARED_RULESET", "true")), register_domain_identity = tobool(get_env("KM_REGISTER_DOMAIN_IDENTITY", "true")) }`
- DO NOT declare `required_providers` — per MEMORY note "Terragrunt provider declarations live in root.hcl"; the root's `generate "provider"` stanza is the single source.

The bootstrap command (Plan 84-07) will set `KM_REGISTER_SHARED_RULESET` and `KM_REGISTER_DOMAIN_IDENTITY` env vars before invoking terragrunt against this directory.
</interfaces>
</context>

<tasks>

<task type="auto">
  <name>Task 1: Create foundation module variables.tf + outputs.tf</name>
  <files>infra/modules/ses-shared-rule-set/v1.0.0/variables.tf, infra/modules/ses-shared-rule-set/v1.0.0/outputs.tf</files>
  <action>
Create the new module directory and write the variables and outputs contracts FIRST (interface-first ordering — Task 2 implements against these).

**`variables.tf`** — declare:
  - `rule_set_name` (string, default `"sandbox-email-shared"`) — shared singleton name
  - `email_domain` (string, no default) — e.g. `"sandboxes.example.com"`
  - `hosted_zone_id` (string, no default) — Route53 zone owning the email domain
  - `aws_region` (string, no default) — needed to construct MX target `inbound-smtp.<region>.amazonaws.com`
  - `register_shared_rule_set` (bool, default `true`) — when `false`, do not create rule-set resources (the existing rule set is reused by name from downstream modules)
  - `register_domain_identity` (bool, default `true`) — when `false`, do not create the domain identity, DKIM, MX, or verification records (assumes they already exist in this account)
  - `tags` (map(string), default `{}`) — propagated to all resources; foundation-level tags

Add a top-of-file comment header explaining: "Foundation module owning account-shared SES state. Per Phase 84, replaces the rule-set / domain-identity ownership that previously lived in `ses/v1.0.0`."

**`outputs.tf`** — declare:
  - `rule_set_name` — `var.rule_set_name` (always the constant; downstream consumers reference by string name)
  - `email_domain` — `var.email_domain` pass-through
  - `domain_identity_arn` — conditional: `try(aws_ses_domain_identity.sandbox[0].arn, null)` (null when register_domain_identity=false; downstream consumers should not rely on this output in that case)

Do not introduce any `required_providers` block — root.hcl owns provider generation.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/infra/modules/ses-shared-rule-set/v1.0.0 && terraform fmt -check -diff variables.tf outputs.tf && terraform init -backend=false 2>&1 | tail -10</automated>
  </verify>
  <done>variables.tf + outputs.tf exist, `terraform fmt -check` clean, `terraform init -backend=false` succeeds with placeholder main.tf (or returns the expected "no main.tf" diagnostic — fine as long as the variable/output syntax parses).</done>
</task>

<task type="auto">
  <name>Task 2: Implement foundation module main.tf (rule set + active + domain identity + DKIM + MX + verification)</name>
  <files>infra/modules/ses-shared-rule-set/v1.0.0/main.tf</files>
  <action>
Write the resource graph against the interface contracts from Task 1.

```hcl
# infra/modules/ses-shared-rule-set/v1.0.0/main.tf
# Foundation: account-shared SES state. See Phase 84 in .planning/ROADMAP.md.
# Plan 84-02 created this module; Plan 84-07 wires `km bootstrap --shared-ses` to apply it.

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

# --- Shared rule set + always-active pointer ---
resource "aws_ses_receipt_rule_set" "shared" {
  count         = var.register_shared_rule_set ? 1 : 0
  rule_set_name = var.rule_set_name

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_ses_active_receipt_rule_set" "shared" {
  count         = var.register_shared_rule_set ? 1 : 0
  rule_set_name = var.rule_set_name

  depends_on = [aws_ses_receipt_rule_set.shared]
}

# --- Shared domain identity + DKIM + DNS records ---
resource "aws_ses_domain_identity" "sandbox" {
  count  = var.register_domain_identity ? 1 : 0
  domain = var.email_domain
}

resource "aws_ses_domain_dkim" "sandbox" {
  count  = var.register_domain_identity ? 1 : 0
  domain = aws_ses_domain_identity.sandbox[0].domain
}

resource "aws_route53_record" "ses_verification" {
  count   = var.register_domain_identity ? 3 : 0
  zone_id = var.hosted_zone_id
  name    = "${element(aws_ses_domain_dkim.sandbox[0].dkim_tokens, count.index)}._domainkey.${var.email_domain}"
  type    = "CNAME"
  ttl     = 600
  records = ["${element(aws_ses_domain_dkim.sandbox[0].dkim_tokens, count.index)}.dkim.amazonses.com"]
}

resource "aws_route53_record" "mx" {
  count   = var.register_domain_identity ? 1 : 0
  zone_id = var.hosted_zone_id
  name    = var.email_domain
  type    = "MX"
  ttl     = 600
  records = ["10 inbound-smtp.${var.aws_region}.amazonaws.com"]
}
```

Cross-check the existing `infra/modules/ses/v1.0.0/main.tf` to confirm:
- The DKIM record naming convention matches the existing module (so a Phase 84 upgrade does not churn unrelated DNS records on first apply).
- The MX target matches existing (priority 10, `inbound-smtp.<region>.amazonaws.com`).
- If the v1.0.0 module also creates an `aws_ses_domain_identity_verification` waiter resource, port it here as well.

The module deliberately does NOT include the S3 bucket policy for the artifact bucket — that stays in the regional v2.0.0 module (Plan 84-03) keyed on per-install prefix.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/infra/modules/ses-shared-rule-set/v1.0.0 && terraform fmt -check -diff && terraform init -backend=false 2>&1 | tail -5 && terraform validate 2>&1 | tail -10</automated>
  </verify>
  <done>`terraform validate` clean. All 5 resource types declared with correct count-gating. No `required_providers` block. `prevent_destroy = true` on the rule set.</done>
</task>

<task type="auto">
  <name>Task 3: Create live wiring at infra/live/use1/ses-shared-rule-set/terragrunt.hcl</name>
  <files>infra/live/use1/ses-shared-rule-set/terragrunt.hcl</files>
  <action>
Create the new live directory and write the terragrunt.hcl. Mirror the existing `infra/live/use1/ses/terragrunt.hcl` structure, but consume the foundation module's inputs.

```hcl
# infra/live/use1/ses-shared-rule-set/terragrunt.hcl
# Live wiring for the foundation SES shared-rule-set module.
# Applied by `km bootstrap --shared-ses` (Plan 84-07).

include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "${get_repo_root()}/infra/modules/ses-shared-rule-set/v1.0.0"
}

locals {
  region_hcl    = read_terragrunt_config(find_in_parent_folders("region.hcl"))
  aws_region    = local.region_hcl.locals.aws_region
  email_domain  = "${get_env("KM_EMAIL_SUBDOMAIN", "sandboxes")}.${get_env("KM_PARENT_DOMAIN", "example.com")}"
  hosted_zone_id = get_env("KM_HOSTED_ZONE_ID", "")  # surfaced by km bootstrap
}

inputs = {
  rule_set_name             = "sandbox-email-shared"
  email_domain              = local.email_domain
  aws_region                = local.aws_region
  hosted_zone_id            = local.hosted_zone_id
  register_shared_rule_set  = tobool(get_env("KM_REGISTER_SHARED_RULESET", "true"))
  register_domain_identity  = tobool(get_env("KM_REGISTER_DOMAIN_IDENTITY", "true"))

  tags = {
    "km:owner"     = "foundation"
    "km:phase"     = "84"
    "km:component" = "ses-shared-rule-set"
  }
}
```

Cross-check the existing `infra/live/use1/ses/terragrunt.hcl` for the EXACT env-var names already in use (e.g., `KM_EMAIL_SUBDOMAIN`, `KM_PARENT_DOMAIN`, `KM_HOSTED_ZONE_ID`). Reuse the same names — Plan 84-07's `km bootstrap` will call `ExportConfigEnvVars` (per MEMORY note "Terragrunt env export required") which sets these from `km-config.yaml`.

If the existing live SES dir references a different env-var name for the hosted zone, adopt that name here verbatim.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/infra/live/use1/ses-shared-rule-set && KM_REGISTER_SHARED_RULESET=true KM_REGISTER_DOMAIN_IDENTITY=true KM_EMAIL_SUBDOMAIN=sandboxes KM_PARENT_DOMAIN=example.com KM_HOSTED_ZONE_ID=Z00000000000000000000 terragrunt --terragrunt-non-interactive hclvalidate 2>&1 | tail -10 || echo "(hclvalidate may not be available; falling back to terraform validate via terragrunt init)"; terragrunt --terragrunt-non-interactive init -backend=false 2>&1 | tail -10</automated>
  </verify>
  <done>terragrunt.hcl parses cleanly. `terragrunt init -backend=false` succeeds (or `hclvalidate` clean). No AWS calls in plan-only mode.</done>
</task>

</tasks>

<verification>
- 4 new files exist: 3 in `infra/modules/ses-shared-rule-set/v1.0.0/` + 1 in `infra/live/use1/ses-shared-rule-set/`.
- `terraform fmt -check` clean.
- `terraform validate` clean (with backend=false).
- No live AWS calls — this plan is plan-only.
- The shared `aws_ses_receipt_rule_set` has `lifecycle.prevent_destroy = true`.
- `register_shared_rule_set` and `register_domain_identity` flags follow Phase 80 cluster-irsa precedent.
</verification>

<success_criteria>
- Foundation module exists, validates, and is consumable from `km bootstrap --shared-ses` (Plan 84-07).
- The module is independent of regional state — Plan 84-03 consumes the rule set by string name only.
- Idempotent on re-apply (register_X=false branch).
</success_criteria>

<output>
After completion, create `.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-02-SUMMARY.md`
</output>
