# Phase 33: EC2 Storage Customization and AMI Selection - Research

**Researched:** 2026-04-02 (updated from 2026-03-28)
**Domain:** Terraform EC2 EBS volumes, AMI data sources, EC2 hibernation, Go profile schema extension
**Confidence:** HIGH

## Summary

Phase 33 adds four distinct EC2 infrastructure capabilities driven by new profile fields: root volume sizing, optional additional EBS volume with auto-mount, hibernation support for on-demand instances, and loose AMI name resolution. All four capabilities require coordinated changes across three layers: the profile schema/types (Go), the Terraform module (ec2spot/v1.0.0), and the HCL/user-data compilers.

The project already has a clear, working pattern for exactly this kind of layered change from prior phases: add Go struct fields, update the JSON schema, extend the `ec2HCLParams` struct and `ec2ServiceHCLTemplate`, add new Terraform variables and resources, and optionally add user-data script logic. This phase follows that pattern four times.

**Operational finding (2026-04-02):** `km pause goose-a7a2431c` fell back to a plain stop with "hibernate not available, stopping normally". Root cause confirmed: the EC2 instance was not launched with `hibernation = true` and an encrypted root volume. The Terraform module currently has neither. Phase 33 must add both.

**Key architectural constraint:** `hibernation = true` on `aws_instance.ec2_ondemand` requires (1) the attribute set at launch time — cannot be retrofitted to a running instance, (2) an encrypted root EBS volume, and (3) a supported instance family. The current Terraform module sets neither `hibernation` nor `encrypted` on the root block device.

**Spot instance scope:** The current `km pause` code explicitly rejects spot instances (line 133-137 in `pause.go`) with a clear error. This is intentional product behavior. Although AWS technically supports hibernation for persistent spot requests, Phase 33 does not add spot hibernation — it is out of scope for this phase. Spot instances get `root_block_device` sizing only (no encryption, no hibernation).

**Primary recommendation:** Implement the four capabilities in order of dependency — AMI resolution first (feeds both instance resources), then root volume sizing (prerequisite for hibernation encryption), then hibernation (on-demand only), then additional EBS volume (independent, user-data-only for mount logic).

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
| `lsblk` + `/dev/xvdf` symlink (AL2023) | OS standard | Detect attached device in user-data | AL2023 udev rules create `/dev/xvdf` → `/dev/nvmeXn1` symlink automatically |
| `blkid` | OS standard | Detect filesystem, get UUID | Needed for idempotent format check and fstab UUID entry |
| `encrypted = true` with default KMS | AWS | EBS encryption for hibernation | AWS account default CMK used when no `kms_key_id` set; no extra KMS resource needed |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `root_block_device` in `aws_instance` | Separate `aws_ebs_volume` for root | Not possible — root volume cannot be separate resource; must be `root_block_device` |
| Per-slug `data "aws_ami"` blocks | Single `data "aws_ami"` with locals map | Single source with locals map is correct — `for_each` on data sources with variable keys causes plan-time errors; locals map is evaluated at plan time |
| `aws_volume_attachment` | `ebs_block_device` inside `aws_instance` | `ebs_block_device` is simpler but does not survive instance replacement; `aws_volume_attachment` is preferred for additional volumes |
| Explicit KMS key resource | `encrypted = true` (default CMK) | Default CMK is sufficient for hibernation; explicit KMS key adds complexity and billing |

**Installation:**
No new packages. All capabilities use existing Terraform AWS provider (~5.x) resources.

## Architecture Patterns

### Recommended Project Structure

No new files needed. All changes extend existing files:

```
pkg/profile/
├── types.go                      — Add RuntimeSpec fields: RootVolumeSize, AdditionalVolume (new struct), Hibernation, AMI
├── schemas/sandbox_profile.json  — Add runtime.rootVolumeSize, runtime.additionalVolume, runtime.hibernation, runtime.ami

pkg/compiler/
├── service_hcl.go                — Extend ec2HCLParams, ec2ServiceHCLTemplate with new module inputs
├── userdata.go                   — Add additional-EBS mount section

infra/modules/ec2spot/v1.0.0/
├── variables.tf                  — New variables: root_volume_size_gb, additional_volume_*, hibernation_enabled, ami_slug
├── main.tf                       — Update data.aws_ami, add root_block_device, hibernation, aws_ebs_volume + attachment
```

