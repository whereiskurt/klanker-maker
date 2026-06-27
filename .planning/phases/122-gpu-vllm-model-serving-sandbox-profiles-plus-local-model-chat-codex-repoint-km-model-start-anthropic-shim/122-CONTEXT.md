# Phase 122: GPU vLLM model-serving + local-model chat — Context

**Gathered:** 2026-06-27
**Status:** Ready for planning
**Source:** Brainstorming design spec (authoritative): `docs/superpowers/specs/2026-06-27-gpu-vllm-serving-profiles-design.md`

> The design spec is the single source of truth. This CONTEXT.md is a planning-oriented
> digest of its locked decisions, risks (R1–R7), and open questions (O1–O9). Read the
> spec in full before planning.
>
> **⚠ RESEARCH CORRECTION (O7, authoritative — see `122-RESEARCH.md`):** Codex now
> requires the OpenAI **Responses API**; vLLM serves only Chat Completions. So a
> single on-box **LiteLLM on `:8001` is a CORE component** (not just the `--anthropic`
> shim) fronting vLLM `:8000` and serving Chat-Completions + Responses (codex) +
> Messages (Claude Code). The codex local-provider knob points at **`:8001`,
> `wire_api="responses"`** — NOT `:8000`. DLAMI = `ami-0a9d213b92dabc044`.

<domain>
## Phase Boundary

**Delivers three things on a sizable phase (plan as waves):**

1. **7 GPU serving profiles** — stand up GPU EC2 sandboxes that serve 70B-class
   local models via **vLLM** on an **AWS Deep Learning AMI (Ubuntu 24.04)** base,
   weights cached on a persistent `additionalVolume`, served as
   `--served-model-name local`. Composed from a NEW abstract fragment
   `profiles/base/gpu/serve.yaml` (Phase 117 inheritance).
2. **Slack chat-with-resume against the local model** — repoint the on-box
   **codex** agent at `http://localhost:8000/v1` (a small `synthesizeCodexConfig`
   change emitting a `[model_providers.local]` block). Reuses the EXISTING
   km-slack inbound poller (per-thread session, `codex exec resume`, allowlist,
   render-rich). No bridge Lambda change, no `km-slack-threads` schema change.
3. **`km model start <sandbox> [--local-port] [--anthropic]`** — laptop-side SSM
   port-forward to the model (mirrors `km vscode start` / `km desktop start`,
   reuses `runReconnectingPortForward` in `internal/app/cmd/shell.go`), PLUS an
   on-box **Anthropic↔OpenAI shim** (LiteLLM `/v1/messages` on `:8001`) so local
   **Claude Code** can drive the remote model.

**NOT in scope (deferred):** goose as a first-class `agent_type` (Path 2);
on-box claude default repointed at local (stays cloud — preserves the A/B);
Kimi K2 (~1T, needs gated P-family). Multi-node serving.
</domain>

<decisions>
## Implementation Decisions (locked)

### Serving stack & base
- **vLLM** (multi-GPU tensor-parallel, OpenAI-compatible). NOT llama.cpp/SGLang/TGI.
- Base AMI: **AWS Deep Learning Base OSS Nvidia Driver GPU AMI (Ubuntu 24.04)**,
  raw AMI ID in `spec.runtime.ami` (region-resolved — O1). Drivers/CUDA/Docker/
  nvidia-container-toolkit pre-baked. Ubuntu → `base/os/debian` userdata path.
- vLLM runs as a **systemd unit** (`vllm.service`) wrapping `docker run … vllm/
  vllm-openai`, `EnvironmentFile=/etc/km/vllm.env`, `--served-model-name local`,
  bound `127.0.0.1:8000`.

