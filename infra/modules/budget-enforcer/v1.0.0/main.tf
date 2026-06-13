# ============================================================
# IAM role for the budget-enforcer Lambda
# ============================================================

resource "aws_iam_role" "budget_enforcer" {
  name = "${var.resource_prefix}-budget-enforcer-${var.sandbox_id}"

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
  name = "${var.resource_prefix}-budget-enforcer-cw-logs-${var.sandbox_id}"
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
  name = "${var.resource_prefix}-budget-enforcer-dynamodb-${var.sandbox_id}"
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
  name = "${var.resource_prefix}-budget-enforcer-ec2-${var.sandbox_id}"
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
  name = "${var.resource_prefix}-budget-enforcer-ecs-${var.sandbox_id}"
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
  name = "${var.resource_prefix}-budget-enforcer-iam-${var.sandbox_id}"
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
  name = "${var.resource_prefix}-budget-enforcer-ses-${var.sandbox_id}"
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

# Policy: DynamoDB sandbox metadata (lock check and status update)
resource "aws_iam_role_policy" "dynamodb_sandbox" {
  count = var.sandbox_table_arn != "" ? 1 : 0
  name  = "${var.resource_prefix}-budget-enforcer-dynamo-sandbox-${var.sandbox_id}"
  role  = aws_iam_role.budget_enforcer.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:UpdateItem",
          "dynamodb:PutItem",
        ]
        Resource = var.sandbox_table_arn
      }
    ]
  })
}

# Policy: EventBridge Scheduler (delete TTL schedule on budget enforcement)
resource "aws_iam_role_policy" "scheduler_cleanup" {
  name = "${var.resource_prefix}-budget-enforcer-scheduler-${var.sandbox_id}"
  role = aws_iam_role.budget_enforcer.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["scheduler:DeleteSchedule"]
        Resource = "*"
      }
    ]
  })
}

# Policy: S3 access for reading stored sandbox profiles (ECS teardown path)
resource "aws_iam_role_policy" "s3_profiles" {
  name = "${var.resource_prefix}-budget-enforcer-s3-${var.sandbox_id}"
  role = aws_iam_role.budget_enforcer.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["s3:GetObject"]
        Resource = "arn:aws:s3:::${var.state_bucket}/artifacts/${var.sandbox_id}/*"
      }
    ]
  })
}

# ============================================================
# Lambda function: budget-enforcer (Go, provided.al2023, arm64)
# ============================================================

resource "aws_lambda_function" "budget_enforcer" {
  function_name = "${var.resource_prefix}-budget-enforcer-${var.sandbox_id}"
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

  # Encrypt env vars under the IAM-delegating platform CMK (var.kms_key_arn) so the
  # role's identity kms:Decrypt (granted below) authorizes env decryption directly —
  # no role-pinned grant to orphan on role recreate. null = aws/lambda managed-key
  # default when unset (fail-soft on installs where the CMK ARN isn't plumbed).
  kms_key_arn = var.kms_key_arn != "" ? var.kms_key_arn : null

  environment {
    variables = {
      KM_BUDGET_TABLE = var.budget_table_name
      # Binary reads KM_SANDBOX_TABLE_NAME (cmd/budget-enforcer/main.go).
      # Was set as KM_SANDBOX_TABLE — close but wrong, off by the _NAME
      # suffix — so the binary fell back to its hardcoded km-sandboxes
      # default and ignored the kph-sandboxes value the operator passed.
      KM_SANDBOX_TABLE_NAME = var.sandbox_table_name
      # Binary uses KM_RESOURCE_PREFIX for prefix-aware paths; without it
      # falls back to literal "km" and writes/reads the wrong resource names.
      KM_RESOURCE_PREFIX = var.resource_prefix
      KM_EMAIL_DOMAIN    = var.email_domain
      KM_STATE_BUCKET    = var.state_bucket
    }
  }

  tags = {
    "km:component"  = "budget-enforcer"
    "km:sandbox_id" = var.sandbox_id
    "km:managed"    = "true"
  }

  # Belt-and-suspenders: replace on role change. With kms_key_arn set above, env
  # decrypt is grant-independent so this is no longer the primary safeguard.
  lifecycle {
    replace_triggered_by = [aws_iam_role.budget_enforcer]
  }
}

# Policy: KMS — decrypt env vars encrypted under the platform CMK (var.kms_key_arn).
# count-gated: only created when the CMK ARN is plumbed; absent ⇒ managed-key fallback
# (no policy needed). This is the identity authorization that makes env decrypt
# grant-independent so role recreation can't strand the function.
resource "aws_iam_role_policy" "kms_decrypt" {
  count = var.kms_key_arn != "" ? 1 : 0
  name  = "${var.resource_prefix}-budget-enforcer-kms-${var.sandbox_id}"
  role  = aws_iam_role.budget_enforcer.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "KMSDecryptEnv"
        Effect   = "Allow"
        Action   = ["kms:Decrypt"]
        Resource = var.kms_key_arn
      }
    ]
  })
}

# CloudWatch Log Group for Lambda logs (30-day retention)
resource "aws_cloudwatch_log_group" "budget_enforcer" {
  name              = "/aws/lambda/${var.resource_prefix}-budget-enforcer-${var.sandbox_id}"
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
  name = "${var.resource_prefix}-budget-scheduler-${var.sandbox_id}"

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
  name = "${var.resource_prefix}-budget-scheduler-invoke-${var.sandbox_id}"
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
  name       = "${var.resource_prefix}-budget-${var.sandbox_id}"
  group_name = "default"

  # Run every minute for real-time compute cost tracking
  schedule_expression          = "rate(1 minute)"
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
