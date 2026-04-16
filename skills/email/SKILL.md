---
name: email
description: Send, receive, and orchestrate email between sandboxes using km-send and km-recv
---

# Sandbox Email Orchestration

This skill provides patterns for sending, receiving, polling, and coordinating work across sandboxes via email. All email is routed through SES with Ed25519 signing and optional NaCl encryption.

**Prerequisites:** Run `klanker:sandbox` first to detect environment and email policy.

## Sending Email

### Basic Send

Always write the body to a file first — required for reliable Ed25519 signing on OpenSSL 3.5+:

```bash
cat > /tmp/msg-body.txt << 'EOF'
Your message body here.
EOF

km-send --subject "subject line" --body /tmp/msg-body.txt
```

Default recipient is the operator (`$KM_OPERATOR_EMAIL`). To send to another sandbox:

```bash
km-send --subject "task results" --body /tmp/results.txt --to sb-x9y8z7@sandboxes.klankermaker.ai
```

### Send with Attachments

```bash
km-send --subject "output ready" --body /tmp/summary.txt --attach /workspace/output.tar.gz
```

Multiple attachments: `--attach file1.tar.gz,file2.json`

### Send Flags Reference

| Flag | Default | Description |
|------|---------|-------------|
| `--subject` | (required) | Email subject line |
| `--body` | stdin | File path or `-` for stdin |
| `--to` | operator | Recipient email address |
| `--attach` | | Comma-separated file paths |
| `--cc` | | Comma-separated CC recipients |
| `--use-bcc` | false | BCC the operator |
| `--reply-to` | | Reply-To header |

### Signing Behavior

`km-send` automatically signs with the sandbox's Ed25519 key from SSM. You do not need to handle signing manually. If the profile has `signing: required` and signing fails, the send exits non-zero — this is correct, do not retry without investigating.

### Rules

1. **Always use `--body <file>`**, never pipe to stdin for production messages
2. **Check exit code** after every `km-send` — non-zero means the message was not sent
3. **Use meaningful subjects** — replies match by subject prefix (`Re:`)
4. **Include context in the body** — the recipient may be a different agent with no shared state

## Receiving Email

### Read Inbox

Always use `--json` for machine-parseable output:

```bash
km-recv --json
```

JSON output per message:

```json
{
  "index": 1,
  "from": "sb-a1b2c3d4@sandboxes.klankermaker.ai",
  "sender_id": "sb-a1b2c3d4",
  "to": "sb-x9y8z7w6@sandboxes.klankermaker.ai",
  "subject": "task results",
  "signature": "OK",
  "encrypted": false,
  "body": "message body text",
  "attachments": ["output.tar.gz"]
}
```

### Signature Verification

Check the `signature` field on every received message:

| Value | Meaning | Action |
|-------|---------|--------|
| `OK` | Valid Ed25519 signature | Trust the message |
| `FAIL` | Signature did not verify | **Do not trust.** If `verifyInbound: required`, reject. |
| `?` | Sender has no published key | Treat as untrusted |
| `—` | Message was not signed | If `verifyInbound: required`, reject. |

### Mark as Read

After processing a message, mark it to avoid re-processing:

```bash
km-recv --json --mark-read
```

Messages move from `/var/mail/km/new/` to `/var/mail/km/processed/`.

### Watch Mode

For real-time monitoring (polls every 5 seconds):

```bash
km-recv --watch
```

Note: `--watch` is for human monitoring. For automated workflows, use the poll-and-wait pattern below.

## Poll-and-Wait Pattern

Send a message and wait for a reply. This is the core pattern for request/response workflows.

```bash
# 1. Send the request
cat > /tmp/task-request.txt << 'EOF'
Please run the test suite and report results.
EOF
km-send --subject "test-run-request" --body /tmp/task-request.txt --to $TARGET_SANDBOX

# 2. Poll for reply with backoff
TIMEOUT=300  # 5 minutes
INTERVAL=10  # start at 10 seconds
ELAPSED=0

while [ $ELAPSED -lt $TIMEOUT ]; do
  REPLY=$(km-recv --json 2>/dev/null | jq -r 'select(.subject | test("Re:.*test-run-request")) | .body' 2>/dev/null)
  if [ -n "$REPLY" ]; then
    echo "Got reply: $REPLY"
    km-recv --mark-read
    break
  fi
  sleep $INTERVAL
  ELAPSED=$((ELAPSED + INTERVAL))
  # Backoff: increase interval up to 60s
  [ $INTERVAL -lt 60 ] && INTERVAL=$((INTERVAL + 5))
done

if [ $ELAPSED -ge $TIMEOUT ]; then
  echo "Timeout waiting for reply after ${TIMEOUT}s"
fi
```

