# Phase 107: Reconcile 22 Stale cmd Unit Tests — Research

**Researched:** 2026-06-11
**Domain:** Go unit test hygiene — `internal/app/cmd/` package
**Confidence:** HIGH (all findings derived from running the tests live + reading production code)

---

## Summary

All 22 named failing tests were run individually and as a full suite (600 s timeout required — real AWS calls in many tests). The failure set is exactly the 22 named tests; no extra failures exist. Every test was traced to its production function and the exact assertion mismatch was identified.

**Classification breakdown:** 19 STALE-TEST (update assertion to match current behavior), 3 ESCALATE (shell stopped/unknown/missing — production code silently discards user-visible errors via intentional `return nil`; these are real usability gaps, not just stale assertions).

**Primary recommendation:** Fix the 19 STALE-TEST tests in-place, no production code changes. For the 3 ESCALATE tests, present to the user: either weaken the test to match current nil behavior, or make a targeted production code fix first.

---

## Baseline Confirmation

Running `go test ./internal/app/cmd/ -count=1 -timeout 600s` confirms exactly 22 `--- FAIL` lines:

```
TestApplyLifecycleOverrides_RunCreateRemoteSignature
TestAtList_WithRecords
TestCreateDockerWritesComposeFile
TestEmailRead_EncryptedMessageAutoDecrypts
TestEmailSend_BodyFromStdin
TestEmailSend_SuccessNoAttachments
TestEmailSend_TwoAttachments
TestLearnOutputPath
TestListCmd_EmptyStateBucketError
TestLoadEFSOutputs_NotExist
TestLockCmd_RequiresStateBucket
TestRunAgentAuthClaude_TeesAndCleans
TestShellCmd_MissingInstanceID
TestShellCmd_StoppedSandbox
TestShellCmd_UnknownSubstrate
TestShellDockerContainerName
TestShellDockerNoRootFlag
TestStatusCmd_EmptyStateBucketError
TestUninitContinuesPastModuleErrors
TestUninitDestroyOrder
TestUninitDetectsBackendDrift
TestUnlockCmd_RequiresStateBucket
```

Currently-green tests that MUST stay green: `TestScoped*` (10 tests, Phase 105), `TestRunInitPlan_ModuleOrder` (1 test, Phase 105 — hardcodes count=22).

---

## Per-Test Triage Table

### Subsystem: shell (docker) — `shell_docker_test.go`

#### TEST-1: `TestShellDockerContainerName`
**File/line:** `shell_docker_test.go:51`
**Assertion:** `strings.Contains(fullCmd, "/bin/bash")` → FAIL
**Actual command built by `execDockerShell`:** `docker exec -it -u sandbox km-sb-docker-1-main bash --login`
**Root cause:** `shell.go:846` uses `"bash", "--login"` not `"/bin/bash"`. Changed to use a login shell so `/etc/profile.d/` scripts run.
**Classification:** STALE-TEST
**Fix:** Change `"/bin/bash"` assertion to `"bash --login"` (or check for `"bash"` and `"--login"` separately).

#### TEST-2: `TestShellDockerNoRootFlag`
**File/line:** `shell_docker_test.go:123`
**Assertion:** `!strings.Contains(fullCmd, "-u")` → FAIL (expects NO `-u`)
**Actual behavior:** `execDockerShell` (`shell.go:843-844`) ALWAYS adds `-u sandbox` in the non-root path: `args = append(args, "-u", "sandbox")`.
**Root cause:** The sandbox runs as user `sandbox`; `-u sandbox` is now always passed to ensure correct user context regardless of Docker's default.
**Classification:** STALE-TEST
**Fix:** Remove the `strings.Contains(fullCmd, "-u")` assertion, OR change it to assert `-u sandbox` is present (matching the symmetric test `TestShellDockerRootFlag` which already verifies `-u root`).

---

### Subsystem: shell (EC2/substrate routing) — `shell_test.go`

These three tests expose a real behavioral gap in the production code, not just stale assertions. They are classified ESCALATE.

