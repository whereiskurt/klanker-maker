# GPU model serving (Phase 122)

Run a large local LLM (70B-class) on a GPU EC2 sandbox via **vLLM**, fronted by an
on-box **Bifrost** multi-provider gateway, and reach it through every km interface:
VS Code Remote-SSH, Slack chat-with-resume, on-box terminal/headless codex, and a
new `km model start` laptop port-forward (incl. local Claude Code).

> **Status (2026-06-27):** code-complete and unit-green (`go test ./...` 41 ok / 0
> FAIL; 20/20 profiles validate). **Live on-hardware UAT (G3–G9) is pending an AWS
> G-instance quota increase** — see [Prerequisites](#prerequisites). Behaviors below
> marked *(design)* are the intended contract, not yet hardware-verified.

## Profile matrix

All seven extend a new abstract fragment `profiles/base/gpu/serve.yaml` and serve
their model as `--served-model-name local` (so one Continue config / one codex knob
works everywhere). 12x = quantized/cheaper; 48x = full-precision/headroom.

| Profile | Instance | VRAM | Model | Precision | TP |
|---|---|---|---|---|---|
| `gpu-qwen-12x` | g6e.12xlarge | 192GB (4×L40S) | `Qwen/Qwen2.5-72B-Instruct-AWQ` | AWQ 4-bit | 4 |
| `gpu-llama-12x` | g6e.12xlarge | 192GB | `meta-llama/Llama-3.3-70B-Instruct` | FP8 | 4 |
| `gpu-qwen-48x` | g6e.48xlarge | 384GB (8×L40S) | `Qwen/Qwen2.5-72B-Instruct` | FP16 | 8 |
| `gpu-llama-48x` | g6e.48xlarge | 384GB | `meta-llama/Llama-3.3-70B-Instruct` | FP16 | 8 |
| `gpu-glmair-12x` | g6e.12xlarge | 192GB | `zai-org/GLM-4.5-Air` (106B MoE) | 4-bit/FP8 | 4 |
| `gpu-kimidev-12x` | g6e.12xlarge | 192GB | `moonshotai/Kimi-Dev-72B` (dense) | GPTQ 8-bit | 4 |
| `gpu-glm46-48x` | g6e.48xlarge | 384GB | `zai-org/GLM-4.6` (355B MoE) | 4-bit | 8 |

Only the two **Llama** leaves are gated (need `HF_TOKEN` after accepting the Meta
license). Qwen / GLM / Kimi are ungated. **Kimi K2 (~1T) is intentionally out of
scope** — it exceeds 384GB and needs gated P-family capacity.

## Architecture — vLLM + Bifrost multi-provider router

```
laptop ── km vscode start / km model start ──▶ SSM port-forward (sshd / :8001)
   ┌──────────────────────────────────────────────────────────────────────┐
   │  g6e GPU sandbox (DLAMI Ubuntu 24.04)                                  │
   │                                                                        │
   │  Continue ─────────────────▶ vLLM :8000 /v1 (chat-completions)         │
   │  codex / Claude Code / Slack ▶ Bifrost :8001 (router) ──┐              │
   │                                                         ├─▶ vLLM :8000 (local)
   │                                  routes by model name ──┤              │
   │                                                         ├─▶ Bedrock (SigV4, role)
   │                                                         └─▶ Anthropic / OpenAI (key)
   │  weights cached on /data (300GB EBS) · OTEL → :4318 (km tracing)       │
   └──────────────────────────────────────────────────────────────────────┘
```

**Why a gateway is core (O7):** Codex requires the OpenAI **Responses API** (Feb
2026); vLLM serves only Chat Completions — so codex cannot point straight at vLLM.
Bifrost translates Responses → vLLM, *and* serves the Anthropic Messages API for
Claude Code, *and* routes by model name to cloud providers. One endpoint, many
models — "new model = one route". (Bifrost is a ~40MB Go single binary, matching
km's sidecar model; LiteLLM is the documented fallback. Current vLLM also serves
`/v1/responses` natively, so codex is not strictly gateway-dependent, but routing
through Bifrost gives uniform multi-provider access.)

### Routes — the `model` string callers send (validated live, GPU-free)

Bifrost (`maximhq/bifrost:v1.6.0`, run as a docker systemd unit; config is a
mounted app-dir `config.json`, port via `APP_PORT=8001`) has **no named-route
config** — routing is implicit: the caller sends `model = "<provider>/<model-id>"`.
The `providers` block (Bedrock = region-only instance role; a `vllm-local` custom
OpenAI provider; key-gated `anthropic`/`openai`) is in
`configFiles["/etc/km/bifrost-config.json"]`.

| Intent | `model` string callers send | Auth | Verified |
|---|---|---|---|
| local vLLM | `vllm-local/local` | none | ⚠️ schema valid; 502 until vLLM serves :8000 (box-only) |
| Claude via Bedrock | `bedrock/us.anthropic.claude-sonnet-4-6` (or `…opus-4-8`) | **instance role / SigV4 — no key** | ✅ live |
| Claude direct | `anthropic/claude-sonnet-4-6` | `ANTHROPIC_API_KEY` (SOPS) | ⚠️ key-gated (box-only) |
| OpenAI gpt-oss via Bedrock | `bedrock/openai.gpt-oss-120b` **(no `-1:0` suffix — catalog 404s)** | **instance role — no key** | ✅ live (120b + 20b) |
| OpenAI frontier | `openai/gpt-5` | `OPENAI_API_KEY` | dormant (until key) |

**Gotchas (found in the live rehearsal):** gpt-oss IDs must drop the Bedrock
`-1:0` version suffix; Claude needs the `us.` inference-profile prefix (bare IDs
error "on-demand throughput isn't supported"). Endpoints: OpenAI/codex →
`http://localhost:8001/openai/v1` (`…/responses` and `…/chat/completions`), Claude
Code → `ANTHROPIC_BASE_URL=http://localhost:8001/anthropic`. Cloud routes egress
through km's MITM proxy → metered into `BUDGET#ai` rows automatically. Full
validation: `.planning/phases/122-*/122-BIFROST-VALIDATION.md`.

## Interfaces

The codex repoint (`spec.agent.default: codex`, `agent.codex.localBaseURL:
http://localhost:8001/v1`) is **box-global**, so the local model is reachable four ways:

1. **VS Code** — `km vscode start <id>` → Remote-SSH → install Continue (reads the
   pre-seeded `~/.continue/config.yaml` → `localhost:8000/v1`, model `local`).
2. **Slack** — @-mention the per-sandbox channel → on-box codex → Bifrost → local
   model → threaded reply, with `codex exec resume` continuity. `/claude` still
   routes to cloud (the cloud-vs-local A/B in one channel).
3. **Terminal / headless** — `km shell <id>` → `codex`, or `km agent run <id> --codex`.
4. **Laptop dev** — `km model start <id>`:
   - default: SSM-forward `localhost:8000` → OpenAI endpoint for codex/Continue/aider/curl.
   - `--anthropic`: forward Bifrost `:8001`; set `ANTHROPIC_BASE_URL=http://localhost:8001/anthropic`
     (Bifrost's Anthropic ingress path — **not** `/v1/messages`) + a dummy
     `ANTHROPIC_AUTH_TOKEN` → local **Claude Code** drives the remote model.
   - `km model status <id>` checks the gateway/forward health (`GET /v1/models`).

## Secrets

- **`ANTHROPIC_API_KEY`** (the `claude-anthropic` route): SOPS-encrypt with the
  `klanker-application` profile + the shared `alias/km-sandbox-secrets` KMS key into
  `secrets/gpu.enc.yaml`, referenced via `spec.secrets.sopsFile`. The sandbox role
  reads it at boot (never in operator context). See `docs/sandbox-secrets.md`.
  ```bash
  AWS_PROFILE=klanker-application aws ssm get-parameter --name /km/secrets/anthropic-api-key \
    --with-decryption --query Parameter.Value --output text \
    | sed 's/^/ANTHROPIC_API_KEY: /' > /tmp/a.yaml
  sops --config /dev/null --encrypt --kms 'arn:aws:kms:us-east-1:<acct>:alias/km-sandbox-secrets' \
    /tmp/a.yaml > secrets/gpu.enc.yaml && rm /tmp/a.yaml
  ```
- **`HF_TOKEN`** (Llama leaves only): same SOPS pattern after accepting the Meta
  Llama-3.3 license on HuggingFace.
- **Keyless routes** (`local`, `claude-bedrock`, `gpt-oss-bedrock`) need no secret —
  they use the sandbox instance role. The role must grant `bedrock:InvokeModel`
  (+ `…WithResponseStream`) for the Claude model IDs **and** `openai.gpt-oss-120b-1:0`
  / `openai.gpt-oss-20b-1:0` (verify — gpt-oss may not be in the default Bedrock allowlist).

## Prerequisites

1. **AWS G-instance quota.** New accounts default to **0** vCPU for "Running
   On-Demand G and VT instances" (`L-DB2E81BA`). g6e.12x needs 48, g6e.48x needs 192.
   **Verify against the target account/region** (not the org management account —
   quotas are per-account):
   ```bash
   AWS_PROFILE=klanker-application aws service-quotas get-service-quota \
     --service-code ec2 --quota-code L-DB2E81BA --region us-east-1
   ```
   Request an increase via `aws service-quotas request-service-quota-increase` if 0
   (~1–2 day turnaround; GPU asks can be gated).
2. **DLAMI AMI** — `base/gpu/serve.yaml` pins `ami-0a9d213b92dabc044` (Deep Learning
   Base OSS Nvidia Driver GPU AMI, Ubuntu 24.04, us-east-1). Re-resolve monthly with
   the `describe-images` command in the fragment comment.

## Deploy surface

- **Operator binary** (`km model start`, the `extends`-flatten path for remote
  create): `make build`.
- **Remote create** (the default for EC2 — the create-handler Lambda compiles the
  profile, and the new `agent.codex.localBaseURL` schema field must reach its
  embedded `toolchain/km`): `make build-lambdas` + `km init --dry-run=false` (a full
  apply — **not** `--sidecars`, which doesn't refresh the create-handler zip). The
  embedded `toolchain/km` must be **linux/arm64** or cold-create fails with "exec
  format error".
- **Local create** (`km create … --local`) uses the operator binary directly and
  needs no Lambda refresh — handy for one-off bring-ups.
- No new Terraform module / DDB table / bridge change. Existing sandboxes don't gain
  the GPU profiles retroactively (resolved at `km create` time).

## Cost & lifecycle

`base/gpu/serve` sets `ttl: 8h`, `idleTimeout: 1h`, `teardownPolicy: stop`,
`spot: false` (on-demand — GPU spot capacity is unreliable and an interruption kills
the session), and raises `budget.compute.maxSpendUSD` off the `base/platform` 0.50
default (12x → 120, 48x → 300; the 0.50 default would suspend a $10/hr box in
minutes). On-demand g6e.12x ≈ $10.5/hr, g6e.48x ≈ $30/hr — `km pause`/`km stop` when
idle; the `/data` weights volume survives stop, so resume is fast. `km destroy`
promptly after a UAT run.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `VcpuLimitExceeded: limit of 0` at create | G-instance quota is 0 in this account/region — request an increase (Prerequisites). |
| vLLM container loses GPUs after `systemctl daemon-reload` | DLAMI+systemd cgroup race — the unit has `ExecStartPre=/bin/sleep 5` + `--gpus all --ipc=host`; restart `vllm.service`. |
| vLLM OOM on bring-up | Lower `--gpu-memory-utilization` (0.90→0.80) and/or `--max-model-len` in the leaf's `/etc/km/vllm.env` (`VLLM_EXTRA`) and re-create. GLM/Kimi tuning is MEDIUM-confidence — adjust live. |
| `claude-anthropic` route 401/empty | `ANTHROPIC_API_KEY` not reaching Bifrost: Bifrost reads `/etc/km/bifrost-env`, while SOPS injects `/etc/sandbox-secrets.env` — ensure the env is wired into `bifrost.service` and that Bifrost expands `${ANTHROPIC_API_KEY}` in its JSON config. |
| `claude-bedrock` / `gpt-oss-bedrock` 403 | Sandbox role lacks `bedrock:InvokeModel` for that model ID — add the Claude + `openai.gpt-oss-*` IDs to the role's Bedrock allowlist. |
| Slack 👀 but no reply | Inbound poller not emitted — needs `Spec.CLI != nil` (satisfied by `base/platform`); check `systemctl is-active km-slack-inbound-poller` + `/etc/km/notify.env`. |
| Remote create: "profile base/gpu/serve not found" | The create-handler has no `profiles/base/` fragments — the operator binary must flatten `extends` before upload (refresh `toolchain/km` via `km init`). |

See also: `docs/sandbox-secrets.md` (SOPS), `docs/codex-parity.md` (codex/Slack
agent switching), `docs/slack-notifications.md` (inbound poller), `klanker:vscode`
skill (Remote-SSH), and the design spec
`docs/superpowers/specs/2026-06-27-gpu-vllm-serving-profiles-design.md`.
