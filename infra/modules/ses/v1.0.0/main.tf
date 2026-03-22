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

# Inbound receipt rule: store all sandbox email to S3 under mail/ prefix.
# Recipients filter uses the domain wildcard. The sandbox-id is embedded in
# the To address; agents parse headers to identify which sandbox owns the message.
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

  depends_on = [aws_s3_bucket_policy.ses_inbound, aws_ses_active_receipt_rule_set.km_sandbox]
}

# ============================================================
# S3 Bucket Policy — allow SES to write inbound email
# ============================================================

data "aws_iam_policy_document" "ses_inbound" {
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
      variable = "aws:Referer"
      values   = [data.aws_caller_identity.current.account_id]
    }
  }
}

resource "aws_s3_bucket_policy" "ses_inbound" {
  bucket = var.artifact_bucket_name
  policy = data.aws_iam_policy_document.ses_inbound.json
}
