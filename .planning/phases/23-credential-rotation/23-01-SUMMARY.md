---
phase: 23-credential-rotation
plan: "01"
subsystem: pkg/aws
tags: [rotation, ed25519, ecdsa, ssm, dynamodb, s3, cloudwatch, tdd]
dependency_graph:
  requires:
    - pkg/aws/identity.go (GenerateSandboxIdentity, FetchPublicKey, IdentitySSMAPI, IdentityTableAPI)
    - pkg/aws/cloudwatch.go (CWLogsAPI, isAlreadyExists)
  provides:
    - pkg/aws/rotation.go (RotateSandboxIdentity, UpdateIdentityPublicKey, RotateProxyCACert, ReEncryptSSMParameters, WriteRotationAudit)
  affects:
    - Plan 23-02 (km roll creds Cobra command — consumes these functions)
tech_stack:
  added: []
  patterns:
    - TDD (RED-GREEN)
    - Narrow interface pattern (RotationSSMAPI embeds IdentitySSMAPI + adds GetParametersByPath)
    - Unconditional DynamoDB PutItem for rotation vs. attribute_not_exists for creation
key_files:
  created:
    - pkg/aws/rotation.go
    - pkg/aws/rotation_test.go
  modified: []
decisions:
  - RotationSSMAPI embeds IdentitySSMAPI (adds GetParametersByPath) so RotateSandboxIdentity can call GenerateSandboxIdentity directly without interface conversion
  - UpdateIdentityPublicKey reads existing DynamoDB record via FetchPublicKey before PutItem to preserve alias, allowedSenders, email_address, and policy fields
  - WriteRotationAudit uses its own RotationCWAPI interface (subset of CWLogsAPI) to avoid bringing in the full cloudwatch.go dependency in tests
  - Log stream name format is {YYYY-MM-DD}/{event.Event} for time-based filtering
  - Fingerprint format sha256:XXXXXXXXXXXXXXXX (first 8 bytes = 16 hex chars) matches plan spec
metrics:
  duration: 266s
  completed_date: "2026-03-27"
  tasks_completed: 1
  files_created: 2
---

# Phase 23 Plan 01: Core Credential Rotation Library Summary

Ed25519 key rotation, ECDSA P-256 proxy CA rotation, SSM bulk re-encryption, and CloudWatch audit logging via unconditional PutItem in pkg/aws/rotation.go.

## What Was Built

`pkg/aws/rotation.go` provides the building-block functions for the `km roll creds` Cobra command (Plan 23-02):

- **RotateSandboxIdentity** — fetches old public key fingerprint from DynamoDB, generates new Ed25519 key pair via `GenerateSandboxIdentity` (SSM storage), updates DynamoDB via `UpdateIdentityPublicKey`, returns old+new fingerprints
- **UpdateIdentityPublicKey** — unconditional `PutItem` that reads existing record via `FetchPublicKey` to merge/preserve alias, allowedSenders, email_address, and policy fields before overwriting only the `public_key`
- **RotateProxyCACert** — fetches old cert from S3 for fingerprint, generates ECDSA P-256 CA cert (5-year validity, IsCA=true), uploads PEM cert+key to `sidecars/km-proxy-ca.crt` / `sidecars/km-proxy-ca.key`
- **ReEncryptSSMParameters** — `GetParametersByPath(/sandbox/{id}/, Recursive=true, WithDecryption=true)` then `PutParameter(Overwrite=true, KeyId=kmsKeyID)` for each, returns count
- **WriteRotationAudit** — creates `/km/credential-rotation` log group (idempotent), stream `{date}/{event}`, writes JSON-marshaled `RotationAuditEvent` via PutLogEvents
- **RotationAuditEvent** — struct with Event, SandboxID, KeyType, BeforeFP, AfterFP, Timestamp, Success, Error (JSON-tagged)
- **Fingerprints** — `sha256:XXXXXXXXXXXXXXXX` format (SHA-256 of key/cert bytes, first 8 bytes as hex)

## Tests

`pkg/aws/rotation_test.go` — 11 tests with mock AWS clients:

| Test | Verifies |
|------|----------|
| TestRotateSandboxIdentity_WithExistingKey | oldFP populated, newFP differs, SSM+DynamoDB called |
| TestRotateSandboxIdentity_FreshSandbox | oldFP is empty, newFP non-empty, PutItem called |
| TestUpdateIdentityPublicKey_NoConditionExpression | ConditionExpression is nil on PutItemInput |
| TestUpdateIdentityPublicKey_OverwritesExistingKey | new public_key attribute value in PutItem |
| TestRotateProxyCACert_UploadsToCorrectS3Paths | 2 PutObject calls at correct S3 keys |
| TestRotateProxyCACert_WithExistingCert | old cert read, function succeeds |
| TestReEncryptSSMParameters_ReEncryptsAllParams | path, Recursive, WithDecryption, Overwrite all verified |
| TestReEncryptSSMParameters_EmptyPath | returns 0, no error |
| TestWriteRotationAudit_WritesStructuredJSON | JSON has BeforeFP, AfterFP, SandboxID, Event |
| TestWriteRotationAudit_LogStreamNameIncludesDate | stream name contains date + event type |
| TestEd25519Fingerprint_DeterministicFormat | sha256: prefix + 16 hex chars |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] RotationSSMAPI needed to embed IdentitySSMAPI**
- **Found during:** GREEN phase (build error)
- **Issue:** `RotateSandboxIdentity` calls `GenerateSandboxIdentity(ctx, ssmClient, ...)` which takes `IdentitySSMAPI`. The plan defined `RotationSSMAPI` with its own PutParameter/GetParameter methods, but `*ssm.Client` couldn't satisfy both interfaces simultaneously without embedding.
- **Fix:** Changed `RotationSSMAPI` to embed `IdentitySSMAPI` and add `GetParametersByPath`. Mock in test got `DeleteParameter` stub added.
- **Files modified:** `pkg/aws/rotation.go`, `pkg/aws/rotation_test.go`
- **Commit:** 882b05b

## Verification

```
go test ./pkg/aws/ -run "TestRotat|TestUpdateIdentityPublicKey|TestReEncryptSSMParameters|TestWriteRotationAudit|TestEd25519Fingerprint" -v -count=1
# PASS: 11/11 tests

go vet ./pkg/aws/
# clean (no output)
```

## Self-Check: PASSED

- pkg/aws/rotation.go: FOUND
- pkg/aws/rotation_test.go: FOUND
- Commits: 8234e50 (RED), 882b05b (GREEN)
- All 11 rotation tests pass
- go vet clean
- UpdateIdentityPublicKey verified to NOT use attribute_not_exists
