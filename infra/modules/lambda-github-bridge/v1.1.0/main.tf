data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  function_name = "${var.resource_prefix}-github-bridge"
}

# ============================================================
# IAM role for the GitHub bridge Lambda
# ============================================================

resource "aws_iam_role" "github_bridge" {
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
    "km:component" = "github-bridge"
    "km:managed"   = "true"
  })
}

# Policy: CloudWatch Logs
resource "aws_iam_role_policy" "cloudwatch_logs" {
  name = "${local.function_name}-cw-logs"
  role = aws_iam_role.github_bridge.id

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

# Policy: KMS — decrypt SSM SecureString parameters (webhook-secret, private-key)
resource "aws_iam_role_policy" "kms_decrypt" {
  name = "${local.function_name}-kms"
  role = aws_iam_role.github_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "KMSDecrypt"
        Effect   = "Allow"
        Action   = ["kms:Decrypt"]
        Resource = var.kms_key_arn != "" ? var.kms_key_arn : "arn:aws:kms:*:${data.aws_caller_identity.current.account_id}:key/*"
      }
    ]
  })
}

# Policy: SSM — read GitHub App config (webhook-secret, bot-login, app-client-id,
# private-key, installation-id) under /{prefix}/config/github/*
resource "aws_iam_role_policy" "ssm_github_config" {
  name = "${local.function_name}-ssm-github"
  role = aws_iam_role.github_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SSMGitHubConfig"
        Effect = "Allow"
        Action = [
          "ssm:GetParameter",
          "ssm:GetParameters",
        ]
        Resource = "arn:aws:ssm:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:parameter/${var.resource_prefix}/config/github/*"
      }
    ]
  })
}

# Policy: DynamoDB — nonce conditional write for replay protection (shared with Slack bridge)
resource "aws_iam_role_policy" "dynamodb_nonce" {
  name = "${local.function_name}-dynamodb-nonce"
  role = aws_iam_role.github_bridge.id

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

# Policy: DynamoDB — alias-index GSI query (warm-path alias→sandbox_id resolution)
# + GetItem on base table (github_inbound_queue_url attribute lookup)
# + UpdateItem on base table (status write-back after auto-resume, Phase 98-06 Gap B fix)
# + DeleteItem on base table (Phase 109: clear an orphaned status=stopped row whose
#   EC2 instance is gone, so the alias resolves as absent for cold-create).
# CRITICAL: UpdateItem/DeleteItem only — full-row PutItem is intentionally excluded to
# avoid the SandboxMetadata lossy round-trip footgun (attributes not in the struct are stripped).
resource "aws_iam_role_policy" "dynamodb_sandboxes" {
  name = "${local.function_name}-dynamodb-sandboxes"
  role = aws_iam_role.github_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "DDBSandboxesAliasGSI"
        Effect = "Allow"
        Action = ["dynamodb:Query"]
        Resource = [
          "${var.sandboxes_table_arn}/index/alias-index",
        ]
      },
      {
        Sid      = "DDBSandboxesGetItem"
        Effect   = "Allow"
        Action   = ["dynamodb:GetItem"]
        Resource = var.sandboxes_table_arn
      },
      {
        Sid    = "DDBSandboxesUpdateItem"
        Effect = "Allow"
        # UpdateItem flips status=running after auto-resume; DeleteItem (Phase 109)
        # clears an orphaned status=stopped row whose instance is gone. PutItem is
        # still excluded — no full-row write access to sandbox rows.
        Action = [
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem",
        ]
        Resource = var.sandboxes_table_arn
      }
    ]
  })
}

# Policy: DynamoDB — km-github-threads GetItem/PutItem/UpdateItem for thread/session continuity
# (Phase 98-02: GH-X-CONTINUITY + GH-X-THREADBYPASS)
# Gated: only added when github_threads_table_arn is non-empty (backward compat with installs
# that predate 98-00 and have not yet applied the table module).
resource "aws_iam_role_policy" "dynamodb_github_threads" {
  count = var.github_threads_table_arn != "" ? 1 : 0

  name = "${local.function_name}-dynamodb-github-threads"
  role = aws_iam_role.github_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "DDBGitHubThreadsReadWrite"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:UpdateItem",
        ]
        Resource = var.github_threads_table_arn
      }
    ]
  })
}

# Policy: SQS — send inbound messages to per-sandbox github-inbound FIFO queues (warm path)
# Per-sandbox queues follow the naming convention {resource_prefix}-github-inbound-{sandbox_id}.fifo
resource "aws_iam_role_policy" "sqs_send_github_inbound" {
  name = "${local.function_name}-sqs-github-inbound"
  role = aws_iam_role.github_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SQSSendGitHubInbound"
        Effect = "Allow"
        Action = [
          "sqs:SendMessage",
          "sqs:GetQueueAttributes",
          "sqs:GetQueueUrl",
        ]
        Resource = "arn:aws:sqs:*:${data.aws_caller_identity.current.account_id}:${var.resource_prefix}-github-inbound-*.fifo"
      }
    ]
  })
}

