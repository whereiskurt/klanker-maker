locals {
  repo_root  = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars  = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")

  # Region from region.hcl in the parent directory (e.g., infra/live/use1/region.hcl)
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
  region_full   = local.region_config.locals.region_full
}

# Default provider only — the s3-replication module defines its own
# provider "aws" { alias = "replica" } in main.tf, so we must not
# generate a duplicate here.
# Standalone (no root include) to avoid duplicate generate "provider" blocks.
generate "provider" {
  path      = "provider.tf"
  if_exists = "overwrite_terragrunt"

  contents = <<-EOF
    terraform {
      required_version = ">= 1.6.0"

      required_providers {
        aws = {
          source  = "hashicorp/aws"
          version = ">= 5.0"
        }
      }
    }

    provider "aws" {
      region = "${local.region_full}"

      default_tags {
        tags = {
          ManagedBy = "Terragrunt"
          km_label  = "${local.site_vars.locals.site.label}"
        }
      }
    }
  EOF
}

remote_state {
  backend = "s3"

  generate = {
    path      = "backend.tf"
    if_exists = "overwrite_terragrunt"
  }

  config = {
    bucket         = local.site_vars.locals.backend.bucket
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/s3-replication/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/s3-replication/v1.0.0"
}

inputs = {
  source_bucket_name      = get_env("KM_ARTIFACTS_BUCKET", "")
  source_bucket_arn       = "arn:aws:s3:::${get_env("KM_ARTIFACTS_BUCKET", "")}"
  destination_region      = get_env("KM_REPLICA_REGION", "us-west-2")
  destination_bucket_name = "${get_env("KM_ARTIFACTS_BUCKET", "")}-replica"
}

# Retry configuration for transient AWS API errors
errors {
  retry "transient_network" {
    retryable_errors = concat(
      get_default_retryable_errors(), [
        "(?s).*dial tcp .*: i/o timeout.*",
        "(?s).*no such host.*",
        "(?s).*connection reset by peer.*",
        "(?s).*context deadline exceeded.*",
        "(?s).*request send failed.*",
        "(?s).*[aA]ccess [dD]enied for [lL]og[dD]estination.*",
        "(?s).*bucket must exist.*",
        "(?s).*bucket must have versioning enabled.*",
        "(?s).*reading S3 Bucket CORS Configuration.*couldn't find resource.*",
        "(?s).*Missing Resource Identity After Create.*",
      ]
    )

    max_attempts       = 6
    sleep_interval_sec = 10
  }
}
