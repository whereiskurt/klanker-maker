---
phase: 98-github-bridge-expansion
plan: 00
subsystem: testing
tags: [github, dynamodb, terraform, tdd, nyquist, bridge, check-runs, pr-create, thread-store]

requires:
  - phase: 97-github-app-bridge
    provides: km-github-bridge Lambda, km-github CLI comment/review, EventBridgeAdapter cold path

provides:
  - RED unit tests for all Phase 98 behaviors (GH-X-CHECK, GH-X-PRCREATE, GH-X-CONTINUITY, GH-X-THREADBYPASS, GH-X-RESUME, GH-X-SHARED, GH-COLD-CREATE)
  - DynamoDB km-github-threads TF module (hash=repo S, range=number N)
  - Live terragrunt unit for dynamodb-github-threads in use1/
  - Guard test TestRegionalModulesIncludesGitHubThreads (RED until 98-04)

affects:
  - 98-01 (implements runCheck/runPRCreate to go GREEN on check_test.go / prcreate_test.go)
  - 98-02 (implements DynamoGitHubThreadStore, GitHubThreadStore interface, WebhookHandler.Threads field)
  - 98-04 (adds dynamodb-github-threads to regionalModules(), implements Resumer/ResolveByAliasWithStatus, fixes EventBridgeAdapter, preStageGitHubProfiles)

tech-stack:
  added: []
  patterns:
    - "phase98_wave0 build tag isolates RED test scaffolding from normal builds"
    - "DynamoDB threads table uses hash=repo(S)+range=number(N) — NOT Slack's two-string schema"
    - "TestRegionalModulesIncludesGitHubThreads runs untagged in normal suite to stay RED as a continuous gate"

key-files:
  created:
    - cmd/km-github/check_test.go
    - cmd/km-github/prcreate_test.go
    - pkg/github/bridge/thread_store_test.go
    - pkg/github/bridge/resolve_phase98_test.go
    - pkg/github/bridge/webhook_handler_phase98_test.go
    - pkg/github/bridge/aws_adapters_test.go
    - internal/app/cmd/init_github_prestage_test.go
    - infra/modules/dynamodb-github-threads/v1.0.0/main.tf
    - infra/modules/dynamodb-github-threads/v1.0.0/variables.tf
    - infra/modules/dynamodb-github-threads/v1.0.0/outputs.tf
    - infra/live/use1/dynamodb-github-threads/terragrunt.hcl
  modified:
    - internal/app/cmd/init_test.go

key-decisions:
  - "Build tag phase98_wave0 guards all RED tests so go build ./... stays green throughout Phase 98 execution"
  - "DynamoDB threads table key schema: hash=repo(S), range=number(N) — number is N not S (differs from Slack threads)"
  - "TestRegionalModulesIncludesGitHubThreads is NOT behind a build tag — runs in normal suite to prevent silent non-deployment (Phase 97 footgun pattern)"
  - "runPRCreateWith signature includes stdout io.Writer (8th param) to enable html_url capture in tests — extends the *With pattern"
  - "resolve_phase98_test.go is a characterization test (Resolve already supports shared alias); tagged for wave isolation only"

patterns-established:
  - "Wave 0 Nyquist pattern: all Phase 98 tests written BEFORE implementation; each references not-yet-existing symbols"
  - "Module guard tests run in normal suite untagged to be continuously RED until deploy wiring is done"

requirements-completed: [GH-X-CHECK, GH-X-PRCREATE, GH-X-CONTINUITY, GH-X-THREADBYPASS, GH-X-SHARED, GH-X-RESUME, GH-COLD-CREATE]

duration: 10min
completed: 2026-06-07
---

# Phase 98 Plan 00: Nyquist Wave 0 Summary

**8 RED test files + km-github-threads DynamoDB TF module establish the complete Phase 98 feedback sampling gate before any implementation ships**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-06-07T14:34:20Z
- **Completed:** 2026-06-07T14:44:00Z
- **Tasks:** 3
- **Files modified/created:** 12

## Accomplishments

- All 8 test files from 98-VALIDATION.md § Wave 0 Requirements exist behind `//go:build phase98_wave0` or in the normal suite
- Every Phase 98 unit test references a not-yet-implemented symbol and is RED under `go test -tags phase98_wave0`
- km-github-threads TF module (hash=repo S, range=number N) + live terragrunt unit created and `terraform fmt` clean
- `TestRegionalModulesIncludesGitHubThreads` added to `init_test.go` without a build tag — RED in the normal suite as a continuous deploy gate

