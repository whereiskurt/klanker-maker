---
name: slack
description: Post messages from inside a sandbox to its per-sandbox Slack channel using km-slack — for end-of-task status, progress notes, operator pings, threaded transcript replies, and Block-Kit-rendered output
---

# Sandbox → Slack Notifications

This skill posts a message from inside a sandbox to its per-sandbox Slack channel (`#sb-{id}` or a shared override). It uses `/opt/km/bin/km-slack`, which signs the message with the sandbox's Ed25519 key and POSTs it to the `km-slack-bridge` Lambda. The bridge does the actual Slack API call.

**Primary use case:** an interactive agent finishing a task wants to ping the operator's Slack — "started the build", "tests green", "blocked, see thread".

**Prerequisites:** Run `klanker:sandbox` first to detect environment.

## Cross-references

- `klanker:sandbox` — environment + tooling detection (run first)
- `klanker:email` — alternative channel; use email for sandbox-to-sandbox, Slack for operator-facing pings
- `klanker:operator` — to *request* an operator action, email the operator inbox instead of posting to Slack
- `klanker:init` — operator-side bootstrap of `km slack init`, scopes, and bridge deploy

## Step 1: Confirm Slack Is Wired Up

Slack posting is **opt-in per profile**. If the sandbox wasn't created with `notifySlackEnabled: true`, the env vars below will be empty and posting will fail.

```bash
echo "KM_NOTIFY_SLACK_ENABLED=$KM_NOTIFY_SLACK_ENABLED"  # must be 1
echo "KM_SLACK_CHANNEL_ID=$KM_SLACK_CHANNEL_ID"          # must be C... (Slack channel ID)
echo "KM_SLACK_BRIDGE_URL=$KM_SLACK_BRIDGE_URL"          # must be Lambda Function URL
test -x /opt/km/bin/km-slack && echo "km-slack: OK" || echo "km-slack: MISSING"
```

If any of these are empty/missing, **stop and tell the user**. Slack delivery requires:
- The profile sets `spec.cli.notifySlackEnabled: true` (and usually `notifySlackPerSandbox: true`)
- The sandbox was created **after** `km init --sidecars` shipped the km-slack binary
- The signing key at `/sandbox/$KM_SANDBOX_ID/signing-key` is reachable (same key as `km-send`)

The signing-key check from `klanker:sandbox` also covers Slack — km-slack and km-send share that one Ed25519 key.

## Step 2: Post a Simple Message

The body must come from a file (stdin is rejected — same OpenSSL 3.5+ constraint as km-send):

```bash
cat > /tmp/slack-msg.txt << 'EOF'
✅ Build green. Ready for review.
EOF

/opt/km/bin/km-slack post \
  --channel "$KM_SLACK_CHANNEL_ID" \
  --body /tmp/slack-msg.txt
```

On success the binary prints `km-slack: posted ts=<slack-ts>` to stderr and exits 0. The `ts` is the Slack message timestamp — keep it if you want to reply in-thread later.

### Capture the parent ts for threading

```bash
PARENT_TS=$(/opt/km/bin/km-slack post \
  --channel "$KM_SLACK_CHANNEL_ID" \
  --body /tmp/slack-msg.txt 2>&1 | sed -n 's/.*posted ts=\(.*\)/\1/p')
echo "Parent ts: $PARENT_TS"
```

## Step 3: Optional Subject (bold header)

`--subject` renders as a bold header line above the body. Useful for at-a-glance scanning, but **omit it for clean threaded replies** — repeated headers are noisy:

```bash
/opt/km/bin/km-slack post \
  --channel "$KM_SLACK_CHANNEL_ID" \
  --subject "Build complete" \
  --body /tmp/slack-msg.txt
```

## Step 4: Reply in Thread

Pass `--thread <parent-ts>` to keep follow-ups under one thread:

```bash
/opt/km/bin/km-slack post \
  --channel "$KM_SLACK_CHANNEL_ID" \
  --thread "$PARENT_TS" \
  --body /tmp/slack-followup.txt
```

The interactive agent's own messages **arrive in their own thread automatically** when transcript streaming is on (`KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=1`) — in that case you usually want `--thread "$KM_SLACK_THREAD_TS"` so your post lands inside the conversation thread instead of cluttering the channel root:

```bash
THREAD="${KM_SLACK_THREAD_TS:-}"
THREAD_FLAG=""
[ -n "$THREAD" ] && THREAD_FLAG="--thread $THREAD"
/opt/km/bin/km-slack post --channel "$KM_SLACK_CHANNEL_ID" $THREAD_FLAG --body /tmp/slack-msg.txt
```

## Step 5: Choose a Render Mode

`km-slack post --render plain|mrkdwn|blocks`.

