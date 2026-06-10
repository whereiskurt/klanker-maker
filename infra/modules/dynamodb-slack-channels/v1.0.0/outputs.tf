output "table_name" {
  description = "Name of the Slack channels DynamoDB table."
  value       = aws_dynamodb_table.slack_channels.name
}

output "table_arn" {
  description = "ARN of the Slack channels DynamoDB table."
  value       = aws_dynamodb_table.slack_channels.arn
}
