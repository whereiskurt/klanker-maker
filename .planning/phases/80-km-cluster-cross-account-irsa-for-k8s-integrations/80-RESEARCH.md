# Phase 80: km cluster — Cross-Account IRSA for k8s Integrations — Research

**Researched:** 2026-05-11
**Domain:** Terraform IAM module extraction + Terragrunt HCL generation + Go Cobra subcommand
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- **Single phase 80.** No 80/81 split. Module extract + new module + CLI + config + integration test all ship together.
- **Extract `km-operator-policy/v1.0.0/` as a shared module.** Do NOT duplicate create-handler's policy set into cluster-irsa. Both `create-handler` and `cluster-irsa` consume the shared module.
- **Refactor of `create-handler` must produce zero net IAM JSON change** — verified via `terragrunt plan` in `infra/live/use1/create-handler/` showing no `aws_iam_role_policy` replacements/destroys.
- **Full `km cluster add --dry-run=false` against `klanker-application` profile** as the phase-close integration test. Sequence: dry-run plan → apply → `aws iam get-role` proves role exists → `km cluster list` shows it → `km cluster rm --dry-run=false` destroys → role gone from IAM + entry gone from `km-config.yaml`. No k8s pod assume-role test required at phase close.

### CLI Flags on `km cluster add`
| Flag | Default | Required |
|---|---|---|
| `--name` | (none) | yes |
| `--oidc-provider-arn` | (none) | yes |
| `--namespace` | `*` | no |
| `--service-account` | `km` | no |
| `--aws-profile` | `klanker-application` | no |
| `--region` | `us-east-1` | no |
| `--verbose` | `false` | no |
| `--dry-run` | `true` | no |

### CLI Behavior Contracts
- **Idempotency:** if a cluster with the same `--name` exists in `km-config.yaml`, print the existing role ARN and exit 0 without re-applying.
- **Pre-flight validation:** call `sts.GetCallerIdentity` before any Terragrunt invocation (pattern from `cmd/init.go:310-316`).
- **AWS profile resolution:** must call `ExportConfigEnvVars(cfg)` before invoking `terragrunt` (memory: non-default `resource_prefix` installs hit 403 HeadBucket otherwise).
- **Output capture:** after apply, run `terragrunt output -json` in the stack directory, extract `role_arn` from the JSON map. Persist to `km-config.yaml` using a new `persistKMConfigFieldsWithSlice` variant (see Architecture Patterns section).
- **Rollback on persistence failure:** if `terragrunt apply` succeeds but `km-config.yaml` write fails, leave the IAM role in place and emit a clear error pointing the operator at `km cluster rm` cleanup. Do NOT attempt to undo the apply automatically.

### Config Schema (LOCKED)
```go
type ClusterConfig struct {
    Name             string `mapstructure:"name"               yaml:"name"`
    OIDCProviderARN  string `mapstructure:"oidc_provider_arn"  yaml:"oidc_provider_arn"`
    Namespace        string `mapstructure:"namespace"          yaml:"namespace"`
    ServiceAccount   string `mapstructure:"service_account"    yaml:"service_account"`
    RoleARN          string `mapstructure:"role_arn"           yaml:"role_arn"`
}
// Config gains: Clusters []ClusterConfig `mapstructure:"clusters" yaml:"clusters"`
```
Absent `clusters:` key = empty slice; no migration needed for existing installs.

### Trust Policy Details (LOCKED)
- `Principal.Federated` references the OIDC provider ARN in the cluster's AWS account (NOT klanker's `data.aws_caller_identity`).
- Strip `arn:aws:iam::<account>:oidc-provider/` prefix to derive condition variable key.
- `aud` condition: always `StringEquals "sts.amazonaws.com"`.
- `sub` condition: `StringLike` when `--namespace` or `--service-account` contains `*`, else `StringEquals`.
- `--namespace` default: `*`. `--service-account` default: `km`.

### IAM Role Naming (LOCKED)
- Role name: `{resource_prefix}-cluster-{cluster_name}`.
- Terraform state key: `${tf_state_prefix}/${region_label}/cluster-{cluster_name}/terraform.tfstate`.
- Terragrunt stack directory: `infra/live/{region-label}/cluster-{name}/`.

### Claude's Discretion
- Internal helper organization: copy the structure of `internal/app/cmd/init.go`.
- Test layout: mirror existing `cmd/*_test.go` conventions (table-driven, `t.Run` subtests).
- Error message phrasing: follow tone of existing `km` errors (no emoji, concrete next-action hint).
- Terragrunt template embedding: `embed.FS` vs. raw string constant — pick whichever matches existing pattern. **Research finding: existing codebase uses raw `fmt.Sprintf` string for region.hcl and `const` raw string templates for budget_enforcer_hcl.go. Use raw string constant for cluster terragrunt.hcl template.**
- Pre-existing data sources in trust policy: drop `data "aws_caller_identity" "current"` unless needed.
- Region label derivation: use `compiler.RegionLabel()` — confirmed in `pkg/compiler/region.go`.
- `km cluster list` output format: use `text/tabwriter` as in `ami.go:348`.
- Refactor mechanism for `create-handler`: see Architecture Patterns — Option B (module accepts `role_id`) is recommended.

### Deferred Ideas (OUT OF SCOPE)
- `km doctor` checks (`cluster_irsa_trust_healthy`, `cluster_irsa_stale_roles`).
- `km cluster manifest` subcommand.
- Multiple service-account/namespace pairs per role.
- K8s pod-side smoke test in `km doctor`.
- Custom inline policy hooks.
- Cross-region role provisioning beyond single per-region stack.
- Multi-account cluster trust.
</user_constraints>

---

