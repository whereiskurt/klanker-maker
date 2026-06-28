---
phase: 124-platform-wide-az-failover-and-capacity-feasibility-for-ec2-launches
plan: "04"
subsystem: capacity
tags: [capacity, az-ranking, gpu-quota, km-capacity, tdd]
dependency_graph:
  requires: [124-01, 124-02]
  provides: [pkg/capacity.RankAZs, km capacity command, capacity-aware AZ ordering in km create]
  affects: [internal/app/cmd/create.go, pkg/capacity/rankaz.go]
tech_stack:
  added: [servicequotas SDK wired into create.go via sqsvc alias]
  patterns: [TDD RED/GREEN, narrow interface mocking, tabwriter table rendering]
key_files:
  created:
    - pkg/capacity/rankaz_test.go
    - internal/app/cmd/capacity.go
    - internal/app/cmd/capacity_test.go
  modified:
    - pkg/capacity/rankaz.go (stub replaced with full implementation; IsGPUFamily exported)
    - internal/app/cmd/create.go (RankAZs wired pre-loop; servicequotas import)
    - internal/app/cmd/root.go (newCapacityCmd registered)
decisions:
  - Stale ICE (expired >45min window) yields VerdictLikely, not VerdictUnknown — stale ICE = possible recovery
  - nil CapacityEntry (store unavailable) yields VerdictUnknown; zero-value entry yields VerdictLikely
  - isGPUFamily exported as IsGPUFamily for use in capacity.go command
  - RankAZs skips docker substrate (no AZs to rank)
  - freshICEWindow = time.Duration(capacity.ICETTLSeconds)*time.Second (mirrors DDB TTL exactly)
metrics:
  duration: "853s"
  completed: "2026-06-28"
  tasks: 3
  files: 6
---

# Phase 124 Plan 04: Capacity-Aware AZ Ranking + km capacity Command Summary

Full RankAZs (offerings filter, GPU quota gate, azPreference, sticky last-success, ICE deprioritize) wired into km create pre-loop; km capacity feasibility command with honest per-AZ verdict table.

## What Was Built

### Task 1: Implement capacity-aware RankAZs (TDD)

Replaced the Phase 124-01 stub in `pkg/capacity/rankaz.go` with the full implementation:

- **DescribeAZOfferings**: calls `DescribeInstanceTypeOfferings` (LocationType=availability-zone, filters by location + instance-type); drops AZs not in the offerings result; on API error warns and falls back to allAZs (non-fatal).
- **IsGPUFamily**: detects "g*" and "vt*" prefixes (case-insensitive); exported for use in the capacity command.
- **GetGPUVCPUQuota**: queries Service Quotas L-DB2E81BA for the regional GPU vCPU headroom.
- **GPU quota gate**: if `IsGPUFamily(instanceType)` and headroom == 0 → returns `*QuotaError{QuotaCode: "L-DB2E81BA", Headroom: 0}` immediately. Non-GPU types skip the quota call entirely.
- **azPreference merge**: `intersect(azPreference, offered)` placed first; remaining offered AZs follow.
- **rankScore sorting**: last-success AZ first (score=2), no-signal middle (score=0), fresh-ICE last (score=-1); alphabetical tiebreak for stability.

Five tests in `pkg/capacity/rankaz_test.go` following TDD RED→GREEN:
- `TestRankAZs_DropsNonOffering`
- `TestRankAZs_GPUQuotaBlock`
- `TestRankAZs_AZPreference`
- `TestRankAZs_ICEStickySuccess`
- `TestRankAZs` (subtests: non-GPU skips quota; offerings error falls back)

### Task 2: Wire RankAZs into km create

Inserted a `RankAZs` call in `internal/app/cmd/create.go` after `maxAttempts` is set and before the BDM/snapshot pre-flight, applicable to all non-docker substrates with at least one AZ:

