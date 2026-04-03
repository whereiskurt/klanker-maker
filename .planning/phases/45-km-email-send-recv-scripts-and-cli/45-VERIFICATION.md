---
phase: 45-km-email-send-recv-scripts-and-cli
verified: 2026-04-03T22:30:00Z
status: passed
score: 8/8 must-haves verified
re_verification: false
---

# Phase 45: km Email Send/Recv Scripts and CLI Verification Report

**Phase Goal:** Close the gap between Phase 14's crypto library and in-sandbox usability. Deploy km-send and km-recv bash scripts into sandboxes (pure bash + AWS CLI + openssl, no km binary) that produce/consume signed MIME emails with attachments. Add operator-side km email send and km email read Go CLI commands for orchestrating inter-sandbox communication with authoritative Ed25519 verification and auto-decryption.
**Verified:** 2026-04-03T22:30:00Z
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | km-send bash script deployed via userdata.go with Ed25519 signing, multipart MIME, SES send | VERIFIED | `pkg/compiler/userdata.go` lines 520-702; heredoc KMSEND inside `{{- if .SandboxEmail }}` block; `ed25519_privkey_to_pem()`, `build_mime()`, `openssl pkeyutl -sign -rawin`, `aws sesv2 send-email` all present |
| 2 | km-recv bash script deployed via userdata.go with mailbox reader, best-effort verify, --json/--watch | VERIFIED | `pkg/compiler/userdata.go` lines 722-1106; heredoc KMRECV; `ed25519_pubkey_to_pem()`, `verify_signature()`, DynamoDB lookup, `openssl pkeyutl -verify`, `--json`/`--watch`/`--no-move` flags all present |
| 3 | km email send Go CLI command with --from, --to, --subject, --body, --attach flags | VERIFIED | `internal/app/cmd/email.go` `newEmailSendCmd()` lines 96-129; all 4 flags marked required; `--attach` optional; `runEmailSend()` implements full logic including attachment loading |
| 4 | km email read Go CLI command with authoritative Ed25519 verify, auto-decrypt, --json/--raw | VERIFIED | `internal/app/cmd/email.go` `newEmailReadCmd()` lines 243-268; `runEmailRead()` fetches sender pubkey from DynamoDB, calls `ParseSignedMessage`, auto-decrypts via `autoDecrypt()` using SSM private key |
| 5 | Multipart MIME support in pkg/aws (Attachment type, buildRawMIME, ParseSignedMessage) | VERIFIED | `pkg/aws/identity.go` line 78: `Attachment struct`; line 589: `buildRawMIME` with `[]Attachment`; `pkg/aws/mailbox.go` line 59: `Attachments []Attachment` on `MailboxMessage`; multipart parser lines 180-235 |
| 6 | Shared MIME contract (X-KM-Sender-ID, X-KM-Signature, X-KM-Encrypted headers) | VERIFIED | Go: `identity.go` lines 596-599 writes headers; `mailbox.go` lines 169-171 reads them. Bash: `userdata.go` km-send `build_mime()` writes headers (lines 620-621, 636-637); km-recv `parse_headers()` reads them (lines 813-814) |
| 7 | No km binary in sandbox email scripts; no encryption in bash scripts | VERIFIED | km binary download is eBPF-enforcement-only (`{{- if or (eq .Enforcement "ebpf") (eq .Enforcement "both") }}`). km-send contains zero encrypt/nacl references; km-recv only reads `X-KM-Encrypted` header and displays it — no decryption attempted |
| 8 | All phase tests pass (TestEmail\|TestMultipart\|TestKmSend\|TestKmRecv) | VERIFIED | All 17 `TestEmail*` tests pass; all 5 `TestMultipart*` tests pass; all 6 `TestKmSend*` tests pass; all 6 `TestKmRecv*` tests pass. Full `pkg/aws` and `pkg/compiler` suites pass clean. |

