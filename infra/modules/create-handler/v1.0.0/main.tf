data "aws_caller_identity" "current" {}

# ============================================================
# IAM role for the create-handler Lambda
# ============================================================

resource "aws_iam_role" "create_handler" {
  name = "${var.resource_prefix}-create-handler"

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
    "km:component" = "create-handler"
    "km:managed"   = "true"
  }
}

# Policy: CloudWatch Logs for Lambda execution logs
resource "aws_iam_role_policy" "cloudwatch_logs" {
  name = "${var.resource_prefix}-create-handler-cw-logs"
  role = aws_iam_role.create_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = ["logs:*"]
        Resource = [
          "arn:aws:logs:*:${data.aws_caller_identity.current.account_id}:log-group:/aws/lambda/${var.resource_prefix}-*",
          "arn:aws:logs:*:${data.aws_caller_identity.current.account_id}:log-group:/aws/lambda/${var.resource_prefix}-*:*",
          "arn:aws:logs:*:${data.aws_caller_identity.current.account_id}:log-group:/${var.resource_prefix}/sandboxes/*",
          "arn:aws:logs:*:${data.aws_caller_identity.current.account_id}:log-group:/${var.resource_prefix}/sandboxes/*:*",
        ]
      },
      {
        # DescribeLogGroups requires wildcard resource
        Effect   = "Allow"
        Action   = ["logs:DescribeLogGroups"]
        Resource = "*"
      }
    ]
  })
}

# Shared operator permissions module (14 policies — cloudwatch_logs stays inline above)
module "km_operator_policy" {
  source = "../../km-operator-policy/v1.0.0"

  role_id                   = aws_iam_role.create_handler.id
  resource_prefix           = var.resource_prefix
  artifact_bucket_arn       = var.artifact_bucket_arn
  state_bucket              = var.state_bucket
  dynamodb_table_name       = var.dynamodb_table_name
  dynamodb_budget_table_arn = var.dynamodb_budget_table_arn
  sandbox_table_name        = var.sandbox_table_name
  identities_table_name     = var.identities_table_name
  slack_threads_table_name  = var.slack_threads_table_name
}

# ============================================================
# Lambda function: create-handler (container image, arm64)
# ============================================================

resource "aws_lambda_function" "create_handler" {
  function_name = "${var.resource_prefix}-create-handler"
  description   = "Provisions a new sandbox by running km create as a subprocess when a SandboxCreate EventBridge event is received"
  role          = aws_iam_role.create_handler.arn

  # Zip Lambda from local file — handler downloads km/terraform/terragrunt from S3 at cold start
  filename         = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)
  handler          = "bootstrap"
  runtime          = "provided.al2023"
  architectures    = ["arm64"]

  # 15-minute timeout: cold start toolchain download + terraform init + apply
  timeout     = 900
  memory_size = 1536

  # 10GB ephemeral storage: toolchain binaries (~250MB) + terraform provider download (~500MB)
  ephemeral_storage {
    size = 10240
  }

  environment {
    variables = {
      KM_ARTIFACTS_BUCKET = var.artifact_bucket_name
      KM_EMAIL_DOMAIN     = var.email_domain
      KM_OPERATOR_EMAIL   = var.operator_email
      KM_STATE_BUCKET     = var.state_bucket
      KM_STATE_PREFIX     = var.state_prefix
      KM_REGION_LABEL     = var.region_label
      KM_RESOURCE_PREFIX  = var.resource_prefix
      KM_TOOLCHAIN_DIR    = "/tmp/toolchain"
      # Handler reads KM_SANDBOX_TABLE_NAME and KM_IDENTITIES_TABLE
      # (cmd/create-handler/main.go:103-115). The previous SANDBOX_TABLE_NAME
      # name didn't match what the binary looked up, so the handler fell back
      # to its hardcoded km-sandboxes default — broken on any non-default
      # resource_prefix install.
      KM_SANDBOX_TABLE_NAME = var.sandbox_table_name
      KM_IDENTITIES_TABLE   = var.identities_table_name
    }
  }

  tags = {
    "km:component" = "create-handler"
    "km:managed"   = "true"
  }

  # Replace Lambda if role is replaced — Lambda env-var KMS grants bind to role unique-ID
  lifecycle {
    replace_triggered_by = [aws_iam_role.create_handler]
  }
}

