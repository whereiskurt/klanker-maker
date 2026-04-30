---
phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events
plan: 03
subsystem: infra
tags: [slack, ed25519, lambda, bridge, tdd, replay-protection, authorization]

# Dependency graph
requires:
  - phase: 63-02
    provides: pkg/slack (SlackEnvelope, VerifyEnvelope, CanonicalJSON, SignEnvelope, ActionPost/Archive/Test, SenderOperator, EnvelopeVersion)
  - phase: 14-sandbox-identity-signed-email-ed25519-key-pairs-for-inter-sandbox-trust
    provides: Ed25519 signing model; DynamoDB km-identities as source of truth for public keys
provides:
  - pkg/slack/bridge/interfaces.go — five narrow injectable interfaces consumed by Handler
  - pkg/slack/bridge/handler.go — Handler struct with Handle() implementing all seven verification steps
  - pkg/slack/bridge/handler_test.go — 21 unit tests covering every positive and negative branch
affects: [63-06, 63-07, 63-08, 63-09]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Pure-library Lambda handler: no AWS SDK or Lambda runtime imports; Plan 06 wires production dependencies"
    - "Narrow injectable interfaces: five interfaces allow test injection without any AWS credentials"
    - "Typed error ErrSlackRateLimited surfaces as HTTP 503 + Retry-After header"
    - "Seven-step verification pipeline: parse → timestamp → nonce → pubkey → signature → authz → bot-token → dispatch"
    - "DynamoDB public key lookup (via PublicKeyFetcher) enforced at interface contract level (RESEARCH.md correction #1)"

key-files:
  created:
    - pkg/slack/bridge/interfaces.go
    - pkg/slack/bridge/handler.go
    - pkg/slack/bridge/handler_test.go

key-decisions:
  - "PublicKeyFetcher interface documents DynamoDB as backend (NOT SSM) — enforces RESEARCH.md correction #1 at the type level"
  - "ErrSlackRateLimited typed error struct with RetryAfterSeconds field enables 503+Retry-After response without string parsing"
  - "Handler.Now is injectable for deterministic timestamp tests without real time.Now()"
  - "Channel-mismatch auth rejects sandbox post to non-owned channel; empty owned channel also rejects (no channel = no permission)"
  - "Sandbox cannot call archive or test actions — always 403; only operator can"
  - "Header sender (X-KM-Sender-ID) is defense-in-depth: mismatch with envelope.SenderID returns 401 before signature check"
  - "No AWS SDK or Lambda runtime imports in this package — Plan 06 (cmd/km-slack-bridge/main.go) owns production wiring"

patterns-established:
  - "Handler struct with injectable interfaces + Now func: testable without mocking entire AWS SDK"
  - "errResp/jsonResp helpers: consistent JSON error response construction"
  - "slackResponse function: centralizes Slack upstream error → bridge HTTP status mapping"
  - "fakeKeys/fakeNonces/fakeChannels/fakeToken/fakeSlack: in-memory fakes in _test.go avoid any network/DB in unit tests"

requirements-completed: [SLCK-04]

# Metrics
duration: 3min
completed: 2026-04-30
---

# Phase 63 Plan 03: km-slack-bridge Lambda Handler Summary

**Ed25519-verified bridge Lambda handler with seven-step pipeline: replay protection (timestamp + DynamoDB nonce), channel-mismatch authorization, action-level authz (sandbox post-only), and SlackPoster dispatch — all pure-library with narrow injectable interfaces and 21 unit tests**

## Performance

- **Duration:** 3 min
- **Started:** 2026-04-30T01:17:12Z
- **Completed:** 2026-04-30T01:20:09Z
- **Tasks:** 1 (TDD: implemented skeleton → test → pass in one pass)
- **Files modified:** 3 created

## Accomplishments

