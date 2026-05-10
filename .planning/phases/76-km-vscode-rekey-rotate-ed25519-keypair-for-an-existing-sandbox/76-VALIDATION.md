---
phase: 76
slug: km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox
status: draft
nyquist_compliant: true
wave_0_complete: true
created: 2026-05-09
revised: 2026-05-09
---

# Phase 76 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (`testing` package) |
| **Config file** | none — `go test ./...` from repo root |
| **Quick run command** | `go test ./internal/app/cmd/ -run 'TestVSCodeRekey' -timeout 30s` |
| **Full suite command** | `go test ./internal/app/cmd/ -run 'TestVSCode' -timeout 60s` |
| **Estimated runtime** | ~5 seconds (in-process mocks; no AWS calls) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./internal/app/cmd/ -run 'TestVSCodeRekey' -timeout 30s`
- **After every plan wave:** Run `go test ./internal/app/cmd/ -run 'TestVSCode' -timeout 60s`
- **Before `/gsd:verify-work`:** `go test ./...` must be green
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 76-00-01 | 00 | 0 | Test stubs for all rekey behaviors | unit | `go test ./internal/app/cmd/ -run 'TestVSCodeRekey' -timeout 30s` | ❌ W0 | ⬜ pending |
| 76-01-01 | 01 | 1 | `newVSCodeRekeyCmd` registered under `km vscode` | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_CommandRegistered` | ❌ W0 | ⬜ pending |
| 76-01-02 | 01 | 1 | `--force` and `--yes` flags accessible on rekey cobra command | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_FlagsExist` | ❌ W0 | ⬜ pending |
| 76-01-03 | 01 | 1 | Pre-flight: EC2 not running → error pointing at `km resume` | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_NotRunning` | ❌ W0 | ⬜ pending |
| 76-01-04 | 01 | 1 | Pre-flight: locked sandbox without `--force` → error pointing at `km unlock` | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_Locked_NoForce` | ❌ W0 | ⬜ pending |
| 76-01-05 | 01 | 1 | Pre-flight: `--force` skips lock check entirely | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_Locked_WithForce` | ❌ W0 | ⬜ pending |
| 76-01-06 | 01 | 1 | Pre-flight: `vscodeEnabled:false` (sshd+authkeys both absent) → vscode-not-enabled error (covers pre-Phase-73) | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_VSCodeDisabled` | ❌ W0 | ⬜ pending |
| 76-01-07 | 01 | 1 | Pre-flight: sshd active + authkeys absent → unexpected-state error (local-key-present + remote-absent inconsistent case) | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_Inconsistent` | ❌ W0 | ⬜ pending |
| 76-01-08 | 01 | 1 | Pre-flight: sshd inactive + authkeys present → sshd-not-running error | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_SSHDDown` | ❌ W0 | ⬜ pending |
| 76-02-01 | 02 | 2 | Local-key state: local present + remote present → normal rotation succeeds | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_NormalRotation` | ❌ W0 | ⬜ pending |
| 76-02-02 | 02 | 2 | Local-key state: local absent + remote present → cross-laptop bootstrap succeeds | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_CrossLaptop` | ❌ W0 | ⬜ pending |
| 76-02-03 | 02 | 2 | SSM verification mismatch → hard error, local key untouched | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_VerifyMismatch` | ❌ W0 | ⬜ pending |
| 76-02-04 | 02 | 2 | Atomic rename ordering: `.pub.new`→`.pub` then `.new`→priv | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_RenameOrdering` | ❌ W0 | ⬜ pending |
| 76-02-05 | 02 | 2 | Pre-existing `.new` / `.pub.new` scratch files unconditionally overwritten | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_OverwritesScratch` | ❌ W0 | ⬜ pending |
| 76-02-06 | 02 | 2 | Confirmation prompt: `--yes` skips prompt, no stdin read | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_YesFlag` | ❌ W0 | ⬜ pending |
| 76-02-07 | 02 | 2 | Confirmation prompt: "n" answer aborts cleanly (exit 0, no key changes) | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_ConfirmNo` | ❌ W0 | ⬜ pending |
| 76-02-08 | 02 | 2 | Output: `✓` step markers printed in correct order | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_OutputMarkers` | ❌ W0 | ⬜ pending |
| 76-03-01 | 03 | 2 | Docs: `CLAUDE.md` lists `km vscode rekey` with flags | manual | `grep -q 'km vscode rekey' CLAUDE.md` | ❌ W0 | ⬜ pending |
| 76-03-02 | 03 | 2 | Docs: `docs/vscode.md` covers all three pain-point scenarios + active-session note | manual | `grep -q 'rekey' docs/vscode.md && grep -q 'old key until reconnect' docs/vscode.md` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

**Sampling continuity:** No 3 consecutive tasks lack automated verification. Tasks 76-03-01/02 (docs) use grep-based content checks as their automated proxy.

**Wave structure:** Phase 76 consolidated to 4 plans / 3 waves (originally drafted as 5 plans / 5 waves). Plan 03 (docs) runs in Wave 2 in parallel with Plan 02 (implementation) because docs only depend on the locked CLI surface from Plan 01 (Wave 1).

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/vscode_test.go` — append all `TestVSCodeRekey_*` test stubs (file already exists; no new file)
- [ ] No new test infrastructure needed — existing `vsCodeSSMMock`, `vsCodeFetcherMock`, `newVSCodeEC2Sandbox`, `healthySSMOutput`, `captureStdout` are reused as-is
- [ ] Optional helper: `rekeyTempHomeDir(t *testing.T) string` if multiple tests need a temp `~/.km/keys` (Wave 0 stub adds it if pattern emerges)

*All `TestVSCodeRekey_*` stubs must compile and fail with a clear "not implemented" error before Wave 1 begins.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end rekey against a live sandbox | Pain point #1: AMI-baked stale-keys recovery | Requires actual AWS sandbox + AL2023 SELinux + SSM | (1) Bake an AMI from a Phase 73 sandbox via `km shell --learn --ami`. (2) `km create` from the baked AMI. (3) `km vscode start` → confirm STALE key works. (4) `km vscode rekey <id>` → enter `y`. (5) Reconnect VS Code → confirm NEW key works and authenticates. |
| End-to-end cross-laptop bootstrap | Pain point #2: cross-laptop portability | Requires two physical/virtual machines | (1) Create sandbox on machine A. (2) On machine B (no `~/.km/keys/<id>`), run `km vscode rekey <id> --yes`. (3) Confirm key written to `~/.km/keys/<id>` on machine B. (4) `km vscode start` → confirm VS Code Remote-SSH connects. |
| Lock override flow | Pre-flight #3 (locked + `--force`) | Requires real DDB lock + interactive prompt | (1) `km lock <id>`. (2) `km vscode rekey <id>` → confirm error pointing at `km unlock`. (3) `km vscode rekey <id> --force` → confirm proceeds and rotates. |
| `restorecon` correctness on AL2023 | SSM install script must run `restorecon -R -v /home/sandbox/.ssh` | Requires AL2023 SELinux enforcing-mode sandbox | After rekey, `km shell <id> -- ls -Z /home/sandbox/.ssh/authorized_keys` → confirm context is `unconfined_u:object_r:ssh_home_t:s0`. Reconnect VS Code → confirm sshd accepts the new key (would silently fail with wrong context). |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references (single test stubs file append)
- [x] No watch-mode flags
- [x] Feedback latency < 30s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** ready for execution
