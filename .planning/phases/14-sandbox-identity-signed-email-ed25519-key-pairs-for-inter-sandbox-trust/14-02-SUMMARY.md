---
phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
plan: "02"
subsystem: identity
tags: [ed25519, nacl, x25519, ssm, dynamodb, ses, email-signing, encryption, crypto]

# Dependency graph
requires:
  - phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
    plan: "01"
    provides: EmailSpec struct, IdentityTableName config, DynamoDB km-identities module

provides:
  - GenerateSandboxIdentity: Ed25519 key pair, SSM SecureString at /sandbox/{id}/signing-key
  - GenerateEncryptionKey: X25519 (NaCl box) key pair, SSM at /sandbox/{id}/encryption-key
  - PublishIdentity: DynamoDB PutItem with sandbox_id, public_key, email_address, encryption_public_key
  - FetchPublicKey: DynamoDB GetItem returning IdentityRecord or nil
  - SignEmailBody / VerifyEmailSignature: Ed25519 sign+verify over body bytes
  - SendSignedEmail: raw MIME via SES Content.Raw with X-KM-Signature, X-KM-Sender-ID, X-KM-Encrypted
  - EncryptForRecipient / DecryptFromSender: NaCl box.SealAnonymous / OpenAnonymous
  - CleanupSandboxIdentity: SSM DeleteParameter (idempotent) + DynamoDB DeleteItem
  - IdentitySSMAPI / IdentityTableAPI: narrow interfaces for DI
  - IdentityRecord: struct for public key lookup results

affects:
  - 14-03 (CLI wiring calls these functions directly)

# Tech tracking
tech-stack:
  added:
    - golang.org/x/crypto/nacl/box (promoted from indirect to direct dependency for X25519 encryption)
  patterns:
    - Narrow IdentitySSMAPI interface (PutParameter/GetParameter/DeleteParameter) — same pattern as SSMAPI in handlers_secrets.go
    - Narrow IdentityTableAPI interface (PutItem/GetItem/DeleteItem) — same pattern as BudgetAPI in budget.go
    - Package-level functions (not methods on struct) — matches SendLifecycleNotification and CleanupSandboxEmail
    - Raw MIME construction via strings.Builder for SES Content.Raw custom headers
    - Encryption policy switch on "required"/"optional"/"off" — clean three-value gate

key-files:
  created:
    - pkg/aws/identity.go
    - pkg/aws/identity_test.go
  modified:
    - go.mod (golang.org/x/crypto promoted to direct)
    - go.sum

key-decisions:
  - "SignEmailBody signs body only (not headers) — per research decision; simpler to verify and avoids SES header rewriting issues"
  - "SendSignedEmail uses Content.Raw (not Content.Simple) — SES Simple strips custom X-KM-* headers; Raw preserves them"
  - "Encryption gate implemented in SendSignedEmail signature directly — Plan 03 passes profile.Email.Encryption value; no extra indirection"
  - "box.SealAnonymous used for encryption — sender identity not embedded; simpler for v1; box.OpenAnonymous on decrypt"
  - "PublishIdentity uses ConditionExpression attribute_not_exists(sandbox_id) then swallows ConditionalCheckFailedException for idempotency"
  - "CleanupSandboxIdentity deletes both signing-key and encryption-key SSM paths — even if encryption key was never generated, swallows ParameterNotFound"

patterns-established:
  - "TDD RED: test file committed first with undefined symbols to prove compile failures"
  - "NaCl box.SealAnonymous / OpenAnonymous pattern for anonymous sender encryption in sandbox email"
  - "Raw MIME builder (strings.Builder + CRLF) pattern for SES custom header injection"

requirements-completed:
  - IDENT-KEYGEN
  - IDENT-SSM
  - IDENT-PUBLISH
  - IDENT-SIGN
  - IDENT-VERIFY
  - IDENT-ENCRYPT
  - IDENT-CLEANUP
  - IDENT-SEND-SIGNED

# Metrics
duration: 5min
completed: 2026-03-23
---

# Phase 14 Plan 02: Sandbox Identity Library Summary

**Ed25519 key generation + SSM storage + DynamoDB publish + signed raw MIME email via SES + NaCl X25519 encryption with policy gate (required/optional/off) — 24 tests, zero regressions**

## Performance

