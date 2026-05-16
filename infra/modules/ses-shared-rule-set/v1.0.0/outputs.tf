output "rule_set_name" {
  description = "Name of the singleton SES receipt rule set. Constant regardless of register_shared_rule_set — downstream consumers reference by string name."
  value       = var.rule_set_name
}

output "email_domain" {
  description = "Email domain passed through from input for downstream convenience."
  value       = var.email_domain
}

output "domain_identity_arn" {
  description = "ARN of the SES domain identity. Null when register_domain_identity=false — downstream consumers should not rely on this output in that case."
  value       = try(aws_ses_domain_identity.sandbox[0].arn, null)
}