### Pattern 1: Root Volume Sizing via `root_block_device`

**What:** In `aws_instance.ec2_ondemand` and `aws_spot_instance_request.ec2spot`, add a `root_block_device` block.
**When to use:** Whenever `rootVolumeSize` > 0 in the profile (0 means "use AMI default") OR whenever `hibernation_enabled` is true (encryption requires an explicit `root_block_device` block).

```hcl
# infra/modules/ec2spot/v1.0.0/main.tf — on-demand instance
resource "aws_instance" "ec2_ondemand" {
  for_each = local.ec2_ondemand_map
  # ... existing args ...

  dynamic "root_block_device" {
    for_each = var.root_volume_size_gb > 0 || var.hibernation_enabled ? [1] : []
    content {
      volume_size           = var.root_volume_size_gb > 0 ? var.root_volume_size_gb : null
      volume_type           = "gp3"
      encrypted             = var.hibernation_enabled
      delete_on_termination = true
    }
  }

  hibernation = var.hibernation_enabled
}

# Spot instance: root_block_device for sizing only, no encryption, no hibernation
resource "aws_spot_instance_request" "ec2spot" {
  for_each = local.ec2spot_map
  # ... existing args ...

  dynamic "root_block_device" {
    for_each = var.root_volume_size_gb > 0 ? [1] : []
    content {
      volume_size           = var.root_volume_size_gb
      volume_type           = "gp3"
      delete_on_termination = true
      # No encrypted: spot instances don't use hibernation (km pause rejects spot)
    }
  }
}
```

### Pattern 2: Hibernation

**What:** `hibernation = true` on `aws_instance.ec2_ondemand`. Encrypted root volume required.
**When to use:** `spec.runtime.hibernation: true` in profile AND `spec.runtime.spot: false` (on-demand only).
**Constraint:** AWS enforces that hibernation requires encrypted root volume. Terraform will error at plan time if `encrypted = false` and `hibernation = true`.

Verified AWS prerequisites for hibernation (from official docs, 2026-04-02):
1. `hibernation = true` set at launch time (cannot be enabled after launch)
2. Encrypted root EBS volume (`encrypted = true` in `root_block_device`)
3. Root volume large enough to hold RAM contents (t3.medium = 4 GiB RAM; root >= 8 GiB default AL2023 is sufficient for hibernation state, but larger workloads may need 20+ GB)
4. Supported instance family (T3, M5, M6, M7, C5, C6, R5, R6, I3, and many more — full list in AWS docs; t3.medium is confirmed supported)
5. Instance must not have been running for more than 60 days (AWS hibernation duration limit: instance cannot stay hibernated for more than 60 days)
6. Instance RAM must be less than 150 GiB (Linux)
7. On-demand instances only for the km pause workflow (spot instances are explicitly rejected in `pause.go`)

```hcl
variable "hibernation_enabled" {
  type        = bool
  description = "Enable EC2 hibernation. Requires on-demand instances and encrypted root volume."
  default     = false
}
```

The compiler must validate that `hibernation: true` is not combined with `spot: true`. This validation belongs in `pkg/compiler` (semantic layer), not the profile schema (structural layer).

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
  device_name = "/dev/sdf"
  volume_id   = aws_ebs_volume.additional[0].id
  instance_id = var.use_spot
    ? aws_spot_instance_request.ec2spot[keys(local.ec2spot_map)[0]].spot_instance_id
    : aws_instance.ec2_ondemand[keys(local.ec2_ondemand_map)[0]].id
  force_detach = true
}
```

**IMPORTANT:** Use `/dev/sdf` as the `device_name` in `aws_volume_attachment`, not `/dev/xvdf`. AWS API accepts `/dev/sdf`; AL2023 udev rules map it to both `/dev/xvdf` (symlink) and the actual NVMe device (`/dev/nvme1n1`). The user-data script should check `/dev/sdf` or `/dev/xvdf` (both work on AL2023 due to udev symlinks), then `/dev/nvme1n1` as a fallback for Ubuntu where udev symlinks may not be created automatically.

### Pattern 4: Loose AMI Resolution

**What:** Replace the single hardcoded `data "aws_ami" "base_ami"` with a locals map keyed by slug.
**When to use:** Always — even the default case uses this pattern with `amazon-linux-2023` as the resolved slug.

```hcl
locals {
  ami_filters = {
    "amazon-linux-2023" = {
      name_pattern = "al2023-ami-2023.*-x86_64"
      owner        = "amazon"
    }
    "ubuntu-24.04" = {
      name_pattern = "ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"
      owner        = "099720109477"  # Canonical's AWS account ID
    }
    "ubuntu-22.04" = {
      name_pattern = "ubuntu/images/hvm-ssd-gp3/ubuntu-jammy-22.04-amd64-server-*"
      owner        = "099720109477"  # Canonical's AWS account ID
    }
  }
  resolved_ami_slug = var.ami_slug != "" ? var.ami_slug : "amazon-linux-2023"
}

