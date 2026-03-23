---
phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
plan: 04
subsystem: identity
tags: [dynamodb, ed25519, km-status, policy-fields, gap-closure]

# Dependency graph
requires:
  - phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
    provides: "Plan 03: identity wiring in create.go, status.go, status_test.go, FetchPublicKey via IdentityFetcher DI"

provides:
  - "IdentityRecord.Signing, VerifyInbound, Encryption fields read from DynamoDB"
  - "PublishIdentity stores policy values as signing_policy, verify_inbound_policy, encryption_policy attrs"
  - "km status Identity section displays Signing, Verify Inbound, Encryption lines (legacy rows show 'unknown')"
  - "IDENT-STATUS-WIRE requirement satisfied"

affects:
  - "phase-15-km-doctor (reads km status output format)"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Conditional DynamoDB attribute storage: non-empty string written, empty omitted for legacy row compatibility"
    - "Display fallback: empty IdentityRecord fields render as 'unknown' in km status output"

key-files:
  created: []
  modified:
    - "pkg/aws/identity.go"
    - "pkg/aws/identity_test.go"
    - "internal/app/cmd/create.go"
    - "internal/app/cmd/status.go"
    - "internal/app/cmd/status_test.go"

key-decisions:
  - "Conditionally add policy DynamoDB attributes only when non-empty — empty string means 'not specified'; omitted attrs preserve full legacy row compatibility without schema migration"
  - "Display 'unknown' when policy field is empty string — communicates that the field exists but was not set at provisioning time (legacy sandbox)"

patterns-established:
  - "Policy field round-trip: store non-empty at create time, read at status time, display with 'unknown' fallback for legacy rows"

requirements-completed:
  - IDENT-STATUS-WIRE

# Metrics
duration: 4min
completed: 2026-03-23
---

# Phase 14 Plan 04: Sandbox Identity Status Wire Summary

**km status Identity section now displays Signing, Verify Inbound, and Encryption policy fields stored in DynamoDB at provisioning time; legacy rows show 'unknown'**

## Performance

- **Duration:** ~4 min
- **Started:** 2026-03-23T04:18:48Z
- **Completed:** 2026-03-23T04:22:54Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments

- Extended `IdentityRecord` with `Signing`, `VerifyInbound`, `Encryption` fields; `PublishIdentity` conditionally stores them as `signing_policy`, `verify_inbound_policy`, `encryption_policy` DynamoDB attributes
- `FetchPublicKey` reads the three policy attributes into `IdentityRecord`; missing attributes return empty strings (no error — legacy row compat)
- `printSandboxStatus` renders three new lines below `Public Key:` in the Identity section; empty fields display as `"unknown"` for legacy DynamoDB rows
- `create.go` now passes `resolvedProfile.Spec.Email.{Signing,VerifyInbound,Encryption}` into `PublishIdentity` so policy values are stored at provisioning time
- All 4 new tests pass; full `go test ./...` passes with zero regressions across 14 packages

## Task Commits

1. **Task 1: Extend IdentityRecord + PublishIdentity + FetchPublicKey with policy fields** - `e0d8271` (feat)
2. **Task 2: Wire policy fields through create.go and display in status.go + status_test.go** - `e4e8059` (feat)

## Files Created/Modified

- `pkg/aws/identity.go` — Added `Signing`, `VerifyInbound`, `Encryption` to `IdentityRecord`; updated `PublishIdentity` signature with three new string params; `FetchPublicKey` reads policy attributes
- `pkg/aws/identity_test.go` — Updated existing `PublishIdentity` call sites (2 tests), added `makeIdentityGetItemOutputWithPolicies` helper, added 4 new tests: `PolicyFieldsStored`, `EmptyPolicyFieldsOmitted`, `PolicyFieldsReadBack`, `LegacyRowEmptyPolicies`
- `internal/app/cmd/create.go` — Wired `resolvedProfile.Spec.Email.{Signing,VerifyInbound,Encryption}` into `PublishIdentity` call at Step 15
- `internal/app/cmd/status.go` — `printSandboxStatus` renders `Signing:`, `Verify Inbound:`, `Encryption:` lines with `"unknown"` fallback
- `internal/app/cmd/status_test.go` — `TestStatus_IdentitySection` gains 6 new assertions for policy labels and values; new `TestStatus_IdentitySection_LegacyRow` verifies `"unknown"` appears 3 times for empty-field record

## Decisions Made

- Conditionally add policy DynamoDB attributes only when non-empty: empty string means "not specified" and the attribute is omitted, preserving full legacy row compatibility without any schema migration
- Display `"unknown"` when a policy field is empty string: communicates that the field exists but was not set at provisioning time (e.g., sandbox was provisioned before Plan 14-04)

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- IDENT-STATUS-WIRE is the final unverified requirement for Phase 14 — phase is now complete
- Phase 15 (km doctor) can proceed; it depends on km status output format which is now stable

---
*Phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust*
*Completed: 2026-03-23*
