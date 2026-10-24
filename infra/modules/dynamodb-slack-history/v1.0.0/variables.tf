variable "table_name" {
  description = "Name of the Slack history mirror table (e.g. km-slack-history)."
  type        = string
  default     = "km-slack-history"
}
variable "kms_key_arn" {
  description = "Platform CMK for SSE; empty uses the AWS-owned key. The bridge role already decrypts under it."
  type        = string
  default     = ""
}
variable "tags" {
  description = "Resource tags to merge onto the table."
  type        = map(string)
  default     = {}
}
