# DynamoDB Slack threads table — v1.1.0.
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
#
# v1.1.0 adds:
#   session-index GSI — hash key claude_session_id (S), KEYS_ONLY projection.
#   Enables O(1) session-id → (channel_id, thread_ts) resolution for the bridge
#   lookup-thread action (Plan 02) and km slack reply --session (Plan 04).
#   Bridge Upsert intentionally OMITS claude_session_id; only the sandbox-side
#   poller writes it — prevents empty-string GSI key collisions.
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

  attribute {
    name = "claude_session_id"
    type = "S"
  }

  global_secondary_index {
    name            = "session-index"
    hash_key        = "claude_session_id"
    projection_type = "KEYS_ONLY"
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