- Implemented `pkg/slack/bridge/interfaces.go` with five narrow interfaces: `PublicKeyFetcher` (DynamoDB-backed in Plan 06), `NonceStore` (DynamoDB conditional write), `ChannelOwnershipFetcher` (DynamoDB km-sandboxes), `BotTokenFetcher` (SSM SecureString), `SlackPoster` (pkg/slack.Client wrapper)
- Implemented `pkg/slack/bridge/handler.go` with `Handler.Handle()` executing all seven verification steps in order from CONTEXT.md, with correct HTTP status mapping: 400 (bad envelope/missing fields), 401 (stale timestamp/replayed nonce/bad signature/header mismatch), 403 (channel mismatch/forbidden action), 404 (unknown sender), 500 (nonce unavailable/key lookup failed/bot token unavailable), 502 (Slack upstream error), 503 (Slack rate-limited + Retry-After)
- Implemented `pkg/slack/bridge/handler_test.go` with 21 tests covering all branches: positive sandbox post, channel mismatch, sandbox archive/test forbidden, operator archive/test, stale timestamp (past and future), replayed nonce, bad signature (wrong key), unknown sender, bot token missing, Slack 429 → 503 + Retry-After, Slack 5xx → 502 with code propagated, DynamoDB nonce unavailable, bad JSON envelope, missing required fields, header sender mismatch, no header sender (still verifies)

## Task Commits

Each task was committed atomically:

1. **Task 1: Bridge interfaces, handler, and comprehensive tests** - `36c9395` (feat)

**Plan metadata:** (docs commit to follow)

## Files Created/Modified

- `pkg/slack/bridge/interfaces.go` — Five narrow interfaces + typed errors (ErrNonceReplayed, ErrSenderNotFound, ErrSlackRateLimited)
- `pkg/slack/bridge/handler.go` — Handler struct + Handle() with seven verification steps; Request/Response types; errResp/jsonResp/slackResponse helpers
- `pkg/slack/bridge/handler_test.go` — 21 unit tests using ed25519.GenerateKey(nil) ephemeral keys and in-memory fakes

## Decisions Made

- **PublicKeyFetcher interface enforces DynamoDB contract:** Interface comment explicitly documents that production implementation calls `pkg/aws.FetchPublicKey()` against DynamoDB `km-identities`, NOT SSM. This bakes RESEARCH.md correction #1 into the type contract so Plan 06 cannot accidentally use the wrong backend.
- **ErrSlackRateLimited as typed struct:** RetryAfterSeconds and Method fields enable both semantic error handling (Is/As pattern) and header construction without string manipulation.
- **Sandbox action authorization order:** The sandbox `archive`/`test` forbidden check runs before channel ownership check, matching the authorization flow from CONTEXT.md — reducing DynamoDB reads on authorization failures.
- **Empty owned channel = 403:** `owned == ""` (no Slack channel configured for sandbox) also returns `channel_mismatch`, preventing a sandbox with no channel from posting anywhere.

## Deviations from Plan

None - plan executed exactly as written. The `pkg/slack` package (Plan 02 dependency) was already present with `payload.go` and `payload_test.go` providing the required `SlackEnvelope`, `VerifyEnvelope`, `SignEnvelope`, `CanonicalJSON`, `ActionPost/Archive/Test`, `SenderOperator`, and `EnvelopeVersion` symbols.

## Issues Encountered

None. Tests all passed on first run.

## User Setup Required

None - no external service configuration required. Plan 06 (cmd/km-slack-bridge/main.go) wires production AWS clients against these interfaces.

## Next Phase Readiness

- `pkg/slack/bridge` is fully tested and ready for Plan 06 (Lambda main entry point) to wrap it
- Plan 06 needs to: adapt `APIGatewayV2HTTPRequest` → `bridge.Request`, implement all five interfaces with real AWS SDK clients, wire SSM-backed BotTokenFetcher and DynamoDB-backed PublicKeyFetcher/NonceStore/ChannelOwnershipFetcher
- Channel mismatch authorization is the meaningful blast-radius limiter from CONTEXT.md — a compromised sandbox can only post to its own channel
- The handler has no AWS SDK imports, so it compiles anywhere and is safe for local dev and CI without AWS credentials

---
*Phase: 63-slack-notify-hook-for-claude-code-permission-and-idle-events*
*Completed: 2026-04-30*
