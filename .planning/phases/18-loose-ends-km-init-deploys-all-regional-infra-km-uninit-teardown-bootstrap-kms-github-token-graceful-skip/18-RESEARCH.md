# Phase 18: Loose Ends — Research

**Researched:** 2026-03-23
**Domain:** Go CLI command expansion (Cobra), Terragrunt orchestration, AWS SDK v2
**Confidence:** HIGH

## Summary

Phase 18 closes operational gaps discovered during live testing. The codebase is in good shape: Phases 15-17 implemented `km doctor`, `km bootstrap`, KMS key creation, GitHub App token integration, and sandbox identity. What remains is expanding `km init` from network-only to all-regional-infra, adding a new `km uninit` teardown command, making the github-token module skip gracefully when not configured, wiring `state_bucket` into `km configure`, and updating `km doctor` to verify all regional infra components (not just VPC).

All six terragrunt configs that `km init` must deploy already exist under `infra/live/use1/`: `network/`, `dynamodb-budget/`, `dynamodb-identities/`, `ses/`, `s3-replication/`, and `ttl-handler/`. The `km init` command currently only applies `network/` and saves `outputs.json`. Expanding it means calling `runner.Apply()` on the remaining five directories in dependency order.

`km uninit` is a net-new command (no `uninit.go` exists). It must mirror `km init` in reverse: check for active sandboxes, then destroy TTL handler, SES, S3 replication, DynamoDB tables, and network last. The same Go DI + Cobra patterns used by existing commands apply directly.

The github-token issue is a real problem in production: `sandbox_iam_role_arn` is a required variable with no default, so when a profile has no `sourceAccess.github`, the compiler skips HCL generation (correct), but `km create` still calls `generateAndStoreGitHubToken` if `spec.sourceAccess.github != nil`. When GitHub App SSM params don't exist, it fails with an ugly stack trace instead of a clean "skipped (not configured)" message. The fix is in `generateAndStoreGitHubToken`: detect `ParameterNotFound` and return a sentinel that the caller labels as "skipped".

**Primary recommendation:** Treat this as four focused work streams in one phase: (1) expand `km init` sequentially across all six regional modules, (2) add `km uninit` as a new Cobra command in `uninit.go`, (3) fix github-token graceful skip in `generateAndStoreGitHubToken`, (4) add `state_bucket` prompt to `km configure`. Each stream is independent and can be planned as separate waves.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/spf13/cobra` | already in go.mod | CLI command tree | Existing pattern — all commands use it |
| `github.com/aws/aws-sdk-go-v2` | already in go.mod | AWS API calls (S3, DynamoDB, STS, EC2) | Project standard — no alternatives |
| `github.com/whereiskurt/klankrmkr/pkg/terragrunt` | local pkg | Terragrunt runner | Existing abstraction, used by init/create/destroy |
| `github.com/aws/aws-sdk-go-v2/service/s3` | already in go.mod | List sandboxes (active-sandbox check in uninit) | Existing pattern in list.go |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/aws/aws-sdk-go-v2/service/ssm/types` | already in go.mod | `ParameterNotFound` error detection | github-token graceful skip pattern |
| `errors.As()` standard library | stdlib | Typed error unwrapping for AWS SDK errors | Already used in doctor.go checkGitHubConfig |

**No new dependencies are needed.** All required libraries are already in `go.mod`.

## Architecture Patterns

### Recommended Project Structure

No new directories are needed. Additions:

```
internal/app/cmd/
├── uninit.go          # new — km uninit command
├── uninit_test.go     # new — unit tests
└── (help/uninit.txt)  # new — help text

infra/live/use1/       # existing — no new terragrunt configs needed
```

### Pattern 1: Sequential Terragrunt Apply in km init

**What:** `km init` iterates a hardcoded ordered list of regional module directories and calls `runner.Apply()` on each in dependency order. Skip directories that don't exist (handles older regions that don't have all modules yet).

**When to use:** Any time km init needs idempotent region-wide deployment.

