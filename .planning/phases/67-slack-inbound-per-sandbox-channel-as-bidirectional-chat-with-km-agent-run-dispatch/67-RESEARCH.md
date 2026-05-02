# Phase 67: Slack Inbound — Per-sandbox Channel as Bidirectional Chat with km agent run Dispatch — Research

**Researched:** 2026-05-02
**Domain:** SQS FIFO (new to codebase), Slack Events API HMAC verification, DynamoDB GSI, systemd bash poller pattern, claude --resume session_id
**Confidence:** HIGH (code-verified), MEDIUM (SQS SDK — not yet in go.mod), HIGH (claude CLI — official docs verified)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Queue Topology:**
- One SQS FIFO queue per sandbox; naming `{resource_prefix}-slack-inbound-{sandbox-id}.fifo`
- Created at `km create`, deleted at `km destroy`; URL in DDB `slack_inbound_queue_url`, injected as `KM_SLACK_INBOUND_QUEUE_URL`
- MessageGroupId = sandbox-id; MessageDeduplicationId = Slack event_id
- 14-day retention, 30s VisibilityTimeout, NO DLQ
- IAM: sandbox instance role gets `sqs:ReceiveMessage/DeleteMessage/GetQueueAttributes` on own queue; bridge Lambda gets `sqs:SendMessage` on `{prefix}-slack-inbound-*.fifo`

**Delivery Semantics:**
- Stateless `km agent run` per SQS message; session continuity via DDB `km_slack_threads` (channel_id + thread_ts → claude_session_id)
- Poller: SQS long-poll → DDB lookup → `km agent run [--resume <session-id>] --prompt <text>` → parse output.json → DDB write-back → `DeleteMessage`
- New DDB table `{prefix}-km_slack_threads`: PK=channel_id (S), SK=thread_ts (S); attrs: claude_session_id, sandbox_id, last_turn_ts, turn_count, created_at; TTL 30 days

**Inbound Enablement:**
- New profile field `spec.cli.notifySlackInboundEnabled` (bool, default false)
- Validation: requires `notifySlackEnabled: true` AND `notifySlackPerSandbox: true`; must NOT have `notifySlackChannelOverride` set
- No CLI flag overrides; profile-only

**Bridge Lambda Extensions:**
- New `POST /events` route for Slack Events API; existing `POST /` unchanged
- Slack signing-secret HMAC-SHA256 verification; `url_verification` challenge response
- event_id dedup via existing `km_slack_bridge_nonces`; bot-loop filter; channel→sandbox GSI lookup
- SQS SendMessage to per-sandbox queue; DDB upsert to `km_slack_threads`
- Return 200 in <3s (no blocking calls after SQS write)

**Sandbox-Side Poller:**
- Bash + systemd; file `/opt/km/bin/km-slack-inbound-poller` + `/etc/systemd/system/km-slack-inbound-poller.service`
- Mirror `km-mail-poller` pattern exactly; inline heredoc in `pkg/compiler/userdata.go`
- Conditional generation: only when `notifySlackInboundEnabled: true`

**Edge Cases (LOCKED):**
- Paused sandbox: SQS retains 14d; optional one-time "paused; queued" hint with 1h cooldown
- `km destroy`: drain up to 30s, delete queue, delete `km_slack_threads` rows for channel_id
- Bot-loop filter: drop `event.user == bot_user_id`, subtype bot_message/message_changed/message_deleted, event.bot_id present

**Signing secret:** stored at `/km/slack/signing-secret` (new SSM SecureString); captured during `km slack init`

**Phase 66 dependency:** new resources use `cfg.GetResourcePrefix()` (planned in Phase 66-01-PLAN.md; method returns "km" when ResourcePrefix field is empty).

### Claude's Discretion

- GSI vs. table scan with cache for `slack_channel_id` channel→sandbox lookup (GSI preferred)
- Bash poller exact polling cadence (WaitTimeSeconds, retry backoff)
- DDB cache TTL on sandbox poller side (probably 30s for thread lookups)
- Whether to extract poller heredoc to a separate asset file or inline (follow Phase 62/63 precedent — inline)
- Exact wording of "ready" announcement and "paused; queued" hint

### Deferred Ideas (OUT OF SCOPE)

- Mention-based sandbox spawning
- Permission-prompt round-trip via Slack
- Slack interactive features (Block Kit, slash commands, modals)
- Auto-resume of paused sandboxes on inbound activity
- Inbound on shared channel or override-mode channels
- DM delivery, multi-recipient routing
- Block Kit / rich formatting for outbound replies
- CLI flag overrides for `notifySlackInboundEnabled`
- Retroactive inbound on Phase 63 sandboxes
- Turn cancellation / interrupt
- Multi-sandbox cross-talk
- Slack-thread → Claude transcript mirroring
</user_constraints>

---

## Summary

Phase 67 closes the Slack bidirectional loop by adding three new moving parts: (1) a new `POST /events` handler in the existing `km-slack-bridge` Lambda that verifies Slack signing-secret HMAC, deduplicates via the existing nonces table, resolves channel→sandbox via a new DDB GSI, and writes to a per-sandbox SQS FIFO queue; (2) a per-sandbox systemd bash poller (`km-slack-inbound-poller`) that mirrors `km-mail-poller` exactly, performing SQS long-poll → DDB session lookup → `claude -p --resume <session-id>` → DDB session write-back; (3) the SQS queue lifecycle wired into `km create` / `km destroy` SDK calls (not Terraform).

