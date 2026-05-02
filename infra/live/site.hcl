locals {

  site = {
    label           = "km"
    tf_state_prefix = "tf-km"
    domain          = get_env("KM_DOMAIN", "klankermaker.ai")
    random_suffix   = get_env("KMGUID", "")
  }

  # Account IDs from km-config.yaml via KM_* env vars (CONF-03)
  # organization = AWS Organizations management account (SCP target); blank skips SCP deployment.
  # dns_parent   = AWS account owning the parent Route53 hosted zone for cfg.Domain DNS delegation.
  accounts = {
    organization = get_env("KM_ACCOUNTS_ORGANIZATION", "")
    dns_parent   = get_env("KM_ACCOUNTS_DNS_PARENT", "")
    terraform    = get_env("KM_ACCOUNTS_TERRAFORM", "")
    application  = get_env("KM_ACCOUNTS_APPLICATION", "")
  }

  # Secrets loaded from SOPS-encrypted file or plaintext fallback.
  # Uses a single run_cmd with shell logic to avoid HCL eager-evaluating
  # both ternary branches (which would call sops on a non-existent file).
  secret_values = jsondecode(
    run_cmd("--terragrunt-quiet", "sh", "-c",
      "if [ -f '${get_terragrunt_dir()}/.secrets.sops.json' ]; then sops --decrypt '${get_terragrunt_dir()}/.secrets.sops.json'; elif [ -f '${get_terragrunt_dir()}/.secrets.json' ]; then cat '${get_terragrunt_dir()}/.secrets.json'; else echo '{}'; fi"
    )
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
