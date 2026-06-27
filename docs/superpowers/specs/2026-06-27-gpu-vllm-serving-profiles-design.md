# GPU vLLM model-serving sandbox profiles — design

**Date:** 2026-06-27
**Status:** Approved (brainstorming) → ready for GSD Phase 122 planning
**Scope note:** Phase 122 grew during brainstorming and now spans three
deliverables: (1) the 7 GPU serving profiles, (2) Slack chat-with-resume against
the local model via an on-box codex repoint, and (3) `km model start` — a
laptop-side SSM port-forward to the model, including an on-box Anthropic↔OpenAI
shim so local **Claude Code** can drive the remote model. The phase is sizable;
the GSD plan should break it into waves (profiles → codex/Slack → `km model
start` + shim → live UAT). Goose-as-first-class-agent (Path 2) remains a deferred
follow-on.
**Author:** operator + Claude (brainstorming session)

## ⚠ Research correction (2026-06-27, supersedes details below — see `122-RESEARCH.md` O7)

The GSD research pass found that **Codex now requires the OpenAI Responses API**
(Feb 2026), and **vLLM serves only Chat Completions** — so codex **cannot** point
directly at vLLM `:8000` as written below. A **LiteLLM gateway is mandatory**, and
LiteLLM *also* serves the Anthropic Messages API. Net architecture change:

- **A single on-box LiteLLM on `:8001` is now a CORE component** (not just the
  optional `--anthropic` shim). It fronts vLLM `:8000` and serves all three
  dialects: Chat Completions (passthrough), **Responses** (for codex), and
  **Messages** (for Claude Code).
- The codex local-provider knob points at **`:8001`** with `wire_api = "responses"`
  — not `:8000`.
- `km model start` passthrough and Continue may still hit vLLM `:8000` directly
  (chat-completions), but codex/Claude-Code go through LiteLLM `:8001`.
- Confirmed AMI: `ami-0a9d213b92dabc044` (us-east-1 DLAMI Ubuntu 24.04, raw ID
  passes through `isRawAMIID()`; Ubuntu userdata path is chosen at runtime from
  `/etc/os-release`). Quant repos: `cyankiwi/GLM-4.5-Air-AWQ-4bit`,
  `QuantTrio/GLM-4.6-AWQ`, `btbtyler09/Kimi-Dev-72B-GPTQ-8bit` (no AWQ).

The rest of this spec stands; treat `122-RESEARCH.md` as authoritative where they
differ.

## Problem / goal

The km platform now has a fully operational EC2 sandbox layout (composable
inheritance, network enforcement, learn mode, AMI bake, VS Code Remote-SSH).
The operator wants to **run a large local LLM on a GPU EC2 instance** and front
it with a VS Code coding assistant — without leaving the km workflow.

Concretely: a small family of SandboxProfiles that stand up a GPU instance,
serve a 70B-class model over an OpenAI-compatible endpoint, let the operator
point a VS Code plugin at it via `km vscode start`, chat with it through the
existing km-slack inbound semantics (per-thread, with resume) by repointing the
on-box codex agent at the local endpoint, **and reach it from local laptop
development via a new `km model start` SSM port-forward — including an on-box
Anthropic↔OpenAI shim so local Claude Code can drive the remote model.**

## Decisions locked in brainstorming

| Decision | Choice | Rationale |
|---|---|---|
| Model tier | Multi-GPU big (70B–120B) | Operator wants real quality, not a 7B toy. |
| Serving stack | **vLLM** | Multi-GPU tensor-parallel, OpenAI-compatible API, broadest model + quant coverage. llama.cpp rejected (weak multi-GPU). |
| GPU base | **AWS Deep Learning Base OSS Nvidia Driver GPU AMI (Ubuntu 24.04)** | Drivers + CUDA + Docker + nvidia-container-toolkit pre-baked → vLLM is one `docker run`. |
| Access path | **VS Code Remote-SSH onto the box** (`km vscode start`) | Plugin runs ON the box, hits `http://localhost:8000` — no port-forward of the model, no auth, no metering, fully private. |
| Weights cache | **`additionalVolume` 300GB + `teardownPolicy: stop`** | Pull weights once; volume survives pause/resume. Lost only on full `km destroy`. |
| Models | **Qwen2.5-72B-Instruct (ungated)** + **Llama-3.3-70B-Instruct (gated)** | Qwen = zero-friction default; Llama = useful, needs `HF_TOKEN`. |
| Sizes | g6e.12xlarge (4×L40S) + g6e.48xlarge (8×L40S) | 12x = quantized/cheaper; 48x = full fp16/max quality. |
| Slack chat agent | **codex repointed at `localhost:8000`** (`agent.default: codex`) | Reuses the existing first-class codex `agent_type` + `codex exec resume` per-thread continuity. One small `synthesizeCodexConfig` change. Path 2 (goose) deferred. |
| km claude agent | **Keep**, points at cloud (Bedrock/Anthropic) | "Keep claude" satisfied. Box gains a duality: `/claude` = cloud, default/`/codex` = local 70B, both with thread resume — A/B in one channel. `km agent run --claude` still works. |

