---
phase: 88-codex-openai-budget-metering-http-proxy-interceptor-for-api-openai-com-price-table-incrementaispend-wiring-mirrors-anthropic-pipeline
plan: "07"
subsystem: infra
tags: [openai, codex, budget-metering, http-proxy, dynamodb, uat, sidecars]

# Dependency graph
requires:
  - phase: 88-04
    provides: openai.go production metering module + BedrockModelRate extension
  - phase: 88-05
    provides: proxy.go third intercept block + transparent.go meterOpenAIResponse
  - phase: 88-06
    provides: userdata.go buildL7ProxyHosts Codex gate (api.openai.com)
provides:
  - Live-verified end-to-end OpenAI metering pipeline (curl -> proxy -> DDB BUDGET#ai#gpt-4o-mini-2024-07-18)
  - UAT runbook (88-07-UAT.md) for future regressions
  - CLAUDE.md updated with three-provider Budget Metering Coverage section
  - Phase 88 OAI-BUDGET-09 requirement validated in production conditions
affects: [phase-89, codex-sandbox-profiles, km-status, km-otel, budget-enforcement]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "UAT via direct curl through MITM proxy rather than Codex CLI (avoids WebSocket-first client behavior)"
    - "DDB row shape BUDGET#ai#{modelID} used identically for all three AI providers"

key-files:
  created:
    - .planning/phases/88-codex-openai-budget-metering-http-proxy-interceptor-for-api-openai-com-price-table-incrementaispend-wiring-mirrors-anthropic-pipeline/88-07-UAT.md
    - .planning/phases/88-codex-openai-budget-metering-http-proxy-interceptor-for-api-openai-com-price-table-incrementaispend-wiring-mirrors-anthropic-pipeline/88-07-SUMMARY.md
  modified:
    - CLAUDE.md

key-decisions:
  - "UAT used direct curl (stream:true, include_usage:true) rather than km agent run --codex because Codex CLI 0.133.0 attempts WebSocket-first (wss://api.openai.com/v1/responses) which CloudFlare WAF rejects; curl proves the metering pipeline correctly"
  - "gpt-4o-mini-2024-07-18 rate-table entry confirmed: $0.00015/1k input + $0.0006/1k output; spentUSD=0.0000033 matches math"
  - "Scenario 3 (budget exhaustion 403) deferred to optional — Scenarios 1+2 sufficient for OAI-BUDGET-09"

patterns-established:
  - "OpenAI metering UAT: provision learn.v2.codex sandbox + direct curl with stream+usage, then DDB query on BUDGET#ai#gpt-* SK"

requirements-completed: [OAI-BUDGET-09]

# Metrics
duration: operator-led deployment + live UAT
completed: 2026-05-26
---

# Phase 88 Plan 07: Live UAT — OpenAI metering pipeline verified end-to-end via curl through MITM proxy into DDB BUDGET#ai#gpt-4o-mini row with spentUSD > 0

**OpenAI token metering pipeline confirmed live: curl to api.openai.com through MITM proxy produces BUDGET#ai#gpt-4o-mini-2024-07-18 DynamoDB row with correct cost math; km status renders per-model breakdown; CLAUDE.md updated with three-provider metering coverage**

## Performance

- **Duration:** Operator deployment + live UAT (non-autonomous plan)
- **Started:** 2026-05-25T22:00:00Z (approx)
- **Completed:** 2026-05-26T00:30:00Z (approx, per DDB last_updated timestamp)
- **Tasks:** 3 of 3 complete
- **Files modified:** 2 (88-07-UAT.md, CLAUDE.md)

## Accomplishments

- Deployed new http-proxy sidecar binary (embedding openai.go, proxy.go intercept, transparent.go) via `make sidecars && km init --sidecars` — operator confirmed build at HEAD `b95db7f` (`v0.3.721`)
- Live sandbox `learn-1bf3a9d8` (profiles/learn.v2.codex.yaml, enforcement: both) produced a real DDB row: `PK=SANDBOX#learn-1bf3a9d8 SK=BUDGET#ai#gpt-4o-mini-2024-07-18 inputTokens=14 outputTokens=2 spentUSD=0.0000033` within 3 seconds of SSE stream completion
- `km status learn-1bf3a9d8` rendered per-model OpenAI breakdown alongside Claude spend column (correctly empty for Codex-only sandbox)
- UAT runbook persisted to `88-07-UAT.md` (4 scenarios documented) for future regression checks
- CLAUDE.md extended with "Budget Metering Coverage (Phase 88)" section listing all three provider intercepts

## Task Commits

Each task was committed atomically:

1. **Task 3: Write UAT runbook + update CLAUDE.md** - `b95db7f` (docs) — prior executor
2. **Task 1: Build and deploy sidecars** - operator deploy, no source commit (deploy-only step)
3. **Task 2: Live UAT — Scenarios 1+2 PASS** - orchestrator-verified, no source commit (UAT execution)

**Plan metadata:** (this SUMMARY commit — see final_commit step)

## Files Created/Modified

- `.planning/phases/88-.../88-07-UAT.md` - 4-scenario runbook for live OpenAI metering verification
- `CLAUDE.md` - Added Budget Metering Coverage section (three-provider table: Bedrock, Anthropic, OpenAI)
- `.planning/phases/88-.../88-07-SUMMARY.md` - This file

## UAT Evidence

**Sandbox:** `learn-1bf3a9d8` from `profiles/learn.v2.codex.yaml` (`spec.cli.agent: codex`, `network.enforcement: both`). Destroyed post-UAT.

### Scenario 1 — Metering pipeline (PASS)

- SSM-exec into sandbox; direct curl to `https://api.openai.com/v1/chat/completions` with `stream:true, include_usage:true, model:gpt-4o-mini`
- HTTP 200, SSE stream completed, `[DONE]` marker, usage block `prompt_tokens=14 completion_tokens=2`
- DDB row appeared in `km-budgets` within 3 seconds:
  - `PK = SANDBOX#learn-1bf3a9d8`
  - `SK = BUDGET#ai#gpt-4o-mini-2024-07-18`
  - `inputTokens = 14`
  - `outputTokens = 2`
  - `spentUSD = 0.0000033`
  - Rate-table math: 14 × $0.00015/1k + 2 × $0.0006/1k = $0.0000033 — CORRECT
  - `last_updated = 2026-05-26T00:29:48Z`

### Scenario 2 — km status rendering (PASS)

```
AI:      $0.0000 / $2.0000 (0.0%)
  gpt-4o-mini-2024-07-18:        $0.0000 (14 in / 2 out)
```

Per-model breakdown surfaces the gpt-4o-mini row. Format matches existing Anthropic/Bedrock pattern.

### Scenario 3 — Budget exhaustion 403 (DEFERRED — optional per plan)

Not executed in this UAT cycle. Deferred to Phase 89 regression or dedicated budget-exhaustion scenario.

### Scenario 4 — Cleanup

`km destroy learn-1bf3a9d8 --remote --yes` dispatched (Lambda async teardown).

## Known Limitations / Follow-up Candidates

These are Phase-88-orthogonal findings — NOT Phase 88 bugs. The metering pipeline works correctly.

### 1. Codex CLI 0.133.0 WebSocket-first behavior

Codex CLI attempts `wss://api.openai.com/v1/responses` first. CloudFlare WAF rejects the MITM-presented TLS handshake on WebSocket upgrades with "Attack attempt detected". Codex then falls back to HTTPS `https://api.openai.com/v1/responses` but drops the Authorization header on the fallback (codex client bug or auth-channel separation), so the request returns 401.

Net effect: `km agent run --prompt ... --wait` with a Codex-profile sandbox does not complete through the MITM proxy. The metering pipeline works correctly for any HTTPS POST to `api.openai.com` (proved via curl).

Recommend a follow-up phase to either: (a) add a proxy-side WS pass-through allowlist for OpenAI wss endpoints with separate metering, or (b) document a Codex-on-API-key invocation flag that forces HTTPS-only and re-attaches auth. File as Phase 89+ candidate.

### 2. ./bin/km vs ./km binary path confusion

`make build` writes the binary to `./km` (Makefile rule: `-o km ./cmd/km/`), but a stale binary at `./bin/km` (May 18, commit `9d787da`) shadows it on $PATH if `./bin` is exported first. UAT used `./km` directly to avoid this.

Recommend: either clarify in CLAUDE.md that the canonical post-build path is `./km`, remove the stale `./bin/km`, or add a Makefile install target. Developer-ergonomics issue, not a Phase 88 deliverable.

### 3. Sandbox OpenAI key injection

No automated path for injecting `OPENAI_API_KEY` into sandbox env from SSM at boot time. UAT used out-of-band injection via SSM send-command + temp file. Possible follow-up: add `spec.execution.envFromSSM` schema field for declarative key injection at sandbox boot. Out of Phase 88 scope.

## Decisions Made

- Used direct curl (`stream:true, include_usage:true`) as UAT vehicle instead of `km agent run` because Codex CLI 0.133.0 WebSocket-first behavior prevents end-to-end completion through MITM proxy. Curl proves the metering pipeline (interceptor → cost calc → DDB write) correctly without the CLI layer.
- Scenario 3 (budget exhaustion 403) marked DEFERRED — Scenarios 1+2 are sufficient to satisfy OAI-BUDGET-09 ("live sandbox produces BUDGET#ai#gpt-* row with spentUSD > 0").
- Rate table entry confirmed: gpt-4o-mini-2024-07-18 at $0.00015/1k input + $0.0006/1k output (exact match to usage data).

## Deviations from Plan

### Auto-fixed Issues

None from code perspective. One UAT method deviation:

**[Rule 1 - Bug/Workaround] Used curl instead of km agent run for Scenario 1**
- **Found during:** Task 2 (Live UAT)
- **Issue:** Codex CLI 0.133.0 attempts WebSocket-first; CloudFlare WAF rejects MITM TLS handshake on WS upgrades; subsequent HTTPS fallback drops Authorization header → 401
- **Fix:** Direct curl with `stream:true, include_usage:true, model:gpt-4o-mini` through proxy — proves intercept, cost calc, and DDB write pipeline end-to-end
- **Impact:** Metering pipeline fully verified. Codex CLI integration is a follow-up candidate (Phase 89+)

---

**Total deviations:** 1 (method substitution for UAT vehicle; metering pipeline verified correctly)
**Impact on plan:** UAT goal achieved. Plan's must_have "live Codex sandbox produces BUDGET#ai#gpt-* DDB row with spentUSD > 0" satisfied.

## Issues Encountered

- Codex CLI WebSocket-first behavior blocked `km agent run` path through MITM proxy. Resolved by using direct curl as UAT vehicle (see Known Limitations above).

## User Setup Required

None — all pipeline changes are in http-proxy sidecar (auto-deployed via `km init --sidecars`).

## Next Phase Readiness

- Phase 88 complete. All 7 plans executed.
- Phase 88 OAI-BUDGET-09 validated in production conditions.
- Remaining OAI-BUDGET requirements (01-07) validated via unit + integration tests in prior plans.
- Ready for `/gsd:verify-work` then `/gsd:end-phase 88`.
- Phase 89 candidates: Codex CLI WebSocket-first MITM workaround, `spec.execution.envFromSSM` key injection, budget-exhaustion 403 Scenario 3 live validation.

---
*Phase: 88-codex-openai-budget-metering*
*Completed: 2026-05-26*

## Self-Check: PASSED
