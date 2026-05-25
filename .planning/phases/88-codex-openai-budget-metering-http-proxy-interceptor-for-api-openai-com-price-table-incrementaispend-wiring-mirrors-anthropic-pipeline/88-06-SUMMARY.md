---
phase: 88-codex-openai-budget-metering-http-proxy-interceptor-for-api-openai-com-price-table-incrementaispend-wiring-mirrors-anthropic-pipeline
plan: "06"
subsystem: infra
tags: [ebpf, openai, codex, l7-proxy, budget-metering, enforcement]

# Dependency graph
requires:
  - phase: 88-03
    provides: "RED tests: TestL7ProxyHostsWithCodex and TestL7ProxyHostsWithCodexAndBedrock in pkg/compiler/userdata_test.go"
provides:
  - "buildL7ProxyHosts appends api.openai.com when spec.cli.agent == codex"
  - "connect4 DNAT redirect for OpenAI traffic in enforcement: ebpf | both modes"
  - "Wave 1 complete: 88-04 + 88-05 + 88-06 all landed for Codex budget metering"
affects:
  - "Wave 2 deploy + UAT plans (88-07+)"
  - "Any plan that relies on OpenAI traffic flowing through MITM proxy for codex sandboxes"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "L7 proxy host gate: conditional append based on profile feature flag (agent type)"
    - "nil-safe CLI spec check: p.Spec.CLI != nil && p.Spec.CLI.Agent == value"

key-files:
  created: []
  modified:
    - pkg/compiler/userdata.go

key-decisions:
  - "Gate on p.Spec.CLI != nil check before Agent comparison — CLISpec pointer may be nil for profiles without CLI block"
  - "Appended api.openai.com after existing Bedrock branch to preserve Pitfall #5 constraint (useBedrock branch left byte-unchanged)"
  - "Non-codex profiles hitting OpenAI directly (raw OpenAI SDK in a Claude sandbox) deferred to follow-up phase per RESEARCH.md § Open Questions #2"

patterns-established:
  - "L7 host order: GitHub, Bedrock/Anthropic, OpenAI — documented in NOTE comment for future refactor detection"

requirements-completed:
  - OAI-BUDGET-07

# Metrics
duration: 2min
completed: "2026-05-25"
---

# Phase 88 Plan 06: Codex L7 Proxy Host Gate Summary

**Added api.openai.com to buildL7ProxyHosts gated on spec.cli.agent==codex, routing OpenAI traffic through connect4 DNAT and the MITM meter in ebpf/both enforcement modes**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-05-25T22:22:47Z
- **Completed:** 2026-05-25T22:24:08Z
- **Tasks:** 1
- **Files modified:** 1

## Accomplishments
- Added a nil-safe Codex agent gate to `buildL7ProxyHosts` in `pkg/compiler/userdata.go`
- TestL7ProxyHostsWithCodex: RED → GREEN
- TestL7ProxyHostsWithCodexAndBedrock: RED → GREEN
- All 4 pre-existing L7 host tests remain GREEN
- Existing useBedrock branch left byte-unchanged (Researcher Pitfall #5 honored)
- Wave 1 complete: 88-04 (openai.go) + 88-05 (proxy.go + transparent.go) + 88-06 (userdata.go)

## Task Commits

1. **Task 1: Add Codex gate to buildL7ProxyHosts** - `ca09c43` (feat)

**Plan metadata:** (docs commit — see final commit)

## Files Created/Modified
- `pkg/compiler/userdata.go` - Added Codex agent conditional to buildL7ProxyHosts (10 insertions)

## Decisions Made
- Used `p.Spec.CLI != nil && p.Spec.CLI.Agent == "codex"` (nil-safe) instead of bare `p.Spec.CLI.Agent == "codex"` — CLISpec is a pointer and can be nil for profiles without a CLI block; bare access would panic at runtime
- Kept the new branch AFTER the existing useBedrock block to honor Pitfall #5 and preserve the exact byte content of the existing Anthropic gate
- Added a `// NOTE:` comment documenting host order (GitHub, Bedrock/Anthropic, OpenAI) as an anchor for future plan-checkers

## Deviations from Plan

None - plan executed exactly as written, with one minor deviation: the plan showed `p.Spec.CLI.Agent == "codex"` but the CLISpec field is a pointer, so `p.Spec.CLI != nil &&` was added as a nil guard. This is a correctness requirement (Rule 2 — missing null check) rather than a scope change, and it does not affect any test expectations since all tests that set `p.Spec.CLI` provide a non-nil value.

## Issues Encountered
- Pre-existing unrelated failures in the full compiler suite (TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock, TestUserDataNotifyEnv_NoChannelOverride_NoChannelID, TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime, TestUserDataKMTracingServicectlStart, TestAuditHookNonBlocking, TestGitHubUserDataGITASKPASS) — confirmed pre-existing via git stash; out of scope for this plan.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Wave 1 complete: all three compiler/sidecar changes for Codex budget metering have landed
- Ready for Wave 2: deploy (km init --sidecars) + UAT verification
- Operator must run `km init --sidecars` to push updated sidecars to management Lambda

## Self-Check: PASSED
- `pkg/compiler/userdata.go` modified and committed (ca09c43)
- `grep -n "api.openai.com" pkg/compiler/userdata.go` returns 2 matches (comment + code)
- `go test ./pkg/compiler/ -run TestL7Proxy -count=1` → 6 PASS
- git log confirms ca09c43 exists

---
*Phase: 88-codex-openai-budget-metering*
*Completed: 2026-05-25*
