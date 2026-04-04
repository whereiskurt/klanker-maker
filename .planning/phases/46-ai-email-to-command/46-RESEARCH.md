# Phase 46: AI Email-to-Command — Research

**Researched:** 2026-04-03
**Domain:** Bedrock Haiku invocation from Lambda (Go), email conversation threading, S3 state, IAM extension
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Replace subject-keyword dispatch with AI interpretation using Bedrock Haiku
- Fast-path preserved: YAML attachment + "create" subject still works as before — no Haiku call
- KM-AUTH safe phrase required for ALL paths — AI interpretation does not bypass auth
- Use Haiku specifically (not Sonnet/Opus) — cost per email ~$0.001
- Confirmation required — Haiku never directly triggers actions; always goes through confirmation loop
- Low confidence (< 0.7) → send clarifying question reply instead of guessing
- Conversation state stored in S3 under `mail/conversations/{thread-id}.json`
- State machine: `new` → `interpreted` → `awaiting_confirmation` → `confirmed`/`revised`/`cancelled`
- Thread tracking via MIME `Message-ID` / `In-Reply-To` / `References` headers
- Haiku returns structured JSON: `{ "command", "profile", "overrides", "confidence", "reasoning" }`
- "yes" reply → Lambda triggers EventBridge create event (existing remote-create pattern)

### Claude's Discretion
- Exact system prompt wording and JSON schema for Haiku output
- Whether to add Bedrock IAM policy inline or as a new aws_iam_role_policy resource block
- Whether BedrockAPI interface lives in `pkg/aws/bedrock.go` or inline in the Lambda
- Handling of email threading edge cases (missing In-Reply-To header)
- How to present available profiles as context to Haiku (embed profile summaries vs. list names)
- Lambda timeout increase amount (currently 60s; Haiku is fast, 120s is sufficient)

### Deferred Ideas (OUT OF SCOPE)
- Multi-turn clarification beyond one round of "describe changes"
- Extend/pause/resume via email (status and create only for this phase)
- Markdown or HTML email replies
- Per-operator conversation history or memory
</user_constraints>

---

## Summary

Phase 46 replaces the rigid keyword-matching dispatch in `cmd/email-create-handler/main.go` with a Bedrock Haiku AI interpretation path. When an operator sends a free-form email without a YAML attachment, the Lambda calls Haiku to extract intent (command, profile, overrides), replies with a human-readable confirmation, and waits for operator approval. The existing fast-path (YAML attachment) is fully preserved for backward compatibility.

The primary new dependencies are: (1) `aws-sdk-go-v2/service/bedrockruntime` — not yet in go.mod, must be `go get`-ted, and (2) conversation state JSON persisted to S3 under `mail/conversations/`. The existing handler struct already has `S3Client`, `DynamoClient`, and `EventBridgeClient`, so extending it is additive. The IAM role needs one new `bedrock:InvokeModel` policy statement.

**Primary recommendation:** Use `bedrockruntime.InvokeModel` with the Messages API JSON body (not the Converse API), because it gives direct control over the JSON response format and avoids extra type indirection. Keep the Bedrock interface narrow so it is mockable in tests without hitting real AWS.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/bedrockruntime` | latest (not yet in go.mod) | Invoke Haiku model | Official AWS Go SDK v2 for Bedrock Runtime |
| `encoding/json` | stdlib | Marshal Haiku request / unmarshal response | Already used throughout codebase |
| `net/mail` | stdlib | Parse MIME headers (Message-ID, In-Reply-To) | Already used in existing handler |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/aws/aws-sdk-go-v2/service/s3` | already in go.mod | Read/write conversation state JSON to S3 | State machine persistence |
| `github.com/whereiskurt/klankrmkr/pkg/profile` | internal | `ListBuiltins()`, `Parse()` | Generating profile context for Haiku prompt |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `InvokeModel` + raw JSON | Converse API (`bedrockruntime.Converse`) | Converse is cleaner for multi-turn but adds type indirection; InvokeModel gives exact JSON control for structured output |
| S3 conversation state | DynamoDB | DynamoDB adds cost and schema; S3 is already available and free for these small JSON files |

