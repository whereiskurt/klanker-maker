---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: 03
type: execute
wave: 1
depends_on: [01]
files_modified:
  - infra/modules/ses/v2.0.0/main.tf
  - infra/modules/ses/v2.0.0/variables.tf
  - infra/modules/ses/v2.0.0/outputs.tf
  - infra/live/use1/ses/terragrunt.hcl
autonomous: true
requirements:
  - SES-PER-INSTALL-RULES

must_haves:
  truths:
    - "A new regional module exists at `infra/modules/ses/v2.0.0/` that ONLY owns prefix-named receipt rules — not the rule set, not the active-rule-set, not the domain identity, not the DKIM/MX/verification records"
    - "v2.0.0 declares `aws_ses_receipt_rule.operator_inbound` (name `${var.resource_prefix}-operator-inbound`, recipient `operator-${var.resource_prefix}@${var.email_domain}`, rule_set_name string-literal `\"sandbox-email-shared\"`)"
    - "v2.0.0 declares `aws_ses_receipt_rule.sandbox_catchall` (name `${var.resource_prefix}-sandbox-catchall`, recipient `${var.email_domain}` whole-domain match, rule_set_name string-literal `\"sandbox-email-shared\"`, with `after = aws_ses_receipt_rule.operator_inbound.name` so the specific match wins on ordering)"
    - "Both rules write to S3 under prefix-scoped keys: operator-inbound uses `mail/create/${var.resource_prefix}/`, catchall uses `mail/${var.resource_prefix}/`"
    - "v2.0.0 does NOT declare `activate_rule_set` variable nor `aws_ses_active_receipt_rule_set` resource"
    - "The S3 bucket policy for the artifact bucket is updated to grant SES PutObject under the prefix-scoped keys"
    - "v1.0.0 stays in tree untouched (historical reference)"
    - "`infra/live/use1/ses/terragrunt.hcl` is updated to reference `ses/v2.0.0` and drops the `activate_rule_set` input + the `KM_SES_ACTIVATE_RULESET` env-var read"
  artifacts:
    - path: "infra/modules/ses/v2.0.0/main.tf"
      provides: "Two aws_ses_receipt_rule resources + S3 bucket policy + sandbox domain-identity data source"
    - path: "infra/modules/ses/v2.0.0/variables.tf"
      provides: "resource_prefix, email_domain, artifact_bucket_name"
    - path: "infra/modules/ses/v2.0.0/outputs.tf"
      provides: "operator_inbound_rule_name, sandbox_catchall_rule_name"
    - path: "infra/live/use1/ses/terragrunt.hcl"
      provides: "Live wiring points at v2.0.0; activate_rule_set input removed"
  key_links:
    - from: "infra/modules/ses/v2.0.0/main.tf"
      to: "Foundation module's `sandbox-email-shared` rule set"
      via: "Hardcoded string constant rule_set_name = \"sandbox-email-shared\""
      pattern: "rule_set_name *= *\"sandbox-email-shared\""
    - from: "infra/modules/ses/v2.0.0/main.tf"
      to: "Foundation module's domain identity"
      via: "data source `aws_ses_domain_identity` (looked up by domain name, no cross-state required)"
      pattern: "data \"aws_ses_domain_identity\""
---

<objective>
Create the new regional SES module at `infra/modules/ses/v2.0.0/` that owns ONLY the per-install rules. `v1.0.0/` stays in tree untouched (historical reference, per CONTEXT.md "Specific Ideas"). Update `infra/live/use1/ses/terragrunt.hcl` to point at v2.0.0 and drop the Phase 82.1 `activate_rule_set` input + `KM_SES_ACTIVATE_RULESET` env-var read.

