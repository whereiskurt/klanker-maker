# Phase 56: Learn Mode AMI Snapshot and Lifecycle Management — Research

**Researched:** 2026-04-26
**Domain:** AWS EC2 AMI lifecycle management + Go CLI extension
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Snapshot timing and content:**
- Snapshot fires *before* the SIGUSR1 flush in `runLearnPostExit` (`shell.go:589`). Cleanest because the AMI captures the state the operator just shaped; flush runs after against a stable still-running instance.
- Content: root volume + `additionalVolume` (e.g., `/data`). EFS is **always excluded**.
- Instance state: **live snapshot with `NoReboot=true`** via AWS `CreateImage`. Instance keeps running.
- `km ami bake <sandbox-id>` ships as a peer command alongside `--learn --ami`. Same code path.

**AMI lifecycle policy — `km ami delete`:**
- Default safety: refuse if any profile in `profiles/*.yaml` references the AMI ID in `spec.runtime.ami`. `--force` overrides.
- Snapshot cleanup: auto-delete the underlying EBS snapshots when deregistering. Use `DeleteAssociatedSnapshots: aws.Bool(true)` on `DeregisterImage` call.
- Confirmation: prompt before delete; `--yes` skips.

**AMI lifecycle policy — `km doctor` stale check:**
- Threshold: configurable in `km-config.yaml` as `doctor.staleAMIDays`, **default 30 days**.
- "Unused" definition: strict — AMI must be **(a) not referenced by any local profile in `profiles/`** AND **(b) not actively backing any running sandbox**. Both required to flag as stale.
- Action: doctor flags only (no auto-delete).

**Multi-region behavior:**
- `km ami list` default scope: current `KM_REGION` only. `--all-regions` walks configured regions in parallel. Single-region via `--region <r>`.
- Cross-region copy: NO auto-copy. `km ami copy <ami-id> --to-region <r>` is the manual path.
- `km doctor` stale check default scope: current `KM_REGION` only, `--all-regions` opt-in.
- Phase 33 slug verification task: run `terraform plan` or equivalent compiler test against `ca-central-1` with `ami: amazon-linux-2023`.

**`km ami list` output and filtering:**
- Narrow columns: ID, Name, Age, Size (GB), Source profile, In-use refcount.
- Wide columns: add source sandbox-id, region, snapshot count, encrypted (bool), instance type at bake, estimated $/month (`size_gb × $0.05` hardcoded approximation).
- Default sort: newest first (creation date descending).
- All four filters ship: `--profile`, `--age`, `--unused`, `--region`.

### Claude's Discretion
- AMI `Name` field format — planner picks (suggest: `km-{profile-name}-{sandbox-id}-{YYYYMMDDHHMMSS}`).
- AMI tag schema — must include sandbox-id, profile, alias, date. Planner adds others as useful.
- Snapshot-failure mid-flow behavior — suggest: log the error, write the generated profile *without* the `ami:` field, exit non-zero.
- Dry-run / preview — whether `km ami delete --dry-run` shows what would be deleted. Suggest: yes.
- Progress reporting during snapshot — suggest: poll with timestamped log lines via `ImageAvailableWaiter`.
- Error message tone and exit codes — follow existing `km` conventions.
- Encryption inheritance — AWS `CreateImage` inherits root-volume encryption automatically; no operator-facing flag needed.

### Deferred Ideas (OUT OF SCOPE)
- Auto-cross-region copy on snapshot
- AMI sharing across AWS accounts
- AMI version chaining / rebake
- Multi-region platform deployment (Phase 62-ish)
- Auto-cleanup mode for `km doctor --apply`
- Time-based usage tracking via CloudTrail
- AMI cost dashboard / `km ami cost` command
</user_constraints>

<phase_requirements>
## Phase Requirements

Phase 56 has no pre-assigned IDs in the roadmap. The following phase-local IDs are proposed to cover the full scope:

| ID | Description | Research Support |
|----|-------------|-----------------|
| P56-01 | `--ami` flag on `km shell --learn` triggers `CreateImage` before SIGUSR1 flush; generated profile gets `spec.runtime.ami` | See: `runLearnPostExit` insertion point at `shell.go:621`; `CreateImage` SDK verified |
| P56-02 | AMI tagging schema: tags applied atomically via `TagSpecifications` in `CreateImage` call | See: SDK `CreateImageInput.TagSpecifications`; supports both `image` and `snapshot` resource types in one call |
| P56-03 | `km ami bake <sandbox-id>` peer command; same `bakeAMI()` function as P56-01 path | See: Cobra subcommand pattern in `at.go`, `email.go` |
| P56-04 | `km ami list` with narrow default and `--wide` columns, sorted newest-first | See: `km list` pattern in `list.go`; `Image.CreationDate` string field available |
| P56-05 | `km ami list` filters: `--profile`, `--age`, `--unused`, `--region`, `--all-regions` | Research covers multi-region parallel pattern from `doctor.go:1817` |
| P56-06 | `km ami delete <ami-id>` with profile refcount check, `--force`, `--dry-run`, `--yes`, auto-snapshot cleanup | `DeregisterImage` with `DeleteAssociatedSnapshots: true`; profile scan pattern documented |
| P56-07 | `km ami copy <ami-id> --to-region <r>` cross-region replication | `CopyImage` SDK verified; returns ImageId immediately (pending state); re-tag required |
| P56-08 | `checkStaleAMIs` in `km doctor`; plugs into `runChecks` fan-out; configurable `doctor.staleAMIDays` | See: `checkStaleKMSKeys` pattern; `Config` struct extension pattern |
| P56-09 | `Doctor.StaleAMIDays int` field added to `Config` struct and `km-config.yaml` | See: `config.go` Load() defaults pattern |
| P56-10 | IAM policy additions: `ec2:DeregisterImage`, `ec2:DeleteSnapshot`, `ec2:DescribeImages` (operator role) | See: IAM gap analysis in Architecture Patterns; `bootstrap.go` SCP context |
| P56-11 | Generated-profile writer emits `ami: ami-xxxxxxxx` in `spec.runtime.ami` via `Generate()`/`GenerateAnnotatedYAML()` | See: `allowlistgen/generator.go`; `RuntimeSpec.AMI` field exists via Phase 33.1 |
| P56-12 | Phase 33 slug verification in ca-central-1 | Manual test plan item; closes Phase 33 open human-verification item |
</phase_requirements>

