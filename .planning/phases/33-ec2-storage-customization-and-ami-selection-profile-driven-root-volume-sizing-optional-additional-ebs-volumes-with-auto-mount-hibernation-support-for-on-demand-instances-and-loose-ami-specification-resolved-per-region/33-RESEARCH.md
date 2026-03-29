# Phase 33: EC2 Storage Customization and AMI Selection - Research

**Researched:** 2026-03-28
**Domain:** Terraform EC2 EBS volumes, AMI data sources, EC2 hibernation, Go profile schema extension
**Confidence:** HIGH

## Summary

Phase 33 adds four distinct EC2 infrastructure capabilities driven by new profile fields: root volume sizing, optional additional EBS volume with auto-mount, hibernation support for on-demand instances, and loose AMI name resolution. All four capabilities require coordinated changes across three layers: the profile schema/types (Go), the Terraform module (ec2spot/v1.0.0), and the HCL/user-data compilers.

The project already has a clear, working pattern for exactly this kind of layered change from prior phases: add Go struct fields, update the JSON schema, extend the `ec2HCLParams` struct and `ec2ServiceHCLTemplate`, add new Terraform variables and resources, and optionally add user-data script logic. This phase follows that pattern four times.

The single most important architectural constraint is hibernation: it requires an encrypted root volume (`encrypted = true` on `aws_ebs_volume` / `root_block_device`), is incompatible with spot instances (AWS hard limit), and requires the instance type to support hibernation. Spot instances already use a different Terraform resource (`aws_spot_instance_request`), so the hibernation attribute only applies to `aws_instance.ec2_ondemand`. The loose AMI resolution replaces the hardcoded `al2023-ami-2023.*-x86_64` filter in `data "aws_ami" "base_ami"` with a data-source-per-slug approach, keeping AL2023 as the default.

**Primary recommendation:** Implement the four capabilities in order of dependency — AMI resolution first (it feeds both instance resources), then root volume sizing (prerequisite for hibernation encryption), then hibernation, then additional EBS volume (independent, user-data-only for mount logic).

## Standard Stack

### Core
| Library / Resource | Version | Purpose | Why Standard |
|--------------------|---------|---------|--------------|
| `aws_instance` `root_block_device` | Terraform AWS provider ~5.x | Root EBS volume sizing and encryption | Only way to configure boot volume before launch |
| `aws_ebs_volume` + `aws_volume_attachment` | Terraform AWS provider ~5.x | Additional EBS volume, separate lifecycle | Decoupled from instance; survives stop/start |
| `data "aws_ami"` | Terraform AWS provider ~5.x | AMI lookup by name filters per region | Official pattern for latest-AMI resolution |
| `hibernation = true` on `aws_instance` | Terraform AWS provider ~5.x | EC2 hibernation | Only valid argument to enable hibernation |
| `mkfs.ext4` + `/etc/fstab` via user-data | Shell / AL2023 | Format and auto-mount additional EBS | Standard Linux idiom; fstab ensures remount on reboot |

### Supporting
| Library / Resource | Version | Purpose | When to Use |
|--------------------|---------|---------|-------------|
| `lsblk` / `blkid` | OS standard | Detect attached device name in user-data | Needed because device name can vary (xvd vs nvme) |
| `aws_kms_key` or `aws_ebs_default_kms_key_id` | Terraform | KMS key for root-volume encryption required by hibernation | Only when hibernation is enabled |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `root_block_device` in `aws_instance` | Separate `aws_ebs_volume` for root | Not possible — root volume cannot be separate resource; must be `root_block_device` |
| Per-slug `data "aws_ami"` blocks | Single parameterized `data "aws_ami"` with local map | Single source with `for_each` on slugs is cleaner but `for_each` on data sources requires known keys at plan time; per-slug approach (using `count` gated on slug match) is simpler |
| `aws_volume_attachment` | `ebs_block_device` inside `aws_instance` | `ebs_block_device` is simpler but does not survive instance replacement; `aws_volume_attachment` is preferred for additional volumes |

## Architecture Patterns

### Recommended Project Structure

No new files needed. All changes extend existing files:

