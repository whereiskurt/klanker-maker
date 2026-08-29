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

  # Per-substrate module version pin (Phase 125 REQ-125-SUBPIN). This file is
  # copied VERBATIM with no templating by pkg/terragrunt.CreateSandboxDir, for
  # both `km create` and the `km destroy` local-directory-missing fallback —
  # so this map is the single live version pin for every new sandbox.
  #
  # Before this map existed, terraform.source below hardcoded a single shared
  # version literal for both substrates. That silently pointed the ECS
  # substrate at a nonexistent ecs module directory — ecs's only real version
  # is v1.0.0. TestSubstrateVersionPinPointsAtExistingModules
  # (pkg/terragrunt/substrate_version_pin_test.go) now fails the build if any
  # substrate here resolves to a module directory that does not exist.
  substrate_module_versions = {
    ec2spot = "v1.6.0"
    ecs     = "v1.0.0"
  }
  substrate_module_version = lookup(local.substrate_module_versions, local.svc_config.locals.substrate_module, "v1.0.0")

  # Cross-account launch (Phase 126, REQ-126-LAUNCH). The compiler writes this
  # local into service.hcl only when spec.runtime.launchAccount is set on the
  # profile; try(...) defaults it to "" for every pre-126 service.hcl and for
  # the cold-clone destroy fallback's synthesized service.hcl, neither of
  # which declare this local at all.
  launch_account = try(local.svc_config.locals.launch_account, "")

  # The role-assumption stanza, rendered inline into the provider generate
  # block below via a plain string interpolation rather than an HCL
  # %{ if }/%{ endif } template control sequence. That was the first approach
  # tried; verified by direct execution against the pinned terragrunt v0.99.1
  # that %{ if }/%{ endif } markers embedded in a <<- heredoc interact with
  # the heredoc's automatic dedent-margin calculation in ways that either
  # leave stray trailing whitespace on the blank line above default_tags or
  # disable dedent for the whole heredoc, both of which break the dormant
  # byte-identity guarantee this template exists to provide. A conditional
  # local avoids the heredoc dedent machinery entirely. Empty (nothing
  # rendered at all) whenever launch_account is empty.
  assume_role_block = local.launch_account != "" ? "\n  assume_role {\n    role_arn    = \"${local.svc_config.locals.launcher_role_arn}\"\n    external_id = \"${local.svc_config.locals.launcher_external_id}\"\n  }" : ""
}

# Include root terragrunt.hcl (remote_state + provider generation)
#
# The deep-merge argument below is REQUIRED once this template declares its
# own provider-generation override further down. Under terragrunt's default
# (shallow) merge behaviour, a same-labeled generate block in both parent
# (root.hcl) and child is a hard error ("Detected generate blocks with the
# same name: [provider]"), verified by direct execution against the pinned
# terragrunt v0.99.1 -- not read from documentation. That collision is static
# and structural: it fires on every sandbox render, cross-account or not,
# dormant launch account or not, so omitting this argument breaks ALL
# sandbox creates, not only cross-account ones. This setting is configured
# per-include in the child file, so it affects only this template and none
# of the 40+ other units across the repo that also include root.hcl.
include "root" {
  path           = find_in_parent_folders("root.hcl")
  merge_strategy = "deep"
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

# AWS provider generation override (Phase 126, REQ-126-LAUNCH).
#
# Because the include above deep-merges, this block fully REPLACES root.hcl's
# same-labeled provider-generation block (it does not merge into it), so this
# contents heredoc reproduces root.hcl's entire generated output
# byte-for-byte -- the terraform{} block with its required_version and both
# required_providers pins, and the provider "aws" stanza with the identical
# region expression and default_tags root uses -- plus exactly one addition:
# a conditional role-assumption block, guarded on local.launch_account being
# non-empty. Losing any of root's original contents here would silently
# affect every sandbox, not just cross-account ones (see infra/live/root.hcl
# for the block being overridden). The region expression deliberately reads
# local.site_vars.locals.region.full (root's own expression), NOT
# local.svc_config.locals.region_full, so the dormant (no launch account)
# render is provably identical to what root alone would have generated.
# The state backend stays in the home account by design (see remote_state
# override above, untouched) -- only the provider crosses accounts.
generate "provider" {
  path      = "provider.tf"
  if_exists = "overwrite_terragrunt"

  contents = <<-EOF
    terraform {
      required_version = ">= 1.7.0"

      required_providers {
        aws = {
          source  = "hashicorp/aws"
          version = "6.46.0"
        }
        tls = {
          source  = "hashicorp/tls"
          version = "4.3.0"
        }
      }
    }

    provider "aws" {
      region = "${local.site_vars.locals.region.full}"${local.assume_role_block}

      default_tags {
        tags = {
          ManagedBy  = "Terragrunt"
          km_label   = "${local.site_vars.locals.site.label}"
        }
      }
    }
  EOF
}

# Terraform source points to the appropriate module based on substrate, at
# that substrate's own pinned version (see local.substrate_module_versions above).
terraform {
  source = "${local.repo_root}/infra/modules/${local.svc_config.locals.substrate_module}/${local.substrate_module_version}"
}

inputs = merge(
  # Common inputs for all sandboxes.
  # km_label and resource_prefix carry the same value (the operator's
  # resource_prefix) by different names — km_label is the Phase-2 name
  # used in tags, resource_prefix is the Phase-66 name used in IAM ARNs.
  # Passing both keeps the modules' two consumers happy until we converge
  # on a single name in a future cleanup. Without resource_prefix here,
  # ec2spot's IAM policies would default to "km" and deny access to any
  # operator-prefixed resource (kph-budgets, kph-sandboxes, /kph/sandbox/*).
  {
    km_label         = local.site_vars.locals.site.label
    km_random_suffix = local.site_vars.locals.site.random_suffix
    resource_prefix  = local.site_vars.locals.site.label
    region_label     = local.region_label
    region_full      = local.svc_config.locals.region_full
    sandbox_id       = local.sandbox_id
  },
  # Module-specific inputs from service.hcl
  local.svc_config.locals.module_inputs
)