data "aws_ami" "base_ami" {
  count       = local.total_ec2spot_count > 0 ? 1 : 0
  most_recent = true
  owners      = [local.ami_filters[local.resolved_ami_slug].owner]

  filter {
    name   = "name"
    values = [local.ami_filters[local.resolved_ami_slug].name_pattern]
  }
  filter {
    name   = "architecture"
    values = ["x86_64"]
  }
  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}
```

**Ubuntu 22.04 pattern correction:** The original research used `hvm-ssd` for Ubuntu 22.04. Current Canonical documentation specifies `hvm-ssd-gp3` for Ubuntu 22.04 newer images (canonical changed to gp3 storage for 22.04 images post-2023). Use `hvm-ssd-gp3` for both 22.04 and 24.04. If the search returns no results with `hvm-ssd-gp3` for 22.04, fall back to `hvm-ssd` as an alternative — but `hvm-ssd-gp3` is now the correct pattern per Canonical docs.

Canonical owner ID `099720109477` is the stable AWS account ID for all Ubuntu official AMIs across all regions (verified against Ubuntu AWS documentation, 2026-04-02).

Profile value `ami: amazon-linux-2023` or `ami: ubuntu-24.04` maps directly to a map key. The compiler passes the value through as `ami_slug`. An empty string in the profile means "use default" (AL2023).

### Pattern 5: User-Data Auto-Mount for Additional Volume

**What:** User-data section that formats and mounts the additional EBS volume at the specified `mountPoint`. This must be idempotent (check if already formatted).
**When to use:** `additionalVolume.size > 0` in profile.

AL2023 udev note: AL2023 automatically creates a symlink `/dev/xvdf` → actual NVMe device when the volume is attached with device name `/dev/sdf`. The user-data script can reliably use `/dev/xvdf` on AL2023. For Ubuntu, udev symlinks are not automatic — probe NVMe device names directly.

```bash
# ============================================================
# 2.3. Additional EBS volume: format and mount (if configured)
# ============================================================
{{- if .AdditionalVolumeMountPoint }}
echo "[km-bootstrap] Waiting for additional EBS volume to attach..."
DEVICE=""
for i in $(seq 1 30); do
  # AL2023: udev creates /dev/xvdf symlink automatically from /dev/sdf attachment
  # Ubuntu/other: fall through to direct NVMe probe
  for dev in /dev/xvdf /dev/sdf /dev/nvme1n1 /dev/nvme2n1; do
    if [ -b "$dev" ]; then
      # Verify it's not the root device
      ROOT_DEV=$(lsblk -no PKNAME $(df / | tail -1 | awk '{print $1}') 2>/dev/null || echo "")
      DEV_BASE=$(basename "$dev")
      if [ "$DEV_BASE" != "$ROOT_DEV" ] && ! df / 2>/dev/null | grep -q "$dev"; then
        DEVICE="$dev"
        break 2
      fi
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
  # Mount and add to fstab for persistence across reboots
  DEVICE_UUID=$(blkid -s UUID -o value "$DEVICE")
  if [ -n "$DEVICE_UUID" ] && ! grep -q "$DEVICE_UUID" /etc/fstab 2>/dev/null; then
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
- **Enabling hibernation on spot instances:** The current `km pause` explicitly rejects spot instances. Do not add `hibernation = true` to `aws_spot_instance_request`. If spot hibernation is ever desired, it requires `instance_interruption_behavior = "hibernate"` and `type = "persistent"` on the spot request — a separate, larger feature.
- **Formatting the additional volume unconditionally in user-data:** If Terraform taint/redeploy reattaches the same volume, unconditional `mkfs` will destroy data. Always check `blkid` first.
- **Hardcoding only `/dev/nvme1n1` in user-data without AL2023 symlink fallback:** AL2023 udev rules create `/dev/xvdf` automatically; probing that path is more reliable than guessing the NVMe index.
- **Not checking that the detected device is not the root device:** The root volume is also NVMe on Nitro. If the additional volume index is 0 (rare but possible), naive probing can target the root device.
- **Using `hvm-ssd` pattern for Ubuntu 22.04:** Current Canonical images use `hvm-ssd-gp3`. The `hvm-ssd` pattern still matches older images but `hvm-ssd-gp3` is more current.
- **Making `rootVolumeSize` a required field:** It should be optional (pointer or zero-value default) so existing profiles without it continue to work with the AMI's default root volume size.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| AMI ID resolution per region | Region-to-AMI-ID map in Go code | `data "aws_ami"` with filters in Terraform | AWS manages AMI IDs; a static map goes stale immediately |
| EBS encryption for hibernation | Custom KMS key management | `encrypted = true` on `root_block_device` (uses account default CMK) | AWS handles KMS key association automatically; no `aws_kms_key` resource needed |
| Device attachment detection | Polling `/proc/partitions` | Standard `blkid` + wait loop in user-data, relying on AL2023 udev symlinks | `blkid` is the standard Linux tool; AL2023 udev handles the xvd/nvme translation |
| Filesystem persistence on reboot | Custom mount script as systemd unit | `/etc/fstab` with UUID and `nofail` option | fstab is the correct Linux idiom; UUID is stable even if device name changes; `nofail` prevents boot hang if volume detaches |

