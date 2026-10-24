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
  mock_outputs = {
    table_name = "km-identities"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-identities"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

# km-sandboxes: provides slack_channel_id for channel ownership checks
dependency "sandboxes" {
  config_path = "../dynamodb-sandboxes"
  mock_outputs = {
    table_name = "km-sandboxes"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-sandboxes"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

# km-slack-bridge-nonces: provides the replay-protection nonce table
dependency "nonces" {
  config_path = "../dynamodb-slack-nonces"
  mock_outputs = {
    table_name = "km-slack-bridge-nonces"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-slack-bridge-nonces"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

# km-slack-threads: provides the Slack thread-to-session mapping table (Phase 67/Pitfall 9)
dependency "slack_threads" {
  config_path = "../dynamodb-slack-threads"
  mock_outputs = {
    table_name = "km-slack-threads"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-slack-threads"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

# km-slack-history (2026-10-24): the per-route channel mirror the bridge writes
# and reads. Also orders this unit after the table under run-all.
dependency "slack_history" {
  config_path = "../dynamodb-slack-history"
  mock_outputs = {
    table_name = "km-slack-history"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-slack-history"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

# km-github-threads / km-h1-threads (2026-10-18): the bridge's `session` action
# records GitHub/H1 agent sessions there on the pollers' behalf, since the
# sandbox role has no threads-table grant (ec2spot v1.9.0). The dependency also
# orders this unit after both tables under terragrunt run-all.
dependency "github_threads" {
  config_path = "../dynamodb-github-threads"
  mock_outputs = {
    table_name = "km-github-threads"
    table_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/km-github-threads"
  }
  mock_outputs_allowed_terraform_commands = ["validate", "plan", "destroy", "init", "apply", "show"]
}

dependency "h1_threads" {
  config_path = "../dynamodb-h1-threads"
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
  lambda_zip_path       = "${local.repo_root}/build/km-slack-bridge.zip"
  quota_table_arn       = dependency.action_quota.outputs.table_arn
  identities_table_name = dependency.identities.outputs.table_name
  identities_table_arn  = dependency.identities.outputs.table_arn
  sandboxes_table_name  = "${local.site_vars.locals.site.label}-sandboxes"
  sandboxes_table_arn   = dependency.sandboxes.outputs.table_arn
  nonces_table_name     = dependency.nonces.outputs.table_name
  nonces_table_arn      = dependency.nonces.outputs.table_arn
  kms_key_arn           = get_env("KM_PLATFORM_KMS_KEY_ARN", "")
  bot_token_path        = "/${local.site_vars.locals.site.label}/slack/bot-token"
  # Phase 68: bridge needs S3 read access to fetch transcripts/* objects
  # uploaded by the sandbox-side hook before forwarding to Slack files.upload.
  artifacts_bucket = get_env("KM_ARTIFACTS_BUCKET", "")
  # Phase 66: prefix-aware inputs (Pitfall 9 fix)
  resource_prefix          = local.site_vars.locals.site.label
  signing_secret_path      = "/${local.site_vars.locals.site.label}/slack/signing-secret"
  slack_threads_table_name = dependency.slack_threads.outputs.table_name
  # 2026-10-24: channel history mirror + pacing
  slack_history_table_name       = dependency.slack_history.outputs.table_name
  slack_history_table_arn        = dependency.slack_history.outputs.table_arn
  slack_history_page_size        = get_env("KM_SLACK_HISTORY_PAGE_SIZE", "200")
  slack_history_interval_seconds = get_env("KM_SLACK_HISTORY_INTERVAL_SECONDS", "2")
  # 2026-10-18: km-slack session writes for the GitHub/H1 pollers
  github_threads_table_name = dependency.github_threads.outputs.table_name
  h1_threads_table_name     = dependency.h1_threads.outputs.table_name
  nonce_table_name          = "${local.site_vars.locals.site.label}-slack-bridge-nonces"

  # Phase 91 — polite-bot mode toggle (install-level default). Operators export
  # KM_SLACK_MENTION_ONLY=true in their shell before `km init --sidecars` to flip
  # the install to mention-only. Empty/unset → "false" (every-message).
  slack_mention_only = get_env("KM_SLACK_MENTION_ONLY", "false")

  # Phase 91 — pre-warmed bot user ID injected from operator shell. km slack init
  # writes the value to SSM at {prefix}slack/bot-user-id; operators export
  # KM_SLACK_BOT_USER_ID=<UID> before `km init --sidecars` to seed the Lambda env.
  # When empty, the bridge falls back to a live auth.test call.
  slack_bot_user_id = get_env("KM_SLACK_BOT_USER_ID", "")

  # Phase 91.4 — first-only-react toggle. Phase 91.1's ExportTerragruntEnvVars
  # populates KM_SLACK_REACT_ALWAYS from km-config.yaml key slack.react_always
  # (tri-state *bool). Empty/unset → "true" (chatty-reactor, current behaviour).
  # Set km-config slack.react_always: false to flip to first-only reactions.
  slack_react_always = get_env("KM_SLACK_REACT_ALWAYS", "true")

  # 2026-10-17 — reaction quiet period. ExportTerragruntEnvVars populates
  # KM_SLACK_REACT_QUIET_SECONDS from km-config.yaml slack.react_quiet_seconds
  # (tri-state *int) only when set; unset → "180". "0" turns it off.
  slack_react_quiet_seconds = get_env("KM_SLACK_REACT_QUIET_SECONDS", "180")

  # Phase 95 — federated relay peer list. Comma-joined /events URLs of sibling
  # km installs. Populated by ExportTerragruntEnvVars from km-config.yaml
  # slack.peer_bridges []string. Empty/absent => no relay (federation off).
  # Requires km init --dry-run=false to deploy (km init --sidecars does NOT
  # update the Lambda environment.variables block — terragrunt apply required).
  slack_peer_bridges = get_env("KM_SLACK_PEER_BRIDGES", "")

  # Phase 96 — default-router toggle. Operator sets slack.default_router: true
  # in km-config.yaml for the front-door install only. Default false = dormant.
  # Requires km init --dry-run=false (NOT --sidecars) — same constraint as mention_only.
  slack_default_router = get_env("KM_SLACK_DEFAULT_ROUTER", "false")

  # Phase 118 — install-level trigger allowlist (comma-joined Uxxxx). Operator sets
  # slack.allow in km-config.yaml; km init exports KM_SLACK_ALLOW. Empty = everyone
  # allowed (dormant). Requires km init --dry-run=false (NOT --sidecars) — env-block change.
  slack_allow = get_env("KM_SLACK_ALLOW", "")

  # 2026-10-16 - Slack default route. km init exports alias, profile and the two
  # lists (ALLOW = install list, PROFILE_ALLOW = the route profile's allowEmails)
  # from km-config.yaml slack.default_route, or none. Requires km init --dry-run=false
  # (env-block change; --sidecars does not update it).
  slack_default_route_alias         = get_env("KM_SLACK_DEFAULT_ROUTE_ALIAS", "")
  slack_default_route_profile       = get_env("KM_SLACK_DEFAULT_ROUTE_PROFILE", "")
  slack_default_route_allow         = get_env("KM_SLACK_DEFAULT_ROUTE_ALLOW", "")
  slack_default_route_profile_allow = get_env("KM_SLACK_DEFAULT_ROUTE_PROFILE_ALLOW", "")

  # 2026-10-22 - channel route table hash. km init / km init --slack export it
  # after writing SSM /{prefix}/slack/routes. Empty = no routes (dormant).
  slack_routes_sha256 = get_env("KM_SLACK_ROUTES_SHA256", "")

  tags = {
    "km:component" = "slack-bridge"
    "km:managed"   = "true"
  }
}