## Summary

Phase 80 adds cross-account IRSA support to klanker-maker so a persistent k8s service in the Greenhouse EKS account (`874364631781`) can invoke `km` commands against klanker resources in the klanker account (`850919910932`) without static IAM user keys.

The implementation has three interlocking pieces: (1) extract the 14 inline IAM policy resources from `infra/modules/create-handler/v1.0.0/main.tf` into a shared `km-operator-policy/v1.0.0/` module, refactor create-handler to consume it with zero net IAM diff; (2) create a new `cluster-irsa/v1.0.0/` module that provisions the cross-account trust role consuming the same shared policy module; (3) add a `km cluster add|list|rm` Cobra command tree in `internal/app/cmd/cluster.go` that generates a per-cluster `terragrunt.hcl`, runs apply/destroy, and persists cluster metadata to `km-config.yaml`.

All patterns — runner construction, credential pre-flight, HCL generation, config persistence, tabwriter output — have high-confidence verified implementations in the existing codebase. The one new problem is persisting a `[]ClusterConfig` slice to `km-config.yaml`; the existing `persistKMConfigFields(map[string]string)` helper does not handle slice types and must be extended.

**Primary recommendation:** Use Option B (shared module accepts `role_id` input and creates `aws_iam_role_policy` resources internally) for the km-operator-policy module — this is the cleanest refactor producing zero net diff.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/spf13/cobra` | existing | CLI command tree | All km commands use it |
| `github.com/aws/aws-sdk-go-v2` | existing | AWS SDK (sts, iam) | Used throughout |
| `gopkg.in/yaml.v3` | existing | km-config.yaml R/W | Used by `persistKMConfigFields` |
| `text/tabwriter` | stdlib | `km cluster list` output | Used by ami.go:348, at.go:623, email.go:582 |
| `text/template` | stdlib | HCL content generation | NOT needed — use raw string const |
| Terraform/Terragrunt | existing | IaC module application | All infra modules |

### No New Dependencies
Phase 80 requires zero new Go module dependencies. All packages needed are already in `go.mod`.

### Installation
```bash
# No new packages — all existing
make build    # Always required after CLI edits (ldflags version)
```

---

## Architecture Patterns

### Recommended Project Structure

New files:
```
infra/modules/km-operator-policy/v1.0.0/
├── main.tf        # 14 aws_iam_role_policy resources + data.aws_caller_identity
├── variables.tf   # role_id, resource_prefix, artifact_bucket_arn, state_bucket,
│                  # dynamodb_table_name, dynamodb_budget_table_arn, sandbox_table_name,
│                  # identities_table_name
└── outputs.tf     # (empty or internal — no outputs needed; role_id passed in)

infra/modules/cluster-irsa/v1.0.0/
├── main.tf        # aws_iam_role.cluster_irsa + trust policy + module.km_operator_policy
├── variables.tf   # cluster_name, oidc_provider_arn, namespace, service_account_name,
│                  # resource_prefix, state_bucket, artifact_bucket_arn,
│                  # dynamodb_table_name, dynamodb_budget_table_arn,
│                  # sandbox_table_name, identities_table_name
└── outputs.tf     # role_arn, role_name