The key research findings are: `--resume` is a real `claude` CLI flag (alias `-r`) and `--output-format json` produces a `session_id` field in the `ResultMessage`; SQS FIFO is not yet in go.mod and requires adding `github.com/aws/aws-sdk-go-v2/service/sqs`; the Slack Events API signing verification is a standard HMAC-SHA256 pattern (~20 lines Go); Phase 66's `cfg.GetResourcePrefix()` is planned but not yet shipped — Phase 67 must treat Phase 66 as a prerequisite or use the fallback pattern `cfg.SandboxTableName`-style with a `GetSlackThreadsTableName()` helper.

**Primary recommendation:** Follow the `km-mail-poller` pattern verbatim for the sandbox poller. Use the existing `bridge.Handler` dependency-injection pattern to add a parallel `EventsHandler` with its own interface set. Add SQS to go.mod as a new SDK dependency.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/sqs` | ~v1.x (latest compatible with aws-sdk-go-v2 v1.41.5) | SQS FIFO CreateQueue, SendMessage, ReceiveMessage, DeleteMessage, ChangeMessageVisibility | Must be added — not yet in go.mod |
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | v1.57.0 (already in go.mod) | km_slack_threads PutItem/Query; sandbox metadata GSI Query | Already used by bridge aws_adapters.go |
| `github.com/aws/aws-sdk-go-v2/service/ssm` | v1.68.3 (already in go.mod) | `/km/slack/signing-secret` SecureString fetch | Already used by SSMBotTokenFetcher |
| `crypto/hmac` + `crypto/sha256` | stdlib | Slack signing-secret verification | Standard Go crypto stdlib; no external dependency needed |
| `encoding/hex` | stdlib | Decode/compare Slack X-Slack-Signature hex digest | Standard Go stdlib |
| AWS CLI (bash, on sandbox) | available via instance profile | SQS ReceiveMessage/DeleteMessage in bash poller | Same pattern as km-mail-poller using aws s3/ssm CLI |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/aws/aws-sdk-go-v2/service/sqs/types` | (bundled with sqs) | SQS type structs (Message, CreateQueueInput, etc.) | All SQS calls in bridge aws_adapters and km create/destroy |
| `log/slog` | stdlib (Go 1.21+) | Structured logging in EventsHandler | Same logger pattern as bridge/handler.go |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| SQS FIFO + poller | Lambda per message | Lambda adds cold-start latency, requires VPC or internet gateway for SSM; FIFO queue + poller is the km-mail-poller precedent |
| DDB km_slack_threads table | In-memory state in poller | Poller is stateless bash; must persist across restarts and pause/resume cycles |
| GSI on sandbox metadata | Full table scan + cache | GSI is O(1) at Lambda invocation cost; scan is O(n) — unacceptable at scale |

**Installation:**
```bash
# Add to go.mod (run from repo root)
go get github.com/aws/aws-sdk-go-v2/service/sqs@latest
```

---

## Architecture Patterns

### Existing Bridge Handler Structure (verified from code)

`pkg/slack/bridge/handler.go` has a single `Handle(ctx, *Request) *Response` method on `Handler`. The `Handler` struct holds five interface fields (Keys, Nonces, Channels, Token, Slack). The `cmd/km-slack-bridge/main.go` wires them during Lambda cold start.

**Phase 67 extension:** Add a parallel `EventsHandler` struct in new file `pkg/slack/bridge/events_handler.go` with its own interface set (SQS sender, DDB thread store, sandbox resolver). The Lambda `main.go` dispatches by path: `rawPath == "/events"` → EventsHandler, otherwise → existing Handler. This keeps the existing handler completely unchanged.

### Pattern 1: Slack Events API Signing Verification

**What:** HMAC-SHA256 of `v0:{X-Slack-Request-Timestamp}:{raw_body}` compared against `X-Slack-Signature: v0=<hex>`. Timestamp must be within ±5 minutes.

**When to use:** Every `POST /events` request before any parsing.

**Example:**
```go
// Source: https://api.slack.com/authentication/verifying-requests-from-slack
import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "strconv"
    "time"
)

func verifySlackSignature(signingSecret, timestamp, body, slackSig string) error {
    ts, err := strconv.ParseInt(timestamp, 10, 64)
    if err != nil {
        return fmt.Errorf("bad timestamp")
    }
    skew := time.Now().Unix() - ts
    if skew < 0 { skew = -skew }
    if skew > 300 {
        return fmt.Errorf("stale timestamp")
    }
    baseString := fmt.Sprintf("v0:%s:%s", timestamp, body)
    mac := hmac.New(sha256.New, []byte(signingSecret))
    mac.Write([]byte(baseString))
    expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
    if !hmac.Equal([]byte(expected), []byte(slackSig)) {
        return fmt.Errorf("bad signature")
    }
    return nil
}
```

### Pattern 2: Slack url_verification Challenge

**What:** Slack sends a one-time `url_verification` event with a `challenge` field. Handler must echo `{"challenge":"<value>"}` back immediately (before signing verification, per Slack spec).

**When to use:** During initial Events API URL configuration in Slack App settings.

```go
type slackChallenge struct {
    Type      string `json:"type"`
    Challenge string `json:"challenge,omitempty"`
}
// In handler: if event.Type == "url_verification", return jsonResp(200, map[string]any{"challenge": event.Challenge})
// IMPORTANT: challenge response must bypass signature verification per Slack docs
```

### Pattern 3: SQS FIFO CreateQueue (at km create)

**What:** Runtime SDK call from `internal/app/cmd/create.go`, not Terraform. Queue name follows `{prefix}-slack-inbound-{sandbox-id}.fifo`.

