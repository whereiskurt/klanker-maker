---
phase: 46-ai-email-to-command
plan: 01
subsystem: ai
tags: [bedrock, haiku, bedrockruntime, email, conversation-state, s3, tdd]

# Dependency graph
requires:
  - phase: 04-lifecycle-hardening-artifacts-email
    provides: OperatorEmailHandler struct, OperatorS3API interface, email dispatch pattern
provides:
  - BedrockRuntimeAPI interface (narrow, mockable) in cmd/email-create-handler
  - callHaiku/parseHaikuResponse/buildSystemPrompt functions
  - InterpretedCommand struct with Type classification (info/action)
  - ConversationState/ConversationMsg types for email thread tracking
  - extractThreadID (In-Reply-To > References > Message-ID priority)
  - loadConversation/saveConversation (S3 under mail/conversations/{thread-id}.json)
affects:
  - 46-02 (haiku.go and conversation.go wired into handler dispatch logic)

# Tech tracking
tech-stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/bedrockruntime v1.50.4
  patterns:
    - "TDD with mockBedrock implementing BedrockRuntimeAPI interface"
    - "Lenient JSON parsing: json.Number + strconv.ParseFloat for LLM numeric outputs"
    - "S3-backed conversation state with conversationKey(threadID) helper"
    - "Thread ID extraction following RFC 5322 email threading standards"

key-files:
  created:
    - cmd/email-create-handler/haiku.go
    - cmd/email-create-handler/haiku_test.go
    - cmd/email-create-handler/conversation.go
    - cmd/email-create-handler/conversation_test.go
  modified:
    - go.mod
    - go.sum

key-decisions:
  - "BedrockRuntimeAPI interface kept handler-local (not in pkg/aws) due to narrow Lambda scope"
  - "InterpretedCommand.Type defaults to 'action' if missing from Haiku response (backward compat)"
  - "Lenient confidence parsing handles both float and string forms from LLM output"
  - "Overrides defaults to empty map (not nil) when Haiku returns null overrides field"
  - "mockS3WithBody separate from mockS3 to capture PutObject body without modifying shared mock"

patterns-established:
  - "LLM interface narrow: BedrockRuntimeAPI exposes only InvokeModel — fully mockable in tests"
  - "Two-phase JSON parse for LLM output: raw map[string]json.RawMessage first, then typed extraction"
  - "S3 conversation key: mail/conversations/{thread-id}.json (ArtifactBucket)"

requirements-completed: []

# Metrics
duration: 3min
completed: 2026-04-04
---

# Phase 46 Plan 01: AI Email-to-Command Haiku Layer Summary

**Bedrock Haiku invocation layer and S3-backed email conversation state for km AI email-to-command flow, with 22 unit tests covering lenient JSON parsing, thread ID extraction, and full S3 state round-trip**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-04-04T02:27:33Z
- **Completed:** 2026-04-04T02:30:53Z
- **Tasks:** 2
- **Files modified:** 6 (4 created, 2 modified)

## Accomplishments

- BedrockRuntimeAPI narrow interface + callHaiku/parseHaikuResponse with lenient type handling for LLM JSON quirks (string confidence, null overrides)
- buildSystemPrompt categorizing all km commands into info vs. action types with profile/sandbox context
- ConversationState/ConversationMsg types with full S3 serialization under mail/conversations/ prefix
- extractThreadID following RFC 5322 email threading (In-Reply-To > References > Message-ID)

## Task Commits

1. **Task 1: Add bedrockruntime dependency and create haiku.go + haiku_test.go** - `666cde8` (feat)
2. **Task 2: Create conversation.go + conversation_test.go** - `ecc44a2` (feat)

## Files Created/Modified

- `cmd/email-create-handler/haiku.go` - BedrockRuntimeAPI interface, callHaiku, parseHaikuResponse, buildSystemPrompt, InterpretedCommand struct
- `cmd/email-create-handler/haiku_test.go` - 12 unit tests for Haiku invocation with mockBedrock
- `cmd/email-create-handler/conversation.go` - ConversationState/ConversationMsg types, extractThreadID, loadConversation, saveConversation
- `cmd/email-create-handler/conversation_test.go` - 10 unit tests for conversation state and thread extraction
- `go.mod` / `go.sum` - bedrockruntime v1.50.4 added

## Decisions Made

- BedrockRuntimeAPI lives in cmd/email-create-handler (handler-local) rather than pkg/aws — narrow scope, single Lambda
- InterpretedCommand.Type defaults to "action" when absent from Haiku response for backward compatibility
- Lenient confidence parsing uses json.Number first, then string fallback to handle LLM type coercion
- Separate mockS3WithBody type added (alongside existing mockS3) to capture PutObject body bytes for saveConversation test assertions

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

Pre-existing test failure: `TestHandleStatus_ReturnsMetadata` panics with nil DynamoDB client (nil pointer in `pkg/aws/sandbox_dynamo.go:252`). This failure predates Phase 46 work (confirmed by checking git stash). Logged as pre-existing; out of scope.

## Next Phase Readiness

- haiku.go exports: BedrockRuntimeAPI, InterpretedCommand, callHaiku, buildSystemPrompt, parseHaikuResponse — ready for Plan 02 wiring
- conversation.go exports: ConversationState, ConversationMsg, extractThreadID, loadConversation, saveConversation — ready for Plan 02 wiring
- Plan 02 needs to add BedrockClient field to OperatorEmailHandler and wire the AI interpretation path into Handle()

---
*Phase: 46-ai-email-to-command*
*Completed: 2026-04-04*
