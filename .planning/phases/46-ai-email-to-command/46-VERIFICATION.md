---
phase: 46-ai-email-to-command
verified: 2026-04-03T00:00:00Z
status: gaps_found
score: 12/13 must-haves verified
gaps:
  - truth: "All named tests pass: TestCallHaiku|TestParseHaiku|TestBuildSystemPrompt|TestExtractThreadID|TestConversation|TestHandleEmail_AI|TestHandleConversation|TestHandleEmail_Fast|TestHandleEmail_Status|TestHandleEmail_Missing"
    status: partial
    reason: "TestHandleStatus_ReturnsMetadata panics with nil pointer dereference. handleStatus was refactored in plan 46-02 from S3-based metadata lookup to DynamoDB (ReadSandboxMetadataDynamo), but newTestHandler does not set DynamoClient. The test seeds mock S3 with metadata JSON but the code now calls client.GetItem on a nil DynamoDB client."
    artifacts:
      - path: "cmd/email-create-handler/main_test.go"
        issue: "TestHandleStatus_ReturnsMetadata uses newTestHandler which leaves DynamoClient nil; test still seeds S3 with metadata JSON from old S3 path"
      - path: "cmd/email-create-handler/main.go"
        issue: "handleStatus now calls awspkg.ReadSandboxMetadataDynamo (line 281) — no nil guard for h.DynamoClient"
    missing:
      - "Update TestHandleStatus_ReturnsMetadata to use newTestHandlerWithAI (which sets mockDynamo) and seed mockDynamo with the sandbox metadata record instead of S3"
      - "Or add a nil guard in handleStatus for h.DynamoClient (matching the pattern used in handleAIInterpretation line 337)"
---

# Phase 46: AI Email-to-Command Verification Report

**Phase Goal:** Replace the rigid keyword-matching email-create-handler with a conversational AI-powered flow. Operator sends free-form email to operator@sandboxes.{domain} describing what they want. Lambda calls Haiku to interpret the intent, resolve it to a km command with profile selection and overrides, and replies with a structured confirmation template. Operator replies "yes" to execute, or describes changes for another round. Safe phrase auth is preserved.
**Verified:** 2026-04-03
**Status:** gaps_found
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Haiku AI invocation via BedrockRuntimeAPI interface + callHaiku function | VERIFIED | haiku.go:15-17 defines interface; haiku.go:64-97 implements callHaiku with InvokeModelInput, correct content-type, response parsing |
| 2 | buildSystemPrompt with info/action command classification | VERIFIED | haiku.go:171-220 — "Info commands (type: info)" and "Action commands (type: action)" sections, all 7 commands listed |
| 3 | InterpretedCommand with Type field ("info" vs "action"), confidence, overrides | VERIFIED | haiku.go:20-33 — Command, Type, Profile, Overrides, Confidence, Reasoning fields; type defaults to "action" if absent |
| 4 | Conversation state in S3 under mail/conversations/{thread-id}.json | VERIFIED | conversation.go:37-39 conversationKey(); saveConversation writes to that key; loadConversation reads from it |
| 5 | Thread extraction from MIME In-Reply-To/References/Message-ID | VERIFIED | conversation.go:44-68 — In-Reply-To first, References fallback (first entry), Message-ID final fallback; angle brackets stripped |
| 6 | Handle() dispatch: thread check → YAML fast-path → status fast-path → AI path → help | VERIFIED | main.go:193-218 — exactly this order; threadID extracted, loadConversation called, then subject-based dispatch, then AI path |
| 7 | Info commands (list, status) reply immediately without confirmation | VERIFIED | main.go:374-377 — cmd.Type == "info" routes to handleInfoCommand which sends reply with no conversation save; tests pass |
| 8 | Action commands go through confirmation template → conversation state → reply handling | VERIFIED | main.go:379-381 routes to sendActionConfirmation; state saved as "awaiting_confirmation"; handleConversationReply handles follow-up |
| 9 | Yes reply triggers EventBridge event; cancel/revision handled | VERIFIED | main.go:529-613 (executeConfirmedCommand) — PutSandboxCreateEvent for create, PublishSandboxCommand for destroy/extend/pause/resume; cancel at 480-485; revision at 488-490 |
| 10 | KM-AUTH preserved for all paths | VERIFIED | main.go:170-187 — KM-AUTH extracted and validated before any dispatch; missing or wrong phrase returns rejection before Bedrock or thread check |
| 11 | YAML attachment fast-path preserved (backward compat) | VERIFIED | main.go:202-205 — "create" subject + any content routes to handleCreate; TestHandleEmail_FastPath_YAMLAttachment passes |
| 12 | Terraform: bedrock:InvokeModel IAM, 120s timeout, BEDROCK_MODEL_ID env var | VERIFIED | main.tf:154-168 aws_iam_role_policy.bedrock_invoke with bedrock:InvokeModel; timeout=120 at line 212; BEDROCK_MODEL_ID env var at line 223; variables.tf:34-38 bedrock_model_id var |
| 13 | All named tests pass | PARTIAL | 29/30 pass; TestHandleStatus_ReturnsMetadata panics — nil pointer in handleStatus after S3→DynamoDB refactor |