```go
// Source: AWS SDK v2 SQS documentation
import (
    awssdk "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/sqs"
)

func createInboundQueue(ctx context.Context, sqsClient *sqs.Client, queueName string) (string, error) {
    out, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{
        QueueName: awssdk.String(queueName),
        Attributes: map[string]string{
            "FifoQueue":                 "true",
            "ContentBasedDeduplication": "false", // explicit MessageDeduplicationId (event_id)
            "VisibilityTimeout":         "30",
            "MessageRetentionPeriod":    "1209600", // 14 days in seconds
        },
    })
    if err != nil {
        return "", fmt.Errorf("create SQS queue %s: %w", queueName, err)
    }
    return awssdk.ToString(out.QueueUrl), nil
}
```

**Rollback:** if CreateQueue fails, `km create` aborts same as channel-creation failure (Phase 63 pattern).

### Pattern 4: SQS FIFO SendMessage (bridge Lambda)

**What:** Bridge `EventsHandler` sends inbound Slack message to per-sandbox queue.

```go
// Source: AWS SDK v2 SQS documentation
_, err := sqsClient.SendMessage(ctx, &sqs.SendMessageInput{
    QueueUrl:               awssdk.String(queueURL),
    MessageBody:            awssdk.String(bodyJSON),
    MessageGroupId:         awssdk.String(sandboxID),
    MessageDeduplicationId: awssdk.String(eventID), // Slack event_id — globally unique
})
```

**Confidence:** MEDIUM — API shape from official docs; not yet in codebase to verify locally.

### Pattern 5: km-slack-inbound-poller systemd Unit (mirrors km-mail-poller exactly)

**What:** Inline heredoc in `pkg/compiler/userdata.go`. Conditionally generated when `notifySlackInboundEnabled: true`. Enable/start in the same systemctl line as km-mail-poller.

**Verified from code:** `pkg/compiler/userdata.go:1107-1121` shows km-mail-poller unit template:
```
[Unit]
Description=...
After=network.target
[Service]
User=root
Environment=SANDBOX_ID={{ .SandboxID }}
Environment=KM_ARTIFACTS_BUCKET={{ .KMArtifactsBucket }}
{{ if .AllowedSenders }}Environment=KM_ALLOWED_SENDERS={{ .AllowedSenders }}{{ end }}
ExecStart=/opt/km/bin/km-mail-poller
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
```

km-slack-inbound-poller unit replaces `KM_ARTIFACTS_BUCKET` env with `KM_SLACK_INBOUND_QUEUE_URL` (injected via NotifyEnv template). The env var is already conditionally populated when `notifySlackInboundEnabled: true`.

**Enable/start line** at `pkg/compiler/userdata.go:1667-1672`:
```
systemctl enable km-http-proxy km-audit-log km-tracing{{ if .SandboxEmail }} km-mail-poller{{ end }}
```
Extend to:
```
systemctl enable km-http-proxy km-audit-log km-tracing{{ if .SandboxEmail }} km-mail-poller{{ end }}{{ if .SlackInboundEnabled }} km-slack-inbound-poller{{ end }}
```

### Pattern 6: claude --resume and session_id in output.json

**What:** The claude CLI `--resume` flag (alias `-r`) accepts a session ID UUID. The `--output-format json` result message includes a `session_id` field at the top level.

**Verified from official docs (code.claude.com/docs):**
- `claude -p "prompt" --output-format json --dangerously-skip-permissions --bare --resume <session-id>` — for continuation turns
- `claude -p "prompt" --output-format json --dangerously-skip-permissions --bare` — for first turn (no --resume)
- Output JSON ResultMessage fields: `type`, `subtype`, `result`, `total_cost_usd`, `session_id`, `num_turns`, `duration_ms`, `usage`
- `session_id` is always present in ResultMessage regardless of subtype (success, error_max_turns, etc.)

**Poller extracts session_id:**
```bash
SESSION_ID=$(jq -r '.session_id // empty' "$RUN_DIR/output.json" 2>/dev/null || true)
```

**Important:** `--bare` skips auto-discovery of hooks, skills, plugins, MCP. This keeps agent run fast and deterministic.

### Pattern 7: DDB GSI for channel_id → sandbox lookup

**What:** New GSI on `km_slack_threads` table (or on km-sandboxes sandbox metadata table) keyed by `slack_channel_id`. The bridge EventsHandler queries this GSI to find which sandbox owns a given channel.

**Existing precedent (verified from code):** `infra/modules/dynamodb-sandboxes/v1.0.0/main.tf` already has `alias-index` GSI on `alias` field. Adding `slack_channel_id-index` GSI on `slack_channel_id` (S) follows the same pattern.

**Terraform fragment for new GSI on km-sandboxes:**
```hcl
attribute {
  name = "slack_channel_id"
  type = "S"
}
global_secondary_index {
  name            = "slack_channel_id-index"
  hash_key        = "slack_channel_id"
  projection_type = "ALL"
}
```

This is an ADDITIVE change to `infra/modules/dynamodb-sandboxes/v1.0.0/main.tf` — bump to v1.1.0.

**Bridge Go adapter for GSI query:**
```go
// DDB Query on slack_channel_id-index
out, err := ddbClient.Query(ctx, &dynamodb.QueryInput{
    TableName:              awssdk.String(sandboxesTable),
    IndexName:              awssdk.String("slack_channel_id-index"),
    KeyConditionExpression: awssdk.String("slack_channel_id = :cid"),
    ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
        ":cid": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
    },
    Limit: awssdk.Int32(1),
})
```

New bridge interface: `SandboxByChannelFetcher.FetchBySandboxChannel(ctx, channelID) (sandboxID, queueURL string, err error)`.

### Pattern 8: Config Table Name Pattern (Phase 66 dependency)

**What:** Phase 66 plans `cfg.GetResourcePrefix()` (returns "km" fallback). This helper is NOT yet shipped. Phase 67 must handle this gracefully.

