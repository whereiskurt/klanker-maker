# infra/modules/ses/v2.0.0/variables.tf
# Per-install SES rules. The shared rule set + domain identity now live in
# infra/modules/ses-shared-rule-set/v1.0.0 (Phase 84). This module only
# attaches prefix-named rules to that shared rule set.

variable "resource_prefix" {
  type        = string
  description = "Per-install discriminator. Used to namespace rule names and S3 key prefixes."
}

variable "email_domain" {
  type        = string
  description = "Full email domain, e.g. \"sandboxes.example.com\". Must match the foundation module's email_domain."
}

variable "artifact_bucket_name" {
  type        = string
  description = "S3 bucket receiving inbound mail. SES writes under mail/<resource_prefix>/ and mail/create/<resource_prefix>/."
}

variable "tags" {
  type    = map(string)
  default = {}
}

# Phase 126 — cross-account capacity borrowing (REQ-126-REGISTER).
# Additive with an empty default: an install with no links emits no extra
# bucket-policy statement and its policy is byte-identical to pre-126.
variable "launch_accounts_json" {
  description = "JSON object (KM_LAUNCH_ACCOUNTS) of registered cross-account capacity-borrowing links, keyed by link name. Each entry's box_role_arn gets a read-only (GetObject + ListBucket, never PutObject) statement on the artifacts bucket policy. Declared HERE because this module owns that bucket policy declaratively — only one aws_s3_bucket_policy can exist per bucket, so without this every km init erased the grant km account register wrote imperatively."
  type        = string
  default     = ""
}
