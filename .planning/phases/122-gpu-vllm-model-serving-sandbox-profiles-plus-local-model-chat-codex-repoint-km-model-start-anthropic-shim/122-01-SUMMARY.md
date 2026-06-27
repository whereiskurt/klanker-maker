---
phase: 122-gpu-vllm-model-serving-sandbox-profiles-plus-local-model-chat-codex-repoint-km-model-start-anthropic-shim
plan: "01"
subsystem: profile-schema, compiler, cmd
tags: [phase-122, wave-0, tdd-red-stubs, codex-local-provider, dlami, gpu-profiles]
dependency_graph:
  requires: []
  provides:
    - AgentCodexSpec.LocalBaseURL + LocalModel fields (types.go)
    - JSON schema agent.codex.localBaseURL + localModel properties
    - RED test stub for synthesizeCodexConfig [model_providers.local] emission (Plan 03)
    - GREEN schema round-trip test for localBaseURL/localModel
    - GREEN DLAMI AMI passthrough test (ami-0a9d213b92dabc044)
    - GREEN GPU raw AMI + no-desktop = no hard errors test
    - t.Skip scaffold for km model start port-forward wiring (Plan 04)
  affects:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/compiler/agent_codex_test.go (new)
    - pkg/profile/schema_storage_test.go (appended)
    - pkg/compiler/ec2_storage_test.go (appended)
    - pkg/profile/validate_test.go (appended)
    - internal/app/cmd/model_test.go (new)
tech_stack:
  added: []
  patterns:
    - TDD RED/GREEN Wave 0 stub pattern (Nyquist samples for Plans 03/04)
    - t.Skip placeholder with inline Plan 04 assertion documentation
    - Additive JSON schema extension (additionalProperties:false satisfied)
key_files:
  created:
    - pkg/compiler/agent_codex_test.go
    - internal/app/cmd/model_test.go
  modified:
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - pkg/profile/schema_storage_test.go
    - pkg/compiler/ec2_storage_test.go
    - pkg/profile/validate_test.go
decisions:
  - "LocalBaseURL/LocalModel use JSON tag names matching schema property names exactly (Go → schema key_link)"
  - "model_test.go uses t.Skip (not a compile-failing forward-reference) because model.go doesn't exist yet; file-level comment documents exact Plan 04 assertions"
  - "DLAMI test anchors ami-0a9d213b92dabc044 from 122-CONTEXT.md to catch regressions"
  - "GPU validate test asserts zero hard errors (not zero warnings) — WARN on raw AMI is desktop-gated"
metrics:
  duration: "256s"
  completed_date: "2026-06-27"
  tasks_completed: 3
  tasks_total: 3
  files_modified: 7
---

# Phase 122 Plan 01: Wave 0 Foundation — AgentCodexSpec Fields + Test Scaffolds Summary

AgentCodexSpec extended with LocalBaseURL/LocalModel + JSON schema properties; six Wave 0 test files created/extended as Nyquist samples for Plans 03 and 04.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Add LocalBaseURL/LocalModel to AgentCodexSpec + JSON schema | cde3bffb | pkg/profile/types.go, pkg/profile/schemas/sandbox_profile.schema.json |
| 2 | Write four pkg-level Wave 0 RED test stubs | e64af278 | pkg/compiler/agent_codex_test.go (new), pkg/profile/schema_storage_test.go, pkg/compiler/ec2_storage_test.go, pkg/profile/validate_test.go |
| 3 | Write internal/app/cmd/model_test.go scaffold | 46792220 | internal/app/cmd/model_test.go (new) |

## What Was Built

