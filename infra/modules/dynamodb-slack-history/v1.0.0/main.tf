# {prefix}-slack-history (2026-10-24): the per-route Slack channel mirror.
# PK channel (C…) / SK ts holds messages; PK "#meta" / SK <channel id> holds a
# channel's route + backfill state. TTL on expires_at (N, epoch seconds).
# PITR and backups stay OFF on purpose: the rows are a copy of Slack content
# with a stated retention, and a recoverable backup would make that retention
# false. Readable by the bridge role only; the sandbox role has no grant
# (pkg/terragrunt TestSandboxRole_HasNoGrantOnHistoryTable).
resource "aws_dynamodb_table" "slack_history" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "channel"
  range_key    = "ts"

  attribute {
    name = "channel"
    type = "S"
  }
  attribute {
    name = "ts"
    type = "S"
  }

  ttl {
    attribute_name = "expires_at"
    enabled        = true
  }

  point_in_time_recovery {
    enabled = false
  }

  server_side_encryption {
    enabled     = true
    kms_key_arn = var.kms_key_arn != "" ? var.kms_key_arn : null
  }

  tags = merge(var.tags, {
    Name      = var.table_name
    Component = "km-slack-history"
  })
}