## The profile matrix (4 leaves)

| Profile | Instance | VRAM | Model (`--served-model-name local`) | Precision | TP | ~$/hr (on-demand) |
|---|---|---|---|---|---|---|
| `profiles/gpu-qwen-12x.yaml`  | g6e.12xlarge | 192GB | `Qwen/Qwen2.5-72B-Instruct-AWQ` | AWQ 4-bit | 4 | ~$10.5 |
| `profiles/gpu-llama-12x.yaml` | g6e.12xlarge | 192GB | `meta-llama/Llama-3.3-70B-Instruct` | FP8 | 4 | ~$10.5 |
| `profiles/gpu-qwen-48x.yaml`  | g6e.48xlarge | 384GB | `Qwen/Qwen2.5-72B-Instruct` | FP16 | 8 | ~$30 |
| `profiles/gpu-llama-48x.yaml` | g6e.48xlarge | 384GB | `meta-llama/Llama-3.3-70B-Instruct` | FP16 | 8 | ~$30 |
| `profiles/gpu-glmair-12x.yaml` | g6e.12xlarge | 192GB | `zai-org/GLM-4.5-Air` (106B MoE) | 4-bit/FP8 | 4 | ~$10.5 |
| `profiles/gpu-kimidev-12x.yaml` | g6e.12xlarge | 192GB | `moonshotai/Kimi-Dev-72B` (72B dense, Qwen2 arch) | AWQ 4-bit | 4 | ~$10.5 |
| `profiles/gpu-glm46-48x.yaml` | g6e.48xlarge | 384GB | `zai-org/GLM-4.6` (355B MoE) — 4-bit community quant | 4-bit (AWQ/GPTQ) | 8 | ~$30 |

All seven advertise the served model as **`local`** so a single Continue config
works everywhere.

**Feasibility note (corrects an earlier brainstorm overstatement):** "frontier
MoE" is NOT uniformly out of reach. MoE memory scales with *total* params (all
experts resident), so GLM-4.6 (355B) ≈ ~190GB at 4-bit → **fits g6e.48xlarge
(384GB) on existing G-quota.** GLM-4.5-Air (106B) and Kimi-Dev-72B (dense) fit
the 12x. The 8×L40S box is PCIe (no NVLink) so flagship-MoE throughput is
modest-but-usable, not blazing. **Only Kimi K2 (~1T, ~500GB @ 4-bit) remains out
of scope** — it exceeds 384GB and needs P-family (p4de/p5, 640GB+) with a gated
quota request; see Out of scope.

## Architecture / data path

```
Laptop ── km vscode start ──▶ SSM port-forward (sshd:22 ONLY)
                                   │
                              VS Code Remote-SSH  (edit files + run Continue ON the box)
   ┌────────────────────────────────┴───────────────────────────────┐
   │  g6e.12x/48x  (4–8× L40S)  — DLAMI Ubuntu 24.04                  │
   │                                                                 │
   │   Continue plugin ──▶ http://localhost:8000/v1   (no auth)      │
   │                          │                                      │
   │                   vllm.service (systemd → docker, --restart)    │
   │                          │  reads /etc/km/vllm.env              │
   │                   /data/hf  (300GB EBS, HF_HOME, weight cache)  │
   └─────────────────────────────────────────────────────────────────┘
```

The entire inference path is `localhost` on the box. Nothing crosses the
network at request time; nothing is metered by the http-proxy MITM.

