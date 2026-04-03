# Phase 43: Regional EFS Shared Filesystem - Research

**Researched:** 2026-04-02
**Domain:** AWS EFS provisioning via Terraform + EC2 userdata mount + Go compiler/profile integration
**Confidence:** HIGH

## Summary

Phase 43 adds a Regional EFS filesystem provisioned once by `km init` and mounted on-demand by any EC2 sandbox with `spec.runtime.mountEFS: true`. The EFS filesystem ID is captured from Terraform outputs, stored in the network `outputs.json` (alongside VPC and subnet data), and flowed through the existing `NetworkConfig` struct into the compiler's userdata template. The sandbox EC2 userdata installs `amazon-efs-utils`, mounts the filesystem at a configurable path (default `/shared`) using TLS + `_netdev,nofail` options, and adds an `/etc/fstab` entry for persistence. A dedicated EFS security group created during `km init` allows NFS port 2049 ingress from the sandbox EC2 security group. EFS is never destroyed by `km destroy` — it persists across all sandbox lifecycles.

The implementation touches six files: a new `infra/modules/efs/v1.0.0/` Terraform module, a new `infra/live/{regionLabel}/efs/terragrunt.hcl` Terragrunt config, updates to `regionalModules()` in `init.go` (and its output-capture logic), a new `EFSFilesystemID` field on `NetworkConfig` and `NetworkOutputs`, new profile fields on `RuntimeSpec`, and a new conditional block in the userdata template.

**Primary recommendation:** Mirror the existing EBS additional-volume pattern (Phase 33) exactly — new profile fields on `RuntimeSpec`, new field on `NetworkConfig` propagated through `Compile()`, and a conditional block in `userDataTemplate`. The EFS Terraform module is a straightforward single-AZ-per-subnet mount-target loop; the security group ingress rule sources from the EC2 sandbox SG ID already available as a network output.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| EFS-01 | `km init` provisions a Regional EFS filesystem (General Purpose, Elastic throughput, encrypted at rest) with one mount target per AZ in the shared VPC | Terraform `aws_efs_file_system` + `aws_efs_mount_target` per subnet; mirrors network module pattern |
| EFS-02 | EFS filesystem ID is stored so `km create` can reference it at sandbox provisioning time | Captured from Terraform outputs into `infra/live/{regionLabel}/efs/outputs.json`, added to `NetworkOutputs` and `NetworkConfig`, loaded by `LoadNetworkOutputs()` |
| EFS-03 | Profile fields `spec.runtime.mountEFS` (bool) and `spec.runtime.efsMountPoint` (string, default "/shared") control per-sandbox EFS mounting | Two new fields on `RuntimeSpec` in `pkg/profile/types.go`; compiler reads them at `compileEC2()` time |
| EFS-04 | EC2 sandbox userdata installs `amazon-efs-utils`, mounts EFS with TLS + `_netdev,nofail`, writes `/etc/fstab` entry | New conditional block in `userDataTemplate` in `pkg/compiler/userdata.go`; follows Phase 33 EBS mount block pattern |
| EFS-05 | A security group created during `km init` allows NFS port 2049 from sandbox SGs so mounts succeed | `aws_security_group` + `aws_security_group_rule` in EFS module; ingress source = sandbox SG from network outputs |
| EFS-06 | `km destroy` does NOT remove EFS — it persists across sandbox lifecycles | EFS module is applied by `km init` only, not referenced in sandbox Terragrunt dir; no destroy path needed |
</phase_requirements>

## Standard Stack

### Core
| Library/Tool | Version | Purpose | Why Standard |
|-------------|---------|---------|--------------|
| `aws_efs_file_system` | Terraform AWS provider ~5.x | Provision Regional EFS with encryption, Elastic throughput | AWS-managed resource; standard Terraform resource type |
| `aws_efs_mount_target` | Terraform AWS provider ~5.x | One mount target per AZ subnet | Required for NFS access from EC2 instances in that AZ |
| `amazon-efs-utils` | AL2023 package repo | Provides `mount.efs` helper, TLS via stunnel, `fstab` integration | AWS-maintained; required for `efs` mount type with `tls` option |