```
pkg/profile/
├── types.go                      — Add RuntimeSpec fields: RootVolumeSize, AdditionalVolume (new struct), Hibernation, AMI
├── schemas/sandbox_profile.json  — Add runtime.rootVolumeSize, runtime.additionalVolume, runtime.hibernation, runtime.ami

pkg/compiler/
├── service_hcl.go                — Extend ec2HCLParams, ec2ServiceHCLTemplate with new module inputs
├── userdata.go                   — Add additional-EBS mount section (section 2.4 or similar)

infra/modules/ec2spot/v1.0.0/
├── variables.tf                  — New variables: root_volume_size_gb, additional_volume_*, hibernation_enabled, ami_slug
├── main.tf                       — Update data.aws_ami, add root_block_device, hibernation, aws_ebs_volume + attachment
```

### Pattern 1: Root Volume Sizing via `root_block_device`

**What:** In `aws_instance.ec2_ondemand` and `aws_spot_instance_request.ec2spot`, add a `root_block_device` block.
**When to use:** Whenever `rootVolumeSize` > 0 in the profile (0 means "use AMI default").

```hcl
# infra/modules/ec2spot/v1.0.0/main.tf
resource "aws_instance" "ec2_ondemand" {
  for_each = local.ec2_ondemand_map
  # ... existing args ...

  dynamic "root_block_device" {
    for_each = var.root_volume_size_gb > 0 ? [1] : []
    content {
      volume_size           = var.root_volume_size_gb
      volume_type           = "gp3"
      encrypted             = var.hibernation_enabled ? true : false
      delete_on_termination = true
    }
  }

  hibernation = var.hibernation_enabled
}
```

For `aws_spot_instance_request`, add the same `root_block_device` dynamic block but **omit** `hibernation` (spot instances do not support hibernation at AWS level).

### Pattern 2: Hibernation

**What:** `hibernation = true` on `aws_instance.ec2_ondemand`. Encrypted root volume is required.
**When to use:** `spec.runtime.hibernation: true` in profile AND `spec.runtime.spot: false` (on-demand only).
**Constraint:** AWS enforces that hibernation requires encrypted root volume. Terraform will error at plan time if `encrypted = false` and `hibernation = true`.

```hcl
variable "hibernation_enabled" {
  type        = bool
  description = "Enable EC2 hibernation. Requires on-demand instances and encrypted root volume."
  default     = false
}
```

The compiler must validate that `hibernation: true` is not combined with `spot: true`. This validation should be in `pkg/compiler` (semantic layer), not profile schema (structural layer), since it is a cross-field constraint.

### Pattern 3: Additional EBS Volume

**What:** `aws_ebs_volume` + `aws_volume_attachment` resources scoped to `count = var.additional_volume_size_gb > 0 ? 1 : 0`.
**When to use:** `spec.runtime.additionalVolume.size > 0` in profile.

```hcl
resource "aws_ebs_volume" "additional" {
  count             = var.additional_volume_size_gb > 0 ? 1 : 0
  availability_zone = local.effective_azs[0]
  size              = var.additional_volume_size_gb
  encrypted         = var.additional_volume_encrypted
  type              = "gp3"
  tags = {
    "km:sandbox-id" = var.sandbox_id
    Name            = "km-sandbox-${var.sandbox_id}-data"
  }
}

resource "aws_volume_attachment" "additional" {
  count       = var.additional_volume_size_gb > 0 ? 1 : 0
  device_name = "/dev/xvdf"
  volume_id   = aws_ebs_volume.additional[0].id
  instance_id = var.use_spot
    ? aws_spot_instance_request.ec2spot[keys(local.ec2spot_map)[0]].spot_instance_id
    : aws_instance.ec2_ondemand[keys(local.ec2_ondemand_map)[0]].id
  force_detach = true
}
```

**IMPORTANT:** `aws_volume_attachment` requires the actual instance ID, not the spot request ID. For spot instances, use `spot_instance_id` from `aws_spot_instance_request`.

### Pattern 4: Loose AMI Resolution

**What:** Replace the single hardcoded `data "aws_ami" "base_ami"` with a map-keyed lookup that supports multiple slugs.
**When to use:** Always — even the default case uses this pattern with `al2023` as the resolved slug.

