# Foundation module owning account-shared SES state. Per Phase 84, replaces the
# rule-set / domain-identity ownership that previously lived in `ses/v1.0.0`.
# Applied by `km bootstrap --shared-ses` (Plan 84-07).

variable "rule_set_name" {
  type        = string
  description = "Name of the singleton SES receipt rule set. Shared across all installs in the account."
  default     = "sandbox-email-shared"
}

variable "email_domain" {
  type        = string
  description = "The email subdomain for sandbox addresses, e.g. 'sandboxes.example.com'. Becomes the SES domain identity."
}

variable "hosted_zone_id" {
  type        = string
  description = "Route53 hosted zone ID where DNS records (DKIM CNAME, TXT, MX) will be created."
}

variable "aws_region" {
  type        = string
  description = "AWS region name (e.g. 'us-east-1'). Needed to construct the MX target inbound-smtp.<region>.amazonaws.com."
}

variable "register_shared_rule_set" {
  type        = bool
  description = "When true (default), the module creates the singleton SES receipt rule set and sets it as active. When false, no rule-set resources are created — downstream modules reference the rule set by the constant string var.rule_set_name."
  default     = true
}

variable "register_domain_identity" {
  type        = bool
  description = "When false, the module skips creating the SES domain identity, DKIM, MX, and verification records — assumes they already exist in this account. Set by km bootstrap --shared-ses auto-detect logic (Plan 84-07)."
  default     = true
}

variable "tags" {
  type        = map(string)
  description = "Tags propagated to all resources created by this module."
  default     = {}
}
