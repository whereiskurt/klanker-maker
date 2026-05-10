---
phase: 76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox
plan: "02"
subsystem: vscode-rekey
tags: [vscode, ssh, keygen, rekey, ssm, tdd]
dependency_graph:
  requires: ["76-01"]
  provides: ["complete-runVSCodeRekey", "pubkeyFingerprint", "rekeyInstallSpyMock"]
  affects: ["vscode.go", "vscode_test.go"]
tech_stack:
  added: ["golang.org/x/crypto/ssh (FingerprintSHA256)", "pkg/sshkey.GenerateAndWrite"]
  patterns: ["spy-mock pattern for non-deterministic keygen", "sequenced SSM mock", "atomic rename .pub-first"]
key_files:
  modified:
    - internal/app/cmd/vscode.go
    - internal/app/cmd/vscode_test.go
decisions:
  - "Used rekeyInstallSpyMock (spy on install script, extract pubkey, return as readback) to test non-deterministic ed25519 keygen without mocking the keygen itself"
  - "Fixed TestVSCodeRekey_Locked_WithForce (Wave 1 test) to use rekeyInstallSpyMock — it previously relied on stub behavior (returning nil after pre-flight) that no longer applies"
  - "Used strings.TrimRight(line, '\\r\\n') not TrimSpace to preserve embedded spaces in the pubkey line per 76-RESEARCH.md Pitfall 5"
  - "Readback parsing uses strings.Index for '=== READBACK ===' (first occurrence, per 76-RESEARCH.md)"
metrics:
  duration: "854s (~14min)"
  completed_date: "2026-05-10"
  tasks_completed: 2
  files_modified: 2
---

# Phase 76 Plan 02: Complete runVSCodeRekey Implementation Summary

**One-liner:** Full `km vscode rekey` flow: local-key classification + confirmation prompt + ed25519 scratch-path keygen + SSM install/readback verify + atomic .pub-first rename + step markers; all 16 TestVSCodeRekey_* tests green.

## What Was Built

### Task 1: Complete runVSCodeRekey + pubkeyFingerprint helper

Replaced the `(Plan 76-02 wires up keygen + push + commit here)` placeholder in `runVSCodeRekey` with the 7-step complete implementation:

1. **Local key state classification** — `os.Stat` on `~/.km/keys/<id>.pub`; `localKeyAbsent` bool gates confirmation prompt variant and final step message.
2. **Confirmation prompt** — prints old fingerprint (or cross-laptop message), reads stdin via `fmt.Scanln`; accepts y/Y/yes; anything else → `Aborted.` + `return nil` (exit 0). Bypassed entirely when `yes == true`.
3. **Keypair generation to scratch** — `sshkey.GenerateAndWrite(privNewPath, pubNewPath, "km-"+sandboxID)`; GenerateAndWrite uses `os.WriteFile` semantics → pre-existing scratch files are overwritten automatically.
4. **SSM install script** — single round-trip with `set -e`, `restorecon … || true` guard, `cat > authorized_keys << 'KEY'` heredoc, `echo "=== READBACK ===" ; head -1 authorized_keys` at end.
5. **Readback verification** — finds `=== READBACK ===` marker, strips `\r\n` with `TrimRight`, compares byte-for-byte against `newPubKeyLine`; mismatch → hard error pointing at `km shell <id> -- cat ~/.ssh/authorized_keys` and `km vscode rekey <id>` retry; local files unchanged.
6. **Atomic rename** — `.pub.new → .pub` BEFORE `.new → privFinalPath` per CONTEXT.md design (if step 2 fails, ssh keeps using old private key).
7. **Final output** — "replaced" vs "created" based on `localKeyAbsent`; "Rekey complete. Active VS Code sessions stay on the old key until reconnect."

Added `pubkeyFingerprint(pubPath string) string` helper at end of vscode.go — reads pubPath, parses with `gossh.ParseAuthorizedKey`, returns `gossh.FingerprintSHA256(pk)`. Returns descriptive placeholder on error.

