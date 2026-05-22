# `additionalSnapshots` — snapshot-backed EBS volumes in SandboxProfile — design

**Status:** Drafted from brainstorming session — awaiting user review before plan-out.
**Author:** brainstorming session, 2026-05-21.
**Date:** 2026-05-21.

## Problem

Today a profile can declare exactly one extra EBS volume via `spec.runtime.additionalVolume` — and that volume is always provisioned **empty** (`aws_ebs_volume` with no `snapshot_id`), then formatted ext4 on first boot. There is no way for a profile to declare *"attach a volume materialised from an existing EBS snapshot."*

The operator use case driving this is straightforward: an EBS snapshot already exists out-of-band (a curated dataset, a model cache, a git mirror, a pre-warmed build tree), and every sandbox spun up from a given profile should mount a fresh writable copy of it. Today the operator has to bake the snapshot's contents into an AMI (heavy, slow, region-bound) or shell into each sandbox and restore it manually. Neither composes with `km create`.

This spec adds an opt-in `spec.runtime.additionalSnapshots: [...]` field — a list of `(snapshotId, mountPoint, device?, encrypted?, size?)` tuples. On `km create`, each entry produces one `aws_ebs_volume` whose `snapshot_id` is the declared snapshot, plus an `aws_volume_attachment` and a userdata-mounted entry in `/etc/fstab`. The materialised volumes share the lifecycle of `additionalVolume`: they're owned by the sandbox's terragrunt state and disappear on `km destroy`. The source snapshot is never touched.

## Goals

- Profiles can declare 0..N snapshot-backed EBS volumes alongside (not instead of) the existing `additionalVolume`.
- Each entry produces a fresh `aws_ebs_volume` from the declared snapshot, attached to the sandbox at boot, mounted at the declared `mountPoint`, owned by the sandbox's terragrunt state.
- Device names are picked safely: each entry may pin `/dev/sd[f-p]` explicitly, or let the compiler auto-allocate. Auto-allocation honours AMI block-device-mappings, the existing `additionalVolume`'s claimed device, and other entries in the same list.
- The filesystem on the snapshot is preserved as-is: userdata mounts whatever FS `blkid` reports and never reformats a volume that already has a filesystem.
- Validation is layered: `km validate` checks schema (format, uniqueness, mountpoint sanity, EC2-only); `km create` additionally calls `DescribeSnapshots` to fail fast if a snapshot is missing, not `completed`, or inaccessible — before terragrunt runs.
- Behaviour and naming match the existing `additionalVolume` patterns where possible — same device-rotation helper, same userdata mount idioms, same EC2-only restriction, same lifecycle.

## Non-goals (YAGNI cuts deferred to future specs)

- **No `preserveOnDestroy` / `snapshotOnDestroy` per-entry knobs.** The materialised volume always dies with the sandbox (`km destroy`). If operators later need snapshot-restore-then-snapshot-on-destroy round-trips, that earns its own spec.
- **No KMS key selection.** v1 honours AWS's default behaviour: if the snapshot is encrypted, the new volume is encrypted with the same key; if `encrypted: true` is set explicitly on an unencrypted snapshot, AWS encrypts the new volume with the account's default EBS KMS key. No `kmsKeyId` field.
- **No cross-account snapshot validation warnings.** `DescribeSnapshots` will simply error if the caller's IAM role can't see the snapshot; that error is surfaced verbatim to the operator with a hint about cross-account sharing.
- **No unification with `additionalVolume` into a single `additionalVolumes` list.** Both fields coexist. A future spec may deprecate `additionalVolume`, but not in this phase.
- **No support for non-EC2 substrates.** ECS/Docker substrates reject `additionalSnapshots` at validation time — same rule as today's `additionalVolume`.
- **No `learnMode` integration.** Learned profiles do not emit `additionalSnapshots`. The field is operator-authored only.
- **No size auto-shrink.** EBS does not support shrinking a volume below its snapshot size; if `size:` is specified and `< snapshot.VolumeSize`, validation fails at create time with a clear error.

## Schema

New optional field on `spec.runtime`, parallel to `additionalVolume`:

```yaml
spec:
  runtime:
    substrate: ec2
    ami: amazon-linux-2023

    # Existing — unchanged
    additionalVolume:
      size: 30
      mountPoint: /data

    # NEW — N snapshot-backed volumes, each materialised fresh per sandbox
    additionalSnapshots:
      - snapshotId: snap-0123abcdef0123456
        mountPoint: /opt/models       # required
        device: /dev/sdh              # optional; auto-picked from /dev/sd[f-p] if omitted
        encrypted: true               # optional; default = snapshot's encryption state
        size: 200                     # optional; default = snapshot size; must be >= snapshot size
      - snapshotId: snap-04567890abcdef012
        mountPoint: /opt/cache
        # device auto-picked, no other options
```

