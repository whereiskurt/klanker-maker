---
phase: 17-sandbox-email-mailbox-access-control-aliases-allow-lists-self-mail-s3-reader
plan: "02"
subsystem: aws-identity, aws-mailbox
tags: [email, alias, allowedSenders, dynamodb, gsi, s3, mailbox, ed25519, allow-list, self-mail]
dependency_graph:
  requires:
    - "17-01: EmailSpec.Alias and AllowedSenders fields in pkg/profile/types.go"
    - "17-01: DynamoDB identities v1.1.0 module with alias-index GSI"
  provides:
    - IdentityQueryAPI interface and FetchPublicKeyByAlias (alias-index GSI query)
    - MatchesAllowList function (*, self, exact ID, wildcard alias via path.Match)
    - Extended PublishIdentity with alias and allowedSenders parameters
    - Extended IdentityRecord with Alias and AllowedSenders fields
    - pkg/aws/mailbox.go with MailboxS3API, MailboxMessage, ListMailboxMessages, ReadMessage, ParseSignedMessage
    - ErrSenderNotAllowed typed sentinel
  affects:
    - Plan 17-03: create.go wiring (uses extended PublishIdentity, alias/allowedSenders from profile)
    - Future sandbox-internal tooling consuming mailbox reader library
tech_stack:
  added: []
  patterns:
    - TDD (RED→GREEN) for all identity and mailbox functions
    - Narrow-interface pattern: IdentityQueryAPI separate from IdentityTableAPI
    - path.Match for wildcard alias pattern matching
    - net/mail.ReadMessage for MIME header/body parsing
    - ErrSenderNotAllowed as typed sentinel for errors.Is() discrimination
key_files:
  created:
    - pkg/aws/mailbox.go
    - pkg/aws/mailbox_test.go
  modified:
    - pkg/aws/identity.go
    - pkg/aws/identity_test.go
    - internal/app/cmd/create.go
decisions:
  - "[Phase 17-02]: IdentityQueryAPI is a separate narrow interface from IdentityTableAPI — callers needing GSI query inject only Query, not Put/Get/Delete; follows existing narrow-interface pattern"
  - "[Phase 17-02]: MatchesAllowList is exported (not matchesAllowList) — called by mailbox.go ParseSignedMessage cross-package boundary"
  - "[Phase 17-02]: ParseSignedMessage enforces allow-list before signature verification — signature check is best-effort (sets SignatureOK=false, no error); allow-list is mandatory gate"
  - "[Phase 17-02]: Self-mail bypass evaluated in ParseSignedMessage directly (senderID == receiverSandboxID) rather than via MatchesAllowList 'self' pattern — simpler, avoids needing receiverSandboxID in all callers"
  - "[Phase 17-02]: ListMailboxMessages uses mail/ prefix (flat) — per Phase 17 research decision Option A; no per-recipient subdirectory filtering"
  - "[Phase 17-02]: create.go auto-updated (Rule 3 auto-fix) — old 10-param PublishIdentity signature caused compile error; alias and allowedSenders now threaded from EmailSpec"
metrics:
  duration: 352s
  completed: "2026-03-23"
  tasks_completed: 2
  files_changed: 5
---

# Phase 17 Plan 02: Identity Alias GSI Lookup, Allow-List Matching, and Mailbox Reader — Summary

**One-liner:** Extended identity.go with IdentityQueryAPI/FetchPublicKeyByAlias (alias-index GSI), MatchesAllowList (*/self/exact/wildcard), and alias/allowedSenders in PublishIdentity; created mailbox.go with MIME parsing, Ed25519 verification, and allow-list enforcement.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 (RED) | Failing tests for FetchPublicKeyByAlias, MatchesAllowList, PublishIdentity alias/allowedSenders | d423f58 | pkg/aws/identity_test.go |
| 1 (GREEN) | Extend identity.go with IdentityQueryAPI, FetchPublicKeyByAlias, MatchesAllowList, alias/allowedSenders | f06433e | identity.go, identity_test.go, create.go |
| 2 (RED) | Failing tests for ListMailboxMessages, ReadMessage, ParseSignedMessage | 52a8d4a | pkg/aws/mailbox_test.go |
| 2 (GREEN) | Create mailbox.go | 0520186 | pkg/aws/mailbox.go |

## What Was Built

### Task 1: Identity Extension (TDD)

Added to `pkg/aws/identity.go`:

**IdentityQueryAPI interface** — narrow interface with Query method only (separate from IdentityTableAPI following existing narrow-interface pattern).

**FetchPublicKeyByAlias** — queries `alias-index` GSI with `alias = :alias` KeyConditionExpression, Limit 1. Returns `(nil, nil)` for unknown aliases (consistent with FetchPublicKey semantics). Parses all IdentityRecord fields including new Alias and AllowedSenders.

