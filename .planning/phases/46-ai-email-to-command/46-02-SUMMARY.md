---
phase: 46-ai-email-to-command
plan: 02
subsystem: ai
tags: [bedrock, haiku, email, conversation-state, eventbridge, tdd, terraform]

# Dependency graph
requires:
  - phase: 46-ai-email-to-command
    plan: 01
    provides: BedrockRuntimeAPI interface, callHaiku, ConversationState, extractThreadID, loadConversation, saveConversation
provides:
  - AI interpretation dispatch path wired into Handle() in cmd/email-create-handler
  - handleAIInterpretation: Haiku invocation → info/action routing
  - handleConversationReply: yes/cancel/revision conversation state machine
  - executeConfirmedCommand: EventBridge dispatch for create (S3 profile upload) and destroy/extend/pause/resume
  - Terraform: bedrock:InvokeModel IAM policy, Lambda timeout 120s, BEDROCK_MODEL_ID env var
affects:
  - infra/live/use1/email-handler (Terraform apply needed for IAM, timeout, env var)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "replyIntent() line scanner: skips KM-AUTH prefix lines to find yes/cancel/revision intent"
    - "Graceful nil DynamoClient: info commands skip sandbox listing when DynamoClient is nil"
    - "Dual fast-path preservation: YAML+create subject and status subject bypass Haiku"

key-files:
  created: []
  modified:
    - cmd/email-create-handler/main.go
    - cmd/email-create-handler/main_test.go
    - infra/modules/email-handler/v1.0.0/main.tf
    - infra/modules/email-handler/v1.0.0/variables.tf

key-decisions:
  - "replyIntent() scans lines not full body — KM-AUTH prefix lines precede yes/cancel keyword"
  - "mockDynamo added to main_test.go — newTestHandlerWithAI sets DynamoClient to avoid nil panic"
  - "Non-create EventBridge dispatch uses awspkg.PublishSandboxCommand(sandboxID, eventType) — existing signature"

requirements-completed: []

# Metrics
duration: 9min
completed: 2026-04-04
---

# Phase 46 Plan 02: AI Email Dispatch Wiring Summary

**AI interpretation path wired into email handler dispatch with confirmation flow, conversation reply handling (yes/cancel/revision), EventBridge dispatch on confirmation, and Terraform Bedrock IAM + timeout updates**

## Performance

- **Duration:** ~9 min
- **Started:** 2026-04-04T02:34:43Z
- **Completed:** 2026-04-04T02:44:00Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments

- Refactored Handle() dispatch: thread check → YAML fast-path → status fast-path → AI path → help fallback
- handleAIInterpretation routes info commands (list/status) to immediate execution, action commands to confirmation template
- handleConversationReply implements yes/cancel/revision state machine with `replyIntent()` line scanner
- executeConfirmedCommand dispatches EventBridge create (with S3 profile YAML upload) and destroy/extend/pause/resume
- handleRevision calls Haiku second round with original command context + operator revision message
- 10 new tests covering all required behaviors from plan behavior block
- Terraform: bedrock:InvokeModel IAM policy scoped to model ARN, timeout 120s, BEDROCK_MODEL_ID env var, bedrock_model_id variable

## Task Commits

1. **Task 1: Refactor main.go dispatch + add AI path + conversation reply handling** - `7c0db73` (feat)
2. **Task 2: Update Terraform — Bedrock IAM policy, Lambda timeout, env var** - `04a4530` (feat)

## Files Created/Modified

- `cmd/email-create-handler/main.go` — BedrockClient/BedrockModelID fields, Handle() refactor, 6 new methods, main() Bedrock init
- `cmd/email-create-handler/main_test.go` — mockDynamo, newTestHandlerWithAI, buildPlainEmailWithHeaders, 10 new tests
- `infra/modules/email-handler/v1.0.0/main.tf` — aws_iam_role_policy.bedrock_invoke, timeout 120, BEDROCK_MODEL_ID env var
- `infra/modules/email-handler/v1.0.0/variables.tf` — bedrock_model_id variable with default

## Decisions Made

- `replyIntent()` line scanner: operator emails contain KM-AUTH line before the yes/cancel keyword. Full-body `HasPrefix` fails; line-by-line scan with KM-AUTH skip solves this correctly.
- `mockDynamo` added to test file because `newTestHandlerWithAI` requires non-nil DynamoClient for handleStatus and list paths. Pre-existing `newTestHandler` left unchanged.
- Non-create EventBridge dispatch uses `awspkg.PublishSandboxCommand(ctx, client, sandboxID, eventType)` — matches existing function signature in idle_event.go.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] KM-AUTH prefix in body blocked yes/cancel detection**
- **Found during:** Task 1 test execution (TestHandleConversation_YesReply, TestHandleConversation_CancelReply failing)
- **Issue:** `handleConversationReply` used `strings.HasPrefix(normalized, "yes")` on full body. Body starts with "km-auth: secret123\n" not "yes", so intent was never detected.
- **Fix:** Added `replyIntent()` function that iterates lines, skips blank and KM-AUTH lines, returns intent from first meaningful line.
- **Files modified:** `cmd/email-create-handler/main.go`
- **Commit:** `7c0db73`

**2. [Rule 2 - Missing Critical] mockDynamo needed for AI path tests**
- **Found during:** Task 1 test execution — `TestHandleEmail_StatusStillWorks` panicked with nil DynamoDB in `newTestHandlerWithAI`
- **Issue:** `newTestHandlerWithAI` was not setting DynamoClient; handleStatus and DynamoDB scan in list command would nil-panic.
- **Fix:** Added `mockDynamo` implementing `SandboxMetadataAPI` (returning empty results) and set it as default in `newTestHandlerWithAI`.
- **Files modified:** `cmd/email-create-handler/main_test.go`
- **Commit:** `7c0db73`

## Pre-existing Issues (Out of Scope)

- `TestHandleStatus_ReturnsMetadata` panics (nil DynamoDB in `newTestHandler`) — pre-existing, predates Phase 46. Documented in 46-01 SUMMARY. Not fixed.
- `cmd/ttl-handler` test build failure (missing GetSchedule method on mock) — pre-existing.
- `cmd/configui` and `internal/app/cmd` test failures — pre-existing.

## Self-Check: PASSED

- `cmd/email-create-handler/main.go` — exists, contains BedrockClient field and handleAIInterpretation
- `cmd/email-create-handler/main_test.go` — exists, contains TestHandleEmail_AIPath tests
- `infra/modules/email-handler/v1.0.0/main.tf` — exists, contains bedrock_invoke policy
- Commits 7c0db73 and 04a4530 verified in git log

---
*Phase: 46-ai-email-to-command*
*Completed: 2026-04-04*
