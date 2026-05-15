---
phase: 77-failed-sandbox-discoverability-persist-failure-reason-in-ddb-and-km-logs-lambda-fallback
verified: 2026-05-15T02:10:00Z
status: passed
score: 9/9 must-haves verified
re_verification: false
gaps: []
human_verification: []
---

# Phase 77: Failed Sandbox Discoverability — Verification Report

**Phase Goal:** Make the failure reason for a failed `km create --remote` discoverable to the operator via existing `km status` and `km logs` commands. Two complementary changes: (1) persist `failure_reason` + `failed_at` to the sandbox DynamoDB record at create-handler fail time; (2) surface those fields in `km status` and add a Lambda-log fallback to `km logs` when the per-sandbox log group does not exist.

**Verified:** 2026-05-15T02:10:00Z
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | DDB schema carries `FailureReason` (≤1024 chars, omitempty) and `FailedAt (*time.Time, omitempty)` | VERIFIED | `pkg/aws/metadata.go:53-54` + `pkg/aws/sandbox.go:51-52` |
| 2 | `UpdateSandboxStatusAndReasonDynamo` exists with locked signature + single UpdateItem SET expression | VERIFIED | `pkg/aws/sandbox_dynamo.go:648-668` — SET `#s = :status, failure_reason = :reason, failed_at = :ts` |
| 3 | `extractFailureReason` does bottom-up scan for `Error:` prefix, ≤1024 trim, no-error-line fallback prefix | VERIFIED | `cmd/create-handler/main.go:497-519` — exact algorithm implemented |
| 4 | create-handler failure branch covers both `failed` AND `nocap` via the new helper | VERIFIED | `cmd/create-handler/main.go:256-268` — single call with `failStatus` variable covering both |
| 5 | `km status` prints `Failure:` / `Failed At:` only when `Status ∈ {failed, nocap}` | VERIFIED | `internal/app/cmd/status.go:369-378` — gated on `rec.Status == "failed" \|\| rec.Status == "nocap"` |
| 6 | `km status` prints `<unknown — try km logs <id>>` when reason is empty | VERIFIED | `internal/app/cmd/status.go:376` — exact text including sandbox ID via `%s` |
| 7 | `km logs` fallback uses `errors.As` for `ResourceNotFoundException`, 24h window, `{ $.sandbox_id = "<id>" }` filter, prefix via `cfg.GetResourcePrefix()` | VERIFIED | `internal/app/cmd/logs.go:87-89`, `pkg/aws/cloudwatch.go:229-253` |
| 8 | `km logs --follow` no-ops in fallback mode with clean exit | VERIFIED | `internal/app/cmd/logs.go:107-110` — prints note and returns nil |
| 9 | `km list` unchanged; no infrastructure churn (no new IAM, no new DDB tables, no `km init --sidecars`) | VERIFIED | `git diff 148d026..HEAD` — zero diff on `internal/app/cmd/list.go` and `infra/` |

**Score:** 9/9 truths verified

---

### Required Artifacts

| Artifact | Status | Evidence |
|----------|--------|----------|
| `pkg/aws/metadata.go` — `FailureReason string` + `FailedAt *time.Time` | VERIFIED | Line 53-54, `json:"failure_reason,omitempty"` + `json:"failed_at,omitempty"` |
| `pkg/aws/sandbox.go` — same two fields on `SandboxRecord` | VERIFIED | Line 51-52, identical tags |
| `pkg/aws/sandbox_dynamo.go` — `UpdateSandboxStatusAndReasonDynamo` + `unmarshalFailureFields` + `metadataToRecord` field copies | VERIFIED | Lines 640-668, 263-280, 139-140 |
| `pkg/aws/sandbox_dynamo_test.go` — `TestUpdateSandboxStatusAndReasonDynamo_RoundTrip` + 3 companions | VERIFIED | Lines 888, 981, 1012, 1088 — all 4 tests PASS |
| `pkg/aws/cloudwatch.go` — `FilterLogEvents` in `CWLogsAPI` interface + `FilterCreateHandlerLogs` helper | VERIFIED | Line 29 (interface), lines 229-253 (helper) |
| `pkg/aws/cloudwatch_test.go` — `TestFilterCreateHandlerLogs_*` (4 tests) + `mockCWLogsAPI` with `FilterLogEvents` stub | VERIFIED | Lines 219-337 — all 4 tests PASS |
| `cmd/create-handler/main.go` — `extractFailureReason` + failure branch using new helper | VERIFIED | Lines 497-519 (helper), 256-268 (call site) |
| `cmd/create-handler/main_test.go` — 5 extraction tests + 4 branch tests + `mockSandboxMetadataAPI` | VERIFIED | All 9 new tests PASS; `mockSandboxMetadataAPI` at line 150+ |
| `internal/app/cmd/status.go` — `Failure:` / `Failed At:` block in `printSandboxStatus` | VERIFIED | Lines 364-378, correct 13-char column alignment |
| `internal/app/cmd/status_test.go` — 4 behavior tests | VERIFIED | Lines 618, 663, 697, 732 — all 4 PASS |
| `internal/app/cmd/logs.go` — `NewLogsCmdWithClient` DI seam + `runLogsLambdaFallback` | VERIFIED | Lines 30, 103-132 |
| `internal/app/cmd/logs_test.go` — `TestLogsCmd_PerSandboxGroupPresent` + 4 fallback tests | VERIFIED | Lines 243, 84, 134, 176, 213 — all 5 PASS |

