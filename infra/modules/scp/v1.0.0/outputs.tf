output "policy_id" {
  description = "The unique ID of the SCP (used for policy attachment and cross-references)."
  value       = aws_organizations_policy.sandbox_containment.id
}

output "policy_arn" {
  description = "The ARN of the SCP."
  value       = aws_organizations_policy.sandbox_containment.arn
}
