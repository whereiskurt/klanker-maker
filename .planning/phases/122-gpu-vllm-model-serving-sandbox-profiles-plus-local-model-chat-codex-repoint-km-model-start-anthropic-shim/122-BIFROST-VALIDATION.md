# Phase 122 — Bifrost Gateway Local Validation

**Validated:** 2026-06-27 on Mac (Docker, `maximhq/bifrost:v1.6.0`), AWS `klanker-application` (052251888500, us-east-1).
**Method:** booted Bifrost in Docker, file-only declarative config, exercised every route against real Bedrock, drove codex 0.23.0 through it. No EC2/GPU.

---

## 0. TL;DR — what the original profile got wrong

| # | Original (wrong) | Reality |
|---|------------------|---------|
| 1 | Install via `curl .../releases/download/v1.0.6/bifrost_linux_amd64` (404) | No release binary. Ships as Docker image `maximhq/bifrost:v1.6.0` (and npx `@maximhq/bifrost`). Run via `docker run`. |
| 2 | `bifrost --config X --host --port` CLI flags | No such flags. Config is read from a mounted **app-dir** (`/app/data/config.json`); port/host via env `APP_PORT` / `APP_HOST`. |
| 3 | Top-level `routes`, `anthropic_ingress`, `telemetry`, `server` blocks | None of these exist in the real schema. Routing is implicit: `model = "<provider>/<model-id>"`. Endpoints `/openai`, `/anthropic` are always-on. |
| 4 | `bedrock` key with bare `aws_region` + `models` list of full IDs | Real schema: `bedrock_key_config: { region }` (instance-role) and per-key `models`. Static IDs like `openai.gpt-oss-120b-1:0` (with `-1:0` suffix) **404 in Bifrost's catalog** — use `openai.gpt-oss-120b` (no version suffix). |
| 5 | Claude model `anthropic.claude-3-5-sonnet-20241022-v2:0` | Not enabled + needs inference profile. Use `us.anthropic.claude-sonnet-4-6` (or `...sonnet-4-5-20250929-v1:0`, `...opus-4-8`). On-demand bare Claude IDs error: "on-demand throughput isn't supported, use an inference profile." |
| 6 | `vllm` provider with inline `base_url`/`allow_private_network` on the key | Custom OpenAI-compatible upstreams use a **separate provider** with `network_config.base_url` + `custom_provider_config.base_provider_type: openai`. |

**Everything proved working end-to-end:** gpt-oss-120b/20b (Bedrock), Claude (Bedrock) via both `/openai` and `/anthropic` endpoints, the OpenAI Responses API, and codex 0.23.0 (Responses **and** chat).

---

## 1. CORRECTED `/etc/km/bifrost-config.json`

Real Bifrost v1.6.x schema. Bedrock uses **instance-role only** (region, no static keys → AWS default credential chain picks up the EC2 instance profile = SigV4, no key inside the box). The 5-route intent is preserved as **model-ID conventions** (Bifrost has no named-route concept; the "route" is just the `provider/model` string the caller sends).

```json
{
  "config_store": {
    "enabled": false
  },
  "providers": {
    "bedrock": {
      "keys": [
        {
          "name": "bedrock-iam",
          "models": ["*"],
          "weight": 1.0,
          "bedrock_key_config": {
            "region": "us-east-1"
          }
        }
      ]
    },
    "anthropic": {
      "keys": [
        {
          "name": "anthropic-direct",
          "value": "env.ANTHROPIC_API_KEY",
          "models": ["*"],
          "weight": 1.0
        }
      ]
    },
    "openai": {
      "keys": [
        {
          "name": "openai-frontier",
          "value": "env.OPENAI_API_KEY",
          "models": ["*"],
          "weight": 1.0
        }
      ]
    },
    "vllm-local": {
      "keys": [
        {
          "name": "vllm-key",
          "value": "dummy",
          "models": ["*"],
          "weight": 1.0
        }
      ],
      "network_config": {
        "base_url": "http://127.0.0.1:8000",
        "default_request_timeout_in_seconds": 120
      },
      "custom_provider_config": {
        "base_provider_type": "openai",
        "allow_private_network": true,
        "allowed_requests": {
          "chat_completion": true,
          "chat_completion_stream": true,
          "responses": true,
          "responses_stream": true
        }
      }
    }
  }
}
```

### The 5 "routes" — now expressed as the model string callers send

