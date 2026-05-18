variable "resource_prefix" {
  type        = string
  default     = "km"
  description = "Per-install discriminator. Default 'km' renders 'km-Sandbox-Session', byte-identical to the document-name pattern but DIFFERENT from v1.0.0's 'KM-Sandbox-Session' (uppercase K). This is the load-bearing transition step for Phase 84.4.1: the canonical km install's document is renamed from KM-Sandbox-Session to km-Sandbox-Session at apply time."
  validation {
    condition     = can(regex("^[a-z][a-z0-9]{0,11}$", var.resource_prefix))
    error_message = "resource_prefix must be 1-12 chars, start with lowercase letter, contain only [a-z0-9]."
  }
}

variable "tags" {
  type        = map(string)
  default     = {}
  description = "Tags to apply to the SSM document."
}
