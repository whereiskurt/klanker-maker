---
phase: 80-km-cluster-cross-account-irsa-for-k8s-integrations
plan: "05"
subsystem: cluster-irsa-cli
tags: [cobra, terragrunt, irsa, k8s, iam, dry-run, config-persistence]
dependency_graph:
  requires: [80-03, 80-04]
  provides: [km-cluster-cmd, ClusterRunner-interface, runner-Plan-method]
  affects: [root.go, pkg/terragrunt/runner.go, internal/app/cmd/cluster.go]
tech_stack:
  added: []
  patterns: [cobra-parent-with-subcommands, newClusterRunner-seam, PersistClustersConfigFunc-seam, strings.NewReplacer-HCL-gen, tabwriter-list-output]
key_files:
  created:
    - internal/app/cmd/cluster.go
  modified:
    - pkg/terragrunt/runner.go
    - internal/app/cmd/root.go
    - internal/app/cmd/cluster_test.go
decisions:
  - "Exported RunClusterAdd/RunClusterRm/GenerateClusterHCL/PersistClustersConfig + seam vars (NewClusterRunnerFunc, PersistClustersConfigFunc) so cmd_test package (external) can inject mocks without unexported gymnastics"
  - "RunClusterAdd takes repoRoot explicitly so tests supply t.TempDir() without changing CWD"
  - "PersistClustersConfig(configPath, clusters) takes configPath explicitly; PersistClustersConfigFunc wraps it with findRepoRoot() for production"
  - "No auto-destroy on persist failure — CONTEXT.md rollback contract preserved; error message includes literal 'km cluster rm' for TestClusterAddPersistFailure assertion"
  - "runner.Plan reuses buildCommand+runCommand factory — identical to Reconfigure, no new fields on Runner struct"
metrics:
  duration_seconds: 630
  completed_date: "2026-05-12"
  tasks_completed: 3
  tasks_total: 3
  files_created: 1
  files_modified: 3
---

# Phase 80 Plan 05: km cluster CLI (add/list/rm) Summary

Land the user-facing `km cluster {add,list,rm}` Cobra command tree with HCL generation, config persistence, region.hcl bootstrap, terragrunt runner integration, and all six unit tests passing; wired into root.go.

## What Was Built

### pkg/terragrunt/runner.go — Plan method (lines 98-107)

Six-line addition immediately after `Reconfigure`:

```go
// Plan runs `terragrunt plan` inside sandboxDir for dry-run preview without
// mutating state. Used by km cluster add --dry-run=true so operators can review
// the IAM role that WOULD be created before flipping --dry-run=false.
func (r *Runner) Plan(ctx context.Context, sandboxDir string) error {
    cmd := r.buildCommand(ctx, sandboxDir, "plan")
    return r.runCommand(cmd)
}
```

Reuses `buildCommand`+`runCommand` factory — identical pattern to `Reconfigure`. No new fields on `Runner` struct.

### internal/app/cmd/cluster.go — 509 lines, 9 functions

| Function | Type | Purpose |
|---|---|---|
| `GenerateClusterHCL` | exported | `strings.NewReplacer` substitution in HCL template; `${...}` HCL interpolations untouched |
| `PersistClustersConfig` | exported | Read km-config.yaml → set `raw["clusters"]` → write back (preserves all other keys) |
| `NewClusterCmd` | exported | Parent cobra.Command + AddCommand for add/list/rm |
| `newClusterAddCmd` | private | 8-flag Cobra command; calls `RunClusterAdd` |
| `newClusterListCmd` | private | Cobra command; calls `runClusterList` |
| `newClusterRmCmd` | private | Cobra command; calls `RunClusterRm` |
| `RunClusterAdd` | exported | Full add flow: idempotency → pre-flight → ExportConfigEnvVars → region.hcl bootstrap → HCL write → Plan OR Apply → Output → persist |
| `runClusterList` | private | tabwriter table with NAME/NAMESPACE/SERVICE ACCOUNT/ROLE ARN |
| `RunClusterRm` | exported | Find → pre-flight → ExportConfigEnvVars → Reconfigure → Destroy → remove from cfg → persist → RemoveAll |

**Seam vars (exported):**
- `NewClusterRunnerFunc` — factory for `ClusterRunner`; tests inject `mockClusterRunner`
- `PersistClustersConfigFunc` — wraps `PersistClustersConfig` with `findRepoRoot()`; tests inject failure

**ClusterRunner interface (exported):**
```go
type ClusterRunner interface {
    Plan(ctx context.Context, dir string) error
    Apply(ctx context.Context, dir string) error
    Destroy(ctx context.Context, dir string) error
    Reconfigure(ctx context.Context, dir string) error
    Output(ctx context.Context, dir string) (map[string]interface{}, error)
}
```

