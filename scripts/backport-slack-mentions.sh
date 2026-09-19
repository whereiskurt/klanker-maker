#!/usr/bin/env bash
# backport-slack-mentions.sh — give an ALREADY-RUNNING sandbox the v0.8.22
# Slack @-mention fixes without `km destroy && km create`.
#
#   scripts/backport-slack-mentions.sh <sandbox-id> [--restart]
#
# Two things reach a sandbox only at boot, and this puts both on a live box:
#
#   1. /opt/km/bin/km-slack — the sidecar with the renderer fix (<@U…> no
#      longer HTML-escaped) and `--mention`. Re-fetched from the install's
#      s3://<artifacts>/sidecars/km-slack, which `km init --dry-run=false`
#      on v0.8.22 has already refreshed. PREREQUISITE: run that deploy first,
#      or this copies the OLD binary over itself.
#   2. /opt/km/bin/km-slack-inbound-poller — the bash script userdata wrote,
#      patched in place with the `[Slack] From: <@U…>` preamble and the
#      KM_SLACK_SENDER_ID export in all three dispatch sites. Purely additive;
#      idempotent (marker-checked); `bash -n` before the swap; original kept
#      as .bak. The inserted text is byte-identical to what v0.8.22 renders.
#
# The patched poller only takes effect after `systemctl restart
# km-slack-inbound-poller`, which kills any IN-FLIGHT turn (the message
# returns to the queue after the visibility timeout — nothing is lost, but
# that turn's reply will not post). So the restart is opt-in: pass --restart
# when the box is quiet, or restart it yourself later.
#
# Runs over SSM as root (AWS-RunShellScript runs dash, so the on-box half is
# shipped base64-encoded and executed under bash). Needs the same AWS creds
# `km shell` needs. Resolves the instance by the km:sandbox-id tag, which km
# writes regardless of resource_prefix.
set -euo pipefail

usage() { echo "usage: $0 <sandbox-id> [--restart]" >&2; exit 2; }

# ---------------------------------------------------------------------------
# On-box half. Everything below the marker runs as root on the sandbox.
# ---------------------------------------------------------------------------
onbox() {
  set -euo pipefail
  RESTART="${1:-0}"
  if [ -z "${KM_ARTIFACTS_BUCKET:-}" ] && [ -r /etc/profile.d/km-identity.sh ]; then
    # shellcheck disable=SC1091
    . /etc/profile.d/km-identity.sh
  fi
  [ -n "${KM_ARTIFACTS_BUCKET:-}" ] || { echo "KM_ARTIFACTS_BUCKET unset and km-identity.sh unreadable" >&2; exit 1; }

  echo "== 1/2 km-slack sidecar"
  aws s3 cp "s3://$KM_ARTIFACTS_BUCKET/sidecars/km-slack" /opt/km/bin/km-slack.new --only-show-errors
  chmod 0755 /opt/km/bin/km-slack.new
  if /opt/km/bin/km-slack.new post -h 2>&1 | grep -q -- '-mention'; then
    mv /opt/km/bin/km-slack.new /opt/km/bin/km-slack
    echo "   km-slack: replaced (has --mention)"
  else
    rm -f /opt/km/bin/km-slack.new
    echo "   km-slack: the object in S3 has no --mention — deploy v0.8.22 (km init --dry-run=false) first" >&2
    exit 1
  fi

  echo "== 2/2 km-slack-inbound-poller"
  P=/opt/km/bin/km-slack-inbound-poller
  if [ ! -f "$P" ]; then
    echo "   no Slack inbound poller on this box (profile has no notification.slack.inbound) — nothing to patch"
  elif grep -q 'km-slack-sender-preamble' "$P"; then
    echo "   poller: already patched"
  else
    python3 - "$P" <<'PY'
import re, sys
p = sys.argv[1]
s = open(p).read()
if "km-slack-sender-preamble" in s:
    sys.exit("poller already carries the sender preamble; refusing to double-patch")
anchor = "      # Phase 70 Plan 70-06: cross-agent switch sequence"
if anchor not in s:
    sys.exit("anchor line not found; poller layout differs from what this backport expects")
block = r'''      # --- km-slack-sender-preamble ---
      # Who sent this turn. Slack resolves a mention ONLY from a literal <@U…>
      # token; the bridge forwards the raw event text, so any <@U…> the human
      # typed is already in TEXT — but the sender's OWN id lives only in the SQS
      # body's .user, and without it "can you @ me back" is unanswerable (an
      # agent that guesses writes "@Kurt", which Slack renders as plain text).
      # Prepend it as a one-line preamble and export it into every dispatched
      # turn (below) so the agent can write <@$SENDER_ID> inline or pass
      # --mention "$KM_SLACK_SENDER_ID" to km-slack post/reply. Self-contained
      # (derives SENDER_ID from BODY here) so the compiler test can run it as-is.
      SENDER_ID=$(echo "$BODY" | jq -r '.user // empty' 2>/dev/null || true)
      if [ -n "$SENDER_ID" ]; then
        HDR_FILE="$(mktemp)"
        printf '[Slack] From: <@%s> (also in $KM_SLACK_SENDER_ID). To notify this person in your reply write <@%s> inline, or pass --mention %s to km-slack post/reply. A bare @name is plain text to Slack — never invent an id; use this one or a <@U…> token from the message.\n\n' "$SENDER_ID" "$SENDER_ID" "$SENDER_ID" > "$HDR_FILE"
        cat "$PROMPT_FILE" >> "$HDR_FILE"
        mv "$HDR_FILE" "$PROMPT_FILE"
        chmod 644 "$PROMPT_FILE"
        chown sandbox:sandbox "$PROMPT_FILE" 2>/dev/null || true
      fi
      # --- end km-slack-sender-preamble ---

'''
s = s.replace(anchor, block + anchor, 1)
n = 0
def add_export(m):
    global n
    n += 1
    return m.group(0) + "\n" + m.group(1) + "export KM_SLACK_SENDER_ID='$SENDER_ID'"
s = re.sub(r"^( *)export KM_SLACK_THREAD_TS='\$THREAD_TS'$", add_export, s, flags=re.M)
if n != 3:
    sys.exit(f"expected 3 inline KM_SLACK_THREAD_TS exports (codex resume, codex first, claude), found {n}")
open(p + ".new", "w").write(s)
PY
    bash -n "$P.new"
    cp -p "$P" "$P.bak"
    chmod 0755 "$P.new"
    mv "$P.new" "$P"
    echo "   poller: patched (original at $P.bak)"
  fi

  if [ -f "$P" ]; then
    if [ "$RESTART" = "1" ]; then
      systemctl restart km-slack-inbound-poller
      echo "   poller: restarted"
    else
      echo "   poller: NOT restarted — the preamble applies after: sudo systemctl restart km-slack-inbound-poller"
    fi
  fi
  echo "== done"
}

