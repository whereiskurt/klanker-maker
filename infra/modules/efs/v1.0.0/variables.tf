variable "vpc_id" {
  description = "ID of the shared VPC to place mount targets in."
  type        = string
}

variable "subnet_ids" {
  description = "List of subnet IDs (one per AZ) in which to create EFS mount targets."
  type        = list(string)
}

variable "km_label" {
  description = "Short platform label (e.g. 'klankrmkr') used for resource naming."
  type        = string
}

variable "region_label" {
  description = "Short region label (e.g. 'use1') used for resource naming and creation tokens."
  type        = string
}
