# Phase 10: SCP Sandbox Containment — Research

**Researched:** 2026-03-22
**Domain:** AWS Organizations Service Control Policies, Terraform aws_organizations_policy, multi-account IAM containment
**Confidence:** HIGH (SCP API and Terraform resources well-documented; policy JSON patterns verified from AWS official sources)

---

## Summary

Phase 10 deploys an org-level Service Control Policy (SCP) that acts as the account-level backstop for sandbox containment. Even if a sandbox IAM role is misconfigured or a privilege-escalation bug exists in a profile compiler, the SCP hard-blocks the most dangerous breakout vectors at the AWS Organizations layer — a layer that cannot be overridden by any role within the application account. The SCP covers six threat categories: network escape, security group mutation, instance mutation, IAM escalation, storage exfiltration, and SSM pivoting.

The Terraform implementation lives in a standalone module `infra/modules/scp/` and is attached to the application account (not an OU root) via `aws_organizations_policy_attachment`. The Terraform provider must be configured to authenticate to the **management account** — the account that owns the AWS Organization — since SCP management is an Organizations API operation and that API is only callable from the management account or a delegated administrator account.

The `km bootstrap` command, currently a dry-run stub, gains real SCP provisioning as one of its bootstrap steps: resolve management account credentials → apply the `infra/modules/scp/` Terragrunt unit → print confirmation. The SCP uses `ArnNotLike` on `aws:PrincipalARN` to carve out trusted km system roles (provisioner, lifecycle, TTL handler, ECS spot handler, budget enforcer) from each Deny statement.

**Primary recommendation:** One Terraform module, one SCP document (multiple statements), one Terragrunt unit in the management account scope, attached by account ID to the application account. Use `ArnNotLike` wildcard patterns to carve out km system roles. Do not use NotPrincipal — it is not supported in SCPs.

---

## Standard Stack

### Core

| Library / Resource | Version | Purpose | Why Standard |
|--------------------|---------|---------|--------------|
| `aws_organizations_policy` | AWS provider ≥ 5.0 | Creates the SCP document | Only Terraform resource for SCP; `type = "SERVICE_CONTROL_POLICY"` |
| `aws_organizations_policy_attachment` | AWS provider ≥ 5.0 | Attaches SCP to account/OU/root | Targets account ID (not OU) for single-account sandbox isolation |
| AWS provider alias `management` | ≥ 5.0 | Authenticates to management account | SCPs are Organizations API; only callable from management account |
| `aws_iam_policy_document` data source | ≥ 5.0 | Generates SCP JSON | Validates IAM JSON at plan time; use instead of inline heredoc |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `templatefile()` Terraform function | built-in | Substitute account IDs into SCP JSON | Used in `aws_iam_policy_document` alternative if cross-account ARN patterns are needed |
| Terragrunt `find_in_parent_folders` | 0.55+ | Inherit site.hcl accounts block | Consistent with all other Terragrunt units in this repo |

### Installation

No new Go dependencies. No new Terraform providers beyond `hashicorp/aws >= 5.0` (already required project-wide).

---

## Architecture Patterns

### Recommended Project Structure

```
infra/modules/scp/
└── v1.0.0/
    ├── main.tf          # aws_organizations_policy + aws_organizations_policy_attachment
    ├── variables.tf     # application_account_id, allowed_regions, trusted_role_arns
    └── outputs.tf       # policy_id, policy_arn

infra/live/management/   # NEW: Terragrunt unit running in management account context
└── scp/
    └── terragrunt.hcl   # sources scp module; inherits site.hcl for account IDs

internal/app/cmd/
└── bootstrap.go         # extend runBootstrap() to call terragrunt apply on management/scp
```

### Pattern 1: SCP Document Structure with Trusted-Role Carve-Outs

**What:** Each Deny statement in the SCP carries an `ArnNotLike` condition on `aws:PrincipalARN`. Roles matching any trusted pattern are exempt from the Deny.

**When to use:** All Deny statements that must allow km system roles to continue operating. Without the carve-out, km's own provisioner role would be blocked from creating VPCs and security groups.

