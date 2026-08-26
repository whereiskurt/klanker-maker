variable "lambda_zip_path" {
  description = "Path to the compiled km-webhook-bridge Lambda zip (linux/arm64 bootstrap binary)"
  type        = string
}

variable "resource_prefix" {
  description = "Resource prefix used for naming (default: km)"
  type        = string
  default     = "km"
}

variable "tags" {
  description = "Tags to apply to all resources created by this module"
  type        = map(string)
  default     = {}
}

variable "kms_key_arn" {
  description = "ARN (or alias ARN) of the platform KMS key for decrypting SSM SecureString parameters. Empty string falls back to a broad account-scoped key resource."
  type        = string
  default     = ""
}

variable "sandboxes_table_name" {
  description = "Name of the DynamoDB km-sandboxes table for alias-index lookup"
  type        = string
  default     = "km-sandboxes"
}

variable "sandboxes_table_arn" {
  description = "ARN of the DynamoDB km-sandboxes table (used in IAM policy)"
  type        = string
}

variable "nonces_table_name" {
  description = "Name of the DynamoDB nonces table (shared with Slack/GitHub/H1 bridges; default: km-slack-bridge-nonces)"
  type        = string
  default     = "km-slack-bridge-nonces"
}

variable "nonces_table_arn" {
  description = "ARN of the DynamoDB nonces table (used in IAM policy)"
  type        = string
}

# ============================================================
# Generic webhook ingress configuration (Phase 127)
# ============================================================

variable "webhook_sources_json" {
  description = "JSON-serialized webhook source + rate-limit config (KM_WEBHOOK_SOURCES). Shape: {sources:[{name,auth{type,header,secret_path},replay_ttl_seconds,field_paths,rules[{match,alias,profile,on_absent,cooldown_seconds,group_by,prompt}]}], rate_limit:{max_dispatches,window_seconds}}. Empty string = bridge dormant (every request silent-drops)."
  type        = string
  default     = ""
}

variable "webhook_rate_limit_json" {
  description = "Reserved for a future standalone KM_WEBHOOK_RATE_LIMIT env var. Currently unused: rate_limit travels embedded inside webhook_sources_json (cmd/km-webhook-bridge reads it from there, not from a separate env var). Kept as a forward-compatible placeholder input so a later change can split it out without a module signature change."
  type        = string
  default     = ""
}

variable "artifacts_bucket" {
  description = "S3 artifacts bucket name (for EventBridge artifact_bucket field on cold create). Required for cold-create to work."
  type        = string
  default     = ""
}

variable "artifacts_prefix" {
  description = "S3 artifacts prefix (for EventBridge artifact_prefix field on cold create)."
  type        = string
  default     = ""
}

# Phase 121: action-quota table ARN for quota enforcement on webhook_dispatch.
# Empty default = table not yet provisioned (quota IAM policy is omitted, bridge dormant).
variable "quota_table_arn" {
  description = "Phase 121: ARN of the {prefix}-action-quota DynamoDB table. Empty = quota IAM policy omitted."
  type        = string
  default     = ""
}