#### TEST-3: `TestShellCmd_StoppedSandbox`
**File/line:** `shell_test.go:307`
**Assertion:** `err == nil` expected to fail → FAIL (got nil)
**What the test expects:** Running `km shell sb-stopped` on a stopped sandbox returns an error.
**What production does:** `runShellWithSSM` (`shell.go:360-362`) DOES build the error `"sandbox sb-stopped is stopped — start it with 'km budget add'"`. BUT `NewShellCmdWithFetcher`'s `RunE` (`shell.go:284-296`) captures `runErr` and then `return nil` unconditionally in the non-learn path. The error is silently discarded.
**Why `return nil` is there:** Intentional design (`commit aaf76a4f`) — avoids cobra printing a spurious error message when a normal interactive SSM session exits. The alternative constructor `newShellCmdWithSSM` (private) does `return runErr` and its comment says it is the constructor for tests needing error propagation. But it is private, so tests using the public `NewShellCmdWithFetcher` cannot get error propagation.
**Operational impact:** `km shell sb-stopped` currently exits 0 with no error output — the operator sees nothing. This is a usability regression.
**Classification:** ESCALATE — the test is correct in spirit. Fixing it requires either (a) a small production code change to distinguish pre-flight errors from session exit errors in `NewShellCmdWithFetcher`, or (b) weakening the test to accept nil and drop the verification. User must decide.
**Recommended path:** Production code fix — in `NewShellCmdWithFetcher`'s `RunE`, return `runErr` when it is a pre-flight error (stopped/unsupported substrate/missing instance). Only suppress session-exit errors (errors from the actual `execFn` call). See `newShellCmdWithSSM:211` for the correct pattern.

#### TEST-4: `TestShellCmd_UnknownSubstrate`
**File/line:** `shell_test.go:332`
**Assertion:** same pattern — expects non-nil error for substrate `"k8s"`, got nil.
**Root cause:** Same `return nil` swallow in `NewShellCmdWithFetcher`.
**Classification:** ESCALATE (same as TEST-3)

#### TEST-5: `TestShellCmd_MissingInstanceID`
**File/line:** `shell_test.go:362`
**Assertion:** expects non-nil error when no EC2 instance ARN in resources, got nil.
**Root cause:** Same `return nil` swallow.
**Classification:** ESCALATE (same as TEST-3)

---

### Subsystem: email — `email_test.go`

All four email failures share the same root cause: the mock SSM parameter keys are missing the `km` resource prefix.

#### TEST-6: `TestEmailSend_SuccessNoAttachments`
**File/line:** `email_test.go:406`
**Error:** `"send email: read signing key from SSM (/km/sandbox/sb-sender1/signing-key): ParameterNotFound"`
**Root cause:** `testEmailCfg()` (`email_test.go:183`) returns a `Config` with no `ResourcePrefix` set. `Config.GetResourcePrefix()` defaults to `"km"`. The production SSM path is `SigningKeyPath(prefix, id)` → `"/{prefix}/sandbox/{id}/signing-key"` → `/km/sandbox/sb-sender1/signing-key`. The mock at line 387 registers key as `/sandbox/sb-sender1/signing-key` (no `km/` prefix).
**Classification:** STALE-TEST
**Fix:** Change mock SSM key from `"/sandbox/sb-sender1/signing-key"` to `"/km/sandbox/sb-sender1/signing-key"`.

#### TEST-7: `TestEmailSend_TwoAttachments`
**File/line:** `email_test.go:456`
**Root cause:** Same SSM prefix mismatch (line 427).
**Fix:** Change `"/sandbox/sb-sender1/signing-key"` → `"/km/sandbox/sb-sender1/signing-key"`.

#### TEST-8: `TestEmailSend_BodyFromStdin`
**File/line:** `email_test.go:514`
**Root cause:** Same SSM prefix mismatch (line 480).
**Fix:** Change `"/sandbox/sb-sender1/signing-key"` → `"/km/sandbox/sb-sender1/signing-key"`.

#### TEST-9: `TestEmailRead_EncryptedMessageAutoDecrypts`
**File/line:** `email_test.go:788`
**Error:** Output shows ciphertext body preview instead of decrypted plaintext — decryption never ran because SSM key lookup failed.
**Root cause:** Same prefix mismatch. Mock at line 779: `fmt.Sprintf("/sandbox/%s/encryption-key", sandboxID)` → `/sandbox/sb-recip01/encryption-key`. Production path: `EncryptionKeyPath(prefix, id)` → `/km/sandbox/sb-recip01/encryption-key`.
**Fix:** Change `fmt.Sprintf("/sandbox/%s/encryption-key", sandboxID)` → `fmt.Sprintf("/km/sandbox/%s/encryption-key", sandboxID)`.

