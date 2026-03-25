# DynamoDB identity tracking table — v1.1.0.
#
# Changes from v1.0.0:
#   - Added 'alias' attribute (S) as GSI hash key.
#   - Added 'alias-index' GSI (ALL projection) for alias-based lookups.
#
# Key design:
#   sandbox_id (S) = hash key — one row per sandbox identity record.
#   alias (S)      = GSI hash key — enables lookup by human-friendly alias name.
#   No sort key — each sandbox has exactly one identity row.
#
# Billing: PAY_PER_REQUEST — identity writes happen at sandbox creation and
# key rotation events; on-demand avoids provisioned capacity waste.
#
# TTL: expiresAt attribute used for automatic cleanup after sandbox destroy
# grace period — avoids requiring explicit delete in teardown path.
resource "aws_dynamodb_table" "identities" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "sandbox_id"

  # No sort key — each sandbox has a single identity row keyed by sandbox_id.

  # No DynamoDB Streams — no Lambda trigger needed for identity rows.
  # Identity events are read on-demand by sandboxes performing key lookup.

  attribute {
    name = "sandbox_id"
    type = "S"
  }

  attribute {
    name = "alias"
    type = "S"
  }

  # GSI for alias-based lookups — allows resolving sandbox_id from a human-
  # friendly alias name (e.g. "research.team-a") without a full table scan.
  global_secondary_index {
    name            = "alias-index"
    hash_key        = "alias"
    projection_type = "ALL"
  }

  # TTL on expiresAt — allows automatic expiry of identity records after
  # sandbox teardown. Set expiresAt to sandbox teardown time + grace period.
  ttl {
    attribute_name = "expiresAt"
    enabled        = true
  }

  # Global table replicas for multi-region sandbox deployments.
  # replica_regions variable is empty by default (single-region v1 deployment).
  # Follows the same global table replication pattern as dynamodb-budget module.
  dynamic "replica" {
    for_each = var.replica_regions
    content {
      region_name = replica.value
    }
  }

  tags = merge(var.tags, {
    Module  = "dynamodb-identities"
    Version = "v1.1.0"
  })
}
