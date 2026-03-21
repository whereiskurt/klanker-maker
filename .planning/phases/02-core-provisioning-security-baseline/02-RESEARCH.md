# Phase 2: Core Provisioning & Security Baseline - Research

**Researched:** 2026-03-21
**Domain:** Go CLI provisioning commands, Terragrunt orchestration, AWS EC2 spot + ECS Fargate, Security Groups, IMDSv2, SSM secrets, SOPS/KMS, AWS SDK v2 tag-based discovery
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Compiler Design**
- Output format: JSON tfvars (`terraform.tfvars.json`) — unambiguous escaping, easy to generate from Go structs via json.Marshal
- Single tfvars file per sandbox — Terragrunt handles module orchestration via dependencies
- Substrate branching via flag in tfvars: `substrate="ec2"` or `"ecs"` — Terragrunt conditionally includes the right modules. Compiler stays simple.
- Basic user-data bootstrap script generated in Phase 2: SSM agent, IMDSv2 config, basic networking. Phase 3 adds sidecar injection to this script.

**Create/Destroy UX**
- Sandbox IDs: `sb-` prefix + 8 hex chars (e.g. `sb-a1b2c3d4`) — short enough to type, used in AWS tags, state paths, CLI output
- Progress: stream Terraform output in real time — operator sees every resource being created, no mystery
- No local state — rely entirely on Terraform state in S3 + AWS resource tags (`km:sandbox-id`) for discovery
- `km destroy sb-a1b2c3d4` looks up `km:sandbox-id` tag in AWS to find the Terragrunt state path in S3, then runs destroy. Works from any terminal.

**Spot Instance Strategy**
- Spot unavailable: fail with message, don't auto-fallback to on-demand. No surprise costs.
- ECS: `spot: true` → FARGATE_SPOT capacity provider only. `spot: false` → FARGATE only. Clean mapping, same semantics as EC2.
- CLI flag: `km create --on-demand profile.yaml` overrides `spot: true` in the profile for this one sandbox without editing the profile.

**Secrets + GitHub Access**
- Secrets injected as environment variables at boot — user-data script fetches from SSM, exports as env vars. Simple, standard.
- GitHub access via GitHub App installation token stored in SSM SecureString, injected as `GITHUB_TOKEN` env var. Works with HTTPS git operations, rotates automatically.
- SOPS decryption at provision time (not compile time) — compiler writes SSM parameter ARNs into tfvars, user-data script decrypts at boot using instance IAM role. Secrets never in Terraform state.

### Claude's Discretion
- Terragrunt dependency graph structure (how modules reference each other)
- Exact user-data script content for Phase 2 baseline
- AWS SDK usage patterns for tag-based sandbox lookup in `km destroy`
- EC2 spot request configuration details (request type, interruption behavior)
- Security group rule specifics (which ports, which CIDRs)

### Deferred Ideas (OUT OF SCOPE)
None — discussion stayed within phase scope
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| PROV-01 | Operator can run `km create <profile>` to compile profile into Terragrunt inputs and provision EC2 + VPC + IAM | `pkg/compiler/` (new) outputs `terraform.tfvars.json`; `pkg/terragrunt/` runner invokes `terragrunt apply`; streams stdout |
| PROV-02 | Operator can run `km destroy <sandbox-id>` to cleanly tear down all sandbox resources | AWS SDK `resourcegroupstaggingapi` lookups sandbox by `km:sandbox-id` tag; resolves S3 state path; invokes `terragrunt destroy`; EC2 spot: cancel the spot request separately |
| PROV-08 | Every sandbox resource is tagged with `km:sandbox-id` for tracking and cost attribution | All six Terraform modules already emit `km:sandbox-id` tag; `aws_ec2_tag` resources propagate tags from spot request to actual instance |
| PROV-09 | Operator can specify substrate (`ec2` or `ecs`) in the profile's `runtime.substrate` field | `substrate` field exists in `RuntimeSpec`; compiler outputs `substrate_module` key in `service.hcl`; Terragrunt template already branches via `local.svc_config.locals.substrate_module` |
| PROV-10 | ECS substrate provisions Fargate task with sidecar containers defined in the task definition | ecs-cluster, ecs-task, ecs-service modules already exist; Phase 2 uses placeholder sidecar images; real sidecar images are Phase 3 |
| PROV-11 | EC2 sandboxes use spot instances by default; on-demand fallback is configurable per profile | `aws_spot_instance_request` with `wait_for_fulfillment=true`; `spot_type="one-time"`; `--on-demand` CLI flag overrides profile |
| PROV-12 | ECS sandboxes use Fargate Spot capacity provider by default; on-demand fallback is configurable per profile | `aws_ecs_cluster_capacity_providers` already configures FARGATE_SPOT as default; service `capacity_provider_strategy` in ecs-service module controls per-sandbox override |
| NETW-01 | Security Groups enforce egress restrictions as the primary enforcement layer | network module `sandbox_mgmt` SG has no egress rules; compiler adds `aws_security_group_rule` resources scoped to profile's `allowedHosts` CIDRs |
| NETW-04 | IAM role is session-scoped with configurable duration and region lock | `aws_iam_role` with `MaxSessionDuration`; `aws_iam_role_policy` with `Condition: aws:RequestedRegion`; populated from `identity.allowedRegions` and `identity.roleSessionDuration` |
| NETW-05 | IMDSv2 is enforced (`http-tokens=required`) on all sandbox EC2 instances | `metadata_options { http_tokens = "required", http_put_response_hop_limit = 1 }` already in ec2spot module; verify and protect from future changes |
| NETW-06 | Secrets are injected via SSM Parameter Store with allowlist of permitted secret refs | Compiler writes secret path ARNs into tfvars; user-data `aws ssm get-parameter --with-decryption`; IAM role scoped to exact parameter paths |
| NETW-07 | SOPS encrypts secrets at rest with KMS keys provisioned as part of Klanker Maker infrastructure | `alias/km-sops` KMS key already exists; SOPS-encrypted `.sops.json` files; site.hcl already handles SOPS decrypt on-the-fly for Terragrunt |
| NETW-08 | GitHub source access controls allowlist repos, refs, and permissions (clone/fetch/push) | GitHub App token in SSM SecureString (`/km/github/app-token`); injected as `GITHUB_TOKEN`; user-data git config scoped to `allowedRepos` |
</phase_requirements>

