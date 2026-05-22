# Phase 87: additionalSnapshots — snapshot-backed EBS volumes in SandboxProfile — Research

**Researched:** 2026-05-21
**Domain:** Profile schema, Go compiler, userdata bash templating, Terraform EC2 module, AWS EC2 SDK (DescribeSnapshots)
**Confidence:** HIGH — every claim below is pinned to an actual file/line in the repo.

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Schema shape: `spec.runtime.additionalSnapshots` list, parallel to `additionalVolume`. Fields: `snapshotId` (required), `mountPoint` (required), `device` (optional), `encrypted *bool` (optional pointer), `size int` (optional, 0=inherit).
- `Encrypted` is a Go pointer (`*bool`) so omitted ≠ false; nil → terraform `null`.
- `snapshotId` regex `^snap-[0-9a-f]{8,17}$`. `device` regex `^/dev/sd[f-p]$`.
- Layer 1 validation (`km validate`, no AWS calls): EC2-only, regex, mountpoint blocklist, device uniqueness, size ≥ 1.
- Layer 2 validation (`km create` only): single `DescribeSnapshots` pre-flight; abort before compile on failure; IAM-missing is WARN-not-FATAL.
- Mountpoint blocklist: `/`, `/shared`, `/workspace`, `/proc`, `/sys`, `/dev`, `/etc`, `/usr`, `/var`, `/root`, `/home`, `/boot`, `/tmp`, `/run`, `/opt` (top-level).
- Collision checks: across snapshot entries and with `additionalVolume.MountPoint`.
- Device pool `/dev/sd[f-p]` (11 slots). `pickAdditionalVolumeDevice` gains `claimed map[string]bool` param.
- Userdata refactored to `range .AdditionalVolumeMounts` loop with `blkid` FS detection; `ext4` → `${FSTYPE}` in fstab.
- Byte-identical backward-compat for `additionalVolume`-only userdata (modulo the `ext4` → `${FSTYPE}` substitution).
- Terraform module: `infra/modules/ec2spot/v1.1.0/` (copy of v1.0.0 + additions). `v1.0.0` untouched.
- New TF resources: `aws_ebs_volume.snapshot` (for_each) + `aws_volume_attachment.snapshot` (for_each). Tags include `km:source-snapshot`.
- Compiler bump: `ec2spot/v1.1.0` emitted for new sandboxes.
- EC2-only; non-EC2 substrates rejected at Layer 1 (same error-wording style as line 681 in service_hcl.go).
- No `learnMode` integration, no `preserveOnDestroy`, no `kmsKeyId`.
- Error messages must name offending entry index.
- `InvalidSnapshot.NotFound` → 3-line hint (region / sharing / deleted).

