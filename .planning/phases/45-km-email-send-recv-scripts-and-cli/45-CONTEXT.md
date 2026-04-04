---
phase: 45
name: km-send/km-recv sandbox scripts & km email send/read CLI
status: planned
depends_on: [14, 17]
---

# Phase 45: Context

## Problem

Phase 14 built the full Ed25519 signing, verification, and X25519 encryption library in `pkg/aws/identity.go` and `pkg/aws/mailbox.go`. But nothing in-sandbox can use it. Agents send email with bare `aws sesv2 send-email` producing unsigned, unencrypted messages. The receive side (`ParseSignedMessage`) sees them as `Plaintext: true, SignatureOK: false`.

The crypto infrastructure exists but the last mile — getting it into the sandbox agent's hands — is missing.

## Solution

Two layers:

1. **In-sandbox bash scripts** (`/opt/km/bin/km-send` and `/opt/km/bin/km-recv`) — pure bash + AWS CLI + openssl. No `km` binary dependency. Deployed via `userdata.go` alongside `km-mail-poller`.

2. **Operator-side Go CLI** (`km email send` and `km email read`) — uses existing `pkg/aws` library for authoritative verification, auto-decryption, and orchestrating inter-sandbox comms.

Both layers share a MIME contract: `X-KM-Sender-ID`, `X-KM-Signature`, `X-KM-Encrypted` headers; multipart/mixed for attachments; signature covers text body only.

## Key Design Decisions

- **No km binary in sandbox** — pure bash + AWS CLI + openssl keeps it simple and avoids versioning
- **Body-only signing** — consistent with Phase 14 design; attachments integrity-protected by SES transport
- **Best-effort verification in bash** — `km-recv` attempts Ed25519 verify via openssl but never blocks message delivery on crypto failure; Go CLI is authoritative
- **No encryption in bash scripts** — encryption stays Go-only; enable when stronger guarantees needed
- **Auto-decrypt in Go CLI** — if `X-KM-Encrypted: true` and we have SSM access, just decrypt. No ceremony.

## Existing Code Touched

- `pkg/aws/identity.go` — extend `SendSignedEmail` + `buildRawMIME` for multipart/mixed attachments
- `pkg/aws/mailbox.go` — extend `ParseSignedMessage` for multipart MIME, attachment extraction
- `pkg/compiler/userdata.go` — add `km-send` and `km-recv` scripts
- `internal/app/cmd/root.go` — register `NewEmailCmd`
- `internal/app/cmd/email.go` — new file, `km email send` and `km email read` subcommands
