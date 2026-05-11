variable "role_id" {
  description = "ID of the IAM role to attach policies to (create_handler.id or IRSA role id)"
  type        = string
}

variable "resource_prefix" {
  description = "Prefix for all resource names (default: km)"
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

variable "dynamodb_table_name" {
  description = "DynamoDB table name for Terraform state locking"
  type        = string
}

variable "dynamodb_budget_table_arn" {
  description = "ARN of the DynamoDB budget table for IAM policy scoping"
  type        = string
}

variable "sandbox_table_name" {
  description = "Name of the DynamoDB sandbox metadata table"
  type        = string
}

variable "identities_table_name" {
  description = "Name of the DynamoDB identities table"
  type        = string
}
