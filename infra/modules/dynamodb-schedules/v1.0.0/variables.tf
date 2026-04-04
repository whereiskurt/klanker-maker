variable "table_name" {
  type        = string
  default     = "km-schedules"
  description = "Name of the DynamoDB schedule metadata table."
}

variable "tags" {
  type        = map(string)
  default     = {}
  description = "Tags to apply to the DynamoDB table and all associated resources."
}
