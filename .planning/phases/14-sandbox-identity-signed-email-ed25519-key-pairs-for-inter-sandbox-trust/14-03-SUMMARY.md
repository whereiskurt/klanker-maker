---
phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
plan: 03
subsystem: identity
tags: [ed25519, x25519, ssm, dynamodb, cli, km-create, km-destroy, km-status, dependency-injection]

# Dependency graph
requires:
  - phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
    provides: "Plan 02: identity.go — GenerateSandboxIdentity, GenerateEncryptionKey, PublishIdentity, FetchPublicKey, CleanupSandboxIdentity"

provides:
  - "km create provisions Ed25519 signing key + optional X25519 encryption key; publishes to DynamoDB identities table (non-fatal Step 15)"
  - "km destroy cleans up signing/encryption keys from SSM and identity row from DynamoDB (idempotent Step 11)"
  - "km status displays Identity section with truncated public key when sandbox has a published identity"
  - "IdentityFetcher DI interface + realIdentityFetcher + NewStatusCmdWithAllFetchers for testable identity display"

affects:
  - "Phase 15 (km doctor): may want to verify identity table and SSM key existence"
  - "Any phase that reads km status output"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Non-fatal Step 15 in km create: email section guard + GenerateSandboxIdentity + conditional GenerateEncryptionKey + PublishIdentity"
    - "Non-fatal Step 11 in km destroy: CleanupSandboxIdentity after SES cleanup, mirroring Step 10 pattern"
    - "IdentityFetcher DI interface: parallel to BudgetFetcher — NewStatusCmdWithAllFetchers accepts 4th parameter"
    - "realIdentityFetcher backed by kmaws.FetchPublicKey — satisfies IdentityTableAPI interface"
    - "Source-level verification test pattern: os.ReadFile + strings.Contains for CLI wiring confirmation"
    - "NewStatusCmdWithFetchers delegates to NewStatusCmdWithAllFetchers(nil) for backward compatibility"

key-files:
  created: []
  modified:
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go
    - internal/app/cmd/status.go
    - internal/app/cmd/status_test.go
    - internal/app/cmd/create_test.go

key-decisions:
  - "KMS key alias for identity keys uses KM_PLATFORM_KMS_KEY_ARN env var with alias/km-platform fallback — same approach as Step 13a GitHub token (avoids cfg.Label/cfg.Region fields that don't exist on Config)"
  - "NewStatusCmdWithFetchers delegates to NewStatusCmdWithAllFetchers with nil identity fetcher — preserves backward compatibility for all existing callers"
  - "Identity section in km status shows first 16 chars of base64 public key + '...' suffix — consistent truncation for display"
  - "Identity fetch error in km status is silently swallowed (same as budget) — graceful degradation over correctness failure"
  - "Step 11 (identity cleanup) runs AFTER Step 10 (SES cleanup) — both non-fatal, both post-terraform-destroy"

patterns-established:
  - "IdentityFetcher: DI interface parallel to BudgetFetcher with FetchIdentity(ctx, sandboxID) (*IdentityRecord, error)"
  - "Source-level verification tests for CLI wiring: read source file, check call site patterns via strings.Contains"

requirements-completed:
  - IDENT-CREATE-WIRE
  - IDENT-DESTROY-WIRE
  - IDENT-STATUS-WIRE

# Metrics
duration: 6min
completed: 2026-03-23
---

# Phase 14 Plan 03: CLI Identity Lifecycle Wiring Summary

**Ed25519 signing key + optional X25519 encryption key wired into km create/destroy/status with non-fatal patterns and IdentityFetcher DI interface**

## Performance

- **Duration:** 6 min (365 seconds)
- **Started:** 2026-03-23T03:52:57Z
- **Completed:** 2026-03-23T04:00:00Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments

- km create (Step 15): generates Ed25519 signing key via GenerateSandboxIdentity, optionally generates X25519 encryption key when profile.email.encryption is "optional" or "required", publishes both to DynamoDB via PublishIdentity — all non-fatal
- km destroy (Step 11): calls CleanupSandboxIdentity to delete SSM signing/encryption key parameters and DynamoDB identity row — idempotent, non-fatal, mirrors Step 10 SES cleanup pattern
- km status: IdentityFetcher DI interface, realIdentityFetcher, NewStatusCmdWithAllFetchers, identity section with truncated public key, graceful degradation on fetch error or missing record

## Task Commits

Each task was committed atomically:

1. **Task 1: Wire identity provisioning into km create + cleanup into km destroy** - `8dfec5f` (feat)
2. **Task 2: Add IdentityFetcher to km status** - `6f55622` (feat)

## Files Created/Modified

- `internal/app/cmd/create.go` - Added Step 15: identity provisioning (GenerateSandboxIdentity + optional GenerateEncryptionKey + PublishIdentity), non-fatal, guarded by `Spec.Email != nil`
- `internal/app/cmd/destroy.go` - Added Step 11: CleanupSandboxIdentity after Step 10 SES cleanup; added dynamodbpkg import
- `internal/app/cmd/status.go` - IdentityFetcher interface, realIdentityFetcher, NewStatusCmdWithAllFetchers, identity section in printSandboxStatus, runStatus accepts identityFetcher
- `internal/app/cmd/status_test.go` - fakeIdentityFetcher, runStatusCmdWithAllFetchers helper, TestStatus_IdentitySection, TestStatus_IdentityFetchError, TestStatus_NoIdentity
- `internal/app/cmd/create_test.go` - TestRunCreate_IdentityProvisioning, TestRunDestroy_IdentityCleanup (source-level verification)

## Decisions Made

- KMS key alias uses `KM_PLATFORM_KMS_KEY_ARN` env var (alias/km-platform fallback) — same as Step 13a GitHub token approach, since cfg.Label and cfg.Region fields do not exist on config.Config
- `NewStatusCmdWithFetchers` delegates to `NewStatusCmdWithAllFetchers(nil)` for backward compatibility — zero changes to existing test helpers
- Identity section shows first 16 chars of base64 public key + "..." — concise display without revealing full key

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] cfg.Label and cfg.Region do not exist on Config — fixed KMS alias construction**
- **Found during:** Task 1 (create.go identity provisioning)
- **Issue:** Plan specified `fmt.Sprintf("alias/km-%s-ssm-%s-%s", cfg.Label, cfg.Region, sandboxID)` but Config struct has no Label or Region fields
- **Fix:** Used `os.Getenv("KM_PLATFORM_KMS_KEY_ARN")` with `alias/km-platform` fallback — matches existing Step 13a GitHub token pattern; also updated test pattern assertion to `alias/km-platform`
- **Files modified:** internal/app/cmd/create.go, internal/app/cmd/create_test.go
- **Verification:** Build passes, TestRunCreate_IdentityProvisioning passes
- **Committed in:** 8dfec5f (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - Bug)
**Impact on plan:** Fix was necessary for compilation. Pattern follows established KM_PLATFORM_KMS_KEY_ARN approach from Phase 13. No scope creep.

## Issues Encountered

None — both tasks executed cleanly after the KMS alias fix.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Phase 14 is now complete: identity library (Plan 02) is wired into all three CLI lifecycle points
- Phase 15 (km doctor): can verify identity SSM parameters and DynamoDB identity table health
- Identity provisioning is non-fatal throughout — operator can confirm identity records via `km status`

---
*Phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust*
*Completed: 2026-03-23*
