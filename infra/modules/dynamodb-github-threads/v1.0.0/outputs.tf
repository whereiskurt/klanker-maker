output "table_name" {
  description = "Name of the GitHub threads DynamoDB table."
  value       = aws_dynamodb_table.github_threads.name
}

output "table_arn" {
  description = "ARN of the GitHub threads DynamoDB table."
  value       = aws_dynamodb_table.github_threads.arn
}
