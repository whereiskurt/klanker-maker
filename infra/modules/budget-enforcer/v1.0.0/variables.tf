variable "lambda_zip_path" {
  description = "Path to the compiled Go Lambda bootstrap zip file (budget-enforcer binary)"
  type        = string
}

variable "budget_table_name" {
  description = "Name of the DynamoDB budget tracking table (e.g. km-budgets)"
  type        = string
  default     = "km-budgets"
}

variable "state_bucket" {
  description = "S3 bucket name for sandbox state/profiles (KM_STATE_BUCKET)"
  type        = string
}

variable "email_domain" {
  description = "Email domain for sandbox notifications (e.g. sandboxes.klankermaker.ai)"
  type        = string
  default     = "sandboxes.klankermaker.ai"
}

variable "sandbox_id" {
  description = "Unique sandbox identifier (e.g. sb-a1b2c3d4)"
  type        = string
}

variable "instance_type" {
  description = "EC2 instance type or Fargate task size string (for logging)"
  type        = string
  default     = ""
}

variable "spot_rate" {
  description = "Pre-calculated hourly spot rate in USD. Set at sandbox creation time."
  type        = number
  default     = 0.0
  # TODO: spot rate resolution — options:
  # (a) Embed a static rate from a per-instance-type lookup table at compile time (current approach).
  # (b) Resolve at Lambda runtime from the AWS Pricing API on each invocation.
  # Using option (a) for simplicity. The rate is embedded in the EventBridge payload
  # at sandbox creation time (km create calculates from pkg/aws/pricing.go).
}

variable "substrate" {
  description = "Sandbox substrate type: \"ec2\" or \"ecs\""
  type        = string
  validation {
    condition     = contains(["ec2", "ecs"], var.substrate)
    error_message = "substrate must be \"ec2\" or \"ecs\""
  }
}

variable "created_at" {
  description = "RFC3339 timestamp of sandbox creation (used to calculate elapsed cost)"
  type        = string
}

variable "role_arn" {
  description = "IAM role ARN of the sandbox execution role (for Bedrock policy revocation)"
  type        = string
}

variable "instance_id" {
  description = "EC2 instance ID to stop when compute budget is exhausted (empty for ECS)"
  type        = string
  default     = ""
}

variable "task_arn" {
  description = "ECS task ARN to stop when compute budget is exhausted (empty for EC2)"
  type        = string
  default     = ""
}

variable "cluster_arn" {
  description = "ECS cluster ARN containing the task (empty for EC2)"
  type        = string
  default     = ""
}

variable "operator_email" {
  description = "Operator email address for budget enforcement notifications"
  type        = string
  default     = ""
}

variable "budget_table_arn" {
  description = "ARN of the DynamoDB budget table for IAM policy scoping"
  type        = string
}

variable "sandbox_table_name" {
  description = "Name of the DynamoDB sandbox metadata table (e.g. km-sandboxes)"
  type        = string
  default     = "km-sandboxes"
}

variable "sandbox_table_arn" {
  description = "ARN of the DynamoDB sandbox metadata table for lock check and status update"
  type        = string
  default     = ""
}