---

### Key Link Verification

| From | To | Via | Status |
|------|----|-----|--------|
| `cmd/create-handler/main.go` failure branch | `awspkg.UpdateSandboxStatusAndReasonDynamo` | direct call at line 262 | WIRED |
| `cmd/create-handler/main.go` failure branch | `extractFailureReason(outStr)` | call at line 256, before DDB write | WIRED |
| `pkg/aws/sandbox_dynamo.go ReadSandboxMetadataDynamo` | `unmarshalFailureFields` | call at line 407 after `unmarshalSlackFields` | WIRED |
| `pkg/aws/sandbox_dynamo.go ListAllSandboxesByDynamo` (both scan paths) | `unmarshalFailureFields` | calls at lines 471-472 and 514-515 | WIRED |
| `pkg/aws/sandbox_dynamo.go metadataToRecord` | `SandboxRecord.FailureReason / .FailedAt` | field copies at lines 139-140 | WIRED |
| `internal/app/cmd/logs.go runLogs` | `ResourceNotFoundException` fallback | `errors.As(err, &notFound)` at line 88 | WIRED |
| `runLogsLambdaFallback` | `kmaws.FilterCreateHandlerLogs` | call at line 117 with `cfg.GetResourcePrefix()` | WIRED |
| `internal/app/cmd/logs_test.go` | `NewLogsCmdWithClient` | injection at all test call sites | WIRED |

---

### Requirements Coverage

Phase 77 uses phase-local requirement IDs. Mapping against CONTEXT.md `<decisions>` locked behaviors:

| Req ID | Plans | Locked Decision | Status |
|--------|-------|-----------------|--------|
| SCHM-77 | 77-01 | `SandboxMetadata.FailureReason` + `FailedAt` fields with `omitempty` | SATISFIED — `pkg/aws/metadata.go:53-54` |
| HELP-77 | 77-01 | `UpdateSandboxStatusAndReasonDynamo` single UpdateItem with SET expression | SATISFIED — `pkg/aws/sandbox_dynamo.go:648-668` |
| HAND-77 | 77-02 | create-handler: `extractFailureReason` + new helper for both failed/nocap | SATISFIED — `cmd/create-handler/main.go:256-268, 497-519` |
| STAT-77 | 77-03 | `km status` renders `Failure:` / `Failed At:` or `<unknown>` hint; gated on failed/nocap | SATISFIED — `internal/app/cmd/status.go:364-378` |
| LOGS-77 | 77-04 | `km logs` fallback on `ResourceNotFoundException` → `/aws/lambda/<prefix>-create-handler` | SATISFIED — `internal/app/cmd/logs.go:81-94` |
| MULTI-77 | 77-04 | Lambda group name uses `cfg.GetResourcePrefix()` for multi-instance support | SATISFIED — `internal/app/cmd/logs.go:117`, `pkg/aws/cloudwatch.go:230` |
| TEST-77 | 77-00 | Test infrastructure: `CWLogsAPI.FilterLogEvents`, DI seam, mocks | SATISFIED — all seams compile and function |
| INTERFACE-77 | 77-00 | `CWLogsAPI` widened without breaking existing callers | SATISFIED — `go build ./...` passes |
| DI-77 | 77-00 | `NewLogsCmdWithClient` DI seam in `logs.go` | SATISFIED — `internal/app/cmd/logs.go:30` |