---

## Summary

Phase 56 adds AMI snapshot baking and lifecycle management to the learn-mode workflow. The core API surface is the AWS EC2 SDK's `CreateImage`, `DescribeImages`, `DeregisterImage` (with `DeleteAssociatedSnapshots`), and `CopyImage` — all confirmed present in the project's pinned SDK version (`aws-sdk-go-v2/service/ec2@v1.296.0`).

The project has no existing Go code for AMI management. The new `pkg/aws/ec2_ami.go` helper file and `internal/app/cmd/ami.go` Cobra command file are both net-new. Every structural pattern needed is already demonstrated by existing code: `doctor.go`'s `checkStaleKMSKeys` for the stale-check pattern; `at.go`/`email.go` for the subcommand tree; `list.go` for narrow-vs-wide tabwriter output; `destroy.go` for confirmation prompts; `shell.go` for the `runLearnPostExit` insertion point.

One IAM gap exists: the operator-side (`klanker-terraform` profile, AWSReservedSSO role) does not currently have `ec2:DeregisterImage`, `ec2:DeleteSnapshot` in any documented policy. `ec2:DescribeImages` is present in Lambda roles but not explicitly granted to the operator role. Phase 56 must add these to `bootstrap.go`'s operator guidance and ensure the SSO role in the application account has these permissions. `ec2:CreateImage` and `ec2:CopyImage` are already listed in the SCP trusted-base deny exceptions, meaning they are implicitly allowed for the SSO operator role.

**Primary recommendation:** New file `pkg/aws/ec2_ami.go` with Go interface + real implementation covering `CreateImage`/`DescribeImages`/`DeregisterImage`/`CopyImage`; new file `internal/app/cmd/ami.go` for the Cobra subcommand tree; extend `runLearnPostExit` in `shell.go` to call the bake function before the flush; extend `allowlistgen.Generator.Generate()` to emit `spec.runtime.ami`; add `checkStaleAMIs` to `doctor.go`; add `Doctor.StaleAMIDays` to `config.go`.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/ec2` | v1.296.0 (already in go.mod) | CreateImage, DescribeImages, DeregisterImage, CopyImage | Already pinned in project; all AMI operations are EC2 API |
| `github.com/spf13/cobra` | v1.9.1 (already in go.mod) | km ami subcommand tree | Consistent with all other km commands |
| `text/tabwriter` | stdlib | Narrow/wide table output | Same package as `km list`, `km email read` |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `ec2.ImageAvailableWaiter` | from ec2 v1.296.0 | Poll until AMI state = `available` | Use instead of manual sleep loop; default min delay 15s, max delay 120s |
| `ec2.ImageExistsWaiter` | from ec2 v1.296.0 | Confirm AMI exists before operating on it | Use in `km ami delete` pre-flight |
| `sync.WaitGroup` + goroutines | stdlib | Parallel multi-region `--all-regions` queries | Same pattern as `runChecks` in doctor.go |
| `github.com/goccy/go-yaml` | v1.19.2 (already in go.mod) | Parse profiles for AMI refcount scan | Same YAML library used throughout profile package |

### Installation
```bash
# No new dependencies needed — all packages already in go.mod
```

---

## Architecture Patterns

### Recommended File Structure
```
pkg/aws/
└── ec2_ami.go          # New: AMI CRUD interface + real implementation

internal/app/cmd/
└── ami.go              # New: km ami list/delete/bake/copy Cobra tree

pkg/allowlistgen/
└── generator.go        # Extend: emit spec.runtime.ami in Generate()

internal/app/cmd/
└── shell.go            # Extend: --ami flag + bakeAMI() call in runLearnPostExit
└── doctor.go           # Extend: checkStaleAMIs + DoctorDeps.EC2AMIClient

internal/app/config/
└── config.go           # Extend: Doctor.StaleAMIDays int field
```

### Pattern 1: SDK Interface + Real Implementation (ec2_ami.go)

**What:** Narrow interface covering the four AMI operations, implemented by a struct wrapping `*ec2.Client`. Mirrors how `KMSCleanupAPI`, `EC2InstanceAPI`, etc. are defined in `doctor.go`.

**When to use:** All new AWS API surfaces in this codebase follow this interface pattern for testability.

```go
// Source: pkg/aws/doctor.go lines 127-130 as model; new interface in pkg/aws/ec2_ami.go

// EC2AMIAPI is the narrow EC2 interface for AMI lifecycle operations.
// Implemented by *ec2.Client.
type EC2AMIAPI interface {
    CreateImage(ctx context.Context, params *ec2.CreateImageInput, optFns ...func(*ec2.Options)) (*ec2.CreateImageOutput, error)
    DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
    DeregisterImage(ctx context.Context, params *ec2.DeregisterImageInput, optFns ...func(*ec2.Options)) (*ec2.DeregisterImageOutput, error)
    CopyImage(ctx context.Context, params *ec2.CopyImageInput, optFns ...func(*ec2.Options)) (*ec2.CopyImageOutput, error)
}
```

### Pattern 2: CreateImage with Atomic Tagging

**What:** `CreateImageInput.TagSpecifications` accepts `[]types.TagSpecification` with `ResourceType` `image` and `snapshot`. Tags applied to both image and snapshots in a single API call. No separate `CreateTags` call needed.

**When to use:** Always when baking — atomic tagging prevents orphaned untagged snapshots.

```go
// Source: aws-sdk-go-v2/service/ec2@v1.296.0/api_op_CreateImage.go lines 123-137

