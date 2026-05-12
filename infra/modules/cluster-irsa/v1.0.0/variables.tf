# Cluster-specific variables
variable "cluster_name" {
  description = "Name of the k8s cluster (used in IAM role name: {resource_prefix}-cluster-{cluster_name})"
  type        = string
}

variable "oidc_provider_arn" {
  description = "ARN naming the cluster's OIDC issuer (e.g. arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE). The account portion is informational — the module derives the issuer URL from this ARN. When register_oidc_provider=true (default), the module creates a local aws_iam_openid_connect_provider. When register_oidc_provider=false, the module references an existing provider (same-account or multi-stack reuse)."
  type        = string
}

variable "namespace" {
  description = "Kubernetes namespace for the service account; supports wildcard '*' for StringLike condition"
  type        = string
}

variable "service_account_name" {
  description = "Kubernetes service account name; supports wildcard '*' for StringLike condition"
  type        = string
}

variable "register_oidc_provider" {
  description = "When true (default), the module creates a new aws_iam_openid_connect_provider mirroring the cluster issuer URL in the klanker account. When false, the module references an existing provider via data source (same-account scenario, or multi-stack against the same EKS issuer). Set by km cluster add auto-detect logic; override with --register-oidc-provider=true|false."
  type        = bool
  default     = true
}

# Passthrough variables to km-operator-policy module (names must match km-operator-policy/v1.0.0/variables.tf verbatim)
variable "resource_prefix" {
  description = "Prefix for all resource names (default: km)"
  type        = string
}

variable "state_bucket" {
  description = "S3 bucket holding Terraform state for sandbox modules"
  type        = string
}

variable "artifact_bucket_arn" {
  description = "ARN of the S3 artifact bucket for IAM policy scoping"
  type        = string
}

variable "dynamodb_table_name" {
  description = "DynamoDB table name for Terraform state locking"
  type        = string
}

variable "dynamodb_budget_table_arn" {
  description = "ARN of the DynamoDB budget table for IAM policy scoping"
  type        = string
}

variable "sandbox_table_name" {
  description = "Name of the DynamoDB sandbox metadata table"
  type        = string
}

variable "identities_table_name" {
  description = "Name of the DynamoDB identities table"
  type        = string
}
