data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  # Expand each service across its list of regions
  expanded_services = flatten([
    for service in var.ecs_services :
    [
      for region in service.regions :
      {
        key                                = "${service.name}-${region}"
        name                               = service.name
        region                             = region
        cluster_name                       = service.cluster_name
        task_family                        = service.task_family
        desired_count                      = service.desired_count
        force_new_deployment               = service.force_new_deployment
        assign_public_ip                   = service.assign_public_ip
        service_discovery                  = service.service_discovery
        deployment_circuit_breaker         = service.deployment_circuit_breaker
        deployment_maximum_percent         = service.deployment_maximum_percent
        deployment_minimum_healthy_percent = service.deployment_minimum_healthy_percent
        capacity_provider_strategy         = service.capacity_provider_strategy
      }
    ]
  ])

  # Filter services for the current region only
  region_services = [
    for service in local.expanded_services :
    service if service.region == var.region_full
  ]

  # Create a map of services by name for this region
  services_map = {
    for service in local.region_services :
    service.name => {
      name                               = service.name
      region                             = service.region
      cluster_name                       = service.cluster_name
      task_family                        = service.task_family
      desired_count                      = service.desired_count
      force_new_deployment               = service.force_new_deployment
      assign_public_ip                   = service.assign_public_ip
      service_discovery                  = service.service_discovery
      deployment_circuit_breaker         = service.deployment_circuit_breaker
      deployment_maximum_percent         = service.deployment_maximum_percent
      deployment_minimum_healthy_percent = service.deployment_minimum_healthy_percent
      capacity_provider_strategy         = service.capacity_provider_strategy
      # Construct service name: km-sandbox-id-name-region_label
      service_name = "km-${var.sandbox_id}-${service.name}-${var.region_label}"
      # Subnet selection based on assign_public_ip
      subnets = service.assign_public_ip ? var.public_subnet_ids : var.private_subnet_ids
    }
  }
}

# Service Discovery Service (optional — for intra-sandbox sidecar communication)
resource "aws_service_discovery_service" "service" {
  for_each = {
    for name, service in local.services_map :
    name => service if try(service.service_discovery.container_name, "") != ""
  }

  name = each.value.service_discovery.name

  dns_config {
    namespace_id = var.clusters[each.value.cluster_name].namespace_id

    dns_records {
      type = "A"
      ttl  = each.value.service_discovery.ttl
    }

    routing_policy = "MULTIVALUE"
  }

  tags = {
    Name             = each.value.service_discovery.name
    Service          = each.key
    Region           = var.region_label
    "km:label"       = var.km_label
    "km:sandbox-id"  = var.sandbox_id
  }
}

# ECS Service with FARGATE_SPOT capacity (no load balancer — sandboxes use direct service discovery)
resource "aws_ecs_service" "service" {
  for_each = local.services_map

  name                 = each.value.service_name
  cluster              = var.clusters[each.value.cluster_name].cluster_id
  task_definition      = var.task_definitions[each.value.task_family]
  desired_count        = each.value.desired_count
  force_new_deployment = each.value.force_new_deployment

  # FARGATE_SPOT preferred; falls back to FARGATE if spot unavailable
  dynamic "capacity_provider_strategy" {
    for_each = each.value.capacity_provider_strategy
    content {
      capacity_provider = capacity_provider_strategy.value.capacity_provider
      weight            = capacity_provider_strategy.value.weight
      base              = capacity_provider_strategy.value.base
    }
  }

  network_configuration {
    subnets          = each.value.subnets
    security_groups  = var.security_group_ids
    assign_public_ip = each.value.assign_public_ip
  }

  # Service discovery registration (optional)
  dynamic "service_registries" {
    for_each = try(each.value.service_discovery.container_name, "") != "" ? [1] : []
    content {
      registry_arn   = aws_service_discovery_service.service[each.key].arn
      container_name = each.value.service_discovery.container_name
    }
  }

  deployment_circuit_breaker {
    enable   = each.value.deployment_circuit_breaker.enable
    rollback = each.value.deployment_circuit_breaker.rollback
  }

  deployment_maximum_percent         = each.value.deployment_maximum_percent
  deployment_minimum_healthy_percent = each.value.deployment_minimum_healthy_percent

  tags = {
    Name             = each.value.service_name
    Service          = each.key
    Cluster          = each.value.cluster_name
    Region           = var.region_label
    "km:label"       = var.km_label
    "km:sandbox-id"  = var.sandbox_id
  }
}
