locals {
  # Compute absolute path to repo root anchored by CLAUDE.md
  repo_root  = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars  = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  svc_config = read_terragrunt_config("${get_terragrunt_dir()}/service.hcl")

  # Sandbox identity — the profile compiler writes this value when generating sandbox dirs
  sandbox_id = local.svc_config.locals.sandbox_id

  # Region extracted from the directory path: infra/live/<region>/sandboxes/<sandbox_id>/
  # The profile compiler places sandboxes under the correct region directory
  region_label = local.svc_config.locals.region_label

  # Per-sandbox state key includes region + sandbox_id for isolation (INFR-06)
  state_key = "${local.region_label}/sandboxes/${local.sandbox_id}"
}

# Include root terragrunt.hcl (remote_state + provider generation)
include "root" {
  path = find_in_parent_folders("root.hcl")
}

# Override the remote_state key to include region + sandbox_id for isolation
remote_state {
  backend = "s3"

  generate = {
    path      = "backend.tf"
    if_exists = "overwrite_terragrunt"
  }

  config = {
    bucket         = local.site_vars.locals.backend.bucket
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.state_key}/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

# Terraform source points to the appropriate module based on substrate
terraform {
  source = "${local.repo_root}/infra/modules/${local.svc_config.locals.substrate_module}/v1.0.0"
}

inputs = merge(
  # Common inputs for all sandboxes
  {
    km_label         = local.site_vars.locals.site.label
    km_random_suffix = local.site_vars.locals.site.random_suffix
    region_label     = local.region_label
    region_full      = local.svc_config.locals.region_full
    sandbox_id       = local.sandbox_id
  },
  # Module-specific inputs from service.hcl
  local.svc_config.locals.module_inputs
)
