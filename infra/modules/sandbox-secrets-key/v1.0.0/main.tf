# infra/modules/sandbox-secrets-key/v1.0.0/main.tf
# Foundation: account-shared KMS key for SOPS bundle decryption. See Phase 89.
# Applied by `km bootstrap --shared-secrets-key` (Plan 89-04).
#
# Resource ownership:
#   - aws_kms_key.secrets        (prevent_destroy=true, enable_key_rotation=true)
#   - aws_kms_alias.secrets      (alias/${var.resource_prefix}-sandbox-secrets)
#
# NOTE: No required_providers block — root.hcl is the single source of provider
# declarations (memory project_terragrunt_providers_in_root).

data "aws_caller_identity" "current" {}

data "aws_iam_policy_document" "secrets_key_policy" {
  # Grant account root full admin over the key. This enables IAM identity
  # policies in the account to delegate key access — specifically the
  # ec2spot role policy (ec2spot/v1.2.0) which scopes kms:Decrypt to this
  # key via kms:ResourceAliases (a condition key valid in identity policies
  # but NOT in key policies — AWS rejects it as MalformedPolicyDocument).
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
}

# ============================================================
# KMS key for SOPS bundle encryption/decryption
# ============================================================

resource "aws_kms_key" "secrets" {
  count = var.register_secrets_key ? 1 : 0

  description             = "${var.resource_prefix} sandbox secrets (SOPS) — Phase 89"
  deletion_window_in_days = 30
  enable_key_rotation     = true
  policy                  = data.aws_iam_policy_document.secrets_key_policy.json

  tags = merge(var.tags, {
    Name      = "${var.resource_prefix}-sandbox-secrets"
    Purpose   = "sops-bundle-decryption"
    ManagedBy = "Terragrunt"
  })

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_kms_alias" "secrets" {
  count = var.register_secrets_key ? 1 : 0

  name          = "alias/${var.resource_prefix}-sandbox-secrets"
  target_key_id = aws_kms_key.secrets[0].key_id
}
