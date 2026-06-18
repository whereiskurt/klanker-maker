data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

# ============================================================
# Shared Lambda execution role for all {prefix}-check-* Lambdas.
#
# Grants the minimum permissions needed by the check Lambda bootstrap:
#   - CloudWatch Logs: write Lambda logs.
#   - S3 read:  s3://{artifacts}/checks/*     (read check source/config objects).
#   - S3 write: s3://{artifacts}/check-runs/* (write per-run output.json).
#   - EventBridge: emit CheckDispatch events to the km.sandbox bus.
#   - SSM: read per-check secrets under {prefix}/checks/*.
#   - DynamoDB: read the {prefix}-checks table (GetItem + Query).
#
# NOT included: EC2/SQS/resume permissions — those belong to ttl-handler (Stage B).
# ============================================================

resource "aws_iam_role" "check_runner" {
  name = var.role_name

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
    Name      = var.role_name
    Component = "km-check"
  })
}

# Policy: CloudWatch Logs
resource "aws_iam_role_policy" "cloudwatch_logs" {
  name = "${var.role_name}-cw-logs"
  role = aws_iam_role.check_runner.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "CloudWatchLogs"
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

# Policy: S3 read access for checks/* (source objects, bootstrap assets)
resource "aws_iam_role_policy" "s3_checks_read" {
  name = "${var.role_name}-s3-checks-read"
  role = aws_iam_role.check_runner.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "S3ChecksRead"
        Effect   = "Allow"
        Action   = ["s3:GetObject"]
        Resource = "arn:aws:s3:::${var.artifacts_bucket}/checks/*"
      }
    ]
  })
}

# Policy: S3 write access for check-runs/* (per-run output.json capture)
resource "aws_iam_role_policy" "s3_check_runs_write" {
  name = "${var.role_name}-s3-check-runs-write"
  role = aws_iam_role.check_runner.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "S3CheckRunsWrite"
        Effect   = "Allow"
        Action   = ["s3:PutObject"]
        Resource = "arn:aws:s3:::${var.artifacts_bucket}/check-runs/*"
      }
    ]
  })
}

# Policy: EventBridge — emit CheckDispatch events to the km.sandbox bus
resource "aws_iam_role_policy" "eventbridge_put_events" {
  name = "${var.role_name}-eventbridge"
  role = aws_iam_role.check_runner.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "EventBridgePutEvents"
        Effect   = "Allow"
        Action   = ["events:PutEvents"]
        Resource = "arn:aws:events:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:event-bus/default"
      }
    ]
  })
}

# Policy: SSM — read per-check secrets under {prefix}/checks/*
resource "aws_iam_role_policy" "ssm_checks" {
  name = "${var.role_name}-ssm-checks"
  role = aws_iam_role.check_runner.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "SSMChecksRead"
        Effect   = "Allow"
        Action   = ["ssm:GetParameter"]
        Resource = "arn:aws:ssm:*:*:parameter/${var.resource_prefix}/checks/*"
      }
    ]
  })
}

# Policy: DynamoDB — read the {prefix}-checks table
resource "aws_iam_role_policy" "dynamodb_checks" {
  name = "${var.role_name}-dynamodb-checks"
  role = aws_iam_role.check_runner.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "DynamoDBChecksRead"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:Query",
        ]
        Resource = "arn:aws:dynamodb:*:*:table/${var.table_name}"
      }
    ]
  })
}