### Supporting
| Library/Tool | Version | Purpose | When to Use |
|-------------|---------|---------|-------------|
| `aws_efs_backup_policy` | Terraform AWS provider | Enable/disable AWS Backup integration | Optional — can be added as a follow-up |
| `stunnel` (via amazon-efs-utils) | Bundled | TLS encryption of NFS traffic | Automatically used when `tls` mount option is present |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `amazon-efs-utils` mount type | Raw NFS `nfs4` mount | `amazon-efs-utils` handles TLS, DNS resolution, IAM auth; raw NFS skips all of that |
| Elastic throughput | Provisioned IOPS | Elastic auto-scales; Provisioned is for predictable high-throughput workloads |

**Installation (in userdata):**
```bash
yum install -y amazon-efs-utils
```

## Architecture Patterns

### Recommended Project Structure
```
infra/
├── modules/
│   └── efs/
│       └── v1.0.0/
│           ├── main.tf        # aws_efs_file_system, mount targets, security group
│           ├── variables.tf   # vpc_id, subnet_ids, sandbox_sg_id, km_label, region_label
│           └── outputs.tf     # filesystem_id, security_group_id
└── live/
    └── {regionLabel}/
        └── efs/
            └── terragrunt.hcl # reads network outputs, provides EFS module inputs
```

### Pattern 1: EFS Terraform Module (main.tf)
**What:** Single module creates filesystem + mount targets + security group in one Terraform apply
**When to use:** Always; this is the only EFS module in the project

```hcl
# Source: AWS Terraform provider docs (HIGH confidence)
resource "aws_efs_file_system" "shared" {
  creation_token   = "km-shared-${var.region_label}"
  performance_mode = "generalPurpose"
  throughput_mode  = "elastic"
  encrypted        = true

  tags = {
    Name         = "km-shared-efs-${var.region_label}"
    "km:purpose" = "shared-sandbox-filesystem"
  }
}

resource "aws_efs_mount_target" "shared" {
  count           = length(var.subnet_ids)
  file_system_id  = aws_efs_file_system.shared.id
  subnet_id       = var.subnet_ids[count.index]
  security_groups = [aws_security_group.efs.id]
}

resource "aws_security_group" "efs" {
  name        = "km-efs-${var.region_label}"
  description = "NFS ingress for km EFS shared filesystem"
  vpc_id      = var.vpc_id

  ingress {
    from_port       = 2049
    to_port         = 2049
    protocol        = "tcp"
    security_groups = [var.sandbox_sg_id]
    description     = "NFS from sandbox instances"
  }
}
```

### Pattern 2: Terragrunt EFS Config (follows existing network pattern)
**What:** `terragrunt.hcl` for `infra/live/{regionLabel}/efs/` reads network outputs
**When to use:** Always

```hcl
# Pattern mirrors infra/live/{regionLabel}/network/terragrunt.hcl
locals {
  repo_root     = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars     = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
  region_full   = local.region_config.locals.region_full

  # Read network outputs to get VPC, subnets, sandbox SG
  network_outputs = jsondecode(file("${get_terragrunt_dir()}/../network/outputs.json"))
}

terraform {
  source = "${local.repo_root}/infra/modules/efs/v1.0.0"
}

inputs = {
  km_label      = local.site_vars.locals.site.label
  region_label  = local.region_label
  vpc_id        = local.network_outputs.vpc_id.value
  subnet_ids    = local.network_outputs.public_subnets.value
  sandbox_sg_id = local.network_outputs.sandbox_mgmt_sg_id.value
}
```

### Pattern 3: `regionalModules()` Registration (follows existing pattern exactly)
**What:** Add `efs` module after `network` in `regionalModules()` in `init.go`
**When to use:** This is the only way `km init` picks up new modules

