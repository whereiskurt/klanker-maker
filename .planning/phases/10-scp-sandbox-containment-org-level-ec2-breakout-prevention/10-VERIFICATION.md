---
phase: 10-scp-sandbox-containment-org-level-ec2-breakout-prevention
verified: 2026-03-22T00:00:00Z
status: passed
score: 13/13 must-haves verified
re_verification: false
---

# Phase 10: SCP Sandbox Containment Verification Report

**Phase Goal:** Implement SCP-based sandbox containment at the AWS Organizations level to prevent EC2 breakout.
**Verified:** 2026-03-22
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths (Plan 01)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | SCP denies security group mutation for non-trusted roles | VERIFIED | `DenySGMutation` statement in main.tf with `ArnNotLike` condition on `aws:PrincipalARN` using `local.trusted_arns_base`; 9 actions covered |
| 2 | SCP denies network escape for non-trusted roles | VERIFIED | `DenyNetworkEscape` statement with 12 actions (CreateVpc, CreateSubnet, CreateRouteTable, CreateRoute, CreateInternetGateway, AttachInternetGateway, CreateEgressOnlyInternetGateway, CreateNatGateway, CreateVpcPeeringConnection, AcceptVpcPeeringConnection, CreateTransitGateway, CreateTransitGatewayVpcAttachment); `ArnNotLike` condition on `trusted_arns_base` |
| 3 | SCP denies instance mutation for non-trusted roles | VERIFIED | `DenyInstanceMutation` statement denies RunInstances, ModifyInstanceAttribute, ModifyInstanceMetadataOptions; carve-out uses `trusted_arns_instance` (includes km-ecs-spot-handler, excludes km-ecs-task-*) |
| 4 | SCP denies IAM escalation for non-trusted roles with budget-enforcer carve-out | VERIFIED | `DenyIAMEscalation` statement denies CreateRole, AttachRolePolicy, DetachRolePolicy, PassRole, AssumeRole; carve-out uses `trusted_arns_iam` which concatenates base + km-budget-enforcer-* |
| 5 | SCP denies storage exfiltration for non-trusted roles | VERIFIED | `DenyStorageExfiltration` statement denies CreateSnapshot, CopySnapshot, CreateImage, CopyImage, ExportImage; uses `trusted_arns_base` |
| 6 | SCP denies SSM cross-instance pivoting for non-operator roles | VERIFIED | `DenySSMPivot` statement denies ssm:SendCommand, ssm:StartSession; uses `trusted_arns_ssm` (km-ec2spot-ssm-* and AWSReservedSSO_*_* only, hardcoded in module) |
| 7 | SCP denies Organizations/account discovery for all roles | VERIFIED | `DenyOrganizationsDiscovery` statement has NO condition block — applies to all roles; denies ListAccounts, DescribeOrganization, ListRoots, ListOrganizationalUnitsForParent, ListChildren |
| 8 | SCP enforces region lock using NotAction double-negative pattern with global service exemptions | VERIFIED | `DenyOutsideAllowedRegions` uses `not_actions` with 20 global service patterns (iam:*, sts:*, organizations:*, support:*, health:*, trustedadvisor:*, cloudfront:*, waf:*, shield:*, route53:*, route53domains:*, budgets:*, ce:*, cur:*, globalaccelerator:*, networkmanager:*, pricing:*, and 3 S3 account-level actions); `StringNotEquals aws:RequestedRegion` condition with no trusted role carve-out |
| 9 | Terragrunt unit deploys SCP from management account context via assume_role | VERIFIED | `infra/live/management/scp/terragrunt.hcl` generates provider block with `assume_role { role_arn = "arn:aws:iam::${local.accounts.management}:role/km-org-admin" }`, region hardcoded to us-east-1 |

