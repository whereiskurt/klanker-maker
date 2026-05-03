variable "lambda_zip_path" {
  description = "Path to the compiled km-slack-bridge Lambda zip (linux/arm64 bootstrap binary)"
  type        = string
}

variable "bot_token_path" {
  description = "SSM parameter path for the Slack bot token (SecureString)"
  type        = string
  default     = "/km/slack/bot-token"
}

variable "identities_table_name" {
  description = "Name of the DynamoDB km-identities table for Ed25519 public key lookup"
  type        = string
  default     = "km-identities"
}

variable "identities_table_arn" {
  description = "ARN of the DynamoDB km-identities table (used in IAM policy)"
  type        = string
}

variable "sandboxes_table_name" {
  description = "Name of the DynamoDB km-sandboxes table for channel ownership lookup"
  type        = string
  default     = "km-sandboxes"
}

variable "sandboxes_table_arn" {
  description = "ARN of the DynamoDB km-sandboxes table (used in IAM policy)"
  type        = string
}

variable "nonces_table_name" {
  description = "Name of the DynamoDB km-slack-bridge-nonces table"
  type        = string
  default     = "km-slack-bridge-nonces"
}

variable "nonces_table_arn" {
  description = "ARN of the DynamoDB km-slack-bridge-nonces table (used in IAM policy)"
  type        = string
}

variable "kms_key_arn" {
  description = "ARN (or alias ARN) of the platform KMS key for decrypting SSM SecureString parameters. Empty string falls back to a broad account-scoped key resource."
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags to apply to all resources created by this module"
  type        = map(string)
  default     = {}
}

# ============================================================
# Phase 67-05 additions — inbound events path
# ============================================================

variable "resource_prefix" {
  description = "Resource prefix used for per-sandbox SQS queue names ({resource_prefix}-slack-inbound-{sandbox_id}.fifo)"
  type        = string
  default     = "km"
}

variable "slack_threads_table_name" {
  description = "Name of the DynamoDB km-slack-threads table for Slack thread tracking"
  type        = string
  default     = "km-slack-threads"
}

variable "signing_secret_path" {
  description = "SSM parameter path for the Slack signing secret (SecureString used for HMAC verification)"
  type        = string
  default     = "/km/slack/signing-secret"
}

# ============================================================
# Phase 67.1 — ACK reaction emoji (👀 by default)
# ============================================================

variable "slack_ack_emoji" {
  description = "Emoji NAME (without colons, e.g. \"eyes\" not \":eyes:\") used for the ACK reaction the bridge posts after a successful SQS enqueue of an inbound Slack message. Defaults to \"eyes\" (👀). Override to use a different emoji workspace-wide. Bot must have reactions:write scope."
  type        = string
  default     = "eyes"
}