input := &ec2.CreateImageInput{
    InstanceId:  aws.String(instanceID),
    Name:        aws.String(amiName),  // km-{profile}-{sandbox-id}-{YYYYMMDDHHMMSS}
    NoReboot:    aws.Bool(true),
    Description: aws.String(fmt.Sprintf("Baked from sandbox %s (%s)", sandboxID, profileName)),
    TagSpecifications: []types.TagSpecification{
        {
            ResourceType: types.ResourceTypeImage,
            Tags: kmTags(sandboxID, profileName, alias, instanceType, region),
        },
        {
            ResourceType: types.ResourceTypeSnapshot,
            Tags: kmTags(sandboxID, profileName, alias, instanceType, region),
        },
    },
}
out, err := amiClient.CreateImage(ctx, input)
// out.ImageId is returned immediately; AMI is in "pending" state
```

### Pattern 3: Waiter for AMI Availability

**What:** `ec2.NewImageAvailableWaiter` polls `DescribeImages` until `State == available`. Built into SDK, no manual polling needed. Default: 15s min delay, 120s max delay.

**When to use:** After `CreateImage` and `CopyImage` calls — both return immediately with the new AMI ID while the image is `pending`.

```go
// Source: aws-sdk-go-v2/service/ec2@v1.296.0/api_op_DescribeImages.go lines 387-499

waiter := ec2.NewImageAvailableWaiter(ec2Client)
if err := waiter.Wait(ctx, &ec2.DescribeImagesInput{
    ImageIds: []string{aws.ToString(createOut.ImageId)},
}, 15*time.Minute); err != nil {
    return fmt.Errorf("AMI %s did not become available: %w", aws.ToString(createOut.ImageId), err)
}
```

### Pattern 4: DeregisterImage with Automatic Snapshot Cleanup

**What:** `DeregisterImageInput.DeleteAssociatedSnapshots = aws.Bool(true)` deletes all snapshots associated with the AMI in the same API call. If a snapshot is shared with multiple AMIs, AWS silently skips it (does not error).

**When to use:** Always on `km ami delete` — this is the correct ordering: deregister first, snapshots cleaned up automatically.

```go
// Source: aws-sdk-go-v2/service/ec2@v1.296.0/api_op_DeregisterImage.go lines 52-83

_, err := amiClient.DeregisterImage(ctx, &ec2.DeregisterImageInput{
    ImageId:                   aws.String(amiID),
    DeleteAssociatedSnapshots: aws.Bool(true),
    // DryRun: aws.Bool(true),  // for --dry-run preview
})
```

**Critical:** `DeleteAssociatedSnapshots` was added to the AWS API and is present in the SDK at v1.296.0. No separate `DeleteSnapshot` calls needed for the standard path. Separate calls only needed if you want fine-grained error handling per snapshot.

### Pattern 5: Snapshot IDs Discovery (fallback path for dry-run display)

**What:** `DescribeImages` returns `Image.BlockDeviceMappings[]` where each mapping has `Ebs.SnapshotId`. Used for the `--dry-run` display to show which snapshots would be deleted.

```go
// Source: types.go — Image.BlockDeviceMappings → BlockDeviceMapping.Ebs → EbsBlockDevice.SnapshotId

func snapshotIDsFromImage(img types.Image) []string {
    var ids []string
    for _, bdm := range img.BlockDeviceMappings {
        if bdm.Ebs != nil && bdm.Ebs.SnapshotId != nil {
            ids = append(ids, *bdm.Ebs.SnapshotId)
        }
    }
    return ids
}
```

### Pattern 6: DescribeImages Owner Filter (list own AMIs)

**What:** Filter `Owners: []string{"self"}` returns only AMIs owned by the caller's account. Tag filter `km:baked-by=km` narrows to km-baked images. Pagination via `NextToken`.

```go
// Source: aws-sdk-go-v2/service/ec2@v1.296.0/api_op_DescribeImages.go

input := &ec2.DescribeImagesInput{
    Owners: []string{"self"},
    Filters: []types.Filter{
        {Name: aws.String("tag:km:sandbox-id"), Values: []string{"*"}},
        {Name: aws.String("state"), Values: []string{"available"}},
    },
}
// Note: DescribeImages does not paginate via NextToken — returns all matching images in one call
// (unlike DescribeInstances). No pagination loop needed.
```

**Important:** `DescribeImages` with `Owners: ["self"]` does NOT paginate. It returns all matching images in a single response regardless of count. No `NextToken` loop is needed.

### Pattern 7: Cobra Subcommand Tree (ami.go)

**What:** Same structure as `at.go` (list/cancel) and `email.go` (send/read). Parent `km ami` command with four children.

```go
// Source: internal/app/cmd/email.go lines 63-80 as model

func NewAMICmd(cfg *config.Config) *cobra.Command {
    ami := &cobra.Command{
        Use:          "ami",
        Short:        "Manage custom AMIs baked from sandboxes",
        SilenceUsage: true,
    }
    ami.AddCommand(newAMIListCmd(cfg))
    ami.AddCommand(newAMIDeleteCmd(cfg))
    ami.AddCommand(newAMIBakeCmd(cfg))
    ami.AddCommand(newAMICopyCmd(cfg))
    return ami
}
// Register in root.go: rootCmd.AddCommand(NewAMICmd(cfg))
```

### Pattern 8: checkStaleAMIs (doctor.go extension)

**What:** Follows `checkStaleKMSKeys` pattern exactly: (1) list all km-tagged AMIs, (2) build active set from profiles + running sandboxes, (3) compare age against threshold, (4) return delta. `dryRun=true` skips deletion but still reports.

```go
// Source: doctor.go:780-891 as structural model

