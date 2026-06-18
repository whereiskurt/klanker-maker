variable "role_name" {
  description = "Name of the shared Lambda execution role (e.g. km-check-runner)."
  type        = string
  default     = "km-check-runner"
}

variable "artifacts_bucket" {
  description = "Name of the km artifacts S3 bucket (e.g. km-artifacts-123456789012)."
  type        = string
}

variable "resource_prefix" {
  description = "km resource prefix (e.g. km). Used to scope SSM parameter paths."
  type        = string
  default     = "km"
}

variable "table_name" {
  description = "Name of the {prefix}-checks DynamoDB table (e.g. km-checks)."
  type        = string
  default     = "km-checks"
}

variable "tags" {
  description = "Resource tags to merge onto the IAM role."
  type        = map(string)
  default     = {}
}
