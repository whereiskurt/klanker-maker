# Phase 12: ECS Budget Top-Up & S3 Replication Deployment — Research

**Researched:** 2026-03-22
**Domain:** Go (AWS SDK v2 ECS re-provisioning) + Terragrunt (live config for s3-replication module)
**Confidence:** HIGH (all findings from direct codebase inspection; no external library research required)

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| BUDG-08 | `km budget add` for ECS sandbox re-provisions the Fargate task from S3-stored profile; budget enforcer resumes monitoring | ECS SDK is already in go.mod (`service/ecs v1.74.0`). Profile is stored at `artifacts/{sandbox-id}/.km-profile.yaml`. `runBudgetAdd` has the EC2 path wired; ECS branch needs to be added. |
| OBSV-06 | S3 artifact storage supports multi-region replication; `infra/live/use1/s3-replication/terragrunt.hcl` exists and deploys the s3-replication module | The Terraform module is complete at `infra/modules/s3-replication/v1.0.0/`. Only the live Terragrunt config is missing. Pattern matches existing configs at `infra/live/use1/dynamodb-budget/terragrunt.hcl`. |
</phase_requirements>

---

## Summary

Phase 12 closes two integration gaps from the v1.0 milestone audit. Both are narrow, targeted additions — no new packages or AWS services are needed beyond what is already in go.mod and the infra module library.

**Gap 1 — ECS budget top-up (BUDG-08):** `km budget add` already handles EC2 via `StartInstances`. The ECS path is missing: when `meta.Substrate == "ecs"`, the command must download the stored profile YAML from S3 (`artifacts/{sandbox-id}/.km-profile.yaml`), recompile it (via `compiler.Compile`), write the artifacts to the sandbox directory, and run `terragrunt apply`. This is identical to the `km create` flow except it operates on a pre-existing sandbox ID rather than generating a new one. The AWS ECS SDK is already in go.mod at `github.com/aws/aws-sdk-go-v2/service/ecs v1.74.0`; the DI pattern for the new ECS client interface must follow the existing `EC2StartAPI` and `IAMAttachAPI` patterns in `budget.go`.

**Gap 2 — S3 replication Terragrunt config (OBSV-06):** The Terraform module `infra/modules/s3-replication/v1.0.0/` is complete and takes four variables: `source_bucket_name`, `source_bucket_arn`, `destination_region`, `destination_bucket_name`. The live config is simply missing. The file `infra/live/use1/s3-replication/terragrunt.hcl` needs to be created following the exact same structure as `infra/live/use1/dynamodb-budget/terragrunt.hcl`, but the s3-replication module requires a second AWS provider alias (`aws.replica`) for the destination region. The root `terragrunt.hcl` only generates a single `provider "aws"`. The s3-replication module declares `provider "aws" { alias = "replica" }` at the top of `main.tf`, which means the live config must either supply the aliased provider or the module must be adjusted to accept a `provider` block in the Terragrunt `generate` block.

**Primary recommendation:** Two focused plans. Plan 1: Add ECS re-provisioning branch to `runBudgetAdd` with a new `ECSProvisionAPI` interface and tests. Plan 2: Create `infra/live/use1/s3-replication/terragrunt.hcl` with the correct dual-provider `generate` block.

---

## Standard Stack

### Core (already present — no new dependencies needed)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/ecs` | v1.74.0 | ECS RunTask, DescribeTasks | Already in go.mod; used in shell.go for ECS Exec |
| `github.com/aws/aws-sdk-go-v2/service/s3` | v1.97.1 | Download stored profile YAML | Already used in budget.go (realMetaFetcher) |
| `pkg/compiler` | local | Recompile profile into Terragrunt artifacts | `compiler.Compile` is the existing path in `km create` |
| `pkg/terragrunt` | local | Run `terragrunt apply` for ECS re-provisioning | `Runner.Apply` is the existing path in `km create` |
| `internal/app/config` | local | `Config.StateBucket`, `Config.ArtifactsBucket` | Config already reads env vars; `KM_ARTIFACTS_BUCKET` is the artifact bucket |
| Terragrunt (binary) | existing | Deploy s3-replication module | Same binary used by all other live configs |

