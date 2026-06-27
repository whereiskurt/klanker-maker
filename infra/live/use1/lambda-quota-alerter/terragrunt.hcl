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
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/lambda-quota-alerter/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/lambda-quota-alerter/v1.0.0"
}

# dynamodb-action-quota: provides stream_arn for the event_source_mapping
# and table_name/table_arn for IAM + env vars.
# mock_outputs_allowed_terraform_commands includes "show" (memory project_terragrunt_show_needs_mocks).
dependency "quota_table" {
  config_path = "../dynamodb-action-quota"
  mock_outputs = {
    table_name = "km-action-quota"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-action-quota"
    stream_arn = "arn:aws:dynamodb:us-east-1:000000000000:table/km-action-quota/stream/2026-06-27T00:00:00.000"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

# km-sandboxes: provides table_arn for the GetItem IAM grant.
dependency "sandboxes" {
  config_path = "../dynamodb-sandboxes"
  mock_outputs = {
    table_name = "km-sandboxes"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-sandboxes"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

inputs = {
  lambda_zip_path = "${local.repo_root}/build/km-quota-alerter.zip"

  # Phase 121: DDB Stream → Lambda event source mapping
  quota_stream_arn = dependency.quota_table.outputs.stream_arn
  quota_table_name = dependency.quota_table.outputs.table_name
  quota_table_arn  = dependency.quota_table.outputs.table_arn

  # km-sandboxes: resolve slack_channel_id for channel-level user notices
  sandboxes_table_name = "${local.site_vars.locals.site.label}-sandboxes"
  sandboxes_table_arn  = dependency.sandboxes.outputs.table_arn

  # Operator email + SES domain (injected from km-config.yaml via km init)
  operator_email = get_env("KM_OPERATOR_EMAIL", "")
  email_domain   = "${local.site_vars.locals.site.email_subdomain}.${local.site_vars.locals.site.domain}"

  # Optional Slack control channel + bot token for channel-level notices
  slack_control_channel = get_env("KM_SLACK_CONTROL_CHANNEL", "")
  bot_token_path        = "/${local.site_vars.locals.site.label}/slack/bot-token"

  # Platform CMK for encrypted env vars (same pattern as lambda-slack-bridge)
  kms_key_arn = get_env("KM_PLATFORM_KMS_KEY_ARN", "")

  # Resource prefix flows through to IAM resource names
  resource_prefix = local.site_vars.locals.site.label

  artifacts_bucket = get_env("KM_ARTIFACTS_BUCKET", "")

  tags = {
    "km:component" = "quota-alerter"
    "km:managed"   = "true"
  }
}
