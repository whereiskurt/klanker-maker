# Phase 09: Live Infrastructure & Operator Docs — Research

**Researched:** 2026-03-22
**Domain:** Terragrunt live infrastructure configs, Go Lambda cross-compilation, operator documentation
**Confidence:** HIGH

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| PROV-05 | Sandbox auto-destroys after TTL expires | `ttl-handler` Lambda + Terraform module exists; needs `infra/live/use1/ttl-handler/terragrunt.hcl` and compiled Lambda zip |
| BUDG-02 | DynamoDB global table stores budget limits and spend | `dynamodb-budget` Terraform module exists; needs `infra/live/use1/dynamodb-budget/terragrunt.hcl` |
| BUDG-06 | Warning email at 80% budget threshold | `budget-enforcer` Lambda + `ses` module required; budget-enforcer is per-sandbox (deployed by `km create`) |
| BUDG-07 | Dual-layer enforcement at 100% compute and AI budget | Per-sandbox `budget-enforcer` Lambda + EventBridge schedule; compiler already generates `budget_enforcer_inputs` block in service.hcl |
| MAIL-01 | SES configured globally with Route53 domain verification | `ses` Terraform module exists; needs `infra/live/use1/ses/terragrunt.hcl`; requires Route53 zone ID and artifact bucket name |
| INFR-01 | AWS multi-account setup documented | Human prerequisite (km bootstrap is a stub); needs operator guide documenting the setup procedure |
| INFR-02 | AWS SSO configured for operator access | Human prerequisite (km configure writes config but doesn't provision SSO); needs operator guide |
</phase_requirements>

---

## Summary

Phase 9 closes the last live-infrastructure and documentation gap before the v1.0 milestone. Four Terraform modules exist and are fully coded but have no Terragrunt deployment configs in `infra/live/use1/`. Three of these (`ttl-handler`, `dynamodb-budget`, `ses`) are shared, account-level services deployed once per environment. The fourth (`budget-enforcer`) is per-sandbox and deployed dynamically by `km create` — the compiler already generates `budget_enforcer_inputs` in service.hcl but no Terragrunt wrapper instantiates those inputs into the module.

The Lambda binaries (`cmd/ttl-handler/main.go` and `cmd/budget-enforcer/main.go`) are fully implemented and tested. The missing piece is: (1) cross-compile the Go Lambdas for `linux/arm64` (the module uses `provided.al2023` + `arm64`), zip them, and deploy with the Terragrunt configs, and (2) write `OPERATOR-GUIDE.md` documenting the full bootstrap procedure covering INFR-01 and INFR-02.

The existing Makefile covers sidecar build/push but has no Lambda build targets. The Makefile must be extended. The pattern from the network `terragrunt.hcl` is the reference for all four new configs — `read_terragrunt_config` + `find_in_parent_folders("CLAUDE.md")` is the established pattern for repo-root anchoring.

**Primary recommendation:** Write four `terragrunt.hcl` files in `infra/live/use1/{ttl-handler,dynamodb-budget,ses,budget-enforcer}/`, extend the Makefile with Lambda build targets, and write `OPERATOR-GUIDE.md` documenting the full first-time setup sequence.

---

## Standard Stack

### Core

| Library / Tool | Version | Purpose | Why Standard |
|----------------|---------|---------|--------------|
| Terragrunt | `>= 0.67` (already in use) | Wrap Terraform modules with DRY configs | Already in use; `infra/live/` hierarchy is established |
| Terraform / aws provider | `~> 5.0` (from root `terragrunt.hcl`) | AWS resource management | Locked in root `generate "provider"` block |
| Go | `1.25.5` (from `go.mod`) | Lambda binary compilation | Project Go version |
| `aws-lambda-go` | `v1.53.0` (from `go.mod`) | Lambda runtime for `provided.al2023` | Already a dependency; both Lambda cmds use it |
| `GOOS=linux GOARCH=arm64` | — | Cross-compile for Graviton Lambda | Modules declare `architectures = ["arm64"]`; arm64 is cheaper than x86 |

### Supporting

| Library / Tool | Version | Purpose | When to Use |
|----------------|---------|---------|-------------|
| `zip` (system) | — | Package `bootstrap` binary for Lambda deployment | Lambda `provided.al2023` runtime expects a zip with a file named `bootstrap` |
| AWS CLI | — | S3 upload of Lambda zips, ECR operations | Already used in Makefile for sidecar uploads |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Local zip file path in `var.lambda_zip_path` | S3 source for Lambda zip | Local path is simpler for operator-deployed infra; S3 source needed for CI/CD but not required for v1 |
| arm64 Lambda | x86_64 Lambda | arm64 is ~20% cheaper and already what the module declares; do not change |

**Installation / Build:**
```bash
# Add to Makefile — Lambda targets
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o build/ttl-handler-bootstrap ./cmd/ttl-handler/
zip build/ttl-handler.zip -j build/ttl-handler-bootstrap  # file inside zip must be named "bootstrap"

GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o build/budget-enforcer-bootstrap ./cmd/budget-enforcer/
zip build/budget-enforcer.zip -j build/budget-enforcer-bootstrap
```

**Key constraint:** The Go Lambda `provided.al2023` runtime requires the executable inside the zip to be named `bootstrap`. The `-j` flag strips paths; the binary must be renamed to `bootstrap` before zipping. See module `main.tf`: `handler = "bootstrap"`.

---

## Architecture Patterns

### Recommended Project Structure

```
infra/live/use1/
├── network/           # existing — deployed
├── region.hcl         # existing — region constants
├── ttl-handler/
│   └── terragrunt.hcl # NEW — shared Lambda, deployed once
├── dynamodb-budget/
│   └── terragrunt.hcl # NEW — shared table, deployed once
├── ses/
│   └── terragrunt.hcl # NEW — shared SES domain, deployed once
└── budget-enforcer/
    └── terragrunt.hcl # NEW — per-sandbox wrapper (see Per-Sandbox Pattern below)

build/
├── ttl-handler-bootstrap      # compiled Go arm64 binary
├── ttl-handler.zip            # Lambda deployment package
├── budget-enforcer-bootstrap  # compiled Go arm64 binary
└── budget-enforcer.zip        # Lambda deployment package

OPERATOR-GUIDE.md              # NEW — root-level operator setup guide
```

### Pattern 1: Shared Service `terragrunt.hcl` (network is the reference)

Copy the structure from `infra/live/use1/network/terragrunt.hcl`. Key elements:

```hcl
locals {
  repo_root     = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars     = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
  region_full   = local.region_config.locals.region_full
}

include "root" {
  path = find_in_parent_folders("terragrunt.hcl")
}

remote_state {
  backend = "s3"
  generate = { path = "backend.tf", if_exists = "overwrite_terragrunt" }
  config = {
    bucket         = local.site_vars.locals.backend.bucket
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/ttl-handler/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/ttl-handler/v1.0.0"
}

inputs = {
  artifact_bucket_name = get_env("KM_ARTIFACTS_BUCKET", "")
  artifact_bucket_arn  = "arn:aws:s3:::${get_env("KM_ARTIFACTS_BUCKET", "")}"
  email_domain         = local.site_vars.locals.site.domain  # or "sandboxes.${site.domain}"
  operator_email       = get_env("KM_OPERATOR_EMAIL", "")
  lambda_zip_path      = "${local.repo_root}/build/ttl-handler.zip"
}
```

**Source:** Derived directly from `infra/live/use1/network/terragrunt.hcl` (examined) — HIGH confidence.

### Pattern 2: `dynamodb-budget` Shared Table

Minimal inputs — the module defaults `table_name = "km-budgets"`. The Terragrunt config only needs to set tags and optionally `replica_regions`. State key: `${tf_state_prefix}/use1/dynamodb-budget/terraform.tfstate`.

```hcl
inputs = {
  table_name      = "km-budgets"
  replica_regions = []
  tags = {
    "km:component" = "budget-tracking"
    "km:managed"   = "true"
  }
}
```

### Pattern 3: `ses` Domain Identity

The SES module needs `route53_zone_id`. This is an operator-supplied value (the Route53 hosted zone for the sandboxes subdomain). Best sourced from `get_env("KM_ROUTE53_ZONE_ID", "")` — consistent with the KM_* env var pattern in `site.hcl`.

```hcl
inputs = {
  domain               = "sandboxes.${local.site_vars.locals.site.domain}"
  route53_zone_id      = get_env("KM_ROUTE53_ZONE_ID", "")
  artifact_bucket_name = get_env("KM_ARTIFACTS_BUCKET", "")
  artifact_bucket_arn  = "arn:aws:s3:::${get_env("KM_ARTIFACTS_BUCKET", "")}"
}
```

**Important:** `route53_zone_id` is not currently in `site.hcl`. The planner must decide: add it to `site.hcl` as a `get_env("KM_ROUTE53_ZONE_ID", "")` local, or reference it inline in the ses `terragrunt.hcl`. The inline approach is lower risk (no site.hcl modification required).

### Pattern 4: `budget-enforcer` Per-Sandbox Wrapper

The budget-enforcer is per-sandbox. The compiler already generates a `budget_enforcer_inputs` block in service.hcl. The terragrunt.hcl for budget-enforcer in `infra/live/use1/budget-enforcer/` is a **template reference**, not a directly deployable config. The actual per-sandbox budget-enforcer is deployed as part of `km create` — the existing sandbox `terragrunt.hcl` (in `infra/live/sandboxes/{id}/`) needs an additional `terraform` block that sources the budget-enforcer module when `budget_enforcer_inputs` is present.

**Two viable approaches (planner must choose one):**

Option A: Extend the sandbox `terragrunt.hcl` template to conditionally deploy the budget-enforcer module as a second `terraform` block. This is complex in Terragrunt (a single `terragrunt.hcl` has one `terraform` source block).

Option B: The profile compiler generates a *second* directory `infra/live/sandboxes/{id}/budget-enforcer/` alongside the main sandbox dir, with its own `terragrunt.hcl` that sources `infra/modules/budget-enforcer/v1.0.0`. The compiler already builds `budget_enforcer_inputs` — it just needs to write this second dir. `km create` runs `terragrunt apply` in both dirs sequentially.

**Recommendation:** Option B. The compiler's existing `Compile()` function produces `CompiledArtifacts` with separate files — adding a second dir output is a clean extension. `km create` already calls `runner.Apply(sandboxDir)` — extend to call `runner.Apply(budgetEnforcerDir)` when a budget is defined.

**Evidence:** `service_hcl.go` already has `budget_enforcer_inputs` block template — the data model is ready, just not written to a separate file. The sandbox `terragrunt.hcl` template uses `local.svc_config.locals.module_inputs` merge — the budget-enforcer needs its own config because it's a different Terraform source.

### Pattern 5: Lambda Build in Makefile

The existing Makefile has sidecar targets (linux/amd64). Lambda targets must use arm64 (modules declare `architectures = ["arm64"]`):

```makefile
## build-lambdas: cross-compile Go Lambda binaries for arm64 (provided.al2023 runtime)
build-lambdas:
	@mkdir -p build
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o build/ttl-handler-bootstrap ./cmd/ttl-handler/
	mv build/ttl-handler-bootstrap build/bootstrap && zip build/ttl-handler.zip -j build/bootstrap && rm build/bootstrap
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o build/budget-enforcer-bootstrap ./cmd/budget-enforcer/
	mv build/budget-enforcer-bootstrap build/bootstrap && zip build/budget-enforcer.zip -j build/bootstrap && rm build/bootstrap
```

Alternatively, use a temp dir to avoid clobbering `bootstrap` in parallel builds:
```makefile
	cd $(shell mktemp -d) && cp /path/to/binary ./bootstrap && zip $(REPO_ROOT)/build/ttl-handler.zip bootstrap
```

The cleaner approach: compile directly to `build/bootstrap`, zip, then rename to avoid temp dirs.

### Anti-Patterns to Avoid

- **Putting `KM_ROUTE53_ZONE_ID` as a default in site.hcl**: The zone ID is environment-specific — do not hardcode even as a default. Keep it env-var only.
- **Using amd64 for Lambda builds**: The module declares `arm64`. Building amd64 will cause `exec format error` at Lambda invocation.
- **Naming the Lambda binary anything other than `bootstrap`**: `provided.al2023` runtime requires the executable inside the zip to be named `bootstrap`. Using the package name as filename will silently fail.
- **Deploying budget-enforcer as a shared (non-per-sandbox) resource**: The module creates IAM roles and EventBridge schedules scoped to `var.sandbox_id`. Deploying it once without a sandbox ID will fail validation.
- **Running `terragrunt run-all` across `infra/live/` before shared services exist**: The DynamoDB table and SES domain must exist before sandbox creation. Order matters; document this in the operator guide.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Lambda deployment package | Custom archive script | `zip` with `-j` flag + named `bootstrap` | Go Lambda convention; hand-rolling creates naming bugs |
| State backend config per module | Duplicate `remote_state` blocks | `find_in_parent_folders("terragrunt.hcl")` + `include "root"` | Established pattern in every existing `terragrunt.hcl` |
| SES DNS verification records | Manual Route53 API calls | The `ses` Terraform module handles DKIM CNAMEs + TXT + MX | Module already does it; hand-rolling DNS records misses DKIM rotation |
| Operator setup validation | Shell scripts | Document `km configure` + `aws sts get-caller-identity` as verification steps | The `km configure` command already validates AWS access |

---

## Common Pitfalls

### Pitfall 1: Lambda Architecture Mismatch (arm64 vs amd64)

**What goes wrong:** Build with `GOARCH=amd64` (the Makefile sidecar default), deploy to arm64 Lambda. Lambda returns `exec format error` on every invocation. PROV-05 and BUDG-06/07 appear deployed but are silently broken.

**Why it happens:** The Makefile sidecar targets use `GOARCH=amd64` (Fargate nodes are x86). The Lambda modules declare `architectures = ["arm64"]` for Graviton. Different targets, same Makefile variable.

**How to avoid:** Lambda targets must override `GOARCH=arm64`. Do not reuse `GOARCH := amd64` from sidecar targets.

**Warning signs:** Lambda invocation logs show `fork/exec /var/task/bootstrap: exec format error`.

### Pitfall 2: Lambda zip contains path prefix (not flat `bootstrap`)

**What goes wrong:** `zip build/ttl-handler.zip build/bootstrap` (without `-j`) creates a zip with `build/bootstrap` inside. Lambda can't find the `bootstrap` entry point.

**Why it happens:** Default `zip` behavior preserves directory structure. `-j` flag strips the directory.

**How to avoid:** Always use `zip -j output.zip path/to/bootstrap` or `cd build && zip ../out.zip bootstrap`.

### Pitfall 3: `route53_zone_id` missing at ses deploy time

**What goes wrong:** Operator runs `terragrunt apply` in `infra/live/use1/ses/` without setting `KM_ROUTE53_ZONE_ID`. Terraform plan shows `route53_zone_id = ""` and fails with "invalid hosted zone ID".

**Why it happens:** The zone ID is not in `site.hcl` defaults (unlike `KM_DOMAIN` which has a fallback). There's no existing env var for Route53 zone ID.

**How to avoid:** Add `KM_ROUTE53_ZONE_ID` to the operator guide prerequisites list. Add a Terragrunt `precondition` or document the validation step.

### Pitfall 4: budget-enforcer deployed before DynamoDB table exists

**What goes wrong:** `km create` runs budget-enforcer Terraform apply before `infra/live/use1/dynamodb-budget/` has been applied. Budget Lambda starts but all DynamoDB calls fail — budget enforcement is silently inactive.

**Why it happens:** `km create` doesn't know whether shared infrastructure is deployed. No ordering dependency is enforced.

**How to avoid:** Document in OPERATOR-GUIDE.md that shared services (`dynamodb-budget`, `ttl-handler`, `ses`) must be deployed before first `km create`. Optionally, have `km create` check for the table's existence and emit a warning.

### Pitfall 5: TTL Lambda zip path hardcoded relative to operator's CWD

**What goes wrong:** `lambda_zip_path = "${local.repo_root}/build/ttl-handler.zip"` is in the terragrunt.hcl. Operator applies before running `make build-lambdas`. Terraform errors: `zip file not found`.

**Why it happens:** Terraform validates the `filename` attribute at plan time; if the file doesn't exist locally, plan fails with an unhelpful error.

**How to avoid:** Document the build step (`make build-lambdas`) as a prerequisite in OPERATOR-GUIDE.md, before `terragrunt apply`. Optionally, add a Makefile `lambdas-deploy` target that runs build then apply.

### Pitfall 6: SES not in sandbox-email-capable region

**What goes wrong:** SES module deployed to us-east-1 but operator's domain is verified in another region. SES receipt rules can only receive email in SES-inbound capable regions.

**Why it happens:** SES inbound email (receipt rules) is only supported in specific regions: us-east-1, us-west-2, eu-west-1. The `site.hcl` uses `get_env("KM_REGION", "us-east-1")` — this is safe, but if operator overrides to another region, SES inbound fails silently.

**How to avoid:** Document the supported regions in OPERATOR-GUIDE.md. The default `us-east-1` is correct.

---

## Code Examples

### terragrunt.hcl structure for a new shared service (derived from network reference)

```hcl
# infra/live/use1/ttl-handler/terragrunt.hcl
locals {
  repo_root     = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars     = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
}

include "root" {
  path = find_in_parent_folders("terragrunt.hcl")
}

remote_state {
  backend = "s3"
  generate = {
    path      = "backend.tf"
    if_exists = "overwrite_terragrunt"
  }
  config = {
    bucket         = local.site_vars.locals.backend.bucket
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/ttl-handler/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/ttl-handler/v1.0.0"
}

inputs = {
  artifact_bucket_name = get_env("KM_ARTIFACTS_BUCKET", "")
  artifact_bucket_arn  = "arn:aws:s3:::${get_env("KM_ARTIFACTS_BUCKET", "")}"
  email_domain         = "sandboxes.${local.site_vars.locals.site.domain}"
  operator_email       = get_env("KM_OPERATOR_EMAIL", "")
  lambda_zip_path      = "${local.repo_root}/build/ttl-handler.zip"
}
```
*Source: Derived from `infra/live/use1/network/terragrunt.hcl` (examined directly) — HIGH confidence.*

### Per-sandbox budget-enforcer directory from compiler

The compiler's `CompiledArtifacts` struct needs a new output path. Current struct (from `compiler.go`) writes `ServiceHCL`, `TerragruntHCL`. New field needed:

```go
// In pkg/compiler/compiler.go CompiledArtifacts
type CompiledArtifacts struct {
    ServiceHCL          string // existing
    TerragruntHCL       string // existing
    BudgetEnforcerDir   string // NEW: path to per-sandbox budget-enforcer dir (empty if no budget)
    BudgetEnforcerHCL   string // NEW: content of budget-enforcer/terragrunt.hcl
}
```

The budget-enforcer terragrunt.hcl for a sandbox reads `budget_enforcer_inputs` from the sibling service.hcl:

```hcl
# infra/live/sandboxes/{sandbox-id}/budget-enforcer/terragrunt.hcl
locals {
  repo_root  = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars  = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  svc_config = read_terragrunt_config("${get_terragrunt_dir()}/../service.hcl")
  be_inputs  = local.svc_config.locals.budget_enforcer_inputs
}

include "root" {
  path = find_in_parent_folders("terragrunt.hcl")
}

remote_state {
  backend = "s3"
  generate = { path = "backend.tf", if_exists = "overwrite_terragrunt" }
  config = {
    bucket         = local.site_vars.locals.backend.bucket
    key            = "${local.site_vars.locals.site.tf_state_prefix}/sandboxes/${local.be_inputs.sandbox_id}/budget-enforcer/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/budget-enforcer/v1.0.0"
}

inputs = merge(local.be_inputs, {
  lambda_zip_path  = "${local.repo_root}/build/budget-enforcer.zip"
  budget_table_name = "km-budgets"
  budget_table_arn  = "arn:aws:dynamodb:${local.site_vars.locals.region.full}:${local.site_vars.locals.accounts.application}:table/km-budgets"
  state_bucket      = get_env("KM_ARTIFACTS_BUCKET", "")
  email_domain      = "sandboxes.${local.site_vars.locals.site.domain}"
})
```

*Note: `local.be_inputs` from service.hcl has `sandbox_id`, `substrate`, `spot_rate`, `instance_type`, `created_at` (empty placeholder), `role_arn` (empty placeholder), `instance_id`/`task_arn`/`cluster_arn`. The `created_at`, `role_arn`, `instance_id`/`task_arn`/`cluster_arn` are populated after first apply — this is a known limitation (see Open Questions).*

### OPERATOR-GUIDE.md content areas (for planner)

The guide must cover:
1. Prerequisites: AWS account IDs (management, terraform, application), SSO start URL, domain name, Route53 zone ID
2. `km configure` invocation and what it writes
3. How to deploy shared infrastructure in order: `dynamodb-budget` → `ses` → `ttl-handler`
4. How to build Lambda binaries: `make build-lambdas`
5. How to build and push sidecars: `make ecr-push`
6. How to run first `km create` and verify
7. Verification: check DynamoDB table exists, SES domain status, TTL Lambda accessible, ECR images present

---

## Module Input Requirements (complete variable audit)

### `infra/modules/ttl-handler/v1.0.0`

| Variable | Type | Required? | Notes |
|----------|------|-----------|-------|
| `artifact_bucket_name` | string | YES | `KM_ARTIFACTS_BUCKET` env var |
| `artifact_bucket_arn` | string | YES | `arn:aws:s3:::${bucket}` |
| `email_domain` | string | NO (default: `sandboxes.klankermaker.ai`) | Derive from `site.domain` |
| `operator_email` | string | NO (default: `""`) | `KM_OPERATOR_EMAIL` env var |
| `lambda_zip_path` | string | YES | Must exist locally at apply time |

### `infra/modules/dynamodb-budget/v1.0.0`

| Variable | Type | Required? | Notes |
|----------|------|-----------|-------|
| `table_name` | string | NO (default: `km-budgets`) | Use default |
| `replica_regions` | list(string) | NO (default: `[]`) | Start with no replicas |
| `tags` | map(string) | NO (default: `{}`) | Add standard km tags |

### `infra/modules/ses/v1.0.0`

| Variable | Type | Required? | Notes |
|----------|------|-----------|-------|
| `domain` | string | YES | `sandboxes.${site.domain}` |
| `route53_zone_id` | string | YES | No default in module — `KM_ROUTE53_ZONE_ID` env var |
| `artifact_bucket_name` | string | YES | `KM_ARTIFACTS_BUCKET` env var |
| `artifact_bucket_arn` | string | YES | `arn:aws:s3:::${bucket}` |

### `infra/modules/budget-enforcer/v1.0.0`

| Variable | Type | Required? | Notes |
|----------|------|-----------|-------|
| `lambda_zip_path` | string | YES | `build/budget-enforcer.zip` |
| `budget_table_name` | string | NO (default: `km-budgets`) | Use default |
| `budget_table_arn` | string | YES | No default; construct from account/region |
| `state_bucket` | string | YES | `KM_ARTIFACTS_BUCKET` |
| `email_domain` | string | NO (default: `sandboxes.klankermaker.ai`) | Derive from `site.domain` |
| `sandbox_id` | string | YES | From `budget_enforcer_inputs` |
| `instance_type` | string | NO | From `budget_enforcer_inputs` |
| `spot_rate` | number | NO (default: `0.0`) | From `budget_enforcer_inputs` |
| `substrate` | string | YES | `"ec2"` or `"ecs"` |
| `created_at` | string | YES | RFC3339; must be non-empty for cost calc |
| `role_arn` | string | YES | IAM role ARN for Bedrock revocation |
| `instance_id` | string | NO | EC2 instance ID |
| `task_arn` | string | NO | ECS task ARN |
| `cluster_arn` | string | NO | ECS cluster ARN |
| `operator_email` | string | NO | `KM_OPERATOR_EMAIL` |

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Terraform workspace for isolation | Per-sandbox directory with unique state key | Phase 1 | Each sandbox has its own tfstate; no workspace sharing |
| Lambda x86_64 | Lambda arm64 (Graviton) | Phase 4 (TTL) / Phase 6 (budget) | ~20% cheaper; arm64 binary required |
| Manual DNS for SES | Terraform `aws_route53_record` in ses module | Phase 4 | DKIM rotation + verification automated |

---

## Open Questions

1. **`created_at`, `role_arn`, `instance_id`/`task_arn` in budget-enforcer module inputs**
   - What we know: `budget_enforcer_inputs` in service.hcl has placeholders `created_at = ""` and `role_arn = ""` because these values are only known *after* the sandbox Terraform apply (EC2 instance ID comes from module output).
   - What's unclear: How does `km create` populate these fields in the EventBridge payload? The compiler writes the HCL at pre-apply time; post-apply it would need to read Terragrunt outputs and update the schedule payload.
   - Recommendation: The budget-enforcer terragrunt.hcl should be generated after the main sandbox apply completes. `km create` must read Terraform outputs from the ec2spot/ecs module (instance ID, role ARN), then write the budget-enforcer dir with populated values, then apply it. This is a **compiler and create-command concern** — the planner should scope a plan to wire this output-reading step.

2. **Route53 zone ID source**
   - What we know: The ses module requires `route53_zone_id`. No existing env var or site.hcl local covers this.
   - What's unclear: Should `KM_ROUTE53_ZONE_ID` be added to `site.hcl` as a new `accounts`-style entry, or referenced only in the ses terragrunt.hcl?
   - Recommendation: Add to `site.hcl` as `route53_zone_id = get_env("KM_ROUTE53_ZONE_ID", "")` — consistent with the existing accounts pattern. Propagate via `local.site_vars.locals.route53_zone_id` in the ses config.

3. **Operator guide scope: INFR-01 and INFR-02 are "human prerequisites"**
   - What we know: From the audit: `km bootstrap` is a dry-run stub. AWS SSO provisioning (`km configure`) writes config but doesn't create SSO assignments.
   - What's unclear: Should Phase 9 add a `km bootstrap` implementation or just document the manual steps?
   - Recommendation: Document the manual steps in OPERATOR-GUIDE.md. Implementing `km bootstrap` is a larger scope and not needed to satisfy INFR-01/02 — the requirement says "AWS multi-account setup" which is inherently a human action; the documentation satisfies the requirement.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go test (`go test ./...`) |
| Config file | None (standard Go toolchain) |
| Quick run command | `go test ./cmd/ttl-handler/... ./cmd/budget-enforcer/... -count=1` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| PROV-05 | TTL Lambda processes sandbox_id, uploads artifacts, sends notification, deletes schedule | unit | `go test ./cmd/ttl-handler/... -count=1` | `cmd/ttl-handler/main_test.go` exists |
| BUDG-02 | DynamoDB table created with correct schema (PK/SK, stream, TTL) | infra-smoke | Manual `terragrunt plan` inspection | `infra/live/use1/dynamodb-budget/terragrunt.hcl` (Wave 0 gap) |
| BUDG-06 | Warning email sent at 80% threshold (once-only) | unit | `go test ./cmd/budget-enforcer/... -count=1` | `cmd/budget-enforcer/main_test.go` exists |
| BUDG-07 | EC2 StopInstances / ECS StopTask / IAM Bedrock detach at 100% | unit | `go test ./cmd/budget-enforcer/... -count=1` | `cmd/budget-enforcer/main_test.go` exists |
| MAIL-01 | SES domain + DKIM + MX records configured | infra-smoke | Manual `terragrunt plan` inspection | `infra/live/use1/ses/terragrunt.hcl` (Wave 0 gap) |
| INFR-01 | OPERATOR-GUIDE.md covers AWS multi-account setup | doc-review | Manual review | `OPERATOR-GUIDE.md` (Wave 0 gap) |
| INFR-02 | OPERATOR-GUIDE.md covers SSO configuration | doc-review | Manual review | `OPERATOR-GUIDE.md` (Wave 0 gap) |

### Sampling Rate

- **Per task commit:** `go test ./cmd/ttl-handler/... ./cmd/budget-enforcer/... -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green + `terragrunt validate` on all new configs before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `infra/live/use1/ttl-handler/terragrunt.hcl` — covers PROV-05 infrastructure
- [ ] `infra/live/use1/dynamodb-budget/terragrunt.hcl` — covers BUDG-02 infrastructure
- [ ] `infra/live/use1/ses/terragrunt.hcl` — covers MAIL-01 infrastructure
- [ ] `infra/live/use1/budget-enforcer/terragrunt.hcl` — reference template for per-sandbox budget enforcer
- [ ] `OPERATOR-GUIDE.md` — covers INFR-01, INFR-02
- [ ] Makefile `build-lambdas` target — prerequisite for TTL + budget-enforcer deployment

---

## Sources

### Primary (HIGH confidence)

- Direct file examination: `infra/live/use1/network/terragrunt.hcl` — terragrunt pattern reference
- Direct file examination: `infra/live/site.hcl` — site-level locals and env var pattern
- Direct file examination: `infra/live/terragrunt.hcl` — root config with provider generation
- Direct file examination: `infra/modules/ttl-handler/v1.0.0/{main,variables}.tf` — module inputs
- Direct file examination: `infra/modules/dynamodb-budget/v1.0.0/{main,variables}.tf` — module inputs
- Direct file examination: `infra/modules/ses/v1.0.0/{main,variables}.tf` — module inputs
- Direct file examination: `infra/modules/budget-enforcer/v1.0.0/{main,variables}.tf` — module inputs
- Direct file examination: `cmd/ttl-handler/main.go` + `cmd/budget-enforcer/main.go` — Lambda entrypoints, runtime env vars
- Direct file examination: `pkg/compiler/service_hcl.go` — `budget_enforcer_inputs` template structure
- Direct file examination: `Makefile` — existing build patterns and GOARCH/GOOS variables
- Direct file examination: `go.mod` — Go version, Lambda dependency version

### Secondary (MEDIUM confidence)

- `v1.0-MILESTONE-AUDIT.md` — confirms four missing live configs, names the gaps explicitly
- `STATE.md` accumulated decisions — confirms `provided.al2023 + arm64` pattern for Lambda, `PLACEHOLDER_ECR` pattern

### Tertiary (LOW confidence)

- None.

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all modules and existing patterns examined directly
- Architecture: HIGH — derived from existing terragrunt.hcl files in the repo
- Pitfalls: HIGH — arm64/amd64 mismatch and zip naming are directly observable from module source
- Open questions: MEDIUM — `created_at`/`role_arn` population timing requires tracing km create flow

**Research date:** 2026-03-22
**Valid until:** 2026-06-22 (stable Terragrunt/Terraform patterns; unlikely to change significantly)
