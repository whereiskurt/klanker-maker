# ============================================================
# SCP: Org-level sandbox containment backstop (v2.0.0)
#
# Purpose: Prevents sandbox role EC2/network/IAM breakout at
# the AWS Organizations layer, independent of IAM role policy.
# Even a misconfigured sandbox role cannot escape these denies.
#
# Phase 84.4: All hardcoded "km-" literals replaced by
# "${var.resource_prefix}-" so multiple installs in the same
# AWS account/Organization each guard their OWN role names.
#
# Phase 84.4.1: SCP cross-install composition — trusted ARN slots
# now use *-* suffix patterns (account + prefix both wildcarded)
# so the canonical km install's SCP also trusts rg-, tg-, etc.
# roles without requiring per-install policy-body editing.
#
# Security trade-off: operator-only IAM:CreateRole in the
# application account is the primary guard; SCP is defense-in-
# depth. Deployed roles still need cross-account assume-role
# grants. Documented in OPERATOR-GUIDE.md (Phase 84.4.1-06).
# ============================================================

locals {
  # Base trusted ARNs passed by the operator (SSO + provisioner/lifecycle roles).
  # Phase 84.4.1: trusted_arns_base is passed via var.trusted_role_arns and uses
  # *-* patterns at the caller (infra/live/management/scp/terragrunt.hcl).
  trusted_arns_base = var.trusted_role_arns

  # Instance mutation carve-out: base roles + spot handler (which launches instances).
  # Phase 84.4.1: account + prefix both wildcarded — trusts any install's spot handler.
  trusted_arns_instance = concat(
    local.trusted_arns_base,
    ["arn:aws:iam::*:role/*-ecs-spot-handler"]
  )

  # IAM escalation carve-out: base roles + budget-enforcer (needs AttachRolePolicy/
  # DetachRolePolicy for Bedrock IAM revocation on budget breach).
  # Phase 84.4.1: account + prefix both wildcarded.
  trusted_arns_iam = concat(
    local.trusted_arns_base,
    ["arn:aws:iam::*:role/*-budget-enforcer-*"]
  )

  # SSM pivot carve-out: only SSM instance roles and operator SSO — NOT the full
  # trusted_arns_base. Intentionally more restrictive: only roles that legitimately
  # use SSM for instance access.
  # {prefix}-github-token-refresher-* only needs SSM GetParameter/PutParameter.
  # Phase 84.4.1: account + prefix both wildcarded across all SSM carve-out roles.
  trusted_arns_ssm = [
    "arn:aws:iam::*:role/*-ec2spot-ssm-*",
    "arn:aws:iam::*:role/*-github-token-refresher-*",
    "arn:aws:iam::*:role/*-ttl-handler",
    "arn:aws:iam::*:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*",
  ]
}

# ============================================================
# SCP policy document — 5 deny statements (consolidated to fit
# 5,120-byte SCP limit with full SSO role path ARNs)
# ============================================================

data "aws_iam_policy_document" "sandbox_containment" {

  # 1. Deny SG mutation, network escape, and storage exfiltration for non-trusted roles.
  #    Consolidated from 3 statements — all use trusted_arns_base condition.
  statement {
    sid    = "DenyInfraAndStorage"
    effect = "Deny"

    actions = [
      # SG mutation
      "ec2:CreateSecurityGroup",
      "ec2:DeleteSecurityGroup",
      "ec2:AuthorizeSecurityGroup*",
      "ec2:RevokeSecurityGroup*",
      "ec2:ModifySecurityGroupRules",
      # Network escape
      "ec2:CreateVpc",
      "ec2:CreateSubnet",
      "ec2:CreateRouteTable",
      "ec2:CreateRoute",
      "ec2:*InternetGateway",
      "ec2:CreateNatGateway",
      "ec2:*VpcPeeringConnection",
      "ec2:CreateTransitGateway*",
      # Storage exfiltration
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

  # 2. Deny instance mutation for non-trusted roles + spot handler exception.
  #    {prefix}-ecs-spot-handler launches Spot instances as part of normal platform operation.
  #    {prefix}-ecs-task-* is intentionally NOT carved out — that IS the sandbox workload.
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

  # 3. Deny IAM escalation for non-trusted roles + budget-enforcer exception.
  #    {prefix}-budget-enforcer-* needs AttachRolePolicy/DetachRolePolicy to revoke
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

  # 4. Deny SSM cross-instance pivoting + Organizations discovery.
  #    Consolidated: SSM pivot uses restricted SSM roles; Org discovery has no condition
  #    (applies to ALL roles — management account exempt by AWS design).
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

  statement {
    sid    = "DenyOrgDiscovery"
    effect = "Deny"

    actions = [
      "organizations:List*",
      "organizations:Describe*",
    ]

    resources = ["*"]
  }

  # 5. Region lock — deny all actions outside allowed regions, except global services.
  #    Uses not_actions (NotAction) pattern so global services work regardless of region.
  #    Trusted roles (operators, provisioner, lifecycle) are exempt so they can create
  #    cross-region resources like S3 replication buckets.
  statement {
    sid    = "DenyOutsideRegion"
    effect = "Deny"

    not_actions = [
      "iam:*",
      "sts:*",
      "organizations:*",
      "support:*",
      "health:*",
      "trustedadvisor:*",
      "cloudfront:*",
      "waf:*",
      "shield:*",
      "route53:*",
      "route53domains:*",
      "budgets:*",
      "ce:*",
      "cur:*",
      "globalaccelerator:*",
      "networkmanager:*",
      "pricing:*",
      "bedrock:*",
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

    # Trusted roles can operate cross-region (e.g. S3 replication to us-west-2).
    condition {
      test     = "ArnNotLike"
      variable = "aws:PrincipalArn"
      values   = var.trusted_role_arns
    }
  }
}

# ============================================================
# SCP resource — org-level SERVICE_CONTROL_POLICY
# ============================================================

resource "aws_organizations_policy" "sandbox_containment" {
  name        = "${var.resource_prefix}-sandbox-containment"
  description = "Org-level backstop preventing sandbox role EC2/network/IAM breakout"
  type        = "SERVICE_CONTROL_POLICY"
  content     = data.aws_iam_policy_document.sandbox_containment.json

  tags = {
    "km:component"       = "scp"
    "km:managed"         = "true"
    "km:resource-prefix" = var.resource_prefix
  }
}

# ============================================================
# SCP attachment — attach to the application account
# ============================================================

resource "aws_organizations_policy_attachment" "sandbox_containment" {
  policy_id = aws_organizations_policy.sandbox_containment.id
  target_id = var.application_account_id
}

# ============================================================
# 5,120-byte SCP policy size guard (Phase 84.4).
# AWS hard limit is 5,120 bytes; we use 5,000 as the safety
# threshold (120-byte buffer). Trips at plan-time, before AWS
# rejects the apply with a cryptic SizeExceeded error.
# ============================================================

resource "terraform_data" "scp_size_guard" {
  lifecycle {
    precondition {
      condition     = length(data.aws_iam_policy_document.sandbox_containment.json) <= 5000
      error_message = "SCP policy is ${length(data.aws_iam_policy_document.sandbox_containment.json)} bytes — exceeds 5,000-byte safety threshold (AWS hard limit is 5,120). Remove a trusted ARN or consolidate statements. Consider whether the new addition justifies bumping the threshold."
    }
  }
}
