variable "lambda_zip_path" {
  description = "Path to the compiled km-github-bridge Lambda zip (linux/arm64 bootstrap binary)"
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
  description = "Name of the DynamoDB nonces table (shared with Slack bridge; default: km-slack-bridge-nonces)"
  type        = string
  default     = "km-slack-bridge-nonces"
}

variable "nonces_table_arn" {
  description = "ARN of the DynamoDB nonces table (used in IAM policy)"
  type        = string
}

# ============================================================
# GitHub-specific configuration
# ============================================================

variable "github_repos_json" {
  description = "JSON-serialized list of RepoEntry objects (KM_GITHUB_REPOS). Shape: {repos:[{match,alias,profile,allow[]}], default_profile}. Empty string = bridge dormant (all repos silently dropped)."
  type        = string
  default     = ""
}

variable "github_default_profile" {
  description = "Fallback SandboxProfile name when a matched repo entry has no profile set."
  type        = string
  default     = "github-review"
}

variable "webhook_secret_path" {
  description = "SSM path for the GitHub webhook signing secret (SecureString)"
  type        = string
  default     = "/km/config/github/webhook-secret"
}

variable "bot_login_path" {
  description = "SSM path for the GitHub App bot login name (e.g. 'myapp[bot]')"
  type        = string
  default     = "/km/config/github/bot-login"
}

variable "app_client_id_path" {
  description = "SSM path for the GitHub App client ID"
  type        = string
  default     = "/km/config/github/app-client-id"
}

variable "private_key_path" {
  description = "SSM path for the GitHub App RSA private key PEM (SecureString)"
  type        = string
  default     = "/km/config/github/private-key"
}

variable "installation_id_path" {
  description = "SSM path for the GitHub App installation ID"
  type        = string
  default     = "/km/config/github/installation-id"
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
# Phase 98-02: km-github-threads thread/session continuity
# ============================================================

variable "github_threads_table_name" {
  description = "Name of the DynamoDB km-github-threads table for (repo, number) continuity tracking"
  type        = string
  default     = "km-github-threads"
}

variable "github_threads_table_arn" {
  description = "ARN of the DynamoDB km-github-threads table (used in IAM policy). Required when github_threads_table_name is set."
  type        = string
  default     = ""
}
