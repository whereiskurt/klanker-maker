# DynamoDB HackerOne threads table — v1.0.0 (Phase 103).
#
# Key design:
#   report_id (S) = hash key  — HackerOne report ID (e.g. "1234567"). String, NOT
#                               Number — HackerOne report IDs are opaque identifiers
#                               and the customer-API / webhook payloads carry them as
#                               JSON strings; keeping them S avoids any N coercion risk.
#   target    (S) = sort key  — the fanout target key (alias or "h1-{handle}"). One
#                               report fans to N targets (Phase 103 multi-target
#                               dispatch); each (report_id, target) pair is a distinct
#                               thread so N targets never collide on a single report.
#
# Each row maps a (report_id, target) pair to:
#   sandbox_id       (S) — the alias-resolved sandbox handling this report thread.
#   agent_session_id (S) — the last Claude/Codex agent session ID for this thread.
#   agent_type       (S) — "claude" | "codex" (Phase 102 analog). SCHEMA-ON-WRITE:
#                          NOT declared as an attribute here — DynamoDB is schemaless
#                          for non-key attributes, so the column is written by the
#                          bridge at upsert time with NO Terraform migration (mirrors
#                          the GitHub km-github-threads agent_type hangover).
#
# Billing: PAY_PER_REQUEST — HackerOne webhook dispatch is bursty; on-demand avoids
#   provisioned-capacity waste (identical posture to dynamodb-github-threads).
#
# TTL: ttl_expiry (N, Unix epoch seconds) — DynamoDB native TTL removes stale thread
#   rows after a period of inactivity. Writers supply now_unix + N*24*3600. TTL MUST
#   be a Number (Unix epoch), NOT ISO8601.
#
# NOTE: no required_providers block — root.hcl's generate "provider" stanza is the
# single source of provider config (memory project_terragrunt_providers_in_root).
resource "aws_dynamodb_table" "h1_threads" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "report_id"
  range_key    = "target"

  attribute {
    name = "report_id"
    type = "S"
  }

  attribute {
    name = "target"
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
    Component = "km-h1-inbound"
  })
}
