output "operator_inbound_rule_name" {
  value = aws_ses_receipt_rule.operator_inbound.name
}

output "sandbox_catchall_rule_name" {
  value = aws_ses_receipt_rule.sandbox_catchall.name
}
