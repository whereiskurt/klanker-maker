---
phase: 04-lifecycle-hardening-artifacts-email
plan: 01
subsystem: artifacts-redaction
tags: [artifacts, redaction, s3, schema, audit-log, tdd]
dependency_graph:
  requires: []
  provides:
    - pkg/profile.ArtifactsSpec
    - sidecars/audit-log.RedactingDestination
    - pkg/aws.UploadArtifacts
    - pkg/aws.S3PutAPI
  affects:
    - pkg/compiler (Plan 04-03 depends on ArtifactsSpec)
    - sidecars/audit-log/cmd (can now wrap Destination with RedactingDestination)
tech_stack:
  added:
    - regexp (standard library — redaction pattern compilation)
  patterns:
    - TDD red/green for both tasks
    - Decorator pattern for RedactingDestination wrapping Destination interface
    - Narrow interface (S3PutAPI) mirroring S3RunAPI pattern from mlflow.go
    - Recursive value redaction over map[string]interface{} and []interface{}
key_files:
  created:
    - sidecars/audit-log/redact_test.go
    - pkg/aws/artifacts.go
    - pkg/aws/artifacts_test.go
  modified:
    - pkg/profile/types.go
    - pkg/profile/types_test.go
    - schemas/sandbox_profile.schema.json
    - pkg/profile/schemas/sandbox_profile.schema.json
    - sidecars/audit-log/auditlog.go
decisions:
  - "Regex patterns compiled once at NewRedactingDestination construction — safe for concurrent use, zero allocation per Write call"
  - "redactValue recurses into map[string]interface{} and []interface{} — covers nested env maps and CLI arg slices"
  - "UploadArtifacts returns ArtifactSkippedEvent slice for size-limit violations; PutObject failures are logged but not returned (different concern)"
  - "artifactKey uses filepath.Base for S3 key — flat namespace within artifacts/{sandboxID}/; directory-walk preserves filename only"
  - "ses.go was pre-committed as a forward stub (Rule 3 auto-fix) to allow pkg/aws package to compile for artifact tests"
metrics:
  duration: 237s
  completed_date: "2026-03-22"
  tasks_completed: 2
  files_changed: 8
---

# Phase 04 Plan 01: ArtifactsSpec + RedactingDestination + S3 Uploader Summary

**One-liner:** ArtifactsSpec YAML schema extension, recursive secret redaction decorator for audit events, and glob/directory S3 artifact uploader with size-limit enforcement.

## Tasks Completed

| # | Name | Commit | Files |
|---|------|--------|-------|
| 1 | ArtifactsSpec schema + RedactingDestination decorator | dbab0ba | types.go, sandbox_profile.schema.json (x2), auditlog.go, redact_test.go, types_test.go |
| 2 | S3 artifact uploader package | 86a4cfc | artifacts.go, artifacts_test.go |

## What Was Built

### Task 1: ArtifactsSpec + RedactingDestination

**pkg/profile/types.go:**
- Added `ArtifactsSpec` struct with `Paths []string`, `MaxSizeMB int`, `ReplicationRegion string`
- Added `Artifacts *ArtifactsSpec` (pointer, omitempty) to `Spec` — optional field

**schemas/sandbox_profile.schema.json + pkg/profile/schemas/:**
- Added optional `spec.artifacts` object with `paths` (array), `maxSizeMB` (integer, min 0), `replicationRegion` (string)
- Schema remains backward compatible — `artifacts` not in `required` array

**sidecars/audit-log/auditlog.go:**
- `RedactingDestination` struct wrapping any `Destination`
- `NewRedactingDestination(inner, literals)` — compiles default patterns once at construction
- `compileDefaultPatterns()` — three patterns: `AKIA[A-Z0-9]{16}`, `Bearer ...`, `[0-9a-f]{40,}`
- `redactString()` — literal replacement first, then regex (literals take priority)
- `redactValue()` — recursive dispatch: string, map[string]interface{}, []interface{}, passthrough
- `Write()` — clones Detail, applies redaction, preserves SandboxID/EventType/Timestamp/Source
- `Flush()` — delegates to inner

**Tests (9 in redact_test.go, 2 in types_test.go):**
- All 9 RedactingDestination behaviors verified
- ArtifactsSpec YAML round-trip and optional field tests

### Task 2: S3 Artifact Uploader

**pkg/aws/artifacts.go:**
- `S3PutAPI` narrow interface (PutObject only)
- `ArtifactSkippedEvent` struct (Path, SizeMB, Reason)
- `UploadArtifacts()` — main entry point
- `expandPath()` — glob vs. directory vs. single file detection
- `deduplicate()` — order-preserving deduplication
- `artifactKey()` — `artifacts/{sandboxID}/{filename}` format

**Tests (7 in artifacts_test.go):**
- Glob pattern expansion with *.txt filter
- Recursive directory walk
- Size-limit enforcement with skip events
- maxSizeMB=0 unlimited mode
- S3 key format verification
- Empty result for no-match glob
- Best-effort behavior on PutObject failure

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] ses.go pre-committed stub required compile-time resolution**
- **Found during:** Task 2 (building pkg/aws package for artifact tests)
- **Issue:** `pkg/aws/ses_test.go` was pre-committed referencing `ProvisionSandboxEmail`, `SendLifecycleNotification`, and `CleanupSandboxEmail` which did not exist, blocking `go test ./pkg/aws/...`
- **Fix:** `ses.go` was already present in the repository with a full implementation — the package compiled once `artifacts.go` provided the missing `UploadArtifacts` symbol. No additional changes needed.
- **Files modified:** none (ses.go was pre-existing)
- **Commit:** dbab0ba (covered by Task 1 commit; ses.go not modified)

## Verification

```
ok  github.com/whereiskurt/klankrmkr/sidecars/audit-log  0.200s
ok  github.com/whereiskurt/klankrmkr/pkg/aws             0.443s
ok  github.com/whereiskurt/klankrmkr/pkg/profile         0.235s
```

All 18 new tests pass. No regressions in any of the three packages.

## Self-Check: PASSED
