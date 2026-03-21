output "services" {
  description = "Map of ECS service details by service name"
  value = {
    for name, service in aws_ecs_service.service :
    name => {
      service_id    = service.id
      service_name  = service.name
      service_arn   = service.arn
      cluster       = service.cluster
      desired_count = service.desired_count
    }
  }
}

output "service_discovery_services" {
  description = "Map of service discovery service details by service name"
  value = {
    for name, sd in aws_service_discovery_service.service :
    name => {
      id   = sd.id
      name = sd.name
      arn  = sd.arn
    }
  }
}
