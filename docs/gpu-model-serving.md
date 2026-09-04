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

All of them extend a new abstract fragment `profiles/base/gpu/serve.yaml` and serve
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
| `gpu-qwen38-oblit-12x` | g6e.12xlarge | 192GB | `OBLITERATUS/Qwen3.8-27B-OBLITERATED` (27.8B) | **BF16** | 4 |

**L4 capacity-fallback leaves.** When the L40S fleet (g6e) is capacity-dry in
us-east-1, these run the same stack on g6.12xlarge (4×L4 = 96GB, 48 vCPU). L4 has
roughly a third of the L40S memory bandwidth, so decode is noticeably slower —
fine for demos and batch, sluggish for interactive chat. Context is trimmed to fit
the smaller per-GPU headroom.

| Profile | Instance | VRAM | Model | Precision | TP | ctx |
|---|---|---|---|---|---|---|
| `gpu-qwen-12x-l4` | g6.12xlarge | 96GB (4×L4) | `Qwen/Qwen2.5-72B-Instruct-AWQ` | AWQ 4-bit | 4 | 16384 |
| `gpu-qwen-32b-l4` | g6.12xlarge | 96GB | `Qwen/Qwen2.5-32B-Instruct-AWQ` | AWQ 4-bit | 4 | 32768 |
| `gpu-qwen38-oblit-12x-l4` | g6.12xlarge | 96GB | `OBLITERATUS/Qwen3.8-27B-OBLITERATED` | **BF16** | 4 | 32768 |

**Single-GPU FP8 leaf — the capacity-reachable option.** Every leaf above needs a
4- or 8-GPU shape, and those are the shapes that go capacity-dry first. A live
`run-instances` sweep on 2026-08-22 (us-east-1) found `g6e.12xlarge` **dry in all
four offered AZs** and `g6.12xlarge` available in only one — while the
**single-L40S** shapes had capacity in two. Quota was never the constraint (64
G-vCPU, headroom in every AZ).

What forces the big shape is precision, not parameter count: 27.8B in BF16 is
~55.6GB, which fits on no single card. FP8 halves that to ~29.9GB, which fits one
L40S with room for KV.

| Profile | Instance | VRAM | Model | Precision | TP | ctx |
|---|---|---|---|---|---|---|
| `gpu-qwen38-oblit-fp8-4x` | g6e.4xlarge | 48GB (1×L40S) | `MrPewpy/Qwen3.8-27B-OBLITERATED-FP8` | **FP8** | 1 | 131072 |

$3.00/hr versus $10.49 for `g6e.12xlarge`, and 16 of the G-vCPU quota instead of
48. The weights are a third-party `llm-compressor` repack (`FP8_DYNAMIC`,
`ignore: [lm_head]`, `recipe.yaml` published); MMLU 80.70 vs 81.4 for BF16, and
the abliteration survives quantization. vLLM reads the `compressed-tensors` config
straight from `config.json`, so the leaf passes **no** `--quantization` flag —
adding one would override the checkpoint's own scheme.

Two settings are specific to the single-card shape and are documented in the leaf:

- **`--max-num-seqs 128`** (default 256). This architecture is hybrid — the
  Gated-DeltaNet linear-attention layers hold a fixed-size state per *sequence*,
  and vLLM allocates one "Mamba cache block" per concurrent decode slot from
  whatever VRAM the weights left. Only 177 fit here, so the default aborts
  CUDA-graph capture at startup with `max_num_seqs (256) exceeds available Mamba
  cache blocks (177)`. This is a **concurrency** ceiling, not a context one.
- **`VLLM_CACHE_ROOT`** pointed inside `/data/hf`. `torch.compile`'s Inductor and
  CUDA-graph cache otherwise lands in the container's writable layer, and the unit
  runs `docker run --rm` — so every restart re-compiled from scratch. Measured on
  this leaf: compilation **125.04s → 5.91s**, init engine 211.68s → 91.04s. `/data`
  survives `km stop`, so a stop/resume now returns warm.