**Key insight:** All four capabilities have first-class Terraform/AWS support. No custom AWS SDK calls or background daemons are needed.

## Common Pitfalls

### Pitfall 1: Hibernation Not Available at Runtime — Must Be Set at Launch

**What goes wrong:** `km pause` reports "hibernate not available, stopping normally". **This is what happened in production on 2026-04-02.**
**Why it happens:** The Terraform module does not currently set `hibernation = true` on `aws_instance.ec2_ondemand`. AWS requires `hibernation = true` plus an encrypted root volume at launch time. These cannot be added to an existing instance.
**How to avoid:** Phase 33 adds `hibernation = var.hibernation_enabled` to `aws_instance.ec2_ondemand` and adds `root_block_device { encrypted = true }` when hibernation is enabled.
**Warning signs:** `km pause` falls back to normal stop with "hibernate not available" message.
**Detection:** `aws ec2 describe-instances --filters "Name=hibernation-options.configured,Values=true"` — existing instances will not appear.

### Pitfall 2: Spot Instance Volume Attachment Race Condition

**What goes wrong:** `aws_volume_attachment` for the additional volume tries to attach before the spot instance is fully running. The `spot_instance_id` attribute of `aws_spot_instance_request` is only available after the request is fulfilled.
**Why it happens:** `aws_spot_instance_request` sets `spot_instance_id` asynchronously; Terraform's dependency graph may not serialize correctly.
**How to avoid:** The `wait_for_fulfillment = true` flag already present in the module means `spot_instance_id` is populated before attachment. Reference `aws_spot_instance_request.ec2spot[key].spot_instance_id` directly as the `instance_id` in `aws_volume_attachment`.
**Warning signs:** `Error: instance_id is empty` or `Error: volume attachment failed` during apply.

### Pitfall 3: `for_each` on Data Sources With Variable Input

**What goes wrong:** Using `for_each` on `data "aws_ami"` keyed by a variable value causes "The `for_each` map includes keys derived from resource attributes that cannot be determined until apply" errors.
**Why it happens:** Terraform evaluates `for_each` keys at plan time; a variable from input is fine but a computed value is not.
**How to avoid:** Use a local variable (`local.resolved_ami_slug`) with an explicit default, and use `count = 1` on a single `data "aws_ami"` resource. The `ami_filters` local map is a pure local — Terraform resolves it at plan time.
**Warning signs:** `Error: Invalid for_each argument` during plan.

