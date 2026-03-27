output "lambda_function_arn" {
  description = "ARN of the email handler Lambda — pass to SES module's email_create_handler_arn"
  value       = aws_lambda_function.email_handler.arn
}

output "lambda_function_name" {
  description = "Name of the email handler Lambda function"
  value       = aws_lambda_function.email_handler.function_name
}

output "lambda_role_arn" {
  description = "ARN of the Lambda execution IAM role"
  value       = aws_iam_role.email_handler.arn
}
