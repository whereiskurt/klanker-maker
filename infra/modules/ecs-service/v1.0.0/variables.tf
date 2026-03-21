variable "km_label" {
  type        = string
  description = "Klanker Maker site label (e.g. 'km')"
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

variable "ecs_services" {
  type = list(object({
    name                 = string
    regions              = list(string)
    cluster_name         = string
    task_family          = string # Must match task definition family from ecs-task module
    desired_count        = optional(number, 1)
    force_new_deployment = optional(bool, true)

    # Network configuration
    assign_public_ip = optional(bool, false)

    # Service discovery configuration (optional — for intra-sandbox communication)
    service_discovery = optional(object({
      name           = string
      ttl            = optional(number, 10)
      container_name = optional(string, "") # Empty = skip service discovery registration
    }), null)

    # Deployment configuration
    deployment_circuit_breaker = optional(object({
      enable   = bool
      rollback = bool
    }), { enable = true, rollback = false })

    deployment_maximum_percent         = optional(number, 200)
    deployment_minimum_healthy_percent = optional(number, 50)

    # Capacity provider strategy — defaults to FARGATE_SPOT with FARGATE fallback
    capacity_provider_strategy = optional(list(object({
      capacity_provider = string
      weight            = number
      base              = optional(number, 0)
    })), [
      {
        capacity_provider = "FARGATE_SPOT"
        weight            = 1
        base              = 0
      },
      {
        capacity_provider = "FARGATE"
        weight            = 0
        base              = 1
      }
    ])
  }))
  description = "List of ECS service definitions (no load balancer — sandboxes use service discovery)"
  default     = []
}

# Dependency outputs from other modules
variable "task_definitions" {
  type        = map(string)
  description = "Map of task definition ARNs by task family name from ecs-task module"
  default     = {}
}

variable "clusters" {
  type = map(object({
    cluster_id     = string
    cluster_name   = string
    cluster_arn    = string
    namespace_id   = string
    namespace_name = string
  }))
  description = "Map of cluster details by cluster name from ecs-cluster module"
  default     = {}
}

variable "vpc_id" {
  type        = string
  description = "VPC ID"
}

variable "private_subnet_ids" {
  type        = list(string)
  description = "Private subnet IDs for ECS services"
  default     = []
}

variable "public_subnet_ids" {
  type        = list(string)
  description = "Public subnet IDs for ECS services"
  default     = []
}

variable "security_group_ids" {
  type        = list(string)
  description = "Security group IDs for ECS services"
  default     = []
}
