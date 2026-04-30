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
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/lambda-slack-bridge/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/lambda-slack-bridge/v1.0.0"
}

# km-identities: provides Ed25519 public key table (RESEARCH.md correction #1)
dependency "identities" {
  config_path = "../dynamodb-identities"
  mock_outputs_allowed_on_destroy = true
  mock_outputs = {
    table_name = "km-identities"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-identities"
  }
}

# km-sandboxes: provides slack_channel_id for channel ownership checks
dependency "sandboxes" {
  config_path = "../dynamodb-sandboxes"
  mock_outputs_allowed_on_destroy = true
  mock_outputs = {
    table_name = "km-sandboxes"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-sandboxes"
  }
}

# km-slack-bridge-nonces: provides the replay-protection nonce table
dependency "nonces" {
  config_path = "../dynamodb-slack-nonces"
  mock_outputs_allowed_on_destroy = true
  mock_outputs = {
    table_name = "km-slack-bridge-nonces"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-slack-bridge-nonces"
  }
}

inputs = {
  lambda_zip_path       = "${local.repo_root}/build/km-slack-bridge.zip"
  identities_table_name = dependency.identities.outputs.table_name
  identities_table_arn  = dependency.identities.outputs.table_arn
  sandboxes_table_name  = dependency.sandboxes.outputs.table_name
  sandboxes_table_arn   = dependency.sandboxes.outputs.table_arn
  nonces_table_name     = dependency.nonces.outputs.table_name
  nonces_table_arn      = dependency.nonces.outputs.table_arn
  kms_key_arn           = get_env("KM_PLATFORM_KMS_KEY_ARN", "")
  bot_token_path        = "/km/slack/bot-token"
  tags = {
    "km:component" = "slack-bridge"
    "km:managed"   = "true"
  }
}
