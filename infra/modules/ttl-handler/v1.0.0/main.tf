data "aws_caller_identity" "current" {}

# ============================================================
# IAM role for the TTL handler Lambda
# ============================================================

resource "aws_iam_role" "ttl_handler" {
  name = "${var.resource_prefix}-ttl-handler"

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

# Policy: CloudWatch Logs for Lambda execution logs and sandbox log export
resource "aws_iam_role_policy" "cloudwatch_logs" {
  name = "${var.resource_prefix}-ttl-handler-cw-logs"
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
          "logs:DeleteLogGroup",
          "logs:CreateExportTask",
          "logs:DescribeExportTasks",
        ]
        Resource = "arn:aws:logs:*:*:*"
      }
    ]
  })
}

# Policy: S3 access for profile download and artifact upload
resource "aws_iam_role_policy" "s3_artifacts" {
  name = "${var.resource_prefix}-ttl-handler-s3"
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
  name = "${var.resource_prefix}-ttl-handler-ses"
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
  name = "${var.resource_prefix}-ttl-handler-scheduler"
  role = aws_iam_role.ttl_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["scheduler:DeleteSchedule"]
        Resource = "arn:aws:scheduler:*:*:schedule/default/${var.resource_prefix}-ttl-*"
      },
      {
        Effect = "Allow"
        Action = [
          "scheduler:CreateSchedule",
          "scheduler:DeleteSchedule",
          "scheduler:GetSchedule",
        ]
        Resource = "arn:aws:scheduler:*:*:schedule/${var.resource_prefix}-at/*"
      },
      {
        Effect   = "Allow"
        Action   = ["iam:PassRole"]
        Resource = var.scheduler_role_arn != "" ? var.scheduler_role_arn : "*"
      }
    ]
  })
}

# Policy: TTL extend / resume-reschedule — handleExtend and the resume path both
# rediscover the handler's own Lambda ARN (lambda:GetFunction on self) and the
# scheduler role (iam:GetRole on {prefix}-ttl-scheduler), then recreate the TTL
# schedule in the default scheduler group ({prefix}-ttl-*). Without these grants
# `km extend --remote` and remote resume fail at GetFunction with AccessDenied and
# the TTL schedule is never rewritten. (The local `km extend` path is unaffected —
# it writes DynamoDB directly from the operator.) iam:PassRole on the scheduler
# role is already granted in the scheduler_delete policy below.
resource "aws_iam_role_policy" "extend_reschedule" {
  name = "${var.resource_prefix}-ttl-handler-extend-reschedule"
  role = aws_iam_role.ttl_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "SelfGetFunction"
        Effect   = "Allow"
        Action   = ["lambda:GetFunction"]
        Resource = "arn:aws:lambda:*:${data.aws_caller_identity.current.account_id}:function:${var.resource_prefix}-ttl-handler"
      },
      {
        Sid      = "SchedulerRoleGetRole"
        Effect   = "Allow"
        Action   = ["iam:GetRole"]
        Resource = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.resource_prefix}-ttl-scheduler"
      },
      {
        Sid    = "TTLScheduleReschedule"
        Effect = "Allow"
        Action = [
          "scheduler:CreateSchedule",
          "scheduler:GetSchedule",
        ]
        Resource = "arn:aws:scheduler:*:*:schedule/default/${var.resource_prefix}-ttl-*"
      },
    ]
  })
}

