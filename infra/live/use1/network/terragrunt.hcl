locals {
  repo_root  = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars  = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")

  # Region from region.hcl in the parent directory (e.g., infra/live/us-east-1/region.hcl)
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
  region_full   = local.region_config.locals.region_full

  # Full private subnet CIDR list — one per AZ. The network module counts private
  # subnets, their route tables, the subnet associations, the EIPs AND the NAT
  # gateways all off length(var.vpc.private_subnets_cidr), so trimming this list
  # is what makes network.private_subnet_count control the NAT bill.
  all_private_subnets_cidr = ["10.0.101.0/24", "10.0.102.0/24", "10.0.103.0/24", "10.0.104.0/24"]

  # KM_PRIVATE_SUBNET_COUNT — install-level cap on how many of the above are built,
  # written by `km init` from km-config.yaml network.private_subnet_count. The
  # default is the list's OWN length rather than a literal "4": an operator who
  # adds a fifth CIDR above gets it automatically, and the dormant path can never
  # silently start truncating. Out-of-range values are rejected by `km init`
  # (config.ValidatePrivateSubnetCount) before they reach slice(), which would
  # otherwise fail here with an error naming neither the key nor this file.
  private_subnet_count = tonumber(
    get_env("KM_PRIVATE_SUBNET_COUNT", tostring(length(local.all_private_subnets_cidr)))
  )
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
  source = "${local.repo_root}/infra/modules/network/v1.1.0"
}

inputs = {
  km_label     = local.site_vars.locals.site.label
  region_label = local.region_label
  sandbox_id   = "shared"

  vpc = {
    cidr_block              = "10.0.0.0/16"
    enable_dns_hostnames    = true
    enable_dns_support      = true
    public_subnets_cidr     = ["10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24", "10.0.4.0/24"]
    private_subnets_cidr    = slice(local.all_private_subnets_cidr, 0, local.private_subnet_count)
    availability_zone_count = 4
    tags = {
      "km:purpose" = "shared-sandbox-vpc"
    }
  }

  # Phase 125: install-level toggle for per-AZ NAT gateways. tobool(...) is
  # load-bearing here — unlike every other get_env toggle in this repo, which
  # flows into a Lambda environment.variables string map needing no
  # coercion, var.nat_gateway.enabled is a native Terraform bool. The
  # default "false" keeps this dormant when KM_NAT_GATEWAY_ENABLED is unset,
  # reproducing Phase 124 behaviour byte-for-byte.
  nat_gateway = {
    enabled = tobool(get_env("KM_NAT_GATEWAY_ENABLED", "false"))
  }
}
