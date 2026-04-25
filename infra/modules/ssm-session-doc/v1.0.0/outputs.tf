output "document_name" {
  description = "Name of the KM-Sandbox-Session SSM document."
  value       = aws_ssm_document.km_sandbox_session.name
}

output "document_arn" {
  description = "ARN of the KM-Sandbox-Session SSM document."
  value       = aws_ssm_document.km_sandbox_session.arn
}