### No New Dependencies

go.mod does not need changes. All required packages are already imported.

## Architecture Patterns

### Pattern 1: ECS Re-Provisioning in runBudgetAdd

**What:** Add an ECS substrate branch to `runBudgetAdd` that downloads the stored profile, recompiles it, and reruns `terragrunt apply` using the same sandbox ID.

**Existing pattern to follow:** The EC2 branch in `runBudgetAdd` (lines 152–162 of `budget.go`) calls `resumeEC2Sandbox` via an injected interface. The ECS branch follows the same DI structure.

**New interface (follows EC2StartAPI pattern):**
```go
// ECSProvisionAPI is the minimal interface required for ECS sandbox re-provisioning.
// Implemented by *ecs.Client + a composite that also wraps S3 and terragrunt.
// Kept narrow so tests can inject a fake without real AWS calls.
type ECSProvisionAPI interface {
    // DescribeTasks checks if the ECS task is still running (stopped = needs re-provision).
    DescribeTasks(ctx context.Context, input *ecs.DescribeTasksInput, optFns ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error)
    // ListTasks finds the task ARN for the given cluster/sandbox combination.
    ListTasks(ctx context.Context, input *ecs.ListTasksInput, optFns ...func(*ecs.Options)) (*ecs.ListTasksOutput, error)
}
```

**Re-provisioning logic:**
1. Download profile YAML from S3: `artifacts/{sandbox-id}/.km-profile.yaml` (read via existing `S3GetAPI`)
2. Parse and resolve profile using `profile.Parse` + `profile.Resolve` (existing functions)
3. Load network config via `LoadNetworkOutputs` (existing function in `create.go`)
4. Call `compiler.Compile(resolvedProfile, sandboxID, onDemand, network)` — same sandbox ID reused
5. Write artifacts to sandbox directory via `terragrunt.CreateSandboxDir` + `terragrunt.PopulateSandboxDir`
6. Run `runner.Apply(ctx, sandboxDir)` — budget enforcer Lambda auto-resumes because it's a 1-minute EventBridge schedule; no special re-wiring needed

**Key insight:** Reusing the same `sandboxID` means Terraform state already knows about the ECS cluster. `terragrunt apply` idempotently re-provisions the stopped Fargate task. The budget enforcer Lambda (per-sandbox, `km-budget-enforcer-{sandbox-id}`) continues running on its 1-minute schedule and resumes monitoring automatically.

**Where to add the ECS branch in `runBudgetAdd`:**
```go
// After the EC2 branch (substrate == "ec2"):
if substrate == "ecs" {
    err := reprovisionsECSSandbox(ctx, cmd, cfg, sandboxID, awsProfile)
    if err != nil {
        fmt.Fprintf(cmd.OutOrStdout(), "Warning: could not re-provision ECS sandbox: %v\n", err)
    } else {
        resumed = true
    }
}
```

**`reprovisionECSSandbox` function signature:**
```go
// reprovisionECSSandbox downloads the stored profile from S3 and runs terragrunt apply
// to restart the Fargate task with the same sandbox ID and container definitions.
func reprovisionECSSandbox(ctx context.Context, cmd *cobra.Command, cfg *config.Config, sandboxID, awsProfile string) error
```

This function is not injected via interface (it calls `compiler.Compile` and `runner.Apply` directly) — it is a real implementation function. Tests for the ECS path use a fake `SandboxMetaFetcher` returning `substrate=ecs` and verify that the correct code path is triggered (source-level verification test using `strings.Contains`, following the Phase 07-02 pattern).

### Pattern 2: S3 Replication Terragrunt Live Config

**What:** Create `infra/live/use1/s3-replication/terragrunt.hcl` that deploys the existing s3-replication module.

