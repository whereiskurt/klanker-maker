---
name: slack
description: Post messages from inside a sandbox to its per-sandbox Slack channel using km-slack — for end-of-task status, progress notes, operator pings, @-mentioning people (the sender, someone they named, @here), threaded transcript replies, and Block-Kit-rendered output
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

Slack posting is **opt-in per profile**. If the sandbox wasn't created with `notification.slack.enabled: true`, the env vars below will be empty and posting will fail.

```bash
echo "KM_NOTIFY_SLACK_ENABLED=$KM_NOTIFY_SLACK_ENABLED"  # must be 1
echo "KM_SLACK_CHANNEL_ID=$KM_SLACK_CHANNEL_ID"          # must be C... (Slack channel ID)
echo "KM_SLACK_BRIDGE_URL=$KM_SLACK_BRIDGE_URL"          # must be Lambda Function URL
test -x /opt/km/bin/km-slack && echo "km-slack: OK" || echo "km-slack: MISSING"
```

If any of these are empty/missing, **stop and tell the user**. Slack delivery requires:
- The profile sets `spec.notification.slack.enabled: true` (and usually `notification.slack.perSandbox: true`)
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

On success the binary prints `km-slack: posted ts=<slack-ts>` to **stderr** and exits 0; nothing goes to stdout. The `ts` is the Slack message timestamp — keep it if you want to reply in-thread later. `--channel` and `--body` are required (exit 2 when missing); any failure to load the key, reach the bridge or get `ok` back exits 1.

### Capture the parent ts for threading

`--new-message` forces a top-level post (it drops any `--thread`) and prints `ts=<ts>` to **stdout**, which is the clean way to capture it:

```bash
PARENT_TS=$(/opt/km/bin/km-slack post --new-message \
  --channel "$KM_SLACK_CHANNEL_ID" \
  --body /tmp/slack-msg.txt | sed -n 's/^ts=//p')
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

`KM_SLACK_THREAD_TS` is set **only on a Slack-inbound turn**: the inbound poller exports it per turn together with `KM_SLACK_CHANNEL_ID` (the channel the message came from, which under the default route may not be your `#sb-…` channel) and `KM_SLACK_SENDER_ID`. Transcript streaming does not set it. On such a turn your final reply is **posted to that thread automatically** when the turn ends, so you normally do not post there yourself. A successful `km-slack post --thread`/`reply` into a thread cancels that automatic post (see "Mentioning people" below), so a post into the current turn's thread must carry your complete answer.

The bridge accepts a post into your own channel, or into a thread the bridge routed to you (`--thread` required). A top-level post, or any post into another sandbox's channel, comes back as `403 channel_mismatch`.

## Step 5: Choose a Render Mode

`km-slack post|reply|dm --render plain|mrkdwn|blocks|blocks-rich`.

| Mode | Output | When to use |
|---|---|---|
| `plain` | Literal text, markdown not interpreted | Logs, traces, anything where preserving raw characters matters |
| `mrkdwn` | CommonMark → Slack mrkdwn (bold, italic, links, code preserved byte-for-byte) | Short status posts with light formatting |
| `blocks` | Block Kit (header / section / context / divider), auto-fallback to mrkdwn at the 50-block cap | Long structured output, transcript-style content, anything with multiple headings |
| `blocks-rich` | Tier-3 opt-in (Phase 111): prose → native `markdown` blocks (verbatim GFM, clickable `[label](url)` anchors), GFM pipe tables → dedicated `table` blocks (not monospace grids), H1 → `header`, 🔧 → `context`; falls back to `blocks`, then `mrkdwn`, when the 12K markdown cap or the 50-block cap is hit | Reports with tables, research summaries with native anchor links, structured output where table readability matters. Set `KM_SLACK_AI_FOOTER=true` in `/etc/km/notify.env` to append an AI-disclaimer footer. See `docs/slack-notifications.md` § Phase 111. |

