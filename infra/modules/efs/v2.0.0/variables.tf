variable "vpc_id" {
  description = "ID of the shared VPC to place mount targets in."
  type        = string
}

variable "subnet_ids" {
  description = "List of subnet IDs (one per AZ) in which to create EFS mount targets."
  type        = list(string)
}

variable "km_label" {
  description = "Short platform label (e.g. 'klanker-maker') used for resource naming."
  type        = string
}

variable "region_label" {
  description = "Short region label (e.g. 'use1') used for resource naming and creation tokens."
  type        = string
}

variable "resource_prefix" {
  type        = string
  default     = "km"
  description = "Resource-name prefix. Default 'km' renders byte-identical names to v1.0.0."
  validation {
    condition     = can(regex("^[a-z][a-z0-9]{0,11}$", var.resource_prefix))
    error_message = "resource_prefix must be 1-12 chars, start with a lowercase letter, contain only [a-z0-9]."
  }
}
