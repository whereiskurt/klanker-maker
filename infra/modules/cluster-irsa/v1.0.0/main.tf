# moved {} block MUST be first — before locals — so Terraform processes state migration
# before evaluating any resource. This is the backward-compat path for Phase 80 stacks
# that have aws_iam_openid_connect_provider.this (unindexed) in state.
# After the first successful apply of the new module, this block is a no-op.
moved {
  from = aws_iam_openid_connect_provider.this
  to   = aws_iam_openid_connect_provider.this[0]
}

locals {
  oidc_provider_host = replace(var.oidc_provider_arn, "/^arn:aws:iam::[0-9]+:oidc-provider\\//", "")
  oidc_provider_url  = "https://${local.oidc_provider_host}"
  has_wildcard       = can(regex("\\*", var.namespace)) || can(regex("\\*", var.service_account_name))
  sub_condition      = local.has_wildcard ? "StringLike" : "StringEquals"

  # oidc_provider_arn_local selects the ARN from whichever branch owns the provider.
  # When register_oidc_provider=true: the resource we created.
  # When register_oidc_provider=false: the existing provider we looked up via data source.
  oidc_provider_arn_local = var.register_oidc_provider ? (
    aws_iam_openid_connect_provider.this[0].arn
  ) : (
    data.aws_iam_openid_connect_provider.existing[0].arn
  )
}

# Fetch the cluster's OIDC discovery endpoint TLS certificate.
# Only evaluated when we are creating a new provider (register_oidc_provider=true).
data "tls_certificate" "oidc" {
  count = var.register_oidc_provider ? 1 : 0
  url   = local.oidc_provider_url
}

# Create a local OIDC provider mirror — only when register_oidc_provider=true.
# AWS STS requires the OIDC provider to be registered in the SAME account as the IAM role.
resource "aws_iam_openid_connect_provider" "this" {
  count           = var.register_oidc_provider ? 1 : 0
  url             = local.oidc_provider_url
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.oidc[0].certificates[0].sha1_fingerprint]

  tags = {
    "km:cluster" = var.cluster_name
    "km:manager" = "km-cluster"
  }
}

# Reference an existing OIDC provider — only when register_oidc_provider=false.
# Terraform evaluates data sources with count=0 as absent (no API calls made).
# The url argument is always local.oidc_provider_url (a well-formed non-null string),
# which avoids the Terraform 1.6.x count=0 data source null-interpolation regression.
data "aws_iam_openid_connect_provider" "existing" {
  count = var.register_oidc_provider ? 0 : 1
  url   = local.oidc_provider_url
}

data "aws_iam_policy_document" "trust" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [local.oidc_provider_arn_local]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_provider_host}:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = local.sub_condition
      variable = "${local.oidc_provider_host}:sub"
      values   = ["system:serviceaccount:${var.namespace}:${var.service_account_name}"]
    }
  }
}

resource "aws_iam_role" "cluster_irsa" {
  name                 = "${var.resource_prefix}-cluster-${var.cluster_name}"
  assume_role_policy   = data.aws_iam_policy_document.trust.json
  max_session_duration = 3600

  tags = {
    "km:cluster" = var.cluster_name
    "km:manager" = "km-cluster"
  }
}

module "km_operator_policy" {
  source = "../../km-operator-policy/v1.0.0"

  role_id                   = aws_iam_role.cluster_irsa.id
  resource_prefix           = var.resource_prefix
  artifact_bucket_arn       = var.artifact_bucket_arn
  state_bucket              = var.state_bucket
  dynamodb_table_name       = var.dynamodb_table_name
  dynamodb_budget_table_arn = var.dynamodb_budget_table_arn
  sandbox_table_name        = var.sandbox_table_name
  identities_table_name     = var.identities_table_name
}
