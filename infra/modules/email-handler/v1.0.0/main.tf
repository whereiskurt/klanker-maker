data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

# ============================================================
# IAM role for the email handler Lambda
# ============================================================

resource "aws_iam_role" "email_handler" {
  name = "km-email-handler"

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
    "km:component" = "email-handler"
    "km:managed"   = "true"
  }
}

# Policy: CloudWatch Logs
resource "aws_iam_role_policy" "cloudwatch_logs" {
  name = "km-email-handler-cw-logs"
  role = aws_iam_role.email_handler.id

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

# Policy: S3 — read emails from mail/create/ prefix, read metadata, upload profiles
resource "aws_iam_role_policy" "s3_access" {
  name = "km-email-handler-s3"
  role = aws_iam_role.email_handler.id

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
      },
      {
        Effect = "Allow"
        Action = [
          "s3:GetObject",
        ]
        Resource = [
          "arn:aws:s3:::${var.state_bucket}/tf-km/sandboxes/*/metadata.json",
        ]
      },
    ]
  })
}

# Policy: SES — send reply emails
resource "aws_iam_role_policy" "ses_send" {
  name = "km-email-handler-ses"
  role = aws_iam_role.email_handler.id

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

# Policy: SSM — read safe phrase parameter
resource "aws_iam_role_policy" "ssm_read" {
  name = "km-email-handler-ssm"
  role = aws_iam_role.email_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["ssm:GetParameter"]
        Resource = "arn:aws:ssm:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:parameter${var.safe_phrase_ssm_key}"
      }
    ]
  })
}

# Policy: EventBridge — publish SandboxCreate events
resource "aws_iam_role_policy" "eventbridge_publish" {
  name = "km-email-handler-eventbridge"
  role = aws_iam_role.email_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["events:PutEvents"]
        Resource = "arn:aws:events:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:event-bus/default"
      }
    ]
  })
}

# Policy: EventBridge Scheduler — create/manage schedules for deferred operations
resource "aws_iam_role_policy" "scheduler" {
  count = var.scheduler_role_arn != "" ? 1 : 0

  name = "km-email-handler-scheduler"
  role = aws_iam_role.email_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "scheduler:CreateSchedule",
          "scheduler:DeleteSchedule",
          "scheduler:GetSchedule",
        ]
        Resource = "arn:aws:scheduler:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:schedule/km-at/*"
      },
      {
        Effect   = "Allow"
        Action   = ["iam:PassRole"]
        Resource = var.scheduler_role_arn
      }
    ]
  })
}

# Policy: KMS — decrypt SSM SecureString parameters and Lambda env vars
resource "aws_iam_role_policy" "kms_decrypt" {
  name = "km-email-handler-kms"
  role = aws_iam_role.email_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "KMSDecrypt"
        Effect   = "Allow"
        Action   = ["kms:Decrypt"]
        Resource = "arn:aws:kms:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:key/*"
      }
    ]
  })
}

# Policy: Bedrock — invoke Haiku model for AI email interpretation
resource "aws_iam_role_policy" "bedrock_invoke" {
  name = "km-email-handler-bedrock"
  role = aws_iam_role.email_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["bedrock:InvokeModel"]
        Resource = [
          "arn:aws:bedrock:${data.aws_region.current.name}::foundation-model/*anthropic*",
          "arn:aws:bedrock:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:inference-profile/*anthropic*",
          "arn:aws:bedrock:us-*::foundation-model/*anthropic*"
        ]
      }
    ]
  })
}

# Policy: DynamoDB km-sandboxes — read/write sandbox metadata
resource "aws_iam_role_policy" "dynamodb_sandboxes" {
  name = "km-email-handler-dynamodb-sandboxes"
  role = aws_iam_role.email_handler.id

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

# ============================================================
# Lambda function
# ============================================================

resource "aws_lambda_function" "email_handler" {
  function_name = "km-email-create-handler"
  description   = "Processes operator emails: create sandboxes, check status"
  role          = aws_iam_role.email_handler.arn

  runtime          = "provided.al2023"
  handler          = "bootstrap"
  filename         = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)

  timeout       = 120
  memory_size   = 256
  architectures = ["arm64"]

  environment {
    variables = {
      KM_ARTIFACTS_BUCKET    = var.artifact_bucket_name
      KM_STATE_BUCKET        = var.state_bucket
      KM_EMAIL_DOMAIN        = var.email_domain
      KM_SAFE_PHRASE_SSM_KEY = var.safe_phrase_ssm_key
      SANDBOX_TABLE_NAME     = "km-sandboxes"
      BEDROCK_MODEL_ID       = var.bedrock_model_id
      KM_SCHEDULER_ROLE_ARN  = var.scheduler_role_arn
      KM_CREATE_HANDLER_ARN  = var.create_handler_arn
    }
  }

  tags = {
    "km:component" = "email-handler"
    "km:managed"   = "true"
  }

  # Replace Lambda if role is replaced — Lambda env-var KMS grants bind to role unique-ID
  lifecycle {
    replace_triggered_by = [aws_iam_role.email_handler]
  }
}

# CloudWatch Log Group
resource "aws_cloudwatch_log_group" "email_handler" {
  name              = "/aws/lambda/km-email-create-handler"
  retention_in_days = 30

  tags = {
    "km:component" = "email-handler"
    "km:managed"   = "true"
  }
}
