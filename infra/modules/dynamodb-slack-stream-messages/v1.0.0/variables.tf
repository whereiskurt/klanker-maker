variable "table_name" {
  description = "Name of the Slack stream messages DynamoDB table (e.g. km-slack-stream-messages). Must match Config.GetSlackStreamMessagesTableName()."
  type        = string
  default     = "km-slack-stream-messages"
}

variable "tags" {
  description = "Resource tags to merge onto the DynamoDB table."
  type        = map(string)
  default     = {}
}
