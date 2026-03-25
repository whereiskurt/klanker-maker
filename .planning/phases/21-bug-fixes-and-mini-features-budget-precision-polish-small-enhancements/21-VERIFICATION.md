---
phase: 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements
verified: 2026-03-25T22:00:00Z
status: human_needed
score: 17/20 must-haves verified
re_verification: false
human_verification:
  - test: "E2E sidecar verification — DNS proxy, HTTP proxy, audit log, OTel tracing"
    expected: "DNS proxy blocks non-allowed domains (NXDOMAIN), HTTP proxy blocks/allows correctly, audit log captures commands, OTel traces appear with sandbox-id tag"
    why_human: "Requires live running sandbox on AWS. Cannot verify sidecar enforcement without real EC2 instance and CloudWatch log groups."
  - test: "GitHub repo cloning and locking — allowed repo clones succeed, non-allowed fail"
    expected: "km configure github sets up GitHub App token; git clone of allowed repo succeeds; git clone of non-allowed repo returns auth failure"
    why_human: "Requires live GitHub App configuration and AWS SSM integration. No unit test covers the full clone flow end-to-end."
  - test: "Inter-sandbox email — two sandboxes send and receive signed email"
    expected: "SendSignedEmail from A to B succeeds; ListMailboxMessages on B returns the message; ParseSignedMessage returns SignatureOK=true"
    why_human: "Requires two live provisioned sandboxes with SES receipting configured and S3 mailbox buckets."
  - test: "Email allow-list enforcement — non-allowed sender is rejected"
    expected: "ParseSignedMessage returns ErrSenderNotAllowed or SignatureOK=false when sender is not in allowedSenders list"
    why_human: "Requires live SES email delivery and multiple sandbox identities. Unit tests verify the parsing logic; live behavior depends on SES receipt rule routing."
  - test: "Safe phrase email override — KM-AUTH phrase triggers authorization"
    expected: "Email containing KM-AUTH: <phrase> causes ParseSignedMessage to return SafePhraseOK=true when phrase matches SSM-stored value"
    why_human: "Safe phrase extraction is unit-tested. End-to-end flow (SSM lookup + comparison against live email) requires live sandbox with SSM parameter set at creation."
  - test: "Klanker action approval via email — sandbox polls for APPROVED/DENIED reply"
    expected: "SendApprovalRequest sends email to operator; after operator replies with APPROVED, PollForApproval returns Approved=true"
    why_human: "Code is implemented and unit-tested. Live flow requires real SES delivery, operator Gmail reply, and S3 mailbox polling."
  - test: "OTP secret sync — secrets injected at boot and deleted from SSM after first read"
    expected: "Sandbox user-data fetches OTP secrets from SSM, exports as KM_OTP_* env vars, then deletes the SSM parameters"
    why_human: "User-data rendering is unit-tested (TestOTPSecretsInjected). Live behavior requires sandbox boot with OTP profile config and SSM parameter pre-seeded."
  - test: "CloudWatch log export to S3 on km destroy — exported .gz objects appear in S3"
    expected: "After km destroy, aws s3 ls s3://<artifact-bucket>/logs/<sandbox-id>/ shows at least one exported object"
    why_human: "ExportSandboxLogs fires CreateExportTask asynchronously. Unit tests verify the API call is made; live verification requires real CloudWatch log data and S3 artifact bucket."
---

# Phase 21: Bug Fixes and Mini-Features Verification Report

