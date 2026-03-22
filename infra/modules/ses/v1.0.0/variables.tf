variable "domain" {
  type        = string
  description = "The email subdomain for sandbox addresses, e.g. 'sandboxes.klankermaker.ai'"
}

variable "route53_zone_id" {
  type        = string
  description = "Route53 hosted zone ID where DNS records (DKIM CNAME, TXT, MX) will be created"
}

variable "artifact_bucket_name" {
  type        = string
  description = "S3 bucket name for storing inbound email, e.g. 'km-sandbox-artifacts-ea554771'"
}

variable "artifact_bucket_arn" {
  type        = string
  description = "ARN of the artifact S3 bucket, used in the bucket policy allowing SES writes"
}
