locals {
  # Compute absolute path to repo root anchored by CLAUDE.md
  repo_root  = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars  = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  svc_config = read_terragrunt_config("${get_terragrunt_dir()}/service.hcl")

  # Sandbox identity — the profile compiler writes this value when generating sandbox dirs
  sandbox_id = local.svc_config.locals.sandbox_id

  # Per-sandbox state key includes sandbox_id for isolation (INFR-06)
  # Each sandbox gets its own Terraform state, fully isolated from others
  state_key = "sandboxes/${local.sandbox_id}"
}

# Include root terragrunt.hcl (remote_state + provider generation)
include "root" {
  path = find_in_parent_folders("terragrunt.hcl")
}

# Override the remote_state key to include sandbox_id for isolation
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
# The profile compiler substitutes the correct module path (ec2spot or ecs-*)
# Example for ECS: "${local.repo_root}/infra/modules/ecs-cluster/v1.0.0"
# Example for EC2: "${local.repo_root}/infra/modules/ec2spot/v1.0.0"
terraform {
  source = "${local.repo_root}/infra/modules/${local.svc_config.locals.substrate_module}/v1.0.0"
}

inputs = merge(
  # Common inputs for all sandboxes
  {
    km_label       = local.site_vars.locals.site.label
    km_random_suffix = local.site_vars.locals.site.random_suffix
    region_label   = local.site_vars.locals.region.label
    region_full    = local.site_vars.locals.region.full
    sandbox_id     = local.sandbox_id
  },
  # Module-specific inputs from service.hcl
  local.svc_config.locals.module_inputs
)
