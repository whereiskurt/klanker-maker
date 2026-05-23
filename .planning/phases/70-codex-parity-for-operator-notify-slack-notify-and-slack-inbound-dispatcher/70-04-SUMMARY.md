---
phase: 70-codex-parity-for-operator-notify-slack-notify-and-slack-inbound-dispatcher
plan: "04"
subsystem: slack-sidecar-bridge
tags: [slack, km-slack, bridge, permalink, update, new-message, phase-70]
dependency_graph:
  requires: []
  provides: [km-slack-permalink-subcommand, km-slack-update-subcommand, km-slack-post-new-message-flag, bridge-action-permalink, bridge-action-update]
  affects: [plan-70-06-cross-agent-switch]
tech_stack:
  added: []
  patterns: [run/runWith-testable-inner-pattern, postForPermalink-helper, runPermalinkWith-runUpdateWith-testable-inners]
key_files:
  created: []
  modified:
    - cmd/km-slack/main.go
    - cmd/km-slack/main_test.go
    - pkg/slack/payload.go
    - pkg/slack/payload_test.go
    - pkg/slack/client.go
    - pkg/slack/bridge/interfaces.go
    - pkg/slack/bridge/handler.go
    - pkg/slack/bridge/aws_adapters.go
    - pkg/slack/bridge/handler_test.go
    - pkg/slack/bridge/events_handler_test.go
decisions:
  - "Extended PostResponse with Permalink field so postForPermalink can decode bridge response without re-implementing PostToBridge"
  - "Added runPermalinkWith + runUpdateWith testable inner functions mirroring run/runWith pattern for unit testing without SSM"
  - "New SlackEnvelope fields (MessageTS, Text) inserted in alphabetical JSON-tag order to maintain canonical signing determinism"
  - "SlackPosterAdapter in aws_adapters.go gains GetPermalink + UpdateMessage implementations using same call() helper"
metrics:
  duration: "591s"
  completed_date: "2026-05-22"
  tasks_completed: 3
  tasks_total: 3
  files_modified: 10
---

# Phase 70 Plan 04: km-slack Permalink + Update + New-Message Summary

Three new Slack API surface elements added to km-slack sidecar AND bridge Lambda, unblocking Plan 70-06 cross-agent thread switching.

## What Was Built

### Sidecar layer (cmd/km-slack)

**`--new-message` flag on `post` subcommand** — Forces `thread_ts=""` (new top-level message instead of thread reply). When set, also prints `ts=<value>` to STDOUT so the Plan 70-06 poller can capture the new thread's ts with:
```bash
NEW_TOP_TS=$(km-slack post --new-message --channel C... --body /tmp/b | grep '^ts=' | sed 's/^ts=//')
```

**`permalink` subcommand** — `km-slack permalink --channel C... --ts NNNNNN.MMMMMM`. Signs an `ActionPermalink` envelope, POSTs to bridge, prints the permalink URL to STDOUT. Non-zero exit on failure so the poller falls back to `(unavailable)` per CONTEXT.md locked failure mode.

**`update` subcommand** — `km-slack update --channel C... --ts NNNNNN.MMMMMM --text "..."` (or `--body <file>`). Signs an `ActionUpdate` envelope. Exit 0 on success.

Both new subcommands use `runPermalinkWith` / `runUpdateWith` testable inner functions (mirrors `run`/`runWith` pattern), allowing tests to inject an ephemeral Ed25519 key without touching SSM.

### Bridge layer (pkg/slack)

**`pkg/slack/payload.go`:**
- `ActionPermalink = "permalink"` and `ActionUpdate = "update"` constants
- `SlackEnvelope.MessageTS string \`json:"message_ts"\`` — alphabetical insertion between `filename` and `nonce`
- `SlackEnvelope.Text string \`json:"text"\`` — alphabetical insertion between `subject` and `thread_ts`
- Handler action validation extended to accept the two new actions

**`pkg/slack/client.go`:**
- `SlackAPIResponse.Permalink string \`json:"permalink,omitempty"\`` field
- `PostResponse.Permalink string \`json:"permalink,omitempty"\`` field — allows `postForPermalink` to decode bridge response through `PostToBridge`
- `GetPermalink(ctx, channel, messageTS) (string, error)` method wrapping `chat.getPermalink`
- `UpdateMessage(ctx, channel, ts, text) (string, error)` method wrapping `chat.update`

**`pkg/slack/bridge/interfaces.go`:**
- `SlackPoster` extended: `GetPermalink(ctx, channel, messageTS string) (string, error)` and `UpdateMessage(ctx, channel, ts, text string) (string, error)`