---

### Subsystem: uninit — `uninit_test.go`

All three uninit failures share the same root cause: the `wantOrder` slice has 19 entries but `regionalModules()` now returns 22.

**Modules added since the test was written:**
- Phase 103 added `dynamodb-h1-threads` and `lambda-h1-bridge`
- Phase 104 added `dynamodb-slack-channels`

**Production reverse order** (what uninit actually produces, 22 entries):
`ses, lambda-h1-bridge, dynamodb-h1-threads, lambda-github-bridge, lambda-slack-bridge, sqs-inbound-dlq, dynamodb-github-threads, dynamodb-slack-stream-messages, dynamodb-slack-channels, dynamodb-slack-threads, dynamodb-slack-nonces, email-handler, ttl-handler, create-handler, s3-replication, ssm-session-doc, dynamodb-schedules, dynamodb-sandboxes, dynamodb-identities, dynamodb-budget, efs, network`

#### TEST-10: `TestUninitDestroyOrder`
**File/line:** `uninit_test.go:103`
**Assertion:** `len(runner.calls) != len(wantOrder)` → 22 != 19 FAIL
**Classification:** STALE-TEST
**Fix:** Add the 3 missing entries to `wantOrder` in the correct positions. The `wantOrder` slice (reversed apply order) needs: after `"lambda-github-bridge"` add `"lambda-h1-bridge"` and `"dynamodb-h1-threads"`, and after `"dynamodb-slack-stream-messages"` add `"dynamodb-slack-channels"`. See the exact reverse order above.

#### TEST-11: `TestUninitContinuesPastModuleErrors`
**File/line:** `uninit_test.go:223`
**Same root cause:** Expected 19, got 22.
**Classification:** STALE-TEST
**Fix:** Same as TEST-10 — update `wantOrder` in this test's assertion too.

#### TEST-12: `TestUninitDetectsBackendDrift`
**File/line:** `uninit_test.go:467`
**Same root cause:** `"expected 19 Destroy calls (continue past drift), got 22"`
**Classification:** STALE-TEST
**Fix:** Same — update expected count and order.

---

### Subsystem: state-bucket guards — `list_test.go`, `lock_test.go`, `unlock_test.go`, `status_test.go`

These four tests were written when the commands checked `StateBucket` as a hard pre-condition. Production now uses DynamoDB as the primary backend; `StateBucket` is only required as an S3 fallback when DynamoDB's table is missing (`ResourceNotFoundException`). In the test environment, the real DynamoDB table exists, so the S3 fallback never fires.

#### TEST-13: `TestListCmd_EmptyStateBucketError`
**File/line:** `list_test.go:191`
**Expected:** `err != nil` containing `"state bucket not configured"`
**Actual:** `err == nil` — `ListAllSandboxesByDynamo` succeeds against real DynamoDB, returns 0 records, command prints "No running sandboxes." and exits 0.
**Root cause:** The `StateBucket` guard (`list.go:307-308`) only fires when DynamoDB returns `ResourceNotFoundException`. In this test environment, the table exists so DynamoDB scan succeeds.
**Classification:** STALE-TEST
**Fix:** Remove the test's requirement for `err != nil`. The test should be rewritten to verify that when `StateBucket=""` but DynamoDB works, the command succeeds (or to test the S3-fallback path specifically by injecting a mock that returns `ResourceNotFoundException`). Simplest fix: the test's precondition (StateBucket guard fires) no longer reflects production behavior. Change the test to assert that `km list` with empty `StateBucket` either succeeds (when DynamoDB is available) or returns an AWS-config error — NOT a "state bucket" error. The companion test `TestListCmd_RealBucketFromConfig` (currently green) already demonstrates the correct pattern.

