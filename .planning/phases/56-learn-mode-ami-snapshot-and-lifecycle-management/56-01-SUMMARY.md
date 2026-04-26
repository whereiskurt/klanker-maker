---
phase: 56-learn-mode-ami-snapshot-and-lifecycle-management
plan: 01
subsystem: aws
tags: [ec2, ami, snapshot, go, testing, sdk-v2]

# Dependency graph
requires:
  - phase: 33-ami-slug-resolution
    provides: RuntimeSpec.AMI field and raw-ID AMI schema in spec.runtime.ami
provides:
  - EC2AMIAPI narrow interface for AMI lifecycle operations (CreateImage, DescribeImages, DeregisterImage, CopyImage, CreateTags)
  - BakeAMI — live snapshot with NoReboot=true, atomic TagSpecifications, ImageAvailableWaiter
  - ListBakedAMIs — self-owned, km-tagged filter, sorted newest-first
  - DeleteAMI — DeregisterImage with DeleteAssociatedSnapshots=true, dryRun propagated
  - SnapshotIDsFromImage — EBS BDM traversal for dry-run preview
  - CopyAMI — CopyImage + waiter + re-tag in destination region (Pitfall 3 fix)
  - KMBakeTags — full tag set with optional km:alias omission when alias is empty
  - AMIName — sanitized, 128-char-capped, deterministic UTC timestamp name builder
  - 12 unit tests covering all public surfaces
affects: [56-02, 56-03, 56-04, 56-05, 56-06]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - describeImagesClient adapter struct bridges EC2AMIAPI to ec2.DescribeImagesAPIClient for waiter without type assertion
    - KMBakeTags omits km:alias tag entirely when alias is empty (no blank-value tag)
    - AMIName truncates sanitized profile prefix to keep total name within 128 chars

key-files:
  created:
    - pkg/aws/ec2_ami.go
    - pkg/aws/ec2_ami_test.go
  modified: []

key-decisions:
  - "EC2AMIAPI includes CreateTags method (5 methods total) so CopyAMI can re-tag without requiring a separate wider interface"
  - "describeImagesClient adapter bridges EC2AMIAPI to ec2.DescribeImagesAPIClient for NewImageAvailableWaiter — avoids runtime type assertion that would break mocks"
  - "KMBakeTags omits km:alias entirely when alias is empty rather than writing an empty-string tag"
  - "AMIName suffix (-sandboxID-YYYYMMDDHHMMSS) is fixed-width; profile portion is truncated to fill remaining 128-char budget"

patterns-established:
  - "Pattern: EC2AMIAPI + describeImagesClient adapter for waiter injection without casting"
  - "Pattern: BakeAMI returns (amiID, err) on waiter timeout so caller knows the AMI exists but is partial"
  - "Pattern: CopyAMI returns (dstAMIID, err) on post-copy failures for same caller-side handling"

requirements-completed: [P56-01, P56-02, P56-07]

# Metrics
duration: 2min
completed: 2026-04-26
---

# Phase 56 Plan 01: EC2AMIAPI Interface and AMI Lifecycle Helpers Summary

**EC2AMIAPI narrow interface plus BakeAMI/ListBakedAMIs/DeleteAMI/CopyAMI/KMBakeTags/AMIName helpers with 12 unit tests — foundational AWS SDK layer for all Wave 2 AMI commands**

## Performance

- **Duration:** 2 min
- **Started:** 2026-04-26T17:27:18Z
- **Completed:** 2026-04-26T17:29:00Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments
- `EC2AMIAPI` narrow interface with 5 operations covering all Wave 2 consumers; `*ec2.Client` satisfies it directly
- `BakeAMI` with NoReboot=true live snapshot, atomic TagSpecifications on image+snapshot in a single CreateImage call, waiter via `describeImagesClient` adapter (no runtime cast needed)
- `CopyAMI` copies image then re-tags AMI and its snapshots in the destination region after waiter (Pitfall 3 from RESEARCH.md)
- 12 unit tests all passing; mock satisfies `EC2AMIAPI` via compile-time `var _ EC2AMIAPI = (*mockEC2AMI)(nil)` assertion

## Public Surface (for downstream plans to import)

