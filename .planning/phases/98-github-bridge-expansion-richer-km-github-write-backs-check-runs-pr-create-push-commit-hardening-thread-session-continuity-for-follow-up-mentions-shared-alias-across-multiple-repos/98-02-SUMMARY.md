---
phase: 98-github-bridge-expansion
plan: 02
subsystem: github-bridge / compiler
tags: [github, thread-continuity, session-resume, dynamodb, iam, poller, tdd]
dependency_graph:
  requires: [98-00, 98-01]
  provides: [GH-X-CONTINUITY, GH-X-THREADBYPASS]
  affects: [pkg/github/bridge, cmd/km-github-bridge, pkg/compiler, infra/modules/lambda-github-bridge]
tech_stack:
  added: [DynamoGitHubThreadStore, GitHubThreadStore interface, DynamoGitHubThreadClient interface]
  patterns: [conditional-PutItem (attribute_not_exists), DynamoDB UpdateItem SET, thread-bypass short-circuit, Slack thread-bypass mirror]
key_files:
  created:
    - pkg/github/bridge/webhook_handler_phase98_04_test.go
    - infra/modules/lambda-github-bridge/v1.1.0/main.tf
    - infra/modules/lambda-github-bridge/v1.1.0/variables.tf
    - infra/modules/lambda-github-bridge/v1.1.0/outputs.tf
  modified:
    - pkg/github/bridge/interfaces.go
    - pkg/github/bridge/aws_adapters.go
    - pkg/github/bridge/thread_store_test.go
    - pkg/github/bridge/webhook_handler.go
    - pkg/github/bridge/webhook_handler_phase98_test.go
    - cmd/km-github-bridge/main.go
    - infra/live/use1/lambda-github-bridge/terragrunt.hcl
    - pkg/compiler/userdata.go
    - pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh
decisions:
  - "DynamoGitHubThreadClient: separate interface (superset of DynamoQueryPutter + UpdateItem) rather than widening DynamoQueryPutter, to avoid forcing all existing fakes to implement UpdateItem"
  - "Thread-bypass insert before step-5 mention check (not after), mirroring Phase 91.3 Slack events_handler.go:311-339"
  - "Upsert only on successful SQS send (not on cold-create path) — cold create is handled by 98-04 resume logic"
  - "IAM policy gated on github_threads_table_arn != '' for backward compat with pre-98-00 installs"
  - "Regenerated pre-Phase-92 userdata golden baseline — learn.v2 has GitHubInboundEnabled:true so poller session-write content changes affected it"
  - "Kept TestHandle_AutoResume behind phase98_wave3 build tag (98-04 implements SandboxResumer + ResolveByAliasWithStatus)"
metrics:
  duration: "540s"
  completed_date: "2026-06-07"
  tasks_completed: 3
  tasks_total: 3
  files_modified: 9
  files_created: 4
---

# Phase 98 Plan 02: Thread/Session Continuity Summary

**One-liner:** DynamoGitHubThreadStore adapter + Handle() thread-bypass short-circuit + Upsert on warm dispatch + poller session read/write backed by km-github-threads, enabling follow-up PR replies without re-@-mention and session continuity across turns.

## What Was Built

### Task 1: DynamoGitHubThreadStore adapter (GREEN TestGitHubThreadStore)

Added `GitHubThreadStore` interface and `DynamoGitHubThreadClient` (DynamoQueryPutter + UpdateItem) to `interfaces.go`. Implemented `DynamoGitHubThreadStore` in `aws_adapters.go` with:

- `Upsert`: conditional `PutItem` with `attribute_not_exists(repo)` — swallows `ConditionalCheckFailedException` (idempotent, never overwrites existing session)
- `LookupSandbox`: `GetItem` returning `(sandbox_id, agent_session_id)` — returns `("","",nil)` for absent rows (first dispatch)
- `UpdateSession`: `UpdateItem SET agent_session_id = :sid`

Removed `//go:build phase98_wave0` tag from `thread_store_test.go`. All 4 `TestGitHubThreadStore_*` tests pass.

### Task 2: Thread-bypass + Upsert wiring in Handle()

Added `Threads GitHubThreadStore` field to `WebhookHandler`. Inserted step-4b before step-5 mention filter:

```
// Step 4b: known-thread bypass
if h.Threads != nil {
    if sid, _, err := h.Threads.LookupSandbox(ctx, repo, number); err == nil && sid != "" {
        threadKnown = true
    }
}
// Step 5: if !mention && !threadKnown { 200-drop }
```

Added `Upsert` call in warm dispatch path after successful SQS send (non-fatal on error). `Threads == nil` → Phase 97 behavior (backward compatible).

