# Cluster-specific variables
variable "cluster_name" {
  description = "Name of the k8s cluster (used in IAM role name: {resource_prefix}-cluster-{cluster_name})"
  type        = string
}

variable "oidc_provider_arn" {
  description = "ARN naming the cluster's OIDC issuer (e.g. arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE). The account portion is informational — the module derives the issuer URL from this ARN and registers a LOCAL `aws_iam_openid_connect_provider` in the current account (STS requires the provider to live in the same account as the IAM role). Single stack per issuer URL: re-using the same URL across multiple cluster-irsa stacks will fail on duplicate provider — use wildcard namespace for multi-SA scenarios."
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