**Key constraint:** SCPs do NOT support `NotPrincipal`. Use `Condition.ArnNotLike.aws:PrincipalARN` instead. As of September 2025, AWS Organizations supports full IAM language in SCPs (conditions in Allow statements, resource ARNs, wildcards at beginning/middle of Action strings, NotResource), but `NotPrincipal` remains unsupported.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "DenyNetworkEscape",
      "Effect": "Deny",
      "Action": [
        "ec2:CreateVpc",
        "ec2:CreateSubnet",
        "ec2:CreateRouteTable",
        "ec2:CreateRoute",
        "ec2:CreateInternetGateway",
        "ec2:AttachInternetGateway",
        "ec2:CreateEgressOnlyInternetGateway",
        "ec2:CreateNatGateway",
        "ec2:CreateVpcPeeringConnection",
        "ec2:AcceptVpcPeeringConnection",
        "ec2:CreateTransitGateway",
        "ec2:CreateTransitGatewayVpcAttachment"
      ],
      "Resource": "*",
      "Condition": {
        "ArnNotLike": {
          "aws:PrincipalARN": [
            "arn:aws:iam::ACCOUNT_ID:role/km-provisioner-*",
            "arn:aws:iam::ACCOUNT_ID:role/km-lifecycle-*"
          ]
        }
      }
    }
  ]
}
```

**The trusted role set for carve-outs, derived from existing module IAM role names:**

| Role Pattern | Source Module | Carve-out Needed For |
|---|---|---|
| `km-provisioner-*` | (phase 10 adds this convention) | All network + instance + IAM + SG creation |
| `km-lifecycle-*` | (phase 10 adds this convention) | Instance mutation, IAM role detach/reattach |
| `km-ttl-handler` | `infra/modules/ttl-handler/v1.0.0/` | No SCP carve-out needed (TTL doesn't create resources) |
| `km-ecs-spot-handler` | `infra/modules/ecs-spot-handler/v1.0.0/` | Instance mutation (ECS stop/start) |
| `km-budget-enforcer-*` | `infra/modules/budget-enforcer/v1.0.0/` | IAM DetachRolePolicy only |
| `km-budget-scheduler-*` | `infra/modules/budget-enforcer/v1.0.0/` | No direct resource access (invokes Lambda) |
| `km-ec2spot-ssm-*` | `infra/modules/ec2spot/v1.0.0/` | SSM access (operator shell sessions) |
| `km-ecs-task-*` | `infra/modules/ecs/v1.0.0/` | Sandbox workload role (NOT exempt from containment) |
| `km-ecs-exec-*` | `infra/modules/ecs/v1.0.0/` | ECS exec role |

**CRITICAL FINDING:** The current codebase has no `km-provisioner-*` or `km-lifecycle-*` roles. The provisioning today runs under the **operator's own AWS SSO credentials**. Phase 10 must either: (a) define a naming convention for the provisioner role that operators assume during `km create`, or (b) carve out the operator's SSO-assigned role ARN (too environment-specific). Option (a) is correct — the SCP module should accept a `trusted_role_arns` variable that operators populate from their km-config.yaml, defaulting to a pattern.

### Pattern 2: Terraform Provider for Management Account

**What:** The SCP module requires AWS API calls to `organizations.*`, which are only valid when authenticated to the management (root) account.

**When to use:** Any Terragrunt unit in `infra/live/management/` must use a provider configured with management account credentials.

```hcl
# infra/live/management/scp/terragrunt.hcl
locals {
  site = read_terragrunt_config(find_in_parent_folders("site.hcl")).locals.site
  accounts = read_terragrunt_config(find_in_parent_folders("site.hcl")).locals.accounts
}

generate "provider" {
  path      = "provider.tf"
  if_exists = "overwrite_terragrunt"
  contents  = <<-EOF
    provider "aws" {
      region = "us-east-1"  # Organizations is a global service, us-east-1
      assume_role {
        role_arn = "arn:aws:iam::${local.accounts.management}:role/km-org-admin"
      }
    }
  EOF
}

terraform {
  source = "${dirname(find_in_parent_folders("CLAUDE.md"))}/infra/modules/scp//v1.0.0"
}

