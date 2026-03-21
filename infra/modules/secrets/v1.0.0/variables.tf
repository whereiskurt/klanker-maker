variable "km_label" {
  type        = string
  description = "Klanker Maker site label (e.g. 'km')"
}

variable "region_label" {
  type        = string
  description = "Short AWS region label (e.g. 'use1', 'usw2')"
}

variable "region_full" {
  type        = string
  description = "Full AWS region name (e.g. 'us-east-1')"
}

variable "sandbox_id" {
  type        = string
  description = "Sandbox identifier for resource naming and tagging"
}

variable "ssm_prefix_template" {
  type        = string
  description = "SSM parameter path prefix template. Supports {{KM_LABEL}}, {{REGION_LABEL}}, {{REGION}}."
  default     = "/{{KM_LABEL}}/sandboxes/{{REGION_LABEL}}"
}

variable "secrets" {
  description = "Secrets configuration — structure defines what secrets exist, values come from secret_values"
  type = object({
    definitions = map(object({
      description = optional(string, "")
      keys        = list(string)
    }))
  })
  default = {
    definitions = {}
  }
}

variable "secret_values" {
  description = "Secret values — map of secret_name -> key -> value. Marked sensitive."
  type        = map(map(string))
  sensitive   = true
  default     = {}
}
