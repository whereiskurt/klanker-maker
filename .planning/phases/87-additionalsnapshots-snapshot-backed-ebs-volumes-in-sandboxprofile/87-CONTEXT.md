# Phase 87: additionalSnapshots — snapshot-backed EBS volumes in SandboxProfile — Context

**Gathered:** 2026-05-21
**Status:** Ready for planning
**Source:** PRD Express Path — `docs/superpowers/specs/2026-05-21-additional-snapshots-design.md`

<domain>
## Phase Boundary

Add a new `spec.runtime.additionalSnapshots: [...]` field to SandboxProfile — a list of `(snapshotId, mountPoint, device?, encrypted?, size?)` tuples. Each entry materialises a fresh `aws_ebs_volume` from an existing EBS snapshot, attaches it on `/dev/sd[f-p]` (auto-allocated or pinned), and mounts via userdata-detected filesystem type. Coexists with the existing `additionalVolume` field — both can be set on the same profile. EC2-only.

What's IN this phase:

- Schema: `AdditionalSnapshotSpec` Go type + JSON schema entry + `RuntimeSpec.AdditionalSnapshots` field.
- Layer 1 validation (`km validate`, no AWS calls): EC2-only substrate, regex on `snapshotId`, mountpoint sanity, device uniqueness, size positive.
- Layer 2 validation (`km create` only): single `DescribeSnapshots` pre-flight covering state, region, size override, IAM degradation.
- Compiler: extend `pickAdditionalVolumeDevice` to take a `claimed` set; render `additional_snapshots = [...]` HCL block; pass parallel mount list to userdata.
- Userdata: refactor the single `additionalVolume` mount block into a `range` loop with `blkid`-detected FS type; preserve byte-identical output for the `additionalVolume`-only case (modulo `ext4` → `${FSTYPE}`).
- Terraform module: new minor version `ec2spot/v1.1.0` with `additional_snapshots` variable + `aws_ebs_volume.snapshot` + `aws_volume_attachment.snapshot` resources (per-entry, `for_each`).
- Compiler bump: emit `ec2spot/v1.1.0` source path on new sandboxes.
- Tests: Go unit (types, validate, aws_validate, service_hcl, userdata, ec2_storage) + 8 operator-driven UAT scenarios.

What's OUT (deferred per spec § Non-goals):

- `preserveOnDestroy` / `snapshotOnDestroy` per-entry knobs.
- `kmsKeyId` field — v1 uses AWS defaults.
- Cross-account snapshot validation warnings (rely on verbatim AWS error + hint).
- Unifying `additionalVolume` and `additionalSnapshots` into one list.
- Non-EC2 substrates (ECS/Docker reject at validation).
- `learnMode` integration — field is operator-authored only.
- Size auto-shrink — pre-flight hard-fails when `size < snapshot.VolumeSize`.

</domain>

<decisions>
## Implementation Decisions

These are LOCKED — derived from the PRD spec (`docs/superpowers/specs/2026-05-21-additional-snapshots-design.md`). Planner: do NOT revisit these unless a hard constraint surfaces during research that makes one unimplementable.

### Schema shape

- New field `spec.runtime.additionalSnapshots` — a list (0..N entries), parallel to existing `spec.runtime.additionalVolume` (singular). Both can be set.
- Per-entry required fields: `snapshotId`, `mountPoint`.
- Per-entry optional fields: `device`, `encrypted`, `size`.
- `Encrypted` is `*bool` (pointer) in Go — omitted (nil) marshals to terraform `null`, allowing AWS to inherit the snapshot's encryption state. Pointer (not plain `bool`) chosen deliberately so omitted ≠ false.
- `Size` is `int` GB; `0` / omitted = inherit snapshot size. Explicit `size < snapshot.VolumeSize` is rejected at create-time pre-flight (Layer 2).
- `snapshotId` regex `^snap-[0-9a-f]{8,17}$` (AWS supports both 8- and 17-character hex IDs).
- `device` (when explicit) regex `^/dev/sd[f-p]$`; auto-allocation pool is the same `/dev/sd[f-p]` range.

### Validation — two layers

