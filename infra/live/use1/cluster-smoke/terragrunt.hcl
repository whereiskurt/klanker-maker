locals {
  repo_root     = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars     = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
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
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/cluster-smoke/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  # Use // so Terragrunt copies infra/modules/ into the cache (not just cluster-irsa/v1.0.0),
  # making the sibling km-operator-policy/v1.0.0/ module resolvable via the relative path
  # "../../km-operator-policy/v1.0.0" in cluster-irsa/v1.0.0/main.tf.
  source = "${local.repo_root}/infra/modules//cluster-irsa/v1.0.0"
}

inputs = {
  cluster_name              = "smoke"
  oidc_provider_arn         = "arn:aws:iam::123456789012:oidc-provider/fake.example.com"
  namespace                 = "*"
  service_account_name      = "km"
  resource_prefix           = local.site_vars.locals.site.label
  state_bucket              = local.site_vars.locals.backend.bucket
  artifact_bucket_arn       = "arn:aws:s3:::${local.site_vars.locals.backend.bucket}"
  dynamodb_table_name       = local.site_vars.locals.backend.dynamodb_table
  dynamodb_budget_table_arn = "arn:aws:dynamodb:${local.region_config.locals.region_full}:${local.account_id}:table/${local.site_vars.locals.site.label}-budgets"
  sandbox_table_name        = "${local.site_vars.locals.site.label}-sandboxes"
  identities_table_name     = "${local.site_vars.locals.site.label}-identities"
}
