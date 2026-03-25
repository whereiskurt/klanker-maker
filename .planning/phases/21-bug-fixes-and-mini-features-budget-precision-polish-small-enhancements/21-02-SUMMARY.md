---
phase: 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements
plan: "02"
subsystem: email-auth-otp
tags: [mailbox, safe-phrase, otp, user-data, create-cmd, ssm]
dependency_graph:
  requires: []
  provides: [safe-phrase-extraction, otp-injection, safe-phrase-ssm-storage]
  affects: [pkg/aws/mailbox.go, pkg/compiler/userdata.go, internal/app/cmd/create.go]
tech_stack:
  added: [regexp (kmAuthPattern), crypto/rand, encoding/hex]
  patterns: [KM-AUTH pattern extraction, delete-after-read OTP, non-fatal SSM PutParameter]
key_files:
  created: []
  modified:
    - pkg/aws/mailbox.go
    - pkg/aws/mailbox_test.go
    - pkg/profile/types.go
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go
    - internal/app/cmd/create.go
    - internal/app/cmd/create_test.go
decisions:
  - SafePhraseOK=false when expectedSafePhrase="" (skip check) even when KM-AUTH is present
  - Safe phrase shown once at create time to stdout, never in profile YAML
  - OTP env name derived from last path segment with KM_OTP_ prefix and uppercase
  - Safe phrase generation is non-fatal; sandbox proceeds even if SSM write fails
metrics:
  duration: 452s
  completed_date: "2026-03-25T21:08:11Z"
  tasks_completed: 2
  files_changed: 7
---

# Phase 21 Plan 02: Safe Phrase Email Override and OTP Secret Injection Summary

**One-liner:** Safe phrase KM-AUTH extraction in ParseSignedMessage + OTP delete-after-read injection in user-data + crypto/rand safe phrase generation and SSM storage at km create.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Safe phrase extraction in ParseSignedMessage | 4c35299 | pkg/aws/mailbox.go, pkg/aws/mailbox_test.go |
| 2 | OTP secret sync and safe phrase generation at sandbox creation | 40e653f | pkg/profile/types.go, pkg/compiler/userdata.go, pkg/compiler/userdata_test.go, internal/app/cmd/create.go, internal/app/cmd/create_test.go |

## What Was Built

### Task 1: Safe phrase extraction in ParseSignedMessage

- Added `SafePhrase string` and `SafePhraseOK bool` fields to `MailboxMessage` struct
- Added `expectedSafePhrase string` parameter to `ParseSignedMessage` (backward compatible — pass `""` to skip)
- Added `kmAuthPattern = regexp.MustCompile("(?m)KM-AUTH:\\s*(\\S+)")` for extraction
- Logic: extract phrase from body; if `expectedSafePhrase != ""` and matches, set `SafePhraseOK = true`
- Updated all 7 existing test call sites to pass `""` as the new parameter
- Added 6 new TestSafePhrase* tests: extraction, match, mismatch, absent, empty expected, start-of-line

### Task 2: OTP secret sync and safe phrase generation

**Profile types:**
- Added `OTPSpec` struct with `Secrets []string` field
- Added `OTP *OTPSpec` pointer field to `Spec` struct (nil-safe, yaml:"otp,omitempty")

**User-data template (section 3.5):**
- New OTP section renders bash snippets for each secret: `get-parameter` + export env var + `delete-parameter`
- `otpEnvName()` helper derives `KM_OTP_<UPPERCASE_SEGMENT>` from last SSM path component
- 4 new TestOTP* tests covering injection, env name derivation, absent, multiple secrets

**km create Step 12d:**
- Generates 32-char random hex safe phrase via `crypto/rand`
- Stores at `/sandbox/{sandboxID}/safe-phrase` as SecureString with KMS key
- Prints once: `Safe phrase (save this): <phrase>`
- Non-fatal: sandbox provisioned even if SSM write fails
- `TestCreateSafePhraseStorage` source-level test verifies all required patterns

## Verification Results

```
grep -n 'SafePhrase' pkg/aws/mailbox.go     — shows struct fields and extraction logic ✓
grep -n 'OTPSpec' pkg/profile/types.go      — shows the new spec type ✓
grep -n 'safe-phrase' internal/app/cmd/create.go — shows SSM storage at creation ✓
go test ./... -run TestSafePhrase|TestOTP|TestCreate — all pass ✓
```

## Deviations from Plan

None — plan executed exactly as written.

**Pre-existing out-of-scope failure:** `TestBootstrapSCPApplyPath` in `internal/app/cmd` fails due to expired AWS SSO credentials — not caused by this plan's changes. Logged to deferred-items.

## Self-Check: PASSED

- pkg/aws/mailbox.go — FOUND
- pkg/profile/types.go — FOUND
- pkg/compiler/userdata.go — FOUND
- internal/app/cmd/create.go — FOUND
- commit 4c35299 — FOUND
- commit 40e653f — FOUND
