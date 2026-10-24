output "table_name" {
  value = aws_dynamodb_table.slack_history.name
}
output "table_arn" {
  value = aws_dynamodb_table.slack_history.arn
}