# Policy: Terraform destroy permissions — allows the Lambda to run terraform destroy
# on sandbox state. Scoped to km-* resources where possible.
resource "aws_iam_role_policy" "terraform_destroy" {
  name = "${var.resource_prefix}-ttl-handler-terraform-destroy"
  role = aws_iam_role.ttl_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "TerraformStateAccess"
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject",
          "s3:ListBucket",
        ]
        Resource = [
          "arn:aws:s3:::${var.state_bucket}",
          "arn:aws:s3:::${var.state_bucket}/*",
        ]
      },
      {
        Sid    = "TerraformLockTable"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:DeleteItem",
        ]
        Resource = "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.state_prefix}-locks-*"
      },
      {
        Sid    = "EC2SandboxDestroy"
        Effect = "Allow"
        Action = [
          "ec2:TerminateInstances",
          "ec2:StopInstances",
          "ec2:StartInstances",
          "ec2:DescribeInstances",
          "ec2:CancelSpotInstanceRequests",
          "ec2:DescribeSpotInstanceRequests",
          "ec2:DeleteSecurityGroup",
          "ec2:RevokeSecurityGroupEgress",
          "ec2:RevokeSecurityGroupIngress",
          "ec2:DescribeSecurityGroups",
          "ec2:DescribeSecurityGroupRules",
          "ec2:DeleteTags",
          "ec2:DescribeTags",
          "ec2:DescribeVpcs",
          "ec2:DescribeSubnets",
          "ec2:DescribeAvailabilityZones",
          "ec2:DescribeImages",
          "ec2:DescribeSpotPriceHistory",
          "ec2:DescribeNetworkInterfaces",
          "ec2:DescribeVolumes",
          "ec2:DeleteVolume",
          "ec2:DetachVolume",
          "ec2:DescribeInstanceTypes",
          "ec2:DescribeInstanceAttribute",
          "ec2:ModifyInstanceAttribute",
          "ec2:DescribeInstanceCreditSpecifications",
        ]
        Resource = "*"
      },
      {
        Sid    = "SSMAgentRun"
        Effect = "Allow"
        Action = [
          "ssm:SendCommand",
          "ssm:GetCommandInvocation",
        ]
        Resource = "*"
      },
      {
        Sid    = "IAMSandboxDestroy"
        Effect = "Allow"
        Action = [
          "iam:DeleteRole",
          "iam:DeleteRolePolicy",
          "iam:DetachRolePolicy",
          "iam:GetRole",
          "iam:GetRolePolicy",
          "iam:ListRolePolicies",
          "iam:ListAttachedRolePolicies",
          "iam:ListInstanceProfilesForRole",
          "iam:RemoveRoleFromInstanceProfile",
          "iam:DeleteInstanceProfile",
          "iam:GetInstanceProfile",
          "iam:PassRole",
          "iam:CreateRole",
          "iam:PutRolePolicy",
          "iam:AttachRolePolicy",
          "iam:TagRole",
        ]
        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.resource_prefix}-ec2spot-*",
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.resource_prefix}-budget-enforcer-*",
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.resource_prefix}-budget-scheduler-*",
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:instance-profile/${var.resource_prefix}-ec2spot-*",
        ]
      },
      {
        Sid    = "LambdaCleanup"
        Effect = "Allow"
        Action = [
          "lambda:DeleteFunction",
          "lambda:GetFunction",
          "lambda:RemovePermission",
          "lambda:GetPolicy",
        ]
        Resource = "arn:aws:lambda:*:${data.aws_caller_identity.current.account_id}:function:${var.resource_prefix}-budget-enforcer-*"
      },
      {
        Sid    = "SchedulerCleanup"
        Effect = "Allow"
        Action = [
          "scheduler:DeleteSchedule",
          "scheduler:GetSchedule",
        ]
        Resource = "arn:aws:scheduler:*:*:schedule/default/${var.resource_prefix}-budget-*"
      },
      {
        Sid    = "KMSCleanup"
        Effect = "Allow"
        Action = [
          "kms:DescribeKey",
          "kms:ScheduleKeyDeletion",
          "kms:DeleteAlias",
          "kms:ListAliases",
        ]
        Resource = "*"
      },
    ]
  })
}

# ============================================================
# Lambda function: TTL handler (Go, provided.al2023, arm64)
# ============================================================

