---
phase: 97-github-comment-trigger-mvp
plan: 02
subsystem: github-bridge
tags: [github, sqs, eventbridge, permissions, cold-create, fifo]

# Dependency graph
requires:
  - phase: 97-01
    provides: SandboxProfile schema + km-github-bridge Lambda skeleton
provides:
  - "CompilePermissions extended with comment→issues:write, review→pull_requests:write, checks→checks:write"
  - "GitHubInboundWritePerms() helper returning the canonical github-inbound write permission set"
  - "generateAndStoreGitHubToken call-site passes write verbs instead of nil for github-inbound sandboxes"
  - "SandboxCreateDetail.GithubEnvelope field carries webhook envelope over EventBridge"
  - "CreateEvent.GithubEnvelope field matches for Lambda deserialization"
  - "GitHubInboundQueueName(prefix, id) helper in pkg/aws/sqs.go"
  - "CreateGitHubInboundQueue / DeleteGitHubInboundQueue FIFO queue lifecycle functions"
  - "SandboxMetadata.GithubInboundQueueURL + SandboxRecord.GithubInboundQueueURL with full marshal/unmarshal round-trip"
  - "create-handler drainGithubEnvelope: enqueues carried envelope into github-inbound FIFO queue post-provision"
affects:
  - 97-03 (builds CreateGitHubInboundQueue/DeleteGitHubInboundQueue on top of GitHubInboundQueueName)
  - 97-04 (km-github-bridge cold path uses SandboxCreateDetail.GithubEnvelope)
  - 97-05 (github-inbound poller drains the queue that create-handler populates)

# Tech tracking
tech-stack:
  added: [crypto/sha256 for FIFO dedup ID generation]
  patterns:
    - "Best-effort enqueue: SQS error logs warn but does not fail the create"
    - "FIFO dedup via SHA-256 hex of envelope body (5-minute dedup window protection)"
    - "GithubInboundSQSAPI narrow interface for DI in create-handler tests"
    - "CompilePermissions verb-based API (profile verbs → GitHub API permission map)"

key-files:
  created: []
  modified:
    - pkg/github/token.go
    - pkg/github/token_test.go
    - pkg/aws/eventbridge.go
    - pkg/aws/eventbridge_test.go
    - pkg/aws/sqs.go
    - pkg/aws/metadata.go
    - pkg/aws/sandbox.go
    - pkg/aws/sandbox_dynamo.go
    - cmd/create-handler/main.go
    - cmd/create-handler/main_test.go
    - internal/app/cmd/create.go

key-decisions:
  - "Added checks→checks:write verb to CompilePermissions so the full github-inbound write set {issues:write, pull_requests:write, contents:write, checks:write} is achievable via verb slice"
  - "Pitfall 6 fix: generateAndStoreGitHubToken call-site in create.go passes [comment, review, push, checks] instead of nil for github-inbound sandboxes"
  - "drainGithubEnvelope is best-effort — SQS SendMessage failure logs warn but does not fail the create; operator can re-mention"
  - "FIFO MessageDeduplicationId = SHA-256 hex of envelope body (ContentBasedDeduplication=false, manual dedup required)"
  - "GithubInboundQueueName owned by pkg/aws (plan 02) not plan 03 — eliminates intra-wave ordering dependency"

patterns-established:
  - "Verb-to-permission mapping pattern: CompilePermissions handles profile verbs; GitHubInboundWritePerms() returns the compiled canonical set"
  - "GithubInboundQueueURL round-trip invariant: both marshalSandboxItem and unmarshalGitHubFields must be kept symmetric (project_sandboxmetadata_lossy_roundtrip footgun)"

requirements-completed: [GH-BRIDGE-ROUTE]

# Metrics
duration: 9min
completed: 2026-06-06
---

# Phase 97 Plan 02: GitHub Permission Scopes + Cold-Create Envelope Carry Summary

**CompilePermissions extended with write verbs (comment/review/checks) + github envelope carries through EventBridge SandboxCreate to create-handler FIFO enqueue — Pitfall 1 and Pitfall 6 fixed**

## Performance

- **Duration:** 9 min (527s)
- **Started:** 2026-06-06T19:12:35Z
- **Completed:** 2026-06-06T19:21:22Z
- **Tasks:** 3
- **Files modified:** 11

## Accomplishments

- Extended `CompilePermissions` with `comment→issues:write`, `review→pull_requests:write`, `checks→checks:write` and added `GitHubInboundWritePerms()` canonical write permission helper
- Fixed Pitfall 6: `generateAndStoreGitHubToken` call-site in `create.go` now passes `[comment, review, push, checks]` verbs instead of `nil` so per-sandbox tokens carry write permissions
- Fixed Pitfall 1: `SandboxCreateDetail.GithubEnvelope` carries the webhook JSON through EventBridge; `CreateEvent` deserializes it; `create-handler.drainGithubEnvelope` enqueues it into the github-inbound FIFO queue post-provision with SHA-256 dedup
- Implemented `GitHubInboundQueueName`, `CreateGitHubInboundQueue`, `DeleteGitHubInboundQueue` in `pkg/aws/sqs.go`; `GithubInboundQueueURL` field added to `SandboxMetadata`/`SandboxRecord` with full marshal/unmarshal round-trip in `sandbox_dynamo.go`