func checkStaleAMIs(ctx context.Context, amiClient EC2AMIAPI, lister SandboxLister, profilesDir string, staleDays int, dryRun bool) CheckResult {
    name := "Stale AMIs"
    if amiClient == nil {
        return CheckResult{Name: name, Status: CheckSkipped, Message: "EC2 AMI client not available"}
    }
    // 1. List all km-tagged AMIs in the region
    // 2. Build set of AMI IDs referenced by profiles in profilesDir
    // 3. Build set of AMI IDs backing running sandboxes (from lister)
    // 4. Flag AMIs older than staleDays that appear in neither set
    // 5. Return CheckWarn with list; no delete action in Phase 56
}
```

### Pattern 9: runLearnPostExit Insertion Point

**What:** The `--ami` flag triggers a `bakeAMI()` call at `shell.go:621`, BEFORE the `flushEC2Observations` call at line 621. The returned AMI ID is passed to `GenerateProfileFromJSON` (or equivalent) to emit `spec.runtime.ami` in the generated YAML.

```go
// Source: internal/app/cmd/shell.go:589-686 — insertion after line 610 (fetcher setup) and before line 621 (flushEC2Observations call)

// In runLearnPostExit, after fetcher.FetchSandbox(ctx, sandboxID):
if amiFlag && rec.Substrate == "ec2" || rec.Substrate == "ec2spot" || rec.Substrate == "ec2demand" {
    amiID, err := bakeAMI(ctx, cfg, rec, sandboxID, profileName)
    if err != nil {
        log.Warn().Err(err).Msg("AMI bake failed — generating profile without ami field")
        amiID = ""  // warn-and-continue; exit non-zero after profile write
    }
    // pass amiID into state for GenerateProfileFromJSON
}
// then existing flush code at line 621...
```

### Pattern 10: Generator Extension for `spec.runtime.ami`

**What:** `allowlistgen.Generator.Generate()` already populates `p.Spec.Runtime.*` fields (line 60 in `generator.go`). Adding `AMI` follows the same pattern as `InitCommands` was added for Phase 55.

```go
// Source: pkg/allowlistgen/generator.go lines 116-120

// In Generate(), after InitCommands population:
if amiID != "" {
    p.Spec.Runtime.AMI = amiID
}
```

The `Recorder` should gain an optional `amiID string` field or the AMI ID should be passed as a parameter to `GenerateAnnotatedYAML`. Phase 55 precedent: `RecordCommand` was added to `Recorder`; `RecordAMI(id string)` follows the same pattern.

### Pattern 11: Config Extension for `doctor.staleAMIDays`

**What:** Add `Doctor.StaleAMIDays int` to `Config` struct. Set default to 30 via `v.SetDefault`. Map from `km-config.yaml` key `doctor.stale_ami_days`.

```go
// Source: internal/app/config/config.go:127-129 as pattern (MaxSandboxes, SchedulesTableName)

// In Config struct:
DoctorStaleAMIDays int  // Maps to km-config.yaml doctor.stale_ami_days. Default: 30.

// In Load():
v.SetDefault("doctor.stale_ami_days", 30)

// In unmarshal section:
cfg.DoctorStaleAMIDays = v.GetInt("doctor.stale_ami_days")
```

### Anti-Patterns to Avoid

- **Separate CreateTags call after CreateImage:** Use `TagSpecifications` in `CreateImage` input for atomic tagging. A separate call can fail leaving untagged resources.
- **Deleting snapshots before DeregisterImage:** AWS requires deregister first. `DeleteAssociatedSnapshots: true` on DeregisterImage is the correct order.
- **Polling DescribeImages in a manual sleep loop:** Use `ec2.NewImageAvailableWaiter` — it handles backoff and timeout correctly.
- **Pagination loop on DescribeImages:** Not needed — it is not a paginated API unlike DescribeInstances.
- **Assuming CopyImage tags are inherited:** Tags are per-region. After `CopyImage`, must re-tag the new AMI in the destination region using `CreateTags`.
- **Using ec2.New (SDK v1 style):** The project uses `aws-sdk-go-v2`. Always `ec2.NewFromConfig(awsCfg)` with region override for cross-region ops.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Poll until AMI available | Manual `for` loop with `time.Sleep` | `ec2.NewImageAvailableWaiter` | Built into SDK, handles backoff and error states |
| Delete snapshots on AMI removal | Separate `DeleteSnapshot` calls per snapshot | `DeregisterImage` with `DeleteAssociatedSnapshots: true` | Atomic, handles shared-snapshot case, fewer API calls |
| Duration string parsing for `--age` filter | Custom time parser | `time.ParseDuration` + `time.Since` pattern | `--age 7d` can be `7 * 24 * time.Hour`; use `olebedev/when` if calendar semantics needed (already in go.mod) |
| Profile refcount scan YAML parser | Regex on file text | `profile.Parse()` from `pkg/profile` package | Handles all YAML dialects, comments, indentation |
| Tabwriter column formatting | Custom string builder | `text/tabwriter` (same as `km list`) | Already used in `list.go` and `email.go` |

**Key insight:** AWS SDK v2 at v1.296.0 has first-class support for AMI lifecycle management including the waiter pattern (`ImageAvailableWaiter`, `ImageExistsWaiter`) — no custom polling infrastructure needed.

---

## Common Pitfalls

### Pitfall 1: AMI Naming Constraints
**What goes wrong:** `CreateImage` fails with `InvalidAMIName.InvalidFormat` if the name contains characters outside the allowed set.
**Why it happens:** AWS Name field: 3-128 chars, only alphanumeric + `()[]/ .-_'@`. Colons are NOT allowed.
**How to avoid:** Sanitize the profile name and sandbox-id before constructing the AMI name. Use hyphens. Format: `km-{sanitized-profile}-{sandbox-id}-{YYYYMMDDHHMMSS}`. Sanitize with `regexp.MustCompile(`[^a-zA-Z0-9._/-]`).ReplaceAllString(s, "-")`.
**Warning signs:** Error message contains `InvalidAMIName`.

### Pitfall 2: CreateImage Returns Before Snapshot Completes
**What goes wrong:** `CreateImage` returns an AMI ID immediately; the AMI is in `pending` state. Attempting to use it (launch instances, copy) fails with `InvalidAMIID.Unavailable`.
**Why it happens:** Snapshot creation is asynchronous. The API is non-blocking by design.
**How to avoid:** Always wait with `ec2.NewImageAvailableWaiter` before returning the AMI ID to the caller. For the learn-mode flow, this is acceptable because the user sees progress output while waiting.
**Warning signs:** Any operation on the AMI ID immediately after `CreateImage` returns `InvalidAMIID.Unavailable`.

