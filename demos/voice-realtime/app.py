"""Realtime voice AI demo — streaming cascade (STT -> LLM -> TTS).

This is the "best cascade" architecture from the HF/Cerebras reference blog, but
with premium streaming pieces swapped in for the weak CPU-only defaults that make
the smolagents / huggingface `speech-to-speech` demos sound flat:

    transport : FastRTC (WebRTC, with Silero VAD + turn-taking + barge-in)
    STT       : Deepgram  (nova-3 by default; swap to Flux for EOT streaming)
    LLM       : Cerebras or Groq (OpenAI-compatible, ultra-fast token streaming)
    TTS       : ElevenLabs Flash v2.5 (~75ms inference, best voice), streamed

The single most important trick lives in `response()`: we DO NOT run the stages
serially (STT -> wait -> LLM -> wait -> TTS). We stream LLM tokens and, the moment
the first *sentence* is complete, we start synthesizing + playing it while the LLM
is still generating the rest. The user hears speech begin ~1 sentence after they
stop talking instead of after the whole reply is written. That overlap — plus a
fast LLM to kill the P95 tail — is what makes it feel real-time.

Every knob is an env var (see .env.example). Runs identically on a laptop
(`run-local.sh`) and on a km sandbox (`profiles/voice-realtime.yaml`).
"""

from __future__ import annotations

import io
import os
import re
import wave

import gradio as gr
import httpx
import numpy as np
from fastrtc import AdditionalOutputs, AlgoOptions, ReplyOnPause, Stream
from openai import OpenAI

# --------------------------------------------------------------------------- #
# Config (all overridable via environment)                                    #
# --------------------------------------------------------------------------- #

DEEPGRAM_API_KEY = os.environ.get("DEEPGRAM_API_KEY", "")
ELEVENLABS_API_KEY = os.environ.get("ELEVENLABS_API_KEY", "")

# LLM: default Cerebras (the reference blog's engine); Groq is a drop-in fallback.
# Both speak the OpenAI API, so we just swap base_url + key + model.
CEREBRAS_API_KEY = os.environ.get("CEREBRAS_API_KEY", "")
GROQ_API_KEY = os.environ.get("GROQ_API_KEY", "")
if CEREBRAS_API_KEY:
    LLM_BASE_URL = os.environ.get("LLM_BASE_URL", "https://api.cerebras.ai/v1")
    LLM_API_KEY = CEREBRAS_API_KEY
    LLM_MODEL = os.environ.get("LLM_MODEL", "llama-3.3-70b")
else:
    LLM_BASE_URL = os.environ.get("LLM_BASE_URL", "https://api.groq.com/openai/v1")
    LLM_API_KEY = GROQ_API_KEY
    LLM_MODEL = os.environ.get("LLM_MODEL", "llama-3.3-70b-versatile")

DEEPGRAM_MODEL = os.environ.get("DEEPGRAM_MODEL", "nova-3")
ELEVENLABS_VOICE_ID = os.environ.get("ELEVENLABS_VOICE_ID", "EXAVITQu4vr4xnSDxMaL")  # "Sarah"
ELEVENLABS_MODEL = os.environ.get("ELEVENLABS_MODEL", "eleven_flash_v2_5")
TTS_SAMPLE_RATE = 24_000  # ElevenLabs pcm_24000

SYSTEM_PROMPT = os.environ.get(
    "SYSTEM_PROMPT",
    "You are a warm, quick-witted voice assistant. Keep replies short and "
    "conversational — one or two sentences unless asked for more. You are being "
    "spoken to out loud, so never use markdown, lists, or emoji; write the way "
    "you would actually speak.",
)

PORT = int(os.environ.get("PORT", "8000"))
HOST = os.environ.get("HOST", "127.0.0.1")  # loopback: km sandboxes have no inbound

_llm = OpenAI(base_url=LLM_BASE_URL, api_key=LLM_API_KEY or "missing")


# --------------------------------------------------------------------------- #
# Stage 1 — STT (Deepgram)                                                     #
# --------------------------------------------------------------------------- #