---

## Summary

Phase 2 connects the Phase 1 schema+modules foundation to live AWS infrastructure. The work divides into three parallel streams: (1) the Go `pkg/compiler/` package that translates a `SandboxProfile` struct into a `terraform.tfvars.json` file and a `service.hcl` file; (2) the Go `pkg/terragrunt/` runner that streams `terragrunt apply/destroy` output and handles sandbox directory lifecycle; and (3) the Go `pkg/aws/` package that queries the AWS Resource Groups Tagging API to resolve sandbox state from tag to S3 state path. These three packages converge in `internal/app/cmd/create.go` and `destroy.go`.

The Terraform modules are already in place from Phase 1. The key insight from inspecting them is that the security group egress rules are intentionally empty — the comment "Phase 2 profile compiler adds per-profile egress rules" appears in both `network/v1.0.0/main.tf` and `ec2spot/v1.0.0/main.tf`. Phase 2 must add `aws_security_group_rule` resources (via tfvars) for egress. The modules are not modified — the compiler generates these rules as Terraform resource definitions and writes them into the sandbox-specific `terragrunt.hcl` inputs block, or better, as a separate `sg_rules.tf.json` file in the sandbox working directory.

The destroy path has one known complication: `aws_spot_instance_request` with `wait_for_fulfillment=true` does NOT terminate the actual EC2 instance when the Terraform resource is destroyed — it only cancels the spot request. Phase 2 must add explicit EC2 instance termination before running `terragrunt destroy`, using the instance ID stored in Terraform state outputs.

**Primary recommendation:** Build compiler → terragrunt runner → create command in that order. Destroy is simpler than create — build it after create works end-to-end on at least one substrate. ECS and EC2 can be built in parallel since the Terraform modules are independent.

---

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| github.com/spf13/cobra | v1.8.1 | CLI commands (`km create`, `km destroy`) | Already in go.mod; established Cobra pattern from validate.go |
| github.com/spf13/viper | v1.19.0 | Config binding | Already in go.mod; Config struct DI pattern established |
| github.com/rs/zerolog | v1.33.0 | Structured logging | Already in go.mod; zero-alloc JSON for streaming output |
| github.com/aws/aws-sdk-go-v2 | latest | AWS API calls for tag-based discovery and SSM | v2 is context-aware; v1 is EOL; not yet in go.mod — add now |
| github.com/aws/aws-sdk-go-v2/service/ec2 | module version | EC2 describe-instances, terminate-instances | Needed for spot instance teardown and instance ID lookup |
| github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi | module version | Tag-based sandbox discovery for `km destroy` | Replaces scanning all of EC2/ECS — one API call finds all tagged resources |
| github.com/aws/aws-sdk-go-v2/service/ssm | module version | SSM parameter lookup, secret path verification | Secrets injection validation at create time |
| github.com/google/uuid | latest | Sandbox ID generation | Generates the 8-hex-char suffix for `sb-XXXXXXXX` IDs |
| encoding/json | stdlib | JSON tfvars generation | json.Marshal from Go struct → `terraform.tfvars.json`; no external dep needed |
| os/exec | stdlib | Terragrunt subprocess execution | Streams stdout/stderr in real time via pipe |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| github.com/aws/aws-sdk-go-v2/config | module version | AWS credential loading | Loads credentials from SSO profile or environment |
| github.com/aws/aws-sdk-go-v2/service/sts | module version | STS GetCallerIdentity | Pre-flight check that AWS credentials are valid before any apply |
| github.com/aws/aws-sdk-go-v2/service/s3 | module version | State path construction + existence check | Optional: verify Terraform state exists before running destroy |
| text/template | stdlib | user-data script generation | Populate the bootstrap shell script with sandbox-specific values |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `terraform.tfvars.json` via encoding/json | hashicorp/hcl/v2 to generate HCL | JSON is unambiguous and locked as the decision; hcl/v2 only needed if reading existing HCL |
| `os/exec` streaming Terragrunt | Terraform Go library (terraform-exec) | terraform-exec adds complexity; `os/exec` with `io.Copy(os.Stdout, cmd.Stdout)` is 20 lines and sufficient |
| resourcegroupstaggingapi | EC2 describe-instances with tag filter | Tagging API works across ALL AWS resource types in one call; EC2-only filter misses VPC, IAM, ECS resources |

**Installation:**
```bash
go get github.com/aws/aws-sdk-go-v2/config@latest
go get github.com/aws/aws-sdk-go-v2/service/ec2@latest
go get github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi@latest
go get github.com/aws/aws-sdk-go-v2/service/ssm@latest
go get github.com/aws/aws-sdk-go-v2/service/sts@latest
go get github.com/google/uuid@latest
```

---

## Architecture Patterns

### Recommended Project Structure

