# infra/live/use1/sandbox-secrets-key/terragrunt.hcl
# Live wiring for the foundation KMS secrets key module.
# Applied by `km bootstrap --shared-secrets-key` (Plan 89-04).
#
# KM_REGISTER_SECRETS_KEY is set by the `km bootstrap --shared-secrets-key`
# auto-detect logic before invoking terragrunt. Default true = fresh account,
# first-time apply.

locals {
  repo_root     = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars     = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  aws_region    = local.region_config.locals.region_full
  resource_prefix = local.site_vars.locals.site.label
}

include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "${local.repo_root}/infra/modules/sandbox-secrets-key/v1.0.0"
}

inputs = {
  resource_prefix      = local.resource_prefix
  aws_region           = local.aws_region
  register_secrets_key = tobool(get_env("KM_REGISTER_SECRETS_KEY", "true"))

  tags = {
    "km:owner"     = "foundation"
    "km:phase"     = "89"
    "km:component" = "sandbox-secrets-key"
  }
}