**Score:** 12/13 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `cmd/email-create-handler/haiku.go` | BedrockRuntimeAPI interface, callHaiku, buildSystemPrompt, parseHaikuResponse, InterpretedCommand, haikuRequest/Response | VERIFIED | 221 lines; all exports present and substantive |
| `cmd/email-create-handler/haiku_test.go` | Unit tests for Haiku invocation with mock BedrockRuntimeAPI; TestCallHaiku | VERIFIED | TestCallHaiku_Success/LowConfidence/InvokeError, TestParseHaikuResponse_* (5 cases), TestBuildSystemPrompt_* (4 cases) — all 12 pass |
| `cmd/email-create-handler/conversation.go` | ConversationState, ConversationMsg, extractThreadID, loadConversation, saveConversation | VERIFIED | 111 lines; all types and functions present with real S3 round-trip logic |
| `cmd/email-create-handler/conversation_test.go` | Unit tests for conversation state and thread extraction; TestExtractThreadID | VERIFIED | 5 TestExtractThreadID_* + TestConversationState_* (2) + TestLoadConversation_* (2) + TestSaveConversation — all 10 pass |
| `cmd/email-create-handler/main.go` | Refactored dispatch with AI interpretation path, BedrockClient on handler struct | VERIFIED | BedrockClient/BedrockModelID fields at lines 121-122; handleAIInterpretation, handleConversationReply, executeConfirmedCommand implemented |
| `cmd/email-create-handler/main_test.go` | Tests for AI path, conversation reply handling, fast-path preservation; TestHandleEmail_AIPath | PARTIAL | TestHandleEmail_AIPath_ActionCommand and 7 other AI tests pass; TestHandleStatus_ReturnsMetadata FAILS (nil panic) |
| `infra/modules/email-handler/v1.0.0/main.tf` | Bedrock IAM policy, updated Lambda timeout and env vars; "bedrock_invoke" | VERIFIED | aws_iam_role_policy.bedrock_invoke at line 154; timeout=120 at line 212; BEDROCK_MODEL_ID env var at line 223 |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `cmd/email-create-handler/main.go` | `cmd/email-create-handler/haiku.go` | callHaiku() in AI interpretation path | WIRED | callHaiku called at main.go:347 (handleAIInterpretation) and main.go:638 (handleRevision); BedrockRuntimeAPI interface bridged at struct level |
| `cmd/email-create-handler/main.go` | `cmd/email-create-handler/conversation.go` | loadConversation/saveConversation in reply handling | WIRED | loadConversation at main.go:195; saveConversation at main.go:364, 461, 483, 609, 647 |
| `cmd/email-create-handler/main.go` | `pkg/aws/eventbridge.go` | PutSandboxCreateEvent on confirmed command | WIRED | awspkg.PutSandboxCreateEvent called at main.go:572 inside executeConfirmedCommand case "create"; awspkg.PublishSandboxCommand at main.go:595 for destroy/extend/pause/resume |
| `cmd/email-create-handler/haiku.go` | `bedrockruntime.InvokeModel` | BedrockRuntimeAPI interface | WIRED | InvokeModel called at haiku.go:78 with InvokeModelInput |
| `cmd/email-create-handler/conversation.go` | `s3.GetObject/PutObject` | OperatorS3API interface | WIRED | GetObject at conversation.go:75; PutObject at conversation.go:101; key pattern "mail/conversations/" confirmed |

### Requirements Coverage

No requirements listed in plan frontmatter (`requirements: []` for both plans). No REQUIREMENTS.md IDs to cross-reference.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `cmd/email-create-handler/main_test.go` | ~889 | TestHandleStatus_ReturnsMetadata calls handleStatus via newTestHandler (nil DynamoClient) | BLOCKER | Test panics; handleStatus was refactored to DynamoDB but test not updated |
| `cmd/email-create-handler/main.go` | 278-281 | handleStatus has no nil guard on h.DynamoClient before calling ReadSandboxMetadataDynamo | WARNING | Panic in production if DynamoClient accidentally nil; matches pattern in handleAIInterpretation (line 337 has nil guard) |

### Human Verification Required

None — all critical paths are covered by automated tests or are programmatically verifiable.

### Gaps Summary

Phase 46 achieves its goal substantively. The AI interpretation flow is fully wired: Haiku invocation, conversation state in S3, dispatch logic (thread check → YAML fast-path → status fast-path → AI path → help), info/action command classification, confirmation template, yes/cancel/revision handling, and EventBridge dispatch on confirmation are all present and passing tests.

One gap blocks the "all named tests pass" must-have: `TestHandleStatus_ReturnsMetadata` panics because `handleStatus` was refactored in plan 46-02 to read metadata from DynamoDB instead of S3, but the test was not updated — it still seeds mock S3 with metadata JSON and uses `newTestHandler` which leaves `DynamoClient` nil.

Fix options (either suffices):
1. Update `TestHandleStatus_ReturnsMetadata` to use `newTestHandlerWithAI` (which provides `mockDynamo`) and seed `mockDynamo` with the sandbox record instead of S3.
2. Add a nil guard in `handleStatus` at line 281 before calling `ReadSandboxMetadataDynamo`, matching the defensive pattern already used in `handleAIInterpretation` (line 337).

---

_Verified: 2026-04-03_
_Verifier: Claude (gsd-verifier)_
