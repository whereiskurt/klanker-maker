# Map of all task definitions in this region by task name
output "tasks" {
  description = "Map of ECS task definitions by name"
  value = {
    for name, task in local.tasks_map :
    name => {
      task_definition_arn      = aws_ecs_task_definition.task[name].arn
      task_definition_family   = aws_ecs_task_definition.task[name].family
      task_definition_revision = aws_ecs_task_definition.task[name].revision
      cluster_name             = task.cluster_name
      network_mode             = task.network_mode
      task_cpu                 = task.task_cpu
      task_memory              = task.task_memory
      containers               = task.containers
      region                   = var.region_full
      region_label             = var.region_label
    }
  }
}

output "task_definition_arns" {
  description = "Map of task definition ARNs by task name"
  value = {
    for name, _ in local.tasks_map :
    name => aws_ecs_task_definition.task[name].arn
  }
}

output "task_definition_families" {
  description = "Map of task definition families by task name"
  value = {
    for name, _ in local.tasks_map :
    name => aws_ecs_task_definition.task[name].family
  }
}

output "task_names" {
  description = "List of task names in this region"
  value       = keys(local.tasks_map)
}
