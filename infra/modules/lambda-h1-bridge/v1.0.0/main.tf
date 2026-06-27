data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  function_name = "${var.resource_prefix}-h1-bridge"
}

# ============================================================
# km-h1-bridge — Phase 103 HackerOne comment-trigger bridge Lambda
#
# Forked from infra/modules/lambda-github-bridge/v1.1.0. The structural deltas vs
# the GitHub module:
#   - DROPPED: GitHub App credential SSM params (client-id/private-key/installation-id)
#     and the bot-login param — HackerOne's customer API is HTTP Basic Auth (no App
#     install model). DROPPED the federation peer_bridges + default_router env vars
#     (federation out of scope — each H1 program webhook points at one install's URL).
#   - ADDED: SSM read of /{prefix}/config/h1/* (webhook-secret, api-username, api-token,
#     commands), the km-h1-threads RW grant, and the h1-inbound-*.fifo SQS send grant.
#
# IMPORTANT (CLAUDE.md memory project_terragrunt_providers_in_root):
#   This module does NOT declare required_providers — root.hcl's generate "provider"
#   stanza is the single source of provider configuration across all modules.
#
# IAM <-> runtime cross-check (an init_test guards the PRESENCE of this block). Every
# AWS call the Lambda makes at runtime has a matching grant below:
#   - SSMSecretFetcher.Fetch / readH1APICreds / SSMCommandsFetcher.Fetch
#       -> ssm:GetParameter(s) on /{prefix}/config/h1/* + kms:Decrypt (SecureString)
#   - DynamoH1NonceStore.CheckAndStore        -> dynamodb:PutItem (nonces table)
#   - DynamoAliasResolver.ResolveByAlias*      -> dynamodb:Query (sandboxes alias-index)
#   - DynamoAliasResolver.H1QueueURL           -> dynamodb:GetItem (sandboxes base table)
#   - DynamoSandboxStatusWriter.SetStatusRunning -> dynamodb:UpdateItem (sandboxes base table)
#   - DynamoH1ThreadStore.*                     -> dynamodb:GetItem/PutItem/UpdateItem (h1-threads)
#   - H1SQSAdapter.Send                         -> sqs:SendMessage ({prefix}-h1-inbound-*.fifo)
#   - EventBridgeAdapter.PutSandboxCreate       -> events:PutEvents (default bus)
#   - EC2Resumer.StartSandbox                   -> ec2:DescribeInstances + ec2:StartInstances
# ============================================================

# ============================================================
# IAM role for the H1 bridge Lambda
# ============================================================

resource "aws_iam_role" "h1_bridge" {
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
    "km:component" = "h1-bridge"
    "km:managed"   = "true"
  })
}

# Policy: CloudWatch Logs
resource "aws_iam_role_policy" "cloudwatch_logs" {
  name = "${local.function_name}-cw-logs"
  role = aws_iam_role.h1_bridge.id

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

# Policy: KMS — decrypt SSM SecureString parameters (webhook-secret, api-token)
resource "aws_iam_role_policy" "kms_decrypt" {
  name = "${local.function_name}-kms"
  role = aws_iam_role.h1_bridge.id

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

# Policy: SSM — read HackerOne config (webhook-secret, api-username, api-token,
# commands) under /{prefix}/config/h1/*
resource "aws_iam_role_policy" "ssm_h1_config" {
  name = "${local.function_name}-ssm-h1"
  role = aws_iam_role.h1_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SSMH1Config"
        Effect = "Allow"
        Action = [
          "ssm:GetParameter",
          "ssm:GetParameters",
        ]
        Resource = "arn:aws:ssm:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:parameter/${var.resource_prefix}/config/h1/*"
      }
    ]
  })
}

# Policy: DynamoDB — nonce conditional write for replay protection (shared with Slack/GitHub bridges)
resource "aws_iam_role_policy" "dynamodb_nonce" {
  name = "${local.function_name}-dynamodb-nonce"
  role = aws_iam_role.h1_bridge.id

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
# + GetItem on base table (h1_inbound_queue_url attribute lookup)
# + UpdateItem on base table (status write-back after auto-resume)
# + DeleteItem on base table (Phase 109: clear an orphaned status=stopped row whose
#   EC2 instance is gone, so the alias resolves as absent for cold-create).
# CRITICAL: UpdateItem/DeleteItem only — full-row PutItem is intentionally excluded to
# avoid the SandboxMetadata lossy round-trip footgun (attributes not in the struct are stripped).
resource "aws_iam_role_policy" "dynamodb_sandboxes" {
  name = "${local.function_name}-dynamodb-sandboxes"
  role = aws_iam_role.h1_bridge.id

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

# Policy: DynamoDB — km-h1-threads GetItem/PutItem/UpdateItem for (report_id, target)
# thread/session continuity. Gated: only added when h1_threads_table_arn is non-empty
# (backward compat with installs that have not yet applied the table module from Plan 08).
resource "aws_iam_role_policy" "dynamodb_h1_threads" {
  count = var.h1_threads_table_arn != "" ? 1 : 0

  name = "${local.function_name}-dynamodb-h1-threads"
  role = aws_iam_role.h1_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "DDBH1ThreadsReadWrite"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:UpdateItem",
        ]
        Resource = var.h1_threads_table_arn
      }
    ]
  })
}

# Phase 121 (H1-01): quota table read/write for h1_comment metering.
# Gated on var.quota_table_arn — empty = table not yet provisioned → policy omitted.
resource "aws_iam_role_policy" "dynamodb_action_quota" {
  count = var.quota_table_arn != "" ? 1 : 0
  name  = "${local.function_name}-dynamodb-action-quota"
  role  = aws_iam_role.h1_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "DDBActionQuotaReadWrite"
        Effect = "Allow"
        Action = [
          "dynamodb:UpdateItem",
          "dynamodb:GetItem",
        ]
        Resource = var.quota_table_arn
      }
    ]
  })
}

