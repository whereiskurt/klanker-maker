---
phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
verified: 2026-03-23T05:30:00Z
status: passed
score: 14/14 must-haves verified
re_verification:
  previous_status: gaps_found
  previous_score: 13/14
  gaps_closed:
    - "km status displays Signing:, Verify Inbound:, and Encryption: lines in the Identity section when an identity record exists"
  gaps_remaining: []
  regressions: []
---

# Phase 14: Sandbox Identity & Signed Email Verification Report

**Phase Goal:** Every sandbox gets an Ed25519 key pair at creation. Private key stored in SSM (KMS-encrypted), public key published to a DynamoDB km-identities table. Outbound emails are digitally signed, inbound emails can require signature verification, and encryption is optionally layered on via X25519 key exchange. Profile controls (email.signing, email.verifyInbound, email.encryption) govern behavior per sandbox.
**Verified:** 2026-03-23T05:30:00Z
**Status:** passed
**Re-verification:** Yes — after gap closure plan 14-04

## Goal Achievement

### Observable Truths

| #  | Truth | Status | Evidence |
|----|-------|--------|----------|
| 1  | Profile YAML with spec.email section validates via km validate | ✓ VERIFIED | EmailSpec struct on Spec pointer field; JSON schema enum ["required","optional","off"]; go test ./pkg/profile/... passes |
| 2  | spec.email fields accept only required\|optional\|off enum values | ✓ VERIFIED | schemas/sandbox_profile.schema.json lines 63-78 define enum constraint; profile tests pass |
| 3  | Built-in profiles include email policy defaults per security tier | ✓ VERIFIED | hardened.yaml/sealed.yaml: signing:required, verifyInbound:required, encryption:off; open-dev.yaml/restricted-dev.yaml: signing:optional, verifyInbound:optional, encryption:off |
| 4  | IdentityTableName defaults to km-identities in config | ✓ VERIFIED | config.go:120 v.SetDefault("identity_table_name", "km-identities"); config.go:190 wire; config tests pass |
| 5  | DynamoDB identities Terraform module is deployable | ✓ VERIFIED | infra/modules/dynamodb-identities/v1.0.0/main.tf: aws_dynamodb_table with sandbox_id PK, PAY_PER_REQUEST, expiresAt TTL; Terragrunt live config references module correctly |
| 6  | GenerateSandboxIdentity creates Ed25519 key pair and stores private key in SSM | ✓ VERIFIED | identity.go:80-100 uses crypto/ed25519, stores at /sandbox/{id}/signing-key via SSM PutParameter SecureString; all TestIdentity* tests pass |
| 7  | PublishIdentity writes public key to DynamoDB km-identities table | ✓ VERIFIED | identity.go:145: signature now includes signing, verifyInbound, encryption params; conditionally stores signing_policy, verify_inbound_policy, encryption_policy DynamoDB attrs when non-empty |
| 8  | FetchPublicKey retrieves a sandbox public key from DynamoDB | ✓ VERIFIED | identity.go GetItem reads signing_policy, verify_inbound_policy, encryption_policy attributes into IdentityRecord.Signing/VerifyInbound/Encryption; missing attrs return empty string (no error) |
| 9  | SignEmailBody produces a valid Ed25519 signature over body bytes; VerifyEmailSignature returns nil for valid, error for tampered | ✓ VERIFIED | identity.go:224-256; round-trip sign+verify test passes; tampered body returns error |
| 10 | SendSignedEmail constructs raw MIME with X-KM-Signature, X-KM-Sender-ID; encryption policy gates enforced | ✓ VERIFIED | identity.go:308-410 uses Content.Raw; encryption=required rejects when no recipient key; encryption=optional sends plaintext when no key; all three branches tested |
| 11 | CleanupSandboxIdentity deletes SSM key and DynamoDB row idempotently | ✓ VERIFIED | identity.go:424-465 deletes both signing-key and encryption-key SSM paths, swallows ParameterNotFound; DynamoDB DeleteItem |
| 12 | km create generates Ed25519 key pair, stores in SSM, publishes to DynamoDB (non-fatal) | ✓ VERIFIED | create.go:476-484 Step 15: resolvedProfile.Spec.Email.{Signing,VerifyInbound,Encryption} extracted and passed into PublishIdentity; non-fatal |
| 13 | km destroy deletes signing key from SSM and identity row from DynamoDB (idempotent) | ✓ VERIFIED | destroy.go:286 CleanupSandboxIdentity called after SES cleanup; non-fatal |
| 14 | km status shows identity section with public key, signing policy, verify policy, encryption policy | ✓ VERIFIED | status.go:317-344: Identity block renders Public Key:, Signing:, Verify Inbound:, Encryption: lines; empty fields fall back to "unknown"; TestStatus_IdentitySection asserts all three policy labels and values; TestStatus_IdentitySection_LegacyRow asserts "unknown" appears 3 times — both tests PASS |

