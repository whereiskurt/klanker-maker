---
phase: 122-gpu-vllm-model-serving-sandbox-profiles-plus-local-model-chat-codex-repoint-km-model-start-anthropic-shim
plan: "02"
subsystem: profiles, gpu-serving, bifrost-gateway
tags: [phase-122, wave-2, gpu-profiles, bifrost, vllm, dlami, abstract-fragment]
dependency_graph:
  requires:
    - 122-01 (AgentCodexSpec.LocalBaseURL/LocalModel fields + JSON schema + test scaffolds)
  provides:
    - profiles/base/gpu/serve.yaml (abstract fragment: vLLM unit + Bifrost 5-route gateway + OTEL)
    - 7 GPU leaf profiles (km validate green on merged bytes)
    - scripts/validate-all-profiles.sh (20-entry inventory, exits 0)
  affects:
    - profiles/base/gpu/serve.yaml (new)
    - profiles/gpu-qwen-12x.yaml (new)
    - profiles/gpu-llama-12x.yaml (new)
    - profiles/gpu-qwen-48x.yaml (new)
    - profiles/gpu-llama-48x.yaml (new)
    - profiles/gpu-glmair-12x.yaml (new)
    - profiles/gpu-kimidev-12x.yaml (new)
    - profiles/gpu-glm46-48x.yaml (new)
    - scripts/validate-all-profiles.sh (updated)
tech_stack:
  added:
    - Bifrost v1.0.6 (maximhq/bifrost, Go binary, multi-provider model router, :8001)
    - vLLM (docker-run systemd unit, :8000, chat-completions + responses API)
    - AWS DLAMI ami-0a9d213b92dabc044 (Ubuntu 24.04, 2026-06-26)
  patterns:
    - Phase 117 composable multi-parent inheritance (extends list)
    - Bool zero-value trap mitigation (spot/hibernation in leaves, NOT fragment)
    - initCommandsAppend for /etc/km/vllm.env (not configFiles — ordering constraint)
    - SOPS HF_TOKEN injection via grep append (Llama leaves only)
    - Bifrost multi-provider JSON route config (5 named routes)
key_files:
  created:
    - profiles/base/gpu/serve.yaml
    - profiles/gpu-qwen-12x.yaml
    - profiles/gpu-llama-12x.yaml
    - profiles/gpu-qwen-48x.yaml
    - profiles/gpu-llama-48x.yaml
    - profiles/gpu-glmair-12x.yaml
    - profiles/gpu-kimidev-12x.yaml
    - profiles/gpu-glm46-48x.yaml
  modified:
    - scripts/validate-all-profiles.sh
decisions:
  - "Bifrost v1.0.6 pinned (2026-06-25 release) via direct GitHub binary download — avoids npm/npx dependency and matches km's sidecar-binary install pattern"
  - "Bifrost config JSON schema: providers{openai,anthropic,bedrock,vllm} + routes[5] + anthropic_ingress + telemetry — top-level keys from maximhq/bifrost v1.0.6 config format"
  - "claude-bedrock + gpt-oss-bedrock: keyless routes (Bedrock provider uses LoadDefaultConfig / EC2 instance role SigV4 — NO static API key needed)"
  - "gpt-frontier route: enabled:false (dormant until OPENAI_API_KEY provisioned via SOPS)"
  - "claude-anthropic route: active/wired — operator SOPS-provisions ANTHROPIC_API_KEY at UAT"
  - "vllm.env written in initCommandsAppend for ALL leaves (NOT configFiles) — configFiles section 7.6 runs AFTER initCommands; any HF_TOKEN append via initCommandsAppend would race if file didn't exist yet"
  - "allowedSecretPaths declared in fragment (/sandbox/*/secrets) so all GPU leaves inherit it; Llama leaves redeclare (list-union dedupes — no double entry)"
  - "shell/workingDir/sourceAccess added to fragment (Rule 1 fix — required fields absent from all base fragments; all leaves would fail merged-bytes validation without them)"
  - "IAM gap flagged for Plan 05 UAT: gpt-oss model IDs (openai.gpt-oss-120b-1:0, openai.gpt-oss-20b-1:0) require bedrock:InvokeModel grant; existing ec2spot/sandbox role may not cover them"
metrics:
  duration: "270s"
  completed_date: "2026-06-27"
  tasks_completed: 3
  tasks_total: 3
  files_modified: 9
---

# Phase 122 Plan 02: GPU Profiles — base/gpu/serve Fragment + 7 Leaves + Validate Gate

Abstract GPU serving fragment with Bifrost 5-route model router (vLLM/claude-bedrock/claude-anthropic/gpt-oss-bedrock/gpt-frontier) + 7 leaf profiles (g6e.12x/48x, all km validate green on merged bytes).

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Author base/gpu/serve.yaml — vLLM unit + Bifrost router + OTEL | a12e55d1 | profiles/base/gpu/serve.yaml |
| 2 | Author 7 GPU leaf profiles + fix fragment required fields | 7ce23a01 | 7 leaf profiles + serve.yaml patch |
| 3 | HF_TOKEN SOPS stanza (Llama leaves already authored in T2) + validate-all gate | cd47f968 | scripts/validate-all-profiles.sh |

