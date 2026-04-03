output "filesystem_id" {
  description = "The ID of the EFS shared filesystem."
  value       = aws_efs_file_system.shared.id
}

output "security_group_id" {
  description = "The ID of the EFS NFS security group."
  value       = aws_security_group.efs.id
}