```go
// In internal/app/cmd/init.go — add after "network" entry:
{
    name:    "efs",
    dir:     filepath.Join(regionDir, "efs"),
    envReqs: nil,  // no env vars required; reads network outputs.json
},
```

**CRITICAL:** The `efs` module must come after `network` in the slice because `terragrunt.hcl` reads `network/outputs.json` via `jsondecode(file(...))`. If `network` hasn't applied yet, the file won't exist and Terragrunt will fail.

### Pattern 4: EFS outputs capture in `init.go`
**What:** After the `efs` module applies, capture `filesystem_id` and persist to a known location
**When to use:** Mirrors the `network` module output-capture block at line 359

```go
// In RunInitWithRunner(), after the module loop body:
if mod.name == "efs" {
    outputMap, err := runner.Output(ctx, mod.dir)
    if err == nil {
        outputJSON, _ := json.MarshalIndent(outputMap, "", "  ")
        outputsFile := filepath.Join(mod.dir, "outputs.json")
        os.WriteFile(outputsFile, outputJSON, 0o644)
        if v, ok := outputMap["filesystem_id"]; ok {
            fmt.Printf("  EFS filesystem: %v\n", extractValue(v))
        }
    }
}
```

### Pattern 5: `NetworkConfig.EFSFilesystemID` propagation
**What:** Add `EFSFilesystemID string` to `NetworkConfig` in `service_hcl.go` and `NetworkOutputs` in `init.go`; populate in `LoadNetworkOutputs()`
**When to use:** Always; this is how the EFS ID reaches the userdata template

```go
// pkg/compiler/service_hcl.go
type NetworkConfig struct {
    VPCID             string
    PublicSubnets     []string
    AvailabilityZones []string
    RegionLabel       string
    EmailDomain       string
    ArtifactsBucket   string
    SpotRateUSD       float64
    Alias             string
    // NEW: EFS filesystem ID for profile-driven mounts (Phase 43)
    EFSFilesystemID   string
}

// internal/app/cmd/init.go
type NetworkOutputs struct {
    VPCID             string   `json:"vpc_id"`
    PublicSubnets     []string `json:"public_subnets"`
    AvailabilityZones []string `json:"availability_zones"`
    SandboxMgmtSGID   string   `json:"sandbox_mgmt_sg_id"`
    // NEW
    EFSFilesystemID   string   `json:"efs_filesystem_id"`
}
```

However, the EFS outputs are in `efs/outputs.json`, not `network/outputs.json`. The cleanest approach is to load EFS outputs separately, analogous to how `LoadNetworkOutputs()` reads `network/outputs.json`:

```go
// Option A: separate LoadEFSOutputs() function (cleanest)
func LoadEFSOutputs(repoRoot, regionLabel string) (string, error) {
    outputsFile := filepath.Join(repoRoot, "infra", "live", regionLabel, "efs", "outputs.json")
    // ... parse filesystem_id.value ...
    // returns "" when file doesn't exist (EFS not initialized)
}

// In create.go, after LoadNetworkOutputs():
efsID, _ := LoadEFSOutputs(repoRoot, regionLabel)
network.EFSFilesystemID = efsID
```

### Pattern 6: Profile fields on `RuntimeSpec`
**What:** Two new fields on `RuntimeSpec` in `pkg/profile/types.go`
**When to use:** Follow the `AdditionalVolume *AdditionalVolumeSpec` pattern (Phase 33)

```go
// In RuntimeSpec (pkg/profile/types.go):
// MountEFS controls whether this sandbox mounts the regional EFS shared filesystem (EC2 only).
MountEFS bool `yaml:"mountEFS,omitempty" json:"mountEFS,omitempty"`
// EFSMountPoint is the filesystem path at which EFS is mounted (default "/shared").
EFSMountPoint string `yaml:"efsMountPoint,omitempty" json:"efsMountPoint,omitempty"`
```

### Pattern 7: Userdata EFS mount block
**What:** Conditional block in `userDataTemplate` that installs `amazon-efs-utils` and mounts EFS
**When to use:** When `MountEFS` is true; follows the Phase 33 EBS volume mount block (section 2.6)

