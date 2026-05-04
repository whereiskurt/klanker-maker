# Phase 68: Slack Transcript Streaming (Phase A) - Research

**Researched:** 2026-05-03
**Domain:** Claude Code hook extensions, Slack files API (3-step upload), Go Lambda streaming I/O, DynamoDB provisioning, EC2/Lambda IAM
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Profile schema:**
- New field: `spec.cli.notifySlackTranscriptEnabled` (bool, default `false`)
- Validation rule (hard): Requires `notifySlackEnabled: true` AND `notifySlackPerSandbox: true`
- Validation rule (hard): Incompatible with `notifySlackChannelOverride`
- No `EnvelopeVersion` bump — stays at `1`

**CLI surface:**
- `km agent run`: add `--transcript-stream` and `--no-transcript-stream` flags
- `km shell`: same two flags
- Injected env var: `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED=1` or `=0`

**Sandbox env vars:**
- `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED` — master gate from `/etc/profile.d/km-notify-env.sh`
- `KM_SLACK_STREAM_TABLE` — DDB table name, runtime-injected by `km create`

**Hook script changes (`pkg/compiler/userdata.go`):**
- Register `PostToolUse` in `mergeNotifyHookIntoSettings` at line 2546-2557
- Use Claude Code's `session_id` from hook stdin as `{sid}` for `/tmp/` files
- Streaming: tail JSONL from offset, collect new assistant text + tool_use entries, post via `km-slack post`, write ts + offset to DDB, update offset file
- Tool-call rendering: `🔧 {tool_name}: {one-line-input}` with tool-specific renderers + generic fallback (first 80 chars of `jq -c .`)
- Auto-thread-parent: If `KM_SLACK_THREAD_TS` unset and `/tmp/km-slack-thread.{sid}` missing, post parent message `🤖 [sb-id] turn started — {prompt[:80]}`, cache ts to `/tmp/km-slack-thread.{sid}`
- Upload (Stop branch): gzip transcript → S3 → `km-slack upload`
- Cleanup: rm `/tmp/km-slack-thread.{sid}` and `/tmp/km-slack-stream.{sid}.offset` at Stop
- Hook timeout: existing `timeout 10s` wrapper; always exits 0

**Envelope schema (`pkg/slack/payload.go`):**
- Add four additive fields: `S3Key`, `Filename`, `ContentType`, `SizeBytes int64`
- Add const `ActionUpload = "upload"`
- Alphabetical struct tag order preserved

**Bridge handler:**
- New action route `ActionUpload` in the existing dispatcher
- Validation order before AWS work: S3Key prefix, filename sanitization, ContentType allow-list, SizeBytes cap, Channel non-empty
- Slack 3-step flow: `files.getUploadURLExternal` → PUT (streamed S3 body) → `files.completeUploadExternal`
- Scope check on cold start via `auth.test`

**`km-slack` sandbox binary:**
- New subcommand `upload`: signs ActionUpload envelope, POSTs to bridge, retries via `BridgeBackoff`
- New subcommand `record-mapping`: writes stream→transcript mapping to DDB using sandbox IAM `PutItem`

**DDB table:**
- `{prefix}-km-slack-stream-messages`
- PK `channel_id` (S), SK `slack_ts` (S); attrs: `sandbox_id`, `session_id`, `transcript_offset` (N), `ttl_expiry` (N)
- On-demand billing, TTL on `ttl_expiry` (30 days)
- New module `infra/modules/dynamodb-slack-stream-messages/` mirroring Phase 67's `dynamodb-slack-threads/`
- Config helper `Config.GetSlackStreamMessagesTableName()` mirroring `GetSlackThreadsTableName()`

**IAM additions:**
- Bridge Lambda role: `s3:GetObject` and `s3:HeadObject` on `${KM_ARTIFACTS_BUCKET}/transcripts/*`
- Sandbox EC2 instance role: `s3:PutObject` on `${KM_ARTIFACTS_BUCKET}/transcripts/{sandbox_id}/*` and `dynamodb:PutItem` on `{prefix}-km-slack-stream-messages`

**S3 layout:** `transcripts/{sandbox_id}/{session_id}.jsonl.gz` in existing `KM_ARTIFACTS_BUCKET`

**Error handling:** Hook always exits 0; every failure silently logs to stderr

**`km doctor` checks (three new):**
1. `slack_transcript_table_exists`
2. `slack_files_write_scope`
3. `slack_transcript_stale_objects`

**Operator warning at `km create`:** Print to stderr when `notifySlackTranscriptEnabled: true` resolves

### Claude's Discretion