# Policy: EC2 — describe and start stopped sandbox instances (auto-resume path, Phase 98-04)
# ec2:DescribeInstances scoped to "*" (Describe actions do not support resource-level conditions).
# ec2:StartInstances scoped to instances of THIS install via the km:resource-prefix tag.
# (Phase 98 gap-fix: the original condition keyed on "km:managed=true" — a tag NO km sandbox
# actually carries — so every auto-resume StartInstances was denied with UnauthorizedOperation.
# km tags every sandbox EC2 instance with km:resource-prefix=<prefix>, km:sandbox-id, km:label;
# scope to km:resource-prefix for least-privilege + multi-instance isolation.)
resource "aws_iam_role_policy" "ec2_resume" {
  name = "${local.function_name}-ec2-resume"
  role = aws_iam_role.github_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "EC2DescribeInstances"
        Effect = "Allow"
        Action = ["ec2:DescribeInstances"]
        # Describe actions do not support resource-level permissions.
        Resource = "*"
      },
      {
        Sid      = "EC2StartInstances"
        Effect   = "Allow"
        Action   = ["ec2:StartInstances"]
        Resource = "arn:aws:ec2:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:instance/*"
        Condition = {
          StringEquals = {
            "aws:ResourceTag/km:resource-prefix" = var.resource_prefix
          }
        }
      }
    ]
  })
}

# Policy: EventBridge — publish SandboxCreate events for cold-create dispatch
resource "aws_iam_role_policy" "eventbridge_put_events" {
  name = "${local.function_name}-eventbridge"
  role = aws_iam_role.github_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "EventBridgePutSandboxCreate"
        Effect = "Allow"
        Action = ["events:PutEvents"]
        # Scope to the default event bus (SandboxCreate events use the default bus).
        Resource = "arn:aws:events:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:event-bus/default"
      }
    ]
  })
}

# ============================================================
# Lambda function
# ============================================================

resource "aws_lambda_function" "github_bridge" {
  function_name    = local.function_name
  description      = "Phase 97 GitHub App bridge: verifies X-Hub-Signature-256, dispatches @-mention comments to per-repo aliased sandboxes"
  role             = aws_iam_role.github_bridge.arn
  filename         = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  # 60s mirrors lambda-slack-bridge (synchronous 👀 reaction + HMAC verify must
  # complete within GitHub's ~10s ack window; set generously for cold-start + SSM).
  timeout     = 60
  memory_size = 256

  environment {
    variables = {
      KM_RESOURCE_PREFIX        = var.resource_prefix
      KM_GITHUB_REPOS           = var.github_repos_json
      KM_GITHUB_DEFAULT_PROFILE = var.github_default_profile
      KM_NONCE_TABLE            = var.nonces_table_name
      KM_SANDBOX_TABLE_NAME     = var.sandboxes_table_name
      KM_GITHUB_THREADS_TABLE   = var.github_threads_table_name
      KM_WEBHOOK_SECRET_PATH    = var.webhook_secret_path
      KM_BOT_LOGIN_PATH         = var.bot_login_path
      KM_APP_CLIENT_ID_PATH     = var.app_client_id_path
      KM_PRIVATE_KEY_PATH       = var.private_key_path
      KM_INSTALLATION_ID_PATH   = var.installation_id_path
      KM_ARTIFACTS_BUCKET       = var.artifacts_bucket
      KM_ARTIFACTS_PREFIX       = var.artifacts_prefix
      KM_GITHUB_PEER_BRIDGES    = var.github_peer_bridges
      KM_GITHUB_DEFAULT_ROUTER  = var.github_default_router
    }
  }

  tags = merge(var.tags, {
    "km:component" = "github-bridge"
    "km:managed"   = "true"
  })

  # CLAUDE.md memory: replace_triggered_by on IAM role to avoid stale
  # aws/lambda KMS grants when the IAM role is recreated.
  lifecycle {
    replace_triggered_by = [aws_iam_role.github_bridge]
  }
}

# CloudWatch Log Group
resource "aws_cloudwatch_log_group" "github_bridge" {
  name              = "/aws/lambda/${local.function_name}"
  retention_in_days = 30

  tags = merge(var.tags, {
    "km:component" = "github-bridge"
    "km:managed"   = "true"
  })
}

# ============================================================
# Lambda Function URL
#
# authorization_type = "NONE" because auth is application-layer:
#   X-Hub-Signature-256 HMAC verify + nonce replay protection.
#   No IAM auth needed at the HTTP layer.
# ============================================================

resource "aws_lambda_function_url" "github_bridge" {
  function_name      = aws_lambda_function.github_bridge.function_name
  authorization_type = "NONE"

  cors {
    allow_origins = ["*"]
    allow_methods = ["POST"]
    allow_headers = ["content-type", "x-hub-signature-256", "x-github-event", "x-github-delivery"]
  }
}

# Explicit resource-based policy for public Function URL invocation.
# Without this, Lambda replacement (including role-triggered replacements)
# causes the URL to return 403 until the policy is manually re-added.
resource "aws_lambda_permission" "function_url_public" {
  statement_id           = "FunctionURLAllowPublicAccess"
  action                 = "lambda:InvokeFunctionUrl"
  function_name          = aws_lambda_function.github_bridge.function_name
  principal              = "*"
  function_url_auth_type = "NONE"
}