resource "aws_lambda_function" "ttl_handler" {
  function_name = "${var.resource_prefix}-ttl-handler"
  description   = "Uploads artifacts and sends ttl-expired notification when EventBridge TTL fires"
  role          = aws_iam_role.ttl_handler.arn

  # Go Lambda: custom runtime on Amazon Linux 2023, arm64 for Graviton cost efficiency
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  filename         = var.lambda_zip_path
  source_code_hash = filebase64sha256(var.lambda_zip_path)

  # 15-minute timeout: terraform init + destroy can take several minutes
  timeout       = 900
  memory_size   = 1536
  architectures = ["arm64"]

  # 2GB ephemeral storage: terraform init downloads the AWS provider (~500MB)
  ephemeral_storage {
    size = 2048
  }

  environment {
    variables = {
      KM_ARTIFACTS_BUCKET = var.artifact_bucket_name
      KM_EMAIL_DOMAIN     = var.email_domain
      KM_OPERATOR_EMAIL   = var.operator_email
      KM_STATE_BUCKET     = var.state_bucket
      KM_STATE_PREFIX     = var.state_prefix
      KM_REGION_LABEL     = var.region_label
      # SANDBOX_TABLE_NAME (no KM_ prefix) was a dead var — binary reads
      # KM_SANDBOX_TABLE_NAME, set immediately below. Removed to avoid
      # the same drift bug we fixed in create-handler / email-handler.
      KM_CREATE_HANDLER_ARN = var.create_handler_arn
      KM_SCHEDULER_ROLE_ARN = var.scheduler_role_arn
      KM_TTL_HANDLER_NAME   = "${var.resource_prefix}-ttl-handler"
      KM_TTL_SCHEDULER_ROLE = "${var.resource_prefix}-ttl-scheduler"
      KM_AT_GROUP_NAME      = "${var.resource_prefix}-at"
      KM_SANDBOX_TABLE_NAME = var.sandbox_table_name
      KM_BUDGET_TABLE       = var.budget_table_name
      KM_SCHEDULES_TABLE    = var.schedules_table_name
      # Binary uses KM_RESOURCE_PREFIX for prefix-aware paths; without it
      # falls back to literal "km" — silently wrong-prefix on non-default
      # installs.
      KM_RESOURCE_PREFIX = var.resource_prefix
      # km-identities table — TTL handler deletes the sandbox row during teardown
      # so a reused alias does not inherit a stale pubkey via the alias-index GSI.
      KM_IDENTITIES_TABLE = var.identities_table_name
    }
  }

  tags = {
    "km:component" = "ttl-handler"
    "km:managed"   = "true"
  }

  # Replace Lambda if role is replaced — Lambda env-var KMS grants bind to role unique-ID
  lifecycle {
    replace_triggered_by = [aws_iam_role.ttl_handler]
  }
}

# CloudWatch Log Group for Lambda logs
resource "aws_cloudwatch_log_group" "ttl_handler" {
  name              = "/aws/lambda/${var.resource_prefix}-ttl-handler"
  retention_in_days = 30

  tags = {
    "km:component" = "ttl-handler"
    "km:managed"   = "true"
  }
}

# ============================================================
# EventBridge Scheduler execution role: assumed by Scheduler to invoke Lambda
# ============================================================

resource "aws_iam_role" "scheduler_invoke" {
  name = "${var.resource_prefix}-ttl-scheduler"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Service = "scheduler.amazonaws.com"
        }
        Action = "sts:AssumeRole"
      },
    ]
  })

  tags = {
    "km:component" = "ttl-handler"
    "km:managed"   = "true"
  }
}

resource "aws_iam_role_policy" "scheduler_invoke_lambda" {
  name = "${var.resource_prefix}-ttl-scheduler-invoke"
  role = aws_iam_role.scheduler_invoke.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = "lambda:InvokeFunction"
        Resource = compact([
          aws_lambda_function.ttl_handler.arn,
          var.create_handler_arn,
        ])
      },
    ]
  })
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

# ============================================================
# EventBridge rule: route SandboxIdle events to TTL Lambda (PROV-06)
# ============================================================

resource "aws_cloudwatch_event_rule" "sandbox_idle" {
  name        = "${var.resource_prefix}-sandbox-idle"
  description = "Routes SandboxIdle events from audit-log sidecar to TTL Lambda for sandbox teardown"

  event_pattern = jsonencode({
    source      = ["km.sandbox"]
    detail-type = ["SandboxIdle"]
    detail = {
      event_type = ["idle"]
    }
  })

  tags = {
    "km:component" = "ttl-handler"
    "km:managed"   = "true"
  }
}

resource "aws_cloudwatch_event_target" "idle_to_ttl" {
  rule      = aws_cloudwatch_event_rule.sandbox_idle.name
  target_id = "${var.resource_prefix}-ttl-handler-idle"
  arn       = aws_lambda_function.ttl_handler.arn

  # Transform EventBridge envelope to match TTLEvent struct shape
  input_transformer {
    input_paths = {
      sandbox_id = "$.detail.sandbox_id"
    }
    input_template = <<-TEMPLATE
    {"sandbox_id": <sandbox_id>, "event_type": "idle"}
    TEMPLATE
  }
}

