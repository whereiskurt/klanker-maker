---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: 03
subsystem: infra
tags: [terraform, ses, email, multi-instance, receipt-rules, s3-policy]

# Dependency graph
requires:
  - phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
    provides: "Plan 84-01 context: ses-shared-rule-set foundation module decision"
provides:
  - "New infra/modules/ses/v2.0.0/ — rule-only SES module (operator_inbound + sandbox_catchall per-install)"
  - "Live wiring at infra/live/use1/ses/terragrunt.hcl updated to v2.0.0"
  - "Phase 82.1 activate_rule_set mechanism removed from live wiring and v2.0.0"
affects: [84-04, 84-05, 84-06, 84-07, 84-08, 84-09, 84-10]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "rule_set_name as string constant (no aws_ses_receipt_rule_set data source exists in AWS provider)"
    - "Per-install S3 bucket policy scoped to prefix paths: mail/<prefix>/* and mail/create/<prefix>/*"
    - "after= ordering between operator_inbound and sandbox_catchall rules for specific-before-general matching"
    - "Terragrunt module versioning: v2.0.0 created alongside v1.0.0 (historical reference preserved)"

key-files:
  created:
    - infra/modules/ses/v2.0.0/main.tf
    - infra/modules/ses/v2.0.0/variables.tf
    - infra/modules/ses/v2.0.0/outputs.tf
  modified:
    - infra/live/use1/ses/terragrunt.hcl

key-decisions:
  - "rule_set_name is a string constant 'sandbox-email-shared' — there is no aws_ses_receipt_rule_set data source in the AWS Terraform provider"
  - "v2.0.0 S3 bucket policy preserves CloudWatch Logs export grants from v1.0.0 (single policy per bucket)"
  - "v1.0.0 stays in tree untouched as historical reference — not deleted, not modified"
  - "activate_rule_set variable and KM_SES_ACTIVATE_RULESET env-var removed from live wiring (Phase 82.1 cleanup)"

patterns-established:
  - "Per-install SES rules: use resource_prefix to namespace rule names and S3 key prefixes"
  - "SES receipt rule ordering: operator_inbound first, sandbox_catchall second (after= link)"

requirements-completed: [SES-PER-INSTALL-RULES]

# Metrics
duration: 6min
completed: 2026-05-16
---

# Phase 84 Plan 03: Regional SES v2.0.0 Module Summary

**Rule-only SES v2.0.0 module with prefix-namespaced operator_inbound and sandbox_catchall rules attached to the foundation-owned 'sandbox-email-shared' rule set via string-constant rule_set_name**

## Performance

- **Duration:** 6 min
- **Started:** 2026-05-16T20:08:22Z
- **Completed:** 2026-05-16T20:14:43Z
- **Tasks:** 3
- **Files modified:** 4

## Accomplishments
- Created `infra/modules/ses/v2.0.0/` with variables.tf, outputs.tf, and main.tf implementing two prefix-namespaced SES receipt rules
- S3 bucket policy scoped to per-install prefix paths only (`mail/<prefix>/` and `mail/create/<prefix>/`) with CloudWatch Logs grants preserved
- Updated `infra/live/use1/ses/terragrunt.hcl` to point at v2.0.0 and removed all Phase 82.1 artifacts (`activate_rule_set` input, `KM_SES_ACTIVATE_RULESET` env-var)
- `v1.0.0/` left untouched in tree as historical reference; terragrunt init succeeds with v2.0.0 module

## Task Commits

Each task was committed atomically:

1. **Task 1: v2.0.0 interface contracts (variables.tf + outputs.tf)** - `7987854` (feat)
2. **Task 2: v2.0.0 main.tf — prefix-named rules + scoped S3 bucket policy** - `8ea8aec` (feat)
3. **Task 3: Live wiring update — v2.0.0, drop activate_rule_set** - `5e62a6a` (feat)

**Plan metadata:** (docs commit — see below)

## Files Created/Modified
- `infra/modules/ses/v2.0.0/variables.tf` — 4 inputs: resource_prefix, email_domain, artifact_bucket_name, tags
- `infra/modules/ses/v2.0.0/outputs.tf` — 2 outputs: operator_inbound_rule_name, sandbox_catchall_rule_name
- `infra/modules/ses/v2.0.0/main.tf` — Two aws_ses_receipt_rule resources + S3 bucket policy with CloudWatch Logs grants
- `infra/live/use1/ses/terragrunt.hcl` — Source path updated v1.0.0→v2.0.0; activate_rule_set + 3 removed inputs; email_domain replaces domain

## Decisions Made
- **rule_set_name as string constant:** No `aws_ses_receipt_rule_set` data source exists in the AWS Terraform provider, so `"sandbox-email-shared"` is a literal string. This is correct by design (RESEARCH Pitfall 2).
- **CloudWatch Logs grants in v2.0.0 bucket policy:** v1.0.0's single bucket policy owned all SES + CloudWatch grants. Since only one `aws_s3_bucket_policy` can exist per bucket and v2.0.0 replaces v1.0.0, the CloudWatch Logs export statements were ported to v2.0.0 to avoid dropping existing grants.
- **Live wiring drops route53_zone_id, artifact_bucket_arn, email_create_handler_arn:** These were needed for domain identity, DKIM, and Lambda notification resources — all moved to the foundation module. v2.0.0 only needs email_domain, artifact_bucket_name, and resource_prefix.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed variable description string interpolation**
- **Found during:** Task 1 verification (terraform init)
- **Issue:** variables.tf used `${resource_prefix}` inside a description string, which Terraform does not allow (variables cannot be referenced in descriptions)
- **Fix:** Replaced interpolation with angle-bracket placeholder `<resource_prefix>` in the description text
- **Files modified:** infra/modules/ses/v2.0.0/variables.tf
- **Verification:** `terraform init -backend=false` succeeded after fix
- **Committed in:** `8ea8aec` (included in Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - Bug)
**Impact on plan:** Minor syntax fix; no scope change.

## Issues Encountered
- `terraform validate` in module-standalone mode fails for this project (modules don't declare `required_providers` — root.hcl generates them). Validated using a temp directory with provider override + shared cache directory from the live terragrunt cache. The plan's verification step was adapted accordingly.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- v2.0.0 SES module is structurally complete and validated
- Live wiring ready for `km init` apply once the foundation module (Plan 84-02) is deployed
- Plan 84-08 can now clean up remaining `activate_rule_set` / `KM_SES_ACTIVATE_RULESET` references in docs, OPERATOR-GUIDE.md, and CLAUDE.md

---
*Phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix*
*Completed: 2026-05-16*