### Claude's Discretion
- Exact Go file split for Layer 2 (`aws_validate.go` is a fresh file under `pkg/profile/`; helpers inline or extracted is planner's call).
- Exact name of nullable-`*bool` HCL template helper to reuse.
- Whether to emit an example profile under `profiles/`.
- Verbosity of `[km-bootstrap]` log lines per snapshot entry.
- Single batched `DescribeSnapshots` request vs N parallel (spec says single batch; planner may revise on research findings).

### Deferred Ideas (OUT OF SCOPE)
- `preserveOnDestroy` / `snapshotOnDestroy` per-entry knobs.
- `kmsKeyId` explicit KMS key.
- Cross-account snapshot validation warnings beyond verbatim AWS error + hint.
- Unified `additionalVolumes` collapsing singular + list.
- `km snapshot copy --to <region>`.
- `learnMode` emitting `additionalSnapshots`.
- Non-EC2 substrate support.
- Size auto-shrink.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| SNAP-01 | Schema: `AdditionalSnapshotSpec` Go struct + JSON schema entry + `RuntimeSpec.AdditionalSnapshots` field | `pkg/profile/types.go` lines 130–164 show exact `AdditionalVolumeSpec` + `RuntimeSpec` shape to extend |
| SNAP-02 | Layer 1 validation (`km validate`, no AWS calls) | `pkg/profile/validate.go` `ValidateSemantic` pattern; error type at lines 14–33 |
| SNAP-03 | Layer 2 validation (`km create` AWS pre-flight via `DescribeSnapshots`) | `EC2VolumeAPI` in `doctor_ebs.go` has `DescribeSnapshots` already; `create.go` lines 619–631 show BDM pre-flight wiring pattern |
| SNAP-04 | Compiler — `pickAdditionalVolumeDevice` extension + HCL render | `service_hcl.go` lines 42–65 (function), 126–128 (template), 466–469 (struct fields) |
| SNAP-05 | Userdata — loop refactor with blkid FS detection | `userdata.go` lines 96–138 (current single-entry block); no `nvmeAlias` Go template helper exists |
| SNAP-06 | Terraform module `ec2spot/v1.1.0` | `v1.0.0/variables.tf` lines 113–129 and `main.tf` lines 685–708 are the exact patterns to copy |
| SNAP-07 | Backward compatibility — zero-diff for legacy profiles | Template at `infra/templates/sandbox/terragrunt.hcl` line 43 is the version bump point |
| SNAP-08 | Tests: Go unit + 8 operator UAT | `ec2_storage_test.go` (370 lines) is the model; `validate_test.go` table-driven pattern |
</phase_requirements>

---

## Existing Code Map

Every claim in CONTEXT.md / BRIEF.md pinned to actual file:line.

### Q1 — `service_hcl.go:681` EC2-only additionalVolume check

**CONFIRMED.** File: `pkg/compiler/service_hcl.go`, lines 671–690.

```go
// line 673
func validateEC2StorageFields(p *profile.SandboxProfile, useSpot bool) error {
    substrate := p.Spec.Runtime.Substrate
    if p.Spec.Runtime.Hibernation && useSpot {
        return fmt.Errorf("hibernation requires on-demand instance...")
    }
    if p.Spec.Runtime.Hibernation && !strings.HasPrefix(substrate, "ec2") {
        return fmt.Errorf("hibernation is not supported for %s substrate", substrate)
    }
    // line 681:
    if p.Spec.Runtime.AdditionalVolume != nil && !strings.HasPrefix(substrate, "ec2") {
        return fmt.Errorf("additionalVolume is not supported for %s substrate", substrate)
    }
    ...
}
```

The new `additionalSnapshots` check must be added to this same function, immediately after line 682, using an identical `!strings.HasPrefix(substrate, "ec2")` guard and analogous wording: `"additionalSnapshots is not supported for %s substrate"`.

### Q2 — `pickAdditionalVolumeDevice` definition, call sites, `amiDevices` source

**CONFIRMED.** Definition at `pkg/compiler/service_hcl.go` lines 42–65:

```go
func pickAdditionalVolumeDevice(amiDevices []string) string {
    occupied := make(map[string]bool, len(amiDevices)*2)
    for _, d := range amiDevices {
        occupied[d] = true
        if strings.HasPrefix(d, "/dev/xvd") {
            occupied["/dev/sd"+d[len("/dev/xvd"):]] = true
        }
        if strings.HasPrefix(d, "/dev/sd") {
            occupied["/dev/xvd"+d[len("/dev/sd"):]] = true
        }
    }
    for _, c := range additionalVolumeDeviceCandidates {
        if !occupied[c] {
            return c
        }
    }
    return "/dev/sdf" // fallback
}
```

**CURRENT SIGNATURE:** `func pickAdditionalVolumeDevice(amiDevices []string) string`

**Call sites (non-test):**
- `pkg/compiler/service_hcl.go:767` — `pickAdditionalVolumeDevice(amiBDMDeviceNames)` (sole production call)

**Test call sites:**
- `pkg/compiler/ec2_storage_test.go:378` — `pickAdditionalVolumeDevice(tc.amiDevices)` (direct internal call)

The signature change to `func pickAdditionalVolumeDevice(amiDevices []string, claimed map[string]bool) string` requires updating both the definition and two call sites plus extending the `occupied` map to absorb `claimed` before iterating candidates.

**`amiDevices` source:** `pkg/aws/ec2_ami.go:161` — `func AMIBDMDeviceNames(ctx, client EC2AMIAPI, amiID string) ([]string, error)` calls `DescribeImages` and extracts `BlockDeviceMappings`. Wired in `internal/app/cmd/create.go:622–631`:

```go
// create.go:622–631 — existing BDM lookup (Phase 56.1)
var amiBDMDevices []string
if compiler.IsRawAMIID(resolvedProfile.Spec.Runtime.AMI) && resolvedProfile.Spec.Runtime.AdditionalVolume != nil {
    ec2Client := ec2svc.NewFromConfig(awsCfg)
    devices, lookupErr := awspkg.AMIBDMDeviceNames(ctx, ec2Client, resolvedProfile.Spec.Runtime.AMI)
    if lookupErr != nil {
        log.Warn().Err(lookupErr).Str("ami", resolvedProfile.Spec.Runtime.AMI).Msg("BDM lookup failed; defaulting to /dev/sdf")
    } else {
        amiBDMDevices = devices
    }
}
```

**CRITICAL DISCREPANCY — SNAP-04 IMPACT:** The BDM lookup is currently gated on `resolvedProfile.Spec.Runtime.AdditionalVolume != nil`. When `additionalSnapshots` is set but `additionalVolume` is nil, the BDM lookup is skipped today, meaning the compiler receives `nil` for `amiBDMDevices` and defaults to `/dev/sdf`. Phase 87 must extend this gate to also trigger when `len(resolvedProfile.Spec.Runtime.AdditionalSnapshots) > 0`. This fix is required for UAT-4 (AMI BDM collision) to work correctly with snapshots-only profiles.

Phase 67 (learn --ami) wired AMI BDM enumeration; `AMIBDMDeviceNames` handles both slug AMIs (does nothing — BDM lookup only fires for raw AMI IDs) and raw IDs. The function signature does not handle slug AMIs at all — it only calls `DescribeImages` when `amiID` is a non-empty raw ID.

### Q3 — `ec2ServiceHCLTemplate` — existing `additional_volume_*` emission and nullable-bool helper

**CONFIRMED.** Template at `pkg/compiler/service_hcl.go` lines 75–167. The relevant section (lines 125–128):

```
    # Additional EBS volume (Phase 33 / 56.1)
    additional_volume_size_gb    = {{ .AdditionalVolumeSizeGB }}
    additional_volume_encrypted  = {{ .AdditionalVolumeEncrypted }}
    additional_volume_device_name = "{{ .AdditionalVolumeDeviceName }}"
```

**CRITICAL FINDING — nullable-bool helper DOES NOT EXIST.** The current `AdditionalVolumeEncrypted` field is `bool` (not `*bool`) in the `ec2HCLParams` struct (line 467) and emits `true`/`false` directly via Go's default `{{ .Field }}` formatting. There is NO existing nullable-`*bool` → HCL-`null` template helper in `service_hcl.go`.

The PRD/BRIEF states "reuse whatever HCL template helper `service_hcl.go` uses elsewhere for nullable scalars" — but no such helper exists today. The planner must CREATE a new template func (e.g., `boolPtrHCL`) that takes `*bool` and returns either `null` (nil) or `true`/`false` as a string. This is a fresh implementation, not a reuse.

The `templateFuncs` map is at `pkg/compiler/service_hcl.go:573`:

```go
var templateFuncs = template.FuncMap{
    "sgRuleHCL": func(r SGRule) string { ... },
    "joinStrings": func(ss []string) string { ... },
    // ADD HERE: "boolPtrHCL": func(b *bool) string { if b == nil { return "null" } ... }
}
```

### Q4 — EC2 service.hcl render struct shape

`ec2HCLParams` struct at `pkg/compiler/service_hcl.go` lines 430–477. Existing `additionalVolume` fields (lines 465–469):

```go
// Additional EBS volume fields (Phase 33 / 56.1)
AdditionalVolumeSizeGB     int    // 0 means no additional volume
AdditionalVolumeEncrypted  bool   // encrypt the additional EBS volume
AdditionalVolumeMountPoint string // mount point (e.g. "/data")
AdditionalVolumeDeviceName string // device name (e.g. "/dev/sdf")
```

New fields for Phase 87 slot in after line 469:
```go
// Additional snapshot-backed volumes (Phase 87)
AdditionalSnapshots []AdditionalSnapshotEntry  // ordered, after device-allocation
```

### Q5 — `userdata.go` — the `{{- if .AdditionalVolumeMountPoint }}` block

**CONFIRMED.** File: `pkg/compiler/userdata.go` lines 96–138 (full block):

```bash
{{- if .AdditionalVolumeMountPoint }}

# ============================================================
# 2.6. Additional EBS volume: format and mount (Phase 33)
# ============================================================
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

**CRITICAL FINDING — `nvmeAlias` template helper DOES NOT EXIST.** The BRIEF and PRD spec describe a `nvmeAlias` Go template function that maps `/dev/sdX` to `/dev/xvdX`. This helper is NOT in the current template function map. The current approach is a hard-coded bash `for dev in /dev/xvdf /dev/sdf /dev/nvme1n1 /dev/nvme2n1` loop — not a parametric device probe.

The PRD's proposed refactored userdata (quoted in the spec) uses `{{ nvmeAlias .Device }}` as a Go template call. This means the planner must either:
1. Register a `nvmeAlias` template func in the userdata template parser, OR
2. Use a different approach consistent with the existing shell pattern (probing both `/dev/xvdX` and `/dev/sdX` by constructing the alias in bash, not in Go template).

The existing code shows the current approach is bash-side aliasing via the for-loop over known aliases. The PRD spec's `nvmeAlias` template call is aspirational, not pre-existing. Planner must resolve: register a new Go template func, or adapt the refactored loop to match the existing shell pattern.

**60-second attach poll:** Confirmed — `seq 1 30` with `sleep 2` per iteration = 60 seconds max.

**Render-struct field name:** `AdditionalVolumeMountPoint` (string) at `pkg/compiler/userdata.go:3263`. This field and the `{{- if .AdditionalVolumeMountPoint }}` gating pattern are what get replaced with the `range .AdditionalVolumeMounts` approach.

**Userdata render struct location:** `pkg/compiler/userdata.go` around line 3240–3311 (the full struct). Relevant wiring at lines 3688–3690:
```go
if p.Spec.Runtime.AdditionalVolume != nil {
    params.AdditionalVolumeMountPoint = p.Spec.Runtime.AdditionalVolume.MountPoint
}
```

### Q6 — `compiler.go` — EC2 module source path

**CONFIRMED.** The version string `v1.0.0` lives in the **sandbox template** at `infra/templates/sandbox/terragrunt.hcl` line 43:
```
source = "${local.repo_root}/infra/modules/${local.svc_config.locals.substrate_module}/v1.0.0"
```

This file is COPIED by `pkg/terragrunt/sandbox.go:CreateSandboxDir` (lines 10–29) into each new sandbox directory. The BRIEF says "bump compiler source path in `compiler.go`" but the actual version string is in the **sandbox template file**, not compiler.go. `compiler.go` does not contain the version string at all. The bump is to `infra/templates/sandbox/terragrunt.hcl` line 43: change `v1.0.0` to `v1.1.0`. This also ensures existing sandbox directories already written with `v1.0.0` are not affected.

### Q7 — `pkg/profile/types.go` — `AdditionalVolumeSpec` and `RuntimeSpec`

**CONFIRMED.** `AdditionalVolumeSpec` at lines 130–138:
```go
type AdditionalVolumeSpec struct {
    Size       int    `yaml:"size" json:"size"`
    MountPoint string `yaml:"mountPoint" json:"mountPoint"`
    Encrypted  bool   `yaml:"encrypted,omitempty" json:"encrypted,omitempty"`
}
```

`RuntimeSpec` at lines 140–164. The `AdditionalVolume` field is at line 152–153:
```go
AdditionalVolume *AdditionalVolumeSpec `yaml:"additionalVolume,omitempty" json:"additionalVolume,omitempty"`
```

New `AdditionalSnapshots` field slots in immediately after line 153:
```go
AdditionalSnapshots []AdditionalSnapshotSpec `yaml:"additionalSnapshots,omitempty" json:"additionalSnapshots,omitempty"`
```

Note: `AdditionalVolumeSpec.Encrypted` is a plain `bool`, not `*bool`. The new `AdditionalSnapshotSpec.Encrypted` must be `*bool` — this is a different type than what `AdditionalVolumeSpec` uses.

### Q8 — `sandbox_profile.schema.json` — additionalVolume entry

**CONFIRMED.** Located at `pkg/profile/schemas/sandbox_profile.schema.json` lines 198–219:
```json
"additionalVolume": {
  "type": "object",
  "description": "Optional extra EBS volume...",
  "additionalProperties": false,
  "required": ["size", "mountPoint"],
  "properties": {
    "size": { "type": "integer", "minimum": 1 },
    "mountPoint": { "type": "string", "minLength": 1 },
    "encrypted": { "type": "boolean" }
  }
}
```

The schema is embedded in the `spec.runtime` object (within the nested JSON). New `additionalSnapshots` array entry slots in immediately after the `additionalVolume` object (after line 219).

### Q9 — `pkg/profile/validate.go` — Layer 1 validation pattern

**CONFIRMED.** Entry points:
- `Validate(raw []byte) []ValidationError` at line 41 — runs schema + semantic.
- `ValidateSemantic(p *SandboxProfile) []ValidationError` at line 219 — for Layer 1 semantic rules.

`ValidationError` type at lines 14–26:
```go
type ValidationError struct {
    Path      string
    Message   string
    IsWarning bool
}
func (e ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Path, e.Message) }
```

For array-typed fields, path should be `"spec.runtime.additionalSnapshots[2].mountPoint"` style. The existing code has no array-index examples; the planner must adopt `fmt.Sprintf("spec.runtime.additionalSnapshots[%d].%s", i, field)` convention consistently.

**No `additionalVolume` semantic rule in `validate.go` today.** The EC2-only check for `additionalVolume` is in `pkg/compiler/service_hcl.go:681` (compiler-side), NOT in `validate.go`. The BRIEF/CONTEXT wants the Layer 1 substrate check to be in `validate.go` for `additionalSnapshots`. This is a behavioral difference from the existing `additionalVolume` pattern — the planner should note that adding substrate checks to `validate.go` for snapshots (while `additionalVolume` checks remain in the compiler) creates an inconsistency. The planner could either: (a) add the EC2-only check to `validate.go` only for `additionalSnapshots` (as spec says), or (b) also move the `additionalVolume` EC2-only check to `validate.go` for consistency. The CONTEXT.md says match "same error wording style" — it does NOT say "same location." Option (a) matches the spec without scope creep.

### Q10 — AWS-calling code in `pkg/profile/`

**CONFIRMED: None today.** `pkg/profile/` contains no AWS SDK imports or calls. The new `aws_validate.go` file will be the first AWS caller in this package. The DI / mocking pattern to follow is from `pkg/aws/ec2_ami.go` — narrow interface approach:

```go
// EC2AMIAPI narrow interface at pkg/aws/ec2_ami.go:21–31
type EC2AMIAPI interface {
    CreateImage(...)
    DescribeImages(...)
    ...
}
```

For `aws_validate.go`, define a narrow `EC2SnapshotAPI` interface with only `DescribeSnapshots`:
```go
type EC2SnapshotAPI interface {
    DescribeSnapshots(ctx context.Context, params *ec2.DescribeSnapshotsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error)
}
```

This matches the existing `EC2VolumeAPI` interface in `internal/app/cmd/doctor_ebs.go:42–47` which already declares `DescribeSnapshots` with this identical signature.

### Q11 — `create.go` — where to wire Layer 2 pre-flight

**CONFIRMED.** The wiring point is between step 5 (AWS credentials validated) and step 6 (network config load) OR after step 6 but before the AZ retry loop (step 7 compile). The existing BDM lookup pattern at lines 619–631 is the closest analog — it runs after credentials are validated and before `compiler.Compile` is called inside the retry loop.

For Phase 87, the Layer 2 pre-flight must run BEFORE the retry loop starts (line 638) and BEFORE any sandbox directory is created. The profile's region is locked at line 467: `region := resolvedProfile.Spec.Runtime.Region`. The AWS client for the pre-flight must use `awsCfg` (from line 392) which points to the operator's AWS profile, not a region-specific override. The EC2 client for `DescribeSnapshots` must be constructed with the sandbox's target region override:

```go
// Pattern from existing BDM lookup at create.go:624
ec2Client := ec2svc.NewFromConfig(awsCfg)
// For snapshot pre-flight, must override region:
ec2Client := ec2svc.NewFromConfig(awsCfg, func(o *ec2svc.Options) { o.Region = region })
```

The resolved region is at `resolvedProfile.Spec.Runtime.Region` (line 467) and is used for all subsequent AWS calls. This is the correct region for `DescribeSnapshots`.

### Q12 — Region resolution precedence in `km create`

**CONFIRMED.** Region is read from `resolvedProfile.Spec.Runtime.Region` at `create.go:467`. The profile's region is set during resolution (flag → profile → default). There is no flag-level region override for EC2 substrate beyond what is in the profile. The `awsCfg` AWS config is loaded once at line 392 without a region override; the SDK uses the operator's default profile region. All EC2 API calls in `create.go` use `ec2svc.NewFromConfig(awsCfg)` without region override — they rely on the sandbox's target region being set in `awsCfg` OR on the fact that cross-region EC2 calls return `InvalidSnapshot.NotFound` naturally.

For the pre-flight, the safest approach is to explicitly override the region when constructing the EC2 client: `ec2svc.NewFromConfig(awsCfg, func(o *ec2svc.Options) { o.Region = region })`.

### Q13 — `validate.go` vs `create.go` — pre-flight NOT in validate

**CONFIRMED.** `internal/app/cmd/validate.go` (155 lines) calls only `profile.Validate(raw)` and `profile.ValidateSemantic(resolved)`. No AWS calls, no config, no credentials. The new `aws_validate.go` function must be wired into `create.go` only, not referenced from `validate.go`.

### Q14 — `variables.tf` — `additional_volume_*` variables (full quote)

**CONFIRMED.** `infra/modules/ec2spot/v1.0.0/variables.tf` lines 113–129:
```hcl
variable "additional_volume_size_gb" {
  type        = number
  description = "Additional EBS data volume size in GB. 0 means no additional volume."
  default     = 0
}