```go
// pkg/aws — package aws

type EC2AMIAPI interface {
    CreateImage(ctx context.Context, params *ec2.CreateImageInput, optFns ...func(*ec2.Options)) (*ec2.CreateImageOutput, error)
    DescribeImages(ctx context.Context, params *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
    DeregisterImage(ctx context.Context, params *ec2.DeregisterImageInput, optFns ...func(*ec2.Options)) (*ec2.DeregisterImageOutput, error)
    CopyImage(ctx context.Context, params *ec2.CopyImageInput, optFns ...func(*ec2.Options)) (*ec2.CopyImageOutput, error)
    CreateTags(ctx context.Context, params *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
}

func AMIName(profileName, sandboxID string, t time.Time) string
func KMBakeTags(sandboxID, profileName, alias, instanceType, sourceRegion, kmVersion string) []types.Tag
func BakeAMI(ctx context.Context, client EC2AMIAPI, instanceID, amiName, description string, tags []types.Tag, waitTimeout time.Duration) (string, error)
func ListBakedAMIs(ctx context.Context, client EC2AMIAPI) ([]types.Image, error)
func DeleteAMI(ctx context.Context, client EC2AMIAPI, amiID string, dryRun bool) error
func SnapshotIDsFromImage(img types.Image) []string
func CopyAMI(ctx context.Context, srcClient, dstClient EC2AMIAPI, srcRegion, dstRegion, srcAMIID, name, description string, tags []types.Tag, waitTimeout time.Duration) (string, error)
```

## Task Commits

Each task was committed atomically:

1. **Task 1: EC2AMIAPI interface and core helpers** - `a4a68f0` (feat)
2. **Task 2: Mock-based unit tests** - `56afa0b` (test)

## Files Created/Modified
- `pkg/aws/ec2_ami.go` — EC2AMIAPI interface, BakeAMI, ListBakedAMIs, DeleteAMI, SnapshotIDsFromImage, CopyAMI, KMBakeTags, AMIName, describeImagesClient adapter
- `pkg/aws/ec2_ami_test.go` — mockEC2AMI + 12 tests covering all public surfaces

## Decisions Made
- `EC2AMIAPI` includes `CreateTags` as a fifth method so `CopyAMI` can re-tag in the destination region without requiring a separate interface. Wave 2 consumers only need one interface to satisfy.
- `describeImagesClient` adapter struct wraps `EC2AMIAPI` and implements `ec2.DescribeImagesAPIClient` explicitly. This lets `ec2.NewImageAvailableWaiter` accept both `*ec2.Client` and test mocks without a runtime type assertion (`client.(ec2.DescribeImagesAPIClient)` would panic on mocks).
- `KMBakeTags` omits `km:alias` entirely when `alias == ""` rather than writing `km:alias=""`. Cleaner tag set; consumers check for tag presence.
- `AMIName` suffix is fixed-width (`-{sandboxID}-{14-digit-ts}`); the sanitized profile prefix is truncated to keep the total within the 128-char AWS limit.

## Deviations from Plan

None - plan executed exactly as written.

The one minor implementation choice: the plan suggested a runtime type assertion `client.(ec2.DescribeImagesAPIClient)` for the waiter path, but an adapter struct was used instead. This is functionally equivalent and more testable — the mock satisfies the adapter struct's single-method interface directly rather than requiring a separate runtime cast that would panic on mock types.

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required. No `make build` needed (no CLI changes in this plan).

## Next Phase Readiness
- `pkg/aws/EC2AMIAPI` + all helpers are ready for import by Plans 56-02 through 56-06
- Wave 2 consumers (km ami Cobra tree, km shell --learn --ami, km doctor checkStaleAMIs) can use the public surface without additional AWS SDK plumbing
- No new go.mod dependencies; all imports were already in the pinned SDK at v1.296.0

## Self-Check

Files exist:
- `pkg/aws/ec2_ami.go` — confirmed (created, build passes)
- `pkg/aws/ec2_ami_test.go` — confirmed (created, 12 tests pass)

Commits exist:
- `a4a68f0` — Task 1 feat commit
- `56afa0b` — Task 2 test commit

## Self-Check: PASSED

---
*Phase: 56-learn-mode-ami-snapshot-and-lifecycle-management*
*Completed: 2026-04-26*
