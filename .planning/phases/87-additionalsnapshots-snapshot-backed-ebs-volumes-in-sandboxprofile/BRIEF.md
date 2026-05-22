# Phase 87: additionalSnapshots — snapshot-backed EBS volumes in SandboxProfile

**Status:** Not planned yet.
**Source spec:** `docs/superpowers/specs/2026-05-21-additional-snapshots-design.md`.
**Date drafted:** 2026-05-21.

## In scope (SNAP-01..SNAP-08)

### SNAP-01: Schema — `spec.runtime.additionalSnapshots: [...]`

- New `AdditionalSnapshotSpec` Go struct in `pkg/profile/types.go`:
  - `SnapshotID string` (yaml `snapshotId`) — required.
  - `MountPoint string` (yaml `mountPoint`) — required.
  - `Device string` (yaml `device,omitempty`) — optional pin to `/dev/sd[f-p]`.
  - `Encrypted *bool` (yaml `encrypted,omitempty`) — pointer so omitted ≠ false; nil → terraform `null` → AWS inherits snapshot's encryption.
  - `Size int` (yaml `size,omitempty`) — optional GB override; 0/omitted → match snapshot size.
- New `AdditionalSnapshots []AdditionalSnapshotSpec` field on `RuntimeSpec` (parallel to existing `AdditionalVolume *AdditionalVolumeSpec`).
- JSON schema entry in `pkg/profile/schemas/sandbox_profile.schema.json`:
  - `snapshotId` pattern `^snap-[0-9a-f]{8,17}$` (AWS supports 8- and 17-char hex IDs).
  - `mountPoint` pattern `^/`.
  - `device` pattern `^/dev/sd[f-p]$`.
  - `encrypted` boolean.
  - `size` integer ≥ 1.
  - `additionalProperties: false`.

### SNAP-02: Layer 1 validation — `km validate` (schema rules, no AWS calls)

In `pkg/profile/validate.go` (and friends):

- **EC2-only:** reject `additionalSnapshots` for any non-`ec2*` substrate with the same error wording style as today's `additionalVolume` check in `pkg/compiler/service_hcl.go:681`.
- **`snapshotId` format:** must match `^snap-[0-9a-f]{8,17}$`.
- **`mountPoint` safety:**
  - Must start with `/`.
  - Reject equality with: `/`, `/shared`, `/workspace`, `/proc`, `/sys`, `/dev`, `/etc`, `/usr`, `/var`, `/root`, `/home`, `/boot`, `/tmp`, `/run`, `/opt` (top-level — `/opt/foo` is fine).
  - Reject collision with `additionalVolume.MountPoint` (when both set).
  - Reject duplicates across `additionalSnapshots` entries.
- **`device` (explicit only):** `^/dev/sd[f-p]$`; unique across entries; must not equal the device the compiler would auto-pick for `additionalVolume`.
- **`size` (when set):** `>= 1`. The `>= snapshot.VolumeSize` check is Layer 2 (needs an API call).

Errors must name the offending entry index for arrays.

### SNAP-03: Layer 2 validation — `km create` AWS pre-flight (new file)

In `pkg/profile/aws_validate.go` (new):

- Runs only on `km create`, BEFORE the compiler emits any HCL — must abort with zero on-disk terragrunt artifacts.
- Single `DescribeSnapshots` call carrying every snapshot ID in the profile.
- All snapshots must be in the sandbox's target region (AWS's natural `InvalidSnapshot.NotFound` enforces this).
- Every returned snapshot must have `State == "completed"`. `pending`/`error` → reject.
- For each entry with explicit `size`, require `size >= snapshot.VolumeSize`.
- `InvalidSnapshot.NotFound` → error names the missing snapshot ID + 3-line hint about region, sharing, or deletion.
- If caller lacks `ec2:DescribeSnapshots`, log a WARN and skip the pre-flight; terragrunt apply becomes the fallback failure path (same degradation as `km doctor` AWS checks).

### SNAP-04: Compiler — device allocation + HCL render