inputs = {
  application_account_id = local.accounts.application
  allowed_regions        = [get_env("KM_REGION", "us-east-1")]
  trusted_role_arns      = [
    "arn:aws:iam::${local.accounts.application}:role/km-provisioner-*",
    "arn:aws:iam::${local.accounts.application}:role/km-lifecycle-*",
    "arn:aws:iam::${local.accounts.application}:role/km-ecs-spot-handler",
    "arn:aws:iam::${local.accounts.application}:role/km-ttl-handler",
    "arn:aws:iam::${local.accounts.application}:role/km-budget-enforcer-*",
    "arn:aws:iam::${local.accounts.application}:role/km-ec2spot-ssm-*",
  ]
}
```

### Pattern 3: Region Lock SCP — Double-Negative Pattern

**What:** Deny everything except global services outside allowed regions. Uses `NotAction` (global service exceptions) + `StringNotEquals` on `aws:RequestedRegion`.

**When to use:** This is the standard AWS-recommended region lock pattern. Must NOT deny global services (IAM, STS, Organizations, CloudFront, Route53, Support, etc.) because those have single us-east-1 endpoints.

```json
{
  "Sid": "DenyOutsideAllowedRegions",
  "Effect": "Deny",
  "NotAction": [
    "iam:*",
    "sts:*",
    "organizations:*",
    "support:*",
    "cloudfront:*",
    "route53:*",
    "route53domains:*",
    "budgets:*",
    "ce:*",
    "cur:*",
    "health:*",
    "trustedadvisor:*",
    "waf:*",
    "shield:*",
    "globalaccelerator:*",
    "networkmanager:*",
    "pricing:*",
    "s3:GetAccountPublic*",
    "s3:ListAllMyBuckets",
    "s3:PutAccountPublic*"
  ],
  "Resource": "*",
  "Condition": {
    "StringNotEquals": {
      "aws:RequestedRegion": ["${allowed_region_1}", "${allowed_region_2}"]
    }
  }
}
```

**Note:** The region lock SCP should NOT carry a `trusted_role_arns` carve-out. Region restriction should apply to all roles equally, including km system roles. The SCP module `allowed_regions` variable drives this.

### Pattern 4: Terraform Module Structure

```hcl
# infra/modules/scp/v1.0.0/main.tf

resource "aws_organizations_policy" "sandbox_containment" {
  name        = "km-sandbox-containment"
  description = "Org-level SCP preventing sandbox IAM role breakout from EC2/network/IAM"
  type        = "SERVICE_CONTROL_POLICY"
  content     = data.aws_iam_policy_document.sandbox_containment.json
  tags = {
    "km:component" = "scp"
    "km:managed"   = "true"
  }
}

