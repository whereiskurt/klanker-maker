---
phase: 73-km-vscode-remote-session-via-ssm
plan: "01"
subsystem: sshkey
tags: [keygen, ed25519, ssh, crypto, tdd]
dependency_graph:
  requires: [73-00]
  provides: [sshkey.GenerateAndWrite]
  affects: [73-04, 73-05]
tech_stack:
  added: []
  patterns: [ed25519-keygen, openssh-pem, authorized-keys-format]
key_files:
  created: []
  modified:
    - pkg/sshkey/keygen.go
    - pkg/sshkey/keygen_test.go
decisions:
  - "Manual pubkey line construction (fmt.Sprintf) instead of gossh.MarshalAuthorizedKey — preserves comment field"
  - "0o700 on parent directory, 0o600 on private key, 0o644 on public key — SSH security requirements"
  - "Returned pubContent has no trailing newline — safe for heredoc embedding in Wave 2 userdata templates"
metrics:
  duration: 352s
  completed: "2026-05-07T23:56:44Z"
  tasks_completed: 2
  files_modified: 2
---

# Phase 73 Plan 01: SSH Key Generation (pkg/sshkey) Summary

**One-liner:** ed25519 keypair generation writing OpenSSH PEM private key (0600) and authorized_keys-format public key (0644) using crypto/ed25519 + golang.org/x/crypto/ssh.

## What Was Built

`pkg/sshkey.GenerateAndWrite(privPath, pubPath, comment string) (pubContent string, err error)`

Production implementation (~54 lines) replacing the Wave 0 stub. The function:

1. Creates the parent directory of `privPath` via `os.MkdirAll(..., 0o700)`
2. Generates an ed25519 keypair with `ed25519.GenerateKey(cryptorand.Reader)`
3. Marshals the private key to OpenSSH PEM via `gossh.MarshalPrivateKey(priv, comment)` + `pem.EncodeToMemory`
4. Writes private key PEM to `privPath` with mode 0600
5. Constructs the pubkey line manually: `"ssh-ed25519 <base64> <comment>"` (avoids `gossh.MarshalAuthorizedKey` which drops the comment)
6. Writes pubkey line + `"\n"` to `pubPath` with mode 0644
7. Returns the pubkey line WITHOUT trailing newline

## Seven Invariants Tested and Passing

| Test | Assertion |
|------|-----------|
| `TestGenerateAndWrite_ModePriv` | Private key file has mode 0600 |
| `TestGenerateAndWrite_ModePub` | Public key file has mode 0644 |
| `TestGenerateAndWrite_PubKeyParses` | Returned pubLine and .pub file both parse via `gossh.ParseAuthorizedKey` |
| `TestGenerateAndWrite_Comment` | pubLine ends with ` km-sb-abc123` (comment preserved) |
| `TestGenerateAndWrite_NoTrailingNewline` | Returned pubLine has no trailing `\n` |
| `TestGenerateAndWrite_CreatesParentDir` | Deep nested parent dir created with mode 0700 |
| `TestGenerateAndWrite_Idempotent` | Two calls with same paths succeed; second pubLine differs (fresh randomness) |

## Production File

- Path: `pkg/sshkey/keygen.go`
- Lines: 54
- Dependencies: stdlib only (`crypto/ed25519`, `crypto/rand`, `encoding/base64`, `encoding/pem`, `fmt`, `os`, `path/filepath`) + `golang.org/x/crypto/ssh` (already in go.mod v0.49.0)

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| Task 1: Implement keygen.go | `97ea480` | feat(73-01): implement pkg/sshkey.GenerateAndWrite |
| Task 2: Activate tests | `8e2942b` | test(73-01): activate all seven keygen_test.go assertions |

## Deviations from Plan

None - plan executed exactly as written.

The test file's blank `_ "golang.org/x/crypto/ssh"` import was upgraded to a named `gossh` import to enable `gossh.ParseAuthorizedKey` calls in `_PubKeyParses` — this is mechanical and exactly what the plan described.

## Self-Check: PASSED

- `pkg/sshkey/keygen.go` exists (54 lines)
- `pkg/sshkey/keygen_test.go` exists (7 passing tests, 0 skipped)
- Commit `97ea480` exists
- Commit `8e2942b` exists
- `go test ./pkg/sshkey/... -count=1 -v` output: 7 PASS, 0 FAIL, 0 SKIP
