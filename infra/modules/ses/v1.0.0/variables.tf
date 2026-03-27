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

variable "email_create_handler_arn" {
  type        = string
  description = "ARN of the email-create-handler Lambda. When non-empty, the create-inbound receipt rule and S3 notification are created to route create@ emails to the mail/create/ prefix."
  default     = ""
}
