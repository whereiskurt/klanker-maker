output "event_rule_arn" {
  description = "ARN of the EventBridge rule that watches for ECS Fargate Spot interruption events"
  value       = aws_cloudwatch_event_rule.ecs_spot_interruption.arn
}

output "lambda_function_arn" {
  description = "ARN of the spot handler Lambda function"
  value       = aws_lambda_function.spot_handler.arn
}

output "lambda_role_arn" {
  description = "ARN of the Lambda execution IAM role"
  value       = aws_iam_role.spot_handler.arn
}
