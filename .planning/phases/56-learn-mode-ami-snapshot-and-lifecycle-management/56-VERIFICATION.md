---
phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
verified: 2026-04-26T17:30:00Z
status: passed
score: 12/12 must-haves verified
re_verification: false
human_verification:
  - test: "Run km shell --learn --ami <sandbox-id> against a live EC2 sandbox, exit the shell, and confirm the generated learned.*.yaml contains spec.runtime.ami: ami-xxxxxxxx"
    expected: "Profile file includes spec.runtime.ami with a valid AMI ID; km ami list shows the AMI with correct km tags"
    why_human: "Requires a live EC2 sandbox, SSM access, and real CreateImage API call — cannot mock end-to-end"
  - test: "P56-12: ca-central-1 slug resolution. Set KM_REGION=ca-central-1, create a profile with spec.runtime.ami: amazon-linux-2023, run km validate, then terraform plan -var region=ca-central-1. Confirm data.aws_ami.base_ami resolves to a non-empty ami-xxx ID."
    expected: "Resolution succeeds with a non-empty AMI ID owned by Amazon"
    why_human: "Requires AWS API access to ca-central-1; procedure is documented in 56-06-SUMMARY.md under 'Manual Verification: Phase 33 Slug Resolution in ca-central-1'"
  - test: "Run km doctor (with --all-regions) against a live account and confirm checkStaleAMIs appears in output"
    expected: "If AMIs older than doctor_stale_ami_days (default 30) exist unreferenced by profiles and not backing sandboxes, they appear as WARN; otherwise the check shows OK/SKIP"
    why_human: "Requires live AWS credentials; no live sandboxes available in test environment"
  - test: "Run km ami list against a live account and verify the tabwriter narrow and --wide output renders correctly"
    expected: "Narrow shows 6 columns (AMI ID, NAME, AGE, SIZE, PROFILE, REFS); --wide adds 6 more (REGION, SANDBOX-ID, SNAPS, ENCRYPTED, INSTANCE, $/MONTH)"
    why_human: "Output formatting verified by unit test but real AWS data path requires live credentials"
---

# Phase 56: Learn-Mode AMI Snapshot and Lifecycle Management — Verification Report