**Task 1 — Types + Schema:**
- `AgentCodexSpec` gains two optional string fields: `LocalBaseURL` (Bifrost gateway
  `:8001` endpoint, NOT vLLM `:8000`) and `LocalModel` (model name, default `"local"`).
  Field tags `json:"localBaseURL,omitempty"` / `yaml:"localBaseURL,omitempty"` match
  the JSON schema property names exactly (the plan's key_link constraint).
- `pkg/profile/schemas/sandbox_profile.schema.json` agent.codex block gains
  `localBaseURL` and `localModel` string properties alongside the existing `args`
  property. `additionalProperties:false` contract satisfied (additive change).

**Task 2 — Four test stubs:**
1. `pkg/compiler/agent_codex_test.go` (new) — `TestSynthesizeCodexConfig_LocalProvider`:
   - Sub-test "with LocalBaseURL set" asserts `[model_providers.local]`, `base_url`,
     `wire_api = "responses"`, `env_key = "OPENAI_API_KEY"`, `model_provider = "local"`.
     **Intentionally RED** until Plan 03 wires the emission into synthesizeCodexConfig.
   - Sub-tests "nil Codex" and "empty LocalBaseURL" assert dormancy (no provider block).
     These are **GREEN now** (synthesizeCodexConfig already returns early on nil/empty Codex).

2. `pkg/profile/schema_storage_test.go` (appended) — `TestAgentCodexLocalProvider_SchemaRoundTrip`:
   Validates a profile with both new fields against the embedded schema; parses and
   asserts LocalBaseURL/LocalModel preserved. **GREEN after Task 1.**

3. `pkg/compiler/ec2_storage_test.go` (appended) — `TestRawDLAMIAMIPassthrough`:
   Anchors the specific DLAMI AMI ID `ami-0a9d213b92dabc044` from 122-CONTEXT.md;
   asserts `ami_id = "ami-0a9d213b92dabc044"` and `ami_slug = ""` in HCL output.
   **GREEN** (isRawAMIID + passthrough path already exist in service_hcl.go).

4. `pkg/profile/validate_test.go` (appended) — `TestGPULeafRawAMI_WarnNotError`:
   Validates a GPU-shaped SandboxProfile (g6e.12xlarge, DLAMI AMI, codex local provider,
   no desktop). Asserts zero hard errors. **GREEN** (validateDesktop raw-AMI guard is
   desktop-gated; returns nil early when desktop.enabled is false).

**Task 3 — model_test.go scaffold:**
`internal/app/cmd/model_test.go` (new) — `TestModelStart_PortForwardWiring`:
Uses `t.Skip` with detailed Plan 04 instructions. The file-level comment documents the
exact assertion block (fetcher mock, execFn capture, remote port 8001, localPortNumber).
Compiles cleanly; `go vet ./internal/app/cmd/` is clean.

## Test Status After Plan 01

| Test | Package | Status | GREEN when |
|------|---------|--------|-----------|
| TestSynthesizeCodexConfig_LocalProvider/dormancy (x2) | pkg/compiler | GREEN | Now |
| TestAgentCodexLocalProvider_SchemaRoundTrip | pkg/profile | GREEN | Now |
| TestRawDLAMIAMIPassthrough | pkg/compiler | GREEN | Now |
| TestGPULeafRawAMI_WarnNotError | pkg/profile | GREEN | Now |
| TestSynthesizeCodexConfig_LocalProvider/with_LocalBaseURL_set | pkg/compiler | **RED** | Plan 03 |
| TestModelStart_PortForwardWiring | internal/app/cmd | SKIP | Plan 04 |

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check

Checking created files exist:
- `/Users/khundeck/working/klankrmkr/pkg/compiler/agent_codex_test.go` — created
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/model_test.go` — created
- `AgentCodexSpec.LocalBaseURL` in types.go — added
- `localBaseURL` in schema — added

Checking commits exist:
- `cde3bffb` — Task 1 (feat: AgentCodexSpec fields + schema)
- `e64af278` — Task 2 (test: Wave 0 RED stubs)
- `46792220` — Task 3 (test: model_test.go scaffold)

`go build ./...` — clean (BUILD_EXIT:0)
`go test ./pkg/profile/` — GREEN
`go test ./pkg/compiler/ -run TestSynthesizeCodexConfig_LocalProvider` — RED (expected)

## Self-Check: PASSED
