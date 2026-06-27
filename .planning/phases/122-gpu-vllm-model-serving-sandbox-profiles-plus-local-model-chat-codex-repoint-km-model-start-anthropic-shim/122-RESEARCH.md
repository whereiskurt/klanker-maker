# Phase 122: GPU vLLM Model-Serving Profiles + Local-Model Chat — Research

**Researched:** 2026-06-27
**Domain:** GPU EC2 (g6e L40S), vLLM serving, Codex CLI local-provider repoint, LiteLLM dual-gateway, km operator CLI extension
**Confidence:** HIGH for O1/O3/O6/O8 (live AWS + official docs); HIGH for O7 (critical discovery, verified); MEDIUM for O2 (VRAM math computed, flags from official vLLM docs); MEDIUM for O9 (LiteLLM Responses API confirmed per docs)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- **vLLM** serving stack (multi-GPU tensor-parallel, OpenAI-compatible API). NOT llama.cpp/SGLang/TGI.
- Base AMI: **AWS Deep Learning Base OSS Nvidia Driver GPU AMI (Ubuntu 24.04)**, raw AMI ID in `spec.runtime.ami`. Ubuntu → `base/os/debian` userdata path.
- vLLM as a **systemd unit** (`vllm.service`) wrapping `docker run … vllm/vllm-openai`, `EnvironmentFile=/etc/km/vllm.env`, `--served-model-name local`, bound `127.0.0.1:8000`.
- **7-leaf profile matrix**: gpu-qwen-12x, gpu-llama-12x, gpu-qwen-48x, gpu-llama-48x, gpu-glmair-12x, gpu-kimidev-12x, gpu-glm46-48x.
- Leaf extends: `[base/os/debian, base/network/safenetwork, base/userinit, base/platform, base/slack-persandbox, base/gpu/serve]`.
- `agent.default: codex` → Slack inbound + km shell + km agent run --codex hit the local model (box-global repoint).
- **claude stays cloud-pointed on-box** → `/claude` = cloud, default/`/codex` = local 70B, A/B in one channel.
- `synthesizeCodexConfig` (`pkg/compiler/agent_codex.go:67`) gains a `[model_providers.local]` emission gated on a new profile knob.
- **`km model start <sb> [--local-port 8000] [--anthropic]`** — mirrors vscode.go:196 / desktop.go:206 consumers of `runReconnectingPortForward`.
- On-box shim = LiteLLM `/v1/messages` (or claude-code-proxy), installed by base/gpu/serve, localhost-only. Port `:8001`.
- Only the 2 **Llama** leaves need `HF_TOKEN` — Qwen/GLM/Kimi are ungated.
- `teardownPolicy: stop`, `spot: false`, `ttl: 8h`, `idleTimeout: 1h`.
- `budget.compute.maxSpendUSD`: 12x leaves → $120; 48x leaves → $300.
- AWS quota CLEARED: G+VT = 768 vCPU.

### Claude's Discretion
- Exact YAML field names for the codex local-provider knob (O6) and shim packaging (O9).
- Wave decomposition boundaries, golden-test update mechanics, test layout.
- Per-model `--max-model-len` / `--gpu-memory-utilization` tuning (O2/O8).

### Deferred Ideas (OUT OF SCOPE)
- Goose as a first-class `agent_type` (Path 2).
- On-box claude default repointed at the local model (stays cloud for the A/B).
- Kimi K2 (~1T MoE, ~500GB @ 4-bit, needs gated P-family).
- Local-VS-Code-with-port-forward as a documented alternative.
- Multi-model hot-swap / model router on one box.
</user_constraints>

---

## Summary

Phase 122 delivers three interlocked deliverables: GPU vLLM serving profiles (7 leaves), Slack codex repoint (synthesizeCodexConfig change), and `km model start` (new operator command + on-box LiteLLM dual-gateway). The research resolves all nine open questions with actionable, citable answers.

The single highest-risk discovery is **O7**: Codex CLI dropped Chat Completions support in February 2026 and now requires every provider endpoint to speak the OpenAI Responses API (`wire_api = "responses"`). vLLM serves only `/v1/chat/completions` natively. A LiteLLM proxy layer is therefore **mandatory** between Codex and vLLM — it cannot be skipped. Fortunately, LiteLLM also serves `/v1/messages` (Anthropic-format), so a single on-box LiteLLM instance on `:8001` can bridge both Codex (Responses API) and Claude Code (Anthropic Messages API) to vLLM on `:8000`. This unifies what the design spec framed as two separate gateways into one.

**Primary recommendation:** Deploy one LiteLLM instance on `:8001` for both the Codex→vLLM Responses-API bridge and the Claude-Code→vLLM Anthropic-shim path. Codex points at `localhost:8001` (not `localhost:8000` directly). `km model start` forwards `:8001` by default and adds no `--anthropic` flag split; or split for clarity per the spec.

---

## O1 — DLAMI AMI ID + Ubuntu Userdata Path

### Current AMI (live AWS lookup, 2026-06-27)

| AMI ID | Name | Date |
|--------|------|------|
| `ami-0a9d213b92dabc044` | Deep Learning Base OSS Nvidia Driver GPU AMI (Ubuntu 24.04) 20260626 | 2026-06-26 |
| `ami-04c358d25696de0a0` | Deep Learning Base OSS Nvidia Driver GPU AMI (Ubuntu 24.04) 20260619 | 2026-06-19 |

**Use:** `ami-0a9d213b92dabc044` (latest as of research date).

**Re-resolution command** (run before km create when the profile has been sitting for weeks):
```bash
AWS_PROFILE=klanker-application aws ec2 describe-images \
  --owners amazon \
  --filters "Name=name,Values=Deep Learning Base OSS Nvidia Driver GPU AMI (Ubuntu 24.04)*" \
            "Name=state,Values=available" \
  --query "sort_by(Images, &CreationDate)[-1].ImageId" \
  --region us-east-1 --output text
```

**SSM public parameter:** `/aws/service/deeplearning/ami/` path returns empty — NOT available for DLAMI. Re-resolution must use `describe-images`.

### Raw AMI ID passthrough (confirmed in code)

`pkg/compiler/service_hcl.go:20-33` defines `rawAMIIDPattern = ^ami-[0-9a-f]{8,17}$`. When `spec.runtime.ami` matches this pattern, `isRawAMIID()` returns true → `AMISlug=""`, `AMIID=<raw>` — the Terraform module uses the ID directly, no `data.aws_ami` lookup. Passes through untouched. **Confirmed.**

### Ubuntu userdata path selection

The userdata template uses `KM_OS_ID="${ID:-amzn}"` (from `/etc/os-release`) to detect Ubuntu vs AL2023 at runtime — NOT from the AMI slug at compile time. The DLAMI is Ubuntu 24.04, so `KM_OS_ID=ubuntu`. The OS-aware userdata path (Phase 93.1) then:
- Runs `km_apt_https` (rewrites sources from http → https; the SG allows only 443 egress)
- Sets `ForceIPv4 "true"` for apt
- Installs AWS CLI via `python3` (no `unzip`; portable extractor)
- Uses `ssh.service` (not `sshd.service` which is AL2023)
- Stops `systemd-resolved` (frees `:53` for the eBPF resolver)
- Installs km enforcement sidecars (eBPF + proxy, tested on stock Ubuntu 24.04)