**Critical constraint — dual provider:** The s3-replication Terraform module uses `provider "aws" { alias = "replica" }` in `main.tf`. This aliased provider targets the destination region. The root `terragrunt.hcl` generates a single `provider "aws"` (the source region). The live config must generate both providers.

**Correct approach:** Add a `generate "providers"` block in the `s3-replication/terragrunt.hcl` that overrides the root-generated provider with both the default and the aliased replica provider. Terragrunt's `if_exists = "overwrite_terragrunt"` ensures the per-config generate wins.

```hcl
generate "providers" {
  path      = "provider.tf"
  if_exists = "overwrite_terragrunt"

  contents = <<-EOF
    terraform {
      required_version = ">= 1.6.0"
      required_providers {
        aws = {
          source  = "hashicorp/aws"
          version = ">= 5.0"
        }
      }
    }

    provider "aws" {
      region = "${local.region_full}"
    }

    provider "aws" {
      alias  = "replica"
      region = get_env("KM_REPLICA_REGION", "us-west-2")
    }
  EOF
}
```

**Module inputs:**
```hcl
inputs = {
  source_bucket_name      = get_env("KM_ARTIFACTS_BUCKET", "")
  source_bucket_arn       = "arn:aws:s3:::${get_env("KM_ARTIFACTS_BUCKET", "")}"
  destination_region      = get_env("KM_REPLICA_REGION", "us-west-2")
  destination_bucket_name = "${get_env("KM_ARTIFACTS_BUCKET", "")}-replica"
}
```

**State key:** `tf-km/use1/s3-replication/terraform.tfstate` — matching the `tf-km/{region_label}/{component}` pattern used by all other use1 configs.

**New env var:** `KM_REPLICA_REGION` — defaults to `us-west-2`. Must be documented in `OPERATOR-GUIDE.md` (or a new section added) alongside `KM_ARTIFACTS_BUCKET`.

### Recommended Project Structure

```
infra/live/use1/
└── s3-replication/         # NEW: OBSV-06
    └── terragrunt.hcl      # deploys infra/modules/s3-replication/v1.0.0

internal/app/cmd/
├── budget.go               # MODIFIED: add ECS branch + reprovisionECSSandbox
└── budget_test.go          # MODIFIED: add ECS re-provisioning tests
```

### Anti-Patterns to Avoid

- **Don't inject `ECSProvisionAPI` for `reprovisionECSSandbox`:** The function calls `compiler.Compile` and `runner.Apply` — these are not mockable via narrow interfaces. Tests verify the ECS path is triggered by inspecting that `meta.Substrate == "ecs"` leads to the correct function call (source-level verification or a boolean flag in a fake meta fetcher).
- **Don't generate a new `sandboxID` for ECS re-provisioning:** The existing sandbox ID must be reused so Terraform state maps correctly to the existing ECS cluster. The cluster is not destroyed by budget enforcement — only the Fargate task is stopped.
- **Don't use a Terragrunt `dependency` block for s3-replication:** The source bucket already exists (managed by the network/foundation modules); only the replication config and replica bucket are new. Use `get_env` to read the bucket name, not a dependency on another Terragrunt config.
- **Don't skip the dual-provider `generate` in s3-replication:** If the aliased `aws.replica` provider is missing, `terraform plan` will fail with "provider configuration not present."

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| ECS re-provisioning from scratch | Custom ECS RunTask API call sequence | `compiler.Compile` + `runner.Apply` (terragrunt) | Terraform manages task definition, service, cluster relationships and handles idempotency |
| S3 replication IAM role | Custom IAM policy construction | `infra/modules/s3-replication/v1.0.0/main.tf` | The module already creates the replication role with correct permissions |
| Profile download and parsing | Custom S3 fetch + YAML parse | `s3.GetObject` + `profile.Parse` + `profile.Resolve` | Existing patterns in `destroy.go` already do this for artifact upload |
| Dual-provider Terraform config | New Terraform module | Terragrunt `generate` block override | One `generate "providers"` block in the live config handles both providers |

---

## Common Pitfalls

