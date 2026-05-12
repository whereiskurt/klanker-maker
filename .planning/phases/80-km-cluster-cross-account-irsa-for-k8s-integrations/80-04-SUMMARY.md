---
phase: 80-km-cluster-cross-account-irsa-for-k8s-integrations
plan: "04"
subsystem: config
tags: [config, clusters, irsa, viper, tdd]
dependency_graph:
  requires: [80-01]
  provides: [ClusterConfig struct, Config.Clusters field, viper merge wiring]
  affects: [80-05-cluster-cli]
tech_stack:
  added: []
  patterns: [viper UnmarshalKey, mapstructure slice decode]
key_files:
  created: []
  modified:
    - internal/app/config/config.go
    - internal/app/config/config_clusters_test.go
decisions:
  - "ClusterConfig struct placed above Config struct (consistent with file conventions)"
  - "Clusters field appended at end of Config struct after ContainerSubstratesEnabled"
  - "v.UnmarshalKey used (not GetStringSlice) because slice contains structs, not plain strings"
  - "v.SetDefault clusters to []interface{}{} ensures nil-safe empty slice when key absent"
  - "TestClustersField restructured as two subtests: round-trip + absent-key compat"
metrics:
  duration: "142s"
  completed_date: "2026-05-11"
  tasks_completed: 2
  files_modified: 2
---

# Phase 80 Plan 04: ClusterConfig Struct + Viper Wiring Summary

One-liner: ClusterConfig struct with 5 IRSA fields wired into Config.Clusters via viper UnmarshalKey with SetDefault guard, TestClustersField unskipped with round-trip + absent-key subtests both passing.

## Objective

Add the `ClusterConfig` Go struct and `Config.Clusters []ClusterConfig` field that Plan 80-05
(km cluster CLI) will read and persist. Wire viper so the `clusters:` key in km-config.yaml
populates the field at runtime, fixing RESEARCH.md Pitfall 6 (absent SetDefault + merge key
causes silently empty Clusters slice even when the YAML has entries).

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Add ClusterConfig struct + Config.Clusters + viper wiring | badecb9 | internal/app/config/config.go |
| 2 | Un-skip TestClustersField with real assertions | dff6733 | internal/app/config/config_clusters_test.go |

## Implementation Details

### ClusterConfig placement in config.go

`ClusterConfig` struct added at lines just above the `Config` struct definition (after package
header / imports). Five fields with dual `mapstructure` + `yaml` tags matching the locked
CONTEXT.md definition verbatim:

```go
type ClusterConfig struct {
    Name            string `mapstructure:"name"              yaml:"name"`
    OIDCProviderARN string `mapstructure:"oidc_provider_arn" yaml:"oidc_provider_arn"`
    Namespace       string `mapstructure:"namespace"         yaml:"namespace"`
    ServiceAccount  string `mapstructure:"service_account"   yaml:"service_account"`
    RoleARN         string `mapstructure:"role_arn"          yaml:"role_arn"`
}
```

`Config.Clusters` added at the end of the Config struct, after `ContainerSubstratesEnabled`.

### Viper merge wiring (three edits inside Load())

1. **SetDefault** (next to other SetDefault calls, after `email_subdomain`):
   ```go
   v.SetDefault("clusters", []interface{}{})
   ```

2. **Merge key list** (`"clusters"` appended after `"container_substrates_enabled"`):
   ```go
   "container_substrates_enabled",
   "clusters",
   ```

3. **UnmarshalKey** (after ContainerSubstratesEnabled tri-state block):
   ```go
   if err := v.UnmarshalKey("clusters", &cfg.Clusters); err != nil {
       return nil, fmt.Errorf("unmarshal clusters: %w", err)
   }
   ```

### config.Load() signature changes

None — `Load() (*Config, error)` is unchanged. The new `UnmarshalKey` call follows the
existing error-return pattern.

### TestClustersField redirect mechanism

Uses `os.Chdir(t.TempDir())` with `t.Cleanup(func() { os.Chdir(orig) })` — identical to
the pattern in `config_test.go`. No env var or path argument needed; viper's `v2.AddConfigPath(".")`
picks up `km-config.yaml` from the chdir'd temp directory.

Two subtests:
- `"single cluster entry round-trips"`: writes clusters: list, asserts all 5 fields
- `"absent clusters key yields empty slice with no error"`: no clusters: key, asserts len==0 and nil error

## Verification Results

```
$ go test ./internal/app/config/ -run TestClustersField -v
=== RUN   TestClustersField
=== RUN   TestClustersField/single_cluster_entry_round-trips
=== RUN   TestClustersField/absent_clusters_key_yields_empty_slice_with_no_error
--- PASS: TestClustersField (0.00s)
    --- PASS: TestClustersField/single_cluster_entry_round-trips (0.00s)
    --- PASS: TestClustersField/absent_clusters_key_yields_empty_slice_with_no_error (0.00s)
PASS
ok      github.com/whereiskurt/klanker-maker/internal/app/config        0.329s

$ make build
Built: km v0.2.590 (dff6733)

$ go build ./... && go vet ./...
[clean — pre-existing IPv6 vet warning in sidecars/http-proxy is out of scope]
```

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- FOUND: internal/app/config/config.go
- FOUND: internal/app/config/config_clusters_test.go
- FOUND: .planning/phases/80-km-cluster-cross-account-irsa-for-k8s-integrations/80-04-SUMMARY.md
- FOUND: commit badecb9 (Task 1)
- FOUND: commit dff6733 (Task 2)
