---
phase: 02-core-provisioning-security-baseline
plan: "03"
subsystem: cli-provisioning-commands
tags: [go, cobra, cli, provisioning, terragrunt, aws-sdk-v2, tdd]
dependency_graph:
  requires:
    - pkg/compiler/compiler.go
    - pkg/terragrunt/runner.go
    - pkg/terragrunt/sandbox.go
    - pkg/aws/client.go
    - pkg/aws/discover.go
    - pkg/aws/spot.go
    - internal/app/config/config.go
  provides:
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go
  affects:
    - Phase 3 (sidecar enforcement) — create/destroy are the primary operator interface throughout
tech_stack:
  added: []
  patterns:
    - "NewXxxCmd(cfg *config.Config) *cobra.Command — established Cobra DI pattern applied to create and destroy"
    - "Early validation before provisioning — profile validity and AWS credentials checked before any AWS or filesystem work"
    - "findRepoRoot() — go.mod anchor walk for locating repo root regardless of cwd"
    - "sandboxIDPattern regexp — compile-time regex for sb-[a-f0-9]{8} format enforcement"
    - "Integration-style CLI tests via buildKM() — full binary compiled in test, invoked via exec.Command"
key_files:
  created:
    - internal/app/cmd/create.go
    - internal/app/cmd/create_test.go
    - internal/app/cmd/destroy.go
    - internal/app/cmd/destroy_test.go
    - .gitignore
  modified:
    - internal/app/cmd/root.go
decisions:
  - "findRepoRoot() walks up from runtime.Caller(0) source path then falls back to cwd — works in both tests and production binary without environment variables"
  - "AWS credential validation (STS GetCallerIdentity) is the gate between profile parsing and compilation — compilation never runs against bad credentials"
  - "destroy reconstructs minimal sandbox dir from _template when local dir missing — service.hcl with sandbox_id only is sufficient for Terragrunt state key resolution"
  - "Spot termination failure is a warning not a fatal error — destroy proceeds anyway since the instance may already be terminated or the spot request already cancelled"
metrics:
  duration_minutes: 8
  tasks_completed: 2
  tasks_total: 2
  files_created: 5
  files_modified: 1
  completed_date: "2026-03-21"
requirements_fulfilled:
  - PROV-01
  - PROV-02
  - NETW-05
  - NETW-07
---

# Phase 2 Plan 03: km create and km destroy CLI Commands Summary

**km create and km destroy Cobra commands wiring the compiler, terragrunt runner, and AWS SDK helpers into the primary operator interface for sandbox provisioning and teardown**

## What Was Built

### internal/app/cmd/create.go — km create command

`NewCreateCmd(cfg *config.Config) *cobra.Command` following the established Cobra DI pattern:

**Flags:**
- `--on-demand` (bool, default false) — overrides `spot: true` in the profile for on-demand instances
- `--aws-profile` (string, default "klanker-terraform") — AWS CLI profile to use

**Workflow (runCreate):**
1. Read profile file from disk
2. Parse profile; resolve `extends` inheritance chain if present
3. Run schema validation (on raw child bytes) + semantic validation (on merged profile)
4. Load AWS config via `awspkg.LoadAWSConfig()` and validate credentials via STS pre-flight
5. Compile profile into Terragrunt artifacts via `compiler.Compile()`
6. Create sandbox directory via `terragrunt.CreateSandboxDir()`
7. Populate sandbox directory via `terragrunt.PopulateSandboxDir()`
8. Run `runner.Apply()` — streams terragrunt output in real time to terminal
9. On failure: cleanup local sandbox dir (does not run destroy — partially created resources require manual cleanup)

**Security notes documented in code comments:**
- NETW-05 (IMDSv2): enforced at Terraform module level via `http_tokens = "required"` — no create command code needed
- NETW-07 (SOPS): decryption at provision time via `site.hcl run_cmd("sops", "--decrypt", ...)`; user-data decrypts at boot via IAM role

### internal/app/cmd/destroy.go — km destroy command

`NewDestroyCmd(cfg *config.Config) *cobra.Command` following the same DI pattern:

**Flags:**
- `--aws-profile` (string, default "klanker-terraform") — AWS CLI profile to use
- `--force` (bool, default false) — skip confirmation prompt (future use; currently always proceeds)

**Workflow (runDestroy):**
1. Validate sandbox ID format via `sandboxIDPattern` regexp (`^sb-[a-f0-9]{8}$`) — rejects before any AWS calls
2. Load AWS config and validate credentials
3. Discover sandbox via `awspkg.FindSandboxByID()` — returns `ErrSandboxNotFound` if unknown
4. Locate or reconstruct sandbox directory (from template if missing locally)
5. Run `runner.Output()` to get Terraform output; extract `spot_instance_id` if present
6. If EC2 spot: `awspkg.TerminateSpotInstance()` explicitly terminates instance before destroy (avoids Pitfall 1 — `aws_spot_instance_request` destroy cancels spot request but leaves instance running)
7. Run `runner.Destroy()` — streams terragrunt output in real time
8. Cleanup local sandbox directory on success

### .gitignore

Added `infra/live/sandboxes/sb-*/` to gitignore so per-sandbox generated directories are not committed. The `_template/` directory is explicitly preserved.

### root.go — command registration

`NewCreateCmd` and `NewDestroyCmd` registered in `NewRootCmd` via `AddCommand`.

### Test Coverage

12 tests across `internal/app/cmd/` package, all passing:
- 4 tests for create: flag registration, no-args error, invalid path error, workflow progression
- 4 tests (+6 subtests) for destroy: flag registration, no-args error, invalid ID format (6 cases), valid ID format accepted

## Deviations from Plan

None — plan executed exactly as written.

The plan noted that NETW-05 and NETW-07 are handled at the Terraform module level, not in the create command. This was confirmed during implementation and documented in code comments.

## Self-Check: PASSED

Files verified:
- internal/app/cmd/create.go: FOUND
- internal/app/cmd/create_test.go: FOUND
- internal/app/cmd/destroy.go: FOUND
- internal/app/cmd/destroy_test.go: FOUND
- .gitignore: FOUND
- internal/app/cmd/root.go: modified, create and destroy registered

Commits verified:
- 39e9a11: test(02-03): add failing tests for km create command [RED]
- 420a2ae: feat(02-03): implement km create command [GREEN Task 1]
- 729ba3c: test(02-03): add failing tests for km destroy command [RED]
- 37305f4: feat(02-03): implement km destroy command [GREEN Task 2]

Full test suite: all packages pass (go test ./... -count=1).
