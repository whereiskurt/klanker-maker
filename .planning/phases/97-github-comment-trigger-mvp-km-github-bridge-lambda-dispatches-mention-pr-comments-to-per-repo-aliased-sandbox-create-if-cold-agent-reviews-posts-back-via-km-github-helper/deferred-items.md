# Deferred Items — Phase 97

## Pre-existing failing test (out of scope for Plan 06)

**Test:** `TestGoSourceNamesUseResourcePrefix` in `pkg/hygiene/`

**Issue:** 5 hardcoded `km-` name-construction sites in `doctor_artifacts.go:351`,
`doctor_log_groups.go:62,68,74,80` not in the hygiene allowlist. These are pre-existing
(verified via `git stash` before Plan 06 work). All sites are in files not touched by Plan 06.

**Status:** Pre-existing; not caused by Plan 06 changes. Deferred to a hygiene cleanup plan.

---

## Pre-existing failing test (out of scope for Plan 01)

**Test:** `TestRunDestroy_GitHubTokenCleanup` in `internal/app/cmd/destroy_test.go:17`

**Issue:** Test checks for literal string `/sandbox/%s/github-token` in destroy.go but
the committed destroy.go uses `awspkg.SandboxParameterPath(...)` which generates the path
dynamically. The test was already failing before Plan 01 work began (verified via `git stash`).

**Status:** Pre-existing; not caused by Plan 01 changes. Will be addressed in a future plan
when destroy.go is updated to add the literal format string or the test is updated to match
the production code's helper-based pattern.
