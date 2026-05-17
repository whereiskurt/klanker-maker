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