def _to_wav_bytes(sample_rate: int, audio: np.ndarray) -> bytes:
    """FastRTC hands us (sample_rate, int16 ndarray). Pack it as a WAV for Deepgram."""
    audio = np.asarray(audio).squeeze()
    if audio.dtype != np.int16:
        # Float PCM in [-1, 1] -> int16.
        audio = np.clip(audio, -1.0, 1.0)
        audio = (audio * 32767).astype(np.int16)
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(sample_rate)
        w.writeframes(audio.tobytes())
    return buf.getvalue()


def transcribe(sample_rate: int, audio: np.ndarray) -> str:
    """Utterance -> text. FastRTC's Silero VAD already gave us a clean turn, so a
    single prerecorded call is fast (~150-300ms) and dead simple. For the last
    drop of latency, swap this for Deepgram Flux streaming (see README)."""
    if not DEEPGRAM_API_KEY:
        return ""
    wav = _to_wav_bytes(sample_rate, audio)
    resp = httpx.post(
        "https://api.deepgram.com/v1/listen",
        params={"model": DEEPGRAM_MODEL, "smart_format": "true", "punctuate": "true"},
        headers={"Authorization": f"Token {DEEPGRAM_API_KEY}", "Content-Type": "audio/wav"},
        content=wav,
        timeout=15.0,
    )
    resp.raise_for_status()
    alts = resp.json()["results"]["channels"][0]["alternatives"]
    return alts[0]["transcript"].strip() if alts else ""


# --------------------------------------------------------------------------- #
# Stage 2 — LLM (Cerebras / Groq), streamed and chunked at sentence boundaries #
# --------------------------------------------------------------------------- #

_SENTENCE_END = re.compile(r"([.!?]+[\s\"')\]]*|\n+)")


def stream_sentences(history: list[dict]):
    """Yield the assistant reply one *sentence* at a time as the LLM produces it.
    Sentence granularity is the sweet spot: small enough that TTS starts almost
    immediately, large enough that ElevenLabs gets natural prosody (never
    word-by-word robot speech)."""
    stream = _llm.chat.completions.create(
        model=LLM_MODEL,
        messages=[{"role": "system", "content": SYSTEM_PROMPT}, *history],
        stream=True,
        temperature=0.6,
        max_tokens=300,
    )
    buffer = ""
    for chunk in stream:
        delta = chunk.choices[0].delta.content or ""
        if not delta:
            continue
        buffer += delta
        # Flush every complete sentence, keep the tail.
        while True:
            m = _SENTENCE_END.search(buffer)
            if not m:
                break
            cut = m.end()
            sentence, buffer = buffer[:cut].strip(), buffer[cut:]
            if sentence:
                yield sentence
    if buffer.strip():
        yield buffer.strip()


# --------------------------------------------------------------------------- #
# Stage 3 — TTS (ElevenLabs Flash v2.5), streamed as raw PCM                   #
# --------------------------------------------------------------------------- #

def synthesize(text: str):
    """Stream one sentence of speech. `pcm_24000` = raw 16-bit LE mono, so there's
    no mp3 decode step — we hand FastRTC int16 frames as they arrive off the wire,
    which is what gives a low time-to-first-audio."""
    if not (ELEVENLABS_API_KEY and text):
        return
    url = f"https://api.elevenlabs.io/v1/text-to-speech/{ELEVENLABS_VOICE_ID}/stream"
    body = {
        "text": text,
        "model_id": ELEVENLABS_MODEL,
        "voice_settings": {"stability": 0.4, "similarity_boost": 0.75, "speed": 1.0},
    }
    headers = {"xi-api-key": ELEVENLABS_API_KEY, "Content-Type": "application/json"}
    with httpx.stream(
        "POST", url, params={"output_format": "pcm_24000"},
        json=body, headers=headers, timeout=30.0,
    ) as resp:
        resp.raise_for_status()
        tail = b""
        for raw in resp.iter_bytes(chunk_size=4096):
            if not raw:
                continue
            data = tail + raw
            if len(data) % 2:  # keep a dangling odd byte for the next frame
                tail, data = data[-1:], data[:-1]
            else:
                tail = b""
            if data:
                yield TTS_SAMPLE_RATE, np.frombuffer(data, dtype=np.int16).reshape(1, -1)


