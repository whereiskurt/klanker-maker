data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  function_name = "${var.resource_prefix}-quota-alerter"
}

# ============================================================
# IAM role for the quota-alerter Lambda
# ============================================================

resource "aws_iam_role" "quota_alerter" {
  name = "${local.function_name}-role"

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

  tags = merge(var.tags, {
    "km:component" = "quota-alerter"
    "km:managed"   = "true"
  })
}

# Policy: CloudWatch Logs
resource "aws_iam_role_policy" "cloudwatch_logs" {
  name = "${local.function_name}-cw-logs"
  role = aws_iam_role.quota_alerter.id

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

# Policy: DynamoDB Streams — read from the action-quota stream.
# These four actions are required by Lambda for event source mappings.
resource "aws_iam_role_policy" "ddb_stream_read" {
  name = "${local.function_name}-ddb-stream-read"
  role = aws_iam_role.quota_alerter.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "DDBStreamRead"
        Effect = "Allow"
        Action = [
          "dynamodb:GetRecords",
          "dynamodb:GetShardIterator",
          "dynamodb:DescribeStream",
          "dynamodb:ListStreams",
        ]
        Resource = var.quota_stream_arn
      }
    ]
  })
}

# Policy: DynamoDB — conditional UpdateItem on action-quota (set alert_sent).
resource "aws_iam_role_policy" "ddb_quota_update" {
  name = "${local.function_name}-ddb-quota-update"
  role = aws_iam_role.quota_alerter.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "DDBQuotaAlertSent"
        Effect   = "Allow"
        Action   = ["dynamodb:UpdateItem"]
        Resource = var.quota_table_arn
      }
    ]
  })
}

# Policy: DynamoDB — GetItem on km-sandboxes to resolve slack_channel_id.
# Gated on var.sandboxes_table_arn — empty = channel-notice path not enabled.
resource "aws_iam_role_policy" "ddb_sandboxes_read" {
  count = var.sandboxes_table_arn != "" ? 1 : 0
  name  = "${local.function_name}-ddb-sandboxes-read"
  role  = aws_iam_role.quota_alerter.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "DDBSandboxesGetItem"
        Effect   = "Allow"
        Action   = ["dynamodb:GetItem"]
        Resource = var.sandboxes_table_arn
      }
    ]
  })
}

# Policy: SES — send operator quota-breach notification emails.
resource "aws_iam_role_policy" "ses_send" {
  name = "${local.function_name}-ses-send"
  role = aws_iam_role.quota_alerter.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SESSendQuotaAlert"
        Effect = "Allow"
        Action = [
          "ses:SendEmail",
          "ses:SendRawEmail",
        ]
        Resource = "*"
      }
    ]
  })
}

# Policy: SSM — read Slack bot token (SecureString) for channel-level user notices.
# Gated on var.bot_token_path — empty = channel-notice posting not configured.
resource "aws_iam_role_policy" "ssm_bot_token" {
  count = var.bot_token_path != "" ? 1 : 0
  name  = "${local.function_name}-ssm-bot-token"
  role  = aws_iam_role.quota_alerter.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "SSMBotToken"
        Effect   = "Allow"
        Action   = ["ssm:GetParameter"]
        Resource = "arn:aws:ssm:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:parameter${var.bot_token_path}"
      }
    ]
  })
}

# Policy: KMS — decrypt SSM SecureString parameters and Lambda env vars.
# Only added when a CMK is configured (recommended: the platform km-platform-* key).
resource "aws_iam_role_policy" "kms_decrypt" {
  count = var.kms_key_arn != "" ? 1 : 0
  name  = "${local.function_name}-kms"
  role  = aws_iam_role.quota_alerter.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "KMSDecrypt"
        Effect   = "Allow"
        Action   = ["kms:Decrypt"]
        Resource = var.kms_key_arn
      }
    ]
  })
}

# ============================================================
# Lambda function
# ============================================================

resource "aws_lambda_function" "quota_alerter" {
  function_name    = local.function_name
  description      = "Phase 121 quota alerter: DDB-Stream triggered, sends exactly one SES operator alert per (sandbox, action, window) breach."
  role             = aws_iam_role.quota_alerter.arn
  filename         = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  timeout          = 60
  memory_size      = 256

  kms_key_arn = var.kms_key_arn != "" ? var.kms_key_arn : null

  environment {
    variables = {
      KM_OPERATOR_EMAIL        = var.operator_email
      KM_EMAIL_DOMAIN          = var.email_domain
      KM_QUOTA_TABLE           = var.quota_table_name
      KM_SANDBOX_TABLE_NAME    = var.sandboxes_table_name
      KM_SLACK_CONTROL_CHANNEL = var.slack_control_channel
      KM_BOT_TOKEN_PATH        = var.bot_token_path
      KM_RESOURCE_PREFIX       = var.resource_prefix
    }
  }

  tags = merge(var.tags, {
    "km:component" = "quota-alerter"
    "km:managed"   = "true"
  })

  # Replace the function when the IAM role is recreated (defense-in-depth).
  lifecycle {
    replace_triggered_by = [aws_iam_role.quota_alerter]
  }
}

# CloudWatch Log Group
resource "aws_cloudwatch_log_group" "quota_alerter" {
  name              = "/aws/lambda/${local.function_name}"
  retention_in_days = 30

  tags = merge(var.tags, {
    "km:component" = "quota-alerter"
    "km:managed"   = "true"
  })
}

# ============================================================
# DynamoDB Streams → Lambda event source mapping (Risk 3 —
# first aws_lambda_event_source_mapping in this codebase).
#
# starting_position = "LATEST" so the alerter only processes
# new breach events, not historical ones from before deploy.
#
# bisect_batch_on_function_error = true: on Lambda error, splits
# the batch in half and retries, isolating poison records.
# This prevents a single bad record from blocking the stream shard.
# ============================================================

resource "aws_lambda_event_source_mapping" "quota_stream" {
  event_source_arn  = var.quota_stream_arn
  function_name     = aws_lambda_function.quota_alerter.arn
  starting_position = "LATEST"
  batch_size        = 100

  # Retry configuration:
  # bisect_batch_on_function_error: isolate bad records by halving the batch
  bisect_batch_on_function_error = true
  # maximum_retry_attempts = -1 means retry indefinitely (stream records
  # expire at the shard iterator window, ~24h). Set to a lower value
  # if alert storms are a concern.
  maximum_retry_attempts = 3

  # Only process events when the Lambda is active (not throttled).
  # filter_criteria omitted — process all MODIFY/INSERT/REMOVE records
  # (the handler filters to first-breach MODIFY internally).
}