## What Was Built

### Task 1 — Abstract Fragment `profiles/base/gpu/serve.yaml`

Abstract fragment (`metadata.abstract: true`, `metadata.name: base-gpu-serve`) providing:

- **Runtime:** DLAMI `ami-0a9d213b92dabc044` (Ubuntu 24.04, 2026-06-26), substrate ec2, `additionalVolume` 300GB at `/data`. No `spot`/`hibernation`/`mountEFS` booleans (Phase 117 Pitfall 2 respected).
- **Lifecycle:** ttl 8h, idleTimeout 1h, teardownPolicy stop.
- **Budget:** compute 120 USD (12x floor; leaves override), ai 2.00 USD.
- **Execution:** shell /bin/bash, workingDir /workspace, useBedrock false, env HF_HOME=/data/hf + OPENAI_API_KEY="" (dummy).
- **initCommandsAppend:** download Bifrost v1.0.6 binary to `/usr/local/bin/bifrost`, daemon-reload, enable vllm.service + bifrost.service.
- **configFiles:**
  - `/etc/systemd/system/vllm.service` — docker-run (--ipc=host, --gpus all, :8000, EnvironmentFile=/etc/km/vllm.env, ExecStartPre=sleep 5)
  - `/etc/systemd/system/bifrost.service` — Bifrost on :8001, After=vllm.service, BIFROST_OTLP_ENDPOINT=http://localhost:4318, ExecStartPre=sleep 10
  - `/etc/km/bifrost-config.json` — 5 named routes (see below)
  - `/home/sandbox/.continue/config.yaml` — Continue editor pointing at vLLM :8000/v1
- **sourceAccess:** allowlist, github allowedRepos/Refs `*`.
- **iam.allowedSecretPaths:** `/sandbox/*/secrets` (SOPS injection for ANTHROPIC_API_KEY + HF_TOKEN).
- **agent:** default codex, codex.localBaseURL `http://localhost:8001/v1`, codex.localModel `local`.

### Bifrost Config JSON Schema

Bifrost v1.0.6 config uses `providers{}` + `routes[]` + `anthropic_ingress` + `telemetry` + `server` top-level keys. The 5 named routes:

| Route name | Provider | Backend | Auth | Enabled |
|---|---|---|---|---|
| `local` | vllm | http://localhost:8000 | none (dummy key) | always |
| `claude-bedrock` | bedrock | Bedrock Claude Sonnet/Opus | EC2 instance role (SigV4, no key) | active |
| `claude-anthropic` | anthropic | api.anthropic.com | ANTHROPIC_API_KEY (SOPS at UAT) | active/wired |
| `gpt-oss-bedrock` | bedrock | Bedrock openai.gpt-oss-120b/20b | EC2 instance role (SigV4, no key) | active |
| `gpt-frontier` | openai | api.openai.com | OPENAI_API_KEY (SOPS) | **dormant** (enabled:false) |

`anthropic_ingress.path_prefix: /anthropic` — Claude Code sets `ANTHROPIC_BASE_URL=http://localhost:8001/anthropic`.

Bifrost Bifrost v1.0.6 binary URL: `https://github.com/maximhq/bifrost/releases/download/v1.0.6/bifrost_linux_amd64`

### Task 2 — 7 GPU Leaf Profiles

Each leaf: `extends: [base/os/debian, base/network/safenetwork, base/userinit, base/platform, base/slack-persandbox, base/gpu/serve]`

| Profile | Instance | Model | Quant | TP | Budget |
|---|---|---|---|---|---|
| gpu-qwen-12x | g6e.12xlarge | Qwen/Qwen2.5-72B-Instruct-AWQ | AWQ 4-bit | 4 | $120 |
| gpu-llama-12x | g6e.12xlarge | meta-llama/Llama-3.3-70B-Instruct | FP8 | 4 | $120 |
| gpu-qwen-48x | g6e.48xlarge | Qwen/Qwen2.5-72B-Instruct | FP16 | 8 | $300 |
| gpu-llama-48x | g6e.48xlarge | meta-llama/Llama-3.3-70B-Instruct | FP16 | 8 | $300 |
| gpu-glmair-12x | g6e.12xlarge | cyankiwi/GLM-4.5-Air-AWQ-4bit | AWQ 4-bit | 4 | $120 |
| gpu-kimidev-12x | g6e.12xlarge | btbtyler09/Kimi-Dev-72B-GPTQ-8bit | GPTQ 8-bit | 4 | $120 |
| gpu-glm46-48x | g6e.48xlarge | QuantTrio/GLM-4.6-AWQ | AWQ 4-bit | 8 | $300 |

All leaves set `spot: false`, `hibernation: false` in the leaf spec (bool zero-value trap respected). 48x leaves override `budget.compute.maxSpendUSD: 300`.

