---
phase: 97-github-comment-trigger-mvp
plan: "03"
subsystem: profile-schema, sqs-lifecycle, dynamo-metadata, km-create-destroy
tags: [github-inbound, sqs-fifo, profile-schema, metadata-round-trip, km-create, km-destroy]
dependency_graph:
  requires: [97-01, 97-02]
  provides: [github-inbound-queue-provisioning, github-review-profile, notification-github-schema]
  affects: [97-04, 97-05]
tech_stack:
  added: []
  patterns:
    - deps-struct DI pattern (githubInboundDeps mirrors slackInboundDeps)
    - SandboxMetadata 4-spot round-trip (struct, copy, unmarshal, marshal)
    - TDD RED/GREEN for all tasks
key_files:
  created:
    - pkg/aws/github_inbound_test.go
    - pkg/profile/github_notification_test.go
    - internal/app/cmd/create_github_inbound.go
    - internal/app/cmd/create_github_inbound_test.go
    - internal/app/cmd/destroy_github_inbound.go
    - profiles/github-review.yaml
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/inherit.go
    - pkg/aws/sqs.go
    - pkg/aws/metadata.go
    - pkg/aws/sandbox.go
    - pkg/aws/sandbox_dynamo.go
    - internal/app/cmd/create.go
    - internal/app/cmd/destroy.go
    - scripts/validate-all-profiles.sh
decisions:
  - "GitHubInboundQueueName was absent (plan 02 hadn't landed yet); added it per the plan 03 guarded-add behavior (Wave-1 first-writer rule). Plan 02's commit included our test files as part of the same git state."
  - "github-review.yaml uses non-empty sidecar images (km-dns-proxy:latest etc.) — schema requires minLength:1 even for disabled sidecars, matching all existing profiles."
  - "drainGitHubInbound wired into destroy.go local path only (GitHub token SSM cleanup in Step 7c is a pre-existing separate concern owned by plan 01, not plan 03)."
metrics:
  duration: 13m44s
  completed_date: "2026-06-06"
  tasks_completed: 3
  files_changed: 16
---

# Phase 97 Plan 03: GitHub Inbound Queue Provisioning + Profile Summary

**One-liner:** Per-sandbox `github-inbound` FIFO queue lifecycle (create/destroy/rollback) with DDB 4-spot round-trip, `notification.github.inbound.enabled` tri-state schema, and lean `github-review` built-in profile.

## Tasks Completed

| # | Task | Commit | Key Files |
|---|------|--------|-----------|
| 1 | Profile field + schema + merge; DDB round-trip; SQS create/delete helpers | 89f646e3 (wave-1 shared) | types.go, schema.json, inherit.go, sqs.go, metadata.go, sandbox.go, sandbox_dynamo.go |
| 2 | create_github_inbound.go + destroy_github_inbound.go | 3239d177 | create_github_inbound.go, create_github_inbound_test.go, destroy_github_inbound.go, create.go, destroy.go |
| 3 | github-review built-in profile + validate-all-profiles inventory | 2324efc9 | profiles/github-review.yaml, scripts/validate-all-profiles.sh |

## What Was Built

### Task 1: Profile field + schema + merge; DDB round-trip; SQS helpers

- **`pkg/profile/types.go`**: Added `NotificationGitHubSpec` / `NotificationGitHubInboundSpec` (tri-state `*bool Enabled`) and `Github *NotificationGitHubSpec` on `NotificationSpec`. Mirrors the Slack analog (`NotificationSlackInboundSpec` :147).
- **`pkg/profile/schemas/sandbox_profile.schema.json`**: Added `notification.github.inbound.enabled` boolean schema block (sibling of `slack`), with `additionalProperties: false`.
- **`pkg/profile/inherit.go`**: Added `mergeNotificationGitHubSpec` + `mergeNotificationGitHubInboundSpec`; wired into `mergeNotificationSpec`.
- **`pkg/aws/sqs.go`**: Added `GitHubInboundQueueName(prefix, id)` → `"{prefix}-github-inbound-{id}.fifo"`, `CreateGitHubInboundQueue` (same FIFO attrs as Slack: ContentBasedDedup=false, vis=30s, retention=14d), `DeleteGitHubInboundQueue` (best-effort, QueueDoesNotExist=nil).
- **`pkg/aws/metadata.go`**: Added `GithubInboundQueueURL string` field with the full round-trip warning (project_sandboxmetadata_lossy_roundtrip).
- **`pkg/aws/sandbox.go`**: Added `GithubInboundQueueURL string` to `SandboxRecord`.
- **`pkg/aws/sandbox_dynamo.go`**: Added `GithubInboundQueueURL` in all 4 spots: `metadataToRecord` copy, `unmarshalGitHubFields` (new), `marshalSandboxItem`, and wired `unmarshalGitHubFields` into `ReadSandboxMetadataDynamo` + `ListAllSandboxesByDynamo` + `ListAllSandboxMetadataDynamo`.

