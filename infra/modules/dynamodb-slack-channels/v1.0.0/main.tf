# DynamoDB Slack channels table — v1.0.0.
#
# Key design:
#   alias (S) = hash key  — identifies the sandbox alias (e.g. "myteam").
#
# Each row maps an alias to its current Slack channel_id so the create-handler
# can resolve an existing channel on alias reuse WITHOUT a bounded O(n) scan
# of km-sandboxes. This is the P2 durable store consulted during create-time
# resolution (plan 104-01) before any channel-membership API call.
#
# Billing: PAY_PER_REQUEST — create/destroy cycles are bursty; on-demand avoids
#   provisioned capacity waste.
#
# NO TTL: this mapping must persist across sandbox destroy/recreate cycles.
#   A stale row (channel archived/deleted) self-heals via the channel_not_found
#   recreate path in plan 104-01 resolver: the create-handler attempts GetChannel,
#   detects channel_not_found, creates a fresh channel, and overwrites the row.
#   Do NOT add a ttl {} block — the mapping is intentionally durable.
resource "aws_dynamodb_table" "slack_channels" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "alias"

  attribute {
    name = "alias"
    type = "S"
  }

  point_in_time_recovery {
    enabled = false
  }

  server_side_encryption {
    enabled = true
  }

  tags = merge(var.tags, {
    Name      = var.table_name
    Component = "km-slack-channels"
  })
}
