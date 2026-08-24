#!/usr/bin/env bash
# scripts/stage-model-to-s3.sh — one-time HuggingFace -> S3 weight staging for the
# GPU serving profiles that read their weights from S3 instead of pulling from
# HuggingFace at every boot (profiles/gpu-qwen38-oblit-12x*.yaml).
#
# WHY STAGE AT ALL
#   A HuggingFace pull re-downloads ~55.6GB on every fresh sandbox and depends on
#   the upstream repo staying public and unmodified. Staged in S3 the same bytes
#   land in-region at S3 speed, pinned to whatever revision you staged, and stay
#   available if the repo is later gated, edited, or removed.
#
# WHERE TO RUN IT
#   On a box with fast network and >=60GB free disk — a running km sandbox is
#   ideal, since its instance role can already write the install's artifacts
#   bucket in-region (the region-scoped grant in ec2spot's ec2spot_region_lock).
#   NOT on a laptop: this moves ~111GB total, down then back up.
#
#   Run it against the SAME install the sandbox will run in. The bucket is read
#   from KM_ARTIFACTS_BUCKET, which is per-install — so if you run these GPU
#   sandboxes from a second install (a different resource_prefix, or a different
#   AWS account), stage into THAT install's bucket. The profiles resolve the
#   bucket at boot rather than hardcoding it, so they need no edit either way.
#
# WHAT IT EXCLUDES
#   The upstream repo also ships GGUF (5 quantizations) and MLX (4-bit + 8-bit)
#   copies of the same weights — well over 100GB that nothing in this stack uses.
#   vLLM wants the root BF16 safetensors only, so those trees are excluded.
#
# HF_TOKEN is NOT required: OBLITERATUS/Qwen3.8-27B-OBLITERATED is public and
# ungated. Export it anyway if you point --repo at a gated repo.
#
# Usage:
#   scripts/stage-model-to-s3.sh [--bucket BUCKET] [--slug SLUG]
#                                [--repo REPO] [--workdir DIR] [--keep]
#
#   --bucket   S3 bucket to stage into      (default: $KM_ARTIFACTS_BUCKET)
#   --slug     key prefix under models/     (default: qwen38-oblit-27b)
#   --repo     HuggingFace repo id          (default: OBLITERATUS/Qwen3.8-27B-OBLITERATED)
#   --workdir  local scratch dir            (default: /data/staging/<slug>)
#   --keep     keep the local download after upload (default: delete it)
#
# The --slug MUST match MODEL_SLUG in the profile's km-stage-model.sh.

set -euo pipefail

REPO="OBLITERATUS/Qwen3.8-27B-OBLITERATED"
SLUG="qwen38-oblit-27b"
# Pin the upstream commit by default. This repo is rebuilt in place — the V3
# pass on 2026-08-23 rewrote chat_template.jinja and fixed an eos_token_id/
# pad_token_id bug that caused early stopping, without changing the repo id.
# An unpinned stage therefore silently changes what every sandbox serves. Pass
# --revision main to deliberately take whatever is current.
REVISION="af34629438e091685513fe2f66c0f2918de5734c"
BUCKET="${KM_ARTIFACTS_BUCKET:-}"
WORKDIR=""
KEEP=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bucket)  BUCKET="$2"; shift 2 ;;
    --slug)    SLUG="$2"; shift 2 ;;
    --repo)    REPO="$2"; shift 2 ;;
    --revision) REVISION="$2"; shift 2 ;;
    --workdir) WORKDIR="$2"; shift 2 ;;
    --keep)    KEEP=1; shift ;;
    -h|--help) sed -n '2,40p' "$0"; exit 0 ;;
    *) echo "ERROR: unknown argument: $1" >&2; exit 2 ;;
  esac
done

WORKDIR="${WORKDIR:-/data/staging/${SLUG}}"

if [[ -z "$BUCKET" ]]; then
  echo "ERROR: no bucket. Pass --bucket, or export KM_ARTIFACTS_BUCKET." >&2
  echo "       On a km sandbox: . /etc/profile.d/km-identity.sh" >&2
  exit 2
