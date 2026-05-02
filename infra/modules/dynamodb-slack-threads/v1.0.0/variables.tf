variable "table_name" {
  description = "Name of the Slack threads DynamoDB table (e.g. km-slack-threads)."
  type        = string
  default     = "km-slack-threads"
}

variable "tags" {
  description = "Resource tags to merge onto the DynamoDB table."
  type        = map(string)
  default     = {}
}
