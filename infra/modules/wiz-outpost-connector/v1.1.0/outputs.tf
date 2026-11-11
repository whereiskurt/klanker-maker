output "access_role_arn" {
  description = "Wiz access role ARN (paste into the Wiz connector)"
  value       = aws_cloudformation_stack.this.outputs["WizAccessRoleARNID"]
}

output "scanner_role_arn" {
  description = "Wiz scanner role ARN"
  value       = aws_cloudformation_stack.this.outputs["WizScannerRoleARNID"]
}

output "external_id" {
  description = "External ID for the Wiz connector"
  value       = aws_cloudformation_stack.this.outputs["ExternalID"]
}

output "template_version" {
  description = "WizVersionLastUpdated parameter the stack was deployed with"
  value       = aws_cloudformation_stack.this.parameters["WizVersionLastUpdated"]
}

output "stack_name" {
  description = "CloudFormation stack name"
  value       = aws_cloudformation_stack.this.name
}