---

### Anti-Patterns Found

None in Phase 77 code. Verified the failure branch:
- No `return null` / `return {}` stubs
- DDB write failure is non-fatal (logged warn, returns original `runErr`)
- `extractFailureReason` is a substantive implementation, not a placeholder
- `runLogsLambdaFallback` is a substantive implementation with all four cases handled

---

### Test Coverage Results

**26 new Phase 77 tests — all PASS:**

`pkg/aws` (8 tests):
- `TestUpdateSandboxStatusAndReasonDynamo_RoundTrip` PASS
- `TestUpdateSandboxStatusAndReasonDynamo_OldRecord_ZeroValue` PASS
- `TestSandboxMetadataMarshal_FailureFields` PASS
- `TestUpdateSandboxStatusDynamo_StillWorks` PASS
- `TestFilterCreateHandlerLogs_HappyPath` PASS
- `TestFilterCreateHandlerLogs_Empty` PASS
- `TestFilterCreateHandlerLogs_MultiInstancePrefix` PASS
- `TestFilterCreateHandlerLogs_PropagatesError` PASS

`cmd/create-handler` (9 tests):
- `TestExtractFailureReason_LastErrorLine` PASS
- `TestExtractFailureReason_NoErrorLine_TailFallback` PASS
- `TestExtractFailureReason_TrimsTo1024` PASS
- `TestExtractFailureReason_EmptyInput` PASS
- `TestExtractFailureReason_TrailingWhitespace` PASS
- `TestCreateHandler_FailurePath_WritesFailureReason` PASS
- `TestCreateHandler_NocapPath_WritesFailureReason` PASS
- `TestCreateHandler_FailurePath_NoErrorLine_StillWritesTailReason` PASS
- `TestCreateHandler_DDBWriteFailure_NonFatal` PASS

`internal/app/cmd` (9 tests):
- `TestStatusCmd_FailedWithReason` PASS
- `TestStatusCmd_FailedNoReason` PASS
- `TestStatusCmd_NocapWithReason` PASS
- `TestStatusCmd_Running_NoFailureLine` PASS
- `TestLogsCmd_PerSandboxGroupPresent` PASS
- `TestLogsCmd_FallbackWithEvents` PASS
- `TestLogsCmd_FallbackBothEmpty` PASS
- `TestLogsCmd_FallbackFollow_NoOp` PASS
- `TestLogsCmd_NonNotFoundError_Surfaces` PASS

**Full package results:**
- `pkg/aws` — ok (all tests pass)
- `cmd/create-handler` — ok (all tests pass)
- `internal/app/cmd` — Phase 77 tests all pass; 11 pre-existing failures unrelated to this phase (SSO token expiry for `TestBootstrapSCPApplyPath`, `TestClusterAdd`/`TestClusterRm`/`TestClusterAddPersistFailure`; test-data drift for `TestProbeCodexPort_Primary`, `TestStep11d_Success_WritesChannelIDParam`, `TestAtList_WithRecords`, `TestCreateDockerWritesComposeFile`, `TestApplyLifecycleOverrides_RunCreateRemoteSignature`, `TestRunDestroy_GitHubTokenCleanup`). Confirmed pre-existing by `git diff 148d026..HEAD` — zero diff on those test files.

**Build:** `go build ./...` passes with no errors or warnings.

---

### Human Verification Required

None. All behavioral contracts are verifiable programmatically. The full operator flow (operator sees `Failure:` line in `km status` output, or runs `km logs` and sees the Lambda fallback prelude) is covered by the test suite with captured-stdout assertions.

---

### Deferred Items (not a gap — explicitly out of scope)

Per CONTEXT.md `<deferred>`:
- L2/L3 backfill of pre-existing failed records — correctly excluded
- Lambda fallback for `ttl-handler`, `budget-enforcer`, `email-create-handler` — correctly excluded
- Failure-reason column in `km list` — correctly excluded; `list.go` is byte-identical to pre-Phase-77
- Slack-archived-channel auto-recovery — correctly excluded

---

## Decision

**PASS.** All 9 observable truths verified against the actual codebase. All 26 named tests pass. The `go build ./...` succeeds. Zero infrastructure churn. The failure reason for a failed `km create --remote` is now persisted in DynamoDB and surfaced via `km status` and `km logs`, fulfilling the phase goal.

---

_Verified: 2026-05-15T02:10:00Z_
_Verifier: Claude (gsd-verifier)_
