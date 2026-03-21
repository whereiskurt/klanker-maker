# KMS key outputs
output "kms_key_arn" {
  description = "ARN of the KMS key used for SSM parameter encryption"
  value       = aws_kms_key.ssm.arn
}

output "kms_key_alias" {
  description = "Alias of the KMS key"
  value       = aws_kms_alias.ssm.name
}

# SSM Parameter ARNs
# Format: { "github/client_id" = "arn:aws:ssm:region:account:parameter/km/sandboxes/use1/github/client_id" }
output "ssm_parameter_arns" {
  description = "Map of secret/key to SSM parameter ARNs"
  value = {
    for key, param in aws_ssm_parameter.secret : key => param.arn
  }
}

# SSM Parameter names (for ECS valueFrom)
output "ssm_parameter_names" {
  description = "Map of secret/key to SSM parameter names (for ECS valueFrom)"
  value = {
    for key, param in aws_ssm_parameter.secret : key => param.name
  }
}

# Summary
output "summary" {
  description = "Summary of created secrets"
  value = {
    region         = var.region_full
    sandbox_id     = var.sandbox_id
    ssm_count      = length(aws_ssm_parameter.secret)
    ssm_prefix     = local.ssm_prefix
    kms_key_arn    = aws_kms_key.ssm.arn
  }
}