**Current pattern (verified from codebase):** `internal/app/config/config.go` has individual fields per table (`SandboxTableName`, `BudgetTableName`, etc.) each defaulting to hardcoded strings via `v.SetDefault(...)`. Call-sites use inline fallback: `if cfg.SandboxTableName == "" { t = "km-sandboxes" }`.

**Phase 67 approach:** Add `SlackThreadsTableName` field to `Config` struct and `slack_threads_table_name` key to config/merge list, defaulting to `"km-slack-threads"`. Add `GetSlackThreadsTableName()` helper following the same pattern. This is forward-compatible with Phase 66 migration.

**Also add to bridge Lambda env var:** `KM_SLACK_THREADS_TABLE` (default `"km-slack-threads"`) and `KM_SIGNING_SECRET_PATH` (default `"/km/slack/signing-secret"`).

### Recommended Project Structure (new files)

```
pkg/slack/bridge/
├── events_handler.go          # NEW: Slack Events API handler (signing verify, dispatch)
├── events_handler_test.go     # NEW: handler tests with mock SQS/DDB
├── events_interfaces.go       # NEW: SQS sender + thread store + sandbox resolver interfaces
├── handler.go                 # EXISTING: unchanged (POST / route)
├── aws_adapters.go            # EXISTING: add SQS adapter + thread store adapter + sandbox-by-channel adapter
cmd/km-slack-bridge/
└── main.go                    # MODIFY: dispatch /events to EventsHandler
internal/app/cmd/
├── create.go                  # MODIFY: SQS queue creation in Slack block
├── create_slack.go            # MODIFY: expose SQS queue URL from createSQSQueue()
├── destroy.go                 # MODIFY: drain + SQS delete + DDB cleanup
pkg/compiler/
└── userdata.go                # MODIFY: poller script + systemd unit + env var
pkg/profile/
├── types.go                   # MODIFY: notifySlackInboundEnabled field
└── validate.go                # MODIFY: three validation rules
infra/modules/
├── dynamodb-sandboxes/v1.1.0/ # NEW: adds slack_channel_id GSI
└── dynamodb-slack-threads/    # NEW: km_slack_threads table module
infra/live/.../management/
└── dynamodb/terragrunt.hcl   # MODIFY: add km_slack_threads instance
```

### Anti-Patterns to Avoid

- **Putting SQS queue URL in Terraform output:** queue is per-sandbox runtime lifecycle (SDK at create), not Terraform. Same rule as Phase 63 Slack channels.
- **Blocking on SQS SendMessage in bridge EventsHandler:** Slack requires 200 in <3s. All SQS/DDB writes must complete synchronously but fast (<1s typical); no retry loops inside the handler.
- **Using SQS Standard queue:** out-of-order delivery causes incorrect `thread_ts` on session-lookup; FIFO is mandatory.
- **Calling `DeleteMessage` before `km agent run` completes:** message must return to queue on failure; only delete after successful turn capture.
- **Hardcoding table name `km_slack_threads` without config field:** breaks Phase 66 multi-instance support.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Slack request signing | Custom crypto | `crypto/hmac` + `crypto/sha256` stdlib | Standard; timing-safe `hmac.Equal` |
| SQS FIFO queue lifecycle | Custom queue abstraction | Direct `sqs.Client` SDK calls | SDK handles FIFO attributes, dedup, retention |
| Session continuity | Custom transcript storage | `claude --resume <session_id>` | Claude CLI handles JSONL persistence locally on sandbox |
| DDB event dedup | Second nonce table | Existing `km_slack_bridge_nonces` | Already provisioned, same TTL pattern works for event_id strings |
| Bot user_id lookup | Hardcoded user_id | `auth.test` API response cached at Lambda warm time | User_id changes on token rotation; must fetch dynamically |

---

## Common Pitfalls

### Pitfall 1: Visibility Timeout vs. Agent Run Duration

**What goes wrong:** Default 30s VisibilityTimeout. `km agent run` for a multi-file task can take 60-120s. SQS makes the message visible again after 30s, a second poller loop picks it up, and a duplicate turn fires.

**Why it happens:** FIFO queue + single-threaded poller prevents *concurrent* processing but not *timed-out* re-delivery within the same poller loop.

**How to avoid:** Poller extends visibility BEFORE invoking `km agent run`. Extend to 300s (5min) initially:
```bash
RECEIPT_HANDLE=$(echo "$MSG" | jq -r '.ReceiptHandle')
aws sqs change-message-visibility \
  --queue-url "$KM_SLACK_INBOUND_QUEUE_URL" \
  --receipt-handle "$RECEIPT_HANDLE" \
  --visibility-timeout 300
```
Then invoke `km agent run --wait`. Only call `DeleteMessage` after run exits.

**Warning signs:** Duplicate Claude responses in Slack thread; DDB `turn_count` increments by 2 for a single message.

### Pitfall 2: Bridge Lambda Cold Start Delays SQS Write

**What goes wrong:** Lambda cold start can take 1-2s. Slack requires 200 in <3s. If DDB lookups + SQS write take >2s combined, Slack retries the event (with new event_id — NOT deduplicated by the existing one).

**Why it happens:** DDB scan + SQS write in series; cold-start Lambda initialization.

**How to avoid:** The GSI query + conditional DDB PutItem + SQS SendMessage should each take <300ms at p99. Total hot path: <1s. Use Go context with 2.5s deadline for the combined writes; return 200 even if DDB thread-upsert times out (non-fatal — poller will handle session lookup independently). SQS write is the critical path; DDB thread-row creation can be best-effort.

**Warning signs:** Slack shows "event delivery failed" in App Event Subscriptions; `km_slack_threads` shows duplicate rows for the same `(channel_id, thread_ts)`.

### Pitfall 3: Bot-Loop Self-Message Delivery

