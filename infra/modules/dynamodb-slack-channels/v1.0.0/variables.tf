variable "table_name" {
  description = "Name of the Slack channels DynamoDB table (e.g. km-slack-channels)."
  type        = string
  default     = "km-slack-channels"
}

variable "tags" {
  description = "Resource tags to merge onto the DynamoDB table."
  type        = map(string)
  default     = {}
}
