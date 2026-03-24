# Default provider uses the caller's region (source bucket region).
# Provider alias "replica" targets the destination region.
provider "aws" {
  alias  = "replica"
  region = var.destination_region
}

# ============================================================
# Replica bucket in destination region
# ============================================================

resource "aws_s3_bucket" "replica" {
  provider = aws.replica
  bucket   = var.destination_bucket_name
}

# ============================================================
# Versioning — required on both source and destination for replication
# ============================================================

# Enable versioning on the source bucket (pre-existing bucket, managed elsewhere).
resource "aws_s3_bucket_versioning" "source" {
  bucket = var.source_bucket_name
  versioning_configuration {
    status = "Enabled"
  }
}

# Enable versioning on the replica bucket.
resource "aws_s3_bucket_versioning" "replica" {
  provider = aws.replica
  bucket   = aws_s3_bucket.replica.id
  versioning_configuration {
    status = "Enabled"
  }
}

# ============================================================
# IAM role for S3 replication
# ============================================================

resource "aws_iam_role" "replication" {
  name = "km-s3-replication-${var.source_bucket_name}"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Service = "s3.amazonaws.com"
        }
        Action = "sts:AssumeRole"
      }
    ]
  })
}

resource "aws_iam_role_policy" "replication" {
  name = "km-s3-replication-policy"
  role = aws_iam_role.replication.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "s3:GetReplicationConfiguration",
          "s3:ListBucket"
        ]
        Resource = [var.source_bucket_arn]
      },
      {
        Effect = "Allow"
        Action = [
          "s3:GetObjectVersionForReplication",
          "s3:GetObjectVersionAcl",
          "s3:GetObjectVersionTagging"
        ]
        Resource = ["${var.source_bucket_arn}/*"]
      },
      {
        Effect = "Allow"
        Action = [
          "s3:ReplicateObject",
          "s3:ReplicateDelete",
          "s3:ReplicateTags"
        ]
        Resource = ["${aws_s3_bucket.replica.arn}/*"]
      }
    ]
  })
}

# ============================================================
# Replication configuration on the source bucket
# Only replicate artifacts/ prefix — mail/ is ephemeral inbox data.
# ============================================================

resource "aws_s3_bucket_replication_configuration" "source" {
  bucket = var.source_bucket_name
  role   = aws_iam_role.replication.arn

  rule {
    id     = "replicate-artifacts"
    status = "Enabled"

    filter {
      prefix = "artifacts/"
    }

    delete_marker_replication {
      status = "Enabled"
    }

    destination {
      bucket        = aws_s3_bucket.replica.arn
      storage_class = "STANDARD"
    }
  }

  depends_on = [
    aws_s3_bucket_versioning.source,
    aws_s3_bucket_versioning.replica,
  ]
}
