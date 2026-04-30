output "function_name" {
  description = "Name of the km-slack-bridge Lambda function"
  value       = aws_lambda_function.slack_bridge.function_name
}

output "function_arn" {
  description = "ARN of the km-slack-bridge Lambda function"
  value       = aws_lambda_function.slack_bridge.arn
}

output "function_url" {
  description = "Lambda Function URL for the Slack bridge. Plan 07 (km slack init) reads this and stores it at SSM /km/slack/bridge-url."
  value       = aws_lambda_function_url.slack_bridge.function_url
}

output "lambda_role_arn" {
  description = "ARN of the km-slack-bridge Lambda execution IAM role"
  value       = aws_iam_role.slack_bridge.arn
}