internal/app/cmd/cluster.go    # NewClusterCmd + add/list/rm subcommands
internal/app/cmd/cluster_test.go
```

Modified files:
```
infra/modules/create-handler/v1.0.0/main.tf   # replace 14 aws_iam_role_policy blocks with module call
internal/app/config/config.go                  # ClusterConfig struct + Clusters field + viper key
internal/app/cmd/root.go                       # root.AddCommand(NewClusterCmd(cfg))
```

Generated at runtime:
```
infra/live/{region-label}/cluster-{name}/terragrunt.hcl
km-config.yaml (clusters: list modified)
```

### Pattern 1: Policy Module Refactor — Option B (RECOMMENDED)

**What:** Shared module accepts `role_id` as input; creates all 14 `aws_iam_role_policy` resources internally.

**Why Option B over Option A (map of JSON strings):**
- **Zero net diff guaranteed.** When create-handler switches from inline `aws_iam_role_policy` resources to `module.km_operator_policy.aws_iam_role_policy.*`, Terraform sees resource address changes (`aws_iam_role_policy.s3_artifacts` → `module.km_operator_policy.aws_iam_role_policy.s3_artifacts`). To get zero diff, both old and new addresses must resolve to the same physical resource. This is achievable by using `moved` blocks in create-handler's main.tf (Terraform 1.1+). With Option A, the caller creates policy resources with new addresses and the old ones are destroyed — this CANNOT be zero-diff without moved blocks regardless of approach.
- **Option B is simpler to maintain.** The 14 policies live once, in the shared module. Adding a new policy in the future is a single-file change.
- **Precedent:** Terraform's standard pattern for reusable IAM role policy bundles is "module accepts role ARN/ID, creates policies internally." This is the aws_irsa_role_lotus pattern in the Greenhouse repo.

**Implementation for zero-diff in create-handler:**
```hcl
# infra/modules/km-operator-policy/v1.0.0/variables.tf
variable "role_id" {
  type        = string
  description = "The ID of the IAM role to attach policies to."
}
variable "resource_prefix" { type = string }
variable "artifact_bucket_arn" { type = string }
variable "state_bucket" { type = string }
variable "dynamodb_table_name" { type = string }
variable "dynamodb_budget_table_arn" { type = string }
variable "sandbox_table_name" { type = string }
variable "identities_table_name" { type = string }
```

```hcl
# infra/modules/create-handler/v1.0.0/main.tf (after refactor)
module "km_operator_policy" {
  source = "../../km-operator-policy/v1.0.0"
  role_id                   = aws_iam_role.create_handler.id
  resource_prefix           = var.resource_prefix
  artifact_bucket_arn       = var.artifact_bucket_arn
  state_bucket              = var.state_bucket
  dynamodb_table_name       = var.dynamodb_table_name
  dynamodb_budget_table_arn = var.dynamodb_budget_table_arn
  sandbox_table_name        = var.sandbox_table_name
  identities_table_name     = var.identities_table_name
}
```

```hcl
# Required moved blocks in create-handler/v1.0.0/main.tf to achieve zero diff
# (one for each of the 14 policies)
moved {
  from = aws_iam_role_policy.s3_artifacts
  to   = module.km_operator_policy.aws_iam_role_policy.s3_artifacts
}
moved {
  from = aws_iam_role_policy.dynamodb
  to   = module.km_operator_policy.aws_iam_role_policy.dynamodb
}
# ... 12 more moved blocks
```

**CRITICAL:** The `moved` blocks must remain in `create-handler/v1.0.0/main.tf` permanently (until all operators have run `terragrunt apply` once). They can be removed in a future v1.1.0 version bump.

### Pattern 2: Terragrunt HCL Generation (raw string constant)

**What:** `cluster.go` generates `terragrunt.hcl` content using a raw string constant with `strings.NewReplacer` substitution (NOT `text/template`).

**Why:** `init.go:656` generates `region.hcl` using `fmt.Sprintf` on a raw string. `budget_enforcer_hcl.go` uses a `const` raw string with `text/template`. Both patterns are valid. For cluster HCL, `strings.NewReplacer` with named placeholders (e.g., `{CLUSTER_NAME}`) is the simplest and most readable — no template parsing overhead, easier to audit.

```go
// Source: pattern from internal/app/cmd/init.go:656
const clusterTerragruntHCLTemplate = `locals {
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
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/cluster-{CLUSTER_NAME}/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/cluster-irsa/v1.0.0"
}

inputs = {
  cluster_name         = "{CLUSTER_NAME}"
  oidc_provider_arn    = "{OIDC_PROVIDER_ARN}"
  namespace            = "{NAMESPACE}"
  service_account_name = "{SERVICE_ACCOUNT_NAME}"
  resource_prefix      = local.site_vars.locals.site.label
  state_bucket         = local.site_vars.locals.backend.bucket
  artifact_bucket_arn  = "arn:aws:s3:::${local.site_vars.locals.backend.bucket}"
  dynamodb_table_name  = local.site_vars.locals.backend.dynamodb_table
  dynamodb_budget_table_arn = "arn:aws:dynamodb:${local.region_config.locals.region_full}:${local.account_id}:table/${local.site_vars.locals.site.label}-budgets"
  sandbox_table_name   = "${local.site_vars.locals.site.label}-sandboxes"
  identities_table_name = "${local.site_vars.locals.site.label}-identities"
}
`