**Example:**
```go
// Source: modeled on existing runInit() in internal/app/cmd/init.go

type regionalModule struct {
    dir  string // path relative to regionDir
    name string // human-readable label for progress output
}

// Dependency order: network first (others depend on VPC outputs),
// then independent modules in any order, TTL handler last (needs artifact bucket).
func regionalModules(regionDir string) []regionalModule {
    return []regionalModule{
        {dir: filepath.Join(regionDir, "network"),              name: "network (VPC/subnets/SGs)"},
        {dir: filepath.Join(regionDir, "dynamodb-budget"),      name: "DynamoDB budget table"},
        {dir: filepath.Join(regionDir, "dynamodb-identities"),  name: "DynamoDB identity table"},
        {dir: filepath.Join(regionDir, "ses"),                  name: "SES email infrastructure"},
        {dir: filepath.Join(regionDir, "s3-replication"),       name: "S3 artifact replication"},
        {dir: filepath.Join(regionDir, "ttl-handler"),          name: "TTL handler Lambda"},
    }
}

for _, mod := range regionalModules(regionDir) {
    if _, err := os.Stat(mod.dir); os.IsNotExist(err) {
        fmt.Printf("  Skipping %s (directory not found)\n", mod.name)
        continue
    }
    fmt.Printf("  Applying %s...\n", mod.name)
    if err := runner.Apply(ctx, mod.dir); err != nil {
        return fmt.Errorf("%s provisioning failed: %w", mod.name, err)
    }
}
```

### Pattern 2: km uninit — Reverse-Order Destroy with Active Sandbox Guard

**What:** New `uninit.go` command that (a) checks for active sandboxes in the region via S3 metadata scan, (b) refuses to proceed unless --force is passed or sandboxes are zero, (c) destroys modules in reverse dependency order via `runner.Destroy()`.

**When to use:** Operator decommissions a region.

**Example:**
```go
// Source: modeled on runInit() and destroy.go patterns

func NewUninitCmd(cfg *config.Config) *cobra.Command {
    var awsProfile string
    var region string
    var force bool

    cmd := &cobra.Command{
        Use:   "uninit",
        Short: "Tear down all shared regional infrastructure for a region",
        RunE: func(cmd *cobra.Command, args []string) error {
            return runUninit(cfg, awsProfile, region, force)
        },
    }
    cmd.Flags().StringVar(&awsProfile, "aws-profile", "klanker-application", "")
    cmd.Flags().StringVar(&region, "region", "us-east-1", "")
    cmd.Flags().BoolVar(&force, "force", false, "Destroy even if active sandboxes exist")
    return cmd
}

func runUninit(cfg *config.Config, awsProfile, region string, force bool) error {
    ctx := context.Background()
    // 1. Validate credentials
    // 2. Check active sandboxes — error if any exist and !force
    // 3. Destroy in reverse order: ttl-handler, s3-replication, ses,
    //    dynamodb-identities, dynamodb-budget, network
    // 4. Print summary
}
```

**Active sandbox check:** Use existing `SandboxLister` (list.go / pkg/aws) — filter by region label. The lister uses S3 metadata prefixes; the region is stored in each sandbox's `metadata.json` (`SandboxMetadata.Region`).

### Pattern 3: GitHub Token Graceful Skip

**What:** `generateAndStoreGitHubToken` currently returns an opaque error when SSM params are missing. Fix: detect `ssmtypes.ParameterNotFound` and return a typed sentinel that `runCreate` logs as "skipped (not configured)".

**When to use:** Any profile with `sourceAccess.github` section but no GitHub App configured in SSM.

