---
phase: 101
plan: 03
subsystem: github-bridge
tags: [github, orphan-reply, claim-aware, scatter-gather, tdd]
dependency_graph:
  requires: ["101-01", "101-02"]
  provides: ["GH-ORPHAN-CLAIM", "GH-ORPHAN-REPLY", "GH-ORPHAN-COOLDOWN", "GH-ORPHAN-ROLLOUT"]
  affects: ["pkg/github/bridge", "cmd/km-github-bridge"]
tech_stack:
  added: []
  patterns:
    - "TDD (RED→GREEN) with table-driven subtests"
    - "Claim-aware scatter-gather: same pattern as Slack Phase-96 minus channels"
    - "Cooldown via shared DynamoDB nonces table (gh-router-cooldown: key prefix)"
    - "Lambda-safe bounded PostComment under 5s context"
key_files:
  created:
    - pkg/github/bridge/orphan_reply.go
    - pkg/github/bridge/webhook_handler_phase101_test.go
  modified:
    - pkg/github/bridge/webhook_handler.go
    - cmd/km-github-bridge/main.go
decisions:
  - "jsonClaim helper returns WebhookResponse{StatusCode:200, Body:string(b)} — WebhookResponse has no Headers field, so Content-Type is not set; GitHub ignores response headers"
  - "orphan_reply.go implements all 4 gates in explicit order to match test assertions: DefaultRouter → ContainsMention → Commenter/InstallID → cooldown"
  - "Tasks 2 and 3 implemented in one compilation pass since maybePostGitHubOrphanComment must exist for webhook_handler.go to compile"
metrics:
  duration: 239s
  completed: "2026-06-08"
  tasks_completed: 4
  files_changed: 4
---

# Phase 101 Plan 03: Claim-Aware Scatter-Gather + Orphan Reply Summary

Wired the claim-aware machinery into the GitHub bridge handler and Lambda entrypoint: peer-side `{"claimed":bool}` JSON emit, front-door tally, `maybePostGitHubOrphanComment` with four gates, per-(repo,number) cooldown, and `KM_GITHUB_DEFAULT_ROUTER` dormancy.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 (RED) | Phase 101 failing tests | f0a238e5 | webhook_handler_phase101_test.go (+) |
| 2 (GREEN) | Peer-side claim emit + struct fields + jsonClaim | 1cfa41b5 | webhook_handler.go |
| 3 (GREEN) | maybePostGitHubOrphanComment | e666dced | orphan_reply.go (+) |
| 4 | main.go DefaultRouter + OrphanCooldown wiring | e3227c76 | main.go |

## What Was Built

**`pkg/github/bridge/webhook_handler.go` changes:**
- Added `DefaultRouter bool` and `OrphanCooldown DeliveryNonceStore` fields to `WebhookHandler`
- Added `jsonClaim(bool) WebhookResponse` helper (returns `{"claimed":bool}` JSON body)
- Relayed+miss terminal drop now returns `jsonClaim(false)` (was plain `"ok"`)
- Final matched/dispatch return: guarded with `if req.Headers["x-km-relayed"] != ""` → `jsonClaim(true)` before the existing `return WebhookResponse{200, "ok"}` for non-relayed owned
- Front-door `Broadcast` result captured; when `DefaultRouter=true` tallies `anyClaimed`; zero claims → `maybePostGitHubOrphanComment`

**`pkg/github/bridge/orphan_reply.go` (new):**
- `maybePostGitHubOrphanComment` with 4 gates in order:
  1. `h.DefaultRouter` check
  2. `ContainsMention(payload.Comment.Body, botLogin)` re-check (Phase-100 reorder skipped mention filter on !matched)
  3. `h.Commenter != nil && payload.Installation.ID != 0`
  4. `OrphanCooldown.CheckAndStore("gh-router-cooldown:{owner}/{repo}#{number}", 3600)`
- Guidance text names `github.repos:` and `km init`, references `docs/github-bridge.md`
- `PostComment` under `context.WithTimeout(ctx, 5*time.Second)` (Lambda-safe)

**`cmd/km-github-bridge/main.go` changes:**
- After Phase-100 Relayer guard: sets `webhookHandler.DefaultRouter=true` and `webhookHandler.OrphanCooldown=nonceStore` only when `os.Getenv("KM_GITHUB_DEFAULT_ROUTER") == "true"`
- Reuses existing `*DynamoGitHubNonceStore` as the cooldown store (no new table)
- Logs `"km-github-bridge: orphan-repo default router ENABLED (front door)"` on activation

**`pkg/github/bridge/webhook_handler_phase101_test.go` (new, 12 tests):**
- `PeerClaim_*` (3): relayed-miss → `{"claimed":false}`, relayed-owned → `{"claimed":true}`, non-relayed → `"ok"`
- `OrphanComment_*` (5): happy-path (PostComment contains `github.repos:` + `km init`), AnyClaim_NoPost, NonMention_NoPost, CommenterNil_Skip, InstallationIDZero_Skip
- `OrphanCooldown_*` (2): FirstTime_Posts (key `gh-router-cooldown:acme/widgets#42`, ttl 3600), SecondSuppressed
- `DefaultRouterOff_Silent` (1): dormancy — Broadcast fires but no PostComment

## Deviations from Plan

**1. [Rule 1 - Bug] Tasks 2 and 3 compiled together**
- **Found during:** Task 2 GREEN verification
- **Issue:** `webhook_handler.go` calls `h.maybePostGitHubOrphanComment` which must exist in the same package at compile time; running Task 2 verification before Task 3 was written produced a build failure
- **Fix:** Implemented `orphan_reply.go` (Task 3) before running Task 2's verification command; both committed separately as planned
- **Files modified:** orphan_reply.go
- **Commit:** e666dced

## Self-Check

### Files created/modified exist:
- `pkg/github/bridge/webhook_handler.go`: present (modified)
- `pkg/github/bridge/orphan_reply.go`: present (created)
- `pkg/github/bridge/webhook_handler_phase101_test.go`: present (created)
- `cmd/km-github-bridge/main.go`: present (modified)

### Commits exist:
- f0a238e5: RED tests
- 1cfa41b5: webhook_handler.go GREEN
- e666dced: orphan_reply.go GREEN
- e3227c76: main.go wiring

### Verification checks:
- `go test ./pkg/github/bridge/... -run 'PeerClaim|OrphanComment|OrphanCooldown|DefaultRouterOff'`: PASS
- `go test ./pkg/github/bridge/... -count=1`: PASS (all phases 97–101)
- `go build ./cmd/km-github-bridge/...`: PASS
- `grep 'jsonClaim(false)' webhook_handler.go`: FOUND
- `grep 'maybePostGitHubOrphanComment' webhook_handler.go`: FOUND
- `grep 'gh-router-cooldown:' orphan_reply.go`: FOUND
- `! grep RunningChannel|SandboxChannelInfo orphan_reply.go main.go`: CONFIRMED (no channel machinery)

## Self-Check: PASSED
