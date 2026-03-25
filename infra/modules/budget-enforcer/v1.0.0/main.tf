# ============================================================
# IAM role for the budget-enforcer Lambda
# ============================================================

resource "aws_iam_role" "budget_enforcer" {
  name = "km-budget-enforcer-${var.sandbox_id}"

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
    "km:component"  = "budget-enforcer"
    "km:sandbox_id" = var.sandbox_id
    "km:managed"    = "true"
  }
}

# Policy: CloudWatch Logs for Lambda execution logs
resource "aws_iam_role_policy" "cloudwatch_logs" {
  name = "km-budget-enforcer-cw-logs-${var.sandbox_id}"
  role = aws_iam_role.budget_enforcer.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents",
        ]
        Resource = "arn:aws:logs:*:*:*"
      }
    ]
  })
}

# Policy: DynamoDB budget table access (read spend, write spend, set notification flags)
resource "aws_iam_role_policy" "dynamodb_budget" {
  name = "km-budget-enforcer-dynamodb-${var.sandbox_id}"
  role = aws_iam_role.budget_enforcer.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:UpdateItem",
          "dynamodb:Query",
        ]
        Resource = [
          var.budget_table_arn,
          "${var.budget_table_arn}/index/*",
        ]
      }
    ]
  })
}

# Policy: EC2 instance control (suspend sandbox at compute budget exhaustion)
resource "aws_iam_role_policy" "ec2_control" {
  name = "km-budget-enforcer-ec2-${var.sandbox_id}"
  role = aws_iam_role.budget_enforcer.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "ec2:StopInstances",
          "ec2:StartInstances",
          "ec2:DescribeInstances",
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "aws:ResourceTag/km:sandbox_id" = var.sandbox_id
          }
        }
      }
    ]
  })
}

# Policy: ECS task control (stop task at compute budget exhaustion)
resource "aws_iam_role_policy" "ecs_control" {
  name = "km-budget-enforcer-ecs-${var.sandbox_id}"
  role = aws_iam_role.budget_enforcer.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "ecs:StopTask",
          "ecs:DescribeTasks",
        ]
        Resource = "*"
      }
    ]
  })
}

# Policy: IAM policy detachment (Bedrock backstop when AI budget exhausted)
resource "aws_iam_role_policy" "iam_policy_control" {
  name = "km-budget-enforcer-iam-${var.sandbox_id}"
  role = aws_iam_role.budget_enforcer.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "iam:DetachRolePolicy",
          "iam:AttachRolePolicy",
          "iam:ListAttachedRolePolicies",
        ]
        Resource = var.role_arn
      }
    ]
  })
}

# Policy: SES for budget enforcement notification emails
resource "aws_iam_role_policy" "ses_send" {
  name = "km-budget-enforcer-ses-${var.sandbox_id}"
  role = aws_iam_role.budget_enforcer.id

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

# Policy: S3 access for reading stored sandbox profiles (ECS teardown path)
resource "aws_iam_role_policy" "s3_profiles" {
  name = "km-budget-enforcer-s3-${var.sandbox_id}"
  role = aws_iam_role.budget_enforcer.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = ["s3:GetObject"]
        Resource = "arn:aws:s3:::${var.state_bucket}/artifacts/${var.sandbox_id}/*"
      }
    ]
  })
}

# ============================================================
# Lambda function: budget-enforcer (Go, provided.al2023, arm64)
# ============================================================

resource "aws_lambda_function" "budget_enforcer" {
  function_name = "km-budget-enforcer-${var.sandbox_id}"
  description   = "Tracks compute spend and enforces budget limits for sandbox ${var.sandbox_id}"
  role          = aws_iam_role.budget_enforcer.arn

  # Go Lambda: custom runtime on Amazon Linux 2023, arm64 for Graviton cost efficiency
  runtime       = "provided.al2023"
  handler       = "bootstrap"
  filename      = var.lambda_zip_path
  architectures = ["arm64"]

  # Budget check is fast — 60s timeout is more than sufficient
  timeout     = 60
  memory_size = 128

  environment {
    variables = {
      KM_BUDGET_TABLE  = var.budget_table_name
      KM_EMAIL_DOMAIN  = var.email_domain
      KM_STATE_BUCKET  = var.state_bucket
    }
  }

  tags = {
    "km:component"  = "budget-enforcer"
    "km:sandbox_id" = var.sandbox_id
    "km:managed"    = "true"
  }
}

# CloudWatch Log Group for Lambda logs (30-day retention)
resource "aws_cloudwatch_log_group" "budget_enforcer" {
  name              = "/aws/lambda/km-budget-enforcer-${var.sandbox_id}"
  retention_in_days = 30

  tags = {
    "km:component"  = "budget-enforcer"
    "km:sandbox_id" = var.sandbox_id
    "km:managed"    = "true"
  }
}

# ============================================================
# IAM role for EventBridge Scheduler to invoke the Lambda
# ============================================================

resource "aws_iam_role" "scheduler_invoke" {
  name = "km-budget-scheduler-${var.sandbox_id}"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect    = "Allow"
        Principal = { Service = "scheduler.amazonaws.com" }
        Action    = "sts:AssumeRole"
      }
    ]
  })

  tags = {
    "km:component"  = "budget-enforcer"
    "km:sandbox_id" = var.sandbox_id
    "km:managed"    = "true"
  }
}

resource "aws_iam_role_policy" "scheduler_invoke_lambda" {
  name = "km-budget-scheduler-invoke-${var.sandbox_id}"
  role = aws_iam_role.scheduler_invoke.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["lambda:InvokeFunction"]
        Resource = aws_lambda_function.budget_enforcer.arn
      }
    ]
  })
}

# ============================================================
# EventBridge Scheduler: rate(1 minute) per sandbox
# ============================================================

resource "aws_scheduler_schedule" "budget_check" {
  name       = "km-budget-${var.sandbox_id}"
  group_name = "default"

  # Run every minute for real-time compute cost tracking
  schedule_expression         = "rate(1 minute)"
  schedule_expression_timezone = "UTC"

  # NONE = continue running until explicitly deleted (we delete on sandbox destroy)
  flexible_time_window {
    mode = "OFF"
  }

  target {
    arn      = aws_lambda_function.budget_enforcer.arn
    role_arn = aws_iam_role.scheduler_invoke.arn

    # EventBridge Scheduler passes this JSON payload to the Lambda on each invocation.
    # The payload is set at sandbox creation time with the instance metadata needed
    # for cost calculation and enforcement actions.
    input = jsonencode({
      sandbox_id     = var.sandbox_id
      instance_type  = var.instance_type
      spot_rate      = var.spot_rate
      substrate      = var.substrate
      created_at     = var.created_at
      role_arn       = var.role_arn
      instance_id    = var.instance_id
      task_arn       = var.task_arn
      cluster_arn    = var.cluster_arn
      operator_email = var.operator_email
    })

    retry_policy {
      maximum_retry_attempts = 0
    }
  }
}

# Lambda permission: allow EventBridge Scheduler to invoke the function
resource "aws_lambda_permission" "eventbridge_scheduler" {
  statement_id  = "AllowEventBridgeSchedulerInvoke-${var.sandbox_id}"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.budget_enforcer.function_name
  principal     = "scheduler.amazonaws.com"
  source_arn    = aws_scheduler_schedule.budget_check.arn
}
