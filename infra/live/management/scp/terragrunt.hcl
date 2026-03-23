locals {
  site     = read_terragrunt_config(find_in_parent_folders("site.hcl")).locals.site
  accounts = read_terragrunt_config(find_in_parent_folders("site.hcl")).locals.accounts
  region   = read_terragrunt_config(find_in_parent_folders("site.hcl")).locals.region
}

# Provider: Organizations API is global; must operate from management account.
# The km-org-admin role is provisioned in the management account with Organizations permissions.
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
        role_arn = "arn:aws:iam::${local.accounts.management}:role/km-org-admin"
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
  source = "${dirname(find_in_parent_folders("CLAUDE.md"))}/infra/modules/scp//v1.0.0"
}

inputs = {
  application_account_id = local.accounts.application

  # Single region from site config — SCP region lock enforces this
  allowed_regions = [local.region.full]

  # Trusted roles that can bypass the containment Deny statements.
  # Note: km-budget-enforcer-* and km-ec2spot-ssm-* are intentionally NOT listed here.
  # They are handled inside the module with statement-specific carve-outs (IAM and SSM only).
  trusted_role_arns = [
    # Operator SSO roles — must always be able to manage infrastructure
    "arn:aws:iam::${local.accounts.application}:role/AWSReservedSSO_*_*",
    # Future dedicated provisioner role (not yet deployed as of Phase 10)
    "arn:aws:iam::${local.accounts.application}:role/km-provisioner-*",
    # Future dedicated lifecycle role (not yet deployed as of Phase 10)
    "arn:aws:iam::${local.accounts.application}:role/km-lifecycle-*",
    # Spot handler — launches EC2 Spot instances (instance mutation carve-out handled in module)
    "arn:aws:iam::${local.accounts.application}:role/km-ecs-spot-handler",
    # TTL handler — tears down sandboxes on expiry
    "arn:aws:iam::${local.accounts.application}:role/km-ttl-handler",
  ]
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
