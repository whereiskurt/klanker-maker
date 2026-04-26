# Phase 56: Learn mode AMI snapshot and lifecycle management - Context

**Gathered:** 2026-04-26
**Status:** Ready for planning

<domain>
## Phase Boundary

Add AMI snapshot + lifecycle commands so an operator can bake the state of a learn-mode (or regular) sandbox into a private AMI, write its ID into a generated profile, and manage the resulting AMI inventory.

**In scope:**
- `--ami` flag on `km shell --learn` — snapshot EC2 on exit, write AMI ID into generated profile
- `km ami bake <sandbox-id>` — peer command to bake from a non-learn sandbox
- `km ami list` — show custom AMIs (narrow default + `--wide`)
- `km ami delete <ami-id>` — safe deletion with profile refcount check + auto-snapshot cleanup
- `km ami copy <ami-id> --to-region <r>` — explicit cross-region replication
- `km doctor` extension — stale/unused AMI check
- Verification that Phase 33 slug AMI resolution works in a non-use1 region (e.g., ca-central-1) — closes Phase 33's open human-verification item

**Out of scope (deferred to other phases / future work):**
- Auto-cross-region copy on snapshot
- AMI sharing across AWS accounts
- AMI version chaining (e.g., `km ami rebake` from existing AMI)
- Multi-region platform deployment (regional Terragrunt directories — separate phase, "Phase 62-ish")
- Time-based usage tracking via CloudTrail

</domain>

<decisions>
## Implementation Decisions

### Snapshot timing & content
- **Timing:** snapshot fires *before* the SIGUSR1 flush in `runLearnPostExit` (`shell.go:589`). Cleanest because the AMI captures the state the operator just shaped; flush runs after against a stable still-running instance.
- **Content:** root volume + `additionalVolume` (e.g., `/data`). EFS is **always excluded** (shared filesystem, not snapshotted).
- **Instance state during snapshot:** **live snapshot with `NoReboot=true`** via AWS `CreateImage`. Instance keeps running; learn-mode workloads aren't write-heavy enough to need a stop/reboot for filesystem coherence. Operator can `km destroy` after at their leisure.
- **Bake scope:** ship `km ami bake <sandbox-id>` as a **peer command** alongside `--learn --ami`. Same code path; lets operators promote any well-shaped sandbox to an AMI.

### AMI lifecycle policy — `km ami delete`
- **Default safety:** refuse if any profile in `profiles/*.yaml` references the AMI ID in `spec.runtime.ami`. Print the list of referencing profiles. `--force` overrides.
- **Snapshot cleanup:** auto-delete the underlying EBS snapshots when deregistering the AMI. Operator intent is "remove this AMI" — orphaned snapshots leak cost.
- **Confirmation:** prompt before delete; `--yes` skips.

### AMI lifecycle policy — `km doctor` stale check
- **Threshold:** configurable in `km-config.yaml` as `doctor.staleAMIDays`, **default 30 days**.
- **"Unused" definition:** strict — AMI must be **(a) not referenced by any local profile in `profiles/`** AND **(b) not actively backing any running sandbox**. Both conditions required to flag as stale.
- **Action:** doctor flags only (no auto-delete). Operator runs `km ami delete` after review. (Auto-cleanup `--apply` mode can be added later if needed.)

### Multi-region behavior
- **`km ami list` default scope:** current `KM_REGION` only. `--all-regions` flag walks all configured regions in parallel. Single-region targeted queries via `--region <r>`.
- **Cross-region copy on snapshot:** **NO auto-copy.** Snapshot stays in the source region; the generated profile records that region in `spec.runtime.region`. Phase 33.1's raw-ID schema already pairs an AMI with its region.
- **`km ami copy <ami-id> --to-region <r>`:** explicit peer command for cross-region replication. Wraps `ec2:CopyImage` (already in IAM per `bootstrap.go:386`).
- **`km doctor` stale check default scope:** current `KM_REGION` only, with `--all-regions` opt-in. Mirrors `km ami list` for consistency.
- **Phase 33 slug verification task:** add a Phase 56 task that runs `terraform plan` (or equivalent compiler test) against `ca-central-1` with `ami: amazon-linux-2023` and confirms `data.aws_ami` resolves the canonical AL2023 image for that region. Closes Phase 33's open human-verification item #2.

### `km ami list` output & filtering
- **Columns (narrow default):** ID, Name, Age, Size (GB), Source profile, In-use refcount (6 columns; readable in 80-col terminals).
- **Columns (`--wide`):** add source sandbox-id, region, snapshot count, encrypted (bool), instance type at bake, **estimated $/month** (`size_gb × $0.05` hardcoded approximation; ballpark hint, not a bill).
- **Default sort:** newest first (creation date descending). Matches `km list` and console conventions.
- **Filtering flags (all four ship in Phase 56):**
  - `--profile <name>` — match source-profile tag
  - `--age <duration>` — e.g., `--age 7d`, parses Go-style durations
  - `--unused` — refcount=0 + no profile reference (same definition `km doctor` uses)
  - `--region <r>` — single-region targeted query (complements `--all-regions`)

### Claude's Discretion
- **AMI `Name` field format** — planner picks a sensible scheme (suggest: `km-{profile-name}-{sandbox-id}-{YYYYMMDDHHMMSS}`).
- **AMI tag schema** — must include sandbox-id, profile, alias, date (per ROADMAP). Planner adds others as useful (source-region, source-instance-type, baked-from-ami, km-version).
- **Snapshot-failure mid-flow behavior** — fail the learn flow or warn-and-continue. Suggest: log the error, write the generated profile *without* the `ami:` field, exit non-zero.
- **Dry-run / preview** — whether `km ami delete --dry-run` shows what would be deleted. Suggest: yes, low cost; pairs with doctor output.
- **Progress reporting** during snapshot — `km doctor` style spinner vs polling EC2 image-state until `available`. Suggest: poll with timestamped log lines.
- **Error message tone & exit codes** — follow existing `km` conventions.
- **Encryption inheritance** — AWS `CreateImage` inherits root-volume encryption automatically; no operator-facing flag needed unless a use case emerges.

