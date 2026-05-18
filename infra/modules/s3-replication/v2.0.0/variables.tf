variable "resource_prefix" {
  type        = string
  default     = "km"
  description = "Resource-name prefix. Default 'km' renders byte-identical names to v1.0.0."
  validation {
    condition     = can(regex("^[a-z][a-z0-9]{0,11}$", var.resource_prefix))
    error_message = "resource_prefix must be 1-12 chars, start with a lowercase letter, contain only [a-z0-9]."
  }
}

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