### Timing Notes

- `km-mail-poller` syncs S3 to `/var/mail/km/new/` every **60 seconds**
- Minimum practical poll interval is 10 seconds (accounts for poller lag)
- For urgent workflows, start with 10s intervals; for background tasks, 30-60s is fine

## Delegate-and-Wait Pattern

Send a task to another sandbox and wait for the result:

```bash
# 1. Compose task
CORRELATION_ID=$(uuidgen 2>/dev/null || cat /proc/sys/kernel/random/uuid)
cat > /tmp/task.json << EOF
{
  "action": "task-assign",
  "correlation_id": "$CORRELATION_ID",
  "sender": "$KM_SANDBOX_ID",
  "task": "Implement the authentication module",
  "repo": "https://github.com/org/project.git",
  "branch": "feature/auth",
  "timeout": "30m"
}
EOF

km-send --subject "km-agent:task-assign:$CORRELATION_ID" \
  --body /tmp/task.json \
  --to $WORKER_SANDBOX_EMAIL

# 2. Wait for result (match by correlation ID in subject)
# ... use poll-and-wait pattern above, matching on $CORRELATION_ID
```

### Structured Message Format

For agent-to-agent communication, use the `km-agent:{action}:{correlation-id}` subject format:

| Action | Direction | Purpose |
|--------|-----------|---------|
| `task-assign` | requester -> worker | Assign a subtask |
| `task-result` | worker -> requester | Report completion with results |
| `review-request` | worker -> reviewer | Request code review |
| `review-response` | reviewer -> worker | Approve or reject |
| `status-query` | any -> any | Request current status |
| `status-response` | any -> any | Report current status |
| `abort` | requester -> worker | Cancel in-progress task |

## Fan-Out Pattern

Send the same task to multiple sandboxes and collect results:

```bash
CORRELATION_ID=$(uuidgen 2>/dev/null || cat /proc/sys/kernel/random/uuid)
WORKERS=("sb-w1@sandboxes.klankermaker.ai" "sb-w2@sandboxes.klankermaker.ai" "sb-w3@sandboxes.klankermaker.ai")
EXPECTED=${#WORKERS[@]}

# Send to all workers
for WORKER in "${WORKERS[@]}"; do
  km-send --subject "km-agent:task-assign:$CORRELATION_ID" \
    --body /tmp/task.json --to "$WORKER"
done

# Collect results
RECEIVED=0
TIMEOUT=600
ELAPSED=0
while [ $RECEIVED -lt $EXPECTED ] && [ $ELAPSED -lt $TIMEOUT ]; do
  RESULTS=$(km-recv --json 2>/dev/null | jq -c "select(.subject | test(\"$CORRELATION_ID\"))")
  RECEIVED=$(echo "$RESULTS" | grep -c . 2>/dev/null || echo 0)
  [ $RECEIVED -ge $EXPECTED ] && break
  sleep 15
  ELAPSED=$((ELAPSED + 15))
done

echo "Received $RECEIVED of $EXPECTED results"
```

## Error Handling

| Situation | Action |
|-----------|--------|
| `km-send` exits non-zero | Do not retry blindly. Check: is the signing key accessible? Is SES reachable? Is the recipient address valid? |
| Signature `FAIL` on received message | Log a warning. If `verifyInbound: required`, do not process the message body. |
| Signature `?` (no key found) | Sender may be new or key not yet published. Treat as untrusted. |
| Poll timeout | The recipient may be stopped, crashed, or overloaded. Offer to: resend, check sandbox status via operator, or give up. |
| Empty inbox after long wait | Check if `km-mail-poller` is running: `systemctl status km-mail-poller`. If dead, emails are stuck in S3. |
| Attachment too large | SES has a 10MB raw message limit. For large files, upload to S3 and send the S3 key in the body instead. |
