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
  description = "Sandbox identifier for tagging all resources"
}

variable "ec2spots" {
  type = list(object({
    count                  = number
    region                 = string
    sandbox_id             = string
    instance_type          = string
    spot_price_multiplier  = optional(number, 1.00)
    spot_price_offset      = optional(number, 0.0005)
    block_duration_minutes = optional(number, 0)
    user_data              = optional(string, "")
  }))
  description = "List of EC2 spot instance configurations per region"
  default     = []
}

variable "vpc_id" {
  type        = string
  description = "VPC ID where EC2 spot instances will be created. If empty, a per-sandbox VPC is created."
  default     = ""
}

variable "public_subnets" {
  type        = list(string)
  description = "List of public subnet IDs. If empty, subnets are created in the per-sandbox VPC."
  default     = []
}

variable "availability_zones" {
  type        = list(string)
  description = "List of availability zones. If empty, first two AZs in the region are used."
  default     = []
}

variable "sg_egress_rules" {
  type = list(object({
    from_port   = number
    to_port     = number
    protocol    = string
    cidr_blocks = list(string)
    description = string
  }))
  description = "Security group egress rules compiled from profile. Empty list = no egress allowed."
  default     = []
}

variable "iam_session_policy" {
  type = object({
    max_session_duration = optional(number, 3600)
    allowed_regions      = optional(list(string), [])
  })
  description = "IAM session policy constraints compiled from profile."
  default     = {}
}
