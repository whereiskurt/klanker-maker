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
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/lambda-webhook-bridge/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/lambda-webhook-bridge/v1.0.0"
}

# km-sandboxes: alias-index GSI query (warm-path alias→sandbox_id) + GetItem for the
# webhook_inbound_queue_url attribute + UpdateItem for status write-back after
# auto-resume / auto-freeze latch + DeleteItem for the Phase 109 self-heal.
dependency "sandboxes" {
  config_path = "../dynamodb-sandboxes"
  mock_outputs = {
    table_name = "km-sandboxes"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-sandboxes"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

# km-slack-bridge-nonces: shared nonce table for replay protection AND the storm
# rate-counter. The webhook bridge uses a distinct "webhook-delivery:"/"webhook-rate:"
# key namespace within the same table — no new infra needed.
dependency "nonces" {
  config_path = "../dynamodb-slack-nonces"
  mock_outputs = {
    table_name = "km-slack-bridge-nonces"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-slack-bridge-nonces"
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
  lambda_zip_path     = "${local.repo_root}/build/km-webhook-bridge.zip"
  sandboxes_table_arn = dependency.sandboxes.outputs.table_arn
  nonces_table_arn    = dependency.nonces.outputs.table_arn
  quota_table_arn     = dependency.action_quota.outputs.table_arn

  # Prefix-aware overrides
  resource_prefix      = local.site_vars.locals.site.label
  sandboxes_table_name = "${local.site_vars.locals.site.label}-sandboxes"
  nonces_table_name    = "${local.site_vars.locals.site.label}-slack-bridge-nonces"

  # Platform configuration
  kms_key_arn      = get_env("KM_PLATFORM_KMS_KEY_ARN", "")
  artifacts_bucket = get_env("KM_ARTIFACTS_BUCKET", "")

  # Generic webhook ingress configuration — populated by km-config.yaml `webhooks:`
  # via ExportTerragruntEnvVars (KM_WEBHOOK_SOURCES). Empty string = bridge dormant
  # (every request silent-drops). KM_WEBHOOK_RATE_LIMIT is reserved for a future
  # standalone env var — rate_limit currently travels embedded inside
  # KM_WEBHOOK_SOURCES (see webhook_rate_limit_json's variable doc comment).
  webhook_sources_json    = get_env("KM_WEBHOOK_SOURCES", "")
  webhook_rate_limit_json = get_env("KM_WEBHOOK_RATE_LIMIT", "")

  tags = {
    "km:component" = "webhook-bridge"
    "km:managed"   = "true"
  }
}
