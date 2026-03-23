---
phase: 10-scp-sandbox-containment-org-level-ec2-breakout-prevention
plan: "01"
subsystem: infra
tags: [terraform, terragrunt, scp, organizations, iam, ec2, security]

# Dependency graph
requires:
  - phase: 09-live-infrastructure-operator-docs
    provides: site.hcl locals pattern, module_base pattern, Terragrunt unit structure
  - phase: 06-budget-enforcement-platform-configuration
    provides: km-budget-enforcer-* role naming (IAM carve-out reference)
  - phase: 08-sidecar-build-deployment-pipeline
    provides: km-ecs-spot-handler role naming (instance mutation carve-out reference)
provides:
  - Terraform module infra/modules/scp/v1.0.0/ with 8 deny statements
  - Terragrunt deployment unit infra/live/management/scp/terragrunt.hcl
  - Org-level backstop denying EC2/network/IAM/storage/SSM/org breakout from sandbox account
affects:
  - future provisioning plans (km-provisioner-* needs to be in trusted_role_arns input)
  - budget-enforcer (carve-out for IAM escalation already baked in module)
  - operator guide (new deployment step: km apply management/scp)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - aws_iam_policy_document data source (not hand-coded JSON) for SCP content
    - Statement-specific carve-out locals (trusted_arns_instance, trusted_arns_iam, trusted_arns_ssm)
    - Management account assume_role in Terragrunt provider override
    - not_actions (NotAction) pattern for region lock with global service exemption list

key-files:
  created:
    - infra/modules/scp/v1.0.0/main.tf
    - infra/modules/scp/v1.0.0/variables.tf
    - infra/modules/scp/v1.0.0/outputs.tf
    - infra/live/management/scp/terragrunt.hcl
  modified: []

key-decisions:
  - "ArnNotLike condition on aws:PrincipalARN used instead of NotPrincipal — NotPrincipal is not supported in SCPs"
  - "km-ecs-task-* intentionally NOT carved out from any deny statement — it IS the sandbox workload and must be fully contained"
  - "km-budget-enforcer-* carve-out scoped to DenyIAMEscalation only — not a blanket trusted_role_arns entry — budget enforcer needs AttachRolePolicy/DetachRolePolicy for Bedrock revocation"
  - "km-ecs-spot-handler carve-out scoped to DenyInstanceMutation only — not a blanket trusted_role_arns entry"
  - "trusted_arns_ssm hardcoded inside module (not from var.trusted_role_arns) — only SSM instance roles and operator SSO are allowed, preventing over-broad carve-out"
  - "DenyOrganizationsDiscovery has no condition — applies to ALL roles in application account; management account is exempt by AWS design"
  - "Region lock uses not_actions with comprehensive global service list — no trusted role carve-out, lock applies to operators too"
  - "Management account state key not region-prefixed (management/scp/terraform.tfstate) — Organizations is a global service"

patterns-established:
  - "Statement-specific locals pattern: trusted_arns_base, trusted_arns_instance, trusted_arns_iam, trusted_arns_ssm allow precise per-statement carve-outs without polluting var.trusted_role_arns"
  - "Management account Terragrunt unit overrides root provider with assume_role for km-org-admin"

requirements-completed: [SCP-01, SCP-02, SCP-03, SCP-04, SCP-05, SCP-06, SCP-07, SCP-08, SCP-10]

# Metrics
duration: 3min
completed: 2026-03-23
---

# Phase 10 Plan 01: SCP Sandbox Containment Summary

**AWS Organizations SCP Terraform module with 8 deny statements (SG mutation, network escape, instance mutation, IAM escalation, storage exfiltration, SSM pivot, org discovery, region lock) and Terragrunt management account deployment unit using ArnNotLike + statement-specific carve-out locals**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-03-23T01:04:04Z
- **Completed:** 2026-03-23T01:06:23Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments
- Created `infra/modules/scp/v1.0.0/` with all 8 SCP deny statements using `aws_iam_policy_document` data source (no hand-coded JSON)
- Implemented precise per-statement carve-out locals so km-budget-enforcer-* only bypasses IAM escalation, km-ecs-spot-handler only bypasses instance mutation, and SSM pivot has its own tight carve-out list
- Created `infra/live/management/scp/terragrunt.hcl` with management account provider (km-org-admin assume_role) and global-scope state key

## Task Commits

Each task was committed atomically:

1. **Task 1: Create SCP Terraform module with all deny statements and carve-outs** - `18e0861` (feat)
2. **Task 2: Create Terragrunt live unit for management account SCP deployment** - `c525a56` (feat)

**Plan metadata:** _(docs commit follows)_

## Files Created/Modified
- `infra/modules/scp/v1.0.0/main.tf` - SCP policy document with 8 deny statements, carve-out locals, aws_organizations_policy resource and attachment
- `infra/modules/scp/v1.0.0/variables.tf` - application_account_id, allowed_regions, trusted_role_arns inputs
- `infra/modules/scp/v1.0.0/outputs.tf` - policy_id and policy_arn outputs
- `infra/live/management/scp/terragrunt.hcl` - Terragrunt unit sourcing module, management account provider, trusted role ARN patterns

## Decisions Made
- ArnNotLike condition on `aws:PrincipalARN` used (not NotPrincipal — unsupported in SCPs)
- km-ecs-task-* intentionally NOT in any carve-out list — that is the sandbox workload role
- km-budget-enforcer-* carve-out scoped to DenyIAMEscalation only via `trusted_arns_iam` local
- km-ecs-spot-handler carve-out scoped to DenyInstanceMutation only via `trusted_arns_instance` local
- DenyOrganizationsDiscovery has no condition — applies to ALL roles (management account exempt by AWS design)
- Region lock uses not_actions with global service exemptions; no trusted role carve-out (operators also region-locked)
- State key not region-prefixed (`management/scp/terraform.tfstate`) since Organizations is a global API

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None.

## User Setup Required
Deployment requires:
1. km-org-admin IAM role provisioned in management account with Organizations read/write permissions
2. Run `terragrunt apply` from `infra/live/management/scp/` using management account credentials
3. KM_ACCOUNTS_MANAGEMENT, KM_ACCOUNTS_APPLICATION, KM_REGION set in environment

## Next Phase Readiness
- SCP is the account-level backstop — once deployed, sandbox containment is a property of the AWS account regardless of IAM role misconfiguration
- km-provisioner-* and km-lifecycle-* are pre-carved in trusted_role_arns for when dedicated provisioner roles are introduced
- No blockers for future phases

---
*Phase: 10-scp-sandbox-containment-org-level-ec2-breakout-prevention*
*Completed: 2026-03-23*