func generateClusterHCL(clusterName, oidcProviderARN, namespace, serviceAccount string) string {
    r := strings.NewReplacer(
        "{CLUSTER_NAME}", clusterName,
        "{OIDC_PROVIDER_ARN}", oidcProviderARN,
        "{NAMESPACE}", namespace,
        "{SERVICE_ACCOUNT_NAME}", serviceAccount,
    )
    return r.Replace(clusterTerragruntHCLTemplate)
}
```

**NOTE:** The HCL template uses `${...}` for HCL interpolation, not Go template interpolation. `strings.NewReplacer` with `{PLACEHOLDER}` braces avoids collision with HCL `${...}` syntax.

**IMPORTANT:** The cluster stack directory is NOT under `infra/live/use1/sandboxes/` — it lives at `infra/live/{region-label}/cluster-{name}/`. This means no `region.hcl` file will exist in the parent directory unless `km init` has previously been run for that region. The HCL template reads `region.hcl` from `get_terragrunt_dir()/../region.hcl`. The `km cluster add` command must verify region.hcl exists (or write it) before running terragrunt. See Common Pitfalls section.

### Pattern 3: Credential Pre-Flight (verified from init.go:310-316)

```go
// Source: internal/app/cmd/init.go:310-316
func runClusterAdd(cfg *config.Config, opts clusterAddOpts) error {
    ctx := context.Background()

    awsCfg, err := awspkg.LoadAWSConfig(ctx, opts.awsProfile)
    if err != nil {
        return fmt.Errorf("failed to load AWS config (profile=%s): %w", opts.awsProfile, err)
    }
    if err := awspkg.ValidateCredentials(ctx, awsCfg); err != nil {
        return fmt.Errorf("AWS credential validation failed: %w", err)
    }

    // Export config env vars BEFORE any terragrunt invocation
    ExportConfigEnvVars(cfg)
    // ...
}
```

`LoadAWSConfig` (in `pkg/aws/client.go:23-40`) takes an empty string profile to fall through to environment-variable credential provider — which is what the k8s pod-side `km` binary needs (`KM_AWS_PROFILE=""` in the service config).

### Pattern 4: Output Extraction After Apply (verified from init.go:761-772)

The `runner.Output()` method returns `map[string]interface{}` where each value is a map `{"value": <actual>, "type": <type>}`. Use `extractValue()` to unwrap:

```go
// Source: internal/app/cmd/init.go:805-812 (extractValue helper)
// Source: internal/app/cmd/init.go:761-772 (usage pattern after create-handler apply)
outputMap, err := runner.Output(ctx, stackDir)
if err != nil {
    return fmt.Errorf("getting cluster outputs: %w", err)
}
roleARN := ""
if v, ok := outputMap["role_arn"]; ok {
    roleARN = fmt.Sprintf("%v", extractValue(v))
}
```

`extractValue` is defined in `init.go:805` — it is package-private. `cluster.go` is in the same `cmd` package so it can call `extractValue` directly.

### Pattern 5: Config Persistence — Cluster List (NEW requirement)

The existing `persistKMConfigFields(map[string]string)` helper only handles top-level string fields. Persisting `clusters: []ClusterConfig` requires a different approach because the value is a slice of structs.

**Solution:** Write a new `persistClustersConfig` helper in `cluster.go` that follows the same read-unmarshal-modify-marshal-write pattern as `persistKMConfigFields` but handles the `clusters` key as `[]interface{}`:

```go
// Pattern from persistKMConfigFields (init.go:1723-1751) — same approach, different value type
func persistClustersConfig(clusters []config.ClusterConfig) error {
    configPath := filepath.Join(findRepoRoot(), "km-config.yaml")
    data, err := os.ReadFile(configPath)
    if err != nil {
        return err
    }
    var raw map[string]interface{}
    if err := yaml.Unmarshal(data, &raw); err != nil {
        return err
    }
    if raw == nil {
        raw = make(map[string]interface{})
    }
    raw["clusters"] = clusters  // yaml.Marshal handles []ClusterConfig correctly
    newData, err := yaml.Marshal(raw)
    if err != nil {
        return err
    }
    header := "# km-config.yaml — generated by km configure\n# Add this file to .gitignore\n\n"
    return os.WriteFile(configPath, append([]byte(header), newData...), 0600)
}
```

**Also required:** Add `"clusters"` to the viper merge key list in `config.go:Load()` so the field is loaded from km-config.yaml on subsequent runs:
```go
// internal/app/config/config.go:Load() — merge key list around line 259
"clusters",
```
And add `v.SetDefault("clusters", []interface{}{})` near line 196.

Then populate in the `cfg := &Config{...}` block via `v.UnmarshalKey("clusters", &cfg.Clusters)`.

### Pattern 6: Cobra Parent-With-Subcommands (from slack.go and ami.go)

All parent commands with multiple subcommands use the same pattern:

```go
// Source: internal/app/cmd/slack.go:110-132 / ami.go:33-52
func NewClusterCmd(cfg *config.Config) *cobra.Command {
    clusterCmd := &cobra.Command{
        Use:          "cluster",
        Short:        "Manage cross-account IRSA roles for k8s integrations",
        SilenceUsage: true,
    }
    clusterCmd.AddCommand(newClusterAddCmd(cfg))
    clusterCmd.AddCommand(newClusterListCmd(cfg))
    clusterCmd.AddCommand(newClusterRmCmd(cfg))
    return clusterCmd
}
```

All subcommands in one file (`cluster.go`) — NOT split into separate files. This matches `slack.go` (5 subcommands in one file) and `ami.go` (4 subcommands in one file). The convention is one file per parent command group.

### Pattern 7: Terragrunt Runner Construction (from init.go:469-474)

```go
// Source: internal/app/cmd/init.go:469-474
runner := terragrunt.NewRunner(opts.awsProfile, repoRoot)
runner.Verbose = opts.verbose
if err := runner.Apply(ctx, stackDir); err != nil {
    return fmt.Errorf("cluster apply failed: %w", err)
}
```

For `km cluster rm`, use `runner.Destroy(ctx, stackDir)`. No need for `DestroyWithStderr` variant since there are no lock detection requirements for cluster stacks.

### Pattern 8: `km cluster list` Table Output (from ami.go:348)

```go
// Source: internal/app/cmd/ami.go:348
tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
fmt.Fprintln(tw, "NAME\tNAMESPACE\tSERVICE ACCOUNT\tROLE ARN")
for _, c := range cfg.Clusters {
    fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Name, c.Namespace, c.ServiceAccount, c.RoleARN)
}
tw.Flush()
```

### Anti-Patterns to Avoid
- **Do NOT duplicate the 14 policies into cluster-irsa.** Violates the locked decision; creates silent policy drift.
- **Do NOT use `aws_iam_managed_policy` resources.** The existing pattern uses `aws_iam_role_policy` (inline policies attached directly to roles); stick to this pattern for consistency.
- **Do NOT call `strings.Replace` on HCL with `${...}` interpolation.** The cluster HCL template uses `${local.foo}` HCL syntax. Use `{PLACEHOLDER}` delimiters (with curly braces but no dollar sign) for Go-substituted values to avoid collision.
- **Do NOT omit `moved` blocks in create-handler refactor.** Without them, `terragrunt plan` shows 14 destroys + 14 creates — not zero diff. This violates the locked decision and will interrupt the production create-handler Lambda role.
- **Do NOT call `runner.Apply` before `ExportConfigEnvVars(cfg)`.** Non-default `resource_prefix` installs hit 403 HeadBucket on the state bucket (documented in project memory `project_terragrunt_env_export.md`).

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| AWS credential loading | Custom profile resolver | `awspkg.LoadAWSConfig(ctx, profile)` in `pkg/aws/client.go:23` | Handles SSO, fallback to env, region default |
| Credential validation | Custom STS call | `awspkg.ValidateCredentials(ctx, awsCfg)` in `pkg/aws/client.go:44` | Wraps GetCallerIdentity with error formatting |
| Terragrunt execution | `exec.Command("terragrunt", ...)` | `terragrunt.NewRunner(profile, repoRoot)` | Handles verbose/quiet, env injection, TG_BACKEND_BOOTSTRAP |
| Output extraction | JSON parsing from scratch | `runner.Output(ctx, dir)` + `extractValue(v)` | Wraps `output -json` parsing |
| Config env export | Manual `os.Setenv` loop | `ExportConfigEnvVars(cfg)` in `cmd/init.go:604` | Sets all 8 KM_* vars site.hcl needs |
| Repo root discovery | Hardcoded path | `findRepoRoot()` in cmd package | CLAUDE.md anchor-based discovery |
| Region label | String manipulation | `compiler.RegionLabel(region)` in `pkg/compiler/region.go:7` | Handles all AWS region formats |
| Config persistence | Custom YAML writer | Pattern from `persistKMConfigFields` in `init.go:1723` | Merge-preserving R/M/W idiom |
| Table output | Custom padding | `text/tabwriter` as in `ami.go:348` | Consistent with other list commands |

---

## Common Pitfalls

### Pitfall 1: Missing `region.hcl` in Cluster Stack Parent Directory

**What goes wrong:** `km cluster add` generates `infra/live/use1/cluster-dev-use1-0/terragrunt.hcl`. The HCL file calls `read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")`. If `km init` has never been run for region `use1`, the file `infra/live/use1/region.hcl` does not exist. Terragrunt fails with a cryptic "file not found" error before any AWS call.