In `pkg/compiler/service_hcl.go`:

1. **Extend `pickAdditionalVolumeDevice`** to take a `claimed map[string]bool` (devices already taken). Back-compat: `additionalVolume` calls with `claimed = nil`. `additionalSnapshots` iterates, adding each picked device to `claimed` before the next call.
2. **New render-struct field** on the EC2 service.hcl template: `AdditionalSnapshots []AdditionalSnapshotEntry` (ordered, after device-allocation), where each entry carries `SnapshotID`, `DeviceName`, `MountPoint`, `Encrypted bool`, `SizeGB int` (0 = inherit).
3. **Template addition** in `ec2ServiceHCLTemplate` to emit:
   ```hcl
   additional_snapshots = [
   {{- range .AdditionalSnapshots }}
     { snapshot_id = "...", device_name = "...", encrypted = ..., size_gb = ... },
   {{- end }}
   ]
   ```
4. **Userdata render-struct gains a parallel list** (`AdditionalSnapshotMounts` — devices + mountpoints only) so userdata can iterate over both `additionalVolume` and snapshot entries uniformly.

`*bool` Encrypted marshalling must emit literal `null` (not `false`) when nil — using whatever HCL template helper `service_hcl.go` uses elsewhere for nullable scalars.

### SNAP-05: Userdata — FS-aware mount loop

In `pkg/compiler/userdata.go`:

Replace the current `{{- if .AdditionalVolumeMountPoint }}` single-mount block with `{{- range .AdditionalVolumeMounts }}` over a unified list containing the legacy `additionalVolume` (when set) and each `additionalSnapshots` entry. Two behavioural changes:

1. **Filesystem type detected via `blkid`**, not hard-coded `ext4`. fstab line uses `${FSTYPE}` (resolves to `ext4` for blank volumes — preserves legacy byte output).
2. **One mount block per entry** (`additionalVolume` + each snapshot), in declaration order.

Keep the existing 60-second attach poll, NVMe alias probe (`nvmeAlias` helper maps `/dev/sdX` → `/dev/xvdX`), root-device exclusion, `mkfs.ext4 -F` only when no FS detected, UUID-based fstab append, `chown sandbox:sandbox`, `[km-bootstrap]` log lines.

### SNAP-06: Terraform module — `ec2spot/v1.1.0/` (new minor version, additive)

Per Phase 80 conventions for tagged immutable modules — backward-compatible additive change requires a new minor version.

- Copy `infra/modules/ec2spot/v1.0.0/` → `infra/modules/ec2spot/v1.1.0/`.
- `variables.tf`: add
  ```hcl
  variable "additional_snapshots" {
    type = list(object({
      snapshot_id = string
      device_name = string
      encrypted   = optional(bool)    # null = inherit from snapshot
      size_gb     = optional(number)  # null/0 = inherit
    }))
    default = []
  }
  ```
  Leave `additional_volume_size_gb` / `_encrypted` / `_device_name` untouched.
- `main.tf`: add sibling resources next to existing `aws_ebs_volume.additional` / `aws_volume_attachment.additional`:
  - `aws_ebs_volume.snapshot` — `for_each = { for i, s in var.additional_snapshots : i => s }`, `snapshot_id`, conditional `size` (only when `size_gb > 0`), `encrypted` from var (nullable), `type = "gp3"`, tags `km:sandbox-id` + `km:resource-prefix` + `km:source-snapshot` + `Name = "km-sandbox-${var.sandbox_id}-snap-${each.key}"`.
  - `aws_volume_attachment.snapshot` — `for_each` matching, `device_name`, `volume_id`, instance_id selected from spot or on-demand path (mirror existing `additional` logic), `force_detach = true`.
- Lifecycle: same as the existing `additional` resources — destroyed with the sandbox. No `prevent_destroy`. The source snapshot is referenced by ID only.
- `pkg/compiler/compiler.go`: bump emitted EC2 module source path to `ec2spot/v1.1.0`. Old sandboxes pinned to `v1.0.0` keep working unchanged.

