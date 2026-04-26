---
phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
plan: 06
subsystem: cmd
tags: [doctor, ami, ec2, lifecycle, stale-resources, testing, go]

# Dependency graph
requires:
  - phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
    plan: 01
    provides: pkg/aws/ec2_ami.go — EC2AMIAPI, ListBakedAMIs
  - phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
    plan: 02
    provides: Config.DoctorStaleAMIDays int field with viper default 30
  - phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
    plan: 04
    provides: FindProfilesReferencingAMI exported from internal/app/cmd/ami.go
provides:
  - checkStaleAMIs function in doctor.go — flags stale custom AMIs per region
  - DoctorDeps.EC2AMIClients map[string]kmaws.EC2AMIAPI — per-region AMI client map
  - DoctorDeps.AllRegions bool — wired from --all-regions Cobra flag
  - DoctorConfigProvider.GetDoctorStaleAMIDays() int — threshold accessor
  - DoctorConfigProvider.GetProfileSearchPaths() []string — search-path accessor
  - --all-regions flag on km doctor — expands regional scope to primary + KM_REPLICA_REGION CSV
  - sandboxUsesAMIInDoctor helper — resolves sandbox profile name to file, checks spec.runtime.ami
affects:
  - 56-VERIFICATION.md (manual stale-AMI smoke test row P56-08)
  - 56-CONTEXT.md (flag-only confirmed; no auto-delete in Phase 56)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "checkStaleAMIs follows checkStaleKMSKeys/checkStaleIAMRoles structural pattern: nil-skip, list, active-set filter, CheckOK/CheckWarn with region-scoped name"
    - "EC2AMIClients fan-out in buildChecks mirrors EC2Clients fan-out — one closure per region"
    - "initRealDepsWithExisting accepts pre-allocated deps to propagate AllRegions before client construction"
    - "sandboxUsesAMIInDoctor: case-insensitive base-name match via os.ReadDir + strings.EqualFold; tolerates parse errors silently"

key-files:
  created: []
  modified:
    - internal/app/cmd/doctor.go
    - internal/app/cmd/doctor_test.go
    - internal/app/cmd/shell_ami_test.go

key-decisions:
  - "checkStaleAMIs is flag-only (no deletion) per CONTEXT.md locked decision for Phase 56"
  - "'Unused' = NOT referenced by any profile in cfg.ProfileSearchPaths AND NOT backing any running sandbox (both required per CONTEXT.md)"
  - "Default scope is single-region (KM_REGION); --all-regions opt-in walks PrimaryRegion + KM_REPLICA_REGION CSV in parallel"
  - "sandboxUsesAMIInDoctor is profile-file-based: limitation documented in SUMMARY and code comments"
  - "initRealDepsWithExisting replaces direct initRealDeps call in runDoctor to propagate AllRegions before region list construction"

patterns-established:
  - "Pattern: DoctorConfigProvider gains threshold and search-path accessors for checkStaleAMIs — avoids global Config references in isolated check functions"
  - "Pattern: testConfig and testDoctorConfig both implement new interface methods with sensible defaults (30 days, nil paths)"

requirements-completed: [P56-08, P56-12]

# Metrics
duration: 30min
completed: 2026-04-26
---

# Phase 56 Plan 06: checkStaleAMIs + --all-regions flag Summary

**checkStaleAMIs added to km doctor: flags km-tagged AMIs older than 30 days that are unreferenced by any profile and not backing a running sandbox; --all-regions opt-in expands scope to all configured regions in parallel**

## Performance

- **Duration:** ~30 min
- **Started:** 2026-04-26T20:53:37Z
- **Completed:** 2026-04-26T21:25:00Z
- **Tasks:** 3
- **Files modified:** 3

## Accomplishments

- `checkStaleAMIs` registered in `buildChecks` fan-out alongside `checkStaleKMSKeys` / `checkStaleIAMRoles` / `checkOrphanedEC2`
- `DoctorDeps.EC2AMIClients map[string]kmaws.EC2AMIAPI` added for per-region AMI clients
- `DoctorConfigProvider` extended with `GetDoctorStaleAMIDays() int` and `GetProfileSearchPaths() []string`; `appConfigAdapter` implements both
- `--all-regions` boolean flag added to `km doctor`; wired through `runDoctor` → `DoctorDeps.AllRegions` → `initRealDepsWithExisting`
- `sandboxUsesAMIInDoctor` helper resolves sandbox profile name to YAML file via `os.ReadDir` + `strings.EqualFold` case-insensitive match; tolerates missing/unparsable files silently
- 11 new tests passing: 9 `TestCheckStaleAMIs_*` + 2 `TestDoctor_*` + 1 `TestDoctorCmd_AllRegionsFlagExists`; no regression to existing doctor tests
- Fixed pre-existing `shell_ami_test.go` compilation error (Plan 05 artifact; `ShellExecFunc` type mismatch — Rule 3 auto-fix)
- `make build` succeeds: km v0.1.400; binary includes `--all-regions` flag on `doctor` subcommand

