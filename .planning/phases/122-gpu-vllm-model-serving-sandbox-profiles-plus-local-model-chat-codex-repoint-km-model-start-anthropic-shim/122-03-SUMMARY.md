---
phase: 122-gpu-vllm-model-serving-sandbox-profiles-plus-local-model-chat-codex-repoint-km-model-start-anthropic-shim
plan: 03
subsystem: compiler
tags: [codex, toml, local-provider, vllm, bifrost, golden-test]

# Dependency graph
requires:
  - phase: 122-01
    provides: AgentCodexSpec.LocalBaseURL + LocalModel fields + RED test scaffolds
provides:
  - "[model_providers.local] TOML block emission in synthesizeCodexConfig (Phase 122)"
  - "codex_config_local.golden.toml — byte-identity fixture for local-provider path"
  - "TestSynthesizeCodexConfigLocalProviderGolden with CAPTURE_LOCAL_CODEX_GOLDEN gate"
affects:
  - 122-02 (GPU profiles use agent.codex.localBaseURL knob)
  - 122-05 (live UAT verifies codex routes through Bifrost :8001)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Guarded TOML emission: LocalBaseURL != \"\" gate → append [model_providers.local] block"
    - "CAPTURE_LOCAL_CODEX_GOLDEN=1 env gate for regenerating local-provider golden"
    - "Additive golden strategy: dormant golden unchanged, second fixture for new path"

key-files:
  created:
    - "pkg/compiler/testdata/codex_config_local.golden.toml"
  modified:
    - "pkg/compiler/agent_codex.go"
    - "pkg/compiler/agent_codex_golden_test.go"

key-decisions:
  - "Emit [model_providers.local] block only when LocalBaseURL != \"\" — dormant path byte-identical"
  - "LocalModel defaults to \"local\" when empty string"
  - "base_url emitted verbatim from LocalBaseURL (no hardcoding); profile sets :8001 for Bifrost"
  - "wire_api = \"responses\" per Codex Responses API requirement (since Feb 2026)"
  - "Documented :8000 as fallback in code comment; profile routes through Bifrost :8001 for multi-provider"
  - "GPU full-output golden deferred to Plan 02 (GPU profiles not yet created)"

patterns-established:
  - "CAPTURE_LOCAL_CODEX_GOLDEN=1: additive capture flag mirrors CAPTURE_ADDVOL_GOLDEN pattern"

requirements-completed: [REQ-122-CODEX]

# Metrics
duration: 24min
completed: 2026-06-27
---

# Phase 122 Plan 03: Codex Local-Provider Synthesizer Summary

**`synthesizeCodexConfig` now emits `[model_providers.local]` (wire_api=responses, env_key=OPENAI_API_KEY) when LocalBaseURL is set, routing codex through Bifrost :8001; dormant path byte-identical**

## Performance

- **Duration:** ~24 min
- **Started:** 2026-06-27T18:38:14Z
- **Completed:** 2026-06-27T19:02:17Z
- **Tasks:** 3
- **Files modified:** 3 (+ 1 created)

## Accomplishments

- Turned `TestSynthesizeCodexConfig_LocalProvider` RED→GREEN (3/3 subtests: emit, nil-dormant, empty-dormant)
- Added `[model_providers.local]` emission in `synthesizeCodexConfig` with correct Codex Responses API shape
- Dormant path (nil agent / nil Codex / empty LocalBaseURL) is byte-identical to before — no regression
- Added `codex_config_local.golden.toml` and `TestSynthesizeCodexConfigLocalProviderGolden` with `CAPTURE_LOCAL_CODEX_GOLDEN=1` regeneration gate
- Full `pkg/compiler` suite GREEN; `internal/app/cmd` pre-existing timing-flakiness confirmed not caused by changes

## Task Commits

Each task was committed atomically:

1. **Task 1: Emit [model_providers.local] in synthesizeCodexConfig** - `0422c2dd` (feat)
2. **Task 2: Update codex golden + local-provider golden** - `d403c7c8` (feat)
3. **Task 3: Full-suite regression sweep** - (verification only, no code change)

## Files Created/Modified

- `pkg/compiler/agent_codex.go` — Added guarded local-provider TOML emission + extended doc comment
- `pkg/compiler/agent_codex_golden_test.go` — Added `TestSynthesizeCodexConfigLocalProviderGolden`
- `pkg/compiler/testdata/codex_config_local.golden.toml` — New golden fixture for LocalBaseURL path

## Decisions Made

- `LocalModel` defaults to `"local"` when empty (per plan spec)
- `base_url` emitted verbatim from `c.LocalBaseURL` — no URL hardcoding in the synthesizer; the profile is the authority (sets `http://localhost:8001/v1` = Bifrost)
- `wire_api = "responses"` per Codex Responses API requirement (since Feb 2026)
- `name = "Local vLLM (via Bifrost)"` documents the gateway role in the TOML config
- GPU full-output golden deferred: GPU profiles (Plan 02) haven't been created yet; the GPU leaf golden will be captured when Plan 02 executes

## Deviations from Plan

**1. [Rule — Scope] GPU full-output golden deferred**
- **Found during:** Task 2
- **Issue:** Plan 03 Task 2 says to regenerate the GPU-leaf full-output golden for `gpu-qwen-12x`, but Plan 02 (GPU profiles) hasn't been executed — no `profiles/base/gpu/serve.yaml` or `profiles/gpu-qwen-12x.yaml` exist yet
- **Fix:** Added only the additive `codex_config_local.golden.toml` for the local-provider path. The GPU full-output golden is a Plan 02 artifact; Plan 02 should include or trigger that capture
- **Impact:** No regression. The primary deliverable (RED→GREEN test + local-provider emission) is complete

---

**Total deviations:** 1 (scope — GPU golden deferred to Plan 02 which creates the GPU profiles)
**Impact on plan:** Primary deliverable complete. GPU golden deferral is correct because the prerequisite profiles don't exist yet.

## Issues Encountered

- `TestDestroyCmd_InvalidSandboxID` failed during `go test ./...` parallel run but passed in isolation — confirmed pre-existing timing flakiness in `internal/app/cmd` unrelated to these changes

## Next Phase Readiness

- Plan 02 can now create GPU profiles using `agent.codex.localBaseURL: "http://localhost:8001/v1"` and the synthesizer will emit the correct TOML
- Plan 04 (`km model start`) can proceed independently
- `TestSynthesizeCodexConfig_LocalProvider` is permanently GREEN

---
*Phase: 122-gpu-vllm-model-serving-sandbox-profiles-plus-local-model-chat-codex-repoint-km-model-start-anthropic-shim*
*Completed: 2026-06-27*
