terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
  }
}

# DynamoDB budget tracking table.
#
# Key design:
#   PK (S) = SANDBOX#{sandboxID}  — partition key groups all budget records per sandbox
#   SK (S) = BUDGET#compute       — compute spend row
#   SK (S) = BUDGET#ai#{modelID}  — per-model AI spend row
#   SK (S) = BUDGET#limits        — budget limits configuration row
#
# Billing: PAY_PER_REQUEST — budget writes are infrequent; on-demand avoids
# provisioned capacity waste. Supports global table replication for multi-region
# sandbox deployments.
resource "aws_dynamodb_table" "budget" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "PK"
  range_key    = "SK"

  # DynamoDB Streams: NEW_AND_OLD_IMAGES enables Lambda triggers for budget
  # enforcement checks (budget alert Lambda reads both old and new spend values).
  stream_enabled   = true
  stream_view_type = "NEW_AND_OLD_IMAGES"

  attribute {
    name = "PK"
    type = "S"
  }

  attribute {
    name = "SK"
    type = "S"
  }

  # TTL on expiresAt — allows automatic expiry of spend records after sandbox
  # teardown. Set expiresAt to sandbox teardown time + 30d retention window.
  ttl {
    attribute_name = "expiresAt"
    enabled        = true
  }

  # Global table replicas for multi-region sandbox deployments.
  # Each replica is read-write capable; DynamoDB handles conflict resolution.
  # replica_regions variable is empty by default (single-region deployments).
  dynamic "replica" {
    for_each = var.replica_regions
    content {
      region_name = replica.value
    }
  }

  tags = merge(var.tags, {
    Module  = "dynamodb-budget"
    Version = "v1.0.0"
  })
}
