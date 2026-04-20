# ============================================================
# Lambda package: archive handler.py into a deployable zip
# ============================================================

data "archive_file" "spot_handler" {
  type        = "zip"
  source_file = "${path.module}/lambda/handler.py"
  output_path = "${path.module}/lambda/spot_handler.zip"
}

# ============================================================
# IAM role for the spot handler Lambda
# ============================================================

resource "aws_iam_role" "spot_handler" {
  name = "km-ecs-spot-handler"

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
    "km:component" = "ecs-spot-handler"
    "km:managed"   = "true"
  }
}

# Policy: CloudWatch Logs for Lambda execution logs
resource "aws_iam_role_policy" "cloudwatch_logs" {
  name = "km-spot-handler-cw-logs"
  role = aws_iam_role.spot_handler.id

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

# Policy: ECS Exec to trigger artifact upload inside the stopping container
resource "aws_iam_role_policy" "ecs_exec" {
  name = "km-spot-handler-ecs-exec"
  role = aws_iam_role.spot_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "ecs:ExecuteCommand",
          "ecs:DescribeTasks",
        ]
        Resource = [
          "${var.ecs_cluster_arn}",
          "${var.ecs_cluster_arn}/*",
        ]
      },
      # SSM Messages: required for ECS Exec session channel
      {
        Effect = "Allow"
        Action = [
          "ssmmessages:CreateControlChannel",
          "ssmmessages:CreateDataChannel",
          "ssmmessages:OpenControlChannel",
          "ssmmessages:OpenDataChannel",
        ]
        Resource = "*"
      }
    ]
  })
}

# ============================================================
# Lambda function: ECS Fargate spot interruption handler
# ============================================================

resource "aws_lambda_function" "spot_handler" {
  function_name    = "km-ecs-spot-handler"
  description      = "Triggers artifact upload via ECS Exec when a Fargate Spot task is interrupted"
  role             = aws_iam_role.spot_handler.arn
  runtime          = "python3.12"
  handler          = "handler.handler"
  filename         = data.archive_file.spot_handler.output_path
  source_code_hash = data.archive_file.spot_handler.output_base64sha256
  timeout          = 25 # Fargate gives ~30s; leave 5s margin

  environment {
    variables = {
      ARTIFACT_BUCKET = var.artifact_bucket_name
    }
  }

  tags = {
    "km:component" = "ecs-spot-handler"
    "km:managed"   = "true"
  }

  # Replace Lambda if role is replaced — Lambda env-var KMS grants bind to role unique-ID
  lifecycle {
    replace_triggered_by = [aws_iam_role.spot_handler]
  }
}

# CloudWatch Log Group for Lambda logs
resource "aws_cloudwatch_log_group" "spot_handler" {
  name              = "/aws/lambda/km-ecs-spot-handler"
  retention_in_days = 30

  tags = {
    "km:component" = "ecs-spot-handler"
    "km:managed"   = "true"
  }
}

# ============================================================
# EventBridge rule: watch for ECS Fargate Spot task interruption
# ============================================================

resource "aws_cloudwatch_event_rule" "ecs_spot_interruption" {
  name        = "km-ecs-spot-interruption"
  description = "Captures ECS Fargate Spot task stopping events for artifact upload"

  event_pattern = jsonencode({
    source      = ["aws.ecs"]
    detail-type = ["ECS Task State Change"]
    detail = {
      clusterArn = [var.ecs_cluster_arn]
      lastStatus = ["STOPPING"]
      stopCode   = ["SpotInterruption"]
    }
  })

  tags = {
    "km:component" = "ecs-spot-handler"
    "km:managed"   = "true"
  }
}

# EventBridge target: invoke the spot handler Lambda
resource "aws_cloudwatch_event_target" "lambda" {
  rule      = aws_cloudwatch_event_rule.ecs_spot_interruption.name
  target_id = "km-ecs-spot-handler"
  arn       = aws_lambda_function.spot_handler.arn
}

# Lambda permission: allow EventBridge to invoke the Lambda
resource "aws_lambda_permission" "eventbridge" {
  statement_id  = "AllowEventBridgeInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.spot_handler.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.ecs_spot_interruption.arn
}
