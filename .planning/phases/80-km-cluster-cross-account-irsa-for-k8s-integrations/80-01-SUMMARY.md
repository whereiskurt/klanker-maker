---
phase: 80-km-cluster-cross-account-irsa-for-k8s-integrations
plan: 01
subsystem: testing
tags: [go, testing, tdd, cluster, irsa, k8s, wave0]

# Dependency graph
requires: []
provides:
  - Wave 0 test scaffolding for km cluster subcommand (six skeletons in cluster_test.go)
  - mockClusterRunner struct with Plan/Apply/Destroy/Reconfigure/Output method stubs
  - TestClustersField skeleton in config_clusters_test.go for Clusters field round-trip
  - wave_0_complete: true in 80-VALIDATION.md
affects:
  - 80-04-PLAN (must add ClusterConfig type + Clusters field to satisfy TestClustersField)
  - 80-05-PLAN (must wire newClusterRunner + persistClustersConfigFunc seams to satisfy all six cluster_test.go skeletons)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Wave 0 t.Skip skeleton pattern: tests compile and skip until production code lands"
    - "mockClusterRunner as local test double (avoids mutating init_test.go's mockRunner)"
    - "Plan 80-05 seam pattern documented in comments: newClusterRunner + persistClustersConfigFunc package-level vars"

key-files:
  created:
    - internal/app/cmd/cluster_test.go
    - internal/app/config/config_clusters_test.go
  modified:
    - .planning/phases/80-km-cluster-cross-account-irsa-for-k8s-integrations/80-VALIDATION.md

key-decisions:
  - "mockClusterRunner is local to cluster_test.go, NOT added to init_test.go's mockRunner, to avoid touching unrelated tests"
  - "config_clusters_test.go uses package config_test (external) mirroring config_test.go, not package config (internal) like config_stream_table_test.go"
  - "TestGenerateClusterHCL and TestPersistClusters function names kept as specified in must_haves even though -run TestCluster regex does not match them; all six tests verified individually"

patterns-established:
  - "Wave 0 scaffold: create test skeletons with t.Skip bodies before production code lands"
  - "mockClusterRunner struct: PlanCalled bool, Applied/Destroyed/Reconfigured []string, OutputCalled bool — fields Plan 80-05 expects"
  - "Comment-block contracts inside skip bodies: enumerate exact injection steps for Plan 80-05 executor"

requirements-completed:
  - operator-feature-80

# Metrics
duration: 4min
completed: 2026-05-11
---

# Phase 80 Plan 01: Wave 0 Test Scaffold Summary

**Two-file Wave 0 Nyquist scaffold establishing the mockClusterRunner (Plan/Apply/Destroy/Reconfigure/Output) contract and six t.Skip test skeletons that subsequent plans (80-04, 80-05) must satisfy**

## Performance

- **Duration:** 4 min
- **Started:** 2026-05-11T23:26:30Z
- **Completed:** 2026-05-11T23:30:30Z
- **Tasks:** 2
- **Files modified:** 3 (2 created, 1 updated)

## Accomplishments

- Created `internal/app/cmd/cluster_test.go` with `mockClusterRunner` struct and six test skeletons
- Created `internal/app/config/config_clusters_test.go` with `TestClustersField` round-trip skeleton
- Set `wave_0_complete: true` in `80-VALIDATION.md`
- All files compile clean, `go build ./...` exits 0, `go vet` is clean

## Task Commits

Each task was committed atomically:

1. **Task 1: Create cluster_test.go skeleton with mockClusterRunner** - `031a49b` (test)
2. **Task 2: Create config_clusters_test.go with TestClustersField** - `bf1dc94` (test)

## mockClusterRunner Struct Definition (for Plan 80-05)

```go
// In internal/app/cmd/cluster_test.go (package cmd_test)
type mockClusterRunner struct {
    PlanCalled     bool
    Applied        []string
    Destroyed      []string
    Reconfigured   []string
    OutputCalled   bool
    OutputResult   map[string]interface{}
    ApplyErr       error
    PlanErr        error
    DestroyErr     error
    ReconfigureErr error
    OutputErr      error
}

// Satisfies ClusterRunner interface Plan 80-05 will declare:
func (m *mockClusterRunner) Plan(_ context.Context, _ string) error
func (m *mockClusterRunner) Apply(_ context.Context, dir string) error
func (m *mockClusterRunner) Destroy(_ context.Context, dir string) error
func (m *mockClusterRunner) Reconfigure(_ context.Context, dir string) error
func (m *mockClusterRunner) Output(_ context.Context, _ string) (map[string]interface{}, error)
```