```bash
# Template section — follows section 2.6 (Additional EBS volume):
{{- if .EFSFilesystemID }}

# ============================================================
# 2.7a. EFS shared filesystem: install utils and mount (Phase 43)
# ============================================================
echo "[km-bootstrap] Mounting EFS shared filesystem {{ .EFSFilesystemID }}..."
yum install -y amazon-efs-utils 2>&1 | tee -a /var/log/km-bootstrap.log
mkdir -p "{{ .EFSMountPoint }}"
# Resolve EFS DNS: {fs-id}.efs.{region}.amazonaws.com
EFS_DNS="{{ .EFSFilesystemID }}.efs.${REGION}.amazonaws.com"
# Add fstab entry with _netdev,nofail so boot continues if EFS is unavailable
if ! grep -q "{{ .EFSFilesystemID }}" /etc/fstab 2>/dev/null; then
  echo "${EFS_DNS}:/ {{ .EFSMountPoint }} efs _netdev,nofail,tls 0 0" >> /etc/fstab
fi
mount -a -t efs 2>&1 | tee -a /var/log/km-bootstrap.log || \
  echo "[km-bootstrap] WARNING: EFS mount failed — filesystem may be unavailable"
echo "[km-bootstrap] EFS mounted at {{ .EFSMountPoint }}"
{{- end }}
```

### Anti-Patterns to Avoid
- **Using `/etc/fstab` without `nofail`:** If EFS is unreachable (mount target in another AZ, security group issue), the instance will fail to boot. Always include `nofail`.
- **Using `_netdev` without ensuring network is up:** The `_netdev` option tells systemd to wait for network before mounting; without it, the mount can race with network initialization. Always include `_netdev`.
- **Destroying EFS in `km destroy`:** The design decision is explicit — EFS persists. Adding EFS teardown to `km destroy` or to the sandbox Terragrunt dir would be wrong.
- **Using provisioned throughput mode:** Elastic throughput auto-scales and has no fixed cost; Provisioned would charge even when idle.
- **Hardcoding EFS DNS in userdata:** Use the regional DNS pattern `{fs-id}.efs.{region}.amazonaws.com` where `REGION` is fetched from IMDS (already available as `$REGION` in the bootstrap script by section 2).
- **Writing `security_group_rule` with CIDR instead of SG source:** Cross-SG references are more accurate than `0.0.0.0/0`; the sandbox EC2 SG ID is available from network outputs.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| TLS for NFS | Custom stunnel configuration | `amazon-efs-utils` with `tls` mount option | efs-utils handles stunnel lifecycle, certificate rotation, and restart on failure |
| EFS DNS resolution | Static IP from mount target | Standard EFS DNS `{fs-id}.efs.{region}.amazonaws.com` | EFS DNS automatically routes to the closest healthy mount target AZ; hardcoded IPs break on AZ rotation |
| Mount retry logic | Custom polling loop | `nofail` + `_netdev` in fstab | systemd handles mount retries at boot; `nofail` handles unavailability gracefully |

**Key insight:** `amazon-efs-utils` handles all NFS+TLS complexity including the stunnel wrapper, automatic reconnect, and IAM authorization (if needed). The `mount -t efs` command with `tls` is a one-liner; custom NFS configuration would require managing stunnel config files, port numbers, and restart hooks.

## Common Pitfalls

### Pitfall 1: EFS module applied before network module
**What goes wrong:** `terragrunt.hcl` references `../network/outputs.json` via `jsondecode(file(...))`. If `network` hasn't applied, the file is missing and Terragrunt fails at plan time with "no such file or directory".
**Why it happens:** `regionalModules()` returns an ordered slice; incorrect ordering breaks dependency.
**How to avoid:** Place `efs` after `network` in the `regionalModules()` slice. The function comment already says "Modules are returned in dependency order."
**Warning signs:** `jsondecode: cannot read file` error during `km init`.

