output "table_name" {
  description = "Name of the DynamoDB schedule metadata table."
  value       = aws_dynamodb_table.schedules.name
}

output "table_arn" {
  description = "ARN of the DynamoDB schedule metadata table."
  value       = aws_dynamodb_table.schedules.arn
}
