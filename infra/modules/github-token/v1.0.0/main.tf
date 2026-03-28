# ============================================================
# KMS key for encrypting the GitHub installation token in SSM
# ============================================================

locals {
  # Default SSM parameter name if not explicitly provided
  ssm_param = var.ssm_parameter_name != "" ? var.ssm_parameter_name : "/sandbox/${var.sandbox_id}/github-token"
}

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

resource "aws_kms_key" "github_token" {
  description             = "KMS key for km-github-token-refresher-${var.sandbox_id} SSM SecureString encryption"
  enable_key_rotation     = true
  deletion_window_in_days = 7

  # Key policy: root admin grants full access. Individual roles (Lambda, sandbox)
  # get their KMS permissions via IAM policies attached to their roles, not via
  # key policy principals. This avoids KMS InvalidArnException when roles don't
  # exist yet at key creation time (e.g. remote create via Lambda).
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "AllowRootAdmin"
        Effect = "Allow"
        Principal = {
          AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"
        }
        Action   = "kms:*"
        Resource = "*"
      },
    ]
  })

  tags = {
    "km:component"  = "github-token-refresher"
    "km:sandbox_id" = var.sandbox_id
    "km:managed"    = "true"
  }
}

resource "aws_kms_alias" "github_token" {
  name          = "alias/km-github-token-${var.sandbox_id}"
  target_key_id = aws_kms_key.github_token.key_id
}

# ============================================================
# IAM role for the github-token-refresher Lambda
# ============================================================

resource "aws_iam_role" "github_token_refresher" {
  name = "km-github-token-refresher-${var.sandbox_id}"

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
    "km:component"  = "github-token-refresher"
    "km:sandbox_id" = var.sandbox_id
    "km:managed"    = "true"
  }
}

resource "aws_iam_role_policy" "github_token_refresher" {
  name = "km-github-token-refresher-policy-${var.sandbox_id}"
  role = aws_iam_role.github_token_refresher.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "ReadGitHubAppConfig"
        Effect = "Allow"
        Action = ["ssm:GetParameter", "ssm:GetParameters"]
        Resource = [
          "arn:aws:ssm:${data.aws_region.current.id}:${data.aws_caller_identity.current.account_id}:parameter/km/config/github/*"
        ]
      },
      {
        Sid    = "WriteGitHubToken"
        Effect = "Allow"
        Action = ["ssm:PutParameter"]
        Resource = [
          "arn:aws:ssm:${data.aws_region.current.id}:${data.aws_caller_identity.current.account_id}:parameter${local.ssm_param}"
        ]
      },
      {
        Sid    = "UseKMSKey"
        Effect = "Allow"
        Action = [
          "kms:Encrypt",
          "kms:Decrypt",
          "kms:GenerateDataKey",
        ]
        Resource = [aws_kms_key.github_token.arn]
      },
      {
        Sid    = "CloudWatchLogs"
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents",
        ]
        Resource = "arn:aws:logs:*:*:*"
      },
    ]
  })
}

# ============================================================
# Lambda function: github-token-refresher (Go, provided.al2023, arm64)
# ============================================================

resource "aws_lambda_function" "github_token_refresher" {
  function_name = "km-github-token-refresher-${var.sandbox_id}"
  description   = "Refreshes GitHub App installation token every 45 minutes for sandbox ${var.sandbox_id}"
  role          = aws_iam_role.github_token_refresher.arn

  # Go Lambda: custom runtime on Amazon Linux 2023, arm64 for Graviton cost efficiency
  runtime       = "provided.al2023"
  handler       = "bootstrap"
  filename      = var.lambda_zip_path
  architectures = ["arm64"]

  timeout     = 60
  memory_size = 128

  environment {
    variables = {
      KM_GITHUB_SSM_CONFIG_PREFIX = "/km/config/github"
    }
  }

  tags = {
    "km:component"  = "github-token-refresher"
    "km:sandbox_id" = var.sandbox_id
    "km:managed"    = "true"
  }
}

# CloudWatch Log Group for Lambda logs (30-day retention)
resource "aws_cloudwatch_log_group" "github_token_refresher" {
  name              = "/aws/lambda/km-github-token-refresher-${var.sandbox_id}"
  retention_in_days = 30

  tags = {
    "km:component"  = "github-token-refresher"
    "km:sandbox_id" = var.sandbox_id
    "km:managed"    = "true"
  }
}

# ============================================================
# IAM role for EventBridge Scheduler to invoke the Lambda
# ============================================================

resource "aws_iam_role" "scheduler_invoke" {
  name = "km-github-token-scheduler-${var.sandbox_id}"

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
    "km:component"  = "github-token-refresher"
    "km:sandbox_id" = var.sandbox_id
    "km:managed"    = "true"
  }
}

resource "aws_iam_role_policy" "scheduler_invoke_lambda" {
  name = "km-github-token-scheduler-invoke-${var.sandbox_id}"
  role = aws_iam_role.scheduler_invoke.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["lambda:InvokeFunction"]
        Resource = aws_lambda_function.github_token_refresher.arn
      }
    ]
  })
}

# ============================================================
# EventBridge Scheduler: rate(45 minutes) per sandbox
# ============================================================

resource "aws_scheduler_schedule" "github_token_refresh" {
  name       = "km-github-token-${var.sandbox_id}"
  group_name = "default"

  # Token refresh every 45 minutes — GitHub installation tokens expire after 1 hour
  schedule_expression          = "rate(45 minutes)"
  schedule_expression_timezone = "UTC"

  # NONE = continue running until explicitly deleted (we delete on sandbox destroy)
  flexible_time_window {
    mode = "OFF"
  }

  target {
    arn      = aws_lambda_function.github_token_refresher.arn
    role_arn = aws_iam_role.scheduler_invoke.arn

    # EventBridge Scheduler passes this JSON payload to the Lambda on each invocation.
    # All metadata needed to mint and store the token is embedded at sandbox creation time.
    input = jsonencode({
      sandbox_id         = var.sandbox_id
      installation_id    = var.installation_id
      ssm_parameter_name = local.ssm_param
      kms_key_arn        = aws_kms_key.github_token.arn
      allowed_repos      = var.allowed_repos
      permissions        = var.permissions
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
  function_name = aws_lambda_function.github_token_refresher.function_name
  principal     = "scheduler.amazonaws.com"
  source_arn    = aws_scheduler_schedule.github_token_refresh.arn
}
