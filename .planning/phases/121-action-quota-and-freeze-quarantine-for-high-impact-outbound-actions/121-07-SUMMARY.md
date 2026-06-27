---
phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
plan: 07
subsystem: compiler
tags: [quota, limits, userdata, proxy, dynamodb, compiler, action-quota]

# Dependency graph
requires:
  - phase: 121-02
    provides: "quota.ResolveLimits — per-window profile-wins merge of quota.Limits maps"
  - phase: 121-03
    provides: "profile.LimitsSpec / profile.ActionLimitSpec YAML types + config.LimitsConfig"
  - phase: 121-04
    provides: "UpdateSandboxStringAttrDynamo + SandboxMetadata.ActionLimits round-trip"
provides:
  - "Compiler resolves profile.Limits merged with install defaults → quota.Limits via ResolveLimits"
  - "Proxy receives KM_QUOTA_TABLE + KM_ACTION_LIMITS in systemd drop-in (quota.conf)"
  - "Bridges receive resolved JSON via km-sandboxes.action_limits attr (written at km create)"
  - "profileLimitsToQuota helper (pkg/compiler): profile.LimitsSpec → quota.Limits"
  - "installLimitsToQuota helper (cmd layer): config.LimitsConfig → quota.Limits"
  - "NetworkConfig.InstallLimits carries install defaults into generateUserData"
  - "True dormancy: no limits configured → no env vars emitted, no attr written"
affects:
  - "121-05 (proxy chokepoint) — proxy now receives KM_ACTION_LIMITS from userdata"
  - "121-08 (alerter Lambda) — can read action_limits from km-sandboxes via FetchByChannel"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "NetworkConfig extension pattern: new fields on NetworkConfig carry compile-time config to generateUserData without signature change"
    - "TDD pattern: RED commit (failing test) → GREEN commit (implementation) within a single plan task"
    - "Dormancy guard pattern: non-empty check before emitting env vars prevents byte-identical golden regression"
    - "Two-layer quota helper pattern: profileLimitsToQuota in compiler, profileLimitsToQuotaCreate in cmd layer — avoids import cycle"

key-files:
  created:
    - pkg/compiler/userdata_quota_test.go
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/service_hcl.go
    - internal/app/cmd/create.go

key-decisions:
  - "Use NetworkConfig.InstallLimits (not a new parameter) to thread install defaults into generateUserData — consistent with existing EmailDomain, ArtifactsBucket, Alias pattern"
  - "Two separate conversion helpers (compiler layer vs cmd layer) to avoid import cycle between pkg/compiler and internal/app/config"
  - "Quota drop-in named quota.conf, adjacent to budget.conf — both restart km-http-proxy"
  - "Non-fatal UpdateSandboxStringAttrDynamo: sandbox provisioned even if action_limits write fails (matches existing metadata write pattern)"

patterns-established:
  - "profileLimitsToQuota: canonical profile.LimitsSpec → quota.Limits conversion in pkg/compiler"
  - "installLimitsToQuota: canonical config.LimitsConfig → quota.Limits conversion in cmd layer"
  - "Proxy env drop-in: {{ if .ActionLimitsJSON }} guard prevents emission when dormant"

requirements-completed: [CMP-01]

# Metrics
duration: 12min
completed: 2026-06-27
---

# Phase 121 Plan 07: Compiler Delivery Layer Summary

**Resolved action-limit map wired to both runtime chokepoints at km create: proxy via KM_QUOTA_TABLE + KM_ACTION_LIMITS systemd drop-in, bridges via km-sandboxes.action_limits DDB attr — dormant when no limits configured (byte-identical userdata)**

## Performance

- **Duration:** 12 min
- **Started:** 2026-06-27T13:06:43Z
- **Completed:** 2026-06-27T13:19:19Z
- **Tasks:** 2 (TDD task 1 + task 2)
- **Files modified:** 4

## Accomplishments

