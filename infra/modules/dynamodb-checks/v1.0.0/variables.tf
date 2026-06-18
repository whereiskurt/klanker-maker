variable "table_name" {
  description = "Name of the checks DynamoDB table (e.g. km-checks)."
  type        = string
  default     = "km-checks"
}

variable "tags" {
  description = "Resource tags to merge onto the DynamoDB table."
  type        = map(string)
  default     = {}
}
