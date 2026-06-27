variable "resource_prefix" {
  description = "Install resource prefix (e.g. km, km2). Used in IAM role and function names."
  type        = string
  default     = "km"
}

variable "lambda_zip_path" {
  description = "Local path to the km-quota-alerter Lambda zip built by km init / make build-lambdas."
  type        = string
}

variable "quota_stream_arn" {
  description = "ARN of the DynamoDB Streams endpoint on the {prefix}-action-quota table. Drives the event_source_mapping."
  type        = string
}

variable "quota_table_name" {
  description = "Name of the {prefix}-action-quota DynamoDB table. Used for the conditional alert_sent UpdateItem."
  type        = string
}

variable "quota_table_arn" {
  description = "ARN of the {prefix}-action-quota DynamoDB table. Used for the IAM UpdateItem grant."
  type        = string
}

variable "sandboxes_table_name" {
  description = "Name of the {prefix}-sandboxes DynamoDB table. Used to resolve sandbox Slack channel IDs."
  type        = string
  default     = ""
}

variable "sandboxes_table_arn" {
  description = "ARN of the {prefix}-sandboxes DynamoDB table. Used for the IAM GetItem grant."
  type        = string
  default     = ""
}

variable "operator_email" {
  description = "Operator email address. The alerter sends quota-breach notifications here via SES."
  type        = string
  default     = ""
}

variable "email_domain" {
  description = "SES verified domain (e.g. sandboxes.example.com). Used as the From address base: notifications@{email_domain}."
  type        = string
  default     = ""
}

variable "slack_control_channel" {
  description = "Optional Slack channel ID to post control-plane breach notices. Empty = no Slack control-channel post."
  type        = string
  default     = ""
}

variable "bot_token_path" {
  description = "SSM SecureString path for the Slack bot token (e.g. /km/slack/bot-token). Used for channel-level user notices."
  type        = string
  default     = ""
}

variable "kms_key_arn" {
  description = "ARN of the platform CMK used to encrypt Lambda env vars and SSM SecureString parameters. Empty = use aws/lambda managed key."
  type        = string
  default     = ""
}

variable "artifacts_bucket" {
  description = "S3 artifacts bucket name (KM_ARTIFACTS_BUCKET). Required for Lambda zip deploy."
  type        = string
  default     = ""
}

variable "tags" {
  description = "Resource tags merged onto every resource created by this module."
  type        = map(string)
  default     = {}
}