### Task 2: create_github_inbound.go + destroy_github_inbound.go

- **`create_github_inbound.go`**: `githubInboundDeps` struct DI pattern; `notificationGitHubInbound()` nil-safe gate; `provisionGitHubInboundQueue` — enabled:true creates FIFO, writes `github_inbound_queue_url` DDB attr, writes SSM `/{prefix}/sandbox/{id}/github-inbound-queue-url`; best-effort rollback on DDB or SSM failure; `rollbackGitHubInboundQueue`.
- **`destroy_github_inbound.go`**: `githubDestroyInboundDeps` + `drainGitHubInbound` — deletes FIFO queue + SSM param, both best-effort.
- **`create.go`**: Step 11f wires `provisionGitHubInboundQueue` after Step 11e (Slack inbound), with SQS + SSM client initialization.
- **`destroy.go`**: `drainGitHubInbound` wired into local destroy path when `meta.GithubInboundQueueURL != ""`.

### Task 3: github-review built-in profile + inventory

- **`profiles/github-review.yaml`**: Lean Phase 97 profile; `t3.medium` spot EC2 `us-east-1`; `ttl=2h`/`idleTimeout=20m`/`teardownPolicy=stop`; proxy enforcement; egress `.github.com`/`.githubusercontent.com`/`.amazonaws.com`/`api.anthropic.com`; Claude agent with `Bash/Read/Write/Edit/Glob/Grep/WebFetch` auto-approved; `notification.github.inbound.enabled: true`; prefix `gh`.
- **`scripts/validate-all-profiles.sh`**: Bumped inventory comment 20→21 files, added `profiles/github-review.yaml` entry; all 22 profiles pass `km validate`.

## Success Criteria Status

- [x] `notification.github.inbound.enabled` tri-state validates and merges
- [x] `km create` provisions queue+DDB+SSM when enabled; zero artifacts when disabled; rollback + destroy cleanup
- [x] `github_inbound_queue_url` round-trips all 4 metadata spots (survives pause/resume)
- [x] `CreateGitHubInboundQueue`/`DeleteGitHubInboundQueue` reuse `GitHubInboundQueueName` (no duplicate)
- [x] `github-review` profile validates and is gated by `validate-all-profiles.sh`

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing functionality] GitHubInboundQueueName absent from sqs.go**
- **Found during:** Task 1 (Wave-1 concurrent execution — plan 02 hadn't landed yet)
- **Issue:** `GitHubInboundQueueName` not yet in `pkg/aws/sqs.go` (plan 02 is the owner)
- **Fix:** Added it per the plan 03 guarded-add behavior specified in the OBJECTIVE ("if executing before plan 02 within Wave-1, add the single helper; never duplicate it"). Plan 02's commit (`89f646e3`) ultimately included our test files as part of the same git commit because it ran concurrently.
- **Files modified:** `pkg/aws/sqs.go`
- **Commit:** 89f646e3 (wave-1 shared commit)

**2. [Rule 1 - Bug] github-review.yaml sidecar image minLength validation failure**
- **Found during:** Task 3 (first `km validate` run)
- **Issue:** Empty sidecar `image: ""` fails the schema `minLength: 1` constraint
- **Fix:** Updated sidecar images to `km-dns-proxy:latest`, `km-http-proxy:latest`, `km-audit-log:latest`, `km-tracing:latest` — matching the pattern of all existing profiles
- **Files modified:** `profiles/github-review.yaml`
- **Commit:** 2324efc9

### Pre-existing Issues (out of scope)

- `TestRunDestroy_GitHubTokenCleanup` in `destroy_test.go:17` — pre-existing failure documented in the phase's `deferred-items.md` since Plan 01. Checks for literal format string `/sandbox/%s/github-token` in destroy.go that hasn't been wired yet. Not caused by Plan 03 changes (verified via git stash).

## Self-Check: PASSED

All key files found. Commits 3239d177 and 2324efc9 exist. Tests green:
- `go test ./pkg/profile/ ./pkg/aws/ -run 'GitHub|Notification|Metadata|InboundQueue' -count=1` PASS
- `go test ./internal/app/cmd/ -run GitHubInbound -count=1` PASS
- `bash scripts/validate-all-profiles.sh` PASS (22 profiles)
- `make build` PASS
