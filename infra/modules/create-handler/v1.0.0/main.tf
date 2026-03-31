data "aws_caller_identity" "current" {}

# ============================================================
# IAM role for the create-handler Lambda
# ============================================================

resource "aws_iam_role" "create_handler" {
  name = "km-create-handler"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect    = "Allow"
        Principal = { Service = "lambda.amazonaws.com" }
        Action    = "sts:AssumeRole"
      }
    ]
  })

  tags = {
    "km:component" = "create-handler"
    "km:managed"   = "true"
  }
}

# Policy: CloudWatch Logs for Lambda execution logs
resource "aws_iam_role_policy" "cloudwatch_logs" {
  name = "km-create-handler-cw-logs"
  role = aws_iam_role.create_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = ["logs:*"]
        Resource = [
          "arn:aws:logs:*:${data.aws_caller_identity.current.account_id}:log-group:/aws/lambda/km-*",
          "arn:aws:logs:*:${data.aws_caller_identity.current.account_id}:log-group:/aws/lambda/km-*:*",
          "arn:aws:logs:*:${data.aws_caller_identity.current.account_id}:log-group:/km/sandboxes/*",
          "arn:aws:logs:*:${data.aws_caller_identity.current.account_id}:log-group:/km/sandboxes/*:*",
        ]
      },
      {
        # DescribeLogGroups requires wildcard resource
        Effect   = "Allow"
        Action   = ["logs:DescribeLogGroups"]
        Resource = "*"
      }
    ]
  })
}

# Policy: S3 artifact bucket access (profile download, artifact upload)
resource "aws_iam_role_policy" "s3_artifacts" {
  name = "km-create-handler-s3"
  role = aws_iam_role.create_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "ArtifactBucketAccess"
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:ListBucket",
          "s3:DeleteObject",
        ]
        Resource = [
          var.artifact_bucket_arn,
          "${var.artifact_bucket_arn}/*",
        ]
      }
    ]
  })
}

# Policy: DynamoDB access for Terraform state locking and budget tracking
resource "aws_iam_role_policy" "dynamodb" {
  name = "km-create-handler-dynamodb"
  role = aws_iam_role.create_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "TerraformStateLock"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem",
        ]
        Resource = "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.dynamodb_table_name}"
      },
      {
        Sid    = "BudgetTable"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem",
        ]
        Resource = var.dynamodb_budget_table_arn
      }
    ]
  })
}

# Policy: DynamoDB km-sandboxes — read/write sandbox metadata
resource "aws_iam_role_policy" "dynamodb_sandboxes" {
  name = "km-create-handler-dynamodb-sandboxes"
  role = aws_iam_role.create_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SandboxMetadataTable"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem",
          "dynamodb:Scan",
          "dynamodb:Query",
        ]
        Resource = [
          "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/km-sandboxes",
          "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/km-sandboxes/index/alias-index",
        ]
      }
    ]
  })
}

# Policy: Terraform state S3 access (state read/write for km create subprocess)
resource "aws_iam_role_policy" "terraform_state" {
  name = "km-create-handler-tf-state"
  role = aws_iam_role.create_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "TerraformStateAccess"
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject",
          "s3:ListBucket",
        ]
        Resource = [
          "arn:aws:s3:::${var.state_bucket}",
          "arn:aws:s3:::${var.state_bucket}/*",
        ]
      }
    ]
  })
}

# Policy: EC2 provisioning for sandbox creation
resource "aws_iam_role_policy" "ec2_provisioning" {
  name = "km-create-handler-ec2"
  role = aws_iam_role.create_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "EC2Provision"
        Effect = "Allow"
        Action = [
          "ec2:RunInstances",
          "ec2:DescribeInstances",
          "ec2:DescribeInstanceTypes",
          "ec2:DescribeInstanceAttribute",
          "ec2:DescribeInstanceCreditSpecifications",
          "ec2:CreateSecurityGroup",
          "ec2:AuthorizeSecurityGroupEgress",
          "ec2:AuthorizeSecurityGroupIngress",
          "ec2:RevokeSecurityGroupEgress",
          "ec2:DeleteSecurityGroup",
          "ec2:DescribeSecurityGroups",
          "ec2:DescribeSecurityGroupRules",
          "ec2:TerminateInstances",
          "ec2:CreateTags",
          "ec2:DeleteTags",
          "ec2:DescribeTags",
          "ec2:DescribeVpcs",
          "ec2:DescribeSubnets",
          "ec2:DescribeAvailabilityZones",
          "ec2:DescribeImages",
          "ec2:DescribeSpotPriceHistory",
          "ec2:RequestSpotInstances",
          "ec2:CancelSpotInstanceRequests",
          "ec2:DescribeSpotInstanceRequests",
          "ec2:DescribeNetworkInterfaces",
          "ec2:DescribeVolumes",
          "ec2:ModifyInstanceAttribute",
        ]
        Resource = "*"
      }
    ]
  })
}