## Public Surface

```go
// internal/app/cmd/doctor.go

// EC2AMIClients in DoctorDeps — per-region AMI client map
EC2AMIClients map[string]kmaws.EC2AMIAPI

// AllRegions in DoctorDeps — wired from --all-regions flag
AllRegions bool

// DoctorConfigProvider additions
GetDoctorStaleAMIDays() int
GetProfileSearchPaths() []string

// checkStaleAMIs — stale custom AMI detection (flag-only, Phase 56)
func checkStaleAMIs(ctx context.Context, region string, amiClient kmaws.EC2AMIAPI,
    lister SandboxLister, profileSearchPaths []string, staleDays int) CheckResult

// sandboxUsesAMIInDoctor — profile-file-based sandbox→AMI resolution
func sandboxUsesAMIInDoctor(sb kmaws.SandboxRecord, searchPaths []string, amiID string) bool
```

## Task Commits

1. **TDD RED — TestCheckStaleAMIs (failing)** — `d91ff55`
2. **Task 1: checkStaleAMIs + DoctorDeps + interface extensions (GREEN)** — `df96e3a`
3. **TDD RED — TestDoctor_AllRegions* (failing flag test)** — `a6afac4`
4. **Task 2: --all-regions flag + initRealDepsWithExisting (GREEN)** — `ed9d92b`
5. **Task 3 + SUMMARY** — (this commit)

## Files Created/Modified

- `internal/app/cmd/doctor.go` — DoctorConfigProvider (2 new methods), appConfigAdapter (2 new methods), DoctorDeps (EC2AMIClients + AllRegions), checkStaleAMIs, sandboxUsesAMIInDoctor, buildChecks fan-out, initRealDepsWithExisting, --all-regions flag on NewDoctorCmdWithDeps, profilepkg + path/filepath imports
- `internal/app/cmd/doctor_test.go` — testConfig + testDoctorConfig updated (2 new methods each), os/path/filepath imports, mockEC2AMIDoctor, makeTestAMI, doctorStaleAMIConfig, 11 new tests
- `internal/app/cmd/shell_ami_test.go` — fixed ShellExecFunc type mismatch (pre-existing Plan 05 issue, Rule 3 auto-fix)

## Test Count and Status

**11 new tests passing:**
1. TestCheckStaleAMIs_NilClient_Skipped — PASS
2. TestCheckStaleAMIs_NoAMIs_OK — PASS
3. TestCheckStaleAMIs_AllWithinThreshold_OK — PASS
4. TestCheckStaleAMIs_StaleFound_Warn — PASS
5. TestCheckStaleAMIs_ProfileRefSkipped — PASS
6. TestCheckStaleAMIs_RunningSandboxSkipped — PASS
7. TestCheckStaleAMIs_DescribeImagesError_Warn — PASS
8. TestCheckStaleAMIs_UnparsableCreationDate_Skipped — PASS
9. TestCheckStaleAMIs_RegionInName — PASS
10. TestDoctorCmd_AllRegionsFlagExists — PASS
11. TestDoctor_DefaultRegionScope_OnlyPrimary — PASS
12. TestDoctor_AllRegionsFlag_PopulatesMultipleAMIClients — PASS

*(12 tests total including one added alongside Task 2 flag test)*

## Documented Limitation: sandboxUsesAMIInDoctor Profile-File Scope

`sandboxUsesAMIInDoctor` determines "a running sandbox uses this AMI" by:
1. Resolving `sb.Profile` (bare name, e.g. `"restricted-dev"`) to `<searchDir>/restricted-dev.yaml` via case-insensitive `strings.EqualFold` match on the base name
2. Parsing the resolved YAML and checking `spec.runtime.ami == amiID`

**Limitation:** If the sandbox's profile YAML file has been deleted, renamed, or moved after the sandbox was created, `sandboxUsesAMIInDoctor` returns `false` and the AMI may be incorrectly flagged as stale even though instances are still booting from it.

Operators should be aware that:
- The check is conservative in the profile-reference direction (profiles are found even with case drift)
- The check is NOT conservative in the running-sandbox direction if profile files drift from sandbox records

If `kmaws.SandboxRecord` gains a persisted `AMI` field in the future (written at create time), extend `sandboxUsesAMIInDoctor` to fall back to that field. This would remove the filesystem-based ambiguity entirely.

## Manual Verification: Phase 33 Slug Resolution in ca-central-1 (P56-12)

This task documents the manual verification procedure for Phase 33's open human-verification item #2. The verification must be performed by an operator post-merge and is NOT executed automatically.

**Background:** Phase 56's snapshot-to-profile flow writes a region-scoped raw AMI ID into `spec.runtime.ami`. Before using Phase 56's full lifecycle in ca-central-1, operators need confidence that the slug resolution path (`spec.runtime.ami: amazon-linux-2023` → `data.aws_ami` filter lookup → region-specific AMI ID) works correctly in that region. Phase 56 accepts ownership of this verification from Phase 33 (CONTEXT.md).

