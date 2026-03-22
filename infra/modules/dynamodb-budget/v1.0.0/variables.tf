variable "table_name" {
  type        = string
  default     = "km-budgets"
  description = "Name of the DynamoDB budget tracking table."
}

variable "replica_regions" {
  type        = list(string)
  default     = []
  description = "List of additional AWS regions to create DynamoDB global table replicas in."
}

variable "tags" {
  type        = map(string)
  default     = {}
  description = "Tags to apply to the DynamoDB table and all associated resources."
}
