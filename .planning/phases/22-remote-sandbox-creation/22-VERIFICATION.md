---
phase: 22-remote-sandbox-creation
verified: 2026-03-26T21:45:00Z
status: passed
score: 7/7 must-haves verified
re_verification: false
---

# Phase 22: Remote Sandbox Creation — Verification Report

**Phase Goal:** Enable sandbox creation without local terraform/terragrunt, via Lambda dispatch and email triggers
**Verified:** 2026-03-26T21:45:00Z
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths (from ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `km create --remote` compiles locally, uploads to S3, publishes SandboxCreate EventBridge event — no local terraform required | VERIFIED | `runCreateRemote` in `internal/app/cmd/create.go` lines 688-847: parses profile, compiles artifacts, PutObjects to S3 under `remote-create/{sandbox-id}/`, calls `awspkg.PutSandboxCreateEvent`; `--remote` flag wired at line 85-86 |
| 2 | Operator can email YAML profile to `create@sandboxes.{domain}` with `KM-AUTH: <phrase>`; wrong/missing phrase results in rejection email | VERIFIED | `cmd/email-create-handler/main.go`: `KMAuthPattern` extracted, `ConstantTimeCompare` against SSM, rejection path sends email; 4 relevant tests pass (MissingKMAuth, WrongKMAuth, CorrectKMAuth, InvalidYAML) |
| 3 | Create-handler Lambda runs as container image (ECR arm64) with 0 retry attempts on EventBridge | VERIFIED | `infra/modules/create-handler/v1.0.0/main.tf`: `package_type = "Image"`, `architectures = ["arm64"]`, `maximum_retry_attempts = 0` on EventBridge target |
| 4 | On failure, operator receives email with error details; create-handler does not duplicate success notification (km create subprocess handles it) | VERIFIED | `cmd/create-handler/main.go` lines 122-129: sends `create-failed` via `SendLifecycleNotification` on subprocess error; returns nil on success without sending notification; TestCreateHandler_FailurePath passes |
| 5 | EventBridge rule routes SandboxCreate events to create-handler Lambda | VERIFIED | `aws_cloudwatch_event_rule.sandbox_create` matches `source=km.sandbox, detail-type=SandboxCreate`; `aws_cloudwatch_event_target` targets `aws_lambda_function.create_handler.arn` |
| 6 | SES receipt rule routes `create@` emails to `mail/create/` S3 prefix, triggering email-create Lambda | VERIFIED | `infra/modules/ses/v1.0.0/main.tf`: `aws_ses_receipt_rule.create_inbound` with `object_key_prefix = "mail/create/"`, conditional on `email_create_handler_arn` being set; `aws_s3_bucket_notification` routes prefix to Lambda |
| 7 | Dockerfile produces arm64 container bundling km binary, terraform, terragrunt, and infra/modules | VERIFIED | `cmd/create-handler/Dockerfile`: two-stage build — Stage 1 (amazonlinux:2023) downloads terraform 1.9.8 + terragrunt 0.67.16 arm64; Stage 2 (`provided:al2023-arm64`) copies bootstrap, km, terraform, terragrunt, and infra/ directory |

**Score:** 7/7 truths verified

---

### Required Artifacts (All Three Levels: Exists / Substantive / Wired)

| Artifact | Status | Details |
|----------|--------|---------|
| `pkg/aws/eventbridge.go` | VERIFIED | Exports `SandboxCreateDetail` struct and `PutSandboxCreateEvent`; uses shared `EventBridgeAPI` from `idle_event.go`; 57 lines, not a stub |
| `pkg/aws/eventbridge_test.go` | VERIFIED | 3 tests: Success, FailedEntry, ClientError — all pass |
| `internal/app/cmd/create.go` | VERIFIED | `--remote` flag registered (line 98); `runCreateRemote` function exists (line 688); calls `awspkg.PutSandboxCreateEvent` at line 836 |
| `internal/app/cmd/create_remote_test.go` | VERIFIED | Exists; TestCreateRemote_SourceContainsRunCreateRemote passes |
| `cmd/create-handler/main.go` | VERIFIED | `CreateHandler` struct, `CreateEvent` struct, `Handle` method with full S3 download + subprocess + SES failure notification; 180 lines, substantive |
| `cmd/create-handler/exec.go` | VERIFIED | Separation of `runOSExec` from handler logic for clean test injection |
| `cmd/create-handler/main_test.go` | VERIFIED | 4 tests: JSONRoundTrip, HappyPath, FailurePath, OnDemandFlag — all pass |
| `cmd/email-create-handler/main.go` | VERIFIED | `EmailCreateHandler`, `S3EventRecord`, `Handle` method with full MIME parsing + SSM safe phrase + EventBridge dispatch; 330+ lines, substantive |
| `cmd/email-create-handler/main_test.go` | VERIFIED | 7 tests all pass: JSONDeserialization, MultipartYAML, PlainTextBody, MissingKMAuth, WrongKMAuth, CorrectKMAuth, InvalidYAML |
| `pkg/aws/mailbox.go` | VERIFIED | `KMAuthPattern` exported (line 29); used at line 205 internally and referenced by email-create-handler at line 301 |
| `infra/modules/create-handler/v1.0.0/main.tf` | VERIFIED | Contains `aws_lambda_function`, `aws_cloudwatch_event_rule`, `aws_cloudwatch_event_target` with `maximum_retry_attempts = 0`, `aws_lambda_permission` |
| `infra/modules/create-handler/v1.0.0/variables.tf` | VERIFIED | Defines `ecr_image_uri` and all required variables |
| `infra/modules/create-handler/v1.0.0/outputs.tf` | VERIFIED | Exports `lambda_function_arn`, `lambda_role_arn` (with SCP note), `event_rule_arn` |
| `infra/modules/ses/v1.0.0/main.tf` | VERIFIED | `create_inbound` receipt rule (line 74), `aws_s3_bucket_notification` (line 104), both conditional on `email_create_handler_arn` |
| `cmd/create-handler/Dockerfile` | VERIFIED | Multi-stage with `provided:al2023-arm64` runtime stage (line 42) |
| `infra/live/use1/create-handler/terragrunt.hcl` | VERIFIED | Module source `create-handler/v1.0.0` (line 37) |
| `Makefile` | VERIFIED | `build-create-handler`, `build-email-create-handler`, `push-create-handler` targets all present |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/app/cmd/create.go` | `pkg/aws/eventbridge.go` | `awspkg.PutSandboxCreateEvent` call in `runCreateRemote` | WIRED | Line 836: `awspkg.PutSandboxCreateEvent(ctx, ebClient, detail)` |
| `cmd/create-handler/main.go` | `pkg/aws/ses.go` | `awspkg.SendLifecycleNotification` for create-failed | WIRED | Line 124: `awspkg.SendLifecycleNotification(ctx, h.SESClient, ...)` |
| `cmd/email-create-handler/main.go` | `pkg/aws/mailbox.go` | `awspkg.KMAuthPattern` for safe phrase extraction | WIRED | Line 301: `awspkg.KMAuthPattern.FindStringSubmatch(text)` |
| `cmd/email-create-handler/main.go` | `pkg/aws/eventbridge.go` | `PutSandboxCreateEvent` after validation | PARTIAL | Uses local `putSandboxCreateEvent` (line 103) wrapping `awspkg.EventBridgeAPI` interface — same wire, local duplicate function; correct behavior, not consolidated with `pkg/aws.PutSandboxCreateEvent` |
| `infra/modules/create-handler/v1.0.0/main.tf` | `cmd/create-handler/Dockerfile` | `ecr_image_uri` variable | WIRED | `var.ecr_image_uri` at `image_uri = var.ecr_image_uri` (line 409) |
| `infra/modules/ses/v1.0.0/main.tf` | `cmd/email-create-handler/main.go` | S3 notification triggers email-create Lambda | WIRED | `aws_s3_bucket_notification` routes `mail/create/*` to `var.email_create_handler_arn` (line 113) |

---

### Requirements Coverage

**Note on requirement IDs:** REMOTE-01 through REMOTE-06 are phase-local requirement IDs declared in ROADMAP.md and the PLAN frontmatter `requirements:` fields. They do NOT appear in `.planning/REQUIREMENTS.md` as tracked v1 requirements — they were defined as scoping labels within the phase, not as named entries in the project requirements document. The ROADMAP.md Success Criteria serve as the authoritative testable specification for this phase.

| Plan Requirements Field | Status | Notes |
|------------------------|--------|-------|
| REMOTE-01 (Plans 01, 03): Lambda container + EventBridge dispatch | SATISFIED | Create-handler Lambda, Dockerfile, EventBridge rule all exist and are wired |
| REMOTE-02 (Plan 01): `km create --remote` CLI flag + S3 upload + EventBridge publish | SATISFIED | Flag registered, `runCreateRemote` uploads artifacts, calls `PutSandboxCreateEvent` |
| REMOTE-03 (Plans 02, 03): Email-to-create via SES receipt rule + email-create Lambda | SATISFIED | `email-create-handler` Lambda + SES module `create_inbound` rule |
| REMOTE-04 (Plan 02): Safe phrase auth via KM-AUTH + SSM validation | SATISFIED | `ConstantTimeCompare` against SSM param, rejection emails on failure |
| REMOTE-05 (Plans 01, 03): EventBridge rule with 0 retries routing SandboxCreate | SATISFIED | `aws_cloudwatch_event_rule.sandbox_create` + target with `maximum_retry_attempts = 0` |
| REMOTE-06 (Plan 01): Operator email notification on success/failure | SATISFIED | Failure: `create-failed` notification; Success: delegated to km create subprocess |

**ORPHANED IDs:** REMOTE-01 through REMOTE-06 do not exist as named entries in `.planning/REQUIREMENTS.md`. This is expected — the ROADMAP notes only "Requirements: REMOTE-01 through REMOTE-06" as a scoping reference without formal entries in the requirements document. The scoping is satisfied by the ROADMAP Success Criteria, which are fully verified above.

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `cmd/email-create-handler/main.go` | 92-123 | Duplicate `SandboxCreateDetail` struct and local `putSandboxCreateEvent` — plan noted consolidation with `pkg/aws/eventbridge.go` "in Plan 03" but it was not done | Warning | No functional impact; uses correct `awspkg.EventBridgeAPI` interface; struct fields are identical. The email event is published with an extra `EventBusName: "default"` field absent from the pkg/aws version — minor divergence. |

No TODO/FIXME/placeholder patterns found in any phase 22 artifacts.

---

### Pre-Existing Test Failures (Not Phase 22 Regressions)

Two tests in `internal/app/cmd` fail, confirmed pre-existing before phase 22 commits:

- `TestRunInitWithRunnerAllModules` — module ordering assertion mismatch; fails identically at commit `a84b688` (pre-phase 22)
- `TestStatusCmd_Found` — TTL expiry timestamp format check; fails at same pre-phase commit

Phase 22 did not touch `init.go`, `init_test.go`, `status.go`, or `status_test.go`. These failures are pre-existing and out of scope for this phase's verification.

---

### Human Verification Required

#### 1. Container Image End-to-End

**Test:** Build the create-handler container image (`make build-create-handler && make push-create-handler`), invoke the Lambda with a synthetic `SandboxCreate` event containing a valid artifact prefix, and verify km create subprocess runs to completion or fails with a `create-failed` notification email.
**Expected:** Lambda completes within 900s; operator receives either a "created" or "create-failed" email.
**Why human:** Requires live Docker build, ECR push, AWS Lambda deployment, EventBridge test event, and SES delivery chain — not verifiable programmatically.

#### 2. Email-to-Create End-to-End Flow

**Test:** Send an email to `create@sandboxes.{domain}` with a valid YAML profile as a MIME attachment and `KM-AUTH: <correct-phrase>` in the body; then send a second email with a wrong phrase.
**Expected:** First email triggers sandbox creation (acknowledgment email received); second email results in a rejection email.
**Why human:** Requires live SES receipt rule, S3 notification, Lambda invocation, SSM safe phrase lookup, and email delivery — full cloud stack required.

#### 3. SCP Operator Action

**Test:** After deploying `infra/modules/create-handler/v1.0.0`, retrieve the `lambda_role_arn` output and add it to the Phase 10 SCP `trusted_arns_iam` list.
**Expected:** Create-handler role can perform EC2, IAM, ECS, and DynamoDB actions without SCP denial.
**Why human:** Operator infrastructure change; cannot be verified from code alone.

---

## Gaps Summary

No gaps — all automated checks pass. The one warning (duplicate `SandboxCreateDetail` in `cmd/email-create-handler`) does not block goal achievement; the handler produces valid EventBridge events using the correct `awspkg.EventBridgeAPI` interface.

Three items require human verification (live AWS deployment) but all code-level prerequisites are in place.

---

_Verified: 2026-03-26T21:45:00Z_
_Verifier: Claude (gsd-verifier)_
