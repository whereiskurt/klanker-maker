# Map of all clusters in this region by cluster name
output "clusters" {
  description = "Map of ECS clusters by name"
  value = {
    for name, cluster in local.clusters_map :
    name => {
      cluster_id             = aws_ecs_cluster.cluster[name].id
      cluster_name           = aws_ecs_cluster.cluster[name].name
      cluster_arn            = aws_ecs_cluster.cluster[name].arn
      enable_insights        = cluster.enable_insights
      service_namespace_id   = aws_service_discovery_private_dns_namespace.namespace[name].id
      service_namespace_name = aws_service_discovery_private_dns_namespace.namespace[name].name
      service_namespace_arn  = aws_service_discovery_private_dns_namespace.namespace[name].arn
      # Aliases for ecs-service module compatibility
      namespace_id       = aws_service_discovery_private_dns_namespace.namespace[name].id
      namespace_name     = aws_service_discovery_private_dns_namespace.namespace[name].name
      ecs_task_role_arn  = aws_iam_role.ecs_task_role[name].arn
      ecs_task_role_name = aws_iam_role.ecs_task_role[name].name
      region             = var.region_full
      region_label       = var.region_label
    }
  }
}

output "cluster_names" {
  description = "List of cluster names in this region"
  value       = [for name, cluster in local.clusters_map : cluster.cluster_name]
}

output "cluster_arns" {
  description = "Map of cluster ARNs by cluster name"
  value = {
    for name, _ in local.clusters_map :
    name => aws_ecs_cluster.cluster[name].arn
  }
}

output "cluster_ids" {
  description = "Map of cluster IDs by cluster name"
  value = {
    for name, _ in local.clusters_map :
    name => aws_ecs_cluster.cluster[name].id
  }
}

output "namespace_ids" {
  description = "Map of service discovery namespace IDs by cluster name"
  value = {
    for name, _ in local.clusters_map :
    name => aws_service_discovery_private_dns_namespace.namespace[name].id
  }
}

output "task_role_arns" {
  description = "Map of ECS task role ARNs by cluster name"
  value = {
    for name, _ in local.clusters_map :
    name => aws_iam_role.ecs_task_role[name].arn
  }
}
