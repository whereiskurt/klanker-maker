locals {
  repo_root = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")

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
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/wiz-outpost-connector/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/wiz-outpost-connector/v1.0.0"
}

# KM_WIZ_OUTPOST_JSON is exported by km init from km-config.yaml wiz.outpost
# (pkg/wizoutpost.ExportJSON). The "{}" default + try() keep this unit
# evaluable (e.g. km uninit) after the wiz: block is removed.
# stack_name must equal wizoutpost.StackName(resource_prefix); site.label IS
# KM_RESOURCE_PREFIX (same source lambda-webhook-bridge uses for resource_prefix).
inputs = {
  stack_name = "${local.site_vars.locals.site.label}-wiz-outpost-connector"
  parameters = try(jsondecode(get_env("KM_WIZ_OUTPOST_JSON", "{}")).parameters, {})
}