**Phase Goal:** Polish, harden, and validate the platform with bug fixes, small features, and E2E verification
**Verified:** 2026-03-25T22:00:00Z
**Status:** human_needed
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | km status shows 4-decimal budget amounts | VERIFIED | `%.4f` at lines 287, 296, 308 of status.go |
| 2 | km budget add output shows 4-decimal amounts | VERIFIED | `%.4f` at line 208 of budget.go |
| 3 | km create budget confirmation shows 4-decimal amounts | VERIFIED | `%.4f` at line 458 of create.go |
| 4 | ConfigUI budget display shows 4-decimal amounts | VERIFIED | `$%.4f` at line 68 of handlers_budget.go |
| 5 | CloudWatch logs exported to S3 before log group deletion during km destroy | VERIFIED | destroy.go:369 calls ExportSandboxLogs before line 377 DeleteSandboxLogGroup |
| 6 | CloudWatch logs exported to S3 before log group deletion during TTL-triggered destroy | VERIFIED | ttl-handler/main.go:302 calls ExportSandboxLogs before line 308 DeleteSandboxLogGroup |
| 7 | TTL Lambda IAM policy grants logs:CreateExportTask permission | VERIFIED | main.tf line 42: `"logs:CreateExportTask"` in cloudwatch_logs policy |
| 8 | S3 artifacts bucket allows logs.amazonaws.com to write to logs/ prefix | VERIFIED | main.tf lines 424-438: aws_s3_bucket_policy resource with logs.amazonaws.com Service principal |
| 9 | Safe phrase embedded in email body extracted and verified during ParseSignedMessage | VERIFIED | mailbox.go: kmAuthPattern regex, SafePhrase/SafePhraseOK fields, extraction at lines 182-203 |
| 10 | Safe phrase generated at km create time and stored in SSM, never in profile YAML | VERIFIED | create.go:489-516: crypto/rand generation, SSM PutParameter to /sandbox/{id}/safe-phrase |
| 11 | OTP secrets listed in profile are injected into sandbox at boot and deleted from SSM | VERIFIED | userdata.go:103-113: OTP section renders get-parameter + delete-parameter bash snippets |
| 12 | OTPSpec type exists in profile types | VERIFIED | types.go: OTPSpec struct with Secrets []string, OTP *OTPSpec on Spec |
| 13 | Sandbox can send an approval request email to the operator | VERIFIED | ses.go:87 SendApprovalRequest with KM-APPROVAL-REQUEST subject format |
| 14 | Sandbox can poll its own mailbox for operator APPROVED/DENIED reply | VERIFIED | mailbox.go:227 PollForApproval with ApprovalResult struct at line 209 |
| 15 | Approval request sets From address to sandbox's own SES address so replies route back | VERIFIED | ses.go uses sandboxEmailAddress helper for From field |
| 16 | Operator has a structured checklist to verify all Phase 21 features on live AWS | VERIFIED | docs/e2e-verification-checklist.md exists (430 lines, 7 sections) |
| 17 | Checklist covers sidecar E2E, GitHub cloning, inter-sandbox email, and allow-list enforcement | VERIFIED | Sections 1-4 cover all four items; sign-off table at lines 412-418 |
| 18 | E2E sidecar verification confirmed on live AWS | HUMAN NEEDED | Checklist exists; operator has not executed against live AWS |
| 19 | GitHub cloning/locking confirmed on live AWS | HUMAN NEEDED | Checklist section 2 documents procedure; live execution not confirmed |
| 20 | OTP sync and action approval confirmed on live AWS | HUMAN NEEDED | VALIDATION.md explicitly marks items 8-9 as manual-only; deferred |

**Score:** 17/20 truths verified (3 require human/live-AWS execution)

---

## Required Artifacts

### Plan 01 Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/cmd/status.go` | 4-decimal budget formatting | VERIFIED | `%.4f` at lines 287, 296, 308 |
| `pkg/aws/cloudwatch.go` | ExportSandboxLogs function | VERIFIED | Function at line 86; CreateExportTask in CWLogsAPI interface at line 28 |
| `internal/app/cmd/destroy.go` | Export-before-delete integration | VERIFIED | ExportSandboxLogs at line 369, DeleteSandboxLogGroup at line 377 |
| `cmd/ttl-handler/main.go` | Export-before-delete in TTL path | VERIFIED | ExportSandboxLogs at line 302, DeleteSandboxLogGroup at line 308 |
| `infra/modules/ttl-handler/v1.0.0/main.tf` | IAM policy + S3 bucket policy for log export | VERIFIED | logs:CreateExportTask at line 42; aws_s3_bucket_policy with logs.amazonaws.com at line 438 |

### Plan 02 Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/aws/mailbox.go` | SafePhrase extraction in ParseSignedMessage | VERIFIED | kmAuthPattern, SafePhrase/SafePhraseOK fields, extraction logic lines 182-203 |
| `pkg/aws/mailbox_test.go` | Tests for safe phrase extraction | VERIFIED | 6 TestSafePhrase* tests at lines 320-578 |
| `pkg/compiler/userdata.go` | OTP secret injection with delete-after-read | VERIFIED | OTP section at lines 103-113; otpEnvName helper at lines 14-19 |

