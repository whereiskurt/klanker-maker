output "table_name" {
  description = "Name of the DynamoDB identity tracking table."
  value       = aws_dynamodb_table.identities.name
}

output "table_arn" {
  description = "ARN of the DynamoDB identity tracking table."
  value       = aws_dynamodb_table.identities.arn
}