`base/os/debian` fragment sets `spec.runtime.ami: ubuntu-24.04` — but the GPU leaf OVERRIDES this with the raw `ami-0a9d213b92dabc044` ID. The `ID` field from `/etc/os-release` still reads `ubuntu` on the DLAMI, so the Ubuntu path is selected correctly.

### DLAMI block device mappings (live lookup)

```json
[
  {"Device": "/dev/sda1", "Size": 75},   // root volume
  {"Device": "/dev/sdb",  "Size": null}, // ephemeral (NVMe SSD on g6e)
  {"Device": "/dev/sdc",  "Size": null}  // ephemeral
]
```

The DLAMI uses `/dev/sda1` as root (not `/dev/nvme0n1`). The `additionalVolume` (300GB, mountpoint `/data`) will be attached at `/dev/sdf` (first available from km's `additionalVolumeDeviceCandidates`). No collision with `/dev/sdb`/`/dev/sdc` (ephemeral, not in the `sd[f-p]` range).

### DLAMI + km Ubuntu enforcement: friction assessment (R2)

The DLAMI pre-installs Docker + nvidia-container-toolkit. km's Ubuntu userdata path:
- Does NOT attempt to install Docker (it calls `km_pkg_install` only for km-specific tools).
- Stops `systemd-resolved` — this is DLAMI-safe (Docker daemon does NOT depend on systemd-resolved; Docker uses its own embedded DNS resolver by default).
- eBPF enforcement needs to stop systemd-resolved BEFORE the eBPF DNS resolver starts (the userdata already does this at section 4497).
- Known issue: `systemctl daemon-reload` during boot can cause containers to lose GPU access (NVIDIA container toolkit bug on systemd cgroup drivers). The `vllm.service` unit should be started AFTER cloud-init completes, not via `systemctl enable --now` inside `initCommandsAppend` unless cloud-init runs as the final boot step. Mitigation: use `ExecStartPre=/bin/sleep 5` in the unit, or use `after=cloud-final.service` in the unit's `[Unit]` section.
- **safenetwork is `*` egress** — HuggingFace weight pull (http-proxy MITM in-path) is permitted. The MITM may throttle large LFS pulls (40-140GB). Mitigation: pull weights on first boot with relaxed enforcement, persist on `/data`.

### Validation AMI schema note

`pkg/profile/validate.go:676` has a guard: `strings.HasPrefix(ami, "ubuntu-")` → OK; raw AMI ID → WARN (offline — cannot verify OS family). The GPU leaf will emit a validation warning: "desktop is enabled but spec.runtime.ami [raw-id] is a raw AMI ID — km cannot verify the OS family offline." This warning fires ONLY when `desktop.enabled: true`. Since GPU profiles do NOT enable the desktop, `validateDesktop` is a no-op. No issue.

**Confidence: HIGH** (live AWS, code confirmed)

---

## O2 — vLLM Quant + Serving Flags per Leaf

### VRAM budget math

| Instance | GPUs | VRAM each | Total VRAM |
|----------|------|-----------|-----------|
| g6e.12xlarge | 4× L40S | 48 GB | **192 GB** |
| g6e.48xlarge | 8× L40S | 48 GB | **384 GB** |

**Rule of thumb:** `weight_VRAM_GB ≈ params_B × dtype_bytes_per_param`. For inference: weight VRAM + KV cache VRAM + cuda graph overhead. L40S are PCIe (not NVLink) — tensor parallel works but is bandwidth-limited vs NVLink setups.

### Per-leaf vLLM flags (`VLLM_EXTRA` env var in `/etc/km/vllm.env`)

| Profile | Model | Quant | TP | Weight VRAM | KV headroom | Flags |
|---------|-------|-------|-----|-------------|-------------|-------|
| gpu-qwen-12x | Qwen/Qwen2.5-72B-Instruct-AWQ | AWQ 4-bit | 4 | ~38 GB | ~154 GB | `--quantization awq --max-model-len 32768 --gpu-memory-utilization 0.90` |
| gpu-llama-12x | meta-llama/Llama-3.3-70B-Instruct | FP8 | 4 | ~70 GB | ~122 GB | `--quantization fp8 --max-model-len 16384 --gpu-memory-utilization 0.90` |
| gpu-qwen-48x | Qwen/Qwen2.5-72B-Instruct | FP16 | 8 | ~144 GB | ~240 GB | `--max-model-len 65536 --gpu-memory-utilization 0.90` |
| gpu-llama-48x | meta-llama/Llama-3.3-70B-Instruct | FP16 | 8 | ~144 GB | ~240 GB | `--max-model-len 65536 --gpu-memory-utilization 0.90` |
| gpu-glmair-12x | cyankiwi/GLM-4.5-Air-AWQ-4bit | AWQ 4-bit | 4 | ~55 GB | ~137 GB | `--quantization awq --max-model-len 32768 --gpu-memory-utilization 0.90 --trust-remote-code --enable-auto-tool-choice --tool-call-parser glm45 --reasoning-parser glm45` |
| gpu-kimidev-12x | (see O8 — GPTQ 8-bit recommended) | GPTQ 8-bit | 4 | ~72 GB | ~120 GB | `--quantization gptq --max-model-len 16384 --gpu-memory-utilization 0.90` |
| gpu-glm46-48x | QuantTrio/GLM-4.6-AWQ | AWQ 4-bit | 8 | ~178 GB | ~206 GB | `--quantization awq --max-model-len 65536 --gpu-memory-utilization 0.90 --trust-remote-code --enable-auto-tool-choice --tool-call-parser glm45 --reasoning-parser glm45` |

**All leaves add:** `--served-model-name local` (so a single Continue config works everywhere).

**Docker run template (base/gpu/serve vllm.service):**
```bash
docker run --rm --gpus all --ipc=host \
  -p 127.0.0.1:8000:8000 \
  -v /data/hf:/root/.cache/huggingface \
  --env-file /etc/km/vllm.env \
  vllm/vllm-openai:latest \
  --model ${VLLM_MODEL} \
  --tensor-parallel-size ${VLLM_TP} \
  --served-model-name local \
  ${VLLM_EXTRA}
```

`--ipc=host` is required — vLLM uses shared memory between GPU processes; omitting it causes CUDA errors under multi-GPU tensor-parallel load.

**Confidence: MEDIUM** (VRAM math confirmed, flags from official vLLM docs and Qwen docs; exact `--max-model-len` requires live tuning to avoid OOM on first bring-up)

---

## O3 — HF_TOKEN Injection

### Mechanism (Phase 89 SOPS)

Only the 2 Llama leaves need `HF_TOKEN`. The canonical path from `docs/sandbox-secrets.md`:

1. Create plaintext YAML bundle (never commit):
   ```bash
   cat > /tmp/llama-hf.yaml <<EOF
   HF_TOKEN: hf_xxxxxxxxxxxxxxxxxxxxxxxxxxxxx
   EOF
   ```
2. Encrypt with SOPS + shared KMS key:
   ```bash
   sops --encrypt \
        --kms 'arn:aws:kms:us-east-1:052251888500:alias/km-sandbox-secrets' \
        --input-type yaml --output-type yaml \
        /tmp/llama-hf.yaml > secrets/llama-hf.enc.yaml
   rm /tmp/llama-hf.yaml
   ```
3. Reference from the Llama leaf profiles:
   ```yaml
   spec:
     secrets:
       sopsFile: ./secrets/llama-hf.enc.yaml
     iam:
       allowedSecretPaths:
         - /sandbox/*/secrets     # auto-allowed by the SOPS mechanism
   ```

**Where does HF_TOKEN land?** SOPS decrypts to `/etc/sandbox-secrets.env` (root:sandbox, 0440) and sources it via `/etc/profile.d/zz-sandbox-secrets.sh` in login shells. The `vllm.service` unit uses `EnvironmentFile=/etc/km/vllm.env` — this is a **systemd env file** (no `export` prefix, `KEY=VALUE` format). The SOPS env lands in login-shell scope, NOT in systemd unit scope.

**Fix:** The `initCommandsAppend` in the Llama leaves must explicitly copy `HF_TOKEN` into `/etc/km/vllm.env` after the SOPS decrypt runs. The SOPS decrypt runs at section 5.5 of userdata (before `initCommands`). `initCommandsAppend` runs as part of `initCommands` after base merge. So the pattern is:

```bash
# In Llama leaf's initCommandsAppend:
grep '^HF_TOKEN=' /etc/sandbox-secrets.env >> /etc/km/vllm.env 2>/dev/null || true
```

This is safe: `/etc/km/vllm.env` is created by the `vllm.service`'s `configFiles` entry from the leaf before `initCommandsAppend` runs (configFiles section 7.6 runs after initCommands in the userdata, so the order is: initCommands → configFiles → ... Actually check the ordering below).

**Userdata ordering:** Section 7.6 (configFiles) runs AFTER `initCommands` (section 2.5 area). The `HF_TOKEN` copy must happen AFTER `/etc/km/vllm.env` is written. But configFiles runs after initCommands, so `initCommandsAppend` cannot append to a file that doesn't exist yet.

**Correct approach:** Write `/etc/km/vllm.env` in the leaf's `initCommandsAppend` itself (not via configFiles) for the Llama leaves:

```yaml
execution:
  initCommandsAppend:
    - "mkdir -p /etc/km && printf 'VLLM_MODEL=meta-llama/Llama-3.3-70B-Instruct\nVLLM_TP=4\nVLLM_EXTRA=--quantization fp8 --max-model-len 16384 --gpu-memory-utilization 0.90\n' > /etc/km/vllm.env"
    - "grep '^HF_TOKEN=' /etc/sandbox-secrets.env >> /etc/km/vllm.env 2>/dev/null || true"
```

For non-Llama leaves, the base `gpu/serve` fragment writes `/etc/km/vllm.env` via `configFiles`. The leaf-specific `VLLM_MODEL`/`VLLM_TP`/`VLLM_EXTRA` content is written by the leaf (configFiles key merges cleanly alongside the base's unit-file key, since configFiles is a map).

**Alternative (simpler):** Use `initCommandsAppend` for ALL leaves to write `/etc/km/vllm.env`. Reserve configFiles for `/etc/systemd/system/vllm.service` (the unit file) only. This avoids the ordering concern entirely.

**Confidence: HIGH** (code-confirmed secret injection mechanism)

---

## O6 — Codex Local-Provider Knob Shape

### New fields in AgentCodexSpec (pkg/profile/types.go)

Add two optional string fields to `AgentCodexSpec`:

```go
type AgentCodexSpec struct {
    Tools    AgentToolsSpec `json:"tools,omitempty" yaml:"tools,omitempty"`
    Args     []string       `json:"args,omitempty" yaml:"args,omitempty"`
    // Phase 122: local model provider endpoint.
    // When non-empty, synthesizeCodexConfig emits a [model_providers.local] block
    // pointing codex at the given base URL (must be an OpenAI Responses-compatible
    // endpoint — see O7: LiteLLM wraps vLLM's chat-completions into Responses API).
    // Empty = no provider block emitted (inert hook block only, as before).
    LocalBaseURL string `json:"localBaseURL,omitempty" yaml:"localBaseURL,omitempty"`
    // LocalModel is the model name to pass to the provider. Default: "local".
    LocalModel   string `json:"localModel,omitempty" yaml:"localModel,omitempty"`
}
```

**Profile YAML knob (in base/gpu/serve):**
```yaml
spec:
  agent:
    default: codex
    codex:
      localBaseURL: "http://localhost:8001/v1"
      localModel: "local"
```

Note the base URL is `:8001` (LiteLLM), not `:8000` (vLLM), because Codex requires the Responses API (see O7).

### synthesizeCodexConfig extension

```go
// After the base hook block, when c.LocalBaseURL is set:
if c.LocalBaseURL != "" {
    model := c.LocalModel
    if model == "" {
        model = "local"
    }
    fmt.Fprintf(&b, "\nmodel_provider = %q\nmodel = %q\n", "local", model)
    fmt.Fprintf(&b, "\n[model_providers.local]\n")
    fmt.Fprintf(&b, "name = \"Local vLLM (via LiteLLM)\"\n")
    fmt.Fprintf(&b, "base_url = %q\n", c.LocalBaseURL)
    fmt.Fprintf(&b, "wire_api = \"responses\"\n")
    fmt.Fprintf(&b, "env_key = \"OPENAI_API_KEY\"\n")
}
```

**Resulting config.toml** (emitted to `~/.codex/config.toml`):
```toml
[features]
hooks = true

[[hooks.PermissionRequest]]
... (existing base block) ...

model_provider = "local"
model = "local"

[model_providers.local]
name = "Local vLLM (via LiteLLM)"
base_url = "http://localhost:8001/v1"
wire_api = "responses"
env_key = "OPENAI_API_KEY"
```

The `OPENAI_API_KEY` env var is set to `""` in base/userinit.yaml already — this is the dummy key. Codex reads it via `env_key` → finds an empty string → still authenticates (LiteLLM's `disable_key_check: true` accepts any key).

**JSON schema update:** Add `localBaseURL` and `localModel` string properties to the `agent.codex` schema block (same pattern as `args`).

**Golden test update:** `synthesizeCodexConfig` gains a new table test case: `AgentSpec{Codex: &AgentCodexSpec{LocalBaseURL: "http://localhost:8001/v1", LocalModel: "local"}}` → expected TOML string. Frozen byte-identity goldens are NOT affected (they test the full userdata pipeline; the codex config is a string embedded in the configFiles block — the change adds new content, not a byte-identical mutation of existing content). Update full-output goldens via `CAPTURE_*` flags.

**Confidence: HIGH** (types.go structure confirmed, synthesizeCodexConfig logic confirmed, Codex config.toml schema confirmed via official docs)

---

## O7 — Codex ↔ vLLM API Surface (HIGHEST-RISK — RESOLVED)

### Critical finding: Codex requires OpenAI Responses API only

**Since February 2026, Codex CLI (0.133+) REMOVED support for Chat Completions.** All providers, including third-party and local endpoints, must speak the **OpenAI Responses API** (`/v1/responses`). The `wire_api` key in `config.toml` only accepts `"responses"` (the default).

Source: [Codex config reference](https://developers.openai.com/codex/config-reference) and [morphllm guide](https://www.morphllm.com/codex-provider-configuration): "In February 2026, Codex removed Chat Completions support and now requires all providers to use the Responses API."

### vLLM does NOT natively serve `/v1/responses`

vLLM serves `/v1/chat/completions` (and related endpoints) natively. The `/v1/responses` endpoint was a feature request (issue #14721) opened March 2025 and is NOT in the current vLLM release (as of research date).

**Implication: A gateway layer between Codex and vLLM is MANDATORY.**

### LiteLLM resolves both gateways in one instance

LiteLLM proxy **does** expose `/v1/responses` (Responses API) and bridges it to vLLM's `/v1/chat/completions` internally. LiteLLM also exposes `/v1/messages` (Anthropic Messages API) and bridges it to vLLM. A **single LiteLLM instance on `:8001`** serves:
- Codex: `config.toml base_url = "http://localhost:8001/v1"` → `wire_api = "responses"` → LiteLLM `/v1/responses` → vLLM `/v1/chat/completions`
- Claude Code (laptop, via `km model start`): `ANTHROPIC_BASE_URL=http://localhost:8001` → LiteLLM `/v1/messages` → vLLM `/v1/chat/completions`

This unifies the design spec's "two separate gateways" into one LiteLLM instance.

### Tool-call flags per model family (vLLM serve → Docker run)

vLLM handles tool calling at the serving layer via `--enable-auto-tool-choice` + `--tool-call-parser`:

| Model Family | Parser | Notes |
|---|---|---|
| Qwen2.5-72B | `qwen25` or `hermes` | Tool chat templates in tokenizer_config.json |
| Llama-3.3-70B | `llama3_json` | Standard Llama 3 JSON tool call format |
| GLM-4.5-Air, GLM-4.6 | `glm45` | Confirmed in vLLM GLM recipes |
| Kimi-Dev-72B (Qwen2 arch) | `hermes` | Qwen2 arch inherits Qwen parser |

**Flags to add to all leaves' VLLM_EXTRA:**
```
--enable-auto-tool-choice --tool-call-parser <family>
```

Qwen and Llama leaves need this for coding agent workflows. GLM leaves require it AND `--trust-remote-code`.

**Confidence: HIGH** (official Codex docs, LiteLLM docs, vLLM tool-calling docs — all cross-verified)

---

## O8 — GLM/Kimi Quant Repos + vLLM Arch Support

### GLM-4.5 / GLM-4.6 / GLM-4.7 architecture

Architecture class: `Glm4MoeForCausalLM` (HuggingFace transformers `glm4_moe` module). The vLLM implementation is at `vllm/model_executor/models/glm4_moe_mtp.py` — confirmed in the official HuggingFace GLM-4.x docs and transformers documentation (v5.12.0).

**vLLM support:** Native (no `--trust-remote-code` needed for GLM-4.5/4.6 with an up-to-date vLLM). The GLM recipes docs show `--trust-remote-code` is used in their examples, which is a safe default for GLM models pending upstream registration.

### Concrete 4-bit community quant repos

| Model | Recommended repo | Format | Notes |
|-------|-----------------|--------|-------|
| GLM-4.5-Air (106B MoE) | `cyankiwi/GLM-4.5-Air-AWQ-4bit` | AWQ | Calibrated with nvidia/Llama-Nemotron-Post-Training-Dataset; vLLM-compatible |
| GLM-4.5-Air (alt) | `cpatonn/GLM-4.5-Air-AWQ-4bit` | AWQ | Quantized via vllm-project/llm-compressor; note: `tensor_parallel_size <= 2` caveat (need to test at tp=4) |
| GLM-4.6 (355B MoE) | `QuantTrio/GLM-4.6-AWQ` | AWQ | Described as "professionally quantized 4-bit AWQ version optimized for production vLLM" |
| GLM-4.6 (alt) | `bullpoint/GLM-4.6-AWQ` | AWQ | Community-maintained |
| Kimi-Dev-72B | No AWQ available — use `btbtyler09/Kimi-Dev-72B-GPTQ-8bit` | GPTQ 8-bit | AWQ community request exists but no published AWQ repo found; GPTQ 8-bit (~72GB) fits 4xL40S at 192GB |
| Kimi-Dev-72B (alt) | `moonshotai/Kimi-Dev-72B` | FP16 | 144GB — fits 4xL40S at 192GB with reduced KV cache; use `--gpu-memory-utilization 0.75 --max-model-len 8192` |
| Qwen2.5-72B-AWQ | `Qwen/Qwen2.5-72B-Instruct-AWQ` | AWQ | Official Qwen repo — public, 21 files, confirmed ungated |
| Llama-3.3-70B | `meta-llama/Llama-3.3-70B-Instruct` | FP16 / FP8 | **Gated** — requires HF_TOKEN + Meta license acceptance |

**Kimi-Dev-72B architecture:** Qwen2 (base model is Qwen/Qwen2.5-72B). Confirmed at HuggingFace model card: "base model is Qwen/Qwen2.5-72B with 73B parameters stored in BF16 format." vLLM handles Qwen2 natively with no `--trust-remote-code`.

**GLM VRAM math at 4-bit:**
- GLM-4.5-Air (106B) at AWQ 4-bit: ~106 × 0.5 bytes/param = ~53 GB → fits 4× L40S (192GB) comfortably
- GLM-4.6 (355B) at AWQ 4-bit: ~355 × 0.5 bytes/param = ~178 GB → fits 8× L40S (384GB) with ~206 GB for KV cache

**`--trust-remote-code` needed:** YES for all GLM leaves. NO for Qwen, Llama, Kimi-Dev (Qwen2 arch).

**Confidence: MEDIUM-HIGH** (HF model IDs confirmed live, VRAM math computed, quant repos found via search — need live validation that `cpatonn/GLM-4.5-Air-AWQ-4bit` works at tp=4)

---

## O9 — Anthropic Shim (LiteLLM, unified with O7)

### Decision: LiteLLM proxy (one instance, dual-gateway)

LiteLLM is selected over `claude-code-proxy` because it:
1. Serves **both** `/v1/responses` (Codex path, O7) AND `/v1/messages` (Claude Code path) in one binary.
2. Is actively maintained with vLLM as an explicit provider (`hosted_vllm/` prefix).
3. Has documented "use_chat_completions_api: true" bridge for OpenAI-compatible backends.
4. Is pip-installable (no container overhead inside the GPU sandbox).

### Install and config

**Install in base/gpu/serve `initCommandsAppend`:**
```bash
pip3 install 'litellm[proxy]' --quiet
```

**LiteLLM config file** (written via `configFiles["/etc/km/litellm-config.yaml"]` in base/gpu/serve):
```yaml
model_list:
  - model_name: claude-*
    litellm_params:
      model: openai/local
      api_base: http://localhost:8000/v1
      api_key: "dummy"
      use_chat_completions_api: true
  - model_name: local
    litellm_params:
      model: openai/local
      api_base: http://localhost:8000/v1
      api_key: "dummy"
      use_chat_completions_api: true

litellm_settings:
  drop_params: true
  request_timeout: 600
  modify_params: true

general_settings:
  disable_key_check: true
```

The `claude-*` model pattern intercepts Claude Code requests (Claude Code sends model names like `claude-sonnet-4-5`). The `local` pattern intercepts Codex requests.

**Systemd unit** (written via `configFiles["/etc/systemd/system/litellm.service"]` in base/gpu/serve):
```ini
[Unit]
Description=LiteLLM dual-gateway (Codex Responses API + Claude Code Anthropic shim)
After=network.target vllm.service
Wants=vllm.service

[Service]
Type=simple
User=root
ExecStartPre=/bin/sleep 10
ExecStart=/usr/local/bin/litellm --config /etc/km/litellm-config.yaml --port 8001 --host 127.0.0.1
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
```

**Enable in initCommandsAppend:**
```bash
systemctl daemon-reload && systemctl enable --now litellm.service
```

**Port:** `:8001` bound to `127.0.0.1` only. Reachable only via SSM port-forward.

### Claude Code honors ANTHROPIC_BASE_URL (confirmed)

Claude Code reads `ANTHROPIC_BASE_URL` and `ANTHROPIC_AUTH_TOKEN` (or `ANTHROPIC_API_KEY`). Set on the operator's laptop:
```bash
export ANTHROPIC_BASE_URL="http://localhost:8001"
export ANTHROPIC_AUTH_TOKEN="sk-dummy"
```
Claude Code sends `POST /v1/messages` → LiteLLM translates → vLLM `POST /v1/chat/completions`.

### `km model start` command

`km model start <sandbox> [--local-port 8001] [--codex]` — default forwards `:8001` (LiteLLM, works for both Claude Code and Codex from the laptop). No `--anthropic` split needed since both go through `:8001`.

**Alternative per spec:** keep the `--anthropic` flag as an alias for semantic clarity (forwards `:8001` in both cases). Or simplify: default forward is always `:8001`; a future `--direct` could bypass LiteLLM to `:8000` for raw OpenAI SDK clients.

**HTTP probe for `km model status`:** `GET http://localhost:8001/health` (LiteLLM health endpoint) — more reliable than probing `:8000` directly.

**Confidence: HIGH** (LiteLLM docs confirmed, ANTHROPIC_BASE_URL confirmed, dev.to article shows full working stack)

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| vllm/vllm-openai | latest Docker image | OpenAI-compatible LLM serving | Multi-GPU TP, broadest model support |
| LiteLLM proxy | latest (`pip install 'litellm[proxy]'`) | Dual gateway: Responses API + Anthropic Messages → vLLM chat-completions | Only open tool that bridges both Codex + Claude Code to vLLM |
| DLAMI Ubuntu 24.04 | ami-0a9d213b92dabc044 (2026-06-26) | Pre-baked NVIDIA drivers + CUDA + Docker + nvidia-container-toolkit | Zero driver install friction |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| Qwen/Qwen2.5-72B-Instruct-AWQ | Official HF repo | AWQ 4-bit Qwen baseline | Default ungated model |
| QuantTrio/GLM-4.6-AWQ | Community HF repo | AWQ 4-bit GLM-4.6 355B MoE | 48x flagship MoE leaf |
| cyankiwi/GLM-4.5-Air-AWQ-4bit | Community HF repo | AWQ 4-bit GLM-4.5-Air 106B MoE | 12x MoE leaf |
| btbtyler09/Kimi-Dev-72B-GPTQ-8bit | Community HF repo | GPTQ 8-bit Kimi-Dev | No AWQ available; GPTQ fits 4xL40S |
| meta-llama/Llama-3.3-70B-Instruct | Official HF repo (gated) | FP8/FP16 Llama baseline | Gated — needs HF_TOKEN |

---

## Architecture Patterns

### New fragment structure

```
profiles/
├── base/
│   └── gpu/
│       └── serve.yaml         # abstract fragment (~90% shared)
├── gpu-qwen-12x.yaml           # ~15 lines leaf
├── gpu-llama-12x.yaml
├── gpu-qwen-48x.yaml
├── gpu-llama-48x.yaml
├── gpu-glmair-12x.yaml
├── gpu-kimidev-12x.yaml
└── gpu-glm46-48x.yaml
```

### base/gpu/serve.yaml pattern

```yaml
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: base-gpu-serve
  abstract: true

spec:
  runtime:
    ami: ami-0a9d213b92dabc044    # DLAMI Ubuntu 24.04 (2026-06-26); re-resolve monthly
    substrate: ec2
    spot: false                   # GPU spot capacity unreliable
    additionalVolume:
      size: 300
      mountPoint: /data

  lifecycle:
    ttl: 8h
    idleTimeout: 1h
    teardownPolicy: stop

  budget:
    compute:
      maxSpendUSD: 120            # 12x floor; 48x leaves override to 300
    ai:
      maxSpendUSD: 2.00

  execution:
    env:
      HF_HOME: /data/hf
      OPENAI_API_KEY: ""          # dummy key; LiteLLM uses disable_key_check

    initCommandsAppend:
      - "pip3 install 'litellm[proxy]' --quiet"
      - "systemctl daemon-reload"
      - "systemctl enable --now vllm.service"
      - "systemctl enable --now litellm.service"

    configFiles:
      "/etc/systemd/system/vllm.service": |
        [Unit]
        Description=vLLM serving (GPU sandbox)
        After=docker.service
        [Service]
        Type=simple
        EnvironmentFile=-/etc/km/vllm.env
        ExecStartPre=/bin/sleep 5
        ExecStart=docker run --rm --gpus all --ipc=host \
          -p 127.0.0.1:8000:8000 \
          -v /data/hf:/root/.cache/huggingface \
          --env-file /etc/km/vllm.env \
          vllm/vllm-openai:latest \
          --model ${VLLM_MODEL} \
          --tensor-parallel-size ${VLLM_TP} \
          --served-model-name local \
          ${VLLM_EXTRA}
        Restart=on-failure
        [Install]
        WantedBy=multi-user.target
      "/etc/systemd/system/litellm.service": |
        [Unit]
        Description=LiteLLM (Responses API + Anthropic shim → vLLM)
        After=vllm.service
        [Service]
        ExecStartPre=/bin/sleep 10
        ExecStart=/usr/local/bin/litellm --config /etc/km/litellm-config.yaml --port 8001 --host 127.0.0.1
        Restart=on-failure
        RestartSec=10
        [Install]
        WantedBy=multi-user.target
      "/etc/km/litellm-config.yaml": |
        model_list:
          - model_name: claude-*
            litellm_params:
              model: openai/local
              api_base: http://localhost:8000/v1
              api_key: "dummy"
              use_chat_completions_api: true
          - model_name: local
            litellm_params:
              model: openai/local
              api_base: http://localhost:8000/v1
              api_key: "dummy"
              use_chat_completions_api: true
        litellm_settings:
          drop_params: true
          request_timeout: 600
          modify_params: true
        general_settings:
          disable_key_check: true
      "/home/sandbox/.continue/config.yaml": |
        models:
          - title: Local vLLM (local)
            provider: openai
            model: local
            apiBase: http://localhost:8000/v1
            apiKey: dummy

  agent:
    default: codex
    codex:
      localBaseURL: "http://localhost:8001/v1"
      localModel: "local"
```

### Leaf pattern (non-Llama, e.g. gpu-qwen-12x.yaml)

```yaml
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: gpu-qwen-12x
extends:
  - base/os/debian
  - base/network/safenetwork
  - base/userinit
  - base/platform
  - base/slack-persandbox
  - base/gpu/serve

spec:
  runtime:
    instanceType: g6e.12xlarge
    region: us-east-1

  execution:
    initCommandsAppend:
      - "mkdir -p /etc/km && printf 'VLLM_MODEL=Qwen/Qwen2.5-72B-Instruct-AWQ\nVLLM_TP=4\nVLLM_EXTRA=--quantization awq --max-model-len 32768 --gpu-memory-utilization 0.90 --enable-auto-tool-choice --tool-call-parser hermes\n' > /etc/km/vllm.env"
```

### Leaf pattern (Llama, with HF_TOKEN)

```yaml
extends:
  - base/os/debian
  - base/network/safenetwork
  - base/userinit
  - base/platform
  - base/slack-persandbox
  - base/gpu/serve

spec:
  runtime:
    instanceType: g6e.12xlarge
    region: us-east-1

  secrets:
    sopsFile: ./secrets/llama-hf.enc.yaml

  iam:
    allowedSecretPaths:
      - /sandbox/*/secrets

  execution:
    initCommandsAppend:
      - "mkdir -p /etc/km && printf 'VLLM_MODEL=meta-llama/Llama-3.3-70B-Instruct\nVLLM_TP=4\nVLLM_EXTRA=--quantization fp8 --max-model-len 16384 --gpu-memory-utilization 0.90 --enable-auto-tool-choice --tool-call-parser llama3_json\n' > /etc/km/vllm.env"
      - "grep '^HF_TOKEN=' /etc/sandbox-secrets.env >> /etc/km/vllm.env 2>/dev/null || true"
```

### km model start (new file: internal/app/cmd/model.go)

**Pattern:** mirrors `vscode.go` exactly:
1. Fetch sandbox record + extract EC2 instance ID
2. `buildPortForwardCmd(ctx, instanceID, region, strconv.Itoa(localPort), "8001")` (target the LiteLLM port)
3. HTTP probe: `GET http://127.0.0.1:{localPort}/health` (LiteLLM health; use `httpTunnelProbe` variant — plain HTTP, not HTTPS)
4. `runReconnectingPortForward(ctx, execFn, buildPF, httpTunnelProbe(localPort), true, os.Stdout)`

**New tunnel probe needed:** `httpTunnelProbe` (plain HTTP, not `httpsTunnelProbe` which uses HTTPS + insecure). LiteLLM on `:8001` is plain HTTP (not TLS).

```go
// httpTunnelProbe probes a forwarded plain-HTTP service (e.g. LiteLLM on :8001).
func httpTunnelProbe(localPort int) tunnelProbe {
    client := &http.Client{Timeout: 6 * time.Second}
    url := fmt.Sprintf("http://127.0.0.1:%d/health", localPort)
    return func() bool {
        resp, err := client.Get(url)
        if err != nil { return false }
        _ = resp.Body.Close()
        return true
    }
}
```

### Anti-Patterns to Avoid

- **Writing `HF_TOKEN` to configFiles:** configFiles section (7.6) runs AFTER `initCommands`, but SOPS decryption (5.5) also runs before `initCommands`. Use `initCommandsAppend` to copy `HF_TOKEN` from `/etc/sandbox-secrets.env` → `/etc/km/vllm.env`.
- **Pointing Codex directly at vLLM (:8000):** vLLM serves Chat Completions; Codex requires Responses API since Feb 2026. Always route through LiteLLM (:8001).
- **Bool zero-value trap in base/gpu/serve:** Do NOT set `spot: false` as a bool in the abstract fragment. Keep `spot`, `hibernation`, `mountEFS` in leaves (pointer-bool pattern). The `RuntimeSpec` uses plain bools which cannot distinguish unset from false in fragments.
- **Re-capturing frozen byte-identity golden:** `userdata_learn_v2_pre92_baseline.golden.sh` is FROZEN; hand-patch only. Full-output goldens regen via `CAPTURE_*` flags.
- **Enabling the vllm.service before the weights volume is formatted:** `/data` needs to be formatted and mounted BEFORE the service starts. The `additionalVolume` mount happens in the userdata at section 2.5 (before `initCommandsAppend`). Safe.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Responses API ↔ Chat Completions translation | Custom reverse-proxy Go code | LiteLLM proxy | Stateful conversation, tool-call parsing, model routing |
| Anthropic Messages API ↔ OpenAI bridge | Custom shim | LiteLLM proxy (same instance) | Unified config; Claude Code tool-call format is complex |
| vLLM per-model quantization math | Guess | Official per-model HF repos (AWQ/GPTQ/FP8) | Community-calibrated quants have better accuracy than naive DIY |
| GPU tunnel probe | New TCP probe | `httpTunnelProbe()` (plain HTTP variant of existing `httpsTunnelProbe`) | Reuse existing pattern with minimal diff |

---

## Common Pitfalls

### Pitfall 1: Codex Chat Completions assumption (O7 — HIGHEST RISK)
**What goes wrong:** Codex 0.133+ requires Responses API. Pointing `localBaseURL` directly at vLLM `:8000` gives "wire_api unsupported" or silent failures.
**Why it happens:** vLLM's OpenAI compatibility is Chat Completions only.
**How to avoid:** Route Codex through LiteLLM `:8001`. Always test `codex exec` round-trip in live UAT (gate 5).
**Warning signs:** Codex hangs or errors on first turn against local provider.

### Pitfall 2: Bool zero-value trap in fragments (Phase 117)
**What goes wrong:** `spec.runtime` fragment with `spot: false` pushes `false` onto all children, overriding a leaf that sets `spot: true`.
**Why it happens:** Go's plain `bool` zero value is indistinguishable from "omitted" in deepMerge.
**How to avoid:** Keep `spot`, `hibernation`, `mountEFS` in leaves. The base/gpu/serve fragment should NOT set these fields.
**Warning signs:** `km validate` on merged profile shows unexpected bool values.

### Pitfall 3: vllm.service timing vs cloud-init
**What goes wrong:** `systemctl enable --now vllm.service` in `initCommandsAppend` fires while cloud-init is still running; a mid-boot `systemctl daemon-reload` can cause the vLLM container to lose GPU access (NVIDIA container toolkit + systemd cgroup driver bug).
**Why it happens:** DLAMI + systemd cgroup driver interaction.
**How to avoid:** Add `ExecStartPre=/bin/sleep 5` in the unit. The poller-enable step in `initCommandsAppend` is fine (runs at end of cloud-init, not mid-boot).
**Warning signs:** `nvidia-smi` works but vLLM container exits immediately with GPU error.

### Pitfall 4: DLAMI block device mapping collision
**What goes wrong:** The DLAMI has `/dev/sdb` and `/dev/sdc` listed in BDMs (ephemeral NVMe). km's `additionalVolume` auto-picks from `/dev/sd[f-p]` — no collision. But if the DLAMI rotated and gained `/dev/sdf`, the additionalVolume would collide.
**Why it happens:** `pickAdditionalVolumeDevice` reads `amiBDMDeviceNames` (from the BDM at create time).
**How to avoid:** `km create` resolves the BDM at runtime and passes `amiBDMDeviceNames` to the compiler. The live BDM lookup (Phase 56.1) handles this automatically.

### Pitfall 5: /etc/km/vllm.env written by configFiles runs AFTER initCommands
**What goes wrong:** If the vllm.env file is written via `configFiles` (section 7.6 = after initCommands), any `initCommandsAppend` that appends `HF_TOKEN` to it runs BEFORE the file exists.
**Why it happens:** Userdata section ordering: initCommands → configFiles.
**How to avoid:** Write `/etc/km/vllm.env` in `initCommandsAppend` (not configFiles) for all leaves. Use configFiles only for the systemd unit files and LiteLLM config.

### Pitfall 6: Remote create must flatten extends
**What goes wrong:** `km create --remote` with extends-based GPU leaves uploads the raw child YAML; the create-handler Lambda has no `profiles/base/` fragments → "profile base/gpu/serve not found".
**Why it happens:** The Lambda's km binary runs in an environment without the local profiles/ directory.
**How to avoid:** The Phase 120 fix in `selectRemoteProfileYAML` (`yaml.Marshal(resolvedProfile)`) already flattens extends. Verify the GPU leaves create cleanly via `km create` remote path in UAT.

### Pitfall 7: Schema field needs km init --lambdas to refresh create-handler
**What goes wrong:** The new `localBaseURL`/`localModel` fields in `AgentCodexSpec` are unknown to the Lambda's km binary until refreshed.
**Why it happens:** The create-handler Lambda validates profiles using the embedded km binary (toolchain/km).
**How to avoid:** After merging: `GOOS=linux GOARCH=arm64 go build …` → `make build-lambdas` → `km init --dry-run=false`. The `toolchain/km` must be linux/arm64 (macOS binary = "exec format error" on cold-create).

---

## Code Examples

### Codex config.toml local-provider TOML output
```toml
# Source: synthesizeCodexConfig extension (pkg/compiler/agent_codex.go)
[features]
hooks = true

[[hooks.PermissionRequest]]
matcher = ".*"
[[hooks.PermissionRequest.hooks]]
type = "command"
command = "/opt/km/bin/km-notify-hook PermissionRequest"
timeout = 30
statusMessage = "km: notifying operator"

[[hooks.Stop]]
[[hooks.Stop.hooks]]
type = "command"
command = "/opt/km/bin/km-notify-hook Stop"
timeout = 30

model_provider = "local"
model = "local"

[model_providers.local]
name = "Local vLLM (via LiteLLM)"
base_url = "http://localhost:8001/v1"
wire_api = "responses"
env_key = "OPENAI_API_KEY"
```

### vLLM docker run (Qwen AWQ, 4x L40S)
```bash
# Source: vLLM docs + Qwen docs (qwen.readthedocs.io)
docker run --rm --gpus all --ipc=host \
  -p 127.0.0.1:8000:8000 \
  -v /data/hf:/root/.cache/huggingface \
  --env-file /etc/km/vllm.env \
  vllm/vllm-openai:latest \
  --model Qwen/Qwen2.5-72B-Instruct-AWQ \
  --quantization awq \
  --tensor-parallel-size 4 \
  --served-model-name local \
  --max-model-len 32768 \
  --gpu-memory-utilization 0.90 \
  --enable-auto-tool-choice \
  --tool-call-parser hermes
```

### GLM vLLM serve (GLM-4.6, 8x L40S)
```bash
# Source: docs.vllm.ai/projects/recipes/en/latest/GLM/GLM.html
vllm serve QuantTrio/GLM-4.6-AWQ \
  --tensor-parallel-size 8 \
  --quantization awq \
  --tool-call-parser glm45 \
  --reasoning-parser glm45 \
  --enable-auto-tool-choice \
  --max-model-len 65536 \
  --gpu-memory-utilization 0.90 \
  --served-model-name local \
  --trust-remote-code
```

### LiteLLM stack test (curl)
```bash
# Test Responses API path (Codex path):
curl http://localhost:8001/v1/responses \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer dummy" \
  -d '{"model":"local","input":"ping","stream":false}'

# Test Anthropic path (Claude Code path):
curl http://localhost:8001/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: sk-dummy" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-sonnet-4-5","max_tokens":16,"messages":[{"role":"user","content":"ping"}]}'
```

### km model start (new Cobra command sketch)
```go
// Source: mirrors vscode.go:runVSCodeStart pattern
// File: internal/app/cmd/model.go
func runModelStart(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, sandboxID string, localPort int) error {
    rec, err := fetcher.FetchSandbox(ctx, sandboxID)
    ...
    instanceID, err := extractResourceID(rec.Resources, ":instance/")
    ...
    fmt.Printf("✓ Forwarding localhost:%d → sandbox:8001 (LiteLLM gateway)\n\n", localPort)
    fmt.Printf("Claude Code: export ANTHROPIC_BASE_URL=http://localhost:%d\n", localPort)
    fmt.Printf("Codex: set base_url=\"http://localhost:%d/v1\" in ~/.codex/config.toml\n", localPort)
    region := rec.Region
    buildPF := func(c context.Context) *exec.Cmd {
        return buildPortForwardCmd(c, instanceID, region, strconv.Itoa(localPort), "8001")
    }
    return runReconnectingPortForward(ctx, execFn, buildPF, httpTunnelProbe(localPort), true, os.Stdout)
}
```

---

## Existing Code Map (for the Planner)

| Code Location | Relevance |
|---|---|
| `internal/app/cmd/shell.go:716` | `buildPortForwardCmd` — used by both vscode.go:194 and desktop.go:203; model.go is the 3rd consumer |
| `internal/app/cmd/shell.go:778` | `runReconnectingPortForward` — copy the call pattern exactly |
| `internal/app/cmd/shell.go:733` | `httpsTunnelProbe` — model.go needs `httpTunnelProbe` (plain HTTP variant) |
| `internal/app/cmd/vscode.go:192-196` | Canonical pattern for `km model start` to mirror |
| `internal/app/cmd/desktop.go:203-206` | Second example (HTTPS probe variant) |
| `pkg/compiler/agent_codex.go:67` | `synthesizeCodexConfig` — add `[model_providers.local]` block when `c.LocalBaseURL != ""` |
| `pkg/profile/types.go:772-781` | `AgentCodexSpec` — add `LocalBaseURL string` and `LocalModel string` fields |
| `pkg/profile/schemas/sandbox_profile.schema.json:265` | `agent.codex` schema — add `localBaseURL`, `localModel` string properties |
| `pkg/compiler/userdata.go:5316` | `notifyConfigured` — Phase 120 fixed the CLI-nil gate; GPU profiles safe (have `base/platform` → `cli.noBedrock: true`) |
| `pkg/compiler/userdata.go:4846-4860` | configFiles section 7.6 — runs AFTER initCommands; do NOT write vllm.env here |
| `pkg/compiler/service_hcl.go:20-33` | `isRawAMIID` — raw DLAMI ID passes through to Terraform unchanged |
| `profiles/base/os/debian.yaml` | Sets `ami: ubuntu-24.04`; GPU leaf overrides with raw DLAMI ID |
| `profiles/base/platform.yaml` | Sets `cli.noBedrock: true` → `Spec.CLI != nil` → poller emitted |
| `profiles/base/userinit.yaml` | Installs codex (0.133.0 musl binary); sets `OPENAI_API_KEY: ""` |
| `profiles/base/slack-persandbox.yaml` | Per-sandbox channel + inbound enabled |

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` package + table-driven tests |
| Config file | none (embedded in Go test files) |
| Quick run command | `go test ./pkg/compiler/ ./pkg/profile/ -count=1 -timeout 120s` |
| Full suite command | `go test ./... -count=1 -timeout 600s` |

### Phase Requirements → Test Map

| Gate | Behavior | Test Type | Automated Command | Automated? |
|------|----------|-----------|-------------------|-----------|
| Gate 1: km validate green on all 7 | Profile schema + merged-bytes validation | unit | `go test ./pkg/profile/ -run TestValidate -count=1` | ✅ existing framework |
| Gate 2: synthesizeCodexConfig golden | Local-provider TOML emission | unit | `go test ./pkg/compiler/ -run TestSynthesizeCodexConfig -count=1` | ❌ Wave 0: new test case |
| Gate 3: DLAMI boots, nvidia-smi, vllm.service serves | GPU boot + weight pull + serving | live UAT only | N/A | NO (GPU hardware required) |
| Gate 4: VS Code Continue chat | GUI end-to-end | live UAT only | N/A | NO (GUI + browser) |
| Gate 5: Slack codex round-trip + resume | On-box codex → LiteLLM → vLLM → Slack reply | live UAT (synthetic-HMAC drivable for Slack delivery) | synthetic event_callback POST (see [[project_slack_bridge_inbound_e2e_and_status_attr]]) | PARTIAL (Slack delivery automated; Codex → LiteLLM → vLLM response requires GPU) |
| Gate 6: km model start passthrough | Port-forward + curl/codex completion | live UAT | `curl http://localhost:8001/v1/responses ...` | PARTIAL (port-forward is operator-driven; curl is scriptable) |
| Gate 7: km model start --anthropic + Claude Code | GUI: Claude Code chat via forwarded port | live UAT only | N/A | NO (GUI) |

### Sampling Rate
- **Per task commit:** `go test ./pkg/compiler/ ./pkg/profile/ -count=1 -timeout 120s`
- **Per wave merge:** `go test ./... -count=1 -timeout 600s`
- **Phase gate:** Full suite green + all 7 live UAT gates before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/compiler/agent_codex_test.go` — new table test case for `LocalBaseURL` → `[model_providers.local]` TOML output
- [ ] `pkg/profile/schema_storage_test.go` — new test cases for `agent.codex.localBaseURL` and `agent.codex.localModel` round-trip through JSON schema
- [ ] `pkg/compiler/ec2_storage_test.go` — new test: GPU leaf with raw DLAMI AMI ID passes through as `AMIID`, not `AMISlug`
- [ ] `pkg/profile/validate_test.go` — GPU profile with raw DLAMI AMI ID: validate returns WARN (not ERROR) for desktop=false profiles
- [ ] Golden update: full-output goldens for a representative GPU leaf (`CAPTURE_gpu_qwen_12x=1` or equivalent flag)
- [ ] `internal/app/cmd/model_test.go` — unit tests for `km model start` command wiring (mock execFn, mock fetcher)

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|---|---|---|---|
| Codex CLI supported Chat Completions | Codex requires Responses API only | February 2026 | Any Codex→vLLM integration needs a Responses API gateway (LiteLLM) |
| vLLM did not support GLM-4.x natively | vLLM has `glm4_moe_mtp.py` implementation | 2025-2026 | GLM-4.5/4.6/4.7 deployable without custom code |
| Only `base/os/debian` → ubuntu-24.04 slug | Raw AMI IDs pass through unchanged | Phase 33.1 | DLAMI raw AMI ID in `spec.runtime.ami` works natively |
| notifyConfigured gated on Spec.CLI != nil | Phase 120: also triggers on spec.notification.* | Phase 120 | GPU profiles without explicit `cli:` block still get the Slack poller |

---

## Open Questions

1. **LiteLLM `use_chat_completions_api: true` stability**
   - What we know: documented in LiteLLM `/responses` docs for OpenAI-compatible backends
   - What's unclear: whether the parameter name is stable across LiteLLM minor versions
   - Recommendation: pin LiteLLM version in initCommandsAppend (`pip3 install 'litellm[proxy]==1.x.y'`); test during Wave 1 bring-up

2. **GLM-4.5-Air AWQ at tensor-parallel=4**
   - What we know: `cpatonn/GLM-4.5-Air-AWQ-4bit` notes "tensor_parallel_size <= 2"
   - What's unclear: whether tp=4 works or requires the alternative `cyankiwi/GLM-4.5-Air-AWQ-4bit` repo
   - Recommendation: use `cyankiwi/GLM-4.5-Air-AWQ-4bit` as primary; test tp=4 in live UAT

3. **Kimi-Dev-72B quant — no AWQ available**
   - What we know: no community AWQ repo found for Kimi-Dev-72B (only GPTQ-8bit and GGUF)
   - What's unclear: whether GPTQ-8bit fits cleanly in 192GB at tp=4 for interactive throughput
   - Recommendation: use `btbtyler09/Kimi-Dev-72B-GPTQ-8bit` with `--quantization gptq --max-model-len 16384 --gpu-memory-utilization 0.90`; or FP16 at `--gpu-memory-utilization 0.75 --max-model-len 8192`

4. **`--reasoning-parser glm45` requirement**
   - What we know: the vLLM GLM recipes include `--reasoning-parser glm45` for GLM-4.5 and GLM-4.6
   - What's unclear: whether omitting it causes errors or is just suboptimal (some GLM responses include thinking tags)
   - Recommendation: include it in VLLM_EXTRA for all GLM leaves; it's additive and harmless if the model doesn't emit reasoning tokens

---

## Sources

### Primary (HIGH confidence)
- Live AWS CLI (`describe-images`) — DLAMI AMI IDs us-east-1 (2026-06-27)
- `pkg/compiler/agent_codex.go` — synthesizeCodexConfig structure (code read)
- `pkg/profile/types.go` — AgentCodexSpec, AgentSpec, RuntimeSpec (code read)
- `pkg/compiler/service_hcl.go:20-33` — rawAMIIDPattern, isRawAMIID (code read)
- `pkg/compiler/userdata.go:5316` — notifyConfigured Phase 120 fix (code read)
- [Codex config reference](https://developers.openai.com/codex/config-reference) — wire_api, model_providers schema
- [LiteLLM /responses docs](https://docs.litellm.ai/docs/response_api) — vLLM bridge via use_chat_completions_api
- [vLLM GLM recipes](https://docs.vllm.ai/projects/recipes/en/latest/GLM/GLM.html) — GLM serve flags, tool-call-parser
- [HuggingFace GLM-4.x docs](https://huggingface.co/docs/transformers/model_doc/glm4_moe) — architecture (Glm4MoeForCausalLM), vLLM implementation path

### Secondary (MEDIUM confidence)
- [morphllm Codex provider config](https://www.morphllm.com/codex-provider-configuration) — February 2026 Responses API mandate
- [dev.to vLLM + LiteLLM + Claude Code](https://dev.to/dcruver/running-claude-code-with-local-llms-via-vllm-and-litellm-599b) — full LiteLLM config.yaml working example
- [Qwen readthedocs vLLM](https://qwen.readthedocs.io/en/v2.5/deployment/vllm.html) — AWQ quantization flags
- [vLLM tool calling docs](https://docs.vllm.ai/en/latest/features/tool_calling.html) — --enable-auto-tool-choice, --tool-call-parser per model

### Tertiary (LOW — mark for live validation)
- Community quant repos: `cyankiwi/GLM-4.5-Air-AWQ-4bit`, `QuantTrio/GLM-4.6-AWQ`, `btbtyler09/Kimi-Dev-72B-GPTQ-8bit` — quality and tp=4 compatibility require live UAT
- LiteLLM `use_chat_completions_api: true` parameter stability across versions

---

## Metadata

**Confidence breakdown:**
- DLAMI AMI ID: HIGH (live AWS lookup)
- Raw AMI passthrough: HIGH (code confirmed)
- Ubuntu userdata path: HIGH (code confirmed, OS detection runtime)
- vLLM flags (O2): MEDIUM (VRAM math computed; exact --max-model-len needs live tuning)
- HF_TOKEN injection (O3): HIGH (code-confirmed SOPS mechanism)
- Codex local-provider knob (O6): HIGH (types.go + synthesizeCodexConfig confirmed)
- Codex→vLLM API surface (O7): HIGH (critical discovery: Responses API mandatory since Feb 2026)
- GLM/Kimi repos (O8): MEDIUM-HIGH (HF repos confirmed; tp=4 compatibility live-only)
- Anthropic shim (O9): HIGH (LiteLLM docs + working example confirmed)

**Research date:** 2026-06-27
**Valid until:** 2026-07-27 (stable stack: AMI ID re-resolve monthly; LiteLLM API params: verify before deploy)
