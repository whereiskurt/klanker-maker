output "replica_bucket_arn" {
  description = "ARN of the S3 replica bucket"
  value       = aws_s3_bucket.replica.arn
}

output "replica_bucket_name" {
  description = "Name of the S3 replica bucket"
  value       = aws_s3_bucket.replica.id
}

output "replication_role_arn" {
  description = "ARN of the IAM role used for S3 replication"
  value       = aws_iam_role.replication.arn
}
