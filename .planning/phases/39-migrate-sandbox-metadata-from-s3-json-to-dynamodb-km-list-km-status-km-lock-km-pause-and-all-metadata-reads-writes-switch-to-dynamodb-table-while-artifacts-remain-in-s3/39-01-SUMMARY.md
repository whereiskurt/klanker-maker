---
phase: 39-migrate-sandbox-metadata-s3-to-dynamodb
plan: "01"
subsystem: pkg/aws
tags: [dynamodb, metadata, crud, tdd]
dependency_graph:
  requires: []
  provides:
    - SandboxMetadataAPI interface
    - ReadSandboxMetadataDynamo
    - WriteSandboxMetadataDynamo
    - DeleteSandboxMetadataDynamo
    - ListAllSandboxesByDynamo
    - ResolveSandboxAliasDynamo
    - LockSandboxDynamo
    - UnlockSandboxDynamo
    - UpdateSandboxStatusDynamo
  affects:
    - pkg/aws (new file, no breaking changes)
tech_stack:
  added: []
  patterns:
    - narrow-interface DynamoDB client (SandboxMetadataAPI)
    - manual DynamoDB item marshalling (explicit dynamodbav tags, no json fallback)
    - ConditionExpression for atomic lock/unlock (no read-modify-write race)
    - paginated Scan with LastEvaluatedKey
    - GSI alias-index Query for O(1) alias resolution
    - errors.As for ConditionalCheckFailedException detection
key_files:
  created:
    - pkg/aws/sandbox_dynamo.go
    - pkg/aws/sandbox_dynamo_test.go
  modified: []
decisions:
  - Manual item marshalling over attributevalue.MarshalMap to guarantee Number type for ttl_expiry and alias omission when empty
  - metadataToRecord helper added inline (not imported from sandbox.go) to avoid coupling
key_decisions:
  - Manual DynamoDB item marshalling gives deterministic attribute types (ttl_expiry as N, alias omitted when empty)
  - ConditionExpression-based lock uses attribute_not_exists(locked) OR locked = :f to handle first-time lock and unlocked states
metrics:
  duration: 184s
  completed_date: "2026-03-28"
  tasks_completed: 1
  files_created: 2
---

# Phase 39 Plan 01: DynamoDB Sandbox Metadata CRUD Layer Summary

**One-liner:** DynamoDB CRUD layer with atomic locking via ConditionExpression, paginated Scan, GSI alias resolution, and Number-typed TTL attribute.

## What Was Built

Created `pkg/aws/sandbox_dynamo.go` with the complete DynamoDB data access layer for sandbox metadata:

- `SandboxMetadataAPI` interface (GetItem, PutItem, UpdateItem, DeleteItem, Scan, Query)
- `sandboxItemDynamo` internal struct with explicit `dynamodbav` tags — no json tag fallback
- Manual marshalling/unmarshalling to guarantee correct DynamoDB attribute types
- 8 exported functions covering all CRUD operations

Created `pkg/aws/sandbox_dynamo_test.go` with 13 test cases using `mockSandboxMetadataAPI`.

## Tasks

| Task | Name | Status | Commit |
|------|------|--------|--------|
| 1 (RED) | DynamoDB sandbox metadata CRUD tests | COMPLETE | e5d99cd |
| 1 (GREEN) | DynamoDB sandbox metadata CRUD implementation | COMPLETE | 0bb12ba |
| 1 (REFACTOR) | Remove unused import from test | COMPLETE | 1c0b5a2 |

## Exported Functions

| Function | Purpose |
|----------|---------|
| `ReadSandboxMetadataDynamo` | GetItem + returns ErrSandboxNotFound on empty result |
| `WriteSandboxMetadataDynamo` | PutItem with Number TTL + alias omission when empty |
| `DeleteSandboxMetadataDynamo` | DeleteItem by sandbox_id (idempotent) |
| `ListAllSandboxesByDynamo` | Paginated Scan via LastEvaluatedKey |
| `ResolveSandboxAliasDynamo` | Query alias-index GSI for O(1) lookup |
| `LockSandboxDynamo` | Atomic UpdateItem with ConditionExpression |
| `UnlockSandboxDynamo` | Atomic UpdateItem with ConditionExpression |
| `UpdateSandboxStatusDynamo` | Lightweight status-only UpdateItem |

## Key Design Decisions

**Manual marshalling over attributevalue.MarshalMap:** The `attributevalue` package marshals `int64` as Number, but also marshals zero-value int64 as "0" which would create a spurious TTL. Manual construction gives full control over which attributes are included and their exact types.

**alias omission when empty:** An empty string alias would be stored in the GSI projection, causing the alias-index to accumulate garbage rows. The alias attribute is omitted entirely from the PutItem when the field is empty.

**ConditionExpression for locking:** `attribute_exists(sandbox_id) AND (attribute_not_exists(locked) OR locked = :f)` handles three cases atomically: item doesn't exist (fails safely), item exists and was never locked (attribute_not_exists), item exists and was unlocked (locked = false). Uses `errors.As` for ConditionalCheckFailedException per research guidance.

## Test Coverage

13 test cases, all passing:
- ReadSandboxMetadataDynamo: not-found + populated record with TTL
- WriteSandboxMetadataDynamo: TTL as Number type + alias omission
- DeleteSandboxMetadataDynamo: correct key in DeleteItem
- ListAllSandboxesByDynamo: single page + multi-page pagination
- ResolveSandboxAliasDynamo: found + not-found
- LockSandboxDynamo: condition expression + already-locked error
- UnlockSandboxDynamo: condition expression
- UpdateSandboxStatusDynamo: status attribute in ExpressionAttributeValues

## Verification

```
go test ./pkg/aws/... -run "Dynamo" -count=1    # 13/13 PASS
go vet ./pkg/aws/...                             # no errors
make build                                       # km v0.0.69 OK
```

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED
