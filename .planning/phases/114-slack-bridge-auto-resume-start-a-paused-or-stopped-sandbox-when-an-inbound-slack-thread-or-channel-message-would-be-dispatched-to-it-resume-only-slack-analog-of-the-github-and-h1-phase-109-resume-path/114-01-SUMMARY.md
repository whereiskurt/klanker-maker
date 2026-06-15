---
phase: 114-slack-bridge-auto-resume
plan: "01"
subsystem: pkg/slack/bridge
tags: [ec2-resume, dynamodb, slack-bridge, phase-114, interfaces]
dependency_graph:
  requires: []
  provides:
    - SandboxResumer interface (pkg/slack/bridge)
    - SandboxStatusWriter interface (pkg/slack/bridge, no DeleteSandboxRow)
    - EC2StartAPI interface (pkg/slack/bridge)
    - ErrNoResumableInstance sentinel (pkg/slack/bridge)
    - EC2Resumer struct with km:sandbox-id tag key (Phase-109 fix locked by test)
    - DynamoSandboxStatusWriter using DDBUpdateItemAPI (UpdateItem-only)
  affects:
    - pkg/slack/bridge (additive, no existing code changed)
tech_stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/ec2 (new import to pkg/slack/bridge)
    - github.com/aws/aws-sdk-go-v2/service/ec2/types (ec2types alias)
  patterns:
    - Near-verbatim port of pkg/github/bridge EC2Resumer + DynamoSandboxStatusWriter
    - Slack-specific narrowing: SandboxStatusWriter has no DeleteSandboxRow (no cold-create)
    - DDBUpdateItemAPI reused (vs GitHub's DynamoUpdateItemClient) — avoids Pitfall 6
    - stopping-poll loop (2s interval, 8s timeout) ported verbatim for pause→@-mention race
key_files:
  modified:
    - pkg/slack/bridge/events_interfaces.go
    - pkg/slack/bridge/aws_adapters.go
  created:
    - pkg/slack/bridge/aws_adapters_resume_test.go
decisions:
  - "Reuse DDBUpdateItemAPI (DDBQueryGetPutAPI + UpdateItem) for DynamoSandboxStatusWriter.Client instead of a new narrow interface — avoids RESEARCH.md Pitfall 6 and keeps the mock surface the same as existing pause-hinter tests"
  - "SandboxStatusWriter interface has no DeleteSandboxRow — slack bridge has no cold-create publisher (locked design from 114-CONTEXT.md)"
  - "sandboxIDTagKey() hardcodes km:sandbox-id — Phase-109 e6b9ca75/d8007920 fix; ResourcePrefix retained on struct but inert"
  - "Five tests in bridge_test package (external) to match existing aws_adapters_test.go convention; PutItem assertion ensures lossy-round-trip footgun is impossible"
metrics:
  duration: "215s"
  completed_date: "2026-06-15"
  tasks_completed: 2
  files_modified: 2
  files_created: 1
---

# Phase 114 Plan 01: Slack Bridge EC2 Resume Primitives Summary

**One-liner:** Near-verbatim port of GitHub bridge EC2Resumer + DynamoSandboxStatusWriter into pkg/slack/bridge with Slack-specific narrowing (no DeleteSandboxRow, DDBUpdateItemAPI client) and five unit tests locking the Phase-109 tag-key fix and UpdateItem-only status flip.

## What Was Built

### Task 1: Interfaces + Adapters (ae769f25)

**`pkg/slack/bridge/events_interfaces.go`** — two new interfaces appended:
- `SandboxResumer`: `StartSandbox(ctx, sandboxID) error` — starts a stopped/paused EC2 instance
- `SandboxStatusWriter`: `SetStatusRunning(ctx, sandboxID) error` — flips DDB status; no `DeleteSandboxRow` (Slack has no cold-create path)

**`pkg/slack/bridge/aws_adapters.go`** — three additions:
- `EC2StartAPI` interface (DescribeInstances + StartInstances)
- `ErrNoResumableInstance` sentinel: `"slack-bridge: no resumable EC2 instance"` (wrapped only on terminal zero-instances path; transient AWS errors returned plain)
- `EC2Resumer` struct: `sandboxIDTagKey()` hardcodes `"km:sandbox-id"` ignoring `ResourcePrefix` (Phase-109 fix); `StartSandbox()` filters `stopped+stopping`, polls stopping→stopped for ≤8s, then calls `StartInstances`
- `DynamoSandboxStatusWriter` struct: client typed `DDBUpdateItemAPI` (existing slack-bridge interface), `SetStatusRunning()` uses `UpdateItem` with `SET #st = :running` (never `PutItem`)

### Task 2: Unit Tests (3c933b80)

**`pkg/slack/bridge/aws_adapters_resume_test.go`** — five tests in `bridge_test` package:

| Test | Asserts |
|------|---------|
| `TestEC2Resumer_UsesKmSandboxIdTag` | Tag filter Name = `"tag:km:sandbox-id"` when `ResourcePrefix="sec"` (Phase-109 lock) |
| `TestEC2Resumer_NoInstances_ReturnsErrNoResumable` | `errors.Is(err, ErrNoResumableInstance)=true`; `StartInstances` NOT called |
| `TestEC2Resumer_StoppedInstance_StartsAndReturnsNil` | `StartInstances` called with instance ID; returns nil |
| `TestEC2Resumer_TransientDescribeError_ReturnsPlainError` | `errors.Is(err, ErrNoResumableInstance)=false`; sentinel NOT applied to transient errors |
| `TestDynamoSandboxStatusWriter_UsesUpdateItem` | `UpdateItem` with `SET #st = :running` + correct key/aliases; `PutItem` NEVER called |

## Deviations from Plan

None — plan executed exactly as written.

## Verification

```
go build ./pkg/slack/bridge/...  → ok (no output)
go vet ./pkg/slack/bridge/...    → ok (no output)
go test ./pkg/slack/bridge/...   → ok (5.35s, all tests pass including 5 new)
```

## Self-Check: PASSED

- FOUND: pkg/slack/bridge/events_interfaces.go
- FOUND: pkg/slack/bridge/aws_adapters.go
- FOUND: pkg/slack/bridge/aws_adapters_resume_test.go
- FOUND commit: ae769f25 (Task 1 — interfaces + adapters)
- FOUND commit: 3c933b80 (Task 2 — unit tests)
