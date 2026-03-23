locals {
  repo_root  = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars  = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")

  # Region from region.hcl in the parent directory (e.g., infra/live/us-east-1/region.hcl)
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
  region_full   = local.region_config.locals.region_full
}

include "root" {
  path = find_in_parent_folders("root.hcl")
}

remote_state {
  backend = "s3"

  generate = {
    path      = "backend.tf"
    if_exists = "overwrite_terragrunt"
  }

  config = {
    bucket         = local.site_vars.locals.backend.bucket
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/network/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/network/v1.0.0"
}

inputs = {
  km_label     = local.site_vars.locals.site.label
  region_label = local.region_label
  sandbox_id   = "shared"

  vpc = {
    cidr_block              = "10.0.0.0/16"
    enable_dns_hostnames    = true
    enable_dns_support      = true
    public_subnets_cidr     = ["10.0.1.0/24", "10.0.2.0/24"]
    private_subnets_cidr    = ["10.0.101.0/24", "10.0.102.0/24"]
    availability_zone_count = 2
    tags = {
      "km:purpose" = "shared-sandbox-vpc"
    }
  }

  nat_gateway = {
    enabled = false
  }
}
