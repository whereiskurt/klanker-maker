variable "resource_prefix" {
  description = "km install resource prefix (e.g., 'km' or 'km2')"
  type        = string
}

variable "aws_region" {
  description = "AWS region (informational; alias is region-scoped via provider)"
  type        = string
}

variable "register_secrets_key" {
  description = "Set to false to skip resource creation (mirrors ses-shared-rule-set register_shared_rule_set toggle for bootstrap auto-detect)"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Additional tags merged onto the KMS key"
  type        = map(string)
  default     = {}
}