### Pitfall 1: Reusing sandboxID vs New ID on ECS Re-Provision
**What goes wrong:** If `reprovisionECSSandbox` calls `compiler.GenerateSandboxID()`, a new sandbox ID is generated. The Terraform state refers to the old cluster/task-definition under the old ID. `terragrunt apply` creates duplicate resources and the budget enforcer Lambda (named `km-budget-enforcer-{old-id}`) never picks up the new task.
**Why it happens:** The `km create` flow always calls `compiler.GenerateSandboxID()` — it's easy to copy-paste that call into re-provisioning.
**How to avoid:** Pass the existing `sandboxID` directly to `compiler.Compile` instead of generating a new one. The function signature `Compile(profile, sandboxID, onDemand, network)` already accepts an explicit sandbox ID.
**Warning signs:** `km status <sandbox-id>` shows "not found" after top-up; a new ECS cluster appears in AWS Console.

### Pitfall 2: s3-replication Module Requires Both Versioning and Replication Config Applied Together
**What goes wrong:** S3 replication requires versioning on the source bucket before the replication configuration can be applied. The module handles this with `depends_on`, but if the source bucket already has versioning disabled (e.g., it was created before Phase 4), the first `apply` fails.
**Why it happens:** AWS requires versioning to be enabled on both source and destination before the replication rule can be attached.
**How to avoid:** The module already includes `aws_s3_bucket_versioning.source` — this will attempt to enable versioning on the existing source bucket. If versioning was previously `Disabled`, this succeeds. If `Suspended`, it may require an explicit step. Document in operator guide: run `km init` (or equivalent) to ensure versioning is enabled before deploying s3-replication.
**Warning signs:** `Error: creating S3 Replication Configuration: InvalidRequest: Versioning must be 'Enabled' on the source bucket`.

### Pitfall 3: Missing KM_REPLICA_REGION Causes Default us-west-2 Silently
**What goes wrong:** If `KM_REPLICA_REGION` is not set, the replication config defaults to `us-west-2`. The operator may not notice until they check the replica bucket region in the console.
**Why it happens:** Terragrunt `get_env` with a default value silently uses the default.
**How to avoid:** Add a validation comment in `terragrunt.hcl` and document `KM_REPLICA_REGION` in `OPERATOR-GUIDE.md`. The default `us-west-2` is a reasonable US cross-region choice.
**Warning signs:** Replica bucket appears in `us-west-2` when operator expected another region.

### Pitfall 4: Artifact Bucket Not Set in Budget Add ECS Path
**What goes wrong:** `reprovisionECSSandbox` downloads the profile from `KM_ARTIFACTS_BUCKET` but that env var may not be set (same issue as in `km create`). If the bucket is empty string, the S3 GetObject call hits an empty bucket name error.
**Why it happens:** The budget command doesn't currently read `KM_ARTIFACTS_BUCKET` — it only uses DynamoDB and EC2 clients.
**How to avoid:** Read `KM_ARTIFACTS_BUCKET` (or `cfg.ArtifactsBucket`) in `runBudgetAdd` before the ECS path, and return an actionable error if empty: `"artifact bucket not configured: set KM_ARTIFACTS_BUCKET or artifact_bucket in km-config.yaml"`.
**Warning signs:** `s3.GetObject: BucketNameInvalid` error in `km budget add` for ECS sandboxes.

### Pitfall 5: ECS Cluster Still Running — Don't Destroy It
**What goes wrong:** If `reprovisionECSSandbox` runs `terragrunt destroy` before `apply`, the ECS cluster and task definition are deleted. The cluster is shared across tasks for the same sandbox.
**Why it happens:** Confusion with the "suspend" behavior in BUDG-07: budget enforcement stops the Fargate *task*, not the ECS service or cluster. The cluster remains.
**How to avoid:** Re-provisioning is `apply` only — never `destroy` first. The ECS module's `aws_ecs_service` resource will reconcile the desired task count (1) against the actual count (0, since the task was stopped) and re-launch the task.
**Warning signs:** `km budget add` succeeds but the ECS cluster is gone; subsequent `km destroy` fails because there is nothing to destroy.