### Pitfall 4: Hibernation Validation Gap — Schema Allows Invalid Combinations

**What goes wrong:** A user sets `hibernation: true` and `spot: true` in the profile. The JSON schema validation passes (both are structurally valid), but Terraform apply fails with an AWS API error.
**Why it happens:** JSON schema cannot express cross-field constraints.
**How to avoid:** Add explicit semantic validation in `pkg/compiler` before generating HCL. Return an error like `hibernation requires on-demand instance (spot: false)`. This follows the existing pattern where cross-field validation is done in Go, not the schema.
**Warning signs:** Terraform plan error referencing `aws_spot_instance_request` + `hibernation`.

### Pitfall 5: Root Volume Not Encrypted When Hibernation Enabled

**What goes wrong:** AWS EC2 API rejects the `RunInstances` call if hibernation is requested but the root volume is not encrypted. Error: `InvalidParameterCombination: Hibernation enabled but root volume is not encrypted`.
**Why it happens:** Hibernation writes RAM contents to the encrypted root volume; AWS enforces encryption.
**How to avoid:** In the Terraform module, the `root_block_device` dynamic block must be emitted whenever `hibernation_enabled = true` (even if `root_volume_size_gb = 0`), and `encrypted = var.hibernation_enabled` ensures encryption is set when hibernation is on.
**Warning signs:** `Error: InvalidParameterCombination: Hibernation enabled but root volume is not encrypted`.

### Pitfall 6: `additionalVolume` on ECS Substrate

**What goes wrong:** `additionalVolume` is an EC2-only concept. If an ECS profile accidentally includes it, the compiler generates invalid HCL or silently ignores it.
**Why it happens:** Profile fields are not substrate-scoped in the current schema.
**How to avoid:** Add semantic validation in `pkg/compiler`: if `substrate == "ecs"` and `additionalVolume` is set, return an error. Similarly for `hibernation`.
**Warning signs:** ECS profile fails with unexpected Terraform errors referencing EBS resources.

### Pitfall 7: NVMe Device Name Instability on Non-AL2023

**What goes wrong:** User-data script on Ubuntu uses `/dev/xvdf` to find the additional volume, but `/dev/xvdf` is an AL2023 udev symlink that Ubuntu does not create automatically. The script logs "volume not found" even though the volume attached.
**Why it happens:** AL2023 udev rules map `/dev/sdf` attachments to `/dev/xvdf` automatically. Ubuntu and other distros do not have this udev rule by default and use pure NVMe naming.
**How to avoid:** The user-data probe loop must include both `/dev/xvdf` (AL2023) and `/dev/nvme1n1` (Ubuntu/direct NVMe). Also add a root-device guard to avoid accidentally targeting the root volume.
**Warning signs:** Additional volume mount section logs "WARNING: additional EBS volume device not found after 60s" on Ubuntu AMIs.

### Pitfall 8: Ubuntu 22.04 AMI Name Pattern

**What goes wrong:** Using `hvm-ssd` filter pattern for Ubuntu 22.04 matches older images but not the current ones, which use `hvm-ssd-gp3`.
**Why it happens:** Canonical changed the AMI naming to `hvm-ssd-gp3` for newer 22.04 images (post-2023).
**How to avoid:** Use `ubuntu/images/hvm-ssd-gp3/ubuntu-jammy-22.04-amd64-server-*` for the Ubuntu 22.04 filter.
**Warning signs:** `data.aws_ami.base_ami` returns an older AMI than expected, or no results.

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
    RootVolumeSize   int                   `yaml:"rootVolumeSize,omitempty"`   // GB; 0 = AMI default
    AdditionalVolume *AdditionalVolumeSpec `yaml:"additionalVolume,omitempty"` // nil = no additional volume
    Hibernation      bool                  `yaml:"hibernation,omitempty"`       // on-demand only
    AMI              string                `yaml:"ami,omitempty"`               // slug: "amazon-linux-2023", "ubuntu-24.04", etc.
}