**Why it happens:** `RunInitWithRunner` writes `region.hcl` for known modules, but `km cluster add` is a standalone command that does not invoke `runInit`.

**How to avoid:** Before running terragrunt apply, check if `infra/live/{regionLabel}/region.hcl` exists. If not, write it using the same `fmt.Sprintf` pattern from `init.go:656`. This is safe and idempotent.

```go
// Source: internal/app/cmd/init.go:656-663
regionHCL := fmt.Sprintf(`locals {
  region_label = "%s"
  region_full  = "%s"
}
`, regionLabel, region)
if err := os.WriteFile(filepath.Join(regionDir, "region.hcl"), []byte(regionHCL), 0o644); err != nil {
    return fmt.Errorf("writing region.hcl: %w", err)
}
```

**Warning signs:** `Error: Unable to read file ...region.hcl` in terragrunt output.

### Pitfall 2: `moved` Blocks Omission in create-handler Refactor

**What goes wrong:** Refactoring create-handler's 14 inline `aws_iam_role_policy` resources into a `module.km_operator_policy` call without `moved` blocks causes `terragrunt plan` to show 14 `aws_iam_role_policy` destroys followed by 14 creates — causing a momentary IAM permission gap on the running create-handler Lambda role.

**Why it happens:** Terraform tracks resources by address. Moving from `aws_iam_role_policy.s3_artifacts` to `module.km_operator_policy.aws_iam_role_policy.s3_artifacts` is an address change; without explicit `moved {}` declaration, Terraform sees destroy-then-create.

**How to avoid:** Add one `moved` block per policy in `create-handler/v1.0.0/main.tf`. Verify by running `cd infra/live/use1/create-handler && terragrunt plan -detailed-exitcode`. Expected exit code: 0 (no changes) or all changes show only metadata (no `~` on `aws_iam_role.create_handler`, no `-/+` on any policy).

**Warning signs:** `terragrunt plan` shows `14 to destroy, 14 to add` for `aws_iam_role_policy` resources.

### Pitfall 3: `persistClustersConfig` Overwrites Other Keys

**What goes wrong:** If `persistClustersConfig` marshals only `raw["clusters"] = clusters` and the existing `km-config.yaml` has keys that yaml.Marshal reorders or reformats, operator-customized comments or ordering are lost.

**Why it happens:** `yaml.Marshal` on `map[string]interface{}` does not preserve key ordering or comments. This is the same tradeoff accepted by `persistKMConfigFields`.

**How to avoid:** This is an accepted tradeoff (same as every other config mutation). Document it in the function's godoc comment: "Field ordering and YAML comments are not preserved."

**Warning signs:** `km-config.yaml` loses comments or has reordered keys after `km cluster add`.

### Pitfall 4: `cloudwatch_logs` Policy in km-operator-policy

**What goes wrong:** The `cloudwatch_logs` policy in create-handler references Lambda-specific log group patterns (`/aws/lambda/${var.resource_prefix}-*`). If this policy is included verbatim in km-operator-policy and applied to the cluster IRSA role, the IRSA role gains permission to write Lambda logs — which is harmless but also unnecessary.

**How to avoid:** Include the `cloudwatch_logs` policy in km-operator-policy for maximum compatibility (the spec requires "same surface as create-handler Lambda role"). The cluster IRSA role will have it, which is fine for v1. A future `--minimal-policy` flag could strip Lambda-specific permissions.

### Pitfall 5: `km cluster rm` Must Use `Reconfigure` Before Destroy (if region changed)

**What goes wrong:** If an operator ran `km cluster add` with one `KM_RESOURCE_PREFIX` and later runs `km cluster rm` with a different prefix (or after a Phase 66 upgrade), terragrunt destroy fails with "Backend configuration block has changed."

**How to avoid:** Follow the pattern from `pkg/terragrunt/runner.go:93` — call `runner.Reconfigure(ctx, stackDir)` before `runner.Destroy(ctx, stackDir)` in `km cluster rm`. This is a no-op when the backend is unchanged.

### Pitfall 6: `persistClustersConfig` — `clusters:` Not in Viper Merge Keys

**What goes wrong:** Even if `km-config.yaml` contains a `clusters:` key, `config.Load()` never reads it because the viper merge loop at `config.go:259-293` does not include `"clusters"` in its key list. Result: `cfg.Clusters` is always empty on startup.

**How to avoid:** Add `"clusters"` to the merge key list in `config.go:Load()` AND add `v.SetDefault("clusters", []interface{}{})` before the merge loop. Then populate `cfg.Clusters` via `v.UnmarshalKey("clusters", &cfg.Clusters)` in the `cfg := &Config{...}` block.