Llama leaves additionally carry:
- `secrets.sopsFile: ./secrets/llama-hf.enc.yaml` (operator creates at Plan 05 UAT)
- `iam.allowedSecretPaths: [/sandbox/*/secrets]`
- Second `initCommandsAppend` line: `grep '^HF_TOKEN=' /etc/sandbox-secrets.env >> /etc/km/vllm.env 2>/dev/null || true`

### Task 3 — validate-all-profiles.sh Update

Script updated to 20-entry inventory (header updated: "Phase 122 Plan 02"; count bump). PROFILES array gains all 7 GPU leaves. `bash scripts/validate-all-profiles.sh` exits 0: 8 base fragments skipped, all 20 concrete leaves validate green.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fragment missing required fields (shell, workingDir, sourceAccess.mode)**
- **Found during:** Task 2 verification — all 5 ungated leaves failed `km validate` with `spec.execution.shell: minLength: got 0`, `spec.execution.workingDir: minLength: got 0`, `spec.sourceAccess.mode: value must be one of 'allowlist', 'none'`.
- **Issue:** These required fields are NOT provided by any of the 6 base fragments in the extends chain (base/os/debian, base/network/safenetwork, base/userinit, base/platform, base/slack-persandbox, base/gpu/serve). Pre-existing leaves like learner.yaml set them directly in the leaf spec. The GPU leaves were authored to be minimal per the plan ("~15-25 lines each"), but the merged profile must satisfy all required-field constraints.
- **Fix:** Added `spec.execution.{shell: /bin/bash, workingDir: /workspace, useBedrock: false}` and `spec.sourceAccess: {mode: allowlist, github: {allowedRepos: ["*"], allowedRefs: ["*"]}}` to the abstract fragment. This is the correct placement — they're shared across all 7 GPU leaves and don't conflict with Phase 117 bool-trap concerns (these fields have no zero-value ambiguity for string/enum types).
- **Files modified:** `profiles/base/gpu/serve.yaml`
- **Commit:** 7ce23a01 (combined with Task 2)

### Bifrost Config JSON Schema Note

The exact Bifrost v1.0.6 config JSON schema was inferred from the gateway bakeoff research (`122-RESEARCH-gateway-bakeoff.md`) and the Bifrost docs. The schema uses top-level `providers` (keyed by provider name, each with a `keys` array containing `value`/`models`/`weight` + optional `base_url`/`aws_region`/`allow_private_network`) and a top-level `routes` array (each route: `name`/`description`/`model_name`/`provider`/`enabled`). The `anthropic_ingress` and `telemetry` blocks are Bifrost v1.0.6 top-level extensions. **Confirm schema correctness in Plan 05 live UAT** — if Bifrost rejects the config on startup, the exact field names may need adjustment.

### sopsFile Placeholder Not Needed

The plan asked to "confirm whether the validator needs the file to exist." Confirmed: `pkg/profile/validate.go:519-524` checks only that `sopsFile` ends with `.enc.yaml` (suffix check, offline). The file does NOT need to exist for km validate to pass. No placeholder stub was created. The operator creates `secrets/llama-hf.enc.yaml` at Plan 05 UAT.

## IAM Gap — Plan 05 UAT Required

The Bedrock routes (`claude-bedrock`, `gpt-oss-bedrock`) use the EC2 sandbox IAM instance role (SigV4, no static key). The existing `ec2spot`/sandbox role grants `bedrock:InvokeModel` for Claude model IDs. **The gpt-oss model IDs (`openai.gpt-oss-120b-1:0`, `openai.gpt-oss-20b-1:0`) may NOT yet be in the allowlist.** Verify `iam:SimulatePrincipalPolicy` in Plan 05; add an IAM statement if absent.

## Self-Check

Checking created files exist:
- `/Users/khundeck/working/klankrmkr/profiles/base/gpu/serve.yaml` — FOUND
- `/Users/khundeck/working/klankrmkr/profiles/gpu-qwen-12x.yaml` — FOUND
- `/Users/khundeck/working/klankrmkr/profiles/gpu-llama-12x.yaml` — FOUND
- `/Users/khundeck/working/klankrmkr/profiles/gpu-qwen-48x.yaml` — FOUND
- `/Users/khundeck/working/klankrmkr/profiles/gpu-llama-48x.yaml` — FOUND
- `/Users/khundeck/working/klankrmkr/profiles/gpu-glmair-12x.yaml` — FOUND
- `/Users/khundeck/working/klankrmkr/profiles/gpu-kimidev-12x.yaml` — FOUND
- `/Users/khundeck/working/klankrmkr/profiles/gpu-glm46-48x.yaml` — FOUND
- `scripts/validate-all-profiles.sh exits 0` — VERIFIED (20 profiles valid)

Checking commits exist:
- `a12e55d1` — Task 1 (feat: base/gpu/serve.yaml fragment)
- `7ce23a01` — Task 2 (feat: 7 GPU leaf profiles + fragment fix)
- `cd47f968` — Task 3 (feat: validate-all-profiles.sh update)

## Self-Check: PASSED
