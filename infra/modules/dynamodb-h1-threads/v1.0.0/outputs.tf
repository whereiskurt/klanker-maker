output "table_name" {
  description = "Name of the HackerOne threads DynamoDB table."
  value       = aws_dynamodb_table.h1_threads.name
}

output "table_arn" {
  description = "ARN of the HackerOne threads DynamoDB table."
  value       = aws_dynamodb_table.h1_threads.arn
}