Added imports: `gossh "golang.org/x/crypto/ssh"` and `"github.com/whereiskurt/klanker-maker/pkg/sshkey"`.

**Commit:** `1abf5b7`

### Task 2: Turn 8 Wave 0 stubs green

Added test infrastructure to `vscode_test.go`:

- **`sequencedSSMMock`** — returns a different output on each successive `GetCommandInvocation` call; used by VerifyMismatch.
- **`rekeyInstallSpyMock`** — captures the install script from the second `SendCommand` call; on `GetCommandInvocation` call 2, extracts the pubkey with `extractPubkeyFromInstallScript` and returns it as the readback; used by NormalRotation, CrossLaptop, RenameOrdering, OverwritesScratch, YesFlag, OutputMarkers.
- **`extractPubkeyFromInstallScript(s string) string`** — finds the pubkey line in the heredoc between `<< 'KEY'\n` and `\nKEY\n`.
- **`seedRekeyTestKeys(t, home, sandboxID)`** — generates a real ed25519 keypair to `~/.km/keys/<id>` and `~/.km/keys/<id>.pub`, returns the private key bytes and pubkey line.

Added `"github.com/whereiskurt/klanker-maker/pkg/sshkey"` import.

Uncommented and implemented all 8 stub test bodies:

| Test | What it verifies |
|------|-----------------|
| NormalRotation | Private key bytes change after successful rekey with pre-seeded local keys |
| CrossLaptop | Keys are created (mode 0600/0644) when absent; output says "created" not "replaced" |
| VerifyMismatch | Returns "verification failed"+"Old key still active locally" when readback mismatches; local files unchanged |
| RenameOrdering | Scratch files gone after rekey; committed .pub matches install script; private key updated |
| OverwritesScratch | Committed files are not the crash sentinel bytes (GenerateAndWrite overwrote scratch files) |
| YesFlag | Completes without blocking on stdin; no "[y/N]" in output |
| ConfirmNo | Returns nil (exit 0), prints "Aborted.", local files unchanged when user answers "n" |
| OutputMarkers | All 5 step markers appear in correct order; "Rekey complete" present |

Also fixed `TestVSCodeRekey_Locked_WithForce` (Wave 1 test) — it used `vsCodeSSMMock` (fixed output) which now fails at the install step because `healthySSMOutput` lacks `=== READBACK ===`. Updated to use `rekeyInstallSpyMock` with a pre-seeded HOME.

**Commit:** `046750c`

## Test Results

```
go test ./internal/app/cmd/ -run 'TestVSCode' -timeout 60s
ok  github.com/whereiskurt/klanker-maker/internal/app/cmd  27.116s
```

- 21 total: 5 pre-existing (Start/Status) + 16 new (Rekey)
- 0 SKIP, 0 FAIL

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed TestVSCodeRekey_Locked_WithForce broken by implementation completion**
- **Found during:** Task 2 first test run
- **Issue:** Wave 1 test used `vsCodeSSMMock{output: healthySSMOutput}` — worked with stub (which returned nil after pre-flight) but fails with real implementation because the install SSM call returns `healthySSMOutput` which lacks `=== READBACK ===`
- **Fix:** Updated test to use `rekeyInstallSpyMock` and pre-seed HOME with `seedRekeyTestKeys`
- **Files modified:** `internal/app/cmd/vscode_test.go`
- **Commit:** `046750c`

## Self-Check

- [x] `internal/app/cmd/vscode.go` modified (commit `1abf5b7`)
- [x] `internal/app/cmd/vscode_test.go` modified (commit `046750c`)
- [x] `pubkeyFingerprint` helper present in vscode.go
- [x] `(Plan 76-02 wires up keygen + push + commit here)` marker gone
- [x] 16 TestVSCodeRekey_* tests PASS, 0 SKIP
- [x] `make build` succeeds
- [x] `./km vscode rekey --help` shows complete help text