fi

if ! command -v aws >/dev/null 2>&1; then
  echo "ERROR: aws CLI not found on PATH." >&2
  exit 2
fi

# The HuggingFace CLI is `hf` in current huggingface_hub; older installs ship
# `huggingface-cli`. Accept either, install if neither is present.
HF_BIN=""
if command -v hf >/dev/null 2>&1; then
  HF_BIN="hf"
elif command -v huggingface-cli >/dev/null 2>&1; then
  HF_BIN="huggingface-cli"
else
  echo "==> installing huggingface_hub (no hf CLI found)"
  # Ubuntu 24.04 and Debian 12+ mark the system Python "externally managed"
  # (PEP 668), so a bare `pip install` aborts with
  # "error: externally-managed-environment" — and under `set -e` that killed
  # this script before it downloaded a byte. The DLAMI these GPU profiles boot
  # on is Ubuntu 24.04, so this is the normal path, not an edge case.
  #
  # Prefer an isolated venv: it needs no --break-system-packages override and
  # cannot disturb the system Python that the DLAMI's NVIDIA tooling rides on.
  HF_VENV=/opt/km/hf-cli
  if python3 -m venv "$HF_VENV" >/dev/null 2>&1; then
    "$HF_VENV/bin/pip" install --quiet --upgrade "huggingface_hub[cli]"
    if [[ -x "$HF_VENV/bin/hf" ]]; then
      HF_BIN="$HF_VENV/bin/hf"
    else
      HF_BIN="$HF_VENV/bin/huggingface-cli"
    fi
  else
    # No venv module (python3-venv absent on minimal images). Try a user-site
    # install, then the PEP 668 override as a last resort.
    python3 -m pip install --quiet --upgrade --user "huggingface_hub[cli]" 2>/dev/null \
      || python3 -m pip install --quiet --upgrade --break-system-packages "huggingface_hub[cli]"
    export PATH="${PATH}:${HOME}/.local/bin"
    if command -v hf >/dev/null 2>&1; then
      HF_BIN="hf"
    else
      HF_BIN="huggingface-cli"
    fi
  fi
fi

# Fail loudly here rather than 200 lines later with an opaque "command not found".
if ! command -v "$HF_BIN" >/dev/null 2>&1 && [[ ! -x "$HF_BIN" ]]; then
  echo "ERROR: could not install a HuggingFace CLI (tried venv, --user, --break-system-packages)." >&2
  exit 1
fi

DEST="s3://${BUCKET}/models/${SLUG}/"

echo "==> repo     : ${REPO}"
echo "==> revision : ${REVISION}"
echo "==> local    : ${WORKDIR}"
echo "==> target   : ${DEST}"
echo

mkdir -p "$WORKDIR"

# --------------------------------------------------------------------------
# Pass 1: the index and the small files.
# --------------------------------------------------------------------------
# An exclude-list download is not sufficient here. This repo carries TWO
# safetensors shard sets — an orphaned 18-shard set left over from before the
# V2 multimodal rebuild, and the live 28-shard set the index actually
# references. Excluding only *.gguf/mlx/benchmarks pulls BOTH, roughly doubling
# the download and the S3 footprint with ~55G of weights nothing loads, and
# every linked sandbox then pays cross-account egress on the dead half too.
#
# So: fetch the index first, then ask for exactly the shards it names. That is
# also self-maintaining — the next orphaned shard set costs nothing.
echo "==> downloading index + config files"
"$HF_BIN" download "$REPO" \
  --revision "$REVISION" \
  --local-dir "$WORKDIR" \
  --exclude "*.safetensors" \
  --exclude "*.gguf" \
  --exclude "mlx-4bit/*" \
  --exclude "mlx-8bit/*" \
  --exclude "benchmarks/*"

IDX="${WORKDIR}/model.safetensors.index.json"
if [[ ! -s "$IDX" ]]; then
  echo "ERROR: ${IDX} missing after pass 1 — cannot determine which shards to fetch." >&2
  exit 1