- Compiler resolves `profile.Limits` merged with `NetworkConfig.InstallLimits` via `quota.ResolveLimits` and emits the result into the http-proxy systemd drop-in as `KM_QUOTA_TABLE` and `KM_ACTION_LIMITS`
- Proxy drop-in emission is guarded by `{{ if .ActionLimitsJSON }}` — no limits configured means no lines emitted, no drop-in written, no proxy restart (true dormancy)
- `km create` writes the same resolved JSON to `km-sandboxes.action_limits` so Slack/H1 bridges can read it at dispatch time via `FetchByChannel`
- All `pkg/compiler` golden tests remain GREEN with no byte-identity regression

## Task Commits

Each task was committed atomically:

1. **TDD RED — TestActionLimitsEmission (failing)** - `dfdf4ffe` (test)
2. **Task 1: Compiler resolves limits + emits proxy userdata env (CMP-01)** - `c6e87a1b` (feat)
3. **Task 2: km create writes resolved action_limits to sandbox row** - `6b6507b4` (feat)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata_quota_test.go` — TestActionLimitsEmission (CMP-01): 4 subtests covering limits-present, dormant, install-defaults-only, profile-wins-over-install
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go` — Added QuotaTable + ActionLimitsJSON to userDataParams; profileLimitsToQuota helper; quota.conf drop-in template block; resolution logic in generateUserData
- `/Users/khundeck/working/klankrmkr/pkg/compiler/service_hcl.go` — Added InstallLimits quota.Limits to NetworkConfig; added quota import
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/create.go` — installLimitsToQuota + profileLimitsToQuotaCreate + resolveActionLimitsJSON helpers; network.InstallLimits population; UpdateSandboxStringAttrDynamo call after WriteSandboxMetadataDynamo

## Decisions Made

- `NetworkConfig.InstallLimits` chosen over a new `generateUserData` parameter to avoid changing the signature and breaking all existing test call sites (the `nil` network case already exists and works for tests that don't need install defaults)
- Two mirrored conversion helpers (compiler vs cmd layer) instead of a shared function to avoid the import cycle `internal/app/cmd → pkg/compiler → (already imports profile)` — the helpers are ~15 lines each and stay in sync naturally
- Quota drop-in in a separate `quota.conf` file (not merged into `budget.conf`) so the two features can be deployed/disabled independently

## Deviations from Plan

None — plan executed exactly as written. The only micro-deviation was adding `VSCodeSSHPubKey` to NetworkConfig in test cases that pass a non-nil network (Rule 3 auto-fix: test infrastructure requirement surfaced during GREEN phase).

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Added VSCodeSSHPubKey to NetworkConfig in install-defaults and profile-wins tests**
- **Found during:** Task 1 (TestActionLimitsEmission GREEN phase)
- **Issue:** `generateUserData` validates that VSCodeSSHPubKey is non-empty when VSCode is enabled (the default). Tests passing a non-nil NetworkConfig without the key failed with "VSCodeEnabled=true but VSCodeSSHPubKey is empty"
- **Fix:** Added `VSCodeSSHPubKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 km-test-key"` to NetworkConfig in the two affected subtests (matching the established pattern from other tests)
- **Files modified:** pkg/compiler/userdata_quota_test.go
- **Verification:** All 4 TestActionLimitsEmission subtests pass GREEN
- **Committed in:** c6e87a1b (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 3 — blocking test infrastructure)
**Impact on plan:** Minimal — single line addition per test case, expected pattern per existing tests.

## Issues Encountered

None — all compilation and test runs passed cleanly.

## Next Phase Readiness

- Proxy (Plan 05) already has the table/action taxonomy plumbed; it now receives `KM_QUOTA_TABLE` and `KM_ACTION_LIMITS` at sandbox create time and can call `quota.Record` for each GitHub/email request
- Slack/H1 bridges (Plan 06) already have `action_limits` wired via `FetchByChannel` → `SandboxRoutingInfo`; the DDB attr is now populated by `km create`
- Alerter Lambda (Plan 08) can read `action_limits` from the sandbox row to determine which limits are active
- Deploy: `make build-lambdas` + `km init --dry-run=false` (create-handler carries new userdata quota drop-in); existing sandboxes need `km destroy && km create` to gain the drop-in and the `action_limits` attr

---
*Phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions*
*Completed: 2026-06-27*