- **Duration:** 5 min
- **Started:** 2026-03-23T03:43:57Z
- **Completed:** 2026-03-23T03:48:46Z
- **Tasks:** 2 (RED + GREEN, TDD)
- **Files modified:** 4 (identity.go, identity_test.go, go.mod, go.sum)

## Accomplishments
- Implemented full sandbox identity cryptographic library: Ed25519 key generation, SSM SecureString storage, DynamoDB publishing, email signing/verification, raw MIME email with custom headers, NaCl X25519 encryption, and idempotent cleanup — all in `pkg/aws/identity.go` (462 lines)
- 24 unit tests covering all exported functions via mock interfaces, including all three encryption policy branches (required/optional/off) and ParameterNotFound idempotency — `pkg/aws/identity_test.go` (823 lines)
- Promoted `golang.org/x/crypto/nacl/box` from transitive indirect to direct dependency (v0.49.0 already in go.sum); no new external dependencies added

## Task Commits

Each task was committed atomically:

1. **Task 1: RED phase (failing identity tests)** - `45271ed` (test)
2. **Task 2: GREEN phase (identity.go implementation)** - `8989239` (feat)

_Note: TDD task had RED commit (failing tests) then GREEN commit (implementation). Auto-fix for nil-pointer in test included in GREEN commit._

## Files Created/Modified
- `pkg/aws/identity.go` — Core identity library: IdentitySSMAPI, IdentityTableAPI, IdentityRecord, GenerateSandboxIdentity, GenerateEncryptionKey, PublishIdentity, FetchPublicKey, SignEmailBody, VerifyEmailSignature, SendSignedEmail, EncryptForRecipient, DecryptFromSender, CleanupSandboxIdentity (462 lines)
- `pkg/aws/identity_test.go` — Full unit test coverage via mockIdentitySSMAPI, mockIdentityTableAPI, mockIdentitySESAPI (823 lines, 24 tests)
- `go.mod` — `golang.org/x/crypto` promoted from indirect to direct require
- `go.sum` — updated checksums

## Decisions Made
- `SignEmailBody` signs body only (not headers) — simpler for recipients to verify; headers can change in transit through SES
- `SendSignedEmail` uses `Content.Raw` not `Content.Simple` — SES Simple message type strips non-standard headers; Raw MIME preserves X-KM-Signature, X-KM-Sender-ID, X-KM-Encrypted
- `box.SealAnonymous` for encryption — sender identity not embedded in ciphertext; sender is identified by X-KM-Sender-ID header; decryption via `box.OpenAnonymous`
- `CleanupSandboxIdentity` deletes both `/signing-key` and `/encryption-key` SSM paths, swallowing `ParameterNotFound` for each — safe even if encryption was never enabled for that sandbox

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed nil *testing.T panic in PublishIdentity test**
- **Found during:** Task 2 (GREEN phase — running tests)
- **Issue:** `makeTestKeys(nil)` called with nil `*testing.T` pointer in `TestIdentity_PublishIdentity_PutItemFields`; caused panic on first run
- **Fix:** Removed the `makeTestKeys(nil)` call and dead variable; test already had `ed25519.GenerateKey(rand.Reader)` inline below it
- **Files modified:** `pkg/aws/identity_test.go`
- **Verification:** All 24 identity tests pass, `go test ./pkg/aws/... -run TestIdentity -v -count=1` exits 0
- **Committed in:** `8989239` (GREEN phase commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - Bug in test)
**Impact on plan:** Minimal — leftover helper call during test authoring. No scope creep.

## Issues Encountered
None — once the nil *testing.T was fixed, all tests passed on the first run.

## User Setup Required
None - no external service configuration required. All AWS interactions use mock interfaces in tests.

## Next Phase Readiness
- All identity functions exported and unit-tested via mock interfaces
- Plan 03 (CLI wiring) can call GenerateSandboxIdentity, PublishIdentity, SendSignedEmail, CleanupSandboxIdentity directly
- IdentitySSMAPI and IdentityTableAPI interfaces are defined — Plan 03 injects real AWS clients
- No blockers

## Self-Check: PASSED

- identity.go: FOUND
- identity_test.go: FOUND
- Commit 45271ed (RED): FOUND
- Commit 8989239 (GREEN): FOUND

---
*Phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust*
*Completed: 2026-03-23*
