output "table_name" {
  description = "Name of the DynamoDB sandbox metadata table."
  value       = aws_dynamodb_table.sandboxes.name
}

output "table_arn" {
  description = "ARN of the DynamoDB sandbox metadata table."
  value       = aws_dynamodb_table.sandboxes.arn
}
