---
phase: 45
plan: 4
subsystem: cli
tags: [email, cobra, send, read, signed, encrypted, nacl, ed25519, testable]
dependency_graph:
  requires: [45-01]
  provides: [km-email-send-cmd, km-email-read-cmd]
  affects: [internal/app/cmd, pkg/aws]
tech_stack:
  added: []
  patterns: [cobra-subcommand, injectable-deps, narrow-interface, tabwriter-output, json-output]
key_files:
  created:
    - internal/app/cmd/email.go
    - internal/app/cmd/email_test.go
  modified:
    - internal/app/cmd/root.go
decisions:
  - "Exported EmailSendDeps/EmailReadDeps structs with exported fields for testability from cmd_test package"
  - "emailSSMAPI embeds kmaws.IdentitySSMAPI (full interface) to satisfy SendSignedEmail signature, not just GetParameter"
  - "Auto-decrypt condition uses Encrypted only (not Encrypted && !Plaintext): unsigned encrypted messages have Plaintext=true from ParseSignedMessage so the old condition incorrectly skipped decryption"
  - "allow-list enforcement: nil allowedSenders in ParseSignedMessage rejects non-self senders; tests add receiver identity row with allowed_senders=[*] to permit cross-sandbox messages"
metrics:
  duration: 2380s
  completed_date: "2026-04-03"
  tasks_completed: 7
  files_changed: 3
---

# Phase 45 Plan 4: km email send / km email read Go CLI commands Summary

**One-liner:** Cobra `km email send`/`km email read` subcommands with Ed25519 signing, NaCl auto-decrypt, attachment support, JSON/table/raw output modes, and full injected-mock test coverage.

## What Was Built

Two Cobra subcommands under `km email`:

**`km email send`** — sends a signed (optionally encrypted) inter-sandbox email:
- Flags: `--from`, `--to`, `--subject`, `--body` (file path or `-` for stdin), `--attach` (comma-separated)
- Validates sandbox ID format for both sender and recipient
- Reads encryption policy from sender's DynamoDB identity record
- Calls `kmaws.SendSignedEmail` with attachments and resolved email addresses

**`km email read`** — reads and displays messages from a sandbox mailbox:
- Positional: `sandbox-id`
- Flags: `--json`, `--raw`, `--message-id`
- Fetches sender's public key from DynamoDB for signature verification
- Auto-decrypts `X-KM-Encrypted: true` messages using recipient's SSM private key + DynamoDB public key
- Outputs human-readable tabwriter table (default), JSON array, or raw MIME bytes

**Parent command** `km email` registered in `root.go`.

**Exported dep injection types** (`EmailSendDeps`, `EmailReadDeps`, `EmailSSMAPI`, `EmailS3API`) enable `cmd_test` package testing without AWS credentials.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Auto-decrypt condition skipped unsigned encrypted messages**
- **Found during:** Task 7 (TestEmailRead_EncryptedMessageAutoDecrypts failing)
- **Issue:** `ParseSignedMessage` sets `Plaintext=true` when there is no `X-KM-Signature` header. The auto-decrypt check was `Encrypted && !Plaintext`, so unsigned-but-encrypted messages were never decrypted.
- **Fix:** Changed condition to `Encrypted` only — decryption should proceed regardless of signing status.
- **Files modified:** `internal/app/cmd/email.go`
- **Commit:** 47d1311

**2. [Rule 2 - Missing Critical Functionality] Allow-list enforcement requires receiver identity row in tests**
- **Found during:** Task 7 (TestEmailRead_SinglePlaintextMessage producing empty table)
- **Issue:** `ParseSignedMessage` enforces allow-list using the receiver's `allowedSenders` slice. With a nil slice, all non-self senders are rejected with `ErrSenderNotAllowed`. Test mock had no receiver identity, so message was silently skipped.
- **Fix:** Added receiver identity row with `allowed_senders: ["*"]` to affected tests.
- **Files modified:** `internal/app/cmd/email_test.go`
- **Commit:** 47d1311

## Self-Check: PASSED

- internal/app/cmd/email.go: FOUND
- internal/app/cmd/email_test.go: FOUND
- 9f513d2 (feat commit): FOUND
- 47d1311 (test+fix commit): FOUND