| Mode | Output | When to use |
|---|---|---|
| `plain` | Literal text, markdown not interpreted | Logs, traces, anything where preserving raw characters matters |
| `mrkdwn` | CommonMark → Slack mrkdwn (bold, italic, links, code preserved byte-for-byte) | Short status posts with light formatting |
| `blocks` | Block Kit (header / section / context / divider), auto-fallback to mrkdwn at the 50-block cap | Long structured output, transcript-style content, anything with multiple headings |

Default for no-flag callers is `plain` (preserves the behavior of legacy callers).

The Phase-68 streaming hook (`_km_stream_drain`) and the inbound poller reply both pass `--render "${KM_SLACK_RENDER:-blocks}"`, so new sandboxes render as Block Kit by default. To override per-sandbox (operator-side safety valve):

```bash
echo 'KM_SLACK_RENDER=plain' | sudo tee -a /etc/km/notify.env
# next outbound post on this sandbox → falls back to literal markdown
```

**Tables render aligned.** In `mrkdwn` and `blocks` modes, a Markdown pipe table is reflowed into a column-aligned monospace grid inside a ``` fence — cells are padded to the widest value per column and the raw `|---|` separator becomes a width-matched rule — so it reads as a real table in Slack instead of ragged literal pipes. No action needed; it is automatic.

**Claude is told it is posting to Slack.** The Slack-inbound poller passes `--append-system-prompt` to `claude -p`, instructing it to fence code/paths/logs/tabular data in ``` and prefer bullet lists over wide tables. This hint is Slack-only — the GitHub and HackerOne pollers (native-markdown surfaces) do not get it. Existing sandboxes pick it up only after `km destroy && km create`.

## End-of-Task Pattern

Use this when finishing an interactive task and pinging the operator:

```bash
# 1. Compose body — keep it short, scannable, with explicit status emoji
cat > /tmp/slack-done.txt << 'EOF'
✅ Done: rebased onto main, tests pass.
Branch: feat/slack-skill
Files changed: 3
Next: opening PR.
EOF

# 2. Post — thread under the active conversation if transcript streaming is on
THREAD_FLAG=""
[ -n "$KM_SLACK_THREAD_TS" ] && THREAD_FLAG="--thread $KM_SLACK_THREAD_TS"

/opt/km/bin/km-slack post \
  --channel "$KM_SLACK_CHANNEL_ID" \
  $THREAD_FLAG \
  --body /tmp/slack-done.txt
```

### Status emoji conventions

Pick one leading glyph so the operator can scan a busy channel:

| Glyph | Meaning |
|---|---|
| ✅ | Task complete, no action needed |
| ⚠️ | Complete with caveats — review when convenient |
| ❌ | Failed / blocked — needs attention |
| 🔄 | In progress — heartbeat / midway update |
| ❓ | Question for the operator — waiting on input |

## Transcript Streaming (read-only awareness)

When the profile sets `notifySlackTranscriptEnabled: true` (and `notifySlackEnabled` + `notifySlackPerSandbox`), every Claude turn auto-streams to the sandbox's Slack channel:

- Per-turn assistant text + tool one-liners post into a thread under the channel root
- The final transcript is uploaded as a gzipped JSONL file at Stop (unless the channel is Slack-Connect — see below)
- `KM_SLACK_THREAD_TS` is set in the agent's environment so user-initiated posts can join the same thread

Per-sandbox channels are externally shared (`is_ext_shared: true`) via Slack Connect — Slack's `files.completeUploadExternal` returns silent `internal_error` for these. Per-turn chat lines + auto-thread + DDB record-mapping still work; only the `.jsonl.gz` attachment is missing. The operator pulls it from S3 instead:

```bash
aws s3 ls s3://${KM_ARTIFACTS_BUCKET}/transcripts/<sandbox-id>/
```

## Inbound Slack Messages (bidirectional chat)

When the profile sets `notifySlackInboundEnabled: true`, operator messages in `#sb-{id}` channels become Claude turns inside the sandbox. The pipeline:

1. Slack `message` event → bridge Lambda (HMAC-verified)
2. Bridge ACKs with 👀 reaction, enqueues to per-sandbox SQS FIFO
3. Sandbox-side `km-slack-inbound-poller.service` dequeues and starts `claude -p`
4. The poller sets `KM_SLACK_THREAD_TS` in Claude's env so the reply threads correctly
5. Claude's Stop hook posts the answer back to Slack via `km-slack post --thread "$KM_SLACK_THREAD_TS"`

### File attachments in inbound messages

Per-sandbox channels accept `file_share` uploads (images, PDFs, etc.):

