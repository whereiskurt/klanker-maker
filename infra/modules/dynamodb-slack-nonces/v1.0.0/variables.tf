variable "table_name" {
  type        = string
  default     = "km-slack-bridge-nonces"
  description = "Name of the DynamoDB nonce table for Slack bridge replay protection."
}

variable "tags" {
  type        = map(string)
  default     = {}
  description = "Tags to apply to the DynamoDB table and all associated resources."
}
