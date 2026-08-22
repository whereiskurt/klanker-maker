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
BUCKET="${KM_ARTIFACTS_BUCKET:-}"
WORKDIR=""
KEEP=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bucket)  BUCKET="$2"; shift 2 ;;
    --slug)    SLUG="$2"; shift 2 ;;
    --repo)    REPO="$2"; shift 2 ;;
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
  python3 -m pip install --quiet --upgrade "huggingface_hub[cli]"
  if command -v hf >/dev/null 2>&1; then
    HF_BIN="hf"
  else
    HF_BIN="huggingface-cli"
  fi
fi

DEST="s3://${BUCKET}/models/${SLUG}/"

echo "==> repo   : ${REPO}"
echo "==> local  : ${WORKDIR}"
echo "==> target : ${DEST}"
echo

mkdir -p "$WORKDIR"

echo "==> downloading BF16 safetensors (GGUF and MLX trees excluded)"
"$HF_BIN" download "$REPO" \
  --local-dir "$WORKDIR" \
  --exclude "*.gguf" "mlx-4bit/*" "mlx-8bit/*"

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
echo "Next: km create profiles/gpu-qwen38-oblit-12x.yaml"
echo "      (or profiles/gpu-qwen38-oblit-12x-l4.yaml on the L4 fallback shape)"
