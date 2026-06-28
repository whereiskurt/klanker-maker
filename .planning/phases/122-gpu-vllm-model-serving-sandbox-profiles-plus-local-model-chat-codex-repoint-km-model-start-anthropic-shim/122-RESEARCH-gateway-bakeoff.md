# Phase 122 — LLM Gateway Bakeoff Research

**Goal:** Pick the best OSS gateway to run on the GPU EC2 box at `:8001`, fronting a local vLLM server at `:8000`, that simultaneously serves Codex CLI (Responses API), Claude Code (Anthropic Messages), and other OpenAI-compatible clients (chat completions). Secondary goal: route to cloud providers (Bedrock via IAM role, Anthropic direct, OpenAI) from the same gateway so Phase 122 becomes a durable model router, not just a Codex shim.

---

## TL;DR Up Front

**vLLM natively serves `/v1/responses` as of its current release** — the Codex requirement does not unconditionally require a gateway. However, a gateway is still needed for the Anthropic Messages API (`/v1/messages`) that Claude Code uses. If you also want multi-provider routing (local vLLM + cloud Bedrock/Anthropic from the same named endpoint), the gateway becomes essential infrastructure.

---

## 1. Feature Matrix

| Candidate | `/v1/responses` (Responses API) | `/v1/messages` (Anthropic Messages) | `/v1/chat/completions` passthrough | AWS Bedrock via IAM role (SigV4) | Custom vLLM backend | Language / footprint | Multi-provider routing | Verdict |
|---|---|---|---|---|---|---|---|---|
| **LiteLLM Proxy** | ✅ Confirmed. `/v1/responses` exposed; `use_chat_completions_api: true` bridges to chat-completions backends (since v1.63.8+). | ✅ Confirmed. `/v1/messages` endpoint (LiteLLM calls it "Anthropic Unified"). Translates to any backend in model_list. Stable, not experimental. | ✅ Native passthrough. `hosted_vllm/` prefix routes directly to vLLM chat completions. | ✅ Confirmed. Uses boto3 default credential chain; `aws_role_name` for STS AssumeRole. Omit api_key to use instance profile. | ✅ `api_base: http://localhost:8000/v1` + `hosted_vllm/` prefix. | Python venv, ~200 MB. Large dep tree. CLI: `pip install litellm` | ✅ Full: 100+ providers, per-model routing via `model_list`, instant new-model = one config block. | ✅ Qualified survivor |
| **Bifrost** (maximhq/bifrost) | ✅ Confirmed. Exposes `/v1/responses`; translates to chat completions via `ToChatRequest()`. Confirmed for vLLM backend. | ✅ Confirmed. Exposes `/anthropic` base path (i.e. `http://host:8080/anthropic` — NOT `/v1/messages`). Claude Code must set `ANTHROPIC_BASE_URL=http://localhost:8080/anthropic`. | ✅ Native. | ✅ Confirmed. Uses `AWS LoadDefaultConfig` (instance profile, IRSA, ECS task role all work). `iam_role` mode available. | ✅ Confirmed. Default `base_url: http://localhost:8000`. Set `AllowPrivateNetwork: true` for loopback. | Go binary. Install: `npx -y @maximhq/bifrost` (Node wrapper over Go binary) or Docker. ~40 MB binary. | ✅ 23+ providers. Routing by model prefix. New model = config entry. | ✅ Strong survivor — CAVEAT on `/v1/messages` path |
| **Portkey Gateway** (OSS) | ✅ Confirmed. "Open Responses" compliant; auto-translates to chat completions for non-Responses backends. | UNCONFIRMED. Portkey Cloud supports Anthropic; self-hosted OSS version may differ. GitHub repo is TypeScript, not explicit on `/v1/messages` inbound endpoint for arbitrary backends. | ✅ Via provider routing. | ✅ SigV4 inside gateway; EC2 instance profile documented for self-hosted deploys. | ✅ Ollama/vLLM-style local providers listed. | **TypeScript** (Node.js). Run via `npx @portkey-ai/gateway` or Docker. ~similar to LiteLLM footprint. | ✅ 1,600+ LLMs listed. | ⚠️ Downgraded — acquired by Palo Alto Networks (April 2026); OSS trajectory uncertain; TypeScript footprint; `/v1/messages` UNCONFIRMED on self-hosted. |
| **MLflow AI Gateway** | ⚠️ PARTIAL. Exposes endpoint at `/gateway/openai/v1/responses` — non-standard path requires SDK reconfiguration beyond simple base_url swap. | ⚠️ PARTIAL. `/gateway/anthropic/v1/messages` — same issue: non-standard prefix. Anthropic SDK base_url cannot just be set to `http://localhost:5000`. | ⚠️ Namespaced at `/gateway/<provider>/v1/chat/completions`. | ✅ Bedrock listed as a provider. | ✅ Ollama-compatible backends supported. | Python. Tied to MLflow ecosystem. | ✅ Supported. | ❌ Disqualified — non-standard URL namespacing breaks drop-in SDK config (Codex and Claude Code cannot use plain `OPENAI_BASE_URL` / `ANTHROPIC_BASE_URL`). |
| **vLLM itself** (no extra gateway) | ✅ **Natively serves `/v1/responses`** as of current release. Module: `vllm.entrypoints.openai.serving_responses`. Full streaming + tool call support. | ❌ Does NOT serve `/v1/messages`. | ✅ Natively. | N/A (vLLM IS the backend) | N/A | Python (already running) | ❌ Single backend only — no cloud routing. | ✅ Eliminates gateway for Codex. Still needs gateway for Claude Code + multi-provider. |