# --------------------------------------------------------------------------- #
# Orchestration — the overlapped pipeline                                      #
# --------------------------------------------------------------------------- #

def response(audio: tuple[int, np.ndarray], chatbot: list[dict] | None = None):
    chatbot = chatbot or []
    sample_rate, array = audio

    user_text = transcribe(sample_rate, array)
    if not user_text:
        return  # VAD tripped on noise; say nothing
    chatbot.append({"role": "user", "content": user_text})
    yield AdditionalOutputs(chatbot)  # show the user's line immediately

    spoken = ""
    for sentence in stream_sentences(chatbot):
        spoken += sentence + " "
        for frame in synthesize(sentence):  # <-- speak sentence N while LLM writes N+1
            yield frame

    chatbot.append({"role": "assistant", "content": spoken.strip()})
    yield AdditionalOutputs(chatbot)


# --------------------------------------------------------------------------- #
# Transport — FastRTC WebRTC with Cloudflare TURN (needed behind an            #
# egress-only km sandbox + tunnel; STUN-only is fine for pure localhost).      #
# --------------------------------------------------------------------------- #

def _rtc_configuration():
    """WebRTC media can't reach an egress-only sandbox directly, so we relay via
    TURN. FastRTC proxies Cloudflare's TURN for free with an HF token — the
    lowest-friction way to make the shared link actually carry audio. Falls back
    to STUN-only (works on localhost, and for callers with a public path)."""
    if os.environ.get("HF_TOKEN"):
        try:
            from fastrtc import get_cloudflare_turn_credentials_async
            return get_cloudflare_turn_credentials_async  # FastRTC calls it per-connection
        except Exception:
            pass
    if os.environ.get("TURN_KEY_ID") and os.environ.get("TURN_KEY_API_TOKEN"):
        try:
            from fastrtc import get_cloudflare_turn_credentials
            return get_cloudflare_turn_credentials(
                turn_key_id=os.environ["TURN_KEY_ID"],
                turn_key_api_token=os.environ["TURN_KEY_API_TOKEN"],
            )
        except Exception:
            pass
    return None  # STUN-only


stream = Stream(
    ReplyOnPause(
        response,
        can_interrupt=True,  # barge-in: talk over the assistant and it stops
        algo_options=AlgoOptions(
            audio_chunk_duration=0.6,
            started_talking_threshold=0.2,
            speech_threshold=0.1,
        ),
    ),
    modality="audio",
    mode="send-receive",
    rtc_configuration=_rtc_configuration(),
    additional_inputs=[gr.Chatbot(type="messages", label="Conversation")],
    additional_outputs=[gr.Chatbot(type="messages", label="Conversation")],
    additional_outputs_handler=lambda _old, new: new,
    ui_args={
        "title": "km · realtime voice",
        "subtitle": "Deepgram → Cerebras → ElevenLabs Flash · streamed, overlapped, interruptible",
    },
)


if __name__ == "__main__":
    missing = [n for n, v in (
        ("DEEPGRAM_API_KEY", DEEPGRAM_API_KEY),
        ("ELEVENLABS_API_KEY", ELEVENLABS_API_KEY),
        ("CEREBRAS_API_KEY or GROQ_API_KEY", LLM_API_KEY),
    ) if not v]
    if missing:
        print(f"[warn] missing keys: {', '.join(missing)} — that stage will no-op.")
    print(f"[km-voice] LLM={LLM_MODEL} @ {LLM_BASE_URL} | STT={DEEPGRAM_MODEL} | "
          f"TTS={ELEVENLABS_MODEL} | serving http://{HOST}:{PORT}")
    stream.ui.launch(server_name=HOST, server_port=PORT, quiet=True, show_api=False)