- Exact wave/plan partitioning of the 10 work units
- Whether `UploadFile` client method takes positional args vs options struct
- Whether `record-mapping` is a flag on `km-slack post` or a separate subcommand
- Tool-call one-liner rendering for complex tools (`Edit`, `Bash`)
- File naming on Slack (spec: `claude-transcript-{session_id}.jsonl.gz`)
- Lambda provisioned concurrency adjustment
- DDB module structure (recommended to mirror Phase 67's pattern)

### Deferred Ideas (OUT OF SCOPE)

**Deferred to Phase B:**
- Slack scope `reactions:read` and bridge `/events` route handling `reaction_added`
- New SQS event-type for fork dispatch
- Sandbox-side transcript surgery
- Phase 67 poller extension to route by `(channel_id, thread_ts)`
- New thread parent `🍴 forked from <message-link>`
- Trigger emoji configuration

**Permanently out of scope:**
- Transcript redaction/secret-stripping
- Mid-run threshold-triggered uploads
- Reaction-triggered on-demand uploads
- Full tool I/O in streamed messages
- Stream-without-upload or upload-without-stream toggles
- Streaming on the shared channel
- Replacing Phase 63 idle-ping behavior
</user_constraints>

---

## Summary

Phase 68 extends the existing km-notify-hook by adding a `PostToolUse` branch that streams assistant turns to a per-sandbox Slack thread in real time, and extends the `Stop` branch to upload the full gzipped JSONL transcript as a Slack file. Every component has a direct precedent in Phase 62/63/67, so implementation is largely additive.

The three genuinely new technical surfaces are: (1) the Slack files API 3-step upload flow, which replaced the deprecated `files.upload` endpoint and requires careful streaming to avoid Lambda OOM, (2) the `PostToolUse` Claude Code hook event, which fires synchronously and blocks Claude briefly — rate-limit exposure during heavy tool-call sequences is the key risk to design around, and (3) the `km-slack upload` and `record-mapping` subcommands in the sandbox binary, which require restructuring `cmd/km-slack/main.go` from a single `post`-only command into a multi-subcommand dispatcher.

**Primary recommendation:** Follow the Phase 67 precedent precisely. Every infra, IAM, config-helper, and DDB module pattern from Phase 67 has a ready template in the codebase; don't invent new patterns.

---

## Standard Stack

### Core (all already in use, additive only)

| Library/Component | Version | Purpose | Why Standard |
|---|---|---|---|
| `pkg/slack` package | Phase 63 | Envelope construction, sign/verify, client | Already owns the Slack trust boundary |
| `pkg/slack/bridge` package | Phase 63+67 | Lambda handler, dispatcher, interfaces | All action routing lives here |
| `cmd/km-slack` binary | Phase 63 | Sandbox-side signing + bridge POST | Restructure to multi-subcommand dispatcher |
| `pkg/compiler/userdata.go` | Phase 62+63+67 | Hook heredoc, settings.json merge | All hook logic lives here |
| `infra/modules/dynamodb-slack-threads/v1.0.0` | Phase 67 | DDB table pattern | Mirror exactly for stream-messages table |
| `internal/app/config` package | Phase 67 | Config.GetSlackThreadsTableName() pattern | Mirror for GetSlackStreamMessagesTableName() |
| AWS SDK Go v2 | v2 | S3 GetObject, DDB PutItem | Already in go.mod |

### New Slack API Surface

| API Method | Purpose | Rate Limit Tier |
|---|---|---|
| `files.getUploadURLExternal` | Step 1: obtain presigned upload URL + file_id | Tier 4 (50 req/min per workspace) |
| PUT to `upload_url` | Step 2: stream bytes to Slack CDN | Not a Slack API call; rate not shared with Web API tier |
| `files.completeUploadExternal` | Step 3: associate file with channel/thread | Tier 4 (50 req/min) |
| `auth.test` | Bridge cold-start scope check | Tier 4 |

Note: `files.upload` (v1) is deprecated and must NOT be used. Source: Slack API official docs.

---

## Architecture Patterns

### Recommended Project Structure Changes

```
cmd/km-slack/
└── main.go              ← restructure: add subcommand dispatcher
                           subcommands: post (existing), upload (new), record-mapping (new)

pkg/slack/
├── payload.go           ← add ActionUpload const + four envelope fields
├── client.go            ← add UploadFile method
└── bridge/
    ├── handler.go       ← add ActionUpload case to dispatcher
    ├── interfaces.go    ← add S3ObjectGetter + FileUploader interfaces
    └── aws_adapters.go  ← add SlackFileUploader + S3GetterAdapter

infra/modules/
└── dynamodb-slack-stream-messages/
    └── v1.0.0/
        ├── main.tf
        ├── variables.tf
        └── outputs.tf

pkg/profile/
└── validate.go          ← add notifySlackTranscriptEnabled rules

internal/app/config/
└── config.go            ← add SlackStreamMessagesTableName field + GetSlackStreamMessagesTableName()

internal/app/cmd/
├── agent.go             ← add --transcript-stream / --no-transcript-stream
├── shell.go             ← same
├── create.go            ← operator warning + KM_SLACK_STREAM_TABLE injection
└── doctor_slack.go      ← three new checks (or doctor_slack_transcript.go)
```

### Pattern 1: PostToolUse Hook Event Schema

Claude Code fires `PostToolUse` after each tool completes. The hook stdin JSON is:

```json
{
  "session_id": "sess-abc123",
  "transcript_path": "/home/sandbox/.claude/projects/-workspace/abc123.jsonl",
  "hook_event_name": "PostToolUse",
  "tool_name": "Edit",
  "tool_input": {...},
  "tool_response": {...},
  "cwd": "/workspace"
}
```

Key fields for Phase 68:
- `session_id` — use as `{sid}` for all `/tmp/` file namespacing
- `transcript_path` — tail from offset to find new assistant text and tool_use entries

The hook fires **synchronously** (blocking Claude) but with the existing `timeout 10s` wrapper the maximum impact is 10 seconds. Claude does not proceed until the hook exits or times out.

### Pattern 2: Transcript JSONL Line Types

Verified from `pkg/compiler/testdata/notify-hook-fixture-transcript.jsonl`:

```jsonl
{"type":"user","message":{"content":[{"type":"text","text":"please refactor"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Starting refactor..."}]}}
{"type":"tool_use","name":"Edit","input":{}}
{"type":"tool_result","content":"ok"}
{"type":"assistant","message":{"content":[{"type":"text","text":"I've finished..."}]}}
```

jq paths confirmed:
- Assistant text: `select(.type=="assistant") | .message.content[]? | select(.type=="text") | .text`
- Tool use name: `select(.type=="tool_use") | .name`
- Tool use input: `select(.type=="tool_use") | .input`
- Tool result: `select(.type=="tool_result") | .content`

The existing Stop branch uses exactly this parsing pattern (verified at `userdata.go:441-448`).

**CRITICAL: The fixture shows the transcript is append-only.** New entries are always appended to the end. Offset tracking reads from byte-offset N to EOF and then updates N to the new EOF position.

### Pattern 3: Multi-Subcommand Restructure for km-slack

Current `cmd/km-slack/main.go` checks `os.Args[1] != "post"` as its entire subcommand dispatch. The restructure pattern follows standard Go flag package multi-subcommand:

```go
// main.go
func main() {
    if len(os.Args) < 2 {
        usage(); os.Exit(2)
    }
    switch os.Args[1] {
    case "post":           runPost(os.Args[2:])
    case "upload":         runUpload(os.Args[2:])
    case "record-mapping": runRecordMapping(os.Args[2:])
    default:
        fmt.Fprintln(os.Stderr, "unknown subcommand: "+os.Args[1])
        os.Exit(2)
    }
}
```

Existing `runWith` function signature stays intact; the `post` subcommand is the existing code with only the dispatch changed.

### Pattern 4: Bridge ActionUpload Handler Wiring

The existing handler dispatch in `handler.go` (line 88):

```go
if env.Action != slack.ActionPost && env.Action != slack.ActionArchive && env.Action != slack.ActionTest {
    return errResp(400, "unknown_action")
}
```

**Must be updated** to add `slack.ActionUpload` to the allowed set before the signature verification path reaches the dispatch switch. The upload action requires additional validation fields (`S3Key`, `Filename`, etc.) checked after the existing 7 steps.

The bridge `Handler` struct gains two new injectable interfaces:
- `S3Getter S3ObjectGetter` — for streaming S3 GetObject body
- `FileUploader SlackFileUploader` — for the 3-step Slack upload flow

### Pattern 5: Slack 3-Step Upload — Streaming Implementation

The critical implementation detail is that step 2 must stream the S3 body directly into the PUT request without loading it into memory. The Go pattern:

```go
// Source: Slack API docs (files.getUploadURLExternal)
// Step 1
getURLResp, _ := client.callJSON(ctx, "files.getUploadURLExternal", map[string]any{
    "filename": filename,
    "length":   sizeBytes,
})
uploadURL := getURLResp.UploadURL
fileID := getURLResp.FileID

// Step 2 — streaming: S3 body goes straight into PUT
s3Body, _ := s3client.GetObject(ctx, &s3.GetObjectInput{...})
defer s3Body.Body.Close()
putReq, _ := http.NewRequestWithContext(ctx, "PUT", uploadURL, s3Body.Body)
putReq.Header.Set("Content-Type", contentType)
putReq.ContentLength = sizeBytes   // REQUIRED — Slack rejects chunked encoding
httpClient.Do(putReq)

// Step 3
client.callJSON(ctx, "files.completeUploadExternal", map[string]any{
    "files":      []map[string]any{{"id": fileID, "title": filename}},
    "channel_id": channel,
    "thread_ts":  threadTS,   // omit key if empty
})
```

**Pitfall:** `putReq.ContentLength` must be set explicitly. If Go's `http.Client` detects an unknown content length it falls back to chunked transfer encoding, which Slack's CDN rejects with HTTP 400.

**Memory:** Because `s3Body.Body` is an `io.ReadCloser` (not buffered), peak Lambda memory is only the Go runtime baseline (~30 MB) regardless of transcript size.

### Pattern 6: Offset Tracking in Bash

The streaming hook reads JSONL from a known byte offset:

```bash
sid=$(echo "$payload" | jq -r '.session_id')
offset_file="/tmp/km-slack-stream.${sid}.offset"
offset=0
[[ -f "$offset_file" ]] && offset=$(cat "$offset_file")

# Tail from offset
transcript_path=$(echo "$payload" | jq -r '.transcript_path // ""')
new_lines=$(tail -c +$((offset + 1)) "$transcript_path" 2>/dev/null || echo "")
new_offset=$(wc -c < "$transcript_path" 2>/dev/null || echo "$offset")

# ... process new_lines ...

echo "$new_offset" > "$offset_file"
```

`tail -c +N` prints from byte N onward (1-indexed). `wc -c` gives the file size in bytes.

### Pattern 7: NotifyEnv Template — Adding KM_SLACK_STREAM_TABLE

Looking at userdata.go lines 500-516, the `NotifyEnv` template is a `map[string]string` written via range. The pattern for adding `KM_SLACK_STREAM_TABLE`:

```go
// In the NotifyEnv map population (around userdata.go:2720 area):
if p.Spec.CLI.NotifySlackTranscriptEnabled {
    data.NotifyEnv["KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED"] = "1"
    data.NotifyEnv["KM_SLACK_STREAM_TABLE"] = streamTableName
} else {
    data.NotifyEnv["KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED"] = "0"
}
```

The table name is deterministic (`{prefix}-km-slack-stream-messages`), so it can be hard-coded in the profile.d file without SSM late-binding.

### Pattern 8: Config Helper — Mirroring GetSlackThreadsTableName

Verified exact pattern at `internal/app/config/config.go:329-341`:

```go
// GetSlackStreamMessagesTableName returns the stream-messages DDB table name.
// If SlackStreamMessagesTableName is explicitly set, that value wins. Otherwise
// derived from GetResourcePrefix() + "-km-slack-stream-messages".
func (c *Config) GetSlackStreamMessagesTableName() string {
    if c == nil {
        return "km-slack-stream-messages"
    }
    if c.SlackStreamMessagesTableName != "" {
        return c.SlackStreamMessagesTableName
    }
    return c.GetResourcePrefix() + "-km-slack-stream-messages"
}
```

Note the Phase 67 table is `{prefix}-slack-threads` but Phase 68 CONTEXT.md specifies `{prefix}-km-slack-stream-messages` — the extra `km-` is in the module name, not the prefix pattern. Verify: Phase 67 config helper returns `c.GetResourcePrefix() + "-slack-threads"` which with default prefix gives `km-slack-threads`. Phase 68 should return `c.GetResourcePrefix() + "-km-slack-stream-messages"` giving `km-km-slack-stream-messages` — this looks wrong. **OPEN QUESTION** — see below.

### Pattern 9: EC2 IAM — Adding S3 Transcript Grant

The ec2spot module at `infra/modules/ec2spot/v1.0.0/main.tf` already has per-sandbox IAM policies added one policy per concern (lines 285-458). The new transcript policy follows the same pattern as `ec2spot_slack_inbound_sqs`:

```hcl
resource "aws_iam_role_policy" "ec2spot_slack_transcript_s3" {
  count = local.total_ec2spot_count > 0 ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-slack-transcript-s3"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid    = "S3PutTranscript"
      Effect = "Allow"
      Action = ["s3:PutObject"]
      Resource = "arn:aws:s3:::${var.artifacts_bucket}/transcripts/${var.sandbox_id}/*"
    }]
  })
}
```

A second policy for DDB PutItem follows the same shape. Note: `var.artifacts_bucket` is already defined in the ec2spot module (it's passed in as an input for existing S3 grants).

**VERIFY:** Check `infra/modules/ec2spot/v1.0.0/variables.tf` to confirm `artifacts_bucket` is an existing variable. If not, it needs to be added. This is a HIGH priority pre-check.

### Anti-Patterns to Avoid

- **Buffering S3 body before PUT:** Causes Lambda OOM on large transcripts. Always stream `io.ReadCloser` directly.
- **Using `KM_AGENT_RUN_ID` as session identifier:** Unset for `km shell` interactive sessions. Use `session_id` from hook stdin.
- **Using the deprecated `files.upload` API:** Deprecated by Slack, returns errors for some token types. Only use the 3-step flow.
- **Retrying 4xx/5xx bridge responses with the same envelope:** The nonce is already consumed; reuse triggers `replayed_nonce`. The existing `BridgeBackoff` only retries network errors — this behavior is correct and must not be changed for the `upload` subcommand.
- **Setting `EnvelopeVersion` to 2:** CONTEXT.md explicitly prohibits this. New fields are zero-valued and ignored by old bridge versions.
- **Using bare `go build`:** Always `make build` after editing CLI source (CLAUDE.md memory `feedback_rebuild_km`).

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---|---|---|---|
| Ed25519 signing for upload envelope | Custom signing | Existing `slack.SignEnvelope` + `BuildEnvelope` | Identical to existing `post` path |
| Retry on bridge network errors | Custom retry loop | Existing `BridgeBackoff` in `pkg/slack/client.go:179` | 1s/2s/4s schedule already tested |
| Nonce replay protection for upload | Custom nonce table | Existing bridge `NonceStore.Reserve` path | Already handles the `unknown_action` race |
| DDB table TTL | Custom cleanup Lambda | DynamoDB native TTL on `ttl_expiry` (Number, Unix epoch) | Already used in km-slack-threads and km-slack-bridge-nonces |
| HMAC timestamp window for upload | Custom timestamp check | Existing `MaxClockSkewSeconds = 300` in bridge handler | Same path for all actions |
| Auth.test scope parsing | Custom OAuth parser | Existing `SlackAPIResponse` + `BotUserIDFetcher` pattern | auth.test returns `scopes` in X-OAuth-Scopes header, not in JSON body |

**Key insight:** The upload action rides the ENTIRE existing sign/verify pipeline unchanged. The only new logic is (a) the `ActionUpload` allowed-action check, (b) upload-specific field validation, and (c) the 3-step Slack file upload dispatch.

---

## Common Pitfalls

### Pitfall 1: PostToolUse Rate Limit Exposure

**What goes wrong:** During a heavy autonomous run (`km agent run --prompt "audit all 200 tests"`), Claude may execute 30+ tool calls in a row with no pause. Each `PostToolUse` hook posts to Slack. Slack's rate limit for `chat.postMessage` is approximately 1 message/second per channel (Tier 3: ~50 msg/min burst). At 30 tool calls in 60 seconds, the hook will hit 429s.

**Why it happens:** PostToolUse fires synchronously per tool call; 429s come back as HTTP 503 from the bridge (which maps `ErrSlackRateLimited` to 503 + Retry-After). The sandbox `km-slack post` does NOT retry 5xx responses (by design — nonce is consumed). The message is silently dropped.

**How to avoid:** This is by design — message drops on rate limit are acceptable (transcript file has full record). The hook already exits 0 on all failures. The Retry-After header from the bridge is discarded at the sandbox level; no sleep-retry. This is intentional.

**Warning signs:** Operators see gaps in Slack thread during heavy runs. This is expected behavior, documented in operator guide.

### Pitfall 2: Struct Tag Order for CanonicalJSON

**What goes wrong:** Adding envelope fields in non-alphabetical order breaks the Ed25519 signature verification. The bridge uses `CanonicalJSON` which depends on `encoding/json` struct tag ordering.

**Why it happens:** Go's `encoding/json` encodes fields in the order they appear in the struct. The existing fields are in alphabetical order by json tag name. The new fields (`content_type`, `filename`, `s3_key`, `size_bytes`) must be inserted at the alphabetically correct positions within the struct.

**Correct order** in `SlackEnvelope`:
```
action, body, channel, content_type, filename, nonce, s3_key, sender_id,
size_bytes, subject, thread_ts, timestamp, version
```

**How to avoid:** Verify with `pkg/slack/payload_test.go` — extend with canonical JSON serialization test for `ActionUpload` envelope, confirm field order matches expected JSON.

### Pitfall 3: Slack PUT Requires Explicit ContentLength

**What goes wrong:** If `putReq.ContentLength` is not set, Go's HTTP client uses chunked transfer encoding. Slack's file upload CDN endpoint does not accept chunked encoding and returns HTTP 400.

**Why it happens:** The S3 `GetObject` response body is an `io.ReadCloser` with unknown length if the `ContentLength` response header is not propagated.

**How to avoid:** Explicitly set `putReq.ContentLength = envelope.SizeBytes` (from the envelope's `SizeBytes` field, which the sandbox computed from the gzipped file's `wc -c` output).

### Pitfall 4: Lambda replace_triggered_by on Bridge IAM Role

**What goes wrong:** Adding `s3:GetObject` and `s3:HeadObject` to the bridge Lambda's IAM role may require a full role recreation. Without `replace_triggered_by`, the Lambda retains a stale KMS grant referencing the old role ARN, causing permission errors on the first warm Lambda invocation.

**Why it happens:** AWS Lambda caches the IAM role ARN at function creation; when the role is recreated, the function's KMS policy grant is stale.

**How to avoid:** The bridge module ALREADY has `replace_triggered_by = [aws_iam_role.slack_bridge]` at `infra/modules/lambda-slack-bridge/v1.0.0/main.tf:264-267`. When adding the new S3 policy, add it as a new `aws_iam_role_policy` resource in the module. The `replace_triggered_by` is on the Lambda function resource, not the role policy — adding a new policy to the role does NOT require role recreation, so no additional `replace_triggered_by` trigger is needed for a new policy attachment.

### Pitfall 5: DDB table name double-prefix issue

**What goes wrong:** The existing Phase 67 pattern is `GetResourcePrefix() + "-slack-threads"` which gives `km-slack-threads`. If Phase 68 follows the same pattern for `"-km-slack-stream-messages"` it produces `km-km-slack-stream-messages`.

**Resolution:** The CONTEXT.md specifies the table name as `{prefix}-km-slack-stream-messages`. This means `GetResourcePrefix()` already contains the `km` prefix and the suffix includes a second `km`. The actual table name will be `km-km-slack-stream-messages` with default prefix. The CONTEXT.md is intentional — this matches the module name `dynamodb-slack-stream-messages` with the `km` in the suffix for clarity. Confirm the Terraform `var.table_name` input is set to `{resource_prefix}-km-slack-stream-messages` by the live Terragrunt configuration. Verify before implementation.

**Alternate resolution:** Use suffix `"-slack-stream-messages"` to get `km-slack-stream-messages` (consistent with `km-slack-threads`). This is within Claude's discretion per CONTEXT.md. Recommend verifying with the spec owner before choosing.

### Pitfall 6: `wc -c` includes newline artifacts on some platforms

**What goes wrong:** On macOS, `wc -c` output includes leading whitespace. The offset file contains `     42` instead of `42`, and `tail -c +43` fails.

**How to avoid:** Use `wc -c < "$file" | tr -d ' '` or `stat -c%s "$file"` on Linux. Amazon Linux 2 (the sandbox OS) uses GNU coreutils where `wc -c` outputs a plain number. The hook only runs on the EC2 sandbox, not the operator workstation — platform difference is not an issue for runtime. Tests on macOS should use `wc -c | tr -d ' \t'`.

### Pitfall 7: files.completeUploadExternal thread_ts semantics

**What goes wrong:** Passing `thread_ts: ""` (empty string) in the JSON body to `files.completeUploadExternal` causes Slack to return `invalid_arguments`.

**How to avoid:** Only include `thread_ts` in the payload map when it is non-empty. Use `if threadTS != "" { payload["thread_ts"] = threadTS }` pattern, identical to `chat.postMessage` in the existing codebase.

---

## Code Examples

### Hook Gate Pattern (verified from existing Stop branch)

```bash
# Source: pkg/compiler/userdata.go:413-417
case "$event" in
  PostToolUse) [[ "${KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED:-0}" == "1" ]] || exit 0 ;;
  Notification) [[ "${KM_NOTIFY_ON_PERMISSION:-0}" == "1" ]] || exit 0 ;;
  Stop)         [[ "${KM_NOTIFY_ON_IDLE:-0}"       == "1" ]] || exit 0 ;;
  *)            exit 0 ;;
esac
```

### AppendKMHook for PostToolUse (verified from existing mergeNotifyHookIntoSettings)

```go
// Source: pkg/compiler/userdata.go:2561-2562
appendKMHook("Notification", "/opt/km/bin/km-notify-hook Notification")
appendKMHook("Stop", "/opt/km/bin/km-notify-hook Stop")
// New for Phase 68:
appendKMHook("PostToolUse", "/opt/km/bin/km-notify-hook PostToolUse")
```

### Envelope Field Addition (alphabetical insertion required)

```go
// Source: pkg/slack/payload.go:44-54 (current)
// New struct after Phase 68 — verified alphabetical tag order:
type SlackEnvelope struct {
    Action      string `json:"action"`
    Body        string `json:"body"`
    Channel     string `json:"channel"`
    ContentType string `json:"content_type"`  // NEW — between channel and nonce
    Filename    string `json:"filename"`       // NEW — between content_type and nonce
    Nonce       string `json:"nonce"`
    S3Key       string `json:"s3_key"`         // NEW — between nonce and sender_id
    SenderID    string `json:"sender_id"`
    SizeBytes   int64  `json:"size_bytes"`     // NEW — between sender_id and subject
    Subject     string `json:"subject"`
    ThreadTS    string `json:"thread_ts"`
    Timestamp   int64  `json:"timestamp"`
    Version     int    `json:"version"`
}
```

### DDB Module Structure (mirror of dynamodb-slack-threads/v1.0.0/main.tf)

```hcl
# infra/modules/dynamodb-slack-stream-messages/v1.0.0/main.tf
resource "aws_dynamodb_table" "slack_stream_messages" {
  name         = var.table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "channel_id"
  range_key    = "slack_ts"

  attribute {
    name = "channel_id"
    type = "S"
  }
  attribute {
    name = "slack_ts"
    type = "S"
  }

  ttl {
    attribute_name = "ttl_expiry"
    enabled        = true
  }

  point_in_time_recovery { enabled = false }
  server_side_encryption  { enabled = true }

  tags = merge(var.tags, {
    Name      = var.table_name
    Component = "km-slack-transcript"
  })
}
```

### Bridge Scope Check on Cold Start (new cold-start logic)

```go
// In bridge Lambda main.go, on cold start after token fetch:
// Get X-OAuth-Scopes header from auth.test response
scopes, _ := authTestResp.Header.Get("X-OAuth-Scopes")
if !strings.Contains(scopes, "files:write") {
    // Cache this state; return 400 on ActionUpload requests
    h.missingFilesWrite = true
}
```

Note: `auth.test` returns granted scopes in the `X-OAuth-Scopes` HTTP response header, not in the JSON body. The current `SlackAPIResponse` struct doesn't capture headers — the bridge's `call()` method (or `SlackPosterAdapter.call()`) needs to be extended to capture response headers for scope checking.

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|---|---|---|---|
| `files.upload` | 3-step: `getUploadURLExternal` + PUT + `completeUploadExternal` | 2024 Slack deprecation | New flow is mandatory; old endpoint returns errors |
| Polling for transcript completion | Hook-driven (PostToolUse fires synchronously) | Claude Code hooks model | No polling needed; hook fires after each tool call |
| Per-invocation SSM SendCommand for env injection | SSM Parameter Store at boot (Phase 67 pattern) | Phase 67 (SCP blocks SendCommand in app account) | `KM_SLACK_STREAM_TABLE` should be written to `km-notify-env.sh` at create time, not injected via SendCommand |

---

## Open Questions

1. **DDB table name: `km-slack-stream-messages` vs `km-km-slack-stream-messages`**
   - What we know: CONTEXT.md says `{prefix}-km-slack-stream-messages`; Phase 67 uses `{prefix}-slack-threads`
   - What's unclear: Whether the double `km-km-` is intended or a spec typo
   - Recommendation: Use `{prefix}-slack-stream-messages` (without the extra `km-`) to stay consistent with the `km-slack-threads` pattern. Document the choice in PLAN.md.

2. **`auth.test` scope check implementation detail**
   - What we know: Slack returns scopes in `X-OAuth-Scopes` HTTP header, not JSON body
   - What's unclear: Whether the bridge's `SlackPosterAdapter.call()` method currently captures response headers
   - Recommendation: Verify by reading `pkg/slack/bridge/aws_adapters.go:300-340` fully. If headers are not captured, add header capture to the `call()` method or use a separate raw HTTP call for the scope probe.

3. **ec2spot `artifacts_bucket` variable**
   - What we know: The ec2spot module has an existing IAM policy that references `var.artifacts_bucket`... or does it?
   - What's unclear: Investigation did NOT confirm `artifacts_bucket` is an existing ec2spot variable. The search showed budget, bedrock, SQS, and github policies but no existing S3 artifact policy in the ec2spot module itself. Email attachments go via SES direct, not S3 PutObject from the sandbox.
   - Recommendation: Wave 0 task must read `infra/modules/ec2spot/v1.0.0/variables.tf` to confirm. If `artifacts_bucket` is missing, the Wave 1 schema+infra task must add it as a new variable AND populate it from the Terragrunt service.hcl template.

4. **Lambda cold-start scope check caching**
   - What we know: Bridge cold starts take ~3s; `auth.test` adds one more API call
   - What's unclear: Whether the scope state should be cached for the Lambda's lifetime or re-checked per-request
   - Recommendation: Cache at cold start. The bridge already lazy-loads the bot token on cold start; add scope check to the same cold-start initialization path.

---

## Validation Architecture

> `workflow.nyquist_validation` is `true` in `.planning/config.json` — this section is required.

### Test Framework

| Property | Value |
|---|---|
| Framework | Go test (`go test ./...`) + bash script execution |
| Config file | none — standard Go test runner |
| Quick run command | `go test ./pkg/slack/... ./pkg/compiler/... ./pkg/profile/... ./internal/app/cmd/... -run TestPhase68 -timeout 60s` |
| Full suite command | `go test ./... -timeout 300s` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command | File Exists? |
|---|---|---|---|
| PostToolUse hook gate (disabled) exits 0 immediately | unit/bash | `go test ./pkg/compiler/... -run TestNotifyHook_PostToolUse_GateOff` | Wave 0 gap |
| PostToolUse hook auto-creates thread parent when KM_SLACK_THREAD_TS unset | unit/bash | `go test ./pkg/compiler/... -run TestNotifyHook_PostToolUse_AutoParent` | Wave 0 gap |
| PostToolUse hook uses cached thread parent on 2nd fire | unit/bash | `go test ./pkg/compiler/... -run TestNotifyHook_PostToolUse_CachedThread` | Wave 0 gap |
| PostToolUse offset tracking advances across multiple fires | unit/bash | `go test ./pkg/compiler/... -run TestNotifyHook_PostToolUse_OffsetTracking` | Wave 0 gap |
| Stop hook runs streaming then upload logic | unit/bash | `go test ./pkg/compiler/... -run TestNotifyHook_Stop_TranscriptUpload` | Wave 0 gap |
| ActionUpload envelope canonical JSON serializes with new fields zero-valued | unit | `go test ./pkg/slack/... -run TestCanonicalJSON_ActionUpload` | Wave 0 gap |
| Existing post envelope serializes identically after struct extension | unit | `go test ./pkg/slack/... -run TestCanonicalJSON_PostUnchanged` | Partial (existing test extends) |
| UploadFile 3-step flow succeeds (mock HTTP server) | unit | `go test ./pkg/slack/... -run TestUploadFile_HappyPath` | Wave 0 gap |
| UploadFile step 2 PUT failure leaves no dangling state | unit | `go test ./pkg/slack/... -run TestUploadFile_PUTFails` | Wave 0 gap |
| Bridge ActionUpload S3Key prefix mismatch → 403 | unit | `go test ./pkg/slack/bridge/... -run TestHandler_ActionUpload_PrefixMismatch` | Wave 0 gap |
| Bridge ActionUpload SizeBytes > 100MB → 400 | unit | `go test ./pkg/slack/bridge/... -run TestHandler_ActionUpload_SizeCap` | Wave 0 gap |
| Bridge ActionUpload ContentType not in allow-list → 400 | unit | `go test ./pkg/slack/bridge/... -run TestHandler_ActionUpload_ContentType` | Wave 0 gap |
| Bridge ActionUpload files:write missing → 400 with clear error | unit | `go test ./pkg/slack/bridge/... -run TestHandler_ActionUpload_ScopeMissing` | Wave 0 gap |
| notifySlackTranscriptEnabled=true without notifySlackEnabled=true fails validation | unit | `go test ./pkg/profile/... -run TestValidate_SlackTranscript_RequiresSlackEnabled` | Wave 0 gap |
| notifySlackTranscriptEnabled=true with notifySlackChannelOverride fails validation | unit | `go test ./pkg/profile/... -run TestValidate_SlackTranscript_IncompatibleWithOverride` | Wave 0 gap |
| Config.GetSlackStreamMessagesTableName() derives correctly from prefix | unit | `go test ./internal/app/config/... -run TestConfig_GetSlackStreamMessagesTableName` | Wave 0 gap |
| km doctor slack_transcript_table_exists check | unit | `go test ./internal/app/cmd/... -run TestDoctor_SlackTranscriptTableExists` | Wave 0 gap |
| km doctor slack_files_write_scope check | unit | `go test ./internal/app/cmd/... -run TestDoctor_SlackFilesWriteScope` | Wave 0 gap |
| km doctor slack_transcript_stale_objects check | unit | `go test ./internal/app/cmd/... -run TestDoctor_SlackTranscriptStaleObjects` | Wave 0 gap |
| Phase 63 Stop hook behavior unchanged when transcript disabled | regression | `go test ./pkg/compiler/... -run TestNotifyHook_Stop_EmailOnly` | Existing (extend) |
| Phase 67 inbound poller unaffected | regression | `go test ./pkg/compiler/... -run TestSlackInboundPoller` | Existing (extend) |

### Sampling Rate

- **Per task commit:** `go test ./pkg/slack/... ./pkg/compiler/... -run TestPhase68 -timeout 60s`
- **Per wave merge:** `go test ./... -timeout 300s`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/compiler/testdata/notify-hook-fixture-post-tool-use.json` — PostToolUse stdin fixture
- [ ] `pkg/compiler/testdata/notify-hook-stub-km-slack.sh` — stub for km-slack post/upload (mirrors existing `notify-hook-stub-km-send.sh`)
- [ ] `pkg/slack/bridge/aws_adapters_upload_test.go` — S3Getter and FileUploader mock implementations
- [ ] Framework install: none needed — Go test infrastructure already in place

---

## Sources

### Primary (HIGH confidence)

- Direct code reading: `pkg/slack/payload.go`, `pkg/slack/client.go`, `pkg/slack/bridge/handler.go`, `pkg/slack/bridge/interfaces.go`, `pkg/slack/bridge/aws_adapters.go` — envelope structure, handler pipeline, and wiring patterns verified
- Direct code reading: `pkg/compiler/userdata.go:399-496, 2519-2572` — hook heredoc, mergeNotifyHookIntoSettings verified
- Direct code reading: `pkg/compiler/testdata/notify-hook-fixture-*.json/jsonl` — JSONL schema and fixture shapes verified
- Direct code reading: `pkg/compiler/notify_hook_script_test.go` — test harness pattern verified
- Direct code reading: `infra/modules/dynamodb-slack-threads/v1.0.0/main.tf` — DDB module pattern verified
- Direct code reading: `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` — bridge IAM pattern and `replace_triggered_by` usage verified
- Direct code reading: `infra/modules/ec2spot/v1.0.0/main.tf` — sandbox IAM role policy pattern verified
- Direct code reading: `internal/app/config/config.go:329-341` — GetSlackThreadsTableName helper pattern verified
- Direct code reading: `internal/app/cmd/doctor_slack.go`, `doctor_slack_inbound_test.go` — doctor check patterns verified
- Direct code reading: `internal/app/cmd/create_slack.go` — SlackAPI interface, ChannelInfo usage verified
- Direct code reading: `internal/app/cmd/agent.go:1200-1252` — notifyEnvLines injection pattern verified
- Direct code reading: `cmd/km-slack/main.go` — current single-subcommand structure verified
- Direct code reading: `pkg/profile/types.go:405-436` — CLISpec fields for Slack verified
- Direct code reading: `pkg/profile/validate.go:277-349` — notifySlackInboundEnabled validation rules (precedent for new transcript rules) verified

### Secondary (MEDIUM confidence)

- Slack API documentation (from spec): `files.getUploadURLExternal` / `files.completeUploadExternal` 3-step flow — confirmed as the current standard; `files.upload` deprecated
- Claude Code hook behavior (from codebase fixture + spec context): PostToolUse fires synchronously per tool call with `session_id` and `transcript_path` in stdin JSON

### Tertiary (LOW confidence — flag for validation)

- Slack `X-OAuth-Scopes` response header on `auth.test`: documented in Slack API docs but not verified against production API in this codebase. The bridge currently uses `auth.test` only for token validation (not scope inspection). Implementation must verify header capture.

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — entire stack is Phase 63/67 precedents verified in code
- Architecture: HIGH — patterns lifted directly from existing codebase
- Pitfalls: HIGH (canonical JSON, streaming PUT) / MEDIUM (rate limit, scope header) — verified from code or documented API behavior
- Open questions: 4 identified, none blocking (all resolvable in Wave 0 pre-check)

**Research date:** 2026-05-03
**Valid until:** 2026-06-03 (Slack API 3-step upload is stable; Go patterns are stable)