#### TEST-14: `TestLockCmd_RequiresStateBucket`
**File/line:** `lock_test.go:72`
**Expected:** `err.Error()` contains `"state bucket"`
**Actual:** `"sandbox sb-aabbccdd is already locked"` — `LockSandboxDynamo` (`sandbox_dynamo.go:638-651`) runs a conditional `UpdateItem`; the condition `attribute_exists(sandbox_id)` fails for a non-existent sandbox → `ConditionalCheckFailedException` → returns `"already locked"` error.
**Classification:** STALE-TEST
**Fix:** The test intent was to verify the guard for missing config. The real guard now lives at the DynamoDB layer (different error message). Update the test assertion to accept the actual error: `"sandbox sb-aabbccdd is already locked"` (sandbox doesn't exist → condition fails). Alternatively, re-scope the test: inject a mock DynamoDB that returns `ResourceNotFoundException` to exercise the S3 fallback path, then verify `"state bucket"` error. Simplest fix: change `strings.Contains(err.Error(), "state bucket")` to `strings.Contains(err.Error(), "already locked")` since that IS the correct behavior when the sandbox doesn't exist.

#### TEST-15: `TestUnlockCmd_RequiresStateBucket`
**File/line:** `unlock_test.go:73`
**Expected:** error contains `"state bucket"`
**Actual:** `"sandbox sb-aabbccdd is not locked"` — `UnlockSandboxDynamo` condition: `attribute_exists(sandbox_id) AND locked = :t` fails → `"is not locked"`.
**Classification:** STALE-TEST
**Fix:** Change assertion to accept `"sandbox sb-aabbccdd is not locked"`.

#### TEST-16: `TestStatusCmd_EmptyStateBucketError`
**File/line:** `status_test.go:301`
**Expected:** error contains `"state bucket not configured"` OR `"get sandbox metadata"`
**Actual:** `"sandbox not found: sb-test"` — DynamoDB `GetItem` returns empty result → status code translates to not-found.
**Classification:** STALE-TEST
**Fix:** Change the acceptable error substring to include `"sandbox not found"` (add a third OR clause). `"sandbox not found: sb-test"` is a legitimate metadata error and the test's spirit (verify an error is returned for a missing/unconfigured sandbox) is preserved.

---

### Subsystem: create/docker — `create_docker_test.go`, `create_override_test.go`

#### TEST-17: `TestCreateDockerWritesComposeFile`
**File/line:** `create_docker_test.go:89`
**Error:** `"create.go missing placeholder replacement for operator key (expected 'PLACEHOLDER_OPERATOR_KEY')"`
**Root cause:** The test does a source-code text scan of `create.go` looking for `"PLACEHOLDER_OPERATOR_KEY"`. This placeholder no longer exists in `create.go`. The current placeholders are `PLACEHOLDER_SANDBOX_ROLE_ARN`, `PLACEHOLDER_SIDECAR_ROLE_ARN`, and `PLACEHOLDER_PROXY_CA_B64` (`create.go:1810-1824`). The `PLACEHOLDER_OPERATOR_KEY` check was written anticipating a feature that was either never implemented or implemented differently.
**Classification:** STALE-TEST
**Fix:** Remove the `{"placeholder replacement for operator key", "PLACEHOLDER_OPERATOR_KEY"}` entry from the `checks` slice at line 83-84. The remaining checks (`PLACEHOLDER_SANDBOX_ROLE_ARN`, `DockerComposeExecFunc`, etc.) are still valid.

#### TEST-18: `TestApplyLifecycleOverrides_RunCreateRemoteSignature`
**File/line:** `create_override_test.go:149`
**Expected signature string:**
```
runCreateRemote(cfg *config.Config, profilePath string, onDemand bool, noBedrock bool, awsProfile string, aliasOverride string, ttlOverride string, idleOverride string, clonedFromOverride ...string)
```
**Actual signature** (`create.go:2074`):
```
runCreateRemote(cfg *config.Config, profilePath string, onDemand bool, noBedrock bool, awsProfile string, aliasOverride string, ttlOverride string, idleOverride string, computeBudgetOverride float64, aiBudgetOverride float64, clonedFromOverride ...string) (string, error)
```
**Root cause:** Budget override parameters (`computeBudgetOverride float64, aiBudgetOverride float64`) were added to the signature after the test was written.
**Classification:** STALE-TEST
**Fix:** Update the expected string in the `strings.Contains` check to include the two budget parameters and the `(string, error)` return type. Or, since this is a source-scan test, update it to check for the individual parameters separately rather than the full signature string.

---

### Subsystem: misc

#### TEST-19: `TestRunAgentAuthClaude_TeesAndCleans`
**File/line:** `agent_auth_test.go:821`
**Error:** `"session exited but 'claude auth status' reports loggedIn=false — OAuth flow may have been interrupted"`
**Root cause:** After the OAuth session exits, production calls `verifyCredentialsWritten` → `verifyClaudeAuthStatus` (`agent_auth.go:540`), which runs `"sudo -u sandbox bash -lc 'claude auth status 2>&1' 2>&1"`. The mock `authTestSSM` at line 804 only routes `"tmux list-sessions"` and `"test -f '/home/sandbox/.claude/.credentials.json'"` — no route for `"claude auth status"`. Unrouted commands return the default output `m.successOutput = ""`. Empty string doesn't contain `"loggedIn": true`, so `verifyClaudeAuthStatus` returns the error. The `verifyClaudeAuthStatus` function was added AFTER this test was written.
**Classification:** STALE-TEST
**Fix:** Add a route to `mockSSM.routedOutputs` for `"claude auth status"` returning `{"loggedIn": true}`. Exact entry: `{cmdSubstr: "claude auth status", output: "{\"loggedIn\": true}"}`.

#### TEST-20: `TestAtList_WithRecords`
**File/line:** `at_test.go:449`
**Error:** `"expected schedule name in output, got: 'No scheduled operations.\n'"`
**Root cause:** The mock scan result has `cron_expr: "at(2026-04-04T09:00:00)"`. The `km at list` command (`at.go:607-614`) detects that one-time `at()` schedules whose time has passed are stale and auto-deletes them. April 4, 2026 is in the past (today is June 11, 2026). The record is cleaned up before display → `live` slice is empty → prints "No scheduled operations."
**Classification:** STALE-TEST
**Fix:** Update the mock `cron_expr` to a future date. Since any hardcoded future date will eventually become stale again, use a date far in the future like `"at(2099-12-31T09:00:00)"`.

#### TEST-21: `TestLoadEFSOutputs_NotExist`
**File/line:** `init_test.go:469`
**Error:** `"expected empty filesystem_id when file missing, got 'fs-0bd1911c39bff2d4d'"`
**Root cause:** `LoadEFSOutputs` (`init.go:2655-2678`) was enhanced with a fallback: when `efs/outputs.json` doesn't exist locally, it calls `fetchAndCacheOutputs` (`init.go:2688`) which loads the EFS Terraform state directly from S3. In the test environment with real AWS credentials, this SUCCEEDS and returns the real EFS filesystem ID. The test was written before this S3 fallback existed.
**Classification:** STALE-TEST
**Fix:** The test cannot easily mock `fetchAndCacheOutputs` (private). Update the assertion to accept either an empty string OR a valid EFS filesystem ID (format `"fs-[0-9a-f]+"`) — OR change the test's contract: `LoadEFSOutputs` with a temp dir now returns either empty (when S3 also has nothing) or a real ID (when S3 state is present). The test should be updated to only assert `err == nil` and not constrain the return value. If the test must assert empty, it needs AWS to not work — which requires environment isolation.

#### TEST-22: `TestLearnOutputPath`
**File/line:** `shell_learn_test.go:38`
**Error:** `"expected default learn-output to be 'observed-profile.yaml', got ''"`
**Root cause:** `NewShellCmdWithFetcher` (`shell.go:304`) registers `--learn-output` with default `""` (empty string): `cmd.Flags().StringVar(&learnOutput, "learn-output", "", "Path to write the generated SandboxProfile YAML (default: learned.<sandbox-id>.YYYYMMDDHHMMSS.yaml)")`. The default filename is DYNAMIC (computed from sandbox ID + timestamp at runtime via `DefaultLearnFilename`), not a static string. The test expected a static default `"observed-profile.yaml"` which was the original design before dynamic filenames were added.
**Classification:** STALE-TEST
**Fix:** Change `flag.DefValue != "observed-profile.yaml"` to `flag.DefValue != ""`. The flag's registered default is `""` by design; the actual output filename is computed dynamically.

---

## Triage Summary

| # | Test | File | Classification | One-Line Fix |
|---|------|------|----------------|--------------|
| 1 | TestShellDockerContainerName | shell_docker_test.go | STALE-TEST | Assert `"bash --login"` not `"/bin/bash"` |
| 2 | TestShellDockerNoRootFlag | shell_docker_test.go | STALE-TEST | Remove `-u` absent check; assert `-u sandbox` present |
| 3 | TestShellCmd_StoppedSandbox | shell_test.go | **ESCALATE** | Production silently discards pre-flight errors; user must decide fix |
| 4 | TestShellCmd_UnknownSubstrate | shell_test.go | **ESCALATE** | Same as #3 |
| 5 | TestShellCmd_MissingInstanceID | shell_test.go | **ESCALATE** | Same as #3 |
| 6 | TestEmailSend_SuccessNoAttachments | email_test.go | STALE-TEST | Add `km/` prefix to mock SSM key |
| 7 | TestEmailSend_TwoAttachments | email_test.go | STALE-TEST | Add `km/` prefix to mock SSM key |
| 8 | TestEmailSend_BodyFromStdin | email_test.go | STALE-TEST | Add `km/` prefix to mock SSM key |
| 9 | TestEmailRead_EncryptedMessageAutoDecrypts | email_test.go | STALE-TEST | Add `km/` prefix to SSM encryption key |
| 10 | TestUninitDestroyOrder | uninit_test.go | STALE-TEST | Add 3 new modules to wantOrder at correct positions |
| 11 | TestUninitContinuesPastModuleErrors | uninit_test.go | STALE-TEST | Same module list update |
| 12 | TestUninitDetectsBackendDrift | uninit_test.go | STALE-TEST | Same module list update |
| 13 | TestListCmd_EmptyStateBucketError | list_test.go | STALE-TEST | Remove/relax the nil error assertion |
| 14 | TestLockCmd_RequiresStateBucket | lock_test.go | STALE-TEST | Accept `"already locked"` not `"state bucket"` |
| 15 | TestUnlockCmd_RequiresStateBucket | unlock_test.go | STALE-TEST | Accept `"is not locked"` not `"state bucket"` |
| 16 | TestStatusCmd_EmptyStateBucketError | status_test.go | STALE-TEST | Accept `"sandbox not found"` as valid error |
| 17 | TestCreateDockerWritesComposeFile | create_docker_test.go | STALE-TEST | Remove `PLACEHOLDER_OPERATOR_KEY` check |
| 18 | TestApplyLifecycleOverrides_RunCreateRemoteSignature | create_override_test.go | STALE-TEST | Update signature string with budget params |
| 19 | TestRunAgentAuthClaude_TeesAndCleans | agent_auth_test.go | STALE-TEST | Add `claude auth status` → `{"loggedIn": true}` route |
| 20 | TestAtList_WithRecords | at_test.go | STALE-TEST | Change mock `cron_expr` date to `at(2099-12-31T09:00:00)` |
| 21 | TestLoadEFSOutputs_NotExist | init_test.go | STALE-TEST | Assert no error; don't constrain fsID to be empty |
| 22 | TestLearnOutputPath | shell_learn_test.go | STALE-TEST | Assert `flag.DefValue == ""` not `"observed-profile.yaml"` |

---

## ESCALATE Detail: Shell Pre-Flight Error Swallowing

**Affected tests:** TEST-3, TEST-4, TEST-5 (3 of 22)

**The issue:** `NewShellCmdWithFetcher` (`shell.go:234`) — the exported public constructor — has `RunE` that unconditionally returns `nil` in the non-learn path even when `runShellWithSSM` returns a pre-flight error (stopped sandbox, unsupported substrate, missing instance ARN). This means:

- `km shell sb-stopped` → exits 0, no error output
- `km shell sb-k8s` (unknown substrate) → exits 0, no error output
- `km shell sb-noinstance` (EC2 but no instance ARN) → exits 0, no error output

**Why it was done this way:** To avoid cobra printing a spurious error message after a normal SSM session exits (the SSM subprocess exit code would cause an error). Commit `aaf76a4f` message: "propagate runShell error in --learn".

**The private alternative:** `newShellCmdWithSSM` (`shell.go:164`) is private and does `return runErr`. Its comment at lines 207-211 says it is intended for tests that need pre-flight error propagation.

**Path to resolution (two options):**

Option A — Production code fix (correct but out of scope for test-hygiene phase):
In `NewShellCmdWithFetcher`'s RunE, distinguish pre-flight errors from session errors. Pre-flight errors (stopped, unknown substrate, missing resource) should be returned. Only session-exit errors (from `execFn` → the actual SSM/docker subprocess) should be swallowed. This requires categorizing the errors, which touches production code.

Option B — Weaken the tests (test-hygiene scope):
Accept that `NewShellCmdWithFetcher` returns nil in all non-learn cases. Remove the `t.Fatal("expected error")` assertions from the three tests. Instead verify that the command did NOT call `execFn` (i.e. no SSM command was built) — but this requires capturing `capturedArgs` length as 0. The test would become: "verify that a stopped sandbox attempt produces no SSM command, exit 0."

**User decision needed:** Does the project want to fix the production behavior (Option A, one small production code change warranted) or accept the silent failure (Option B, pure test weakening)?

---

## File/Subsystem Map for Parallel Planning

| File | Failing Tests | Independent? |
|------|--------------|--------------|
| `shell_docker_test.go` | TEST-1, TEST-2 | Yes |
| `shell_test.go` | TEST-3, TEST-4, TEST-5 | Yes (depends on `fakeFetcher` from `status_test.go` — read-only dependency, no change needed there) |
| `email_test.go` | TEST-6, TEST-7, TEST-8, TEST-9 | Yes |
| `uninit_test.go` | TEST-10, TEST-11, TEST-12 | Yes — all share same `wantOrder` pattern |
| `list_test.go` | TEST-13 | Yes |
| `lock_test.go` | TEST-14 | Yes |
| `unlock_test.go` | TEST-15 | Yes |
| `status_test.go` | TEST-16 | Yes (defines `fakeFetcher` used by shell tests — but we're not modifying `fakeFetcher`) |
| `create_docker_test.go` | TEST-17 | Yes |
| `create_override_test.go` | TEST-18 | Yes |
| `agent_auth_test.go` | TEST-19 | Yes |
| `at_test.go` | TEST-20 | Yes |
| `init_test.go` | TEST-21 | Yes |
| `shell_learn_test.go` | TEST-22 | Yes |

**Shared helper note:** `fakeFetcher` is defined in `status_test.go` and referenced from `shell_test.go`. The shell tests (TEST-3, TEST-4, TEST-5) use `fakeFetcher` but we are NOT modifying `fakeFetcher` — so no cross-file breakage risk.

**All 14 files are fully independent** for planning purposes. Each fix touches only its own test file with no cross-file propagation.

---

## Risk: Don't Break Green

**Currently-green tests requiring protection:**

1. `TestRunInitPlan_ModuleOrder` (`init_test.go`) — hardcodes `regionalModules()` count as 22. Our changes to `uninit_test.go` do NOT touch `init_test.go`, so this is safe.
2. `TestScoped*` (10 tests, `init_test.go`) — pure scoped-init logic, unrelated to uninit. Safe.
3. `TestShellDockerRootFlag` (`shell_docker_test.go`) — verifies `-u root`. Our TEST-2 fix changes the non-root test assertion but does not affect the root test. Safe.
4. `TestShellDockerRouting` (`shell_docker_test.go`) — verifies docker routing. Our TEST-1 fix only changes the `/bin/bash` assertion to `bash --login`. Safe.
5. `TestCheckSandboxLock_FailOpenEmptyBucket` (`lock_test.go`) — tests that `CheckSandboxLock` with empty bucket returns nil. This test passes today and our fix to TEST-14 is in a different test function. Safe.

**Verification gate:** After each test-file fix, run:
```bash
go test ./internal/app/cmd/ -count=1 -timeout 600s -run "<fixed test>" -v
```
And before declaring the phase complete:
```bash
go test ./internal/app/cmd/ -count=1 -timeout 600s -run "TestScoped|TestRunInitPlan_ModuleOrder" -v
```

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` (stdlib) |
| Config file | none — `go test` standard |
| Quick run command (single test) | `go test ./internal/app/cmd/ -count=1 -timeout 60s -run <TestName> -v` |
| Full suite command | `go test ./internal/app/cmd/ -count=1 -timeout 600s 2>&1` |
| Exit code capture | `go test ... ; echo "EXIT=$?"` OR `set -o pipefail && go test ... \| grep ...` |

**Critical:** The full suite takes 400-600 seconds due to real AWS calls in many tests. Always capture go test's OWN exit code — never pipe through `tail` or `grep` without `set -o pipefail` or `PIPESTATUS`.

### Before/After Measurement

**Before (baseline, confirmed):**
```
--- FAIL: 22 tests (see list above)
EXIT=1
```

**After (target):**
```
--- FAIL: (0 tests, or only the 3 ESCALATE if user chooses Option B)
EXIT=0 (or EXIT=1 only if ESCALATE tests are left as explicit skips)
```

### Per-Phase Gate (sampling rate per wave)

- **Per-task verification:** `go test ./internal/app/cmd/ -count=1 -timeout 60s -run "<affected test>" -v`
- **Per-file wave verification:** run the full file's tests: `go test ./internal/app/cmd/ -count=1 -timeout 120s -run "<all tests in file>" -v`
- **Phase gate before close:** `go test ./internal/app/cmd/ -count=1 -timeout 600s` — exit 0 required

### Wave 0 Gaps

None — all test infrastructure exists. No new test files, no framework changes, no shared fixture changes needed.

### Guard: No Production Code Regression

- Confirm each changed test still exercises the same production function
- Run `git diff --stat` — diff must be confined to `*_test.go` files only (except if ESCALATE Option A is chosen, which requires explicit user sign-off)
- For the `uninit_test.go` wantOrder fix: cross-check the inserted module names against `regionalModules()` in `init.go` to confirm correct position in reversed order

---

## Architecture Patterns

### Pattern: Source-scan test (brittle)

Tests `TestCreateDockerWritesComposeFile` and `TestApplyLifecycleOverrides_RunCreateRemoteSignature` do `os.ReadFile("create.go")` and `strings.Contains` on the source text. These will break again whenever the function signature changes. The planner should note these as HIGH MAINTENANCE and add a comment in the test explaining what to update when the signature changes.

### Pattern: Mock SSM key path

The `emailMockSSM` mock (`email_test.go`) routes on exact SSM parameter paths. These must match `SigningKeyPath(prefix, id)` and `EncryptionKeyPath(prefix, id)` from `pkg/aws/identity.go`. When `ResourcePrefix` is the default (`""` in Config → falls back to `"km"`), all paths are `/{km}/sandbox/{id}/...`.

### Pattern: Time-dependent stale schedule cleanup

`km at list` silently deletes any `at()` schedule whose fire time has passed. Any test that exercises the list command with a mock DynamoDB record carrying an `at()` `cron_expr` must use a future date (at least a few years out).

---

## Open Questions

1. **ESCALATE decision (3 shell tests):** Should the production `NewShellCmdWithFetcher` be fixed to propagate pre-flight errors (stopped/unknown/missing), or should the tests be weakened to accept nil? The planner needs a user decision before creating the shell_test.go plan.

2. **`TestLoadEFSOutputs_NotExist` fix strategy:** The simplest fix (remove fsID != "" assertion) means the test no longer verifies the "not-exist = empty" contract. An alternative is to inject a failing AWS config so `fetchAndCacheOutputs` returns an error (and therefore `LoadEFSOutputs` returns `""`, nil). The project's AWS profile is `klanker-application`; setting a bogus profile in the test would prevent S3 access. The planner should pick the approach that keeps the test environment-independent.

---

## Sources

**PRIMARY (HIGH confidence):**
- Live test runs: `go test ./internal/app/cmd/ -count=1 -timeout 600s -v 2>&1` — all 22 failures confirmed with exact assertion messages
- Production source: `internal/app/cmd/shell.go`, `email.go`, `init.go`, `list.go`, `lock.go`, `unlock.go`, `at.go`, `create.go`, `agent_auth.go`
- Production source: `pkg/aws/identity.go` (SigningKeyPath, EncryptionKeyPath), `pkg/aws/sandbox_dynamo.go` (LockSandboxDynamo, UnlockSandboxDynamo), `pkg/aws/schedules_dynamo.go` (ListScheduleRecords stale-record cleanup)

**METADATA:**
- Confidence breakdown: Standard stack: N/A | Architecture: HIGH (live code) | Pitfalls: HIGH (live runs)
- Research date: 2026-06-11
- Valid until: indefinitely (findings are code-level, not ecosystem-level)