### Pitfall 3: CopyImage Tags Not Inherited
**What goes wrong:** `km ami copy ami-xxx --to-region eu-west-1` creates an untagged AMI in the destination region.
**Why it happens:** AWS `CopyImage` does NOT copy tags from the source AMI. Tags are region-scoped metadata.
**How to avoid:** After `CopyImage` returns and the new AMI is `available`, call `CreateTags` in the destination region with the same tag set. Requires a separate EC2 client for the destination region: `ec2.NewFromConfig(awsCfg, func(o *ec2.Options){ o.Region = destRegion })`.
**Warning signs:** `km ami list --all-regions` shows AMIs without `km:sandbox-id` tag in the destination region.

### Pitfall 4: Shared Snapshot Silent Skip on Delete
**What goes wrong:** Operator deletes AMI but a snapshot that was shared (e.g., registered under multiple AMIs) is not deleted even though `DeleteAssociatedSnapshots: true` was set.
**Why it happens:** AWS docs: "if a snapshot is associated with multiple AMIs, it won't be deleted even if specified for deletion, although the AMI will still be deregistered." This is intentional safety behavior.
**How to avoid:** Document this behavior in `km ami delete` output ("N snapshots deleted, M skipped (shared)"). Do not treat this as an error.
**Warning signs:** Snapshot count in `--dry-run` output doesn't match actual deletions.

### Pitfall 5: Profile Scan False Negatives on AMI Refcount
**What goes wrong:** `km ami list` shows refcount=0 for an AMI that is referenced in a profile not under `profiles/`.
**Why it happens:** Profile scan is scoped to `profiles/*.yaml`. Operators may have custom profiles elsewhere.
**How to avoid:** Use `cfg.ProfileSearchPaths` (from `config.go`) to walk the configured search paths, not hardcoded `profiles/`.  Check `cfg.ProfileSearchPaths` which defaults to `["./profiles", "~/.km/profiles"]`.
**Warning signs:** Operator reports AMI is "in use" but tool shows refcount=0.

