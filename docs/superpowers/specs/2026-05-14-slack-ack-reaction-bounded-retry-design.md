# Slack ACK reaction bounded retry — design note

**Status:** Proposal — pending operator sign-off, then `/gsd:insert-phase 67.2`.
**Author:** Brainstorm session, 2026-05-14.
**Date:** 2026-05-14.

## Problem

Phase 67.1 added a 👀 ACK reaction on inbound Slack messages so users get
sub-second visual feedback that their message reached the sandbox. The
reaction is posted by the bridge Lambda via `reactions.add` in a
fire-and-forget goroutine after SQS enqueue succeeds.

In production, the 👀 sometimes fails to land — intermittently, on warm
Lambdas, with no clear pattern. The current implementation at
`pkg/slack/bridge/events_handler.go:228-241` does **a single HTTP attempt
with a 5s context timeout**, logs a `Warn` on any failure, and exits.

The discarded failure modes include:

- **HTTP 429 rate-limited.** `SlackReactorAdapter.Add` already produces
  the typed `ErrSlackRateLimited{RetryAfterSeconds}` (`pkg/slack/bridge/aws_adapters.go:494`),
  but the goroutine treats it as opaque and ignores the `Retry-After` value.
- **HTTP 5xx and network errors.** Slack-side incidents (a real one
  on 2026-05-14 prompted this work) produce transient failures that a
  retry would absorb.
- **Slack JSON errors `internal_error`, `service_unavailable`,
  `fatal_error`, `request_timeout`.** Currently all wrapped into a single
  generic error string (`pkg/slack/bridge/aws_adapters.go:512`) and
  discarded.

User impact: silent UX degradation. The user sees no 👀, assumes the
message didn't land, may re-post — at which point the bridge re-enqueues
to SQS (idempotency lives downstream on `event_id`, but a re-post is a
*new* event_id and the agent does pick it up twice).

## Proposed approach

Add bounded retry with backoff **inside `SlackReactorAdapter.Add`**, with
a classified error taxonomy that decides retry-vs-give-up per response.

### Why inside the adapter, not the call site

The adapter already owns the response-parsing knowledge that
distinguishes `already_reacted`, `ErrSlackRateLimited`, and the various
Slack JSON `error` strings. Error classification belongs co-located with
parsing. The handler-side goroutine stays a one-line `Reactor.Add(...)`
call. The `Reactor` interface signature (`pkg/slack/bridge/events_interfaces.go:73-79`)
is unchanged — existing fakes and tests keep working.

### Handler-side change

A single concession at `pkg/slack/bridge/events_handler.go:235`: bump
the `context.WithTimeout` from **5s → 10s** so the retry budget fits
without the last attempt being truncated by deadline. The goroutine
does not block the 200 response, so the extra wall-clock is free.

### Error classification

Three buckets, evaluated in order inside `Add`:

**Success (return nil):**
- HTTP 200 + `ok:true`
- HTTP 200 + `error:"already_reacted"` (idempotent — Slack delivered the
  event twice). Already implemented; preserved.

**Terminal — do NOT retry:**

| Bucket | Slack `error` codes | Log level |
|---|---|---|
| Operator-actionable auth | `invalid_auth`, `not_authed`, `account_inactive`, `token_revoked`, `missing_scope` | **Error** |
| Bad input / unrecoverable | `bad_timestamp`, `message_not_found`, `channel_not_found`, `not_reactable`, `thread_locked`, `invalid_name`, `too_many_emoji`, `too_many_reactions` | **Warn** |

**Transient — retry with backoff:**
- HTTP 429 with `Retry-After` (existing `ErrSlackRateLimited`)
- HTTP 5xx (any 500-599)
- Network errors: `net.Error` timeout, `io.EOF`, connection reset, DNS
  failure (anything `http.Client.Do` returns as a non-`nil` `err`)
- Slack JSON errors: `internal_error`, `service_unavailable`,
  `fatal_error`, `request_timeout`, `ratelimited` (in case Slack returns
  it in the JSON body rather than as HTTP 429)

**Default for unknown error strings:** treat as transient. Safer than
hard-failing on a string Slack adds tomorrow. The cost of one extra
retry on an actually-terminal error is acceptable; the cost of silently
ignoring a new transient signal is not.

### Retry strategy

- **Max 3 attempts total** (1 initial + 2 retries).
- **Backoff schedule:** 200ms, then 600ms, each with ±25% jitter.
  Jitter de-correlates retries across many sandboxes during a Slack
  incident, avoiding a thundering herd hitting Slack's API in lockstep.
- **`Retry-After` override.** If the response is `ErrSlackRateLimited`
  with `RetryAfterSeconds: N`:
  - If `N ≤ remaining context budget`, sleep `N` instead of the backoff
    schedule.
  - If `N > remaining context budget`, return the error immediately
    without sleeping. There is no point burning the deadline.
- **Context cancellation respected at every sleep.** Each backoff sleeps
  inside a `select { case <-time.After(d): case <-ctx.Done(): }`. If the
  10s wall-clock budget or Lambda shutdown fires, return the last error
  immediately.
- **Total worst-case wall-clock:** ~800ms of sleeps + 3 HTTP round-trips,
  comfortably under 10s for any normal Slack latency.

### Log shape

- One **Warn** line on final give-up (preserves the existing
  `events: reaction failed` line operators already grep for).