```
pkg/
├── compiler/               # NEW in Phase 2
│   ├── compiler.go         # Compile(profile, sandboxID) → CompiledArtifacts
│   ├── tfvars.go           # Generate terraform.tfvars.json from profile
│   ├── service_hcl.go      # Generate service.hcl (sandbox_id, substrate_module, module_inputs)
│   └── userdata.go         # Generate user-data.sh (SSM agent, IMDSv2, basic bootstrap)
├── terragrunt/             # NEW in Phase 2
│   ├── runner.go           # Apply(sandboxDir) / Destroy(sandboxDir) — exec + stream
│   └── sandbox.go          # CreateSandboxDir, PopulateSandboxDir, CleanupSandboxDir
├── aws/                    # NEW in Phase 2
│   ├── discover.go         # FindSandboxByID(ctx, sandboxID) → SandboxLocation
│   └── spot.go             # TerminateSpotInstance(ctx, instanceID) before destroy
internal/app/cmd/
├── create.go               # NewCreateCmd — validate → compile → create dir → apply
└── destroy.go              # NewDestroyCmd — discover → maybe terminate spot → destroy
infra/live/sandboxes/
└── <sandbox-id>/           # Created at provision time, deleted at destroy time
    ├── service.hcl          # Written by compiler: sandbox_id, substrate_module, module_inputs
    └── terragrunt.hcl       # Symlink or copy of _template/terragrunt.hcl
```

### Pattern 1: Compiler Output Structure

**What:** `pkg/compiler` reads a `*profile.SandboxProfile` and a sandbox ID, and writes two files into the sandbox directory: `service.hcl` (Terragrunt identity and module inputs) and `user-data.sh` (EC2 bootstrap script). The JSON tfvars approach means all module inputs flow through `module_inputs` in `service.hcl`.

**When to use:** Always. The compiler is a pure function with no AWS side effects — testable without AWS credentials.

**Example — compiler.go interface:**
```go
// pkg/compiler/compiler.go
type CompiledArtifacts struct {
    ServiceHCL string // content for service.hcl
    UserData   string // content for user-data.sh (EC2 only; empty for ECS)
}

func Compile(p *profile.SandboxProfile, sandboxID string, onDemand bool) (*CompiledArtifacts, error) {
    substrate := p.Spec.Runtime.Substrate // "ec2" or "ecs"
    svcHCL, err := generateServiceHCL(p, sandboxID, substrate, onDemand)
    if err != nil {
        return nil, fmt.Errorf("generate service.hcl: %w", err)
    }
    userData := ""
    if substrate == "ec2" {
        userData, err = generateUserData(p, sandboxID)
        if err != nil {
            return nil, fmt.Errorf("generate user-data: %w", err)
        }
    }
    return &CompiledArtifacts{ServiceHCL: svcHCL, UserData: userData}, nil
}
```

**Example — service.hcl generation for EC2:**
```go
// pkg/compiler/service_hcl.go — writes the service.hcl locals block
// Use text/template with strict escaping; never fmt.Sprintf for HCL values.

const ec2ServiceHCLTemplate = `
locals {
  sandbox_id       = "{{ .SandboxID }}"
  substrate_module = "ec2spot"

  module_inputs = {
    sandbox_id  = "{{ .SandboxID }}"
    vpc_id      = "{{ .VPCID }}"   # looked up or created by network module
    ec2spots    = [{
      count         = 1
      region        = "{{ .Region }}"
      sandbox_id    = "{{ .SandboxID }}"
      instance_type = "{{ .InstanceType }}"
      spot_price_multiplier = 1.05
      spot_price_offset     = 0.0
      user_data     = file("${get_terragrunt_dir()}/user-data.sh")
    }]
    {{ range .PublicSubnets }}
    public_subnets     = ["{{ . }}"]
    {{ end }}
    availability_zones = ["{{ .AZ }}"]
  }
}
`
```

**Note:** For Phase 2, the VPC is created by a prerequisite network module apply. The compiler hardcodes or looks up the VPC/subnet IDs from a prior Terragrunt output. The design decision from the template (single terragrunt.hcl calling one module) means Phase 2 runs the network module first, captures its outputs, then runs ec2spot/ecs-cluster. The Terragrunt `dependency` block handles this cleanly.

### Pattern 2: Terragrunt Runner with Real-time Streaming

**What:** `pkg/terragrunt.Runner` wraps `os/exec` to run `terragrunt apply` or `terragrunt destroy` with stdout/stderr piped directly to the operator terminal.

**When to use:** Always for provisioning. Do not buffer output — the operator needs real-time feedback on a 2-5 minute operation.

**Example:**
```go
// pkg/terragrunt/runner.go
func (r *Runner) Apply(ctx context.Context, sandboxDir string) error {
    cmd := exec.CommandContext(ctx, "terragrunt", "apply", "-auto-approve")
    cmd.Dir = sandboxDir
    cmd.Stdout = os.Stdout  // stream directly — operator sees live output
    cmd.Stderr = os.Stderr
    cmd.Env = append(os.Environ(),
        "AWS_PROFILE=klanker-terraform",
        "KMGUID="+r.RandomSuffix, // injected from site.hcl
    )
    return cmd.Run()
}
```

**Key:** `cmd.Stdout = os.Stdout` is all that's needed for real-time streaming. Do not use `CombinedOutput()` — that buffers everything until completion.

### Pattern 3: Tag-Based Sandbox Discovery for `km destroy`

