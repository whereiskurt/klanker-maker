---
phase: 77-failed-sandbox-discoverability-persist-failure-reason-in-ddb-and-km-logs-lambda-fallback
plan: 2
subsystem: create-handler
tags: [dynamodb, failure-discoverability, lambda, go-testing, tdd]

# Dependency graph
requires:
  - 77-00
  - 77-01
provides:
  - extractFailureReason(out string) string — private helper in cmd/create-handler/main.go
  - Failure branch calls UpdateSandboxStatusAndReasonDynamo for both "failed" and "nocap" paths
  - Five extraction tests + four branch tests (nine total new tests)
affects: [77-03, 77-04]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Bottom-up line scan for Error: prefix — returns last match (most actionable root cause)"
    - "Tail-dump fallback prefixed with '<no error line; tail of subprocess output> ' for unstructured output"
    - "1024-char cap applied at the Error: line level (not the tail marker + tail combined)"

key-files:
  created: []
  modified:
    - cmd/create-handler/main.go
    - cmd/create-handler/main_test.go

key-decisions:
  - "Bottom-up scan chosen over compiled regex — simpler, zero dependencies, directly expresses intent (last Error: line wins)"
  - "Empty input returns the noErrorMarker + empty string (documented in test comment) — avoids special-casing"
  - "time imported in main.go; extractFailureReason placed adjacent to execRunCommand as a package-level helper"
  - "UpdateSandboxStatusDynamo removed from failure branch entirely — not left alongside; the new helper subsumes it for both failed+nocap"

requirements-completed: [HAND-77]

# Metrics
duration: 4min
completed: 2026-05-15
---

# Phase 77 Plan 02: Create-Handler Failure Reason Extraction and Persistence Summary

**extractFailureReason helper + wired failure branch: both "failed" and "nocap" status paths now call UpdateSandboxStatusAndReasonDynamo with the extracted reason and timestamp in a single atomic DDB UpdateItem**

## Performance

- **Duration:** 4 min
- **Started:** 2026-05-15T00:09:10Z
- **Completed:** 2026-05-15T00:13:28Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- Implemented `extractFailureReason(out string) string` as a private package-level function in `cmd/create-handler/main.go` (line 497)
- Bottom-up line scan returns the **last** line starting with `"Error:"` (capped at 1024 chars); no-Error-line fallback returns `"<no error line; tail of subprocess output> "` + tail of output
- Replaced `awspkg.UpdateSandboxStatusDynamo` in the failure branch with `awspkg.UpdateSandboxStatusAndReasonDynamo` — applies to both `"failed"` and `"nocap"` paths via the single `failStatus` variable
- Added `time` import to `cmd/create-handler/main.go`
- Five extraction tests + four branch tests, all passing; nine pre-existing tests unchanged

## Task Commits

1. **Task 1: extractFailureReason helper + five extraction tests** — `61cbb91` (feat)
2. **Task 2: wire failure branch to UpdateSandboxStatusAndReasonDynamo + four branch tests** — `a8902a7` (feat)

## Files Created/Modified

- `cmd/create-handler/main.go` — Added `time` import; `extractFailureReason` helper at line 497; failure branch modified at lines 255-271 (replacing `UpdateSandboxStatusDynamo` with `UpdateSandboxStatusAndReasonDynamo`)
- `cmd/create-handler/main_test.go` — Added `errors`, `time`, `dynamodbtypes` imports; five `TestExtractFailureReason_*` tests; four `TestCreateHandler_*` branch tests

## extractFailureReason Algorithm

**Algorithm chosen:** Bottom-up line scan (`strings.Split` on `"\n"`, iterate `len(lines)-1` → 0).

**Why bottom-up:** km error format emits the root cause as the final `Error:` line in a chain — scanning from the bottom returns the most specific, actionable error rather than an intermediate wrapping message.

**Cap logic:** 1024-char cap applied to the matched `Error:` line itself (`line[:maxLen]`). The no-Error-line fallback tail is capped at `maxLen - len(noErrorMarker)` chars before prepending the marker, so the total result is also ≤ 1024.

**Empty input behavior:** Returns `"<no error line; tail of subprocess output> "` (marker + empty string). Documented in `TestExtractFailureReason_EmptyInput`.

## Failure Branch Modification (main.go lines 255-271)

```go
// Phase 77: extract a one-line summary from subprocess output for persistence.
failureReason := extractFailureReason(outStr)
failedAt := time.Now().UTC()

// Update DynamoDB metadata so km list shows the failure instead of stuck "starting".
// Phase 77: switch to the new helper so failure_reason and failed_at land in the same UpdateItem.
if h.DynamoClient != nil && h.TableName != "" {
    if statusErr := awspkg.UpdateSandboxStatusAndReasonDynamo(ctx, h.DynamoClient, h.TableName, event.SandboxID, failStatus, failureReason, failedAt); statusErr != nil {
        log.Warn().Err(statusErr).Str("sandbox_id", event.SandboxID).
            Str("status", failStatus).Msg("failed to update metadata status+reason (non-fatal)")
    } else {
        log.Info().Str("sandbox_id", event.SandboxID).
            Str("status", failStatus).Msg("updated metadata status with failure reason")
    }
}
```

## Success Branch Confirmation

The success branch (`runErr == nil`, starting at line 280) is byte-identical to the pre-77-02 code. Zero changes to the success path.

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- `cmd/create-handler/main.go` exists and contains `extractFailureReason` and `UpdateSandboxStatusAndReasonDynamo`
- `cmd/create-handler/main_test.go` exists and contains `TestExtractFailureReason_LastErrorLine` and all branch tests
- Commits 61cbb91 and a8902a7 present in git log
- `go test ./cmd/create-handler/... -count=1` — all 13 tests pass
- `go vet ./cmd/create-handler/...` — clean
- `go build ./cmd/create-handler/` — clean
- Pre-existing build failures in `internal/app/cmd/logs.go` are from parallel plan 77-04 (unused `time` and `cloudwatchlogstypes` imports added by 77-04), out of scope for this plan

## Notes for Wave 3 Consumers

- **77-03 (km status render):** Can now read `SandboxMetadata.FailureReason` and `SandboxMetadata.FailedAt` from DDB (written by this plan via `UpdateSandboxStatusAndReasonDynamo`)
- **77-04 (km logs fallback):** Unaffected by this plan; `pkg/aws/cloudwatch.go` and `internal/app/cmd/logs.go` untouched
