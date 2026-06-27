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
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/lambda-h1-bridge/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/lambda-h1-bridge/v1.0.0"
}

# km-sandboxes: alias-index GSI query (warm-path alias→sandbox_id) + GetItem for the
# h1_inbound_queue_url attribute + UpdateItem for status write-back after auto-resume.
dependency "sandboxes" {
  config_path = "../dynamodb-sandboxes"
  mock_outputs = {
    table_name = "km-sandboxes"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-sandboxes"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

# km-slack-bridge-nonces: shared nonce table for replay protection. H1 uses a distinct
# "h1-delivery:"+guid key namespace within the same table — no new infra needed.
dependency "nonces" {
  config_path = "../dynamodb-slack-nonces"
  mock_outputs = {
    table_name = "km-slack-bridge-nonces"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-slack-bridge-nonces"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

# km-h1-threads: (report_id, target) → {sandbox_id, agent_session_id, agent_type}
# continuity table. The table module + live unit land in Plan 08; until then this
# dependency resolves via mock_outputs so terraform validate / plan succeed.
dependency "h1_threads" {
  config_path  = "../dynamodb-h1-threads"
  skip_outputs = false
  mock_outputs = {
    table_name = "km-h1-threads"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-h1-threads"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

# km-action-quota: provides the action-quota counter table (Phase 121). Populates
# quota_table_arn → KM_QUOTA_TABLE env + the quota IAM grant. Without this the
# bridge's quota/auto-freeze enforcement stays dormant (KM_QUOTA_TABLE="").
dependency "action_quota" {
  config_path = "../dynamodb-action-quota"
  mock_outputs = {
    table_name = "km-action-quota"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-action-quota"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

inputs = {
  # Required (no-default) inputs
  lambda_zip_path     = "${local.repo_root}/build/km-h1-bridge.zip"
  sandboxes_table_arn = dependency.sandboxes.outputs.table_arn
  nonces_table_arn    = dependency.nonces.outputs.table_arn
  quota_table_arn     = dependency.action_quota.outputs.table_arn

  # km-h1-threads continuity table (gated IAM grant — non-empty ARN enables it)
  h1_threads_table_name = dependency.h1_threads.outputs.table_name
  h1_threads_table_arn  = dependency.h1_threads.outputs.table_arn

  # Prefix-aware overrides
  resource_prefix      = local.site_vars.locals.site.label
  sandboxes_table_name = "${local.site_vars.locals.site.label}-sandboxes"
  nonces_table_name    = "${local.site_vars.locals.site.label}-slack-bridge-nonces"

  # Platform configuration
  kms_key_arn      = get_env("KM_PLATFORM_KMS_KEY_ARN", "")
  artifacts_bucket = get_env("KM_ARTIFACTS_BUCKET", "")

  # HackerOne program configuration — populated by km h1 init / km configure.
  # KM_H1_PROGRAMS is a JSON-serialized program config exported by ExportTerragruntEnvVars
  # from km-config.yaml h1.programs. Empty string = bridge dormant. (init.go wiring: Plan 08.)
  h1_programs_json   = get_env("KM_H1_PROGRAMS", "")
  h1_default_profile = get_env("KM_H1_DEFAULT_PROFILE", "h1-triage")
  h1_bot_handle      = get_env("KM_H1_BOT_HANDLE", "")
  h1_api_base_url    = get_env("KM_H1_API_BASE_URL", "")

  # SSM paths for HackerOne config (GetSsmPrefix() = "/{prefix}/")
  webhook_secret_path = "/${local.site_vars.locals.site.label}/config/h1/webhook-secret"
  api_username_path   = "/${local.site_vars.locals.site.label}/config/h1/api-username"
  api_token_path      = "/${local.site_vars.locals.site.label}/config/h1/api-token"
  commands_path       = "/${local.site_vars.locals.site.label}/config/h1/commands"

  tags = {
    "km:component" = "h1-bridge"
    "km:managed"   = "true"
  }
}