**What:** `km destroy sb-a1b2c3d4` has no local state. It calls AWS Resource Groups Tagging API to find all resources with `km:sandbox-id=sb-a1b2c3d4`, then derives the Terraform state path from the sandbox ID to reconstruct the sandbox directory for `terragrunt destroy`.

**When to use:** Every destroy and any future `km list`/`km status` call.

**Example:**
```go
// pkg/aws/discover.go
func FindSandboxByID(ctx context.Context, cfg aws.Config, sandboxID string) (*SandboxLocation, error) {
    client := resourcegroupstaggingapi.NewFromConfig(cfg)
    out, err := client.GetResources(ctx, &resourcegroupstaggingapi.GetResourcesInput{
        TagFilters: []types.TagFilter{
            {Key: aws.String("km:sandbox-id"), Values: []string{sandboxID}},
        },
    })
    if err != nil {
        return nil, fmt.Errorf("tag lookup for %s: %w", sandboxID, err)
    }
    if len(out.ResourceTagMappingList) == 0 {
        return nil, fmt.Errorf("sandbox %s not found (no tagged resources)", sandboxID)
    }
    // S3 state path is deterministic: tf-km-state-use1/tf-km/sandboxes/<sandbox-id>/terraform.tfstate
    return &SandboxLocation{
        SandboxID:     sandboxID,
        S3StatePath:   fmt.Sprintf("tf-km/sandboxes/%s", sandboxID),
        ResourceCount: len(out.ResourceTagMappingList),
    }, nil
}
```

### Pattern 4: Spot Instance Teardown — Terminate Instance Before Terraform Destroy

**What:** `aws_spot_instance_request` destroy cancels the request but does NOT terminate the EC2 instance. The instance keeps running, accruing cost. Before running `terragrunt destroy`, explicitly terminate the EC2 instance using its instance ID from Terraform state outputs.

**When to use:** Every destroy of an EC2 substrate sandbox.

**Example:**
```go
// pkg/aws/spot.go
func TerminateSpotInstance(ctx context.Context, cfg aws.Config, instanceID string) error {
    client := ec2.NewFromConfig(cfg)
    _, err := client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
        InstanceIds: []string{instanceID},
    })
    if err != nil {
        return fmt.Errorf("terminate EC2 instance %s: %w", instanceID, err)
    }
    // Wait for termination to complete before letting Terraform destroy proceed
    waiter := ec2.NewInstanceTerminatedWaiter(client)
    return waiter.Wait(ctx, &ec2.DescribeInstancesInput{
        InstanceIds: []string{instanceID},
    }, 5*time.Minute)
}
```

**How to get instance ID:** Read from Terraform state via `terragrunt output -json` before destroy. The ec2spot module exposes `spot_instance_id` in its outputs.

### Pattern 5: Terragrunt Dependency Chain for Multi-Module Sandbox

**What:** A full sandbox requires multiple Terraform modules applied in order: `network` first (creates VPC, subnets, security groups), then `ec2spot` or `ecs-cluster + ecs-task + ecs-service`. Terragrunt `dependency` blocks express this ordering.

**When to use:** Phase 2 must decide between two approaches:
1. **Single-module** (simplest): Run all resources in one `terragrunt.hcl` pointing at a combined module. Works but less modular.
2. **Multi-directory** (recommended): Each module gets its own subdirectory under `infra/live/sandboxes/<id>/`, with `dependency` blocks wiring outputs to inputs.

