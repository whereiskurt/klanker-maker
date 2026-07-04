# Realtime voice AI demo on km

A low-latency, **interruptible** voice assistant you talk to in the browser over a
shareable link — running entirely on a km sandbox. It's the "best cascade"
architecture from the [HF × Cerebras post](https://huggingface.co/blog/cerebras-gemma4-voice-ai),
but with the premium streaming pieces swapped in for the weak CPU-only defaults
that make the reference demos ([smolagents/hf-realtime-voice](https://huggingface.co/spaces/smolagents/hf-realtime-voice),
[huggingface/speech-to-speech](https://github.com/huggingface/speech-to-speech))
sound flat.

```
  ┌── your browser ──┐        ┌──────────────── km sandbox (CPU, egress-only) ─────────────────┐
  │  🎙  mic          │  WSS   │  ngrok ── FastRTC (Silero VAD, turn-taking, barge-in)          │
  │  🔊  speaker      │◄──────►│              │                                                 │
  └──────────────────┘  :443  │              ▼                                                  │
        ▲  WebRTC media        │   Deepgram STT ─► Cerebras/Groq LLM ─► ElevenLabs Flash TTS    │
        └── Cloudflare TURN ───┤        (nova-3)      (streamed)          (streamed PCM)         │
           (turns:443)         │              └──────────── overlapped, not serial ─────────────┘
                               └────────────────────────────────────────────────────────────────┘
```

| Stage | This demo | The reference demos use | Why it matters |
|---|---|---|---|
| Transport | **FastRTC** (WebRTC + VAD + barge-in) | FastRTC | Handles mic, turn-taking, interruption for free |
| STT | **Deepgram** nova-3 | Moonshine (CPU) | Cloud-grade accuracy, ~150–300 ms |
| LLM | **Cerebras** / Groq (streamed) | whatever smolagents points at | ~1,800 tok/s kills the P95 tail — the blog's whole thesis |
| TTS | **ElevenLabs Flash v2.5** | Kokoro (CPU) | The single biggest "wow" upgrade — expressive, ~75 ms |

**The one trick that matters** (`app.py` → `response()`): the stages are **overlapped, not
serial**. We stream LLM tokens and, the moment the first *sentence* completes, start
synthesizing + playing it while the LLM is still writing the rest. You hear speech
begin ~1 sentence after you stop talking — not after STT → *then* the whole LLM reply
→ *then* TTS. That overlap plus a fast LLM is what makes it feel real-time. (The HF
`speech-to-speech` repo you flagged runs the stages serially per turn — this is the
fix.) Realistic end-to-end: **~400–600 ms voice-to-voice** with a tight tail.

---

## Why it's shaped this way (km constraints)

A km sandbox is **egress-only** — no inbound, ever (SG has zero ingress rules), and
egress is **TCP 443 + UDP 53 only**. Three consequences baked into the design:

1. **Public link = an outbound tunnel.** The box can't be reached directly, so it
   dials *out* to a tunnel provider that relays public traffic back. We use **ngrok**
   because its agent connects over **443** — `cloudflared` needs port **7844**, which
   km's SG blocks. (Want cloudflared/a stable custom domain? See *Upgrades*.)
2. **WebRTC media needs TURN.** With no inbound and no UDP, browser↔server media
   can't flow directly — it relays through **Cloudflare TURN over TLS/443**. FastRTC
   brokers this for free with an **HF token** (`HF_TOKEN`), the lowest-friction path.
3. **`enforcement: ebpf`, not the default proxy.** The egress allowlist is enforced by
   SNI/DNS **without** MITM interception, so TURN-over-TLS and the HTTP-streaming API
   calls pass through uninspected. The allowlist still pins egress to exactly the
   vendors (`.deepgram.com`, `.elevenlabs.io`, `.cerebras.ai`, `.cloudflare.com`,
   `.ngrok*`, plus AWS/pip/HF) — see `profiles/voice-realtime.yaml`.

Keys are injected via **SOPS** (KMS-decrypted to `/etc/sandbox-secrets.env` at boot)
and never leave the box.

---

## Prerequisites

API keys (free tiers work for a demo):

| Env var | Where | Purpose |
|---|---|---|
| `DEEPGRAM_API_KEY` | https://console.deepgram.com | STT |
| `CEREBRAS_API_KEY` *or* `GROQ_API_KEY` | https://cloud.cerebras.ai · https://console.groq.com | LLM |
| `ELEVENLABS_API_KEY` | https://elevenlabs.io | TTS |
| `HF_TOKEN` | https://huggingface.co/settings/tokens | Cloudflare TURN (WebRTC audio) |
| `NGROK_AUTHTOKEN` | https://dashboard.ngrok.com | Public tunnel |

Optional overrides: `ELEVENLABS_VOICE_ID`, `LLM_MODEL`, `DEEPGRAM_MODEL`,
`SYSTEM_PROMPT` (see `.env.example`).

---

## Try it on your laptop first (recommended)

Iterate against a real mic + the real APIs before spending a sandbox:

```bash
cd demos/voice-realtime
cp .env.example .env      # fill in the keys above
./run-local.sh            # builds a venv, installs deps, launches
# open the printed http://127.0.0.1:8000 and click the mic
```

`HF_TOKEN` is optional locally (STUN works on localhost). The service is byte-for-byte
the same one the sandbox runs.

---

## Deploy to a km sandbox (the shareable demo)

**1. One-time: shared secrets key** (if not already done)

```bash
km bootstrap --shared-secrets-key --dry-run=false
km doctor   # confirm ✓ Shared secrets KMS key
```

**2. Encrypt the keys into a SOPS bundle** the profile expects at
`profiles/secrets/voice.enc.yaml`:

```bash
KMS_ARN=$(aws kms list-aliases --region us-east-1 \
  --query "Aliases[?AliasName=='alias/$(yq -r .resource_prefix km-config.yaml)-sandbox-secrets'].AliasArn" \
  --output text)

cat > /tmp/voice.yaml <<'EOF'
DEEPGRAM_API_KEY: ...
CEREBRAS_API_KEY: ...
ELEVENLABS_API_KEY: ...
HF_TOKEN: ...
NGROK_AUTHTOKEN: ...
EOF

sops --encrypt --kms "$KMS_ARN" --input-type yaml --output-type yaml \
     /tmp/voice.yaml > profiles/secrets/voice.enc.yaml
rm /tmp/voice.yaml
```

**3. Validate + create**

```bash
km validate profiles/voice-realtime.yaml
km create   profiles/voice-realtime.yaml --alias voice
```

**4. Grab the public URL** (boot installs deps + starts the tunnel; give it a few min):

```bash
km shell voice -- cat /opt/km-voice/PUBLIC_URL.txt
# → https://xxxx-xx-xx.ngrok-free.app   ← share this, open it, click the mic
```

Tear down with `km destroy voice --yes` (or let the 6h TTL / 1h idle stop it).

---

## Latency & tuning

Budget for the ~400–600 ms target, and the levers:

- **STT** ~150–300 ms — for the last drop, swap the prerecorded Deepgram call for
  **Deepgram Flux** streaming (native end-of-turn detection replaces VAD, saving
  200–600 ms). `transcribe()` in `app.py` is the single function to change.
- **LLM TTFT** ~100–300 ms — Cerebras is fastest; Groq is the drop-in fallback
  (`GROQ_API_KEY` instead of `CEREBRAS_API_KEY`).
- **TTS TTFA** ~150–300 ms — already streamed as raw PCM (no mp3 decode). Cartesia
  Sonic is a lower-TTFB alternative if you prefer latency over ElevenLabs' voice.
- **Barge-in** — FastRTC's `can_interrupt=True` stops playback when you talk over it.
  For instant cut-off, add ElevenLabs' WebSocket stop-message.

## Upgrades (each is a small, isolated change)

- **Stable public URL / custom domain** — ngrok paid gives a static domain
  (`ngrok http 8000 --domain your.ngrok.app`). A Cloudflare *named* tunnel or a WebRTC
  path over UDP would need km's SG widened (add egress rules in
  `pkg/compiler/security.go`) — a platform change, out of scope here.
- **Self-host the LLM on km's GPU** — point `LLM_BASE_URL` at the on-box Bifrost
  gateway (`http://localhost:8001/openai/v1`, model `vllm-local/local`) and compose
  with `profiles/base/gpu/serve` (Phase 122) to serve a local 70B. Trades Cerebras'
  speed for full self-hosting; needs the G-instance quota.
- **ElevenLabs Conversational AI** — if you'd rather not cascade, their managed
  end-to-end agent (<500 ms, best voice) can replace STT+LLM+TTS; km's job shrinks to
  hosting a custom-LLM webhook behind the same ngrok tunnel.

## Verification status (be honest)

Built and schema-validated (`km validate` passes; `app.py` compiles). **Not** exercised
end-to-end in this environment — there were no API keys or AWS creds here. Before a
live demo, confirm on a real sandbox: (a) the `fastrtc[vad]==0.0.34` pin installs on
Ubuntu 24.04 / Python 3.12 (bump it if not — then re-check `app.py`'s imports and
re-base64 into the profile); (b) Cloudflare TURN-over-TLS negotiates under the
`ebpf` egress lock (the one networking unknown — if audio won't connect, that's the
first thing to check); (c) `km shell voice -- systemctl status km-voice km-tunnel`
shows both green.

## Files

| File | What |
|---|---|
| `app.py` | The streaming cascade (STT → LLM → TTS), overlapped + interruptible |
| `requirements.txt` | Pinned Python deps |
| `run-local.sh` | Run the identical service on your laptop |
| `announce-url.sh` | On-box: capture the ngrok URL → `/opt/km-voice/PUBLIC_URL.txt` |
| `.env.example` | The env knobs |
| `../../profiles/voice-realtime.yaml` | The km profile (embeds `app.py` base64, installs deps + ngrok + systemd units) |
