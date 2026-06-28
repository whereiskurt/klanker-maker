output "table_name" {
  description = "Name of the capacity DynamoDB table."
  value       = aws_dynamodb_table.capacity.name
}

output "table_arn" {
  description = "ARN of the capacity DynamoDB table."
  value       = aws_dynamodb_table.capacity.arn
}
