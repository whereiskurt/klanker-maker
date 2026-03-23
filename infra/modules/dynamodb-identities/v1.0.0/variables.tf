variable "table_name" {
  type        = string
  default     = "km-identities"
  description = "Name of the DynamoDB identity tracking table."
}

variable "replica_regions" {
  description = "Regions for DynamoDB global table replicas. Empty for single-region v1 deployment. Follows the same global table replication pattern as dynamodb-budget module — populate when multi-region sandbox deployment is needed."
  type        = list(string)
  default     = []
}

variable "tags" {
  type        = map(string)
  default     = {}
  description = "Tags to apply to the DynamoDB table and all associated resources."
}