variable "additional_volume_encrypted" {
  type        = bool
  description = "Encrypt the additional EBS volume"
  default     = false
}

variable "additional_volume_device_name" {
  type        = string
  description = "Device name for the additional EBS volume attachment. Defaults to /dev/sdf..."
  default     = "/dev/sdf"
}
```

### Q15 — `main.tf` — `aws_ebs_volume.additional` and `aws_volume_attachment.additional` (full quote)

**CONFIRMED.** `infra/modules/ec2spot/v1.0.0/main.tf` lines 685–708:
```hcl
resource "aws_ebs_volume" "additional" {
  count             = var.additional_volume_size_gb > 0 ? 1 : 0
  availability_zone = local.effective_azs[0]
  size              = var.additional_volume_size_gb
  encrypted         = var.additional_volume_encrypted
  type              = "gp3"

  tags = {
    "km:sandbox-id"      = var.sandbox_id
    "km:resource-prefix" = var.resource_prefix
    Name                 = "km-sandbox-${var.sandbox_id}-data"
  }
}

resource "aws_volume_attachment" "additional" {
  count        = var.additional_volume_size_gb > 0 ? 1 : 0
  device_name  = var.additional_volume_device_name
  volume_id    = aws_ebs_volume.additional[0].id
  instance_id  = length(local.ec2spot_map) > 0 ? aws_spot_instance_request.ec2spot[keys(local.ec2spot_map)[0]].spot_instance_id : aws_instance.ec2_ondemand[keys(local.ec2_ondemand_map)[0]].id
  force_detach = true
}
```

`local.ec2spot_map` and `local.ec2_ondemand_map` are defined at `main.tf` lines 134–144:
```hcl
ec2spot_map = {
  for ec2spot in local.ec2spot_instances :
  ec2spot.key => ec2spot
  if ec2spot.use_spot
}
ec2_ondemand_map = {
  for ec2spot in local.ec2spot_instances :
  ec2spot.key => ec2spot
  if !ec2spot.use_spot
}
```

The snapshot attachment `aws_volume_attachment.snapshot` must copy the `instance_id` ternary verbatim.

### Q16 — Terragrunt provider declarations

**CONFIRMED.** No `required_providers` block or `provider` declaration in `infra/modules/ec2spot/v1.0.0/main.tf`. The AWS provider is declared in `infra/live/root.hcl` via `generate "provider"` (per CLAUDE.md memory and confirmed by absence in the module). The new `v1.1.0` copy must NOT add provider declarations.

---

## AWS SDK & DescribeSnapshots Pattern

### SDK Version
AWS SDK Go v2: `github.com/aws/aws-sdk-go-v2/service/ec2 v1.296.0` (from `go.mod`). Smithy-go v1.25.1 for error detection.

### Existing `DescribeSnapshots` caller (doctor_ebs.go)
`internal/app/cmd/doctor_ebs.go:343–358` is the authoritative pattern:

```go
// Narrow interface (lines 42–47):
type EC2VolumeAPI interface {
    DescribeVolumes(...)
    DescribeSnapshots(ctx context.Context, params *ec2.DescribeSnapshotsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error)
    DescribeImages(...)
    DeleteVolume(...)
}