### Pitfall 2: Mount target count mismatch with subnets
**What goes wrong:** If `subnet_ids` has 4 entries but the VPC's route table doesn't connect those subnets to the internet, mount targets provision but sandbox instances can't reach them.
**Why it happens:** The existing network module creates both public and private subnets. EFS mount targets in private subnets work fine (NFS is internal); public subnets also work since instances are also in public subnets.
**How to avoid:** Use `public_subnets` from network outputs (same subnets where sandbox EC2 instances launch).
**Warning signs:** Mount hangs indefinitely at `mount -a`; check `dmesg` for NFS connection timeout.

### Pitfall 3: Security group rule sourcing sandbox SG vs. CIDR
**What goes wrong:** Using `cidr_blocks = ["0.0.0.0/0"]` for the EFS security group ingress allows all VPC traffic instead of restricting to sandbox instances.
**Why it happens:** Simpler to write than an SG-to-SG reference.
**How to avoid:** Use `source_security_group_id = var.sandbox_sg_id` where `sandbox_sg_id` comes from the network outputs (`sandbox_mgmt_sg_id`).
**Warning signs:** Security scanner flags overly permissive SG rule; or conversely, mount fails because wrong SG ID was used.

### Pitfall 4: `EFSFilesystemID` empty when EFS not initialized
**What goes wrong:** A sandbox profile has `mountEFS: true` but the operator hasn't run `km init` with the EFS module yet. `network.EFSFilesystemID` is empty, and `{{ if .EFSFilesystemID }}` in the template is false — the mount silently doesn't happen.
**Why it happens:** `LoadEFSOutputs()` returns `""` when `efs/outputs.json` doesn't exist.
**How to avoid:** In `create.go` (or `compileEC2()`), check `p.Spec.Runtime.MountEFS && network.EFSFilesystemID == ""` and return a descriptive error: "profile requests mountEFS but EFS not initialized — run 'km init' first".
**Warning signs:** Profile has `mountEFS: true` but `/shared` doesn't exist in the sandbox.

### Pitfall 5: `nofail` + spot interruption interaction
**What goes wrong:** A spot instance is interrupted mid-boot during EFS mount. With `nofail`, systemd continues but the mount is silently skipped; subsequent sandbox commands that write to `/shared` fail with "no such file or directory".
**Why it happens:** `nofail` is required for resilience but means failure is silent.
**How to avoid:** After `mount -a`, verify mount succeeded with `mountpoint -q "{{ .EFSMountPoint }}"`. Log a clear WARNING if it failed. This is the same pattern used for the EBS additional volume (Phase 33 userdata already does this).

### Pitfall 6: `amazon-efs-utils` installation on Ubuntu AMIs
**What goes wrong:** `yum install -y amazon-efs-utils` fails on Ubuntu 22.04/24.04 because they use `apt`.
**Why it happens:** The AMI slug can be `ubuntu-24.04` or `ubuntu-22.04` — the default is `amazon-linux-2023`.
**How to avoid:** Use AMI-detection logic: check `ID_LIKE` in `/etc/os-release`. For AL2023, `yum install`; for Ubuntu/Debian, `apt-get install -y amazon-efs-utils`. Cross-reference existing sidecar install patterns in userdata which currently assume `yum` (AL2023 default). Phase 43 can start with AL2023-only (same as current sidecars) and note the Ubuntu gap.
**Warning signs:** `yum: command not found` in bootstrap log on Ubuntu AMIs.

## Code Examples

Verified patterns from existing codebase:

### How Phase 33 EBS mount block flows (reference pattern)
```go
// pkg/compiler/userdata.go — userDataParams struct (line 921-923)
// Additional EBS volume mount point (Phase 33)
// Empty string means no additional volume.
AdditionalVolumeMountPoint string

// Template section 2.6 in userDataTemplate:
{{- if .AdditionalVolumeMountPoint }}
# ... format, mount, fstab entry ...
{{- end }}
```

