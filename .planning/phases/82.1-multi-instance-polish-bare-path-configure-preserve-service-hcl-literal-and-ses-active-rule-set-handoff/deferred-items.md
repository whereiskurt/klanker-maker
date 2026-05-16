# Deferred Items

## Pre-existing test failure (out of scope)

**Test:** `TestUnlockCmd_RequiresStateBucket` in `internal/app/cmd/unlock_test.go:73`

**Status:** Failing before Plan 82.1-01 changes. Not caused by configure.go / configure_test.go edits.

**Symptom:** `error should mention 'state bucket', got: sandbox sb-aabbccdd is not locked`

**Discovery:** During Plan 82.1-01 full-package regression run.

**Action required:** Separate investigation of unlock_test.go and unlock command behavior.
