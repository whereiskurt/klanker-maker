variable "resource_prefix" {
  type        = string
  default     = "km"
  description = "Resource-name prefix. Default 'km' renders byte-identical names to v1.0.0. Set to a non-'km' value for a second install in the same AWS account/Organization. Pattern: ^[a-z][a-z0-9]{0,11}$ (1-12 lowercase alphanumerics, start with letter)."

  validation {
    condition     = can(regex("^[a-z][a-z0-9]{0,11}$", var.resource_prefix))
    error_message = "resource_prefix must be 1-12 chars, start with a lowercase letter, contain only [a-z0-9]."
  }
}

variable "application_account_id" {
  type        = string
  description = "12-digit AWS account ID where sandboxes run. The SCP is attached to this account."

  validation {
    condition     = can(regex("^[0-9]{12}$", var.application_account_id))
    error_message = "application_account_id must be a 12-digit AWS account ID."
  }
}

variable "allowed_regions" {
  type        = list(string)
  description = "Regions allowed for non-global AWS services. All other regions are denied by the region lock statement. Example: [\"us-east-1\"]."

  validation {
    condition     = length(var.allowed_regions) > 0
    error_message = "allowed_regions must contain at least one region."
  }
}

variable "trusted_role_arns" {
  type        = list(string)
  description = <<-EOT
    Role ARN patterns (supports wildcards) exempt from the general containment Deny statements.
    These roles can mutate security groups, network resources, IAM, storage, and SSM.
    Default includes SSO roles (with aws-reserved/sso.amazonaws.com/ path prefix).

    Callers should add {prefix}-provisioner-* and {prefix}-lifecycle-* patterns when dedicated
    provisioner roles are introduced (currently not yet deployed).

    Note: {prefix}-budget-enforcer-* and {prefix}-ec2spot-ssm-* are NOT passed here — they are
    handled inside the module via statement-specific locals because they only apply to
    specific Deny statements (IAM escalation and SSM pivot, respectively).
  EOT
  default = [
    "arn:aws:iam::*:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*",
  ]
}
