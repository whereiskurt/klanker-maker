variable "table_name" {
  description = "Name of the GitHub threads DynamoDB table (e.g. km-github-threads)."
  type        = string
  default     = "km-github-threads"
}

variable "tags" {
  description = "Resource tags to merge onto the DynamoDB table."
  type        = map(string)
  default     = {}
}
