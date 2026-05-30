---
phase: 91
plan: "04"
subsystem: slack-init
tags: [slack, ssm, bot-user-id, auth-test, mention-only]
dependency_graph:
  requires: [91-00]
  provides: [bot-user-id-ssm-cache]
  affects: [91-03, 91-05]
tech_stack:
  added: []
  patterns: [non-fatal-ssm-write, tdd-red-green, interface-extension]
key_files:
  created: []
  modified:
    - pkg/slack/client.go
    - pkg/slack/client_test.go
    - internal/app/cmd/slack.go
    - internal/app/cmd/slack_mention_init_test.go
    - internal/app/cmd/slack_test.go
decisions:
  - "Used callJSONRaw helper (new) to return raw response bytes so AuthTestWithUserID can decode user_id without touching the existing SlackAPIResponse/SlackUserField polymorphic decode"
  - "AuthTest(ctx) error preserved unchanged — no callers broken; AuthTestWithUserID is additive"
  - "SSM Put for bot-user-id is non-fatal in both RunSlackInit and RunSlackRotateToken — WARN on stderr, continue"
  - "slack_mention_init_test.go changed from package cmd to package cmd_test to reuse fakeSSM/fakeSlackInitAPI fakes already in slack_test.go"
  - "Added fakeSSMCapturingSecure and fakeSSMWithBotUserIDError to test secure=false assertion and non-fatal error path"
metrics:
  duration: "726s"
  completed: "2026-05-30T22:35:11Z"
  tasks_completed: 2
  files_modified: 5
---

# Phase 91 Plan 04: Bot User ID SSM Caching Summary

**One-liner:** `AuthTestWithUserID` on `*slack.Client` + `SlackInitAPI` interface extension + non-fatal SSM write of `{prefix}slack/bot-user-id` in both `km slack init` and `km slack rotate-token`.

## What Was Built

### Task 1: `AuthTestWithUserID` on `pkg/slack/Client`

Added `callJSONRaw(ctx, method, payload) ([]byte, error)` as a companion to `callJSON` — returns the raw response bytes without assuming the decode shape. This avoids touching the existing `SlackAPIResponse` / `SlackUserField` polymorphic decode that handles the string-vs-object `"user"` field.

`AuthTestWithUserID` uses `callJSONRaw` to POST `auth.test` and decode the result into a minimal struct extracting only `ok`, `error`, and `user_id`. Returns `("", *SlackAPIError)` on `ok=false`, `("", err)` on transport error, and `("UBOT...", nil)` on success.

`AuthTest(ctx) error` is preserved unchanged.

Tests added:
- `TestAuthTestWithUserID_OK` — httptest server returns real auth.test shape; assert `("UBOT123", nil)`
- `TestAuthTestWithUserID_NotOK` — server returns `ok=false`; assert `*SlackAPIError{Code: "invalid_auth"}`
- `TestAuthTestWithUserID_TransportError` — server closed before request; assert non-nil error

### Task 2: Interface extension + SSM wiring

Extended `SlackInitAPI` interface in `internal/app/cmd/slack.go` with `AuthTestWithUserID(ctx context.Context) (string, error)`.

`RunSlackInit` step 2: replaced `api.AuthTest(ctx)` with `botUserID, err := api.AuthTestWithUserID(ctx)`. After the bot-token Put (step 3), added a non-fatal `d.SSM.Put(ctx, d.SsmPrefix+"slack/bot-user-id", botUserID, false)` — prints WARN to stderr on failure, logs success to stdout.

`RunSlackRotateToken` step 2: same replacement. After the bot-token Put (step 3), same non-fatal bot-user-id Put block.

`fakeSlackInitAPI` and `fakeRotateAPI` in `slack_test.go` extended with `userID string` field and `AuthTestWithUserID` method returning `(f.userID, f.authErr)`.

Added helpers: `buildSlackTestDepsWithCapturingSSM` and `buildRotateTokenDepsCapturing` accept any `cmd.SlackSSMStore` to inject error-injecting or capturing SSM fakes.

Tests added in `slack_mention_init_test.go` (converted from `package cmd` to `package cmd_test`):
- `TestRunSlackInit_BotUserIDCached` — asserts SSM `Put("/km/slack/bot-user-id", "UBOT_FAKE", false)`
- `TestRunSlackInit_BotUserIDCached_PutErrorIsNonFatal` — asserts `RunSlackInit` returns nil when bot-user-id Put fails
- `TestRotateToken_BotUserIDCached` — asserts SSM `Put("/km/slack/bot-user-id", "UBOT_ROTATED", false)`
- `TestRotateToken_BotUserIDCached_PutErrorIsNonFatal` — asserts `RunSlackRotateToken` returns nil when bot-user-id Put fails

## Deviations from Plan

None — plan executed exactly as written.

## Note for Plan 05

The `{prefix}slack/bot-user-id` SSM path is now populated by `km slack init` / `km slack rotate-token`. Plan 05 reads it via a `getUID func(ctx) (string, error)` closure to implement the `checkSlackBotUserIDCached` doctor check.

## Self-Check: PASSED

- pkg/slack/client.go: FOUND
- internal/app/cmd/slack.go: FOUND
- internal/app/cmd/slack_mention_init_test.go: FOUND
- Task 1 commit 3f0ab9b: FOUND
- Task 2 commit 8923d20: FOUND
