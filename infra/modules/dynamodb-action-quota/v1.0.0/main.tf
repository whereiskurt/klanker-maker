# DynamoDB action-quota table — v1.0.0.
#
# Key design:
#   PK (S) = {sandbox}#{action}  — partition key groups all window counters for one
#             (sandbox, action) pair. E.g. "sb-abc123#github_pr".
#   SK (S) = window              — sort key selects the counter row:
#             "lifetime"              → whole-sandbox-life counter (no TTL)
#             "hour#<epoch/3600>"     → fixed hourly bucket counter
#             "day#<epoch/86400>"     → fixed calendar day bucket counter (UTC)
#
# attrs:
#   count       (N)  — atomic ADD target; compare to limit to detect breach
#   ttl         (N)  — epoch timestamp; absent on "lifetime" rows (no expiry)
#   breached_at (N)  — epoch timestamp of first breach; set once, never cleared
#   alert_sent  (S)  — marker written by alerter after notification; prevents re-alert
#
# Billing: PAY_PER_REQUEST — quota writes are bursty (tied to agent action rate);
#   on-demand avoids provisioned capacity waste. Each agent action is at most 3 writes
#   (one per window), and windows are short-lived (TTL-cleaned automatically).
#
# Streams: NEW_AND_OLD_IMAGES — drives the km-quota-alerter Lambda (plan 09). The
#   alerter inspects old vs new images to detect the first breach on each window
#   (breached_at newly set) and emit exactly one alert per (sandbox, action, window).
#
# TTL: attribute "ttl" (epoch seconds). Absent on "lifetime" rows — those are deleted
#   in the km destroy teardown path (alongside the sandbox row). Hourly / daily bucket
#   rows expire ~2h / ~2d after the bucket closes (TTL set to bucket_end + buffer).
#
# SSE: enabled (AWS-managed key; no CMK needed for quota counters).
#
# No required_providers block — root.hcl owns the provider generate stanza.
resource "aws_dynamodb_table" "action_quota" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "PK"
  range_key    = "SK"

  # Streams: alerter Lambda (km-quota-alerter) is triggered by NEW_AND_OLD_IMAGES so it
  # can compare old image (no breached_at) vs new image (breached_at set) to detect
  # the exact first-breach moment without a conditional read.
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

  # TTL: hourly and daily bucket rows auto-expire; lifetime rows omit this attribute
  # and are cleaned up by km destroy (DynamoDB ignores missing TTL attributes).
  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  server_side_encryption {
    enabled = true
  }

  tags = merge(var.tags, {
    Module  = "dynamodb-action-quota"
    Version = "v1.0.0"
  })
}
