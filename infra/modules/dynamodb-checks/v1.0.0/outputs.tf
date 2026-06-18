output "table_name" {
  description = "Name of the checks DynamoDB table."
  value       = aws_dynamodb_table.checks.name
}

output "table_arn" {
  description = "ARN of the checks DynamoDB table."
  value       = aws_dynamodb_table.checks.arn
}
