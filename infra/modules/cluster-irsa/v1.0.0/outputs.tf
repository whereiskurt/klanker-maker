output "role_arn" {
  value       = aws_iam_role.cluster_irsa.arn
  description = "ARN of the cross-account IRSA role for use in k8s ServiceAccount annotations"
}

output "role_name" {
  value       = aws_iam_role.cluster_irsa.name
  description = "Name of the IAM role"
}

output "oidc_provider_arn" {
  value       = aws_iam_openid_connect_provider.this.arn
  description = "ARN of the locally-registered OIDC provider mirroring the remote cluster issuer (same account as the IAM role — required for STS token validation)"
}
