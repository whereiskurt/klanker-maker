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
  description = "Path to the compiled Go Lambda bootstrap zip file (may include bundled terraform binary)"
  type        = string
}

variable "state_bucket" {
  description = "S3 bucket holding Terraform state (for terraform-based teardown)"
  type        = string
  default     = ""
}

variable "state_prefix" {
  description = "Terraform state key prefix (e.g. 'tf-km')"
  type        = string
  default     = "tf-km"
}

variable "region_label" {
  description = "Short region label (e.g. 'use1') used in state key construction"
  type        = string
  default     = "use1"
}

variable "create_handler_arn" {
  description = "ARN of the km-create-handler Lambda (target for scheduled creates)"
  type        = string
  default     = ""
}

variable "scheduler_role_arn" {
  description = "IAM role ARN that EventBridge Scheduler assumes to invoke Lambda targets"
  type        = string
  default     = ""
}
