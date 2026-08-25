output "ec2spot_instances" {
  description = "Map of EC2 spot instance details"
  value = {
    for k, v in aws_spot_instance_request.ec2spot :
    k => {
      instance_id       = v.spot_instance_id
      public_ip         = v.public_ip
      private_ip        = v.private_ip
      availability_zone = v.availability_zone
      instance_type     = v.instance_type
    }
  }
}

output "ec2spot_security_group_id" {
  description = "Security group ID actually attached to the EC2 instances — the per-sandbox group this module created, or the pre-provisioned link group when launching cross-account (Phase 126)."
  value       = local.effective_security_group_id
}

output "ec2_ondemand_instances" {
  description = "Map of EC2 on-demand instance details"
  value = {
    for k, v in aws_instance.ec2_ondemand :
    k => {
      instance_id       = v.id
      public_ip         = v.public_ip
      private_ip        = v.private_ip
      availability_zone = v.availability_zone
      instance_type     = v.instance_type
    }
  }
}

output "iam_instance_profile_name" {
  description = "IAM instance profile actually attached to the EC2 instances — the per-sandbox profile this module created, or the pre-provisioned link box profile when launching cross-account (Phase 126)."
  value       = local.effective_instance_profile
}

output "iam_role_arn" {
  description = "IAM role ARN for EC2 spot instances (consumed by budget-enforcer dependency block)"
  value       = try(aws_iam_role.ec2spot_ssm[0].arn, "")
}
