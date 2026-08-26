output "function_name" {
  description = "Name of the km-webhook-bridge Lambda function"
  value       = aws_lambda_function.webhook_bridge.function_name
}

output "function_arn" {
  description = "ARN of the km-webhook-bridge Lambda function"
  value       = aws_lambda_function.webhook_bridge.arn
}

output "function_url" {
  description = "Lambda Function URL for the webhook bridge. Paste <this>/{source-name} as the integration's webhook URL (e.g. a Wiz Automation Rule). km init records this at SSM {prefix}config/webhooks/bridge-url — there is no dedicated CLI verb for this bridge."
  value       = aws_lambda_function_url.webhook_bridge.function_url
}

output "lambda_role_arn" {
  description = "ARN of the km-webhook-bridge Lambda execution IAM role"
  value       = aws_iam_role.webhook_bridge.arn
}
