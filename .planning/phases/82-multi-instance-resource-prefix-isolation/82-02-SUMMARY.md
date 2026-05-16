---
phase: 82-multi-instance-resource-prefix-isolation
plan: "02"
subsystem: multi-instance-isolation
tags:
  - resource-prefix
  - table-names
  - hard-fail
  - sidecar-lambdas
  - userdata-compiler
dependency_graph:
  requires:
    - "82-01 (resource_prefix foundation)"
  provides:
    - "configui exits non-zero on missing KM_BUDGET_TABLE"
    - "km-slack-bridge exits non-zero on missing KM_SLACK_THREADS_TABLE"
    - "userdata.go uses prefix-derived table names (3 sites)"
  affects:
    - "cmd/configui (Lambda cold-start behavior)"
    - "cmd/km-slack-bridge (Lambda cold-start behavior)"
    - "pkg/compiler/userdata.go (template generation)"
tech_stack:
  added: []
  patterns:
    - "pure-function extraction for testable hard-fail (resolveBudgetTable / resolveThreadsTable)"
    - "init() vs main() split to avoid os.Exit during test init"
    - "t.Setenv for prefix-scoped unit tests"
key_files:
  created:
    - cmd/configui/main_test.go
    - pkg/compiler/userdata_82_02_test.go
    - .planning/phases/82-multi-instance-resource-prefix-isolation/deferred-items.md
  modified:
    - cmd/configui/main.go
    - cmd/km-slack-bridge/main.go
    - cmd/km-slack-bridge/main_test.go
    - pkg/compiler/userdata.go
decisions:
  - "Moved EventsHandler wiring from init() to wireEventsHandler() called from main() — init() in Go test builds runs before TestMain, so os.Exit in init() would kill the test binary before any test ran; main() is never called by test builds"
  - "Used resourcePrefix + '-slack-threads' / '-slack-stream-messages' (env+prefix-compute) rather than threading cfg *config.Config into generateUserData — the function already derives resourcePrefix from KM_RESOURCE_PREFIX; cfg is not in the signature and threading it has blast radius across many test callers"
  - "service_hcl.go:784 literal deferred — out of scope for this plan (plan scoped to 3 userdata.go sites only); logged to deferred-items.md"
metrics:
  duration: "~30 minutes"
  completed: "2026-05-16"
  tasks: 2
  files_modified: 4
  files_created: 3
---

# Phase 82 Plan 02: Hard-fail sidecar Lambdas + prefix-aware userdata table names

One-liner: Replaced four silent km-literal fallbacks with hard-fail startup validation (configui, km-slack-bridge) and prefix-derived table names (userdata.go three sites) so a second install never cross-routes to the default install's tables.

## What Was Built

### Task 1: Hard-fail configui + km-slack-bridge on missing env (commit 25700f2)

**cmd/configui/main.go:**
- Extracted `resolveBudgetTable(getenv func(string) (string, bool), exit func(int)) string` — the testable core of `budgetTableName()`
- `budgetTableName()` now calls `resolveBudgetTable` with `os.Getenv` and `os.Exit`
- Added `"log/slog"` import
- When `KM_BUDGET_TABLE` is unset: `slog.Error("KM_BUDGET_TABLE not set; cannot determine budget table name")` then `os.Exit(1)`

**cmd/km-slack-bridge/main.go:**
- Extracted `resolveThreadsTable(getenv func(string) (string, bool), exit func(int)) string`
- Moved EventsHandler wiring from `init()` to `wireEventsHandler()`, called from `main()`; package-level vars (`initDDB`, `initSSMC`, `initS3Client`, `initSQSClient`, `initPoster`, `initToken`, `initHTTPClient`, `initNonces`) bridge init() to wireEventsHandler()
- When `KM_SLACK_THREADS_TABLE` is unset: `slog.Error("KM_SLACK_THREADS_TABLE not set; refusing to start with stale default")` then `os.Exit(1)`

