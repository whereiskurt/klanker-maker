locals {
  repo_root  = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars  = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")

  # Region from region.hcl in the parent directory (e.g., infra/live/use1/region.hcl)
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
  region_full   = local.region_config.locals.region_full
}

include "root" {
  path = find_in_parent_folders("terragrunt.hcl")
}

# Override the root-generated provider block to add the replica alias provider.
# The s3-replication module requires provider "aws" { alias = "replica" } targeting
# the destination region. Using the same block name "provider" with
# if_exists = "overwrite_terragrunt" ensures only one provider.tf is generated.
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

    provider "aws" {
      alias  = "replica"
      region = "${get_env("KM_REPLICA_REGION", "us-west-2")}"

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
