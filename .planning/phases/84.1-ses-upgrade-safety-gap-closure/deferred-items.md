# Phase 84.1 — Deferred Items

Items discovered during Phase 84.1 execution that are OUT OF SCOPE for the
current plan and intentionally NOT fixed inline. Each entry records the test
or file involved, what the symptom is, and why it is out of scope.

---

## Discovered during 84.1-01

### `TestUnlockCmd_RequiresStateBucket` fails on local dev environment

**Test:** `internal/app/cmd/unlock_test.go::TestUnlockCmd_RequiresStateBucket`

**Symptom:**
```
unlock_test.go:73: error should mention 'state bucket', got: sandbox sb-aabbccdd is not locked
```

**Diagnosis:**
- `runUnlock` (unlock.go:64) calls `awspkg.UnlockSandboxDynamo` first against
  the real AWS account (uses `awspkg.LoadAWSConfig(ctx, "klanker-terraform")`).
- The dynamo call returns "not locked" instead of `ResourceNotFoundException`,
  so the S3-fallback path (which contains the "state bucket not configured"
  error the test expects) never fires.
- The test was written under the assumption that the DDB table does NOT exist
  (forcing the S3 fallback), but on a fresh dev account where the table DOES
  exist and `sb-aabbccdd` is genuinely absent, the dynamo error path masks the
  fallback's "state bucket" error.

**Why out of scope for 84.1-01:**
- Plan 84.1-01 covers env-var export consolidation (GAP-1 / GAP-7).
- This test exercises the unlock command's error precedence — a pre-existing
  environment-coupling regression unrelated to env-var handling.
- The test passes on a dev account where the DDB sandboxes table does not
  exist; it only fails when the table is present and the requested sandbox
  ID is absent. None of plan 84.1-01's changes touch that code path.

**Recommended remediation (separate plan):**
- Refactor `runUnlock` to either inject the dynamo client (so a test mock can
  return `ResourceNotFoundException` deterministically) or to short-circuit on
  `cfg.StateBucket == ""` before the dynamo call.
- File under a future "test stability / DI hardening" plan.

---

## Discovered during 84.1-03

### Full `go test ./internal/app/cmd/...` suite hangs on cumulative HTTP/2 connections

**Test:** Cumulative — appears to manifest after `TestListCmd_AliasEmpty` runs
in the default sequential test order. Stack traces show goroutines stuck in
`net/http.(*http2Transport).newClientConn` → `http2clientConnReadLoop.run` →
TLS `Read` (i.e. an outbound AWS API connection that never returns).

**Symptom:**
```
panic: test timed out after 1m30s
FAIL  github.com/whereiskurt/klanker-maker/internal/app/cmd  90.846s
```

**Diagnosis:**
- Individual tests pass cleanly when run in isolation
  (`go test -run "TestListCmd_Alias|TestVSCode..." -timeout 10s` all PASS).
- The hang only manifests with the full package suite and is unaffected by
  `-p 1 -parallel 1` (so it is NOT a parallelism race).
- Each Phase 84.1-03 scoped test set runs green in <3s:
  `TestCheckStateLockDigest|TestParseLockID|TestBuildChecks|TestBackendLockTableName`
  → all 11 PASS in 1.3s; broader doctor-scoped run (~14 tests) → PASS in 2.9s.
- A test elsewhere in the suite (most likely an init / shell / VS Code / agent
  flow) is constructing a real AWS HTTP client that holds open an idle
  HTTP/2 connection past test exit, accumulating goroutines until the test
  runtime panics with the deadline timeout.

**Why out of scope for 84.1-03:**
- Plan 84.1-03 closes GAP-8 (state-digest drift detection in `km doctor`)
  via two new functions in `doctor.go` and a wiring update in `buildChecks`.
- None of the new code opens HTTP/2 connections or AWS API clients beyond the
  narrow-interface mocks used in tests (`mockS3StateReader`,
  `mockLockDigestReader`) — both pure in-memory.
- The hang is reproducible at HEAD before this plan's changes (Plan 84.1-01
  completed independently in parallel; the symptom predates either plan).

**Recommended remediation (separate plan):**
- Bisect the test that leaks the HTTP/2 connection. Likely candidates:
  `agent`-prefixed tests, `init`-prefixed integration tests, or any test
  that calls `kmaws.LoadAWSConfig` without a `t.Cleanup` to close the
  HTTP transport.
- Add `t.Cleanup(func() { client.HTTPClient.CloseIdleConnections() })` to
  the offending test once identified.
- File under the same "test stability / DI hardening" track as the
  84.1-01 deferred item.

---