### internal/app/cmd/root.go — line 87

```go
root.AddCommand(NewClusterCmd(cfg))
```

Inserted after `NewVSCodeCmd(cfg)` (line 86), before the `atCmd` block (line 89).

### mockClusterRunner in cluster_test.go

```go
type mockClusterRunner struct {
    PlanCalled     bool
    Applied        []string
    Destroyed      []string
    Reconfigured   []string
    OutputCalled   bool
    OutputResult   map[string]interface{}
    ApplyErr, PlanErr, DestroyErr, ReconfigureErr, OutputErr error
}
```

All five `ClusterRunner` interface methods implemented. `Apply` appends `dir` to `Applied` (unless `ApplyErr` set); `Plan` sets `PlanCalled`; `Output` sets `OutputCalled` and returns `OutputResult`.

## Test Results

All 6 tests pass with zero SKIPs:

```
--- PASS: TestGenerateClusterHCL   (0.00s)
--- PASS: TestClusterAdd           (2.17s)
    --- PASS: TestClusterAdd/dryRun=false_applies_and_persists
    --- PASS: TestClusterAdd/dryRun=true_plans_only
    --- PASS: TestClusterAdd/idempotency:_existing_name_exits_0
--- PASS: TestClusterList          (0.00s)
--- PASS: TestClusterRm            (1.13s)
--- PASS: TestPersistClusters      (0.00s)
--- PASS: TestClusterAddPersistFailure (0.95s)
```

`go test ./pkg/terragrunt/...` also passes after Plan method addition.

## km cluster add --help (audit trail)

```
Provision a cross-account IRSA role for a k8s cluster

Flags:
      --aws-profile string         AWS profile for terragrunt apply (default "klanker-application")
      --dry-run                    plan only; set --dry-run=false to apply (default true)
  -h, --help                       help for add
      --name string                cluster name (required)
      --namespace string           k8s namespace allowed to assume the role (default "*")
      --oidc-provider-arn string   OIDC provider ARN in the cluster's AWS account (required)
      --region string              AWS region for the role (default "us-east-1")
      --service-account string     k8s service account name allowed to assume the role (default "km")
      --verbose                    stream terragrunt output
```

## Smoke Test Result

`./km cluster add --name smoke --oidc-provider-arn arn:aws:iam::123456789012:oidc-provider/fake.example.com --dry-run=true` ran a real terragrunt plan against the klanker AWS account and produced a valid IAM role plan with correct trust policy:

- `aws_iam_role.cluster_irsa` with `AssumeRoleWithWebIdentity` trust
- OIDC host extracted from ARN: `fake.example.com`
- Subject condition: `StringLike "system:serviceaccount:*:km"`
- Generated `infra/live/use1/cluster-smoke/terragrunt.hcl` with `infra/modules//cluster-irsa/v1.0.0` double-slash source

## Deviations from Plan

### Design Adaptation: Exported function signatures

The plan described `runClusterAdd` as private. Since `cluster_test.go` is `package cmd_test` (external), direct calls to private functions aren't possible. I exported `RunClusterAdd`, `RunClusterRm`, `GenerateClusterHCL`, and `PersistClustersConfig` so tests can call them directly with injected `repoRoot` parameters. This avoids `os.Chdir` gymnastics in tests and produces a cleaner API. `NewClusterRunnerFunc` and `PersistClustersConfigFunc` are also exported for the same reason.

This is a minor naming deviation (uppercase vs lowercase) with no behavioral difference. The Cobra command handlers remain private and call the exported functions.

### Design Adaptation: PersistClustersConfig signature

The plan described `persistClustersConfig(clusters []config.ClusterConfig)` relying on `findRepoRoot()` internally. I split it into:
- `PersistClustersConfig(configPath string, clusters []config.ClusterConfig)` — exported, takes explicit path (testable without CWD manipulation)
- `PersistClustersConfigFunc` — closure that wraps the above with `findRepoRoot()` (used by production code)

This enables `TestPersistClusters` to pass `cfgPath` directly and `TestClusterAddPersistFailure` to inject a failure without touching the filesystem.

## Commits

| Hash | Message |
|---|---|
| `09d2a09` | feat(80-05): add runner.Plan method and NewClusterCmd with add/list/rm subcommands |
| `a314b7a` | feat(80-05): un-skip all 6 cluster tests and register NewClusterCmd in root.go |
| `ce2051d` | feat(80-05): verify km binary surfaces cluster command tree |