**`pkg/slack/bridge/handler.go`:**
- Two new dispatch cases `ActionPermalink` and `ActionUpdate` in `Handle()`
- Both enforce channel-scope authorization (sandbox's envelope.Channel must match DDB `slack_channel_id` — same check as `ActionPost`)
- `ActionPermalink`: validates `MessageTS != ""`, calls `SlackPoster.GetPermalink`, returns `{"ok":true,"permalink":"..."}`
- `ActionUpdate`: validates `MessageTS != ""` and `Text != ""`, calls `SlackPoster.UpdateMessage`, returns `{"ok":true,"ts":"..."}`

**`pkg/slack/bridge/aws_adapters.go`:**
- `SlackPosterAdapter` gains `GetPermalink` and `UpdateMessage` methods via the existing `call()` helper
- `slackAPIResponse` gains `Permalink string \`json:"permalink,omitempty"\`` field

### Test mocks updated

`fakeSlack` in `handler_test.go`, `fakeBlockPoster` in `handler_test.go`, and `fakeSlackPoster` in `events_handler_test.go` all gained stub `GetPermalink` + `UpdateMessage` implementations to satisfy the extended `SlackPoster` interface.

## Tests

| Test | File | Status |
|------|------|--------|
| TestPayload_PermalinkAction | pkg/slack/payload_test.go | PASS |
| TestPayload_UpdateAction | pkg/slack/payload_test.go | PASS |
| TestRunPost_NewMessage | cmd/km-slack/main_test.go | PASS |
| TestRunPermalink | cmd/km-slack/main_test.go | PASS |
| TestRunUpdate | cmd/km-slack/main_test.go | PASS |

Wave 0 stubs from Task 1 deleted. All existing tests pass (excluding pre-existing unrelated failure documented below).

## Security Model

The sandbox never holds a raw Slack bot token. All three new surfaces go through the same signed-envelope bridge flow:
1. Sandbox signs envelope with its Ed25519 private key (from SSM)
2. Bridge verifies signature against the sandbox's public key (from DynamoDB km-identities)
3. Bridge checks channel ownership: `envelope.Channel` must equal the sandbox's `slack_channel_id` from DynamoDB km-sandboxes
4. Bridge calls Slack Web API with its bot token (from SSM, 15-min cache)

`ActionPermalink` is read-only but gets the same channel-scope check (defense in depth). `ActionUpdate` requires matching channel ownership (sandboxes can only edit messages in their own channel).

## Downstream Plan Dependencies

Plan 70-06 (cross-agent thread switch) consumes all three surfaces:
- `post --new-message` → creates new top-level thread, captures its `ts`
- `permalink --ts <new_thread_ts>` → resolves permalink URL to embed in handoff message
- `update` → optional polish to edit the handoff post (within Slack's 10-minute window)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] runWith return signature changed to (string, error)**
- **Found during:** Task 3
- **Issue:** `runWith` returned `error` only; `runPost --new-message` needed the message ts to print to stdout
- **Fix:** Changed `runWith` and `run` to return `(string, error)`. Updated all 13 call sites in `main_test.go` with `_, err :=` pattern. Added `runPermalinkWith` and `runUpdateWith` as testable inners.
- **Files modified:** cmd/km-slack/main.go, cmd/km-slack/main_test.go
- **Commit:** 72d937f

**2. [Rule 2 - Missing critical functionality] PostResponse.Permalink field**
- **Found during:** Task 3 implementation of `postForPermalink`
- **Issue:** `PostToBridge` decodes into `PostResponse{OK, TS, Error}` — no `Permalink` field. The bridge `ActionPermalink` response sends `{"ok":true,"permalink":"..."}` but `PostResponse` would silently discard the permalink.
- **Fix:** Added `Permalink string \`json:"permalink,omitempty"\`` to `PostResponse`.
- **Files modified:** pkg/slack/client.go
- **Commit:** 72d937f (same commit as Task 3)

**3. [Rule 2 - Missing critical functionality] SlackPosterAdapter GetPermalink + UpdateMessage**
- **Found during:** Task 2 build
- **Issue:** `SlackPosterAdapter` implements `SlackPoster` but didn't have the two new methods; build would fail at adapter wire-up in Lambda main.
- **Fix:** Added `GetPermalink` and `UpdateMessage` to `SlackPosterAdapter` in `aws_adapters.go` using the existing `call()` helper.
- **Files modified:** pkg/slack/bridge/aws_adapters.go
- **Commit:** b99a75b

**4. [Rule 2 - Missing critical functionality] Mock SlackPoster implementations updated**
- **Found during:** Task 2 test compilation
- **Issue:** `fakeSlack`, `fakeBlockPoster`, `fakeSlackPoster` didn't implement new interface methods
- **Fix:** Stub `GetPermalink` + `UpdateMessage` added to each mock
- **Files modified:** pkg/slack/bridge/handler_test.go, pkg/slack/bridge/events_handler_test.go
- **Commit:** b99a75b

### Out-of-Scope Pre-existing Issue

**TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0** — Pre-existing test failure (confirmed failing on commit b99a75b before Task 3 was written). The test expects `PostToBridge` to retry 503, but the documented behavior is "5xx → fail fast" (replay-nonce protection). Logged to `deferred-items.md`. Not caused by Plan 70-04 changes.

## Self-Check: PASSED

| Check | Result |
|-------|--------|
| cmd/km-slack/main.go exists | FOUND |
| pkg/slack/payload.go exists | FOUND |
| pkg/slack/client.go exists | FOUND |
| pkg/slack/bridge/interfaces.go exists | FOUND |
| pkg/slack/bridge/handler.go exists | FOUND |
| cmd/km-slack/main_test.go exists | FOUND |
| pkg/slack/payload_test.go exists | FOUND |
| Commit 7223f56 (Task 1 stubs) | FOUND |
| Commit b99a75b (Task 2 bridge) | FOUND |
| Commit 72d937f (Task 3 sidecar) | FOUND |
| ActionPermalink in payload.go | FOUND |
| GetPermalink in client.go | FOUND |
| ActionPermalink in handler.go | FOUND |
| TestPayload_PermalinkAction PASS | ok |
| TestPayload_UpdateAction PASS | ok |
| TestRunPost_NewMessage PASS | ok |
| TestRunPermalink PASS | ok |
| TestRunUpdate PASS | ok |
