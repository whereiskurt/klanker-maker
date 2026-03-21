data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  # ECR registry URL for this account and region
  ecr_registry = "${data.aws_caller_identity.current.account_id}.dkr.ecr.${data.aws_region.current.id}.amazonaws.com"

  # Expand each task across its list of regions
  expanded_tasks = flatten([
    for task in var.ecs_tasks :
    [
      for region in task.regions :
      {
        key                = "${task.name}-${region}"
        name               = task.name
        region             = region
        cluster_name       = task.cluster_name
        task_cpu           = task.task_cpu
        task_memory        = task.task_memory
        network_mode       = task.network_mode
        task_role_arn      = task.task_role_arn
        execution_role_arn = task.execution_role_arn
        containers         = task.containers
      }
    ]
  ])

  # Filter tasks for the current region only
  region_tasks = [
    for task in local.expanded_tasks :
    task if task.region == var.region_full
  ]

  # Helper: construct full ECR image URL if not already a full URL
  construct_image_url = {
    for task in local.region_tasks :
    task.name => [
      for container in task.containers : {
        original_image = container.image
        full_image = (
          can(regex("dkr\\.ecr\\.", container.image)) || can(regex("^[0-9]", container.image)) ?
          container.image :
          "${local.ecr_registry}/${var.km_label}-${container.image}"
        )
      }
    ]
  }

  # Create a map of tasks by name for this region
  tasks_map = {
    for task in local.region_tasks :
    task.name => {
      name               = task.name
      cluster_name       = task.cluster_name
      region             = task.region
      task_cpu           = task.task_cpu
      task_memory        = task.task_memory
      network_mode       = task.network_mode
      task_role_arn      = task.task_role_arn
      execution_role_arn = task.execution_role_arn
      # Update container images with full URLs
      containers = [
        for idx, container in task.containers : merge(container, {
          image = local.construct_image_url[task.name][idx].full_image
        })
      ]
      # Generate family name: km-sandbox-id-taskname-region_label
      family = "km-${var.sandbox_id}-${task.name}-${var.region_label}"
    }
  }
}

# IAM Role for ECS Task Execution
# This role is used by ECS to pull images, write logs, and read secrets
resource "aws_iam_role" "execution_role" {
  for_each = local.tasks_map

  name = "km-${each.value.family}-exec-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Service = "ecs-tasks.amazonaws.com"
        }
        Action = "sts:AssumeRole"
        Condition = {
          ArnLike = {
            "aws:SourceArn" = "arn:aws:ecs:${data.aws_region.current.id}:${data.aws_caller_identity.current.account_id}:*"
          }
          StringEquals = {
            "aws:SourceAccount" = data.aws_caller_identity.current.account_id
          }
        }
      }
    ]
  })

  tags = {
    Name             = "km-${each.value.family}-exec-role"
    TaskName         = each.key
    Region           = var.region_label
    "km:label"       = var.km_label
    "km:sandbox-id"  = var.sandbox_id
  }
}

# Attach AWS managed policy for ECS task execution (ECR pull + CloudWatch Logs)
resource "aws_iam_role_policy_attachment" "execution_role_policy" {
  for_each = local.tasks_map

  role       = aws_iam_role.execution_role[each.key].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

# Custom policy for SSM Parameter Store access (for secrets)
resource "aws_iam_role_policy" "ssm_access" {
  for_each = local.tasks_map

  name = "km-${each.value.family}-ssm-access"
  role = aws_iam_role.execution_role[each.key].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "ssm:GetParameters",
          "ssm:GetParameter",
          "ssm:GetParametersByPath"
        ]
        Resource = "arn:aws:ssm:${data.aws_region.current.id}:${data.aws_caller_identity.current.account_id}:parameter/km/*"
      },
      {
        Effect = "Allow"
        Action = [
          "kms:Decrypt"
        ]
        Resource = "*"
      },
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup"
        ]
        Resource = "arn:aws:logs:${data.aws_region.current.id}:${data.aws_caller_identity.current.account_id}:log-group:/ecs/km-${var.sandbox_id}*"
      }
    ]
  })
}