| Original route intent | What the caller sends as `model` | Status |
|---|---|---|
| `local` (vLLM :8000) | `vllm-local/local` (or `vllm-local/<served-model-name>`) | ✅ schema valid; serves once vLLM is up on :8000 (502 connection-refused until then — proves provider registered) |
| `claude-bedrock` (IAM role) | `bedrock/us.anthropic.claude-sonnet-4-6` | ✅ live `CLAUDEBR-OK` |
| `claude-anthropic` (direct key) | `anthropic/claude-sonnet-4-6` (resolves once `ANTHROPIC_API_KEY` is SOPS-provisioned) | ⚠️ schema valid, key-gated (untested — no key locally; provider boots fine with dummy) |
| `gpt-oss-bedrock` (IAM role) | `bedrock/openai.gpt-oss-120b` **(no `-1:0` suffix!)** | ✅ live `GPTOSS120-OK`; 20b = `bedrock/openai.gpt-oss-20b` → `GPTOSS20-OK` |
| `gpt-frontier` (dormant) | `openai/gpt-5` etc. (active only when `OPENAI_API_KEY` provisioned) | ⚠️ schema valid, key-gated. To keep it dormant, just don't provision the key (provider boots with dummy, requests fail cleanly). |

**Telemetry:** there is NO `telemetry` block in config.json. Bifrost's telemetry plugin is active by default (`plugin status: telemetry - active` in boot logs). OTLP export is configured via standard env vars on the process — set `OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_EXPORTER_OTLP_PROTOCOL` in the container env (which the systemd unit already does). No JSON config needed; the original `telemetry: { otlp_endpoint }` block was invented and must be deleted.