fi

# --------------------------------------------------------------------------
# Pass 2: exactly the shards the index references.
# --------------------------------------------------------------------------
# hf download takes positional FILENAMES; passing them turns the command into
# "fetch exactly these", which is precisely what we want here. Note this is the
# same behaviour that silently defeats --exclude (see the flag note below), so
# the two passes must stay separate — never combine filenames with --exclude.
mapfile -t SHARDS < <(python3 -c "import json,sys;print('\n'.join(sorted(set(json.load(open(sys.argv[1]))['weight_map'].values()))))" "$IDX")
if [[ ${#SHARDS[@]} -eq 0 ]]; then
  echo "ERROR: index references no shards — refusing to stage an empty weight set." >&2
  exit 1
fi

echo "==> downloading ${#SHARDS[@]} shard(s) named by the index"
"$HF_BIN" download "$REPO" \
  --revision "$REVISION" \
  --local-dir "$WORKDIR" \
  "${SHARDS[@]}"

# Flag note, kept from the original single-pass form because it still governs
# pass 1: one --exclude PER pattern. huggingface_hub 1.x moved this CLI to
# Typer, where --exclude takes a single TEXT value; trailing bare patterns are
# then parsed as the positional FILENAMES argument instead, which silently
# flips the command into "download exactly these files" and warns
#   "Ignoring `--exclude` since filenames have been explicitly set"
# before fetching 0 files and exiting 0. Repeated flags DO accumulate.

# Verify locally before uploading — a partial upload turns into a crash-looping
# vllm.service on every sandbox that syncs it, so catch it here instead.
IDX="${WORKDIR}/model.safetensors.index.json"
if [[ ! -s "$IDX" ]]; then
  echo "ERROR: ${IDX} missing after download — nothing to stage." >&2
  exit 1
fi

echo "==> verifying shard set against model.safetensors.index.json"
REQUIRED=$(python3 -c "import json,sys;print('\n'.join(sorted(set(json.load(open(sys.argv[1]))['weight_map'].values()))))" "$IDX")
missing=0
for shard in $REQUIRED; do
  if [[ ! -s "${WORKDIR}/${shard}" ]]; then
    echo "ERROR: missing shard ${shard}" >&2
    missing=1
  fi
done
for f in config.json generation_config.json tokenizer.json tokenizer_config.json; do
  if [[ ! -s "${WORKDIR}/${f}" ]]; then
    echo "ERROR: missing ${f}" >&2
    missing=1
  fi
done
if [[ $missing -ne 0 ]]; then
  echo "ERROR: local download incomplete; refusing to stage a broken model dir." >&2
  exit 1
fi

echo "==> uploading to ${DEST}"
aws s3 sync "$WORKDIR" "$DEST" --only-show-errors

echo
echo "staged: ${DEST}"
echo "shards: $(echo "$REQUIRED" | wc -l | tr -d ' ')"
echo "size  : $(du -sh "$WORKDIR" | cut -f1)"

if [[ $KEEP -eq 0 ]]; then
  echo "==> removing local copy (${WORKDIR}); pass --keep to retain it"
  find "$WORKDIR" -mindepth 1 -delete
  rmdir "$WORKDIR" 2>/dev/null || true
fi

echo
echo "Next: km create <the profile whose MODEL_SLUG matches --slug ${SLUG}>"
echo "        BF16        profiles/gpu-qwen38-oblit-12x.yaml      (4x L40S, g6e.12xlarge)"
echo "        BF16        profiles/gpu-qwen38-oblit-12x-l4.yaml   (4x L4,   g6.12xlarge)"
echo "        FP8         profiles/gpu-qwen38-oblit-fp8-4x.yaml   (1x L40S, g6e.4xlarge)"
echo
echo "      If vllm.service is already crash-looping on this box waiting for these"
echo "      weights, it heals itself on the next Restart=on-failure tick (~30s)."
