# GPU vLLM model-serving sandbox profiles — design

**Date:** 2026-06-27
**Status:** Approved (brainstorming) → ready for GSD Phase 122 planning
**Author:** operator + Claude (brainstorming session)

## Problem / goal

The km platform now has a fully operational EC2 sandbox layout (composable
inheritance, network enforcement, learn mode, AMI bake, VS Code Remote-SSH).
The operator wants to **run a large local LLM on a GPU EC2 instance** and front
it with a VS Code coding assistant — without leaving the km workflow.

Concretely: a small family of SandboxProfiles that stand up a GPU instance,
serve a 70B-class model over an OpenAI-compatible endpoint, and let the
operator point a VS Code plugin at it via `km vscode start`.

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
| km claude agent | **Keep** (inherited from `base/userinit` + `base/platform`) | Incidental to serving but handy for `km agent run`. |

## The profile matrix (4 leaves)

| Profile | Instance | VRAM | Model (`--served-model-name local`) | Precision | TP | ~$/hr (on-demand) |
|---|---|---|---|---|---|---|
| `profiles/gpu-qwen-12x.yaml`  | g6e.12xlarge | 192GB | `Qwen/Qwen2.5-72B-Instruct-AWQ` | AWQ 4-bit | 4 | ~$10.5 |
| `profiles/gpu-llama-12x.yaml` | g6e.12xlarge | 192GB | `meta-llama/Llama-3.3-70B-Instruct` | FP8 | 4 | ~$10.5 |
| `profiles/gpu-qwen-48x.yaml`  | g6e.48xlarge | 384GB | `Qwen/Qwen2.5-72B-Instruct` | FP16 | 8 | ~$30 |
| `profiles/gpu-llama-48x.yaml` | g6e.48xlarge | 384GB | `meta-llama/Llama-3.3-70B-Instruct` | FP16 | 8 | ~$30 |

All four advertise the served model as **`local`** so a single Continue config
works everywhere.

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

### Each leaf sets ONLY its deltas (~15 lines)

- `extends: [base/os/debian, base/network/safenetwork, base/userinit, base/platform, base/gpu/serve]`
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

1. **File AWS quota increases today** — "Running On-Demand G instances" vCPU
   limit defaults to **0**; a g6e.12xlarge needs 48 vCpu, 48xlarge needs 192.
   1–2 day turnaround. Hard gate before any `km create` succeeds.
2. Resolve the current DLAMI AMI ID for the region (O1).
3. (Llama) accept the Meta license on HF, SOPS-encrypt the token.
4. **First bring-up of each size in a relaxed-enforcement / learn variant** to
   shake out DLAMI-vs-km-networking friction (R2) and the MITM-on-large-download
   concern (R1'), confirm `nvidia-smi` sees all GPUs and vLLM serves, *then*
   trust the locked-down production profile.
5. `km create profiles/gpu-qwen-12x.yaml --alias gpu1`
6. `km vscode start gpu1` → connect Remote-SSH → install the Continue extension
   (it reads the pre-seeded config) → chat hits `localhost:8000`.

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

## Out of scope (YAGNI)

- Frontier MoE models (Kimi K2 ~1T, GLM-4.6 355B) — not feasible/affordable on a
  single km sandbox; explicitly declined in brainstorming.
- llama.cpp / SGLang / TGI serving stacks — vLLM chosen.
- Local-VS-Code-with-port-forward access path — Remote-SSH chosen (variant may be
  documented, not built).
- Slack per-sandbox notifications — omitted for focus (addable via
  `base/slack-persandbox`).
- Multi-model hot-swap / model router on one box — one model per profile.