```hcl
locals {
  ami_filters = {
    "amazon-linux-2023" = {
      name_pattern = "al2023-ami-2023.*-x86_64"
      arch         = "x86_64"
    }
    "ubuntu-24.04" = {
      name_pattern = "ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"
      arch         = "x86_64"
    }
    "ubuntu-22.04" = {
      name_pattern = "ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"
      arch         = "x86_64"
    }
  }
  resolved_ami_slug = var.ami_slug != "" ? var.ami_slug : "amazon-linux-2023"
  ami_owner = startswith(local.resolved_ami_slug, "ubuntu") ? "099720109477" : "amazon"
}

data "aws_ami" "base_ami" {
  count       = local.total_ec2spot_count > 0 ? 1 : 0
  most_recent = true
  owners      = [local.ami_owner]

  filter {
    name   = "name"
    values = [local.ami_filters[local.resolved_ami_slug].name_pattern]
  }
  filter {
    name   = "architecture"
    values = [local.ami_filters[local.resolved_ami_slug].arch]
  }
  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}
```

Profile value `ami: amazon-linux-2023` or `ami: ubuntu-24.04` maps directly to a map key. The compiler passes the value through as `ami_slug`. An empty string in the profile means "use default" (AL2023).

### Pattern 5: User-Data Auto-Mount for Additional Volume

**What:** User-data section that formats and mounts the additional EBS volume at the specified `mountPoint`. This must be idempotent (check if already formatted).
**When to use:** `additionalVolume.size > 0` in profile.

```bash
# ============================================================
# 2.3. Additional EBS volume: format and mount (if configured)
# ============================================================
{{- if .AdditionalVolumeMountPoint }}
echo "[km-bootstrap] Waiting for additional EBS volume to attach..."
DEVICE=""
for i in $(seq 1 30); do
  # Try both xvdf (paravirtual) and nvme1n1 (NVMe) naming
  for dev in /dev/xvdf /dev/nvme1n1; do
    if [ -b "$dev" ]; then
      DEVICE="$dev"
      break 2
    fi
  done
  sleep 2
done

if [ -n "$DEVICE" ]; then
  # Format only if no filesystem exists (idempotent)
  if ! blkid "$DEVICE" &>/dev/null; then
    echo "[km-bootstrap] Formatting $DEVICE as ext4..."
    mkfs.ext4 -F "$DEVICE"
  fi
  mkdir -p "{{ .AdditionalVolumeMountPoint }}"
  # Mount and add to fstab for persistence
  DEVICE_UUID=$(blkid -s UUID -o value "$DEVICE")
  if ! grep -q "$DEVICE_UUID" /etc/fstab 2>/dev/null; then
    echo "UUID=${DEVICE_UUID} {{ .AdditionalVolumeMountPoint }} ext4 defaults,nofail 0 2" >> /etc/fstab
  fi
  mount -a
  chown sandbox:sandbox "{{ .AdditionalVolumeMountPoint }}" 2>/dev/null || true
  echo "[km-bootstrap] Additional volume mounted at {{ .AdditionalVolumeMountPoint }}"
else
  echo "[km-bootstrap] WARNING: additional EBS volume device not found after 60s"
fi
{{- end }}
```

### Anti-Patterns to Avoid

- **Using `ebs_block_device` inside `aws_instance` for additional volumes:** It is tied to the instance lifecycle; a replacement destroys the data. Use `aws_ebs_volume` + `aws_volume_attachment` instead.
- **Enabling hibernation on spot instances:** AWS rejects it. The compiler must guard against `hibernation: true` + `spot: true` combination with a clear error message.
- **Formatting the additional volume unconditionally in user-data:** If Terraform taint/redeploy reattaches the same volume, unconditional `mkfs` will destroy data. Always check `blkid` first.
- **Hardcoding device name `/dev/xvdf` in user-data without NVMe fallback:** AL2023 on Nitro instances uses NVMe naming (`/dev/nvme1n1`). User-data must probe both.
- **Making `rootVolumeSize` a required field:** It should be optional (pointer or zero-value default) so existing profiles without it continue to work with the AMI's default root volume size.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| AMI ID resolution per region | Region-to-AMI-ID map in Go code | `data "aws_ami"` with filters in Terraform | AWS manages AMI IDs; a static map goes stale immediately |
| EBS encryption for hibernation | Custom KMS key management | `encrypted = true` on `root_block_device` (uses account default CMK) | AWS handles KMS key association automatically |
| Device attachment detection | Polling `/proc/partitions` | Standard `blkid` + wait loop in user-data | `blkid` is the standard Linux tool for this |
| Filesystem persistence on reboot | Custom mount script as systemd unit | `/etc/fstab` with `nofail` option | fstab is the correct Linux idiom; nofail prevents boot hang if volume detaches |