# Policy: IAM role and instance profile management for sandbox EC2 roles
resource "aws_iam_role_policy" "iam_sandbox" {
  name = "km-create-handler-iam"
  role = aws_iam_role.create_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "IAMRoleManagement"
        Effect = "Allow"
        Action = [
          "iam:CreateRole",
          "iam:PutRolePolicy",
          "iam:DeleteRolePolicy",
          "iam:DeleteRole",
          "iam:GetRole",
          "iam:GetRolePolicy",
          "iam:ListRolePolicies",
          "iam:AttachRolePolicy",
          "iam:DetachRolePolicy",
          "iam:ListAttachedRolePolicies",
          "iam:TagRole",
          "iam:UntagRole",
        ]
        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/km-*",
        ]
      },
      {
        Sid    = "IAMInstanceProfile"
        Effect = "Allow"
        Action = [
          "iam:CreateInstanceProfile",
          "iam:AddRoleToInstanceProfile",
          "iam:RemoveRoleFromInstanceProfile",
          "iam:DeleteInstanceProfile",
          "iam:GetInstanceProfile",
          "iam:ListInstanceProfilesForRole",
          "iam:TagInstanceProfile",
          "iam:UntagInstanceProfile",
        ]
        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:instance-profile/km-*",
        ]
      },
      {
        Sid    = "IAMPassRole"
        Effect = "Allow"
        Action = ["iam:PassRole"]
        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/km-*",
        ]
      },
      {
        Sid      = "STSCallerIdentity"
        Effect   = "Allow"
        Action   = ["sts:GetCallerIdentity"]
        Resource = "*"
      }
    ]
  })
}

# Policy: ECS cluster and task management for sandbox workloads
resource "aws_iam_role_policy" "ecs_provisioning" {
  name = "km-create-handler-ecs"
  role = aws_iam_role.create_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "ECSManagement"
        Effect = "Allow"
        Action = [
          "ecs:CreateCluster",
          "ecs:DeleteCluster",
          "ecs:RegisterTaskDefinition",
          "ecs:DeregisterTaskDefinition",
          "ecs:CreateService",
          "ecs:DeleteService",
          "ecs:UpdateService",
          "ecs:DescribeServices",
          "ecs:DescribeClusters",
          "ecs:DescribeTaskDefinition",
          "ecs:ListTaskDefinitions",
          "ecs:TagResource",
          "ecs:UntagResource",
        ]
        Resource = "*"
      }
    ]
  })
}

# Policy: EventBridge Scheduler for TTL schedule creation
resource "aws_iam_role_policy" "scheduler" {
  name = "km-create-handler-scheduler"
  role = aws_iam_role.create_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SchedulerManagement"
        Effect = "Allow"
        Action = [
          "scheduler:CreateSchedule",
          "scheduler:DeleteSchedule",
          "scheduler:GetSchedule",
          "scheduler:UpdateSchedule",
        ]
        Resource = "arn:aws:scheduler:*:${data.aws_caller_identity.current.account_id}:schedule/default/km-*"
      },
      {
        Sid    = "SchedulerPassRole"
        Effect = "Allow"
        Action = ["iam:PassRole"]
        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/km-ttl-scheduler",
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/km-budget-scheduler-*",
        ]
      }
    ]
  })
}

