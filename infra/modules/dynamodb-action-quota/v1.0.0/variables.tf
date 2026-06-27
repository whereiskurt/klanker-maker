variable "table_name" {
  description = "Name of the action-quota DynamoDB table (e.g. km-action-quota)."
  type        = string
  default     = "km-action-quota"
}

variable "tags" {
  description = "Resource tags to merge onto the DynamoDB table."
  type        = map(string)
  default     = {}
}