## Slack chat with resume (folded into Phase 122)

The km Slack inbound poller (rendered into userdata by `pkg/compiler/userdata.go`)
is already a generic per-thread conversational dispatcher: per-thread session
continuity (`agent_session_id` + `agent_type` in `km-slack-threads`), resume
branched per agent (`claude --resume` / `codex exec resume`), 👀 ack,
mention/thread gating, allowlist, per-thread parallelism, streaming drain,
`blocks-rich` rendering, `/claude` `/codex` verbs. **The model behind the agent
is orthogonal to all of these.** CLAUDE.md states the intent: "Future agents
(Goose etc.) slot in as new `agent_type` enum values."

So Slack-chat-with-resume against the local model reduces to: **point an on-box
agent runtime with a resume concept at `localhost:8000`.** Codex is already a
first-class `agent_type` in the poller and is installed by `base/userinit`, so
this is the cheapest path.

**Self-contained box:** the poller dispatches the agent *on the same instance*
that serves the model. Slack @-mention → on-box `codex exec` → `localhost:8000`
→ local 70B → threaded reply, with `codex exec resume` continuity. Nothing
metered, fully private, identical km-slack UX.

### What the codex repoint requires

1. **`synthesizeCodexConfig` extension** (`pkg/compiler/agent_codex.go:67`).
   Today it emits inert hook blocks only — no model provider. Add a
   `[model_providers.local]` block (`base_url = "http://localhost:8000/v1"`,
   `model_provider = "local"`, `model = "local"`, dummy env key) emitted when a
   new profile knob is set. Knob shape TBD in the plan (O6) — likely
   `spec.agent.codex.baseURL` + `spec.agent.codex.model`.
2. **`spec.agent.default: codex`** in `base/gpu/serve` so Slack inbound + `km
   shell`/`km agent run` default to the local model. `claude` stays installed and
   cloud-pointed; `/claude` verb routes to it.
3. **Extend `base/slack-persandbox`** in all 7 leaves (per-sandbox channel +
   `notification.slack.inbound.enabled` → provisions the inbound FIFO + poller).
   The poller is only emitted when `Spec.CLI != nil` — satisfied by
   `base/platform` (`cli.noBedrock: true`). (Memory: notify/poller gated on
   `spec.cli`.)

No `km-slack-threads` schema change (the session-id column is already
agent-agnostic). No bridge Lambda change. The compiler change + profile wiring
is the whole delta beyond the serving profiles.

### On-box interfacing is free (the repoint is box-global)

`agent.default: codex` + the local-provider config is **not Slack-specific** —
it repoints codex for the whole box. So the local 70B is reachable through
**four interfaces with zero additional work**:

1. **VS Code** — Continue plugin → `localhost:8000` (Remote-SSH).
2. **Terminal** — `km shell gpu1` → run `codex` interactively → local model.
3. **Headless** — `km agent run gpu1 --codex --prompt "…"`, the prompt queue,
   `km at … agent run --codex` → local model.
4. **Slack** — @-mention → on-box codex turn → local model, with resume.

All `localhost`, all unmetered, all private. "Interface it on the sandbox" is
comprehensively yes for codex.

**claude stays cloud (deliberate).** Claude Code speaks the Anthropic Messages
API (`/v1/messages`), not OpenAI's shape, so it cannot point straight at vLLM —
it would need an Anthropic↔OpenAI translation sidecar (LiteLLM `/v1/messages` /
a `claude-code-proxy` shim) + `ANTHROPIC_BASE_URL`. Out of scope (see deferred):
we keep claude cloud-pointed so `/claude` (cloud) vs default `/codex` (local 70B)
remains a live A/B in the same channel, and we avoid the higher reliability risk
of driving Claude Code's agent loop with an open 70B.

### Deferred: Path 2 — goose as a first-class agent_type

The codebase-anticipated clean form (goose natively supports OpenAI-compatible
providers + session resume). Requires a real `goose` branch in the poller
dispatch (first-turn + resume + session-id capture + reply-post), goose config
synthesis → `localhost:8000`, `EFFECTIVE_AGENT` handling, optional `/goose`
verb, golden tests, and mandatory live UAT (poller bash is invisible to Go
goldens). Reusable for any future local model. Out of Phase 122; documented as a
follow-on once the codex repoint proves the vision.