Reported KV on a live box: **141,803 tokens**, i.e. 1.08× concurrency at the full
131072 context — enough, but only just.

**vLLM image is pinned** (`VLLM_IMAGE=vllm/vllm-openai:v0.27.1` in
`base/gpu/serve`, digest-identical to `:latest` when pinned). Two reasons: this
architecture only became supported in 0.27.1, and the `torch.compile` hash
includes the vLLM version — so a silent `:latest` bump discards every cached graph
fleet-wide. Override per-leaf by setting `VLLM_IMAGE` in its `/etc/km/vllm.env`;
the unit declares the default *before* `EnvironmentFile`, so the leaf wins.

(`gpu-rehearsal-cpu` is a GPU-free Docker rehearsal leaf for validating the Bifrost
config without burning GPU capacity — see `122-BIFROST-VALIDATION.md`.)

Only the two **Llama** leaves are gated (need `HF_TOKEN` after accepting the Meta
license). Qwen / GLM / Kimi / OBLITERATUS are ungated. **Kimi K2 (~1T) is
intentionally out of scope** — it exceeds 384GB and needs gated P-family capacity.

### Qwen3.8-27B-OBLITERATED — what differs from the other leaves

The two `gpu-qwen38-oblit-*` leaves are the first here to serve **native BF16 with
no `--quantization` flag** (27.78B params ≈ 55.6GB across 18 shards, ~13.9GB/GPU at
TP=4), the first to read their weights from **S3 rather than HuggingFace** (see
[Weight delivery](#weight-delivery-huggingface-pull-vs-s3-staging)), and the first
that needs a **non-`hermes` tool-call parser**:

- **`--tool-call-parser qwen3_xml`, not `hermes`.** This model's chat template emits
  the XML form (`<tool_call><function=name><parameter=x>`), not the Hermes
  `<tool_call>{json}` form every other GPU leaf uses. With `hermes`, serving looks
  healthy and codex is silently unable to call tools.
- **Architecture `Qwen3_5ForConditionalGeneration`** — hybrid linear attention
  (48 linear + 16 full-attention layers of 64) and multimodal (image + video).
  Supported by vLLM from **v0.27.1** (2026-08-11). If a future `vllm-openai:latest`
  regresses, pin the image tag in the `vllm.service` drop-in.
- **No 48xlarge variant, deliberately.** TP=8 would have to replicate the model's 4
  KV heads across 8 ranks for no throughput gain; TP=4 divides cleanly (24 attention
  heads, 4 KV heads, 48 linear value heads).
- **`--override-generation-config {"temperature":0,"repetition_penalty":1.15}`**
  bakes the model card's required sampling in server-side, so codex / Continue /
  Slack / `km model start` all inherit it with no per-client config. Thinking needs
  no flag — the shipped chat template already defaults it off, and enabling it
  reintroduces refusals.
- **JSON in `VLLM_EXTRA` must stay space-free.** The base unit passes `$VLLM_EXTRA`
  unbraced so systemd word-splits it; a space inside `{"video":0}` would shatter it
  into broken argv. (Inner double quotes *are* safe: an unquoted `EnvironmentFile`
  value enters systemd's `VALUE` state, where only `\` and newline are special.)
- **Abliterated model** — refusal directions removed from the weights. The card is
  explicit that a system prompt or thinking mode partially reintroduces refusals, so
  the serving flags send neither. Treat this box's output as unfiltered by
  construction, and note that `base/network/safenetwork` gives it `*`/`*` egress.

## Weight delivery: HuggingFace pull vs S3 staging

Most leaves let vLLM pull weights from HuggingFace on first boot into `/data/hf`.
That re-downloads on every fresh sandbox and depends on the upstream repo staying
public and unmodified.

The `gpu-qwen38-oblit-*` leaves instead read **pre-staged weights from S3**. Stage
them once per install:

```bash
# On a box with fast network and >=60GB free disk — a running km sandbox is ideal.
# NOT a laptop: this moves ~111GB total (down, then back up).
. /etc/profile.d/km-identity.sh          # exports KM_ARTIFACTS_BUCKET
scripts/stage-model-to-s3.sh             # --bucket/--slug/--repo to override
```

This lands the BF16 safetensors at `s3://$KM_ARTIFACTS_BUCKET/models/qwen38-oblit-27b/`,
excluding the repo's GGUF (5 quantizations) and MLX (4-bit + 8-bit) trees — well over
100GB that nothing in this stack uses.

The FP8 leaf reads a **different slug**, so stage it separately (~30GB, one file
set, no GGUF/MLX trees to exclude):

```bash
scripts/stage-model-to-s3.sh \
  --repo MrPewpy/Qwen3.8-27B-OBLITERATED-FP8 \
  --slug qwen38-oblit-27b-fp8
```

The `--slug` MUST match `MODEL_SLUG` in the leaf's `km-stage-model.sh`; a mismatch
shows up as a permanently crash-looping `vllm.service` complaining the weights are
not staged. Staging *from the GPU box itself* is usually right when capacity is
tight — the instance is the scarce resource, not the ~$1.50 of GPU time, and
`vllm.service` crash-looping while the upload runs is harmless and self-healing.

The FP8 leaf also derives a patched chat template next to the weights at staging
time — see Troubleshooting for why.

At boot the leaf's `initCommands` install `/usr/local/bin/km-stage-model.sh` plus a
`vllm.service.d/10-stage-model.conf` drop-in that runs it as an `ExecStartPre`.
Three details are load-bearing:

- **`TimeoutStartSec=3600` in the drop-in.** systemd's 90s default start timeout
  applies to `ExecStartPre` and would kill a 55.6GB sync mid-flight.
- **`VLLM_MODEL` is the in-container path** (`/root/.cache/huggingface/models/…`).
  The base unit mounts `-v /data/hf:/root/.cache/huggingface`, so a host path does
  not resolve inside the container.
- **The writes must land in `initCommands`, not `initCommandsAppend`.** All
  `initCommands` precede every `initCommandsAppend`, so the drop-in exists before
  `base/gpu/serve` runs `systemctl daemon-reload && systemctl enable --now
  vllm.service` — picked up on vLLM's first start, no restart, no wasted partial
  download.

The staging script verifies every shard named in `model.safetensors.index.json`
before uploading, and `km-stage-model.sh` verifies the same set after syncing. If
S3 is not staged yet the unit fails loudly in `systemctl status vllm` rather than
crash-looping on a half-populated model dir — and because the base unit sets
`Restart=on-failure`/`RestartSec=30`, a box booted *while* staging is still
uploading heals itself once the upload completes.

**No IAM change is required.** The sandbox role already carries `Action:"*"` on
`Resource:"*"` scoped to `iam.allowedRegions` (`ec2spot_region_lock`, ec2spot
`main.tf`), which is what the boot-time sidecar `s3 cp` calls rely on.

**These leaves are account-portable.** The bucket is resolved at boot from
`KM_ARTIFACTS_BUCKET` rather than hardcoded, so the same profile works against any
install — just stage the weights into the bucket of whichever install you run in.

Escape hatch: set `VLLM_MODEL=OBLITERATUS/Qwen3.8-27B-OBLITERATED` in
`/etc/km/vllm.env` and `systemctl restart vllm` to pull direct from HuggingFace.

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

## Cloud routing: two modes (`local` always works either way)

The Bifrost gateway builds `config.json` at boot with `jq`, including a provider
**only when it's usable**. A GPU profile picks one of two cloud-routing modes:

| Mode | How to select | Bifrost providers |
|---|---|---|
| **Direct keys** (default) | `spec.secrets.sopsFile` referencing a bundle with `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` | `vllm-local` + `anthropic` (if its key present) + `openai` (if its key present) |
| **Keyless Bedrock** | drop `spec.secrets`, set **`spec.iam.allowBedrock: true`** | `vllm-local` + `bedrock` (instance-role SigV4, no keys) |

`spec.iam.allowBedrock` (generic `*bool`, default off) grants the role Bedrock IAM
**without** `useBedrock`'s agent-env injection (the on-box `claude` stays
cloud-pointed). It also writes `/etc/km/bedrock.enabled`, which the jq config reads
to add the `bedrock` provider. An empty key value (`ANTHROPIC_API_KEY=`) is treated
as absent. The two modes can be combined (keys *and* allowBedrock) — all providers
appear.

**Bedrock IAM note:** the GA OpenAI-OSS models (`bedrock/openai.gpt-oss-120b`) use
the newer **`bedrock-mantle:CreateInference`** action, not classic
`bedrock:InvokeModel` — both are granted by the `ec2spot_bedrock` policy (active
when `useBedrock || allowBedrock`).

## Secrets (direct-keys mode)

Each GPU leaf references its **own** SOPS bundle via `spec.secrets.sopsFile`
(`secrets/<leaf>.enc.yaml`): qwen/glm/kimi carry `ANTHROPIC_API_KEY` +
`OPENAI_API_KEY`; the Llama leaves add `HF_TOKEN` (gated model). Since Phase 133 the
bundle is never decrypted to disk (never in operator context either): only the
ciphertext lands on the box, and the `km-secretsd` broker decrypts per request. The
bring-up therefore asks for the keys explicitly —
`km-env exec --only ANTHROPIC_API_KEY,OPENAI_API_KEY -- ...` writing
`/etc/km/bifrost-env`, and `km-env exec --only HF_TOKEN -- ...` writing
`/etc/km/vllm.env` — rather than grepping the deleted
`/etc/sandbox-secrets.env`. See `docs/brokered-secrets.md`
§ Migrating a non-agent secret consumer.

Bundles are encrypted with the shared `km-sandbox-secrets` KMS key via the repo
`.sops.yaml` creation rule (`secrets/.*\.enc\.yaml$` → the key **ARN**, not the
alias — sops 3.11 rejects the alias as a creation rule). Re-encrypt / edit a
bundle with `sops secrets/<leaf>.enc.yaml`. To (re)build one from the SSM-stored
values:
```bash
export AWS_PROFILE=klanker-application AWS_REGION=us-east-1
ANTH=$(aws ssm get-parameter --name /km/secrets/anthropic-api-key --with-decryption --query Parameter.Value --output text)
OAI=$(aws ssm get-parameter --name /km/secrets/openai-api-key  --with-decryption --query Parameter.Value --output text)
printf 'ANTHROPIC_API_KEY: %s\nOPENAI_API_KEY: %s\n' "$ANTH" "$OAI" > secrets/qwen.enc.yaml
sops -e -i secrets/qwen.enc.yaml      # encrypt in place (matches the .sops.yaml rule)
```
(For Llama, prepend `HF_TOKEN: $(aws ssm get-parameter --name /km/secrets/hf-token …)`.)

## Prerequisites

1. **AWS G-instance quota.** New accounts default to **0** vCPU for "Running
   On-Demand G and VT instances" (`L-DB2E81BA`). g6e.12x needs 48, g6e.48x needs 192.
   The L4 fallback shapes draw on the **same** quota — g6.12x is also 48 vCPU — so
   falling back from g6e to g6 dodges L40S *capacity*, not a quota wall.
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
| `claude-anthropic` route 401/empty | `ANTHROPIC_API_KEY` not reaching Bifrost. Bifrost reads `/etc/km/bifrost-env`, which `base/gpu/serve`'s bring-up populates via `km-env exec --only ANTHROPIC_API_KEY,OPENAI_API_KEY` (Phase 133 — there is no `/etc/sandbox-secrets.env` any more). Check the key is in the bundle (`km-env list`), that `/etc/km/bifrost-env` actually has the line, that the file is wired into `bifrost.service`, and that Bifrost expands `${ANTHROPIC_API_KEY}` in its JSON config. If `km-env` itself failed, `/var/log/cloud-init-output.log` carries the error under a misleading `No init script found in S3 (skipped)`. |
| `claude-bedrock` / `gpt-oss-bedrock` 403 | Sandbox role lacks `bedrock:InvokeModel` for that model ID — add the Claude + `openai.gpt-oss-*` IDs to the role's Bedrock allowlist. |
| Slack 👀 but no reply | Inbound poller not emitted — needs `Spec.CLI != nil` (satisfied by `base/platform`); check `systemctl is-active km-slack-inbound-poller` + `/etc/km/notify.env`. |
| Remote create: "profile base/gpu/serve not found" | The create-handler has no `profiles/base/` fragments — the operator binary must flatten `extends` before upload (refresh `toolchain/km` via `km init`). |
| `vllm.service` fails at `ExecStartPre`, "weights are not staged in S3 yet" | The `gpu-qwen38-oblit-*` leaves read weights from S3 — run `scripts/stage-model-to-s3.sh` once per install. The unit retries every 30s, so it recovers on its own once the upload finishes; no restart needed. |
| Model serves fine but codex never calls tools | Wrong tool-call parser. Qwen3.5 emits XML (`<function=…>`), so it needs `--tool-call-parser qwen3_xml`; `hermes` parses nothing and fails silently. |
| vLLM argparse `IndexError` at boot after editing `VLLM_EXTRA` | A space crept into a JSON value. `$VLLM_EXTRA` is word-split unbraced, so `{"video": 0}` becomes two argv tokens — keep JSON space-free. |
| `vllm serve: error: argument --limit-mm-per-prompt: Value {video:0} cannot be converted` (exit 2/INVALIDARGUMENT) | Space-free is necessary but **not sufficient** — that same unbraced split also does shell-style quote removal, eating the JSON's own double quotes. Wrap each JSON value in **single quotes**: `'{"video":0}'` arrives intact, `{"video":0}` and `{\"video\":0}` both arrive as `{video:0}`. Not an `EnvironmentFile` problem — the value is correct in the environment; `ExecStart` expansion strips it. |
| `ValueError: max_num_seqs (256) exceeds available Mamba cache blocks (N)` | Hybrid-architecture concurrency ceiling on a small VRAM budget — set `--max-num-seqs` below N (128 on the single-L40S leaf). Lowering `--max-model-len` does **not** help; this is per-sequence state, not per-token KV. |
| `Bind for 127.0.0.1:8000 failed: port is already allocated`, then crash-loop | Pre-fix `vllm.service` had no `--name`/`ExecStop`, so `systemctl restart` killed the docker *client* while the container kept the port, and `Restart=on-failure` looped against its own orphan. Fixed in `base/gpu/serve`; on an older box recover with `docker rm -f $(docker ps -aq --filter ancestor=vllm/vllm-openai:latest)`. |
| Claude Code via Bifrost 400s `System message must be at the beginning.` | The model's shipped `chat_template.jinja` raises on a `system`-role entry that is not first in `messages`, which Claude Code produces in normal use. Bifrost is **not** at fault (it correctly merges the Anthropic `system` field, including multiple blocks). The FP8 leaf stages a patched copy that re-emits a stray system message as a user turn and points vLLM at it with `--chat-template`. |
| `error: externally-managed-environment` from `stage-model-to-s3.sh` | PEP 668 on Ubuntu 24.04 (the DLAMI). Fixed — the script now installs `huggingface_hub` into a venv. On an older copy, re-pull the script or pass `--break-system-packages`. |
| `stage-model-to-s3.sh` reports `Fetching 0 files` then exits 0 | huggingface_hub 1.x moved the CLI to Typer, where `--exclude` takes one value and trailing bare patterns are parsed as the positional `FILENAMES` argument — silently turning the command into "download exactly these files". Use one `--exclude` per pattern. |

See also: `docs/sandbox-secrets.md` (SOPS), `docs/codex-parity.md` (codex/Slack
agent switching), `docs/slack-notifications.md` (inbound poller), `klanker:vscode`
skill (Remote-SSH), and the design spec
`docs/superpowers/specs/2026-06-27-gpu-vllm-serving-profiles-design.md`.
