output "table_name" {
  description = "Name of the DynamoDB budget tracking table."
  value       = aws_dynamodb_table.budget.name
}

output "table_arn" {
  description = "ARN of the DynamoDB budget tracking table."
  value       = aws_dynamodb_table.budget.arn
}

output "stream_arn" {
  description = "ARN of the DynamoDB Streams endpoint for Lambda budget enforcement triggers."
  value       = aws_dynamodb_table.budget.stream_arn
}
