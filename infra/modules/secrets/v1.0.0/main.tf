data "aws_caller_identity" "current" {}

locals {
  # Variable substitutions for SSM path prefix
  ssm_prefix = replace(
    replace(
      replace(
        var.ssm_prefix_template,
        "{{KM_LABEL}}", var.km_label
      ),
      "{{REGION_LABEL}}", var.region_label
    ),
    "{{REGION}}", var.region_full
  )

  # Build a flattened map of all secret/key combinations for SSM
  # Result: { "github/client_id" = { secret = "github", key = "client_id", value = "xxx" }, ... }
  ssm_secrets = {
    for pair in flatten([
      for secret_name, secret_def in var.secrets.definitions : [
        for key in secret_def.keys : {
          secret_name = secret_name
          key         = key
          description = secret_def.description
          value       = try(var.secret_values[secret_name][key], "")
        }
      ]
    ]) : "${pair.secret_name}/${pair.key}" => pair
  }
}

# =============================================================================
# KMS Key for SSM Parameter Encryption
# Creates a regional key for encrypting SSM SecureString parameters
# =============================================================================

resource "aws_kms_key" "ssm" {
  description             = "${var.km_label} SSM parameter encryption key - ${var.region_label} - sandbox ${var.sandbox_id}"
  deletion_window_in_days = 30
  enable_key_rotation     = true
  policy                  = data.aws_iam_policy_document.ssm_key_policy.json

  tags = {
    Name             = "${var.km_label}-ssm-key-${var.region_label}"
    "km:label"       = var.km_label
    "km:sandbox-id"  = var.sandbox_id
    Region           = var.region_label
    Purpose          = "ssm-parameter-encryption"
    ManagedBy        = "Terragrunt"
  }
}

resource "aws_kms_alias" "ssm" {
  name          = "alias/${var.km_label}-ssm-${var.region_label}-${var.sandbox_id}"
  target_key_id = aws_kms_key.ssm.key_id
}

data "aws_iam_policy_document" "ssm_key_policy" {
  # Allow account root full access (required for key administration)
  statement {
    sid    = "EnableAccountAdmin"
    effect = "Allow"
    principals {
      type        = "AWS"
      identifiers = ["arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"]
    }
    actions   = ["kms:*"]
    resources = ["*"]
  }

  # Allow SSM to use the key
  statement {
    sid    = "AllowSSMAccess"
    effect = "Allow"
    principals {
      type        = "Service"
      identifiers = ["ssm.amazonaws.com"]
    }
    actions = [
      "kms:Encrypt",
      "kms:Decrypt",
      "kms:GenerateDataKey*",
      "kms:DescribeKey"
    ]
    resources = ["*"]
  }

  # Allow ECS tasks to decrypt parameters (for container secrets)
  statement {
    sid    = "AllowECSTaskDecrypt"
    effect = "Allow"
    principals {
      type        = "Service"
      identifiers = ["ecs-tasks.amazonaws.com"]
    }
    actions = [
      "kms:Decrypt",
      "kms:DescribeKey"
    ]
    resources = ["*"]
    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [data.aws_caller_identity.current.account_id]
    }
  }
}

# =============================================================================
# SSM Parameter Store
# Creates one SecureString parameter per secret/key combination
# =============================================================================

resource "aws_ssm_parameter" "secret" {
  for_each = local.ssm_secrets

  name        = "${local.ssm_prefix}/${each.key}"
  description = "${each.value.description} - ${each.value.key}"
  type        = "SecureString"
  value       = each.value.value != "" ? each.value.value : "PLACEHOLDER"
  key_id      = aws_kms_key.ssm.arn

  tags = {
    "km:label"      = var.km_label
    "km:sandbox-id" = var.sandbox_id
    Region          = var.region_label
    SecretName      = each.value.secret_name
    SecretKey       = each.value.key
  }

  lifecycle {
    ignore_changes = [
      # Allow manual updates without Terraform overwriting
      value
    ]
  }
}
