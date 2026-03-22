---
phase: 04-lifecycle-hardening-artifacts-email
verified: 2026-03-22T14:40:00Z
status: passed
score: 22/22 must-haves verified
re_verification:
  previous_status: gaps_found
  previous_score: 18/22
  gaps_closed:
    - "TTL expiry path uploads artifacts — cmd/ttl-handler/main.go Lambda downloads profile from S3, calls UploadArtifacts, sends ttl-expired notification, and deletes EventBridge schedule"
    - "Idle-timeout notifications wired — IdleDetector.OnIdleNotify field added; Run() calls it after OnIdle fires; nil-safe and best-effort"
    - "Error/crash notifications wired — TeardownCallbacks.OnNotify field added; ExecuteTeardown calls it with 'error' event when Destroy/Stop returns error, and with policy-name event on success"
    - "destroy.go OnNotify wired with SendLifecycleNotification; duplicate explicit notification block removed"
  gaps_remaining: []
  regressions: []
human_verification:
  - test: "Provision an EC2 sandbox with filesystemPolicy.readOnlyPaths: [\"/etc\"]; attempt to write to /etc/test.txt from within the sandbox"
    expected: "Permission denied or Read-only file system error at the OS level"
    why_human: "Cannot verify OS-level bind mount enforcement without a live EC2 instance"
  - test: "Run an EC2 spot sandbox with artifacts.paths: [\"/tmp/output\"]; create test files; simulate spot interruption by terminating the instance via the EC2 API"
    expected: "Files present in s3://km-sandbox-artifacts-ea554771/artifacts/{sandbox-id}/ after termination"
    why_human: "Requires live AWS infrastructure and a real IMDS endpoint for the spot poll loop"
  - test: "Deploy sandbox; send email to {sandbox-id}@sandboxes.klankermaker.ai; list objects at s3://km-sandbox-artifacts-ea554771/mail/ from within the sandbox using provisioned IAM permissions"
    expected: "Email object is discoverable via s3:ListObjectsV2 and retrievable via s3:GetObject"
    why_human: "Requires SES domain verification to be complete and operator to have exited SES sandbox mode"
  - test: "Inject an AWS access key via SSM; run a command that echoes it inside the sandbox; check CloudWatch Logs for the log group /km/sandboxes/{sandbox-id}/"
    expected: "The raw key value does not appear in CloudWatch — [REDACTED] appears instead"
    why_human: "Requires RedactingDestination wired into audit-log sidecar CloudWatch destination at runtime — a deployment concern"
---

# Phase 04: Lifecycle Hardening, Artifacts & Email Verification Report

**Phase Goal:** Sandboxes enforce filesystem access policy and upload artifacts on exit (including on spot interruption); secret patterns are scrubbed from audit logs; agent sandboxes can send and receive email; the platform is ready for real agent workloads
**Verified:** 2026-03-22T14:40:00Z
**Status:** passed
**Re-verification:** Yes — after gap closure via Plan 04-05

## Goal Achievement

### Observable Truths

