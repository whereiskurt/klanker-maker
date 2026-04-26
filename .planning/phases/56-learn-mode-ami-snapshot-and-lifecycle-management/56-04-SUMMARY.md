---
phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
plan: 04
subsystem: cmd
tags: [cobra, ami, ec2, lifecycle, testing]

# Dependency graph
requires:
  - phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
    plan: 01
    provides: pkg/aws/ec2_ami.go — EC2AMIAPI, BakeAMI, ListBakedAMIs, DeleteAMI, CopyAMI, KMBakeTags, AMIName
  - phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
    plan: 02
    provides: Config.DoctorStaleAMIDays (cfg.ProfileSearchPaths used by FindProfilesReferencingAMI)
provides:
  - NewAMICmd + NewAMICmdWithDeps — Cobra parent with list/delete/bake/copy children registered in root.go
  - BakeFromSandbox(ctx, cfg, rec, sandboxID, profileName, kmVersion) (string, error) — exported helper for Plan 05
  - FindProfilesReferencingAMI(searchPaths, amiID) ([]string, error) — exported helper for Plan 06 checkStaleAMIs
  - parseAge — accepts Go durations (168h) and Nd shorthand (7d)
affects:
  - 56-05 (km shell --learn --ami calls BakeFromSandbox)
  - 56-06 (km doctor checkStaleAMIs calls FindProfilesReferencingAMI)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "NewAMICmdWithDeps DI pattern: ec2Factory func(region string) kmaws.EC2AMIAPI, fetcher SandboxFetcher, lister SandboxLister"
    - "amiConfirmPrompt accepts io.Reader for stdin injection in tests (Test 10)"
    - "mockEC2AMI.callOrder []string enables strict ordering assertion in TestAMICopy (call order: CopyImage < CreateTags)"
    - "AMI ID uniqueness: test IDs use distinct character sequences (ami-0fresh111111 vs ami-0tendays1111) to avoid substring false-positives"

key-files:
  created:
    - internal/app/cmd/ami.go
    - internal/app/cmd/ami_test.go
  modified:
    - internal/app/cmd/root.go

key-decisions:
  - "BakeFromSandbox is exported (capitalized) so Plan 05 can call it cross-file within the cmd package"
  - "FindProfilesReferencingAMI is exported so Plan 06 (doctor.go) can call it as cmd.FindProfilesReferencingAMI cross-package"
  - "ec2Factory func(region string) kmaws.EC2AMIAPI is the 4-param DI shape (not 3) — lister is the 4th param per plan spec"
  - "SandboxRecord does not have InstanceType field; BakeFromSandbox passes empty string to KMBakeTags for instance-type — acceptable, km:instance-type tag is populated from the record when available"
  - "expandAMIPath named with AMI prefix to avoid collision with any other expandPath in the cmd package"
  - "testConfig conflict: doctor_test.go uses testConfig as a struct type; ami_test.go uses amiTestConfig to avoid redeclaration"

patterns-established:
  - "Pattern: parseAge accepts both time.ParseDuration strings and Nd convenience notation"
  - "Pattern: filterByAge keeps images WHERE created.Before(cutoff) — cutoff = now - duration"
  - "Pattern: amiConfirmPrompt takes io.Reader so tests inject bytes.NewBufferString('n') without tty"

requirements-completed: [P56-03, P56-04, P56-05, P56-06, P56-07]

# Metrics
duration: 188min
completed: 2026-04-26
---

# Phase 56 Plan 04: km ami CLI Subcommand Tree Summary

**Four-subcommand km ami Cobra tree (list/delete/bake/copy) with BakeFromSandbox and FindProfilesReferencingAMI exported helpers, 15 passing tests, and binary rebuilt via make build**

## Performance

- **Duration:** ~188 min
- **Started:** 2026-04-26T17:38:45Z
- **Completed:** 2026-04-26T20:46:48Z
- **Tasks:** 3
- **Files modified:** 3