Per CONTEXT.md "Additional Decisions" § Sandbox catchall: catchall uses whole-domain recipient `sandboxes.${domain}` (NOT a wildcard — AWS SES doesn't support them). Both installs' catchall rules fire on every sandbox email; S3 prefix isolation prevents cross-contamination.

Per RESEARCH § Pattern 1: `rule_set_name` is a string constant, NOT a Terraform reference (there is no `aws_ses_receipt_rule_set` data source in the AWS provider).

Purpose: After this plan, the regional SES module is rule-only. The activate-rule-set resource that Phase 82.1 added is structurally absent from v2.0.0; Plan 84-08 will delete the now-orphaned `activate_rule_set` artifacts from v1.0.0 docs/runbook.

Output:
- 3 new files in `infra/modules/ses/v2.0.0/`
- 1 edited file (`infra/live/use1/ses/terragrunt.hcl`)
- `v1.0.0/` is UNTOUCHED
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-CONTEXT.md
@.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-RESEARCH.md

@infra/modules/ses/v1.0.0/main.tf
@infra/modules/ses/v1.0.0/variables.tf
@infra/modules/ses/v1.0.0/outputs.tf
@infra/live/use1/ses/terragrunt.hcl

<interfaces>
<!-- v2.0.0 module surface -->

Variables (variables.tf):
- `resource_prefix` (string) — per-install discriminator from km configure
- `email_domain` (string) — e.g. `"sandboxes.example.com"` (matches foundation module's `email_domain`)
- `artifact_bucket_name` (string) — same S3 bucket the v1.0.0 module wrote to (per-install or shared — confirm by reading existing live wiring)
- `tags` (map(string), default `{}`)

Resources (main.tf):
1. Data source for the existing domain identity (Foundation module owns it; v2.0.0 only consumes it):
   - `data "aws_ses_domain_identity" "sandbox" { domain = var.email_domain }` — Note: at the time of writing, the AWS provider may not have this data source for SES; if so, treat the domain identity as implicit (SES validates by name) and skip the data source. The MX/DKIM are already in place via foundation; the receipt rules don't require an explicit Terraform reference to the identity.
2. `aws_ses_receipt_rule.operator_inbound`:
   - `name = "${var.resource_prefix}-operator-inbound"`
   - `rule_set_name = "sandbox-email-shared"`  (string constant — RESEARCH Pitfall 2)
   - `recipients = ["operator-${var.resource_prefix}@${var.email_domain}"]`
   - `enabled = true`, `scan_enabled = false`
   - `s3_action { bucket_name = var.artifact_bucket_name, object_key_prefix = "mail/create/${var.resource_prefix}/", position = 1 }`
   - Other actions (Lambda invoke?, SNS?) — port from v1.0.0/main.tf if v1.0.0's rules called additional actions; the goal is functional parity for an install's own rule.
3. `aws_ses_receipt_rule.sandbox_catchall`:
   - `name = "${var.resource_prefix}-sandbox-catchall"`
   - `rule_set_name = "sandbox-email-shared"`
   - `recipients = [var.email_domain]`  (whole-domain match — see CONTEXT.md "Additional Decisions" § Sandbox catchall)
   - `enabled = true`, `scan_enabled = false`
   - `s3_action { bucket_name = var.artifact_bucket_name, object_key_prefix = "mail/${var.resource_prefix}/", position = 1 }`
   - `after = aws_ses_receipt_rule.operator_inbound.name` — specific match wins on ordering
4. `aws_s3_bucket_policy.mail` — port from v1.0.0 but narrow the SES PutObject grant to `mail/${var.resource_prefix}/*` and `mail/create/${var.resource_prefix}/*` only (per-install scope; siblings have their own bucket policies that grant access to their prefixes).

Outputs (outputs.tf):
- `operator_inbound_rule_name` — `aws_ses_receipt_rule.operator_inbound.name`
- `sandbox_catchall_rule_name` — `aws_ses_receipt_rule.sandbox_catchall.name`

NO outputs for `rule_set_name` (now a constant), `domain_identity_arn` (foundation owns it), or anything else that moved to foundation.

Live wiring update (`infra/live/use1/ses/terragrunt.hcl`):
- Change `terraform.source` from `.../ses/v1.0.0` to `.../ses/v2.0.0`.
- Remove any `activate_rule_set` input.
- Remove the `KM_SES_ACTIVATE_RULESET = get_env(...)` env read (if present in the file at line ~47 per RESEARCH).
- Remove inputs for `domain`, `hosted_zone_id`, `email_subdomain` if they were only used for the moved-to-foundation resources (keep them only if v2.0.0 still needs them for `email_domain`).
- Pass `email_domain` (built from `KM_EMAIL_SUBDOMAIN` + `KM_PARENT_DOMAIN`) and `artifact_bucket_name`.
- DO NOT modify the rest of the file structure (`include "root"`, `dependencies`, `locals`).
</interfaces>
</context>

<tasks>

<task type="auto">
  <name>Task 1: Create v2.0.0 module — variables.tf + outputs.tf (interface contracts)</name>
  <files>infra/modules/ses/v2.0.0/variables.tf, infra/modules/ses/v2.0.0/outputs.tf</files>
  <action>
Create `infra/modules/ses/v2.0.0/` and write variables.tf + outputs.tf FIRST.

`variables.tf`:
```hcl
# infra/modules/ses/v2.0.0/variables.tf
# Per-install SES rules. The shared rule set + domain identity now live in
# infra/modules/ses-shared-rule-set/v1.0.0 (Phase 84). This module only
# attaches prefix-named rules to that shared rule set.

variable "resource_prefix" {
  type        = string
  description = "Per-install discriminator. Used to namespace rule names and S3 key prefixes."
}

variable "email_domain" {
  type        = string
  description = "Full email domain, e.g. \"sandboxes.example.com\". Must match the foundation module's email_domain."
}

variable "artifact_bucket_name" {
  type        = string
  description = "S3 bucket receiving inbound mail. SES writes under mail/${resource_prefix}/ and mail/create/${resource_prefix}/."
}

variable "tags" {
  type    = map(string)
  default = {}
}
```

`outputs.tf`:
```hcl
output "operator_inbound_rule_name" {
  value = aws_ses_receipt_rule.operator_inbound.name
}

output "sandbox_catchall_rule_name" {
  value = aws_ses_receipt_rule.sandbox_catchall.name
}
```

Add NO `activate_rule_set` variable. Add NO `rule_set_name` output. NO `required_providers` block.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/infra/modules/ses/v2.0.0 && terraform fmt -check -diff variables.tf outputs.tf && ! grep -q "activate_rule_set" variables.tf && echo "variables clean"</automated>
  </verify>
  <done>Both files exist, format-clean. variables.tf has 4 inputs (no `activate_rule_set`). outputs.tf has 2 outputs (no `rule_set_name`).</done>
</task>

<task type="auto">
  <name>Task 2: Implement v2.0.0 main.tf with prefix-named rules + scoped S3 bucket policy</name>
  <files>infra/modules/ses/v2.0.0/main.tf</files>
  <action>
Write the two rules + S3 bucket policy. Port any additional rule actions from `v1.0.0/main.tf` (e.g., if v1.0.0's operator-inbound rule had a Lambda action or SNS notification, replicate it here exactly).

Critical constraints:
- `rule_set_name = "sandbox-email-shared"` (literal string, NOT a resource reference; RESEARCH Pitfall 2)
- `aws_ses_receipt_rule.sandbox_catchall` has `after = aws_ses_receipt_rule.operator_inbound.name`
- NO `aws_ses_receipt_rule_set` resource
- NO `aws_ses_active_receipt_rule_set` resource
- NO `aws_ses_domain_identity` / `aws_ses_domain_dkim` / `aws_route53_record` (foundation owns these)

```hcl
# infra/modules/ses/v2.0.0/main.tf
# Per-install rules attached to the foundation's "sandbox-email-shared" rule set.

# Operator inbound: SES routes operator-${prefix}@<domain> to mail/create/${prefix}/ in S3.
resource "aws_ses_receipt_rule" "operator_inbound" {
  name          = "${var.resource_prefix}-operator-inbound"
  rule_set_name = "sandbox-email-shared"  # String constant — foundation owns the rule-set resource.
  recipients    = ["operator-${var.resource_prefix}@${var.email_domain}"]
  enabled       = true
  scan_enabled  = false

  s3_action {
    bucket_name       = var.artifact_bucket_name
    object_key_prefix = "mail/create/${var.resource_prefix}/"
    position          = 1
  }
  # PORT any additional actions (Lambda invoke, SNS publish, etc.) from infra/modules/ses/v1.0.0/main.tf operator-inbound rule.
}

# Sandbox catchall: whole-domain match (no wildcards in classic SES).
# Both installs' catchall rules fire on every sandbox email; S3 prefix isolation
# (mail/${prefix}/) is the per-install boundary. The mail-handler Lambda's
# "unknown sandbox ID → drop" logic handles cross-contamination.
resource "aws_ses_receipt_rule" "sandbox_catchall" {
  name          = "${var.resource_prefix}-sandbox-catchall"
  rule_set_name = "sandbox-email-shared"
  recipients    = [var.email_domain]
  enabled       = true
  scan_enabled  = false

  after = aws_ses_receipt_rule.operator_inbound.name  # Specific match wins.

  s3_action {
    bucket_name       = var.artifact_bucket_name
    object_key_prefix = "mail/${var.resource_prefix}/"
    position          = 1
  }
  # PORT any additional actions from infra/modules/ses/v1.0.0/main.tf catchall rule.
}

# S3 bucket policy: grant SES PutObject on this install's prefix paths.
# Note: if v1.0.0 uses a consolidated bucket policy that BOTH installs need
# to coexist in, this module's policy resource must be additive — review
# v1.0.0/main.tf S3 policy and adopt the same merge strategy. If the policy
# is generated per-install and the bucket is per-install, this is straightforward.
resource "aws_s3_bucket_policy" "mail" {
  bucket = var.artifact_bucket_name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "AllowSESPutObjectScopedToPrefix-${var.resource_prefix}"
        Effect = "Allow"
        Principal = { Service = "ses.amazonaws.com" }
        Action = "s3:PutObject"
        Resource = [
          "arn:aws:s3:::${var.artifact_bucket_name}/mail/create/${var.resource_prefix}/*",
          "arn:aws:s3:::${var.artifact_bucket_name}/mail/${var.resource_prefix}/*",
        ]
        Condition = {
          StringEquals = {
            "aws:Referer" = data.aws_caller_identity.current.account_id
          }
        }
      },
    ]
  })
}

data "aws_caller_identity" "current" {}
```

Important caveat: If `v1.0.0/main.tf` uses a SINGLE bucket policy resource (not multiple per-install), then v2.0.0 cannot also create a sibling resource on the same bucket — Terraform will fight. Two options:
- (a) Read v1.0.0's policy. If it's already scoped per-install (likely uses `${var.resource_prefix}/` in the resource ARNs), the v2.0.0 policy is structurally the same; Terraform's `replace` semantics will handle migration when the live dir switches modules.
- (b) If v1.0.0's policy is consolidated, surface this in the verify step and document a planner-decision: bucket policy stays in v1.0.0 (live dir still calls it conditionally?) OR moves to a separate `s3-mail-policy` module. **Default assumption: per-install bucket policy is fine for v2.0.0.** Operator UAT (Plan 84-10) will catch any policy drift.

After writing, run `terraform fmt`, `terraform init -backend=false`, `terraform validate`.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/infra/modules/ses/v2.0.0 && terraform fmt -check -diff && terraform init -backend=false 2>&1 | tail -5 && terraform validate 2>&1 | tail -10 && grep -c "sandbox-email-shared" main.tf | grep -q "^2$" && echo "two rule_set_name references"</automated>
  </verify>
  <done>main.tf validates clean. Both rules reference `sandbox-email-shared` as a string constant. catchall has `after = aws_ses_receipt_rule.operator_inbound.name`. No active-rule-set resource. No domain-identity/DKIM/MX/verification records.</done>
</task>

<task type="auto">
  <name>Task 3: Update live wiring at infra/live/use1/ses/terragrunt.hcl to point at v2.0.0 and drop activate_rule_set</name>
  <files>infra/live/use1/ses/terragrunt.hcl</files>
  <action>
Edit (not rewrite) the existing live SES terragrunt.hcl:

1. Change `terraform { source = "${get_repo_root()}/infra/modules/ses/v1.0.0" }` → `.../ses/v2.0.0`.
2. Remove the `activate_rule_set` input line (per RESEARCH, currently at ~line 47).
3. Remove any `KM_SES_ACTIVATE_RULESET = get_env(...)` env-var read.
4. Remove inputs that v2.0.0 no longer accepts: `domain`, `hosted_zone_id`, `email_subdomain` (if previously passed). Replace with the v2.0.0 contract: `email_domain = "<derived>"`, `artifact_bucket_name = "<derived>"`, `resource_prefix = "${get_env(\"KM_RESOURCE_PREFIX\", \"km\")}"`.
5. KEEP: `include "root"`, `dependencies` block (if any), the `locals` block (adjust as needed).

After editing, ensure the file has NO references to:
- `activate_rule_set`
- `KM_SES_ACTIVATE_RULESET`

(These two strings remain elsewhere — CLAUDE.md, OPERATOR-GUIDE.md, v1.0.0/* — and will be cleaned by Plan 84-08. This task only handles the live terragrunt.hcl.)
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr/infra/live/use1/ses && grep -c "v2.0.0" terragrunt.hcl | grep -q "^1$" && echo "points at v2.0.0" && ! grep -q "activate_rule_set\|KM_SES_ACTIVATE_RULESET" terragrunt.hcl && echo "no Phase 82.1 leftovers in this file" && KM_RESOURCE_PREFIX=km KM_EMAIL_SUBDOMAIN=sandboxes KM_PARENT_DOMAIN=example.com terragrunt --terragrunt-non-interactive init -backend=false 2>&1 | tail -5</automated>
  </verify>
  <done>Live terragrunt.hcl references v2.0.0, no Phase 82.1 references, terragrunt init succeeds offline.</done>
</task>

</tasks>

<verification>
- `infra/modules/ses/v2.0.0/` has main.tf, variables.tf, outputs.tf — `terraform validate` clean.
- `infra/modules/ses/v1.0.0/` is UNTOUCHED.
- `infra/live/use1/ses/terragrunt.hcl` references v2.0.0 and has no `activate_rule_set` / `KM_SES_ACTIVATE_RULESET` strings.
- `grep -rn "aws_ses_receipt_rule_set\|aws_ses_active_receipt_rule_set" infra/modules/ses/v2.0.0/` returns nothing (rule-set + active are foundation-only).
</verification>

<success_criteria>
- v2.0.0 module is rule-only and validates.
- Live wiring references v2.0.0 and drops Phase 82.1 inputs.
- v1.0.0 remains in tree as historical reference.
- Together with Plan 84-02, the regional + foundation Terraform split is structurally complete.
</success_criteria>

<output>
After completion, create `.planning/phases/84-ses-per-install-rule-namespacing-via-operator-address-prefix/84-03-SUMMARY.md`
</output>
