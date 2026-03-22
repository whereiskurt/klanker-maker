output "lambda_arn" {
  description = "ARN of the budget-enforcer Lambda function"
  value       = aws_lambda_function.budget_enforcer.arn
}

output "lambda_function_name" {
  description = "Name of the budget-enforcer Lambda function"
  value       = aws_lambda_function.budget_enforcer.function_name
}

output "schedule_name" {
  description = "Name of the EventBridge Scheduler schedule (used to delete it on sandbox destroy)"
  value       = aws_scheduler_schedule.budget_check.name
}

output "lambda_role_arn" {
  description = "ARN of the Lambda execution IAM role"
  value       = aws_iam_role.budget_enforcer.arn
}