### Go type (`pkg/profile/types.go`)

```go
// AdditionalSnapshotSpec defines an extra EBS volume materialised from a snapshot
// and auto-mounted at boot. The source snapshot is never modified; the volume is
// destroyed with the sandbox.
type AdditionalSnapshotSpec struct {
    // SnapshotID is the EBS snapshot to materialise. Must exist in the same region
    // as the sandbox and be in `completed` state. Format: ^snap-[0-9a-f]{8,17}$.
    SnapshotID string `yaml:"snapshotId" json:"snapshotId"`
    // MountPoint is the absolute filesystem path to mount the materialised volume at.
    // Must not collide with /, /shared (EFS), /workspace (root), additionalVolume.MountPoint,
    // or any other entry in additionalSnapshots.
    MountPoint string `yaml:"mountPoint" json:"mountPoint"`
    // Device is the AWS device name (e.g. /dev/sdh). Optional — when omitted, the
    // compiler auto-allocates from /dev/sd[f-p] avoiding AMI block-device-mappings
    // and other claimed devices.
    Device string `yaml:"device,omitempty" json:"device,omitempty"`
    // Encrypted optionally forces the materialised volume's encryption state.
    // Pointer so omitted ≠ false: when nil, terraform receives `null` and AWS
    // inherits the snapshot's encryption (an encrypted snapshot always produces
    // an encrypted volume; an unencrypted snapshot produces an unencrypted volume
    // unless this field is explicitly `true`). Setting `false` on an encrypted
    // snapshot is rejected by AWS at apply time.
    Encrypted *bool `yaml:"encrypted,omitempty" json:"encrypted,omitempty"`
    // Size optionally overrides the materialised volume size in GB. Must be >= the
    // source snapshot's size. When 0/omitted, the volume is sized to match the snapshot.
    Size int `yaml:"size,omitempty" json:"size,omitempty"`
}

type RuntimeSpec struct {
    // ...existing fields unchanged...
    AdditionalVolume    *AdditionalVolumeSpec    `yaml:"additionalVolume,omitempty" json:"additionalVolume,omitempty"`
    AdditionalSnapshots []AdditionalSnapshotSpec `yaml:"additionalSnapshots,omitempty" json:"additionalSnapshots,omitempty"`
}
```

### JSON schema (`pkg/profile/schemas/sandbox_profile.schema.json`)

A sibling `additionalSnapshots` array entry next to `additionalVolume`, with item schema:

```json
{
  "additionalSnapshots": {
    "type": "array",
    "items": {
      "type": "object",
      "required": ["snapshotId", "mountPoint"],
      "properties": {
        "snapshotId": { "type": "string", "pattern": "^snap-[0-9a-f]{8,17}$" },
        "mountPoint": { "type": "string", "pattern": "^/" },
        "device":     { "type": "string", "pattern": "^/dev/sd[f-p]$" },
        "encrypted":  { "type": "boolean" },
        "size":       { "type": "integer", "minimum": 1 }
      },
      "additionalProperties": false
    }
  }
}
```

## Validation

Two layers — schema at `km validate`, AWS at `km create`.

### Layer 1: schema (`pkg/profile/validate.go`)

Runs on every `km validate` and as a prerequisite to `km create`:

- **EC2-only:** reject `additionalSnapshots` for any non-`ec2*` substrate, with the same error wording as today's `additionalVolume` check (`pkg/compiler/service_hcl.go:681`).
- **`snapshotId` format:** must match `^snap-[0-9a-f]{8,17}$`. (AWS allows 8- or 17-character hex IDs.)
- **`mountPoint` is absolute and safe:**
  - Must start with `/`.
  - Must not equal `/`, `/shared` (reserved for EFS), `/workspace` (reserved for the root-disk working dir), `/proc`, `/sys`, `/dev`, `/etc`, `/usr`, `/var`, `/root`, `/home`, `/boot`, `/tmp`, `/run`, `/opt` (top-level only — `/opt/foo` is fine).
  - Must not equal `additionalVolume.MountPoint` (when both are set).
  - Must be unique across all `additionalSnapshots` entries.
- **`device` (when set):** must match `^/dev/sd[f-p]$`, must be unique across entries, must not equal the device the compiler would pick for `additionalVolume`.
- **`size` (when set):** must be `>= 1`. The `>= snapshot.VolumeSize` check happens in Layer 2 (it requires an API call).

