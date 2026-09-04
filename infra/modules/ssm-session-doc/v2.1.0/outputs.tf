output "document_name" {
  description = "Name of the per-install SSM Session Manager document (e.g. km-Sandbox-Session, tg-Sandbox-Session)."
  value       = aws_ssm_document.sandbox_session.name
}

output "document_arn" {
  description = "ARN of the per-install SSM Session Manager document."
  value       = aws_ssm_document.sandbox_session.arn
}
