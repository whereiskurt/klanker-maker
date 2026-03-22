locals {

  site = {
    label           = "km"
    tf_state_prefix = "tf-km"
    domain          = get_env("KM_DOMAIN", "klankermaker.ai")
    random_suffix   = get_env("KMGUID", "")
  }

  # Account IDs from km-config.yaml via KM_* env vars (CONF-03)
  accounts = {
    management  = get_env("KM_ACCOUNTS_MANAGEMENT", "")
    terraform   = get_env("KM_ACCOUNTS_TERRAFORM", "")
    application = get_env("KM_ACCOUNTS_APPLICATION", "")
  }

  # Secrets loaded from SOPS-encrypted file or plaintext fallback
  # SOPS decrypt happens on-the-fly; plaintext file is for local development only
  secret_values = jsondecode(
    fileexists("${get_terragrunt_dir()}/.secrets.sops.json")
    ? run_cmd("--terragrunt-quiet", "sops", "--decrypt", "${get_terragrunt_dir()}/.secrets.sops.json")
    : fileexists("${get_terragrunt_dir()}/.secrets.json")
    ? file("${get_terragrunt_dir()}/.secrets.json")
    : "{}"
  )

  # Sandbox configuration — Phase 2 compiler will populate per-sandbox values
  sandbox = {
    # Populated by the profile compiler; placeholder here for pattern reference
    id = get_env("KM_SANDBOX_ID", "template")
  }

  region = {
    label = get_env("KM_REGION_LABEL", "use1")
    full  = get_env("KM_REGION", "us-east-1")
  }

  # S3 + DynamoDB backend configuration for Terraform state
  backend = {
    bucket         = "${local.site.tf_state_prefix}-state-${local.region.label}"
    dynamodb_table = "${local.site.tf_state_prefix}-locks-${local.region.label}"
    region         = local.region.full
    encrypt        = true
  }

  # KMS key alias for SOPS encryption
  kms_alias = "alias/km-sops"

  # Module base path relative to repo root
  module_base = "${dirname(find_in_parent_folders("CLAUDE.md"))}/infra/modules"

}
