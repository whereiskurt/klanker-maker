output "role_arn" {
  value       = aws_iam_role.cluster_irsa.arn
  description = "ARN of the cross-account IRSA role for use in k8s ServiceAccount annotations"
}

output "role_name" {
  value       = aws_iam_role.cluster_irsa.name
  description = "Name of the IAM role"
}
