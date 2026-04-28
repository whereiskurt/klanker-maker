---
phase: 57-email-enhancement-km-send-no-sign-for-external-recipients-km-recv-multipart-rfc5322-fixes-safe-phrase-validation-on-inbound-marketplace-plugin-email-docs
plan: "01"
subsystem: compiler/userdata
tags: [km-send, no-sign, bash, email, heredoc, signing, mime, external-recipients]

requires:
  - phase: 57-00
    provides: 15 RED test stubs in userdata_phase57_test.go locking Phase 57 contract

provides:
  - km-send --no-sign flag in KMSEND heredoc (userdata.go) enabling plain email to external recipients

affects:
  - phase: 57-02 (km-recv RFC 5322 + multipart/alternative + [EXTERNAL] hint — unrelated tests remain RED)
  - phase: 57-03 (km-mail-poller safe phrase gate — unrelated tests remain RED)

tech-stack:
  added: []
  patterns:
    - "Bash set -u safe flag init: NO_SIGN=false, PEM_FILE='', SIGNATURE='' before arg-parse"
    - "Conditional signing gate: if ! $NO_SIGN; then ... else PEM_FILE=''; SIGNATURE=''; fi"
    - "Conditional X-KM-* header emission: [[ -n var ]] && printf pattern in emit_headers()"
    - "KM-AUTH block stays unconditional — runs before signing gate (auth independent of Ed25519)"

key-files:
  created: []
  modified:
    - pkg/compiler/userdata.go

key-decisions:
  - "PEM_FILE and SIGNATURE initialized to empty string before arg-parse so set -u cannot trip when --no-sign skips assignment (Pitfall 2 from RESEARCH.md)"
  - "KM-AUTH operator-inbox safe-phrase append block (lines ~755-771) left unconditional — it is an authentication mechanism independent of Ed25519 signing; tests enforce this ordering"
  - "emit_headers() uses [[ -n var ]] && printf guard for both X-KM-* headers so empty values emit nothing"
  - "SENDER_ID cleared just before build_mime call (not inside emit_headers) so the clearing is visible at the call site"

requirements-completed:
  - PHASE57-KMSEND-NOSIGN

duration: 6min
completed: 2026-04-28T20:30:09Z
---

# Phase 57 Plan 01: km-send --no-sign Flag Summary

**4 surgical edits to the KMSEND heredoc in userdata.go enabling plain email to external (non-sandbox) recipients while keeping KM-AUTH safe-phrase appending unconditional**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-04-28T20:24:58Z
- **Completed:** 2026-04-28T20:30:09Z
- **Tasks:** 2
- **Files modified:** 1

## Accomplishments

- All 3 Phase 57 km-send tests (`TestUserData_KmSend_NoSignFlag`, `TestUserData_KmSend_NoSignOmitsHeaders`, `TestUserData_KmSend_NoSignKeepsAuthPhrase`) turned GREEN
- All 6 pre-existing TestKmSend* tests remain GREEN (SSMFetch, OpensslSign, SESv2Send, PKCS8Prefix, PresentWhenEmailSet, AbsentWhenNoEmail)
- `make build` succeeds: `Built: km v0.2.410 (afc45c0)`

## The 4 Surgical Edits Made to the KMSEND Heredoc

### Edit 1A — Flag-variable initialization (after `REPLY_TO=""`)

Added three new lines so `set -u` cannot trip when `--no-sign` skips SSM/sign assignment:

```bash
NO_SIGN=false
PEM_FILE=""
SIGNATURE=""
```

### Edit 1B — Usage banner extension

Appended `[--no-sign]` to the second usage echo line:

```bash
echo "       [--cc addr1,addr2,...] [--use-bcc] [--reply-to <addr>] [--no-sign]" >&2
```

### Edit 1C — Arg-parse case arm (after `--reply-to`)

Added case arm for the new flag:

```bash
--no-sign)   NO_SIGN=true;  shift ;;
```

### Edit 2A — Gate SSM fetch + openssl sign behind `NO_SIGN`

Replaced the unconditional SSM/sign block with a conditional:

```bash
if ! $NO_SIGN; then
  PRIVKEY_B64=$(aws ssm get-parameter \
    --name "/sandbox/$KM_SANDBOX_ID/signing-key" \
    --with-decryption \
    --query 'Parameter.Value' \
    --output text)
  PEM_FILE=$(ed25519_privkey_to_pem "$PRIVKEY_B64")
  SIGNATURE=$(openssl pkeyutl -sign -inkey "$PEM_FILE" -rawin -in "$BODY_TMP" | base64 -w0)
else
  PEM_FILE=""
  SIGNATURE=""
fi
```

### Edit 2B — Clear SENDER_ID before `build_mime` call

Inserted just before the `MIME_FILE=$(build_mime ...)` call:

```bash
if $NO_SIGN; then
  SENDER_ID=""
fi
```

### Edit 2C — Conditional X-KM-* header emission in `emit_headers()`

Replaced unconditional printf lines with guarded versions:

```bash
[[ -n "$sender_id" ]] && printf 'X-KM-Sender-ID: %s\r\n' "$sender_id"
[[ -n "$signature" ]] && printf 'X-KM-Signature: %s\r\n' "$signature"
```

### Edit 2D — Fork success-message printf

```bash
if $NO_SIGN; then
  echo "[km-send] Sent unsigned email to $TO"
else
  echo "[km-send] Sent signed email to $TO (sig: ${SIGNATURE:0:12}...)"
fi
```

## KM-AUTH Block — Unchanged and Unconditional

The KM-AUTH operator-inbox safe-phrase auto-append block (lines ~755-771) was intentionally left untouched. It runs BEFORE the `if ! $NO_SIGN` signing gate and remains unconditional. `TestUserData_KmSend_NoSignKeepsAuthPhrase` enforces the ordering: `OPERATOR_INBOX=` assignment must appear at a lower string index than `if ! $NO_SIGN`.

## Test Results

```
=== RUN   TestUserData_KmSend_NoSignFlag
--- PASS: TestUserData_KmSend_NoSignFlag (0.00s)
=== RUN   TestUserData_KmSend_NoSignOmitsHeaders
--- PASS: TestUserData_KmSend_NoSignOmitsHeaders (0.00s)
=== RUN   TestUserData_KmSend_NoSignKeepsAuthPhrase
--- PASS: TestUserData_KmSend_NoSignKeepsAuthPhrase (0.00s)
PASS

TestKmSendPresentWhenEmailSet     PASS
TestKmSendAbsentWhenNoEmail       PASS
TestKmSendContainsSSMFetch        PASS
TestKmSendContainsOpensslSign     PASS
TestKmSendContainsSESv2Send       PASS
TestKmSendContainsPKCS8Prefix     PASS
```

## make build Output

```
go build -ldflags '-X .../version.Number=v0.2.410 -X .../version.GitCommit=afc45c0' -o km ./cmd/km/
Built: km v0.2.410 (afc45c0)
```

## Task Commits

1. **Task 1: Wire --no-sign into km-send arg parsing** - `afc45c0`
2. **Task 2: Gate SSM/sign + conditional headers + fork success message** - `1833e27`

## Deviations from Plan

None — plan executed exactly as written. The 4 surgical edits (1A, 1B, 1C for Task 1; 2A, 2B, 2C, 2D for Task 2) were applied as specified in the plan. No pre-existing code paths were disturbed.

Note: `gofmt -l` shows a pre-existing struct field alignment issue in an unrelated struct (~line 1851). This was present before Plan 57-01 and is out of scope per deviation Rule 3 (scope boundary).

## Self-Check: PASSED
