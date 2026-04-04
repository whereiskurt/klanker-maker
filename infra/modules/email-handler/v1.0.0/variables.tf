variable "artifact_bucket_name" {
  description = "Name of the S3 artifact bucket for profile uploads and email storage"
  type        = string
}

variable "artifact_bucket_arn" {
  description = "ARN of the S3 artifact bucket for IAM policy scoping"
  type        = string
}

variable "state_bucket" {
  description = "S3 bucket holding sandbox metadata (for status lookups)"
  type        = string
  default     = ""
}

variable "email_domain" {
  description = "Email domain for operator replies (e.g. sandboxes.klankermaker.ai)"
  type        = string
  default     = "sandboxes.klankermaker.ai"
}

variable "lambda_zip_path" {
  description = "Path to the compiled email-create-handler Lambda zip"
  type        = string
}

variable "safe_phrase_ssm_key" {
  description = "SSM parameter path for the KM-AUTH safe phrase"
  type        = string
  default     = "/km/config/remote-create/safe-phrase"
}

variable "bedrock_model_id" {
  description = "Bedrock model ID for AI email interpretation (e.g. us.anthropic.claude-haiku-4-5-20251001-v1:0)"
  type        = string
  default     = "us.anthropic.claude-haiku-4-5-20251001-v1:0"
}