**What goes wrong:** When Claude replies via `km-notify-hook` → `km-slack post`, Slack delivers a `message` event to the `/events` endpoint with `event.user == <bot_user_id>`. Without the bot-loop filter, this creates an infinite loop.

**Why it happens:** Slack delivers ALL messages to the subscribed Events API URL, including messages posted by the bot itself.

**How to avoid:** Filter at bridge handler, BEFORE SQS write:
1. `event.bot_id != ""` — any bot message
2. `event.subtype` is `"bot_message"`, `"message_changed"`, `"message_deleted"` 
3. `event.user == cachedBotUserID` — own message (fetch via `auth.test` at cold start, cache for Lambda lifetime)

**Warning signs:** Slack channel fills with repetitive Claude responses; SQS queue depth grows unboundedly.

### Pitfall 4: Signing Secret vs. Bot Token Confusion

**What goes wrong:** The Slack signing secret (for verifying Events API webhooks) is different from the bot token (for posting messages). They're stored at different SSM paths.

**Why it happens:** Phase 63 added bot token (`/km/slack/bot-token`) but signing secret is new in Phase 67 (`/km/slack/signing-secret`). Confusing them causes either signature verification failures (using bot token as signing secret) or token exposure (passing signing secret to chat.postMessage).

**How to avoid:** 
- `/km/slack/bot-token` (SecureString, KMS): Slack API calls only (SSMBotTokenFetcher, unchanged)
- `/km/slack/signing-secret` (SecureString, KMS): HMAC verification in EventsHandler only (new SSMSigningSecretFetcher, same cache pattern as SSMBotTokenFetcher)
- `km slack init` extension captures both: bot token (existing) and signing secret (new prompt)

### Pitfall 5: session_id Missing from output.json

**What goes wrong:** Poller tries to extract `session_id` from output.json but gets empty string because `claude -p --output-format json` failed or wrote to stderr.log instead.

**Why it happens:** If claude exits non-zero, output.json may be empty or contain an error message, not valid JSON.

**How to avoid:**
```bash
# Capture exit code
claude -p "$PROMPT" --output-format json --dangerously-skip-permissions --bare \
  ${RESUME_ARG} > "$RUN_DIR/output.json" 2>"$RUN_DIR/stderr.log"
RUN_EXIT=$?

SESSION_ID=""
if [ $RUN_EXIT -eq 0 ] && [ -s "$RUN_DIR/output.json" ]; then
  SESSION_ID=$(jq -r '.session_id // empty' "$RUN_DIR/output.json" 2>/dev/null || true)
fi

# Only write DDB + DeleteMessage if session_id captured (success path)
if [ -n "$SESSION_ID" ]; then
  # DDB write-back, then DeleteMessage
else
  # Log failure; let message return to queue via VisibilityTimeout
  echo "[km-slack-inbound-poller] WARN: agent run failed (exit $RUN_EXIT), message will retry"
fi
```

### Pitfall 6: DDB Write Race Between Bridge and Poller

**What goes wrong:** Bridge writes `km_slack_threads` row (creating if absent) on inbound. Poller reads and writes back `claude_session_id` after agent run. Two rapid Slack messages in the same thread cause two concurrent bridge writes and potential lost write on poller side.

**Why it happens:** DDB `PutItem` without condition; second message overwrites first.

**How to avoid:** 
- Bridge writes with `attribute_not_exists(channel_id)` condition for new rows (idempotent on replay)
- Bridge updates existing rows with `SET last_turn_ts = :ts IF last_turn_ts < :ts` conditional update
- Poller writes with `SET claude_session_id = :sid, last_turn_ts = :ts, turn_count = turn_count + 1`
- FIFO queue serializes consumption so concurrent race is prevented at the poller side; bridge writes are idempotent inserts, not updates to claude_session_id

### Pitfall 7: Phase 66 GetResourcePrefix() Not Yet Available

**What goes wrong:** CONTEXT.md says "must use `cfg.GetResourcePrefix()`" but Phase 66 hasn't shipped yet. Referencing an undefined method will break compilation.

**Why it happens:** Phase 66 is planned but its plans describe adding `GetResourcePrefix()` to `Config` struct — this is not yet in the codebase.

**How to avoid:** Add the minimal Phase-66-compatible pattern in Phase 67 itself: add `ResourcePrefix string` field to Config (if not already present from Phase 66), add `GetResourcePrefix()` method (returns "km" fallback), add `GetSlackThreadsTableName()` helper. Document that Phase 66 will migrate existing hardcoded names. Phase 67 touches SQS queue naming and `km_slack_threads` table — use the helpers from day one.

---

## Code Examples

### Slack HMAC Signing Verification (stdlib, no external deps)

```go
// Source: https://api.slack.com/authentication/verifying-requests-from-slack
// pkg/slack/bridge/events_handler.go

func verifySlackRequest(signingSecret, tsHeader, rawBody, sigHeader string) error {
    ts, err := strconv.ParseInt(tsHeader, 10, 64)
    if err != nil || tsHeader == "" {
        return fmt.Errorf("events: missing or invalid X-Slack-Request-Timestamp")
    }
    skew := time.Now().Unix() - ts
    if skew < 0 { skew = -skew }
    if skew > 300 {
        return fmt.Errorf("events: stale timestamp (%ds)", skew)
    }
    baseStr := "v0:" + tsHeader + ":" + rawBody
    mac := hmac.New(sha256.New, []byte(signingSecret))
    mac.Write([]byte(baseStr))
    computed := "v0=" + hex.EncodeToString(mac.Sum(nil))
    if !hmac.Equal([]byte(computed), []byte(sigHeader)) {
        return fmt.Errorf("events: signature mismatch")
    }
    return nil
}
```

### SQS FIFO Queue Naming

