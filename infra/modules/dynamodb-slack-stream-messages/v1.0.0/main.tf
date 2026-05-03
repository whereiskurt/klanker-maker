# DynamoDB Slack stream messages table — v1.0.0.
#
# Purpose: Phase 68 transcript streaming maps each Slack message the bridge
# delivers (and the per-turn replies the sandbox posts back) to a transcript
# byte offset so a future Phase B can resolve a Slack reaction back into the
# corresponding section of the gzipped JSONL transcript.
#
# Key design:
#   channel_id (S) = hash key  — identifies the Slack channel (per-sandbox channel).
#   slack_ts   (S) = sort key  — Slack message timestamp (event.ts for inbound,
#                                chat.postMessage response.ts for outbound).
#
# Attributes (not declared as keys, written by km-slack record-mapping):
#   sandbox_id        (S) — owning sandbox (denormalised for cross-channel queries).
#   session_id        (S) — Claude session id active for the message.
#   transcript_offset (N) — byte offset into the streaming transcript JSONL.
#   ttl_expiry        (N) — Unix epoch seconds; DynamoDB TTL removes stale rows.
#
# Billing: PAY_PER_REQUEST — chat traffic is bursty; on-demand avoids
#   provisioned-capacity waste.
#
# TTL: ttl_expiry (N, Unix epoch seconds) — DynamoDB native TTL automatically
#   removes stale message rows. NOTE: ttl_expiry MUST be a Number (Unix epoch),
#   NOT an ISO8601 string. Writers must supply: now_unix + retention_seconds.
#
# This phase only writes; no consumer reads from this table yet (Phase B).
resource "aws_dynamodb_table" "slack_stream_messages" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "channel_id"
  range_key    = "slack_ts"

  attribute {
    name = "channel_id"
    type = "S"
  }

  attribute {
    name = "slack_ts"
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
    Component = "km-slack-transcript"
    ManagedBy = "klankermaker"
  })
}