// Call pattern with pagination (lines 341–358):
var snaps []ec2types.Snapshot
var snapToken *string
for {
    out, err := ec2Client.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
        OwnerIds: []string{"self"},
        Filters: []ec2types.Filter{
            {Name: awssdk.String("tag:km:sandbox-id"), Values: []string{"*"}},
        },
        NextToken: snapToken,
    })
    if err != nil {
        return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not describe EBS snapshots: %v", err)}
    }
    snaps = append(snaps, out.Snapshots...)
    if out.NextToken == nil { break }
    snapToken = out.NextToken
}
```

### Phase 87 pre-flight pattern

For `aws_validate.go`, the call uses `SnapshotIds` filter (not owner/tag filter) to look up specific IDs by name. AWS `DescribeSnapshots` with explicit `SnapshotIds` returns `InvalidSnapshot.NotFound` for any ID that does not exist in the current region or is inaccessible:

```go
// Boilerplate for aws_validate.go:
import (
    "context"
    "errors"
    "fmt"
    "strings"

    "github.com/aws/aws-sdk-go-v2/service/ec2"
    ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
    smithy "github.com/aws/smithy-go"
)

type EC2SnapshotAPI interface {
    DescribeSnapshots(ctx context.Context, params *ec2.DescribeSnapshotsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error)
}

