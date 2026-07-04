#!/usr/bin/env bash
# Wait for ngrok to establish the tunnel, then persist + log the public URL so the
# operator can grab it with:  km shell <id> -- cat /opt/km-voice/PUBLIC_URL.txt
set -u
URL=""
for _ in $(seq 1 90); do
  URL="$(curl -fsS http://127.0.0.1:4040/api/tunnels 2>/dev/null \
         | jq -r '.tunnels[]?.public_url' 2>/dev/null \
         | grep -m1 '^https' || true)"
  [ -n "$URL" ] && break
  sleep 2
done
if [ -n "$URL" ]; then
  printf '%s\n' "$URL" > /opt/km-voice/PUBLIC_URL.txt
  logger -t km-voice "realtime voice demo public URL: $URL"
  echo "km-voice public URL: $URL"
else
  logger -t km-voice "ngrok tunnel URL not found after timeout"
fi
