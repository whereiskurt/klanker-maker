variable "km_label" {
  type        = string
  description = "Klanker Maker site label (e.g. 'km')"
}

variable "region_label" {
  type        = string
  description = "Short AWS region label (e.g. 'use1', 'usw2')"
}

variable "sandbox_id" {
  type        = string
  description = "Sandbox identifier for tagging all resources"
}

variable "vpc" {
  type = object({
    cidr_block              = string
    enable_dns_hostnames    = optional(bool, true)
    enable_dns_support      = optional(bool, true)
    public_subnets_cidr     = list(string)
    private_subnets_cidr    = list(string)
    availability_zone_count = number
    tags                    = optional(map(string), {})
  })
  description = "VPC configuration including CIDR blocks and subnets"
}

variable "nat_gateway" {
  type = object({
    enabled = optional(bool, false)
  })
  description = "NAT Gateway configuration"
  default = {
    enabled = false
  }
}
