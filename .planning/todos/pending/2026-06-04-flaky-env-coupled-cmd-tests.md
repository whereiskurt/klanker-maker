# Test hygiene: isolate env-coupled flaky tests in internal/app/cmd

**Created:** 2026-06-04
**Source:** Discovered during Phase 94 close-out while running a clean full `go test` gate.
**Status:** pending
**Severity:** low (test-only — no product defect; these pass/fail by machine environment)

## Problem

`go test ./internal/app/cmd/...` does NOT pass cleanly on a configured operator
machine. A set of tests assert "missing/empty config → command errors", but they
construct the **real** command path (e.g. `NewListCmdWithLister(cfg, nil)` — "nil
forces the real lister construction path"), which then reads the **ambient**
environment (`km-config.yaml` in repo root, `KM_*` env vars, real AWS creds) and
**succeeds** — so the expected error is `nil` and the assertion fails.

Evidence (TestListCmd_EmptyStateBucketError):
```go
cfg := &config.Config{StateBucket: ""}          // explicitly empty
listCmd := cmd.NewListCmdWithLister(cfg, nil)   // nil → real lister, hits real AWS
// expects err contains "state bucket not configured" ... gets nil
// runtime ~0.89s == real network call, not a unit assertion
```

The failure SET is **non-deterministic** across runs (network/timing coupling),
which is why it reads as "flaky":
- One run failed only `TestRunAgentAuthClaude_TeesAndCleans` (it literally opened
  a browser for the OAuth flow — `✓ Opened OAuth URL in your default browser`).
- Another run failed a different 12.

## Observed offenders (union across runs — not exhaustive)

- `TestRunAgentAuthClaude_TeesAndCleans` (agent_auth_test.go) — invokes real `claude auth status` / browser OAuth
- `TestEmailRead_EncryptedMessageAutoDecrypts`
- `TestLoadEFSOutputs_NotExist`
- `TestListCmd_EmptyStateBucketError`
- `TestStatusCmd_EmptyStateBucketError`
- `TestLockCmd_RequiresStateBucket`
- `TestUnlockCmd_RequiresStateBucket`
- `TestShellDockerContainerName`
- `TestShellDockerNoRootFlag`
- `TestShellCmd_StoppedSandbox`
- `TestShellCmd_UnknownSubstrate`
- `TestShellCmd_MissingInstanceID`
- `TestLearnOutputPath`

## NOT in scope of this todo / confirmed unrelated

These are **pre-existing** and independent of Phase 94. My Phase 94 commits never
touched `agent_auth.go`, `list.go`, `shell.go`, `email.go`, `status.go`,
`lock.go`, etc. (verified via `git log --grep=94-0 -- <file>`). The Phase 94 code
(`doctor_log_groups.go`, `doctor_ddb_rows.go`, `doctor_artifacts.go`, the config
knobs, the compiler migration) and `pkg/slack` / `pkg/compiler` / `internal/app/config`
all pass reliably in targeted and aggregate runs.

## Suggested fix

- Make these tests hermetic: inject a fake lister/AWS client (the test seam already
  exists — `NewListCmdWithLister` takes a lister; the test passes `nil` to force the
  real path, which is the bug). Pass a fake that returns a controlled result/error.
- Clear ambient `KM_*` env in the test (`t.Setenv` to empty) and point config away
  from any real `km-config.yaml` so "empty StateBucket" actually stays empty.
- For `TestRunAgentAuthClaude_*`: stub the browser-open + `claude auth status`
  invocation so it never touches a real binary/OAuth flow.
- Add a CI guard: `go test ./internal/app/cmd/...` should pass in a clean,
  credential-less environment (the real signal these tests were meant to give).

## Acceptance

`go test ./internal/app/cmd/...` passes deterministically with no AWS creds and no
ambient `KM_*` env / `km-config.yaml` present.