func ValidateSnapshotsAWS(ctx context.Context, client EC2SnapshotAPI, p *SandboxProfile) error {
    if len(p.Spec.Runtime.AdditionalSnapshots) == 0 {
        return nil
    }
    ids := make([]string, len(p.Spec.Runtime.AdditionalSnapshots))
    for i, s := range p.Spec.Runtime.AdditionalSnapshots {
        ids[i] = s.SnapshotID
    }
    out, err := client.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
        SnapshotIds: ids,
    })
    if err != nil {
        // IAM missing: WARN and skip
        var apiErr smithy.APIError
        if errors.As(err, &apiErr) {
            if apiErr.ErrorCode() == "UnauthorizedOperation" || apiErr.ErrorCode() == "AccessDenied" {
                // log WARN and return nil (graceful degradation)
                return nil
            }
            if strings.Contains(apiErr.ErrorCode(), "InvalidSnapshot.NotFound") {
                // extract snap ID from message; return detailed error
            }
        }
        return fmt.Errorf("DescribeSnapshots failed: %w", err)
    }
    // Assert state == "completed", size constraints
    ...
}
```

### Pagination note

`DescribeSnapshots` with explicit `SnapshotIds` (up to 1000 IDs per call) still paginates, but Phase 87 is bounded to 11 entries maximum (the device pool), so a single call with no pagination loop is sufficient and correct. Confirmed: the device pool is 11 slots (`/dev/sd[f-p]`).

### IAM degradation pattern

Two patterns exist in the codebase:
1. `doctor.go:658` — `strings.Contains(err.Error(), "AccessDenied")` (string check, simpler)
2. `configure.go:103–105` — `errors.As(err, &apiErr); apiErr.ErrorCode() == "AccessDenied"` (smithy typed, preferred)

Use the smithy typed pattern (`configure.go` style) for `aws_validate.go`. The IAM missing error codes for EC2 are `"UnauthorizedOperation"` (EC2-specific) not `"AccessDenied"` (S3/IAM-specific). Test with both.

---

## Phase 80 Module-Versioning Recipe

### Confirmed pattern (dynamodb-identities: v1.0.0 → v1.1.0)

```
infra/modules/dynamodb-identities/
├── v1.0.0/   ← IMMUTABLE, untouched
└── v1.1.0/   ← new directory, copy of v1.0.0 + additive diff
```

**Step-by-step for `ec2spot/v1.1.0`:**

1. `cp -r infra/modules/ec2spot/v1.0.0/ infra/modules/ec2spot/v1.1.0/`
2. Edit `infra/modules/ec2spot/v1.1.0/variables.tf` — add `additional_snapshots` variable block.
3. Edit `infra/modules/ec2spot/v1.1.0/main.tf` — add `aws_ebs_volume.snapshot` + `aws_volume_attachment.snapshot` resource blocks.
4. Edit `infra/templates/sandbox/terragrunt.hcl` line 43 — change `v1.0.0` to `v1.1.0`.

**No `import {}` or `removed {}` blocks needed.** The existing sandbox directories in `infra/live/` already have their own `terragrunt.hcl` with `v1.0.0` hardcoded (confirmed: `infra/live/use1/sandboxes/gebpf-e71924ec/terragrunt.hcl:43` says `v1.0.0`). Those existing files are NOT touched — they are operator-committed files that pin to the old module. Only the template is updated so new sandboxes created after Phase 87 use `v1.1.0`.

**Note:** `pkg/terragrunt/modulehygiene_test.go` includes a lint test that warns on hardcoded `km-` prefix literals in `v1.0.0/` modules (lines 205–252) and skips `v1.0.0` in the non-hardcoded prefix audit (line 294). The new `v1.1.0` module is subject to the prefix audit — do not hardcode `km-` in tags or names; use `var.resource_prefix` (as `v1.0.0` already does for the `km:resource-prefix` tag).

---

## Validation Architecture

> `workflow.nyquist_validation` not explicitly set to false in `.planning/config.json` — include this section.

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` (stdlib) — no separate test runner |
| Config file | none (plain `go test ./...`) |
| Quick run command | `go test ./pkg/profile/... ./pkg/compiler/... -count=1 -run TestAdditional` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| SNAP-01 | Parse YAML with 0/1/3 snapshot entries; assert struct shape | unit | `go test ./pkg/profile/... -run TestAdditionalSnapshot` | ❌ Wave 0 |
| SNAP-02 | Layer 1: bad regex, mountpoint collision, reserved paths, non-EC2, device dupe | unit | `go test ./pkg/profile/... -run TestValidate` | ❌ Wave 1 |
| SNAP-03 | Layer 2: mock DescribeSnapshots — happy, NotFound, pending, size-too-small, IAM-warn | unit | `go test ./pkg/profile/... -run TestAWSValidate` | ❌ Wave 2 |
| SNAP-04 | HCL render: one entry per snapshot, device alloc respects pins + AMI BDM + additionalVolume | unit | `go test ./pkg/compiler/... -run TestAdditionalSnapshot` | ❌ Wave 3 |
| SNAP-05 | Userdata golden: legacy additionalVolume-only output byte-identical (modulo `${FSTYPE}`); multi-entry loop order | unit | `go test ./pkg/compiler/... -run TestUserdata` | ❌ Wave 3 |
| SNAP-06 | TF validate on dry-run profile with v1.1.0 module | manual (terragrunt validate) | n/a | ❌ Wave 4 |
| SNAP-07 | Profiles without additionalSnapshots → zero HCL diff vs pre-Phase-87 | unit (golden) | `go test ./pkg/compiler/... -run TestBackwardCompat` | ❌ Wave 3 |
| SNAP-08 | 8 UAT scenarios (real AWS) | integration / manual | `km create`, SSM in, verify | ❌ Wave 5 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/profile/... ./pkg/compiler/... -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/profile/types_test.go` — extend with SNAP-01 YAML parse cases
- [ ] `pkg/profile/aws_validate_test.go` (NEW) — mock interface + table-driven SNAP-03 cases
- [ ] `pkg/profile/validate_test.go` — extend with SNAP-02 Layer 1 cases
- [ ] `pkg/compiler/service_hcl_test.go` — extend with SNAP-04 HCL render assertions
- [ ] `pkg/compiler/userdata_test.go` — extend with SNAP-05 golden file + loop assertions
- [ ] `pkg/compiler/ec2_storage_test.go` — extend `TestPickAdditionalVolumeDevice` for new `claimed` param

### Aliasing risks (Nyquist)

**SNAP-07 (golden file, HIGHEST RISK):** The byte-identical userdata assertion is most aliasing-prone. If the refactored `range .AdditionalVolumeMounts` loop produces the same bash for the single-entry case, the test passes — but it may silently omit the `blkid`-based `FSTYPE` substitution in a way that works identically on ext4 volumes at runtime while failing on xfs/btrfs. Mitigation: golden file must specifically assert the fstab line contains `${FSTYPE}` (not hard-coded `ext4`).

**SNAP-03 (mocked DescribeSnapshots):** The mock for "IAM-missing" must use the correct EC2 error code (`UnauthorizedOperation`, not `AccessDenied`) or the WARN-and-skip path won't be exercised. Aliasing risk: a test that mocks `err != nil` without checking the specific code path will not catch the wrong error type being tested.

**SNAP-04 (device allocation):** Tests must cover the exact case where explicit `device` on entry 0 + auto-picked entry 1 yields the correct next-available slot skipping both AMI BDM and the pinned device. A simple "auto-pick from empty claimed" test does not catch the cross-entry deduplication.

---

## Risks & Unknowns

### Risk 1: BRIEF/CONTEXT claim "compiler.go bump" is WRONG

**Finding:** The module version string `v1.0.0` is in `infra/templates/sandbox/terragrunt.hcl:43`, not in `compiler.go`. The BRIEF.md says "bump `compiler.go` to emit `ec2spot/v1.1.0`" — this is incorrect. The actual bump location is the sandbox terragrunt template. Impact: planner must target the right file.

### Risk 2: `nvmeAlias` template helper DOES NOT EXIST

The PRD spec and BRIEF describe a `nvmeAlias` Go template func used as `{{ nvmeAlias .Device }}` in the userdata template. No such func is registered today. The existing code uses a hard-coded bash `for dev in /dev/xvdf /dev/sdf /dev/nvme1n1 /dev/nvme2n1` enumeration — device-agnostic, not parametric. The planner must choose: (a) register a new `nvmeAlias` template func that returns the xvd/sd alias for a given device, or (b) adopt a parametric bash probe that enumerates both the given device name and its alias. Option (b) is more self-contained and avoids adding a new template func dependency.

### Risk 3: Nullable-bool HCL helper DOES NOT EXIST

No `boolPtrHCL` or equivalent template function exists in `templateFuncs`. The planner must add one to `pkg/compiler/service_hcl.go:573`. Draft implementation:
```go
"boolPtrHCL": func(b *bool) string {
    if b == nil { return "null" }
    if *b { return "true" }
    return "false"
},
```
Used in the template as `{{ boolPtrHCL .Encrypted }}` where `.Encrypted` is `*bool`.

### Risk 4: BDM lookup gate must be extended for snapshots-only profiles

`create.go:623` currently gates BDM lookup on `resolvedProfile.Spec.Runtime.AdditionalVolume != nil`. If a profile has `additionalSnapshots` but no `additionalVolume`, the BDM lookup is skipped today, and the compiler gets nil for `amiBDMDevices`. Since Phase 87 extends `pickAdditionalVolumeDevice` to use both AMI BDM and claimed devices, the BDM lookup gate MUST be broadened to:
```go
if compiler.IsRawAMIID(resolvedProfile.Spec.Runtime.AMI) &&
   (resolvedProfile.Spec.Runtime.AdditionalVolume != nil || len(resolvedProfile.Spec.Runtime.AdditionalSnapshots) > 0) {
```
Without this, UAT-4 (AMI with BDM declaring /dev/sdf, profile uses only additionalSnapshots) will collide.

### Risk 5: `AdditionalVolumeEncrypted` is `bool`, new `Encrypted` must be `*bool`

The existing `AdditionalVolumeSpec.Encrypted` is a plain `bool` (line 137 in types.go). The new `AdditionalSnapshotSpec.Encrypted` must be `*bool`. These are different types — the planner must not accidentally copy the existing bool pattern. The struct field needs pointer syntax and the `omitempty` yaml tag behaves differently for pointer vs value types with go-yaml.

### Risk 6: `for_each` index key type in Terraform

The spec uses `{ for i, s in var.additional_snapshots : i => s }` with `i` as the loop index. In Terraform, `for` expression indices on lists are integers, but `for_each` keys must be strings. The correct pattern is `{ for i, s in var.additional_snapshots : tostring(i) => s }`. This is consistent with how the `ec2spot_instances` local builds keys (line 117: `key = "${ec2spot.region}-${idx}-${instance_idx}"`). The PRD spec's `for i, s in var.additional_snapshots : i => s` will work if Terraform auto-converts the index, but `tostring(i)` is safer and explicit.

### Risk 7: `AdditionalSnapshotEntry.Encrypted` type in Go render struct

The BRIEF shows `AdditionalSnapshotEntry.Encrypted bool` in the Go render struct. But since the profile field is `*bool`, the template must emit `null` when nil, not `false`. The render struct should keep `Encrypted *bool` and use the `boolPtrHCL` template func rather than flattening to `bool`.

### Risk 8: `modulehygiene_test.go` lint for v1.1.0

`pkg/terragrunt/modulehygiene_test.go` explicitly SKIPS `v1.0.0` in the hardcoded-prefix audit (line 294: `if vName == "v1.0.0" { continue }`). The new `v1.1.0` will be subject to this check. The `v1.0.0/main.tf` contains `Name = "km-sandbox-${var.sandbox_id}-data"` and `"km:sandbox-id"` — these use literal `km-` rather than `var.resource_prefix`. The new `v1.1.0` resources must use `"km-sandbox-${var.sandbox_id}-snap-${each.key}"` — this also has a literal `km-`. To pass the hygiene test, the snapshot volume Name tag should use `"${var.resource_prefix}-sandbox-${var.sandbox_id}-snap-${each.key}"` or the test will WARN. Check whether the hygiene test actually fails or just warns (WARN-only per line 252) — it is WARN-only, not a blocker.

### Risk 9: Layer 1 EC2-only check location inconsistency

The existing `additionalVolume` EC2-only check is in the COMPILER (`service_hcl.go:681`), not in `validate.go`. The spec puts the `additionalSnapshots` EC2-only check in `validate.go`. This means `km validate` will catch the substrate error for snapshots but NOT for additionalVolume (where the compiler catches it). This is acceptable per spec ("same wording style" not "same location") but the planner should document the inconsistency clearly so it is not perceived as a bug.

---

## Recommended Wave Order

Based on the actual dependency graph found in code (refined from BRIEF.md's Wave 0–5 sketch):

**Wave 0 — Schema + Test Stubs (SNAP-01 only)**
- Add `AdditionalSnapshotSpec` to `pkg/profile/types.go`
- Add `AdditionalSnapshots []AdditionalSnapshotSpec` to `RuntimeSpec`
- Add `additionalSnapshots` array to JSON schema
- Create stub test files (RED): `pkg/profile/types_test.go` parse tests, `pkg/profile/aws_validate_test.go` skeleton, `pkg/profile/validate_test.go` SNAP-02 cases

**Wave 1 — Layer 1 Validation (SNAP-02)**
- Add Layer 1 rules to `pkg/profile/validate.go` `ValidateSemantic`
- Also add EC2-only check to `pkg/compiler/service_hcl.go:validateEC2StorageFields` (for parity with additionalVolume path)
- Unit tests GREEN

**Wave 2 — Layer 2 AWS Pre-flight (SNAP-03)**
- Create `pkg/profile/aws_validate.go` with `EC2SnapshotAPI` interface + `ValidateSnapshotsAWS` function
- Add `boolPtrHCL` template func to `service_hcl.go:templateFuncs`
- Wire into `create.go` after credentials load, before compile loop; also extend BDM lookup gate (Risk 4)
- Unit tests GREEN (mocked)
- Waves 1 and 2 are parallel after Wave 0

**Wave 3 — Compiler + Userdata (SNAP-04 + SNAP-05)**
- Extend `pickAdditionalVolumeDevice` signature to `(amiDevices []string, claimed map[string]bool) string`
- Update call sites (service_hcl.go:767 and ec2_storage_test.go:378)
- Add `AdditionalSnapshotEntry` type and `AdditionalSnapshots []AdditionalSnapshotEntry` to `ec2HCLParams`
- Add template block in `ec2ServiceHCLTemplate` for `additional_snapshots = [...]`
- Add `AdditionalVolumeMountEntry` struct and `AdditionalVolumeMounts []AdditionalVolumeMountEntry` to userdata render struct
- Refactor `{{- if .AdditionalVolumeMountPoint }}` → `{{- range .AdditionalVolumeMounts }}`; add `blkid` FS detection; add nvmeAlias probe (bash-side or new template func)
- Wire device allocation in `generateEC2ServiceHCL` for snapshot entries
- Golden-file test for legacy userdata byte-identity (modulo `${FSTYPE}`)
- Wave 3 depends on Wave 0; can start in parallel with Wave 2's `boolPtrHCL` work

**Wave 4 — Terraform Module v1.1.0 (SNAP-06)**
- `cp -r infra/modules/ec2spot/v1.0.0/ infra/modules/ec2spot/v1.1.0/`
- Add `additional_snapshots` variable to `variables.tf`
- Add `aws_ebs_volume.snapshot` + `aws_volume_attachment.snapshot` to `main.tf`
- Change `infra/templates/sandbox/terragrunt.hcl:43` from `v1.0.0` to `v1.1.0`
- `terragrunt validate` clean check
- Wave 4 depends on Wave 3 (compiler render-struct defines what HCL is emitted)

**Wave 5 — UAT + Docs (SNAP-07 + SNAP-08)**
- Backward-compat assertions (zero-diff for legacy profiles) — mostly Wave 3 golden tests, but final validation here
- 8 UAT scenarios (real AWS): UAT-1 through UAT-8 from BRIEF.md
- `OPERATOR-GUIDE.md` section under EC2 substrate
- `CLAUDE.md` bullet under Architecture
- Optional: example profile in `profiles/`

**Key parallel opportunities:**
- Wave 1 and Wave 2 are independent after Wave 0 (different files)
- Wave 3's `boolPtrHCL` addition can start as soon as Wave 1 is done (no dependency)
- Wave 4 can start as soon as Wave 3's compiler render-struct is stable (does not need userdata changes)

---

## Sources

### Primary (HIGH confidence)
- `pkg/compiler/service_hcl.go` — `pickAdditionalVolumeDevice` (lines 42–65), `validateEC2StorageFields` (lines 671–691), `ec2HCLParams` struct (lines 430–477), `ec2ServiceHCLTemplate` (lines 75–167), `templateFuncs` (line 573)
- `pkg/compiler/userdata.go` — `{{- if .AdditionalVolumeMountPoint }}` block (lines 96–138), render struct (lines 3240–3311), wiring (lines 3688–3690)
- `pkg/compiler/compiler.go` — `Compile` function (lines 75–99), `compileEC2` (lines 101–160)
- `pkg/profile/types.go` — `AdditionalVolumeSpec` (lines 130–138), `RuntimeSpec` (lines 140–164)
- `pkg/profile/validate.go` — `ValidationError` type (lines 14–33), `ValidateSemantic` entry (lines 219–411)
- `pkg/profile/schemas/sandbox_profile.schema.json` — `additionalVolume` schema (lines 198–219)
- `infra/modules/ec2spot/v1.0.0/variables.tf` — all variable declarations
- `infra/modules/ec2spot/v1.0.0/main.tf` — `aws_ebs_volume.additional` (lines 685–708), locals (lines 95–145)
- `infra/templates/sandbox/terragrunt.hcl` — version string (line 43)
- `pkg/terragrunt/sandbox.go` — template copy mechanism (lines 10–29)
- `internal/app/cmd/create.go` — region lock (line 467), BDM lookup (lines 619–631), wiring (lines 638–648)
- `internal/app/cmd/validate.go` — confirms no AWS calls (155 lines, examined in full)
- `internal/app/cmd/doctor_ebs.go` — `EC2VolumeAPI` with `DescribeSnapshots` (lines 42–47), usage pattern (lines 341–358)
- `internal/app/cmd/configure.go` — smithy APIError pattern (lines 103–115)
- `pkg/aws/ec2_ami.go` — `EC2AMIAPI` narrow interface (lines 21–31), `AMIBDMDeviceNames` (lines 158–179)
- `pkg/compiler/ec2_storage_test.go` — existing test structure (443 lines, examined in full)
- `go.mod` — AWS SDK v2 versions

### Secondary (MEDIUM confidence)
- `pkg/terragrunt/modulehygiene_test.go` — module hygiene rules for v1.1.0 (lines 205–294); WARN-only, not FAIL
- `infra/modules/dynamodb-identities/v1.1.0/main.tf` — confirmed copy-and-diff pattern for minor version bump (no import/moved blocks needed for additive changes)

---

## RESEARCH COMPLETE

**Phase:** 87 - additionalSnapshots — snapshot-backed EBS volumes in SandboxProfile
**Confidence:** HIGH

### Key Findings

1. **Three spec claims are wrong:** (a) The module version bump is in `infra/templates/sandbox/terragrunt.hcl:43`, not `compiler.go`. (b) The `nvmeAlias` Go template helper does not exist — must be created or replaced with a bash-side probe. (c) No nullable-bool HCL helper exists — `boolPtrHCL` must be added to `templateFuncs`.

2. **BDM lookup gate must be extended:** `create.go:623` currently gates BDM enumeration on `AdditionalVolume != nil`. Phase 87 must extend this to also trigger when `len(AdditionalSnapshots) > 0` or UAT-4 will silently fail for snapshots-only profiles.

3. **`DescribeSnapshots` pattern confirmed in doctor_ebs.go:** `EC2VolumeAPI` interface (line 42) and paginated usage (lines 341–358) are the direct model for `aws_validate.go`. The smithy error-code pattern from `configure.go:103–115` (`errors.As(err, &apiErr); apiErr.ErrorCode()`) is the correct IAM-missing detection approach, using `"UnauthorizedOperation"` as the EC2 error code.

### File Created
`.planning/phases/87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile/87-RESEARCH.md`

### Confidence Assessment

| Area | Level | Reason |
|------|-------|--------|
| Schema changes | HIGH | `types.go` and JSON schema read directly; shape is clear |
| Layer 1 validation | HIGH | `validate.go` and `ValidationError` type read directly |
| Layer 2 pre-flight | HIGH | `doctor_ebs.go` provides exact SDK pattern; `create.go` wiring point confirmed |
| Compiler / service_hcl | HIGH | All structs, funcs, templates, and templates read directly; 3 gaps documented |
| Userdata refactor | HIGH | Existing block quoted exactly; nvmeAlias gap documented |
| Terraform module | HIGH | `variables.tf` and `main.tf` read directly; copy-bump pattern confirmed via dynamodb-identities |
| Version bump location | HIGH | Template file confirmed; BRIEF claim corrected |

### Open Questions
- None blocking. All gaps are implementation decisions for the planner (boolPtrHCL implementation, nvmeAlias approach choice), not unknowns.