**Example:**
```go
// Source: errors.As pattern already used in doctor.go checkGitHubConfig

import ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

// ErrGitHubNotConfigured signals that GitHub App SSM params are absent.
// runCreate logs this as "skipped (not configured)" rather than as an error.
var ErrGitHubNotConfigured = errors.New("GitHub App not configured — run 'km configure github' first")

func generateAndStoreGitHubToken(...) error {
    appClientIDOut, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
        Name: aws.String("/km/config/github/app-client-id"),
    })
    if err != nil {
        var notFound *ssmtypes.ParameterNotFound
        if errors.As(err, &notFound) {
            return ErrGitHubNotConfigured
        }
        return fmt.Errorf("read app-client-id from SSM: %w", err)
    }
    // ... rest of function unchanged
}

// In runCreate:
if tokenErr := generateAndStoreGitHubToken(...); tokenErr != nil {
    if errors.Is(tokenErr, ErrGitHubNotConfigured) {
        fmt.Printf("Step 13a: GitHub token skipped (not configured)\n")
    } else {
        log.Warn().Err(tokenErr).Msg("Step 13a: GitHub App token generation failed (non-fatal)")
    }
}
```

### Pattern 4: km configure — state_bucket prompt

**What:** Add `state_bucket` as a prompted field in `runConfigure`. Write it to `km-config.yaml`. The config loader already reads `state_bucket` from km-config.yaml into `cfg.StateBucket` — this just adds the wizard prompt.

**When to use:** All new configure wizard executions.

**Example:**
```go
// In configure.go runConfigure — add to platformConfig struct:
type platformConfig struct {
    // ... existing fields ...
    StateBucket     string `yaml:"state_bucket,omitempty"`
}

// In interactive wizard:
stateBucket, err = prompt(out, scanner, "S3 state bucket name (for km list/status)", stateBucket)

// In non-interactive path — optional, no --state-bucket required flag needed
// (can be empty for operators who set KM_STATE_BUCKET in env)
```

### Pattern 5: km doctor — Regional Infra Checks

**What:** Add DynamoDB check for `km-budgets` and `km-identities` (already present), and add Lambda function existence checks for `ttl-handler` and an SES verified identity check. The existing `checkDynamoTable` and `checkKMSKey` patterns are reusable. For Lambda and SES, new narrow interfaces follow the existing pattern.

**Current state:** `km doctor` already checks:
- Config fields
- AWS credentials
- S3 state bucket
- DynamoDB budget table (checkOK)
- DynamoDB identity table (demoted to checkWarn)
- KMS key alias/km-platform
- SCP attachment
- GitHub SSM config
- Per-region VPC (via EC2 DescribeVpcs)
- Active sandbox summary

**Missing checks per phase requirements:**
- SES verified identity (is the domain verified in SES?)
- TTL handler Lambda deployed (is `km-ttl-handler` Lambda present?)

These can be CheckWarn (not CheckError) since they're informational — doctor is a health check, not a blocker.

### Anti-Patterns to Avoid

- **Parallel Terragrunt applies across modules:** The six modules have dependency ordering (network outputs are referenced by SES, etc.). Run sequentially, not via goroutines.
- **Hardcoding regionDir paths:** Use `compiler.RegionLabel(region)` (already used in init.go) to map region strings to directory labels.
- **Destroying with active sandboxes:** `km uninit` without `--force` must error before any `runner.Destroy()` calls. Don't partially destroy then fail.
- **Breaking the non-fatal github-token pattern in create.go:** The fix must preserve the non-fatal wrapper. Only the inner error message changes.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Active sandbox detection | Custom S3 scan | `SandboxLister` interface + `ListSandboxes()` already in pkg/aws | Already tested, handles pagination, region filter |
| Terragrunt destroy orchestration | Custom subprocess | `runner.Destroy(ctx, dir)` from `pkg/terragrunt` | Existing abstraction handles streaming, error propagation |
| ParameterNotFound detection | String matching on error message | `errors.As(err, &ssmtypes.ParameterNotFound{})` | AWS SDK types already imported in cmd package |
| Sequential apply loop | Per-module custom functions | One `regionalModules()` function returning ordered slice | Avoids 6x code duplication |

**Key insight:** Every piece of infrastructure for this phase already exists as code — the work is wiring, not building. The six terragrunt configs exist. The runner pattern exists. The DI interfaces exist. The lister exists.

