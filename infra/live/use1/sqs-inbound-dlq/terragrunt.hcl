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
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/sqs-inbound-dlq/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/sqs-inbound-dlq/v1.2.0"
}

inputs = {
  github_dlq_name = "${local.site_vars.locals.site.label}-github-inbound-dlq.fifo"
  slack_dlq_name  = "${local.site_vars.locals.site.label}-slack-inbound-dlq.fifo"
  # Phase 127: shared generic-webhook inbound FIFO DLQ. Matches
  # pkg/aws.WebhookInboundDLQName(prefix), which internal/app/cmd/create_webhook_inbound.go
  # derives deterministically via pkg/aws.DLQArn — no terragrunt dependency needed on
  # this module from lambda-webhook-bridge (same pattern as github/slack/h1).
  webhook_dlq_name = "${local.site_vars.locals.site.label}-webhook-inbound-dlq.fifo"
  # v1.2.0 (2026-09-19): shared HackerOne inbound FIFO DLQ. Matches
  # pkg/aws.H1InboundDLQName(prefix) exactly — create_h1_inbound.go derives the
  # RedrivePolicy ARN from that name, and SQS rejects the per-sandbox queue at
  # CreateQueue (after the EC2 apply) if the target does not exist.
  h1_dlq_name = "${local.site_vars.locals.site.label}-h1-inbound-dlq.fifo"
  tags = {
    "km:component" = "km-inbound-dlq"
    "km:managed"   = "true"
  }
}
