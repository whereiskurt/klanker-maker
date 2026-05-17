# infra/modules/ses-shared-rule-set/v1.0.0/main.tf
# Foundation: account-shared SES state. See Phase 84 in .planning/ROADMAP.md.
# Plan 84-02 created this module; Plan 84-07 wires `km bootstrap --shared-ses` to apply it.
#
# Resource ownership (moved here from ses/v1.0.0 per Phase 84 CONTEXT.md):
#   - aws_ses_receipt_rule_set.shared      (prevent_destroy=true)
#   - aws_ses_active_receipt_rule_set.shared
#   - aws_ses_domain_identity.sandbox
#   - aws_ses_domain_dkim.sandbox          (3 DKIM tokens)
#   - aws_route53_record.dkim              (3 CNAME records — matches ses/v1.0.0 naming)
#   - aws_route53_record.ses_verification  (1 TXT record — domain verification)
#   - aws_route53_record.mx               (1 MX record)
#
# NOTE: No aws_ses_receipt_rule_set or aws_ses_domain_identity data sources exist in the
# AWS provider (RESEARCH Pitfall 2). When register_X=false this module simply does not
# create those resources; downstream consumers reference the rule set by the string
# constant var.rule_set_name and the domain identity by its known ARN pattern.

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

# ============================================================
# Phase 84.1: import blocks for in-place upgrade from Phase 82.x
# ============================================================
# These import blocks bring pre-existing AWS resources (created by the old
# ses/v1.0.0 regional module) under foundation management. They are
# idempotent: on fresh installs the import target does not exist in AWS,
# terraform tolerates the import failure and proceeds to create. After the
# first successful apply, the resources are in foundation state and these
# blocks become no-ops on every subsequent apply.
#
# Operators on fresh accounts can leave these blocks in place — they have no
# effect when the AWS resource does not exist. See Phase 84.1 GAP-6.
#
# NOTE: count-based addressing ([0]) — these target the count=1 resource
# variant. When register_*=true (the Phase 84.1 default), index [0] is the
# active resource.
#
# H9 (plan-checker rev 1): NO DKIM CNAME import blocks. DKIM record names
# embed AWS-generated tokens that are not knowable at plan time without
# already having the identity in state. Attempting to express them here
# would either use placeholder tokens (guaranteed plan failure) or require
# a two-pass apply that the operator has to babysit. Instead, DKIM record
# import is OPERATOR-RUN — see OPERATOR-GUIDE.md § Phase 84.1 upgrade
# safety (shipped by Plan 84.1-05) for the exact `terraform import` runbook.

import {
  to = aws_ses_receipt_rule_set.shared[0]
  id = var.rule_set_name
}

# AWS provider: the active receipt rule set's terraform ID is the rule set
# NAME (the activation pointer is per-account/region, but the import target
# uses the name).
import {
  to = aws_ses_active_receipt_rule_set.shared[0]
  id = var.rule_set_name
}

import {
  to = aws_ses_domain_identity.sandbox[0]
  id = var.email_domain
}

import {
  to = aws_ses_domain_dkim.sandbox[0]
  id = var.email_domain
}

# Route53 records — the AWS provider's import ID format is "<zone_id>_<name>_<type>"
# See https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/route53_record#import
# The verification TXT and MX records are single-instance (count=1) and have
# deterministic names knowable at plan time — safe to import via blocks.
import {
  to = aws_route53_record.ses_verification[0]
  id = "${var.hosted_zone_id}__amazonses.${var.email_domain}_TXT"
}

import {
  to = aws_route53_record.mx[0]
  id = "${var.hosted_zone_id}_${var.email_domain}_MX"
}

# ============================================================
# Shared rule set + always-active pointer
# ============================================================

resource "aws_ses_receipt_rule_set" "shared" {
  count         = var.register_shared_rule_set ? 1 : 0
  rule_set_name = var.rule_set_name

  lifecycle {
    prevent_destroy = true
  }
}

# Activates the shared rule set as the account-level active receipt rule set.
# SES allows exactly one active rule set per account/region.
# count=0 when register_shared_rule_set=false — the rule set is assumed already active.
resource "aws_ses_active_receipt_rule_set" "shared" {
  count         = var.register_shared_rule_set ? 1 : 0
  rule_set_name = var.rule_set_name

  depends_on = [aws_ses_receipt_rule_set.shared]
}

# ============================================================
# Shared domain identity + DKIM + DNS records
# ============================================================

resource "aws_ses_domain_identity" "sandbox" {
  count  = var.register_domain_identity ? 1 : 0
  domain = var.email_domain
}

resource "aws_ses_domain_dkim" "sandbox" {
  count  = var.register_domain_identity ? 1 : 0
  domain = aws_ses_domain_identity.sandbox[0].domain
}

# DKIM CNAME records (x3) — required for email authentication.
# Named "dkim" to match ses/v1.0.0 naming so a Phase 84 migration does not
# churn unrelated DNS records on first apply.
resource "aws_route53_record" "dkim" {
  count   = var.register_domain_identity ? 3 : 0
  zone_id = var.hosted_zone_id
  name    = "${aws_ses_domain_dkim.sandbox[0].dkim_tokens[count.index]}._domainkey.${var.email_domain}"
  type    = "CNAME"
  ttl     = 600
  records = ["${aws_ses_domain_dkim.sandbox[0].dkim_tokens[count.index]}.dkim.amazonses.com"]
}

# Domain verification TXT record — proves SES domain ownership.
resource "aws_route53_record" "ses_verification" {
  count   = var.register_domain_identity ? 1 : 0
  zone_id = var.hosted_zone_id
  name    = "_amazonses.${var.email_domain}"
  type    = "TXT"
  ttl     = 600
  records = [aws_ses_domain_identity.sandbox[0].verification_token]
}

# MX record — routes inbound email to SES.
# Priority 10, target: inbound-smtp.<region>.amazonaws.com
resource "aws_route53_record" "mx" {
  count   = var.register_domain_identity ? 1 : 0
  zone_id = var.hosted_zone_id
  name    = var.email_domain
  type    = "MX"
  ttl     = 300
  records = ["10 inbound-smtp.${var.aws_region}.amazonaws.com"]
}
