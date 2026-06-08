output "github_dlq_arn" {
  description = "ARN of the shared GitHub inbound FIFO DLQ (target of per-sandbox github-inbound redrivePolicy)."
  value       = aws_sqs_queue.github_inbound_dlq.arn
}

output "slack_dlq_arn" {
  description = "ARN of the shared Slack inbound FIFO DLQ (target of per-sandbox slack-inbound redrivePolicy)."
  value       = aws_sqs_queue.slack_inbound_dlq.arn
}

output "github_dlq_url" {
  description = "URL of the shared GitHub inbound FIFO DLQ."
  value       = aws_sqs_queue.github_inbound_dlq.id
}

output "slack_dlq_url" {
  description = "URL of the shared Slack inbound FIFO DLQ."
  value       = aws_sqs_queue.slack_inbound_dlq.id
}