## Task Commits

1. **Task 1: Extend CompilePermissions + fix github-inbound token scopes** - `f02b2eb7` (feat)
2. **Task 2: Carry github envelope through SandboxCreateDetail + CreateEvent** - `89f646e3` (feat)
3. **Task 3: GitHubInboundQueueName + create-handler drains envelope post-provision** - `df2faed6` (feat)

## Files Created/Modified

- `pkg/github/token.go` - Added comment/review/checks verb mappings to CompilePermissions; added GitHubInboundWritePerms()
- `pkg/github/token_test.go` - Table-driven tests for new verb mappings and GitHubInboundWritePerms
- `pkg/aws/eventbridge.go` - Added GithubEnvelope field to SandboxCreateDetail
- `pkg/aws/eventbridge_test.go` - Round-trip tests for GithubEnvelope marshal/omitempty/PutSandboxCreateEvent
- `pkg/aws/sqs.go` - Added GitHubInboundQueueName, CreateGitHubInboundQueue, DeleteGitHubInboundQueue
- `pkg/aws/metadata.go` - Added GithubInboundQueueURL field to SandboxMetadata
- `pkg/aws/sandbox.go` - Added GithubInboundQueueURL field to SandboxRecord
- `pkg/aws/sandbox_dynamo.go` - marshalSandboxItem/unmarshalGitHubFields/metadataToRecord updated for GithubInboundQueueURL
- `cmd/create-handler/main.go` - GithubEnvelope field in CreateEvent; SQSClient field; drainGithubEnvelope method; sqs.NewFromConfig in main()
- `cmd/create-handler/main_test.go` - mockSQSSendAPI + enqueue/no-enqueue/error-non-fatal tests
- `internal/app/cmd/create.go` - githubWriteVerbs passed to generateAndStoreGitHubToken instead of nil

## Decisions Made

- Added `checks→checks:write` as a new CompilePermissions verb to complete the github-inbound permission set without bypassing the verb abstraction layer
- `GitHubInboundQueueName` ownership moved to plan 02 (rather than plan 03) to eliminate intra-wave ordering dependency — plan 03 can build `CreateGitHubInboundQueue` on top without a cross-plan compile dependency
- FIFO `MessageDeduplicationId` uses SHA-256 hex of the envelope body since `ContentBasedDeduplication=false` is locked per the slack-inbound queue pattern
- Enqueue is best-effort (log warn, not fail) so a transient SQS error at provision time doesn't leave the sandbox in a failed state; operator can re-mention

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Implemented Plan 03 symbols required by pre-committed github_inbound_test.go**
- **Found during:** Task 2 (running tests against pkg/aws)
- **Issue:** `pkg/aws/github_inbound_test.go` (package internal, pre-committed with plan 03 test stubs) referenced `CreateGitHubInboundQueue`, `DeleteGitHubInboundQueue`, `SandboxMetadata.GithubInboundQueueURL`, `unmarshalGitHubFields`, and `metadataToRecord.GithubInboundQueueURL` — all Plan 03 symbols. Package `pkg/aws` would not compile without them, blocking all tests.
- **Fix:** Implemented all referenced symbols (already partially pre-authored in the branch): `CreateGitHubInboundQueue`/`DeleteGitHubInboundQueue` in `sqs.go`, `GithubInboundQueueURL` in `metadata.go`/`sandbox.go`, `unmarshalGitHubFields`/`marshalSandboxItem` extension in `sandbox_dynamo.go`, `metadataToRecord` copy in `sandbox_dynamo.go`. Confirmed none were redefined in plan 03 scope.
- **Files modified:** pkg/aws/sqs.go, pkg/aws/metadata.go, pkg/aws/sandbox.go, pkg/aws/sandbox_dynamo.go
- **Verification:** `go test ./pkg/aws/ -count=1` green; all github_inbound_test.go tests pass
- **Committed in:** df2faed6 (Task 3 commit)

---

**Total deviations:** 1 auto-fixed (1 Rule 3 blocking)
**Impact on plan:** The pre-committed test file expected plan 02+03 symbols in one package. Implementing them now is consistent with the coordination note (plan 02 owns `GitHubInboundQueueName`; plan 03 should not redefine it). No scope creep.

## Issues Encountered

None beyond the Rule 3 deviation above.

## Next Phase Readiness

- Wave 1 foundation complete: permission scopes, envelope carry, and queue name helper are all in place
- Plan 03 can provision/destroy the github-inbound queue at `km create`/`km destroy` time using `CreateGitHubInboundQueue`/`DeleteGitHubInboundQueue` (already implemented)
- Plan 04 (km-github-bridge Wave 2) can populate `SandboxCreateDetail.GithubEnvelope` on cold-create path and the create-handler will drain it automatically
- `make build` passes; `go test ./pkg/github/ ./pkg/aws/ ./cmd/create-handler/` all green

---
*Phase: 97-github-comment-trigger-mvp*
*Completed: 2026-06-06*
