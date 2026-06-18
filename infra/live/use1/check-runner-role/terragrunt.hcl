locals {
  repo_root  = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars  = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")

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
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/check-runner-role/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/check-runner-role/v1.0.0"
}

# No dependency blocks — the role references only string inputs (bucket name,
# resource prefix, table name), not other module outputs.

inputs = {
  role_name        = "${local.site_vars.locals.site.label}-check-runner"
  resource_prefix  = local.site_vars.locals.site.label
  artifacts_bucket = get_env("KM_ARTIFACTS_BUCKET", "")
  table_name       = "${local.site_vars.locals.site.label}-checks"
  tags = {
    "km:component" = "km-check"
    "km:managed"   = "true"
  }
}
