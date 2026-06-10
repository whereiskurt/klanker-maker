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

# Phase 91.6 — Slack threads write permission for the create-handler Lambda.
# Closes a Phase 67-07 bug: postReadyAnnouncement upsert silently failed with
# AccessDeniedException because the create-handler role had no PutItem on
# km-slack-threads. Result: Sandbox Ready threads had no anchor row, so user
# replies got blocked by the mention-only filter instead of triggering the
# Phase 91.3 thread-bypass.
variable "slack_threads_table_name" {
  description = "Name of the km-slack-threads DynamoDB table. Empty disables the IAM grant (back-compat for pre-Phase-67 installs)."
  type        = string
  default     = ""
}

# Phase 104.3 — Slack channels table for km create O(1) alias→channel_id lookup.
# Conditionally grants GetItem/PutItem/DescribeTable so installs without
# Phase 104 don't acquire an unused policy.
variable "slack_channels_table_name" {
  description = "Name of the km-slack-channels DynamoDB table. Empty disables the IAM grant (back-compat for pre-Phase-104 installs)."
  type        = string
  default     = ""
}
