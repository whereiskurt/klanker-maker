data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  function_name = "km-slack-bridge"
}

# ============================================================
# IAM role for the Slack bridge Lambda
# ============================================================

resource "aws_iam_role" "slack_bridge" {
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
    "km:component" = "slack-bridge"
    "km:managed"   = "true"
  })
}

# Policy: CloudWatch Logs
resource "aws_iam_role_policy" "cloudwatch_logs" {
  name = "${local.function_name}-cw-logs"
  role = aws_iam_role.slack_bridge.id

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

# Policy: SSM — read bot token (SecureString, decrypted at read time by KMS)
resource "aws_iam_role_policy" "ssm_bot_token" {
  name = "${local.function_name}-ssm"
  role = aws_iam_role.slack_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["ssm:GetParameter"]
        Resource = "arn:aws:ssm:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:parameter${var.bot_token_path}"
      }
    ]
  })
}

# Policy: KMS — decrypt SSM SecureString parameters and Lambda env vars
resource "aws_iam_role_policy" "kms_decrypt" {
  name = "${local.function_name}-kms"
  role = aws_iam_role.slack_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "KMSDecrypt"
        Effect = "Allow"
        Action = ["kms:Decrypt"]
        # Scope to the platform KMS key; allow wildcard so the Lambda also
        # decrypts env vars if the key ARN changes.
        Resource = var.kms_key_arn != "" ? var.kms_key_arn : "arn:aws:kms:*:${data.aws_caller_identity.current.account_id}:key/*"
      }
    ]
  })
}

# Policy: DynamoDB — public key lookup (km-identities) + channel lookup (km-sandboxes)
# Per RESEARCH.md correction #1: public keys are in DynamoDB, NOT SSM.
resource "aws_iam_role_policy" "dynamodb_read" {
  name = "${local.function_name}-dynamodb-read"
  role = aws_iam_role.slack_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "PublicKeyAndChannelLookup"
        Effect = "Allow"
        Action = ["dynamodb:GetItem"]
        Resource = [
          var.identities_table_arn,
          var.sandboxes_table_arn,
        ]
      }
    ]
  })
}

# Policy: DynamoDB — nonce conditional write for replay protection
resource "aws_iam_role_policy" "dynamodb_nonce" {
  name = "${local.function_name}-dynamodb-nonce"
  role = aws_iam_role.slack_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "NonceReplayProtection"
        Effect   = "Allow"
        Action   = ["dynamodb:PutItem"]
        Resource = var.nonces_table_arn
      }
    ]
  })
}

# Policy: SQS — send inbound messages to per-sandbox FIFO queues (Phase 67-05)
# Per-sandbox queues are runtime-created with the naming convention
# {resource_prefix}-slack-inbound-{sandbox_id}.fifo; wildcard covers all of them.
resource "aws_iam_role_policy" "sqs_send_inbound" {
  name = "${local.function_name}-sqs-inbound"
  role = aws_iam_role.slack_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SQSSendInbound"
        Effect = "Allow"
        Action = [
          "sqs:SendMessage",
          "sqs:GetQueueAttributes",
          "sqs:GetQueueUrl",
        ]
        Resource = "arn:aws:sqs:*:${data.aws_caller_identity.current.account_id}:${var.resource_prefix}-slack-inbound-*.fifo"
      }
    ]
  })
}

# Policy: DynamoDB — Slack thread tracking reads/writes (Phase 67-05)
resource "aws_iam_role_policy" "dynamodb_slack_threads" {
  name = "${local.function_name}-dynamodb-slack-threads"
  role = aws_iam_role.slack_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "DDBSlackThreads"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:Query",
        ]
        Resource = [
          "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.slack_threads_table_name}",
          "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.slack_threads_table_name}/index/*",
        ]
      },
      {
        Sid    = "DDBSandboxesChannelGSI"
        Effect = "Allow"
        Action = ["dynamodb:Query"]
        Resource = [
          "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.sandboxes_table_name}/index/slack_channel_id-index",
        ]
      }
    ]
  })
}