### Profile matrix (7 leaves, all extend base/gpu/serve)
| Profile | Instance | Model | Precision | TP |
|---|---|---|---|---|
| gpu-qwen-12x | g6e.12xlarge | Qwen2.5-72B-Instruct-AWQ | AWQ 4-bit | 4 |
| gpu-llama-12x | g6e.12xlarge | Llama-3.3-70B-Instruct | FP8 | 4 |
| gpu-qwen-48x | g6e.48xlarge | Qwen2.5-72B-Instruct | FP16 | 8 |
| gpu-llama-48x | g6e.48xlarge | Llama-3.3-70B-Instruct | FP16 | 8 |
| gpu-glmair-12x | g6e.12xlarge | GLM-4.5-Air (106B MoE) | 4-bit/FP8 | 4 |
| gpu-kimidev-12x | g6e.12xlarge | Kimi-Dev-72B (dense, Qwen2 arch) | AWQ 4-bit | 4 |
| gpu-glm46-48x | g6e.48xlarge | GLM-4.6 (355B MoE) | 4-bit | 8 |

### Inheritance & fragment
- New abstract `base/gpu/serve.yaml` (`metadata.abstract: true`) holds the ~90%:
  AMI, `additionalVolume` 300GB `/data` + `HF_HOME=/data/hf`, the vLLM systemd
  unit, the Continue config (`~/.continue/config.yaml` → `localhost:8000/v1`,
  model `local`), `agent.default: codex` + the codex local-provider knob, the
  Anthropic shim sidecar, raised `budget.compute.maxSpendUSD` (base/platform's
  0.50 default would suspend the box in minutes — leaves raise: 12x→$120,
  48x→$300), lifecycle `ttl 8h`/`idle 1h`/`teardownPolicy stop`, `spot:false`.
- Leaf extends: `[base/os/debian, base/network/safenetwork, base/userinit,
  base/platform, base/slack-persandbox, base/gpu/serve]`. Each leaf sets ONLY:
  `instanceType`, `/etc/km/vllm.env` (model+TP+quant), 48x budget bump, and
  (llama only) `iam.allowedSecretPaths` + `HF_TOKEN`.
- **safenetwork is `*` egress** (open) so HF/Docker Hub are already allowed (R1
  dissolves). `base/platform` provides `cli.noBedrock:true` ⇒ `Spec.CLI != nil`
  ⇒ the slack inbound poller IS emitted.

### Chat / agents
- `agent.default: codex` → Slack inbound + `km shell` + `km agent run --codex`
  all hit the local model (box-global repoint = 4 free interfaces).
- **claude stays cloud-pointed on-box** → `/claude` = cloud, default/`/codex` =
  local 70B, A/B in one channel.
- `synthesizeCodexConfig` (`pkg/compiler/agent_codex.go:67`) gains a
  `[model_providers.local]` emission gated on a new profile knob (shape = O6).

### km model start (operator-side)
- New `internal/app/cmd/model.go`: `km model start <sb> [--local-port 8000]
  [--anthropic]` + `km model status <sb>`. Reuses `runReconnectingPortForward`
  + an HTTP probe (`GET /v1/models`). Layer 1 = OpenAI passthrough (laptop →
  vLLM:8000). Layer 2 `--anthropic` = forward the on-box shim `:8001`.
- On-box shim = LiteLLM `/v1/messages` (or claude-code-proxy), installed by
  base/gpu/serve, localhost-only (O9 = choice/packaging/port).

### Secrets
- Only the 2 **Llama** leaves are gated → SOPS-encrypt `HF_TOKEN` with the
  **`klanker-application`** AWS profile + shared secrets KMS key (Phase 89),
  declare in `iam.allowedSecretPaths`, inject into `/etc/km/vllm.env` (O3).
  Qwen/GLM/Kimi are ungated.

### Definition of done = FULL LIVE UAT (7 gates)
1. fragment + 7 profiles authored; `km validate` green on all 7.
2. `synthesizeCodexConfig` change + unit tests + updated goldens.
3. real `km create` (start with one **g6e.12x**) → DLAMI boots → `nvidia-smi`
   sees all GPUs → weights pull to `/data` → `vllm.service` serves `local`.
