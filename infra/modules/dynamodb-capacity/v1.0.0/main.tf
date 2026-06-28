# DynamoDB capacity table — v1.0.0.
#
# Key design:
#   PK (S) = instanceType  — e.g. "g6e.12xlarge"
#   SK (S) = az            — e.g. "us-east-1a"
#
# attrs:
#   available  (BOOL) — true = capacity confirmed by a successful dry-run; false = ICE observed
#   ttl        (N)    — epoch timestamp; rows expire after a configurable window so stale
#                       capacity observations don't permanently suppress AZ selection.
#
# A (instanceType, az) row is written whenever the AZ sweep probes a spot or on-demand
# launch and observes InsufficientCapacityException (false) or a successful dry-run (true).
# The sweep loop reads these rows first and skips known-bad (instanceType, az) pairs so the
# Lambda's 900s budget isn't wasted waiting on a terraform timeouts block.
#
# Billing: PAY_PER_REQUEST — capacity writes are rare (one probe per AZ per sweep);
#   on-demand avoids provisioned capacity waste.
#
# TTL: attribute "ttl" (epoch seconds). Capacity observations go stale quickly; rows
#   should expire within minutes to hours so a recovered AZ is tried again soon.
#
# SSE: enabled (AWS-managed key; no CMK needed for capacity probe records).
#
# No required_providers block — root.hcl owns the provider generate stanza.
resource "aws_dynamodb_table" "capacity" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "instanceType"
  range_key    = "az"

  attribute {
    name = "instanceType"
    type = "S"
  }

  attribute {
    name = "az"
    type = "S"
  }

  # TTL: capacity observations expire automatically so stale ICE records don't
  # permanently suppress an AZ after capacity is restored.
  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  server_side_encryption {
    enabled = true
  }

  tags = merge(var.tags, {
    Module  = "dynamodb-capacity"
    Version = "v1.0.0"
  })
}
