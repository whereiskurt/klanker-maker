---
phase: 88
slug: codex-openai-budget-metering
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-24
---

# Phase 88 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Sourced from `88-RESEARCH.md` § Validation Architecture (researcher pre-filled).

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go stdlib `testing` (no third-party — matches existing sidecar tests) |
| **Config file** | None — `go test` discovers `*_test.go` by convention |
| **Quick run command** | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAI -count=1` |
| **Full suite command** | `go test ./sidecars/http-proxy/... -count=1` |
| **Integration command** | `go test ./sidecars/http-proxy/httpproxy/ -run TestHTTPProxy_OpenAI -count=1` |
| **Estimated runtime** | ~5s quick, ~20s full sidecar, ~3-5min repo-wide |

---

## Sampling Rate

- **After every task commit:** Run `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAI -count=1`
- **After every plan wave:** Run `go test ./sidecars/http-proxy/... -count=1 && go test ./pkg/compiler/ -run TestL7Proxy -count=1`
- **Before `/gsd:verify-work`:** `go test ./... -count=1` (full repo) must be green
- **Max feedback latency:** 5 seconds (quick), 20 seconds (wave)

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 88-01-01 | 01 | 0 | OAI-BUDGET-01 | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAITokens_ResponsesAPI_SSE -count=1` | ❌ W0 | ⬜ pending |
| 88-01-02 | 01 | 0 | OAI-BUDGET-01 | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAITokens_ChatCompletions_SSE -count=1` | ❌ W0 | ⬜ pending |
| 88-01-03 | 01 | 0 | OAI-BUDGET-01 | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAITokens_NonStreaming -count=1` | ❌ W0 | ⬜ pending |
| 88-01-04 | 01 | 0 | OAI-BUDGET-01 | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAITokens_EmptyBody -count=1` | ❌ W0 | ⬜ pending |
| 88-01-05 | 01 | 0 | OAI-BUDGET-01 | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAITokens_CacheTokens -count=1` | ❌ W0 | ⬜ pending |
| 88-01-06 | 01 | 0 | OAI-BUDGET-01 | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAITokens_ReasoningTokens -count=1` | ❌ W0 | ⬜ pending |
| 88-01-07 | 01 | 0 | OAI-BUDGET-01 | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestExtractOpenAITokens_ChatNoUsage -count=1` | ❌ W0 | ⬜ pending |
| 88-02-01 | 02 | 0 | OAI-BUDGET-02 | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestOpenAIRateTableCompleteness -count=1` | ❌ W0 | ⬜ pending |
| 88-03-01 | 03 | 0 | OAI-BUDGET-03 | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestCalculateOpenAICost_CacheArithmetic -count=1` | ❌ W0 | ⬜ pending |
| 88-03-02 | 03 | 0 | OAI-BUDGET-03 | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestCalculateOpenAICost_ZeroCache -count=1` | ❌ W0 | ⬜ pending |
| 88-04-01 | 04 | 1 | OAI-BUDGET-04 | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestOpenAIBlockedResponse -count=1` | ❌ W0 | ⬜ pending |
| 88-05-01 | 05 | 1 | OAI-BUDGET-05 | integration | `go test ./sidecars/http-proxy/httpproxy/ -run TestOpenAIAIByModelIntegration -count=1` | ❌ W0 | ⬜ pending |
| 88-05-02 | 05 | 1 | OAI-BUDGET-05 | integration | `go test ./sidecars/http-proxy/httpproxy/ -run TestHTTPProxy_OpenAIMetered -count=1` | ❌ W0 | ⬜ pending |
| 88-06-01 | 06 | 1 | OAI-BUDGET-06 | integration | `go test ./sidecars/http-proxy/httpproxy/ -run TestTransparent_OpenAI -count=1` | ❌ W0 | ⬜ pending |
| 88-07-01 | 07 | 1 | OAI-BUDGET-07 | unit | `go test ./pkg/compiler/ -run TestL7ProxyHostsWithCodex -count=1` | ❌ W0 | ⬜ pending |
| 88-09-01 | 09 | 2 | OAI-BUDGET-09 | manual | UAT runbook in `88-09-UAT.md` | N/A (manual) | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `sidecars/http-proxy/httpproxy/openai_test.go` — file does not exist; tests for OAI-BUDGET-01..06 land here
- [ ] `sidecars/http-proxy/httpproxy/testdata/openai-responses-sse-completed.txt` — captured SSE fixture for Responses API
- [ ] `sidecars/http-proxy/httpproxy/testdata/openai-chat-completions-sse-with-usage.txt` — Chat Completions SSE w/ `include_usage=true`
- [ ] `sidecars/http-proxy/httpproxy/testdata/openai-responses-nonstream.json` — non-streaming JSON body fixture
- [ ] `pkg/compiler/l7_proxy_hosts_test.go` (or extend existing test file) — gate test for `buildL7ProxyHosts` Codex branch

Existing `anthropic_test.go` + `testdata/` patterns are the reference.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Live Codex sandbox calls api.openai.com and DynamoDB row appears | OAI-BUDGET-09 | Requires live OpenAI API key + provisioned sandbox + CLI roundtrip | Spin up `codex` profile sandbox, `km agent run --codex "echo hi"`, query DynamoDB `BUDGET#ai#gpt-5.x` row, assert spentUSD > 0 |
| Budget exhaustion triggers proxy 403 for OpenAI traffic | OAI-BUDGET-04 | Requires populating budget row to limit, then live request | Set sandbox `spec.budget.ai.usd: 0.001`, make a Codex call, expect proxy-returned 403 with `OpenAIBlockedResponse` JSON envelope |
| Budget exhaustion triggers IAM revoke alongside proxy 403 | OAI-BUDGET-04 | Verifies budget-enforcer Lambda treats OpenAI rows identically to Claude rows | Above scenario + verify sandbox's IAM session credentials are revoked within enforcer poll interval |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies (16 / 16)
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify (verified — only 88-09-01 is manual)
- [ ] Wave 0 covers all MISSING references (openai_test.go + 3 testdata fixtures + L7 host test)
- [ ] No watch-mode flags (`go test -count=1` everywhere — defeats build cache, ensures fresh run)
- [ ] Feedback latency < 5s for per-task quick run
- [ ] `nyquist_compliant: true` set in frontmatter once Wave 0 tests committed

**Approval:** pending (autonomous planning — will be marked approved after gsd-plan-checker pass)
