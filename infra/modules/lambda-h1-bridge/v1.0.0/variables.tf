variable "lambda_zip_path" {
  description = "Path to the compiled km-h1-bridge Lambda zip (linux/arm64 bootstrap binary)"
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
  description = "Name of the DynamoDB nonces table (shared with Slack/GitHub bridges; default: km-slack-bridge-nonces)"
  type        = string
  default     = "km-slack-bridge-nonces"
}

variable "nonces_table_arn" {
  description = "ARN of the DynamoDB nonces table (used in IAM policy)"
  type        = string
}

# ============================================================
# HackerOne-specific configuration
# ============================================================

variable "h1_programs_json" {
  description = "JSON-serialized program config (KM_H1_PROGRAMS). Shape: {programs:[{handle,targets[],allow[],bot_handle,events{},commands{},default_command}], default_profile, bot_handle}. Empty string = bridge dormant (all programs silently dropped)."
  type        = string
  default     = ""
}

variable "h1_default_profile" {
  description = "Fallback SandboxProfile name when a matched program target has no profile set."
  type        = string
  default     = "h1-triage"
}

variable "h1_bot_handle" {
  description = "Install-wide comment-keyword token (e.g. '@km'). A program may override it per-program in h1_programs_json."
  type        = string
  default     = ""
}

variable "webhook_secret_path" {
  description = "SSM path for the HackerOne webhook signing secret (SecureString)"
  type        = string
  default     = "/km/config/h1/webhook-secret"
}

variable "api_username_path" {
  description = "SSM path for the HackerOne customer-API Basic-Auth username"
  type        = string
  default     = "/km/config/h1/api-username"
}

variable "api_token_path" {
  description = "SSM path for the HackerOne customer-API Basic-Auth token (SecureString)"
  type        = string
  default     = "/km/config/h1/api-token"
}

variable "h1_api_base_url" {
  description = "HackerOne customer-API base URL (optional override; default https://api.hackerone.com/v1)."
  type        = string
  default     = ""
}

variable "commands_path" {
  description = "SSM path for the h1 command set (base64-encoded CommandSet JSON; absent = command pass dormant)."
  type        = string
  default     = "/km/config/h1/commands"
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

# ============================================================
# km-h1-threads thread/session continuity (Plan 08 provisions the table)
# ============================================================

variable "h1_threads_table_name" {
  description = "Name of the DynamoDB km-h1-threads table for (report_id, target) continuity tracking"
  type        = string
  default     = "km-h1-threads"
}

variable "h1_threads_table_arn" {
  description = "ARN of the DynamoDB km-h1-threads table (used in IAM policy). Empty string = grant omitted (backward compat before the table module is applied)."
  type        = string
  default     = ""
}