resource "aws_organizations_policy_attachment" "sandbox_containment" {
  policy_id = aws_organizations_policy.sandbox_containment.id
  target_id = var.application_account_id  # account ID string, e.g. "123456789012"
}
```

**target_id** accepts an account ID (12-digit string), OU ID (`ou-xxxx-yyyy`), or root ID (`r-xxxx`). Use account ID for single-account attachment.

### Anti-Patterns to Avoid

- **Using `NotPrincipal` in SCPs:** Not supported. Always use `Condition.ArnNotLike.aws:PrincipalARN`.
- **Running the SCP Terraform unit from the application account:** `organizations:*` calls must originate from the management account or delegated administrator. Applying this module with application account credentials silently fails with AccessDenied.
- **Omitting global services from the region lock `NotAction` list:** IAM, STS, CloudFront, Route53 etc. are physically in us-east-1 — denying them breaks AWS SSO, Terraform state access, and certificate management across all accounts.
- **One SCP per control category:** AWS limits 5 SCPs per target. Combine all sandbox containment statements into one document.
- **Carving out `km-ecs-task-*` from containment Denies:** The ECS task role IS the sandbox workload role and should be fully contained. Do not add it to trusted_role_arns.
- **Blanket deny of `iam:*` without carve-outs:** Breaks Lambda execution roles that use `iam:PassRole` legitimately. Scope IAM denies to specific high-risk actions.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| SCP JSON generation | Hand-coded string template | `aws_iam_policy_document` data source | Validates JSON at plan time, handles escaping |
| Multi-account provider setup | Custom credential injection | Terraform `provider` alias with `assume_role` block | Built into AWS provider; handles STS token refresh |
| Policy attachment tracking | Custom tagging/tracking | `aws_organizations_policy_attachment` resource lifecycle | Terraform tracks attach/detach in state |
| SCP document combining | Multiple SCP resources | Single `aws_organizations_policy` with multiple Statements | 5 SCP-per-target limit; one document is the standard |

**Key insight:** SCP management is a thin Terraform layer around the Organizations API. The complexity is in the IAM JSON design (which actions to deny, which condition patterns to use), not in the Terraform resource itself.

---

## Common Pitfalls

### Pitfall 1: Applying the Module with Application Account Credentials

**What goes wrong:** `aws organizations create-policy` returns `AWSOrganizationsNotInUseException` or `AccessDeniedException` when called outside the management account. The error message is not always obvious.

**Why it happens:** AWS Organizations APIs are scoped to the management account. Running Terraform with application account credentials (the normal credential context for all other km Terragrunt units) fails.

**How to avoid:** The `infra/live/management/` Terragrunt unit must use an `assume_role` block pointing to a role in the management account (e.g., `km-org-admin`). This role must exist and must have `organizations:CreatePolicy`, `organizations:AttachPolicy`, `organizations:DeletePolicy` permissions.

**Warning signs:** Terraform plan succeeds but apply fails with `AccessDeniedException` on `CreatePolicy`.

### Pitfall 2: SCP Blocks km's Own Provisioner Before Carve-Outs Are Wired

**What goes wrong:** SCP is deployed first (correct), but the operator's SSO role that runs `km create` is not in the `trusted_role_arns` list. Every subsequent `km create` fails because the operator's role is denied VPC/SG creation.

**Why it happens:** The current codebase does not have a dedicated `km-provisioner-*` role. `km create` runs under the operator's SSO-assigned AdministratorAccess role, which is account-specific and not captured in km-config.yaml.

**How to avoid:** The SCP module's `trusted_role_arns` variable must include the operator's role ARN pattern. Provide a default that matches common SSO role naming (`arn:aws:iam::*:role/AWSReservedSSO_*_*`) AND a configurable override via `km-config.yaml` key `scp.trusted_role_arns`. The operator sets this to their actual provisioner role before running `km bootstrap`.

**Warning signs:** `km create` returns `UnauthorizedOperation` immediately after `km bootstrap` succeeds.

### Pitfall 3: Region Lock Breaks IAM and STS Operations

**What goes wrong:** After deploying the region lock SCP, `km create` fails because `sts:AssumeRole` or `iam:GetRole` calls are denied (they appear to come from us-east-1 regardless of configured region).

**Why it happens:** IAM and STS are global services with single endpoints in us-east-1. If the region lock SCP does not exempt them via `NotAction`, every IAM/STS call from any region is denied.

**How to avoid:** The region lock `NotAction` list must include at minimum: `iam:*`, `sts:*`, `organizations:*`, `support:*`, `cloudfront:*`, `route53:*`, `health:*`, `budgets:*`, `ce:*`, `trustedadvisor:*`, `waf:*`.

**Warning signs:** After deploying the region lock SCP, AWS Console login fails, or `aws sts get-caller-identity` returns AccessDenied.

### Pitfall 4: budget-enforcer IAM Scope Must Not Be Blocked

**What goes wrong:** The budget-enforcer Lambda does `iam:DetachRolePolicy` and `iam:AttachRolePolicy` on sandbox roles (to revoke Bedrock access when AI budget exhausted). The IAM escalation Deny in the SCP would block this.

**Why it happens:** The IAM Deny statement blocks `iam:DetachRolePolicy`, `iam:AttachRolePolicy` for all roles except trusted ones. budget-enforcer role pattern `km-budget-enforcer-*` must be in the IAM carve-out.

**How to avoid:** Include `km-budget-enforcer-*` in the `ArnNotLike` carve-out on the IAM escalation Deny statement. Also, scope that Deny to high-risk actions only (`CreateRole`, `PassRole`, `AssumeRole`), not to `DetachRolePolicy`/`AttachRolePolicy` — or give budget-enforcer its own carve-out.

**Warning signs:** Budget enforcement stops working: Bedrock calls continue after AI budget exhausted, and Lambda CloudWatch logs show `AccessDenied` on IAM calls.

### Pitfall 5: Organizations Deny Blocks km Itself

**What goes wrong:** The Organizations discovery Deny (blocking `organizations:*` for all roles) blocks the `km-org-admin` management account role from creating the SCP.

**Why it happens:** The region lock `NotAction` exempts `organizations:*` globally (it is a global service). The separate Organizations Deny only applies to the application account — it should block sandbox roles from calling `organizations:ListAccounts` to discover sibling accounts, but must not block the management account role.

**How to avoid:** The Organizations Deny statement is scoped to block in the application account only (the SCP is attached to that account). The management account role never has this SCP applied to it — SCPs are not self-enforcing on the management account. This is correct by design. No carve-out needed in the application account SCP for the management account role.

---

## Code Examples

### Full SCP Statement Set

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "DenySGMutation",
      "Effect": "Deny",
      "Action": [
        "ec2:CreateSecurityGroup",
        "ec2:DeleteSecurityGroup",
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:AuthorizeSecurityGroupEgress",
        "ec2:RevokeSecurityGroupIngress",
        "ec2:RevokeSecurityGroupEgress",
        "ec2:ModifySecurityGroupRules",
        "ec2:UpdateSecurityGroupRuleDescriptionsIngress",
        "ec2:UpdateSecurityGroupRuleDescriptionsEgress"
      ],
      "Resource": "*",
      "Condition": {
        "ArnNotLike": {
          "aws:PrincipalARN": "${trusted_role_arns}"
        }
      }
    },
    {
      "Sid": "DenyNetworkEscape",
      "Effect": "Deny",
      "Action": [
        "ec2:CreateVpc",
        "ec2:CreateSubnet",
        "ec2:CreateRouteTable",
        "ec2:CreateRoute",
        "ec2:CreateInternetGateway",
        "ec2:AttachInternetGateway",
        "ec2:CreateEgressOnlyInternetGateway",
        "ec2:CreateNatGateway",
        "ec2:CreateVpcPeeringConnection",
        "ec2:AcceptVpcPeeringConnection",
        "ec2:CreateTransitGateway",
        "ec2:CreateTransitGatewayVpcAttachment"
      ],
      "Resource": "*",
      "Condition": {
        "ArnNotLike": {
          "aws:PrincipalARN": "${trusted_role_arns}"
        }
      }
    },
    {
      "Sid": "DenyInstanceMutation",
      "Effect": "Deny",
      "Action": [
        "ec2:RunInstances",
        "ec2:ModifyInstanceAttribute",
        "ec2:ModifyInstanceMetadataOptions"
      ],
      "Resource": "*",
      "Condition": {
        "ArnNotLike": {
          "aws:PrincipalARN": "${trusted_role_arns}"
        }
      }
    },
    {
      "Sid": "DenyIAMEscalation",
      "Effect": "Deny",
      "Action": [
        "iam:CreateRole",
        "iam:AttachRolePolicy",
        "iam:PassRole",
        "iam:AssumeRole"
      ],
      "Resource": "*",
      "Condition": {
        "ArnNotLike": {
          "aws:PrincipalARN": "${trusted_role_arns_including_budget_enforcer}"
        }
      }
    },
    {
      "Sid": "DenyStorageExfiltration",
      "Effect": "Deny",
      "Action": [
        "ec2:CreateSnapshot",
        "ec2:CopySnapshot",
        "ec2:CreateImage",
        "ec2:CopyImage",
        "ec2:ExportImage"
      ],
      "Resource": "*",
      "Condition": {
        "ArnNotLike": {
          "aws:PrincipalARN": "${trusted_role_arns}"
        }
      }
    },
    {
      "Sid": "DenySSMPivot",
      "Effect": "Deny",
      "Action": [
        "ssm:SendCommand",
        "ssm:StartSession"
      ],
      "Resource": "*",
      "Condition": {
        "ArnNotLike": {
          "aws:PrincipalARN": "${operator_and_km_ec2spot_ssm_arns}"
        }
      }
    },
    {
      "Sid": "DenyOrganizationsDiscovery",
      "Effect": "Deny",
      "Action": [
        "organizations:ListAccounts",
        "organizations:DescribeOrganization",
        "organizations:ListRoots",
        "organizations:ListOrganizationalUnitsForParent",
        "organizations:ListChildren"
      ],
      "Resource": "*"
    }
  ]
}
```

