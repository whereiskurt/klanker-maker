---
phase: 77-failed-sandbox-discoverability-persist-failure-reason-in-ddb-and-km-logs-lambda-fallback
plan: 1
subsystem: aws-dynamo
tags: [dynamodb, schema, sandbox-metadata, failure-discoverability, go-testing, tdd]

# Dependency graph
requires:
  - 77-00
provides:
  - SandboxMetadata.FailureReason (string, json:"failure_reason,omitempty")
  - SandboxMetadata.FailedAt (*time.Time, json:"failed_at,omitempty")
  - SandboxRecord.FailureReason (string, json:"failure_reason,omitempty")
  - SandboxRecord.FailedAt (*time.Time, json:"failed_at,omitempty")
  - UpdateSandboxStatusAndReasonDynamo(ctx, client, table, id, status, reason, failedAt)
  - unmarshalFailureFields helper called from all three read-path call sites
  - marshalSandboxItem includes failure fields for full PutItem persistence
  - metadataToRecord copies both failure fields into SandboxRecord
affects: [77-02, 77-03, 77-04]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Additive struct fields with omitempty — no migration required"
    - "unmarshalXxxFields pattern: separate helper called after toSandboxMetadata(), matching unmarshalSlackFields"
    - "Single UpdateItem SET expression for multi-field atomic status transition"
    - "RFC3339 string storage for timestamps (consistent with created_at, locked_at, expires_at project convention)"

key-files:
  created: []
  modified:
    - pkg/aws/metadata.go
    - pkg/aws/sandbox.go
    - pkg/aws/sandbox_dynamo.go
    - pkg/aws/sandbox_dynamo_test.go

key-decisions:
  - "failure_reason and failed_at stored as RFC3339 String attributes (not Number epoch) — consistent with locked_at, expires_at convention"
  - "unmarshalFailureFields is a separate function mirroring unmarshalSlackFields, not inlined in toSandboxMetadata() — keeps the unmarshal pattern consistent"
  - "marshalSandboxItem includes failure fields (omit when empty) so WriteSandboxMetadataDynamo (full PutItem replace) does not silently drop them on read-modify-write paths — same rationale as SlackInboundQueueURL fix"
  - "UpdateSandboxStatusAndReasonDynamo does NOT replace UpdateSandboxStatusDynamo — both coexist; Wave 2 plans wire the new helper into the create-handler failure branch"

requirements-completed: [SCHM-77, HELP-77]

# Metrics
duration: 3min
completed: 2026-05-15
---

# Phase 77 Plan 01: DynamoDB Schema + Writer/Reader Plumbing Summary

**FailureReason/FailedAt fields added to SandboxMetadata + SandboxRecord; UpdateSandboxStatusAndReasonDynamo helper and unmarshalFailureFields read-path propagation shipped with four passing TDD tests**

## Performance

- **Duration:** 3 min
- **Started:** 2026-05-15T00:04:57Z
- **Completed:** 2026-05-15T00:08:06Z
- **Tasks:** 2
- **Files modified:** 4

## Accomplishments

- Added `FailureReason string` (json:"failure_reason,omitempty") and `FailedAt *time.Time` (json:"failed_at,omitempty") to both `SandboxMetadata` (metadata.go) and `SandboxRecord` (sandbox.go)
- Implemented `UpdateSandboxStatusAndReasonDynamo` helper with locked signature: single UpdateItem SET expression `SET #s = :status, failure_reason = :reason, failed_at = :ts`
- Added `unmarshalFailureFields(item, meta)` helper following the `unmarshalSlackFields` pattern; wired into all three read-path call sites (ReadSandboxMetadataDynamo, ListAllSandboxesByDynamo, ListAllSandboxMetadataDynamo)
- Updated `marshalSandboxItem` to include failure fields in PutItem writes (prevents silent drop on read-modify-write paths)
- Updated `metadataToRecord` to copy FailureReason and FailedAt into SandboxRecord
- Shipped four passing TDD tests proving the full contract

## Task Commits

1. **Task 1: Add FailureReason/FailedAt to SandboxMetadata and SandboxRecord** — `356b115` (feat)
2. **Task 2 RED: Failing tests** — `3e959e9` (test)
3. **Task 2 GREEN: Implementation** — `638ecf3` (feat)

## Files Created/Modified

- `pkg/aws/metadata.go` — Added FailureReason and FailedAt to SandboxMetadata struct
- `pkg/aws/sandbox.go` — Added FailureReason and FailedAt to SandboxRecord struct
- `pkg/aws/sandbox_dynamo.go` — UpdateSandboxStatusAndReasonDynamo helper; unmarshalFailureFields; marshalSandboxItem failure fields; metadataToRecord field copies; unmarshalFailureFields wired at 3 call sites
- `pkg/aws/sandbox_dynamo_test.go` — Four new TDD tests

## New Helper Signature and SET Expression

```go
func UpdateSandboxStatusAndReasonDynamo(
    ctx context.Context,
    client SandboxMetadataAPI,
    tableName, sandboxID, status, reason string,
    failedAt time.Time,
) error
```

**SET expression:** `SET #s = :status, failure_reason = :reason, failed_at = :ts`
- `#s` is an alias for `status` (DynamoDB reserved word)
- `failure_reason` and `failed_at` are NOT reserved words — no alias needed

## unmarshalFailureFields Location and Call Sites

**Declaration:** `pkg/aws/sandbox_dynamo.go` after `unmarshalSlackFields` (line ~263)

**Call sites (all immediately after unmarshalSlackFields):**
1. `ReadSandboxMetadataDynamo` — line ~407
2. `ListAllSandboxesByDynamo` (inside scan loop) — line ~472
3. `ListAllSandboxMetadataDynamo` (inside scan loop) — line ~515

## TestUpdateSandboxStatusAndReasonDynamo_RoundTrip Assertions (for Wave 2 reference)

```go
// 1. UpdateExpression contains both attribute names
contains(expr, "failure_reason")  // true
contains(expr, "failed_at")       // true

// 2. :reason expression attribute value equals input reason
mock.updateItemInput.ExpressionAttributeValues[":reason"].(*AttributeValueMemberS).Value == "Error: x"

// 3. :ts is non-empty RFC3339-parseable and equals failedAt input
time.Parse(time.RFC3339, tsv.Value) == failedAt

// 4. ReadSandboxMetadataDynamo populates both fields from DDB item
got.FailureReason == "Error: x"
got.FailedAt != nil && got.FailedAt.Equal(failedAt)
```

## Decisions Made

- RFC3339 String storage for `failed_at` (consistent with all other timestamp attributes in this table: `locked_at`, `expires_at`, `created_at`)
- `unmarshalFailureFields` as a named helper (not inlined) — matches established `unmarshalSlackFields` pattern, keeps diff reviewable
- `marshalSandboxItem` updated to include failure fields — prevents silent field drop on read-modify-write paths (same rationale as the Phase 67 `SlackInboundQueueURL` fix documented in `TestWriteSandboxMetadataDynamo_PersistsSlackInboundQueueURL`)
- `UpdateSandboxStatusDynamo` left byte-for-byte unchanged — both helpers coexist; Wave 2 plans wire the new one into the create-handler failure branch

## Deviations from Plan

None - plan executed exactly as written.

## Self-Check: PASSED

All files exist. All commits present (356b115, 3e959e9, 638ecf3).

## Deferred Items

- Pre-existing vet warning in `sidecars/http-proxy/httpproxy/transparent.go:204` (IPv6 format string) — out of scope, logged to `deferred-items.md`