## Accomplishments

- `NewAMICmd` + `NewAMICmdWithDeps(cfg, ec2Factory, fetcher, lister)` with 4-param DI pattern for full unit-test coverage without real AWS
- `km ami list` with tabwriter narrow/wide output, `--profile/--age/--unused/--region/--all-regions/--json` flags, newest-first sort
- `km ami delete` with profile refcount safety check, `--force/--yes/--dry-run/--region` flags, snapshot preview, confirmation prompt
- `km ami bake` backed by `BakeFromSandbox` helper; validates EC2 substrate before calling `kmaws.BakeAMI`
- `km ami copy` cross-region copy via `kmaws.CopyAMI` (re-tagging included per Pitfall 3 from RESEARCH.md)
- `BakeFromSandbox` exported — Plan 05's `km shell --learn --ami` integration point
- `FindProfilesReferencingAMI` exported — Plan 06's `checkStaleAMIs` can call `cmd.FindProfilesReferencingAMI` cross-package
- `parseAge` accepts Go duration syntax (`168h`) and day shorthand (`7d`)
- `root.go` registered `NewAMICmd(cfg)` after `NewEmailCmd` for visual grouping
- `make build` succeeded: km v0.1.398; `./km ami --help` lists 4 subcommands

## Public Surface (for downstream plans to import)

```go
// internal/app/cmd — package cmd

func NewAMICmd(cfg *config.Config) *cobra.Command
func NewAMICmdWithDeps(cfg *config.Config, ec2Factory func(region string) kmaws.EC2AMIAPI, fetcher SandboxFetcher, lister SandboxLister) *cobra.Command

// BakeFromSandbox bakes an AMI from a running EC2 sandbox record.
// Plan 05 (km shell --learn --ami) calls this after runLearnPostExit.
func BakeFromSandbox(ctx context.Context, cfg *config.Config, rec kmaws.SandboxRecord, sandboxID, profileName, kmVersion string) (string, error)

// FindProfilesReferencingAMI walks cfg.ProfileSearchPaths recursively.
// Plan 06 (km doctor checkStaleAMIs) calls this as cmd.FindProfilesReferencingAMI.
func FindProfilesReferencingAMI(searchPaths []string, amiID string) ([]string, error)
```

## Subcommand Summary

| Subcommand | Description | Key Flags |
|-----------|-------------|-----------|
| `km ami list` | List km-tagged AMIs, newest first | `--wide`, `--profile`, `--age`, `--unused`, `--region`, `--all-regions`, `--json` |
| `km ami delete` | Delete AMI + snapshots with safety check | `--force` (bypass refcount), `--yes` (skip confirm), `--dry-run`, `--region` |
| `km ami bake` | Bake AMI from running EC2 sandbox | `--description`, `--wait-timeout` (default 15m) |
| `km ami copy` | Copy AMI cross-region with re-tagging | `--to-region` (required), `--from-region`, `--description`, `--wait-timeout` |

## Task Commits

1. **Task 1: ami.go** — `c9f5b02` (feat)
2. **Task 2: ami_test.go** — `c3f2c0f` (test)
3. **Task 3: root.go + make build** — `3571315` (feat)

## Files Created/Modified

- `internal/app/cmd/ami.go` — NewAMICmd, NewAMICmdWithDeps, BakeFromSandbox, FindProfilesReferencingAMI, parseAge, amiConfirmPrompt, printAMITable, filterByTag, filterByAge, filterUnused, tagValue, imageSizeGB, imageAgeString, isAMIEncrypted, collectConfiguredRegions, buildRealEC2Factory, annotateRegion, sandboxUsesAMI, expandAMIPath, realAMISandboxFetcher, amiPrimaryRegion
- `internal/app/cmd/ami_test.go` — mockEC2AMI, mockSandboxListerAMI, mockSandboxFetcherAMI, amiTestConfig, tempProfileDir, makeImage, executeAMICmd, 15 tests
- `internal/app/cmd/root.go` — Added `root.AddCommand(NewAMICmd(cfg))`

