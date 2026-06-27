---
phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
plan: "04"
subsystem: pkg/aws
tags: [dynamo, metadata, freeze-quarantine, action-quota, round-trip]
dependency_graph:
  requires: [121-01]
  provides: [freeze-quarantine-persistence-substrate, action-limits-round-trip]
  affects: [pkg/aws/metadata.go, pkg/aws/sandbox.go, pkg/aws/sandbox_dynamo.go]
tech_stack:
  added: []
  patterns: [atomic-UpdateItem-with-condition-guard, omitempty-marshal-pattern, unmarshal-helper-pattern]
key_files:
  created: []
  modified:
    - pkg/aws/metadata.go
    - pkg/aws/sandbox.go
    - pkg/aws/sandbox_dynamo.go
    - pkg/aws/sandbox_dynamo_test.go
decisions:
  - "FreezeSandboxDynamo uses no frozen-state guard in ConditionExpression — idempotent re-freeze updates reason/timestamp (matches plan spec; operator intent: auto-freeze can refresh reason on repeated violations)"
  - "unmarshalFrozenFields is a defensive no-op pass (primary parsing is in unmarshalSandboxItem + toSandboxMetadata); exists as the canonical call-site annotation mirror for unmarshalSlackFields/unmarshalGitHubFields"
  - "ActionFrozen emitted as BOOL (not string) matching locked/locked_at style; FrozenAt as RFC3339 S matching locked_at style"
metrics:
  duration: 288s
  completed_date: "2026-06-27"
  tasks_completed: 2
  files_modified: 4
---

# Phase 121 Plan 04: Phase 121 DynamoDB persistence substrate for freeze-quarantine Summary

Thread the five new `km-sandboxes` attrs through the full marshal/unmarshal round-trip and add atomic `FreezeSandboxDynamo`/`UnfreezeSandboxDynamo` writers so quota quarantine state survives full-row PutItem paths (resume/extend/ttl-handler).

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Add five attrs to SandboxMetadata/SandboxRecord + thread round-trip | d049e422 | metadata.go, sandbox.go, sandbox_dynamo.go |
| 2 | FreezeSandboxDynamo + UnfreezeSandboxDynamo + tests | 0decb5ce | sandbox_dynamo.go, sandbox_dynamo_test.go |

## What Was Built

**Task 1 — Round-trip substrate (META-01/02):**

- Added `ActionLimits string`, `ActionFrozen bool`, `FrozenReason string`, `FrozenAt *time.Time`, `FrozenBy string` to `SandboxMetadata` (metadata.go)
- Added `ActionFrozen bool` to `SandboxRecord` (sandbox.go) for `km list` rendering
- Added corresponding fields to `sandboxItemDynamo` struct (sandbox_dynamo.go)
- Threaded through all four round-trip sites:
  - `marshalSandboxItem`: emits all five attrs with omitempty-style conditionals (no false-zero attrs)
  - `unmarshalSandboxItem`: reads all five attrs from raw DDB item
  - `toSandboxMetadata`: assigns all five from `sandboxItemDynamo` struct, parsing `frozen_at` as RFC3339 → `*time.Time`
  - `unmarshalFrozenFields` helper: defensive pass called from `ReadSandboxMetadataDynamo`, `ListAllSandboxesByDynamo`, `ListAllSandboxMetadataDynamo`
- Added `ActionFrozen: meta.ActionFrozen` to `metadataToRecord`

**Task 2 — Atomic freeze/unfreeze writers:**

- `FreezeSandboxDynamo(ctx, client, tableName, sandboxID, reason, by string) error`:
  - `UpdateItem SET action_frozen = :t, frozen_reason = :reason, frozen_at = :now, frozen_by = :by`
  - `ConditionExpression = attribute_exists(sandbox_id)` (no frozen-state guard — idempotent re-freeze)
  - `frozen_at = time.Now().UTC().Format(time.RFC3339)`
  - Wraps `ErrSandboxNotFound` on `ConditionalCheckFailedException`
- `UnfreezeSandboxDynamo(ctx, client, tableName, sandboxID string) error`:
  - `UpdateItem SET action_frozen = :f REMOVE frozen_reason, frozen_at, frozen_by`
  - `ConditionExpression = attribute_exists(sandbox_id)` (idempotent; REMOVE of absent attrs is no-op)
  - Wraps `ErrSandboxNotFound` on `ConditionalCheckFailedException`

**Tests (sandbox_dynamo_test.go):**

- `TestSandboxMetadataRoundTrip` (META-01): all five attrs survive marshal→unmarshal
- `TestMarshalFrozen` (META-02): attrs emitted when set; completely omitted when zero-value
- `TestFreezeSandboxDynamo`: UpdateExpression shape, :t/:reason/:by/:now values, ConditionExpression, ErrSandboxNotFound on missing row
- `TestUnfreezeSandboxDynamo`: REMOVE clause shape, :f value, ConditionExpression, ErrSandboxNotFound on missing row

## Verification

```
go test ./pkg/aws/... -run 'TestSandboxMetadataRoundTrip|TestMarshalFrozen' -count=1  # PASS
go test ./pkg/aws/... -count=1                                                          # PASS (all existing tests green)
grep -q FreezeSandboxDynamo pkg/aws/sandbox_dynamo.go                                  # FOUND
grep -q UnfreezeSandboxDynamo pkg/aws/sandbox_dynamo.go                                # FOUND
```

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- FOUND: pkg/aws/metadata.go
- FOUND: pkg/aws/sandbox.go
- FOUND: pkg/aws/sandbox_dynamo.go
- FOUND: pkg/aws/sandbox_dynamo_test.go
- FOUND: commit d049e422 (Task 1)
- FOUND: commit 0decb5ce (Task 2)
