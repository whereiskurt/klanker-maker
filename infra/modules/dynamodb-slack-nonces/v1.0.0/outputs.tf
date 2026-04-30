output "table_name" {
  description = "Name of the DynamoDB nonce table."
  value       = aws_dynamodb_table.nonces.name
}

output "table_arn" {
  description = "ARN of the DynamoDB nonce table."
  value       = aws_dynamodb_table.nonces.arn
}
