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
  description = "When true (default), this module MANAGES the shared SES receipt rule set + active pointer. When false, no rule-set resources are created — assumes the resource exists and is managed elsewhere (sibling install or out-of-band). Phase 84.1: semantics changed from 'create only on first apply' to 'manage this resource'. Once foundation owns the resource in state, this flag stays true; flipping to false intentionally orphans the resource (which prevent_destroy on aws_ses_receipt_rule_set.shared then blocks at the terraform layer)."
  default     = true
}

variable "register_domain_identity" {
  type        = bool
  description = "When true (default), this module MANAGES the SES domain identity + DKIM + MX + verification records. When false, the module does not declare those resources — assumes they are managed elsewhere. Phase 84.1: same semantic change as register_shared_rule_set — 'manage this resource', not 'create only on first apply'. Auto-detect in km bootstrap (cmd/bootstrap.go detectSharedSESState) prefers foundation state ownership over AWS reality so re-runs are idempotent."
  default     = true
}

variable "tags" {
  type        = map(string)
  description = "Tags propagated to all resources created by this module."
  default     = {}
}
