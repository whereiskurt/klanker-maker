output "table_name" {
  description = "Name of the action-quota DynamoDB table."
  value       = aws_dynamodb_table.action_quota.name
}

output "table_arn" {
  description = "ARN of the action-quota DynamoDB table."
  value       = aws_dynamodb_table.action_quota.arn
}

output "stream_arn" {
  description = "ARN of the DynamoDB Streams endpoint — used as the event-source mapping for the km-quota-alerter Lambda (plan 09)."
  value       = aws_dynamodb_table.action_quota.stream_arn
}