**Key insight:** All four capabilities have first-class Terraform/AWS support. No custom AWS SDK calls or background daemons are needed.

## Common Pitfalls

### Pitfall 1: Spot Instance Volume Attachment Race Condition
**What goes wrong:** `aws_volume_attachment` for the additional volume tries to attach before the spot instance is fully running. The `spot_instance_id` attribute of `aws_spot_instance_request` is only available after the request is fulfilled.
**Why it happens:** `aws_spot_instance_request` sets `spot_instance_id` asynchronously; Terraform's dependency graph may not serialize correctly.
**How to avoid:** The `wait_for_fulfillment = true` flag already present in the module means `spot_instance_id` is populated before attachment. Reference `aws_spot_instance_request.ec2spot[key].spot_instance_id` directly as the `instance_id` in `aws_volume_attachment`.
**Warning signs:** `Error: instance_id is empty` or `Error: volume attachment failed` during apply.

### Pitfall 2: `for_each` on Data Sources With Variable Input
**What goes wrong:** Using `for_each` on `data "aws_ami"` keyed by a variable value causes "The `for_each` map includes keys derived from resource attributes that cannot be determined until apply" errors.
**Why it happens:** Terraform evaluates `for_each` keys at plan time; a variable from input is fine but a computed value is not.
**How to avoid:** Use a local variable (`local.resolved_ami_slug`) with an explicit default, and use `count = 1` on a single `data "aws_ami"` resource. The `ami_filters` local map is a pure local — Terraform resolves it at plan time.
**Warning signs:** `Error: Invalid for_each argument` during plan.

### Pitfall 3: Hibernation Validation Gap — Schema Allows Invalid Combinations
**What goes wrong:** A user sets `hibernation: true` and `spot: true` in the profile. The JSON schema validation passes (both are structurally valid), but Terraform apply fails with an AWS API error.
**Why it happens:** JSON schema cannot express cross-field constraints.
**How to avoid:** Add explicit semantic validation in `pkg/compiler` before generating HCL. Return an error like `hibernation requires on-demand instance (spot: false)`. This follows the existing pattern where cross-field validation is done in Go, not the schema.
**Warning signs:** Terraform plan error referencing `aws_spot_instance_request` + `hibernation`.

### Pitfall 4: Root Volume `encrypted = false` With `hibernation = true`
**What goes wrong:** AWS EC2 API rejects the `RunInstances` call if hibernation is requested but the root volume is not encrypted.
**Why it happens:** Hibernation writes RAM contents to the encrypted root volume; AWS enforces encryption.
**How to avoid:** In the Terraform module, enforce `encrypted = var.hibernation_enabled || var.root_volume_encrypted` so hibernation always forces encryption. This is simpler than a Terraform `precondition` block and avoids confusing plan-time errors.
**Warning signs:** `Error: InvalidParameterCombination: Hibernation enabled but root volume is not encrypted`.

### Pitfall 5: `additionalVolume` on ECS Substrate
**What goes wrong:** `additionalVolume` is an EC2-only concept. If an ECS profile accidentally includes it, the compiler generates invalid HCL or silently ignores it.
**Why it happens:** Profile fields are not substrate-scoped in the current schema.
**How to avoid:** Add a semantic validation rule in `pkg/compiler` (or `pkg/profile`): if `substrate == "ecs"` and `additionalVolume` is set, return an error. Similarly for `hibernation` and `rootVolumeSize` (ECS Fargate manages its own storage).
**Warning signs:** ECS profile fails with unexpected Terraform errors referencing EBS resources.

### Pitfall 6: NVMe Device Naming on Nitro Instances
**What goes wrong:** User-data uses `/dev/xvdf` to find the additional volume, but newer instance types (c5, m5, t3, etc.) use NVMe naming (`/dev/nvme1n1`).
**Why it happens:** AWS Nitro hypervisor presents EBS volumes as NVMe devices; the xvd* names are only for older Xen-based instances.
**How to avoid:** Probe both device paths in user-data (as shown in Pattern 5 above). The attached volume will be one of them.
**Warning signs:** Mount section in user-data logs "additional EBS volume device not found" even though the volume attached successfully.