# Lambda permission: allow EventBridge Events to invoke the TTL Lambda (for idle events)
# Note: This is separate from eventbridge_scheduler (which allows the Scheduler service).
resource "aws_lambda_permission" "eventbridge_events" {
  statement_id  = "AllowEventBridgeEventsInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.ttl_handler.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.sandbox_idle.arn
}

# ============================================================
# EventBridge rule: route SandboxCommand events to TTL Lambda (full detail passthrough)
# Used by email Lambda relay for schedule-create, destroy, extend, etc.
# ============================================================

resource "aws_cloudwatch_event_rule" "sandbox_command" {
  name        = "${var.resource_prefix}-sandbox-command"
  description = "Routes SandboxCommand events to TTL Lambda — full detail passthrough for schedule-create relay"

  event_pattern = jsonencode({
    source      = ["km.sandbox"]
    detail-type = ["SandboxIdle"]
    detail = {
      event_type = [{ "anything-but" : "idle" }]
    }
  })

  tags = {
    "km:component" = "ttl-handler"
    "km:managed"   = "true"
  }
}

resource "aws_cloudwatch_event_target" "command_to_ttl" {
  rule      = aws_cloudwatch_event_rule.sandbox_command.name
  target_id = "${var.resource_prefix}-ttl-handler-command"
  arn       = aws_lambda_function.ttl_handler.arn

  # Pass full detail through — no input transformer.
  # The TTL handler unmarshals the full TTLEvent struct from $.detail.
  input_path = "$.detail"
}

resource "aws_lambda_permission" "eventbridge_command" {
  statement_id  = "AllowEventBridgeCommandInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.ttl_handler.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.sandbox_command.arn
}

# ============================================================
# EventBridge rule: route CheckDispatch events to TTL Lambda (Phase 116 Stage B)
# Emitted by the check Lambda bootstrap (Stage A) when when_py predicate is truthy.
# Source: "km.sandbox", DetailType: "CheckDispatch".
# The target input_path "$.detail" passes the inner dict as the TTLEvent payload.
# ============================================================

resource "aws_cloudwatch_event_rule" "check_dispatch" {
  name        = "${var.resource_prefix}-check-dispatch"
  description = "Routes CheckDispatch events from check Lambda bootstraps to TTL Lambda for alias-targeted resume-or-cold-create (Phase 116 Stage B)"

  event_pattern = jsonencode({
    source      = ["km.sandbox"]
    detail-type = ["CheckDispatch"]
  })

  tags = {
    "km:component" = "ttl-handler"
    "km:managed"   = "true"
  }
}

resource "aws_cloudwatch_event_target" "check_dispatch_to_ttl" {
  rule      = aws_cloudwatch_event_rule.check_dispatch.name
  target_id = "${var.resource_prefix}-ttl-handler-check-dispatch"
  arn       = aws_lambda_function.ttl_handler.arn

  # Pass full detail through so TTLEvent fields (check_name, alias, prompt, etc.)
  # are unmarshalled directly. Mirrors sandbox_command's input_path approach.
  input_path = "$.detail"
}

resource "aws_lambda_permission" "eventbridge_check_dispatch" {
  statement_id  = "AllowEventBridgeCheckDispatchInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.ttl_handler.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.check_dispatch.arn
}

# ============================================================
# IAM policies: sandbox resource teardown (PROV-05/PROV-06)
# ============================================================

# Policy: Tag API for discovering sandbox resources by km:sandbox-id tag
resource "aws_iam_role_policy" "tag_discovery" {
  name = "${var.resource_prefix}-ttl-handler-tag-discovery"
  role = aws_iam_role.ttl_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["tag:GetResources"]
      Resource = "*"
    }]
  })
}

# Policy: EC2 terminate for destroying sandbox instances after TTL/idle
resource "aws_iam_role_policy" "ec2_teardown" {
  name = "${var.resource_prefix}-ttl-handler-ec2-teardown"
  role = aws_iam_role.ttl_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "ec2:TerminateInstances",
        "ec2:StopInstances",
        "ec2:StartInstances",
        "ec2:DescribeInstances",
      ]
      Resource = "*"
    }]
  })
}

