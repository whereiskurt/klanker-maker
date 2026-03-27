output "lambda_function_arn" {
  description = "ARN of the create-handler Lambda function"
  value       = aws_lambda_function.create_handler.arn
}

output "lambda_role_arn" {
  description = "ARN of the create-handler IAM role. Add this to the Phase 10 SCP trusted_arns_iam list to allow sandbox provisioning actions outside the SCP deny boundary."
  value       = aws_iam_role.create_handler.arn
}

output "event_rule_arn" {
  description = "ARN of the EventBridge rule that routes SandboxCreate events to this Lambda"
  value       = aws_cloudwatch_event_rule.sandbox_create.arn
}