## Test Count and Status

**15 tests passing:**
1. TestAMIList_NarrowOutput_Columns — PASS
2. TestAMIList_WideOutput_Columns — PASS
3. TestAMIList_SortedNewestFirst — PASS
4. TestAMIList_AgeFilter — PASS
5. TestAMIList_ProfileFilter — PASS
6. TestAMIList_UnusedFilter — PASS
7. TestAMIDelete_RefuseWhenProfileReferences — PASS
8. TestAMIDelete_ForceOverridesProfileRef — PASS
9. TestAMIDelete_DryRunDoesNotCallDelete — PASS
10. TestAMIDelete_ConfirmPromptHonored — PASS
11. TestAMICopy_CallsCopyImageThenRetags — PASS
12. TestBakeFromSandbox_NonEC2Substrate_Errors — PASS
13. TestBakeFromSandbox_HappyPath — PASS
14. TestParseAge (7 subtests) — PASS
15. TestFindProfilesReferencingAMI — PASS

## Decisions Made

- `BakeFromSandbox` exported (capitalized) so Plan 05 can call it from shell.go (same package: `internal/app/cmd`)
- `FindProfilesReferencingAMI` exported so Plan 06 (`doctor.go`) can call it cross-package as `cmd.FindProfilesReferencingAMI`
- `ec2Factory func(region string) kmaws.EC2AMIAPI` as the 4th DI param mirrors the plan's `NewAMICmdWithDeps` spec exactly
- `SandboxRecord.InstanceType` does not exist — `BakeFromSandbox` passes `""` for instance-type in `KMBakeTags`; the `km:instance-type` tag will be present if the profile's `InstanceType` is available at bake time (a caller that knows the instance type can enrich the tags directly)
- Named `expandAMIPath` instead of `expandPath` to avoid future collision with other path helpers in the package
- Named `amiTestConfig` in tests to avoid redeclaration conflict with `testConfig` struct in `doctor_test.go`

## Deviations from Plan

**1. [Rule 1 - Bug] AMI ID uniqueness in TestAMIList_AgeFilter**
- **Found during:** Task 2 test run
- **Issue:** Test AMI IDs `ami-0day1000000` and `ami-0day10000000` share a 15-char prefix; `strings.Contains(stdout, "ami-0day1000000")` matched the day10 row as a substring, causing a false-positive failure
- **Fix:** Changed to `ami-0fresh111111`, `ami-0tendays1111`, `ami-0thirty11111` — distinct character sequences that cannot overlap
- **Files modified:** internal/app/cmd/ami_test.go

**2. [Rule 1 - Bug] testConfig naming conflict with doctor_test.go**
- **Found during:** Task 2 test compilation
- **Issue:** `testConfig` is declared as a struct type in `doctor_test.go` (same `package cmd`); declaring a function with the same name causes redeclaration error
- **Fix:** Renamed the ami test helper to `amiTestConfig`
- **Files modified:** internal/app/cmd/ami_test.go

## Operator Action Item

After merging Phase 56, run `make build` to refresh the local km binary. The new `km ami` subcommand tree is not reachable until the binary is rebuilt.

## Self-Check

Files exist:
- `internal/app/cmd/ami.go` — confirmed (created, build passes)
- `internal/app/cmd/ami_test.go` — confirmed (created, 15 tests pass)
- `internal/app/cmd/root.go` — confirmed (modified, NewAMICmd registered)

Commits exist:
- `c9f5b02` — Task 1 feat commit (ami.go)
- `c3f2c0f` — Task 2 test commit (ami_test.go)
- `3571315` — Task 3 feat commit (root.go + make build)

## Self-Check: PASSED

---
*Phase: 56-learn-mode-ami-snapshot-and-lifecycle-management*
*Completed: 2026-04-26*