The EFS pattern follows this exactly: `EFSFilesystemID` (empty = disabled, non-empty = enabled) + `EFSMountPoint` (defaults to `/shared`).

### How NetworkConfig flows from create.go to compiler
```go
// internal/app/cmd/create.go (around line 334):
networkOutputs, err := LoadNetworkOutputs(repoRoot, regionLabel)
// ...
network := &compiler.NetworkConfig{
    VPCID:             networkOutputs.VPCID,
    PublicSubnets:     networkOutputs.PublicSubnets,
    AvailabilityZones: networkOutputs.AvailabilityZones,
    RegionLabel:       regionLabel,
    EmailDomain:       emailDomain,
    ArtifactsBucket:   cfg.ArtifactsBucket,
    SpotRateUSD:       spotRate,
    Alias:             alias,
    // Phase 43: add EFSFilesystemID here
}
artifacts, err := compiler.Compile(p, sandboxID, onDemand, network)
```

### How `generateUserData` receives NetworkConfig fields
```go
// pkg/compiler/service_hcl.go — buildEC2Params() (around line 629)
// NetworkConfig fields are spread into ec2HCLParams struct
// which is then passed to generateUserData() via userDataParams

// The EFSFilesystemID should be added to userDataParams and
// populated in generateUserData() from NetworkConfig:
params.EFSFilesystemID = network.EFSFilesystemID
params.EFSMountPoint   = efsMountPoint // from profile, default "/shared"
```

### How `outputs.json` is loaded for a module
```go
// internal/app/cmd/init.go (line 418-448)
func LoadNetworkOutputs(repoRoot, regionLabel string) (*NetworkOutputs, error) {
    outputsFile := filepath.Join(repoRoot, "infra", "live", regionLabel, "network", "outputs.json")
    // ... read and parse ...
}

// Pattern to follow for LoadEFSOutputs:
func LoadEFSOutputs(repoRoot, regionLabel string) (string, error) {
    outputsFile := filepath.Join(repoRoot, "infra", "live", regionLabel, "efs", "outputs.json")
    // returns "" (not error) when file doesn't exist
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| S3 for cross-sandbox artifact sharing | EFS shared filesystem | Phase 43 | Lower latency, no API calls; cross-sandbox reads/writes via standard POSIX filesystem |
| Manual NFS setup with raw `nfs4` mount type | `amazon-efs-utils` + `mount -t efs` | AWS best practice | Handles TLS, DNS routing, HA automatically |

**Not applicable in this phase:**
- EFS IAM authorization (not needed; security group restriction is sufficient for this use case)
- EFS access points (useful for per-sandbox path isolation in future phases)
- EFS replication (not required; EFS is multi-AZ by design via mount targets)

## Open Questions

1. **Ubuntu AMI support for `amazon-efs-utils`**
   - What we know: Current userdata assumes `yum` (AL2023). Ubuntu uses `apt`. `amazon-efs-utils` is available in Ubuntu repos (`apt-get install -y amazon-efs-utils`).
   - What's unclear: Should Phase 43 add AMI detection or restrict `mountEFS` to AL2023 only?
   - Recommendation: Start with AL2023-only; add a validation error in the profile validator if `mountEFS: true` and `ami` is `ubuntu-*`. Document the restriction.

2. **EFS availability zone optimization**
   - What we know: Spot instances are placed in `local.effective_azs[instance_idx % len(azs)]`. Cross-AZ NFS traffic costs $0.01/GB per direction. The design accepts this cost.
   - What's unclear: Should the EFS mount target in the instance's AZ be preferred via a regional DNS override?
   - Recommendation: Use the standard EFS DNS endpoint; it routes to the closest available mount target. Document the cross-AZ cost. No custom DNS needed.

3. **`km uninit` EFS teardown**
   - What we know: `km init` provisions EFS; `km destroy` explicitly does NOT remove it. There is a `km uninit` command for full platform teardown.
   - What's unclear: Should `km uninit` also destroy EFS? Phase scope says only `km init` provisions it.
   - Recommendation: Out of scope for Phase 43. EFS teardown should be addressed in a future phase or as part of `km uninit`. Add a note in `km uninit` help text.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (`go test ./...`) |
| Config file | none (standard Go test runner) |
| Quick run command | `go test ./pkg/compiler/... -run TestEFS -v` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| EFS-01 | Terraform module files exist with correct resources | smoke (file presence) | `go test ./internal/app/cmd/... -run TestEFSModuleFiles` | Wave 0 |
| EFS-02 | `LoadEFSOutputs()` returns filesystem ID from outputs.json | unit | `go test ./internal/app/cmd/... -run TestLoadEFSOutputs` | Wave 0 |
| EFS-03 | Profile fields `mountEFS` / `efsMountPoint` parse correctly | unit | `go test ./pkg/profile/... -run TestEFSProfileFields` | Wave 0 |
| EFS-04 | Userdata contains EFS install+mount block when `mountEFS: true` | unit | `go test ./pkg/compiler/... -run TestUserDataEFSMount` | Wave 0 |
| EFS-04 | Userdata omits EFS block when `mountEFS: false` | unit | `go test ./pkg/compiler/... -run TestUserDataNoEFSMount` | Wave 0 |
| EFS-05 | EFS SG allows port 2049 from sandbox SG | unit/smoke (TF file check) | `go test ./internal/app/cmd/... -run TestEFSSGConfig` | Wave 0 |
| EFS-06 | `km destroy` does not reference EFS module | unit | `go test ./internal/app/cmd/... -run TestDestroyNoEFS` | Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/compiler/... -run TestEFS -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/compiler/efs_userdata_test.go` — covers EFS-03, EFS-04 (mountEFS profile field + userdata block)
- [ ] `internal/app/cmd/efs_test.go` — covers EFS-02 (LoadEFSOutputs), EFS-06 (destroy no-op)
- [ ] `infra/modules/efs/v1.0.0/main.tf` — covers EFS-01, EFS-05 (Terraform module)