// AdditionalVolumeSpec describes an additional EBS data volume.
type AdditionalVolumeSpec struct {
    Size       int    `yaml:"size"`                   // GB; required
    MountPoint string `yaml:"mountPoint"`             // e.g. /data; required
    Encrypted  bool   `yaml:"encrypted,omitempty"`    // default false
}
```

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

### HCL Template: New module-level variables

The new variables are module-level inputs (not per-instance in the `ec2spots` list), because all instances in a sandbox share the same profile. Add them alongside existing module inputs:

```hcl
# In ec2ServiceHCLTemplate, inside module_inputs (after enable_bedrock):
    root_volume_size_gb    = {{ .RootVolumeSizeGB }}
    hibernation_enabled    = {{ .HibernationEnabled }}
    ami_slug               = "{{ .AMISlug }}"
```

Extended `ec2HCLParams` struct:
```go
type ec2HCLParams struct {
    // ... existing fields ...
    RootVolumeSizeGB   int
    HibernationEnabled bool
    AMISlug            string
    // AdditionalVolume fields handled separately via userdata
}
```

Populating the new params:
```go
RootVolumeSizeGB:   p.Spec.Runtime.RootVolumeSize,  // 0 means AMI default
HibernationEnabled: p.Spec.Runtime.Hibernation,
AMISlug: func() string {
    if p.Spec.Runtime.AMI != "" {
        return p.Spec.Runtime.AMI
    }
    return "amazon-linux-2023"
}(),
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Hardcoded AL2023 AMI filter in `data "aws_ami"` | Parameterized AMI slug → filter map in locals | This phase | Profiles can specify OS; default stays AL2023 |
| No root volume configuration (AMI default ~8GB) | Optional `rootVolumeSize` drives `root_block_device` | This phase | Larger workloads needing >8GB can specify 30-100GB |
| No hibernation support (pause falls back to stop) | `hibernation = true` on on-demand instances with encrypted root | This phase | `km pause` actually hibernates instead of stopping |
| No additional storage beyond root | Optional `additionalVolume` with auto-mount | This phase | Data-heavy workloads get a dedicated volume at a known path |
| Ubuntu 22.04 AMI pattern `hvm-ssd` | Current: `hvm-ssd-gp3` | Canonical changed 2023+ | `hvm-ssd-gp3` filter returns more recent Ubuntu 22.04 images |

**Deprecated/outdated from original research:**
- Ubuntu 22.04 `hvm-ssd` pattern: use `hvm-ssd-gp3` instead (Canonical changed naming)
- Assumption "spot instances cannot hibernate": AWS supports spot hibernation via persistent request type, but this is out of scope for Phase 33 given existing `km pause` behavioral decision

## Open Questions

1. **Should `rootVolumeSize` also apply to spot instances?**
   - What we know: `root_block_device` is valid on `aws_spot_instance_request` as well.
   - Recommendation: Yes — apply `rootVolumeSize` to both spot and on-demand instances. Encryption on root volume is only forced when hibernation is enabled.

2. **Which AMI slugs should be supported at launch?**
   - Recommendation: Support `amazon-linux-2023`, `ubuntu-24.04`, and `ubuntu-22.04` with x86_64 only for Phase 33. ARM64 support is additive.

3. **Additional volume for spot instances — data persistence after interruption**
   - What we know: `aws_ebs_volume` (separate resource) persists after spot interruption. The fstab entry already exists from first boot, so `mount -a` on restart will remount it.
   - Recommendation: Document this behavior; no additional code needed.

4. **What happens if `km pause` is called on an on-demand instance that was launched without hibernation (pre-Phase 33)?**
   - What we know: `pause.go` already handles this gracefully — it falls back to normal stop when `UnsupportedHibernationConfiguration` is returned.
   - Recommendation: No code change needed; the fallback is correct and intentional.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` package (standard) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./pkg/compiler/... ./pkg/profile/... -run "RootVolume\|AdditionalVolume\|Hibernat\|AMI" -count=1` |
| Full suite command | `go test ./pkg/profile/... ./pkg/compiler/... -count=1` |

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
| P33-09 | `hibernation_enabled = true` forces `encrypted = true` in root_block_device HCL | unit | `go test ./pkg/compiler/... -run TestHibernationForceEncryption -v` | No — Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/compiler/... ./pkg/profile/... -v`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/compiler/ec2_storage_test.go` — covers P33-01 through P33-09
- [ ] `pkg/profile/schema_storage_test.go` — covers P33-07 (schema validation for new fields)

