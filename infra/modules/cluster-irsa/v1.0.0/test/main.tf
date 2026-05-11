# Smoke-test fixture for infra/modules/cluster-irsa/v1.0.0
#
# Not part of production stacks. Run terraform init -backend=false and terraform validate
# in this directory to confirm both wildcard and literal sub_condition instantiations
# resolve without errors.
#
# Wildcard: namespace="*" triggers StringLike on the sub trust condition.
# Literal:  namespace="prod" triggers StringEquals on the sub trust condition.

module "cluster_irsa_wildcard" {
  source = "./.."

  cluster_name              = "test-wild"
  oidc_provider_arn         = "arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE"
  namespace                 = "*"
  service_account_name      = "km"
  resource_prefix           = "km"
  state_bucket              = "fake-state"
  artifact_bucket_arn       = "arn:aws:s3:::fake-artifacts"
  dynamodb_table_name       = "fake-locks"
  dynamodb_budget_table_arn = "arn:aws:dynamodb:us-east-1:123456789012:table/fake-budgets"
  sandbox_table_name        = "fake-sandboxes"
  identities_table_name     = "fake-identities"
}

module "cluster_irsa_literal" {
  source = "./.."

  cluster_name              = "test-lit"
  oidc_provider_arn         = "arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLE"
  namespace                 = "prod"
  service_account_name      = "km"
  resource_prefix           = "km"
  state_bucket              = "fake-state"
  artifact_bucket_arn       = "arn:aws:s3:::fake-artifacts"
  dynamodb_table_name       = "fake-locks"
  dynamodb_budget_table_arn = "arn:aws:dynamodb:us-east-1:123456789012:table/fake-budgets"
  sandbox_table_name        = "fake-sandboxes"
  identities_table_name     = "fake-identities"
}

output "wildcard_role_arn" {
  value = module.cluster_irsa_wildcard.role_arn
}

output "literal_role_arn" {
  value = module.cluster_irsa_literal.role_arn
}
