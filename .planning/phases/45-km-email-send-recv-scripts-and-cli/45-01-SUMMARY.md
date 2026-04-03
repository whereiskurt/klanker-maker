---
phase: 45-km-email-send-recv-scripts-and-cli
plan: 1
subsystem: api
tags: [go, mime, multipart, email, ed25519, nacl, ses, attachments]

# Dependency graph
requires:
  - phase: identity-email
    provides: "Ed25519 signing/verification, NaCl box encryption, SendSignedEmail, ParseSignedMessage"
provides:
  - "Attachment struct (Filename, Data) in pkg/aws"
  - "buildRawMIME produces multipart/mixed when attachments present"
  - "SendSignedEmail accepts []Attachment; signature covers body only"
  - "ParseSignedMessage extracts attachments from multipart/mixed; decodes base64 parts"
  - "MailboxMessage.Attachments []Attachment field"
affects: [45-02, 45-03, bash-email-scripts, km-email-cli]

# Tech tracking
tech-stack:
  added: [mime, mime/multipart, encoding/hex]
  patterns:
    - "Signature-body-only: Ed25519 signs text part only, never attachment data"
    - "Graceful base64 decode: invalid CTE bytes kept raw, no error returned"
    - "Backward-compatible nil attachments: single-part text/plain behavior preserved"

key-files:
  created: []
  modified:
    - pkg/aws/identity.go
    - pkg/aws/mailbox.go
    - pkg/aws/identity_test.go
    - pkg/aws/mailbox_test.go

key-decisions:
  - "Signature covers text body only, not attachment data — attachments can change without invalidating message auth"
  - "Attachment.Data is decoded bytes (base64 CTE decoded on parse) — callers get raw bytes not encoded strings"
  - "generateMIMEBoundary uses crypto/rand (32 hex chars) — no collisions with body content"
  - "Graceful CTE decode failure: keep raw bytes, no error — allows non-standard senders"

patterns-established:
  - "Nil/empty attachments → single-part text/plain (backward compat); non-empty → multipart/mixed"
  - "ParseSignedMessage: first text/plain part = body (verified), Content-Disposition: attachment parts = Attachments"

requirements-completed: []

# Metrics
duration: 5min
completed: 2026-04-03
---

# Phase 45 Plan 1: Multipart MIME support in pkg/aws Summary

**Attachment type + multipart/mixed MIME production and parsing in pkg/aws using Ed25519 body-only signatures and base64 CTE decoding**

## Performance

- **Duration:** 5 min
- **Started:** 2026-04-03T20:35:09Z
- **Completed:** 2026-04-03T20:40:48Z
- **Tasks:** 6
- **Files modified:** 4

## Accomplishments
- Added `Attachment` struct and extended `buildRawMIME` to produce `multipart/mixed` messages with `application/octet-stream` parts when attachments are provided
- Extended `SendSignedEmail` with `[]Attachment` parameter; signature still covers text body only via `bodyToSign`
- Extended `ParseSignedMessage` to detect `multipart/mixed`, extract body from first `text/plain` part, and decode `base64` CTE attachment parts into raw bytes; `MailboxMessage.Attachments` field added
- Full test coverage: 8 new tests across identity_test.go and mailbox_test.go including a complete round-trip (build → parse → verify)

## Task Commits

Each task was committed atomically:

1. **Tasks 1+2: Attachment type, buildRawMIME multipart, SendSignedEmail update** - `6a95143` (feat)
2. **Task 3: ParseSignedMessage multipart + base64 decode** - `a01ac1b` (feat)
3. **Task 4: Tests for multipart buildRawMIME** - `6416bfc` (test)
4. **Tasks 5+3-fix: multipart ParseSignedMessage tests + base64 decode fix** - `1886dba` (test)
5. **Task 6: Round-trip test** - `07f99f6` (test)

## Files Created/Modified
- `/Users/khundeck/working/klankrmkr/pkg/aws/identity.go` - Added `Attachment` struct, `generateMIMEBoundary()`, extended `buildRawMIME` and `SendSignedEmail`
- `/Users/khundeck/working/klankrmkr/pkg/aws/mailbox.go` - Added `Attachments []Attachment` to `MailboxMessage`, multipart parsing in `ParseSignedMessage`, base64 CTE decoding
- `/Users/khundeck/working/klankrmkr/pkg/aws/identity_test.go` - Updated 7 existing `SendSignedEmail` calls to pass `nil`; added 5 new multipart tests
- `/Users/khundeck/working/klankrmkr/pkg/aws/mailbox_test.go` - Added `buildMultipartTestMIME` helper + 4 multipart `ParseSignedMessage` tests

## Decisions Made
- Signature covers text body only (not attachments) — matches plan spec and preserves the invariant that auth applies to the communication content, not file payloads
- `ParseSignedMessage` decodes `Content-Transfer-Encoding: base64` parts so callers receive raw bytes rather than encoded strings
- Graceful CTE decode failure: keep raw bytes without returning an error (non-standard senders should not hard-fail parsing)
- `generateMIMEBoundary` uses `crypto/rand` for a 32-hex-char boundary — avoids collision with body content

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Base64 attachment data not decoded in ParseSignedMessage**
- **Found during:** Task 5 (multipart ParseSignedMessage tests)
- **Issue:** `partData` from `multipart.Part.Read` contains the raw base64-encoded string when `Content-Transfer-Encoding: base64` is set; `Attachment.Data` was returning encoded bytes instead of raw bytes
- **Fix:** Added base64 CTE detection and decoding in the attachment branch; whitespace stripped before decoding; graceful fallback to raw bytes on decode failure
- **Files modified:** pkg/aws/mailbox.go
- **Verification:** `TestParseSignedMessage_Multipart_ExtractsBodyAndAttachments` passes; round-trip test confirms binary data integrity
- **Committed in:** `1886dba` (Task 5 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - Bug)
**Impact on plan:** Essential correctness fix — without decoding, callers would receive base64 strings not binary data.

## Issues Encountered
None beyond the base64 CTE decode issue documented above.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- `pkg/aws` multipart MIME foundation complete; wave 1 done
- Plan 45-02 (bash send/receive scripts) and 45-03 (Go CLI `km email` commands) can now use `Attachment` type and multipart `SendSignedEmail`/`ParseSignedMessage`
- All 6 plan tasks complete; `go test ./pkg/aws/... -v` passes with zero failures

## Self-Check: PASSED

---
*Phase: 45-km-email-send-recv-scripts-and-cli*
*Completed: 2026-04-03*