**Installation:**
```bash
cd /Users/khundeck/working/klankrmkr
go get github.com/aws/aws-sdk-go-v2/service/bedrockruntime
```

---

## Architecture Patterns

### Existing Handler Structure (what to extend)

`OperatorEmailHandler` struct already has:
- `S3Client` (`OperatorS3API`) — add `PutObject` call for conversation JSON
- `DynamoClient` — already wired, needed to list running sandboxes for Haiku context
- `EventBridgeClient` — already wired, used for confirmed create dispatch
- `SESClient` — already wired, used for reply
- `ArtifactBucket` + `StateBucket` — conversation JSON goes to `StateBucket` (or `ArtifactBucket` — see discretion)

**Add to struct:**
- `BedrockClient  BedrockRuntimeAPI` — narrow interface for `InvokeModel`
- `BuiltinProfiles []string` — injected at Lambda startup from `profile.ListBuiltins()` + scanned `profiles/` directory

### New interface (narrow, mockable)

```go
// Source: pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/bedrockruntime
type BedrockRuntimeAPI interface {
    InvokeModel(ctx context.Context, params *bedrockruntime.InvokeModelInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error)
}
```

### Haiku Request JSON

```go
// Source: https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-anthropic-claude-messages.html
type haikuRequest struct {
    AnthropicVersion string          `json:"anthropic_version"`
    MaxTokens        int             `json:"max_tokens"`
    System           string          `json:"system"`
    Messages         []haikuMessage  `json:"messages"`
    Temperature      float64         `json:"temperature"`
}

type haikuMessage struct {
    Role    string `json:"role"`    // "user" or "assistant"
    Content string `json:"content"`
}
```

**Model ID (confirmed from profiles/ao.yaml):**
```
us.anthropic.claude-haiku-4-5-20251001-v1:0
```

**InvokeModel call:**
```go
body, _ := json.Marshal(haikuRequest{
    AnthropicVersion: "bedrock-2023-05-31",
    MaxTokens:        512,
    Temperature:      0.1,  // low temp for deterministic structured output
    System:           systemPrompt,
    Messages: []haikuMessage{
        {Role: "user", Content: emailBody},
    },
})
out, err := h.BedrockClient.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
    ModelId:     aws.String("us.anthropic.claude-haiku-4-5-20251001-v1:0"),
    ContentType: aws.String("application/json"),
    Accept:      aws.String("application/json"),
    Body:        body,
})
```

**Response parsing:**
```go
// Source: https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-anthropic-claude-messages-request-response.html
type haikuResponse struct {
    Content []struct {
        Type string `json:"type"`
        Text string `json:"text"`
    } `json:"content"`
    StopReason string `json:"stop_reason"`
    Usage struct {
        InputTokens  int `json:"input_tokens"`
        OutputTokens int `json:"output_tokens"`
    } `json:"usage"`
}
```

The `content[0].text` field contains the Haiku JSON output. Parse that text string as a second JSON unmarshal step into an `InterpretedCommand` struct.

### InterpretedCommand struct

```go
type InterpretedCommand struct {
    Command    string                 `json:"command"`
    Profile    string                 `json:"profile"`
    Overrides  map[string]interface{} `json:"overrides"`
    Confidence float64                `json:"confidence"`
    Reasoning  string                 `json:"reasoning"`
}
```

### Conversation State (S3 JSON)

