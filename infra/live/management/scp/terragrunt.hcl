locals {
  site     = read_terragrunt_config(find_in_parent_folders("site.hcl")).locals.site
  accounts = read_terragrunt_config(find_in_parent_folders("site.hcl")).locals.accounts
}

# Provider: Organizations API is global; must operate from management account.
# The {prefix}-org-admin role (per-install; e.g. km-org-admin, rg-org-admin) is
# provisioned in the management account with Organizations permissions. Its name is
# resource_prefix-scoped (local.site.label) so a non-default install assumes its OWN
# org-admin role — matching `km bootstrap --scp` guidance (runShowSCP) and `km uninit`
# teardown, which both use {prefix}-org-admin.
generate "provider" {
  path      = "provider.tf"
  if_exists = "overwrite_terragrunt"
  contents  = <<-EOF
    terraform {
      required_version = ">= 1.6.0"

      required_providers {
        aws = {
          source  = "hashicorp/aws"
          version = ">= 5.0"
        }
      }
    }

    provider "aws" {
      region = "us-east-1"

      assume_role {
        role_arn = "arn:aws:iam::${local.accounts.organization}:role/${local.site.label}-org-admin"
      }

      default_tags {
        tags = {
          ManagedBy  = "Terragrunt"
          km_label   = "${local.site.label}"
        }
      }
    }
  EOF
}

# State key scoped to management account — not region-prefixed since Organizations is global
remote_state {
  backend = "s3"

  generate = {
    path      = "backend.tf"
    if_exists = "overwrite_terragrunt"
  }

  config = {
    bucket         = read_terragrunt_config(find_in_parent_folders("site.hcl")).locals.backend.bucket
    key            = "${local.site.tf_state_prefix}/management/scp/terraform.tfstate"
    region         = read_terragrunt_config(find_in_parent_folders("site.hcl")).locals.backend.region
    encrypt        = read_terragrunt_config(find_in_parent_folders("site.hcl")).locals.backend.encrypt
    dynamodb_table = read_terragrunt_config(find_in_parent_folders("site.hcl")).locals.backend.dynamodb_table
  }
}

terraform {
  source = "${dirname(find_in_parent_folders("CLAUDE.md"))}/infra/modules/scp//v3.0.0"
}

# The policy document is rendered by pkg/scp from km-config.yaml and passed in
# verbatim — this unit no longer authors it. `km bootstrap --scp-only` (and
# `eval $(km env)` for a hand-driven terragrunt run) set KM_SCP_POLICY_JSON.
#
# get_env with NO default is deliberate: an unset variable fails the run
# instead of applying an empty or stale policy to the application account.
inputs = {
  resource_prefix        = get_env("KM_RESOURCE_PREFIX", "km")
  application_account_id = local.accounts.application
  policy_json            = get_env("KM_SCP_POLICY_JSON")
}

# Retry configuration for transient AWS API errors
errors {
  retry "transient_network" {
    retryable_errors = concat(
      get_default_retryable_errors(), [
        "(?s).*dial tcp .*: i/o timeout.*",
        "(?s).*no such host.*",
        "(?s).*connection reset by peer.*",
        "(?s).*context deadline exceeded.*",
        "(?s).*request send failed.*",
        "(?s).*[aA]ccess [dD]enied for [lL]og[dD]estination.*",
        "(?s).*bucket must exist.*",
        "(?s).*bucket must have versioning enabled.*",
        "(?s).*reading S3 Bucket CORS Configuration.*couldn't find resource.*",
        "(?s).*Missing Resource Identity After Create.*",
      ]
    )

    max_attempts       = 6
    sleep_interval_sec = 10
  }
}
