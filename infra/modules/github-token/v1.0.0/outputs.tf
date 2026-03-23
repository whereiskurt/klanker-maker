output "lambda_function_arn" {
  description = "ARN of the github-token-refresher Lambda function"
  value       = aws_lambda_function.github_token_refresher.arn
}

output "kms_key_arn" {
  description = "ARN of the KMS key used to encrypt the GitHub token in SSM"
  value       = aws_kms_key.github_token.arn
}

output "kms_key_id" {
  description = "Key ID of the KMS key used to encrypt the GitHub token in SSM"
  value       = aws_kms_key.github_token.key_id
}