```go
// Stored at: s3://{artifact_bucket}/mail/conversations/{thread-id}.json
type ConversationState struct {
    ThreadID       string             `json:"thread_id"`
    Sender         string             `json:"sender"`
    Started        time.Time          `json:"started"`
    Updated        time.Time          `json:"updated"`
    State          string             `json:"state"`   // new|interpreted|awaiting_confirmation|confirmed|revised|cancelled
    ResolvedCmd    *InterpretedCommand `json:"resolved_command,omitempty"`
    ConfirmMsgID   string             `json:"confirm_message_id,omitempty"` // Message-ID of confirmation email sent
    Messages       []ConversationMsg  `json:"messages"`
}

type ConversationMsg struct {
    Role    string    `json:"role"`    // "operator" or "system"
    Content string    `json:"content"`
    At      time.Time `json:"at"`
}
```

### Thread ID Extraction

```go
// net/mail header parsing — already imported in main.go
func extractThreadID(msg *mail.Message) string {
    // Reply: use In-Reply-To (strips angle brackets)
    if inReplyTo := msg.Header.Get("In-Reply-To"); inReplyTo != "" {
        return strings.Trim(inReplyTo, "<> \t")
    }
    // References: use first entry (root of thread)
    if refs := msg.Header.Get("References"); refs != "" {
        parts := strings.Fields(refs)
        if len(parts) > 0 {
            return strings.Trim(parts[0], "<> \t")
        }
    }
    // New thread: use own Message-ID
    return strings.Trim(msg.Header.Get("Message-ID"), "<> \t")
}
```

### Dispatch Logic (replacement for current `switch` block)

```
Handle() flow:
  1. Parse MIME, extract sender, subject, KM-AUTH (existing)
  2. Validate KM-AUTH against SSM (existing)
  3. Extract thread ID from headers
  4. Check if thread-id conversation state exists in S3
     a. EXISTS → loadConversation → handleConversationReply (YES/cancel/revise)
     b. NOT EXISTS → start new interpretation flow
  5. New flow:
     a. YAML attachment present → fast-path existing handleCreate (no Haiku)
     b. No YAML → callHaiku(emailBody, profiles, sandboxList)
     c. confidence < 0.7 → sendClarifyingQuestion reply, save state=new
     d. confidence >= 0.7 → sendConfirmation reply, save state=awaiting_confirmation
  6. Confirmation reply flow:
     a. body contains "yes" (case-insensitive) → dispatch EventBridge create → save state=confirmed
     b. body contains "cancel" → sendCancellation reply → save state=cancelled
     c. otherwise → run another Haiku round with original + revision context → updated confirmation
```

### Recommended Project Structure Addition

```
cmd/email-create-handler/
├── main.go              # extend OperatorEmailHandler struct + dispatch
├── main_test.go         # existing tests + new AI path tests
├── haiku.go             # callHaiku(), buildSystemPrompt(), parseHaikuResponse()
├── haiku_test.go        # unit tests with mock BedrockRuntimeAPI
├── conversation.go      # loadConversation(), saveConversation(), handleConversationReply()
└── conversation_test.go # state machine tests

pkg/aws/
└── (no new file needed — BedrockRuntimeAPI is handler-local due to narrow scope)

infra/live/use1/email-handler/
└── .terragrunt-cache/...main.tf  # add bedrock:InvokeModel policy
```

### System Prompt Design

The system prompt must tell Haiku:
1. What km commands exist and their parameters
2. Available profiles with brief description (name, TTL, instanceType, tool)
3. Currently running sandboxes (from DynamoDB) for destroy/status commands
4. Required output JSON format with schema
5. Instruction to set `confidence` < 0.7 if ambiguous

**Profile context approach:** Enumerate built-in profiles (`profile.ListBuiltins()`) + scan `profiles/` directory at Lambda startup, build a brief summary string. Do NOT embed full YAML — just name, metadata.name, instanceType, TTL, tool label. This keeps the prompt under 2K tokens.

### IAM Policy Addition (Terraform)

Add a new `aws_iam_role_policy` resource to `infra/live/use1/email-handler/.../main.tf`:

```hcl
resource "aws_iam_role_policy" "bedrock_invoke" {
  name = "km-email-handler-bedrock"
  role = aws_iam_role.email_handler.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["bedrock:InvokeModel"]
        Resource = "arn:aws:bedrock:us-east-1::foundation-model/us.anthropic.claude-haiku-4-5-20251001-v1:0"
      }
    ]
  })
}
```

Also add `BEDROCK_MODEL_ID` env var to the Lambda so the model ID is not hardcoded.

### Lambda Timeout

Current: 60 seconds. Haiku responses are fast (~1–3s), but two Haiku calls (revision round) plus S3 reads/writes needs headroom. Increase to **120 seconds** to be safe. Change `timeout = 120` in the Terraform.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| JSON structured output from LLM | Custom regex extraction | Haiku `temperature: 0.1` + explicit JSON schema in system prompt | LLMs are reliable at JSON when temperature is low and schema is explicit |
| Conversation threading | Custom ID generation | MIME Message-ID / In-Reply-To headers | Already in RFC 5322 standard; email clients maintain this automatically |
| Thread state persistence | DynamoDB table | S3 JSON file | Simpler, no schema, existing S3 permissions, conversations are low-volume |
| Profile discovery | Hardcoded list | `profile.ListBuiltins()` + embed.FS scan | Already exists in `pkg/profile/builtins.go` |
| Bedrock auth | Custom signing | `bedrockruntime.NewFromConfig(cfg)` | AWS SDK handles SigV4 signing automatically |

---

## Common Pitfalls

### Pitfall 1: Haiku returns valid JSON but with wrong field types
**What goes wrong:** Haiku might return `"confidence": "0.85"` (string) instead of `0.85` (float). Or `"overrides": null` instead of `{}`.
**Why it happens:** LLMs don't always respect JSON type coercion.
**How to avoid:** Use `json.Number` or lenient unmarshaling; validate confidence is numeric and in [0,1] range; default overrides to empty map if nil.
**Warning signs:** `json.Unmarshal` succeeds but confidence comparison fails.

### Pitfall 2: Missing In-Reply-To header on operator replies
**What goes wrong:** Some email clients (especially mobile) omit `In-Reply-To` even when replying. Thread ID lookup returns empty string, conversation state is not found.
**Why it happens:** RFC 5322 says In-Reply-To is optional.
**How to avoid:** Also check `References` header (first entry is thread root). As last resort, scan conversation state by sender+recency. Document this edge case.
**Warning signs:** Lambda falls into "new interpretation" flow for what looks like a reply.

### Pitfall 3: Bedrock model ID format — cross-region inference vs. base
**What goes wrong:** Using `anthropic.claude-haiku-3-20240307-v1:0` (base) when the account uses cross-region inference profiles.
**Why it happens:** The project already uses `us.anthropic.*` prefixed IDs in profiles/ao.yaml.
**How to avoid:** Use `us.anthropic.claude-haiku-4-5-20251001-v1:0` (confirmed in `profiles/ao.yaml`). Make it a Lambda env var `BEDROCK_MODEL_ID` so it can be overridden.
**Warning signs:** `ValidationException: The provided model identifier is invalid`.

### Pitfall 4: bedrockruntime not in go.mod
**What goes wrong:** Build fails with `cannot find module providing github.com/aws/aws-sdk-go-v2/service/bedrockruntime`.
**Why it happens:** The package is not yet in go.mod (confirmed by searching go.mod).
**How to avoid:** Run `go get github.com/aws/aws-sdk-go-v2/service/bedrockruntime` before writing code. Also update Makefile targets if they reference specific module lists.
**Warning signs:** `go build` fails immediately.

### Pitfall 5: Lambda timeout during two-round Haiku call
**What goes wrong:** Revision round (operator sends "make it larger") triggers two Haiku calls + S3 read + S3 write + SES send. With 60s timeout this can fail.
**Why it happens:** Current Lambda timeout is 60s.
**How to avoid:** Increase Lambda `timeout = 120` in Terraform.
**Warning signs:** Lambda logs show `Task timed out after 60.00 seconds`.