## Common Pitfalls

### Pitfall 1: s3-replication Has a Non-Root Provider Block

**What goes wrong:** `s3-replication/terragrunt.hcl` has its own `generate "provider"` block (with a `replica` alias) and does NOT include the root `root.hcl`. Calling `runner.Apply()` on it works fine — but `runner.Destroy()` must also work. Verify the runner's destroy path doesn't assume root include.

**Why it happens:** S3 cross-region replication requires two provider configs (source + replica regions) which can't coexist with a generic root include.

**How to avoid:** Test `runner.Destroy()` on `s3-replication` specifically in uninit.

### Pitfall 2: SES Module Requires KM_ROUTE53_ZONE_ID

**What goes wrong:** `ses/terragrunt.hcl` reads `KM_ROUTE53_ZONE_ID` via `get_env()`. If this env var is not set during `km init`, the SES module will fail with an empty-string zone ID.

**Why it happens:** SES domain verification requires Route53 DNS records, which requires the hosted zone ID.

**How to avoid:** In `runInit`, check that `KM_ROUTE53_ZONE_ID` is set before attempting to apply SES, or document it as a prerequisite. Similar to how `KM_ARTIFACTS_BUCKET` is required for TTL handler and S3 replication.

### Pitfall 3: TTL Handler and S3 Replication Require KM_ARTIFACTS_BUCKET

**What goes wrong:** Both `ttl-handler/terragrunt.hcl` and `s3-replication/terragrunt.hcl` read `KM_ARTIFACTS_BUCKET` via `get_env()`. If unset, the module gets an empty string which Terraform may reject.

**Why it happens:** These modules were designed to be applied manually with env vars set, not called from a Go command that auto-applies everything.

**How to avoid:** Before applying these modules, check `os.Getenv("KM_ARTIFACTS_BUCKET")` and either skip those modules with a warning, or error with a clear message pointing to the env var.

### Pitfall 4: uninit Active Sandbox Check Depends on StateBucket

**What goes wrong:** The `SandboxLister` requires `cfg.StateBucket` to be set to list sandboxes. If `state_bucket` is empty, the check can't be performed.

**Why it happens:** `km list` uses S3-based metadata storage introduced in Phase 11.

**How to avoid:** If `cfg.StateBucket` is empty, emit a warning "cannot verify active sandboxes — state_bucket not configured" and require `--force` to proceed.

### Pitfall 5: github-token sandbox_iam_role_arn Is Required (No Default)

**What goes wrong:** The `github-token` Terraform module has `sandbox_iam_role_arn` as a required variable with no default. Even if the HCL is generated, if the value is empty string, Terraform will error at plan time.

**Why it happens:** The HCL generation in `generateGitHubTokenHCL` doesn't include `sandbox_iam_role_arn` — it expects the service.hcl `github_token_inputs` block to supply it, but that block also doesn't include it (see service_hcl.go lines 89-95).

