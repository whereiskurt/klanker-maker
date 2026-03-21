data "aws_caller_identity" "current" {}

locals {
  # Filter clusters for the current region
  region_clusters = [
    for cluster in var.ecs_clusters :
    cluster if cluster.region == var.region_full
  ]

  # Create a map of clusters by name for this region
  clusters_map = {
    for cluster in local.region_clusters :
    cluster.name => {
      name            = cluster.name
      region          = cluster.region
      enable_insights = cluster.enable_insights
      # Generate cluster name: sandbox-id-name-region_label (e.g., "my-sandbox-app-use1")
      cluster_name   = "${var.sandbox_id}-${cluster.name}-${var.region_label}"
      # Generate namespace: sandbox-id-name-region_label.local
      namespace_name = cluster.namespace_name != "" ? cluster.namespace_name : "${var.sandbox_id}-${cluster.name}-${var.region_label}.local"
    }
  }
}

# Service Discovery Private DNS Namespace
resource "aws_service_discovery_private_dns_namespace" "namespace" {
  for_each = local.clusters_map

  name        = each.value.namespace_name
  description = "Private DNS namespace for ${each.value.cluster_name}"
  vpc         = var.vpc_id

  tags = {
    Name             = each.value.namespace_name
    ClusterName      = each.key
    Region           = var.region_label
    "km:label"       = var.km_label
    "km:sandbox-id"  = var.sandbox_id
  }
}

# ECS Cluster with Fargate + Fargate Spot capacity providers
resource "aws_ecs_cluster" "cluster" {
  for_each = local.clusters_map

  name = each.value.cluster_name

  setting {
    name  = "containerInsights"
    value = each.value.enable_insights ? "enabled" : "disabled"
  }

  tags = {
    Name             = each.value.cluster_name
    ClusterName      = each.key
    Region           = var.region_label
    "km:label"       = var.km_label
    "km:sandbox-id"  = var.sandbox_id
  }
}

# Cluster capacity provider association: FARGATE + FARGATE_SPOT
resource "aws_ecs_cluster_capacity_providers" "cluster" {
  for_each = local.clusters_map

  cluster_name = aws_ecs_cluster.cluster[each.key].name

  capacity_providers = ["FARGATE", "FARGATE_SPOT"]

  default_capacity_provider_strategy {
    capacity_provider = "FARGATE_SPOT"
    weight            = 1
    base              = 0
  }
}

# IAM Role for ECS Tasks (per cluster)
resource "aws_iam_role" "ecs_task_role" {
  for_each = local.clusters_map

  name = "km-ecs-task-role-${each.value.cluster_name}-${var.km_random_suffix}"

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
            "aws:SourceArn" = "arn:aws:ecs:${var.region_full}:${data.aws_caller_identity.current.account_id}:*"
          }
          StringEquals = {
            "aws:SourceAccount" = data.aws_caller_identity.current.account_id
          }
        }
      }
    ]
  })

  tags = {
    Name             = "km-ecs-task-role-${each.value.cluster_name}"
    ClusterName      = each.key
    Region           = var.region_label
    "km:label"       = var.km_label
    "km:sandbox-id"  = var.sandbox_id
  }
}

# Minimal task role policy — SSM, ECR, CloudWatch Logs (least privilege baseline)
# Phase 2 compiler will extend this based on profile identity.awsPermissions
resource "aws_iam_role_policy" "ecs_task_policy" {
  for_each = local.clusters_map

  name = "km-ecs-task-policy-${each.value.cluster_name}"
  role = aws_iam_role.ecs_task_role[each.key].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SSMAccess"
        Effect = "Allow"
        Action = [
          "ssm:GetParameters",
          "ssm:GetParameter",
          "ssm:GetParametersByPath"
        ]
        Resource = "arn:aws:ssm:${var.region_full}:${data.aws_caller_identity.current.account_id}:parameter/km/*"
      },
      {
        Sid    = "KMSDecrypt"
        Effect = "Allow"
        Action = [
          "kms:Decrypt",
          "kms:DescribeKey"
        ]
        Resource = "*"
      },
      {
        Sid    = "CloudWatchLogs"
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents"
        ]
        Resource = "arn:aws:logs:${var.region_full}:${data.aws_caller_identity.current.account_id}:log-group:/ecs/km-${var.sandbox_id}*"
      },
      {
        Sid    = "ServiceDiscovery"
        Effect = "Allow"
        Action = [
          "servicediscovery:RegisterInstance",
          "servicediscovery:DeregisterInstance",
          "servicediscovery:DiscoverInstances"
        ]
        Resource = "*"
      }
    ]
  })
}

# Attach AWS Managed Policy for ECS Task Execution (ECR pull + CloudWatch Logs)
resource "aws_iam_role_policy_attachment" "ecs_task_execution_policy" {
  for_each = local.clusters_map

  role       = aws_iam_role.ecs_task_role[each.key].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonECSTaskExecutionRolePolicy"
}