### Pitfall 6: S3 race condition on conversation state
**What goes wrong:** If the operator sends two emails in rapid succession, two Lambda invocations read the same (nonexistent) conversation state simultaneously and both create new conversations.
**Why it happens:** S3 PutObject is not transactional.
**How to avoid:** This is acceptable for low-volume operator email flow. Document it. For production hardening, a DynamoDB conditional write could be added, but that's out of scope.
**Warning signs:** Duplicate confirmation emails sent to operator.

### Pitfall 7: Profile list in system prompt grows stale
**What goes wrong:** Operator adds a new profile to `profiles/` but Lambda binary is not redeployed, so Haiku doesn't know about it.
**Why it happens:** Profile list is embedded at build time via `embed.FS` or listed at startup from built-in list.
**How to avoid:** Supplement built-in list with a runtime S3 scan of `profiles/` prefix in ArtifactBucket at Lambda cold start (same bucket already used for remote-create). Cache for Lambda lifetime. Document that new profiles need a Lambda redeploy OR an S3 upload to the profiles prefix.

---

## Code Examples

### Verified: InvokeModel with Messages API (from AWS official docs + pkg.go.dev)

```go
// Source: https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/bedrockruntime
// Source: https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-anthropic-claude-messages.html

import (
    "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
    awssdk "github.com/aws/aws-sdk-go-v2/aws"
)

func callHaiku(ctx context.Context, client BedrockRuntimeAPI, modelID, system, userMsg string) (string, error) {
    reqBody, err := json.Marshal(map[string]interface{}{
        "anthropic_version": "bedrock-2023-05-31",
        "max_tokens":        512,
        "temperature":       0.1,
        "system":            system,
        "messages": []map[string]string{
            {"role": "user", "content": userMsg},
        },
    })
    if err != nil {
        return "", fmt.Errorf("marshal haiku request: %w", err)
    }

    out, err := client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
        ModelId:     awssdk.String(modelID),
        ContentType: awssdk.String("application/json"),
        Accept:      awssdk.String("application/json"),
        Body:        reqBody,
    })
    if err != nil {
        return "", fmt.Errorf("bedrock InvokeModel: %w", err)
    }

    var resp struct {
        Content []struct {
            Type string `json:"type"`
            Text string `json:"text"`
        } `json:"content"`
    }
    if err := json.Unmarshal(out.Body, &resp); err != nil {
        return "", fmt.Errorf("unmarshal haiku response: %w", err)
    }
    if len(resp.Content) == 0 || resp.Content[0].Type != "text" {
        return "", fmt.Errorf("unexpected haiku response structure")
    }
    return resp.Content[0].Text, nil
}
```

### Verified: Thread ID extraction from net/mail headers

```go
// Source: net/mail standard library, RFC 5322
import "net/mail"

func extractThreadID(msg *mail.Message) string {
    clean := func(s string) string { return strings.Trim(s, "<> \t\r\n") }
    if v := msg.Header.Get("In-Reply-To"); v != "" {
        return clean(v)
    }
    if v := msg.Header.Get("References"); v != "" {
        if parts := strings.Fields(v); len(parts) > 0 {
            return clean(parts[0])
        }
    }
    return clean(msg.Header.Get("Message-ID"))
}
```

### Verified: S3 conversation state round-trip

```go
// State key format: mail/conversations/{thread-id}.json
func conversationKey(threadID string) string {
    return fmt.Sprintf("mail/conversations/%s.json", threadID)
}

func (h *OperatorEmailHandler) loadConversation(ctx context.Context, threadID string) (*ConversationState, error) {
    out, err := h.S3Client.GetObject(ctx, &s3.GetObjectInput{
        Bucket: awssdk.String(h.ArtifactBucket),
        Key:    awssdk.String(conversationKey(threadID)),
    })
    if err != nil {
        return nil, err // caller checks for NoSuchKey
    }
    defer out.Body.Close()
    var state ConversationState
    if err := json.NewDecoder(out.Body).Decode(&state); err != nil {
        return nil, fmt.Errorf("decode conversation state: %w", err)
    }
    return &state, nil
}
```

