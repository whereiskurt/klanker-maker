---
phase: 102-github-bridge-agent-verbs-claude-and-codex-select-the-per-thread-agent-in-a-pr-comment-slack-phase-70-analog
plan: "02"
subsystem: github-bridge
tags: [github, agent-type, dynamodb, thread-store, tdd]
dependency_graph:
  requires: []
  provides: [agent_type-persisted-in-km-github-threads]
  affects: [pkg/github/bridge/interfaces.go, pkg/github/bridge/aws_adapters.go, pkg/github/bridge/webhook_handler.go]
tech_stack:
  added: []
  patterns: [schema-on-write DynamoDB attribute, UpdateItem-only mutation]
key_files:
  created: []
  modified:
    - pkg/github/bridge/interfaces.go
    - pkg/github/bridge/aws_adapters.go
    - pkg/github/bridge/webhook_handler.go
    - pkg/github/bridge/webhook_handler_phase98_test.go
    - pkg/github/bridge/thread_store_test.go
decisions:
  - "InvalidateStaleSession keeps agent_type on stale rows (does not REMOVE it) — agent binding persists across sandbox recreations; next turn re-binds to the stored agent_type"
  - "agent_type is schema-on-write: old rows return agentType='' (profile default); no Terraform change, no migration"
  - "UpdateSession writes agentType as empty string when '' passed (valid), so the attribute exists for future reads — downstream treats '' as profile default"
metrics:
  duration: 324s
  completed: "2026-06-08"
---

# Phase 102 Plan 02: Thread Store agent_type Persistence Summary

One-liner: Extended `km-github-threads` DDB store to read/write `agent_type` via projection + UpdateItem — schema-on-write, no Terraform change, old rows return `""`.

## What Was Built

Extended the `GitHubThreadStore` interface and `DynamoGitHubThreadStore` implementation to carry `agent_type` as a fourth return value from `LookupSandbox` and a fifth parameter to `UpdateSession`. This is the persistence half of Phase 102: a PR/issue thread can now remember whether it dispatches to `claude` or `codex`.

**Storage contract:**
- `agent_type` lives ONLY in `km-github-threads` — never in `km-sandboxes` (avoids SandboxMetadata lossy round-trip footgun).
- Written via `SET agent_session_id = :sid, agent_type = :at` in a single `UpdateItem`.
- Read via `ProjectionExpression: "sandbox_id, agent_session_id, agent_type"`.
- Absent attribute → `""` (pre-Phase-102 rows, treated as profile default downstream).
- `InvalidateStaleSession` preserves `agent_type` (only REMOVEs `agent_session_id`) — agent binding survives sandbox recreation.

## Tasks Completed

| Task | Description | Commit |
|------|-------------|--------|
| 1 (RED) | Failing tests: agent_type LookupSandbox 4-return + UpdateSession 5-arg | fde9bb84 |
| 2 (GREEN) | Extend interfaces.go, aws_adapters.go, fix webhook_handler.go + phase98 mock | 1a77c6c5 |
| 3 | Full-package regression + InvalidateStaleSession decision documented | (no code change) |

## Verification Results

- `go build ./...`: CLEAN
- `TestGitHubThreadStore_Upsert`: PASS
- `TestGitHubThreadStore_LookupSandbox_Found` (new: agentType="codex"): PASS
- `TestGitHubThreadStore_LookupSandbox_NoAgentType` (new: back-compat ""): PASS
- `TestGitHubThreadStore_LookupSandbox_NotFound` (updated: 4-return): PASS
- `TestGitHubThreadStore_UpdateSession` (updated: agent_type in UpdateItem): PASS
- `TestHandle_ThreadBypass` (Phase 98): PASS
- `TestHandle_StaleSession_Invalidated` (Phase 98 Gap E): PASS
- `TestHandle_SameBoxSession_NotInvalidated` (Phase 98 Gap E): PASS

Pre-existing Plan 01 RED failures: `TestHandle_AgentVerbConflict`, `TestHandle_EnvelopeCarriesAgent`, `TestCommandParse//claude_AND_/codex_→_AgentVerbConflict=true` — Plan 01 GREEN responsibility, not Plan 02.

## Decisions Made

1. **`InvalidateStaleSession` keeps `agent_type`:** The stale-session invalidation removes `agent_session_id` but does NOT remove `agent_type`. If a PR was bound to `codex` and the sandbox is recreated, the next turn still dispatches to `codex`. This is acceptable (RESEARCH D3): the PR conversation context dictates the agent choice; re-prompting to confirm agent preference on every box recreate is worse UX than inheriting the prior choice.

2. **Schema-on-write, no migration:** The `agent_type` DDB attribute is written on new turns only. Pre-Phase-102 rows return `agentType=""`. Callers treat `""` as "use profile default". No Terraform change, no migration job.

3. **`UpdateSession` writes `""` as a valid `agent_type`:** When called with `agentType=""`, an empty string is written (the attribute exists). This is intentional — downstream interprets `""` as "profile default", and future reads see the attribute rather than getting it from `item["agent_type"]` absent.

## Deviations from Plan

None — plan executed exactly as written. Pre-existing Plan 01 RED test failures are expected (Plan 02 has no dependency on Plan 01) and are tracked in Plan 01's scope.

## Self-Check

- `pkg/github/bridge/interfaces.go` — FOUND (modified)
- `pkg/github/bridge/aws_adapters.go` — FOUND (modified)
- `pkg/github/bridge/webhook_handler.go` — FOUND (modified)
- `pkg/github/bridge/thread_store_test.go` — FOUND (modified)
- Commit fde9bb84 — FOUND
- Commit 1a77c6c5 — FOUND

## Self-Check: PASSED
