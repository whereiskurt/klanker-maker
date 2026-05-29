---
phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
plan: 01
subsystem: slack
tags: [slack, go, tdd, users.lookupByEmail, conversations.invite, client-primitives]

# Dependency graph
requires:
  - phase: 72-00
    provides: Wave 0 stub test files (client_lookup_test.go, client_invite_test.go with t.Skip stubs)
provides:
  - "LookupUserByEmail method on *slack.Client (users.lookupByEmail, Layer 1)"
  - "InviteUserToChannel method on *slack.Client (conversations.invite, Layer 1, idempotent)"
  - "SlackAPIResponse.User field (additive, id only)"
  - "9 passing Layer 1 tests (4 lookup + 5 invite)"
affects:
  - 72-04 (EnsureMemberByEmail orchestrator consumes both methods)
  - 72-05 (km slack invite command)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Boolean-miss vs error distinction: users_not_found swallowed to (false, nil); all other errors surface as *SlackAPIError"
    - "Idempotency via error-code detection: already_in_channel swallowed to nil, mirrors JoinChannel contract"
    - "Email lowercasing before API dispatch (Pitfall 6: Slack profile email can be case-sensitive)"

key-files:
  created: []
  modified:
    - pkg/slack/client.go
    - pkg/slack/client_lookup_test.go
    - pkg/slack/client_invite_test.go

key-decisions:
  - "users_not_found maps to (false, nil) not an error — orchestrator branches on boolean, not error inspection"
  - "already_in_channel maps to nil (idempotent) — matches JoinChannel contract verbatim"
  - "Email lowercased + trimmed before dispatch — per Pitfall 6 from 72-RESEARCH.md"
  - "Sentinel ErrAlreadyInChannel deferred to Plan 72-04 — scope kept tight here; orchestrator will resolve via interface or strict sibling method"
  - "InviteUserToChannel uses single-user invocation only — bulk deferred per CONTEXT.md"

patterns-established:
  - "Boolean-miss pattern: typed boolean miss separates 'not found' from 'error' for lookup APIs"
  - "Idempotency-via-error-code: swallow specific Slack error codes rather than pre-checking state"

requirements-completed:
  - VALIDATION-Layer-1

# Metrics
duration: 2min
completed: 2026-05-29
---

# Phase 72 Plan 01: Layer 1 Slack Client Primitives Summary

**`users.lookupByEmail` and `conversations.invite` wired as tested *slack.Client methods with boolean-miss and idempotency contracts, 9 Layer 1 tests GREEN**

## Performance

- **Duration:** 2 min
- **Started:** 2026-05-29T18:49:42Z
- **Completed:** 2026-05-29T18:51:58Z
- **Tasks:** 2 (both TDD)
- **Files modified:** 3

## Accomplishments
- `LookupUserByEmail(ctx, email) (string, bool, error)`: `users_not_found` → `("", false, nil)` typed boolean miss; `missing_scope` and all other errors → `*SlackAPIError`; input email lowercased+trimmed
- `InviteUserToChannel(ctx, channelID, userID) error`: `already_in_channel` → `nil` (idempotent); `cant_invite_self`, `not_in_channel`, `user_is_restricted` → `*SlackAPIError`
- `SlackAPIResponse.User struct{ ID string }` field added (additive — no risk to existing decoders)
- 9 new tests all GREEN (4 lookup: Found, NotFound, MissingScope, LowercasesEmail; 5 invite: OK, AlreadyMember, CantInviteSelf, NotInChannel, UserIsRestricted)
- Zero regressions — full `pkg/slack` suite (216 tests) PASS, `go vet` clean, `make build` passing

## Final Method Signatures (from godoc)

```go
// LookupUserByEmail wraps users.lookupByEmail. Returns (id, true, nil) on hit;
// ("", false, nil) on users_not_found (typed boolean miss); ("", false, *SlackAPIError)
// on any other Slack error including missing_scope.
func (c *Client) LookupUserByEmail(ctx context.Context, email string) (string, bool, error)

// InviteUserToChannel wraps conversations.invite for a single user.
// Idempotent: treats already_in_channel as success (matching JoinChannel's contract).
func (c *Client) InviteUserToChannel(ctx context.Context, channelID, userID string) error
```

## Task Commits

Each task was committed atomically:

1. **Task 1: Implement LookupUserByEmail + extend SlackAPIResponse** - `948f312` (feat)
2. **Task 2: Implement InviteUserToChannel (idempotent)** - `239a694` (feat)

**Plan metadata:** (docs commit follows)

_Note: Both tasks used TDD (RED → GREEN). No separate REFACTOR commits needed — code was clean on first pass._

## Files Created/Modified
- `pkg/slack/client.go` - Added `User` field to `SlackAPIResponse`, added `LookupUserByEmail` method, added `InviteUserToChannel` method
- `pkg/slack/client_lookup_test.go` - Replaced t.Skip stubs with 4 real assertions (Found, NotFound, MissingScope, LowercasesEmail)
- `pkg/slack/client_invite_test.go` - Replaced t.Skip stubs with 5 real assertions (OK, AlreadyMember, CantInviteSelf, NotInChannel, UserIsRestricted)

## Decisions Made

**Email lowercasing (Pitfall 6):** Per 72-RESEARCH.md Pitfall 6, Slack's `users.lookupByEmail` matches against the email Slack stores in the user profile, which can be case-sensitive in edge cases. We lowercase + trim before dispatch to avoid spurious `users_not_found` on casing mismatch. Test `TestClient_LookupUserByEmail_LowercasesEmail` proves the invariant.

**Deferred: sentinel `ErrAlreadyInChannel`:** Plan 72-04's `EnsureMemberByEmail` orchestrator needs to distinguish "just invited" from "was already a member". This plan keeps the idempotent `InviteUserToChannel` (swallows `already_in_channel` → nil). Plan 72-04 has discretion to add a sibling `inviteUserToChannelStrict` or thread a sentinel error. Decision deferred per plan guidance to keep 72-01 scope tight.

**No Slack Lambda/sidecar/signing code touched:** Per CONTEXT.md scope guard, this plan is operator-side client primitives only. The bridge Lambda, signing, and sidecar code are unaffected.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## Note for Plan 72-04

The `EnsureMemberByEmail` orchestrator must distinguish "user was just invited" from "user was already a member". Options:
1. Add a sibling `InviteUserToChannelStrict` returning a typed result enum (`Invited` / `AlreadyMember`)
2. Add a package-level `ErrAlreadyInChannel` sentinel and thread it through a strict variant
3. Structure an `InviteAPI` interface that splits the methods at the call layer

Plan 72-04 has the discretion to pick. The idempotent `InviteUserToChannel` remains the safe default for callers that don't care about the distinction.

## Next Phase Readiness

- Layer 1 primitives are complete and tested; Plan 72-02 (manifest template) and Plan 72-04 (orchestrator) can proceed in parallel
- `LookupUserByEmail` and `InviteUserToChannel` are ready to be composed by Wave 2

---
*Phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator*
*Completed: 2026-05-29*
