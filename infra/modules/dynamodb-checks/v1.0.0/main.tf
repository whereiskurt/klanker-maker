# DynamoDB checks table — v1.0.0.
#
# Key design:
#   name (S) = hash key — check name (e.g. "qotd", "wiz-threat-intel").
#
# Each row tracks one SDK-managed check Lambda:
#   arn             (S) — Lambda ARN
#   runtime         (S) — "python3.13"
#   packageType     (S) — "zip" | "image"
#   memory          (N) — MB
#   timeout         (N) — seconds
#   schedule        (S) — EventBridge Scheduler expression or empty
#   env             (M) — non-secret K=V pairs
#   secretPaths     (SS) — SSM param paths for secrets
#   sourceHash      (S) — SHA256 of resolved KM_CHECK_TRIGGER JSON
#   triggerSummary  (S) — human-readable trigger description
#   createdAt       (S) — ISO8601
#   updatedAt       (S) — ISO8601
#
# Billing: PAY_PER_REQUEST — check deployments and invocations are sparse;
#   on-demand avoids provisioned capacity waste.
#
# No TTL: check rows are long-lived (lifetime = check Lambda lifetime).
# SSE: enabled for at-rest encryption.
resource "aws_dynamodb_table" "checks" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "name"

  attribute {
    name = "name"
    type = "S"
  }

  server_side_encryption {
    enabled = true
  }

  tags = merge(var.tags, {
    Name      = var.table_name
    Component = "km-check"
  })
}