**Catalog gotcha (the gpt-oss blocker, root-caused):** Bifrost validates every model ID against its built-in model catalog and returns `404 "The model '<id>' does not exist"` for unknowns. The catalog lists gpt-oss as `openai.gpt-oss-120b` (and `gpt-oss-120b`), but the **full Bedrock ID with the version suffix `-1:0` is NOT a resolvable catalog entry** — so `bedrock/openai.gpt-oss-120b-1:0` 404s while `bedrock/openai.gpt-oss-120b` succeeds and reaches Bedrock. Drop the `-1:0`. (Aliases don't help — they resolve to the bad full ID and re-hit the wall.) Claude IS in the catalog under its full ID, so Claude only needs the `us.` inference-profile prefix.

---

## 2. CORRECTED Bifrost install + systemd unit (replaces the fake binary curl)

No binary download. The box already has Docker (DLAMI). Bifrost runs as a `docker run` systemd unit, config bind-mounted as the app-dir, AWS creds from the **instance role** (default credential chain — no env vars, no key files needed inside the container for SigV4).

### `initCommandsAppend` (replace the curl line)

```yaml
    initCommandsAppend:
      - "docker pull maximhq/bifrost:v1.6.0"
      - "mkdir -p /etc/km/bifrost-data && cp /etc/km/bifrost-config.json /etc/km/bifrost-data/config.json"
      - "systemctl daemon-reload"
      - "systemctl enable --now vllm.service"
      - "systemctl enable --now bifrost.service"
```

> The config file must live **inside the mounted app-dir as `config.json`**. We keep the authored file at `/etc/km/bifrost-config.json` (so it stays a clean `configFiles` entry) and copy it into the app-dir `/etc/km/bifrost-data/config.json` at boot. (Alternatively bind-mount the single file to `/app/data/config.json` — but Bifrost also writes `logs.db`/`config.db` into the app-dir, so mount a **directory**, not just the file.)

### `bifrost.service` unit (replaces the old binary-based unit)

```ini
[Unit]
Description=Bifrost multi-provider model router (vLLM + Bedrock + Anthropic)
After=docker.service vllm.service network-online.target
Wants=docker.service vllm.service network-online.target

[Service]
Type=simple
# AWS creds come from the EC2 INSTANCE ROLE via the SDK default credential chain
# (no AWS_* env vars needed for SigV4). IMDS is reachable from the default docker
# bridge, so --network host is NOT required for credential resolution; we use
# host networking so :8001 and the loopback vLLM (:8000) are reachable.
ExecStartPre=/bin/sleep 10
ExecStartPre=-/usr/bin/docker rm -f km-bifrost
ExecStart=/usr/bin/docker run --rm --name km-bifrost \
  --network host \
  -e APP_PORT=8001 \
  -e APP_HOST=127.0.0.1 \
  -e AWS_REGION=us-east-1 \
  -e OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
  -e OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
  --env-file /etc/km/bifrost-env \
  -v /etc/km/bifrost-data:/app/data \
  maximhq/bifrost:v1.6.0
ExecStop=/usr/bin/docker rm -f km-bifrost
Restart=on-failure
RestartSec=15

[Install]
WantedBy=multi-user.target
```

Notes:
- `--network host` → the container shares the host netns: Bifrost binds `127.0.0.1:8001`, reaches local vLLM at `http://127.0.0.1:8000`, and IMDS (`169.254.169.254`) resolves the instance role. (If you prefer the bridge network, the vLLM `base_url` must become `http://host.docker.internal:8000` or the docker-bridge gateway IP, and add `--add-host=host.docker.internal:host-gateway`. Host networking is simplest.)
- `/etc/km/bifrost-env` (the optional `--env-file`) is where the operator SOPS-injects `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` for the key-gated routes. Absent → those routes fail cleanly; Bedrock routes need nothing.
- **No `AWS_ACCESS_KEY_ID` etc.** — leaving them unset is what forces the SDK to use the instance role (SigV4). The local Mac test passed env creds only because a laptop has no instance profile; the box must NOT.

---

## 3. Exact endpoint paths / ports (all verified live on :8001)

| Consumer | Base URL | Full path hit | Model string | Verified |
|---|---|---|---|---|
| **OpenAI SDK / curl (chat)** | `http://localhost:8001/openai/v1` | `POST /openai/v1/chat/completions` | `bedrock/openai.gpt-oss-120b` | ✅ 200 |
| **OpenAI Responses API** | `http://localhost:8001/openai/v1` | `POST /openai/v1/responses` | `bedrock/openai.gpt-oss-120b` | ✅ 200 (returns proper `responses` object) |
| **Claude Code (`ANTHROPIC_BASE_URL`)** | `http://localhost:8001/anthropic` | `POST /anthropic/v1/messages` | `bedrock/us.anthropic.claude-sonnet-4-6` | ✅ 200 `CLAUDEANTHRO-OK` |
| **codex** | `http://localhost:8001/openai/v1` | see §4 | `bedrock/openai.gpt-oss-120b` | ✅ 200 |
| **Continue / local vLLM direct** | `http://localhost:8000/v1` (NOT via Bifrost) | `POST /v1/chat/completions` | `local` | unchanged from authored config (direct to vLLM) |

- **Port: 8001** (set via `APP_PORT=8001`). Default is 8080 — must override.
- `ANTHROPIC_BASE_URL=http://localhost:8001/anthropic` for Claude Code. The SDK appends `/v1/messages`. ✅ confirmed the path resolves and a Bedrock-backed Claude reply comes back.
- Bifrost also serves its web UI at `http://<host>:8001/` (same port). Harmless; loopback-bound.

### codex config.toml that worked (Responses)

```toml
model = "bedrock/openai.gpt-oss-120b"
model_provider = "bifrost"

[model_providers.bifrost]
name = "Bifrost local gateway"
base_url = "http://localhost:8001/openai/v1"
wire_api = "responses"
```

`OPENAI_API_KEY=dummy` in the env (codex requires *a* key string even though Bifrost ignores it for Bedrock). For the profile's `agent.codex.localBaseURL` keep `http://localhost:8001/openai/v1` and add `wire_api`. **The authored `localBaseURL: "http://localhost:8001/v1"` is WRONG — it must be `/openai/v1`** (the `/openai` prefix selects the OpenAI-compat ingress; bare `/v1` 404s).

---

## 4. Does codex 0.23.0 use Responses or chat-completions?

**Both — codex 0.23.0 honors `wire_api` and works against either.** Captured from Bifrost access logs (UA `codex_cli_rs/0.23.0 (Mac OS 26.3.1; arm64)`):

| codex `wire_api` | Endpoint codex actually hit | Result |
|---|---|---|
| `"responses"` | `POST /openai/v1/responses` (200) | ✅ `CODEX-RESPONSES-OK` |
| `"chat"` | `POST /openai/v1/chat/completions` (200) | ✅ `CODEX-CHAT-OK` |

Evidence (Bifrost log lines):
```
POST /openai/v1/responses        200  UA: codex_cli_rs/0.23.0 (Mac OS 26.3.1; arm64)   # wire_api=responses
POST /openai/v1/chat/completions 200                                                    # wire_api=chat
```

**Verdict:** The design claim "Codex requires the OpenAI Responses API" is **half-right for 0.23.0**: Responses is codex's native/default path for OpenAI-family providers and it works perfectly through Bifrost (Bifrost translates `/responses` → Bedrock Converse). But codex 0.23.0 ALSO supports `wire_api = "chat"` as an explicit fallback, which also works through Bifrost. So Responses is **not strictly required** at 0.23.0 — but it IS the recommended/default wire, and since both Bifrost and vLLM serve `/responses`, keep `wire_api = "responses"`. (If a future codex drops chat-completions entirely, you're already on the right path.)

