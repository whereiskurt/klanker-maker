variable "artifact_bucket_name" {
  description = "Name of the S3 artifact bucket (e.g. km-sandbox-artifacts-ea554771)"
  type        = string
}

variable "artifact_bucket_arn" {
  description = "ARN of the S3 artifact bucket for IAM policy scoping"
  type        = string
}

variable "email_domain" {
  description = "Email domain for sandbox notifications (e.g. sandboxes.klankermaker.ai)"
  type        = string
  default     = "sandboxes.klankermaker.ai"
}

variable "operator_email" {
  description = "Operator email address for lifecycle notifications. If empty, notifications are not sent."
  type        = string
  default     = ""
}

variable "lambda_zip_path" {
  description = "Path to the compiled Go Lambda bootstrap zip file"
  type        = string
}