---

## Code Examples

### Full Policy List: All 14 Policies from create-handler/v1.0.0/main.tf

These are the policy resource names to extract into `km-operator-policy`:

| Resource Name | Policy Name (in AWS) | Covers |
|---|---|---|
| `cloudwatch_logs` | `{prefix}-create-handler-cw-logs` | CloudWatch Logs for Lambda |
| `s3_artifacts` | `{prefix}-create-handler-s3` | Artifact bucket |
| `dynamodb` | `{prefix}-create-handler-dynamodb` | State lock + budget tables |
| `dynamodb_sandboxes` | `{prefix}-create-handler-dynamodb-sandboxes` | Sandboxes + identities tables |
| `terraform_state` | `{prefix}-create-handler-tf-state` | State bucket ops (9 actions) |
| `ec2_provisioning` | `{prefix}-create-handler-ec2` | 29 EC2 actions |
| `iam_sandbox` | `{prefix}-create-handler-iam` | CreateRole/PassRole on `{prefix}-*` |
| `ecs_provisioning` | `{prefix}-create-handler-ecs` | ECS cluster/service/task |
| `scheduler` | `{prefix}-create-handler-scheduler` | EventBridge Scheduler + PassRole |
| `ssm` | `{prefix}-create-handler-ssm` | SSM Parameter Store `/{prefix}/*` |
| `ssm_send_command` | `{prefix}-create-handler-ssm-send-command` | SendCommand to tagged EC2 |
| `ses_send` | `{prefix}-create-handler-ses` | SES SendEmail |
| `lambda_budget` | `{prefix}-create-handler-lambda` | Lambda `*` on `{prefix}-*` functions |
| `kms` | `{prefix}-create-handler-kms` | KMS `*` on all resources |
| `sqs_slack_inbound` | `{prefix}-create-handler-sqs-slack-inbound` | SQS FIFO queue lifecycle |

**Note:** That is 15 resource names including `cloudwatch_logs`. The spec says 14 policies for the shared module. Verify whether to include `cloudwatch_logs` in the shared module (it references Lambda log group patterns). The spec table lists 14 policies and does NOT include `cloudwatch_logs` — that policy is Lambda-specific. The km-operator-policy module should contain the 14 policies listed in the spec table; `cloudwatch_logs` stays inline in create-handler only.

**Revised count: 14 policies to extract + `cloudwatch_logs` stays in create-handler = 14 `moved` blocks needed.**

### Trust Policy HCL (verified from spec)

```hcl
# Source: docs/superpowers/specs/2026-05-11-km-cluster-cross-account-irsa-design.md
locals {
  oidc_provider_host = replace(var.oidc_provider_arn, "/^arn:aws:iam::[0-9]+:oidc-provider\\//", "")
  has_wildcard   = can(regex("\\*", var.namespace)) || can(regex("\\*", var.service_account_name))
  sub_condition  = local.has_wildcard ? "StringLike" : "StringEquals"
}

data "aws_iam_policy_document" "trust" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [var.oidc_provider_arn]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_provider_host}:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = local.sub_condition
      variable = "${local.oidc_provider_host}:sub"
      values   = ["system:serviceaccount:${var.namespace}:${var.service_account_name}"]
    }
  }
}
```

### ExportConfigEnvVars Call Pattern (verified from init.go:320)

```go
// Source: internal/app/cmd/init.go:318-320
// Must be called before ANY terragrunt.Runner invocation.
// Exports: KM_ARTIFACTS_BUCKET, KM_ACCOUNTS_*, KM_DOMAIN, KM_REGION,
//          KM_OPERATOR_EMAIL, KM_SCHEDULER_ROLE_ARN, KM_RESOURCE_PREFIX, KM_EMAIL_SUBDOMAIN
ExportConfigEnvVars(cfg)
```

### Terragrunt Runner: Verbose/Quiet (verified from pkg/terragrunt/runner.go)

```go
// Source: pkg/terragrunt/runner.go:22-29, init.go:469-474
runner := terragrunt.NewRunner(awsProfile, repoRoot)
runner.Verbose = verbose
// In quiet mode (default): output buffered, only errors/warnings printed.
// In verbose mode: output streamed live to stdout/stderr.
```

---

## site.hcl Locals — Confirmed Spelling

Verified by reading `/Users/khundeck/working/klankrmkr/infra/live/site.hcl` directly:

| Local Reference | Value |
|---|---|
| `local.site_vars.locals.site.label` | `get_env("KM_RESOURCE_PREFIX", "km")` — the resource prefix |
| `local.site_vars.locals.site.tf_state_prefix` | `"tf-${KM_RESOURCE_PREFIX}"` |
| `local.site_vars.locals.backend.bucket` | `"${tf_state_prefix}-state-${region_label}"` |
| `local.site_vars.locals.backend.region` | `local.region.full` |
| `local.site_vars.locals.backend.encrypt` | `true` |
| `local.site_vars.locals.backend.dynamodb_table` | `"${tf_state_prefix}-locks-${region_label}"` |

**CONFIRMED:** All six locals referenced in the generated `cluster-{name}/terragrunt.hcl` template exist with these exact paths. The existing `create-handler/terragrunt.hcl` uses these same paths and serves as the authoritative reference.

---

## Module Versioning Conventions

**Confirmed from `ls infra/modules/*/`:**