---

## 5. Remaining gaps (box-only or untested locally)

- **`claude-anthropic` direct-key route** (`anthropic/claude-sonnet-4-6` via `api.anthropic.com`): provider schema validated + boots clean with a dummy key, but NOT exercised with a real key (none locally). The operator SOPS-provisions `ANTHROPIC_API_KEY` into `/etc/km/bifrost-env`; verify on the box. Same for `gpt-frontier` (`OPENAI_API_KEY`).
- **vLLM `local` route**: schema validated (provider registers; routing reaches the upstream and returns 502 connection-refused since no vLLM was running). Cannot prove a real local completion without a GPU. The `network_config.base_url` + `custom_provider_config` shape is correct and accepted. Confirm on the box once vLLM serves :8000, and confirm the `--served-model-name local` lines up with the `model` you send (`vllm-local/local`).
- **Instance-role SigV4**: proved the **region-only, no-static-key** `bedrock_key_config` works (requests reached Bedrock and returned). On the box the AWS SDK default chain must resolve the **EC2 instance profile** (not env creds). Two box-only checks: (a) the sandbox instance role grants `bedrock:InvokeModel` + `bedrock:InvokeModelWithResponseStream` for `openai.gpt-oss-120b-1:0`, `openai.gpt-oss-20b-1:0`, and the `us.anthropic.claude-*` inference-profile ARNs (the IAM note in the authored profile flagged this — still a real Plan-05 UAT item; **confirm gpt-oss model IDs are in the role allowlist**); (b) the container reaches IMDS for the instance role (host networking handles this).
- **Model-ID drift**: Claude IDs change. Validated current ones: `us.anthropic.claude-sonnet-4-6`, `us.anthropic.claude-sonnet-4-5-20250929-v1:0`, `us.anthropic.claude-opus-4-8` (all live via Bifrost→Bedrock). The stale `claude-3-5-sonnet-20241022-v2:0` / `claude-3-opus-20240229` in the original config are NOT enabled — replaced above.
- **OTLP telemetry**: telemetry plugin is active by default; export is env-driven, not config-driven. Confirm spans actually land at the km tracing sidecar (:4318) on the box — only assertable end-to-end there.
- **App-dir writes**: Bifrost writes `logs.db`/`config.db` into the mounted app-dir. On the box make sure `/etc/km/bifrost-data` is writable by the container (root in container → fine) and survives restarts; it's on root vol, not `/data` — fine for an ephemeral logs db.

---

## BIFROST VALIDATION COMPLETE

1. **Does the config work?** Yes — the corrected schema boots clean in file-only mode on :8001 and serves real completions through all live routes (gpt-oss-120b/20b + Claude via Bedrock, on both `/openai` and `/anthropic` endpoints).
2. **What changed?** Install is `docker run maximhq/bifrost:v1.6.0` (no release binary/CLI flags); config is the real `providers`-only schema (Bedrock = region-only instance-role, separate `vllm-local` custom provider, no invented `routes`/`telemetry`/`server`/`anthropic_ingress` blocks); gpt-oss IDs drop the `-1:0` suffix (catalog 404 otherwise); Claude uses `us.` inference-profile IDs; codex `localBaseURL` fixed to `/openai/v1`.
3. **Codex Responses verdict?** Codex 0.23.0 hit `/openai/v1/responses` with `wire_api="responses"` (proven via UA in Bifrost logs) AND `/openai/v1/chat/completions` with `wire_api="chat"` — both work; Responses is default/recommended but not strictly required at 0.23.0.
4. **Endpoints/ports:** OpenAI `:8001/openai/v1/{chat/completions,responses}`, Claude-Code `ANTHROPIC_BASE_URL=:8001/anthropic` (→`/v1/messages`), codex `base_url=:8001/openai/v1` `wire_api=responses`.
5. **Still box-only:** real-key Anthropic/OpenAI routes, the live vLLM `local` route, instance-role SigV4 + the Bedrock IAM allowlist covering the gpt-oss model IDs, and OTLP spans reaching the :4318 sidecar.
