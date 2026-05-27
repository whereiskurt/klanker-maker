output "alias_name" {
  description = "KMS alias name (alias/{prefix}-sandbox-secrets)"
  value       = var.register_secrets_key ? aws_kms_alias.secrets[0].name : ""
}

output "key_arn" {
  description = "KMS key ARN"
  value       = var.register_secrets_key ? aws_kms_key.secrets[0].arn : ""
}

output "key_id" {
  description = "KMS key ID"
  value       = var.register_secrets_key ? aws_kms_key.secrets[0].key_id : ""
}