# CloudWatch Log Group for Lambda logs
resource "aws_cloudwatch_log_group" "create_handler" {
  name              = "/aws/lambda/${var.resource_prefix}-create-handler"
  retention_in_days = 30

  tags = {
    "km:component" = "create-handler"
    "km:managed"   = "true"
  }
}

# ============================================================
# EventBridge rule: route SandboxCreate events to create-handler Lambda
# ============================================================

resource "aws_cloudwatch_event_rule" "sandbox_create" {
  name        = "${var.resource_prefix}-sandbox-create"
  description = "Routes SandboxCreate events to the create-handler Lambda for sandbox provisioning"

  event_pattern = jsonencode({
    source      = ["km.sandbox"]
    detail-type = ["SandboxCreate"]
  })

  tags = {
    "km:component" = "create-handler"
    "km:managed"   = "true"
  }
}

resource "aws_cloudwatch_event_target" "create_to_lambda" {
  rule      = aws_cloudwatch_event_rule.sandbox_create.name
  target_id = "${var.resource_prefix}-create-handler"
  arn       = aws_lambda_function.create_handler.arn

  # CRITICAL: 0 retries — sandbox creation is NOT idempotent. A retry after partial
  # completion would attempt to re-provision an already-partially-created sandbox.
  retry_policy {
    maximum_retry_attempts       = 0
    maximum_event_age_in_seconds = 60
  }
}

# Lambda permission: allow EventBridge Events to invoke the create-handler Lambda
resource "aws_lambda_permission" "eventbridge_events" {
  statement_id  = "AllowEventBridgeEventsInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.create_handler.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.sandbox_create.arn
}

# ============================================================
# moved {} blocks — record extraction of 14 policies into km-operator-policy/v1.0.0
# cloudwatch_logs has NO moved block — it was NOT extracted (stays inline above)
# ============================================================

moved {
  from = aws_iam_role_policy.s3_artifacts
  to   = module.km_operator_policy.aws_iam_role_policy.s3_artifacts
}

moved {
  from = aws_iam_role_policy.dynamodb
  to   = module.km_operator_policy.aws_iam_role_policy.dynamodb
}

moved {
  from = aws_iam_role_policy.dynamodb_sandboxes
  to   = module.km_operator_policy.aws_iam_role_policy.dynamodb_sandboxes
}

moved {
  from = aws_iam_role_policy.terraform_state
  to   = module.km_operator_policy.aws_iam_role_policy.terraform_state
}

moved {
  from = aws_iam_role_policy.ec2_provisioning
  to   = module.km_operator_policy.aws_iam_role_policy.ec2_provisioning
}

moved {
  from = aws_iam_role_policy.iam_sandbox
  to   = module.km_operator_policy.aws_iam_role_policy.iam_sandbox
}

moved {
  from = aws_iam_role_policy.ecs_provisioning
  to   = module.km_operator_policy.aws_iam_role_policy.ecs_provisioning
}

moved {
  from = aws_iam_role_policy.scheduler
  to   = module.km_operator_policy.aws_iam_role_policy.scheduler
}

moved {
  from = aws_iam_role_policy.ssm
  to   = module.km_operator_policy.aws_iam_role_policy.ssm
}

moved {
  from = aws_iam_role_policy.ssm_send_command
  to   = module.km_operator_policy.aws_iam_role_policy.ssm_send_command
}

moved {
  from = aws_iam_role_policy.ses_send
  to   = module.km_operator_policy.aws_iam_role_policy.ses_send
}

moved {
  from = aws_iam_role_policy.lambda_budget
  to   = module.km_operator_policy.aws_iam_role_policy.lambda_budget
}

moved {
  from = aws_iam_role_policy.kms
  to   = module.km_operator_policy.aws_iam_role_policy.kms
}

moved {
  from = aws_iam_role_policy.sqs_slack_inbound
  to   = module.km_operator_policy.aws_iam_role_policy.sqs_slack_inbound
}