- One **Debug** line per intermediate retry (silent at default Lambda
  log level; visible if `KM_LOG_LEVEL=debug`).
- Auth-class terminal errors logged at **Error** so they stand out
  against the existing Warn baseline. These require operator action
  (rotate token, re-install app for missing scope) and should not be
  buried.

Structured fields on every log line: `channel`, `ts`, `emoji`, `attempt`
(1-indexed), `err`. The `attempt` field is new and lets future grep
queries count retry exhaustion vs first-attempt success.

## Components and data flow

```
                ┌─────────────────────────────────────────┐
                │ EventsHandler.dispatch goroutine        │
                │ (events_handler.go:228-241)             │
                │                                         │
                │   ctx = WithTimeout(bg, 10s)  ← 5s→10s  │
                │   Reactor.Add(ctx, ch, ts, emoji)       │
                │   on err: log Warn (UNCHANGED)          │
                └────────────────────┬────────────────────┘
                                     │
                                     ▼
        ┌────────────────────────────────────────────────────┐
        │ SlackReactorAdapter.Add  (aws_adapters.go:459)     │
        │                                                    │
        │   for attempt := 1; attempt <= 3; attempt++ {      │
        │     resp = call reactions.add                      │
        │     class = classify(resp)                         │
        │     switch class {                                 │
        │     case Success:        return nil                │
        │     case Terminal:       log + return err          │
        │     case Transient:      sleep + continue          │
        │     case RateLimited(d): sleep min(d, budget) + continue │
        │     }                                              │
        │   }                                                │
        │   return lastErr                                   │
        └────────────────────────────────────────────────────┘
```

No external dependencies, no schema changes, no profile-field changes.

## Test plan

New tests in `pkg/slack/bridge/aws_adapters_test.go`:

1. `TestReactor_Retries_OnInternalError_ThenSucceeds` — fake server
   returns `internal_error` twice, then `ok:true`. Expect: 3 calls, nil
   error.
2. `TestReactor_RetriesExhausted_ReturnsError` — fake returns
   `internal_error` 4 times. Expect: 3 calls, wrapped error.
3. `TestReactor_TerminalError_NoRetry` — `message_not_found`. Expect: 1
   call, wrapped error.
4. `TestReactor_AuthError_NoRetry_LogsError` — `invalid_auth`. Expect: 1
   call, error log level (assert via captured logger).
5. `TestReactor_429_HonorsRetryAfter` — first response 429 with
   `Retry-After: 1`, second 200. Expect: 2 calls, ~1s wall-clock, nil
   error.
6. `TestReactor_429_RetryAfterExceedsBudget` — `Retry-After: 30` with
   tightened 2s context. Expect: 1 call, `ErrSlackRateLimited` returned
   (no sleep, no second attempt).
7. `TestReactor_ContextCanceled_StopsRetrying` — cancel ctx during first
   backoff. Expect: 1 call, `ctx.Err()` returned promptly.
8. `TestReactor_NetworkError_Retries` — `httptest` server that closes
   the connection. Expect: 3 calls, then give up.
9. `TestReactor_AlreadyReacted_NoRetry_NoError` — kept to prevent
   regression of Phase 67.1 idempotency behavior.
10. `TestReactor_UnknownErrorString_TreatedTransient` — Slack returns
    `error:"some_new_thing"`. Expect: 3 calls (default-transient).

Existing handler-side tests (`TestEventsHandler_Reactor_FailureDoesNotBlock`,
`TestEventsHandler_Reactor_BotLoopSkips`) stay passing — the handler
contract is unchanged.

Jitter is not deterministic, so tests inject a `now` / `sleep` function
or use a `randSource io.Reader` field on the adapter to seed the jitter
deterministically. The existing adapter has no time injection today;
adding one is part of this phase.

## Deployment

Bridge-only change. Mirrors the Phase 67.1 deployment pattern exactly:

```bash
make build
km init --lambdas        # redeploy km-slack-bridge Lambda
```

No sandbox redeploy required. No `km init --sidecars`. No SSM/DDB
schema. No profile-field additions. No new IAM permissions.

## Out of scope

- Retry for `chat.postMessage` and `conversations.archive`. Different
  blast radius — postMessage failures are already visible to the user
  (their reply doesn't land), and archive is a one-shot lifecycle path
  triggered by `km destroy` where the operator already sees the error.
  Both are tracked in the existing pre-Phase-74 deferred-items if a
  later phase wants to revisit.
- CloudWatch metric filters / `km doctor` check on ACK failure rate.
  Discussed in brainstorming and explicitly cut for scope. A follow-up
  could add `slack_ack_reaction_failure_rate` as a doctor check reading
  CloudWatch Metric Filters, if recurring failures justify the
  observability investment.
- Reaction TTL / cleanup. The 👀 stays on the message forever today.
  Not a robustness concern.

## Rollout

- Phase 67.2 inserted between 67.1 and 68 via `/gsd:insert-phase 67.2`.
- Single plan, single PR. ~80 LOC change + ~200 LOC of tests.
- No feature flag. The retry behavior is strictly more reliable than the
  current single-attempt; there is no caller that would prefer the old
  behavior.
- Rollback: revert the PR and `km init --lambdas`. Bridge Lambda
  reverts cleanly; no state migration to undo.