### Verified: EventBridge dispatch (existing pattern, unchanged)

```go
// Source: pkg/aws/eventbridge.go (existing)
// No changes needed — existing PutSandboxCreateEvent() is reused directly.
if err := awspkg.PutSandboxCreateEvent(ctx, h.EventBridgeClient, awspkg.SandboxCreateDetail{
    SandboxID:      sandboxID,
    ArtifactBucket: h.ArtifactBucket,
    ArtifactPrefix: artifactPrefix,
    OperatorEmail:  sender,
    CreatedBy:      "email-ai",
}); err != nil {
    return fmt.Errorf("publish SandboxCreate event: %w", err)
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Legacy Anthropic text completion API (`Human:`/`Assistant:` prompt) | Messages API with `system` + `messages[]` array | Claude 3 (2024) | System prompts are first-class; no prompt injection risk |
| Separate Bedrock model IDs per region | Cross-region inference profile IDs (`us.anthropic.*`) | 2024 | Use `us.anthropic.*` prefix for us-east-1 deployment (already confirmed in project) |
| Converse API for simple invocation | InvokeModel with raw JSON body | N/A | InvokeModel is lower-level but gives exact JSON control; suitable for structured output |

**Deprecated/outdated:**
- `anthropic.claude-v2` prompt format: Do NOT use `Human: ... \n\nAssistant:` format. Use Messages API.
- `anthropic.claude-3-haiku-20240307-v1:0` base model ID: Project uses cross-region inference `us.anthropic.claude-haiku-4-5-20251001-v1:0`.

---

## Open Questions

1. **Where to store conversation state: ArtifactBucket or StateBucket?**
   - What we know: Handler has both. ArtifactBucket already has `s3:PutObject` permission across entire bucket. StateBucket has read-only access.
   - What's unclear: Whether conversations should be co-located with remote-create artifacts or kept separate.
   - Recommendation: Use `ArtifactBucket` under `mail/conversations/` prefix — existing IAM allows PutObject there. No Terraform change needed.

2. **How does operator get the confirmation email's Message-ID to reply-to correctly?**
   - What we know: SES `SendEmail` currently uses `Simple` message mode which does not set a custom `Message-ID` header.
   - What's unclear: Whether SES auto-generates a Message-ID that email clients surface as a reply-to target.
   - Recommendation: SES sets `Message-ID` automatically. Email clients respect it. This should work without custom headers, but testing with a real email client is required.

3. **Profile discovery at runtime vs. compile time**
   - What we know: `profile.ListBuiltins()` returns `["open-dev", "restricted-dev", "hardened", "sealed"]`. Custom profiles live in `profiles/*.yaml` and `pkg/profile/builtins/` (embed.FS at build time).
   - What's unclear: Whether custom profiles in `profiles/` are deployed to S3 as a profiles/ prefix.
   - Recommendation: For Phase 46, embed the built-in list from `profile.ListBuiltins()` and the profiles/ directory files into the system prompt at build time. Document that new profiles require Lambda redeploy. Runtime S3 scan is a future improvement.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go test (`testing` stdlib) |
| Config file | none — standard `go test ./...` |
| Quick run command | `go test ./cmd/email-create-handler/... -v -count=1` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| EMAIL-AI-01 | Free-form email triggers Haiku interpretation (no YAML) | unit | `go test ./cmd/email-create-handler/... -run TestHandleEmail_AIPath -v` | Wave 0 |
| EMAIL-AI-02 | Low confidence (< 0.7) sends clarifying question, no EventBridge | unit | `go test ./cmd/email-create-handler/... -run TestHandleEmail_LowConfidence -v` | Wave 0 |
| EMAIL-AI-03 | "yes" reply dispatches EventBridge create event | unit | `go test ./cmd/email-create-handler/... -run TestHandleConversation_YesReply -v` | Wave 0 |
| EMAIL-AI-04 | Revision reply (not "yes") triggers second Haiku round | unit | `go test ./cmd/email-create-handler/... -run TestHandleConversation_RevisionReply -v` | Wave 0 |
| EMAIL-AI-05 | "cancel" reply sends cancellation, no EventBridge | unit | `go test ./cmd/email-create-handler/... -run TestHandleConversation_CancelReply -v` | Wave 0 |
| EMAIL-AI-06 | YAML attachment still fast-paths to existing create handler | unit | `go test ./cmd/email-create-handler/... -run TestHandleEmailCreate_MultipartYAMLAttachment -v` | ✅ existing |
| EMAIL-AI-07 | KM-AUTH still required on all AI paths | unit | `go test ./cmd/email-create-handler/... -run TestHandleEmail_MissingKMAuth -v` | ✅ existing (adapt) |
| EMAIL-AI-08 | Thread ID extracted from In-Reply-To header | unit | `go test ./cmd/email-create-handler/... -run TestExtractThreadID -v` | Wave 0 |
| EMAIL-AI-09 | Conversation state serializes/deserializes correctly | unit | `go test ./cmd/email-create-handler/... -run TestConversationState -v` | Wave 0 |
| EMAIL-AI-10 | Haiku response JSON parsed into InterpretedCommand | unit | `go test ./cmd/email-create-handler/... -run TestParseHaikuResponse -v` | Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./cmd/email-create-handler/... -v -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `cmd/email-create-handler/haiku_test.go` — covers EMAIL-AI-01, EMAIL-AI-02, EMAIL-AI-10 (mock BedrockRuntimeAPI)
- [ ] `cmd/email-create-handler/conversation_test.go` — covers EMAIL-AI-03, EMAIL-AI-04, EMAIL-AI-05, EMAIL-AI-08, EMAIL-AI-09
- [ ] `go get github.com/aws/aws-sdk-go-v2/service/bedrockruntime` — must run before any build

---

## Sources

### Primary (HIGH confidence)
- `pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/bedrockruntime` — InvokeModelInput/Output types, Converse API
- `https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-anthropic-claude-messages.html` — Messages API request/response format
- `cmd/email-create-handler/main.go` (codebase) — existing handler struct, interfaces, dispatch pattern
- `pkg/aws/eventbridge.go` (codebase) — PutSandboxCreateEvent pattern
- `pkg/profile/builtins.go` (codebase) — ListBuiltins(), built-in profile names
- `profiles/ao.yaml` (codebase) — confirmed Haiku model ID: `us.anthropic.claude-haiku-4-5-20251001-v1:0`
- `infra/live/use1/email-handler/.../main.tf` (codebase) — existing IAM policies, Lambda timeout=60, env vars

### Secondary (MEDIUM confidence)
- `https://docs.aws.amazon.com/code-library/latest/ug/go_2_bedrock-runtime_code_examples.html` — Go SDK Bedrock examples (Claude v2 legacy, adapted for v3 Messages API)
- `https://docs.aws.amazon.com/bedrock/latest/userguide/bedrock-runtime_example_bedrock-runtime_Converse_AnthropicClaude_section.html` — Converse API Go pattern

### Tertiary (LOW confidence)
- None

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — bedrockruntime SDK confirmed at pkg.go.dev; Messages API format from official AWS docs; model ID confirmed in codebase
- Architecture: HIGH — existing handler struct fully read; all extension points identified; IAM, env vars, S3 keys documented
- Pitfalls: MEDIUM — model ID and SDK installation gaps are confirmed; race condition and threading edge cases are inferred from RFC/email patterns

**Research date:** 2026-04-03
**Valid until:** 2026-05-03 (Bedrock model IDs change infrequently; SDK is stable)