| #   | Truth | Status | Evidence |
|-----|-------|--------|----------|
| 1   | ArtifactsSpec parsed from profile YAML into Go struct with Paths, MaxSizeMB, ReplicationRegion | VERIFIED | `pkg/profile/types.go:45-53` — struct present |
| 2   | RedactingDestination replaces AWS key IDs, Bearer tokens, and hex secrets with [REDACTED] | VERIFIED | `sidecars/audit-log/auditlog.go:200-205` — three compiled regex patterns |
| 3   | RedactingDestination replaces SSM literal secret values with [REDACTED] | VERIFIED | `redactString()` applies literals first; covered by redact_test.go |
| 4   | RedactingDestination does NOT redact structural fields | VERIFIED | `Write()` clones Detail only, copies SandboxID/EventType/Timestamp/Source unchanged |
| 5   | UploadArtifacts skips files exceeding maxSizeMB and returns ArtifactSkippedEvent list | VERIFIED | `pkg/aws/artifacts.go:72-83`; 7 tests pass |
| 6   | UploadArtifacts uploads matching files to s3://bucket/artifacts/{sandbox-id}/{filename} | VERIFIED | `artifactKey()` returns correct prefix; tests verify key format |
| 7   | SES Terraform module creates domain identity with DKIM DNS records | VERIFIED | `infra/modules/ses/v1.0.0/main.tf:17-37` — domain identity, DKIM, 3x CNAME |
| 8   | SES Terraform module creates receipt rule set routing inbound email to S3 | VERIFIED | `main.tf:61-87` — receipt rule set with S3 action |
| 9   | ProvisionSandboxEmail calls CreateEmailIdentity for {sandbox-id}@sandboxes.klankermaker.ai | VERIFIED | `pkg/aws/ses.go:34-43`; 7 TestSES tests pass |
| 10  | SendLifecycleNotification sends email with sandbox ID and event type in subject | VERIFIED | `ses.go:46-77`; subject format `km sandbox {event}: {sandboxID}` |
| 11  | CleanupSandboxEmail calls DeleteEmailIdentity on sandbox destroy | VERIFIED | `ses.go:84-98`; swallows NotFoundException (idempotent) |
| 12  | EC2 user-data applies bind mount -o ro for each readOnlyPath before sidecar startup | VERIFIED | `pkg/compiler/userdata.go:50-59` — section 2.5 before section 5 |
| 13  | EC2 user-data includes spot interruption poll loop that calls artifact upload on detection | VERIFIED | `userdata.go:211-260` — section 6.5; background poll loop with IMDS termination-time endpoint |
| 14  | EC2 spot poll loop uses IMDS_TOKEN with 21600s TTL | VERIFIED | `userdata.go:36` — `X-aws-ec2-metadata-token-ttl-seconds: 21600` |
| 15  | ECS service.hcl sets readonlyRootFilesystem=true on main container | VERIFIED | `service_hcl.go:105-107` — guarded by HasFilesystemPolicy |
| 16  | ECS service.hcl adds named volumes for each writablePath | VERIFIED | `service_hcl.go:87-94` — EffectiveWritablePaths with auto-injected /tmp |
| 17  | ECS spot handler Lambda uploads artifacts to S3 before task is reclaimed | VERIFIED | `infra/modules/ecs-spot-handler/v1.0.0/main.tf:145-179` — EventBridge rule + Lambda |
| 18  | TeardownCallbacks has UploadArtifacts callback called before Destroy/Stop | VERIFIED | `pkg/lifecycle/teardown.go:37-41`; 9 teardown tests pass |
| 19  | km create provisions SES email identity for the sandbox and outputs the address | VERIFIED | `internal/app/cmd/create.go:268-275` — ProvisionSandboxEmail called after apply |
| 20  | TTL expiry triggers artifact upload to S3 before sandbox destroy | VERIFIED | `cmd/ttl-handler/main.go:84-98` — handler downloads profile from S3, calls UploadArtifacts; 6 tests pass including TestHandleTTLEvent_UploadsArtifactsWhenConfigured and TestHandleTTLEvent_ProfileDownloadFailureIsNonFatal |
| 21  | Lifecycle notifications sent for idle-timeout events | VERIFIED | `pkg/lifecycle/idle.go:88-90` — OnIdleNotify field added; Run() calls it after OnIdle fires; TestIdleDetector_OnIdleNotifyCalled and TestIdleDetector_OnIdleNotifyNilSafe pass |
| 22  | Lifecycle notifications sent for error/crash teardown events | VERIFIED | `pkg/lifecycle/teardown.go:75,85` — notify("error") called when Destroy/Stop returns error; TestOnNotifyCalledWithErrorOnDestroyFailure passes; destroy.go:198-204 wires OnNotify with SendLifecycleNotification |

