output "table_name" {
  description = "Name of the Slack threads DynamoDB table."
  value       = aws_dynamodb_table.slack_threads.name
}

output "table_arn" {
  description = "ARN of the Slack threads DynamoDB table."
  value       = aws_dynamodb_table.slack_threads.arn
}