## Local dev against the model — `km model start`

A new operator command that brings the remote model to the **laptop**, mirroring
`km vscode start` / `km desktop start` (which both already use
`runReconnectingPortForward` in `internal/app/cmd/shell.go` with a liveness
probe + auto-reconnect). New file `internal/app/cmd/model.go`; a third consumer
of the same helper with an HTTP probe (`GET /v1/models`).

```
km model start <sandbox> [--local-port 8000] [--anthropic]
km model status <sandbox>
```

### Layer 1 — OpenAI passthrough (default)

SSM port-forward `laptop 127.0.0.1:8000 → sandbox:8000` (vLLM), auto-reconnect,
SSM-only (no SG/public change). Prints ready-to-paste config. The laptop now has
an **OpenAI-shaped** endpoint at `http://localhost:8000/v1`, model `local`, dummy
key — works immediately with **codex (local), Continue/Cline on the laptop,
aider, the OpenAI SDK, curl**. "Local dev against the remote model" with codex is
free the moment this exists.

### Layer 2 — Anthropic shim (`--anthropic`) for local Claude Code

Claude Code speaks the Anthropic Messages API (`/v1/messages`), not OpenAI, so a
raw forward won't drive it. `base/gpu/serve` runs a small **Anthropic↔OpenAI
translation shim** on the box (LiteLLM `/v1/messages`, or a `claude-code-proxy`)
on `:8001` over vLLM `:8000`. `km model start --anthropic` forwards `:8001`
instead; the operator sets `ANTHROPIC_BASE_URL=http://localhost:8001` + a dummy
key and their **laptop Claude Code** drives the remote 70B.

**Scope boundary — two different "claudes":**
- The **on-box** claude (used by Slack `/claude`, `km agent run --claude`) stays
  **cloud-pointed** — unchanged, preserves the cloud-vs-local A/B.
- The shim serves the **laptop** Claude Code via the forwarded port only. It does
  NOT repoint the on-box agent default (still codex→local).

### Caveats (carried into UAT)

