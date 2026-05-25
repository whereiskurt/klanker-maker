# Phase 88: Codex/OpenAI budget metering — Context

**Gathered:** 2026-05-24
**Status:** Ready for planning
**Source:** User conversation 2026-05-24 — autonomous planning authorized

<domain>
## Phase Boundary

This phase closes the gap surfaced by the user's question "Do you know if the
budget considers codex outputs too?" — today the answer is no. The
`sidecars/http-proxy/httpproxy/` MITM meter only knows about two hosts:

- `anthropic.go` — `api.anthropic.com` direct API (hard-coded Claude price table at lines 149-164).
- `bedrock.go` — Bedrock InvokeModel (Claude models on Bedrock).

OpenAI/Codex traffic to `api.openai.com` flows through the proxy unmetered.
`spec.budget` enforcement (IAM revoke + proxy 403 paths, both already wired
through `cmd/budget-enforcer/`) consequently never fires on Codex-only
sandboxes; their `BUDGET#ai#{modelID}` DynamoDB rows are always absent and
only the agent-agnostic compute spend row accrues.

**Phase ships:** a third interceptor in the same pattern, hooked into the
same `pkg/aws/budget.IncrementAISpend(ctx, client, table, sandboxID, modelID,
inputTokens, outputTokens, costUSD)` call, with a hard-coded OpenAI price
table mirroring the Claude one. The DynamoDB schema, budget-enforcer Lambda,
threshold-warning email, IAM-revoke path, and proxy 403 path all stay
unchanged — they read `BUDGET#ai#{modelID}` rows generically. The only
addition is a new intercept path that writes those rows for OpenAI calls.

**Companion to:** Phase 6 (budget enforcement introduced), Phase 70 (Codex
parity for operator-notify / Slack notify / Slack inbound dispatcher).

</domain>

<decisions>
## Implementation Decisions

### Locked by user (do not re-litigate in research / planning)

- **Scope:** OpenAI direct API (`api.openai.com`) only. Google/Mistral/etc.
  deferred to future phases.
- **Pattern:** Mirror `anthropic.go` line-for-line — response-stream parse →
  token-count extraction → `IncrementAISpend(modelID, in, out, cost)`. Do
  not invent a new abstraction; the third intercept slots in cleanly
  alongside the existing two.
- **Price table:** Hard-coded in the new Go file, same shape as
  `anthropic.go`'s `claudeModelPrices` map. Research must produce current
  $/1K input + $/1K output prices for at minimum:
  - gpt-5 family (gpt-5, gpt-5-mini if shipped)
  - gpt-4o family (gpt-4o, gpt-4o-mini)
  - o1 reasoning models (o1, o1-mini, o1-pro)
  - Any codex-specific endpoints the OpenAI Codex CLI hits (research
    whether codex uses chat/completions or has its own endpoint).
  - Whitelist exact model-id strings the API returns (alias forms AND
    dated variants like `gpt-4o-2024-08-06`).
- **DynamoDB row shape:** `BUDGET#ai#{modelID}` — already exists for
  Claude rows. OpenAI modelIDs slot in as new keys; no new SK pattern.
- **Enforcement parity:** Existing exceeds-budget paths (IAM revoke +
  proxy 403) read `spentUSD` from per-model rows and the aggregated
  total — both fire identically once OpenAI rows accrue. No new
  enforcement code.
- **Cache-token handling:** Phase 6 quick-task #4 added cache-token
  metering to the Anthropic Claude path (commit `b35f9d5`). If OpenAI's
  API returns analogous prompt-caching token counts (cached_input_tokens),
  the same accounting must flow through — count cached input at the
  cache-read price, not the full input price. If OpenAI's API doesn't
  expose cache tokens, this is a no-op for the OpenAI path.

### Out of scope (locked)

- Budget UI changes (`cmd/configui/main.go` budget rendering stays as-is —
  it already iterates `BUDGET#ai#*` generically; new OpenAI rows render
  for free).
- OTEL / MLflow spend reporting changes.
- Non-OpenAI providers (Google Gemini, Mistral, Anthropic-via-OpenAI-SDK).
- Changes to `pkg/aws/budget.go` schema or `cmd/budget-enforcer/main.go`
  enforcement logic — both are agent-agnostic and need no modification.
- Operator CLI surface — `km otel` and `km info` already aggregate per-
  model spend; OpenAI rows surface without flag changes.

### Claude's Discretion

- File layout: probably `sidecars/http-proxy/httpproxy/openai.go` (sibling
  to `anthropic.go` and `bedrock.go`). Could split into `openai.go` +
  `openai_pricing.go` if the price table is long.
- Stream-parse format: chat/completions SSE differs from
  Anthropic's event-stream — research must surface the exact delta shape
  for `chunk.choices[].delta` vs `chunk.usage` (usage usually arrives
  only in the final chunk when `stream_options.include_usage=true`).
- TLS allowlist: `api.openai.com` needs to be added to the L7 proxy host
  list (probably via `pkg/compiler/userdata.go` `buildL7ProxyHosts` or
  similar) so connect4 DNAT redirects OpenAI traffic to the http-proxy
  in eBPF+proxy mode. Research must locate the analogous wiring for
  `api.anthropic.com`.
- Failure mode: if model is unknown to the price table, log
  `WARN: unknown openai model "<id>"` and write a row with cost=0 (or
  skip the row entirely — researcher should check what `anthropic.go`
  does for unknown Claude models and mirror it).
- Test fixtures: capture a real OpenAI streaming response (or
  hand-construct one matching the documented schema) for golden tests
  in the new `openai_test.go`.

</decisions>

<specifics>
## Specific Ideas

- Reference implementation: `sidecars/http-proxy/httpproxy/anthropic.go`
  for the parse shape; `sidecars/http-proxy/httpproxy/bedrock.go` for the
  host-key/endpoint routing pattern.
- Token accounting: `IncrementAISpend(ctx, client, table, sandboxID,
  modelID, inputTokens, outputTokens, costUSD)` at `pkg/aws/budget.go:65`
  is the exact call shape — preserve it.
- Aggregation read path: `pkg/aws/budget.go:233+` (the function that
  reads per-model rows for the enforcer) is agent-agnostic; verify
  during plan-checker that no Claude-specific assumption sneaks in.
- Cache-token precedent: commit `b35f9d5` (quick task #4) for the
  cache-aware Anthropic accounting pattern.

</specifics>

<deferred>
## Deferred Ideas

- Non-OpenAI providers (Google Gemini, Mistral, Cohere) — separate phases
  per provider once OpenAI proves the third-intercept-path pattern.
- A registry-driven price table loaded from S3 so operators can patch
  pricing without redeploying sidecars — current hard-coded approach
  matches Anthropic, ship parity first.
- OpenAI batch API (`/v1/batches`) which has different pricing — defer
  until any sandbox actually uses it.
- Assistants API / Threads API — not used by the Codex CLI, defer.

</deferred>

---

*Phase: 88-codex-openai-budget-metering-...*
*Context gathered: 2026-05-24 via autonomous planning authorization*