### Layer 2: AWS pre-flight (`pkg/profile/aws_validate.go` — new file)

Runs only on `km create`, before the compiler emits any HCL. Single `DescribeSnapshots` call carrying every snapshot ID in the profile:

- All declared snapshots must be in the sandbox's target region (AWS naturally enforces this — out-of-region snapshots return `InvalidSnapshot.NotFound`).
- Every returned snapshot must have `State == "completed"`. `pending` or `error` is rejected.
- For each entry with an explicit `size`, `size >= snapshot.VolumeSize` is required.
- If `DescribeSnapshots` errors with `InvalidSnapshot.NotFound`, the error message names the missing snapshot ID and hints: *"snapshot may be in another region, not yet shared with this account, or deleted."*
- If the caller lacks `ec2:DescribeSnapshots` (e.g. operator's IAM role), the pre-flight is skipped with a WARN — terragrunt will surface the real error during apply. (Same degradation pattern as `km doctor` AWS checks.)

## Compiler changes

### `pkg/compiler/service_hcl.go`

1. **Extend `pickAdditionalVolumeDevice`** to take an already-claimed set:
   ```go
   func pickAdditionalVolumeDevice(amiDevices []string, claimed map[string]bool) string
   ```
   `additionalVolume` calls it with `claimed = nil` (back-compat). `additionalSnapshots` iterates entries, calling it for each and adding the picked device to `claimed` before the next call.

2. **New template fields** on the EC2 service.hcl render struct:
   ```go
   AdditionalSnapshots []AdditionalSnapshotEntry  // ordered, after device-allocation
   ```
   where:
   ```go
   type AdditionalSnapshotEntry struct {
       SnapshotID  string
       DeviceName  string  // resolved (auto or explicit)
       MountPoint  string
       Encrypted   bool
       SizeGB      int     // 0 = inherit from snapshot
   }
   ```

3. **Template addition** in `ec2ServiceHCLTemplate` — emit the list as terraform input:
   ```hcl
   additional_snapshots = [
   {{- range .AdditionalSnapshots }}
     {
       snapshot_id  = "{{ .SnapshotID }}"
       device_name  = "{{ .DeviceName }}"
       encrypted    = {{ .Encrypted }}
       size_gb      = {{ .SizeGB }}
     },
   {{- end }}
   ]
   ```

4. **Userdata template inputs** also gain a parallel list (devices + mountpoints only) so userdata can iterate. Specifically a new `AdditionalSnapshotMounts []AdditionalSnapshotMount` field on the userdata render struct.

### `pkg/compiler/userdata.go`

Replace the current single `{{- if .AdditionalVolumeMountPoint }}` block with a loop that handles both `additionalVolume` (when present) and each `additionalSnapshots` entry:

```bash
{{- range .AdditionalVolumeMounts }}

# ============================================================
# Additional EBS volume mount: {{ .Device }} -> {{ .MountPoint }}{{ if .FromSnapshot }} (from {{ .SnapshotID }}){{ end }}
# ============================================================
echo "[km-bootstrap] Waiting for EBS volume {{ .Device }} ({{ .MountPoint }}) to attach..."
DEVICE=""
for i in $(seq 1 30); do
  # Probe both raw device name and NVMe alias
  for dev in "{{ .Device }}" "{{ nvmeAlias .Device }}"; do
    if [ -b "$dev" ]; then
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
  # Snapshot-backed volumes have a filesystem already; blank volumes don't.
  if ! blkid "$DEVICE" &>/dev/null; then
    echo "[km-bootstrap] Formatting $DEVICE as ext4..."
    mkfs.ext4 -F "$DEVICE"
  fi
  FSTYPE=$(blkid -s TYPE -o value "$DEVICE" 2>/dev/null || echo ext4)
  mkdir -p "{{ .MountPoint }}"
  DEVICE_UUID=$(blkid -s UUID -o value "$DEVICE")
  if [ -n "$DEVICE_UUID" ] && ! grep -q "$DEVICE_UUID" /etc/fstab 2>/dev/null; then
    echo "UUID=${DEVICE_UUID} {{ .MountPoint }} ${FSTYPE} defaults,nofail 0 2" >> /etc/fstab
  fi
  mount -a
  chown sandbox:sandbox "{{ .MountPoint }}" 2>/dev/null || true
  echo "[km-bootstrap] Mounted $DEVICE at {{ .MountPoint }} (FS: ${FSTYPE})"
else
  echo "[km-bootstrap] WARNING: EBS volume {{ .Device }} not found after 60s"
fi
{{- end }}
```

Two behavioural changes vs today:

1. **FS type detected**, not hard-coded `ext4`. The existing `mkfs.ext4`-only-if-no-FS branch already handled snapshot-restored ext4 volumes by accident; we now also handle xfs/btrfs/etc. correctly in fstab.
2. **Loop body emitted once per mount** (`additionalVolume` + each `additionalSnapshots` entry).

The `nvmeAlias` template helper maps `/dev/sdX` to `/dev/xvdX`.

## Terraform module changes

### `infra/modules/ec2spot/v1.0.0/variables.tf`

Add one new variable:

```hcl
variable "additional_snapshots" {
  type = list(object({
    snapshot_id = string
    device_name = string
    encrypted   = optional(bool)    # null = inherit from snapshot
    size_gb     = optional(number)  # null/0 = inherit from snapshot
  }))
  default = []
}
```

The compiler emits `encrypted = null` (not `false`) when the operator omitted the field — preserving "inherit snapshot state" semantics. Go side: `*bool` field marshals to literal `null` via the HCL template helper used elsewhere in `service_hcl.go`.

`additional_volume_size_gb` / `_encrypted` / `_device_name` stay as-is — backwards compatible.

### `infra/modules/ec2spot/v1.0.0/main.tf`

Add sibling resources next to the existing `aws_ebs_volume.additional` / `aws_volume_attachment.additional`:

```hcl
# ============================================================
# Additional EBS volumes from snapshots (Phase 87)
# ============================================================

resource "aws_ebs_volume" "snapshot" {
  for_each          = { for i, s in var.additional_snapshots : i => s }
  availability_zone = local.effective_azs[0]
  snapshot_id       = each.value.snapshot_id
  size              = try(each.value.size_gb, null) != null && each.value.size_gb > 0 ? each.value.size_gb : null
  encrypted         = try(each.value.encrypted, null)
  type              = "gp3"

  tags = {
    "km:sandbox-id"      = var.sandbox_id
    "km:resource-prefix" = var.resource_prefix
    "km:source-snapshot" = each.value.snapshot_id
    Name                 = "km-sandbox-${var.sandbox_id}-snap-${each.key}"
  }
}

resource "aws_volume_attachment" "snapshot" {
  for_each     = { for i, s in var.additional_snapshots : i => s }
  device_name  = each.value.device_name
  volume_id    = aws_ebs_volume.snapshot[each.key].id
  instance_id  = length(local.ec2spot_map) > 0 ? aws_spot_instance_request.ec2spot[keys(local.ec2spot_map)[0]].spot_instance_id : aws_instance.ec2_ondemand[keys(local.ec2_ondemand_map)[0]].id
  force_detach = true
}
```

Lifecycle: same as `additional` — destroyed with the sandbox. No `prevent_destroy`. The source snapshot is referenced by ID only; terraform never touches it.

### Module version bump

This is a backward-compatible additive change to a versioned module (`v1.0.0`). Per Phase 80 conventions for tagged immutable modules, this requires a new minor version: `infra/modules/ec2spot/v1.1.0/` (copy of v1.0.0 + the diff above), with `compiler.go` updated to emit `v1.1.0` in the source path. Old sandboxes pinned to `v1.0.0` keep working unchanged.

## Failure modes

| Condition | Where caught | Behaviour |
|---|---|---|
| `snapshotId` malformed | `km validate` (regex) | Profile rejected, error names the bad entry index. |
| `mountPoint` collides with EFS/reserved/another entry | `km validate` | Profile rejected, error names both colliding entries. |
| `device` collides with another entry or AMI BDM | `km validate` (when explicit) / compiler (when auto-pick exhausts pool) | Explicit collision → validation error. Auto-pick exhaustion (>11 entries + AMI volumes) → compiler error naming the offending entry. |
| Snapshot doesn't exist / wrong region / not shared | `km create` pre-flight (`DescribeSnapshots`) | Create aborts before terragrunt runs; error names snapshot ID + region + 3-line hint. |
| Snapshot in `pending` or `error` state | `km create` pre-flight | Create aborts; error names snapshot ID + state. |
| `size < snapshot.VolumeSize` | `km create` pre-flight | Create aborts; error states both sizes. |
| Operator IAM lacks `ec2:DescribeSnapshots` | `km create` pre-flight | WARN logged, pre-flight skipped, terragrunt apply runs as fallback (slower failure path). |
| EBS volume fails to attach within 60s | userdata | `[km-bootstrap] WARNING` logged to `/var/log/km-bootstrap.log`; sandbox boots without the mount. `km otel <sb>` surfaces the warning. (Same behaviour as today's `additionalVolume`.) |
| Snapshot decryption fails (KMS key not granted to EC2) | EC2 boot | Volume attachment fails; userdata WARNs; sandbox boots without the mount. Operator runbook: grant `kms:CreateGrant` on the key to the sandbox's EC2 service role. |

## Backward compatibility

- `additionalVolume` is unchanged in syntax, semantics, terraform shape, and userdata behaviour.
- Profiles without `additionalSnapshots` produce zero diff in the rendered HCL or userdata.
- Existing sandboxes pinned to `ec2spot/v1.0.0` are untouched; the new module is `ec2spot/v1.1.0`.
- The userdata mount block is refactored from a single `{{- if }}` to a `{{- range }}`, but the rendered bash for the `additionalVolume`-only case is byte-identical save for switching `ext4` → `${FSTYPE}` in the fstab line (which still resolves to `ext4` for blank volumes).

## Testing

### Unit (Go)

- `pkg/profile/types_test.go` — parse a YAML with 0, 1, and 3 snapshot entries; assert struct shape.
- `pkg/profile/validate_test.go` — table-driven cases for every Layer 1 rule (bad regex, collision, reserved mountpoint, non-EC2 substrate, explicit-device dupes).
- `pkg/profile/aws_validate_test.go` (new) — mock `DescribeSnapshots` to cover: happy path, NotFound, pending state, size override < snapshot size, IAM-missing degradation.
- `pkg/compiler/service_hcl_test.go` — assert the rendered HCL has one `additional_snapshots = [...]` block per entry, with correct device allocation when some are explicit and others auto.
- `pkg/compiler/userdata_test.go` — golden-file test that asserts the rendered userdata for `additionalVolume` alone is byte-identical to the pre-refactor output (modulo the `${FSTYPE}` substitution), and that adding snapshot entries emits one mount block per entry in declaration order.
- `pkg/compiler/ec2_storage_test.go` (existing) — extend coverage to assert auto-picked devices avoid both AMI BDM and prior entries.

### Integration / UAT (operator-driven)

- **UAT-1: single snapshot, auto-device.** Create a fresh snapshot via `aws ec2 create-snapshot` from a known-good volume. Author a profile with one `additionalSnapshots` entry, no `device`. `km create`, SSM in, verify `mount | grep <mountPoint>`, verify file contents match the source. `km destroy`, verify `aws ec2 describe-volumes` shows the materialised volume gone, source snapshot intact.
- **UAT-2: two snapshots + `additionalVolume`.** All three should mount. Devices: `/dev/sdf` (additionalVolume, auto), `/dev/sdg` and `/dev/sdh` (snapshots, auto). Verify no collision.
- **UAT-3: explicit devices.** Pin one entry to `/dev/sdh`. Verify the rendered HCL respects the pin.
- **UAT-4: AMI BDM collision.** Use a baked AMI whose BDM already declares `/dev/sdf`. Verify auto-pick lands on `/dev/sdg` for both `additionalVolume` and the first snapshot.
- **UAT-5: missing snapshot.** Profile references `snap-deadbeefdeadbeef0`. `km create` fails at pre-flight with the documented error wording; no terragrunt artifact is left on disk.
- **UAT-6: wrong region.** Snapshot exists in `us-east-1`, profile targets `us-west-2`. Same failure as UAT-5.
- **UAT-7: size override.** Snapshot is 50 GB. Profile sets `size: 100`. Verify materialised volume is 100 GB.
- **UAT-8: shrink rejected.** Snapshot is 50 GB. Profile sets `size: 20`. Pre-flight rejects before terragrunt runs.

## Future work (deliberately out of scope)

- **`snapshotOnDestroy: true`** — auto-snapshot the materialised volume before `km destroy` so the next sandbox can resume from it. Needs a Lambda or a `aws_ebs_snapshot` resource + tag-based discovery.
- **`preserveOnDestroy: true`** — `lifecycle.prevent_destroy = true` on the volume so it survives `km destroy`. Useful for handing off long-lived data between sandboxes.
- **Unified `additionalVolumes: [...]`** — collapse `additionalVolume` (singular) and `additionalSnapshots` into one list where each entry is either `{size}` or `{snapshotId}`. Schema break; deferred until both fields are stable in production.
- **`kmsKeyId`** — explicit KMS key for re-encryption. Today's default (snapshot's key or account default) covers the common case.
- **Cross-region snapshot copy.** If a snapshot lives in a different region, today the operator copies it manually. A future `km snapshot copy <id> --to <region>` helper could automate that.
- **`learnMode` integration** — auto-detect interesting datasets and propose snapshot-backed entries in the generated profile. Speculative.
