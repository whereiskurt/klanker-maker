---
phase: 73-km-vscode-remote-session-via-ssm
plan: "00"
subsystem: vscode-remote
tags: [wave-0, stub-seeding, tdd, sshkey, sshconfig, vscode]
dependency_graph:
  requires: []
  provides:
    - sshkey.GenerateAndWrite signature locked
    - sshconfig.UpsertHost / RemoveHost + HostOptions struct locked
    - cmd.NewVSCodeCmd / newVSCodeCmdInternal cobra shape locked
    - profile.IsVSCodeEnabled + VSCodeEnabled pointer field (stub reference)
    - compiler.VSCodeEnabled + VSCodeSSHPubKey userdata params (stub reference)
  affects:
    - pkg/sshkey (new package)
    - internal/app/cmd (2 new files)
    - pkg/profile/types_test.go (appended)
    - pkg/compiler/userdata_test.go (appended)
tech_stack:
  added:
    - pkg/sshkey package (new; golang.org/x/crypto/ssh imported in test only)
  patterns:
    - Wave 0 stub-seeding with t.Skip + commented-out assertions
    - TDD RED phase: 22 tests all SKIP, Wave 1+ turns them GREEN
key_files:
  created:
    - pkg/sshkey/keygen.go
    - pkg/sshkey/keygen_test.go
    - internal/app/cmd/sshconfig.go
    - internal/app/cmd/sshconfig_test.go
    - internal/app/cmd/vscode.go
    - internal/app/cmd/vscode_test.go
  modified:
    - pkg/profile/types_test.go
    - pkg/compiler/userdata_test.go
decisions:
  - "Test files in internal/app/cmd use package cmd (not cmd_test) for internal-symbol access to HostOptions + vscode internals"
  - "boolPtr helper reused from validate_test.go rather than redeclared in types_test.go to avoid vet redeclaration error"
  - "sshconfig/vscode stubs return errors.New('not implemented') rather than nil — callers get explicit feedback rather than silent no-ops"
metrics:
  duration: 855s
  completed: "2026-05-07"
  tasks_completed: 3
  files_changed: 8
---

# Phase 73 Plan 00: Wave 0 Stub Seeding Summary

Wave 0 stub seeding for Phase 73 (km vscode Remote-SSH over SSM): 8 files
(5 new + 2 appended + 1 new package), 22 failing tests all SKIPping so the
Wave 1 suite stays green while locking the symbol surface for parallel implementation.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | pkg/sshkey stub package + 7 failing tests | f2e4418 | pkg/sshkey/keygen.go, pkg/sshkey/keygen_test.go |
| 2 | sshconfig + vscode stubs + 15 failing tests | b4829ff | internal/app/cmd/sshconfig.go, sshconfig_test.go, vscode.go, vscode_test.go |
| 3 | VSCode tests in profile/types + compiler/userdata | 9344e94 | pkg/profile/types_test.go, pkg/compiler/userdata_test.go |

## Symbol Surface Locked for Wave 1

| Symbol | File | Wave 1 Plan |
|--------|------|-------------|
| `GenerateAndWrite(privPath, pubPath, comment string) (string, error)` | pkg/sshkey/keygen.go | 73-01 |
| `IsVSCodeEnabled(cli *CLISpec) bool` | pkg/profile/types.go | 73-02 |
| `CLISpec.VSCodeEnabled *bool` | pkg/profile/types.go | 73-02 |
| `UpsertHost(configPath, alias string, opts HostOptions) error` | internal/app/cmd/sshconfig.go | 73-03 |
| `RemoveHost(configPath, alias string) error` | internal/app/cmd/sshconfig.go | 73-03 |
| `HostOptions{HostName, Port, User, IdentityFile}` | internal/app/cmd/sshconfig.go | 73-03 |
| `userDataParams.VSCodeEnabled bool` | pkg/compiler/userdata.go | 73-04 |
| `userDataParams.VSCodeSSHPubKey string` | pkg/compiler/userdata.go | 73-04 |
| `NewVSCodeCmd(cfg *config.Config) *cobra.Command` | internal/app/cmd/vscode.go | 73-06 |
| `newVSCodeCmdInternal(cfg, fetcher, execFn, ssmClient) *cobra.Command` | internal/app/cmd/vscode.go | 73-06 |

## Test Inventory (22 new tests, all SKIP)

| Package | Tests | Wave to implement |
|---------|-------|-------------------|
| pkg/sshkey | 7 (ModePriv, ModePub, PubKeyParses, Comment, NoTrailingNewline, CreatesParentDir, Idempotent) | Wave 1 Plan 73-01 |
| internal/app/cmd (sshconfig) | 9 (UpsertCreatesFile, UpsertAppends, UpsertReplaces, UpsertInsertsBefore, PreservesOutside, RemovePreservesOthers, RemoveCleans, RemoveIdempotentMissing, RemoveIdempotentNoFile) | Wave 1 Plan 73-03 |
| internal/app/cmd (vscode) | 6 (MissingPrivateKey, BuildsPortForwardArgs, OutputContainsHostAlias, SSHDInactive, PrePhase73, Healthy) | Wave 3 Plan 73-06 |
| pkg/profile | 2 (DefaultTrue, False) | Wave 1 Plan 73-02 |
| pkg/compiler | 3 (VSCodeEnabled, VSCodeDisabled, VSCodePubKey) | Wave 1 Plan 73-04 |

## Verification Results

- `make build` — PASS (km v0.2.539, commit 9344e94)
- `go build ./pkg/... ./internal/...` — PASS (no errors)
- `go vet ./pkg/... ./internal/...` — PASS (clean)
- All 22 new tests: SKIP (not FAIL) — suite stays green
- `grep -r "TODO Wave 1" --include='*_test.go'` — 21 matching lines (requirement: ≥18)
- Pre-existing failures in pkg/compiler (4 tests) and timeouts in internal/app/cmd confirmed unrelated to Wave 0 changes

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] boolPtr redeclaration in profile_test package**
- **Found during:** Task 3
- **Issue:** pkg/profile/validate_test.go already declares `func boolPtr(b bool) *bool` in the same `package profile_test`; adding a second declaration caused a `go vet` redeclaration error
- **Fix:** Removed the duplicate `boolPtr` helper from types_test.go; added a comment pointing to the existing definition in validate_test.go
- **Files modified:** pkg/profile/types_test.go
- **Commit:** 9344e94

## Self-Check: PASSED

All 8 files found on disk. All 3 task commits verified in git log.
