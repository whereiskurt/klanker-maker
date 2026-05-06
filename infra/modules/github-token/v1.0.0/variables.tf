variable "sandbox_id" {
  description = "Unique sandbox identifier (e.g. sb-a1b2c3d4)"
  type        = string
}

variable "resource_prefix" {
  description = "Phase-66 multi-instance resource prefix (e.g. 'km', 'kph'). Applied to IAM resource names + the Lambda's KM_GITHUB_SSM_CONFIG_PREFIX env var so the per-sandbox github-token-refresher reads from /{prefix}/config/github/* and the create-handler's iam:CreateRole policy doesn't deny the role create."
  type        = string
  default     = "km"
}

variable "lambda_zip_path" {
  description = "Path to the compiled Go Lambda bootstrap zip file (github-token-refresher binary)"
  type        = string
}

variable "installation_id" {
  description = "GitHub App installation ID for the target organization/repository"
  type        = string
}

variable "ssm_parameter_name" {
  description = "SSM parameter path where the GitHub token is written after each refresh"
  type        = string
  default     = ""
}

variable "allowed_repos" {
  description = "List of repository full names (owner/repo) the token is scoped to"
  type        = list(string)
  default     = []
}

variable "permissions" {
  description = "JSON-encoded GitHub App permissions map (e.g. '{\"contents\":\"read\"}')"
  type        = string
  default     = "{}"
}

variable "sandbox_iam_role_arn" {
  description = "IAM role ARN of the sandbox execution role — granted KMS decrypt so the sandbox can read the token"
  type        = string
}
