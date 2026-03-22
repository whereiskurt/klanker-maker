variable "source_bucket_name" {
  description = "The primary artifact bucket name (e.g. km-sandbox-artifacts-ea554771)"
  type        = string
}

variable "source_bucket_arn" {
  description = "ARN of the source artifact bucket"
  type        = string
}

variable "destination_region" {
  description = "AWS region for the replica bucket (e.g. us-west-2)"
  type        = string
}

variable "destination_bucket_name" {
  description = "Name for the replica bucket (e.g. km-sandbox-artifacts-ea554771-replica)"
  type        = string
}
