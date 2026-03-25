data "aws_caller_identity" "current" {}

# ============================================================
# IAM role for the TTL handler Lambda
# ============================================================

resource "aws_iam_role" "ttl_handler" {
  name = "km-ttl-handler"

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
    "km:component" = "ttl-handler"
    "km:managed"   = "true"
  }
}

# Policy: CloudWatch Logs for Lambda execution logs
resource "aws_iam_role_policy" "cloudwatch_logs" {
  name = "km-ttl-handler-cw-logs"
  role = aws_iam_role.ttl_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents",
          "logs:DeleteLogGroup",
        ]
        Resource = "arn:aws:logs:*:*:*"
      }
    ]
  })
}

# Policy: S3 access for profile download and artifact upload
resource "aws_iam_role_policy" "s3_artifacts" {
  name = "km-ttl-handler-s3"
  role = aws_iam_role.ttl_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:ListBucket",
        ]
        Resource = [
          "${var.artifact_bucket_arn}",
          "${var.artifact_bucket_arn}/*",
        ]
      }
    ]
  })
}

# Policy: SES for lifecycle notification emails
resource "aws_iam_role_policy" "ses_send" {
  name = "km-ttl-handler-ses"
  role = aws_iam_role.ttl_handler.id

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

# Policy: EventBridge Scheduler self-cleanup (delete the TTL schedule after firing)
resource "aws_iam_role_policy" "scheduler_delete" {
  name = "km-ttl-handler-scheduler"
  role = aws_iam_role.ttl_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["scheduler:DeleteSchedule"]
        Resource = "arn:aws:scheduler:*:*:schedule/default/km-ttl-*"
      }
    ]
  })
}

# Policy: Terraform destroy permissions — allows the Lambda to run terraform destroy
# on sandbox state. Scoped to km-* resources where possible.
resource "aws_iam_role_policy" "terraform_destroy" {
  name = "km-ttl-handler-terraform-destroy"
  role = aws_iam_role.ttl_handler.id

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
      },
      {
        Sid    = "TerraformLockTable"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:DeleteItem",
        ]
        Resource = "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.state_prefix}-locks-*"
      },
      {
        Sid    = "EC2SandboxDestroy"
        Effect = "Allow"
        Action = [
          "ec2:TerminateInstances",
          "ec2:DescribeInstances",
          "ec2:CancelSpotInstanceRequests",
          "ec2:DescribeSpotInstanceRequests",
          "ec2:DeleteSecurityGroup",
          "ec2:RevokeSecurityGroupEgress",
          "ec2:RevokeSecurityGroupIngress",
          "ec2:DescribeSecurityGroups",
          "ec2:DescribeSecurityGroupRules",
          "ec2:DeleteTags",
          "ec2:DescribeTags",
          "ec2:DescribeVpcs",
          "ec2:DescribeSubnets",
          "ec2:DescribeAvailabilityZones",
          "ec2:DescribeImages",
          "ec2:DescribeSpotPriceHistory",
          "ec2:DescribeNetworkInterfaces",
          "ec2:DescribeVolumes",
          "ec2:DescribeInstanceTypes",
          "ec2:DescribeInstanceAttribute",
          "ec2:ModifyInstanceAttribute",
          "ec2:DescribeInstanceCreditSpecifications",
        ]
        Resource = "*"
      },
      {
        Sid    = "IAMSandboxDestroy"
        Effect = "Allow"
        Action = [
          "iam:DeleteRole",
          "iam:DeleteRolePolicy",
          "iam:DetachRolePolicy",
          "iam:GetRole",
          "iam:GetRolePolicy",
          "iam:ListRolePolicies",
          "iam:ListAttachedRolePolicies",
          "iam:ListInstanceProfilesForRole",
          "iam:RemoveRoleFromInstanceProfile",
          "iam:DeleteInstanceProfile",
          "iam:GetInstanceProfile",
          "iam:PassRole",
          "iam:CreateRole",
          "iam:PutRolePolicy",
          "iam:AttachRolePolicy",
          "iam:TagRole",
        ]
        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/km-ec2spot-*",
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/km-budget-enforcer-*",
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/km-budget-scheduler-*",
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:instance-profile/km-ec2spot-*",
        ]
      },
      {
        Sid    = "LambdaCleanup"
        Effect = "Allow"
        Action = [
          "lambda:DeleteFunction",
          "lambda:GetFunction",
          "lambda:RemovePermission",
          "lambda:GetPolicy",
        ]
        Resource = "arn:aws:lambda:*:${data.aws_caller_identity.current.account_id}:function:km-budget-enforcer-*"
      },
      {
        Sid    = "SchedulerCleanup"
        Effect = "Allow"
        Action = [
          "scheduler:DeleteSchedule",
          "scheduler:GetSchedule",
        ]
        Resource = "arn:aws:scheduler:*:*:schedule/default/km-budget-*"
      },
      {
        Sid    = "KMSCleanup"
        Effect = "Allow"
        Action = [
          "kms:DescribeKey",
          "kms:ScheduleKeyDeletion",
          "kms:DeleteAlias",
          "kms:ListAliases",
        ]
        Resource = "*"
      },
    ]
  })
}

