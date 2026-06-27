---
phase: 122
slug: gpu-vllm-model-serving-sandbox-profiles-plus-local-model-chat-codex-repoint-km-model-start-anthropic-shim
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-27
---

# Phase 122 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Source: `122-RESEARCH.md` § Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` package + table-driven tests (existing) |
| **Config file** | none (embedded in Go test files) |
| **Quick run command** | `go test ./pkg/compiler/ ./pkg/profile/ -count=1 -timeout 120s` |
| **Full suite command** | `go test ./... -count=1 -timeout 600s` |
| **Estimated runtime** | ~90–300 seconds |

> NOTE: capture the command's OWN exit code (not a pipe's) — `go test | tail`
> masks a real FAIL (project memory: feedback_check_go_test_exit_not_pipe).

---

## Sampling Rate

- **After every task commit:** `go test ./pkg/compiler/ ./pkg/profile/ -count=1 -timeout 120s`
- **After every plan wave:** `go test ./... -count=1 -timeout 600s`
- **Before `/gsd:verify-work`:** Full suite green + all 7 live-UAT gates passed
- **Max feedback latency:** ~300 seconds (full suite)

---

## Per-Gate Verification Map

| Gate | Behavior | Test Type | Automated Command | Automated? |
|------|----------|-----------|-------------------|-----------|
| G1 | `km validate` green on all 7 profiles (merged-bytes) | unit | `go test ./pkg/profile/ -run TestValidate -count=1` + `scripts/validate-all-profiles.sh` | ✅ existing |
| G2 | `synthesizeCodexConfig` emits `[model_providers.local]` (wire_api responses, :8001) | unit | `go test ./pkg/compiler/ -run TestSynthesizeCodexConfig -count=1` | ❌ W0: new case |
| G2b | codex knob round-trips JSON schema + raw DLAMI AMI passes as AMIID | unit | `go test ./pkg/profile/ -run 'TestSchema\|TestValidate' -count=1` | ❌ W0: new cases |
| G2c | representative GPU leaf full-output golden | unit | `go test ./pkg/compiler/ -run TestUserdata -count=1` (CAPTURE flag to regen) | ❌ W0: golden |
| G2d | `km model start` command wiring (mock execFn/fetcher) | unit | `go test ./internal/app/cmd/ -run TestModel -count=1 -timeout 600s` | ❌ W0: new file |
| G3 | DLAMI boots → `nvidia-smi` all GPUs → weights pull → `vllm.service` serves `local` | **live UAT only** | N/A (GPU hardware) | NO |
| G4 | VS Code Remote-SSH + Continue chat | **live UAT only** | N/A (GUI/browser) | NO |
| G5 | Slack codex round-trip + resume; `/claude` = cloud | live UAT (Slack delivery synthetic-HMAC drivable) | synthetic `event_callback` POST to bridge `/events` (project memory: slack_bridge_inbound_e2e) | PARTIAL |
| G6 | `km model start` passthrough → local codex/curl completion | live UAT (curl scriptable; forward operator-driven) | `curl http://localhost:8001/v1/responses …` through the forward | PARTIAL |
| G7 | `km model start --anthropic` → local Claude Code chat | **live UAT only** | N/A (GUI) | NO |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements (new automated coverage to add)

- [ ] `pkg/compiler/agent_codex_test.go` — table case: `LocalBaseURL`/`LocalModel` → `[model_providers.local]` TOML (wire_api=responses, base_url=:8001, env_key=OPENAI_API_KEY)
- [ ] `pkg/profile/schema_storage_test.go` — `agent.codex.localBaseURL` + `agent.codex.localModel` round-trip through JSON schema
- [ ] `pkg/compiler/ec2_storage_test.go` — GPU leaf with raw DLAMI AMI ID passes through as `AMIID` (not `AMISlug`)
- [ ] `pkg/profile/validate_test.go` — GPU profile w/ raw DLAMI AMI: validate WARN-not-ERROR (desktop=false path)
- [ ] full-output goldens for one representative GPU leaf (sanctioned `CAPTURE_*` flag — do NOT touch the frozen pre-92 baseline; project memory: frozen_byte_identity_golden_capture_trap)
- [ ] `internal/app/cmd/model_test.go` — `km model start` wiring (mock execFn, mock fetcher, port-forward command build)

---

## Manual-Only Verifications (no automated coverage — MUST live-UAT)

| Behavior | Gate | Why Manual | Test Instructions |
|----------|------|------------|-------------------|
| GPU boot + vLLM serving | G3 | Needs real g6e GPU hardware | `km create profiles/gpu-qwen-12x.yaml --alias gpu1`; SSM: `nvidia-smi`, `systemctl status vllm`, `curl localhost:8000/v1/models` |
| Codex↔LiteLLM↔vLLM (R6/O7) | G3,G5,G6 | Needs running model + LiteLLM | on-box `codex exec "..."` → expect a completion; verify LiteLLM `/responses` translates |
| Slack codex round-trip + resume | G5 | Poller bash invisible to Go goldens (project memory: skill_bash_needs_live_uat) | synthetic-HMAC `event_callback` POST; verify reply + a follow-up resumes the thread; `/claude` hits cloud |
| VS Code + Continue (GUI) | G4 | GUI client | operator: `km vscode start gpu1` → Remote-SSH → install Continue → chat |
| Local Claude Code via shim (GUI) | G7 | GUI client + Claude-Code-on-70B (R7) | operator: `km model start gpu1 --anthropic`; `ANTHROPIC_BASE_URL=localhost:8001` Claude Code chat (scope: chat + light edits) |

---

## Validation Sign-Off

- [ ] All non-GPU behaviors have `<automated>` verify or a Wave 0 dependency
- [ ] Sampling continuity: no 3 consecutive code tasks without automated verify
- [ ] Wave 0 covers all MISSING test references
- [ ] No watch-mode flags
- [ ] Feedback latency < 300s
- [ ] Live-UAT gates (G3–G7) explicitly tracked in the UAT plan (not silently skipped)
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
