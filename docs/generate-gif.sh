#!/usr/bin/env bash
# Generate GIF variations from klankerdome.mp4
# Pick the best one, rename to klankerdome.gif, delete the rest.

set -euo pipefail
cd "$(dirname "$0")"

SRC="klankerdome.mp4"
PALETTE="/tmp/km-palette.png"

generate() {
  local name="$1" fps="$2" width="$3" dither="$4" trim="${5:-}"
  local out="klankerdome-${name}.gif"
  local trim_flags=""
  [[ -n "$trim" ]] && trim_flags="-ss ${trim%,*} -t ${trim#*,}"

  echo "==> $out  (${width}w, ${fps}fps, dither=$dither${trim:+, trim=$trim})"

  # Pass 1: palette
  ffmpeg -y $trim_flags -i "$SRC" \
    -vf "fps=${fps},scale=${width}:-1:flags=lanczos,palettegen=stats_mode=diff" \
    "$PALETTE" 2>/dev/null

  # Pass 2: render
  ffmpeg -y $trim_flags -i "$SRC" -i "$PALETTE" \
    -lavfi "fps=${fps},scale=${width}:-1:flags=lanczos [x]; [x][1:v] paletteuse=dither=${dither}" \
    "$out" 2>/dev/null

  local size
  size=$(du -h "$out" | cut -f1)
  echo "    $out  ${size}"
}

#              name              fps  width  dither           trim(ss,duration)
generate       "480-12-sierra"   12   480    sierra2_4a
generate       "480-15-sierra"   15   480    sierra2_4a
generate       "480-12-floyd"    12   480    floyd_steinberg
generate       "600-12-sierra"   12   600    sierra2_4a
generate       "480-10-sierra"   10   480    sierra2_4a
generate       "480-12-sierra-5s" 12  480    sierra2_4a       "0,5"

echo ""
echo "Done. Compare and pick one:"
ls -lhS klankerdome-*.gif