# Policy: SSM Parameter Store for safe phrase and GitHub token
resource "aws_iam_role_policy" "ssm" {
  name = "km-create-handler-ssm"
  role = aws_iam_role.create_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SSMParameterAccess"
        Effect = "Allow"
        Action = [
          "ssm:GetParameter",
          "ssm:PutParameter",
          "ssm:DeleteParameter",
        ]
        Resource = "arn:aws:ssm:*:${data.aws_caller_identity.current.account_id}:parameter/km/*"
      }
    ]
  })
}

# Policy: SES for sandbox creation notification emails
resource "aws_iam_role_policy" "ses_send" {
  name = "km-create-handler-ses"
  role = aws_iam_role.create_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["ses:SendEmail", "ses:SendRawEmail"]
        Resource = "*"
      }
    ]
  })
}

# Policy: Lambda management for per-sandbox budget-enforcer functions
resource "aws_iam_role_policy" "lambda_budget" {
  name = "km-create-handler-lambda"
  role = aws_iam_role.create_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "LambdaPerSandbox"
        Effect = "Allow"
        Action = ["lambda:*"]
        Resource = [
          "arn:aws:lambda:*:${data.aws_caller_identity.current.account_id}:function:km-*",
        ]
      }
    ]
  })
}

# Policy: KMS for encrypting/decrypting sandbox secrets
resource "aws_iam_role_policy" "kms" {
  name = "km-create-handler-kms"
  role = aws_iam_role.create_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["kms:*"]
        Resource = "*"
      }
    ]
  })
}

# ============================================================
# Lambda function: create-handler (container image, arm64)
# ============================================================

resource "aws_lambda_function" "create_handler" {
  function_name = "km-create-handler"
  description   = "Provisions a new sandbox by running km create as a subprocess when a SandboxCreate EventBridge event is received"
  role          = aws_iam_role.create_handler.arn

  # Zip Lambda from local file — handler downloads km/terraform/terragrunt from S3 at cold start
  filename         = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]

  # 15-minute timeout: cold start toolchain download + terraform init + apply
  timeout     = 900
  memory_size = 1536

  # 10GB ephemeral storage: toolchain binaries (~250MB) + terraform provider download (~500MB)
  ephemeral_storage {
    size = 10240
  }

  environment {
    variables = {
      KM_ARTIFACTS_BUCKET = var.artifact_bucket_name
      KM_EMAIL_DOMAIN     = var.email_domain
      KM_OPERATOR_EMAIL   = var.operator_email
      KM_STATE_BUCKET     = var.state_bucket
      KM_STATE_PREFIX     = var.state_prefix
      KM_REGION_LABEL     = var.region_label
      KM_TOOLCHAIN_DIR    = "/tmp/toolchain"
    }
  }

  tags = {
    "km:component" = "create-handler"
    "km:managed"   = "true"
  }
}

# CloudWatch Log Group for Lambda logs
resource "aws_cloudwatch_log_group" "create_handler" {
  name              = "/aws/lambda/km-create-handler"
  retention_in_days = 30

  tags = {
    "km:component" = "create-handler"
    "km:managed"   = "true"
  }
}

# ============================================================
# EventBridge rule: route SandboxCreate events to create-handler Lambda
# ============================================================

resource "aws_cloudwatch_event_rule" "sandbox_create" {
  name        = "km-sandbox-create"
  description = "Routes SandboxCreate events to the create-handler Lambda for sandbox provisioning"

  event_pattern = jsonencode({
    source      = ["km.sandbox"]
    detail-type = ["SandboxCreate"]
  })

  tags = {
    "km:component" = "create-handler"
    "km:managed"   = "true"
  }
}

resource "aws_cloudwatch_event_target" "create_to_lambda" {
  rule      = aws_cloudwatch_event_rule.sandbox_create.name
  target_id = "km-create-handler"
  arn       = aws_lambda_function.create_handler.arn

  # CRITICAL: 0 retries — sandbox creation is NOT idempotent. A retry after partial
  # completion would attempt to re-provision an already-partially-created sandbox.
  retry_policy {
    maximum_retry_attempts       = 0
    maximum_event_age_in_seconds = 60
  }
}

# Lambda permission: allow EventBridge Events to invoke the create-handler Lambda
resource "aws_lambda_permission" "eventbridge_events" {
  statement_id  = "AllowEventBridgeEventsInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.create_handler.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.sandbox_create.arn
}
