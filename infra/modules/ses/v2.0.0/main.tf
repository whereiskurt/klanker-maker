# infra/modules/ses/v2.0.0/main.tf
# Per-install rules attached to the foundation's "sandbox-email-shared" rule set.
# The shared rule set, domain identity, DKIM, MX, and verification records are
# owned by the foundation module (infra/modules/ses-shared-rule-set/v1.0.0).
# This module ONLY manages: two prefix-named receipt rules + the S3 bucket policy.
#
# PHASE 84.1: This module ships with `removed {}` blocks at the bottom of the
# file for the resources that v1.0.0 owned but v2.0.0 no longer manages
# (rule set, domain identity, DKIM, MX, verification TXT). The corresponding
# `import {}` blocks live in infra/modules/ses-shared-rule-set/v1.0.0/main.tf.
# The pair makes the Phase 82.x → 84 in-place upgrade safe — zero AWS
# destruction during cutover. See
# .planning/phases/84.1-ses-upgrade-safety-gap-closure/84.1-04-PLAN.md.

data "aws_caller_identity" "current" {}

# Cross-account capacity-borrowing links (Phase 126). Decoded from the same
# KM_LAUNCH_ACCOUNTS payload the ttl-handler and create-handler modules consume,
# so the artifacts-bucket read grant below cannot drift from the link records.
# jsondecode() rejects an empty string, so the dormant default short-circuits
# to {} first — an install with no links emits no extra bucket-policy statement
# and its policy is byte-identical to pre-126.
locals {
  launch_accounts = var.launch_accounts_json != "" ? jsondecode(var.launch_accounts_json) : {}
}

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

  depends_on = [aws_s3_bucket_policy.mail]
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

  depends_on = [aws_s3_bucket_policy.mail]
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

  # Cross-account capacity borrowing (Phase 126, REQ-126-REGISTER).
  #
  # A linked account's box role needs read access to this bucket to fetch its
  # profile, sidecars and staged model weights at boot. `km account register`
  # ALSO writes this statement imperatively via PutBucketPolicy — but this file
  # owns the bucket policy declaratively, and the comment at the top of this
  # block is the reason why that mattered: only ONE aws_s3_bucket_policy can
  # exist per bucket, so every `km init` reconciled the policy and ERASED the
  # imperative grant. A cross-account box created after any apply then could not
  # read the home bucket at all, and `km doctor`'s remedy ("re-run
  # km account register") was a loop the next init undid.
  #
  # Emitting the same Sid and the same statement here makes the two writers
  # agree instead of fight: an apply now converges on exactly what register
  # wrote, rather than dropping it. register's imperative write is deliberately
  # KEPT — it covers the window between enrolling a link and the next init.
  #
  # Read-shaped only, matching register: GetObject + ListBucket, never
  # PutObject. The no-cross-account-write invariant on the home bucket is
  # unchanged.
  dynamic "statement" {
    for_each = local.launch_accounts

    content {
      sid    = "${var.resource_prefix}-account-link-${statement.key}-read"
      effect = "Allow"

      principals {
        type        = "AWS"
        identifiers = [statement.value.box_role_arn]
      }

      actions = ["s3:GetObject", "s3:ListBucket"]
      resources = [
        "arn:aws:s3:::${var.artifact_bucket_name}",
        "arn:aws:s3:::${var.artifact_bucket_name}/*",
      ]
    }
  }
}

resource "aws_s3_bucket_policy" "mail" {
  bucket = var.artifact_bucket_name
  policy = data.aws_iam_policy_document.artifacts_bucket.json
}

# ============================================================
# Phase 84.1: removed blocks for in-place upgrade from v1.0.0
# ============================================================
# The v1.0.0 regional ses module owned these resources. Phase 84 moved them
# to the foundation module (ses-shared-rule-set/v1.0.0/). On in-place
# upgrade, the terragrunt source flip from v1.0.0 to v2.0.0 normally plans
# a destroy for any resource present in state but absent from the new
# source. These removed blocks tell terraform: "forget the state entry,
# do NOT destroy the AWS object". The foundation module's import blocks
# then bring the same AWS objects under foundation management.
#
# Fresh installs: state never had these resources → removed blocks are no-ops.
# Phase 82.x upgrades: state has these resources → removed releases them
# cleanly without AWS destruction. See Phase 84.1 GAP-6.
#
# Resource addresses verified against infra/modules/ses/v1.0.0/main.tf as
# of 2026-05-16 (plan-checker rev 1 M10). Do NOT modify these names without
# re-verifying against the actual v1.0.0 source.

removed {
  from = aws_ses_receipt_rule_set.km_sandbox
  lifecycle {
    destroy = false
  }
}

removed {
  from = aws_ses_active_receipt_rule_set.km_sandbox
  lifecycle {
    destroy = false
  }
}

removed {
  from = aws_ses_domain_identity.sandbox
  lifecycle {
    destroy = false
  }
}

removed {
  from = aws_ses_domain_dkim.sandbox
  lifecycle {
    destroy = false
  }
}

removed {
  from = aws_route53_record.dkim
  lifecycle {
    destroy = false
  }
}

removed {
  from = aws_route53_record.ses_verification
  lifecycle {
    destroy = false
  }
}

removed {
  from = aws_route53_record.mx
  lifecycle {
    destroy = false
  }
}
