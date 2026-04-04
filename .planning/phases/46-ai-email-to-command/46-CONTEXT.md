---
phase: 46
name: AI email-to-command — Haiku interprets free-form operator emails into km commands
status: planned
depends_on: [22, 45]
---

# Phase 46: Context

## Problem

The current `email-create-handler` Lambda (`cmd/email-create-handler/main.go`) uses rigid keyword matching on the email subject line:
- Subject contains "create" → expects YAML attachment
- Subject contains "status" → expects sandbox ID in subject
- Anything else → sends help text

This is brittle. The operator must know the exact format, attach YAML files, and use specific keywords. There's no way to say "spin up a goose sandbox for the meshtk repo with a 2h TTL" in natural language.

## Solution

Replace the dispatch logic with a conversational AI flow using Bedrock Haiku:

### Flow

1. **Operator sends free-form email** to `operator@sandboxes.{domain}`
   - "Hey, can you spin up a goose sandbox for whereiskurt/meshtk? Give it 2 hours."
   - KM-AUTH phrase is still required in the body

2. **Lambda receives email** (existing SES → S3 → Lambda trigger)
   - Validates KM-AUTH safe phrase (existing)
   - If YAML attachment present → fast-path to existing create handler (backward compat)
   - Otherwise → AI interpretation path

3. **Lambda calls Bedrock Haiku** with:
   - The email body text
   - Available profiles (from S3 toolchain bucket or compiled list)
   - Available commands: create, destroy, status, extend, pause, resume, lock, unlock
   - Currently running sandboxes (from DynamoDB km-sandboxes table)
   - System prompt explaining the km CLI and expected output format

4. **Haiku returns structured JSON:**
   ```json
   {
     "command": "create",
     "profile": "goose.yaml",
     "overrides": {"ttl": "2h", "allowedRepos": ["whereiskurt/meshtk"]},
     "confidence": 0.92,
     "reasoning": "User wants a goose sandbox for the meshtk repo with 2h TTL"
   }
   ```

5. **Lambda replies with confirmation template:**
   ```
   I'll run:
     km create profiles/goose.yaml --alias goose-meshtk

   With overrides:
     ttl: 2h
     allowedRepos: [whereiskurt/meshtk]

   Profile: goose-ebpf-gatekeeper (t3.medium, us-east-1, eBPF+proxy)

   Reply YES to proceed, or describe changes.
   ```

6. **Operator replies:**
   - "yes" → Lambda triggers EventBridge create event (existing remote-create)
   - "make it a t3.large instead" → another Haiku round → updated confirmation
   - "cancel" → Lambda replies with cancellation acknowledgment

### Conversation State

- Track conversation threads via `Message-ID` / `In-Reply-To` / `References` MIME headers
- Store conversation state in S3 under `mail/conversations/{thread-id}.json`:
  ```json
  {
    "thread_id": "...",
    "sender": "operator@gmail.com",
    "started": "2026-04-03T...",
    "state": "awaiting_confirmation",
    "resolved_command": { ... },
    "messages": [...]
  }
  ```
- State machine: `new` → `interpreted` → `awaiting_confirmation` → `confirmed`/`revised`/`cancelled`

## Existing Code Touched

- `cmd/email-create-handler/main.go` — major refactor: add Haiku integration, conversation state, confirmation flow
- `cmd/email-create-handler/main_test.go` — new tests for AI interpretation path
- Possibly new Bedrock helper in `pkg/aws/` for Haiku invocation (or use existing Bedrock patterns)

## Key Design Decisions

- **Two command types:** Info commands (list, status) reply immediately — no confirmation needed. Action commands (create, destroy, extend, pause, resume) require confirmation before execution.
- **Sandbox-to-operator:** Sandboxes can email the operator (via `km-send --to operator@sandboxes.{domain}`) to request info or actions. Same Haiku interpretation applies.
- **Fast-path preserved:** YAML attachment + "create" subject still works as before — no Haiku call needed
- **Safe phrase required for all paths** — AI interpretation doesn't bypass auth
- **Haiku not Claude** — keeps cost per email interaction trivial (~$0.001/request)
- **Confirmation required for actions** — Haiku never directly triggers destructive actions; info commands reply immediately
- **Low confidence → clarifying question** — if Haiku scores < 0.7, reply asking for clarification instead of guessing
