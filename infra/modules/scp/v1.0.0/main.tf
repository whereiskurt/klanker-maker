# ============================================================
# SCP: Org-level sandbox containment backstop
#
# Purpose: Prevents sandbox role EC2/network/IAM breakout at
# the AWS Organizations layer, independent of IAM role policy.
# Even a misconfigured sandbox role cannot escape these denies.
# ============================================================

locals {
  # Base trusted ARNs passed by the operator (SSO + future provisioner/lifecycle roles)
  trusted_arns_base = var.trusted_role_arns

  # Instance mutation carve-out: base roles + spot handler (which launches instances)
  trusted_arns_instance = concat(
    local.trusted_arns_base,
    ["arn:aws:iam::${var.application_account_id}:role/km-ecs-spot-handler"]
  )

  # IAM escalation carve-out: base roles + budget-enforcer (needs AttachRolePolicy/DetachRolePolicy
  # for Bedrock IAM revocation on budget breach)
  trusted_arns_iam = concat(
    local.trusted_arns_base,
    ["arn:aws:iam::${var.application_account_id}:role/km-budget-enforcer-*"]
  )

  # SSM pivot carve-out: only SSM instance roles and operator SSO — NOT the full trusted_arns_base.
  # This is intentionally more restrictive: only roles that legitimately use SSM for instance access.
  # km-github-token-refresher-* added here (not base/instance/iam) — it only needs SSM GetParameter/PutParameter.
  trusted_arns_ssm = [
    "arn:aws:iam::${var.application_account_id}:role/km-ec2spot-ssm-*",
    "arn:aws:iam::${var.application_account_id}:role/km-github-token-refresher-*",
    "arn:aws:iam::*:role/AWSReservedSSO_*_*",
  ]
}

# ============================================================
# SCP policy document — 8 deny statements
# ============================================================