## Exact Test Function Signatures (contract for Plans 80-04 and 80-05)

### internal/app/cmd/cluster_test.go (package cmd_test)

```
func TestGenerateClusterHCL(t *testing.T)         — cmd.GenerateClusterHCL must substitute 4 placeholders
func TestClusterAdd(t *testing.T)                  — dryRun=false: Apply+Output called; dryRun=true: Plan called only
func TestClusterList(t *testing.T)                 — list cobra command prints cluster NAMEs
func TestClusterRm(t *testing.T)                   — Destroy called; entry removed from cfg.Clusters
func TestPersistClusters(t *testing.T)             — merges clusters into km-config.yaml without clobbering other keys
func TestClusterAddPersistFailure(t *testing.T)    — persist error → non-nil err with "km cluster rm" + no auto-destroy
```

### internal/app/config/config_clusters_test.go (package config_test)

```
func TestClustersField(t *testing.T)               — config.Load() from km-config.yaml with clusters: list round-trips all 5 fields
```

## mockRunner vs. mockClusterRunner Decision

`init_test.go`'s `mockRunner` exposes only `Apply` and `Output` methods (no `Plan`, `Destroy`, `Reconfigure`). The cluster surface requires all five methods, so a local `mockClusterRunner` was defined in `cluster_test.go` rather than mutating `init_test.go`. This avoids breaking the init test's `RunnerInterface` assumption and keeps the two test doubles independent.

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/internal/app/cmd/cluster_test.go` - Six test skeletons + mockClusterRunner (298 lines)
- `/Users/khundeck/working/klankrmkr/internal/app/config/config_clusters_test.go` - TestClustersField skeleton (88 lines)
- `/Users/khundeck/working/klankrmkr/.planning/phases/80-km-cluster-cross-account-irsa-for-k8s-integrations/80-VALIDATION.md` - wave_0_complete: true

## Decisions Made

1. **mockClusterRunner is local, not extending init_test.go's mockRunner** — init_test.go's mockRunner has Apply + Output only; adding Plan/Destroy/Reconfigure would change the RunnerInterface that init tests compile against. Cleanest approach: separate struct.
2. **package config_test (external)** — config_clusters_test.go uses the same package declaration as config_test.go, exercising config.Load() through its public surface rather than bypassing viper internals.
3. **Function names kept as per plan's must_haves** — TestGenerateClusterHCL and TestPersistClusters don't match `-run TestCluster` regex; they're verified individually and all six exist as required.

## Deviations from Plan

None - plan executed exactly as written. The only observation: `-run TestCluster` pattern matches 4 of 6 tests (not `TestGenerateClusterHCL` or `TestPersistClusters`); all 6 are verified individually and all 6 are present in cluster_test.go as required.

## Issues Encountered

Minor compile error in first cluster_test.go draft: referenced `cmd.App` and `cmd.GenerateClusterHCL` which don't exist yet (Plan 80-05 will create them). Fixed by removing forward references from the skeleton bodies — all contract information preserved in comment blocks instead.

## Self-Check Verification

- `internal/app/cmd/cluster_test.go` — FOUND
- `internal/app/config/config_clusters_test.go` — FOUND  
- `031a49b` (Task 1 commit) — FOUND
- `bf1dc94` (Task 2 commit) — FOUND
- `go build ./...` — PASSES
- `go vet ./internal/app/cmd/...` — CLEAN
- `go vet ./internal/app/config/...` — CLEAN

## Self-Check: PASSED

## Next Phase Readiness

- Wave 2+ plans can now cite `go test ./internal/app/cmd/ -run TestCluster -v` as a valid test command
- Plan 80-04 must add `ClusterConfig` type + `Clusters []ClusterConfig` to Config struct
- Plan 80-05 must implement `NewClusterCmd`, `GenerateClusterHCL`, `RunClusterAdd`, `RunClusterRm`, `PersistClustersConfig`, and expose `NewClusterRunnerFunc` + `PersistClustersConfigFunc` seams
- mockClusterRunner in cluster_test.go is ready to wire via those seams the moment Plan 80-05 ships

---
*Phase: 80-km-cluster-cross-account-irsa-for-k8s-integrations*
*Completed: 2026-05-11*
