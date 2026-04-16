---
name: operator
description: Request platform actions by emailing the operator inbox with natural language commands
---

# Operator Communication

This skill handles communication between a sandbox and the Klanker Maker platform operator. The operator inbox (`operator@sandboxes.klankermaker.ai`) has a Haiku AI interpreter that understands natural language commands and can execute platform operations.

**Prerequisites:** Run `klanker:sandbox` first to detect environment and email policy.

## How It Works

1. You compose a natural language request and send it to `$KM_OPERATOR_EMAIL`
2. The operator Lambda receives it, validates the KM-AUTH safe phrase, and passes it to Haiku
3. Haiku interprets the command and executes it (or replies asking for confirmation)
4. You receive a reply email with the result

The `KM-AUTH` safe phrase is automatically appended by `km-send` when sending to the operator. You do not need to include it manually.

## Requesting Scheduled Operations

Ask the operator to schedule future actions using natural language. The Haiku interpreter maps these to `km at` commands.

### Schedule an Agent Run

```bash
cat > /tmp/op-request.txt << 'EOF'
Schedule an agent run on my sandbox in 30 minutes with prompt: "run the test suite and email me the results"
EOF
km-send --subject "schedule agent run" --body /tmp/op-request.txt
```

### Schedule on Another Sandbox

```bash
cat > /tmp/op-request.txt << 'EOF'
Schedule an agent run on sb-worker01 at 5pm today with prompt: "pull latest and rebuild" and use --auto-start in case it's paused
EOF
km-send --subject "schedule remote agent" --body /tmp/op-request.txt
```

### Set a Reminder

```bash
cat > /tmp/op-request.txt << 'EOF'
Email me back in 20 minutes with a reminder to check the build results
EOF
km-send --subject "reminder" --body /tmp/op-request.txt
```

### Cancel a Scheduled Operation

```bash
cat > /tmp/op-request.txt << 'EOF'
Cancel the scheduled destroy for sb-worker01
EOF
km-send --subject "cancel schedule" --body /tmp/op-request.txt
```

## Lifecycle Requests

### Extend TTL

```bash
cat > /tmp/op-request.txt << 'EOF'
Extend my TTL by 2 hours
EOF
km-send --subject "extend ttl" --body /tmp/op-request.txt
```

### Add Budget

```bash
cat > /tmp/op-request.txt << 'EOF'
Add $1.00 compute budget and $2.00 AI budget to my sandbox
EOF
km-send --subject "add budget" --body /tmp/op-request.txt
```

### Create a New Sandbox

```bash
cat > /tmp/op-request.txt << 'EOF'
Create a new sandbox from the goose profile with alias worker-3
EOF
km-send --subject "create sandbox" --body /tmp/op-request.txt
```

Note: Create requests require operator confirmation (the Haiku interpreter replies asking for approval before executing).

### Pause/Resume Another Sandbox

```bash
cat > /tmp/op-request.txt << 'EOF'
Pause sb-worker02 — we don't need it until tomorrow
EOF
km-send --subject "pause sandbox" --body /tmp/op-request.txt
```

## Conversation Threading

The Haiku interpreter maintains conversation state via SES Message-ID threading. When following up on a previous request:

- Reply to the operator's response email (the threading happens automatically via subject `Re:` matching)
- Include context: "Yes, go ahead with the create" or "No, cancel that"

The interpreter understands affirmative replies: `yes`, `yup`, `sure`, `ok`, `lgtm`, `looks good`, `go ahead`
And negative replies: `no`, `nope`, `cancel`, `abort`, `nevermind`

## Status Queries

```bash
cat > /tmp/op-request.txt << 'EOF'
What's the status of sb-worker01? Is it still running?
EOF
km-send --subject "status check" --body /tmp/op-request.txt
```

The operator replies with sandbox metadata: status, TTL remaining, budget usage, etc.

## Waiting for Operator Replies

After sending a request, poll for the operator's reply:

```bash
# Send request
km-send --subject "extend ttl" --body /tmp/op-request.txt

# Wait for reply (operator replies are from the operator email address)
TIMEOUT=120
ELAPSED=0
while [ $ELAPSED -lt $TIMEOUT ]; do
  REPLY=$(km-recv --json 2>/dev/null | jq -r "select(.from | test(\"operator\")) | select(.subject | test(\"Re:.*extend ttl\")) | .body" 2>/dev/null)
  if [ -n "$REPLY" ]; then
    echo "Operator reply: $REPLY"
    km-recv --mark-read
    break
  fi
  sleep 10
  ELAPSED=$((ELAPSED + 10))
done
```

Operator replies are typically fast (Lambda execution), but allow 1-2 minutes for the mail poller sync (60s cycle).

## Error Handling

| Situation | Cause | Action |
|-----------|-------|--------|
| No reply after 2 minutes | Operator Lambda may be down, or KM-AUTH failed | Check: is `$KM_SAFE_PHRASE` set? Try resending. |
| Reply says "unauthorized" | KM-AUTH safe phrase is missing or wrong | The safe phrase is auto-appended by `km-send`. If this fails, the sandbox identity may not be provisioned correctly. |
| Reply says "not understood" | Haiku couldn't parse the request | Rephrase more clearly. Use explicit command names: "create", "destroy", "pause", "resume", "extend", "budget-add". |
| Reply asks for confirmation | Destructive or expensive operations need approval | Reply with "yes" or "no" to the same thread. |

## Supported Commands

The Haiku interpreter recognizes these operations:

| Operation | Example Natural Language |
|-----------|------------------------|
| Create sandbox | "Create a sandbox from the learn profile with alias test-1" |
| Destroy sandbox | "Destroy sb-worker01" |
| Pause/Stop | "Pause sb-worker02" |
| Resume | "Resume sb-worker02" |
| Extend TTL | "Extend my TTL by 3 hours" |
| Add budget | "Add $2 compute budget to sb-worker01" |
| Schedule agent run | "Run an agent on sb-worker01 in 1 hour with prompt: ..." |
| Cancel schedule | "Cancel the scheduled destroy for sb-worker01" |
| Status | "What's the status of my sandbox?" |
