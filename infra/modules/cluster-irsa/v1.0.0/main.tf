locals {
  oidc_provider_host = replace(var.oidc_provider_arn, "/^arn:aws:iam::[0-9]+:oidc-provider\\//", "")
  has_wildcard       = can(regex("\\*", var.namespace)) || can(regex("\\*", var.service_account_name))
  sub_condition      = local.has_wildcard ? "StringLike" : "StringEquals"
}

data "aws_iam_policy_document" "trust" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [var.oidc_provider_arn]
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