## Code Examples

### Go: New RuntimeSpec fields

```go
// pkg/profile/types.go - additions to RuntimeSpec
type RuntimeSpec struct {
    Substrate      string             `yaml:"substrate"`
    Spot           bool               `yaml:"spot"`
    InstanceType   string             `yaml:"instanceType"`
    Region         string             `yaml:"region"`
    // New fields for Phase 33:
    RootVolumeSize   int                `yaml:"rootVolumeSize,omitempty"`   // GB; 0 = AMI default
    AdditionalVolume *AdditionalVolumeSpec `yaml:"additionalVolume,omitempty"` // nil = no additional volume
    Hibernation      bool               `yaml:"hibernation,omitempty"`       // on-demand only
    AMI              string             `yaml:"ami,omitempty"`               // slug: "amazon-linux-2023", "ubuntu-24.04", etc.
}

// AdditionalVolumeSpec describes an additional EBS data volume.
type AdditionalVolumeSpec struct {
    Size       int    `yaml:"size"`                   // GB; required
    MountPoint string `yaml:"mountPoint"`             // e.g. /data; required
    Encrypted  bool   `yaml:"encrypted,omitempty"`    // default false
}
```

### HCL Template: ec2spots object extended with new fields

The `ec2spots` variable in `variables.tf` (list of objects) needs new optional fields, and `main.tf` reads them to configure resources. The compiler adds them to the single `ec2spots` list item in the template:

```hcl
# In ec2ServiceHCLTemplate, inside the ec2spots object:
        root_volume_size_gb = {{ .RootVolumeSizeGB }}
        additional_volume_size_gb = {{ .AdditionalVolumeSizeGB }}
        additional_volume_encrypted = {{ .AdditionalVolumeEncrypted }}
        hibernation_enabled = {{ .HibernationEnabled }}
        ami_slug = "{{ .AMISlug }}"
```

Alternatively, these can be module-level variables (not per-instance), which is simpler since all instances in a sandbox share the same profile. Module-level variables are the recommended approach given that the `ec2spots` list currently has a count of 1.

### Semantic Validation in Go Compiler