## Sources

### Primary (HIGH confidence)
- Codebase direct read: `pkg/compiler/userdata.go` — existing userdata template and `userDataParams` struct (lines 863-924)
- Codebase direct read: `pkg/compiler/service_hcl.go` — `NetworkConfig` struct (lines 552-570)
- Codebase direct read: `internal/app/cmd/init.go` — `regionalModules()`, output capture pattern, `LoadNetworkOutputs()` (lines 59-448)
- Codebase direct read: `infra/modules/ec2spot/v1.0.0/main.tf` — EC2 SG pattern, IAM role pattern
- Codebase direct read: `pkg/profile/types.go` — `RuntimeSpec` struct with `AdditionalVolume` pattern
- Codebase direct read: `infra/live/use1/network/outputs.json` — confirmed actual VPC/SG IDs available
- Codebase direct read: `infra/live/use1/network/terragrunt.hcl` — Terragrunt module wiring pattern

### Secondary (MEDIUM confidence)
- ROADMAP.md Phase 43 section (lines 991-1005) — confirmed design decisions verbatim
- Phase 33 EBS volume pattern (userdata section 2.6, `AdditionalVolume*` fields) — confirmed analogous approach

### Tertiary (LOW confidence)
- AWS EFS Terraform provider docs (training knowledge, not verified via Context7 this session) — `aws_efs_file_system` `throughput_mode = "elastic"` is confirmed accurate as of provider ~5.x but should be verified against current provider docs if any uncertainty

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — Terraform EFS resources are stable, `amazon-efs-utils` is the standard AWS tooling
- Architecture: HIGH — all integration points verified by reading actual code; patterns follow existing Phase 33 EBS pattern exactly
- Pitfalls: HIGH — based on direct code reading of existing patterns and well-known EFS/mount edge cases

**Research date:** 2026-04-02
**Valid until:** 2026-05-02 (stable domain; Terraform AWS provider and amazon-efs-utils are mature)