- Bridge Lambda downloads with bot token, stages to S3 under `slack-inbound/<sandbox-id>/<thread_ts>/`
- Sandbox poller mirrors to `/workspace/.km-slack/attachments/<thread_ts>/`
- A natural-language wrapper prepended to the prompt lists absolute paths + MIME types
- Caps: 25 files/msg, 100 MB/file. Over-cap → thread-reply warning to the user

Inside Claude's prompt, attachment files are available at `/workspace/.km-slack/attachments/<thread_ts>/`. Reference them by absolute path.

### Returning the resume command when asked

When the operator asks over Slack for the session ID, the resume command, or how to continue this conversation from a shell, reply with a one-line snippet they can paste into `km shell <sandbox-id>`:

```
cd / && claude --resume <session-id>
```

The session ID lives in the `${KM_RESOURCE_PREFIX:-km}-slack-threads` DynamoDB table, keyed by `(channel_id, thread_ts)`. Both env vars are already exported into the running Claude:

```bash
aws dynamodb get-item \
  --table-name "${KM_SLACK_THREADS_TABLE:-${KM_RESOURCE_PREFIX:-km}-slack-threads}" \
  --key "{\"channel_id\":{\"S\":\"$KM_SLACK_CHANNEL_ID\"},\"thread_ts\":{\"S\":\"$KM_SLACK_THREAD_TS\"}}" \
  --region "${AWS_REGION:-$AWS_DEFAULT_REGION}" \
  --query 'Item.claude_session_id.S' --output text
```

On the very first turn of a thread the row isn't written yet (the poller stores `claude_session_id` only after `claude -p` exits). If the lookup returns empty, tell the user to ask again after this reply lands.

## Limits

| Limit | Value | Notes |
|---|---|---|
| Body size | 40 KB | Enforced client-side AND at the bridge. Larger bodies are rejected — split into multiple posts or upload as a file. |
| Stdin body | not supported | Always write to a file and pass `--body /path/to/file` |
| Channel format | `C...` | Must be a Slack channel ID, not a name. `$KM_SLACK_CHANNEL_ID` is already in this format. |
| Per-message rate | governed by Slack `chat.postMessage` tier-3 limits (~50/min) | Fan-out posts should pace themselves. |
| Inbound attachments | 25 files/msg, 100 MB/file | Bridge caps; over-limit messages still enqueue but with a warning attached. |

## Error Handling

| Symptom | Cause | Action |
|---|---|---|
| `KM_SANDBOX_ID env var not set` | Not running inside a provisioned sandbox | Run `klanker:sandbox` first to confirm environment |
| `KM_SLACK_BRIDGE_URL env var not set` | Profile didn't enable Slack, or sandbox predates Slack support | Recreate sandbox with `notifySlackEnabled: true`, or post via email instead (`klanker:email`) |
| `AWS_REGION (or AWS_DEFAULT_REGION) not set` | Stripped env in a systemd unit or minimal shell | Re-source `/etc/profile.d/km-notify-env.sh` or set `AWS_DEFAULT_REGION` from IMDS |
| `load signing key: ssm GetParameter ... AccessDenied` | IAM role lost SSM read, or wrong `KM_RESOURCE_PREFIX` | Verify the sandbox role still has `ssm:GetParameter` on `/{prefix}/sandbox/{id}/signing-key` |
| `bridge returned not-ok: channel_not_found` | Channel ID stale (channel archived or wrong ID) | Verify `$KM_SLACK_CHANNEL_ID` matches a live channel; per-sandbox channels archive at `km destroy` if `slackArchiveOnDestroy: true` |
| `bridge returned not-ok: not_in_channel` | Bot was removed from the channel | Operator must `/invite @<bot>` in the channel |
| `body file ... exceeds 40960 bytes` | 40 KB cap hit | Trim body or split into multiple posts |
| Exit code 0 but message not visible | Likely a Slack Connect external-share quirk on per-sandbox channels | Check `km otel` for the post event; the message may still be delivered, just rendered differently for external participants |
| Final transcript `.jsonl.gz` upload silently fails | Per-sandbox channels are Slack-Connect-shared; `files.completeUploadExternal` rejects external-shared channels | Pull transcript from S3: `aws s3 ls s3://${KM_ARTIFACTS_BUCKET}/transcripts/<sandbox-id>/` |

## When NOT to Use This Skill

- **Operator action requests** — use `klanker:operator` (email-based natural-language interpreter). Slack posts are one-way notifications; the operator inbox is bidirectional and triggers `km` commands.
- **Sandbox-to-sandbox messages** — use `klanker:email`. Slack channels are operator-facing; other sandboxes don't read them.
- **Long output dumps** — paste the first 40 KB and link to S3 (`s3://$KM_ARTIFACTS_BUCKET/...`) for the rest. Slack truncates and the bridge will reject anyway.