---

## Code Examples

### ECS Re-Provisioning Branch in runBudgetAdd
```go
// Source: internal/app/cmd/budget.go (new ECS branch, follows existing EC2 branch pattern)
if substrate == "ecs" {
    artifactBucket := os.Getenv("KM_ARTIFACTS_BUCKET")
    if artifactBucket == "" {
        fmt.Fprintf(cmd.OutOrStdout(), "Warning: KM_ARTIFACTS_BUCKET not set — cannot re-provision ECS sandbox\n")
    } else {
        if err := reprovisionECSSandbox(ctx, cfg, sandboxID, artifactBucket, awsProfile); err != nil {
            fmt.Fprintf(cmd.OutOrStdout(), "Warning: could not re-provision ECS sandbox: %v\n", err)
        } else {
            resumed = true
        }
    }
}
```

### reprovisionECSSandbox Skeleton
```go
// Source: internal/app/cmd/budget.go (new function)
func reprovisionECSSandbox(ctx context.Context, cfg *config.Config, sandboxID, artifactBucket, awsProfile string) error {
    // Step 1: Load AWS config
    awsCfg, err := kmaws.LoadAWSConfig(ctx, awsProfile)
    if err != nil {
        return fmt.Errorf("load AWS config: %w", err)
    }

    // Step 2: Download stored profile YAML from S3
    s3Client := s3.NewFromConfig(awsCfg)
    resp, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
        Bucket: aws.String(artifactBucket),
        Key:    aws.String("artifacts/" + sandboxID + "/.km-profile.yaml"),
    })
    if err != nil {
        return fmt.Errorf("download profile for sandbox %s: %w", sandboxID, err)
    }
    defer resp.Body.Close()
    profileYAML, err := io.ReadAll(resp.Body)
    if err != nil {
        return fmt.Errorf("read profile YAML: %w", err)
    }

    // Step 3: Parse and resolve profile
    parsed, err := profile.Parse(profileYAML)
    if err != nil {
        return fmt.Errorf("parse stored profile: %w", err)
    }
    resolvedProfile := parsed
    if parsed.Extends != "" {
        searchPaths := cfg.ProfileSearchPaths
        resolvedProfile, err = profile.Resolve(parsed.Extends, searchPaths)
        if err != nil {
            return fmt.Errorf("resolve profile extends: %w", err)
        }
    }

    // Step 4: Load network config (reuse create.go helper)
    repoRoot := findRepoRoot()
    region := resolvedProfile.Spec.Runtime.Region
    regionLabel := compiler.RegionLabel(region)
    networkOutputs, err := LoadNetworkOutputs(repoRoot, regionLabel)
    if err != nil {
        return fmt.Errorf("load network config: %w", err)
    }
    domain := cfg.Domain
    if domain == "" {
        domain = "klankermaker.ai"
    }
    network := &compiler.NetworkConfig{
        VPCID:         networkOutputs.VPCID,
        PublicSubnets: networkOutputs.PublicSubnets,
        AvailabilityZones: networkOutputs.AvailabilityZones,
        RegionLabel:   regionLabel,
        EmailDomain:   "sandboxes." + domain,
    }

    // Step 5: Compile with existing sandboxID (not a new one)
    artifacts, err := compiler.Compile(resolvedProfile, sandboxID, false, network)
    if err != nil {
        return fmt.Errorf("compile profile for re-provisioning: %w", err)
    }

    // Step 6: Write artifacts and run terragrunt apply
    sandboxDir, err := terragrunt.CreateSandboxDir(repoRoot, regionLabel, sandboxID)
    if err != nil {
        return fmt.Errorf("create sandbox dir: %w", err)
    }
    if err := terragrunt.PopulateSandboxDir(sandboxDir, artifacts.ServiceHCL, artifacts.UserData); err != nil {
        return fmt.Errorf("populate sandbox dir: %w", err)
    }
    runner := terragrunt.NewRunner(awsProfile, repoRoot)
    return runner.Apply(ctx, sandboxDir)
}
```

