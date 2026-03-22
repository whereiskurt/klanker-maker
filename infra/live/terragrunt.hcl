locals {
  # Compute absolute path to repo root anchored by CLAUDE.md
  repo_root  = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars  = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
}

# S3 + DynamoDB backend for all Terraform state
remote_state {
  backend = "s3"

  generate = {
    path      = "backend.tf"
    if_exists = "overwrite_terragrunt"
  }

  config = {
    bucket         = local.site_vars.locals.backend.bucket
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${path_relative_to_include()}/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

# AWS provider generation
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
      region = "${local.site_vars.locals.region.full}"

      default_tags {
        tags = {
          ManagedBy  = "Terragrunt"
          km_label   = "${local.site_vars.locals.site.label}"
        }
      }
    }
  EOF
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
