# One output per link-record field on internal/app/config.LaunchAccountConfig
# (see the README's field-to-output mapping table). No output is declared
# sensitive — nothing here carries a secret. The one secret this module's
# resources depend on, the STS ExternalId, is never emitted as an output; it
# lives only as an SSM SecureString in the trusting install (`km account add`
# writes it there, not here).

output "launcher_role_arn" {
  value       = aws_iam_role.launcher.arn
  description = "ARN of the bounded cross-account launcher role. -> LaunchAccountConfig.LauncherRoleARN"
}

output "box_role_arn" {
  value       = aws_iam_role.box.arn
  description = "ARN of the pre-baked, permissions-boundaried box role attached to launched instances. -> LaunchAccountConfig.BoxRoleARN"
}

output "subnet_ids" {
  value       = local.subnet_ids
  description = "One subnet id per availability zone (or the single reused subnet when provision_network = false), index-parallel with availability_zones. -> LaunchAccountConfig.SubnetIDs"
}

output "availability_zones" {
  value       = local.availability_zones
  description = "Availability zone for subnet_ids[i] at the SAME index — the create path pairs them by index. -> LaunchAccountConfig.AvailabilityZones"
}

output "security_group_id" {
  value       = local.security_group_id
  description = "Security group id attached to launched boxes. -> LaunchAccountConfig.SecurityGroupID"
}

output "results_bucket" {
  value       = aws_s3_bucket.results.id
  description = "This account's results bucket name — the B-local write target that account A reads read-only. -> LaunchAccountConfig.ResultsBucket"
}

output "efs_id" {
  value       = var.provision_efs ? aws_efs_file_system.launcher[0].id : ""
  description = "EFS filesystem id, or empty string when provision_efs = false. -> LaunchAccountConfig.EFSID"
}

output "account_id" {
  value       = local.account_id
  description = "This account's (account B's) AWS account id. -> LaunchAccountConfig.AccountID"
}

output "region" {
  value       = var.region
  description = "Region this module's resources were provisioned in. -> LaunchAccountConfig.Region"
}