```go
// internal/app/cmd/create.go or create_slack.go
func inboundQueueName(cfg *config.Config, sandboxID string) string {
    prefix := cfg.GetResourcePrefix() // returns "km" fallback
    return fmt.Sprintf("%s-slack-inbound-%s.fifo", prefix, sandboxID)
}
```

### Bash Poller Session Continuity (core loop)

```bash
# /opt/km/bin/km-slack-inbound-poller (inline in userdata.go heredoc)
#!/bin/bash
QUEUE_URL="${KM_SLACK_INBOUND_QUEUE_URL}"
SANDBOX_ID="${KM_SANDBOX_ID}"
REGION="${AWS_REGION:-us-east-1}"

[ -z "$QUEUE_URL" ] && echo "[km-slack-inbound-poller] KM_SLACK_INBOUND_QUEUE_URL not set, exiting" && exit 0

while true; do
  MSG=$(aws sqs receive-message \
    --queue-url "$QUEUE_URL" \
    --wait-time-seconds 20 \
    --max-number-of-messages 1 \
    --region "$REGION" \
    --output json 2>/dev/null || true)

  BODY=$(echo "$MSG" | jq -r '.Messages[0].Body // empty' 2>/dev/null || true)
  RECEIPT=$(echo "$MSG" | jq -r '.Messages[0].ReceiptHandle // empty' 2>/dev/null || true)

  [ -z "$BODY" ] && continue

  CHANNEL=$(echo "$BODY" | jq -r '.channel')
  THREAD_TS=$(echo "$BODY" | jq -r '.thread_ts')
  TEXT=$(echo "$BODY" | jq -r '.text')

  # Extend visibility before agent run (prevents re-delivery during long runs)
  aws sqs change-message-visibility \
    --queue-url "$QUEUE_URL" \
    --receipt-handle "$RECEIPT" \
    --visibility-timeout 300 \
    --region "$REGION" 2>/dev/null || true

  # DDB lookup for existing session
  DDB_ITEM=$(aws dynamodb get-item \
    --table-name "${KM_SLACK_THREADS_TABLE:-km-slack-threads}" \
    --key "{\"channel_id\":{\"S\":\"$CHANNEL\"},\"thread_ts\":{\"S\":\"$THREAD_TS\"}}" \
    --output json 2>/dev/null || true)
  CLAUDE_SESSION=$(echo "$DDB_ITEM" | jq -r '.Item.claude_session_id.S // empty' 2>/dev/null || true)

  RUN_ID=$(date -u +%Y%m%dT%H%M%SZ)
  RUN_DIR="/workspace/.km-agent/runs/$RUN_ID"
  mkdir -p "$RUN_DIR"

  RESUME_ARG=""
  [ -n "$CLAUDE_SESSION" ] && RESUME_ARG="--resume $CLAUDE_SESSION"

  PROMPT_FILE=$(mktemp)
  echo "$TEXT" > "$PROMPT_FILE"

  export KM_SLACK_THREAD_TS="$THREAD_TS"

  sudo -u sandbox bash -c "
    export KM_SLACK_THREAD_TS='$THREAD_TS'
    claude -p \"\$(cat '$PROMPT_FILE')\" --output-format json \
      --dangerously-skip-permissions --bare $RESUME_ARG \
      > '$RUN_DIR/output.json' 2>'$RUN_DIR/stderr.log'
    echo \$? > '$RUN_DIR/exit_code'
  "
  rm -f "$PROMPT_FILE"

  RUN_EXIT=$(cat "$RUN_DIR/exit_code" 2>/dev/null || echo 1)
  NEW_SESSION=""
  if [ "$RUN_EXIT" -eq 0 ] && [ -s "$RUN_DIR/output.json" ]; then
    NEW_SESSION=$(jq -r '.session_id // empty' "$RUN_DIR/output.json" 2>/dev/null || true)
  fi

  if [ -n "$NEW_SESSION" ]; then
    NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    aws dynamodb put-item \
      --table-name "${KM_SLACK_THREADS_TABLE:-km-slack-threads}" \
      --item "{
        \"channel_id\":{\"S\":\"$CHANNEL\"},
        \"thread_ts\":{\"S\":\"$THREAD_TS\"},
        \"claude_session_id\":{\"S\":\"$NEW_SESSION\"},
        \"sandbox_id\":{\"S\":\"$SANDBOX_ID\"},
        \"last_turn_ts\":{\"S\":\"$NOW\"}
      }" 2>/dev/null || true

    aws sqs delete-message \
      --queue-url "$QUEUE_URL" \
      --receipt-handle "$RECEIPT" \
      --region "$REGION" 2>/dev/null || true
  else
    echo "[km-slack-inbound-poller] WARN: agent run failed (exit $RUN_EXIT), message returns to queue"
  fi
done
```

### EventsHandler Interfaces (new file `events_interfaces.go`)

