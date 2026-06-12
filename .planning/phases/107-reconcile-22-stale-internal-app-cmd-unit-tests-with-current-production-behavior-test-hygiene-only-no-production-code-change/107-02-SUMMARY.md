---
phase: 107-reconcile-22-stale-internal-app-cmd-unit-tests
plan: "02"
subsystem: email-cmd-tests
tags: [test-hygiene, ssm-mocks, email, signing-key, encryption-key]
dependency_graph:
  requires: []
  provides: [email-ssm-mocks-aligned-to-km-prefix]
  affects: [internal/app/cmd/email_test.go]
tech_stack:
  added: []
  patterns: [prefix-scoped SSM key path /km/sandbox/{id}/...]
key_files:
  created: []
  modified:
    - internal/app/cmd/email_test.go
decisions:
  - "Re-keyed 6 SSM mock seeds from /sandbox/... to /km/sandbox/... to match SigningKeyPath/EncryptionKeyPath (pkg/aws/identity.go) production convention"
metrics:
  duration: "~3 minutes"
  completed: "2026-06-12"
  tasks_completed: 1
  files_changed: 1
---

# Phase 107 Plan 02: Email SSM Mock Path Reconciliation Summary

Re-keyed email test SSM mock seeds from legacy un-prefixed `/sandbox/...` paths to prefix-scoped `/km/sandbox/...` paths matching the `SigningKeyPath`/`EncryptionKeyPath` production helpers in `pkg/aws/identity.go`.

## What Was Done

Task 1 (auto): Updated 6 SSM parameter name seeds in `internal/app/cmd/email_test.go`:

- Lines 325, 356, 387, 427, 480: `/sandbox/.../signing-key` → `/km/sandbox/.../signing-key` (5 send-test seeds)
- Line 779: `fmt.Sprintf("/sandbox/%s/encryption-key", sandboxID)` → `fmt.Sprintf("/km/sandbox/%s/encryption-key", sandboxID)` (read-test seed)

No production code changed. No non-test files modified.

## Verification

```
go test ./internal/app/cmd/ -run 'TestEmail(Send|Read)' -count=1 -timeout 600s
ok  github.com/whereiskurt/klanker-maker/internal/app/cmd  75.972s
EXIT=0
```

- `TestEmailSend_SuccessNoAttachments` — PASS
- `TestEmailSend_TwoAttachments` — PASS
- `TestEmailSend_BodyFromStdin` — PASS
- `TestEmailRead_EncryptedMessageAutoDecrypts` — PASS (plaintext "secret message" decrypted correctly)

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| 1    | 248a3458 | test(107-02): re-key email SSM mocks to /km/sandbox/... prefix-scoped paths |

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- `internal/app/cmd/email_test.go` modified: confirmed 6 path corrections present
- Commit 248a3458: exists and contains email_test.go diff showing all 6 `/sandbox/` → `/km/sandbox/` replacements
- No `/sandbox/` signing-key or encryption-key paths remain in email_test.go
- Test run: `ok` + EXIT=0
