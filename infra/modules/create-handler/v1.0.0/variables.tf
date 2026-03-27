variable "lambda_zip_path" {
  description = "Path to the create-handler Lambda zip file"
  type        = string
}

variable "artifact_bucket_name" {
  description = "Name of the S3 artifact bucket (e.g. km-sandbox-artifacts-ea554771)"
  type        = string
}

variable "artifact_bucket_arn" {
  description = "ARN of the S3 artifact bucket for IAM policy scoping"
  type        = string
}

variable "state_bucket" {
  description = "S3 bucket holding Terraform state for sandbox modules"
  type        = string
}

variable "state_prefix" {
  description = "Terraform state key prefix (e.g. 'tf-km')"
  type        = string
  default     = "tf-km"
}

variable "email_domain" {
  description = "Email subdomain for sandbox addresses (e.g. sandboxes.klankermaker.ai)"
  type        = string
}

variable "operator_email" {
  description = "Operator email address for lifecycle notifications"
  type        = string
  default     = ""
}

variable "region_label" {
  description = "Short region label (e.g. 'use1') used in state key construction"
  type        = string
  default     = "use1"
}

variable "dynamodb_table_name" {
  description = "DynamoDB table name for Terraform state locking"
  type        = string
}

variable "dynamodb_budget_table_arn" {
  description = "ARN of the DynamoDB budget table for IAM policy scoping"
  type        = string
}

variable "email_create_handler_arn" {
  description = "ARN of the email-create-handler Lambda (for cross-reference documentation)"
  type        = string
  default     = ""
}

variable "scp_trusted_role_arns" {
  description = "List of IAM role ARNs for SCP carve-out documentation (operator-managed)"
  type        = list(string)
  default     = []
}
