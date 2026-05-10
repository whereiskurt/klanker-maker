---
phase: 77
slug: failed-sandbox-discoverability-persist-failure-reason-in-ddb-and-km-logs-lambda-fallback
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-10
---

# Phase 77 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go standard `testing` package |
| **Config file** | none — Go test runner invoked directly |
| **Quick run command** | `go test ./cmd/create-handler/... ./pkg/aws/... ./internal/app/cmd/... -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~30s (quick), ~3-5min (full) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./cmd/create-handler/... ./pkg/aws/... ./internal/app/cmd/... -count=1`
- **After every plan wave:** Run `go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 60 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 77-W0-01 | W0 | 0 | Wave 0 — DI seam + mock infra | unit (compile) | `go build ./...` | ❌ W0 | ⬜ pending |
| 77-W0-02 | W0 | 0 | `CWLogsAPI.FilterLogEvents` added | unit (compile) | `go build ./pkg/aws/...` | ❌ W0 | ⬜ pending |
| 77-W0-03 | W0 | 0 | `runLogs` accepts injected `CWLogsAPI` | unit | `go test ./internal/app/cmd/ -run TestLogsCmd_PerSandboxGroupPresent -count=1` | ❌ W0 | ⬜ pending |
| 77-01-01 | 01 | 1 | `SandboxMetadata` + `SandboxRecord` field additions | unit | `go test ./pkg/aws/ -run TestSandboxMetadataMarshal -count=1` | ✅ | ⬜ pending |
| 77-01-02 | 01 | 1 | `UpdateSandboxStatusAndReasonDynamo` round-trip | unit | `go test ./pkg/aws/ -run TestUpdateSandboxStatusAndReasonDynamo_RoundTrip -count=1` | ❌ W0 | ⬜ pending |
| 77-02-01 | 02 | 2 | `extractFailureReason` last-`Error:` line wins | unit | `go test ./cmd/create-handler/ -run TestExtractFailureReason_LastErrorLine -count=1` | ❌ W0 | ⬜ pending |
| 77-02-02 | 02 | 2 | `extractFailureReason` no-`Error:` tail prefix | unit | `go test ./cmd/create-handler/ -run TestExtractFailureReason_NoErrorLine_TailFallback -count=1` | ❌ W0 | ⬜ pending |
| 77-02-03 | 02 | 2 | `extractFailureReason` 1024-char trim | unit | `go test ./cmd/create-handler/ -run TestExtractFailureReason_TrimsTo1024 -count=1` | ❌ W0 | ⬜ pending |
| 77-02-04 | 02 | 2 | failed branch calls new helper with reason+timestamp | unit | `go test ./cmd/create-handler/ -run TestCreateHandler_FailurePath_WritesFailureReason -count=1` | ❌ W0 | ⬜ pending |
| 77-02-05 | 02 | 2 | nocap branch also writes reason | unit | `go test ./cmd/create-handler/ -run TestCreateHandler_NocapPath_WritesFailureReason -count=1` | ❌ W0 | ⬜ pending |
| 77-03-01 | 03 | 2 | `km status` failed with reason → `Failure:` line | unit | `go test ./internal/app/cmd/ -run TestStatusCmd_FailedWithReason -count=1` | ✅ | ⬜ pending |
| 77-03-02 | 03 | 2 | `km status` failed without reason → `<unknown>` hint | unit | `go test ./internal/app/cmd/ -run TestStatusCmd_FailedNoReason -count=1` | ✅ | ⬜ pending |
| 77-03-03 | 03 | 2 | `km status` running → no `Failure:` line | unit | `go test ./internal/app/cmd/ -run TestStatusCmd_Running_NoFailureLine -count=1` | ✅ | ⬜ pending |
| 77-03-04 | 03 | 2 | `km status` nocap with reason → `Failure:` line printed | unit | `go test ./internal/app/cmd/ -run TestStatusCmd_NocapWithReason -count=1` | ✅ | ⬜ pending |
| 77-04-01 | 04 | 2 | `km logs` per-sandbox group present → unchanged | unit | `go test ./internal/app/cmd/ -run TestLogsCmd_PerSandboxGroupPresent -count=1` | ❌ W0 | ⬜ pending |
| 77-04-02 | 04 | 2 | `km logs` fallback prints prelude + Lambda events | unit | `go test ./internal/app/cmd/ -run TestLogsCmd_FallbackWithEvents -count=1` | ❌ W0 | ⬜ pending |
| 77-04-03 | 04 | 2 | `km logs` both empty → friendly hint | unit | `go test ./internal/app/cmd/ -run TestLogsCmd_FallbackBothEmpty -count=1` | ❌ W0 | ⬜ pending |
| 77-04-04 | 04 | 2 | `km logs --follow` in fallback → exits cleanly | unit | `go test ./internal/app/cmd/ -run TestLogsCmd_FallbackFollow_NoOp -count=1` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky · ❌ W0 = test infrastructure delivered by Wave 0*

---

## Wave 0 Requirements

The Wave 0 plan must close these test-infrastructure gaps before Wave 1+ tests can compile:

- [ ] `pkg/aws/cloudwatch.go` — add `FilterLogEvents` method to `CWLogsAPI` interface
- [ ] `pkg/aws/cloudwatch_test.go` — extend `mockCWLogsAPI` with `FilterLogEvents` stub
- [ ] `internal/app/cmd/logs.go` — refactor `runLogs` to accept injected `CWLogsAPI` (mirror `NewStatusCmdWithFetcher` DI pattern)
- [ ] `internal/app/cmd/logs_test.go` — add local `mockCWLogsAPI` (or import from `pkg/aws`) covering `GetLogEvents` + `FilterLogEvents` + `ResourceNotFoundException` paths
- [ ] `cmd/create-handler/main_test.go` — add local `mockSandboxMetadataAPI` (existing one in `pkg/aws/sandbox_dynamo_test.go` is package-private to `aws_test` and cannot be imported)

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Real failed sandbox shows reason in `km status` | E2E | DDB schema change requires real-create-failure observation; fully unit-mocked in Wave 1+ | `km create profiles/learn.v2.yaml --alias l4-test` against an archived `#sb-l4-test` channel; expect `failed`; run `km status <id>` and confirm `Failure:` line populated |
| Real `km logs <id>` Lambda fallback prints | E2E | CloudWatch log-group existence is environmental | After above test, run `km logs <id>` (per-sandbox group never created); expect prelude + Lambda events |
| Multi-instance prefix correctness | Multi-instance | `KM_RESOURCE_PREFIX=kph` install paths are environmental | On a non-default-prefix install, confirm fallback group is `/aws/lambda/kph-create-handler` |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all `❌ W0` references in Per-Task Verification Map
- [ ] No watch-mode flags in commands
- [ ] Feedback latency < 60s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