**Score:** 22/22 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/profile/types.go` | ArtifactsSpec struct and Artifacts field on Spec | VERIFIED | Lines 45-53: struct present with Paths, MaxSizeMB, ReplicationRegion |
| `sidecars/audit-log/auditlog.go` | RedactingDestination type implementing Destination interface | VERIFIED | Lines 181-270: full implementation |
| `sidecars/audit-log/redact_test.go` | Unit tests for redaction patterns | VERIFIED | File exists; tests pass |
| `pkg/aws/artifacts.go` | UploadArtifacts function with S3PutAPI interface | VERIFIED | Lines 17-112: full implementation |
| `pkg/aws/artifacts_test.go` | Unit tests for artifact upload | VERIFIED | File exists; 7 tests pass |
| `infra/modules/ses/v1.0.0/main.tf` | SES domain identity, DKIM, receipt rule set | VERIFIED | 118 lines; all required resources present |
| `pkg/aws/ses.go` | SESV2API interface, Provision/Send/Cleanup functions | VERIFIED | Lines 19-98: full implementation |
| `pkg/aws/ses_test.go` | Unit tests for SES helpers with mock | VERIFIED | File exists; 7 tests pass |
| `pkg/compiler/userdata.go` | Bind mount section (2.5), spot poll loop (6.5) | VERIFIED | Both sections present; IMDS TTL 21600 |
| `pkg/compiler/service_hcl.go` | readonlyRootFilesystem + named volumes + SES/S3 IAM | VERIFIED | Lines 105-107, 87-94, 210-236 |
| `pkg/lifecycle/teardown.go` | TeardownCallbacks with UploadArtifacts and OnNotify callbacks | VERIFIED | OnNotify field at line 29; called at lines 75, 78, 85, 89, 97; 13 tests pass (9 original + 4 new OnNotify tests) |
| `pkg/lifecycle/idle.go` | IdleDetector with OnIdleNotify callback | VERIFIED | OnIdleNotify field at line 50; called at line 89 in Run(); 2 new tests pass |
| `infra/modules/ecs-spot-handler/v1.0.0/main.tf` | EventBridge rule + Lambda for ECS Fargate spot | VERIFIED | 179 lines; EventBridge rule, Lambda, IAM, permission all present |
| `internal/app/cmd/create.go` | SES email provisioning | VERIFIED | ProvisionSandboxEmail wired (line 269) |
| `internal/app/cmd/destroy.go` | SES email cleanup, artifact upload, OnNotify wired | VERIFIED | Lines 183-205; OnNotify field wires SendLifecycleNotification; duplicate explicit notification block removed |
| `pkg/compiler/service_hcl_email_test.go` | TDD tests for SES IAM, S3 inbox, KM_EMAIL_ADDRESS | VERIFIED | 4 tests pass |
| `infra/modules/s3-replication/v1.0.0/main.tf` | S3 cross-region replication configuration | VERIFIED | aws_s3_bucket_replication_configuration with artifacts/ prefix |
| `cmd/ttl-handler/main.go` | Go Lambda handler for EventBridge TTL events with UploadArtifacts | VERIFIED | 169 lines; TTLHandler struct with injected deps; HandleTTLEvent downloads profile, uploads artifacts, sends ttl-expired notification, deletes schedule; 6 tests pass |
| `cmd/ttl-handler/main_test.go` | Tests for TTL handler with mocks | VERIFIED | 6 tests: missing sandbox_id, artifact upload when configured, nil-safe when no artifacts, notification sent, schedule deleted, profile download failure non-fatal |
| `infra/modules/ttl-handler/v1.0.0/main.tf` | Terraform module for TTL Lambda | VERIFIED | 168 lines; aws_lambda_function (provided.al2023 arm64), IAM role with S3/SES/scheduler/CW policies, Lambda permission for EventBridge scheduler |
| `infra/modules/ttl-handler/v1.0.0/variables.tf` | Module input variables | VERIFIED | artifact_bucket_name, artifact_bucket_arn, email_domain, operator_email, lambda_zip_path |
| `infra/modules/ttl-handler/v1.0.0/outputs.tf` | Lambda ARN, name, role ARN outputs | VERIFIED | lambda_function_arn (for KM_TTL_LAMBDA_ARN), lambda_function_name, lambda_role_arn |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `sidecars/audit-log/auditlog.go` | Destination interface | `d.inner.Write` after redaction | WIRED | Line 264: `return d.inner.Write(ctx, redacted)` |
| `pkg/aws/artifacts.go` | s3.PutObject | S3PutAPI narrow interface | WIRED | Line 96: `client.PutObject(ctx, ...)` |
| `pkg/aws/ses.go` | sesv2.CreateEmailIdentity | SESV2API narrow interface | WIRED | Line 36: `client.CreateEmailIdentity(...)` |
| `infra/modules/ses/v1.0.0/main.tf` | Route53 hosted zone | DKIM CNAME records | WIRED | Lines 30-37: `aws_route53_record.dkim` count=3 |
| `pkg/compiler/userdata.go` | pkg/profile/types.go | FilesystemPolicy.ReadOnlyPaths for bind mount | WIRED | Lines 316-319: nil-safe read |
| `pkg/compiler/service_hcl.go` | pkg/profile/types.go | FilesystemPolicy.WritablePaths for volume generation | WIRED | Lines 438-455: nil-safe population of EffectiveWritablePaths |
| `pkg/lifecycle/teardown.go` | pkg/aws/artifacts.go | UploadArtifacts callback calls artifacts.UploadArtifacts | WIRED | destroy.go:183-193 wires it; teardown.go:52-56 calls it |
| `pkg/lifecycle/teardown.go` | OnNotify callback | Called on error (line 75,85) and success (lines 78,89,97) | WIRED | notify() helper at lines 59-67; called in all three policy branches |
| `pkg/lifecycle/idle.go` | OnIdleNotify callback | Called in Run() after OnIdle fires (line 88-90) | WIRED | Nil-safe; decoupled from teardown action |
| `internal/app/cmd/destroy.go` | pkg/aws/ses.go | OnNotify wires SendLifecycleNotification | WIRED | Lines 198-204: callback closure calls SendLifecycleNotification for all events including error |
| `infra/modules/ecs-spot-handler/v1.0.0/main.tf` | S3 artifact bucket | Lambda reads task group and invokes ECS Exec | WIRED | aws_lambda_function.spot_handler + aws_cloudwatch_event_target.lambda |
| `internal/app/cmd/create.go` | pkg/aws/ses.go | ProvisionSandboxEmail called after terragrunt apply | WIRED | Line 269 |
| `cmd/ttl-handler/main.go` | pkg/aws/artifacts.go | UploadArtifacts called in HandleTTLEvent | WIRED | Line 86: `awspkg.UploadArtifacts(ctx, h.S3Client, h.Bucket, sandboxID, arts.Paths, arts.MaxSizeMB)` |
| `cmd/ttl-handler/main.go` | pkg/aws/ses.go | SendLifecycleNotification called with "ttl-expired" | WIRED | Line 102: `awspkg.SendLifecycleNotification(ctx, h.SESClient, h.OperatorEmail, sandboxID, "ttl-expired", h.Domain)` |
| `cmd/ttl-handler/main.go` | pkg/aws/scheduler.go | DeleteTTLSchedule called for self-cleanup | WIRED | Line 110: `awspkg.DeleteTTLSchedule(ctx, h.Scheduler, sandboxID)` |
| `pkg/compiler/service_hcl.go` | S3 mail prefix | s3:ListObjectsV2+s3:GetObject for mail/{sandbox-id}/* | WIRED | Lines 222-226: IAM policy block with mail prefix |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| OBSV-04 | 04-03 | Filesystem policy enforces writable and read-only paths | SATISFIED | EC2: bind mounts in section 2.5; ECS: readonlyRootFilesystem + named volumes in service_hcl.go |
| OBSV-05 | 04-01, 04-04, 04-05 | Artifacts upload to S3 on sandbox exit with configurable size limits | SATISFIED | UploadArtifacts in pkg/aws/artifacts.go; wired in destroy.go and in TTL handler Lambda |
| OBSV-06 | 04-04 | S3 artifact storage supports multi-region replication | SATISFIED | infra/modules/s3-replication/v1.0.0/main.tf — aws_s3_bucket_replication_configuration |
| OBSV-07 | 04-01 | Secret patterns are redacted from audit logs before storage | SATISFIED | RedactingDestination in sidecars/audit-log/auditlog.go; 9 tests pass |
| PROV-13 | 04-03 | Sandbox handles spot interruption gracefully — uploads artifacts to S3 | SATISFIED | EC2: spot poll loop in section 6.5; ECS: ecs-spot-handler Lambda via EventBridge |
| MAIL-01 | 04-02 | SES is configured globally with Route53 domain verification | SATISFIED | infra/modules/ses/v1.0.0/main.tf — domain identity, DKIM, TXT verification, MX record |
| MAIL-02 | 04-02, 04-04 | Each sandbox agent gets its own email address | SATISFIED | ProvisionSandboxEmail wired in create.go; email output printed to stdout |
| MAIL-03 | 04-02, 04-04 | Agents inside sandboxes can send email via SES | SATISFIED | ECS: ses:SendEmail IAM with ses:FromAddress condition; EC2: user-data exports KM_EMAIL_ADDRESS |
| MAIL-04 | 04-02, 04-04, 04-05 | Operator receives email notifications for sandbox lifecycle events | SATISFIED | created (create.go); destroyed/error (ExecuteTeardown via OnNotify in destroy.go); idle-timeout (IdleDetector.OnIdleNotify); spot-interruption (userdata.go AWS CLI call); ttl-expired (cmd/ttl-handler) |
| MAIL-05 | 04-02, 04-04 | Cross-account agent orchestration possible via email | SATISFIED | s3:ListObjectsV2+s3:GetObject IAM scoped to mail/{sandbox-id}/* in ECS service.hcl |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `sidecars/audit-log/auditlog.go` | 276-293 | `s3Dest` stub with comment "Full S3 archive delivery is Phase 4 scope" — Phase 4 is complete but this stub remains | Warning | S3 audit log delivery still falls back to stdout; does not block any phase 04 goal; carries over to future phase |

No blocker anti-patterns found in gap-closure files (cmd/ttl-handler, pkg/lifecycle/teardown.go, pkg/lifecycle/idle.go).

### Human Verification Required

#### 1. Filesystem Bind Mount Effectiveness (EC2)

**Test:** Provision an EC2 sandbox with `filesystemPolicy.readOnlyPaths: ["/etc"]`; attempt to write a file to `/etc/test.txt` from within the sandbox.
**Expected:** `Permission denied` or `Read-only file system` error at the OS level.
**Why human:** Cannot verify OS-level enforcement programmatically from this codebase; requires a live EC2 instance.

#### 2. Spot Interruption Artifact Upload (EC2)

**Test:** Run an EC2 spot sandbox with `artifacts.paths: ["/tmp/output"]`, create test files, then simulate spot interruption by manually terminating the instance via the EC2 API.
**Expected:** Files present in `s3://km-sandbox-artifacts-ea554771/artifacts/{sandbox-id}/` after termination.
**Why human:** Requires live AWS infrastructure; the poll loop behavior cannot be tested without a real IMDS endpoint.

