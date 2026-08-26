data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

locals {
  function_name = "${var.resource_prefix}-webhook-bridge"
}

# ============================================================
# km-webhook-bridge — Phase 127 generic webhook ingress bridge Lambda
#
# Forked from infra/modules/lambda-h1-bridge/v1.0.0. The structural deltas vs the
# H1 module:
#   - DROPPED: the h1-threads DynamoDB grant + h1_threads_table_name/_arn vars (a
#     webhook source is one-way — there is no report/thread continuity table, no
#     customer-API Basic-Auth back-channel, and no bot handle to key off of; see
#     pkg/webhook/bridge/interfaces.go doc comment) and the H1-specific SSM path
#     variables (api_username_path/api_token_path/h1_api_base_url/commands_path) —
#     per-source auth secrets live under /{prefix}/config/webhooks/* instead of a
#     single fixed param.
#   - RENAMED: h1_programs_json -> webhook_sources_json (KM_WEBHOOK_SOURCES). This
#     single payload carries BOTH the source list and the rate_limit block — there
#     is deliberately no separate rate-limit variable/env var; splitting one out
#     was considered and rejected (see webhook_sources_json's variable doc
#     comment) because cmd/km-webhook-bridge's parseSourcesEnv reads rate_limit
#     from this same JSON, and a second, always-empty variable would advertise a
#     knob that silently does nothing.
#   - KEPT: the Function URL (auth_type = "NONE" — auth is in-Lambda, per source),
#     the nonces-table IAM (PutItem for replay dedup, UpdateItem for the storm
#     rate-counter — the H1 module only needed PutItem since H1 has no rate
#     limiter), the sandboxes-table IAM (Query on alias-index, GetItem, UpdateItem,
#     DeleteItem — Phase 109 self-heal, never PutItem), sqs:SendMessage,
#     ec2:DescribeInstances/StartInstances, events:PutEvents, ssm:GetParameter +
#     kms:Decrypt (now scoped to /{prefix}/config/webhooks/* — every source's
#     auth.secret_path lives under this prefix), and the quota-table grant.
#
# IMPORTANT (CLAUDE.md memory project_terragrunt_providers_in_root):
#   This module does NOT declare required_providers — root.hcl's generate "provider"
#   stanza is the single source of provider configuration across all modules.
#
# IAM <-> runtime cross-check (an init_test guards the PRESENCE of this block). Every
# AWS call the Lambda makes at runtime has a matching grant below:
#   - SSMSecretFetcher.Fetch (per-source auth.secret_path)  -> ssm:GetParameter(s)
#       on /{prefix}/config/webhooks/* + kms:Decrypt (SecureString)
#   - DynamoWebhookNonceStore.CheckAndStore     -> dynamodb:PutItem (nonces table)
#   - DynamoRateCounter.Increment                -> dynamodb:UpdateItem (nonces table)
#   - DynamoAliasResolver.ResolveByAlias*         -> dynamodb:Query (sandboxes alias-index)
#   - DynamoAliasResolver.QueueURL                -> dynamodb:GetItem (sandboxes base table)
#   - DynamoSandboxStatusWriter.SetStatusRunning  -> dynamodb:UpdateItem (sandboxes base table)
#   - DynamoSandboxStatusWriter.DeleteSandboxRow  -> dynamodb:DeleteItem (sandboxes base table)
#   - DDBActionLimitsFetcher.FetchLimits          -> dynamodb:GetItem (sandboxes base table)
#   - DynamoFreezer.FreezeSandbox                 -> dynamodb:UpdateItem (sandboxes base table)
#   - WebhookSQSAdapter.Send                      -> sqs:SendMessage ({prefix}-webhook-inbound-*.fifo)
#   - EventBridgeAdapter.ColdCreate                -> events:PutEvents (default bus)
#   - EC2Resumer.StartSandbox                      -> ec2:DescribeInstances + ec2:StartInstances
#   - WireActionQuota (Task 9A)                    -> dynamodb:UpdateItem/GetItem (quota table)
# ============================================================

# ============================================================
# IAM role for the webhook bridge Lambda
# ============================================================

