---
phase: 52-clone-sandbox
plan: "01"
subsystem: aws-metadata
tags: [dynamo, metadata, list, clone]
dependency_graph:
  requires: []
  provides: [ClonedFrom-field, cloned-from-dynamo-marshal, km-list-wide-cloned-from]
  affects: [pkg/aws/metadata.go, pkg/aws/sandbox.go, pkg/aws/sandbox_dynamo.go, internal/app/cmd/list.go]
tech_stack:
  added: []
  patterns: [manual-dynamo-marshal, omit-when-empty-pattern]
key_files:
  created: []
  modified:
    - pkg/aws/metadata.go
    - pkg/aws/sandbox.go
    - pkg/aws/sandbox_dynamo.go
    - pkg/aws/sandbox_dynamo_test.go
    - internal/app/cmd/list.go
decisions:
  - "Follow alias omit-when-empty pattern for cloned_from in marshalSandboxItem to prevent item pollution"
  - "ClonedFrom added to SandboxRecord (not just SandboxMetadata) so km list --wide can display it without extra reads"
metrics:
  duration: "~4min"
  completed_date: "2026-04-10"
  tasks_completed: 2
  files_modified: 5
---

# Phase 52 Plan 01: ClonedFrom Lineage Field — DynamoDB and List Summary

ClonedFrom field added to SandboxMetadata/SandboxRecord/sandboxItemDynamo with full marshal/unmarshal round-trip through all 4 DynamoDB locations, plus CLONED FROM column in km list --wide output.

## Tasks Completed

| # | Task | Commit | Files |
|---|------|--------|-------|
| 1 | Add ClonedFrom to metadata structs and DynamoDB marshal/unmarshal | 34d0c17 | metadata.go, sandbox.go, sandbox_dynamo.go, sandbox_dynamo_test.go |
| 2 | Add CLONED FROM column to km list --wide | 02771e5 | list.go |

## Decisions Made

1. **Omit-when-empty pattern for cloned_from** — mirrors the alias field pattern; keeps DynamoDB items clean and avoids empty-string attribute pollution
2. **ClonedFrom on SandboxRecord** — propagating through metadataToRecord allows km list to display clone lineage without extra DynamoDB reads at list time

## Deviations from Plan

None — plan executed exactly as written.

## Verification

- `go test ./pkg/aws/ -count=1` — PASS (all 6 new ClonedFrom tests + all existing tests)
- `go vet ./internal/app/cmd/ ./pkg/aws/` — clean
- `go build ./cmd/km/` — succeeds

## Self-Check: PASSED

- pkg/aws/metadata.go — ClonedFrom field present
- pkg/aws/sandbox.go — ClonedFrom field present
- pkg/aws/sandbox_dynamo.go — cloned_from in struct, unmarshal, marshal, toSandboxMetadata, metadataToRecord
- internal/app/cmd/list.go — CLONED FROM in header and row
- pkg/aws/sandbox_dynamo_test.go — 6 TestClonedFrom_* tests
- Commits 34d0c17, 02771e5 verified in git log
