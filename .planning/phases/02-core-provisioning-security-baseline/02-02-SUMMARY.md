---
phase: 02-core-provisioning-security-baseline
plan: "02"
subsystem: infrastructure-orchestration
tags: [terragrunt, aws-sdk-v2, sandbox-lifecycle, spot-instance, tag-discovery, tdd]
dependency_graph:
  requires:
    - infra/live/sandboxes/_template/terragrunt.hcl
    - pkg/profile/types.go
  provides:
    - pkg/terragrunt/runner.go
    - pkg/terragrunt/sandbox.go
    - pkg/aws/client.go
    - pkg/aws/discover.go
    - pkg/aws/spot.go
  affects:
    - Plan 02-03 (CLI create/destroy commands wire these packages)
tech_stack:
  added:
    - github.com/aws/aws-sdk-go-v2/config v1.32.12
    - github.com/aws/aws-sdk-go-v2/service/ec2 v1.296.0
    - github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi v1.31.9
    - github.com/aws/aws-sdk-go-v2/service/sts v1.41.9
    - github.com/aws/aws-sdk-go-v2/service/ssm v1.68.3
  patterns:
    - Interface-based AWS API mocking (TagAPI, EC2API) for unit testing without real AWS calls
    - BuildXxxCommand pattern exposes exec.Cmd for inspection in tests without running binaries
    - ErrSandboxNotFound sentinel error for typed error handling in destroy command
    - ec2.NewInstanceTerminatedWaiter with 5-minute timeout for clean spot termination
key_files:
  created:
    - pkg/terragrunt/runner.go
    - pkg/terragrunt/sandbox.go
    - pkg/terragrunt/runner_test.go
    - pkg/aws/client.go
    - pkg/aws/discover.go
    - pkg/aws/spot.go
    - pkg/aws/discover_test.go
  modified:
    - go.mod
    - go.sum
decisions:
  - "BuildXxxCommand methods expose exec.Cmd for test inspection without executing terragrunt — preserves testability while keeping Apply/Destroy simple"
  - "TerminateSpotInstance waiter is only invoked when client is *ec2.Client (concrete type assertion) — mock EC2API passes termination call through without waiting, keeping tests fast"
  - "ErrSandboxNotFound defined as package-level sentinel — callers use errors.Is() for typed handling in destroy path"
  - "GetSpotInstanceID handles both Terraform output format (nested {value: ...}) and raw string for robustness"
metrics:
  duration_minutes: 4
  tasks_completed: 2
  tasks_total: 2
  files_created: 7
  files_modified: 2
  completed_date: "2026-03-22"
requirements_fulfilled:
  - PROV-02
---

# Phase 2 Plan 02: Terragrunt Runner and AWS SDK Helpers Summary

Terragrunt runner with real-time streaming and sandbox directory lifecycle, plus AWS SDK v2 helpers for tag-based sandbox discovery and clean spot instance termination before Terraform destroy.

## What Was Built

### pkg/terragrunt/ — Sandbox Orchestration Layer

**runner.go** — `Runner` struct wraps the Terragrunt binary:
- `Apply(ctx, sandboxDir)` streams `terragrunt apply -auto-approve` to os.Stdout/os.Stderr in real time
- `Destroy(ctx, sandboxDir)` streams `terragrunt destroy -auto-approve` in real time
- `Output(ctx, sandboxDir)` captures `terragrunt output -json` and parses it as a `map[string]interface{}`
- `BuildApplyCommand`, `BuildDestroyCommand`, `BuildOutputCommand` expose the `exec.Cmd` for test inspection without running terragrunt
- `AWS_PROFILE` env var injected into every command; `cmd.Dir` set to sandboxDir for workspace isolation

**sandbox.go** — Sandbox directory lifecycle:
- `CreateSandboxDir(repoRoot, sandboxID)` creates `infra/live/sandboxes/<sandboxID>/` and copies `_template/terragrunt.hcl` into it
- `PopulateSandboxDir(sandboxDir, serviceHCL, userData)` writes `service.hcl` (always) and `user-data.sh` (only when non-empty)
- `CleanupSandboxDir(sandboxDir)` calls `os.RemoveAll` for complete teardown

### pkg/aws/ — AWS SDK Helpers

**client.go** — Config loading:
- `LoadAWSConfig(ctx, profile)` loads config with `WithSharedConfigProfile` and hardcoded `us-east-1` region
- `ValidateCredentials(ctx, cfg)` calls STS `GetCallerIdentity` as a pre-flight check before any provisioning

**discover.go** — Tag-based sandbox discovery:
- `TagAPI` interface for `resourcegroupstaggingapi.Client` method subset (enables mocking)
- `FindSandboxByID(ctx, client, sandboxID)` queries `km:sandbox-id` tag filter; returns `*SandboxLocation` or `ErrSandboxNotFound`
- `SandboxLocation` struct with `SandboxID`, `S3StatePath` (`tf-km/sandboxes/<id>`), `ResourceCount`, `ResourceARNs`
- `StatePath()` method returns the deterministic state path string

**spot.go** — Spot instance termination:
- `EC2API` interface for `ec2.Client` method subset (enables mocking)
- `TerminateSpotInstance(ctx, client, instanceID)` calls EC2 `TerminateInstances` then waits via `ec2.NewInstanceTerminatedWaiter` (5-minute timeout, production path only via type assertion)
- `GetSpotInstanceID(terragruntOutput)` extracts `spot_instance_id` from Terraform output JSON format (`{"value": "i-0abc...", "type": "string"}`)

### Test Coverage

12 tests across two packages, all passing without real AWS or Terragrunt calls:
- 7 tests in `pkg/terragrunt`: sandbox lifecycle (create/populate/cleanup) + command construction (apply/destroy/output)
- 5 tests in `pkg/aws`: tag discovery found/not-found, StatePath(), GetSpotInstanceID happy/missing paths

## Deviations from Plan

### Pre-existing Issue (Out of Scope)

**pkg/compiler build failures** — The `pkg/compiler` package was already broken before plan 02-02. Before my changes it failed due to a missing `go.sum` entry for `github.com/google/uuid`. After adding AWS SDK dependencies (which update go.mod/go.sum), it now fails with `undefined: SGRule` and `undefined: IAMSessionPolicy` in `security.go`. These types need to be defined in the compiler package — work that belongs to plan 02-01. Documented in `deferred-items.md`.

No plan-authored deviations — plan executed exactly as written.

## Self-Check: PASSED

Files verified on disk:
- pkg/terragrunt/runner.go: FOUND
- pkg/terragrunt/sandbox.go: FOUND
- pkg/terragrunt/runner_test.go: FOUND
- pkg/aws/client.go: FOUND
- pkg/aws/discover.go: FOUND
- pkg/aws/spot.go: FOUND
- pkg/aws/discover_test.go: FOUND

Commits verified:
- 37ab817: test(02-02): add failing tests for terragrunt runner and sandbox lifecycle [RED]
- 0df28d7: feat(02-02): implement terragrunt runner and sandbox directory lifecycle [GREEN Task 1]
- a26bcf5: test(02-02): add failing tests for AWS discovery and spot instance helpers [RED Task 2]
- 55f9e40: feat(02-02): implement AWS SDK helpers for discovery, spot termination, and config [GREEN Task 2]
