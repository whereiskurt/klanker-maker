# DynamoDB schedule metadata table — v1.0.0.
#
# Key design:
#   schedule_name (S) = hash key — one row per km-at schedule entry.
#   No sort key — each schedule has exactly one metadata row.
#
# Billing: PAY_PER_REQUEST — schedule writes happen at km-at create/cancel
# events; on-demand avoids provisioned capacity waste.
resource "aws_dynamodb_table" "schedules" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "schedule_name"

  attribute {
    name = "schedule_name"
    type = "S"
  }

  tags = merge(var.tags, {
    Module  = "dynamodb-schedules"
    Version = "v1.0.0"
  })
}
