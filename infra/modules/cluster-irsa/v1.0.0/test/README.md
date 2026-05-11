# cluster-irsa module smoke-test fixture

This directory contains a minimal Terraform configuration that exercises both
trust-policy permutations of the `cluster-irsa/v1.0.0` module without touching AWS:

- **wildcard** — `namespace = "*"` → trust condition uses `StringLike`
- **literal** — `namespace = "prod"` → trust condition uses `StringEquals`

## Usage

```bash
cd infra/modules/cluster-irsa/v1.0.0/test
terraform init -backend=false
terraform validate
```

Both module instantiations must pass `terraform validate` (exit 0).

This fixture is NOT part of any production Terragrunt live stack. It exists
solely for repeatable module-level validation during CI and development.