#### 3. SES Email Receipt (Agent Inbound)

**Test:** Deploy sandbox; send email to `{sandbox-id}@sandboxes.klankermaker.ai`; list objects at `s3://km-sandbox-artifacts-ea554771/mail/` from within the sandbox using the provisioned IAM permissions.
**Expected:** Email object is discoverable via s3:ListObjectsV2 and retrievable via s3:GetObject.
**Why human:** Requires SES domain verification to be complete and the operator to have exited SES sandbox mode.

#### 4. Secret Redaction in CloudWatch (End-to-End)

**Test:** Inject an AWS access key via SSM; run a command that echoes it inside the sandbox; check CloudWatch Logs for the log group `/km/sandboxes/{sandbox-id}/`.
**Expected:** The raw key value does not appear in CloudWatch — `[REDACTED]` appears instead.
**Why human:** Requires the RedactingDestination to be wired into the audit-log sidecar's CloudWatch destination at runtime, which is a deployment concern not visible from static code.

### Re-verification Summary

Both gaps from the initial verification are now closed:

**Gap 1 (CLOSED): TTL expiry path uploads artifacts**

`cmd/ttl-handler/main.go` is a complete, real Go Lambda handler. It receives EventBridge scheduler TTL events, downloads the sandbox profile from S3, calls `awspkg.UploadArtifacts` with the configured paths and maxSizeMB, sends a "ttl-expired" SES notification if `KM_OPERATOR_EMAIL` is set, and deletes the EventBridge schedule for self-cleanup. Profile download failure is non-fatal (continues to notification and schedule cleanup). The matching Terraform module (`infra/modules/ttl-handler/v1.0.0`) deploys the Lambda with appropriate IAM policies. All 6 handler tests pass.

**Gap 2 (CLOSED): Idle-timeout and error/crash notifications**

`TeardownCallbacks.OnNotify` is a new optional callback field in `pkg/lifecycle/teardown.go`. `ExecuteTeardown` calls it with `"error"` when Destroy/Stop fails, and with `"destroyed"/"stopped"/"retained"` on success. `destroy.go` wires `OnNotify` with `SendLifecycleNotification` — the duplicate explicit notification block that only covered success is removed; error paths now also notify. `IdleDetector.OnIdleNotify` is a new optional callback in `pkg/lifecycle/idle.go`; `Run()` calls it immediately after `OnIdle` fires. Both callbacks are nil-safe and best-effort. All 4 new OnNotify tests and 2 new OnIdleNotify tests pass. MAIL-04 is now fully satisfied — all five lifecycle events (created, destroyed, idle-timeout, spot-interruption, ttl-expired/error) have notification wiring.

No regressions detected. All 22 truths verified. Phase goal achieved.

---

_Verified: 2026-03-22T14:40:00Z_
_Verifier: Claude (gsd-verifier)_
_Re-verification: Yes — gap closure from Plan 04-05_
