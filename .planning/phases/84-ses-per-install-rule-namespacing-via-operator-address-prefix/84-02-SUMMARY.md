---
phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix
plan: 02
subsystem: infra
tags: [terraform, ses, route53, terragrunt, foundation-module]

# Dependency graph
requires:
  - phase: 84-01
    provides: research decisions on register_X flag pattern and AWS SES data-source limitations
provides:
  - Foundation Terraform module ses-shared-rule-set/v1.0.0 owning account-shared SES state
  - register_shared_rule_set and register_domain_identity idempotency flags
  - Live wiring at infra/live/use1/ses-shared-rule-set/terragrunt.hcl
affects:
  - 84-03 (regional ses/v2.0.0 consumes rule set by string constant)
  - 84-07 (km bootstrap --shared-ses applies this module)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Foundation module pattern: account-shared SES resources owned separately from per-install regional modules"
    - "register_X bool flag with count-gating for idempotent re-apply (Phase 80 cluster-irsa precedent, adapted for SES provider gaps)"
    - "No data-source fallback for SES resources — AWS provider has no aws_ses_receipt_rule_set or aws_ses_domain_identity data sources; downstream consumers use string constants instead"

key-files:
  created:
    - infra/modules/ses-shared-rule-set/v1.0.0/main.tf
    - infra/modules/ses-shared-rule-set/v1.0.0/variables.tf
    - infra/modules/ses-shared-rule-set/v1.0.0/outputs.tf
    - infra/live/use1/ses-shared-rule-set/terragrunt.hcl
  modified: []

key-decisions:
  - "No required_providers block in module — root.hcl owns provider generation (CLAUDE.md memory constraint)"
  - "KM_ROUTE53_ZONE_ID used for hosted_zone_id input to match existing ses/terragrunt.hcl convention"
  - "region_full key used from region.hcl (not aws_region which does not exist in that file)"
  - "aws_route53_record.dkim naming matches ses/v1.0.0 to avoid DNS record churn on Phase 84 migration"
  - "No data-source fallback when register_X=false — AWS SES provider gap; downstream uses string constants"
  - "lifecycle.prevent_destroy=true on aws_ses_receipt_rule_set.shared"

patterns-established:
  - "Foundation module lives in infra/modules/ses-shared-rule-set/ separate from regional ses/ module"
  - "Email domain composed from site_vars (email_subdomain + domain) not raw env vars in live wiring"

requirements-completed:
  - SES-SHARED-RULESET

# Metrics
duration: 4min
completed: 2026-05-16
---

# Phase 84 Plan 02: Foundation SES Shared-Rule-Set Module Summary

**New Terraform module ses-shared-rule-set/v1.0.0 owns account-shared SES rule set + domain identity with register_X idempotency flags, plus live wiring at infra/live/use1/ses-shared-rule-set/**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-05-16T20:08:31Z
- **Completed:** 2026-05-16T20:12:18Z
- **Tasks:** 3
- **Files modified:** 4 (all new)

## Accomplishments
- Foundation module with 7 AWS resources (rule set + active pointer + domain identity + DKIM + 3 DNS records), all count-gated on register_X flags
- `lifecycle.prevent_destroy = true` on `aws_ses_receipt_rule_set.shared` to protect the singleton
- Live wiring reads env vars matching existing ses/ convention (`KM_ROUTE53_ZONE_ID`, `KM_EMAIL_SUBDOMAIN`, `KM_DOMAIN`) for compatibility with `km bootstrap`'s `ExportConfigEnvVars`
- `terraform validate` and `terragrunt run -- init -backend=false` both pass cleanly

## Task Commits

Each task was committed atomically:

1. **Task 1: Create foundation module variables.tf + outputs.tf** - `b312981` (feat)
2. **Task 2: Implement foundation module main.tf** - `5d7e79c` (feat)
3. **Task 3: Create live wiring at infra/live/use1/ses-shared-rule-set/terragrunt.hcl** - `6ce6e48` (feat)

## Files Created/Modified
- `infra/modules/ses-shared-rule-set/v1.0.0/variables.tf` - 7 inputs: rule_set_name, email_domain, hosted_zone_id, aws_region, register_shared_rule_set, register_domain_identity, tags
- `infra/modules/ses-shared-rule-set/v1.0.0/outputs.tf` - 3 outputs: rule_set_name, email_domain, domain_identity_arn (nullable)
- `infra/modules/ses-shared-rule-set/v1.0.0/main.tf` - 7 resource blocks with count-gating; data sources for caller_identity + region
- `infra/live/use1/ses-shared-rule-set/terragrunt.hcl` - Live wiring calling the module; reads site_vars + region.hcl locals

## Decisions Made
- Used `KM_ROUTE53_ZONE_ID` (not `KM_HOSTED_ZONE_ID` as the plan template suggested) — matches the existing `infra/live/use1/ses/terragrunt.hcl` convention so `km bootstrap` needs no new env var exports
- Used `region_full` key from `region.hcl` (plan template referenced `aws_region` which doesn't exist in that file)
- Named DKIM CNAME records `aws_route53_record.dkim` (not `ses_verification`) to match `ses/v1.0.0` naming — prevents DNS churn when Phase 84 migration runs
- Kept `aws_route53_record.ses_verification` as the domain verification TXT record (following existing naming from ses/v1.0.0)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Corrected env var name for hosted_zone_id input**
- **Found during:** Task 3 (create live wiring)
- **Issue:** Plan template specified `KM_HOSTED_ZONE_ID` but existing `ses/terragrunt.hcl` uses `KM_ROUTE53_ZONE_ID`. Using a different name would require a new env var export in `km bootstrap` and create inconsistency.
- **Fix:** Used `KM_ROUTE53_ZONE_ID` in `ses-shared-rule-set/terragrunt.hcl` to match existing convention
- **Files modified:** infra/live/use1/ses-shared-rule-set/terragrunt.hcl
- **Verification:** Cross-checked against existing ses/terragrunt.hcl line 43
- **Committed in:** 6ce6e48 (Task 3 commit)

**2. [Rule 1 - Bug] Corrected region.hcl key name**
- **Found during:** Task 3 (create live wiring)
- **Issue:** Plan template referenced `local.region_hcl.locals.aws_region` but `infra/live/use1/region.hcl` only declares `region_full` (not `aws_region`)
- **Fix:** Used `local.region_config.locals.region_full` in the locals block
- **Files modified:** infra/live/use1/ses-shared-rule-set/terragrunt.hcl
- **Verification:** `terragrunt run -- init -backend=false` succeeds cleanly
- **Committed in:** 6ce6e48 (Task 3 commit)

---

**Total deviations:** 2 auto-fixed (both Rule 1 — incorrect field names in plan template cross-checked against actual file contents)
**Impact on plan:** Both fixes essential for correctness. No scope creep.

## Issues Encountered
- `terragrunt hclvalidate` unavailable in terragrunt v0.99.1 (new CLI redesign); used `terragrunt run -- init -backend=false` instead — confirmed HCL parses cleanly and module is correctly linked

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Foundation module is complete and ready for Plan 84-07 (`km bootstrap --shared-ses`) to apply it
- Plan 84-03 (regional ses/v2.0.0) can reference rule set by string constant `"sandbox-email-shared"` without any cross-state dependency
- `KM_REGISTER_SHARED_RULESET` and `KM_REGISTER_DOMAIN_IDENTITY` env vars ready for Plan 84-07 auto-detect logic

---
*Phase: 84-ses-per-install-rule-namespacing-via-operator-address-prefix*
*Completed: 2026-05-16*