```go
// pkg/slack/bridge/events_interfaces.go

// SQSSender sends a message to a FIFO SQS queue.
type SQSSender interface {
    Send(ctx context.Context, queueURL, body, groupID, deduplicationID string) error
}

// SlackThreadStore reads and writes km_slack_threads DDB table.
type SlackThreadStore interface {
    Get(ctx context.Context, channelID, threadTS string) (claudeSessionID string, err error)
    Upsert(ctx context.Context, channelID, threadTS, sandboxID string) error
}

// SandboxByChannelFetcher resolves Slack channel_id to sandbox metadata.
type SandboxByChannelFetcher interface {
    FetchByChannel(ctx context.Context, channelID string) (sandboxID, queueURL string, paused bool, err error)
}

// SigningSecretFetcher returns the Slack signing secret (cached like SSMBotTokenFetcher).
type SigningSecretFetcher interface {
    Fetch(ctx context.Context) (string, error)
}

// BotUserIDFetcher returns the bot's own user_id for bot-loop filter.
type BotUserIDFetcher interface {
    Fetch(ctx context.Context) (string, error)
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `claude --resume` not wired in km | `claude --resume <session_id>` (CLI flag `-r`) is documented as production-ready | Claude Code SDK docs (2025-2026) | Poller can pass `--resume` directly to `claude -p` invocation |
| Session files local to process | session files at `~/.claude/projects/<cwd>/*.jsonl` on sandbox disk | Persistent across tmux/SSM disconnects | Poller invocations naturally resume because same sandbox user, same cwd |
| SQS not in go.mod | Must add `github.com/aws/aws-sdk-go-v2/service/sqs` | New for Phase 67 | `go get` required; compatible with existing aws-sdk-go-v2 v1.41.5 root module |
| Phase 63 thread_ts field unused | Phase 67 finally consumes `thread_ts` in km-notify-hook (flag already wired in `cmd/km-slack/main.go:47`) | Phase 67 ships it | No code change needed in km-slack binary; env var `KM_SLACK_THREAD_TS` just needs to be set by poller |

**Deprecated / outdated:**
- `--resume` flag was `--continue-session` in very early Claude Code versions — confirmed current flag is `--resume` / `-r` (verified in official docs)

---

## Open Questions

1. **claude --resume with --bare flag interaction**
   - What we know: `--bare` skips hook/skills auto-discovery; `--resume` restores session JSONL. Both flags are documented as compatible.
   - What's unclear: Does `--bare` suppress the session persistence write? If so, the session_id from output.json won't have a matching JSONL on next run.
   - Recommendation: Test in a real sandbox before shipping. If bare+resume doesn't persist, drop `--bare` for resume turns (slight startup cost). The CONTEXT.md script already uses `--bare` in the existing `BuildAgentShellCommands` — maintain consistency.

2. **SQS queue ARN for IAM policy in sandbox instance role**
   - What we know: IAM policy needs `arn:aws:sqs:*:*:{prefix}-slack-inbound-{sandbox-id}.fifo`. This wildcard pattern needs to be in the Terraform IAM role module for EC2 instances.
   - What's unclear: Does the current sandbox EC2 IAM role module accept a list of additional policy statements? Where is the instance role IAM policy defined in `infra/modules/`?
   - Recommendation: Planner should grep for `aws_iam_role_policy` in EC2 sandbox infra modules and verify the attachment pattern before writing Wave 2C tasks.

3. **`km_slack_threads` table TTL field name**
   - What we know: CONTEXT.md says TTL = 30 days from `last_turn_ts`. `last_turn_ts` is stored as ISO8601 string.
   - What's unclear: DDB TTL requires a Number attribute (Unix epoch). Need a separate `ttl_expiry` (N) attribute updated alongside `last_turn_ts`.
   - Recommendation: Terraform module should define `ttl { attribute_name = "ttl_expiry" enabled = true }`. Poller and bridge write `ttl_expiry = now() + 30*24*3600`.

4. **auth.test for bot user_id caching in EventsHandler**
   - What we know: `auth.test` Slack API returns `user_id` for the bot. Cache at Lambda warm time. Token rotation (Phase 63.1) force-cold-starts bridge Lambda, which re-fetches.
   - What's unclear: If signing secret is rotated separately (new Phase 67 concern), does `km slack rotate-token` also need to cover the signing secret? Or is that a separate rotation command?
   - Recommendation: Treat signing-secret rotation as out-of-scope for Phase 67 (same as bot-token rotation was deferred to Phase 63.1). Document the manual rotation path: update SSM, force Lambda cold start.

---

## Validation Architecture

> `workflow.nyquist_validation` is `true` in `.planning/config.json` — section is REQUIRED.

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` package (standard) |
| Config file | none — `go test ./...` from repo root |
| Quick run command | `go test ./pkg/slack/bridge/... ./pkg/profile/... ./pkg/compiler/... ./internal/app/cmd/... -run TestSlack -count=1` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| SLCK-INBOUND-01 | `notifySlackInboundEnabled` validation rules | unit | `go test ./pkg/profile/... -run TestValidate_Slack -count=1` | ❌ Wave 0 |
| SLCK-INBOUND-02 | Compiler: poller script + systemd unit generated when enabled | unit | `go test ./pkg/compiler/... -run TestUserdata_SlackInbound -count=1` | ❌ Wave 0 |
| SLCK-INBOUND-03 | Compiler: no poller when disabled | unit | same file | ❌ Wave 0 |
| SLCK-INBOUND-04 | Bridge `/events` handler: valid message → SQS write + DDB upsert | unit | `go test ./pkg/slack/bridge/... -run TestEventsHandler -count=1` | ❌ Wave 0 |
| SLCK-INBOUND-05 | Bridge `/events`: bad signing secret → 401 | unit | same file | ❌ Wave 0 |
| SLCK-INBOUND-06 | Bridge `/events`: stale timestamp → 401 | unit | same file | ❌ Wave 0 |
| SLCK-INBOUND-07 | Bridge `/events`: url_verification challenge echo | unit | same file | ❌ Wave 0 |
| SLCK-INBOUND-08 | Bridge `/events`: bot self-message filtered | unit | same file | ❌ Wave 0 |
| SLCK-INBOUND-09 | Bridge `/events`: replayed event_id → 200, no SQS write | unit | same file | ❌ Wave 0 |
| SLCK-INBOUND-10 | Bridge `/events`: unknown channel → 200 + log warning | unit | same file | ❌ Wave 0 |
| SLCK-INBOUND-11 | km create: SQS queue created, URL in DDB, env var injected | unit | `go test ./internal/app/cmd/... -run TestCreate_SlackInbound -count=1` | ❌ Wave 0 |
| SLCK-INBOUND-12 | km create: SQS failure → rollback | unit | same file | ❌ Wave 0 |
| SLCK-INBOUND-13 | km destroy: drain + queue delete + thread cleanup | unit | `go test ./internal/app/cmd/... -run TestDestroy_SlackInbound -count=1` | ❌ Wave 0 |
| SLCK-INBOUND-14 | km doctor: stale queue detection | unit | `go test ./internal/app/cmd/... -run TestDoctor_SlackInbound -count=1` | ❌ Wave 0 |
| SLCK-INBOUND-E2E | Full round-trip: Slack message → Claude response in-thread | e2e (manual) | `RUN_SLACK_E2E=1 go test ./test/e2e/slack/... -run TestSlackInbound -count=1 -timeout 120s` | ❌ Wave 0 |

### Sampling Rate

- **Per task commit:** `go test ./pkg/slack/bridge/... ./pkg/profile/... -run TestSlack -count=1`
- **Per wave merge:** `go test ./pkg/... ./internal/app/cmd/... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/slack/bridge/events_handler_test.go` — covers SLCK-INBOUND-04..10 (bridge Events handler)
- [ ] `pkg/profile/validate_slack_inbound_test.go` (or extension to `validate_test.go`) — covers SLCK-INBOUND-01
- [ ] `pkg/compiler/userdata_slack_inbound_test.go` — covers SLCK-INBOUND-02..03
- [ ] `internal/app/cmd/create_slack_inbound_test.go` — covers SLCK-INBOUND-11..12
- [ ] `internal/app/cmd/destroy_slack_inbound_test.go` — covers SLCK-INBOUND-13
- [ ] `internal/app/cmd/doctor_slack_inbound_test.go` — covers SLCK-INBOUND-14
- [ ] Framework install: `go get github.com/aws/aws-sdk-go-v2/service/sqs` — required before any SQS code compiles

---

## Sources

### Primary (HIGH confidence)

- Codebase: `pkg/slack/bridge/handler.go` — existing Handler struct, interface pattern, response shapes
- Codebase: `pkg/slack/bridge/aws_adapters.go` — DynamoDB adapter pattern (GetItem, PutItem, conditional write), SSMBotTokenFetcher 15-min cache pattern
- Codebase: `pkg/slack/bridge/interfaces.go` — interface definitions for dependency injection in tests
- Codebase: `pkg/compiler/userdata.go:739-856,1107-1121,1667-1672` — km-mail-poller script, systemd unit template, systemctl enable/start line
- Codebase: `internal/app/cmd/create_slack.go` — resolveSlackChannel flow, per-sandbox channel creation at runtime, injectSlackEnvIntoSandbox pattern
- Codebase: `internal/app/cmd/agent.go:1162-1263` — BuildAgentShellCommands, claude -p --output-format json --dangerously-skip-permissions --bare invocation, /workspace/.km-agent/runs/ output path
- Codebase: `internal/app/config/config.go` — Config struct pattern, individual table name fields + defaults, viper merge list
- Codebase: `infra/modules/dynamodb-sandboxes/v1.0.0/main.tf` — GSI pattern (alias-index), attribute definition pattern
- Codebase: `infra/modules/dynamodb-slack-nonces/v1.0.0/main.tf` — nonce table pattern (TTL on ttl_expiry N attribute)
- Codebase: `cmd/km-slack-bridge/main.go` — Lambda cold start, env var wiring, events.LambdaFunctionURLRequest dispatch
- Codebase: `.planning/todos/resolved/km-agent-claude-residual-shell-on-exit.md` — confirms `claude --resume <uuid>` is the real flag
- Codebase: `.planning/phases/66-multi-instance-support-configurable-resource-prefix-and-email-subdomain/66-RESEARCH.md` — planned `cfg.GetResourcePrefix()` method signature, fallback to "km"
- Official docs: `https://code.claude.com/docs/en/cli-reference` — `--resume` / `-r` flag, `--output-format json`, `session_id` in ResultMessage
- Official docs: `https://code.claude.com/docs/en/agent-sdk/sessions` — session_id field on ResultMessage, resume patterns
- Official docs: `https://code.claude.com/docs/en/agent-sdk/agent-loop` — ResultMessage fields: `result`, `session_id`, `total_cost_usd`, `subtype`

### Secondary (MEDIUM confidence)

- Slack API docs (via web search): `https://api.slack.com/authentication/verifying-requests-from-slack` — HMAC-SHA256 signing pattern, url_verification challenge
- Slack Events API scopes: `channels:history`, `groups:history` required for inbound (confirmed multiple sources)
- AWS SDK v2 SQS FIFO: CreateQueue attributes pattern — `FifoQueue=true`, `ContentBasedDeduplication=false`, `VisibilityTimeout`, `MessageRetentionPeriod`; `MessageGroupId` + `MessageDeduplicationId` on SendMessage

### Tertiary (LOW confidence — needs validation in real sandbox)

- Interaction between `claude --bare` and `--resume` session persistence — needs sandbox test

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH (all library versions verified from go.mod; SQS is new addition, API shapes from official docs)
- Architecture: HIGH (handler pattern, DDB adapter pattern, systemd template all verified from codebase)
- Pitfalls: HIGH (VisibilityTimeout/agent-run duration race is design-level; bot-loop filter is documented in CONTEXT.md; Phase 66 dependency is verified from config.go grep)
- claude --resume: HIGH (verified from official Claude Code CLI docs and project todo)
- SQS FIFO SDK calls: MEDIUM (correct per official docs; not yet in codebase to verify compile-time)

**Research date:** 2026-05-02
**Valid until:** 2026-06-01 (30 days; SQS SDK API is stable; Claude CLI --resume flag is stable)
