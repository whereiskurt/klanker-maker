---
phase: 45-km-email-send-recv-scripts-and-cli
plan: "02"
subsystem: compiler
tags: [bash, ed25519, mime, ses, email, signing, openssl]

# Dependency graph
requires:
  - phase: 45-01
    provides: Attachment struct and multipart MIME contract in pkg/aws

provides:
  - /opt/km/bin/km-send bash script deployed via userdata.go heredoc
  - ed25519_privkey_to_pem() function with PKCS8 DER envelope construction
  - build_mime() function for single-part and multipart/mixed MIME assembly
  - 6 tests covering km-send deployment and content assertions

affects:
  - 45-03
  - 45-04

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "PKCS8 DER construction via fixed hex prefix (302e020100300506032b657004220420) + 32-byte seed"
    - "Ed25519 signing with openssl pkeyutl -sign -rawin"
    - "SES raw email delivery via base64-encoded MIME blob"
    - "Heredoc scripts inside Go template conditional blocks"

key-files:
  created: []
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go

key-decisions:
  - "PKCS8 prefix is a compile-time constant — Ed25519 OID + DER structure never varies, avoids openssl ASN.1 generation at runtime"
  - "Multipart/mixed boundary generated from /dev/urandom — unique per message, avoids collisions"
  - "km-send uses printf '\\r\\n' for MIME line endings — RFC 2822 compliance inside bash"
  - "parseUserDataTemplate() helper added to userdata.go — enables direct template tests without going through generateUserData"
  - "TestKmSendAbsentWhenNoEmail tests via direct template execution with empty SandboxEmail — generateUserData always populates the field"

patterns-established:
  - "Bash scripts in userdata.go: heredoc delimiters in ALLCAPS (KMSEND, MAILPOLL), chmod+x immediately after"
  - "Ed25519 from SSM: fetch base64, od -An -tx1 to hex, slice seed, xxd -r -p back to binary"

requirements-completed: []

# Metrics
duration: 8min
completed: 2026-04-03
---

# Phase 45 Plan 02: In-sandbox km-send bash script Summary

**km-send pure-bash script deployed to /opt/km/bin/km-send — Ed25519 PKCS8 seed extraction, multipart MIME assembly, and sesv2 raw email sending with 6 passing tests**

## Performance

- **Duration:** ~8 min
- **Started:** 2026-04-03T21:25:00Z
- **Completed:** 2026-04-03T21:33:00Z
- **Tasks:** 5 (1-4 delivered as single commit; Task 5 tests in same commit)
- **Files modified:** 2

## Accomplishments
- `km-send` bash script deployed via userdata.go heredoc inside `{{- if .SandboxEmail }}` block
- `ed25519_privkey_to_pem()`: decodes 64-byte SSM key, extracts 32-byte seed, prepends fixed 16-byte PKCS8 DER prefix, wraps in PEM — no ASN.1 tooling needed
- `build_mime()`: single-part text/plain or multipart/mixed with base64-encoded file attachments and proper `\r\n` MIME line endings
- Signs body with `openssl pkeyutl -sign -rawin`, sends via `aws sesv2 send-email --content "Raw=..."`
- `parseUserDataTemplate()` helper enables direct template execution in tests for empty-SandboxEmail scenario

## Task Commits

Each task was committed atomically:

1. **Tasks 1-4: km-send script + wiring into userdata.go** - `ec01e77` (feat)
2. **Task 5: Tests for km-send deployment** - `ec01e77` (same commit — tests written alongside implementation)

**Plan metadata:** (docs commit to follow)

## Files Created/Modified
- `pkg/compiler/userdata.go` - Added km-send heredoc, ed25519_privkey_to_pem(), build_mime(), parseUserDataTemplate(), bootstrap log line
- `pkg/compiler/userdata_test.go` - 6 new tests: present/absent, SSM fetch, openssl sign, sesv2 send, PKCS8 prefix

## Decisions Made
- PKCS8 DER prefix hard-coded as hex constant — Ed25519 OID (1.3.101.112) and DER framing never change, avoids needing asn1 tooling in sandbox
- `parseUserDataTemplate()` added to production code (not test helper) — small, zero-cost, enables precise template testing
- `TestKmSendAbsentWhenNoEmail` uses direct template execution since `generateUserData()` always populates SandboxEmail with a default domain

## Deviations from Plan

None — plan executed exactly as written. One minor addition: `parseUserDataTemplate()` helper added to support the "absent when no email" test case, which requires direct template access (deviation Rule 2 — missing critical test infrastructure).

## Issues Encountered
None — build and all tests passed on first attempt.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- km-send is deployed and signed; Plans 45-03 and 45-04 can proceed
- The script depends on `/sandbox/$KM_SANDBOX_ID/signing-key` SSM parameter existing (provisioned by km create)
- Ed25519 key format in SSM must be 64-byte base64 (seed||public) — this is the format written by the provisioner

---
*Phase: 45-km-email-send-recv-scripts-and-cli*
*Completed: 2026-04-03*

## Self-Check: PASSED
- FOUND: pkg/compiler/userdata.go
- FOUND: pkg/compiler/userdata_test.go
- FOUND: 45-02-SUMMARY.md
- FOUND commit: ec01e77