## Sources

### Primary (HIGH confidence)
- Direct code inspection of `infra/modules/ec2spot/v1.0.0/main.tf` and `variables.tf` — current state confirmed, no hibernation or root_block_device present
- Direct code inspection of `internal/app/cmd/pause.go` — confirmed UnsupportedHibernationConfiguration fallback at lines 144-150; confirmed spot instance rejection at lines 133-137
- Direct code inspection of `pkg/profile/types.go` — `RuntimeSpec` does not yet have Phase 33 fields
- AWS documentation: [EC2 hibernation prerequisites](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/hibernating-prerequisites.html) — verified 2026-04-02: supported instance families, encrypted root requirement, 150 GiB RAM limit, 60-day hibernation duration limit
- AWS documentation: [Enable hibernation](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/enabling-hibernation.html) — verified 2026-04-02: hibernation must be set at launch via `--hibernation-options Configured=true`; spot instances need `persistent` request type
- Ubuntu AWS documentation: [Find Ubuntu AMIs](https://documentation.ubuntu.com/aws/aws-how-to/instances/find-ubuntu-images/) — verified 2026-04-02: Canonical owner ID `099720109477`, Ubuntu 24.04 uses `hvm-ssd-gp3` pattern, Ubuntu 22.04 uses `hvm-ssd-gp3` for newer images

### Secondary (MEDIUM confidence)
- AWS documentation: [Map EBS volumes to NVMe device names](https://docs.aws.amazon.com/ebs/latest/userguide/identify-nvme-ebs-device.html) — AL2023 udev creates `/dev/xvdf` symlinks; Ubuntu does not; `blkid`+UUID mount is stable across reboots
- Terraform Registry `aws_spot_instance_request` resource docs — `instance_interruption_behavior` supports `hibernate` for persistent requests; verified via web search 2026-04-02

### Tertiary (LOW confidence)
- Ubuntu 22.04 `hvm-ssd-gp3` naming change timing — stated as post-2023 change, but exact version/date boundary for when old `hvm-ssd` images stopped being published is not verified against a canonical source

## Operational Findings (2026-04-02)

Observed in production: `km pause goose-a7a2431c` reported "hibernate not available, stopping normally" and fell back to a plain EC2 stop. Root cause confirmed by code inspection: the EC2 instance was not launched with `hibernation = true` (not present in `aws_instance.ec2_ondemand`) and lacked an encrypted root volume (no `root_block_device` with `encrypted = true` in the Terraform module).

Summary of what Phase 33 must add to fix this:

1. `hibernation = var.hibernation_enabled` on `aws_instance.ec2_ondemand`
2. `root_block_device { encrypted = true }` when `hibernation_enabled = true` (even if no explicit size)
3. New Terraform variable `hibernation_enabled` (default `false`)
4. Compiler semantic validation rejecting `hibernation: true` + `spot: true`
5. Profile schema fields: `RootVolumeSize`, `AdditionalVolume`, `Hibernation`, `AMI`

**Substrate label change:** Substrate metadata now stores `ec2spot` or `ec2demand` (not bare `ec2`). The `pause.go` command routes on the EC2 API instance lifecycle type (`ec2types.InstanceLifecycleTypeSpot`), not the stored substrate label, so this change does not affect pause logic. The compiler `service_hcl.go` already uses `ec2spot` as the `substrate_module` value.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all capabilities use established Terraform AWS provider patterns; hibernation prerequisites verified against current AWS docs
- Architecture: HIGH — patterns follow existing Phase 2-27 conventions; operational failure clarified the exact Terraform change needed
- Pitfalls: HIGH for hibernation prerequisites (confirmed by operational failure + AWS docs); HIGH for Ubuntu 22.04 AMI pattern (verified against Canonical docs); MEDIUM for NVMe device probe ordering in user-data

**Research date:** 2026-04-02 (original: 2026-03-28)
**Valid until:** 2026-05-02 (AWS provider stable; Terraform patterns long-lived)
