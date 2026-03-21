variable "km_label" {
  type        = string
  description = "Klanker Maker site label (e.g. 'km')"
}

variable "km_random_suffix" {
  type        = string
  description = "Random suffix for globally-unique IAM resource names"
  default     = ""
}

variable "region_label" {
  type        = string
  description = "Short AWS region label (e.g. 'use1', 'usw2')"
}

variable "region_full" {
  type        = string
  description = "Full AWS region name (e.g. 'us-east-1')"
}

variable "sandbox_id" {
  type        = string
  description = "Sandbox identifier for resource naming and tagging"
}

variable "vpc_id" {
  type        = string
  description = "VPC ID for service discovery namespace"
}

variable "ecs_clusters" {
  type = list(object({
    name            = string
    region          = string
    enable_insights = optional(bool, false)
    namespace_name  = optional(string, "")
  }))
  description = "List of ECS cluster configurations"
  default     = []
}
