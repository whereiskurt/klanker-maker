output "domain_identity_arn" {
  description = "ARN of the SES domain identity for sandboxes"
  value       = aws_ses_domain_identity.sandbox.arn
}

output "domain_identity_verification_token" {
  description = "SES domain verification token (useful for debugging DNS propagation)"
  value       = aws_ses_domain_identity.sandbox.verification_token
}

output "receipt_rule_set_name" {
  description = "Name of the SES receipt rule set handling inbound sandbox email"
  value       = aws_ses_receipt_rule_set.km_sandbox.rule_set_name
}