## Task Commits

1. **Task 1: RED tests for km-github check + pr create verbs** - `0047b087` (test)
2. **Task 2: RED tests for bridge thread store, bypass, resume, cold-create** - `d6db6d9c` (test)
3. **Task 3: km init RED guards + km-github-threads TF module + live unit** - `2fc7a023` (test+infra)

## Files Created/Modified

- `cmd/km-github/check_test.go` — TestCheck, TestCheck_BadConclusion, TestCheck_MissingHeadSHA, TestCheck_MissingRequired (GH-X-CHECK)
- `cmd/km-github/prcreate_test.go` — TestPRCreate, TestPRCreate_EmptyBody, TestPRCreate_MissingRequired (GH-X-PRCREATE)
- `pkg/github/bridge/thread_store_test.go` — TestGitHubThreadStore_{Upsert,LookupSandbox_Found,LookupSandbox_NotFound,UpdateSession} (GH-X-CONTINUITY)
- `pkg/github/bridge/resolve_phase98_test.go` — TestResolve_SharedAlias (GH-X-SHARED; characterization test)
- `pkg/github/bridge/webhook_handler_phase98_test.go` — TestHandle_ThreadBypass (GH-X-THREADBYPASS) + TestHandle_AutoResume (GH-X-RESUME)
- `pkg/github/bridge/aws_adapters_test.go` — TestEventBridgeAdapter_SandboxID + TestEventBridgeAdapter_ArtifactPrefix (GH-COLD-CREATE)
- `internal/app/cmd/init_github_prestage_test.go` — TestPreStageGitHubProfiles_* (GH-COLD-CREATE; tagged)
- `internal/app/cmd/init_test.go` — added TestRegionalModulesIncludesGitHubThreads (untagged, RED)
- `infra/modules/dynamodb-github-threads/v1.0.0/main.tf` — DynamoDB table resource
- `infra/modules/dynamodb-github-threads/v1.0.0/variables.tf` — table_name + tags variables
- `infra/modules/dynamodb-github-threads/v1.0.0/outputs.tf` — table_name + table_arn outputs
- `infra/live/use1/dynamodb-github-threads/terragrunt.hcl` — live unit with state key + module source

## Decisions Made

- **Build tag isolation**: `//go:build phase98_wave0` on all RED test files so `go build ./...` stays green throughout Phase 98 execution. 98-01 through 98-04 remove the tags as they implement each feature.
- **DDB schema**: `hash=repo(S), range=number(N)` — number is type N (integer), not S. Differs intentionally from Slack threads (channel_id+thread_ts, both S). GitHub PR numbers are natural integers.
- **runPRCreateWith stdout param**: Extended the `*With` pattern with an 8th `stdout io.Writer` param to capture `html_url` output in tests. Documented in the handoff comment.
- **Guard test untagged**: `TestRegionalModulesIncludesGitHubThreads` runs in the normal suite to continuously signal the missing `regionalModules()` entry — same pattern as `TestRegionalModulesIncludesGitHubBridge` that closed the Phase 97 deploy footgun.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed containsStr collision with roll_test.go**
- **Found during:** Task 3 (init_github_prestage_test.go compilation)
- **Issue:** `containsStr(haystack []string, needle string)` collided with `containsStr(s, sub string)` in roll_test.go (different signature; both in `cmd_test` package)
- **Fix:** Renamed to `containsKey` in init_github_prestage_test.go
- **Files modified:** internal/app/cmd/init_github_prestage_test.go
- **Committed in:** 2fc7a023 (Task 3 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 — naming collision)
**Impact on plan:** Minimal; only affects test helper function name, no behavior change.

## Issues Encountered

None beyond the naming collision above.

## Next Phase Readiness

- 98-01 can immediately implement `runCheck`/`runPRCreate` in `cmd/km-github/main.go` and remove `//go:build phase98_wave0` from `check_test.go` and `prcreate_test.go`
- 98-02 can implement `DynamoGitHubThreadStore`, `GitHubThreadStore` interface, and `WebhookHandler.Threads` field
- 98-04 can add `dynamodb-github-threads` to `regionalModules()` (will turn `TestRegionalModulesIncludesGitHubThreads` GREEN), fix `EventBridgeAdapter`, implement `preStageGitHubProfiles`
- No blockers for any downstream plan

---
*Phase: 98-github-bridge-expansion*
*Completed: 2026-06-07*