**Score:** 8/8 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/aws/identity.go` | `Attachment` struct, extended `buildRawMIME`, extended `SendSignedEmail` | VERIFIED | `Attachment` at line 78; `buildRawMIME` at line 589 with `[]Attachment` param; `SendSignedEmail` at line 490 with `[]Attachment`; `generateMIMEBoundary` using crypto/rand at line 643 |
| `pkg/aws/mailbox.go` | `MailboxMessage.Attachments`, multipart parsing in `ParseSignedMessage` | VERIFIED | `Attachments []Attachment` at line 59; multipart branch at lines 180-235 with base64 CTE decoding |
| `pkg/aws/identity_test.go` | Multipart buildRawMIME tests, backward compat, round-trip | VERIFIED | 5 passing `TestMultipart*` tests including `TestMultipart_RoundTrip` |
| `pkg/aws/mailbox_test.go` | Multipart ParseSignedMessage tests, single-part compat | VERIFIED | Tests present and passing (confirmed by `go test ./pkg/aws/...`) |
| `pkg/compiler/userdata.go` | km-send heredoc inside `{{- if .SandboxEmail }}` block | VERIFIED | Lines 520-702; gated at line 464 `{{- if .SandboxEmail }}`; ends at line 1107 `{{- end }}` |
| `pkg/compiler/userdata.go` | km-recv heredoc inside `{{- if .SandboxEmail }}` block | VERIFIED | Lines 722-1106; within same `{{- if .SandboxEmail }}` block |
| `pkg/compiler/userdata_test.go` | 6 km-send tests + 6 km-recv tests | VERIFIED | All 12 tests pass: present/absent gating, SSM fetch, openssl sign/verify, sesv2 send, DynamoDB lookup, mail dir, PKCS8/SPKI DER prefixes |
| `internal/app/cmd/email.go` | `km email send` and `km email read` Cobra subcommands | VERIFIED | `NewEmailCmd`, `newEmailSendCmd`, `newEmailReadCmd`, `runEmailSend`, `runEmailRead`, `autoDecrypt`; registered in `root.go` line 81 |
| `internal/app/cmd/email_test.go` | Full test coverage with injected mocks | VERIFIED | 17 passing tests: flag validation, invalid IDs, success paths, attachments, stdin body, JSON output, auto-decrypt, multipart extraction |
| `internal/app/cmd/root.go` | `NewEmailCmd` registered | VERIFIED | Line 81: `root.AddCommand(NewEmailCmd(cfg))` |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `email.go` `runEmailSend` | `kmaws.SendSignedEmail` | direct call with `[]kmaws.Attachment` | WIRED | Lines 205-213; passes resolved email addresses, sandbox IDs, encryption policy, and attachments |
| `email.go` `runEmailRead` | `kmaws.ParseSignedMessage` | call per message with fetched pubkey | WIRED | Line 385; fetches sender pubkey from DynamoDB (line 370), fetches receiver allowedSenders (line 380) |
| `email.go` `autoDecrypt` | SSM private key + `kmaws.DecryptFromSender` | `GetParameter` then decrypt | WIRED | Lines 414-461; fetches encryption-key from SSM, fetches pubkey from DynamoDB, calls `kmaws.DecryptFromSender` |
| `identity.go` `buildRawMIME` | X-KM headers | written to MIME string builder | WIRED | Lines 596-599; X-KM-Sender-ID, X-KM-Signature, X-KM-Encrypted all written |
| `mailbox.go` `ParseSignedMessage` | X-KM headers | read from parsed net/mail.Message | WIRED | Lines 169-171; headers consumed; multipart branch extracts body + attachments |
| km-send bash | SSM signing key | `aws ssm get-parameter --with-decryption` | WIRED | `userdata.go` line ~599 (relative to KMSEND); key path `/sandbox/$KM_SANDBOX_ID/signing-key` |
| km-send bash | openssl Ed25519 sign | `openssl pkeyutl -sign -rawin` | WIRED | PKCS8 DER built via `ed25519_privkey_to_pem()` then passed to openssl |
| km-send bash | SES raw email | `aws sesv2 send-email --content "Raw=..."` | WIRED | MIME assembled by `build_mime()` then base64-encoded and sent via sesv2 |
| km-recv bash | DynamoDB pubkey lookup | `aws dynamodb get-item --table-name km-identities` | WIRED | `verify_signature()` function; conditional on X-KM-Sender-ID presence |
| km-recv bash | openssl Ed25519 verify | `openssl pkeyutl -verify -rawin` | WIRED | Uses `ed25519_pubkey_to_pem()` then `openssl pkeyutl -verify` |

---

### Requirements Coverage

No REQUIREMENTS.md traceability IDs were declared for this phase (all plan `requirements-completed` fields are empty). The phase is self-contained by plan scope.

---

### Anti-Patterns Found

No stub patterns, TODO/FIXME comments, empty implementations, or console.log-only handlers were found in the phase-modified files.

---

### Pre-Existing Test Failure (Out of Scope)

**`TestUnlockCmd_RequiresStateBucket`** in `internal/app/cmd/unlock_test.go` fails in the full `internal/app/cmd` suite. This test was introduced in commit `22366b1` (phase 30-02, March 29 2026) and is unrelated to phase 45 changes. No phase 45 commit touches `unlock.go` or `unlock_test.go`. This failure predates and post-dates phase 45 without causal connection.

---

### Human Verification Required

The following behaviors cannot be verified programmatically:

#### 1. km-send in a live sandbox

**Test:** Provision a sandbox with SandboxEmail set. SSH in and run: `km-send --to <other-sandbox-id>@sandboxes.klankermaker.ai --subject "test" <<< "hello"`
**Expected:** Script reads KM_SANDBOX_ID and KM_EMAIL_ADDRESS, fetches signing key from SSM `/sandbox/$KM_SANDBOX_ID/signing-key`, produces Ed25519 signature, sends raw MIME via sesv2, prints `[km-send] Sent signed email to ... (sig: ...)`. Recipient sandbox sees the message in S3.
**Why human:** Requires live SSM parameter, live SES API, and actual sandbox runtime environment.

#### 2. km-recv signature verification in a live sandbox

**Test:** After km-send delivers a message, run `km-recv --json` in the recipient sandbox.
**Expected:** JSON output shows `"signature":"verified"` when the sender's public key exists in DynamoDB `km-identities` table.
**Why human:** Requires live DynamoDB table with sender identity row and actual openssl execution.

#### 3. km email read auto-decryption end-to-end

**Test:** Send an encrypted message from a sandbox with `encryption: required` policy. Run `km email read <recipient-sandbox-id>` as operator.
**Expected:** Body is displayed as plaintext (decrypted), not raw NaCl ciphertext. `km email read --json` shows decrypted body in `"body"` field.
**Why human:** Requires live SSM encryption keys and DynamoDB identity records with both sender and recipient keys.

---

## Gaps Summary

No gaps. All 8 must-haves are verified at all three levels (exists, substantive, wired). All phase-specific tests pass. The single pre-existing test failure (`TestUnlockCmd_RequiresStateBucket`) is from phase 30-02 and is unrelated to phase 45.

---

_Verified: 2026-04-03T22:30:00Z_
_Verifier: Claude (gsd-verifier)_
