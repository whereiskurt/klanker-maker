variable "km_label" {
  type        = string
  description = "Klanker Maker site label (e.g. 'km')"
}

variable "km_random_suffix" {
  type        = string
  description = "Random suffix for globally-unique resource names"
  default     = ""
}

variable "region_label" {
  type        = string
  description = "Short AWS region label (e.g. 'use1')"
}

variable "region_full" {
  type        = string
  description = "Full AWS region name (e.g. 'us-east-1')"
}

variable "sandbox_id" {
  type        = string
  description = "Sandbox identifier for tagging all resources"
}

variable "vpc_id" {
  type        = string
  description = "VPC ID from shared network (km init)"
}

variable "public_subnets" {
  type        = list(string)
  description = "Public subnet IDs from shared network"
}

variable "use_spot" {
  type        = bool
  description = "Use FARGATE_SPOT capacity provider (true) or FARGATE (false)"
  default     = true
}

variable "task_cpu" {
  type        = number
  description = "Total CPU units for the Fargate task"
  default     = 1024
}

variable "task_memory" {
  type        = number
  description = "Total memory (MiB) for the Fargate task"
  default     = 2048
}

variable "containers" {
  type = list(object({
    name               = string
    image              = string
    cpu                = number
    memory             = number
    memory_reservation = number
    essential          = bool
    command            = list(string)
    environment        = list(object({ name = string, value = string }))
    port_mappings      = list(object({ containerPort = number, protocol = string }))
    log_stream_prefix  = string
  }))
  description = "Container definitions for the task (main + sidecars)"
}

variable "sg_egress_rules" {
  type = list(object({
    from_port   = number
    to_port     = number
    protocol    = string
    cidr_blocks = list(string)
    description = string
  }))
  description = "Security group egress rules compiled from profile"
  default     = []
}
