output "lambda_function_arn" {
  description = "ARN of the TTL handler Lambda function. Set KM_TTL_LAMBDA_ARN to this value."
  value       = aws_lambda_function.ttl_handler.arn
}

output "lambda_function_name" {
  description = "Name of the TTL handler Lambda function"
  value       = aws_lambda_function.ttl_handler.function_name
}

output "lambda_role_arn" {
  description = "ARN of the Lambda execution IAM role"
  value       = aws_iam_role.ttl_handler.arn
}

output "scheduler_role_arn" {
  description = "ARN of the EventBridge Scheduler execution role (used by km create for TTL schedules)"
  value       = aws_iam_role.scheduler_invoke.arn
}
