# infra/modules/ses/v2.0.0/main.tf
# Per-install rules attached to the foundation's "sandbox-email-shared" rule set.
# The shared rule set, domain identity, DKIM, MX, and verification records are
# owned by the foundation module (infra/modules/ses-shared-rule-set/v1.0.0).
# This module ONLY manages: two prefix-named receipt rules + the S3 bucket policy.

data "aws_caller_identity" "current" {}

# ============================================================
# Receipt Rules (prefix-namespaced, attached to shared rule set)
# ============================================================

# Operator inbound: SES routes operator-${prefix}@<domain> to mail/create/${prefix}/ in S3.
# This rule has higher priority (evaluated first before the catchall).
resource "aws_ses_receipt_rule" "operator_inbound" {
  name          = "${var.resource_prefix}-operator-inbound"
  rule_set_name = "sandbox-email-shared" # String constant — foundation owns the rule-set resource.
  recipients    = ["operator-${var.resource_prefix}@${var.email_domain}"]
  enabled       = true
  scan_enabled  = false

  s3_action {
    bucket_name       = var.artifact_bucket_name
    object_key_prefix = "mail/create/${var.resource_prefix}/"
    position          = 1
  }
}

# Sandbox catchall: whole-domain match (AWS SES does not support per-rule wildcards).
# Both installs' catchall rules fire on every sandbox email; S3 prefix isolation
# (mail/${prefix}/) is the per-install boundary. The mail-handler Lambda's
# "unknown sandbox ID → drop" logic handles cross-contamination at read time.
resource "aws_ses_receipt_rule" "sandbox_catchall" {
  name          = "${var.resource_prefix}-sandbox-catchall"
  rule_set_name = "sandbox-email-shared"
  recipients    = [var.email_domain]
  enabled       = true
  scan_enabled  = false

  after = aws_ses_receipt_rule.operator_inbound.name # Specific match wins.

  s3_action {
    bucket_name       = var.artifact_bucket_name
    object_key_prefix = "mail/${var.resource_prefix}/"
    position          = 1
  }
}

# ============================================================
# S3 Bucket Policy — allow SES inbound email + CloudWatch log export
# ============================================================
# IMPORTANT: Only ONE aws_s3_bucket_policy can exist per bucket across all
# modules. When this module replaces v1.0.0, this policy supersedes the prior one.
# Add new service principals here rather than in other modules.

data "aws_iam_policy_document" "artifacts_bucket" {
  # SES inbound email → this install's prefix paths only (per-install isolation)
  statement {
    sid    = "AllowSESPutObjectScopedToPrefix"
    effect = "Allow"

    principals {
      type        = "Service"
      identifiers = ["ses.amazonaws.com"]
    }

    actions = ["s3:PutObject"]
    resources = [
      "arn:aws:s3:::${var.artifact_bucket_name}/mail/create/${var.resource_prefix}/*",
      "arn:aws:s3:::${var.artifact_bucket_name}/mail/${var.resource_prefix}/*",
    ]

    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [data.aws_caller_identity.current.account_id]
    }
  }

  # CloudWatch Logs export → logs/ prefix (used by CreateExportTask on destroy/TTL).
  # GetBucketAcl is required by CreateExportTask to verify permissions before writing.
  statement {
    sid    = "AllowCloudWatchLogsExport"
    effect = "Allow"

    principals {
      type        = "Service"
      identifiers = ["logs.amazonaws.com"]
    }

    actions   = ["s3:GetBucketAcl"]
    resources = ["arn:aws:s3:::${var.artifact_bucket_name}"]

    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [data.aws_caller_identity.current.account_id]
    }
  }

  statement {
    sid    = "AllowCloudWatchLogsPutObject"
    effect = "Allow"

    principals {
      type        = "Service"
      identifiers = ["logs.amazonaws.com"]
    }

    actions   = ["s3:PutObject"]
    resources = ["arn:aws:s3:::${var.artifact_bucket_name}/logs/*"]

    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [data.aws_caller_identity.current.account_id]
    }
  }
}

resource "aws_s3_bucket_policy" "mail" {
  bucket = var.artifact_bucket_name
  policy = data.aws_iam_policy_document.artifacts_bucket.json
}