Split `webhook_handler_phase98_test.go` into two files:
- `webhook_handler_phase98_test.go` — `TestHandle_ThreadBypass` (GREEN, no build tag)
- `webhook_handler_phase98_04_test.go` — `TestHandle_AutoResume` (behind `phase98_wave3` until 98-04)

Updated `cmd/km-github-bridge/main.go` to construct `DynamoGitHubThreadStore` from `KM_GITHUB_THREADS_TABLE` (default: `{prefix}-github-threads`) and assign to `WebhookHandler.Threads`.

### Task 3: Bridge IAM v1.1.0 + poller session write

**IAM module (v1.1.0):** Copied v1.0.0 → v1.1.0. Added:
- `aws_iam_role_policy.dynamodb_github_threads`: `GetItem/PutItem/UpdateItem` on km-github-threads (count-gated on `github_threads_table_arn != ""`; backward compat)
- `KM_GITHUB_THREADS_TABLE` env var in Lambda environment block
- New variables: `github_threads_table_name`, `github_threads_table_arn`

**Live unit:** Updated `infra/live/use1/lambda-github-bridge/terragrunt.hcl` to source v1.1.0, added `dependency.github_threads` block (mocks for plan/show), passed `github_threads_table_name` + `github_threads_table_arn` inputs.

**Poller session write (`pkg/compiler/userdata.go`):** In the `km-github-inbound-poller` bash script:
- On each message: DDB GetItem for `(repo, number)` → `agent_session_id` → `GITHUB_SESSION`
- Pass `--resume $GITHUB_SESSION` to claude when non-empty (codex path: no resume, session_id not exposed via `--resume`)
- After successful run: extract `.session_id` (claude) or `thread.started.thread_id` (codex) → `aws dynamodb update-item SET agent_session_id`

**Baseline update:** Regenerated `userdata_learn_v2_pre92_baseline.golden.sh` — learn.v2.yaml has `notification.github.inbound.enabled: true` so poller changes affected its rendered output. Both `TestUserdataLearnV2Phase92ByteIdentity` and `TestUserdataKmPrefixByteIdentity` pass.

## Continuity Data Lives in km-github-threads Only

Per the CLAUDE.md memory entry (SandboxMetadata lossy round-trip): NO new attributes were added to `km-sandboxes` / `metadata.go`. Continuity lives exclusively in `km-github-threads`. The `aws/metadata.go` and `aws/dynamodb.go` files are unchanged.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Byte-identity golden stale after poller session write**
- **Found during:** Task 3
- **Issue:** `TestUserdataLearnV2Phase92ByteIdentity` and `TestUserdataKmPrefixByteIdentity` failed because `learn.v2.yaml` has `GitHubInboundEnabled: true`, so the poller session-write additions changed the generated userdata
- **Fix:** Regenerated golden via `CAPTURE_PRE92_BASELINE=1 go test ./pkg/compiler/ -run TestCapturePre92Userdata`
- **Files modified:** `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh`
- **Commit:** ff8160b7

## Verification Results

```
go test ./pkg/github/bridge/... -run 'TestGitHubThreadStore|TestHandle_ThreadBypass' -count=1  → PASS
go test ./pkg/compiler/... -count=1                                                             → PASS
go build ./...                                                                                  → PASS
grep km-github-threads infra/modules/lambda-github-bridge/v1.1.0/main.tf                       → FOUND
grep -iE "km-github-threads|agent_session_id" pkg/compiler/userdata.go                         → FOUND
grep v1.1.0 infra/live/use1/lambda-github-bridge/terragrunt.hcl                                → FOUND
```

## Self-Check: PASSED

- `/Users/khundeck/working/klankrmkr/pkg/github/bridge/interfaces.go` — GitHubThreadStore interface added
- `/Users/khundeck/working/klankrmkr/pkg/github/bridge/aws_adapters.go` — DynamoGitHubThreadStore implemented
- `/Users/khundeck/working/klankrmkr/pkg/github/bridge/webhook_handler.go` — Threads field + step-4b bypass
- `/Users/khundeck/working/klankrmkr/cmd/km-github-bridge/main.go` — DynamoGitHubThreadStore wired
- `/Users/khundeck/working/klankrmkr/infra/modules/lambda-github-bridge/v1.1.0/main.tf` — km-github-threads IAM
- `/Users/khundeck/working/klankrmkr/infra/live/use1/lambda-github-bridge/terragrunt.hcl` — sources v1.1.0
- Commits: 9f87e23f, 8f16130f, ff8160b7