### Terraform Module: main.tf Skeleton

```hcl
# Source: aws_organizations_policy Terraform resource (hashicorp/aws >= 5.0)
# Confidence: HIGH — standard resource, project already uses aws provider >= 5.0

locals {
  trusted_arns_base = concat(var.trusted_role_arns, [
    "arn:aws:iam::${var.application_account_id}:role/AWSReservedSSO_*_*"
  ])

  trusted_arns_iam = concat(local.trusted_arns_base, [
    "arn:aws:iam::${var.application_account_id}:role/km-budget-enforcer-*"
  ])

  trusted_arns_ssm = [
    "arn:aws:iam::${var.application_account_id}:role/km-ec2spot-ssm-*",
    "arn:aws:iam::${var.application_account_id}:role/AWSReservedSSO_*_*"
  ]
}

resource "aws_organizations_policy" "sandbox_containment" {
  name        = "km-sandbox-containment"
  description = "Org-level backstop preventing sandbox role EC2/network/IAM breakout"
  type        = "SERVICE_CONTROL_POLICY"
  content     = data.aws_iam_policy_document.sandbox_containment.json

  tags = {
    "km:component" = "scp"
    "km:managed"   = "true"
  }
}

resource "aws_organizations_policy_attachment" "sandbox_containment" {
  policy_id = aws_organizations_policy.sandbox_containment.id
  target_id = var.application_account_id
}
```