# Policy: SQS — send inbound messages to per-sandbox h1-inbound FIFO queues (warm path)
# Per-sandbox queues follow the naming convention {resource_prefix}-h1-inbound-{sandbox_id}.fifo
resource "aws_iam_role_policy" "sqs_send_h1_inbound" {
  name = "${local.function_name}-sqs-h1-inbound"
  role = aws_iam_role.h1_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SQSSendH1Inbound"
        Effect = "Allow"
        Action = [
          "sqs:SendMessage",
          "sqs:GetQueueAttributes",
          "sqs:GetQueueUrl",
        ]
        Resource = "arn:aws:sqs:*:${data.aws_caller_identity.current.account_id}:${var.resource_prefix}-h1-inbound-*.fifo"
      }
    ]
  })
}

# Policy: EC2 — describe and start stopped sandbox instances (auto-resume path)
# ec2:DescribeInstances scoped to "*" (Describe actions do not support resource-level
# conditions). ec2:StartInstances scoped to THIS install via the km:resource-prefix tag.
resource "aws_iam_role_policy" "ec2_resume" {
  name = "${local.function_name}-ec2-resume"
  role = aws_iam_role.h1_bridge.id

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
  role = aws_iam_role.h1_bridge.id

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

resource "aws_lambda_function" "h1_bridge" {
  function_name    = local.function_name
  description      = "Phase 103 HackerOne bridge: verifies X-H1-Signature, dispatches auto-triage events + @-handle comments to per-program aliased sandboxes (multi-target fanout)"
  role             = aws_iam_role.h1_bridge.arn
  filename         = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  # 60s mirrors lambda-github-bridge (synchronous internal ACK + HMAC verify + SSM
  # fetch must complete inside HackerOne's redelivery window; generous for cold-start).
  timeout     = 60
  memory_size = 256

  # Encrypt env vars under the customer-managed platform CMK (var.kms_key_arn, an
  # IAM-delegating key) instead of the aws/lambda managed key. The role's identity
  # kms:Decrypt (scoped to var.kms_key_arn above) then authorizes env decryption
  # DIRECTLY — no role-pinned KMS grant — so a role-recreating km init can no longer
  # orphan the grant and 502 the function. null = managed-key default when unset.
  kms_key_arn = var.kms_key_arn != "" ? var.kms_key_arn : null

  environment {
    variables = {
      KM_RESOURCE_PREFIX      = var.resource_prefix
      KM_H1_PROGRAMS          = var.h1_programs_json
      KM_H1_DEFAULT_PROFILE   = var.h1_default_profile
      KM_H1_BOT_HANDLE        = var.h1_bot_handle
      KM_NONCE_TABLE          = var.nonces_table_name
      KM_SANDBOX_TABLE_NAME   = var.sandboxes_table_name
      KM_H1_THREADS_TABLE     = var.h1_threads_table_name
      KM_WEBHOOK_SECRET_PATH  = var.webhook_secret_path
      KM_H1_API_USERNAME_PATH = var.api_username_path
      KM_H1_API_TOKEN_PATH    = var.api_token_path
      KM_H1_API_BASE_URL      = var.h1_api_base_url
      KM_COMMANDS_PATH        = var.commands_path
      KM_ARTIFACTS_BUCKET     = var.artifacts_bucket
      KM_ARTIFACTS_PREFIX     = var.artifacts_prefix
      # Phase 121 — action-quota table name for bridge-side quota enforcement
      KM_QUOTA_TABLE = var.quota_table_arn != "" ? "${var.resource_prefix}-action-quota" : ""
    }
  }

  tags = merge(var.tags, {
    "km:component" = "h1-bridge"
    "km:managed"   = "true"
  })

  # Belt-and-suspenders: replace the function when the IAM role is recreated. With
  # kms_key_arn set above, env decrypt is grant-independent so this is no longer the
  # primary safeguard (the CMK is) — kept as harmless defense-in-depth.
  lifecycle {
    replace_triggered_by = [aws_iam_role.h1_bridge]
  }
}

# CloudWatch Log Group
resource "aws_cloudwatch_log_group" "h1_bridge" {
  name              = "/aws/lambda/${local.function_name}"
  retention_in_days = 30

  tags = merge(var.tags, {
    "km:component" = "h1-bridge"
    "km:managed"   = "true"
  })
}

# ============================================================
# Lambda Function URL
#
# authorization_type = "NONE" because auth is application-layer:
#   X-H1-Signature HMAC-SHA256 verify + nonce replay protection.
#   No IAM auth needed at the HTTP layer.
# ============================================================

resource "aws_lambda_function_url" "h1_bridge" {
  function_name      = aws_lambda_function.h1_bridge.function_name
  authorization_type = "NONE"

  cors {
    allow_origins = ["*"]
    allow_methods = ["POST"]
    allow_headers = ["content-type", "x-h1-signature", "x-h1-event", "x-h1-delivery"]
  }
}

# Explicit resource-based policy for public Function URL invocation.
# Without this, Lambda replacement (including role-triggered replacements)
# causes the URL to return 403 until the policy is manually re-added.
resource "aws_lambda_permission" "function_url_public" {
  statement_id           = "FunctionURLAllowPublicAccess"
  action                 = "lambda:InvokeFunctionUrl"
  function_name          = aws_lambda_function.h1_bridge.function_name
  principal              = "*"
  function_url_auth_type = "NONE"
}
