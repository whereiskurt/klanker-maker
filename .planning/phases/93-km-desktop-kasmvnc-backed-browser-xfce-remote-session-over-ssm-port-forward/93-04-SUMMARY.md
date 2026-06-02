---
phase: 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward
plan: 04
subsystem: provisioning
tags: [kasmvnc, desktop, credentials, crypto/rand, km-create, lambda]

# Dependency graph
requires:
  - phase: 93-03
    provides: NetworkConfig.DesktopKasmUser/DesktopKasmPass fields + IsDesktopEnabled helper
provides:
  - Per-sandbox KasmVNC credential (user:pass) generated at km create, stored at ~/.km/desktop/<id>
  - GenerateDesktopCredential exported helper (testable; env-var override for Lambda path)
  - randomPassword base62 helper using crypto/rand
  - Two call sites wired: runCreate (Step 6e) + runCreateRemote (Phase 93 block)
  - desktop-creds.txt S3 artifact upload in runCreateRemote (mirrors vscode-pubkey.txt)
  - TestDesktopCredential GREEN (3 subtests) + TestDesktopCredentialSource GREEN
affects:
  - 93-05 (km desktop start reads ~/.km/desktop/<id>)
  - cmd/create-handler/main.go (sibling plan — should download desktop-creds.txt and pass KM_DESKTOP_KASM_USER/PASS)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Per-sandbox credential generation mirrors VSCode keypair pattern (two call sites: runCreate + runCreateRemote)"
    - "Lambda env-var override pattern: KM_DESKTOP_KASM_USER/PASS prevents re-generation in Lambda subprocess"
    - "S3 artifact upload for credential propagation (desktop-creds.txt mirrors vscode-pubkey.txt)"
    - "Exported helper (GenerateDesktopCredential) for black-box testability from package cmd_test"

key-files:
  created: []
  modified:
    - internal/app/cmd/create.go
    - internal/app/cmd/create_test.go

key-decisions:
  - "Export GenerateDesktopCredential (uppercase) for testability from package cmd_test — keeps call sites thin"
  - "randomPassword extracted as named function with error return (not inline) for clarity and reuse"
  - "Credential not written when KM_DESKTOP_KASM_USER/PASS set — Lambda has no ~/.km to write into"
  - "desktop-creds.txt uploaded to S3 in runCreateRemote (not via EventBridge detail field) — consistent with vscode-pubkey.txt pattern"

requirements-completed: [DSK-08-CREDENTIAL]

# Metrics
duration: 10min
completed: 2026-06-02
---

# Phase 93 Plan 04: Desktop Credential Generation Summary

**Per-sandbox KasmVNC credential (base62 password, user:pass at ~/.km/desktop/<id> mode 0600) generated at km create and threaded into NetworkConfig for userdata boot-time seeding**

## Performance

- **Duration:** 10 min
- **Started:** 2026-06-02T20:51:40Z
- **Completed:** 2026-06-02T21:01:40Z
- **Tasks:** 1 (TDD: RED + GREEN)
- **Files modified:** 2

## Accomplishments
- `randomPassword(n int)` base62 helper using crypto/rand — no colons or newlines, safe for user:pass files
- `GenerateDesktopCredential(homeDir, sandboxID string, network *compiler.NetworkConfig) error` exported helper:
  - Lambda subprocess path: reads `KM_DESKTOP_KASM_USER`/`KM_DESKTOP_KASM_PASS` env, no file write
  - Local path: generates 16-char password, writes `~/.km/desktop/<id>` (0600), threads `NetworkConfig.DesktopKasmUser/Pass`
- Step 6e wired in `runCreate`: `IsDesktopEnabled` guard + `GenerateDesktopCredential` call after VSCode block
- Phase 93 block wired in `runCreateRemote`: same guard + call, uploads `desktop-creds.txt` to S3
- `TestDesktopCredential` GREEN (3 subtests: enabled writes file, two creates differ, env override skips write)
- `TestDesktopCredentialSource` GREEN (10 source-level pattern checks)

## Task Commits

Each task was committed atomically:

1. **Task 1 RED: TestDesktopCredential failing tests** - `78c65a03` (test)
2. **Task 1 GREEN: GenerateDesktopCredential + call sites + desktop-creds.txt** - `9ac04323` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified
- `internal/app/cmd/create.go` - Added `randomPassword`, `GenerateDesktopCredential`, Step 6e in `runCreate`, Phase 93 block in `runCreateRemote`, `desktop-creds.txt` upload
- `internal/app/cmd/create_test.go` - Added `TestDesktopCredential` (3 subtests) + `TestDesktopCredentialSource` (10 checks)

## Decisions Made
- **Export `GenerateDesktopCredential`** (uppercase) so it can be called directly from `package cmd_test` — avoids splitting the test file into two packages
- **`randomPassword` named function** with error return makes the crypto/rand usage explicit and adds a descriptive name vs inline alphabet expansion
- **No file write on Lambda path** — the Lambda's filesystem is ephemeral and has no `~/.km`; the credential file is only useful on the operator's laptop
- **`desktop-creds.txt` S3 upload** (not a new EventBridge field) — exactly mirrors `vscode-pubkey.txt` so `cmd/create-handler/main.go` can adopt the same download+env-inject pattern

## Deviations from Plan

**1. [Rule 1 - Bug] `GenerateDesktopCredential` already existed from 93-03 with inline crypto/rand**
- **Found during:** Task 1 (GREEN phase — stub existed but had no `randomPassword` wrapper and no call sites)
- **Issue:** 93-03 had pre-seeded the function body with inline base62 logic; the plan expected `randomPassword` as a separate named function
- **Fix:** Refactored inline logic into `randomPassword(n int) (string, error)` and updated `GenerateDesktopCredential` to call it; added all missing call sites and `desktop-creds.txt` upload
- **Files modified:** `internal/app/cmd/create.go`
- **Verification:** `go build ./...` + `TestDesktopCredential` all subtests GREEN

---

**Total deviations:** 1 auto-fixed (Rule 1 — pre-existing stub needed refactor + wiring)
**Impact on plan:** Required refactoring inline logic into `randomPassword` (net improvement for readability/testability). No scope creep.

## Issues Encountered
- The Wave 0 stub `TestDesktopCredential` in `desktop_test.go` (package `cmd`, in `t.Skip()`) coexists with the new `TestDesktopCredential` in `create_test.go` (package `cmd_test`) — both run; the stub SKIPs and the new one PASSES. This is expected and correct; 93-05 will remove the skip from `desktop_test.go`.

## Self-Check: PASSED
- `internal/app/cmd/create.go`: FOUND
- `internal/app/cmd/create_test.go`: FOUND
- Commit `9ac04323` (feat): FOUND
- Commit `78c65a03` (test RED): FOUND
- `go test ./internal/app/cmd/... -run TestDesktopCredential -count=1`: GREEN
- `go build ./...`: GREEN

## Next Phase Readiness
- `km desktop start` (93-05) reads `~/.km/desktop/<id>` to display the credential URL — that file is now guaranteed to exist when desktop is enabled
- `cmd/create-handler/main.go` should be updated (outside this plan's scope) to download `desktop-creds.txt` and set `KM_DESKTOP_KASM_USER`/`KM_DESKTOP_KASM_PASS` env vars before spawning the `km create` subprocess — this mirrors the existing `vscode-pubkey.txt` / `KM_VSCODE_SSH_PUBKEY` pattern

---
*Phase: 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward*
*Completed: 2026-06-02*