### bootstrap.go Extension Pattern

The `runBootstrap()` function in `internal/app/cmd/bootstrap.go` currently only prints a dry-run plan. Phase 10 extends it to call terragrunt apply on the SCP module when `--dry-run=false`.

```go
// Pattern: parallel to how km create calls pkg/terragrunt.RunnerApply
// Source: existing cmd/create.go pattern (Phase 02 decisions)
if !dryRun {
    runner := terragrunt.NewRunner(cfg, scpDir)
    if err := runner.Apply(ctx); err != nil {
        return fmt.Errorf("scp bootstrap: %w", err)
    }
    fmt.Println("SCP sandbox-containment policy deployed to application account.")
}
```

The SCP Terragrunt unit directory (`infra/live/management/scp/`) is pre-generated at repo init time — it is not a per-sandbox artifact. The bootstrap command resolves its path relative to the repo root via `findRepoRoot()` (same pattern as other commands).

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|---|---|---|---|
| SCPs support only `Deny` with `Action:"*"` patterns | Full IAM language: conditions in Allow, resource ARNs, `NotResource`, wildcards in middle of actions | September 2025 | ArnNotLike carve-outs are fully supported with all AWS regions |
| `NotPrincipal` in SCPs | Not supported (never was); use `Condition.ArnNotLike` | Always | ArnNotLike is the correct pattern — not a workaround |
| 5 SCP limit per target | Still 5 per target (account or OU) | Unchanged | One combined SCP document is the correct approach |
| Management account is exempt from SCPs it issues | Still exempt | Unchanged | No need to carve out management account role from application account SCP |

**Deprecated/outdated:**
- Using `Principal` or `NotPrincipal` elements in SCPs: never supported, do not attempt.
- Separate SCPs per control category (one for network, one for IAM, etc.): hits the 5-SCP-per-target limit quickly. Combine into one document.

---

## Open Questions

1. **Operator SSO role ARN pattern**
   - What we know: `km create` today runs under the operator's AWS SSO role. The role ARN pattern is `arn:aws:iam::ACCOUNT:role/AWSReservedSSO_AdministratorAccess_RANDOMHEX` — account-specific and unpredictable without configuration.
   - What's unclear: Should the SCP carve-out default to `AWSReservedSSO_*` (broad but safe for a sandbox platform), or require the operator to explicitly configure their role ARN in km-config.yaml?
   - Recommendation: Default to `arn:aws:iam::*:role/AWSReservedSSO_*_*` as the provisioner carve-out, plus allow `km-config.yaml` key `scp.extra_trusted_arns` for explicit additions. Document this clearly in the bootstrap help text.

