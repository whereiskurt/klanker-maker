---
phase: 59-email-sender-allowlist-for-operator-inbox-and-sandbox-inbound
plan: 02
subsystem: email
tags: [ses, lambda, allowlist, sender-filtering, bash, userdata]

requires:
  - phase: 59-01
    provides: "MatchesAllowList with email pattern support (shared utility)"
provides:
  - "Operator inbox sender allowlist enforcement via Lambda (km-config.yaml)"
  - "Sandbox inbound sender filtering via km-mail-poller and km-recv"
  - "KM_ALLOWED_SENDERS env var wired from profile through userdata template"
affects: [email-enforcement, sandbox-security, km-init]

tech-stack:
  added: []
  patterns: ["inline pattern matching in Lambda (avoids pkg/aws import)", "colon-separated env var for multi-value patterns", "belt-and-suspenders filtering at poller + reader"]

key-files:
  created: []
  modified:
    - "cmd/email-create-handler/main.go"
    - "cmd/email-create-handler/main_test.go"
    - "pkg/compiler/userdata.go"

key-decisions:
  - "Inline pattern matching in Lambda instead of importing pkg/aws MatchesAllowList to keep binary lean"
  - "Fail-open when km-config.yaml missing or S3 error for backward compatibility"
  - "Belt-and-suspenders: km-mail-poller filters at download, km-recv filters at display"

patterns-established:
  - "Operator config read: Lambda reads toolchain/km-config.yaml from S3 per invocation (no caching)"
  - "Sender pattern format: colon-separated, supports exact, *@domain, sandbox-id, 'self', '*'"

requirements-completed: [AL-05, AL-06, AL-07]

duration: 8min
completed: 2026-04-21
---

# Phase 59 Plan 02: Sender Allowlist Enforcement Summary

**Lambda sender allowlist from km-config.yaml with sandbox inbound filtering via km-mail-poller/km-recv and KM_ALLOWED_SENDERS env var**

## Performance

- **Duration:** 8 min
- **Started:** 2026-04-21T12:22:02Z
- **Completed:** 2026-04-21T12:30:00Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments
- Lambda reads email.allowedSenders from km-config.yaml in S3 and enforces sender allowlist before safe phrase validation
- Silently drops non-allowed senders; allows all when list is empty (backward compatible)
- km-mail-poller filters at download time, km-recv has belt-and-suspenders check at display time
- AllowedSenders wired from profile EmailSpec through userdata template to systemd env and /etc/profile.d/

## Task Commits

Each task was committed atomically:

1. **Task 1: Add operator inbox sender allowlist to Lambda** - `b5407c8` (test: RED), `0ebfbea` (feat: GREEN)
2. **Task 2: Wire AllowedSenders to sandbox bash scripts via userdata template** - `66d9af0` (feat)

## Files Created/Modified
- `cmd/email-create-handler/main.go` - loadOperatorAllowlist, inline pattern matching, allowlist check before Step 5
- `cmd/email-create-handler/main_test.go` - 4 tests: SenderNotAllowed, SenderAllowed, EmptyAllowlist, AllowlistS3Error
- `pkg/compiler/userdata.go` - AllowedSenders field, joinAllowedSenders helper, km-mail-poller filtering, km-recv filtering, profile.d export

## Decisions Made
- Inline pattern matching in Lambda (path.Match for wildcards) avoids importing full pkg/aws package into Lambda binary
- Fail-open on S3 errors ensures backward compatibility for existing deployments without km-config.yaml email section
- Belt-and-suspenders filtering: km-mail-poller filters at download preventing disk writes, km-recv filters at display preventing leaked messages from showing

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Added DynamoClient to test handlers for status path**
- **Found during:** Task 1 (TDD GREEN)
- **Issue:** Tests for SenderAllowed/EmptyAllowlist/AllowlistS3Error panicked because handleStatus requires DynamoClient which newTestHandler doesn't set
- **Fix:** Added `h.DynamoClient = &mockDynamo{}` and `h.SandboxTableName` to test handlers that proceed past allowlist check
- **Files modified:** cmd/email-create-handler/main_test.go
- **Verification:** All 4 tests pass
- **Committed in:** 0ebfbea (Task 1 GREEN commit)

---

**Total deviations:** 1 auto-fixed (1 bug)
**Impact on plan:** Test infrastructure fix only. No scope creep.

## Issues Encountered
None

## User Setup Required
None - no external service configuration required. After `km init --sidecars`, the Lambda will pick up email.allowedSenders from km-config.yaml. Sandbox profiles with spec.email.allowedSenders will get filtering automatically.

## Next Phase Readiness
- Sender allowlist enforcement complete at both operator inbox and sandbox inbound
- Plan 01 (MatchesAllowList extension) can execute independently
- `km init --sidecars` needed to deploy updated Lambda and toolchain

---
*Phase: 59-email-sender-allowlist-for-operator-inbox-and-sandbox-inbound*
*Completed: 2026-04-21*