**How to avoid:** The compiler-side fix is to add `sandbox_iam_role_arn` to the `github_token_inputs` locals block in service_hcl.go, reading the IAM role ARN from Terraform outputs. OR add a `default = ""` to the variable in the module and guard the KMS policy statement against empty ARN. The graceful-skip approach (don't generate HCL when GitHub unconfigured) sidesteps this entirely for non-GitHub profiles.

### Pitfall 6: Region Label vs Region Full in Directory Paths

**What goes wrong:** `km init` uses `compiler.RegionLabel(region)` to convert `us-east-1` to `use1`. The existing terragrunt configs are under `infra/live/use1/`. A new region passed to `km init` (e.g., `ca-central-1`) would create `infra/live/cac1/` — but the six existing configs only exist under `use1/`.

**Why it happens:** The live configs were created for use1 during Phase 9.

**How to avoid:** `km init` must create the target region directory structure by copying templates (already partially done for network via `infra/templates/network/`). For other modules, the template approach needs to be extended OR the live configs need to be parameterized templates that `km init` instantiates.

**Current state (important):** The `infra/live/use1/` directory contains the actual configs (not templates) and they're already committed. `km init` currently copies `infra/templates/network/terragrunt.hcl` for the network module. The other five modules have no templates. For Phase 18, if the scope is "use1 only", the existing hardcoded dirs can be applied directly. If multi-region is required, template expansion is needed.

## Code Examples

### Checking for Active Sandboxes in a Region

```go
// Source: pkg/aws lister + SandboxMetadata.Region field

func countActiveSandboxes(ctx context.Context, lister SandboxLister, region string) (int, error) {
    records, err := lister.ListSandboxes(ctx, false)
    if err != nil {
        return 0, err
    }
    count := 0
    for _, r := range records {
        if r.Region == region && r.Status == "running" {
            count++
        }
    }
    return count, nil
}
```

### Registering uninit in root.go

```go
// Source: internal/app/cmd/root.go — existing pattern
root.AddCommand(NewUninitCmd(cfg))
```

### DoctorDeps Extension for Lambda Check

```go
// Source: doctor.go DoctorDeps pattern — add narrow interface

// LambdaGetFunctionAPI covers Lambda GetFunction for existence check.
type LambdaGetFunctionAPI interface {
    GetFunction(ctx context.Context, params *lambda.GetFunctionInput, optFns ...func(*lambda.Options)) (*lambda.GetFunctionOutput, error)
}

// checkLambdaFunction verifies a Lambda function exists by name.
func checkLambdaFunction(ctx context.Context, client LambdaGetFunctionAPI, funcName string) CheckResult {
    name := fmt.Sprintf("Lambda (%s)", funcName)
    if client == nil {
        return CheckResult{Name: name, Status: CheckSkipped, Message: "Lambda client not available"}
    }
    _, err := client.GetFunction(ctx, &lambda.GetFunctionInput{
        FunctionName: aws.String(funcName),
    })
    if err != nil {
        return CheckResult{
            Name: name, Status: CheckWarn,
            Message: fmt.Sprintf("Lambda %q not found — run 'km init' to deploy", funcName),
            Remediation: "Run 'km init --region <region>' to deploy the TTL handler Lambda",
        }
    }
    return CheckResult{Name: name, Status: CheckOK, Message: fmt.Sprintf("%q deployed", funcName)}
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `km init` = network only | `km init` = all 6 regional modules | Phase 18 | Operators no longer need 6 manual apply commands |
| github-token failure = stack trace | github-token not configured = "skipped" warning | Phase 18 | Clean operator UX; non-fatal preserved |
| `km configure` no state_bucket | `km configure` prompts for state_bucket | Phase 18 | Fixes `km list`/`km status` failure on fresh setup |
| `km doctor` checks VPC only for region | `km doctor` checks VPC + DynamoDB + Lambda | Phase 18 | Operators get full regional health status |

## Open Questions

1. **Multi-region template expansion**
   - What we know: `infra/live/use1/` has the six configs; `infra/templates/network/` exists for network only.
   - What's unclear: Does Phase 18 need `km init --region ca-central-1` to work (requires templates for all 6 modules), or is use1-only sufficient for v1?
   - Recommendation: For Phase 18, apply existing use1 configs directly (not via template expansion) and document that multi-region requires manual setup. The phase description says "deploys all regional infra in one command" — for use1, this is fully achievable without template expansion.

2. **SES Verification Timing**
   - What we know: SES domain verification requires DNS propagation (minutes to hours). Terragrunt apply of `ses/` creates the verification tokens but DNS propagation is async.
   - What's unclear: Should `km init` warn about async SES verification or treat the apply as complete?
   - Recommendation: Treat apply-success as complete; document that SES sends from the domain only after DNS propagation (existing behavior — no change needed).

3. **uninit destroy order for modules with no Terraform state**
   - What we know: `runner.Destroy()` on a module with no state is a no-op in Terraform.
   - What's unclear: If a subset of modules was never applied (e.g., SES), does `runner.Destroy()` fail?
   - Recommendation: Wrap each `runner.Destroy()` call in a check — if the state key doesn't exist in S3, print "nothing to destroy for X" and continue. This mirrors the idempotency requirement.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) |
| Config file | none — standard `go test ./...` |
| Quick run command | `go test ./internal/app/cmd/... -run TestUninit -v` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command | File Exists? |
|----------|-----------|-------------------|-------------|
| `km init` applies all 6 modules in order | unit (mock runner) | `go test ./internal/app/cmd/... -run TestInit` | ❌ Wave 0 |
| `km uninit` refuses without --force when sandboxes active | unit (mock lister) | `go test ./internal/app/cmd/... -run TestUninit` | ❌ Wave 0 |
| `km uninit` destroys in reverse order | unit (mock runner captures calls) | `go test ./internal/app/cmd/... -run TestUninitDestroyOrder` | ❌ Wave 0 |
| github-token skips gracefully on ParameterNotFound | unit (mock SSM) | `go test ./internal/app/cmd/... -run TestCreateGitHubSkip` | ❌ Wave 0 |
| `km configure` writes state_bucket to YAML | unit (buffer IO) | `go test ./internal/app/cmd/... -run TestConfigureStateBucket` | ❌ Wave 0 |
| `km doctor` checks TTL Lambda deployed | unit (mock Lambda client) | `go test ./internal/app/cmd/... -run TestDoctorLambda` | ❌ Wave 0 |
| `km bootstrap` KMS key creation end-to-end | manual/integration | n/a — requires real AWS | manual-only |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/cmd/... -run TestInit -v`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `internal/app/cmd/uninit_test.go` — covers uninit command (mock runner + lister)
- [ ] `internal/app/cmd/help/uninit.txt` — help text for new command
- [ ] Additional test cases in `internal/app/cmd/create_test.go` for github-token skip path
- [ ] Additional test cases in `internal/app/cmd/configure_test.go` for state_bucket prompt

*(Framework install not needed — Go stdlib `testing` already in use across all cmd tests)*

## Sources

### Primary (HIGH confidence)
- Direct code inspection: `internal/app/cmd/init.go` — current init implementation (network only)
- Direct code inspection: `internal/app/cmd/bootstrap.go` — KMS key creation pattern, ensureKMSPlatformKey()
- Direct code inspection: `internal/app/cmd/doctor.go` — DI interface pattern, buildChecks(), checkDynamoTable()
- Direct code inspection: `internal/app/cmd/create.go` — github-token flow, Step 13a/13b
- Direct code inspection: `pkg/compiler/github_token_hcl.go` — HCL generation gate (sourceAccess.github != nil)
- Direct code inspection: `pkg/compiler/service_hcl.go` — github_token_inputs block
- Direct code inspection: `infra/live/use1/` — all 6 regional module configs confirmed present
- Direct code inspection: `infra/modules/github-token/v1.0.0/variables.tf` — `sandbox_iam_role_arn` required, no default
- Direct code inspection: `internal/app/cmd/configure.go` — current wizard does NOT prompt for state_bucket
- Direct code inspection: `internal/app/config/config.go` — `StateBucket` field exists, `state_bucket` viper key exists
- Direct code inspection: `internal/app/cmd/root.go` — no uninit command registered

### Secondary (MEDIUM confidence)
- `git status` output: `?? infra/live/use1/network/` — untracked but correct content (outputs.json + terragrunt.hcl), not a stale stale directory from top-level; it's the live-used but git-untracked network deployment

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries already in codebase, confirmed in go.mod
- Architecture: HIGH — all patterns directly observed in existing command implementations
- Pitfalls: HIGH — identified from direct code inspection and git status of untracked files
- Validation: HIGH — existing test file structure confirmed, test runner confirmed working

**Research date:** 2026-03-23
**Valid until:** 2026-04-23 (stable codebase, no third-party dependencies changing)
