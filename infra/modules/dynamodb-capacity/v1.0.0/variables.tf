variable "table_name" {
  description = "Name of the capacity DynamoDB table (e.g. km-capacity)."
  type        = string
  default     = "km-capacity"
}

variable "tags" {
  description = "Resource tags to merge onto the DynamoDB table."
  type        = map(string)
  default     = {}
}