2. **management account role for SCP deployment**
   - What we know: The Terragrunt unit needs to assume a role in the management account that has `organizations:CreatePolicy`, `organizations:AttachPolicy`, `organizations:DeletePolicy`, `organizations:DescribePolicy`, `organizations:UpdatePolicy`.
   - What's unclear: Does this role (`km-org-admin`) need to be pre-created by the operator manually before `km bootstrap` can work? If so, the bootstrap command cannot provision it via Terraform (chicken-and-egg).
   - Recommendation: Document as a pre-requisite. The operator manually creates `km-org-admin` in the management account with the minimum Organizations permissions, then `km bootstrap` uses it. Alternative: `km bootstrap --provision-admin-role` flag that creates this role using the operator's current credentials, assuming they are already a management account admin.

3. **SCP on OU vs. account**
   - What we know: Attaching to account ID is more targeted. Attaching to an OU protects all accounts in the OU automatically.
   - What's unclear: The project multi-account design is management + terraform + application. Whether these are all under one OU is operator-configurable.
   - Recommendation: Attach to `application_account_id` specifically. This is safe, precise, and does not require OU knowledge.

---

## IAM Role Inventory (Actual from Codebase)

All role names confirmed from `infra/modules/*/v1.0.0/main.tf`:

| Role Name (actual) | Module | Category |
|---|---|---|
| `km-budget-enforcer-{sandbox_id}` | budget-enforcer | budget enforcement Lambda |
| `km-budget-scheduler-{sandbox_id}` | budget-enforcer | EventBridge Scheduler |
| `km-ecs-task-{sandbox_id}-{region}` | ecs | ECS task execution (sandbox workload) |
| `km-ecs-exec-{sandbox_id}-{region}` | ecs | ECS exec role |
| `km-ecs-spot-handler` | ecs-spot-handler | spot interruption Lambda |
| `km-ec2spot-ssm-{sandbox_id}-{region}` | ec2spot | EC2 instance profile (SSM access) |
| `km-ttl-handler` | ttl-handler | TTL teardown Lambda |
| `km-s3-replication-{bucket}` | s3-replication | S3 replication role |

**Missing patterns for SCP carve-out:** There are no `km-provisioner-*` or `km-lifecycle-*` roles. The SCP must carve out operator SSO roles (`AWSReservedSSO_*`) as the provisioner identity until dedicated provisioner roles are introduced.

**budget-enforcer scoping requirement (from phase description):** The SCP must ensure the budget-enforcer Lambda can only modify sandbox roles (`km-ec2spot-ssm-*`, `km-ecs-task-*`), not arbitrary IAM resources. This is NOT an SCP concern — the budget-enforcer module's IAM policy already scopes `iam:DetachRolePolicy` to `var.role_arn` (a specific sandbox role ARN). The SCP's role is to prevent sandbox roles from escalating — not to limit what budget-enforcer can do. The `km-budget-enforcer-*` role needs carve-out from the IAM escalation Deny so it can continue to call DetachRolePolicy/AttachRolePolicy.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib), existing test suite |
| Config file | `go test ./...` from repo root |
| Quick run command | `go test ./infra/modules/scp/... ./internal/app/cmd/... -run TestBootstrap -count=1` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map

No formal requirement IDs were assigned to Phase 10. Testing strategy maps to the stated behaviors:

| Behavior | Test Type | Automated Command | File Exists? |
|---|---|---|---|
| SCP JSON document is valid IAM JSON | Unit (Go) | `go test ./infra/modules/scp/... -run TestSCPDocumentValid` | No — Wave 0 |
| Trusted role arns are injected correctly | Unit (Terraform plan) | `terraform plan` in test fixture | No — Wave 0 |
| bootstrap --dry-run prints SCP intent | Unit (Go) | `go test ./internal/app/cmd/... -run TestBootstrapDryRun` | No — Wave 0 |
| bootstrap (non-dry-run) calls terragrunt apply | Unit (Go, mock) | `go test ./internal/app/cmd/... -run TestBootstrapApply` | No — Wave 0 |
| Region lock NotAction excludes global services | Manual review | N/A | Manual-only |
| SCP does not block km-budget-enforcer IAM calls | Integration (manual) | `km create` + budget event after bootstrap | Manual-only |

