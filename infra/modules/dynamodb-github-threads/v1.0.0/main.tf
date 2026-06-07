# DynamoDB GitHub threads table — v1.0.0.
#
# Key design:
#   repo   (S) = hash key  — GitHub repository full name (e.g. "myorg/myrepo").
#   number (N) = sort key  — Pull Request / Issue number (integer, stored as N).
#
# NOTE: number is type N (Number), NOT type S (String). This matches the
# Phase 98 thread-store contract where repo+number form a natural composite key
# for a PR thread. Do NOT copy the Slack threads schema (channel_id+thread_ts,
# both S) — GitHub PR numbers are integers and stored as N for correctness.
#
# Each row maps a (repo, number) pair to:
#   sandbox_id       (S) — the alias-resolved sandbox handling this PR thread.
#   agent_session_id (S) — the last Claude/agent session ID for this thread.
#
# Billing: PAY_PER_REQUEST — GitHub webhook dispatch is bursty; on-demand avoids
#   provisioned capacity waste.
#
# TTL: ttl_expiry (N, Unix epoch seconds) — DynamoDB native TTL removes stale
#   thread rows after 30 days of inactivity. Writers must supply:
#   now_unix + 30*24*3600. TTL MUST be a Number (Unix epoch), NOT ISO8601.
resource "aws_dynamodb_table" "github_threads" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "repo"
  range_key    = "number"

  attribute {
    name = "repo"
    type = "S"
  }

  attribute {
    name = "number"
    type = "N"
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
    Component = "km-github-inbound"
  })
}