**Recommended structure (Claude's discretion):**
```
infra/live/sandboxes/sb-a1b2c3d4/
├── network/
│   ├── terragrunt.hcl     # sources infra/modules/network/v1.0.0
│   └── service.hcl        # vpc config
├── compute/               # ec2spot OR (ecs-cluster + ecs-task + ecs-service)
│   ├── terragrunt.hcl     # depends_on network; sources ec2spot or ecs-* module
│   └── service.hcl        # compute config with user_data ref
└── secrets/               # optional: if profile.sourceAccess.github is set
    ├── terragrunt.hcl
    └── service.hcl
```

**The `dependency` block pattern:**
```hcl
# compute/terragrunt.hcl
dependency "network" {
  config_path = "../network"
  mock_outputs_allowed_terraform_commands = ["validate", "plan"]
  mock_outputs = {
    vpc_id         = "vpc-mock"
    public_subnets = ["subnet-mock"]
  }
}

inputs = merge(local.svc_config.locals.module_inputs, {
  vpc_id         = dependency.network.outputs.vpc_id
  public_subnets = dependency.network.outputs.public_subnets
  availability_zones = dependency.network.outputs.availability_zones
})
```

### Anti-Patterns to Avoid

- **Monolithic single-module sandbox:** Putting network + compute into one module makes it impossible to apply them separately (e.g., when adding a new ECS service module). Keep modules independent with dependency blocks.
- **Generating HCL by string interpolation:** service.hcl is generated via `text/template` with strict escaping. Never `fmt.Sprintf("%s", value)` into HCL — use quoted string template functions.
- **Running `terragrunt destroy` without first terminating EC2 instances:** The `aws_spot_instance_request` resource leaves a running instance. Always terminate explicitly, wait for confirmed termination, then destroy.
- **Storing sandbox working directory locally:** The sandbox directory is written to `infra/live/sandboxes/<id>/` which is inside the git repo. Do NOT gitignore it — it must survive across terminal sessions. The template directory `_template/` is committed; per-sandbox directories are gitignored (add to `.gitignore`: `infra/live/sandboxes/sb-*/`).
- **Using `spot_type = "persistent"` for sandboxes:** Persistent spot requests relaunch automatically on interruption. Sandboxes should use `spot_type = "one-time"` — if interrupted, fail clearly rather than silently restarting.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| AWS credential loading | Custom credential chain | `aws-sdk-go-v2/config.LoadDefaultConfig` with profile | SDK handles SSO token refresh, assume-role, env vars automatically |
| Concurrent Terraform state locking | Custom mutex | DynamoDB lock table (already in site.hcl) | DynamoDB lock is already configured; Terragrunt uses it automatically |
| Real-time output streaming | Custom goroutines + channels | `cmd.Stdout = os.Stdout` directly | One line; handles backpressure; no race conditions |
| Tag-based resource lookup | EC2 DescribeInstances iteration | `resourcegroupstaggingapi.GetResources` | One API call returns ALL resource types tagged with sandbox-id |
| Sandbox ID generation | Custom random hex | `github.com/google/uuid` + format | `uuid.New().String()[:8]` gives 8 random hex chars; collision-proof |
| SOPS decryption in Go | Reimplement SOPS decrypt | `sops --decrypt` CLI via `os/exec` | SOPS has KMS auth built in; the CLI is already available; no Go library needed for Phase 2 (compiler just writes ARNs, decryption happens at EC2 boot) |
| user-data base64 encoding | Manual base64 | Pass plain text; Terraform/AWS handles encoding | `aws_spot_instance_request.user_data` accepts plain text; AWS base64-encodes it internally |

**Key insight:** The Terraform modules handle nearly all AWS resource management. The Go CLI is an orchestrator, not a provisioner — it generates inputs and invokes Terragrunt. Resist the urge to call AWS APIs for what Terraform already handles.

---

## Common Pitfalls

### Pitfall 1: `aws_spot_instance_request` Destroy Leaves Running Instance

**What goes wrong:** `terraform destroy` destroys the `aws_spot_instance_request` resource (cancels the request) but the fulfilled EC2 instance keeps running. Operator sees "Destroy complete" but AWS console shows a live instance. Cost accrues.

**Why it happens:** The ec2spot module uses `aws_spot_instance_request`, not `aws_instance`. The Terraform provider's destroy for this resource type only cancels the request, not the instance.

**How to avoid:** Before running `terragrunt destroy` on an EC2 sandbox:
1. Read the instance ID from Terraform state: `terragrunt output -json | jq -r '.spot_instance_id.value'`
2. Call `ec2.TerminateInstances` via AWS SDK
3. Wait for `InstanceStateName = "terminated"` using `ec2.NewInstanceTerminatedWaiter`
4. Only then run `terragrunt destroy`

**Warning signs:** EC2 instances visible in AWS console after `km destroy` reports success; cost anomalies in Cost Explorer for sandbox instance types.

### Pitfall 2: Security Group Has No Egress Rules — Sandbox Can't Reach SSM or SSM Agent Can't Phone Home

**What goes wrong:** The `sandbox_mgmt` security group in the network module has zero egress rules (by design — Phase 2 adds them). A sandbox with no egress rules cannot send outbound traffic at all, including to the AWS Systems Manager endpoints needed for SSM agent to function. The EC2 instance starts but SSM agent can't connect → operator has no access to the instance.

**Why it happens:** "No egress rules" means AWS default-deny for explicit security groups. SSM requires HTTPS (443) egress to `ssm.<region>.amazonaws.com`, `ec2messages.<region>.amazonaws.com`, and `ssmmessages.<region>.amazonaws.com`.

**How to avoid:** The compiler MUST add these egress rules to every EC2 sandbox as non-negotiable baseline:
- TCP 443 egress to `com.amazonaws.us-east-1.ssm` VPC endpoint OR to `0.0.0.0/0` for port 443 initially
- The SG-first enforcement model for Phase 2 baseline: allow 443 egress (Phase 3 tightens this when proxy sidecars handle filtering)

Phase 2 baseline security group egress rules:
```hcl
# Minimum egress for SSM agent + basic operation
# Phase 3 tightens this when proxy sidecars enforce allowlists
egress_rules = [
  { from_port = 443, to_port = 443, protocol = "tcp", cidr_blocks = ["0.0.0.0/0"], description = "HTTPS for SSM agent and package downloads" },
  { from_port = 53,  to_port = 53,  protocol = "udp", cidr_blocks = ["0.0.0.0/0"], description = "DNS resolution" },
]
```

**Warning signs:** EC2 instance launched but `aws ssm describe-instance-information` doesn't show the instance as registered; instance state is "running" but SSM agent status is offline.

### Pitfall 3: Terragrunt Provider Lock File Corruption Under Concurrent Creates

**What goes wrong:** Two simultaneous `km create` commands share a provider plugin cache; concurrent `terragrunt init` corrupts `.terraform.lock.hcl` in the shared cache, causing both to fail with "inconsistent dependency lock file".

**Why it happens:** Default Terraform behavior shares a provider cache at `~/.terraform.d/plugins`. Concurrent inits race on writing the lock file.

**How to avoid:** Each sandbox gets its own `.terraform/` directory because it's in its own subdirectory (`infra/live/sandboxes/<id>/`). Do NOT set `TF_PLUGIN_CACHE_DIR` globally. If caching is needed, use Terragrunt's Provider Cache Server: `terragrunt provider-cache-server` (available in Terragrunt 0.77.x). Pin provider versions explicitly in all module `versions.tf` files.

**Warning signs:** "inconsistent lock file" in Terragrunt output; failures only seen when two creates run simultaneously; passes in serial.

### Pitfall 4: Destroy Requires Profile File That Was Deleted or Moved

**What goes wrong:** `km destroy sb-a1b2c3d4` is implemented to look up the original profile to reconstruct tfvars. The profile was deleted. Destroy fails.

**Why it happens:** Destroy implemented as a mirror of create — re-run compilation then destroy. But destroy doesn't need to recompile; it needs the already-compiled state.

**How to avoid:** The locked decision is to rely on S3 state + AWS tags, not local profile files. The sandbox directory `infra/live/sandboxes/<id>/` is written at create time and contains the compiled `service.hcl`. For destroy:
1. Use the tag lookup to confirm the sandbox exists
2. Reconstruct (or fetch) the sandbox directory from the repo
3. Run `terragrunt destroy` in that directory

The sandbox directory itself (in `infra/live/sandboxes/<id>/`) survives as long as the git repo is intact. For cross-machine destroy, the sandbox dir needs to be re-created from the S3 state path — the state path is deterministic from the sandbox ID.

### Pitfall 5: ECS Spot — `capacity_provider_strategy` Not Set on Service Overrides Cluster Default

**What goes wrong:** The ecs-cluster module sets FARGATE_SPOT as the cluster default capacity provider. But if the ecs-service module's service definition doesn't explicitly set `capacity_provider_strategy`, some AWS regions use FARGATE instead of FARGATE_SPOT for the task. The "default" behavior is region-dependent.

**Why it happens:** ECS cluster capacity provider defaults are advisory, not enforced. The service must explicitly declare its strategy.

**How to avoid:** The compiler must always emit an explicit `capacity_provider_strategy` in the ecs-service inputs:
- `spot: true` → `[{capacity_provider = "FARGATE_SPOT", weight = 1, base = 0}]`
- `spot: false` → `[{capacity_provider = "FARGATE", weight = 1, base = 0}]`

### Pitfall 6: IMDSv2 Hop Limit = 1 Blocks SSM Agent

**What goes wrong:** IMDSv2 `http_put_response_hop_limit = 1` is set in the ec2spot module (already present). This means containers or child processes with more than 1 network hop from the instance cannot reach IMDS. For Phase 2 (no containers on EC2), `hop_limit = 1` is fine — SSM agent runs directly on the host. Do not change this for Phase 2.

**Phase 3 note:** When EC2 sidecars are added (running as systemd services on the host), they also run at hop limit 1 and can reach IMDS. Only containerized workloads would need hop limit 2. Since Phase 2 EC2 has no containers, leave at 1.

**Warning signs:** Only an issue when containers are added to EC2. Document the hop limit setting in the ec2spot module comment for the Phase 3 planner.

---

## Code Examples

### Sandbox ID Generation

```go
// pkg/compiler/compiler.go
import "github.com/google/uuid"

// GenerateSandboxID returns a sandbox ID like "sb-a1b2c3d4"
func GenerateSandboxID() string {
    id := uuid.New().String()
    // Take first 8 hex chars (UUID without dashes starts with 8 hex chars)
    hex := strings.Replace(id, "-", "", -1)[:8]
    return "sb-" + hex
}
```

### JSON tfvars Generation (locked decision: JSON not HCL)

```go
// pkg/compiler/tfvars.go
type EC2SpotTFVars struct {
    KMLabel           string     `json:"km_label"`
    KMRandomSuffix    string     `json:"km_random_suffix"`
    RegionLabel       string     `json:"region_label"`
    RegionFull        string     `json:"region_full"`
    SandboxID         string     `json:"sandbox_id"`
    VPCID             string     `json:"vpc_id"`
    PublicSubnets     []string   `json:"public_subnets"`
    AvailabilityZones []string   `json:"availability_zones"`
    EC2Spots          []EC2Spot  `json:"ec2spots"`
}

func GenerateEC2TFVars(p *profile.SandboxProfile, sandboxID, vpcID string, onDemand bool) ([]byte, error) {
    vars := EC2SpotTFVars{
        KMLabel:    "km",
        SandboxID:  sandboxID,
        RegionFull: p.Spec.Runtime.Region,
        // ... populate from profile
    }
    if !onDemand && p.Spec.Runtime.Spot {
        // spot: use default spot configuration
    }
    return json.MarshalIndent(vars, "", "  ")
}
```

### AWS SDK v2 Config Loading

```go
// pkg/aws/client.go
// Source: aws-sdk-go-v2 official docs
import (
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/ec2"
)

func LoadAWSConfig(ctx context.Context, profile string) (aws.Config, error) {
    return config.LoadDefaultConfig(ctx,
        config.WithSharedConfigProfile(profile), // e.g. "klanker-terraform"
        config.WithRegion("us-east-1"),
    )
}
```

### Resource Groups Tagging API — Find Sandbox Resources

```go
// pkg/aws/discover.go
import (
    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
    tagtypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

func FindSandboxResources(ctx context.Context, cfg aws.Config, sandboxID string) ([]string, error) {
    client := resourcegroupstaggingapi.NewFromConfig(cfg)
    out, err := client.GetResources(ctx, &resourcegroupstaggingapi.GetResourcesInput{
        TagFilters: []tagtypes.TagFilter{
            {
                Key:    aws.String("km:sandbox-id"),
                Values: []string{sandboxID},
            },
        },
    })
    if err != nil {
        return nil, err
    }
    arns := make([]string, 0, len(out.ResourceTagMappingList))
    for _, r := range out.ResourceTagMappingList {
        arns = append(arns, aws.ToString(r.ResourceARN))
    }
    return arns, nil
}
```

### user-data Baseline Script (Phase 2, EC2 only)

```bash
#!/bin/bash
# Phase 2 baseline user-data: SSM agent + IMDSv2 verification
# Phase 3 extends this with sidecar injection + iptables rules
set -euo pipefail

SANDBOX_ID="{{ .SandboxID }}"
REGION="{{ .Region }}"

# Update and install SSM agent (Amazon Linux 2023 includes it; ensure latest)
dnf update -y --quiet
dnf install -y amazon-ssm-agent

# Start SSM agent
systemctl enable amazon-ssm-agent
systemctl start amazon-ssm-agent

# Verify IMDSv2 is enforced (fail loudly if not)
TOKEN=$(curl -s -f -X PUT "http://169.254.169.254/latest/api/token" \
    -H "X-aws-ec2-metadata-token-ttl-seconds: 21600")
if [ -z "$TOKEN" ]; then
    echo "ERROR: IMDSv2 token acquisition failed — instance metadata not available"
    exit 1
fi
echo "IMDSv2 verified: token acquired successfully"

# Inject secrets from SSM as environment variables
# Each allowed secret path is fetched and exported
{{ range .AllowedSecretPaths }}
SECRET_VALUE=$(aws ssm get-parameter \
    --name "{{ . }}" \
    --with-decryption \
    --region "${REGION}" \
    --query "Parameter.Value" \
    --output text 2>/dev/null || echo "")
if [ -n "${SECRET_VALUE}" ]; then
    export "$(basename {{ . }})=${SECRET_VALUE}"
    echo "export $(basename {{ . }})=..." >> /etc/environment
fi
{{ end }}

# Signal ready
echo "SANDBOX_READY sandbox_id=${SANDBOX_ID}" | logger -t km-sandbox
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `aws_instance` with spot_instance_request_id | `aws_spot_instance_request` + `aws_ec2_tag` | ec2spot module already uses this | Need explicit terminate-before-destroy workaround |
| Terraform workspaces for isolation | Terragrunt per-directory | Phase 1 established | State isolation is per-directory, not per-workspace |
| `TF_PLUGIN_CACHE_DIR` shared cache | No shared cache (each sandbox dir is isolated) | Terragrunt 0.50+ | Concurrent creates don't collide |
| `http-tokens = optional` (IMDSv1 default) | `http-tokens = required` (IMDSv2 enforced) | ec2spot module already set | IMDSv2 is already in the module; do not regress |
| SSH ingress for instance access | SSM-only access (no key pairs) | Phase 1 removed SSH from ec2spot | No key pairs, no SSH SG rule, SSM only |

**Deprecated/outdated patterns (do not introduce):**
- `aws_key_pair` resources for sandbox EC2 instances: removed from ec2spot module; SSH access not needed with SSM
- `spot_type = "persistent"`: do not use for sandboxes; use `"one-time"` to prevent auto-relaunch on interruption
- VPC endpoint for SSM instead of 443 egress: valid for production hardening but Phase 2 uses 443 egress to avoid VPC endpoint cost/complexity; Phase 3 can tighten
- `TF_PLUGIN_CACHE_DIR` environment variable: do not set globally; each sandbox dir manages its own `.terraform/`

---

## Open Questions

1. **VPC per sandbox vs. shared VPC**
   - What we know: The network module creates a full VPC per invocation. Each `km create` with EC2 creates a new VPC (3 subnets, IGW, route tables, SGs). Cost: ~$0/month for VPC itself, but NAT gateway costs $30+/month if enabled.
   - What's unclear: Is a full VPC per sandbox the right granularity for Phase 2, or should there be a shared "sandbox VPC" that's provisioned once?
   - Recommendation: Phase 2 uses one VPC per sandbox (the existing module does this). NAT gateway is disabled by default (`nat_gateway.enabled = false`). Sandboxes use public subnets with direct internet access gated by security groups. This matches the Phase 1 module design (public subnet + IGW, no NAT).

2. **Terragrunt apply: single directory or run-all**
   - What we know: The template has a single `terragrunt.hcl`. Multi-module design requires multiple directories and `terragrunt run-all apply`.
   - What's unclear: Should Phase 2 use `run-all` across network + compute subdirectories, or inline all resources into a single module?
   - Recommendation: Phase 2 uses the single-directory approach with the existing template (network + compute in one Terragrunt call sourcing one module). Phase 3 can refactor to multi-directory when secrets module needs to be added. This minimizes complexity for initial working end-to-end flow.

3. **`--on-demand` CLI flag storage in tfvars**
   - What we know: The `--on-demand` flag overrides `spot: true` in the profile for the current create only.
   - What's unclear: How to communicate this to the compiler (override before compilation, or pass as a separate flag to compiler).
   - Recommendation: Pass `onDemand bool` as a parameter to `Compile()` directly. The compiler checks `onDemand || !p.Spec.Runtime.Spot` to determine if spot should be used. This keeps the profile struct immutable.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib), `go test ./...` |
| Config file | none — Go stdlib test runner |
| Quick run command | `go test ./pkg/compiler/... ./pkg/terragrunt/... ./pkg/aws/... -run Unit -short` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| PROV-01 | `Compile()` produces valid `service.hcl` and `user-data.sh` from a profile | unit | `go test ./pkg/compiler/... -run TestCompile -v` | ❌ Wave 0 |
| PROV-01 | `km create` runs end-to-end and provisions EC2 sandbox | manual/smoke | manual AWS deploy + `aws ec2 describe-instances` | ❌ manual only |
| PROV-02 | `km destroy` finds sandbox by tag and destroys all resources | manual/smoke | manual AWS destroy + `aws resourcegroupstaggingapi get-resources` | ❌ manual only |
| PROV-08 | All compiled resources include `km:sandbox-id` tag in their tfvars/module inputs | unit | `go test ./pkg/compiler/... -run TestTagging -v` | ❌ Wave 0 |
| PROV-09 | Compiler outputs `substrate_module = "ec2spot"` for EC2, `"ecs-cluster"` for ECS | unit | `go test ./pkg/compiler/... -run TestSubstrateRouting -v` | ❌ Wave 0 |
| PROV-10 | ECS compiler output includes sidecar container placeholders in module inputs | unit | `go test ./pkg/compiler/... -run TestECSContainerList -v` | ❌ Wave 0 |
| PROV-11 | Compiler emits spot config by default; `onDemand=true` removes spot config | unit | `go test ./pkg/compiler/... -run TestSpotFlag -v` | ❌ Wave 0 |
| PROV-12 | ECS compiler emits `FARGATE_SPOT` strategy when `spot=true`, `FARGATE` when `spot=false` | unit | `go test ./pkg/compiler/... -run TestFargateSpotStrategy -v` | ❌ Wave 0 |
| NETW-01 | EC2 compiler output includes egress SG rules for HTTPS+DNS | unit | `go test ./pkg/compiler/... -run TestSGEgressRules -v` | ❌ Wave 0 |
| NETW-04 | IAM role compilation includes `MaxSessionDuration` and region condition | unit | `go test ./pkg/compiler/... -run TestIAMSessionPolicy -v` | ❌ Wave 0 |
| NETW-05 | ec2spot module has `http_tokens = "required"` — regression test | unit | `go test ./infra/... -run TestIMDSv2Config -v` (or grep check) | ❌ Wave 0 |
| NETW-06 | Secret paths in profile generate scoped SSM IAM policy in compiled output | unit | `go test ./pkg/compiler/... -run TestSecretsInjection -v` | ❌ Wave 0 |
| NETW-07 | SOPS site.hcl decrypt path is tested against placeholder secrets file | unit | manual SOPS test with dummy `.sops.json` | ❌ manual only |
| NETW-08 | GitHub token path is included in secret injection for profiles with `sourceAccess.github` | unit | `go test ./pkg/compiler/... -run TestGitHubToken -v` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/compiler/... -short`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/compiler/compiler_test.go` — covers PROV-01, PROV-08, PROV-09, PROV-10, PROV-11, PROV-12
- [ ] `pkg/compiler/sgegress_test.go` — covers NETW-01
- [ ] `pkg/compiler/iam_test.go` — covers NETW-04, NETW-06, NETW-08
- [ ] `pkg/aws/discover_test.go` — covers PROV-02 (unit mocks for tag API)
- [ ] `pkg/terragrunt/runner_test.go` — covers runner interface (mock exec)
- [ ] No new test framework install needed — Go stdlib testing is already the pattern (see `pkg/profile/*_test.go`)

---

## Sources

### Primary (HIGH confidence)
- `/Users/khundeck/working/klankrmkr/infra/modules/ec2spot/v1.0.0/main.tf` — directly inspected; IMDSv2 already set; spot_type behavior and tag propagation verified
- `/Users/khundeck/working/klankrmkr/infra/modules/network/v1.0.0/main.tf` — directly inspected; confirmed no egress rules on `sandbox_mgmt` SG
- `/Users/khundeck/working/klankrmkr/infra/modules/ecs-cluster/v1.0.0/main.tf` — directly inspected; FARGATE_SPOT as cluster default; ECS task role policy scoped to `/km/*` SSM prefix
- `/Users/khundeck/working/klankrmkr/infra/live/sandboxes/_template/terragrunt.hcl` — directly inspected; substrate branching via `substrate_module` local already in place
- `/Users/khundeck/working/klankrmkr/infra/live/sandboxes/_template/service.hcl` — directly inspected; full ECS sidecar container template with placeholders for Phase 2 compiler
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/validate.go` — directly inspected; Cobra command constructor pattern and Config DI pattern confirmed
- `/Users/khundeck/working/klankrmkr/pkg/profile/types.go` — directly inspected; `RuntimeSpec.Substrate`, `RuntimeSpec.Spot`, `IdentitySpec`, `NetworkSpec` all available
- pkg.go.dev/github.com/aws/aws-sdk-go-v2 — v2 current, v1 EOL; modular service packages
- AWS docs — `aws_spot_instance_request` destroy behavior (cancel request, not terminate instance)
- AWS docs — IMDSv2 `http-tokens=required` official enforcement guidance

### Secondary (MEDIUM confidence)
- .planning/research/PITFALLS.md — Pitfall 2 (spot teardown), Pitfall 6 (concurrent lock) — prior project research; cross-verified against ec2spot module inspection
- .planning/research/ARCHITECTURE.md — Terragrunt per-directory isolation, compile→provision separation — confirmed via module inspection
- Terragrunt docs (docs.terragrunt.com) — `dependency` block, `mock_outputs`, `run-all` behavior — 0.77.x

### Tertiary (LOW confidence)
- None — all critical claims verified against directly inspected code or official docs

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — AWS SDK v2 verified; all other libs already in go.mod
- Architecture: HIGH — Terraform modules directly inspected; template files confirm the design decisions
- Pitfalls: HIGH (spots teardown, SG egress) — verified from module source code; MEDIUM (ECS capacity provider) — from AWS docs, not tested
- Compiler design: HIGH — JSON tfvars locked decision; template pattern confirmed in service.hcl

**Research date:** 2026-03-21
**Valid until:** 2026-04-21 (30 days — stack is stable; AWS provider behavior is stable)
