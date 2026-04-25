variable "document_name" {
  type        = string
  default     = "KM-Sandbox-Session"
  description = "Name of the SSM Session Manager document."
}

variable "tags" {
  type        = map(string)
  default     = {}
  description = "Tags to apply to the SSM document."
}
