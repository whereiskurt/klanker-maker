# DynamoDB nonce table for km-slack-bridge replay protection.
#
# Key design:
#   nonce (S) = hash key — one row per nonce, keyed by the nonce string.
#   No sort key — nonce is globally unique.
#
# Billing: PAY_PER_REQUEST — nonce writes happen on every bridge request.
#   On-demand avoids provisioned capacity waste for bursty traffic.
#
# TTL: ttl_expiry (N, Unix epoch seconds) — DynamoDB native TTL automatically
#   removes expired nonces. Bridge sets TTL to 600s (NonceTTLSeconds).
resource "aws_dynamodb_table" "nonces" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "nonce"

  attribute {
    name = "nonce"
    type = "S"
  }

  ttl {
    attribute_name = "ttl_expiry"
    enabled        = true
  }

  point_in_time_recovery {
    enabled = false
  }

  tags = merge(var.tags, {
    "km:component" = "slack-bridge"
    "km:purpose"   = "replay-protection-nonce-store"
    "km:managed"   = "true"
  })
}
