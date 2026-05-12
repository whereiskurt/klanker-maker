output "role_arn" {
  value       = aws_iam_role.cluster_irsa.arn
  description = "ARN of the cross-account IRSA role for use in k8s ServiceAccount annotations"
}

output "role_name" {
  value       = aws_iam_role.cluster_irsa.name
  description = "Name of the IAM role"
}

output "oidc_provider_arn" {
  value       = local.oidc_provider_arn_local
  description = "ARN of the OIDC provider used as the trust Principal. When register_oidc_provider=true, this is the locally-registered provider created by this module. When register_oidc_provider=false, this is the existing provider looked up via data source."
}
