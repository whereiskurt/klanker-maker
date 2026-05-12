locals {
  oidc_provider_host = replace(var.oidc_provider_arn, "/^arn:aws:iam::[0-9]+:oidc-provider\\//", "")
  oidc_provider_url  = "https://${local.oidc_provider_host}"
  has_wildcard       = can(regex("\\*", var.namespace)) || can(regex("\\*", var.service_account_name))
  sub_condition      = local.has_wildcard ? "StringLike" : "StringEquals"
}

# Fetch the cluster's OIDC discovery endpoint TLS certificate. AWS STS requires the
# OIDC provider to be registered in the SAME account as the IAM role, so we mirror
# the remote cluster's issuer URL into a local provider here. var.oidc_provider_arn
# names the remote provider only to derive its URL — it is not used as the trust
# Principal.
data "tls_certificate" "oidc" {
  url = local.oidc_provider_url
}

resource "aws_iam_openid_connect_provider" "this" {
  url             = local.oidc_provider_url
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.oidc.certificates[0].sha1_fingerprint]

  tags = {
    "km:cluster" = var.cluster_name
    "km:manager" = "km-cluster"
  }
}

data "aws_iam_policy_document" "trust" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.this.arn]
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