**Tests:**
- `cmd/configui/main_test.go::TestMain_RequiresBudgetTable` — 2 sub-tests, both pass
- `cmd/km-slack-bridge/main_test.go::TestMain_RequiresThreadsTable` — 2 sub-tests, both pass

### Task 2: Replace 3 literal table-name defaults in userdata.go (commit 223c59b)

**pkg/compiler/userdata.go:**
- Site 1 (~3330): `threadsTable = "km-slack-threads"` → `threadsTable = resourcePrefix + "-slack-threads"` (notifyEnv block, NotifySlackInboundEnabled branch)
- Site 2 (~3346): `threadsTable = "km-slack-threads"` → `threadsTable = resourcePrefix + "-slack-threads"` (params.SlackThreadsTableName assignment)
- Site 3 (~3361): `streamTable = "km-slack-stream-messages"` → `streamTable = resourcePrefix + "-slack-stream-messages"` (unconditional streamTable block)

`resourcePrefix` is already computed at line 3212 from `os.Getenv("KM_RESOURCE_PREFIX")` with default `"km"`, so the default case (`km` prefix) continues to produce `"km-slack-threads"` / `"km-slack-stream-messages"` — no existing test regression.

**Tests:**
- `pkg/compiler/userdata_82_02_test.go::TestCompile_SlackInboundTableName` — `t.Setenv("KM_RESOURCE_PREFIX", "rg")` with inbound-enabled profile; asserts `rg-slack-threads` present, `km-slack-threads` absent
- `pkg/compiler/userdata_82_02_test.go::TestCompile_SlackStreamTableName` — same with transcript-enabled profile; asserts `rg-slack-stream-messages`

## Decisions Made

1. **init() vs main() split** — `resolveThreadsTable` (which calls `os.Exit`) was moved from `init()` to `wireEventsHandler()` called from `main()`. Go's init ordering guarantees that non-test-file inits run before test-file inits; TestMain runs after all init() functions. Since test builds never call `main()`, the os.Exit validation is only reachable in the real Lambda binary.

2. **env+prefix-compute over cfg threading** — The plan gave two options: thread `cfg *config.Config` into `generateUserData` (touching many callers), or use the already-available `resourcePrefix` local variable. Used the latter; `generateUserData` already computes `resourcePrefix = os.Getenv("KM_RESOURCE_PREFIX") || "km"` at line 3212.

3. **service_hcl.go literal deferred** — `pkg/compiler/service_hcl.go:784` has the identical `streamTable = "km-slack-stream-messages"` pattern but was outside the plan's stated scope of 3 userdata.go sites. Logged to `deferred-items.md`.

## Deviations from Plan

### Auto-fixed Issues

**[Rule 3 - Blocking] Moved EventsHandler wiring to main() to avoid os.Exit in init()**
- **Found during:** Task 1 GREEN phase
- **Issue:** `init()` in Go test builds runs before `TestMain`. Adding `os.Exit(1)` to `init()` caused the test binary to exit before any test ran (confirmed: `=== RUN` lines never appeared). Moving the validation to `main()` — which test builds never call — fixes this without any API change.
- **Fix:** Introduced `wireEventsHandler()` function, package-level `init*` vars (`initDDB`, `initSSMC`, `initS3Client`, `initSQSClient`, `initPoster`, `initToken`, `initHTTPClient`, `initNonces`), and called `wireEventsHandler()` from `main()` before `lambda.Start`.
- **Files modified:** `cmd/km-slack-bridge/main.go`
- **Commit:** 25700f2

### Out-of-scope Pre-existing Failures

The following test failures were confirmed pre-existing (existed before Plan 82-02 changes) and are out of scope:
- `TestHandleValidate_ValidYAML` (configui) — profile schema validation error for `spec.sourceAccess.github`
- `TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock`, `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`, etc. (pkg/compiler) — unrelated notify-hook and userdata failures

## Self-Check: PASSED

- cmd/configui/main_test.go: FOUND
- cmd/km-slack-bridge/main.go: FOUND
- pkg/compiler/userdata_82_02_test.go: FOUND
- commit 25700f2: FOUND
- commit 223c59b: FOUND