# Policy: DynamoDB — UpdateItem on km-sandboxes for DDBPauseHinter LWT (Phase 67-05)
# Phase 63 already grants GetItem on km-sandboxes via dynamodb_read above.
# Phase 67 adds UpdateItem for the last_pause_hint_ts cooldown attribute.
# NOTE: DynamoDB IAM does not support attribute-level scoping; the bridge code
# only writes last_pause_hint_ts.
resource "aws_iam_role_policy" "dynamodb_sandboxes_pause_hint" {
  name = "${local.function_name}-dynamodb-sandboxes-pause-hint"
  role = aws_iam_role.slack_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "DDBSandboxesUpdateLastPauseHint"
        Effect   = "Allow"
        Action   = ["dynamodb:UpdateItem"]
        Resource = var.sandboxes_table_arn
      }
    ]
  })
}

# Phase 68: bridge reads transcripts under transcripts/* on the artifacts bucket.
# The IAM grant is intentionally broad (cross-sandbox) — the bridge enforces
# per-sandbox prefix at envelope-validation time inside handler.go before
# GetObject. The application-layer check is the security boundary; this IAM
# grant is just the AWS-side "you are allowed to attempt the call" gate.
# Gated on var.artifacts_bucket — when empty, the policy is omitted.
# RESEARCH Pitfall 4: adding a policy to the role does NOT trigger the
# replace_triggered_by chain on the Lambda function (only role recreation does).
resource "aws_iam_role_policy" "slack_bridge_transcript_s3_read" {
  count = var.artifacts_bucket != "" ? 1 : 0
  name  = "${local.function_name}-transcript-s3-read"
  role  = aws_iam_role.slack_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "S3GetTranscripts"
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:HeadObject",
        ]
        Resource = "arn:aws:s3:::${var.artifacts_bucket}/transcripts/*"
      }
    ]
  })
}

# Policy: SSM — read signing secret (SecureString, decrypted via kms_decrypt above)
resource "aws_iam_role_policy" "ssm_signing_secret" {
  name = "${local.function_name}-ssm-signing-secret"
  role = aws_iam_role.slack_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SSMSigningSecret"
        Effect = "Allow"
        Action = [
          "ssm:GetParameter",
          "ssm:GetParameters",
        ]
        Resource = "arn:aws:ssm:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:parameter${var.signing_secret_path}"
      }
    ]
  })
}

# ============================================================
# Lambda function
# ============================================================

resource "aws_lambda_function" "slack_bridge" {
  function_name    = local.function_name
  description      = "Phase 63 Slack-notify bridge: verifies Ed25519-signed envelopes and dispatches to Slack Web API"
  role             = aws_iam_role.slack_bridge.arn
  filename         = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  timeout          = 15
  memory_size      = 256

  environment {
    variables = {
      KM_IDENTITIES_TABLE = var.identities_table_name
      KM_SANDBOXES_TABLE  = var.sandboxes_table_name
      KM_NONCE_TABLE      = var.nonces_table_name
      KM_BOT_TOKEN_PATH   = var.bot_token_path
      # Phase 67-05 additions — inbound events path
      KM_SIGNING_SECRET_PATH = var.signing_secret_path
      KM_SLACK_THREADS_TABLE = var.slack_threads_table_name
      KM_RESOURCE_PREFIX     = var.resource_prefix
      # Phase 67.1 addition — ACK reaction emoji
      KM_SLACK_ACK_EMOJI = var.slack_ack_emoji
    }
  }

  tags = merge(var.tags, {
    "km:component" = "slack-bridge"
    "km:managed"   = "true"
  })

  # CLAUDE.md memory: replace_triggered_by on IAM role to avoid stale
  # aws/lambda KMS grants when the IAM role is recreated.
  lifecycle {
    replace_triggered_by = [aws_iam_role.slack_bridge]
  }
}

# CloudWatch Log Group
resource "aws_cloudwatch_log_group" "slack_bridge" {
  name              = "/aws/lambda/${local.function_name}"
  retention_in_days = 30

  tags = merge(var.tags, {
    "km:component" = "slack-bridge"
    "km:managed"   = "true"
  })
}

# ============================================================
# Lambda Function URL — first Function URL in this codebase
#
# authorization_type = "NONE" because auth is application-layer:
#   Ed25519 Ed25519 signature + nonce table provide replay protection.
#   No IAM auth needed at the HTTP layer.
# ============================================================

resource "aws_lambda_function_url" "slack_bridge" {
  function_name      = aws_lambda_function.slack_bridge.function_name
  authorization_type = "NONE"

  cors {
    allow_origins = ["*"]
    allow_methods = ["POST"]
    allow_headers = ["content-type", "x-km-sender-id", "x-km-signature"]
  }
}
