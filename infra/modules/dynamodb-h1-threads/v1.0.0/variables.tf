variable "table_name" {
  description = "Name of the HackerOne threads DynamoDB table (e.g. km-h1-threads)."
  type        = string
  default     = "km-h1-threads"
}

variable "tags" {
  description = "Resource tags to merge onto the DynamoDB table."
  type        = map(string)
  default     = {}
}