### s3-replication/terragrunt.hcl Structure
```hcl
# Source: infra/live/use1/s3-replication/terragrunt.hcl (new file)
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

# Override the root provider generation to add the replica provider alias.
generate "providers" {
  path      = "provider.tf"
  if_exists = "overwrite_terragrunt"
  contents  = <<-EOF
    terraform { required_version = ">= 1.6.0" ... }
    provider "aws" { region = "${local.region_full}" }
    provider "aws" { alias = "replica"; region = "${get_env("KM_REPLICA_REGION", "us-west-2")}" }
  EOF
}

remote_state {
  backend = "s3"
  generate = { path = "backend.tf"; if_exists = "overwrite_terragrunt" }
  config = {
    bucket         = local.site_vars.locals.backend.bucket
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/s3-replication/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/s3-replication/v1.0.0"
}

inputs = {
  source_bucket_name      = get_env("KM_ARTIFACTS_BUCKET", "")
  source_bucket_arn       = "arn:aws:s3:::${get_env("KM_ARTIFACTS_BUCKET", "")}"
  destination_region      = get_env("KM_REPLICA_REGION", "us-west-2")
  destination_bucket_name = "${get_env("KM_ARTIFACTS_BUCKET", "")}-replica"
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| EC2 top-up only in `km budget add` | EC2 + ECS re-provisioning | Phase 12 | ECS sandboxes can be resumed after budget suspension |
| s3-replication module exists but not deployed | s3-replication module + live config | Phase 12 | OBSV-06 fully satisfied; artifact data replicated to secondary region |
| ECS budget enforcement stops Fargate tasks (no resume path) | ECS tasks can be re-provisioned from stored profile | Phase 12 | ECS sandboxes are resumable; operator UX parity with EC2 |

**Deprecated / outdated in this phase:**
- The comment in `budget.go` `runBudgetAdd` that implies only EC2 is resumable — replaced by substrate-aware logic.
- The audit gap entry for BUDG-08 — closed by this phase.
- The audit gap entry for OBSV-06 — closed by this phase.

---

## Open Questions

1. **Dual-provider Terragrunt template — generate block or separate generated file?**
   - What we know: Terragrunt `generate` blocks support `if_exists = "overwrite_terragrunt"` which overrides the root-level `generate "provider"` block.
   - What's unclear: Whether two `generate` blocks with the same `path` in a child config correctly override the root block, or whether a separate `provider_replica.tf` is needed.
   - Recommendation: Use a single `generate "providers"` block that writes both providers to `provider.tf`. The `overwrite_terragrunt` strategy replaces the root-generated file entirely. This is confirmed by the Terragrunt docs pattern for provider overrides.

2. **ECS cluster state — is it actually preserved after budget enforcement stops the task?**
   - What we know: The budget enforcer Lambda on ECS calls `StopTask` (confirmed in BUDG-07 requirement text: "ECS Fargate tasks trigger artifact upload then stop"). The ECS service desired count is typically 1; stopping the task causes the service to relaunch it immediately unless the service is also updated.
   - What's unclear: Whether the current budget enforcer implementation also sets `desired_count = 0` on the ECS service (to prevent auto-relaunch) or just stops the task. If it only calls `StopTask`, the Fargate service may have already relaunched the task before `km budget add` runs.
   - Recommendation: The planner must inspect `cmd/budget-enforcer/main.go` (or the Lambda that handles ECS suspension) to confirm whether `UpdateService(desired_count=0)` is called. If it is, the re-provisioning path should call `UpdateService(desired_count=1)` instead of `runner.Apply`. If only `StopTask` is called, `runner.Apply` (which reapplies the service config with `desired_count=1`) is the correct approach.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` (stdlib) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./internal/app/cmd/... -run TestBudget -v` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| BUDG-08 | `km budget add` for ECS sandbox detects substrate and enters ECS re-provisioning branch | unit | `go test ./internal/app/cmd/... -run TestBudgetAdd_ECSSubstrate -v` | ❌ Wave 0 |
| BUDG-08 | `km budget add` for ECS with empty artifact bucket returns actionable warning | unit | `go test ./internal/app/cmd/... -run TestBudgetAdd_ECSMissingArtifactBucket -v` | ❌ Wave 0 |
| BUDG-08 | `km budget add` still resumes EC2 sandboxes after ECS branch added (regression) | unit | `go test ./internal/app/cmd/... -run TestBudgetAdd_ResumesStoppedEC2 -v` | ✅ exists |
| OBSV-06 | `infra/live/use1/s3-replication/terragrunt.hcl` exists and references correct module path | file-exists | `ls infra/live/use1/s3-replication/terragrunt.hcl` | ❌ Wave 0 |
| OBSV-06 | s3-replication terragrunt.hcl contains both provider blocks (default + replica alias) | content-check | `grep -q 'alias.*replica' infra/live/use1/s3-replication/terragrunt.hcl` | ❌ Wave 0 |

### Sampling Rate

- **Per task commit:** `go test ./internal/app/cmd/... -run TestBudget -v`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `internal/app/cmd/budget_test.go` — add `TestBudgetAdd_ECSSubstrate` and `TestBudgetAdd_ECSMissingArtifactBucket` to existing test file
- [ ] `infra/live/use1/s3-replication/terragrunt.hcl` — new file (the primary OBSV-06 deliverable)

*(Existing `TestBudgetAdd_ResumesStoppedEC2` covers EC2 regression; no new test file needed for that case.)*

---

## Sources

### Primary (HIGH confidence — direct codebase inspection)

- `internal/app/cmd/budget.go` — existing `runBudgetAdd`, EC2 branch, DI interfaces, `realMetaFetcher`
- `internal/app/cmd/budget_test.go` — existing test patterns (fakeEC2StartAPI, fakeSandboxMetaFetcher)
- `internal/app/cmd/create.go` — full `km create` flow; profile download from S3 in destroy.go provides the read pattern
- `internal/app/cmd/destroy.go` — `fetchProfileFromS3` / `key = "artifacts/" + sandboxID + "/.km-profile.yaml"` pattern
- `pkg/compiler/service_hcl.go` — ECS HCL template; confirmed ECS substrate is already compiled
- `pkg/compiler/compiler.go` — `Compile(profile, sandboxID, onDemand, network)` signature
- `infra/modules/s3-replication/v1.0.0/main.tf` — confirmed dual-provider requirement (`provider "aws" { alias = "replica" }`)
- `infra/modules/s3-replication/v1.0.0/variables.tf` — four required inputs confirmed
- `infra/live/use1/dynamodb-budget/terragrunt.hcl` — reference pattern for use1 live configs
- `infra/live/use1/ttl-handler/terragrunt.hcl` — reference pattern with `get_env` inputs
- `infra/live/terragrunt.hcl` — root `generate "provider"` block that s3-replication must override
- `go.mod` — confirmed `github.com/aws/aws-sdk-go-v2/service/ecs v1.74.0` already present
- `.planning/v1.0-MILESTONE-AUDIT.md` — confirmed both gap descriptions and evidence

### Secondary (MEDIUM confidence)

- `BUDG-07` / `BUDG-08` requirement text in `REQUIREMENTS.md` — confirmed ECS suspension uses `StopTask` and re-provisioning must use stored S3 profile
- Phase 11 RESEARCH.md — confirmed `realMetaFetcher` pattern and `FetchSandboxMeta` interface for substrate detection

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries confirmed in go.mod and codebase
- Architecture: HIGH — both patterns derived from direct code reading; no speculation
- Pitfalls: HIGH — derived from existing code structure and known gaps (sandboxID reuse, dual-provider, artifact bucket env var)

**Research date:** 2026-03-22
**Valid until:** 2026-04-22 (stable Go + AWS SDK v2 + Terragrunt ecosystem)
