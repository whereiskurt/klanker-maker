variable "resource_prefix" {
  description = "Install prefix; the policy is named {prefix}-sandbox-containment."
  type        = string
  default     = "km"
}

variable "application_account_id" {
  description = "AWS account the SCP is attached to (accounts.application)."
  type        = string
}

variable "policy_json" {
  description = "The SCP document, rendered by pkg/scp (km bootstrap / km env export it as KM_SCP_POLICY_JSON)."
  type        = string

  validation {
    condition     = can(jsondecode(var.policy_json).Statement)
    error_message = "policy_json is not an IAM policy document. Run through `km bootstrap --scp-only`, or `eval $(km env)`, which render it from km-config.yaml."
  }
}