- **Claude Code on an open 70B is the fussiest combination** (tuned hard for
  Claude's tool-call/thinking format). Fine for chat + light tasks; flakier on
  heavy agentic loops than codex-on-local. Highest-risk of the local-dev paths.
- The shim is an extra on-box component to run/health-check (R7).

## Inheritance structure (DRY via Phase 117 composable inheritance)

### New abstract fragment: `profiles/base/gpu/serve.yaml` (`metadata.abstract: true`)

Holds the common ~90%:

- **AMI:** `spec.runtime.ami: <DLAMI raw AMI ID>` (region-resolved — see Open
  Question O1).
- **Weights volume:** `additionalVolume { size: 300, mountPoint: /data }`,
  `HF_HOME=/data/hf` via `execution.env`.
- **vLLM systemd unit:** `configFiles["/etc/systemd/system/vllm.service"]` —
  `ExecStart=docker run --rm --gpus all -p 127.0.0.1:8000:8000 -v /data/hf:/root/.cache/huggingface --env-file /etc/km/vllm.env vllm/vllm-openai:latest --model ${VLLM_MODEL} --tensor-parallel-size ${VLLM_TP} --served-model-name local ${VLLM_EXTRA}` with `EnvironmentFile=/etc/km/vllm.env`.
- **Enable + start** the unit (`systemctl enable --now vllm`) via `initCommandsAppend`.
- **Continue config:** `configFiles["/home/sandbox/.continue/config.yaml"]` →
  `apiBase: http://localhost:8000/v1`, `model: local`, `apiKey: dummy`,
  `provider: openai`.
- **Budget override:** `budget.compute.maxSpendUSD` raised off the `base/platform`
  default of `0.50` (which would suspend the box in minutes). Base sets a sane
  floor; 48x leaves raise it.
- **Lifecycle:** `ttl: 8h`, `idleTimeout: 1h`, `teardownPolicy: stop`, on-demand
  (`spot: false` — GPU spot capacity is unreliable; an interruption kills the
  session).
- **Slack chat agent:** `spec.agent.default: codex` + the codex local-provider
  knob (`base_url: http://localhost:8000/v1`, model `local`) so Slack inbound
  dispatches the local model. claude stays cloud-pointed.
- **Anthropic shim sidecar:** a LiteLLM (`/v1/messages`) / `claude-code-proxy`
  service on `:8001` over vLLM `:8000`, installed + enabled by the fragment, for
  the `km model start --anthropic` laptop-Claude-Code path. Bound to localhost;
  reached only over the SSM forward.

### Each leaf sets ONLY its deltas (~15 lines)

- `extends: [base/os/debian, base/network/safenetwork, base/userinit, base/platform, base/slack-persandbox, base/gpu/serve]`
- `spec.runtime.instanceType` — `g6e.12xlarge` | `g6e.48xlarge`
- `configFiles["/etc/km/vllm.env"]` — `VLLM_MODEL=…`, `VLLM_TP=4|8`,
  `VLLM_EXTRA=--quantization awq …` (per-leaf, differs by model+precision).
  (configFiles is a map → leaf key merges cleanly alongside the base's unit-file key.)
- 48x leaves: raise `budget.compute.maxSpendUSD` to ~$300.
- **Llama leaves only:** `iam.allowedSecretPaths: [<HF_TOKEN ssm path>]` +
  `HF_TOKEN` injected into `/etc/km/vllm.env` (see Secrets below).

> **Inheritance gotcha (Phase 117 bool zero-value trap):** keep mixed-bool
> blocks like `spec.runtime` (spot/hibernation/mountEFS) in the leaf or a
> pointer-bool fragment, NOT a fragment that writes non-pointer `false` zero
> values onto children. Validate the merged bytes, not the leaf alone.

## Secrets — Llama `HF_TOKEN` (gated model)

Only the two **Llama** leaves are gated. Qwen, GLM-4.5-Air, GLM-4.6, and
Kimi-Dev-72B (and their community quant repos) are ungated → no token needed.

Llama-3.3 requires accepting Meta's license on HuggingFace and authenticating
the weight pull with a HF token.

- Encrypt the token with SOPS using the **`klanker-application`** AWS profile and
  the shared secrets KMS key (`km bootstrap --shared-secrets-key`, Phase 89).
- Declare the SSM path in the llama leaves' `iam.allowedSecretPaths`.
- The boot path materializes the value and writes `HF_TOKEN=…` into
  `/etc/km/vllm.env` before `systemctl start vllm`.
- Exact mechanism (SSM SecureString vs SOPS file decrypt at boot) follows
  `docs/sandbox-secrets.md` — to be nailed in the GSD plan.

Qwen leaves need no secret.

## Bring-up procedure (operator runbook, to be written in the plan)

1. **AWS GPU quota — ALREADY SATISFIED (verified 2026-06-27).** "Running
   On-Demand G and VT instances" = **768 vCPU** in this account (klanker
   application account, us-east-1). g6e.12xlarge needs 48, g6e.48xlarge needs
   192 — both fit with large headroom (~4 concurrent 48xlarge). **No
   quota-increase request needed; the live UAT is unblocked.** (P-family = 76
   vCPU would NOT fit a p4d.24xlarge=96, but we chose G/g6e, not P.)
2. Resolve the current DLAMI AMI ID for the region (O1).
3. (Llama) accept the Meta license on HF, SOPS-encrypt the token.
4. **First bring-up of each size in a relaxed-enforcement / learn variant** to
   shake out DLAMI-vs-km-networking friction (R2) and the MITM-on-large-download
   concern (R1'), confirm `nvidia-smi` sees all GPUs and vLLM serves, *then*
   trust the locked-down production profile.
5. `km create profiles/gpu-qwen-12x.yaml --alias gpu1`
6. `km vscode start gpu1` → connect Remote-SSH → install the Continue extension
   (it reads the pre-seeded config) → chat hits `localhost:8000`.
7. **Slack chat:** @-mention the bot in the per-sandbox channel → confirm the
   on-box codex turn hits `localhost:8000` and replies in-thread; send a
   follow-up to confirm `codex exec resume` continuity. `/claude` in the same
   thread should route to cloud Claude (the local-vs-cloud A/B). This step
   exercises R6/O7 — the codex↔vLLM compatibility gate.

## Definition of done

**Full live UAT** (operator's call). Phase 122 is done when:

1. `base/gpu/serve` fragment + 7 leaf profiles authored; `km validate` green on
   all 7 (merged-bytes validation).
2. `synthesizeCodexConfig` local-provider change merged with unit tests +
   updated goldens.
3. A real `km create` of at least one size → DLAMI boots → `nvidia-smi` sees all
   GPUs → weights pull to `/data` → `vllm.service` serves `local` on
   `localhost:8000`.
4. VS Code Remote-SSH + Continue completes a chat against the served model.
5. **Slack codex round-trip + resume** verified in the per-sandbox channel
   (settles R6/O7), and `/claude` confirmed routing to cloud.
6. **`km model start <sb>`** (passthrough) forwards the laptop to the model; a
   local codex/Continue/curl call against `localhost:8000/v1` returns a
   completion.
7. **`km model start <sb> --anthropic`** + the on-box shim: local **Claude Code**
   with `ANTHROPIC_BASE_URL=localhost:8001` completes a chat against the remote
   70B (settles R7; scoped to chat + light edits).

**Prerequisite status: GPU quota CLEARED** (768 G-family vCPU — step 1 of the
bring-up runbook). No external blocker remains for the live UAT.

## Risks

- **R1' — http-proxy MITM on multi-GB HuggingFace LFS downloads.** `safenetwork`
  is `enforcement: both` with `allowedHosts: "*"`, so egress is *permitted*, but
  the MITM proxy still sits in-path and TLS-intercepts the weight pull — possibly
  slow/memory-heavy for ~40–140GB. Mitigation: pull weights once during the
  relaxed-enforcement bring-up; they persist on `/data` so production boots don't
  re-download. The plan should validate proxy behavior on a large download.
- **R2 — km eBPF/proxy enforcement on the DLAMI.** km's Ubuntu userdata path
  (Phase 93: stops systemd-resolved, installs eBPF/proxy sidecars) is validated
  on *stock* Ubuntu, not the DLAMI (which ships its own Docker + networking).
  Real chance of friction. Mitigation: bring-up validation step 4; fall back to
  `enforcement: proxy` or relaxed if eBPF-on-DLAMI misbehaves.
- **R3 — `base/userinit` is heavy.** It installs goose/codex/claude/nvm/plugins —
  not needed for serving but the operator wants claude kept. Acceptable; note
  the boot-time cost. Could fork a lighter `base/userinit-claudeonly` later (out
  of scope).
- **R4 — Cost.** $10.5–$30/hr. Mitigated by `ttl: 8h` + `idleTimeout: 1h` +
  `teardownPolicy: stop` + raised-but-bounded `budget.compute.maxSpendUSD`.
  Operator must `km pause`/`km stop` when away.
- **R5 — `additionalVolume` not in `instance_ram.go` hibernation table for g6e.**
  Irrelevant: we use `teardownPolicy: stop`, not hibernation. The RAM-table check
  fails open for unknown types anyway.
- **R6 — Codex ↔ vLLM API-shape compatibility.** Codex CLI may expect OpenAI's
  Responses API or specific tool-call/streaming formats; vLLM serves
  `/v1/chat/completions` + partial Responses API. The codex repoint hinges on
  this working. Highest-risk unknown of the chat dimension → must be settled by
  UAT (a real `codex exec` round-trip against the served model) before the
  profiles are trusted. Fallback if incompatible: Path 2 (goose, which is
  designed for arbitrary OpenAI-compatible providers).
- **R7 — Anthropic shim reliability + Claude-Code-on-70B.** The LiteLLM/
  claude-code-proxy shim is an extra on-box component (health, restart, resource
  use). And Claude Code is the fussiest client to drive with an open 70B —
  tool-call/thinking-format adherence may be poor on heavy agentic loops.
  Mitigation: scope the `--anthropic` UAT to chat + light edits first; the
  passthrough (codex/Continue) path is the dependable one. Shim is localhost-only.

## Open questions for the GSD research/plan phase

- **O1 — DLAMI AMI ID resolution.** DLAMI IDs are region-specific and rotate per
  release. Decide: hardcode the current us-east-1 ID in the profile (simple,
  staleness risk) vs resolve at authoring time via the SSM public parameter
  (`/aws/service/deeplearning/ami/...`) / `describe-images` filter and document
  re-resolution. Confirm km's AMI-slug resolver passes a raw AMI ID through
  untouched and that the Ubuntu (`base/os/debian`) userdata path is selected.
- **O2 — Exact vLLM quant flags per leaf** (AWQ vs FP8 vs FP16) and
  `--max-model-len` / `--gpu-memory-utilization` tuning for each VRAM budget.
- **O3 — Secret injection mechanism** for `HF_TOKEN` (Phase 89 SSM SecureString
  vs SOPS-at-boot) and exactly where it writes into `/etc/km/vllm.env`.
- **O4 — Continue extension install** on Remote-SSH: pre-seed config is easy, but
  the extension itself installs on first connect. Document, or explore
  `code --install-extension` automation (the vscode-server isn't present until
  first connect — likely just document).
- **O5 — Whether to ship a 5th `*-learn` bring-up variant** (relaxed enforcement)
  or fold the bring-up into a documented `km shell --learn` step.
- **O6 — Codex local-provider knob shape.** New typed field
  (`spec.agent.codex.baseURL` + `model`?) vs a generic `spec.agent.localEndpoint`.
  How `synthesizeCodexConfig` emits `[model_providers.local]`. Confirm Codex 0.133
  honors a config.toml `model_provider`/`base_url` for a non-OpenAI host.
- **O9 — Anthropic shim choice + packaging.** LiteLLM proxy (`/v1/messages`
  passthrough) vs a lighter `claude-code-proxy`; install path (pip in
  `base/gpu/serve` initCommands vs a container); port (`:8001`); systemd unit;
  whether it maps the served `local` model to the names Claude Code sends. Confirm
  Claude Code honors `ANTHROPIC_BASE_URL` against a non-Anthropic host with a
  dummy key.
- **O8 — GLM/Kimi quant repos + vLLM arch support.** GLM-4.6 / GLM-4.5-Air need
  the `Glm4Moe` arch (confirm installed vLLM version supports it; `--trust-remote-code`?)
  and a concrete 4-bit community quant repo (AWQ/GPTQ/compressed-tensors) of
  adequate quality. Kimi-Dev-72B is Qwen2 arch (well-supported) — pick an AWQ
  repo. Per-model `--max-model-len` vs KV-cache memory for the flagship MoE on
  384GB. Confirm GLM-4.5-Air on 12x: 4-bit (~55GB) vs FP8 (~106GB) — both fit;
  pick for quality/throughput.
- **O7 — Codex API surface against vLLM** (R6): does Codex need the Responses API,
  and does the installed vLLM version expose enough of it? Tool-call format
  parity. Determines whether the repoint works as-is or needs vLLM flags
  (`--enable-auto-tool-choice`, a tool-call parser) / a Codex flag.

## Out of scope (YAGNI)

- **Kimi K2 (~1T MoE)** — ~500GB at 4-bit exceeds the 384GB g6e.48xlarge; needs
  P-family (p4de/p5, 640GB+) with a gated quota request (P = 76 vCPU today, need
  96–192) at ~$40–100/hr. The one frontier model genuinely out of reach on a
  single km G-tier box. (GLM-4.6 355B was reconsidered IN scope — see matrix.)
- Frontier-MoE *multi-node* serving (pipeline/tensor parallel across instances) —
  out of scope; one model per single-node box.
- llama.cpp / SGLang / TGI serving stacks — vLLM chosen.
- Local-VS-Code-with-port-forward access path — Remote-SSH chosen (variant may be
  documented, not built).
- **Goose as a first-class `agent_type` (Path 2)** — deferred follow-on; codex
  repoint proves the vision first. (Slack chat itself is now IN scope via codex.)
- *pi* as a chat agent — less integrated, resume story unknown; goose preferred
  if/when a Path-2 agent is added.
- **On-box claude default repointed at the local model** — out of scope. The
  on-box claude stays cloud-pointed (preserves the `/claude`-vs-`/codex` A/B).
  (NOTE: laptop Claude Code reaching the local model IS in scope, via the
  `km model start --anthropic` shim — that's a different, forwarded path and does
  not change the on-box agent default.)
- Multi-model hot-swap / model router on one box — one model per profile.