### SNAP-07: Backward compatibility — zero-diff for legacy profiles

- `additionalVolume` unchanged in syntax, semantics, terraform shape, userdata behaviour.
- Profiles without `additionalSnapshots` produce zero diff in rendered HCL.
- Userdata for the `additionalVolume`-only case must remain **byte-identical** to pre-refactor output **save for `ext4` → `${FSTYPE}` in the fstab line** (which still resolves to `ext4` at runtime for blank volumes).
- Existing sandboxes pinned to `ec2spot/v1.0.0` are untouched (module is immutable; new sandboxes get `v1.1.0`).
- Non-EC2 substrates (`docker`, future ECS) reject `additionalSnapshots` at validation time — same rule as today's `additionalVolume`.

### SNAP-08: Testing — Go unit + operator-driven UAT

**Go unit tests:**

- `pkg/profile/types_test.go` — parse YAML with 0, 1, and 3 snapshot entries; assert struct shape.
- `pkg/profile/validate_test.go` — table-driven Layer 1: bad regex, mountpoint collision, reserved mountpoints, non-EC2 substrate rejection, explicit-device duplicates, size < 1.
- `pkg/profile/aws_validate_test.go` (new) — mock `DescribeSnapshots`: happy path, `InvalidSnapshot.NotFound`, `pending` state, size-override-too-small, IAM-missing graceful WARN-and-skip.
- `pkg/compiler/service_hcl_test.go` — rendered HCL has one `additional_snapshots` entry per spec entry, device allocation honours explicit pins + auto-fills the rest from `/dev/sd[f-p]` skipping AMI BDM and `additionalVolume`'s device.
- `pkg/compiler/userdata_test.go` — golden file: legacy (`additionalVolume`-only) output is byte-identical modulo `ext4` → `${FSTYPE}`; adding snapshot entries emits one mount block per entry in declaration order.
- `pkg/compiler/ec2_storage_test.go` — extend to assert auto-picked devices avoid BOTH AMI BDM and prior `additionalSnapshots` entries.

**UAT (operator-driven, real AWS):**

| # | Scenario | Expected |
|---|---|---|
| UAT-1 | Single snapshot, auto-device. `aws ec2 create-snapshot` from a known-good volume; profile with one entry, no `device`; `km create`, SSM in, `mount \| grep <mountPoint>`, verify file contents match source; `km destroy`; `aws ec2 describe-volumes` shows materialised volume gone, source snapshot intact. | Mount present + contents match; destroy cleans only the new volume. |
| UAT-2 | Two snapshots + `additionalVolume`. | All three mount; auto-devices land on `/dev/sdf` / `/dev/sdg` / `/dev/sdh` without collision. |
| UAT-3 | Explicit `device: /dev/sdh` on one entry. | Rendered HCL respects the pin. |
| UAT-4 | AMI with BDM declaring `/dev/sdf`. | Auto-pick lands on `/dev/sdg` for both `additionalVolume` and first snapshot. |
| UAT-5 | Profile references `snap-deadbeefdeadbeef0`. | `km create` fails at pre-flight with documented error wording; no terragrunt artifact on disk. |
| UAT-6 | Snapshot in `us-east-1`, profile targets `us-west-2`. | Same failure mode as UAT-5. |
| UAT-7 | Snapshot is 50 GB, profile sets `size: 100`. | Materialised volume is 100 GB. |
| UAT-8 | Snapshot is 50 GB, profile sets `size: 20`. | Pre-flight rejects before terragrunt runs; error states both sizes. |

## Out of scope (deferred per spec § Non-goals)