- Creates per-region EC2 and ServiceQuotas clients (with `o.Region = region`)
- Creates `DynamoCapacityStore` from `cfg.GetCapacityTableName()`
- Passes `resolvedProfile.Spec.Runtime.AZPreference` as the preference hint
- On `*QuotaError`: prints the L-DB2E81BA message and returns error immediately (pre-loop fail-fast)
- On other errors: `log.Warn` and keeps original AZ order
- On success: reorders `network.AvailabilityZones` and reorders `network.PublicSubnets` in lockstep (subnet[i] stays paired with AZ[i])
- Recomputes `maxAttempts = len(ranked)` after ranking (dropped non-offering AZs shrink the loop); on-demand keeps `maxAttempts=1`

### Task 3: km capacity command

New command in `internal/app/cmd/capacity.go`:

```
km capacity <profile.yaml>               # resolve instance type from profile
km capacity --type g6e.12xlarge          # explicit type
km capacity --type g6e.12xlarge --region us-west-2
```

**Verdict computation** (`ComputeCapacityVerdict`, exported for tests):
```
not-offered > quota-blocked > recently-dry > likely > unknown
```
- `not-offered`: AZ not in `DescribeInstanceTypeOfferings`
- `quota-blocked`: GPU family + quota headroom == 0
- `recently-dry`: `LastICEAt` within 45-min window
- `likely`: offered, no quota block, no fresh ICE (covers: has success, stale/expired ICE, or no history at all)
- `unknown`: offered but capacity store entry is nil (store unavailable)

**Output** (5-column tab-aligned table):
```
Capacity report: g6e.12xlarge  region: us-east-1

AZ           OFFERED  QUOTA HEADROOM  LAST ICE              LAST SUCCESS          VERDICT
--           -------  --------------  --------              ------------          -------
us-east-1a   yes      96 vCPU         2026-06-28T17:30Z     —                     recently-dry
us-east-1b   yes      96 vCPU         —                     2026-06-28T16:00Z     likely
us-east-1c   no       96 vCPU         —                     —                     not-offered
```

Command registered in `root.go` alongside other subcommands.

**Tests** in `capacity_test.go`:
- `TestCapacityReport`: 10 verdict scenarios covering all 5 verdict values + precedence
- `TestCapacityVerdictNeverAvailable`: regression guard that no constant equals "available"

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Test case `stale_ICE -> likely` exposed wrong verdict logic**
- **Found during:** Task 3 first test run
- **Issue:** `ComputeCapacityVerdict` returned `unknown` for an offered AZ with a stale (expired) ICE entry because the last condition `LastICEAt == nil && LastSuccessAt == nil` didn't cover the stale-ICE case
- **Fix:** Simplified the post-ICE check to `if entry != nil { return VerdictLikely }` — once past the `recently-dry` gate, any non-nil entry means the ICE has expired and the AZ may have recovered
- **Files modified:** `internal/app/cmd/capacity.go`

**2. [Rule 2 - Missing] isGPUFamily needed to be exported**
- **Found during:** Task 3 implementation
- **Issue:** `internal/app/cmd/capacity.go` needs `IsGPUFamily` for the quota column rendering; unexported function inaccessible across packages
- **Fix:** Renamed `isGPUFamily` → `IsGPUFamily` in `pkg/capacity/rankaz.go`; RankAZs updated internally
- **Files modified:** `pkg/capacity/rankaz.go`

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| 1 RED | e1aac054 | test(124-04): add failing RankAZs tests (TDD RED) |
| 1 GREEN | 3027f705 | feat(124-04): implement capacity-aware RankAZs (TDD GREEN) |
| 2 | f667babc | feat(124-04): wire RankAZs into km create pre-loop AZ ordering |
| 3 | 9389a67b | feat(124-04): km capacity feasibility command + export IsGPUFamily |

## Verification Results

- `go test ./pkg/capacity/... -run TestRankAZs -timeout 30s` — PASS (all 5 ranking tests)
- `go test ./internal/app/cmd/... -run 'TestCapacity|TestAZSweepLoop' -timeout 120s` — PASS
- `go build ./...` — PASS (clean build)