### Observable Truths (Plan 02)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 10 | `km bootstrap --dry-run` shows SCP deployment as a planned bootstrap step | VERIFIED | bootstrap.go lines 153-165 print SCP policy name, target account ID, threat coverage, trusted roles; TestBootstrapDryRunShowsSCP PASSES |
| 11 | `km bootstrap` non-dry-run calls terragrunt apply on management/scp unit | VERIFIED | bootstrap.go lines 173-182: constructs `filepath.Join(findRepoRoot(), "infra", "live", "management", "scp")` and calls `ApplyTerragruntFunc(ctx, scpDir)`; TestBootstrapSCPApplyPath PASSES verifying path suffix |
| 12 | Carve-outs for km system roles listed in bootstrap dry-run output | VERIFIED | bootstrap.go line 159-160: "Trusted roles: AWSReservedSSO_*_*, km-provisioner-*, km-lifecycle-*, km-ecs-spot-handler, km-ttl-handler" in dry-run output |
| 13 | Tests pass for dry-run with/without management account and apply path | VERIFIED | `go test ./internal/app/cmd/... -run TestBootstrap` result: 4/4 PASS (TestBootstrapDryRunShowsSCP, TestBootstrapDryRunNoManagementAccount, TestBootstrapSCPApplyPath, TestBootstrapDryRun) |

**Score:** 13/13 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `infra/modules/scp/v1.0.0/main.tf` | SCP policy resource + attachment + IAM policy document | VERIFIED | 274 lines; `data "aws_iam_policy_document"` with 8 statements; `aws_organizations_policy` resource; `aws_organizations_policy_attachment` resource |
| `infra/modules/scp/v1.0.0/variables.tf` | Module inputs: application_account_id, allowed_regions, trusted_role_arns | VERIFIED | 3 variables with descriptions and validations; `trusted_role_arns` defaults to AWSReservedSSO_*_* |
| `infra/modules/scp/v1.0.0/outputs.tf` | policy_id and policy_arn outputs | VERIFIED | 2 outputs: `policy_id` and `policy_arn` |
| `infra/live/management/scp/terragrunt.hcl` | Management account Terragrunt unit for SCP deployment | VERIFIED | Reads site.hcl, generates management-account provider with km-org-admin assume_role, sets remote state key `management/scp/terraform.tfstate`, passes inputs |
| `internal/app/cmd/bootstrap.go` | SCP bootstrap step in runBootstrap() | VERIFIED | Exports `TerragruntApplyFunc` type and `ApplyTerragruntFunc` var; `NewBootstrapCmdWithWriter` constructor; runBootstrap with context.Context and io.Writer parameters |
| `internal/app/cmd/bootstrap_test.go` | Unit tests for bootstrap dry-run SCP output and apply path | VERIFIED | 3 tests in `package cmd_test`: TestBootstrapDryRunShowsSCP, TestBootstrapDryRunNoManagementAccount, TestBootstrapSCPApplyPath; all pass |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `infra/live/management/scp/terragrunt.hcl` | `infra/modules/scp/v1.0.0/` | `terraform.source` | WIRED | `source = "${dirname(find_in_parent_folders("CLAUDE.md"))}/infra/modules/scp//v1.0.0"` — double-slash for module subdir per Terragrunt convention |
| `infra/live/management/scp/terragrunt.hcl` | `infra/live/site.hcl` | `find_in_parent_folders` | WIRED | `read_terragrunt_config(find_in_parent_folders("site.hcl"))` used for site, accounts, region, and backend config |
| `internal/app/cmd/bootstrap.go` | `infra/live/management/scp/` | path resolution for terragrunt apply | WIRED | `filepath.Join(findRepoRoot(), "infra", "live", "management", "scp")` — TestBootstrapSCPApplyPath verifies suffix |
| `internal/app/cmd/bootstrap.go` | `pkg/terragrunt` | `terragrunt.NewRunner` for SCP apply | WIRED | Import `github.com/whereiskurt/klankrmkr/pkg/terragrunt`; `defaultApplyTerragrunt` calls `terragrunt.NewRunner(awsProfile, repoRoot).Apply(ctx, dir)` |

---

### Requirements Coverage

Plan 01 declares: SCP-01, SCP-02, SCP-03, SCP-04, SCP-05, SCP-06, SCP-07, SCP-08, SCP-10
Plan 02 declares: SCP-09, SCP-11, SCP-12