# ---------------------------------------------------------------------------
# Operator half.
# ---------------------------------------------------------------------------
if [ "${1:-}" = "--on-box" ]; then
  onbox "${2:-0}"
  exit 0
fi

SB="${1:-}"; [ -n "$SB" ] || usage
RESTART=0
[ "${2:-}" = "--restart" ] && RESTART=1
REGION="${AWS_REGION:-${AWS_DEFAULT_REGION:-us-east-1}}"

IID=$(aws ec2 describe-instances --region "$REGION" \
  --filters "Name=tag:km:sandbox-id,Values=$SB" "Name=instance-state-name,Values=running" \
  --query 'Reservations[].Instances[].InstanceId' --output text)
[ -n "$IID" ] && [ "$IID" != "None" ] || { echo "no running instance tagged km:sandbox-id=$SB in $REGION" >&2; exit 1; }
echo "sandbox $SB → $IID"

# Ship this whole file; run its on-box half under bash (SSM's shell is dash).
B64=$(base64 < "$0" | tr -d '\n')
CMD="echo $B64 | base64 -d > /tmp/km-backport.sh && bash /tmp/km-backport.sh --on-box $RESTART; rc=\$?; rm -f /tmp/km-backport.sh; exit \$rc"
CID=$(aws ssm send-command --region "$REGION" --instance-ids "$IID" \
  --document-name AWS-RunShellScript \
  --parameters "$(jq -cn --arg c "$CMD" '{commands:[$c]}')" \
  --query Command.CommandId --output text)

for _ in $(seq 1 60); do
  sleep 3
  ST=$(aws ssm get-command-invocation --region "$REGION" --command-id "$CID" --instance-id "$IID" --query Status --output text 2>/dev/null || echo Pending)
  case "$ST" in Pending|InProgress|Delayed) continue ;; esac
  break
done
aws ssm get-command-invocation --region "$REGION" --command-id "$CID" --instance-id "$IID" \
  --query '[StandardOutputContent,StandardErrorContent]' --output text
[ "$ST" = "Success" ] || { echo "SSM command $CID ended $ST" >&2; exit 1; }
