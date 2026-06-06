output "function_name" {
  description = "Name of the km-github-bridge Lambda function"
  value       = aws_lambda_function.github_bridge.function_name
}

output "function_arn" {
  description = "ARN of the km-github-bridge Lambda function"
  value       = aws_lambda_function.github_bridge.arn
}

output "function_url" {
  description = "Lambda Function URL for the GitHub bridge. Set this as the GitHub App webhook URL (km github init reads it from SSM after km init --dry-run=false stores it)."
  value       = aws_lambda_function_url.github_bridge.function_url
}

output "lambda_role_arn" {
  description = "ARN of the km-github-bridge Lambda execution IAM role"
  value       = aws_iam_role.github_bridge.arn
}
