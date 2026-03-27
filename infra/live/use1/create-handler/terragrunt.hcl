locals {
  repo_root  = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars  = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")

  # Region from region.hcl in the parent directory (e.g., infra/live/use1/region.hcl)
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
  region_full   = local.region_config.locals.region_full
  account_id    = get_aws_account_id()
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
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/create-handler/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/create-handler/v1.0.0"
}

inputs = {
  lambda_zip_path      = "${local.repo_root}/build/create-handler.zip"
  artifact_bucket_name = get_env("KM_ARTIFACTS_BUCKET", "")
  artifact_bucket_arn  = "arn:aws:s3:::${get_env("KM_ARTIFACTS_BUCKET", "")}"
  email_domain         = "sandboxes.${local.site_vars.locals.site.domain}"
  operator_email       = get_env("KM_OPERATOR_EMAIL", "")
  state_bucket         = local.site_vars.locals.backend.bucket
  state_prefix         = local.site_vars.locals.site.tf_state_prefix
  region_label         = local.region_label
  dynamodb_table_name  = local.site_vars.locals.backend.dynamodb_table
  # DynamoDB budget table ARN — table name is km-budgets (see dynamodb-budget module)
  dynamodb_budget_table_arn = "arn:aws:dynamodb:${local.region_full}:${local.account_id}:table/km-budgets"
  # email_create_handler_arn — set after deploying the email-create-handler Lambda (22-02)
  email_create_handler_arn  = get_env("KM_EMAIL_CREATE_HANDLER_ARN", "")
}