# ============================================================
# Lambda function: TTL handler (Go, provided.al2023, arm64)
# ============================================================

resource "aws_lambda_function" "ttl_handler" {
  function_name = "km-ttl-handler"
  description   = "Uploads artifacts and sends ttl-expired notification when EventBridge TTL fires"
  role          = aws_iam_role.ttl_handler.arn

  # Go Lambda: custom runtime on Amazon Linux 2023, arm64 for Graviton cost efficiency
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  filename         = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)

  # 15-minute timeout: terraform init + destroy can take several minutes
  timeout     = 900
  memory_size = 1536
  architectures = ["arm64"]

  # 2GB ephemeral storage: terraform init downloads the AWS provider (~500MB)
  ephemeral_storage {
    size = 2048
  }

  environment {
    variables = {
      KM_ARTIFACTS_BUCKET = var.artifact_bucket_name
      KM_EMAIL_DOMAIN     = var.email_domain
      KM_OPERATOR_EMAIL   = var.operator_email
      KM_STATE_BUCKET     = var.state_bucket
      KM_STATE_PREFIX     = var.state_prefix
      KM_REGION_LABEL     = var.region_label
    }
  }

  tags = {
    "km:component" = "ttl-handler"
    "km:managed"   = "true"
  }
}

# CloudWatch Log Group for Lambda logs
resource "aws_cloudwatch_log_group" "ttl_handler" {
  name              = "/aws/lambda/km-ttl-handler"
  retention_in_days = 30

  tags = {
    "km:component" = "ttl-handler"
    "km:managed"   = "true"
  }
}

# ============================================================
# EventBridge Scheduler execution role: assumed by Scheduler to invoke Lambda
# ============================================================

resource "aws_iam_role" "scheduler_invoke" {
  name = "km-ttl-scheduler"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Service = "scheduler.amazonaws.com"
        }
        Action = "sts:AssumeRole"
      },
    ]
  })

  tags = {
    "km:component" = "ttl-handler"
    "km:managed"   = "true"
  }
}

resource "aws_iam_role_policy" "scheduler_invoke_lambda" {
  name = "km-ttl-scheduler-invoke"
  role = aws_iam_role.scheduler_invoke.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = "lambda:InvokeFunction"
        Resource = aws_lambda_function.ttl_handler.arn
      },
    ]
  })
}

# ============================================================
# EventBridge Scheduler permission: allow scheduler to invoke Lambda
# ============================================================

resource "aws_lambda_permission" "eventbridge_scheduler" {
  statement_id  = "AllowEventBridgeSchedulerInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.ttl_handler.function_name
  principal     = "scheduler.amazonaws.com"
}

# ============================================================
# EventBridge rule: route SandboxIdle events to TTL Lambda (PROV-06)
# ============================================================

resource "aws_cloudwatch_event_rule" "sandbox_idle" {
  name        = "km-sandbox-idle"
  description = "Routes SandboxIdle events from audit-log sidecar to TTL Lambda for sandbox teardown"

  event_pattern = jsonencode({
    source      = ["km.sandbox"]
    detail-type = ["SandboxIdle"]
  })

  tags = {
    "km:component" = "ttl-handler"
    "km:managed"   = "true"
  }
}

resource "aws_cloudwatch_event_target" "idle_to_ttl" {
  rule      = aws_cloudwatch_event_rule.sandbox_idle.name
  target_id = "km-ttl-handler-idle"
  arn       = aws_lambda_function.ttl_handler.arn

  # Transform EventBridge envelope to match TTLEvent struct shape
  input_transformer {
    input_paths = {
      sandbox_id = "$.detail.sandbox_id"
    }
    input_template = <<-TEMPLATE
    {"sandbox_id": <sandbox_id>, "event_type": "idle"}
    TEMPLATE
  }
}

# Lambda permission: allow EventBridge Events to invoke the TTL Lambda (for idle events)
# Note: This is separate from eventbridge_scheduler (which allows the Scheduler service).
resource "aws_lambda_permission" "eventbridge_events" {
  statement_id  = "AllowEventBridgeEventsInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.ttl_handler.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.sandbox_idle.arn
}

# ============================================================
# IAM policies: sandbox resource teardown (PROV-05/PROV-06)
# ============================================================

# Policy: Tag API for discovering sandbox resources by km:sandbox-id tag
resource "aws_iam_role_policy" "tag_discovery" {
  name = "km-ttl-handler-tag-discovery"
  role = aws_iam_role.ttl_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["tag:GetResources"]
      Resource = "*"
    }]
  })
}

# Policy: EC2 terminate for destroying sandbox instances after TTL/idle
resource "aws_iam_role_policy" "ec2_teardown" {
  name = "km-ttl-handler-ec2-teardown"
  role = aws_iam_role.ttl_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "ec2:TerminateInstances",
        "ec2:DescribeInstances",
      ]
      Resource = "*"
    }]
  })
}