### Plan 03 Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/aws/mailbox.go` | PollForApproval function and ApprovalResult type | VERIFIED | ApprovalResult at line 209; PollForApproval at line 227 |
| `pkg/aws/ses.go` | SendApprovalRequest function | VERIFIED | Function at line 87 with KM-APPROVAL-REQUEST subject |
| `pkg/aws/mailbox_test.go` | Tests for approval polling and reply parsing | VERIFIED | 5 TestPollForApproval_* tests at lines 455-575 |

### Plan 04 Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `docs/e2e-verification-checklist.md` | Structured verification procedure with DNS proxy section | VERIFIED | File exists, 430 lines, section 1.1 covers DNS proxy |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/app/cmd/destroy.go` | `pkg/aws/cloudwatch.go` | ExportSandboxLogs call before DeleteSandboxLogGroup | WIRED | Line 369 export, line 377 delete — correct ordering |
| `cmd/ttl-handler/main.go` | `pkg/aws/cloudwatch.go` | ExportSandboxLogs call before DeleteSandboxLogGroup | WIRED | Line 302 export, line 308 delete — correct ordering |
| `infra/modules/ttl-handler/v1.0.0/main.tf` | AWS CloudWatch Logs CreateExportTask API | IAM policy grants logs:CreateExportTask and logs:DescribeExportTasks | WIRED | Both permissions present in cloudwatch_logs policy |
| `infra/modules/ttl-handler/v1.0.0/main.tf` | S3 artifacts bucket | S3 bucket policy allows logs.amazonaws.com PutObject on logs/ prefix | WIRED | aws_s3_bucket_policy resource with logs.amazonaws.com + SourceAccount condition |
| `pkg/aws/mailbox.go` | ParseSignedMessage | SafePhrase field on MailboxMessage + KM-AUTH pattern extraction | WIRED | kmAuthPattern and extraction wired into ParseSignedMessage body |
| `internal/app/cmd/create.go` | SSM PutParameter | Safe phrase generation and SSM storage at sandbox creation | WIRED | create.go:499 stores at /sandbox/{id}/safe-phrase |
| `pkg/aws/ses.go` | SES SendEmail | SendApprovalRequest sends structured email with KM-APPROVAL-REQUEST subject | WIRED | Subject format confirmed at line 89 of ses.go |
| `pkg/aws/mailbox.go` | ListMailboxMessages + ReadMessage | PollForApproval scans mailbox for reply matching approval request | WIRED | PollForApproval calls ListMailboxMessages then ReadMessage per message |

---

## Scope Item Coverage

| Scope Item | Plan | Implementation Status | E2E Status |
|------------|------|-----------------------|------------|
| 1. Budget display precision (4 decimal places) | 21-01 | IMPLEMENTED — all 4 format strings changed | Automated tests pass; live check is trivial |
| 2. CloudWatch log export on teardown | 21-01 | IMPLEMENTED — ExportSandboxLogs in both teardown paths | HUMAN NEEDED — requires live destroy |
| 3. E2E sidecar verification | 21-04 | Checklist created | HUMAN NEEDED — requires live AWS sandbox |
| 4. GitHub repo cloning/locking validation | 21-04 | Checklist section 2 created | HUMAN NEEDED — requires live GitHub App |
| 5. Inter-sandbox email send/receive test | 21-04 | Checklist section 3 created | HUMAN NEEDED — requires two live sandboxes |
| 6. Email allow-list enforcement test | 21-04 | Checklist section 4 created | HUMAN NEEDED — requires live SES |
| 7. Safe phrase email override | 21-02 | IMPLEMENTED — KM-AUTH extraction in ParseSignedMessage | HUMAN NEEDED — unit-tested; live flow deferred |
| 8. Klanker action approval via email | 21-03 | IMPLEMENTED — SendApprovalRequest + PollForApproval | HUMAN NEEDED — unit-tested; live flow deferred |
| 9. One-time password sync | 21-02 | IMPLEMENTED — OTP injection in user-data with delete-after-read | HUMAN NEEDED — unit-tested; live flow deferred |

**Note:** Scope items 3-6 were designated as the E2E checklist targets by Plan 04. Items 7-9 were implemented in code with unit tests; VALIDATION.md explicitly classifies live execution of items 8-9 as manual-only. The checklist was scoped to items 3-6 per Plan 04 task 1 specification.

---

## Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/app/cmd/bootstrap_test.go` | 100 | TestBootstrapSCPApplyPath requires live AWS KMS/SSO credentials; fails in CI | Info | Pre-existing TDD RED test from phase 10-02 (commit beaeddf). Not a Phase 21 regression; documented in all four SUMMARY files. No Phase 21 code caused this failure. |

No placeholder stubs, empty implementations, or TODO-gated code found in Phase 21 modified files.

---

## Test Suite Results

All Phase 21 packages pass:

| Package | Status |
|---------|--------|
| `pkg/aws` | PASS |
| `pkg/compiler` | PASS |
| `pkg/profile` | PASS |
| `cmd/configui` | PASS |
| `cmd/ttl-handler` | PASS |
| `internal/app/cmd` | FAIL (pre-existing TestBootstrapSCPApplyPath — requires live AWS KMS/SSO, not a Phase 21 regression) |

---

## Human Verification Required

All automated checks passed. The following 8 items require operator execution against live AWS:

### 1. E2E Sidecar Verification

**Test:** Follow docs/e2e-verification-checklist.md Section 1 (DNS proxy, HTTP proxy, audit log, OTel tracing) on a live sandbox
**Expected:** DNS proxy blocks non-allowed domains; HTTP proxy enforces allow-list; audit log captures commands; OTel traces appear with sandbox-id tag
**Why human:** Requires live EC2 sandbox with sidecars running

### 2. GitHub Repo Cloning and Locking

**Test:** Follow checklist Section 2 with a configured GitHub App and live sandbox
**Expected:** Clone of allowed repo succeeds; clone of non-allowed repo returns auth failure
**Why human:** Requires live GitHub App token integration and AWS SSM

### 3. Inter-Sandbox Email Send/Receive

**Test:** Follow checklist Section 3 with two live sandboxes
**Expected:** SendSignedEmail succeeds; ListMailboxMessages on receiver finds the message; ParseSignedMessage returns SignatureOK=true
**Why human:** Requires two live provisioned sandboxes with SES receipt rules

### 4. Email Allow-List Enforcement

**Test:** Follow checklist Section 4 with restrictive allowedSenders config
**Expected:** Non-allowed sender rejected (ErrSenderNotAllowed); self-mail with safe phrase succeeds (SafePhraseOK=true)
**Why human:** Requires live SES delivery and multiple sandbox identities

### 5. Safe Phrase Email Override (live flow)

**Test:** Create sandbox (note printed safe phrase), send email containing KM-AUTH: <phrase>, verify ParseSignedMessage returns SafePhraseOK=true
**Expected:** SafePhraseOK=true when phrase matches SSM-stored value
**Why human:** Full flow requires live SSM parameter, live email delivery, and sandbox context

### 6. Klanker Action Approval via Email (live flow)

**Test:** Call SendApprovalRequest from sandbox context; reply APPROVED from operator Gmail; call PollForApproval and verify Approved=true
**Expected:** ApprovalResult.Approved=true after operator reply
**Why human:** Requires real SES delivery and Gmail reply routing

### 7. OTP Secret Sync (live sandbox boot)

**Test:** Add OTP.Secrets to a profile, pre-seed SSM parameters, create sandbox, verify KM_OTP_* env vars are set and SSM parameters deleted
**Expected:** Env vars present at boot; SSM parameters absent after boot
**Why human:** User-data rendering is unit-tested; live behavior requires real sandbox boot

### 8. CloudWatch Log Export to S3 on Destroy

**Test:** Follow checklist Section 6 — destroy a test sandbox, check s3://<artifact-bucket>/logs/<sandbox-id>/ for .gz export objects
**Expected:** At least one exported object appears under logs/ prefix
**Why human:** CreateExportTask is asynchronous; unit tests verify the API call is made but not that AWS actually wrote data to S3

---

## Gaps Summary

No implementation gaps were found. All Phase 21 code changes exist, are substantive (not stubs), are wired into the correct call sites, and have passing unit tests. The `human_needed` status reflects that 8 of the 9 scope items involve live AWS infrastructure that cannot be verified programmatically from the codebase alone.

The only deviation from the phase goal is that the operator reviewed the E2E checklist but did not execute it against live AWS at phase close-out time (documented in 21-04-SUMMARY.md as a deliberate decision). The checklist document itself is complete and ready for execution.

---

_Verified: 2026-03-25T22:00:00Z_
_Verifier: Claude (gsd-verifier)_