- Every module uses a `v1.0.0/` subdirectory. This is universal — 25 modules, all `v1.0.0/`.
- Two modules have also shipped `v1.1.0/`: `dynamodb-identities` and `dynamodb-sandboxes` (Phase 67 GSI additions). The `live/` terragrunt.hcl for those modules references `v1.1.0`.
- **Convention for version bumps:** Create a new subdirectory (`v1.1.0/`) alongside `v1.0.0/`. Update the `live/` terragrunt.hcl `source` path to point at the new version. The old version directory stays in place (not deleted) to avoid breaking state for operators who haven't upgraded.
- **Phase 80 ships both new modules as `v1.0.0/`.** This is correct per the project convention. No version bump needed at introduction.

---

## CLI Registration

`root.go` registers subcommands via `root.AddCommand(New*Cmd(cfg))` on lines 41-97. Pattern for adding `NewClusterCmd`:

```go
// Source: internal/app/cmd/root.go:86 (NewVSCodeCmd registration as reference)
root.AddCommand(NewClusterCmd(cfg))
```

Add after `NewVSCodeCmd(cfg)` (line 86), before the `atCmd` block (line 89).

---

## CLAUDE.md Section Format

The Phase 80 CLAUDE.md section should mirror the Phase 73 (VS Code Remote-SSH) and Phase 79 (Presence daemon) sections. Both sections follow this structure:

1. `## [Feature Name] (Phase N)` — H2 heading with phase reference
2. One-sentence description of what it does.
3. `### [Feature]-specific table(s)` — profile field tables, env var tables, SSM parameter tables as applicable.
4. `### One-time setup` — code block with `make build && km init --sidecars` pattern if schema change; CLI commands.
5. `### Important workflow notes` — bullet list of gotchas, migration notes, rollback.
6. `### Observability` (optional) — if there are log/otel commands.
7. `See docs/...` reference at end.

For Phase 80, the section will be simpler (no profile fields, no env vars, no SSM params to add — this is a pure operator-side feature). The section needs:
- Short description of cross-account IRSA.
- `km cluster add|list|rm` flag table.
- `km-config.yaml` schema addition (clusters list).
- One-time setup: `make build`.
- Important workflow notes: no `km init --sidecars` needed (no Lambda changes), idempotency, `--dry-run=true` default.

---

## Test Conventions

### Existing Test Patterns

From `init_test.go` (verified):
- `package cmd_test` — external test package
- `mockRunner` struct with `applied []string` tracks Apply calls
- `t.TempDir()` for isolated filesystem state
- Table-driven tests with `t.Run(tc.name, ...)`
- `runKMArgsInDir(km, dir, "", "cluster", "list")` pattern for CLI integration

From `slack_test.go` (verified):
- Dependency injection via `SlackCmdDeps` struct; `NewSlackCmdWithDeps` constructor
- Fake implementations as local structs in the `_test.go` file
- `bytes.Buffer` capture for stdout/stderr inspection

### Test Strategy for cluster.go

1. **Unit tests for `generateClusterHCL`:** verify placeholder substitution, no Go template markers leak into output.
2. **Unit tests for `persistClustersConfig`:** write temp `km-config.yaml`, call persist, re-read and assert `clusters:` key.
3. **Unit tests for `km cluster add` via mockRunner:** verify Apply called with correct dir, Output called to get role ARN, cluster entry added to config.
4. **Unit tests for `km cluster list`:** load config with mock clusters, verify tabwriter output.
5. **Unit tests for `km cluster rm`:** verify Destroy called, cluster entry removed from config, directory removed.
6. **Integration test (phase-close):** `km cluster add --name dev-use1-0 --oidc-provider-arn arn:aws:iam::874364631781:... --aws-profile klanker-application --dry-run=false` → `aws iam get-role --role-name km-cluster-dev-use1-0` → `km cluster list` → `km cluster rm`.

No integration-test fixtures for full `terragrunt plan` runs exist in the codebase (tests mock the runner). The phase-close integration test is manual/CLI, not automated Go test.

---

## State of the Art

| Old Approach | Current Approach | Impact |
|---|---|---|
| Static IAM user keys for cross-account k8s | IRSA with AssumeRoleWithWebIdentity | No long-lived secrets, auto-rotating 3600s tokens |
| Duplicate policy JSON across roles | Shared km-operator-policy module | Single source of truth for km-operator permissions |
| `km init` only path for role provisioning | `km cluster add` standalone command | Operators can add cluster integrations without re-running full init |

---

## Open Questions

1. **`cloudwatch_logs` policy in km-operator-policy or not?**
   - What we know: The spec's policy table lists 14 policies for extraction, NOT including `cloudwatch_logs`. The create-handler has 15 total (`cloudwatch_logs` + 14 others).
   - What's unclear: Should the IRSA role have `cloudwatch_logs` permissions to write Lambda-style log groups? The spec says "same surface as create-handler" but the table deliberately omits it.
   - Recommendation: **Exclude `cloudwatch_logs` from km-operator-policy** (keep it inline in create-handler). The IRSA role for k8s does not run Lambda functions and does not need Lambda log group permissions. This means 14 `moved` blocks in create-handler (not 15). Document this as an intentional divergence from "same surface."

2. **`artifact_bucket_arn` variable in km-operator-policy: required or derived?**
   - What we know: The `s3_artifacts` policy needs `var.artifact_bucket_arn`. In create-handler's terragrunt.hcl, it is set as `"arn:aws:s3:::${get_env("KM_ARTIFACTS_BUCKET", "")}"`. In the cluster stack, there is no `KM_ARTIFACTS_BUCKET` get_env available in the template.
   - Recommendation: Pass `artifact_bucket_arn` as a module variable to km-operator-policy, and in the cluster terragrunt.hcl template derive it as `"arn:aws:s3:::${local.site_vars.locals.backend.bucket}"` — same bucket used for state. (The artifacts bucket and state bucket are both `KM_ARTIFACTS_BUCKET` in the klanker account. Verify this matches.)

