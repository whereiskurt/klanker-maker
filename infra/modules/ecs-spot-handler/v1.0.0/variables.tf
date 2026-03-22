variable "ecs_cluster_arn" {
  description = "ARN of the ECS cluster to monitor for spot interruptions"
  type        = string
}

variable "artifact_bucket_name" {
  description = "Name of the S3 bucket for artifact uploads (e.g. km-sandbox-artifacts-ea554771)"
  type        = string
}

variable "artifact_bucket_arn" {
  description = "ARN of the S3 artifact bucket for IAM policy"
  type        = string
}