**Precedence: `--render` flag, then `$KM_SLACK_RENDER`, then `plain`.** km-slack itself defaults to `plain`. The automatic final reply and the streaming hook look rich only because they pass `--render "${KM_SLACK_RENDER:-blocks-rich}"` (Phase 112). So **a post you make yourself is literal markdown unless you pass `--render blocks-rich`** (or the box sets `KM_SLACK_RENDER`). An unknown value on `post`/`reply` warns on stderr and uses `plain`. If Slack rejects a block payload with `invalid_blocks`, km-slack re-posts **once** without blocks using the mrkdwn/plain text it already rendered, and logs a warning. A rendered body over 35 KB is cut there with a `_…truncated…_` footer rather than rejected.

To override the rich default per-sandbox (operator-side safety valve):

```bash
echo 'KM_SLACK_RENDER=plain' | sudo tee -a /etc/km/notify.env
# next outbound post on this sandbox → falls back to literal markdown
```

**Tables render aligned.** In `blocks-rich` a pipe table becomes a native `table` block (over 20 columns or 100 rows it falls back to the grid below). In `mrkdwn` and `blocks` modes, a Markdown pipe table is reflowed into a column-aligned monospace grid inside a ``` fence — cells are padded to the widest value per column and the raw `|---|` separator becomes a width-matched rule — so it reads as a real table in Slack instead of ragged literal pipes. No action needed; it is automatic.

**Claude is told it is posting to Slack.** The Slack-inbound poller passes `--append-system-prompt` to `claude -p`, instructing it to fence code/paths/logs/tabular data in ``` and prefer bullet lists over wide tables. This hint is Slack-only — the GitHub and HackerOne pollers (native-markdown surfaces) do not get it. Existing sandboxes pick it up only after `km destroy && km create`.

## End-of-Task Pattern

Use this when finishing an interactive task (`km shell`, `km agent run`) and pinging the operator. On a Slack-inbound turn do not use it: just finish with your answer, which is posted to the thread automatically.

```bash
# 1. Compose body — keep it short, scannable, with explicit status emoji
cat > /tmp/slack-done.txt << 'EOF'
✅ Done: rebased onto main, tests pass.
Branch: feat/slack-skill
Files changed: 3
Next: opening PR.
EOF

# 2. Post top-level to the sandbox channel
/opt/km/bin/km-slack post \
  --channel "$KM_SLACK_CHANNEL_ID" \
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

To show a verdict on an existing message as a reaction instead, use `km-slack react` with the
whole state set in `--exclusive`, so the previous state is removed for you:

```bash
/opt/km/bin/km-slack react --ts "$TS" --emoji white_check_mark \
  --exclusive hourglass_flowing_sand,white_check_mark,rotating_light
```

Retrying is safe (`already_reacted` and `no_reaction` count as success); the name is bare or
`:colon:`-wrapped, lowercase, no spaces. Own channel only.

## Transcript Streaming (read-only awareness)

When the profile sets `notification.slack.transcript.enabled: true` (and `notification.slack.enabled` + `notification.slack.perSandbox`), every Claude turn auto-streams to the sandbox's Slack channel:

- Per-turn assistant text + tool one-liners post into a thread under the channel root
- The final transcript is uploaded as a gzipped JSONL file at Stop (unless the channel is Slack-Connect — see below)
- The stream thread's ts is cached in `/tmp/km-slack-thread.<session_id>`; `KM_SLACK_THREAD_TS` is NOT set by streaming (only by the inbound poller, per turn)

Per-sandbox channels are externally shared (`is_ext_shared: true`) via Slack Connect — Slack's `files.completeUploadExternal` returns silent `internal_error` for these. Per-turn chat lines + auto-thread + DDB record-mapping still work; only the `.jsonl.gz` attachment is missing. The operator pulls it from S3 instead:

```bash
aws s3 ls s3://${KM_ARTIFACTS_BUCKET}/transcripts/<sandbox-id>/
```

## Inbound Slack Messages (bidirectional chat)

When the profile sets `notification.slack.inbound.enabled: true`, operator messages in `#sb-{id}` channels become Claude turns inside the sandbox. The pipeline:

1. Slack `message` event → bridge Lambda (HMAC-verified)
2. Bridge ACKs with 👀 reaction, enqueues to per-sandbox SQS FIFO (grouped by thread)
3. Sandbox-side `km-slack-inbound-poller.service` dequeues and starts `claude -p`
4. The poller exports `KM_SLACK_THREAD_TS`, `KM_SLACK_CHANNEL_ID` (the message's channel) and `KM_SLACK_SENDER_ID` into the turn's env
5. When the turn ends the **poller** posts the agent's final text into that thread with `--render "${KM_SLACK_RENDER:-blocks-rich}"`, unless the agent already posted there itself (the posted marker). The Stop hook's Slack branch is suppressed on these turns, so there is one reply per turn

### Per-thread parallelism (Phase 119)

By default the poller runs **one turn at a time** (serial). Setting
`notification.slack.inbound.maxConcurrentThreads: N` (`*int`, default `1`) lets the poller
dispatch up to **N distinct Slack threads concurrently** while keeping turns **within a single
thread strictly serial** (FIFO delivery order is preserved per thread). The guarantee:
**parallel-across-threads, serial-within-thread.** Concurrent turns share `/workspace`, so a
cap >1 suits conversational / read-mostly fan-out — keep it at `1` for repo-mutating work.
See `docs/slack-notifications.md` § Phase 119.

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

### Mentioning people ("@ me back", "@ Ron", "@here")

Slack notifies a person **only** from the literal token `<@U…>` (user id) or `<!here>` /
`<!channel>`. `@Kurt`, `@U0KURT` without angle brackets, and display names are plain text —
they render as typed and ping nobody. That is the failure to avoid.

**Where the ids come from — never invent one:**

| You want to @ | The id is in |
|---|---|
| the person who sent this turn ("@ me back") | the `[Slack] From: <@U…>` line at the top of your prompt, also `$KM_SLACK_SENDER_ID` |
| someone the sender @-mentioned ("@ Ron") | the incoming message itself — Slack delivers `@Ron` as `<@U0RON>`, verbatim in your prompt |
| everyone active in the channel | `<!here>` — **only when explicitly asked**; it pages the whole channel |

km cannot look up a name (the bot never enumerates the workspace directory). If asked to @ someone
whose id is not in the prompt, say so and ask them to @-mention that person in the thread.

**Two ways to do it — both notify:**

```bash
# 1. Inline — write the token in the body. Works in every render mode, including the
#    default blocks-rich; the renderer preserves <@U…>/<!here>/<#C…> and escapes everything else.
cat > /tmp/reply.txt << 'EOF'
<@U0KURT> done — tests green, PR is up.
EOF
/opt/km/bin/km-slack reply --body /tmp/reply.txt

# 2. --mention — guaranteed notification independent of the body: prepends a "<@U…>" line
#    (and a leading section block in block modes). Accepts U…, @U…, <@U…>, here, channel;
#    comma-separated or repeated. Rejects names.
/opt/km/bin/km-slack reply --body /tmp/reply.txt --mention "$KM_SLACK_SENDER_ID"
/opt/km/bin/km-slack post --channel "$KM_SLACK_CHANNEL_ID" --thread "$KM_SLACK_THREAD_TS" \
  --body /tmp/reply.txt --mention U0KURT,U0RON
```

Your final reply is posted to the thread automatically when the turn ends, and a `<@U…>` in it
notifies the person. Any `km-slack post`/`reply` into the thread **cancels** that automatic post
(the poller sees the marker km-slack leaves), so such a call must carry your complete answer.
Reach for `--mention` only on a post you make deliberately and completely.

The marker is `/tmp/km-slack-posted.<thread_ts>`, written only after a **successful threaded**
`post` or `reply` (including the `invalid_blocks` re-post). A top-level post, `upload`, `update`
and `dm` write none, and a post into a different thread does not cancel this one.
`KM_SLACK_POSTED_MARKER=0` suppresses it; the streaming hook and the poller's own post use that.

`--mention` rules: `U…`, `W…`, `@U…`, `<@U…>` and lowercase ids are normalised to `<@U…>`;
`here`/`@here`/`<!here>` and `channel` give `<!here>`/`<!channel>`; duplicates are dropped. Anything
else, including a name, fails the command with exit 2 before anything is posted. At the 50-block cap
the mention block is dropped and the text line still notifies.

### Sending someone a DM ("can you DM me the summary?")

```bash
# The id comes from the [Slack] From: line / $KM_SLACK_SENDER_ID — never a name.
/opt/km/bin/km-slack dm --to "$KM_SLACK_SENDER_ID" --text "Summary: …"
/opt/km/bin/km-slack dm --to "$KM_SLACK_SENDER_ID" --body /tmp/summary.md --render blocks-rich --from-thread
```

- `--to` is a bare, uppercase user id (`U…`/`W…`); `<@U…>`, `@U…`, lowercase, `here` and names
  are rejected with exit 2. Give exactly one of `--text` or `--body`. `--render` is
  `plain|mrkdwn|blocks|blocks-rich` (default `$KM_SLACK_RENDER`, then `plain`; an unknown value
  is not rejected and renders plain). There is no `--mention`. On success it prints
  `channel=<D…> ts=<ts>` to stdout.
- The DM is a **top-level** message from the bot; your reply in the current thread still posts
  automatically. `--from-thread` (reads `KM_SLACK_CHANNEL_ID`/`KM_SLACK_THREAD_TS`) appends a
  permalink back to this thread.
- The bridge sends it only to a person on the install's `slack.default_route.allow_emails`.
  A refusal reads `km-slack dm: slack: bridge returned 403: {"ok":false,"error":"dm_not_allowed"}`:
  `dm_not_allowed` means the person is not listed (or their lookup failed); `dm_disabled` means
  this install has no default route. Say so in your reply rather than retrying.
- `channel_mismatch` means `--from-thread` named a thread that is not yours: drop the flag.
- `quota_exceeded` means this sandbox hit its action quota: stop, and say so in your reply.
- A reply the person types **under** your DM message comes back to this sandbox.

## Limits

| Limit | Value | Notes |
|---|---|---|
| Body size | 35 KB rendered, 40 KB hard | km-slack cuts the rendered text at 35 KB and appends a truncation footer, so a long body is shortened, not rejected. Split long output into several posts if all of it matters. |
| Stdin body | not supported | Always write to a file and pass `--body /path/to/file` |
| Channel format | `C...` | Must be a Slack channel ID, not a name. `$KM_SLACK_CHANNEL_ID` is already in this format. |
| Per-message rate | governed by Slack `chat.postMessage` tier-3 limits (~50/min) | Fan-out posts should pace themselves. |
| Inbound attachments | 25 files/msg, 100 MB/file | Bridge caps; over-limit messages still enqueue but with a warning attached. |

## Error Handling

A bridge refusal surfaces as `km-slack <verb>: slack: bridge returned <status>: {"ok":false,"error":"<code>"}`. km-slack retries only network errors (1 s, 2 s, 4 s); any HTTP 4xx/5xx fails at once, because the bridge has already used the request's nonce.

| Symptom | Cause | Action |
|---|---|---|
| `KM_SANDBOX_ID env var not set` | Not running inside a provisioned sandbox | Run `klanker:sandbox` first to confirm environment |
| `KM_SLACK_BRIDGE_URL env var not set` | Profile didn't enable Slack, or sandbox predates Slack support | Recreate sandbox with `notification.slack.enabled: true`, or post via email instead (`klanker:email`) |
| `AWS_REGION (or AWS_DEFAULT_REGION) not set` | Stripped env in a systemd unit or minimal shell | Re-source `/etc/profile.d/km-notify-env.sh` or set `AWS_DEFAULT_REGION` from IMDS |
| `load signing key: ssm GetParameter ... AccessDenied` | IAM role lost SSM read, or wrong `KM_RESOURCE_PREFIX` | Verify the sandbox role still has `ssm:GetParameter` on `/{prefix}/sandbox/{id}/signing-key` |
| `bridge returned 502: …channel_not_found` | Channel ID stale (channel archived or wrong ID) | Verify `$KM_SLACK_CHANNEL_ID` matches a live channel; per-sandbox channels archive at `km destroy` if `notification.slack.archiveOnDestroy: true` |
| `bridge returned 502: …not_in_channel` | Bot was removed from the channel | Operator must `/invite @<bot>` in the channel |
| `bridge returned 403: …channel_mismatch` | The channel is not yours and the post is not into a thread the bridge routed to you (top-level, wrong `--thread`, or `permalink`/`update` in a foreign channel) | Post into your own channel, or into the current turn's thread with `--thread "$KM_SLACK_THREAD_TS"` |
| `bridge returned 429: …quota_exceeded` | The sandbox hit its action quota (`slack_post` counts `post`, `upload` and `dm`) | Stop posting; say so in the reply |
| `bridge returned 503: …rate_limited` | Slack rate-limited the bot | Wait and try again; km-slack does not retry this |
| `rendered body exceeds 40960 bytes` | 40 KB hard cap after rendering | Practically unreachable since the 35 KB truncation; split the post |
| Exit code 0 but message not visible | Likely a Slack Connect external-share quirk on per-sandbox channels | Check `km otel` for the post event; the message may still be delivered, just rendered differently for external participants |
| Final transcript `.jsonl.gz` upload silently fails | Per-sandbox channels are Slack-Connect-shared; `files.completeUploadExternal` rejects external-shared channels | Pull transcript from S3: `aws s3 ls s3://${KM_ARTIFACTS_BUCKET}/transcripts/<sandbox-id>/` |

## Session-aware Reply (km-slack reply)

`km-slack reply` is the smart alternative to `km-slack post` when you want to thread a message into an existing conversation without knowing the thread timestamp in advance.  It resolves the target thread via a four-step chain (first hit wins):

### Resolution Order

| Priority | Source | How it works |
|---|---|---|
| 1 | `--thread <ts>` | Posts directly into that thread, in `--channel` or else `$KM_SLACK_CHANNEL_ID` (an error if neither is set). |
| 2 | `$KM_SLACK_THREAD_TS` env var | Set automatically by the inbound poller when a bridge turn is running. Use this to reply to the message that triggered the current agent turn. |
| 3 | Session ID (`--session <id>` or auto-detected) | Looks up the session in the `session-index` GSI via the bridge Lambda. Returns the `(channel_id, thread_ts)` of the thread the session was last active in. |
| 4 | Channel root fallback | Posts a new top-level message to `--channel` or `$KM_SLACK_CHANNEL_ID`. A lookup error at step 3 also lands here, with a stderr warning. |

`--channel` alone is a channel override for steps 2 to 4, not a thread selector. Steps 1 to 3 post into a thread and so write the posted marker; step 4 is top-level and writes none.

### Session Auto-detect Heuristic

When no `--session` flag is given, `km-slack reply` auto-detects the current session:

- **Claude (`$KM_AGENT=claude` or unset):** scans `~/.claude/projects/**/*.jsonl` and picks the file with the newest modification time. The session ID is the filename stem (UUID without `.jsonl`).
- **Codex (`$KM_AGENT=codex`):** LOW-confidence path. Attempts to read from `~/.codex/store/`. If no session file is found, `km-slack reply` logs a warning to stderr and falls through to channel-root fallback (exit 0, no error).

Auto-detect fires only when neither `--thread` nor `$KM_SLACK_THREAD_TS` is set.

### Flags

```
km-slack reply [flags]
  --thread <ts>     Parent thread timestamp (channel from --channel or $KM_SLACK_CHANNEL_ID)
  --channel <id>    Slack channel ID override (default $KM_SLACK_CHANNEL_ID)
  --session <id>    Explicit session ID; bypasses auto-detect
  --subject <text>  Optional bold header line
  --body <file>     Path to the message body file (required, exit 2 if missing; stdin '-' rejected)
  --render plain|mrkdwn|blocks|blocks-rich  Render mode (default: $KM_SLACK_RENDER, then plain)
  --mention <U…|here|channel>  Notify people (comma-separated or repeated); see "Mentioning people"
```

### Basic usage

```bash
# Reply into the current bridge-turn's thread (step 2: $KM_SLACK_THREAD_TS)
cat > /tmp/reply.txt << 'EOF'
✅ Done. Tests green, ready for review.
EOF
/opt/km/bin/km-slack reply --body /tmp/reply.txt

# Reply using an explicit session ID (step 3)
/opt/km/bin/km-slack reply --session "a1b2c3d4-..." --body /tmp/reply.txt

# Force a specific thread (step 1)
/opt/km/bin/km-slack reply \
  --thread "1717000000.123456" \
  --channel "$KM_SLACK_CHANNEL_ID" \
  --body /tmp/reply.txt
```

### When Slack is not configured

If `$KM_SLACK_CHANNEL_ID` is empty and no `--channel`/`--thread` is given (the sandbox was created without `notification.slack.enabled: true`), `km-slack reply` exits 1 with the message:

```
Slack not configured for this sandbox; re-create with notification.slack.enabled: true
```

### Operator-side equivalent

Operators can send a reply from their local machine using `km slack reply`:

```bash
# Via session ID (queries the session-index GSI directly with operator AWS creds)
km slack reply --session "a1b2c3d4-..." --body /tmp/reply.txt

# Via explicit thread + channel
km slack reply --thread "1717000000.123456" --channel "CXXXXXXXX" --body /tmp/reply.txt

# Via sandbox alias (falls back to channel root of the sandbox's bound channel)
km slack reply --alias my-sandbox --body /tmp/reply.txt
```

### Repair Commands

When thread or channel mappings go stale (a channel was archived or deleted), use these operator repair commands:

| Command | What it does |
|---|---|
| `km slack threads <sandbox-id>` | List all `km-slack-threads` rows for a sandbox (O(n) DDB Scan) |
| `km slack forget-thread --session <id>` | Delete the thread row for a specific session ID |
| `km slack forget-thread --thread <ts> --channel <id>` | Delete a thread row by exact DDB key |
| `km slack prune-threads [sandbox] [--dry-run]` | Scan all rows, probe each channel via `conversations.info`, delete rows pointing at gone channels |
| `km slack forget-channel <alias>` | Delete the alias→channel mapping from `km-slack-channels` (inverse of `km slack adopt`) |

`km doctor` checks for dead channel mappings automatically:

- **Slack thread dead channels** — WARN when any row in `km-slack-threads` points at a non-existent channel. Remediation: `km slack prune-threads`.
- **Slack channel dead alias** — WARN when any alias in `km-slack-channels` maps to a deleted channel. Remediation: `km slack forget-channel <alias>` + `km slack adopt <alias> <new-channel-id>`.

Both checks are SKIP-safe when the Slack bot token is absent.

## Other verbs

You will rarely need these; each goes through the bridge with the same signed envelope.

| Verb | What it does | Notes |
|---|---|---|
| `km-slack upload --channel C… [--thread ts] --s3-key transcripts/$KM_SANDBOX_ID/… --filename f --content-type application/gzip\|application/json\|text/plain --size-bytes N` | The bridge posts an S3 object as a Slack file | The key must sit under `transcripts/<your sandbox id>/`, the file must exist with exactly that size (≤ 100 MB), and the filename must have no `/`. Prints `km-slack upload: ok ts=…` to stderr. Same channel rule as `post`. Writes no marker. |
| `km-slack permalink --channel C… --ts T` | Prints the message's permalink URL to stdout | Own channel only: in a routed foreign channel it is `403 channel_mismatch`. |
| `km-slack update --channel C… --ts T (--text s \| --body file)` | Edits a message the bot posted (`chat.update`) | Own channel only, as `permalink`. The text is sent as-is: no `--render`, no `--mention`, no subject. |
| `km-slack react --ts T --emoji name [--exclusive a,b,c] [--remove] [--channel C…]` | Adds (or removes) the bot's reaction on a message; `--exclusive` removes every other name in the set after the add | Own channel only, as `permalink`. At most 10 exclusive names. `already_reacted`/`no_reaction` are success. Prints `km-slack: reacted ts=… emoji=…` to stdout. |
| `km-slack post\|reply … --broadcast` | With a thread, also shows the reply in the channel (`reply_broadcast`) | `post` needs `--thread` and refuses `--new-message`; `reply` fails if no thread resolves. Use it for a channel-wide ping that should live in the thread. |

`km-slack record-mapping` is internal to the transcript-streaming hook (it writes a DynamoDB row
mapping a posted message to a transcript offset); an agent never calls it.

---

## When NOT to Use This Skill

- **Operator action requests** — use `klanker:operator` (email-based natural-language interpreter). Slack posts are one-way notifications; the operator inbox is bidirectional and triggers `km` commands.
- **Sandbox-to-sandbox messages** — use `klanker:email`. Slack channels are operator-facing; other sandboxes don't read them.
- **Long output dumps** — paste the first 40 KB and link to S3 (`s3://$KM_ARTIFACTS_BUCKET/...`) for the rest. Slack truncates and the bridge will reject anyway.