These IDs are defined inline in ROADMAP.md Phase 10 section. They do NOT appear in the `REQUIREMENTS.md` traceability table (the SCP requirements were added in ROADMAP.md as phase-specific requirements, not merged into the main REQUIREMENTS.md v1 list). This is an acknowledged gap in cross-reference documentation but does not affect implementation completeness.

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| SCP-01 | Plan 01 | SCP denies SG mutation for non-provisioner roles | SATISFIED | `DenySGMutation` in main.tf |
| SCP-02 | Plan 01 | SCP denies network escape for non-provisioner roles | SATISFIED | `DenyNetworkEscape` in main.tf |
| SCP-03 | Plan 01 | SCP denies instance mutation for non-provisioner/lifecycle roles | SATISFIED | `DenyInstanceMutation` in main.tf; km-ecs-spot-handler carved out via `trusted_arns_instance` |
| SCP-04 | Plan 01 | SCP denies IAM escalation for non-provisioner/lifecycle roles | SATISFIED | `DenyIAMEscalation` in main.tf; km-budget-enforcer-* carved out via `trusted_arns_iam` |
| SCP-05 | Plan 01 | SCP denies storage exfiltration for non-provisioner roles | SATISFIED | `DenyStorageExfiltration` in main.tf |
| SCP-06 | Plan 01 | SCP denies SSM pivoting for non-operator roles | SATISFIED | `DenySSMPivot` in main.tf with restricted `trusted_arns_ssm` |
| SCP-07 | Plan 01 | SCP denies Organizations/account discovery for all roles | SATISFIED | `DenyOrganizationsDiscovery` with no condition block |
| SCP-08 | Plan 01 | SCP enforces region lock matching allowed regions | SATISFIED | `DenyOutsideAllowedRegions` with `not_actions` + `StringNotEquals aws:RequestedRegion`; `allowed_regions` input passed from site.hcl |
| SCP-09 | Plan 02 | Budget-enforcer Lambda scoped to only modify sandbox roles | SATISFIED | Pre-existing guarantee from Phase 6: `infra/modules/budget-enforcer/v1.0.0/main.tf` scopes `Resource = var.role_arn`; explicitly noted in Plan 02 as "pre-existing guarantee, not verified by this plan" |
| SCP-10 | Plan 01 | Terraform module `infra/modules/scp/` with variables for account IDs, allowed regions, role ARN patterns | SATISFIED | Module at `infra/modules/scp/v1.0.0/` with three variables |
| SCP-11 | Plan 02 | `km bootstrap` wires SCP creation into Management account provisioning flow | SATISFIED | bootstrap.go non-dry-run path calls `ApplyTerragruntFunc` on management/scp dir |
| SCP-12 | Plan 02 | Carve-outs for km-provisioner-*, km-lifecycle-*, km-ttl-handler, km-ecs-spot-handler, km-budget-enforcer-* verified against role naming conventions | SATISFIED | All carve-out patterns present in terragrunt.hcl trusted_role_arns input and module locals; km-budget-enforcer-* and km-ec2spot-ssm-* correctly scoped to specific statements only |

**Orphaned SCP requirements in REQUIREMENTS.md:** None — no SCP-* IDs appear in REQUIREMENTS.md because these are ROADMAP-level requirements defined for this phase only.

---

### Critical Design Decision Verification

These properties from the PLAN verification checklist were confirmed in the actual code:

| Property | Expected | Actual | Result |
|----------|----------|--------|--------|
| Condition pattern | `ArnNotLike` on `aws:PrincipalARN` | Found 6 occurrences of `ArnNotLike` in main.tf | CORRECT |
| `NotPrincipal` usage | Must be absent (unsupported in SCPs) | 0 occurrences in main.tf | CORRECT |
| `km-ecs-task-*` carve-out | Must NOT appear in any carve-out | Only appears in a comment ("intentionally NOT carved out") | CORRECT |
| `km-budget-enforcer-*` scope | Only in IAM escalation carve-out | Present only in `trusted_arns_iam` local (line 23) | CORRECT |
| `km-ecs-spot-handler` scope | Only in instance mutation carve-out | Present only in `trusted_arns_instance` local (line 16) | CORRECT |
| `DenyOrganizationsDiscovery` condition | NO condition | No condition block in that statement | CORRECT |
| Region lock carve-out | No trusted role carve-out | `DenyOutsideAllowedRegions` has no ArnNotLike condition | CORRECT |

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/app/cmd/bootstrap.go` | 168 | `"[Not yet implemented]"` in dry-run user-facing message | Warning | Misleading UX: the non-dry-run SCP deployment path IS fully implemented at lines 172-182; this message is leftover from before the implementation was added. Does not affect functionality. |

---

### Human Verification Required

The following items cannot be verified programmatically:

#### 1. SCP Deployment in Real AWS Environment

**Test:** With a real AWS management account that owns the organization, run `terragrunt apply` from `infra/live/management/scp/` with the km-org-admin IAM role provisioned.
**Expected:** SCP named `km-sandbox-containment` appears in AWS Organizations, attached to the application account ID.
**Why human:** Requires live AWS credentials, a real Organizations setup, and km-org-admin role provisioned in management account.

#### 2. SCP Effective Denial Confirmation

**Test:** From the application account, attempt ec2:RunInstances using a non-trusted role (e.g., an ECS task role). Also attempt using a trusted role (e.g., AWSReservedSSO_*).
**Expected:** Non-trusted role gets explicit SCP Deny; trusted role succeeds (if IAM policy allows).
**Why human:** Requires live AWS credentials and actual SCP enforcement to validate the ArnNotLike wildcard patterns resolve correctly.

#### 3. Dry-Run Message Accuracy

**Test:** Run `km bootstrap --dry-run` on a system with km-config.yaml configured.
**Expected:** Output accurately reflects the SCP that would be deployed. The "Not yet implemented" trailer (line 168) is misleading — manually verify it does not confuse operators about whether the non-dry-run apply path works.
**Why human:** UX judgment call; the string "[Not yet implemented]" on line 168 of bootstrap.go is a cosmetic inaccuracy that appeared before implementation was complete. Should be cleaned up in a follow-on commit.

---

### Verified Commits

All documented commits from the SUMMARY files exist in the repository:

| Commit | Description |
|--------|-------------|
| `18e0861` | feat(10-01): create SCP Terraform module with 8 deny statements |
| `c525a56` | feat(10-01): create Terragrunt live unit for management account SCP deployment |
| `beaeddf` | test(10-02): add failing tests for bootstrap SCP deployment step |
| `f588d25` | feat(10-02): extend bootstrap.go with SCP deployment step |

---

### Summary

Phase 10 goal is **achieved**. The SCP-based sandbox containment is implemented as a real Terraform module with 8 deny statements using `aws_iam_policy_document` (not hand-coded JSON), correctly employing `ArnNotLike` on `aws:PrincipalARN` instead of the unsupported `NotPrincipal`. Per-statement carve-out locals (`trusted_arns_base`, `trusted_arns_instance`, `trusted_arns_iam`, `trusted_arns_ssm`) ensure that km-budget-enforcer-* bypasses only IAM escalation, km-ecs-spot-handler bypasses only instance mutation, and SSM pivot has its own tight operator-only carve-out — the sandbox workload role (km-ecs-task-*) is correctly excluded from all carve-outs.

The Terragrunt management account unit correctly assumes km-org-admin via `assume_role`, inherits account IDs and state backend from site.hcl, and attaches the SCP to the application account. The `km bootstrap` command is wired with DI-friendly exported symbols (`TerragruntApplyFunc`, `ApplyTerragruntFunc`) following the ShellExecFunc pattern, with 4/4 tests passing.

One cosmetic warning: bootstrap.go line 168 prints "[Not yet implemented]" in the dry-run tail message, which is misleading since the non-dry-run path is fully implemented. This does not affect functionality.

All 12 SCP requirements (SCP-01 through SCP-12) from ROADMAP.md Phase 10 are satisfied by the implementation. The SCP-* IDs are not reflected in REQUIREMENTS.md's traceability table — they exist only in ROADMAP.md — which is an acceptable documentation pattern for phase-specific non-functional requirements.

---

_Verified: 2026-03-22_
_Verifier: Claude (gsd-verifier)_
