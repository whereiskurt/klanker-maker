# Phase 75: 30-day expiration on slack-inbound/ prefix.
# Matches km-slack-threads DDB TTL (30 days). Future phases can add additional
# rules (e.g., Phase 68's transcripts/ prefix) by extending this resource.
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
}
