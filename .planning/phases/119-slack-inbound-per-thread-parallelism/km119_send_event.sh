#!/usr/bin/env bash
# Phase 119 E2E helper — sends a signed synthetic Slack event_callback to the bridge.
# Usage: ./km119_send_event.sh <channel_id> <thread_ts> <event_id> <prompt>
# Example: ./km119_send_event.sh C0BCQML7S6B 1782358000.123456 ev-uat-001 "count slowly to 30 then say done"
set -euo pipefail

CHANNEL_ID="${1:?Usage: $0 <channel_id> <thread_ts> <event_id> <prompt>}"
THREAD_TS="${2:?}"
EVENT_ID="${3:?}"
PROMPT="${4:?}"

BRIDGE_URL="https://6ov5pfv6ml3fjo66liqljsyazi0hsaad.lambda-url.us-east-1.on.aws"
BOT_USER_ID="U0B403U3JKC"
USER_ID="U0HUMANTEST01"

# Fetch signing secret
SECRET=$(AWS_PROFILE=klanker-application AWS_DEFAULT_REGION=us-east-1 \
  aws ssm get-parameter --name /km/slack/signing-secret --with-decryption \
  --query Parameter.Value --output text)

# Timestamp for signature (must be within 300s)
TS=$(date +%s)
EVENT_TS="${THREAD_TS}"

# Build event body — bot_id ABSENT, no subtype, user=U0HUMANTEST01 bypasses bot-loop filter
BODY=$(cat <<ENDBODY
{"token":"synthetic","team_id":"T0TEST","api_app_id":"A0TEST","event":{"type":"message","channel":"${CHANNEL_ID}","user":"${USER_ID}","text":"<@${BOT_USER_ID}> ${PROMPT}","ts":"${EVENT_TS}","thread_ts":"${THREAD_TS}"},"type":"event_callback","event_id":"${EVENT_ID}","event_time":${TS},"authorizations":[{"enterprise_id":null,"team_id":"T0TEST","user_id":"${BOT_USER_ID}","is_bot":true,"is_enterprise_install":false}]}
ENDBODY
)

# Remove trailing newline from BODY for proper signing
BODY=$(echo -n "${BODY}" | tr -d '\n')

# Sign the request
BASESTRING="v0:${TS}:${BODY}"
SIG="v0=$(echo -n "${BASESTRING}" | openssl dgst -sha256 -hmac "${SECRET}" | sed 's/^.* //')"

echo "[km119] Sending event_id=${EVENT_ID} thread_ts=${THREAD_TS} channel=${CHANNEL_ID}"
echo "[km119] Signature: ${SIG}"

HTTP_RESP=$(curl -s -o /tmp/km119_resp.json -w "%{http_code}" \
  -X POST \
  -H "Content-Type: application/json" \
  -H "x-slack-request-timestamp: ${TS}" \
  -H "x-slack-signature: ${SIG}" \
  --data-raw "${BODY}" \
  "${BRIDGE_URL}/events")

echo "[km119] HTTP status: ${HTTP_RESP}"
cat /tmp/km119_resp.json
echo ""
