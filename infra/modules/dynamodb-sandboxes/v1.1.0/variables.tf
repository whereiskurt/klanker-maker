variable "table_name" {
  type        = string
  default     = "km-sandboxes"
  description = "Name of the DynamoDB sandbox metadata table."
}

variable "tags" {
  type        = map(string)
  default     = {}
  description = "Tags to apply to the DynamoDB table and all associated resources."
}
