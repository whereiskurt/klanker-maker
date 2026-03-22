data "aws_caller_identity" "current" {}

locals {
  cluster_name = "km-sandbox-${var.sandbox_id}-${var.region_label}"
  task_family  = "km-sandbox-${var.sandbox_id}"
  service_name = "km-sandbox-${var.sandbox_id}"

  capacity_provider = var.use_spot ? "FARGATE_SPOT" : "FARGATE"
}

# ============================================================
# ECS Cluster
# ============================================================

resource "aws_ecs_cluster" "sandbox" {
  name = local.cluster_name

  setting {
    name  = "containerInsights"
    value = "disabled"
  }

  tags = {
    Name            = local.cluster_name
    "km:sandbox-id" = var.sandbox_id
    "km:label"      = var.km_label
  }
}

resource "aws_ecs_cluster_capacity_providers" "sandbox" {
  cluster_name = aws_ecs_cluster.sandbox.name

  capacity_providers = ["FARGATE", "FARGATE_SPOT"]

  default_capacity_provider_strategy {
    capacity_provider = local.capacity_provider
    weight            = 1
    base              = 0
  }
}

# ============================================================
# Security Group
# ============================================================

resource "aws_security_group" "sandbox" {
  name        = "km-ecs-sandbox-${var.sandbox_id}-${var.region_label}"
  description = "Security group for km ECS sandbox (egress controlled by profile)"
  vpc_id      = var.vpc_id

  tags = {
    Name            = "km-ecs-sandbox-${var.sandbox_id}"
    "km:sandbox-id" = var.sandbox_id
    "km:label"      = var.km_label
  }
}

resource "aws_security_group_rule" "sandbox_egress" {
  count = length(var.sg_egress_rules)

  type              = "egress"
  from_port         = var.sg_egress_rules[count.index].from_port
  to_port           = var.sg_egress_rules[count.index].to_port
  protocol          = var.sg_egress_rules[count.index].protocol
  cidr_blocks       = var.sg_egress_rules[count.index].cidr_blocks
  description       = var.sg_egress_rules[count.index].description
  security_group_id = aws_security_group.sandbox.id
}

# ============================================================
# IAM Roles
# ============================================================

resource "aws_iam_role" "task_execution" {
  name = "km-ecs-exec-${var.sandbox_id}-${var.region_label}"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = {
    Name            = "km-ecs-exec-${var.sandbox_id}"
    "km:sandbox-id" = var.sandbox_id
  }
}

resource "aws_iam_role_policy_attachment" "task_execution" {
  role       = aws_iam_role.task_execution.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}

resource "aws_iam_role" "task_role" {
  name = "km-ecs-task-${var.sandbox_id}-${var.region_label}"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ecs-tasks.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = {
    Name            = "km-ecs-task-${var.sandbox_id}"
    "km:sandbox-id" = var.sandbox_id
  }
}

resource "aws_iam_role_policy" "task_role" {
  name = "km-ecs-task-policy-${var.sandbox_id}"
  role = aws_iam_role.task_role.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "SSMAccess"
        Effect   = "Allow"
        Action   = ["ssm:GetParameters", "ssm:GetParameter"]
        Resource = "arn:aws:ssm:${var.region_full}:${data.aws_caller_identity.current.account_id}:parameter/km/*"
      },
      {
        Sid      = "CloudWatchLogs"
        Effect   = "Allow"
        Action   = ["logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents"]
        Resource = "arn:aws:logs:${var.region_full}:${data.aws_caller_identity.current.account_id}:log-group:/ecs/km-${var.sandbox_id}*"
      }
    ]
  })
}

# ============================================================
# CloudWatch Log Group
# ============================================================

resource "aws_cloudwatch_log_group" "sandbox" {
  name              = "/ecs/km-${var.sandbox_id}"
  retention_in_days = 7

  tags = {
    "km:sandbox-id" = var.sandbox_id
    "km:label"      = var.km_label
  }
}

# ============================================================
# Task Definition
# ============================================================

resource "aws_ecs_task_definition" "sandbox" {
  family                   = local.task_family
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = var.task_cpu
  memory                   = var.task_memory
  execution_role_arn       = aws_iam_role.task_execution.arn
  task_role_arn            = aws_iam_role.task_role.arn

  container_definitions = jsonencode([
    for c in var.containers : {
      name               = c.name
      image              = c.image
      cpu                = c.cpu
      memory             = c.memory
      memoryReservation  = c.memory_reservation
      essential          = c.essential
      command            = length(c.command) > 0 ? c.command : null
      environment        = c.environment
      portMappings = [
        for pm in c.port_mappings : {
          containerPort = pm.containerPort
          protocol      = pm.protocol
        }
      ]
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.sandbox.name
          "awslogs-region"        = var.region_full
          "awslogs-stream-prefix" = c.log_stream_prefix
        }
      }
    }
  ])

  tags = {
    Name            = local.task_family
    "km:sandbox-id" = var.sandbox_id
    "km:label"      = var.km_label
  }
}

# ============================================================
# Service
# ============================================================

resource "aws_ecs_service" "sandbox" {
  name            = local.service_name
  cluster         = aws_ecs_cluster.sandbox.id
  task_definition = aws_ecs_task_definition.sandbox.arn
  desired_count   = 1
  launch_type     = null # Use capacity provider strategy instead

  capacity_provider_strategy {
    capacity_provider = local.capacity_provider
    weight            = 1
    base              = 0
  }

  network_configuration {
    subnets          = var.public_subnets
    security_groups  = [aws_security_group.sandbox.id]
    assign_public_ip = true
  }

  tags = {
    Name            = local.service_name
    "km:sandbox-id" = var.sandbox_id
    "km:label"      = var.km_label
  }

  depends_on = [aws_ecs_cluster_capacity_providers.sandbox]
}
