output "table_name" {
  description = "Name of the Slack stream messages DynamoDB table."
  value       = aws_dynamodb_table.slack_stream_messages.name
}

output "table_arn" {
  description = "ARN of the Slack stream messages DynamoDB table."
  value       = aws_dynamodb_table.slack_stream_messages.arn
}