# ECS Task Definition
# container_definitions is parameterized via the containers variable to support
# the main container + sidecar containers (DNS proxy, HTTP proxy, audit log, tracing)
resource "aws_ecs_task_definition" "task" {
  for_each = local.tasks_map

  depends_on = [
    aws_iam_role_policy_attachment.execution_role_policy,
    aws_iam_role_policy.ssm_access
  ]

  family                   = each.value.family
  network_mode             = each.value.network_mode
  requires_compatibilities = ["FARGATE"]
  cpu                      = each.value.task_cpu
  memory                   = each.value.task_memory
  task_role_arn            = each.value.task_role_arn != "" ? each.value.task_role_arn : null
  execution_role_arn       = each.value.execution_role_arn != "" ? each.value.execution_role_arn : aws_iam_role.execution_role[each.key].arn

  # Fully parameterized container definitions — supports main container + sidecars
  # Sidecar slots: dns-proxy, http-proxy, audit-log, tracing (populated by Phase 2 compiler)
  container_definitions = jsonencode([
    for container in each.value.containers : {
      name              = container.name
      image             = container.image
      cpu               = container.cpu
      memory            = container.memory
      memoryReservation = container.memory_reservation
      essential         = container.essential
      command           = length(container.command) > 0 ? container.command : null

      # Security: Read-only root filesystem (recommended; override per container if needed)
      readonlyRootFilesystem = container.readonly_root_filesystem

      # tmpfs mounts for containers that need write access
      linuxParameters = length(container.tmpfs_mounts) > 0 ? {
        tmpfs = [
          for mount in container.tmpfs_mounts : {
            containerPath = mount.container_path
            size          = mount.size
          }
        ]
      } : null

      # Substitute template variables in environment values
      environment = length(container.environment) > 0 ? [
        for env in container.environment : {
          name = env.name
          value = replace(
            replace(
              replace(
                replace(env.value, "{{REGION_LABEL}}", var.region_label),
                "{{REGION}}", var.region_full
              ),
              "{{KM_LABEL}}", var.km_label
            ),
            "{{SANDBOX_ID}}", var.sandbox_id
          )
        }
      ] : null

      # Substitute template variables in secret paths
      secrets = length(container.secrets) > 0 ? [
        for secret in container.secrets : {
          name = secret.name
          valueFrom = replace(
            replace(
              replace(
                replace(secret.valueFrom, "{{REGION_LABEL}}", var.region_label),
                "{{REGION}}", var.region_full
              ),
              "{{KM_LABEL}}", var.km_label
            ),
            "{{SANDBOX_ID}}", var.sandbox_id
          )
        }
      ] : null

      portMappings = length(container.port_mappings) > 0 ? [
        for port in container.port_mappings : {
          containerPort = port.container_port
          hostPort      = port.host_port
          protocol      = port.protocol
        }
      ] : null

      dependsOn = length(container.depends_on) > 0 ? [
        for dep in container.depends_on : {
          containerName = dep.container_name
          condition     = dep.condition
        }
      ] : null

      healthCheck = container.health_check != null ? {
        command = [
          for cmd in container.health_check.command :
          replace(
            replace(
              replace(cmd, "{{REGION_LABEL}}", var.region_label),
              "{{REGION}}", var.region_full
            ),
            "{{KM_LABEL}}", var.km_label
          )
        ]
        interval    = container.health_check.interval
        timeout     = container.health_check.timeout
        retries     = container.health_check.retries
        startPeriod = container.health_check.start_period
      } : null

      logConfiguration = var.enable_logging ? {
        logDriver = "awslogs"
        options = {
          "awslogs-region"        = data.aws_region.current.id
          "awslogs-group"         = "/ecs/km-${var.sandbox_id}/${container.name}"
          "awslogs-stream-prefix" = container.log_stream_prefix
          "awslogs-create-group"  = "true"
        }
      } : null
    }
  ])

  tags = {
    Name             = each.value.family
    TaskName         = each.key
    Cluster          = each.value.cluster_name
    Region           = var.region_label
    "km:label"       = var.km_label
    "km:sandbox-id"  = var.sandbox_id
  }
}
