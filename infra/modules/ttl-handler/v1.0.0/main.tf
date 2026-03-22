terraform {
  required_version = ">= 1.3"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
}

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

# ============================================================
# Lambda function: TTL handler (Go, provided.al2023, arm64)
# ============================================================

resource "aws_lambda_function" "ttl_handler" {
  function_name = "km-ttl-handler"
  description   = "Uploads artifacts and sends ttl-expired notification when EventBridge TTL fires"
  role          = aws_iam_role.ttl_handler.arn

  # Go Lambda: custom runtime on Amazon Linux 2023, arm64 for Graviton cost efficiency
  runtime  = "provided.al2023"
  handler  = "bootstrap"
  filename = var.lambda_zip_path

  # Generous timeout: artifact upload may take time for large sandboxes
  timeout     = 300
  memory_size = 256
  architectures = ["arm64"]

  environment {
    variables = {
      KM_ARTIFACTS_BUCKET = var.artifact_bucket_name
      KM_EMAIL_DOMAIN     = var.email_domain
      KM_OPERATOR_EMAIL   = var.operator_email
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
# EventBridge Scheduler permission: allow scheduler to invoke Lambda
# ============================================================

resource "aws_lambda_permission" "eventbridge_scheduler" {
  statement_id  = "AllowEventBridgeSchedulerInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.ttl_handler.function_name
  principal     = "scheduler.amazonaws.com"
}
