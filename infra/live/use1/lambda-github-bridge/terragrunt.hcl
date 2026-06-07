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
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/lambda-github-bridge/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/lambda-github-bridge/v1.1.0"
}

# km-sandboxes: provides alias-index GSI query + GetItem for github_inbound_queue_url lookup
dependency "sandboxes" {
  config_path = "../dynamodb-sandboxes"
  mock_outputs = {
    table_name = "km-sandboxes"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-sandboxes"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

# km-slack-bridge-nonces: shared nonce table for replay protection (GitHub uses distinct
# "github-delivery:"+guid key namespace within the same table — no new infra needed)
dependency "nonces" {
  config_path = "../dynamodb-slack-nonces"
  mock_outputs = {
    table_name = "km-slack-bridge-nonces"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-slack-bridge-nonces"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

# km-github-threads: (repo, number) → {sandbox_id, agent_session_id} continuity table
# (Phase 98-02: GH-X-CONTINUITY + GH-X-THREADBYPASS)
dependency "github_threads" {
  config_path = "../dynamodb-github-threads"
  mock_outputs = {
    table_name = "km-github-threads"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-github-threads"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

inputs = {
  # Required (no-default) inputs
  lambda_zip_path   = "${local.repo_root}/build/km-github-bridge.zip"
  sandboxes_table_arn = dependency.sandboxes.outputs.table_arn
  nonces_table_arn    = dependency.nonces.outputs.table_arn

  # Phase 98-02: km-github-threads continuity table
  github_threads_table_name = dependency.github_threads.outputs.table_name
  github_threads_table_arn  = dependency.github_threads.outputs.table_arn

  # Prefix-aware overrides
  resource_prefix      = local.site_vars.locals.site.label
  sandboxes_table_name = "${local.site_vars.locals.site.label}-sandboxes"
  nonces_table_name    = "${local.site_vars.locals.site.label}-slack-bridge-nonces"

  # Platform configuration
  kms_key_arn      = get_env("KM_PLATFORM_KMS_KEY_ARN", "")
  artifacts_bucket = get_env("KM_ARTIFACTS_BUCKET", "")

  # GitHub App configuration — populated by km github init / km configure github.
  # KM_GITHUB_REPOS is a JSON-serialized list of RepoEntry objects exported by
  # ExportTerragruntEnvVars from km-config.yaml github.repos. Empty string = bridge dormant.
  github_repos_json      = get_env("KM_GITHUB_REPOS", "")
  github_default_profile = get_env("KM_GITHUB_DEFAULT_PROFILE", "github-review")

  # SSM paths for GitHub App credentials (GetSsmPrefix() = "/{prefix}/")
  webhook_secret_path   = "/${local.site_vars.locals.site.label}/config/github/webhook-secret"
  bot_login_path        = "/${local.site_vars.locals.site.label}/config/github/bot-login"
  app_client_id_path    = "/${local.site_vars.locals.site.label}/config/github/app-client-id"
  private_key_path      = "/${local.site_vars.locals.site.label}/config/github/private-key"
  installation_id_path  = "/${local.site_vars.locals.site.label}/config/github/installation-id"

  tags = {
    "km:component" = "github-bridge"
    "km:managed"   = "true"
  }
}
