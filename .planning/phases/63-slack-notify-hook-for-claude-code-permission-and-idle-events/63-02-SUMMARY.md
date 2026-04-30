---
phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events
plan: "02"
subsystem: slack
tags: [slack, ed25519, canonical-json, http-client, retry, tdd]

requires:
  - phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
    provides: "Ed25519 signing pattern (pkg/aws/identity.go) — pkg/slack follows the same model"

provides:
  - "pkg/slack/payload.go: SlackEnvelope, BuildEnvelope, CanonicalJSON, SignEnvelope, VerifyEnvelope, ErrBodyTooLarge, MaxBodyBytes, EnvelopeVersion, ActionPost/Archive/Test, SenderOperator"
  - "pkg/slack/client.go: Client, NewClient, SlackAPIResponse, SlackAPIError, AuthTest, PostMessage, CreateChannel, InviteShared, ArchiveChannel, PostToBridge, BridgeBackoff, PostResponse"

affects:
  - 63-03-bridge-handler
  - 63-05-km-slack-binary
  - 63-07-km-slack-init-test
  - 63-09-km-destroy-slack-signoff

tech-stack:
  added: []
  patterns:
    - "Alphabetically-tagged struct fields + encoding/json gives deterministic canonical bytes without a custom serializer"
    - "BridgeBackoff package-level var for test shim — shrink to 1ms in init() to avoid slow retry tests"
    - "callJSON shared dispatcher pattern for all Slack API methods — single point for auth header + error decoding"
    - "TDD: test file created first (RED), implementation follows (GREEN), no refactor step needed"

key-files:
  created:
    - pkg/slack/payload.go
    - pkg/slack/payload_test.go
    - pkg/slack/client.go
    - pkg/slack/client_test.go
  modified: []

key-decisions:
  - "Alphabetical struct tag order + encoding/json gives deterministic canonical JSON; no custom serializer needed — per RESEARCH.md recommendation"
  - "Thin HTTP client with 5 methods, no slack-go/slack SDK dependency — consistent with codebase's no-SDK pkg/aws/ style"
  - "BridgeBackoff is a package-level var so tests can shrink it to milliseconds without test helper complexity"
  - "PostToBridge uses http.DefaultClient (not the Client struct) since bridge calls originate from sandbox binary, not from an operator-side session with a bot token"
  - "4 total attempts (1 initial + 3 retries) matching SLCK-03 spec: retry on 5xx/network, fail-fast on 4xx, ctx-aware"

patterns-established:
  - "SlackEnvelope canonical JSON: struct tags alphabetical, json.Encoder + SetEscapeHTML(false) + TrimRight newline"
  - "PostToBridge retry: BridgeBackoff[attempt-1] sleep in select with ctx.Done() for cancellation during backoff"

requirements-completed: [SLCK-03]

duration: 12min
completed: 2026-04-30
---

# Phase 63 Plan 02: pkg/slack foundation — SlackEnvelope, canonical JSON, Ed25519 sign/verify, Slack Web API client, PostToBridge retry helper Summary

**Zero-dependency stdlib pkg/slack package with alphabetical-tagged SlackEnvelope canonical JSON, Ed25519 sign/verify, thin 5-method Slack Web API client, and a 4-attempt ctx-aware PostToBridge retry helper — shared contract for km-slack binary, bridge Lambda, and operator CLI in Plans 03/05/07/09.**

## Performance

- **Duration:** ~12 min
- **Started:** 2026-04-30T01:07:00Z
- **Completed:** 2026-04-30T01:19:14Z
- **Tasks:** 2
- **Files created:** 4

## Accomplishments

- `pkg/slack/payload.go`: SlackEnvelope struct with alphabetically-tagged JSON fields producing deterministic canonical bytes; BuildEnvelope with 128-bit crypto/rand nonce + Unix timestamp + 40KB body cap; SignEnvelope and VerifyEnvelope using stdlib crypto/ed25519
- `pkg/slack/client.go`: Thin Slack Web API client (AuthTest, PostMessage, CreateChannel, InviteShared, ArchiveChannel) with shared callJSON dispatcher; PostToBridge with 3-backoff retry, fail-fast on 4xx, context cancellation honored during sleep
- 24 total tests (9 payload + 15 client/bridge) all green; go vet clean; go build ./... clean; zero non-stdlib dependencies

## Task Commits

Each task was committed atomically:

1. **Task 1: Build pkg/slack/payload.go** - `81fed82` (feat — TDD RED then GREEN)
2. **Task 2: Build pkg/slack/client.go** - `31495a3` (feat — TDD RED then GREEN)

**Plan metadata:** (docs commit below)

## Files Created/Modified

- `pkg/slack/payload.go` — SlackEnvelope, BuildEnvelope, CanonicalJSON, SignEnvelope, VerifyEnvelope, ErrBodyTooLarge, MaxBodyBytes, EnvelopeVersion, ActionPost/Archive/Test, SenderOperator
- `pkg/slack/payload_test.go` — 9 tests: happy path, 40KB body cap boundary, canonical JSON determinism + field order golden string, sign/verify round-trip, wrong key, mutated body
- `pkg/slack/client.go` — Client, NewClient, SetBaseURL, callJSON, AuthTest, PostMessage, CreateChannel, InviteShared, ArchiveChannel, PostResponse, BridgeBackoff, PostToBridge
- `pkg/slack/client_test.go` — 15 tests: all 5 Slack methods (OK + error paths), Bearer header assertion, PostToBridge happy path, 4xx fail-fast, 5xx retry-then-succeed, persistent 5xx, network error, headers, context cancel

## Decisions Made

- **Alphabetical struct tags + encoding/json**: deterministic canonical bytes without a custom serializer — both km-slack and the bridge Lambda import the same struct, so byte-identity of the signed message is guaranteed
- **No slack-go/slack SDK**: consistent with codebase's no-SDK `pkg/aws/` pattern; 5 methods covers the full Phase 63 API surface
- **BridgeBackoff as exported var**: allows test `init()` to shrink to 1ms without changing function signatures or introducing test helper indirection
- **PostToBridge uses http.DefaultClient**: bridge calls originate from the sandbox binary with no bot token; the Client struct is for operator-side API calls
- **4 total attempts**: matches SLCK-03 spec — 1 initial attempt + 3 retries with 1s/2s/4s backoff

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required. pkg/slack is a pure library; Slack bot token and bridge URL are consumed by Plans 03 and 05.

## Next Phase Readiness

- Plan 03 (bridge Lambda handler) can import `pkg/slack.SlackEnvelope`, `VerifyEnvelope`, `Client` immediately
- Plan 05 (km-slack binary) can import `pkg/slack.BuildEnvelope`, `SignEnvelope`, `PostToBridge` immediately
- Plan 07 (km slack init/test) can import `pkg/slack.Client` for direct Slack API calls
- Plan 09 (km destroy Slack signoff) can import `pkg/slack.SignEnvelope` + `PostToBridge`
- No blockers

---
*Phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events*
*Completed: 2026-04-30*