- **Layer 1 (`km validate`, no AWS calls):** EC2-only substrate, snapshotId regex, mountpoint absoluteness + collision rules + reserved-path blocklist, explicit-device uniqueness, size ≥ 1. Same error-wording style as today's `additionalVolume` EC2-only check.
- **Layer 2 (`km create` only, AWS call):** single `DescribeSnapshots` covering all snapshot IDs. Asserts: `State == "completed"`, region match (via AWS's natural `InvalidSnapshot.NotFound`), `size >= snapshot.VolumeSize` when explicit. IAM-missing `ec2:DescribeSnapshots` is WARN-and-skip (graceful degradation; terragrunt apply becomes the slower fallback failure path). On pre-flight failure: zero terragrunt artifacts left on disk (abort before compile).
- `km validate` does NOT call AWS — keep it fast and offline.

### Mountpoint blocklist

Reserved (reject at Layer 1): `/`, `/shared` (EFS), `/workspace` (root-disk working dir), `/proc`, `/sys`, `/dev`, `/etc`, `/usr`, `/var`, `/root`, `/home`, `/boot`, `/tmp`, `/run`, `/opt` (top-level only; `/opt/foo` is fine).

Also reject: equality with `additionalVolume.MountPoint` (when both set); duplicates across `additionalSnapshots` entries.

### Device allocation

- Pool: `/dev/sd[f-p]` (11 devices: f,g,h,i,j,k,l,m,n,o,p).
- `pickAdditionalVolumeDevice` is extended to take `claimed map[string]bool`. `additionalVolume` calls with `claimed = nil` (back-compat). `additionalSnapshots` iterates entries, building up `claimed` as it goes.
- Allocation skips AMI block-device-mappings (existing parameter to `pickAdditionalVolumeDevice`) AND the explicit pins from other entries AND `additionalVolume`'s claimed device.
- Pool exhaustion (>11 entries + AMI BDM volumes) → compiler error naming the offending entry.

### Userdata mount loop

- Refactor the single `{{- if .AdditionalVolumeMountPoint }}` block into `{{- range .AdditionalVolumeMounts }}` over a unified list (legacy `additionalVolume` when set + each `additionalSnapshots` entry, in that order).
- Filesystem type detected via `blkid -s TYPE -o value "$DEVICE"`, not hard-coded `ext4`. fstab line uses `${FSTYPE}` (resolves to `ext4` for blank-formatted volumes — preserves legacy output).
- `mkfs.ext4 -F` is still gated on "no FS detected" — preserves the existing behaviour that accidentally already worked for snapshot-restored ext4 volumes; now also handles xfs/btrfs/etc. correctly.
- 60-second attach poll, NVMe alias probe (`nvmeAlias` helper maps `/dev/sdX` → `/dev/xvdX`), root-device exclusion, UUID-based fstab append, `chown sandbox:sandbox`, `[km-bootstrap]` log lines — all preserved exactly.

### Backward-compat invariants (must hold)

- Profiles WITHOUT `additionalSnapshots` produce **zero diff** in rendered HCL.
- Userdata for the `additionalVolume`-only case is **byte-identical** to pre-refactor save for `ext4` → `${FSTYPE}` in the fstab line — must be enforced by a golden-file test.
- Existing sandboxes pinned to `ec2spot/v1.0.0` are untouched (module immutability per Phase 80; new sandboxes use `v1.1.0`).
- `additionalVolume` syntax, semantics, terraform shape, userdata behaviour unchanged.

### Terraform module versioning

- Per Phase 80 conventions for tagged immutable modules: backward-compatible additive change requires a new minor version.
- `infra/modules/ec2spot/v1.1.0/` (new) — copy of `v1.0.0/` + the diff. Compiler bumps the emitted source path. `v1.0.0/` stays as-is so existing sandboxes don't drift.
- Variable type: `list(object({ snapshot_id, device_name, encrypted (optional bool), size_gb (optional number) }))`, default `[]`.
- Resources: `aws_ebs_volume.snapshot` (for_each by index) + `aws_volume_attachment.snapshot` (for_each by index, instance_id mirrors the existing `aws_volume_attachment.additional` spot-vs-on-demand ternary).
- Tags: `km:sandbox-id`, `km:resource-prefix`, `km:source-snapshot`, `Name = "km-sandbox-${var.sandbox_id}-snap-${each.key}"`.
- Lifecycle: same as `aws_ebs_volume.additional` — destroyed with the sandbox. No `prevent_destroy`. The source snapshot is reference-only; terraform never touches it.

### Substrate scope

- EC2 substrate (`ec2*`): full support.
- Docker substrate (`km create --docker`): NOT supported. `additionalSnapshots` on any non-`ec2*` substrate → validation error (same wording style as today's `additionalVolume` check at `pkg/compiler/service_hcl.go:681`).
- Future substrates inherit the same EC2-only restriction.

### `learnMode` interaction

- Learned profiles do NOT emit `additionalSnapshots`. The field is operator-authored only. (No changes to `pkg/learnmode/` or generator output.)

### Error wording

- All errors must name the offending entry index for array-typed fields.
- `InvalidSnapshot.NotFound` → 3-line hint: snapshot may be in another region, not yet shared with this account, or deleted.
- `size < snapshot.VolumeSize` → error states both numbers explicitly.

### Claude's Discretion (not specified by PRD — planner picks)

- Exact Go file split for Layer 2 validation (`aws_validate.go` is a fresh file; whether helpers extract or stay inline is the planner's call).
- Whether `aws_validate.go` lives under `pkg/profile/` (per the spec) or a sibling package — spec says `pkg/profile/aws_validate.go`; planner can confirm during research.
- Exact name of the nullable-`*bool` HCL template helper to reuse — `service_hcl.go` has one already for `additional_volume_encrypted`; planner picks.
- Whether to emit one example profile under `profiles/` for the operator UAT runbook — nice-to-have, not load-bearing.
- How verbose `[km-bootstrap]` userdata logging should be per snapshot — match the existing `additionalVolume` line count.
- Whether the `aws_validate.go` `DescribeSnapshots` call uses a single batched request or N parallel — single batch is the spec's choice; planner may revise if AWS pagination/limits surface during research.

</decisions>

<specifics>
## Specific Ideas

Concrete references from the PRD spec that planner must respect:

### Existing patterns to mirror

- `pkg/compiler/service_hcl.go:681` — current `additionalVolume` EC2-only validation. New `additionalSnapshots` check must use the same wording style.
- `infra/modules/ec2spot/v1.0.0/main.tf` — existing `aws_ebs_volume.additional` + `aws_volume_attachment.additional` shape, including the spot-vs-on-demand ternary for `instance_id`. Mirror exactly for the snapshot siblings.
- `infra/modules/ec2spot/v1.0.0/variables.tf` — existing `additional_volume_size_gb` / `_encrypted` / `_device_name` style. Stay backward-compatible.
- `pkg/compiler/userdata.go` — current `{{- if .AdditionalVolumeMountPoint }}` block is the structure to loop-ify.
- Phase 80 module-versioning conventions — copy `v1.0.0/` → `v1.1.0/` (don't mutate `v1.0.0/`); bump compiler source path; preserve old-sandbox pin.

### Example YAML (target shape)

```yaml
spec:
  runtime:
    substrate: ec2
    ami: amazon-linux-2023

    # Existing — unchanged
    additionalVolume:
      size: 30
      mountPoint: /data

    # NEW
    additionalSnapshots:
      - snapshotId: snap-0123abcdef0123456
        mountPoint: /opt/models
        device: /dev/sdh
        encrypted: true
        size: 200
      - snapshotId: snap-04567890abcdef012
        mountPoint: /opt/cache
        # device auto-picked, encrypted/size inherit from snapshot
```

### UAT runbook (planner: lift into Wave 5 verbatim)

8 scenarios from spec § Testing > Integration / UAT — UAT-1 through UAT-8. See BRIEF.md `SNAP-08` for the table.

### Failure mode → error wording mapping

Spec § Failure modes table is the source of truth. Mirror error wording / abort points faithfully. The BRIEF.md "Failure modes" section reproduces it.

</specifics>

<deferred>
## Deferred Ideas

Explicitly out of scope for Phase 87 (PRD spec § Non-goals + § Future work):

- `preserveOnDestroy: true` — `lifecycle.prevent_destroy = true` for hand-off between sandboxes.
- `snapshotOnDestroy: true` — auto-snapshot before `km destroy` for resume-from-state workflows.
- Unified `additionalVolumes: [...]` collapsing the singular/plural fields.
- `kmsKeyId` explicit KMS key field.
- `km snapshot copy <id> --to <region>` cross-region helper.
- `learnMode`-emitted `additionalSnapshots` entries.
- Non-EC2 substrate support.
- Cross-account snapshot validation warnings beyond verbatim AWS error + hint.

</deferred>

---

*Phase: 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile*
*Context gathered: 2026-05-21 via PRD Express Path (`docs/superpowers/specs/2026-05-21-additional-snapshots-design.md`)*