```go
// In pkg/compiler — before calling generateEC2ServiceHCL
if p.Spec.Runtime.Hibernation && p.Spec.Runtime.Spot {
    return nil, fmt.Errorf("hibernation requires on-demand instance (spec.runtime.spot must be false)")
}
if p.Spec.Runtime.Hibernation && p.Spec.Runtime.Substrate == "ecs" {
    return nil, fmt.Errorf("hibernation is not supported for ECS substrate")
}
if p.Spec.Runtime.AdditionalVolume != nil && p.Spec.Runtime.Substrate == "ecs" {
    return nil, fmt.Errorf("additionalVolume is not supported for ECS substrate")
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Hardcoded AL2023 AMI filter in `data "aws_ami"` | Parameterized AMI slug → filter map | This phase | Profiles can specify OS; default stays AL2023 |
| No root volume configuration (AMI default ~8GB) | Optional `rootVolumeSize` drives `root_block_device` | This phase | Larger workloads needing >8GB can specify 30-100GB |
| No hibernation support | `hibernation = true` on on-demand instances | This phase | Cheaper resume pattern for long-running on-demand sandboxes |
| No additional storage beyond root | Optional `additionalVolume` with auto-mount | This phase | Data-heavy workloads get a dedicated, encrypted volume at a known path |

## Open Questions

1. **Should `rootVolumeSize` also apply to spot instances?**
   - What we know: `root_block_device` is valid on `aws_spot_instance_request` as well.
   - What's unclear: Whether the phase description intends hibernation encryption to be the only driver for root volume encryption, or whether non-hibernating instances should also support root size override.
   - Recommendation: Yes — apply `rootVolumeSize` to both spot and on-demand instances. Encryption on root volume should only be forced when hibernation is enabled (encryption is more expensive and changes performance characteristics).

2. **Which AMI slugs should be supported at launch?**
   - What we know: The description mentions `amazon-linux-2023` and `ubuntu-24.04` as examples.
   - What's unclear: Whether to support ARM64 variants (for cost-optimized Graviton instances).
   - Recommendation: Support `amazon-linux-2023`, `ubuntu-24.04`, and `ubuntu-22.04` with x86_64 only for Phase 33. ARM64 support is additive and can be a follow-on.

3. **Additional volume for spot instances — data persistence risk**
   - What we know: Spot instances can be interrupted; an `aws_ebs_volume` (separate resource) persists after interruption and can be reattached to a new instance.
   - What's unclear: Whether the automatic reattachment behavior on spot restart is required in Phase 33.
   - Recommendation: The volume persists (because it is a separate `aws_ebs_volume` resource), but the user-data auto-mount logic only runs at first boot. Document this: on spot interruption and restart, the volume will re-attach (Terraform apply) but the fstab entry already exists, so `mount -a` on boot will remount it. This works correctly.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` package (standard) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./pkg/compiler/... -run TestEC2 -v` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| P33-01 | `rootVolumeSize` field parsed from YAML and included in compiled HCL | unit | `go test ./pkg/compiler/... -run TestRootVolumeSize -v` | No — Wave 0 |
| P33-02 | `additionalVolume` struct parsed; HCL includes volume size, encryption, mount point | unit | `go test ./pkg/compiler/... -run TestAdditionalVolume -v` | No — Wave 0 |
| P33-03 | `hibernation: true` in profile generates `hibernation = true` in on-demand HCL | unit | `go test ./pkg/compiler/... -run TestHibernation -v` | No — Wave 0 |
| P33-04 | `hibernation: true` + `spot: true` returns compiler validation error | unit | `go test ./pkg/compiler/... -run TestHibernationSpotConflict -v` | No — Wave 0 |
| P33-05 | `ami: ubuntu-24.04` generates correct `ami_slug = "ubuntu-24.04"` in HCL | unit | `go test ./pkg/compiler/... -run TestAMISlug -v` | No — Wave 0 |
| P33-06 | Empty `ami` field defaults to `amazon-linux-2023` slug | unit | `go test ./pkg/compiler/... -run TestAMISlugDefault -v` | No — Wave 0 |
| P33-07 | Schema validation rejects `rootVolumeSize` < 0 | unit | `go test ./pkg/profile/... -run TestSchemaRootVolume -v` | No — Wave 0 |
| P33-08 | User-data template includes additional-volume mount section when `additionalVolume` is set | unit | `go test ./pkg/compiler/... -run TestUserDataAdditionalVolume -v` | No — Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/compiler/... ./pkg/profile/... -v`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/compiler/ec2_storage_test.go` — covers P33-01 through P33-08
- [ ] `pkg/profile/schema_storage_test.go` — covers P33-07 (schema validation for new fields)

## Sources

### Primary (HIGH confidence)
- Direct code inspection of `infra/modules/ec2spot/v1.0.0/main.tf` and `variables.tf`
- Direct code inspection of `pkg/compiler/service_hcl.go` and `pkg/compiler/userdata.go`
- Direct code inspection of `pkg/profile/types.go` and `schemas/sandbox_profile.schema.json`
- AWS Terraform provider documentation pattern for `root_block_device`, `aws_ebs_volume`, `aws_volume_attachment`, `hibernation`, `data "aws_ami"` — established AWS provider conventions verified against existing module usage

### Secondary (MEDIUM confidence)
- AWS documentation on EC2 hibernation prerequisites (encrypted root volume required, instance type constraints) — consistent with provider argument behavior observed in module

### Tertiary (LOW confidence)
- NVMe device naming behavior on Nitro instances (xvdf vs nvme1n1) — well-known AWS Nitro characteristic, needs live verification

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all capabilities use established Terraform AWS provider patterns already present in the codebase
- Architecture: HIGH — patterns follow existing Phase 2-27 conventions directly (dynamic blocks, count-based conditionals, HCL template extension)
- Pitfalls: HIGH for hibernation+spot constraint and NVMe naming (AWS hard limits); MEDIUM for race condition details

**Research date:** 2026-03-28
**Valid until:** 2026-04-28 (AWS provider stable; Terraform patterns long-lived)
