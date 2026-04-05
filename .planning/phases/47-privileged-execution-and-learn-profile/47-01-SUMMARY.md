---
phase: 47-privileged-execution-and-learn-profile
plan: "01"
subsystem: profile-compiler
tags:
  - privileged-execution
  - userdata
  - sandbox-profile
  - learn-profile
dependency_graph:
  requires: []
  provides:
    - ExecutionSpec.Privileged field
    - userdata conditional wheel/sudoers
    - profiles/learn.yaml
  affects:
    - pkg/profile/types.go
    - pkg/compiler/userdata.go
    - pkg/profile/schemas/sandbox_profile.schema.json
tech_stack:
  added: []
  patterns:
    - Go template conditional block ({{- if .Privileged }})
    - JSON Schema additionalProperties enforcement
key_files:
  created:
    - profiles/learn.yaml
    - (tests appended to) pkg/compiler/userdata_test.go
  modified:
    - pkg/profile/types.go
    - pkg/compiler/userdata.go
    - pkg/profile/schemas/sandbox_profile.schema.json
decisions:
  - "Added privileged property to JSON schema execution block to allow validation to pass (schema uses additionalProperties: false)"
  - "Added agent section to learn.yaml (maxConcurrentTasks: 1, taskTimeout: 30m) since schema requires it even for human-driven profiles"
metrics:
  duration: "~3 minutes"
  completed_date: "2026-04-05"
  tasks_completed: 6
  files_modified: 5
---

# Phase 47 Plan 01: Privileged Execution Field + Learn Profile Summary

## One-liner

Added `spec.execution.privileged` boolean to grant sandbox users wheel/sudo access via conditional userdata, and created `profiles/learn.yaml` — a wide-open TLD-based traffic observation profile with enforcement: both and tlsCapture enabled.

## What Was Built

### ExecutionSpec.Privileged (pkg/profile/types.go)

Added `Privileged bool` with `yaml:"privileged,omitempty"` after `RsyncFileList`. Defaults to false; omitempty ensures backward compatibility with all existing profiles.

### userdata Template (pkg/compiler/userdata.go)

Added `Privileged bool` to `userDataParams` struct and wired `params.Privileged = p.Spec.Execution.Privileged` in `generateUserData` after the EFS block.

Updated the sandbox user creation block with a Go template conditional:
- When `Privileged=true`: `useradd` with `-G wheel`, writes `/etc/sudoers.d/sandbox` with `NOPASSWD:ALL`, `chmod 0440`
- When `Privileged=false`: plain `useradd` with no wheel group, no sudoers entry

### Tests (pkg/compiler/userdata_test.go)

- `TestUserdataPrivilegedEnabled` — asserts `-G wheel sandbox`, `NOPASSWD:ALL`, `/etc/sudoers.d/sandbox` present in output
- `TestUserdataPrivilegedDisabled` — asserts `-G wheel`, `NOPASSWD`, `sudoers.d` absent from output

Both pass.

### JSON Schema (pkg/profile/schemas/sandbox_profile.schema.json)

Added `privileged` boolean property to the `execution` schema block. Required because the schema uses `additionalProperties: false` and the validator rejected the field without this entry.

### profiles/learn.yaml

Wide-open profile for traffic observation:
- `privileged: true` for sudo/package install
- `enforcement: both` for eBPF + proxy L7 capture
- Broad TLD suffixes (`.com`, `.org`, `.io`, `.dev`, `.ai`, etc.)
- `tlsCapture: enabled: true` for eBPF TLS uprobes
- `ttl: 2h`, `teardownPolicy: destroy` for safety
- Zero AI budget (`maxSpendUSD: 0.00`) — human-driven observation only
- `agent.maxConcurrentTasks: 1` (required by schema)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical Functionality] Added privileged to JSON schema**
- **Found during:** Task 6 (validation run)
- **Issue:** `km validate profiles/learn.yaml` failed with `spec.execution: additional properties 'privileged' not allowed` because the schema uses `additionalProperties: false` and was missing the new field
- **Fix:** Added `privileged` boolean property to execution schema in `sandbox_profile.schema.json`
- **Files modified:** `pkg/profile/schemas/sandbox_profile.schema.json`
- **Commit:** d4af2c9

**2. [Rule 2 - Missing Critical Functionality] Added required agent section to learn.yaml**
- **Found during:** Task 6 (validation run)
- **Issue:** `km validate profiles/learn.yaml` failed with `spec: missing property 'agent'` — the schema requires `agent.maxConcurrentTasks` and `agent.taskTimeout`
- **Fix:** Added `agent: maxConcurrentTasks: 1, taskTimeout: "30m"` to learn.yaml
- **Files modified:** `profiles/learn.yaml`
- **Commit:** d4af2c9

## Verification Results

All plan verification steps passed:

1. `go test ./pkg/compiler/... -run TestUserdataPrivileged` — PASS (both enabled/disabled tests)
2. `go test ./pkg/profile/... -run TestParse` — PASS (4 parse tests, no regression)
3. `make build && ./km validate profiles/learn.yaml` — `profiles/learn.yaml: valid`

## Self-Check

Verified all artifact claims:
- `pkg/profile/types.go` contains `Privileged` field
- `pkg/compiler/userdata.go` contains `Privileged` in params and template
- `pkg/compiler/userdata_test.go` contains `TestUserdataPrivilegedEnabled` and `TestUserdataPrivilegedDisabled`
- `profiles/learn.yaml` contains `privileged: true`
