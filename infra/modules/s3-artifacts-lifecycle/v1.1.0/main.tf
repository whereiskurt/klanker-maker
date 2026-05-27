# Phase 75: 30-day expiration on slack-inbound/ prefix.
# Matches km-slack-threads DDB TTL (30 days). Future phases can add additional
# rules (e.g., Phase 68's transcripts/ prefix) by extending this resource.
#
# Phase 89 (v1.1.0): Added sandbox-secrets-7day rule — 7-day expiration on
# sandboxes/ prefix for SOPS-encrypted bundles uploaded during km create.
resource "aws_s3_bucket_lifecycle_configuration" "artifacts" {
  bucket = var.bucket_name

  rule {
    id     = "slack-inbound-30day"
    status = "Enabled"

    filter {
      prefix = "slack-inbound/"
    }

    expiration {
      days = 30
    }
  }

  rule {
    id     = "sandbox-secrets-7day"
    status = "Enabled"

    filter {
      prefix = "sandboxes/"
    }

    expiration {
      days = 7
    }
  }
}