resource "aws_iam_role" "webhook_bridge" {
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
    "km:component" = "webhook-bridge"
    "km:managed"   = "true"
  })
}

# Policy: CloudWatch Logs
resource "aws_iam_role_policy" "cloudwatch_logs" {
  name = "${local.function_name}-cw-logs"
  role = aws_iam_role.webhook_bridge.id

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

# Policy: KMS — decrypt SSM SecureString parameters (per-source auth secrets)
resource "aws_iam_role_policy" "kms_decrypt" {
  name = "${local.function_name}-kms"
  role = aws_iam_role.webhook_bridge.id

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

# Policy: SSM — read per-source auth secrets under /{prefix}/config/webhooks/*.
# Unlike the GitHub/H1 bridges (one fixed webhook-secret path), a webhook source's
# auth.secret_path is an operator-declared SSM path scoped under this prefix (see
# config.WebhookAuth.SecretPath), so the grant is prefix-wide rather than one name.
resource "aws_iam_role_policy" "ssm_webhook_secrets" {
  name = "${local.function_name}-ssm-webhooks"
  role = aws_iam_role.webhook_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SSMWebhookSecrets"
        Effect = "Allow"
        Action = [
          "ssm:GetParameter",
          "ssm:GetParameters",
        ]
        Resource = "arn:aws:ssm:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:parameter/${var.resource_prefix}/config/webhooks/*"
      }
    ]
  })
}

# Policy: DynamoDB — nonce conditional write for replay protection (PutItem) and
# the storm rate-counter's atomic ADD (UpdateItem), both on the shared nonces
# table (shared with Slack/GitHub/H1 bridges under a distinct key namespace).
resource "aws_iam_role_policy" "dynamodb_nonce" {
  name = "${local.function_name}-dynamodb-nonce"
  role = aws_iam_role.webhook_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "NonceReplayAndRateCounter"
        Effect = "Allow"
        Action = [
          "dynamodb:PutItem",
          "dynamodb:UpdateItem",
        ]
        Resource = var.nonces_table_arn
      }
    ]
  })
}

# Policy: DynamoDB — alias-index GSI query (warm-path alias->sandbox_id resolution)
# + GetItem on base table (webhook_inbound_queue_url attribute lookup + action-limits
# fetch) + UpdateItem on base table (status write-back after auto-resume + auto-freeze
# latch) + DeleteItem on base table (Phase 109: clear an orphaned status=stopped row
# whose EC2 instance is gone, so the alias resolves as absent for cold-create).
# CRITICAL: UpdateItem/DeleteItem only — full-row PutItem is intentionally excluded to
# avoid the SandboxMetadata lossy round-trip footgun (attributes not in the struct are stripped).
resource "aws_iam_role_policy" "dynamodb_sandboxes" {
  name = "${local.function_name}-dynamodb-sandboxes"
  role = aws_iam_role.webhook_bridge.id

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
        # UpdateItem flips status=running after auto-resume and latches
        # action_frozen on auto-quota-breach (DynamoFreezer); DeleteItem
        # (Phase 109) clears an orphaned status=stopped row whose instance is
        # gone. PutItem is still excluded — no full-row write access to
        # sandbox rows.
        Action = [
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem",
        ]
        Resource = var.sandboxes_table_arn
      }
    ]
  })
}

# Phase 121 (Task 9A): quota table read/write for webhook_dispatch metering +
# auto-freeze. Gated on var.quota_table_arn — empty = table not yet provisioned
# → policy omitted (WireActionQuota stays dormant at runtime).
resource "aws_iam_role_policy" "dynamodb_action_quota" {
  count = var.quota_table_arn != "" ? 1 : 0
  name  = "${local.function_name}-dynamodb-action-quota"
  role  = aws_iam_role.webhook_bridge.id

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

# Policy: SQS — send inbound messages to per-sandbox webhook-inbound FIFO queues
# (warm path). Per-sandbox queues follow the naming convention
# {resource_prefix}-webhook-inbound-{sandbox_id}.fifo (pkg/aws.WebhookInboundQueueName).
resource "aws_iam_role_policy" "sqs_send_webhook_inbound" {
  name = "${local.function_name}-sqs-webhook-inbound"
  role = aws_iam_role.webhook_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SQSSendWebhookInbound"
        Effect = "Allow"
        Action = [
          "sqs:SendMessage",
          "sqs:GetQueueAttributes",
          "sqs:GetQueueUrl",
        ]
        Resource = "arn:aws:sqs:*:${data.aws_caller_identity.current.account_id}:${var.resource_prefix}-webhook-inbound-*.fifo"
      }
    ]
  })
}