- `preserveOnDestroy` / `snapshotOnDestroy` per-entry knobs.
- `kmsKeyId` selection (default = snapshot's key or account-default EBS KMS key).
- Cross-account snapshot validation warnings (rely on `DescribeSnapshots` error surfaced verbatim + hint).
- Unifying `additionalVolume` (singular) and `additionalSnapshots` (list) into one schema.
- Non-EC2 substrate support (ECS/Docker reject at validation).
- `learnMode` integration (the field is operator-authored only).
- Size auto-shrink (EBS can't shrink below snapshot size; validation hard-fails).
- `km snapshot copy --to <region>` cross-region helper.

## Safety guards

- Profiles WITHOUT `additionalSnapshots` produce ZERO diff in rendered HCL and ZERO diff in userdata (modulo the `ext4` → `${FSTYPE}` substitution, which still resolves to `ext4`).
- Existing sandboxes pinned to `ec2spot/v1.0.0` are untouched. Module immutability per Phase 80.
- Layer 2 pre-flight failure must NOT leave any terragrunt working directory on disk — abort before compile.
- Source snapshot is read-only — `aws_ebs_volume` references `snapshot_id` only; terraform never touches the snapshot resource.
- Volume lifecycle = sandbox lifecycle — `km destroy` cleans up `aws_ebs_volume.snapshot` instances; source snapshot survives.
- IAM-missing-`ec2:DescribeSnapshots` is WARN-not-FATAL (graceful degradation; terragrunt apply is the fallback failure path).

## Failure modes (from spec § Failure modes)

| Condition | Caught where | Behaviour |
|---|---|---|
| `snapshotId` malformed | `km validate` (regex) | Profile rejected, error names bad entry index. |
| `mountPoint` collision (EFS / reserved / other entry) | `km validate` | Profile rejected, error names colliding entries. |
| `device` collision (explicit) | `km validate` | Validation error. |
| `device` auto-pick exhaustion (>11 entries + AMI volumes) | Compiler | Compiler error names offending entry. |
| Snapshot missing / wrong region / not shared | `km create` pre-flight | Create aborts; error names snap ID + region + 3-line hint. |
| Snapshot in `pending` / `error` state | `km create` pre-flight | Create aborts; error names snap ID + state. |
| `size < snapshot.VolumeSize` | `km create` pre-flight | Create aborts; error states both sizes. |
| Caller lacks `ec2:DescribeSnapshots` | `km create` pre-flight | WARN logged, pre-flight skipped, terragrunt apply is fallback failure path. |
| EBS attach > 60s at boot | userdata | `[km-bootstrap] WARNING` to `/var/log/km-bootstrap.log`; sandbox boots without mount. |
| Snapshot KMS decryption fails | EC2 boot | Volume attach fails; userdata WARNs; sandbox boots without mount. Operator runbook: grant `kms:CreateGrant` to sandbox's EC2 service role. |

## Plan-breakdown hint for /gsd:plan-phase

Suggested wave structure (planner can revise):

- **Wave 0 (schema scaffolding):** SNAP-01 — Go types + JSON schema. RED-state validation/aws_validate stubs (SNAP-02 / SNAP-03 test files exist but tests fail until later waves).
- **Wave 1 (validation — schema layer):** SNAP-02 — `validate.go` rules (EC2-only, regex, mountpoint sanity, device-uniqueness, size positive). Unit tests GREEN.
- **Wave 2 (validation — AWS pre-flight):** SNAP-03 — `aws_validate.go` with mocked `DescribeSnapshots`; integration into `km create` flow before compiler. Unit tests GREEN.
- **Wave 3 (compiler + userdata):** SNAP-04 + SNAP-05 — extend `pickAdditionalVolumeDevice`, render struct fields, ec2 service.hcl template, userdata loop refactor with FS detection. Golden-file regression for `additionalVolume`-only output. Unit tests GREEN.
- **Wave 4 (terraform module v1.1.0):** SNAP-06 — copy `ec2spot/v1.0.0/` → `v1.1.0/`, add `additional_snapshots` variable + resources, bump compiler source path. `terragrunt validate` clean on a dry-run profile.
- **Wave 5 (UAT + docs):** SNAP-07 backward-compat assertions baked into Wave 3/4 tests; SNAP-08 8 UAT scenarios + 1 zero-diff regression check; `CLAUDE.md` bullet + `OPERATOR-GUIDE.md` section.

Wave 1 / Wave 2 can run in parallel after Wave 0 (different files, no shared state). Wave 4 can run in parallel with Wave 3's userdata work but must follow Wave 3's compiler render-struct changes (compiler emits the new HCL the module consumes).

## Risk / unknowns surfaced for planning

- **`*bool` → terraform `null` marshalling.** `service_hcl.go` already has nullable-scalar helpers elsewhere (e.g. for `additional_volume_encrypted`) — planner: confirm exact helper name and reuse, don't reinvent.
- **`pickAdditionalVolumeDevice` signature change.** Existing call sites in `service_hcl.go` need updating to pass `claimed = nil`. Should be a 1-line diff per call site; planner: enumerate all call sites and confirm no external callers (`pkg/compiler/ec2_storage_test.go` already exercises it).
- **EC2 spot vs on-demand instance_id source.** `aws_volume_attachment.snapshot` mirrors `aws_volume_attachment.additional`'s ternary on `length(local.ec2spot_map) > 0`. Planner: confirm the exact terraform reference in the live `v1.0.0/main.tf` to copy verbatim.
- **AMI BDM enumeration.** `pickAdditionalVolumeDevice`'s `amiDevices` parameter source must already be wired (Phase 67 work — `learn --ami`). Planner: verify the existing helper that returns AMI BDM devices is callable from the new `additionalSnapshots` loop, and that it handles both slug AMIs (`amazon-linux-2023`) and raw IDs.
- **Pre-flight scope.** `km validate` does NOT do AWS pre-flight — only `km create` does. Planner: confirm the `aws_validate.go` is wired into `internal/app/cmd/create.go` only, not `internal/app/cmd/validate.go`.
- **Source snapshot region.** Pre-flight must use the sandbox's resolved target region (after `--region` flag + profile + config precedence), not the operator's default profile region. Planner: trace where the region is locked in by `km create` before AWS pre-flight runs.

## Files expected to change

- `pkg/profile/types.go` — `AdditionalSnapshotSpec` struct + `RuntimeSpec.AdditionalSnapshots` field.
- `pkg/profile/types_test.go` — YAML parse tests.
- `pkg/profile/schemas/sandbox_profile.schema.json` — new `additionalSnapshots` array schema.
- `pkg/profile/validate.go` — Layer 1 rules.
- `pkg/profile/validate_test.go` — Layer 1 table-driven tests.
- `pkg/profile/aws_validate.go` (new) — Layer 2 `DescribeSnapshots` pre-flight.
- `pkg/profile/aws_validate_test.go` (new) — mocked DescribeSnapshots tests.
- `pkg/compiler/service_hcl.go` — `pickAdditionalVolumeDevice` extension; `AdditionalSnapshots` render-struct + HCL emission; EC2-only substrate rejection wiring (parity with `additionalVolume`).
- `pkg/compiler/service_hcl_test.go` — HCL render assertions.
- `pkg/compiler/userdata.go` — loop refactor + FS detection.
- `pkg/compiler/userdata_test.go` — golden-file regression + new mount-block assertions.
- `pkg/compiler/ec2_storage_test.go` — extend auto-pick coverage.
- `pkg/compiler/compiler.go` — bump emitted EC2 module path to `ec2spot/v1.1.0`.
- `infra/modules/ec2spot/v1.1.0/` (new dir, copy of v1.0.0 + diff) — `variables.tf` + `main.tf` additions.
- `internal/app/cmd/create.go` — wire `aws_validate.go` pre-flight into the `km create` flow, before compiler.
- `internal/app/cmd/create_test.go` — pre-flight integration test (mocked).
- `OPERATOR-GUIDE.md` — section under EC2 substrate documenting `additionalSnapshots`.
- `CLAUDE.md` — bullet under "## Architecture" or a new section pointer for snapshot-backed volumes.
- `profiles/` — at least one example profile demonstrating `additionalSnapshots` (optional, for the operator UAT runbook).