**Phase Goal:** Add `--ami` flag to `km shell --learn` that snapshots the EC2 instance as a custom AMI on exit. The AMI ID is written into the generated profile YAML at `spec.runtime.ami`. AMIs are tagged with sandbox metadata. Add `km ami list/delete/bake/copy` commands and a `km doctor` stale/unused AMI check. Backward compatible with Phase 33/33.1.
**Verified:** 2026-04-26T17:30:00Z
**Status:** passed (with human verification items noted)
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Operator-side Go code can call CreateImage with NoReboot=true and atomic image+snapshot tags | VERIFIED | `pkg/aws/ec2_ami.go:121-122` — TagSpecifications carries both ResourceTypeImage and ResourceTypeSnapshot; `TestBakeAMI_TagSpecifications` confirms two tag specs |
| 2 | Operator-side Go code can call DeregisterImage with DeleteAssociatedSnapshots=true | VERIFIED | `pkg/aws/ec2_ami.go:182` — `DeleteAssociatedSnapshots: awssdk.Bool(true)`; `TestDeleteAMI_PassesDeleteAssociatedSnapshots` confirms |
| 3 | Operator-side Go code can call CopyImage and re-tag the destination AMI in the new region | VERIFIED | `pkg/aws/ec2_ami.go:217-280` CopyAMI waits for available, then calls CreateTags on destination AMI + snapshots; `TestCopyAMI` exists in test file (test 12) |
| 4 | AMI helpers wait for available state via ec2.NewImageAvailableWaiter | VERIFIED | `pkg/aws/ec2_ami.go:139` and line 235 use `ec2.NewImageAvailableWaiter(describeImagesClient{client})` |
| 5 | Config.DoctorStaleAMIDays field exists with default 30, viper key doctor_stale_ami_days, zero/negative falls back to default | VERIFIED | `internal/app/config/config.go:134,172,235,272,287-288` — field, SetDefault, merge-list, struct-build, clamp all present; 4 tests pass |
| 6 | bootstrap.go SCP DenyInfraAndStorage contains ec2:DeregisterImage, ec2:DeleteSnapshot, ec2:CreateTags; Describe* NOT in any Deny | VERIFIED | `internal/app/cmd/bootstrap.go:64,71,72` — actions present; grep confirms DescribeImages/DescribeSnapshots only appear in WriteOperatorIAMGuidance text, not in SCP statements |
| 7 | BuildSCPPolicy and WriteOperatorIAMGuidance are exported; runShowSCP emits positive-allow guidance | VERIFIED | `bootstrap.go:42` func BuildSCPPolicy; `bootstrap.go:139` func WriteOperatorIAMGuidance; 4 bootstrap tests pass including TestBootstrapShowSCP_EmitsOperatorPositiveAllowGuidance |
| 8 | km ami list/delete/bake/copy Cobra subcommand tree exists, registered in root, flags complete | VERIFIED | `internal/app/cmd/ami.go` 25840 bytes; NewAMICmd/NewAMICmdWithDeps/BakeFromSandbox/FindProfilesReferencingAMI all exported; `root.go:83` AddCommand(NewAMICmd); freshly built `./km ami --help` lists 4 subcommands; all flags confirmed |
| 9 | km shell --ami flag exists, enforced to require --learn, bake fires BEFORE flush | VERIFIED | `shell.go:130` flag registered; `shell.go:98-99` mutual-exclusion enforced; `shell.go:644-654` bake precedes flush at line 658; `TestRunLearnPostExit_AMIFlag_BakesBeforeFlush` test verifies order via recording wrapper |
| 10 | Recorder.RecordAMI + Recorder.AMI() exist; Generate() emits Spec.Runtime.AMI | VERIFIED | `recorder.go:169-182`; `generator.go:123-124`; 6 new tests including TestGenerate_WithAMIAndInitCommands_BothPresent confirming Phase 55 compatibility |
| 11 | GenerateProfileFromJSON signature accepts amiID; all call sites updated | VERIFIED | `shell.go:529` func GenerateProfileFromJSON(data, base, amiID string); bake failure path writes profile without ami and returns non-nil error; `TestGenerateProfileFromJSON_WithAMIID_EmitsRuntimeAMI` passes |
| 12 | checkStaleAMIs registered in doctor buildChecks; DoctorDeps.EC2AMIClients + AllRegions; DoctorConfigProvider extended; --all-regions flag; 12 tests pass | VERIFIED | `doctor.go:1934-1941` buildChecks fan-out; `doctor.go:212,217` EC2AMIClients + AllRegions fields; `doctor.go:160,163` new interface methods; `doctor.go:1620` --all-regions flag; freshly built `./km doctor --help` shows flag; 12 new doctor tests pass |

