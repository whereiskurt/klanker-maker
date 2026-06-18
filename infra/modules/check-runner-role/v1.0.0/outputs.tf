output "role_arn" {
  description = "ARN of the shared check-runner Lambda execution role."
  value       = aws_iam_role.check_runner.arn
}

output "role_name" {
  description = "Name of the shared check-runner Lambda execution role."
  value       = aws_iam_role.check_runner.name
}
