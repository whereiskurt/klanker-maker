---
phase: 22-remote-sandbox-creation
plan: "02"
subsystem: email-create-handler
tags: [lambda, ses, eventbridge, ssm, mime, safe-phrase]
dependency_graph:
  requires: [pkg/aws/mailbox.go, pkg/aws/ses.go, pkg/aws/idle_event.go, pkg/profile]
  provides: [cmd/email-create-handler/main.go, KMAuthPattern export]
  affects: [22-03-PLAN.md (orchestrator Lambda)]
tech_stack:
  added: [crypto/subtle, mime/multipart, net/mail, aws-lambda-go]
  patterns: [TDD red-green, narrow interface injection, constant-time comparison]
key_files:
  created:
    - cmd/email-create-handler/main.go
    - cmd/email-create-handler/main_test.go
  modified:
    - pkg/aws/mailbox.go
decisions:
  - "Defined local SESEmailAPI (send-only) instead of using pkg/aws.SESV2API — avoids requiring CreateEmailIdentity/DeleteEmailIdentity in test mocks; narrower interface is correct here"
  - "Defined local putSandboxCreateEvent with SandboxCreateDetail — pkg/aws/eventbridge.go (Plan 01) not yet merged; will consolidate in Plan 03"
  - "profile.Parse used for YAML validation (goccy/go-yaml); invalid YAML that actually fails must produce mapping errors, not just unknown fields"
metrics:
  duration: 232s
  completed: "2026-03-27"
  tasks: 2
  files: 3
---

# Phase 22 Plan 02: Email-Create Handler Lambda Summary

Email-create Lambda that parses SES-delivered MIME emails, validates KM-AUTH safe phrase against SSM, and dispatches SandboxCreate EventBridge events with exported KMAuthPattern regex.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Export KMAuthPattern in mailbox.go | 5c4337d | pkg/aws/mailbox.go |
| 2 (RED) | Add failing tests for email-create-handler | 4050428 | cmd/email-create-handler/main_test.go |
| 2 (GREEN) | Implement email-create-handler Lambda | 6428f7d | cmd/email-create-handler/main.go, main_test.go |

## What Was Built

### Task 1: Export KMAuthPattern
Renamed `kmAuthPattern` to `KMAuthPattern` (exported) in `pkg/aws/mailbox.go`. Updated the single internal call site at line 205. All existing pkg/aws tests pass.

### Task 2: Email-Create Handler Lambda

**`cmd/email-create-handler/main.go`** implements:

- `S3EventRecord`, `S3Record`, `S3Detail`, `S3Bucket`, `S3Object` structs for SES S3 notification JSON deserialization
- `EmailCreateHandler` struct with injectable `EmailCreateS3API`, `SSMClientAPI`, `awspkg.EventBridgeAPI`, `SESEmailAPI` dependencies
- `Handle(ctx, S3EventRecord) error` method:
  1. Fetches raw MIME email from S3
  2. Parses headers with `net/mail.ReadMessage`
  3. Extracts body text and YAML via `extractBodyAndYAML` (multipart or single-part)
  4. Extracts KM-AUTH phrase via `awspkg.KMAuthPattern`
  5. Rejects with email if phrase missing
  6. Fetches expected phrase from SSM with `WithDecryption: true`
  7. Compares using `crypto/subtle.ConstantTimeCompare`
  8. Rejects with email if mismatch
  9. Validates YAML via `profile.Parse`; rejects with error message if invalid
  10. Generates 8-byte hex sandbox ID via `crypto/rand`
  11. Uploads profile to `remote-create/{sandbox-id}/.km-profile.yaml`
  12. Publishes `SandboxCreate` EventBridge event
  13. Sends acknowledgment email

**`cmd/email-create-handler/main_test.go`** (7 tests, all pass):
- `TestS3EventRecord_JSONDeserialization`
- `TestHandleEmailCreate_MultipartYAMLAttachment`
- `TestHandleEmailCreate_PlainTextBody`
- `TestHandleEmailCreate_MissingKMAuth`
- `TestHandleEmailCreate_WrongKMAuth`
- `TestHandleEmailCreate_CorrectKMAuth_ValidYAML`
- `TestHandleEmailCreate_InvalidYAML`

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Narrowed SES interface to avoid mock incompatibility**
- **Found during:** Task 2 GREEN (build failure)
- **Issue:** `awspkg.SESV2API` includes `CreateEmailIdentity` and `DeleteEmailIdentity` methods not needed here; mock couldn't satisfy interface
- **Fix:** Defined local `SESEmailAPI` with `SendEmail` only — narrower interface is more correct for this handler
- **Files modified:** cmd/email-create-handler/main.go
- **Commit:** 6428f7d

**2. [Rule 1 - Bug] Fixed invalid YAML test string**
- **Found during:** Task 2 GREEN (test failure)
- **Issue:** `:::invalid yaml:::` parsed successfully by goccy/go-yaml as an empty document; test expected parse failure
- **Fix:** Changed test input to `"key: : bad"` which produces a genuine mapping key error
- **Files modified:** cmd/email-create-handler/main_test.go
- **Commit:** 6428f7d

**3. [Note] Plan 01 eventbridge.go not yet merged**
- `pkg/aws/eventbridge.go` with `PutSandboxCreateEvent`/`SandboxCreateDetail` does not exist
- Defined these locally in the handler per plan instructions ("Plan 02 can define its own interface if executing in parallel")
- Will be consolidated in Plan 03

## Self-Check: PASSED

- cmd/email-create-handler/main.go: FOUND
- cmd/email-create-handler/main_test.go: FOUND
- pkg/aws/mailbox.go: FOUND
- 5c4337d (Task 1): FOUND
- 4050428 (Task 2 RED): FOUND
- 6428f7d (Task 2 GREEN): FOUND