# Policy: EC2 — describe and start stopped sandbox instances (auto-resume path)
# ec2:DescribeInstances scoped to "*" (Describe actions do not support resource-level
# conditions). ec2:StartInstances scoped to THIS install via the km:resource-prefix tag.
resource "aws_iam_role_policy" "ec2_resume" {
  name = "${local.function_name}-ec2-resume"
  role = aws_iam_role.webhook_bridge.id

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
  role = aws_iam_role.webhook_bridge.id

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

resource "aws_lambda_function" "webhook_bridge" {
  function_name    = local.function_name
  description      = "Phase 127 generic webhook ingress bridge: authenticates a per-source scheme (bearer/HMAC), drops replays, matches operator-declared rules, and dispatches a prompt to an aliased sandbox (warm FIFO or cold-create)"
  role             = aws_iam_role.webhook_bridge.arn
  filename         = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  # 60s mirrors lambda-github-bridge/lambda-h1-bridge (synchronous ACK + auth verify +
  # SSM fetch must complete inside the source's redelivery window; generous for cold-start).
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
      KM_RESOURCE_PREFIX    = var.resource_prefix
      KM_WEBHOOK_SOURCES    = var.webhook_sources_json
      KM_NONCE_TABLE        = var.nonces_table_name
      KM_SANDBOX_TABLE_NAME = var.sandboxes_table_name
      KM_ARTIFACTS_BUCKET   = var.artifacts_bucket
      KM_ARTIFACTS_PREFIX   = var.artifacts_prefix
      # Phase 121 — action-quota table name for bridge-side quota enforcement
      KM_QUOTA_TABLE = var.quota_table_arn != "" ? "${var.resource_prefix}-action-quota" : ""
    }
  }

  tags = merge(var.tags, {
    "km:component" = "webhook-bridge"
    "km:managed"   = "true"
  })

  # Belt-and-suspenders: replace the function when the IAM role is recreated. With
  # kms_key_arn set above, env decrypt is grant-independent so this is no longer the
  # primary safeguard (the CMK is) — kept as harmless defense-in-depth.
  lifecycle {
    replace_triggered_by = [aws_iam_role.webhook_bridge]
  }
}

# CloudWatch Log Group
resource "aws_cloudwatch_log_group" "webhook_bridge" {
  name              = "/aws/lambda/${local.function_name}"
  retention_in_days = 30

  tags = merge(var.tags, {
    "km:component" = "webhook-bridge"
    "km:managed"   = "true"
  })
}

# ============================================================
# Lambda Function URL
#
# authorization_type = "NONE" because auth is application-layer, per source:
#   bearer token or HMAC-SHA256, both verified in-Lambda, plus nonce replay
#   protection. No IAM auth needed at the HTTP layer.
# ============================================================

resource "aws_lambda_function_url" "webhook_bridge" {
  function_name      = aws_lambda_function.webhook_bridge.function_name
  authorization_type = "NONE"

  cors {
    allow_origins = ["*"]
    allow_methods = ["POST"]
    allow_headers = ["content-type", "authorization", "content-encoding"]
  }
}

# Explicit resource-based policy for public Function URL invocation.
# Without this, Lambda replacement (including role-triggered replacements)
# causes the URL to return 403 until the policy is manually re-added.
resource "aws_lambda_permission" "function_url_public" {
  statement_id           = "FunctionURLAllowPublicAccess"
  action                 = "lambda:InvokeFunctionUrl"
  function_name          = aws_lambda_function.webhook_bridge.function_name
  principal              = "*"
  function_url_auth_type = "NONE"
}
