output "cluster_name" {
  description = "ECS cluster name"
  value       = aws_ecs_cluster.sandbox.name
}

output "cluster_arn" {
  description = "ECS cluster ARN"
  value       = aws_ecs_cluster.sandbox.arn
}

output "service_name" {
  description = "ECS service name"
  value       = aws_ecs_service.sandbox.name
}

output "task_definition_arn" {
  description = "ECS task definition ARN"
  value       = aws_ecs_task_definition.sandbox.arn
}

output "security_group_id" {
  description = "Security group ID for the ECS sandbox"
  value       = aws_security_group.sandbox.id
}

output "capacity_provider" {
  description = "Capacity provider used (FARGATE or FARGATE_SPOT)"
  value       = local.capacity_provider
}