**Score:** 14/14 truths verified

### Gap Closure Detail (Truth 14 — previously FAILED)

**Previous state:** `status.go:316-326` printed only `Identity:` header and `Public Key:` value. No policy fields.

**Current state:**
- `IdentityRecord` struct gained `Signing`, `VerifyInbound`, `Encryption` string fields (`identity.go:59-61`)
- `PublishIdentity` signature extended with three string params; conditionally stores `signing_policy`, `verify_inbound_policy`, `encryption_policy` DynamoDB attributes when non-empty (`identity.go:145, 162-170`)
- `FetchPublicKey` reads those three attributes into record fields; missing attributes return empty strings without error (`identity.go:226-238`)
- `create.go:476-479` extracts policy values from `resolvedProfile.Spec.Email` and passes them to `PublishIdentity`
- `printSandboxStatus` in `status.go:327-343` renders `Signing:`, `Verify Inbound:`, `Encryption:` lines with `"unknown"` fallback for legacy rows
- `TestStatus_IdentitySection` gains 6 new assertions (3 labels + 3 values); new `TestStatus_IdentitySection_LegacyRow` test verifies 3x `"unknown"` for empty-field record

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/profile/types.go` | EmailSpec struct on Spec | ✓ VERIFIED | EmailSpec struct with signing/verifyInbound/encryption yaml fields |
| `schemas/sandbox_profile.schema.json` | JSON schema for spec.email | ✓ VERIFIED | email property with enum constraints; not in required array |
| `internal/app/config/config.go` | IdentityTableName config field | ✓ VERIFIED | Field, SetDefault("km-identities"), wire all present |
| `infra/modules/dynamodb-identities/v1.0.0/main.tf` | DynamoDB table definition | ✓ VERIFIED | aws_dynamodb_table.identities with sandbox_id PK, PAY_PER_REQUEST, expiresAt TTL |
| `infra/live/use1/dynamodb-identities/terragrunt.hcl` | Terragrunt deployment config | ✓ VERIFIED | References dynamodb-identities/v1.0.0, table_name = "km-identities" |
| `pkg/aws/identity.go` | All identity operations with policy fields | ✓ VERIFIED | IdentityRecord has Signing/VerifyInbound/Encryption; PublishIdentity stores them; FetchPublicKey reads them |
| `pkg/aws/identity_test.go` | Full unit test coverage including policy round-trip | ✓ VERIFIED | 4 new tests: PolicyFieldsStored, EmptyPolicyFieldsOmitted, PolicyFieldsReadBack, LegacyRowEmptyPolicies — all PASS |
| `internal/app/cmd/create.go` | Identity provisioning with policy values | ✓ VERIFIED | create.go:476-479 extracts and passes Signing/VerifyInbound/Encryption from resolved profile |
| `internal/app/cmd/destroy.go` | Identity cleanup in km destroy | ✓ VERIFIED | CleanupSandboxIdentity at line 286; non-fatal |
| `internal/app/cmd/status.go` | Identity section with all policy fields | ✓ VERIFIED | status.go:317-344 renders Public Key, Signing, Verify Inbound, Encryption with "unknown" fallback |
| `internal/app/cmd/status_test.go` | Tests asserting all three policy labels | ✓ VERIFIED | TestStatus_IdentitySection (6 new assertions) + TestStatus_IdentitySection_LegacyRow — both PASS |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `pkg/profile/types.go` | `schemas/sandbox_profile.schema.json` | EmailSpec mirrors JSON schema spec.email | ✓ WIRED | Types match: signing/verifyInbound/encryption fields map to enum ["required","optional","off"] in both |
| `infra/live/use1/dynamodb-identities/terragrunt.hcl` | `infra/modules/dynamodb-identities/v1.0.0` | terraform source reference | ✓ WIRED | source = ".../infra/modules/dynamodb-identities/v1.0.0" |
| `pkg/aws/identity.go` | SSM PutParameter/GetParameter/DeleteParameter | IdentitySSMAPI interface | ✓ WIRED | IdentitySSMAPI defined with all three methods; used throughout |
| `pkg/aws/identity.go` | DynamoDB PutItem/GetItem/DeleteItem | IdentityTableAPI interface | ✓ WIRED | IdentityTableAPI with all three methods; policy attrs stored via PutItem, read via GetItem |
| `pkg/aws/identity.go` | SES SendEmail with Raw content | SESV2API interface | ✓ WIRED | Content.Raw used in SendSignedEmail |
| `internal/app/cmd/create.go` | `pkg/aws/identity.go PublishIdentity` | resolvedProfile.Spec.Email.{Signing,VerifyInbound,Encryption} passed as arguments | ✓ WIRED | create.go:476-479: three policy locals extracted from resolved profile, passed to PublishIdentity |
| `pkg/aws/identity.go FetchPublicKey` | `internal/app/cmd/status.go printSandboxStatus` | IdentityRecord.{Signing,VerifyInbound,Encryption} read and displayed | ✓ WIRED | status.go:327-343 reads identity.Signing, identity.VerifyInbound, identity.Encryption from record returned by FetchIdentity |
| `internal/app/cmd/destroy.go` | `pkg/aws/identity.go CleanupSandboxIdentity` | CleanupSandboxIdentity call | ✓ WIRED | destroy.go:286 CleanupSandboxIdentity; non-fatal |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| IDENT-SCHEMA | 14-01 | EmailSpec struct with signing/verifyInbound/encryption on SandboxProfile | ✓ SATISFIED | pkg/profile/types.go EmailSpec; JSON schema enum validation |
| IDENT-DYNAMO | 14-01 | DynamoDB km-identities table provisioned with sandbox_id PK | ✓ SATISFIED | infra/modules/dynamodb-identities/v1.0.0/main.tf; Terragrunt live config |
| IDENT-CONFIG | 14-01 | IdentityTableName config field defaulting to km-identities | ✓ SATISFIED | internal/app/config/config.go IdentityTableName with default |
| IDENT-KEYGEN | 14-02 | Ed25519 key pair generation via Go crypto/ed25519 stdlib | ✓ SATISFIED | GenerateSandboxIdentity + GenerateEncryptionKey in identity.go |
| IDENT-SSM | 14-02 | Private key stored in SSM at /sandbox/{id}/signing-key as SecureString | ✓ SATISFIED | identity.go:88-100 PutParameter SecureString with KMS key |
| IDENT-PUBLISH | 14-02 | Public key published to DynamoDB km-identities table | ✓ SATISFIED | PublishIdentity now also stores signing_policy, verify_inbound_policy, encryption_policy |
| IDENT-SIGN | 14-02 | Outbound email signed with Ed25519; X-KM-Signature header | ✓ SATISFIED | SignEmailBody + SendSignedEmail Content.Raw with X-KM-Signature header |
| IDENT-VERIFY | 14-02 | VerifyEmailSignature validates Ed25519 signatures | ✓ SATISFIED | VerifyEmailSignature returns nil for valid, error for tampered/wrong key |
| IDENT-ENCRYPT | 14-02 | Optional NaCl box encryption via X25519 key exchange | ✓ SATISFIED | EncryptForRecipient / DecryptFromSender; SendSignedEmail encryption policy gate |
| IDENT-CLEANUP | 14-02 | SSM and DynamoDB cleanup idempotent | ✓ SATISFIED | CleanupSandboxIdentity swallows ParameterNotFound; DeleteItem is no-op for missing key |
| IDENT-SEND-SIGNED | 14-02 | SendSignedEmail with encryption policy enforcement | ✓ SATISFIED | All three branches (required/optional/off) implemented and tested |
| IDENT-CREATE-WIRE | 14-03 | km create provisions Ed25519 key pair (non-fatal) | ✓ SATISFIED | create.go Step 15 with email section guard, non-fatal error handling |
| IDENT-DESTROY-WIRE | 14-03 | km destroy cleans up SSM key and DynamoDB row | ✓ SATISFIED | destroy.go Step 11 CleanupSandboxIdentity, non-fatal |
| IDENT-STATUS-WIRE | 14-04 | km status displays public key, signing policy, and encryption policy | ✓ SATISFIED | status.go:317-344 displays all four fields; six new test assertions pass; legacy row shows "unknown" |

**Note on REQUIREMENTS.md traceability:** The IDENT-* requirement IDs are phase-internal and are not tracked in the global `.planning/REQUIREMENTS.md` traceability table. The global requirements (MAIL-01 through MAIL-05) corresponding to email functionality are mapped to Phase 4. Phase 14 extends email with identity/signing — these IDENT-* IDs exist only in the phase PLAN frontmatter. No orphaned global requirements detected.

### Anti-Patterns Found

None. No TODO/FIXME/placeholder patterns in modified files. All policy fields render real data, not stub values.

### Human Verification Required

None. All automated checks are sufficient for this phase's scope.

### Test Suite Results

Full `go test ./...` run confirms zero regressions:

- `internal/app/cmd` — PASS (11 status tests, including TestStatus_IdentitySection and TestStatus_IdentitySection_LegacyRow)
- `pkg/aws` — PASS (28 identity tests, including 4 new policy round-trip tests)
- All 14 tested packages — PASS

### Re-verification Summary

The single gap identified in the initial verification is now fully closed. Plan 14-04 extended the data model, storage, retrieval, display, and test assertions for policy fields in a coherent change across 5 files (commits `e0d8271` and `e4e8059`).

All 14 phase must-haves are verified. Phase 14 goal is achieved.

---

_Verified: 2026-03-23T05:30:00Z_
_Verifier: Claude (gsd-verifier)_
