# infra/live/use1/ses-shared-rule-set/terragrunt.hcl
# Live wiring for the foundation SES shared-rule-set module.
# Applied by `km bootstrap --shared-ses` (Plan 84-07).
#
# KM_REGISTER_SHARED_RULESET / KM_REGISTER_DOMAIN_IDENTITY are set by the
# `km bootstrap --shared-ses` auto-detect logic (Plan 84-07 Task 2) before
# invoking terragrunt. Default true = fresh account, first-time apply.

locals {
  repo_root  = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars  = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")

  # Region from region.hcl in the parent directory
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  aws_region    = local.region_config.locals.region_full

  # Compose email domain the same way ses/terragrunt.hcl does via site_vars
  email_domain = "${local.site_vars.locals.site.email_subdomain}.${local.site_vars.locals.site.domain}"
}

include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "${local.repo_root}/infra/modules/ses-shared-rule-set/v1.0.0"
}

inputs = {
  rule_set_name            = "sandbox-email-shared"
  email_domain             = local.email_domain
  aws_region               = local.aws_region
  hosted_zone_id           = get_env("KM_ROUTE53_ZONE_ID", "")
  register_shared_rule_set = tobool(get_env("KM_REGISTER_SHARED_RULESET", "true"))
  register_domain_identity = tobool(get_env("KM_REGISTER_DOMAIN_IDENTITY", "true"))

  tags = {
    "km:owner"     = "foundation"
    "km:phase"     = "84"
    "km:component" = "ses-shared-rule-set"
  }
}
