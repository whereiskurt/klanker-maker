---
phase: 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward
plan: "05"
subsystem: cli
tags: [desktop, kasmvnc, ssm, port-forward, cobra]
dependency_graph:
  requires: [93-03]
  provides: [km-desktop-cli]
  affects: [internal/app/cmd/root.go]
tech_stack:
  added: []
  patterns: [resolveVSCodeDeps-mirror, buildPortForwardCmd, sendSSMAndWait, vsCodeSSMMock-reuse]
key_files:
  created:
    - internal/app/cmd/desktop.go
  modified:
    - internal/app/cmd/root.go
    - internal/app/cmd/desktop_test.go
    - internal/app/cmd/create.go
decisions:
  - "GenerateDesktopCredential exported (not unexported) because create_test.go uses cmd.GenerateDesktopCredential from external test package"
  - "Added GenerateDesktopCredential to create.go as Rule 3 fix — 93-04 added create_test.go referencing this symbol, causing compile failure without it"
  - "TestDesktopCredentialSource (from 93-04) still fails on randomPassword/IsDesktopEnabled/desktop-creds.txt patterns — deferred to 93-04 to complete"
metrics:
  duration: "336s"
  completed: "2026-06-02"
  tasks_completed: 2
  files_changed: 4
---

# Phase 93 Plan 05: km desktop CLI (start + status) Summary

Added `km desktop start` and `km desktop status` CLI commands with KasmVNC pre-flight checks over SSM port-forward, mirroring `vscode.go` exactly with credential-file read + URL print instead of SSH config.

## What Was Built

### Task 1: desktop.go + root.go registration (commit f25a74cc)

Created `internal/app/cmd/desktop.go` as a structural mirror of `vscode.go`:

- `NewDesktopCmd` / `newDesktopCmdInternal` with `start` + `status` subcommands (no `rekey`)
- `--local-port` flag defaulting to `8444` (KasmVNC display :1 auto-port)
- `resolveDesktopDeps` — identical DI pattern to `resolveVSCodeDeps`
- `runDesktopStart`: port probe → FetchSandbox → extractResourceID → stat `~/.km/desktop/<id>` (clear error with "different machine" hint if absent) → read+split `user:pass` credential → `sendSSMAndWait(desktopStatusScript)` → `parseDesktopStatus` → print `https://localhost:<port>/` + credential + Ctrl-C note → `buildPortForwardCmd(..., "8444")` blocking exec
- `runDesktopStatus`: FetchSandbox → extractResourceID → sendSSMAndWait → parseDesktopStatus → print "KasmVNC desktop ready"; non-zero exit on unhealthy
- `desktopStatusScript`: `systemctl is-active kasmvnc` + `test -f /home/sandbox/.kasmpasswd`
- `parseDesktopStatus`: descriptive errors — both absent → "desktop not enabled ... set spec.runtime.desktop.enabled: true"; kasmvnc active but no passwd → "unexpected state"; only kasmvnc inactive → "desktop not running ... is spec.runtime.desktop.enabled: true set?"

Registered `root.AddCommand(NewDesktopCmd(cfg))` in `root.go` immediately after `NewVSCodeCmd`.

### Task 2: desktop_test.go — GREEN tests (commit ad6133d8)

Replaced Wave 0 `t.Skip` stubs in `TestDesktopStart` and `TestDesktopStatus` with real tests. Left `TestDesktopCredential` skipped (owned by plan 93-04).

**TestDesktopStart** (3 subtests):
- `PortAlreadyInUse`: binds a listener, passes that port; verifies error contains `--local-port`
- `MissingCredentialFile`: HOME points to empty tmpdir; verifies "credential" + "different machine" in error
- `HealthyPath_PrintsURLAndCredential`: seeds `~/.km/desktop/sb-desk-001`, healthy SSM mock, capturing `ShellExecFunc`; asserts `https://localhost:8444/`, `user: kasm`, `pass: s3cr3tP@ssw0rd`, `Ctrl-C` in stdout; asserts `AWS-StartPortForwardingSession` + `8444` in exec args

**TestDesktopStatus** (2 subtests):
- `Healthy_PrintsReady`: healthy SSM mock → nil error, stdout contains "KasmVNC" + "ready"
- `Inactive_ReturnsError_WithDesktopEnabledHint`: inactive SSM mock → non-nil error with `desktop.enabled`

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Added GenerateDesktopCredential to create.go**
- **Found during:** Task 2 (test compilation)
- **Issue:** `create_test.go` (written by plan 93-04 in RED phase) calls `cmd.GenerateDesktopCredential` but the function didn't exist in `create.go`, causing the entire `internal/app/cmd` test package to fail to compile. Without this fix, `TestDesktopStart` and `TestDesktopStatus` could not even be compiled, let alone run.
- **Fix:** Added `GenerateDesktopCredential(homeDir, sandboxID string, network *compiler.NetworkConfig) error` to `create.go`. The function generates a 16-char random alphanumeric password using `crypto/rand`, writes `kasm:<pass>` to `~/.km/desktop/<id>` (mode 0600), and threads `DesktopKasmUser`/`DesktopKasmPass` into `NetworkConfig`. Supports the Lambda env var override path (`KM_DESKTOP_KASM_USER` + `KM_DESKTOP_KASM_PASS`) which skips file write.
- **Files modified:** `internal/app/cmd/create.go`
- **Commit:** ad6133d8

### Deferred Issues

**TestDesktopCredentialSource** (from plan 93-04's `create_test.go`) fails with:
- Missing `func generateDesktopCredential(` (unexported name pattern)
- Missing `func randomPassword(`
- Missing `IsDesktopEnabled` guard in create.go
- Missing `desktop-creds.txt` upload

These are plan 93-04 deliverables that 93-04 must complete in `create.go`. My `GenerateDesktopCredential` implementation satisfies the `TestDesktopCredential` sub-tests in `create_test.go` (all 3 pass) but the source-level pattern check expects additional code that 93-04 will add.

## Self-Check: PASSED

- FOUND: internal/app/cmd/desktop.go
- FOUND: internal/app/cmd/desktop_test.go
- FOUND: .planning/phases/93-.../93-05-SUMMARY.md
- FOUND: commit f25a74cc (Task 1)
- FOUND: commit ad6133d8 (Task 2)
- TestDesktopStart: 3/3 subtests PASS
- TestDesktopStatus: 2/2 subtests PASS
