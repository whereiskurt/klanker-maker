---
phase: 110-session-aware-slack-reply-thread-channel-repair
plan: "02"
subsystem: slack-bridge
tags:
  - slack
  - bridge
  - dynamodb
  - session-lookup
  - ed25519
dependency_graph:
  requires:
    - 110-01 (session-index GSI on km-slack-threads DynamoDB table)
  provides:
    - ActionLookupThread bridge action
    - SessionID envelope field (alphabetical)
    - LookupBySession DDB adapter
    - SlackThreadStore.LookupBySession interface method
  affects:
    - pkg/slack/payload.go
    - pkg/slack/bridge/events_interfaces.go
    - pkg/slack/bridge/aws_adapters.go
    - pkg/slack/bridge/handler.go
tech_stack:
  added: []
  patterns:
    - TDD (RED/GREEN per task)
    - Ed25519 signed bridge action pattern (mirrors ActionPermalink)
    - DynamoDB GSI KEYS_ONLY Query + GetItem base table
    - Sandbox-never-reads-DDB boundary via sandbox_id filter
key_files:
  created:
    - pkg/slack/bridge/lookup_thread_handler_test.go
  modified:
    - pkg/slack/payload.go
    - pkg/slack/payload_test.go
    - pkg/slack/bridge/events_interfaces.go
    - pkg/slack/bridge/aws_adapters.go
    - pkg/slack/bridge/handler.go
    - pkg/slack/bridge/events_handler_test.go
decisions:
  - "EnvelopeVersion stays at 1 — SessionID is an additive zero-valued field (pitfall 4)"
  - "lookup-thread bypasses channel-ownership check (step 6) — security enforced by sandbox_id filter in LookupBySession"
  - "GSI KEYS_ONLY: Query returns only (channel_id, thread_ts), then GetItem base table for sandbox_id + agent_type"
  - "fakeThreadStore declared in lookup_thread_handler_test.go (not handler_test.go) to avoid duplicate type"
  - "fakeThreads.LookupBySession stub added to events_handler_test.go to satisfy updated interface"
metrics:
  duration: "309s"
  completed_date: "2026-06-13"
  tasks_completed: 3
  files_modified: 7
---

# Phase 110 Plan 02: Lookup-Thread Bridge Action + SessionID Envelope Field Summary

**One-liner:** Ed25519-signed `lookup-thread` bridge action with DynamoDB session-index GSI adapter, sandbox_id ownership filter, and alphabetically-inserted `session_id` envelope field for deterministic canonical signing.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | SessionID field + ActionLookupThread + canonical-JSON test | 876c22ed | payload.go, payload_test.go |
| 2 | LookupBySession GSI adapter + extend interface | 659c3938 | events_interfaces.go, aws_adapters.go, events_handler_test.go |
| 3 | lookup-thread dispatch + handler tests | 7acb7447 | handler.go, lookup_thread_handler_test.go |

## What Was Built

### Task 1: Envelope Extension

- Added `ActionLookupThread = "lookup-thread"` to the action constants block in `pkg/slack/payload.go`
- Inserted `SessionID string \`json:"session_id"\`` between `S3Key` and `SenderID` in `SlackEnvelope` (alphabetical by JSON tag)
- `EnvelopeVersion` stays at 1 — additive zero-valued field, backward-compatible
- Updated `TestCanonicalJSON_FieldOrderAlphabetical` golden string and fields list
- Added `TestCanonicalJSON_SessionID` (verifies position between `s3_key` and `sender_id`) and `TestCanonicalJSON_ZeroSessionID` (verifies zero-value serialization)

### Task 2: GSI Adapter

- Extended `SlackThreadStore` interface with `LookupBySession(ctx, sessionID, sandboxID) (channelID, threadTS, agentType, error)`
- Implemented `DDBThreadStore.LookupBySession` in `aws_adapters.go`:
  - Queries `session-index` GSI with `KeyConditionExpression = "claude_session_id = :sid"`
  - GSI is `KEYS_ONLY` — Query returns only `(channel_id, thread_ts)` keys
  - For each result, issues a `GetItem` on the base table to read `sandbox_id` and `agent_type`
  - Returns the first row whose `sandbox_id == sandboxID`; returns empty strings (not error) on miss or cross-sandbox
- Added `LookupBySession` stub to `fakeThreads` in `events_handler_test.go` to satisfy the updated interface
- Verified: bridge Upsert does NOT write `claude_session_id` (pitfall 2 intact)

### Task 3: Handler Dispatch

- Added `Threads SlackThreadStore` field to `Handler` struct
- Added `ActionLookupThread` to action allow-list (single-line `if` check)
- Added channel-ownership bypass in step 6 for `ActionLookupThread`: the normal channel-ownership check is skipped; security is enforced inside `LookupBySession` by the `sandbox_id` filter
- Dispatch case logic:
  - Empty `env.SessionID` → 400 `missing_session_id`
  - Nil `h.Threads` → 500 `threads_store_unavailable`
  - `LookupBySession` error → 500 (via `slackResponse`)
  - `chanID == ""` (miss or cross-sandbox) → 200 `{"ok":true,"found":false}`
  - Hit → 200 `{"ok":true,"found":true,"channel_id":...,"thread_ts":...,"agent_type":...}`
- Created `lookup_thread_handler_test.go` with three tests using verbatim names from the validation strategy:
  - `TestHandler_LookupThread` — happy path, returns found:true with channel/thread/agent
  - `TestHandler_LookupThread_MissingSessionID` — empty session_id → 400
  - `TestHandler_LookupThread_WrongSandbox` — cross-sandbox lookup → 200 found:false

## Verification

```
go test ./pkg/slack/... -count=1 -timeout 120s
ok  github.com/whereiskurt/klanker-maker/pkg/slack            0.314s
ok  github.com/whereiskurt/klanker-maker/pkg/slack/bridge     5.342s
```

All tests green. Bridge Upsert confirmed to not write `claude_session_id`.

## Deviations from Plan

None — plan executed exactly as written.

The only structural note: the test file `lookup_thread_handler_test.go` declares `fakeThreadStore` (distinct from `fakeThreads` in `events_handler_test.go`) because both files are in the `bridge_test` external package and types cannot be duplicated. The new `fakeThreadStore` has a configurable `lookupBySessionFn` for precise per-test control.

## Self-Check: PASSED

All created/modified files verified present. All three task commits verified in git history:
- 876c22ed (Task 1: payload.go, payload_test.go)
- 659c3938 (Task 2: events_interfaces.go, aws_adapters.go, events_handler_test.go)
- 7acb7447 (Task 3: handler.go, lookup_thread_handler_test.go)
