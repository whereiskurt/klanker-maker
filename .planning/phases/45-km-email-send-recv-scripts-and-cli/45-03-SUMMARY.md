---
phase: 45-km-email-send-recv-scripts-and-cli
plan: 03
subsystem: email
tags: [bash, mime, ed25519, openssl, dynamodb, sandbox-email]

# Dependency graph
requires:
  - phase: 45-km-email-send-recv-scripts-and-cli
    provides: "Plan 01 — Attachment struct and multipart MIME support in mailbox.go"

provides:
  - km-recv bash script deployed to /opt/km/bin/km-recv via userdata.go heredoc
  - MIME header/body/attachment parser functions (pure bash)
  - Ed25519 public key PEM conversion from 32-byte base64 DER (SubjectPublicKeyInfo)
  - Best-effort signature verification via DynamoDB pubkey lookup + openssl pkeyutl -verify
  - --json, --watch, --no-move flag support
  - Tests verifying km-recv deployment, DynamoDB lookup, openssl verify, mail dir, DER prefix

affects:
  - 45-km-email-send-recv-scripts-and-cli
  - sandbox email tooling
  - multi-agent email coordination

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Ed25519 SubjectPublicKeyInfo DER = fixed 12-byte prefix (302a300506032b6570032100) + 32-byte raw key"
    - "bash-only MIME parsing via grep/sed/awk — no Python/jq dependency"
    - "Signature verification: best-effort, sets SIG_STATUS global (verified|failed|no-key|unsigned|error)"
    - "km-recv deployed inside {{- if .SandboxEmail }} block in userdata.go, after km-mail-poller systemd unit"

key-files:
  created: []
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go

key-decisions:
  - "Ed25519 SubjectPublicKeyInfo DER prefix is fixed 12-byte hex 302a300506032b6570032100 + 32-byte raw key (matches RFC 8410)"
  - "Signature verification is best-effort: failures set status but do not block message display"
  - "km-recv placed after km-mail-poller systemd unit inside {{- if .SandboxEmail }} block to avoid conflicts with parallel km-send plan"
  - "Pure bash + AWS CLI + openssl — no km binary dependency allows use before km is installed"

patterns-established:
  - "verify_signature() sets global SIG_STATUS rather than returning exit code — allows caller to display status without subshell"
  - "process_messages() uses shopt -s nullglob for safe glob over empty directories"

requirements-completed: []

# Metrics
duration: 5min
completed: 2026-04-03
---

# Phase 45 Plan 03: In-sandbox km-recv bash script Summary

**km-recv bash script deployed via userdata.go heredoc — reads /var/mail/km/new/, parses MIME, verifies Ed25519 signatures via DynamoDB pubkey lookup and openssl pkeyutl, outputs human-readable or newline-delimited JSON**

## Performance

- **Duration:** ~5 min
- **Started:** 2026-04-03T20:43:56Z
- **Completed:** 2026-04-03T20:49:02Z
- **Tasks:** 5
- **Files modified:** 2

## Accomplishments

- km-recv script (~300 lines bash) deployed to /opt/km/bin/km-recv in userdata.go
- Full MIME parser: parse_headers(), extract_body() (single-part + multipart), list_attachments(), save_attachment()
- Ed25519 verification via ed25519_pubkey_to_pem() converting 32-byte base64 key to SubjectPublicKeyInfo DER PEM
- verify_signature() does DynamoDB pubkey lookup then openssl pkeyutl -verify -rawin; sets SIG_STATUS
- 6 tests covering deployment gating, DynamoDB lookup, openssl verify, mail dir, DER prefix

## Task Commits

All tasks committed atomically:

1. **Tasks 1-4: km-recv script + MIME helpers + Ed25519 helper + userdata wiring** - `95557d1` (feat)
2. **Task 5: Tests for km-recv deployment** - `95557d1` (feat, same commit)

**Plan metadata:** (see final commit below)

## Files Created/Modified

- `pkg/compiler/userdata.go` - Added km-recv heredoc block inside `{{- if .SandboxEmail }}` section after km-mail-poller systemd unit
- `pkg/compiler/userdata_test.go` - Added 6 tests: TestKmRecvPresentWhenEmailSet, TestKmRecvAbsentWhenNoEmail, TestKmRecvContainsDynamoDBLookup, TestKmRecvContainsOpensslVerify, TestKmRecvContainsMailDir, TestKmRecvContainsSPKIDERPrefix

## Decisions Made

- Ed25519 SubjectPublicKeyInfo DER prefix is the fixed 12-byte hex `302a300506032b6570032100` (RFC 8410 OID for Ed25519 + bitstring wrapper) followed directly by the 32-byte raw key
- Signature verification is best-effort — failures set SIG_STATUS but never block message display; suitable for sandbox use where senders may be unsigned external systems
- km-recv was placed after the km-mail-poller systemd unit (not after km-send) because km-send was being added in parallel by plan 45-02; per instructions km-recv goes after km-mail-poller and before `{{- end }}`
- Pure bash + AWS CLI + openssl avoids km binary dependency, letting agents use km-recv before km is installed

## Deviations from Plan

None - plan executed exactly as written. km-send was already present when the file was read (parallel plan 45-02 had committed), so km-recv was placed after km-send per the parallel execution instructions.

## Issues Encountered

- File was modified between two consecutive reads (km-send added by parallel plan 45-02) — required re-reading before applying edit. Resolved automatically.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- km-recv is deployed and ready for testing in live sandboxes
- Agents can run `km-recv`, `km-recv --json`, `km-recv --watch` to read inbox
- Plan 45-04 (CLI km inbox/km compose commands) can now proceed

---
*Phase: 45-km-email-send-recv-scripts-and-cli*
*Completed: 2026-04-03*
