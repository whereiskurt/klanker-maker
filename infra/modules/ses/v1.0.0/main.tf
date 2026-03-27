# SES domain identity and email infrastructure for km sandboxes.
# Creates the domain identity with DKIM, DNS records, receipt rule set (active),
# and an inbound receipt rule that routes all sandbox emails to S3.

data "aws_caller_identity" "current" {}

data "aws_region" "current" {}

locals {
  region_name = data.aws_region.current.id
}

# ============================================================
# SES Domain Identity
# ============================================================

resource "aws_ses_domain_identity" "sandbox" {
  domain = var.domain
}

resource "aws_ses_domain_dkim" "sandbox" {
  domain = aws_ses_domain_identity.sandbox.domain
}

# ============================================================
# Route53 DNS Records
# ============================================================

# DKIM CNAME records (x3) — required for email authentication
resource "aws_route53_record" "dkim" {
  count   = 3
  zone_id = var.route53_zone_id
  name    = "${aws_ses_domain_dkim.sandbox.dkim_tokens[count.index]}._domainkey.${var.domain}"
  type    = "CNAME"
  ttl     = 600
  records = ["${aws_ses_domain_dkim.sandbox.dkim_tokens[count.index]}.dkim.amazonses.com"]
}

# Domain verification TXT record
resource "aws_route53_record" "ses_verification" {
  zone_id = var.route53_zone_id
  name    = "_amazonses.${var.domain}"
  type    = "TXT"
  ttl     = 600
  records = [aws_ses_domain_identity.sandbox.verification_token]
}

# MX record — routes inbound email to SES
resource "aws_route53_record" "mx" {
  zone_id = var.route53_zone_id
  name    = var.domain
  type    = "MX"
  ttl     = 300
  records = ["10 inbound-smtp.${local.region_name}.amazonaws.com"]
}

# ============================================================
# Receipt Rule Set and Inbound Rule
# ============================================================

resource "aws_ses_receipt_rule_set" "km_sandbox" {
  rule_set_name = "km-sandbox-email"
}

# Activate the rule set — SES requires exactly one active rule set per region.
resource "aws_ses_active_receipt_rule_set" "km_sandbox" {
  rule_set_name = aws_ses_receipt_rule_set.km_sandbox.rule_set_name
}

# Operator-inbound receipt rule: route emails to operator@{domain} to a separate
# S3 prefix (mail/create/) which triggers the email-create-handler Lambda.
# This rule has position 1 (higher priority) so it matches before sandbox-inbound.
# Conditional on email_create_handler_arn being set — safe to deploy without it.
resource "aws_ses_receipt_rule" "create_inbound" {
  count = var.email_create_handler_arn != "" ? 1 : 0

  name          = "km-operator-inbound"
  rule_set_name = aws_ses_receipt_rule_set.km_sandbox.rule_set_name
  recipients    = ["operator@${var.domain}"]
  enabled       = true
  scan_enabled  = false

  s3_action {
    bucket_name       = var.artifact_bucket_name
    object_key_prefix = "mail/create/"
    position          = 1
  }

  depends_on = [aws_s3_bucket_policy.ses_inbound, aws_ses_active_receipt_rule_set.km_sandbox]
}

# Lambda permission: allow S3 to invoke the email-create-handler Lambda
# when a new object is written to the mail/create/ prefix.
resource "aws_lambda_permission" "s3_email_create" {
  count = var.email_create_handler_arn != "" ? 1 : 0

  statement_id  = "AllowS3InvokeEmailCreate"
  action        = "lambda:InvokeFunction"
  function_name = var.email_create_handler_arn
  principal     = "s3.amazonaws.com"
  source_arn    = var.artifact_bucket_arn
}

# S3 event notification: trigger email-create-handler Lambda on mail/create/ prefix writes.
# Note: Only one aws_s3_bucket_notification can exist per bucket. If another module
# also manages S3 notifications on this bucket, consolidate here.
resource "aws_s3_bucket_notification" "email_create" {
  count = var.email_create_handler_arn != "" ? 1 : 0

  bucket = var.artifact_bucket_name

  lambda_function {
    lambda_function_arn = var.email_create_handler_arn
    events              = ["s3:ObjectCreated:*"]
    filter_prefix       = "mail/create/"
  }

  depends_on = [aws_lambda_permission.s3_email_create]
}

# Inbound receipt rule: store all sandbox email to S3 under mail/ prefix.
# Recipients filter uses the domain wildcard. The sandbox-id is embedded in
# the To address; agents parse headers to identify which sandbox owns the message.
# Position 2 — lower priority than operator-inbound (position 1) so operator@ is
# handled first by the operator-inbound rule when email_create_handler_arn is set.
resource "aws_ses_receipt_rule" "sandbox_inbound" {
  name          = "sandbox-inbound"
  rule_set_name = aws_ses_receipt_rule_set.km_sandbox.rule_set_name
  recipients    = [var.domain]
  enabled       = true
  scan_enabled  = false

  s3_action {
    bucket_name       = var.artifact_bucket_name
    object_key_prefix = "mail/"
    position          = 1
  }

  after = var.email_create_handler_arn != "" ? aws_ses_receipt_rule.create_inbound[0].name : null

  depends_on = [aws_s3_bucket_policy.ses_inbound, aws_ses_active_receipt_rule_set.km_sandbox]
}

# ============================================================
# S3 Bucket Policy — allow SES inbound email + CloudWatch log export
# ============================================================
# IMPORTANT: Only ONE aws_s3_bucket_policy can exist per bucket across all
# modules. This policy is the single source of truth for the artifacts bucket.
# Add new service principals here rather than in other modules.

data "aws_iam_policy_document" "artifacts_bucket" {
  # SES inbound email → mail/ prefix
  statement {
    sid    = "AllowSESPutObject"
    effect = "Allow"

    principals {
      type        = "Service"
      identifiers = ["ses.amazonaws.com"]
    }

    actions   = ["s3:PutObject"]
    resources = ["${var.artifact_bucket_arn}/mail/*"]

    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [data.aws_caller_identity.current.account_id]
    }
  }

  # CloudWatch Logs export → logs/ prefix (used by CreateExportTask on destroy/TTL)
  statement {
    sid    = "AllowCloudWatchLogsExport"
    effect = "Allow"

    principals {
      type        = "Service"
      identifiers = ["logs.amazonaws.com"]
    }

    actions   = ["s3:PutObject"]
    resources = ["${var.artifact_bucket_arn}/logs/*"]

    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [data.aws_caller_identity.current.account_id]
    }
  }
}

resource "aws_s3_bucket_policy" "ses_inbound" {
  bucket = var.artifact_bucket_name
  policy = data.aws_iam_policy_document.artifacts_bucket.json
}
