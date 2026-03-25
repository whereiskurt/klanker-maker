---
phase: 21-bug-fixes-and-mini-features-budget-precision-polish-small-enhancements
plan: "03"
subsystem: email-approval
tags: [mailbox, approval, ses, email, operator-reply, action-gate]
dependency_graph:
  requires: [21-02]
  provides: [approval-request-send, approval-reply-poll]
  affects: [pkg/aws/ses.go, pkg/aws/mailbox.go]
tech_stack:
  added: [strings (ToUpper for case-insensitive match), net/mail (lightweight reply parse)]
  patterns: [KM-APPROVAL-REQUEST subject format, Re: reply matching, case-insensitive APPROVED/DENIED scan]
key_files:
  created: []
  modified:
    - pkg/aws/ses.go
    - pkg/aws/ses_test.go
    - pkg/aws/mailbox.go
    - pkg/aws/mailbox_test.go
decisions:
  - SendApprovalRequest uses sandboxEmailAddress helper for From (domain already contains subdomain prefix)
  - PollForApproval skips unreadable messages rather than aborting on first error
  - Operator reply matching uses case-insensitive subject contains "Re:" and action string
  - No signature verification for operator replies (external plaintext email, not a signed sandbox message)
metrics:
  duration: 249s
  completed_date: "2026-03-25T21:18:47Z"
  tasks_completed: 2
  files_changed: 4
---

# Phase 21 Plan 03: Action Approval via Email Summary

**One-liner:** SendApprovalRequest sends structured email from sandbox mailbox + PollForApproval scans inbox for operator APPROVED/DENIED reply using case-insensitive subject and body matching.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | SendApprovalRequest — structured email to operator | 115fb6d | pkg/aws/ses.go, pkg/aws/ses_test.go |
| 2 | PollForApproval — scan mailbox for operator reply | 6c3cdbb | pkg/aws/mailbox.go, pkg/aws/mailbox_test.go |

## What Was Built

### Task 1: SendApprovalRequest

- Added `SendApprovalRequest(ctx, client, sandboxID, domain, operatorEmail, action, description string) error` to `pkg/aws/ses.go`
- From address uses `sandboxEmailAddress(sandboxID, domain)` — the sandbox's own SES mailbox so operator replies route back via the SES receipt rule
- Subject format: `[KM-APPROVAL-REQUEST] {sandboxID} {action}`
- Body: `Sandbox {sandboxID} requests approval for action: {action}\n\n{description}\n\nReply with APPROVED to authorize or DENIED to reject.`
- 5 tests: FromAddressIsSandboxMailbox, ToAddressIsOperator, SubjectContainsSandboxIDAndAction, BodyContainsActionAndInstructions, Error

### Task 2: PollForApproval and ApprovalResult

- Added `ApprovalResult` struct: `Found bool`, `Approved bool`, `Denied bool`, `Reply string`
- Added `PollForApproval(ctx, client, bucket, sandboxID, emailDomain, action string) (*ApprovalResult, error)` to `pkg/aws/mailbox.go`
- Calls `ListMailboxMessages` then `ReadMessage` for each key
- Matches reply by subject containing `Re:` and action string (case-insensitive via `strings.ToUpper`)
- Scans body for `APPROVED` or `DENIED` (case-insensitive)
- Skips unreadable/unparseable messages rather than aborting poll
- Returns `&ApprovalResult{Found: false}` when no matching reply exists
- Added `mockPollS3` helper in mailbox_test.go for isolated S3 mocking
- 5 tests: Approved, Denied, NotFound, CaseInsensitive, SubjectMustContainAction

## Verification Results

```
grep -n 'SendApprovalRequest' pkg/aws/ses.go              — shows function at line 87 ✓
grep -n 'PollForApproval' pkg/aws/mailbox.go              — shows function at line 227 ✓
grep -n 'ApprovalResult' pkg/aws/mailbox.go               — shows type at line 210 ✓
go test ./pkg/aws/... -run "TestSendApproval|TestPollForApproval" — all 10 tests PASS ✓
go test ./pkg/aws/... — PASS ✓
```

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed From address construction in SendApprovalRequest**
- **Found during:** Task 1 GREEN phase (test failure)
- **Issue:** Initial implementation used `fmt.Sprintf("%s@sandboxes.%s", sandboxID, domain)` but the domain parameter already contains the full subdomain (e.g. `sandboxes.klankermaker.ai`), resulting in double prefix `sb@sandboxes.sandboxes.klankermaker.ai`
- **Fix:** Changed to use existing `sandboxEmailAddress(sandboxID, domain)` helper which correctly formats `{sandboxID}@{domain}`
- **Files modified:** pkg/aws/ses.go
- **Commit:** 115fb6d (no separate commit needed — fixed before GREEN commit)

**Pre-existing out-of-scope failure:** `TestBootstrapSCPApplyPath` in `internal/app/cmd` fails due to expired AWS SSO credentials — pre-existing issue documented in 21-02-SUMMARY, not caused by this plan's changes.

## Self-Check: PASSED

- pkg/aws/ses.go — FOUND
- pkg/aws/ses_test.go — FOUND
- pkg/aws/mailbox.go — FOUND
- pkg/aws/mailbox_test.go — FOUND
- commit 115fb6d — FOUND
- commit 6c3cdbb — FOUND