**MatchesAllowList** — evaluates sender patterns in order:
- `"*"` — permit any sender immediately
- `"self"` — permit if senderID == receiverSandboxID
- exact sandbox ID string match
- wildcard alias using `path.Match(pattern, senderAlias)` when senderAlias is non-empty
- Returns false if no pattern matched or patterns is empty

**Extended PublishIdentity** — two new trailing parameters (`alias string`, `allowedSenders []string`):
- `alias != ""` → stores `alias` as DynamoDB AttributeValueMemberS
- `len(allowedSenders) > 0` → stores `allowed_senders` as AttributeValueMemberSS (StringSet)
- Empty alias and nil allowedSenders are omitted (backward-compatible with legacy rows)

**Extended IdentityRecord** — two new fields: `Alias string` and `AllowedSenders []string`.

**Updated FetchPublicKey** — now parses `alias` and `allowed_senders` attributes into IdentityRecord.

**Updated identity_test.go** — added mock IdentityQueryAPI, 3 FetchPublicKeyByAlias tests, 7 MatchesAllowList tests, 3 PublishIdentity alias/allowedSenders tests. Updated 4 existing PublishIdentity call sites to new 12-param signature.

**Auto-fixed create.go** (Rule 3 — blocking compile error) — threaded `alias` and `allowedSenders` from `resolvedProfile.Spec.Email` to `PublishIdentity` call.

### Task 2: Mailbox Reader (TDD)

Created `pkg/aws/mailbox.go`:

**MailboxS3API** — narrow interface: `ListObjectsV2` + `GetObject`.

**MailboxMessage** struct — MessageID, S3Key, From, To, Subject, Body, SenderID, SignatureOK, Encrypted, Plaintext.

**ListMailboxMessages** — lists all objects under `mail/` prefix. Handles pagination via ContinuationToken loop. Returns all keys without filtering (caller uses ParseSignedMessage for filtering).

**ReadMessage** — GetObject, io.ReadAll body, close reader. Returns raw MIME bytes or error (including NoSuchKey).

**ParseSignedMessage** — full MIME parsing pipeline:
1. `net/mail.ReadMessage` parses headers and body
2. Extracts From, To, Subject, X-KM-Sender-ID, X-KM-Signature, X-KM-Encrypted
3. Allow-list gate: self-mail bypasses; others must match MatchesAllowList
4. If X-KM-Signature present: VerifyEmailSignature → SignatureOK=true/false (no error on failure)
5. If X-KM-Signature absent: Plaintext=true
6. If X-KM-Encrypted=="true": Encrypted=true (decryption is caller's responsibility)

**ErrSenderNotAllowed** — package-level sentinel `errors.New("sender not on allow-list")`.

## Verification

All checks passed:
- `go test ./pkg/aws/... -run "TestFetchPublicKeyByAlias|TestMatchesAllowList|TestPublishIdentity"` — 13 tests PASS
- `go test ./pkg/aws/... -run "TestListMailboxMessages|TestReadMessage|TestParseSignedMessage"` — 11 tests PASS
- `go test ./pkg/aws/... -v` — all tests PASS, no regressions
- `go test ./...` — 15 packages PASS, zero failures

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Updated create.go PublishIdentity call site**
- **Found during:** Task 1 implementation
- **Issue:** Changing PublishIdentity signature from 10 to 12 params caused compile error in create.go
- **Fix:** Added `alias` and `allowedSenders` locals from `resolvedProfile.Spec.Email`; passed as trailing args
- **Files modified:** internal/app/cmd/create.go
- **Commit:** f06433e

**2. Self-mail check placement in ParseSignedMessage**
- **Plan said:** call MatchesAllowList with senderID for self-mail check
- **Actual:** Self-mail evaluated directly (`senderID == receiverSandboxID`) before calling MatchesAllowList — simpler and avoids coupling receiverSandboxID into MatchesAllowList semantics
- **Impact:** Equivalent behavior; self-mail always permitted regardless of allowedSenders list

## Self-Check: PASSED

Files verified:
- pkg/aws/identity.go — FOUND (IdentityQueryAPI, FetchPublicKeyByAlias, MatchesAllowList, extended PublishIdentity)
- pkg/aws/identity_test.go — FOUND (TestFetchPublicKeyByAlias, TestMatchesAllowList, TestPublishIdentity tests added)
- pkg/aws/mailbox.go — FOUND (MailboxS3API, MailboxMessage, ListMailboxMessages, ReadMessage, ParseSignedMessage, ErrSenderNotAllowed)
- pkg/aws/mailbox_test.go — FOUND (TestListMailboxMessages, TestReadMessage, TestParseSignedMessage tests)
- internal/app/cmd/create.go — FOUND (PublishIdentity call updated)

Commits verified:
- d423f58 — test(17-02): failing tests for identity extension
- f06433e — feat(17-02): extend identity.go
- 52a8d4a — test(17-02): failing tests for mailbox reader
- 0520186 — feat(17-02): create mailbox.go
