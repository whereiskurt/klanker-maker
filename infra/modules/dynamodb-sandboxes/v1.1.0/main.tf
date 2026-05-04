# DynamoDB sandbox metadata table — v1.1.0.
#
# Key design:
#   sandbox_id (S) = hash key — one row per sandbox metadata record.
#   alias (S)      = GSI hash key — enables lookup by human-friendly alias name.
#   slack_channel_id (S) = GSI hash key — enables bridge Lambda to resolve
#                          (channel_id) → (sandbox_id, queue_url) for inbound
#                          Slack dispatch without a full table scan.
#   No sort key — each sandbox has exactly one metadata row.
#
# v1.1.0 adds the slack_channel_id-index GSI. This is an ADDITIVE change;
# the primary key, existing alias-index GSI, and TTL are unchanged.
# There is no destroy/replace of the table — Terraform applies this as an
# in-place index addition.
#
# Billing: PAY_PER_REQUEST — sandbox metadata writes happen at create/destroy
# events; on-demand avoids provisioned capacity waste.
#
# TTL: ttl_expiry attribute used for automatic cleanup after sandbox destroy
# grace period — avoids requiring explicit delete in teardown path.
resource "aws_dynamodb_table" "sandboxes" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "sandbox_id"

  # No sort key — each sandbox has a single metadata row keyed by sandbox_id.

  attribute {
    name = "sandbox_id"
    type = "S"
  }

  attribute {
    name = "alias"
    type = "S"
  }

  attribute {
    name = "slack_channel_id"
    type = "S"
  }

  # GSI for alias-based lookups — allows resolving sandbox_id from a human-
  # friendly alias name (e.g. "research.team-a") without a full table scan.
  global_secondary_index {
    name            = "alias-index"
    hash_key        = "alias"
    projection_type = "ALL"
  }

  # GSI for Slack channel-based lookups — allows the bridge Lambda to resolve
  # which sandbox owns a given Slack channel_id without scanning the table.
  # Added in v1.1.0 for Phase 67 Slack inbound dispatch.
  global_secondary_index {
    name            = "slack_channel_id-index"
    hash_key        = "slack_channel_id"
    projection_type = "ALL"
  }

  # TTL on ttl_expiry — allows automatic expiry of sandbox metadata records
  # after sandbox teardown. Set ttl_expiry to sandbox teardown time + grace period.
  ttl {
    attribute_name = "ttl_expiry"
    enabled        = true
  }

  tags = merge(var.tags, {
    Module  = "dynamodb-sandboxes"
    Version = "v1.1.0"
  })
}