---

## 2. Key Research Findings

### vLLM Already Serves `/v1/responses` Natively

**This changes the problem statement.** vLLM's current release natively exposes `/v1/responses`, `/v1/responses/{id}`, and `/v1/responses/{id}/cancel`, compatible with the OpenAI Responses client. Codex CLI can point `OPENAI_BASE_URL=http://localhost:8000/v1` directly at vLLM **with no gateway**.

The gateway need is therefore:
1. **Anthropic Messages API** (`/v1/messages`) — Claude Code requires this. vLLM does NOT serve it.
2. **Multi-provider routing** — routing `model: local` to vLLM AND `model: claude-3-7-sonnet` to Bedrock from the same endpoint, metered through km's MITM proxy.
3. **Unified endpoint** — a single `:8001` that sandboxes can point at regardless of model.

### LiteLLM — Confirmed on All Three Dialects

- `/v1/responses` via `use_chat_completions_api: true`: **documented as of v1.63.8+**, presented as stable (no beta flag).
- `/v1/messages`: The "Anthropic Unified" endpoint is stable and production-ready. Config: set `ANTHROPIC_BASE_URL=http://localhost:4000` and Claude Code's requests arrive at LiteLLM's `/v1/messages`, get routed to any backend in `model_list`.
- Bedrock: boto3 default credential chain; instance profile works without static keys; `aws_role_name` for cross-account STS.
- New model = one `model_list` block, zero code change.
- **Known issue**: a GitHub bug (#23841) titled "multiple issues in anthropic /v1/messages experimental pass-through to openai" exists — verify this doesn't affect the LiteLLM-native translation path (distinct from the raw passthrough path).

### Bifrost — Strong Go Native, Anthropic Path Caveat

- `/v1/responses` → vLLM chat completions: **confirmed** (`ToChatRequest()` translation documented).
- Anthropic: Bifrost exposes an Anthropic-compatible server at `/anthropic` base path. Claude Code needs `ANTHROPIC_BASE_URL=http://localhost:8080/anthropic` — that's non-standard but works.
- Bedrock: `LoadDefaultConfig` picks up EC2 instance profile automatically.
- **Footprint**: Go binary, ~40 MB, launch via `npx -y @maximhq/bifrost` (the Node wrapper is just a launcher — the actual gateway is a compiled Go binary). Or Docker.
- **Caveat**: the `/anthropic` path is Bifrost's convention, not `/v1/messages`. Claude Code specifically uses `ANTHROPIC_BASE_URL` + appends `/v1/messages` — this means Bifrost's path may NOT be drop-in. Need to verify in UAT whether Claude Code appends the path or uses the base URL raw.

### `use_chat_completions_api` Version-Stability Note (LiteLLM)

The parameter is documented since **v1.63.8** and appears in the current stable docs without any "experimental" or "deprecated" flag. However, it is a per-model flag, not a global proxy option. As an alternative, LiteLLM encodes the same behavior in the model ID format: `openai/chat_completions/your-model`. This redundancy suggests the feature is intended to be durable, but watch for renames in major version bumps.

---

## 3. Recommendation

**Keep LiteLLM.** It is the only candidate with:
- Confirmed, documented `/v1/responses` over vLLM via `use_chat_completions_api`
- Confirmed, stable `/v1/messages` (Anthropic Unified endpoint) routing to arbitrary backends
- True drop-in URL semantics (`ANTHROPIC_BASE_URL=http://localhost:4000`, `OPENAI_BASE_URL=http://localhost:4000/v1`) — no path-prefix surprises
- Bedrock via instance profile (boto3 chain, no static key)
- 100+ providers, model_list = one config block per new model
- Active development, no acquisition/trajectory risk

Bifrost is a compelling Go-native alternative and should be a UAT backup if LiteLLM's Python footprint becomes an operational problem. Its main risk is the `/anthropic` path-prefix behavior with Claude Code.

**Note on vLLM + no gateway**: For a Codex-only setup where Claude Code is not needed, vLLM's native `/v1/responses` eliminates the gateway entirely. For Phase 122's full requirements (Codex + Claude Code + multi-model routing), the gateway is still required.

---

## 4. Winner: Install + Minimal Config

### Install (initCommandsAppend line)

```bash
pip install 'litellm[proxy]==1.63.*'   # pin minor, allow patches
```

Or via pipx for isolation:

```bash
pipx install 'litellm[proxy]==1.63.*'
```

Systemd unit pointing at `/usr/local/bin/litellm --config /etc/km/litellm-config.yaml --port 8001`.

### Minimal Config (`/etc/km/litellm-config.yaml`)

```yaml
model_list:
  # Local vLLM — serves /v1/chat/completions + /v1/responses (via bridge) + /v1/messages (via Anthropic Unified)
  - model_name: local
    litellm_params:
      model: hosted_vllm/local
      api_base: http://localhost:8000/v1
      api_key: none              # vLLM may not require a key
      use_chat_completions_api: true   # bridge /v1/responses → /v1/chat/completions

  # Cloud: Bedrock via IAM instance role (no static creds)
  - model_name: claude-3-7-sonnet
    litellm_params:
      model: bedrock/anthropic.claude-3-7-sonnet-20250219-v1:0
      aws_region_name: us-east-1
      # No api_key — boto3 default chain picks up EC2 instance profile

  # Cloud: Anthropic direct
  - model_name: claude-opus-4
    litellm_params:
      model: anthropic/claude-opus-4-5
      api_key: os.environ/ANTHROPIC_API_KEY

litellm_settings:
  drop_params: true   # ignore unsupported params rather than error

general_settings:
  master_key: os.environ/LITELLM_MASTER_KEY  # required for proxy auth
```

### Client Wiring

```bash
# Codex CLI
export OPENAI_BASE_URL=http://localhost:8001/v1
export OPENAI_API_KEY=<LITELLM_MASTER_KEY>

# Claude Code
export ANTHROPIC_BASE_URL=http://localhost:8001
export ANTHROPIC_API_KEY=<LITELLM_MASTER_KEY>

# aider / Continue / curl
# Use http://localhost:8001/v1/chat/completions with model=local
```

---

## 5. UNCONFIRMED Items to Settle in Live UAT

| Item | Why it matters | How to test |
|---|---|---|
| **vLLM `/v1/responses` GA status** | If still experimental in the pinned vLLM version on the GPU box, the `use_chat_completions_api` bridge in LiteLLM becomes load-bearing for Codex. | `curl http://localhost:8000/v1/responses -d '{"model":"local","input":[...]}'` — check 200 vs 404. |
| **LiteLLM `/v1/messages` bug #23841** | The "experimental pass-through" path has known issues. The native translation path (model_list + Anthropic Unified) is different — verify the right path is used. | `ANTHROPIC_BASE_URL=http://localhost:8001 claude --model local "hello"` — confirm reply, check LiteLLM logs for route taken. |
| **`use_chat_completions_api` with vLLM model name** | The vLLM model ID must match what vLLM reports (e.g. `/v1/models` endpoint). A mismatch returns 404 from vLLM. | `curl http://localhost:8000/v1/models` to get the real model name; set `hosted_vllm/<that-name>` in config. |
| **Bedrock Bedrock-runtime `:443` through km MITM proxy** | The GPU box has a MITM proxy for AI spend metering. Bedrock SigV4 requests must flow through it and be correctly metered. Verify `HTTPS_PROXY` is set in LiteLLM's env. | `km otel <sandbox-id>` after a Bedrock call — check for a Bedrock line in the spend summary. |
| **Bifrost `/anthropic` vs Claude Code path** | If Bifrost is used as fallback, verify Claude Code appends `/v1/messages` to `ANTHROPIC_BASE_URL`. If it does, `ANTHROPIC_BASE_URL=http://localhost:8080/anthropic` correctly hits `/anthropic/v1/messages` on Bifrost. | `ANTHROPIC_BASE_URL=http://localhost:8080/anthropic claude --model local "hello"` — check Bifrost logs. |
| **Streaming fidelity for tool calls via `use_chat_completions_api`** | LiteLLM bridges streaming tool calls from Responses format to chat-completions and back. Codex uses tool calls heavily. | Run a Codex task that invokes tools (e.g. file read). Check for malformed `tool_calls` or truncated streams. |

---

## Sources Consulted

- [LiteLLM /responses endpoint docs](https://docs.litellm.ai/docs/response_api)
- [LiteLLM vLLM provider docs](https://docs.litellm.ai/docs/providers/vllm)
- [LiteLLM Anthropic Unified /v1/messages](https://docs.litellm.ai/docs/anthropic_unified/)
- [LiteLLM Bedrock provider + IAM role discussion](https://github.com/BerriAI/litellm/discussions/5873)
- [vLLM online serving endpoints (latest)](https://docs.vllm.ai/en/latest/serving/online_serving/openai_compatible_server/)
- [vLLM serving_responses module](https://docs.vllm.ai/en/latest/api/vllm/entrypoints/openai/serving_responses.html)
- [Bifrost DEV post: Responses API support shipped](https://dev.to/pranay_batta/we-just-shipped-responses-api-support-in-bifrost-and-its-cleaner-than-chat-completions-3pih)
- [Bifrost vLLM provider guide](https://www.getmaxim.ai/bifrost/guides/providers/vllm)
- [Bifrost Bedrock + IAM role docs](https://docs.getbifrost.ai/providers/supported-providers/bedrock)
- [Bifrost Claude Code integration guide](https://www.getmaxim.ai/articles/how-to-use-claude-code-with-any-model-or-provider-using-bifrost/)
- [Portkey Open Responses docs](https://portkey.ai/docs/product/ai-gateway/responses-api)
- [Portkey Bedrock assumed role](https://portkey.ai/docs/product/ai-gateway/virtual-keys/bedrock-amazon-assumed-role)
- [GitHub Portkey gateway](https://github.com/Portkey-AI/gateway)
- [MLflow AI Gateway docs](https://mlflow.org/docs/latest/genai/governance/ai-gateway/)
- [vLLM feature request: Responses API (#14721)](https://github.com/vllm-project/vllm/issues/14721)
