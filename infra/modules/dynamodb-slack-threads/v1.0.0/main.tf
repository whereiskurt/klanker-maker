# DynamoDB Slack threads table — v1.0.0.
#
# Key design:
#   channel_id (S) = hash key  — identifies the Slack channel (per-sandbox channel).
#   thread_ts  (S) = sort key  — Slack thread timestamp; top-level posts use event.ts
#                                as the thread anchor.
#
# Each row maps a (channel_id, thread_ts) pair to a Claude session so the
# km-slack-inbound-poller can resume mid-conversation with `claude --resume`.
#
# Billing: PAY_PER_REQUEST — inbound Slack chat is bursty; on-demand avoids
#   provisioned capacity waste.
#
# TTL: ttl_expiry (N, Unix epoch seconds) — DynamoDB native TTL automatically
#   removes stale thread rows after 30 days of inactivity.
#   NOTE: ttl_expiry MUST be a Number (Unix epoch), NOT an ISO8601 string.
#   Writers (bridge Lambda, poller) must supply: now_unix + 30*24*3600.
resource "aws_dynamodb_table" "slack_threads" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "channel_id"
  range_key    = "thread_ts"

  attribute {
    name = "channel_id"
    type = "S"
  }

  attribute {
    name = "thread_ts"
    type = "S"
  }

  ttl {
    attribute_name = "ttl_expiry"
    enabled        = true
  }

  point_in_time_recovery {
    enabled = false
  }

  server_side_encryption {
    enabled = true
  }

  tags = merge(var.tags, {
    Name      = var.table_name
    Component = "km-slack-inbound"
  })
}
