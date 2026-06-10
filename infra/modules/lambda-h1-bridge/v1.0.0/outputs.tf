output "function_name" {
  description = "Name of the km-h1-bridge Lambda function"
  value       = aws_lambda_function.h1_bridge.function_name
}

output "function_arn" {
  description = "ARN of the km-h1-bridge Lambda function"
  value       = aws_lambda_function.h1_bridge.arn
}

output "function_url" {
  description = "Lambda Function URL for the H1 bridge. Paste this as the HackerOne program webhook URL (km h1 init reads it from SSM after km init --dry-run=false stores it)."
  value       = aws_lambda_function_url.h1_bridge.function_url
}

output "lambda_role_arn" {
  description = "ARN of the km-h1-bridge Lambda execution IAM role"
  value       = aws_iam_role.h1_bridge.arn
}