**Score:** 12/12 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Lines / Evidence |
|----------|----------|--------|-----------------|
| `pkg/aws/ec2_ami.go` | EC2AMIAPI interface, BakeAMI, ListBakedAMIs, DeleteAMI, CopyAMI, KMBakeTags, AMIName, SnapshotIDsFromImage | VERIFIED | 10,736 bytes; all 8 exports confirmed at lines 25,44,75,115,153,179,194,217 |
| `pkg/aws/ec2_ami_test.go` | 12 mock-based unit tests; var _ EC2AMIAPI check | VERIFIED | 12,905 bytes; 12 TestXxx functions at lines 90-350; compile-time assertion at line 45 |
| `internal/app/config/config.go` | DoctorStaleAMIDays field, default 30, viper key doctor_stale_ami_days, clamp | VERIFIED | Field at line 134; default at 172; merge-list at 235; struct-build at 272; clamp at 287-288 |
| `internal/app/config/config_test.go` | 4 TestConfig_DoctorStaleAMIDays_* tests | VERIFIED | Tests at lines 252,273,296,321 |
| `km-config.yaml` | doctor_stale_ami_days: 30 with comment | VERIFIED | Line 29 |
| `internal/app/cmd/bootstrap.go` | BuildSCPPolicy (exported), WriteOperatorIAMGuidance (exported), SCPStatement/SCPPolicyDoc types, ec2:DeregisterImage+DeleteSnapshot+CreateTags in DenyInfraAndStorage | VERIFIED | Types at 22-37; BuildSCPPolicy at 42; WriteOperatorIAMGuidance at 139; new actions at 64,71,72 |
| `internal/app/cmd/ami.go` | NewAMICmd, newAMIListCmd, newAMIDeleteCmd, newAMIBakeCmd, newAMICopyCmd, BakeFromSandbox, FindProfilesReferencingAMI | VERIFIED | 25,840 bytes; all functions present at listed line numbers |
| `internal/app/cmd/ami_test.go` | 13+ unit tests (narrow output, filters, delete safety, copy re-tagging, BakeFromSandbox) | VERIFIED | 15 test functions confirmed |
| `internal/app/cmd/root.go` | AddCommand(NewAMICmd(cfg)) | VERIFIED | Line 83 |
| `internal/app/cmd/shell.go` | --ami flag, bakeFromSandboxFn/flushEC2ObservationsFn vars, bake-before-flush order, GenerateProfileFromJSON(data, base, amiID) signature | VERIFIED | Flags at 130; vars at 39,43; ordering at 644-658; signature at 529 |
| `internal/app/cmd/shell_ami_test.go` | 7 tests (bake order, YAML injection, failure path, no-ami path, docker skip, flag enforcement, round-trip) | VERIFIED | 7 TestXxx functions confirmed |
| `pkg/allowlistgen/recorder.go` | RecordAMI(amiID string), AMI() string, amiID field | VERIFIED | amiID field at 23; RecordAMI at 169; AMI() at 180 |
| `pkg/allowlistgen/generator.go` | Spec.Runtime.AMI emitted when recorder.amiID non-empty | VERIFIED | Lines 123-124 |
| `pkg/allowlistgen/generator_test.go` | TestRecordAMI_*, TestGenerate_WithAMI, TestGenerateAnnotatedYAML_WithAMI, TestGenerate_WithoutAMI, TestGenerate_WithAMIAndInitCommands | VERIFIED | 6 new tests at lines 241-340 |
| `internal/app/cmd/doctor.go` | checkStaleAMIs in buildChecks; DoctorDeps.EC2AMIClients + AllRegions; DoctorConfigProvider.GetDoctorStaleAMIDays + GetProfileSearchPaths; sandboxUsesAMIInDoctor; --all-regions flag | VERIFIED | All confirmed; doctor.go is 72,694 bytes |
| `internal/app/cmd/doctor_test.go` | 12 new stale-AMI tests | VERIFIED | mockEC2AMIDoctor at 1090; 12 TestCheckStaleAMIs_* + TestDoctorCmd_AllRegionsFlagExists + TestDoctor_* tests |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/aws/ec2_ami.go BakeAMI()` | `CreateImageInput.TagSpecifications` | ResourceTypeImage + ResourceTypeSnapshot in one call | WIRED | Lines 121-122; test TestBakeAMI_TagSpecifications asserts 2 tag specs |
| `pkg/aws/ec2_ami.go` | `ec2.NewImageAvailableWaiter` | describeImagesClient{client} adapter satisfies ec2.DescribeImagesAPIClient | WIRED | Lines 139,235 |
| `pkg/aws/ec2_ami.go DeleteAMI()` | `DeregisterImageInput.DeleteAssociatedSnapshots` | Single API call cleans up image + snapshots atomically | WIRED | Line 182; test confirmed |
| `internal/app/cmd/shell.go runLearnPostExit` | `BakeFromSandbox` | bakeFromSandboxFn() called before flushEC2ObservationsFn() | WIRED | Lines 647,658; test-verified order |
| `internal/app/cmd/shell.go GenerateProfileFromJSON` | `pkg/allowlistgen/recorder.go RecordAMI` | amiID parameter triggers rec.RecordAMI(amiID) | WIRED | shell.go:529; generator_test confirms YAML emission |
| `pkg/allowlistgen/generator.go Generate()` | `p.Spec.Runtime.AMI` | r.AMI() non-empty → assign to RuntimeSpec | WIRED | Lines 123-124 |
| `internal/app/cmd/root.go` | `NewAMICmd` | root.AddCommand(NewAMICmd(cfg)) | WIRED | root.go:83; confirmed in fresh binary help output |
| `internal/app/cmd/doctor.go buildChecks` | `checkStaleAMIs` | Fan-out loop over deps.EC2AMIClients at lines 1934-1941 | WIRED | Function registered; allRegions propagated via initRealDepsWithExisting |
| `internal/app/cmd/bootstrap.go runShowSCP` | `WriteOperatorIAMGuidance` | Called after SCP JSON emission | WIRED | WriteOperatorIAMGuidance is exported and callable; test TestBootstrapShowSCP_EmitsOperatorPositiveAllowGuidance verifies content |

---

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| P56-01 | 56-01, 56-05 | BakeAMI helper; --ami flag on km shell | SATISFIED | ec2_ami.go BakeAMI + shell.go --ami + shell_ami_test.go all verified |
| P56-02 | 56-05 | Generated profile contains spec.runtime.ami when bake succeeds | SATISFIED | generator.go:123-124 emits AMI; round-trip test confirmed |
| P56-03 | 56-04 | km ami list subcommand | SATISFIED | newAMIListCmd in ami.go; all flags present; 6 list tests pass |
| P56-04 | 56-04 | km ami delete with profile-refcount safety | SATISFIED | newAMIDeleteCmd; FindProfilesReferencingAMI; --force/--yes/--dry-run; 3 delete tests |
| P56-05 | 56-04 | km ami bake subcommand | SATISFIED | newAMIBakeCmd; BakeFromSandbox exported |
| P56-06 | 56-04 | km ami copy with cross-region re-tagging | SATISFIED | newAMICopyCmd; CopyAMI; TestAMICopy_CallsCopyImageThenRetags |
| P56-07 | 56-01, 56-04 | KMBakeTags + AMIName helpers; narrow vs --wide convention | SATISFIED | ec2_ami.go KMBakeTags/AMIName; ami.go --wide flag; TestAMIList_NarrowOutput_Columns + TestAMIList_WideOutput_Columns |
| P56-08 | 56-06 | km doctor stale AMI check (flag-only) | SATISFIED | checkStaleAMIs in buildChecks; 9 TestCheckStaleAMIs_* tests; flag-only confirmed |
| P56-09 | 56-02 | Config.DoctorStaleAMIDays with default 30 | SATISFIED | config.go field, default, clamp; 4 config tests pass |
| P56-10 | 56-03 | SCP updated with AMI-lifecycle mutating ops + operator IAM guidance | SATISFIED | bootstrap.go:64,71,72 new actions; WriteOperatorIAMGuidance with all 5 ops + rationale |
| P56-11 | 56-05 | Phase 55 initCommands preserved in generated profile when ami is set | SATISFIED | TestGenerate_WithAMIAndInitCommands_BothPresent confirms both fields populated |
| P56-12 | 56-06 | ca-central-1 verification — manual procedure documented | SATISFIED (procedure documented; execution is human) | 56-06-SUMMARY.md lines 156-229 contain step-by-step Yes/No-answerable procedure with expected outcomes table |

All 12 P56-XX requirements accounted for. Note: P56-XX IDs are phase-local and do not appear in the global REQUIREMENTS.md table — this is acceptable per the verification instructions.

---

### Anti-Patterns Found

| File | Pattern | Severity | Impact |
|------|---------|----------|--------|
| None found in Phase 56 artifacts | — | — | No stubs, placeholder returns, or TODO-only implementations detected |

Stub detection scan notes:
- `ami.go` returns are fully implemented (tabwriter output, confirmation prompts, profile scan, AWS API calls)
- `shell.go` bake-before-flush path is substantive; docker/ECS substrate skip includes warning log
- `generator.go` AMI emission is a real field assignment, not a placeholder
- `doctor.go` checkStaleAMIs parses CreationDate, computes age, cross-references profiles and sandboxes

---

### Human Verification Required

#### 1. End-to-End km shell --learn --ami

**Test:** Connect to a live EC2 sandbox with `km shell --learn --ami <sandbox-id>`. Exit the shell (Ctrl-D or exit). Observe stderr for "Baking AMI from sandbox state..." and "AMI ready: ami-xxx". Inspect the generated `learned.*.yaml` file.
**Expected:** File contains `spec.runtime.ami: ami-xxxxxxxxxxxxxxxxx` with a valid AMI ID; `km ami list` shows the AMI tagged with km:sandbox-id, km:profile, km:baked-at, km:baked-by=km.
**Why human:** Requires live EC2 sandbox, SSM session, and real CreateImage API call. Cannot mock the full SSM + AWS round-trip.

#### 2. P56-12: ca-central-1 Slug Resolution

**Test:** Follow the procedure documented in `56-06-SUMMARY.md` under "Manual Verification: Phase 33 Slug Resolution in ca-central-1". Steps: set KM_REGION=ca-central-1, use a profile with `spec.runtime.ami: amazon-linux-2023`, run `km validate`, run terraform plan or `aws ec2 describe-images --owners amazon --region ca-central-1`.
**Expected:** `data.aws_ami.base_ami` resolves to a non-empty ami-xxx ID owned by Amazon. Table in 56-06-SUMMARY.md documents the action for pass and fail outcomes.
**Why human:** Requires live AWS API access to ca-central-1 and Terraform state. Procedure is Yes/No-answerable.

#### 3. km doctor --all-regions Stale-AMI Output

**Test:** Run `km doctor --all-regions` against a live account that has at least one custom AMI (km-tagged) older than 30 days that is not referenced by any profile.
**Expected:** The stale-AMI check appears in the doctor output as WARN with the AMI ID and age. No auto-deletion occurs (flag-only behavior confirmed in code).
**Why human:** Requires live AWS credentials and an aged AMI in the account.

#### 4. km ami list Rendering

**Test:** Run `km ami list` and `km ami list --wide` against a live account with at least one km-tagged AMI.
**Expected:** Narrow shows 6 columns correctly formatted; --wide shows 12+ columns; newest AMIs appear first.
**Why human:** Unit tests use mock data; production tabwriter formatting with real AMI names/IDs may have edge cases.

---

### Gaps Summary

No gaps blocking goal achievement. All automated checks passed:

- All 12 P56-XX requirements are satisfied by substantive, wired implementations
- Test suite: only pre-existing `TestUnlockCmd_RequiresStateBucket` fails (SSO token issue, not Phase 56 related); all other packages including `pkg/aws`, `pkg/allowlistgen`, `pkg/profile`, `pkg/compiler`, `internal/app/config` pass cleanly
- The `internal/app/cmd` package passes all tests except the pre-existing SSO failure
- Phase 33/33.1 baseline preserved: `pkg/profile` and `pkg/compiler` test suites show zero failures
- Binary built successfully at v0.1.403 (commit 4469fd9); `km ami --help` lists 4 subcommands; `km shell --help` shows `--ami`; `km doctor --help` shows `--all-regions`

**Notable implementation detail:** The Makefile outputs the binary to `./km` (not `./bin/km`). The `./bin/km` artifact is an older build (v0.1.400, 16:46) that predates the Phase 56-05 and 56-06 commits. The authoritative current binary is `./km` at v0.1.403. Any CI or documentation referencing `./bin/km` should be updated to `./km` or operators should re-run `make build` post-merge.

---

_Verified: 2026-04-26T17:30:00Z_
_Verifier: Claude (gsd-verifier)_