4. VS Code Remote-SSH + Continue chat against the model (GUI gate — operator).
5. Slack codex round-trip + resume (synthetic-HMAC drivable); `/claude` = cloud.
6. `km model start <sb>` passthrough → local codex/curl completion.
7. `km model start --anthropic` + shim → local Claude Code chat (GUI gate —
   operator; scope to chat + light edits per R7).

### Claude's Discretion
- Exact YAML field names for the codex local-provider knob (O6) and shim
  packaging (O9) — pick the cleanest, document in the plan.
- Wave decomposition boundaries, golden-test update mechanics, test layout.
- Per-model `--max-model-len` / `--gpu-memory-utilization` tuning (O2/O8).
</decisions>

<specifics>
## Specific Ideas / constraints to honor

**Known gotchas (from project memory — MUST honor):**
- **Remote create must flatten `extends`** — `km create --remote` uploads raw
  child YAML; the create-handler Lambda has no `profiles/base/` fragments →
  instant "profile base/... not found". The operator binary flattens via
  `selectRemoteProfileYAML`/`yaml.Marshal(resolvedProfile)`. Verify the GPU
  leaves create cleanly (local AND remote path).
- **Schema change needs `km init --lambdas`** (refresh create-handler toolchain)
  — the new codex local-provider knob is a NEW schema field; remote create
  rejects until the Lambda's `toolchain/km` is refreshed. `toolchain/km` must be
  **linux/arm64** (a macOS binary breaks ALL cold-create with "exec format
  error").
- **Notify poller gated on `Spec.CLI != nil`** (userdata.go ~5610) — satisfied
  by base/platform; confirm the GPU leaves render the slack inbound poller.
- **Bool zero-value trap** in fragments (Phase 117) — keep mixed-bool blocks like
  `spec.runtime` (spot/hibernation/mountEFS) in the leaf, not a non-pointer-bool
  fragment. Validate MERGED bytes, not leaf-alone.
- **Frozen byte-identity goldens** — `pkg/compiler` has a FROZEN pre-92 baseline
  that the test strips SubagentStop from; do NOT re-capture it (hand-patch).
  Full-output goldens regen via CAPTURE_* flags.
- **Poller bash needs live UAT** — SKILL/poller bash is invisible to Go goldens;
  the Slack codex path must be live-UAT'd.
- **`runReconnectingPortForward`** is the shared SSM forward helper
  (`internal/app/cmd/shell.go:778`); `km vscode start` (vscode.go:196) and
  `km desktop start` (desktop.go:206) are the two existing consumers to mirror.
- **AWS quota CLEARED:** G+VT = 768 vCPU covers g6e.12x (48) and 48x (192). No
  increase needed. AWS profile = `klanker-application` (account 052251888500).

**Deploy surface:** `make build` (operator binary — km model start, extends
flattening) + `make build-lambdas` + `km init --dry-run=false` (NOT `--sidecars`
— the schema field + create-handler toolchain need a full apply). Existing
sandboxes don't gain the GPU profiles retroactively (resolved at create time).
</specifics>

<deferred>
## Deferred Ideas
- Goose as a first-class `agent_type` (Path 2) — reusable for any local model;
  needs poller surgery + golden tests + live UAT. Follow-on phase.
- On-box claude default repointed at the local model — kept cloud for the A/B.
- Kimi K2 (~1T MoE, ~500GB @ 4-bit) — needs gated P-family (p4de/p5, 640GB+).
- Local-VS-Code-with-port-forward as a documented alternative to Remote-SSH.
- Multi-model hot-swap / model router on one box.
</deferred>

---

*Phase: 122-gpu-vllm-model-serving-sandbox-profiles-plus-local-model-chat*
*Context derived 2026-06-27 from the brainstorming design spec.*
