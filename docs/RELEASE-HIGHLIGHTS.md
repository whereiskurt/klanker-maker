<!--
  MAINTAINER NOTE — update this file BEFORE tagging each release.

  Its contents are injected VERBATIM into every GitHub release's notes,
  between the install header and goreleaser's auto-generated changelog.
  Wiring: .github/workflows/release.yml ("Load release highlights" step) →
  $KM_RELEASE_HIGHLIGHTS → .goreleaser.yaml `release.header` template.

  Keep it to the few MAJOR, human-curated additions for THIS release (the
  auto-changelog already lists every commit). HTML comments like this one
  are hidden in GitHub's rendered view. If this file is empty/absent the
  section is omitted gracefully.
-->
## ✨ Major additions highlighted

### 🚀 GPU local-model serving — 🧪 **Preview** (Phase 122)

> **Preview / early-adopter.** Shipped code-complete, but **live end-to-end validation is still
> in progress** — the GPU profiles and full Slack→model flows (e.g. Slack→Kimi, Slack→GLM) have
> not yet been exercised end-to-end, are gated on the G-instance quota, and may change. **Not
> recommended for production use yet.**

Seven new GPU SandboxProfiles stand up **vLLM**-served local models on g6e instances (4–8× NVIDIA
L40S), with an on-box multi-provider gateway that makes the local model reachable from *every* km
interface:

- **7 GPU profiles** — `gpu-qwen-{12x,48x}`, `gpu-llama-{12x,48x}`, `gpu-glmair-12x`,
  `gpu-kimidev-12x`, `gpu-glm46-48x` — serving 70B-class weights on a Deep Learning AMI
  (Ubuntu 24.04), weights cached on a 300 GB persistent volume (`HF_HOME`), exposed as
  `--served-model-name local`. Composed from a new abstract `base/gpu/serve` inheritance fragment.
- **On-box Bifrost gateway (`:8001`) — a real multi-provider router**, not a shim. Codex now
  requires the OpenAI **Responses API** and vLLM speaks only Chat Completions, so Bifrost
  (`maximhq/bifrost:v1.6.0`) bridges Responses → vLLM, serves Anthropic Messages for Claude Code,
  and routes purely by the `provider/model` string the caller sends — `vllm-local/local`,
  `bedrock/…` (keyless instance-role SigV4), `anthropic/…`, `bedrock/openai.gpt-oss-120b`. Cloud
  routes flow through the MITM proxy and are **metered automatically**; OTLP telemetry lands in
  `km otel`.
- **Reach the local model from anywhere** — box-global Codex repoint
  (`spec.agent.default: codex`) makes the 70B reachable via **VS Code (Continue)**, **Slack
  chat-with-resume**, on-box terminal/headless Codex, and your laptop via the new
  **`km model start <id> [--anthropic]`** (SSM port-forward). Claude stays cloud-pointed on-box,
  giving you a **`/claude` (cloud) vs `/codex` (local 70B) A/B in one Slack channel**.
- **New CLI:** `km model start` / `km model status`.
- ⚠️ **Prereq:** the On-Demand **G/VT instances** vCPU quota (`L-DB2E81BA`) defaults to **0** per
  account — request it in your target account/region first. Full runbook: `docs/gpu-model-serving.md`.

### 🛰️ Platform-wide AZ failover + capacity feasibility (Phase 124)

`km create` now hunts for capacity instead of failing on the first Availability Zone — directly
taming the GPU-launch pain above (and every spot/on-demand EC2 launch):

- **AZ sweep with classify-and-retry** — `km create` pre-orders the region's AZs, then on a launch
  failure classifies the cause: transient capacity errors (ICE / spot price / spot limit / waiter
  timeout) **rotate to the next AZ**, while quota / auth / invalid errors **fail fast** (no AZ
  rotation can clear a quota wall).
- **`km capacity [profile | --type t]`** — a per-AZ feasibility table with honest verdicts
  (`likely`, `recently-dry`, `not-offered`, `quota-blocked`, `unknown`). Capacity is probabilistic,
  so it never claims "available."
- **`km create --wait-for-capacity[=30m]`** — an outer backoff loop that re-sweeps every 5 minutes
  until capacity frees up or the deadline hits. Dormant by default (single-sweep without the flag).
- **GPU quota fail-fast gate** — for GPU families, `km create` checks the `L-DB2E81BA` G/VT quota
  *before* sweeping and fails immediately (with the increase URL) when headroom is 0.
- **New `{prefix}-capacity` table** records recent ICE / success per (instance-type, AZ) so the
  sweep develops sticky AZ preference; `km doctor` checks the table + GPU quota.

### 🛡️ Action quotas + freeze quarantine (Phase 121)

A circuit-breaker for high-impact outbound actions — like budgets, but for *actions* (PRs opened,
Slack posts, emails sent) — to contain runaway loops or an offensive-test breakout that
accidentally succeeds:

- **`spec.limits` / install-level `limits:`** with per-window caps (lifetime / perHour / perDay)
  and `onBreach: warn | block | freeze`; a limit of **0 is a hard-deny floor**.
- **Freeze quarantine** — auto-freeze on breach; `km freeze` / `km unlock` to drive it manually,
  a `FROZEN` marker in `km list` / `km status`, and a live **Quotas** usage section.
- **`km-quota-alerter`** Lambda (DDB-Stream → SES + Slack) on first breach; enforced at the proxy
  chokepoint and the Slack + HackerOne bridges. **Dormant by default.** See `docs/action-quotas.md`.

### 🧬 Profiles reset + OS-layered fragment library (Phase 120)

The built-in profile library was rebuilt on the composable-inheritance engine: lean leaves that
`extends:` a shared **`profiles/base/`** fragment set + **OS-layered base fragments**, so profiles
compose instead of copy-paste — which is exactly what makes the 7 GPU profiles above so small.

---
*On the horizon:* a from-zero **setup wizard** at klankermaker.ai (interview → AWS config +
`km-config.yaml` + a staged `km` runbook) is in planning.
