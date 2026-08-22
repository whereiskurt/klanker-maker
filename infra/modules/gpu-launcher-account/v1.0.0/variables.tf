variable "resource_prefix" {
  description = "km resource prefix (matches the trusting install's resource_prefix; used to name every resource this module creates: {prefix}-gpu-launcher, {prefix}-gpu-box, {prefix}-results-<account-id>)."
  type        = string
  default     = "km"
}

variable "trust_account_id" {
  description = "Account A's (the trusting/home application account's) AWS account id. Used only to build the account-root ARN for the optional pattern-based trust statement — the exact-ARN statement below needs full role ARNs instead, since IAM principal ARNs do not accept wildcards."
  type        = string
}

variable "trusted_principal_arns" {
  description = "Exact IAM role ARNs in account A that may assume the launcher role: the *-create-handler Lambda role, the *-ttl-handler Lambda role, and/or the operator's own role when it resolves to an exact ARN. All three are meant to be supplied up front, from the first apply, so no second privileged trip through this account's admin credentials is ever required."
  type        = list(string)
  default     = []
}

variable "trusted_principal_arn_patterns" {
  description = "ArnLike glob patterns (e.g. containing a wildcard) matched against the calling principal's ARN — the escape hatch for a principal whose exact ARN is not knowable in advance, such as an IAM Identity Center role. When non-empty, an ADDITIONAL trust statement is added whose Principal is account A's root, which is broader than the exact-ARN statement and therefore opt-in (default empty). Always paired with the external-id condition and this ArnLike test together."
  type        = list(string)
  default     = []
}

variable "external_id" {
  description = "STS ExternalId required on every AssumeRole into the launcher role (confused-deputy protection). Minted once at enrollment and stored only as an SSM SecureString in the trusting install — this module never emits it as an output."
  type        = string
  sensitive   = true

  validation {
    condition     = length(var.external_id) > 0
    error_message = "external_id must not be empty: every launcher trust statement requires it, and an empty value would silently emit a condition-free (and therefore unbounded) trust policy."
  }
}

variable "region" {
  description = "AWS region this module's resources are provisioned in — must match the region the enrollment command's provider targets."
  type        = string
}

variable "instance_types" {
  description = "Allowlisted EC2 instance types the launcher role may RunInstances (e.g. GPU families like g6e.12xlarge). The launcher cannot launch anything outside this list."
  type        = list(string)
}

variable "provision_network" {
  description = "When true, this module provisions its own lean VPC/subnets/route table/security group — one public subnet per AZ, no NAT concept (this account's subnets are always public by design). When false, var.subnet_id and var.security_group_id are passed straight through to the module outputs so an operator can reuse an existing network."
  type        = bool
  default     = false
}

variable "subnet_id" {
  description = "Existing subnet id to reuse when provision_network = false. Ignored when provision_network = true."
  type        = string
  default     = ""
}

variable "security_group_id" {
  description = "Existing security group id to reuse when provision_network = false. Ignored when provision_network = true."
  type        = string
  default     = ""
}

variable "az_count" {
  description = "Number of availability zones to spread the provisioned network across (one subnet per AZ), when provision_network = true. Clamped to however many AZs the region actually reports as available."
  type        = number
  default     = 2
}

variable "vpc_cidr" {
  description = "CIDR block for the provisioned VPC, when provision_network = true."
  type        = string
  default     = "10.90.0.0/16"
}

variable "provision_efs" {
  description = "When true, provisions a B-local EFS filesystem (plus one mount target per subnet and an NFS ingress rule) as B-internal shared working storage — e.g. a shared weights cache for a multi-box GPU fleet. Never crosses the account boundary. Off by default."
  type        = bool
  default     = false
}

variable "enable_bedrock" {
  description = "When true, grants the box role bedrock:InvokeModel[WithResponseStream] against this account's Bedrock. Off by default — Bedrock traffic from a lean box in this account is unmetered by design (no http-proxy MITM sidecar / budget table here, unlike the home account)."
  type        = bool
  default     = false
}

variable "artifacts_bucket_arn" {
  description = "ARN of account A's artifacts S3 bucket (sidecars/toolchain). The box role is granted read-only (GetObject) access to it — the one inbound cross-account data path. Account A's own bucket policy must separately grant this module's box role (see the module README); this variable only shapes the box role's own inline policy."
  type        = string
}