</decisions>

<specifics>
## Specific Ideas

- "AMI baking should feel like a natural extension of `km shell --learn`" — operator goes from "I just shaped this sandbox the way I want" to "now bake it" with one flag, no mode switch.
- **`initCommands` (Phase 55) double as documentation** of what's baked into the AMI. Must remain in the generated profile even when an `ami:` is set, because they serve as the fallback for AMI-less regions.
- **Profile generation flow:** existing `learned.<sandbox-id>.YYYYMMDDHHMMSS.yaml` is the artifact to extend. The new `ami:` line goes into `spec.runtime.ami`; the rest of the profile (DNS suffixes, hosts, initCommands) is unchanged from Phase 31/55.
- **Cost hygiene matters.** Orphaned EBS snapshots are the silent killer; auto-cleanup on `km ami delete` and the doctor stale-check are the two main defenses.

</specifics>

<code_context>
## Existing Code Insights

### Reusable Assets
- **`runLearnPostExit` at `internal/app/cmd/shell.go:589`** — the post-shell-exit hook where snapshot logic plugs in. Snapshot fires before line 619 (`flushEC2Observations` SIGUSR1 trigger).
- **`flushEC2Observations` at `shell.go:688`** — already does SSM SendCommand against the instance; pattern for any post-snapshot bookkeeping.
- **`DefaultLearnFilename` at `shell.go:490-503`** — `learned.<sandbox-id>.<timestamp>.yaml` filename pattern; the generated profile gets the new `ami:` line written to it.
- **`runChecks` at `internal/app/cmd/doctor.go:1261`** — parallel check-runner; new `checkStaleAMIs` follows the `checkStaleKMSKeys` (`doctor.go:780`) / `checkStaleIAMRoles` (`doctor.go:896`) / `checkOrphanedEC2` (`doctor.go:1145`) patterns exactly.
- **`bootstrap.go:386`** — IAM policy already includes `ec2:CreateImage`, `ec2:CopyImage`, `ec2:ExportImage`. No bootstrap changes needed.
- **`pkg/aws/`** — region-aware AWS client helpers; existing pattern for parallel multi-region queries (`km doctor`).

### Established Patterns
- **CLI verb-noun-subverb:** `km ami list/delete/bake/copy` mirrors `km at list/cancel`, `km email send/read`. Cobra subcommand structure is consistent across the codebase.
- **Narrow-vs-wide list output:** `km list` (no flag = 5 cols, `--wide` = all) is the convention to follow.
- **Doctor parallel checks:** `runChecks` fan-out at `doctor.go:1261`; each `checkXxx` returns `CheckResult` independently; results filtered/formatted at `formatCheckLine` (`doctor.go:1350`).
- **Stale resource pattern:** `checkStaleKMSKeys`/`checkStaleIAMRoles`/`checkStaleSchedules`/`checkOrphanedEC2` all follow same shape: lister → AWS query → match against active set → return delta. `checkStaleAMIs` plugs in here.
- **Region resolution:** `KM_REGION` env var driven by `cfg.PrimaryRegion` (`internal/app/cmd/create.go:333`); same pattern for any region-scoped command.
- **Schema config:** `km-config.yaml` is an existing operator-side config file; adding `doctor.staleAMIDays` follows existing pattern.

### Integration Points
- **`internal/app/cmd/shell.go`** — `--ami` flag + post-exit snapshot integration in `runLearnPostExit`.
- **`internal/app/cmd/ami.go` (new)** — Cobra command tree for `km ami list/delete/bake/copy`.
- **`internal/app/cmd/doctor.go`** — `checkStaleAMIs` registered in the check list.
- **`pkg/aws/ec2.go` (existing or new helper file)** — `CreateImage`, `DescribeImages`, `DeregisterImage`, `DeleteSnapshot`, `CopyImage` SDK wrappers.
- **`pkg/profile/`** — generated profile writer extends to include the resolved `ami: ami-xxxxxxxx` line in `spec.runtime.ami` (Phase 33.1's raw-ID path is the consumer).
- **`pkg/config/`** — add `Doctor.StaleAMIDays int` to config struct; default 30.

</code_context>

<deferred>
## Deferred Ideas

- **Auto-cross-region copy on snapshot** — operators wanting same image in 3 regions must bake 3 times for now; `km ami copy` is the manual path. Revisit if the friction shows up.
- **Multi-region platform deployment** — regional Terragrunt directories for ca-central-1, ap-southeast-1, etc. Separate phase ("Phase 62-ish"). Phase 56 only verifies AMI slug logic works there; doesn't deploy the platform.
- **Auto-cleanup mode for `km doctor --apply`** — flag-then-delete in one command. Phase 56 ships flag-only; revisit if operators ask.
- **AMI version chaining / rebake** — `km ami rebake <ami-id>` to create a new AMI from a new sandbox started from the existing one. Future phase.
- **AMI sharing across AWS accounts** — useful for team setups but adds significant scope.
- **Time-based usage tracking** — "AMI not used to launch instances in N days" via CloudTrail. Out of scope; the static "no profile reference + no running sandbox" definition is sufficient.
- **AMI cost dashboard / `km ami cost` command** — Phase 56 surfaces cost in `--wide` output; a dedicated cost rollup command could come later.

</deferred>

---

*Phase: 56-learn-mode-ami-snapshot-and-lifecycle-management*
*Context gathered: 2026-04-26*
