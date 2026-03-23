#!/usr/bin/env bash
# Generate dark/light GIF pair from klankerdome.mp4 for GitHub README.
# Dark: black background blends with GitHub dark mode.
# Light: black areas replaced with #f6f8fa (GitHub light mode background).

set -euo pipefail
cd "$(dirname "$0")"

SRC="klankerdome.mp4"
FPS=12
WIDTH=480
DITHER="sierra2_4a"
PALETTE="/tmp/km-palette.png"

echo "==> Generating dark mode GIF..."
ffmpeg -y -i "$SRC" \
  -vf "fps=${FPS},scale=${WIDTH}:-1:flags=lanczos,palettegen=stats_mode=diff" \
  "$PALETTE" 2>/dev/null

ffmpeg -y -i "$SRC" -i "$PALETTE" \
  -lavfi "fps=${FPS},scale=${WIDTH}:-1:flags=lanczos [x]; [x][1:v] paletteuse=dither=${DITHER}" \
  klankerdome-dark.gif 2>/dev/null

echo "    klankerdome-dark.gif  $(du -h klankerdome-dark.gif | cut -f1)"

echo "==> Generating light mode GIF (black → #f6f8fa)..."
ffmpeg -y -i "$SRC" \
  -vf "fps=${FPS},scale=${WIDTH}:-1:flags=lanczos,colorkey=color=black:similarity=0.15:blend=0.1,format=yuva420p,pad=iw:ih:0:0:color=#f6f8fa,palettegen=stats_mode=diff" \
  "$PALETTE" 2>/dev/null

ffmpeg -y -i "$SRC" -i "$PALETTE" \
  -lavfi "fps=${FPS},scale=${WIDTH}:-1:flags=lanczos,colorkey=color=black:similarity=0.15:blend=0.1,format=yuva420p,pad=iw:ih:0:0:color=#f6f8fa [x]; [x][1:v] paletteuse=dither=${DITHER}" \
  klankerdome-light.gif 2>/dev/null

echo "    klankerdome-light.gif  $(du -h klankerdome-light.gif | cut -f1)"

echo ""
echo "Done. README uses <picture> element for dark/light switching."
ls -lhS klankerdome-{dark,light}.gif