data "aws_iam_policy_document" "sandbox_containment" {

  # 1. Deny security group mutation for non-trusted roles
  statement {
    sid    = "DenySGMutation"
    effect = "Deny"

    actions = [
      "ec2:CreateSecurityGroup",
      "ec2:DeleteSecurityGroup",
      "ec2:AuthorizeSecurityGroupIngress",
      "ec2:AuthorizeSecurityGroupEgress",
      "ec2:RevokeSecurityGroupIngress",
      "ec2:RevokeSecurityGroupEgress",
      "ec2:ModifySecurityGroupRules",
      "ec2:UpdateSecurityGroupRuleDescriptionsIngress",
      "ec2:UpdateSecurityGroupRuleDescriptionsEgress",
    ]

    resources = ["*"]

    condition {
      test     = "ArnNotLike"
      variable = "aws:PrincipalARN"
      values   = local.trusted_arns_base
    }
  }

  # 2. Deny network escape (VPC, subnet, route, IGW, NAT, peering, TGW) for non-trusted roles
  statement {
    sid    = "DenyNetworkEscape"
    effect = "Deny"

    actions = [
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
      "ec2:CreateTransitGatewayVpcAttachment",
    ]

    resources = ["*"]

    condition {
      test     = "ArnNotLike"
      variable = "aws:PrincipalARN"
      values   = local.trusted_arns_base
    }
  }

  # 3. Deny instance mutation for non-trusted roles + spot handler exception
  #    km-ecs-spot-handler launches Spot instances as part of normal platform operation.
  #    km-ecs-task-* is intentionally NOT carved out — that IS the sandbox workload.
  statement {
    sid    = "DenyInstanceMutation"
    effect = "Deny"

    actions = [
      "ec2:RunInstances",
      "ec2:ModifyInstanceAttribute",
      "ec2:ModifyInstanceMetadataOptions",
    ]

    resources = ["*"]

    condition {
      test     = "ArnNotLike"
      variable = "aws:PrincipalARN"
      values   = local.trusted_arns_instance
    }
  }

  # 4. Deny IAM escalation for non-trusted roles + budget-enforcer exception
  #    km-budget-enforcer-* needs AttachRolePolicy/DetachRolePolicy to revoke
  #    Bedrock access on budget breach. It does NOT need CreateRole or PassRole.
  statement {
    sid    = "DenyIAMEscalation"
    effect = "Deny"

    actions = [
      "iam:CreateRole",
      "iam:AttachRolePolicy",
      "iam:DetachRolePolicy",
      "iam:PassRole",
      "iam:AssumeRole",
    ]

    resources = ["*"]

    condition {
      test     = "ArnNotLike"
      variable = "aws:PrincipalARN"
      values   = local.trusted_arns_iam
    }
  }

  # 5. Deny storage exfiltration (snapshots, images, export) for non-trusted roles
  statement {
    sid    = "DenyStorageExfiltration"
    effect = "Deny"

    actions = [
      "ec2:CreateSnapshot",
      "ec2:CopySnapshot",
      "ec2:CreateImage",
      "ec2:CopyImage",
      "ec2:ExportImage",
    ]

    resources = ["*"]

    condition {
      test     = "ArnNotLike"
      variable = "aws:PrincipalARN"
      values   = local.trusted_arns_base
    }
  }

  # 6. Deny SSM cross-instance pivoting — only SSM instance roles and operator SSO can use SSM
  #    This prevents a compromised sandbox from using SSM to pivot to other instances.
  statement {
    sid    = "DenySSMPivot"
    effect = "Deny"

    actions = [
      "ssm:SendCommand",
      "ssm:StartSession",
    ]

    resources = ["*"]

    condition {
      test     = "ArnNotLike"
      variable = "aws:PrincipalARN"
      values   = local.trusted_arns_ssm
    }
  }

  # 7. Deny Organizations account/structure discovery — NO condition, applies to ALL roles.
  #    The management account itself is exempt by AWS design (SCPs don't apply there).
  #    Application account roles should never need to enumerate the org structure.
  statement {
    sid    = "DenyOrganizationsDiscovery"
    effect = "Deny"

    actions = [
      "organizations:ListAccounts",
      "organizations:DescribeOrganization",
      "organizations:ListRoots",
      "organizations:ListOrganizationalUnitsForParent",
      "organizations:ListChildren",
    ]

    resources = ["*"]
  }

  # 8. Region lock — deny all actions outside allowed regions, except global services.
  #    Uses not_actions (NotAction) pattern so global services work regardless of region.
  #    NO trusted role carve-out — region lock applies to ALL roles including operators.
  statement {
    sid    = "DenyOutsideAllowedRegions"
    effect = "Deny"

    not_actions = [
      # IAM and STS are global
      "iam:*",
      "sts:*",
      # Organizations (global control plane)
      "organizations:*",
      # AWS Support and Health
      "support:*",
      "health:*",
      "trustedadvisor:*",
      # CloudFront, WAF, Shield (edge services, us-east-1 only in API but global in scope)
      "cloudfront:*",
      "waf:*",
      "shield:*",
      # Route 53 (global DNS)
      "route53:*",
      "route53domains:*",
      # Billing and cost management (global)
      "budgets:*",
      "ce:*",
      "cur:*",
      # Global Accelerator and Network Manager
      "globalaccelerator:*",
      "networkmanager:*",
      # AWS Marketplace pricing
      "pricing:*",
      # S3 account-level settings (not bucket operations — those are regional)
      "s3:GetAccountPublicAccessBlock",
      "s3:ListAllMyBuckets",
      "s3:PutAccountPublicAccessBlock",
    ]

    resources = ["*"]

    condition {
      test     = "StringNotEquals"
      variable = "aws:RequestedRegion"
      values   = var.allowed_regions
    }
  }
}

# ============================================================
# SCP resource — org-level SERVICE_CONTROL_POLICY
# ============================================================

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

# ============================================================
# SCP attachment — attach to the application account
# ============================================================

resource "aws_organizations_policy_attachment" "sandbox_containment" {
  policy_id = aws_organizations_policy.sandbox_containment.id
  target_id = var.application_account_id
}