3. **`region.hcl` write in `km cluster add` — which path?**
   - What we know: `infra/live/{regionLabel}/region.hcl` is written by `RunInitWithRunner`. If `km init` has never been run for that region, the file is missing.
   - Recommendation: `km cluster add` should write `region.hcl` unconditionally (idempotent) before generating the cluster terragrunt.hcl. Emit a note: `"Writing region.hcl for {regionLabel} (idempotent)"`.

---

## Validation Architecture

> `nyquist_validation` is enabled per `.planning/config.json`.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing stdlib (`testing` package) |
| Config file | none — `go test ./...` convention |
| Quick run command | `go test ./internal/app/cmd/ -run TestCluster -v` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map

| Deliverable | Behavior | Test Type | Automated Command | File Exists? |
|---|---|---|---|---|
| km-operator-policy module | 14 policies extracted from create-handler with zero net diff | manual | `cd infra/live/use1/create-handler && terragrunt plan -detailed-exitcode` | No — manual IAM verification |
| cluster-irsa module | Trust policy allows AssumeRoleWithWebIdentity from OIDC provider | unit (HCL review) + manual | `aws iam get-role --role-name km-cluster-{name}` | No |
| `generateClusterHCL` | Correct HCL content with placeholders substituted | unit | `go test ./internal/app/cmd/ -run TestGenerateClusterHCL -v` | No — Wave 0 |
| `km cluster add` | Calls Apply + Output + persists to config | unit (mockRunner) | `go test ./internal/app/cmd/ -run TestClusterAdd -v` | No — Wave 0 |
| `km cluster list` | Reads clusters from config, prints tabwriter table | unit | `go test ./internal/app/cmd/ -run TestClusterList -v` | No — Wave 0 |
| `km cluster rm` | Calls Destroy + removes config entry + removes dir | unit (mockRunner) | `go test ./internal/app/cmd/ -run TestClusterRm -v` | No — Wave 0 |
| `persistClustersConfig` | Writes + reloads clusters slice | unit | `go test ./internal/app/cmd/ -run TestPersistClusters -v` | No — Wave 0 |
| config.go Clusters field | Loaded from km-config.yaml on startup | unit | `go test ./internal/app/config/ -run TestClustersField -v` | No — Wave 0 |
| Integration | Full add→verify→list→rm sequence | manual e2e | `km cluster add ... --dry-run=false` | No — manual |
| CLAUDE.md update | Doc matches Phase 73/79 format | manual review | n/a | No |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/cmd/ -run TestCluster -v`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work` + manual integration test confirms `km-cluster-dev-use1-0` role exists in IAM and is destroyed by `km cluster rm`

### Wave 0 Gaps
- [ ] `internal/app/cmd/cluster_test.go` — covers TestGenerateClusterHCL, TestClusterAdd, TestClusterList, TestClusterRm, TestPersistClusters
- [ ] `internal/app/config/config_clusters_test.go` — covers TestClustersField loading from km-config.yaml

*(Existing test infrastructure in `cmd_test` package covers the runner mock pattern; new tests reuse `mockRunner` from `init_test.go`.)*

---

## Sources

### Primary (HIGH confidence)
- Verified by direct file read: `infra/modules/create-handler/v1.0.0/main.tf` — all 15 policy resource names and JSON bodies
- Verified by direct file read: `infra/live/use1/create-handler/terragrunt.hcl` — site.hcl locals spellings
- Verified by direct file read: `infra/live/site.hcl` — all 6 backend/site locals confirmed
- Verified by direct file read: `internal/app/cmd/init.go` — ExportConfigEnvVars, persistKMConfigFields, runInit, RunInitWithRunner patterns
- Verified by direct file read: `pkg/compiler/region.go` — RegionLabel() function confirmed
- Verified by direct file read: `pkg/aws/client.go` — LoadAWSConfig, ValidateCredentials signatures
- Verified by direct file read: `pkg/terragrunt/runner.go` — Runner struct, Apply/Destroy/Output methods, verbose/quiet behavior
- Verified by direct file read: `internal/app/cmd/slack.go` — parent-with-subcommands Cobra pattern
- Verified by direct file read: `internal/app/cmd/ami.go` — tabwriter usage
- Verified by direct file read: `internal/app/cmd/root.go` — registration order, no existing NewClusterCmd
- Verified by direct file read: `internal/app/config/config.go` — full Config struct, Load() viper keys, no existing Clusters field
- Verified by `ls infra/modules/*/` — all 25 modules use `v1.0.0/` subdirectory; dynamodb-identities and dynamodb-sandboxes also have v1.1.0

### Secondary (MEDIUM confidence)
- Design spec `docs/superpowers/specs/2026-05-11-km-cluster-cross-account-irsa-design.md` — trust policy HCL, cross-account IRSA explanation, file change summary
- CONTEXT.md `80-CONTEXT.md` — locked decisions, CLI flags, config schema

### Tertiary (LOW confidence / not independently verified)
- Terraform `moved` block behavior for resource address changes (knowledge from training data; verify against `terragrunt plan` output during implementation)

---

## Metadata

**Confidence breakdown:**
- Policy extraction list: HIGH — read from source file directly
- site.hcl locals: HIGH — read from source file directly
- `moved` block zero-diff claim: MEDIUM — training knowledge, must be verified by running `terragrunt plan -detailed-exitcode` during implementation
- Architecture patterns: HIGH — all code paths verified from source
- Pitfalls: HIGH for items with source code citations; MEDIUM for `moved` block pitfall

**Research date:** 2026-05-11
**Valid until:** 2026-06-10 (30 days; stable infrastructure patterns)
