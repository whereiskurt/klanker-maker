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
  description = "Security group ID for EC2 spot instances"
  value       = try(aws_security_group.ec2spot[0].id, "")
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
  description = "IAM instance profile name for EC2 instances"
  value       = try(aws_iam_instance_profile.ec2spot[0].name, "")
}