**Note:** Terraform/SCP correctness cannot be fully automated without a real AWS Organizations account. Unit tests should focus on: correct JSON generation from module variables, correct bootstrap Go code wiring. Integration validation requires a real management account.

### Wave 0 Gaps

- [ ] `infra/modules/scp/v1.0.0/main.tf` — module does not exist yet
- [ ] `infra/modules/scp/v1.0.0/variables.tf` — variables: application_account_id, allowed_regions, trusted_role_arns
- [ ] `infra/modules/scp/v1.0.0/outputs.tf` — policy_id, policy_arn
- [ ] `infra/live/management/scp/terragrunt.hcl` — Terragrunt unit (management account context)
- [ ] bootstrap.go extension — non-dry-run SCP apply path
- [ ] Go unit tests for bootstrap SCP wiring

---

## Sources

### Primary (HIGH confidence)

- AWS What's New September 2025 — full IAM language in SCPs: [https://aws.amazon.com/about-aws/whats-new/2025/09/aws-organizations-iam-language-service-control-policies/](https://aws.amazon.com/about-aws/whats-new/2025/09/aws-organizations-iam-language-service-control-policies/)
- Terraform Registry `aws_organizations_policy` — [https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/organizations_policy](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/organizations_policy)
- AWS SCP syntax documentation — [https://docs.aws.amazon.com/organizations/latest/userguide/orgs_manage_policies_scps_syntax.html](https://docs.aws.amazon.com/organizations/latest/userguide/orgs_manage_policies_scps_syntax.html)
- Existing codebase IAM roles: `infra/modules/*/v1.0.0/main.tf` (all role names verified directly)
- Existing `internal/app/cmd/bootstrap.go` — confirmed dry-run stub, no real provisioning
- Existing `internal/app/config/config.go` — confirmed ManagementAccountID field already in Config struct

### Secondary (MEDIUM confidence)

- Region lock double-negative SCP pattern with global service list — securosis.com blog (verified against known AWS behavior): [https://slaw.securosis.com/p/notwhat-lock-regions-double-negative-scp](https://slaw.securosis.com/p/notwhat-lock-regions-double-negative-scp)
- ArnNotLike carve-out pattern for SCP trusted roles — amirmalik.net SCP starter pack (consistent with AWS docs): [https://amirmalik.net/2025/02/04/aws-scp-starter-pack](https://amirmalik.net/2025/02/04/aws-scp-starter-pack)
- aws-samples terraform-aws-organization-policies module — GitHub: [https://github.com/aws-samples/aws-scps-with-terraform](https://github.com/aws-samples/aws-scps-with-terraform)
- AWS SCP guardrail reference: [https://aws-samples.github.io/aws-iam-permissions-guardrails/guardrails/scp-guardrails.html](https://aws-samples.github.io/aws-iam-permissions-guardrails/guardrails/scp-guardrails.html)

### Tertiary (LOW confidence)

- Community SCP examples for SSM pivot prevention (ArnNotLike pattern) — amirmalik.net (single-source, not independently verified in AWS docs): pattern is consistent with documented ArnNotLike behavior.

---

## Metadata

**Confidence breakdown:**
- Standard stack (Terraform resources): HIGH — `aws_organizations_policy` and `aws_organizations_policy_attachment` are stable, documented Terraform resources; project already uses aws provider >= 5.0
- SCP JSON patterns: HIGH — ArnNotLike carve-out pattern confirmed from multiple sources; region lock NotAction list from authoritative securosis breakdown; September 2025 full IAM language support confirmed from AWS What's New
- Architecture (management account provider): HIGH — standard Terraform assume_role pattern; Organizations API management account restriction is AWS-documented behavior
- Pitfalls: HIGH — all four major pitfalls derived from first-principles analysis of the actual codebase (no km-provisioner role exists; budget-enforcer needs IAM carve-out; region lock global service list)
- bootstrap.go wiring: HIGH — existing bootstrap.go stub and cmd patterns clearly show extension points

**Research date:** 2026-03-22
**Valid until:** 2026-09-22 (SCP API is stable; the full IAM language feature landed Sept 2025 so the ArnNotLike patterns are freshly verified)