### Verification Procedure

1. Set `KM_REGION=ca-central-1` (or use a profile with `spec.runtime.region: ca-central-1`).

2. Create or locate a profile YAML with the default slug:
   ```yaml
   spec:
     runtime:
       ami: amazon-linux-2023
   ```

3. Run `km validate <profile.yaml>` — **expected:** validation passes.

4. Perform a dry-run plan to verify `data.aws_ami.base_ami` resolution:
   ```bash
   # Option A: use km create with dry-run (if available)
   km create <profile.yaml> --no-apply

   # Option B: run terraform plan directly against the ec2spot module
   cd infra/modules/ec2spot/v1.0.0/
   terraform init
   terraform plan -var='region=ca-central-1' -var='ami_slug=amazon-linux-2023'
   ```

5. Inspect plan output: look for `data.aws_ami.base_ami` — the `image_id` attribute must be a non-empty AMI ID (format `ami-xxxxxxxxxxxxxxx`) owned by Amazon.

6. **(Optional but recommended)** Confirm the slug resolves natively in ca-central-1:
   ```bash
   aws ec2 describe-images \
     --owners amazon \
     --filters "Name=name,Values=al2023-ami-2023.*-kernel-6.1-x86_64" \
     --region ca-central-1 \
     --query 'Images[].[ImageId,Name,CreationDate]' \
     --output table
   ```
   **Expected:** at least one row, confirming Amazon Linux 2023 AMIs exist in ca-central-1 under the canonical name pattern.

### Expected Outcomes

| Outcome | Action |
|---------|--------|
| Resolution succeeds (non-empty AMI ID) | P56-12 verified. Close Phase 33's open human-verification item #2 in VERIFICATION.md |
| Resolution fails (empty or error) | File a GitHub issue. Check `infra/modules/ec2spot/v1.0.0/main.tf` `data.aws_ami` filter block for region-specific tuning. (Not expected — Amazon Linux 2023 name patterns are stable globally.) |

**This procedure does NOT execute the verification.** An operator must run the steps above post-merge. The result should be recorded in `56-VERIFICATION.md` under "Manual Verification Results".

## Decisions Made

- `checkStaleAMIs` is flag-only — no deletion performed in Phase 56, per CONTEXT.md locked decision.
- "Unused" definition: NOT referenced by any local profile AND NOT backing any running sandbox (both conditions required per CONTEXT.md).
- Default scope: single region (`KM_REGION`). `--all-regions` expands to `PrimaryRegion + KM_REPLICA_REGION` CSV.
- `sandboxUsesAMIInDoctor` uses profile-file-based resolution. SandboxRecord has no persisted AMI field; this is documented as a limitation.
- `initRealDepsWithExisting` replaces direct `initRealDeps` call so `AllRegions` propagates into region list construction before client allocation.

## Deviations from Plan

**1. [Rule 3 - Blocking] Fixed shell_ami_test.go ShellExecFunc type mismatch**
- **Found during:** Task 1 test compilation
- **Issue:** `shell_ami_test.go` (Plan 05 untracked file) was calling `NewShellCmdWithFetcher` with `func(_ interface{Args() []string}) error` instead of `func(c *exec.Cmd) error`, causing package-wide build failure
- **Fix:** Updated the lambda to `func(_ *exec.Cmd) error { return nil }` and added `"os/exec"` import
- **Files modified:** `internal/app/cmd/shell_ami_test.go`
- **Commit:** `df96e3a`

## Operator Action Items

1. **After merging Phase 56:** Run `make build` to refresh the local km binary with `km doctor --all-regions` support.
2. **After merging Phase 56:** Run `km init --sidecars` once to refresh the management Lambda's bundled km binary (per project memory `project_schema_change_requires_km_init.md`).
3. **Post-merge operator verification:** Follow the P56-12 procedure documented above to confirm ca-central-1 slug resolution works; record result in `56-VERIFICATION.md`.

## Self-Check

Files exist:
- `internal/app/cmd/doctor.go` — confirmed (modified, build passes)
- `internal/app/cmd/doctor_test.go` — confirmed (modified, 12 new tests pass)
- `internal/app/cmd/shell_ami_test.go` — confirmed (modified, compilation fixed)
- `.planning/phases/56-learn-mode-ami-snapshot-and-lifecycle-management/56-06-SUMMARY.md` — this file

Commits exist:
- `d91ff55` — TDD RED (Task 1 failing tests)
- `df96e3a` — Task 1 GREEN (checkStaleAMIs + DoctorDeps + interface extensions)
- `a6afac4` — TDD RED (Task 2 failing tests)
- `ed9d92b` — Task 2 GREEN (--all-regions flag)

## Self-Check: PASSED

---
*Phase: 56-learn-mode-ami-snapshot-and-lifecycle-management*
*Completed: 2026-04-26*
