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
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/ses/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/ses/v2.0.0"
}

inputs = {
  # v2.0.0: rule-only module; rule set, domain identity, DKIM, and MX records
  # are owned by the foundation module (ses-shared-rule-set/v1.0.0, Phase 84).
  email_domain         = "${local.site_vars.locals.site.email_subdomain}.${local.site_vars.locals.site.domain}"
  artifact_bucket_name = get_env("KM_ARTIFACTS_BUCKET", "")
  resource_prefix      = get_env("KM_RESOURCE_PREFIX", "km")
}
