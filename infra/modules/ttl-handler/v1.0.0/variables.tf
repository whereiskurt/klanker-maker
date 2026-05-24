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

variable "resource_prefix" {
  description = "Prefix for all resource names (default: km)"
  type        = string
  default     = "km"
}

variable "sandbox_table_name" {
  description = "Name of the DynamoDB sandbox metadata table"
  type        = string
  default     = "km-sandboxes"
}

variable "budget_table_name" {
  description = "Name of the DynamoDB budget table"
  type        = string
  default     = "km-budgets"
}

variable "schedules_table_name" {
  description = "Name of the DynamoDB schedules table"
  type        = string
  default     = "km-schedules"
}

variable "identities_table_name" {
  description = "Name of the DynamoDB km-identities table — used by the TTL handler to delete the sandbox's identity row during teardown so a reused alias does not inherit a stale pubkey via the alias-index GSI."
  type        = string
  default     = "km-identities"
}