# Policy: DynamoDB km-sandboxes — read/write sandbox metadata
resource "aws_iam_role_policy" "dynamodb_sandboxes" {
  name = "${var.resource_prefix}-ttl-handler-dynamodb-sandboxes"
  role = aws_iam_role.ttl_handler.id

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
          "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.sandbox_table_name}",
          "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.sandbox_table_name}/index/alias-index",
        ]
      }
    ]
  })
}

# Policy: cleanup of the per-sandbox identity on teardown — DDB row + three SSM params
# (signing-key, encryption-key, safe-phrase). Mirrors what internal/app/cmd/destroy.go
# does on the local-destroy path so the remote-destroy path no longer leaks identity rows
# (a stale row trips the bridge's alias-index lookup with 401 bad_signature when the alias is reused).
resource "aws_iam_role_policy" "identity_cleanup" {
  name = "${var.resource_prefix}-ttl-handler-identity-cleanup"
  role = aws_iam_role.ttl_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "IdentityTableDelete"
        Effect   = "Allow"
        Action   = ["dynamodb:DeleteItem"]
        Resource = "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.identities_table_name}"
      },
      {
        Sid    = "IdentitySSMDelete"
        Effect = "Allow"
        Action = ["ssm:DeleteParameter"]
        Resource = [
          "arn:aws:ssm:*:${data.aws_caller_identity.current.account_id}:parameter/${var.resource_prefix}/sandbox/*/signing-key",
          "arn:aws:ssm:*:${data.aws_caller_identity.current.account_id}:parameter/${var.resource_prefix}/sandbox/*/encryption-key",
          "arn:aws:ssm:*:${data.aws_caller_identity.current.account_id}:parameter/${var.resource_prefix}/sandbox/*/safe-phrase",
        ]
      }
    ]
  })
}

# Policy: Phase 116 Stage B — widened permissions for check dispatch
#   events:PutEvents — cold-create path emits SandboxCreate to the km.sandbox EventBridge bus
#   lambda:InvokeFunction on {prefix}-check-* — handleCheckRun one-shot invocation
#   dynamodb:PutItem on nonces table — cooldown nonce store (CheckAndStore)
#   dynamodb:Query on sandboxes/alias-index — alias resolution (ttlAliasResolver)
#
# EC2 Start/Describe and SSM SendCommand are already present in terraform_destroy policy
# (EC2SandboxDestroy and SSMAgentRun stanzas) — no delta required for handleAgentRun.
resource "aws_iam_role_policy" "check_dispatch" {
  name = "${var.resource_prefix}-ttl-handler-check-dispatch"
  role = aws_iam_role.ttl_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "CheckDispatchEventBridge"
        Effect = "Allow"
        Action = ["events:PutEvents"]
        # The km.sandbox default event bus is the account default bus (no custom bus ARN).
        # Scoped to default event bus in the current account/region.
        Resource = "arn:aws:events:*:${data.aws_caller_identity.current.account_id}:event-bus/default"
      },
      {
        Sid    = "CheckRunInvoke"
        Effect = "Allow"
        Action = ["lambda:InvokeFunction"]
        # Invoke any check Lambda in this account ({prefix}-check-*).
        Resource = "arn:aws:lambda:*:${data.aws_caller_identity.current.account_id}:function:${var.resource_prefix}-check-*"
      },
      {
        Sid    = "CheckNonceStorePut"
        Effect = "Allow"
        Action = ["dynamodb:PutItem"]
        # Nonces table — shared with Slack/GitHub bridges ({prefix}-slack-bridge-nonces).
        # The DDB table name follows the slack-bridge convention; no separate table needed.
        Resource = "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.resource_prefix}-slack-bridge-nonces"
      },
    ]
  })
}

# ============================================================
# S3 bucket policy: allow CloudWatch Logs service to write exported logs
# NOTE: The S3 bucket policy for CloudWatch log export (logs.amazonaws.com → logs/*)
# is consolidated in infra/modules/ses/v1.0.0/main.tf to avoid conflicts.
# Only one aws_s3_bucket_policy can exist per bucket — the SES module owns it.