### Pitfall 6: IAM Permission Gap for Operator Role
**What goes wrong:** `km ami delete` or `km ami list` fails with `UnauthorizedOperation` even though the operator has `ec2:CreateImage`.
**Why it happens:** The SCP grants `ec2:CreateImage` and `ec2:CopyImage` to the AWSReservedSSO role (listed in `trustedBase`), but `ec2:DescribeImages`, `ec2:DeregisterImage`, and `ec2:DeleteSnapshot` are NOT in the SCP deny list (they are Describe/Delete operations, not Create/Copy) — however, the IAM role for the operator/klanker-terraform profile must also explicitly allow them. `ec2:DescribeImages` exists in Lambda roles but the operator-facing IAM policy in `bootstrap.go` doesn't explicitly document these.
**How to avoid:** Phase 56 MUST add `ec2:DescribeImages`, `ec2:DeregisterImage`, `ec2:DeleteSnapshot`, `ec2:CreateTags`, and `ec2:DescribeSnapshots` to the operator role guidance in `bootstrap.go`. These are not blocked by the SCP (they're not in `DenyInfraAndStorage`) but must be affirmatively allowed.
**Warning signs:** `UnauthorizedOperation` on `DescribeImages` or `DeregisterImage`.

### Pitfall 7: `DescribeImages` With No Owner Filter Is Very Slow
**What goes wrong:** `km ami list` takes 30+ seconds or returns thousands of results.
**Why it happens:** Without `Owners: []string{"self"}`, `DescribeImages` searches the entire AMI catalog.
**How to avoid:** Always pass `Owners: []string{"self"}` and the `km:sandbox-id` tag filter. Already covered in Pattern 6.

---

## Code Examples

### Bake AMI Function Signature
```go
// pkg/aws/ec2_ami.go

// BakeAMI creates an AMI from a running EC2 instance with km tags.
// Uses NoReboot=true (live snapshot). Waits until the AMI is available.
// Returns the new AMI ID or error.
func BakeAMI(ctx context.Context, client EC2AMIAPI, instanceID, amiName, description string, tags []types.Tag) (string, error) {
    tagSpecs := []types.TagSpecification{
        {ResourceType: types.ResourceTypeImage,    Tags: tags},
        {ResourceType: types.ResourceTypeSnapshot, Tags: tags},
    }
    out, err := client.CreateImage(ctx, &ec2.CreateImageInput{
        InstanceId:        aws.String(instanceID),
        Name:              aws.String(amiName),
        Description:       aws.String(description),
        NoReboot:          aws.Bool(true),
        TagSpecifications: tagSpecs,
    })
    if err != nil {
        return "", fmt.Errorf("create image: %w", err)
    }
    amiID := aws.ToString(out.ImageId)

    fmt.Fprintf(os.Stderr, "[ami] snapshot started: %s (waiting for available state...)\n", amiID)
    waiter := ec2.NewImageAvailableWaiter(client.(ec2.DescribeImagesAPIClient))
    if err := waiter.Wait(ctx, &ec2.DescribeImagesInput{ImageIds: []string{amiID}}, 15*time.Minute); err != nil {
        return amiID, fmt.Errorf("AMI %s did not become available within 15 minutes: %w", amiID, err)
    }
    return amiID, nil
}
```

### Standard AMI Tag Set
```go
// pkg/aws/ec2_ami.go — function to build standard km tags for baked AMIs

// KMBakeTags returns the standard tag set for a km-baked AMI and its snapshots.
func KMBakeTags(sandboxID, profileName, alias, instanceType, region string) []types.Tag {
    return []types.Tag{
        {Key: aws.String("km:sandbox-id"),      Value: aws.String(sandboxID)},
        {Key: aws.String("km:profile"),          Value: aws.String(profileName)},
        {Key: aws.String("km:alias"),            Value: aws.String(alias)},
        {Key: aws.String("km:baked-at"),         Value: aws.String(time.Now().UTC().Format(time.RFC3339))},
        {Key: aws.String("km:source-region"),    Value: aws.String(region)},
        {Key: aws.String("km:instance-type"),    Value: aws.String(instanceType)},
        {Key: aws.String("Name"),                Value: aws.String(fmt.Sprintf("km-%s-%s", sanitizeName(profileName), sandboxID))},
    }
}
```

### AMI Name Format
```go
// AMI name: km-{sanitized-profile}-{sandbox-id}-{YYYYMMDDHHMMSS}
// Max 128 chars; only alphanumeric + ()[]/ .-_'@ allowed

func amiName(profileName, sandboxID string, t time.Time) string {
    safe := regexp.MustCompile(`[^a-zA-Z0-9._/ -]`).ReplaceAllString(profileName, "-")
    return fmt.Sprintf("km-%s-%s-%s", safe, sandboxID, t.UTC().Format("20060102150405"))
}
```

### Doctor checkStaleAMIs Shape
```go
// internal/app/cmd/doctor.go — new check following checkStaleKMSKeys structure

func checkStaleAMIs(ctx context.Context, amiClient EC2AMIAPI, lister SandboxLister, profilesDir string, staleDays int, dryRun bool) CheckResult {
    name := "Stale AMIs"
    if amiClient == nil {
        return CheckResult{Name: name, Status: CheckSkipped, Message: "EC2 AMI client not available"}
    }

    // List all self-owned km-tagged AMIs
    out, err := amiClient.DescribeImages(ctx, &ec2.DescribeImagesInput{
        Owners: []string{"self"},
        Filters: []types.Filter{
            {Name: aws.String("tag:km:sandbox-id"), Values: []string{"*"}},
        },
    })
    if err != nil {
        return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list AMIs: %v", err)}
    }

    // Build referenced AMI set from profile scan + running sandboxes
    // ... (pattern: checkStaleKMSKeys lines 805-839)

    // Flag AMIs older than staleDays not in either active set
    threshold := time.Now().AddDate(0, 0, -staleDays)
    // ... report stale AMIs; no delete action in Phase 56
}
```

### Multi-Region Parallel Pattern (for --all-regions)
```go
// Source: doctor.go:1812-1828 per-region EC2 client construction pattern

// For km ami list --all-regions: construct one ec2 client per region and run in parallel.
// Regions come from cfg.PrimaryRegion + KM_REPLICA_REGION env (same as doctor.go).

func listAMIsInRegions(ctx context.Context, awsCfg aws.Config, regions []string) []amiEntry {
    var mu sync.Mutex
    var wg sync.WaitGroup
    var all []amiEntry
    for _, region := range regions {
        wg.Add(1)
        go func(r string) {
            defer wg.Done()
            regionCfg := awsCfg.Copy()
            regionCfg.Region = r
            client := ec2.NewFromConfig(regionCfg)
            entries := listAMIsInRegion(ctx, client, r)
            mu.Lock()
            all = append(all, entries...)
            mu.Unlock()
        }(region)
    }
    wg.Wait()
    return all
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Separate `CreateTags` after `CreateImage` | `TagSpecifications` in `CreateImageInput` | ~2020 (AWS API update) | Single call atomically tags image + snapshots |
| Manual `DeleteSnapshot` loop after `DeregisterImage` | `DeleteAssociatedSnapshots: true` on `DeregisterImageInput` | Recent SDK addition | Single call deregisters and cleans up |
| Polling DescribeImages manually | `ec2.NewImageAvailableWaiter` | aws-sdk-go-v2 from the start | SDK handles backoff, timeout, error states |

**Deprecated/outdated:**
- `aws-sdk-go` (v1): project uses `aws-sdk-go-v2`. All patterns here use v2 only.
- Separate `DeleteSnapshot` calls: use `DeleteAssociatedSnapshots: true` instead.

---

## IAM Requirements Analysis

### Confirmed Present (no action needed)
| Permission | Where | Notes |
|------------|-------|-------|
| `ec2:CreateImage` | SCP `DenyInfraAndStorage` trusted-base exemption (AWSReservedSSO) | Operator SSO role is in trustedBase |
| `ec2:CopyImage` | SCP `DenyInfraAndStorage` trusted-base exemption | Same |
| `ec2:DescribeImages` | `create-handler` Lambda role; `ttl-handler` Lambda role | Present in Lambda roles, NOT blocked by SCP |

### Gaps Requiring Addition in Phase 56
| Permission | Needed By | Notes |
|------------|-----------|-------|
| `ec2:DescribeImages` | `km ami list`, `km ami delete`, `checkStaleAMIs` | Not in SCP deny list; must be in operator IAM allow policy |
| `ec2:DeregisterImage` | `km ami delete` | Not in SCP deny list; must be explicitly allowed |
| `ec2:DeleteSnapshot` | `km ami delete` (fallback path) | Not in SCP deny list; needed if `DeleteAssociatedSnapshots` returns partial failure |
| `ec2:CreateTags` | `km ami copy` (re-tag in dest region) | Already in `create-handler` as `ec2:CreateTags` |
| `ec2:DescribeSnapshots` | `km ami list --wide` (snapshot count) | Read-only; not in SCP deny list |

**Action:** `bootstrap.go` operator guidance section must document these permissions. If the operator uses `klanker-terraform` profile (which assumes into km-provisioner role), the `km-provisioner` Terraform module needs these added. The SCP does NOT block any of these operations for the AWSReservedSSO role.

---

## Phase 33 Slug Verification in ca-central-1

**Background:** Phase 33 verification report lists "human-verification item #2: AMI slug resolution in a non-use1 region." Phase 56 closes this.

**Approach:** The `ami_filters` locals map in `infra/modules/ec2spot/v1.0.0/main.tf` (lines 16-30) uses `amazon` owner for AL2023 and Canonical account `099720109477` for Ubuntu. Both owners publish to all commercial regions including `ca-central-1`. No code changes expected; this is a verification task.

**Test plan:** Run `terraform plan` against a `ca-central-1` sandbox profile with `ami: amazon-linux-2023` and confirm `data.aws_ami.base_ami` resolves a non-empty image ID. Document result in Phase 56 VERIFICATION.md as human-verification item (requires live AWS access).

**Confidence:** HIGH — AWS publishes AL2023 and Ubuntu AMIs in all commercial regions; the filter patterns are valid globally.

---

## Validation Architecture

Nyquist validation is enabled (`workflow.nyquist_validation: true` in `.planning/config.json`).

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go standard `testing` package + `testify` v1.11.1 |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./pkg/aws/... ./internal/app/cmd/... -run "TestAMI\|TestBakeAMI\|TestCheckStaleAMI\|TestListAMI\|TestDeleteAMI" -count=1` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| P56-01 | `--ami` flag wires bakeAMI before flush in runLearnPostExit | unit | `go test ./internal/app/cmd/... -run TestLearnPostExit_AMIFlag -count=1` | ❌ Wave 0 |
| P56-02 | CreateImage TagSpecifications includes image + snapshot resource types | unit | `go test ./pkg/aws/... -run TestBakeAMI_TagSpecifications -count=1` | ❌ Wave 0 |
| P56-03 | `km ami bake` calls same bakeAMI function as --learn --ami path | unit | `go test ./internal/app/cmd/... -run TestAMIBakeCmd -count=1` | ❌ Wave 0 |
| P56-04 | `km ami list` narrow output columns: ID, Name, Age, Size, Source, Refcount | unit | `go test ./internal/app/cmd/... -run TestAMIList_NarrowOutput -count=1` | ❌ Wave 0 |
| P56-05 | `km ami list --unused` returns only AMIs not in any profile and not backing running sandbox | unit | `go test ./internal/app/cmd/... -run TestAMIList_UnusedFilter -count=1` | ❌ Wave 0 |
| P56-06 | `km ami delete` refuses when AMI referenced in profile unless --force | unit | `go test ./internal/app/cmd/... -run TestAMIDelete_ProfileRefCheck -count=1` | ❌ Wave 0 |
| P56-07 | `km ami copy` re-tags AMI in destination region | unit | `go test ./internal/app/cmd/... -run TestAMICopy_RetaggersDestination -count=1` | ❌ Wave 0 |
| P56-08 | checkStaleAMIs returns CheckOK when no stale AMIs exist | unit | `go test ./internal/app/cmd/... -run TestCheckStaleAMIs_AllActive -count=1` | ❌ Wave 0 |
| P56-08 | checkStaleAMIs returns CheckWarn with list when stale AMIs found | unit | `go test ./internal/app/cmd/... -run TestCheckStaleAMIs_StaleFound -count=1` | ❌ Wave 0 |
| P56-08 | checkStaleAMIs skips AMIs referenced by profiles | unit | `go test ./internal/app/cmd/... -run TestCheckStaleAMIs_SkipsProfileRef -count=1` | ❌ Wave 0 |
| P56-09 | Config.DoctorStaleAMIDays defaults to 30 | unit | `go test ./internal/app/config/... -run TestConfig_DoctorStaleAMIDays -count=1` | ❌ Wave 0 |
| P56-11 | Generate() emits spec.runtime.ami when AMI ID is set on Recorder | unit | `go test ./pkg/allowlistgen/... -run TestGenerate_WithAMI -count=1` | ❌ Wave 0 |
| P56-12 | Phase 33 slug verification ca-central-1 | manual | N/A — requires live AWS + terraform | N/A |

### Mock Pattern for AMI Tests

Follow `doctor_test.go` interface-mock pattern:

```go
// internal/app/cmd/ami_test.go (new file)

type mockEC2AMIClient struct {
    createOut  *ec2.CreateImageOutput
    createErr  error
    describeOut *ec2.DescribeImagesOutput
    describeErr error
    deregisterErr error
    copyOut    *ec2.CopyImageOutput
    copyErr    error
}

func (m *mockEC2AMIClient) CreateImage(ctx context.Context, params *ec2.CreateImageInput, optFns ...func(*ec2.Options)) (*ec2.CreateImageOutput, error) {
    return m.createOut, m.createErr
}
// ... etc

var _ EC2AMIAPI = (*mockEC2AMIClient)(nil)  // compile-time interface check
```

**Note:** `ec2.NewImageAvailableWaiter` requires a `DescribeImagesAPIClient` interface, not `EC2AMIAPI`. Tests should bypass the waiter by injecting a mock that returns `available` state immediately from `DescribeImages`. Or use `ec2.NewImageAvailableWaiter` with a mock that satisfies `DescribeImagesAPIClient`.

### Sampling Rate
- **Per task commit:** `go test ./pkg/aws/... ./internal/app/cmd/... -run "TestAMI\|TestBakeAMI\|TestCheckStaleAMI" -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/aws/ec2_ami_test.go` — mock interface and BakeAMI unit tests (P56-01, P56-02)
- [ ] `internal/app/cmd/ami_test.go` — mockEC2AMIClient + list/delete/bake/copy command tests (P56-03 to P56-07)
- [ ] `internal/app/cmd/doctor_test.go` additions — mockEC2AMIClient extension + checkStaleAMIs tests (P56-08)
- [ ] `internal/app/config/config_test.go` additions — DoctorStaleAMIDays default test (P56-09)
- [ ] `pkg/allowlistgen/generator_test.go` additions — TestGenerate_WithAMI (P56-11)

---

## Open Questions

1. **`EC2AMIAPI` vs `DescribeImagesAPIClient` for waiter**
   - What we know: `ec2.NewImageAvailableWaiter` requires `DescribeImagesAPIClient`, which is a generated interface with just `DescribeImages`. Our `EC2AMIAPI` also has `DescribeImages`, but the Go type system requires explicit interface satisfaction.
   - What's unclear: Whether to embed `DescribeImagesAPIClient` in `EC2AMIAPI` or cast at call site.
   - Recommendation: `EC2AMIAPI` should embed or be a superset of `DescribeImagesAPIClient` so the client can be passed to `NewImageAvailableWaiter` directly. Alternatively, define `EC2AMIAPI` and use a wrapper: `waiter := ec2.NewImageAvailableWaiter(ec2.DescribeImagesAPIClient(client))` — requires a type assertion or struct wrapping.

2. **`km ami list` In-use refcount definition**
   - What we know: CONTEXT.md says narrow column = "In-use refcount". Doctor "unused" definition requires BOTH no profile ref AND no running sandbox. The refcount shown in `km ami list` should be the count of profile references + running sandbox count.
   - What's unclear: Should `refcount` show "profile refs + running sandbox uses" or just one?
   - Recommendation: Refcount = (count of profiles referencing the AMI) + (count of running sandboxes using the AMI as their source — resolvable from DynamoDB records if sandbox metadata includes the AMI ID used at create time). For Phase 56, keeping it simple: profile reference count only (running sandbox check is the DynamoDB scan). The `--unused` filter applies the strict dual-condition. The narrow refcount column is the profile count.

3. **`km-config.yaml` YAML key path for `doctor.staleAMIDays`**
   - What we know: Viper maps nested keys with dots. `doctor.stale_ami_days` would read from `doctor:` block in YAML.
   - What's unclear: Existing config keys use snake_case flat structure (e.g., `sandbox_table_name`), not nested blocks. Using `doctor.stale_ami_days` introduces the first nested config key.
   - Recommendation: Keep flat: add `doctor_stale_ami_days: 30` to `km-config.yaml`. Map as `v.SetDefault("doctor_stale_ami_days", 30)` and `cfg.DoctorStaleAMIDays = v.GetInt("doctor_stale_ami_days")`. Consistent with existing flat config structure.

4. **Phase 33 slug test in ca-central-1: terraform plan vs unit test**
   - What we know: The `ami_filters` map uses `amazon` and `099720109477` as owners — both publish to all commercial regions. No code change is expected.
   - What's unclear: Whether `terraform plan` is feasible in CI for ca-central-1 (heavyweight) vs a Go unit test using the compiler.
   - Recommendation: Document as a human-verification item in VERIFICATION.md (same pattern as Phase 55's EC2 end-to-end test). The compiler emits `ami_slug` and the Terraform data source does the lookup — there's no Go code to unit-test for this specific verification. The manual step is: create a ca-central-1 sandbox with an AL2023 slug and confirm it boots.

---

## Sources

### Primary (HIGH confidence)
- `aws-sdk-go-v2/service/ec2@v1.296.0/api_op_CreateImage.go` — CreateImageInput.TagSpecifications, NoReboot semantics, output ImageId
- `aws-sdk-go-v2/service/ec2@v1.296.0/api_op_DeregisterImage.go` — DeregisterImageInput.DeleteAssociatedSnapshots; shared-snapshot skip behavior documented in SDK comments
- `aws-sdk-go-v2/service/ec2@v1.296.0/api_op_DescribeImages.go` — ImageAvailableWaiter, ImageExistsWaiter, non-pagination behavior, Owners filter
- `aws-sdk-go-v2/service/ec2@v1.296.0/api_op_CopyImage.go` — CopyImageOutput.ImageId; no tag inheritance
- `aws-sdk-go-v2/service/ec2@v1.296.0/types/types.go` — Image struct fields (State, CreationDate, Tags, BlockDeviceMappings, SourceInstanceId); EbsBlockDevice.SnapshotId; ImageState enum
- `internal/app/cmd/doctor.go` — checkStaleKMSKeys (line 780), checkStaleIAMRoles (line 896), checkOrphanedEC2 (line 1145), runChecks (line 1261), DoctorDeps pattern, per-region EC2 client construction (lines 1812-1828)
- `internal/app/cmd/shell.go` — runLearnPostExit (line 589), flushEC2Observations (line 688), DefaultLearnFilename (line 498), learnObservedState struct (line 480)
- `pkg/allowlistgen/generator.go` — Generate() structure (line 30), InitCommands injection pattern (lines 116-120), GenerateAnnotatedYAML (line 142)
- `internal/app/config/config.go` — Config struct, Load() defaults pattern, Viper flat key convention
- `internal/app/cmd/bootstrap.go` — trustedBase (line 325), DenyInfraAndStorage SCP (line 376-389), CreateImage/CopyImage in trusted exemption
- `infra/modules/ec2spot/v1.0.0/main.tf` — ami_filters locals map (lines 16-30), ca-central-1 applicability
- `internal/app/cmd/doctor_test.go` — mockXxx interface pattern, var _ Interface = (*mockXxx)(nil) compile check

### Secondary (MEDIUM confidence)
- `infra/modules/create-handler/v1.0.0/main.tf` line 206: `ec2:DescribeImages` in Lambda role — confirms the permission exists in the system but not the operator role
- Phase 33.1 VERIFICATION.md: P33.1-07 confirms raw AMI ID flows end-to-end through schema → compiler → HCL → Terraform — the same data path Phase 56 uses to populate `spec.runtime.ami`
- Phase 55 VERIFICATION.md: LEARN-CMD-01 through LEARN-CMD-07 — confirms how `initCommands` was added to `Generate()`; same injection pattern for `AMI` field

### Tertiary (LOW confidence)
- AWS documentation statement that `DescribeImages` is not paginated — verified in SDK code (no NextToken in output type) which is HIGH confidence; the original AWS docs claim is tertiary but SDK code confirms it

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all SDK operations verified from Go module cache at pinned version v1.296.0
- Architecture: HIGH — patterns verified against existing working code in project
- Pitfalls: HIGH — pitfalls 1-4 verified from SDK docs and source; pitfalls 5-7 verified from codebase IAM analysis
- Validation architecture: HIGH — test framework and mock pattern directly from doctor_test.go

**Research date:** 2026-04-26
**Valid until:** 2026-05-26 (AWS EC2 API is stable; SDK version is pinned)
