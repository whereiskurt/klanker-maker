# Phase 71: Agent Playbook Orchestration — Research

**Researched:** 2026-05-05
**Domain:** Go CLI, AWS Lambda, SQS FIFO, DynamoDB, systemd, sandbox-side sidecar
**Confidence:** HIGH — all findings verified against actual source files in this repo

## Summary

Phase 71 adds a YAML playbook primitive on top of existing Phase 67 infrastructure. The SPEC is unusually complete: architecture decisions are locked, the three DDB tables are specified, the runner sidecar algorithm is written out step by step, and the operator UX (CLI surface, km at extension) is fully described. Research focus is therefore implementation-detail verification: exact file locations, existing IAM coverage, patterns to mirror, and pitfalls the SPEC underestimates.

The good news: Phase 67's `km-slack-inbound-poller` (embedded in `pkg/compiler/userdata.go` lines 1152–1391) is a complete, shipping reference for every subprocess the runner must perform. The session_id capture pattern (`jq -r '.session_id // empty' output.json`), the SSM queue-URL retry/backoff, the SQS visibility-timeout extension before a long claude run, the systemd EnvironmentFile gotcha, and the `claude -p --output-format json --dangerously-skip-permissions $RESUME_ARG` invocation are all there and verified. The runner can be modeled line-for-line on the poller.

The less-good news: the SPEC declares "content-addressed S3 storage" and "ULID run_id" without acknowledging that (a) no SHA-256 content-addressing helper exists in the codebase yet, and (b) no ULID library is in go.mod — the existing agent-run pattern uses `time.Now().UTC().Format("20060102T150405Z")` for run IDs. Both are small gaps but the planner must allocate tasks for them. The TTL Lambda also needs new SQS and DynamoDB permissions in its IAM module before the `playbook-run` handler can call `sqs:SendMessage` or write to the three new tables.

**Primary recommendation:** Mirror Phase 67 exactly. Every novel piece (queue provisioning, SSM write, userdata template conditional, systemd unit, destroy cleanup, doctor check) has a working Phase 67 analogue. Deviate only where playbooks differ from Slack inbound by design (FIFO MessageGroupId is `playbook:name` not `sandbox-id`, runner walks multiple steps per message, session map key is `(playbook, sandbox)` not `(channel, thread_ts)`).

---

## 1. Phase 67 Reference Patterns (km-slack-inbound-poller)

### Entry point and shape

The poller is **not a Go binary** — it is a bash script embedded in `pkg/compiler/userdata.go` as a heredoc (lines 1152–1391). The SPEC says the runner is "shaped exactly like km-slack-inbound-poller" — this means the runner is also a bash script embedded in userdata.go, **not a Go sidecar binary**. (The SPEC's `sidecars/playbook-runner/` notation is aspirational; the prior art is a bash heredoc. The planner should clarify: bash heredoc in userdata vs. Go binary. Both are valid but the Phase 67 precedent is bash. If a Go binary is chosen, it needs a Makefile sidecar target and S3 upload flow like km-slack. This research recommends bash heredoc to match Phase 67 and minimize risk.)

### SQS receive loop with visibility-timeout extension

```bash
# userdata.go:1246–1276
MSG=$(aws sqs receive-message \
  --queue-url "$QUEUE_URL" \
  --wait-time-seconds 20 \
  --max-number-of-messages 1 \
  --region "$REGION" \
  --output json 2>/dev/null || true)
# ...
# Extend visibility BEFORE agent run (Pitfall 1 per source comment):
aws sqs change-message-visibility \
  --queue-url "$QUEUE_URL" \
  --receipt-handle "$RECEIPT" \
  --visibility-timeout 300 \
  --region "$REGION" 2>/dev/null || true
```

The poller extends to 300 s once before the claude invocation. The SPEC calls for 60-second slice extension during long-running steps. The difference: Slack inbound expects O(minutes) turns; playbook steps could be O(tens of minutes) for complex audit steps. The planner should specify a background extension loop (e.g., while claude is running, `sleep 45; change-message-visibility 60` in a subshell) rather than a single pre-extend. The slack-inbound approach is adequate for Slack but undershoots for playbooks.

### Queue URL from SSM (SCP workaround)

```bash
# userdata.go:1180–1194
PARAM_NAME="/sandbox/${SANDBOX_ID}/slack-inbound-queue-url"
if [ -z "$QUEUE_URL" ]; then
  for attempt in 1 2 3 4 5 6 7 8 9 10; do
    QUEUE_URL=$(aws ssm get-parameter --name "$PARAM_NAME" --region "$REGION" \
      --query 'Parameter.Value' --output text 2>/dev/null || true)
    [ -n "$QUEUE_URL" ] && [ "$QUEUE_URL" != "None" ] && break
    QUEUE_URL=""
    sleep 30
  done
fi
```

The SSM parameter is written by `internal/app/cmd/create_slack_inbound.go:129` at km create time:
```
paramName := "/sandbox/" + deps.SandboxID + "/slack-inbound-queue-url"
```

For playbooks: use `/sandbox/{id}/playbook-queue-url`. Same 10-attempt, 30s-sleep retry. The same org-level SCP that blocks SSM SendCommand applies here — the planner must not allow env var direct injection and must specify the SSM-read fallback.

### Session_id capture from `claude -p` output

```bash
# userdata.go:1313–1323
sudo -u sandbox bash -c "
  set -a; for f in /etc/profile.d/*.sh; do source \"\$f\" 2>/dev/null || true; done; set +a
  export KM_SLACK_THREAD_TS='$THREAD_TS'
  claude -p \"\$(cat '$PROMPT_FILE')\" --output-format json \
    --dangerously-skip-permissions $RESUME_ARG \
    > '$RUN_DIR/output.json' 2>'$RUN_DIR/stderr.log'
  echo \$? > '$RUN_DIR/exit_code'
" || true
# ...
NEW_SESSION=$(jq -r '.session_id // empty' "$RUN_DIR/output.json" 2>/dev/null || true)
```

Key detail: the poller uses `--output-format json` **without `--bare`**. The TTL agent-run handler (ttl-handler/main.go:625) uses `--output-format json --bare`. The `.session_id` field appears in both shapes, but `--bare` suppresses system prompt output. For the playbook runner, use the same invocation as the slack-inbound-poller (no `--bare`) so the output is consistent with the session capture pattern.

Confirmed: `claude -p --output-format json` emits a JSON object with `.session_id` on every invocation. Subsequent `--resume <id>` reuses the same session. The poller reads the new session_id from the previous invocation's output.json and passes it as `--resume $NEW_SESSION` on the next turn.

### MessageGroupId

The slack-inbound poller writes messages to the queue from the bridge Lambda with `MessageGroupId=sandbox-id` (all Slack messages for a sandbox serialize within that group). For playbooks, SPEC specifies `MessageGroupId=playbook:{playbook_name}` — different playbooks on the same sandbox run in parallel, same playbook runs serialize. This is the correct design and the SQS FIFO semantics support it.

The FIFO queue's `ContentBasedDeduplication=false` (confirmed in `pkg/aws/sqs.go:63`) means the caller supplies `MessageDeduplicationId`. For playbooks this should be the `run_id` (which the TTL Lambda mints as a ULID/timestamp).

---

## 2. `km at` Extension Surface

### schedulableCommands map (at.go:40–50)

```go
var schedulableCommands = map[string]schedulableCommand{
    "create":     {targetARNField: "create"},
    "destroy":    {targetARNField: "ttl", eventType: "destroy"},
    "kill":       {targetARNField: "ttl", eventType: "destroy"},
    "stop":       {targetARNField: "ttl", eventType: "stop"},
    "pause":      {targetARNField: "ttl", eventType: "stop"},
    "resume":     {targetARNField: "ttl", eventType: "resume"},
    "extend":     {targetARNField: "ttl", eventType: "extend"},
    "budget-add": {targetARNField: "ttl", eventType: "budget-add"},
    "agent-run":  {targetARNField: "ttl", eventType: "agent-run"},
}
```

**Add one entry:**
```go
"playbook-run": {targetARNField: "ttl", eventType: "playbook-run"},
```

### Two-word command merge (at.go:143–146)

```go
// Merge two-word commands: "agent run" → "agent-run"
if cmdArg == "agent" && len(extraArgs) > 0 && extraArgs[0] == "run" {
    cmdArg = "agent-run"
    extraArgs = extraArgs[1:]
}
```

**Add parallel block immediately after:**
```go
if cmdArg == "playbook" && len(extraArgs) > 0 && extraArgs[0] == "run" {
    cmdArg = "playbook-run"
    extraArgs = extraArgs[1:]
}
```

### buildTargetInput extension (at.go:405, currently lines 486–504)

The `agent-run` branch parses `--prompt`, `--no-bedrock`, `--auto-start` from extraArgs. The `playbook-run` branch needs to parse `--sandbox` (the sandbox-id) and optionally `--s3-uri` or derive the s3_uri from the DDB table. The minimal shape:

```go
if cmdArg == "playbook-run" {
    detail["playbook"] = extraArgs[0] // playbook name (required)
    for i := 1; i < len(extraArgs); i++ {
        switch extraArgs[i] {
        case "--sandbox":
            if i+1 < len(extraArgs) { detail["sandbox_id"] = extraArgs[i+1]; i++ }
        }
    }
    if _, ok := detail["playbook"]; !ok {
        return "", fmt.Errorf("playbook-run requires a playbook name")
    }
}
```

Note: The Lambda resolves the S3 URI from DDB by looking up `name` in `{prefix}-playbooks` — the CLI does not need to pass it. `sandboxID` is already resolved by the km at machinery (lines 196–202) from the first extra arg using `ResolveSandboxID`. But for `playbook-run` the first extra arg is the playbook name, not the sandbox-id. The planner must specify that `sandboxID` in `buildTargetInput` comes from the `--sandbox` flag for this command, not positional args.

### km at list display

`km at list` reads `ScheduleRecord.Command` from DDB (written as the cmdArg string). "playbook-run" will appear in the command column unchanged. No additional changes needed.

---

## 3. TTL Lambda Event Handler

### Existing switch (ttl-handler/main.go:199–226)

```go
switch event.EventType {
case "stop":    return h.handleStop(ctx, event)
case "resume":  return h.handleResume(ctx, event)
case "extend":  return h.handleExtend(ctx, event)
case "budget-add": return h.handleBudgetAdd(ctx, event)
case "agent-run":  return h.handleAgentRun(ctx, event)
case "schedule-create": return h.handleScheduleCreate(ctx, event)
default:
    // "ttl", "idle", "destroy", "" — check teardownPolicy
    ...
}
```

**Add before `default:`:**
```go
case "playbook-run":
    return h.handlePlaybookRun(ctx, event)
```

### TTLEvent struct additions needed

```go
// Add to TTLEvent struct:
PlaybookName string `json:"playbook,omitempty"`
PlaybookS3URI string `json:"s3_uri,omitempty"`
// SandboxID already exists in TTLEvent
```

### handlePlaybookRun algorithm (verified against handleAgentRun pattern)

1. `ec2:DescribeInstances` by sandbox tag — same pattern as handleAgentRun (lines 518–541).
2. If instance terminated/missing: `dynamodb:PutItem` to `{prefix}-playbook-runs` with `status=failed`, fire operator-notify (same SES path as handleAgentRun), return non-zero error.
3. If stopped/stopping: `ec2:StartInstances` (lines 549–553). Unlike handleAgentRun, **do not wait** for running state — per SPEC, the Lambda enqueues immediately without waiting on boot. The runner's visibility-timeout extension handles the boot race.
4. Resolve S3 URI: if `event.PlaybookS3URI` is empty, `dynamodb:GetItem` on `{prefix}-playbooks` by `name` to fetch `s3_uri`.
5. Mint `run_id` (timestamp format like `runID := time.Now().UTC().Format("20060102T150405Z")` — ULID is not in go.mod).
6. `dynamodb:PutItem` to `{prefix}-playbook-runs`.
7. `sqs:SendMessage` to `{prefix}-playbook-{sandbox_id}.fifo`.
8. Return nil.

### IAM gaps in TTL Lambda

The current `infra/modules/ttl-handler/v1.0.0/main.tf` IAM policies do **not** include:
- `sqs:SendMessage` on playbook FIFO queues
- `dynamodb:GetItem`, `dynamodb:PutItem`, `dynamodb:UpdateItem` on the three new playbook tables

These must be added as new `aws_iam_role_policy` resources in `infra/modules/ttl-handler/v1.0.0/main.tf`. Existing EC2 and SSM perms (`ec2:DescribeInstances`, `ec2:StartInstances`, `ssm:SendCommand`) are already present (lines 160–196). The `dynamodb_sandboxes` policy (lines 512–538) covers the sandboxes table but not the new playbook tables — a new `dynamodb_playbooks` policy is needed.

The Lambda env vars also need: `KM_PLAYBOOK_TABLE`, `KM_PLAYBOOK_RUNS_TABLE`, `KM_PLAYBOOK_SESSIONS_TABLE` added to the Lambda `environment.variables` block (lines 287–303 in main.tf).

---

## 4. Profile Schema Additions

### types.go CLISpec (pkg/profile/types.go:354–447)

Add after `NotifySlackTranscriptEnabled` (line 447):

```go
// PlaybookEnabled enables per-sandbox playbook orchestration — provisions
// the SQS FIFO queue + DLQ and installs km-playbook-runner.service via userdata.
// Default: false. Profile-only — no CLI flag override (Phase 71).
PlaybookEnabled bool `yaml:"playbookEnabled,omitempty"`
```

No cross-field constraints needed (SPEC: "boolean only; no cross-field constraints"). Default false is automatic (zero value for bool). No pointer type needed (compare: `NotifySlackInboundEnabled bool` at line 436, same shape).

### JSON schema (pkg/profile/schemas/sandbox_profile.schema.json:534)

Add after `notifySlackTranscriptEnabled` block (lines 539–543):

```json
"playbookEnabled": {
  "type": "boolean",
  "default": false,
  "description": "Enable per-sandbox playbook runner — provisions SQS FIFO queue + DLQ and installs km-playbook-runner.service. Default false. Phase 71."
}
```

No `required` constraints, no `if/then` validation rules needed.

---

## 5. Provisioning at km create

### Pattern: create_slack_inbound.go

The playbook provisioning should follow `internal/app/cmd/create_slack_inbound.go` exactly:

1. New file: `internal/app/cmd/create_playbook.go`
2. `slackInboundDeps` → `playbookDeps` struct with `SQS awspkg.SQSClient`, `UpdateSandboxAttr func(...)`, `PutSSMParameter func(...)`
3. `provisionSlackInboundQueue` → `provisionPlaybookQueue`
4. Queue name: `awspkg.PlaybookQueueName(resourcePrefix, sandboxID)` → `"{prefix}-playbook-{sandbox_id}.fifo"`
5. DLQ creation is **in addition** to the main queue (SPEC calls for DLQ with `maxReceiveCount=5`). The slack-inbound queue has no DLQ (gap). Phase 71 must add DLQ creation.
6. SSM parameter: `/sandbox/{id}/playbook-queue-url` (same SCP workaround)
7. `rollbackPlaybookQueue` analogous to `rollbackSlackInboundQueue`

### pkg/aws/sqs.go additions

Add helpers mirroring `SlackInboundQueueName` / `CreateSlackInboundQueue` / `DeleteSlackInboundQueue`:

```go
func PlaybookQueueName(resourcePrefix, sandboxID string) string {
    return fmt.Sprintf("%s-playbook-%s.fifo", resourcePrefix, sandboxID)
}

func PlaybookDLQName(resourcePrefix, sandboxID string) string {
    return fmt.Sprintf("%s-playbook-%s-dlq.fifo", resourcePrefix, sandboxID)
}
```

`CreatePlaybookQueue` must also create the DLQ and set `RedrivePolicy` on the main queue. The existing `CreateSlackInboundQueue` does not set RedrivePolicy — this is new territory.

### userdata.go template additions

Add a `PlaybookEnabled bool` field to the `UserDataParams` struct (analogous to `SlackInboundEnabled` at line 2789). Add three conditional blocks:

1. The runner bash script (analogous to lines 1152–1391) under `{{- if .PlaybookEnabled }}`
2. The systemd unit file (analogous to lines 1659–1679)
3. The `systemctl enable/start` line additions (analogous to lines 2226–2231)

**Critical — systemd EnvironmentFile gotcha:** The systemd unit's `EnvironmentFile` must point to `/etc/km/notify.env` (systemd-format, no `export` prefix), NOT `/etc/profile.d/km-notify-env.sh` (shell-format, rejected by systemd). This is documented in CLAUDE.md §Slack inbound and in the source comment at userdata.go:1665–1670:

```
EnvironmentFile=-/etc/km/notify.env
```

The leading `-` makes it tolerant of a missing file. The playbook runner unit must use the same pattern.

---

## 6. DDB Table Provisioning

### Module pattern (from dynamodb-slack-threads)

Each new table gets its own module directory following the versioned module pattern:

```
infra/modules/dynamodb-playbooks/v1.0.0/
  main.tf       — aws_dynamodb_table resource
  variables.tf  — table_name, tags
  outputs.tf    — table_name, table_arn
infra/live/use1/dynamodb-playbooks/
  terragrunt.hcl
```

Repeat for `dynamodb-playbook-sessions` and `dynamodb-playbook-runs`.

### Key design differences from slack-threads

| Table | PK | SK | GSI | TTL |
|---|---|---|---|---|
| `{prefix}-playbooks` | `name` (S) | — | — | — |
| `{prefix}-playbook-sessions` | `playbook` (S) | `sandbox_id` (S) | — | 90d via `ttl_expiry` |
| `{prefix}-playbook-runs` | `run_id` (S) | — | `(playbook, started_at)` for list-runs | — |

The `{prefix}-playbook-runs` table needs a GSI declaration in Terraform (the slack-threads module has no GSI). Example (from the `dynamodb-sandboxes` module's alias-index):

```hcl
global_secondary_index {
  name            = "playbook-started-index"
  hash_key        = "playbook"
  range_key       = "started_at"
  projection_type = "ALL"
}
attribute { name = "playbook"; type = "S" }
attribute { name = "started_at"; type = "S" }
```

### SSE: AWS-owned vs CMK

The existing `dynamodb-slack-threads` module uses AWS-owned SSE (`server_side_encryption { enabled = true }` without `kms_master_key_id`). The SPEC says "encrypted with the platform CMK." This is a gap between the SPEC and the established pattern. The planner must decide: match SPEC (add CMK) or match Phase 67 precedent (AWS-owned SSE). Research recommendation: use AWS-owned SSE to match Phase 67, update SPEC accordingly. Adding CMK requires the Lambda's IAM role to have `kms:GenerateDataKey` + `kms:Decrypt` on the CMK ARN — additional IAM surface.

### km init extension (init.go:85–183)

Add three entries to the `initModules` slice after `dynamodb-slack-stream-messages` (line 162):

```go
{
    name: "dynamodb-playbooks",
    dir:  filepath.Join(regionDir, "dynamodb-playbooks"),
    envReqs: nil,
},
{
    name: "dynamodb-playbook-sessions",
    dir:  filepath.Join(regionDir, "dynamodb-playbook-sessions"),
    envReqs: nil,
},
{
    name: "dynamodb-playbook-runs",
    dir:  filepath.Join(regionDir, "dynamodb-playbook-runs"),
    envReqs: nil,
},
```

The Lambda module (lambda-slack-bridge-equivalent, which for TTL handler is a Terraform module) needs the table ARNs added to its IAM policy for `dynamodb:GetItem`, `dynamodb:PutItem`, `dynamodb:UpdateItem`. The TTL Lambda module (`infra/modules/ttl-handler/v1.0.0/main.tf`) must be updated with new `aws_iam_role_policy` resources.

---

## 7. Operator-Notify Hook — Payload Extension

### Current km-notify-hook dispatch (userdata.go:609–666)

The hook formats `subject` and `body_text` in a `case "$event"` block and then sends via email + Slack branches. There is no `kind` field routing inside the hook itself — the hook always reacts to the Claude Code event type (Notification, Stop, PostToolUse). There is no operator-side formatter Lambda.

The SPEC says "the existing notify-hook formatter (Phase 62/63) routes this to email and Slack with a one-line subject/headline." However, the playbook runner fires the notify hook **from inside the sandbox** (SPEC step 9: "Fire operator-notify hook (existing Phase 62 entry-point)"). The runner calls `/opt/km/bin/km-notify-hook` directly from bash, passing a synthetic event. This means:

1. The runner must invoke km-notify-hook with a custom payload (not a Claude Code hook event).
2. km-notify-hook's existing event routing does not handle a "playbook-run-completed" kind — it only handles "Notification", "Stop", "PostToolUse".
3. The runner must either: (a) add a new case to km-notify-hook's event routing, or (b) call km-send + km-slack directly (bypassing km-notify-hook), or (c) synthesize a "Stop"-like payload and rely on the existing hook formatting.

**Recommendation:** Option (a) — add a new `playbook-run-completed` case to km-notify-hook's `case "$event"` block (userdata.go around line 417). The runner calls `echo "$json_payload" | /opt/km/bin/km-notify-hook playbook-run-completed`. The hook formats subject = `[km] playbook {name} on {sandbox}: {status} ({n}/{m})` and body = the JSON payload. This is minimal-change-surface and keeps the email+Slack routing unified.

The planner must specify that this case is added to the km-notify-hook heredoc in userdata.go and that the runner constructs a JSON payload matching the SPEC's fields.

---

## 8. `km doctor` Check Pattern

### CheckResult type (doctor.go:63–68)

```go
type CheckResult struct {
    Name        string      `json:"name"`
    Status      CheckStatus `json:"status"`
    Message     string      `json:"message"`
    Remediation string      `json:"remediation,omitempty"`
}
```

Statuses: `CheckOK`, `CheckWarn`, `CheckError`, `CheckSkipped`.

### Adding checks (doctor.go:2263–2290 — the Phase 67 inbound pattern)

New file: `internal/app/cmd/doctor_playbook.go` with three functions:

```
checkPlaybookRunnerServiceActive(ctx, listPlaybookEnabled, ssmClient) CheckResult
checkPlaybookQueueExists(ctx, listPlaybookEnabled, sqsClient) CheckResult
checkPlaybookDLQDepth(ctx, listPlaybookEnabled, sqsClient) CheckResult
```

Each function follows the `checkSlackInboundQueueExists` pattern:
- Takes a `listPlaybookEnabled func(context.Context) ([]playbookRow, error)` callback
- Implements SKIPPED when deps are nil
- Implements OK when no playbook-enabled sandboxes found
- Implements ERROR (demoted to WARN in the runner) when checks fail

The `playbookRow` struct mirrors `inboundRow` (doctor_slack.go:37–40):
```go
type playbookRow struct {
    SandboxID      string
    QueueURL       string
    DLQUrl         string
    InstanceID     string
}
```

`checkPlaybookRunnerServiceActive` needs SSM (`ssm:SendCommand` with "docker inspect" or systemctl status) or a different liveness probe. The slack doctor checks use SQS queue depth as a proxy for health — not SSM. For `playbook_runner_service_active`, the planner should specify SSM-based systemctl check OR accept that this check is always `CheckSkipped` at operator-side doctor time (sandbox-internal systemd state is not directly observable without SSM). Phase 67's equivalent check is `slack_inbound_queue_exists` (queue depth probe, not systemd probe). The SPEC's `playbook_runner_service_active` is operationally harder — flag this as a design decision for the planner.

Add the three check closures to `buildDoctorChecks` (doctor.go) following the pattern at lines 2269–2290:

```go
playbookSQS := deps.PlaybookSQS
listPlaybook := deps.PlaybookListSandboxesWithPlaybook
checks = append(checks, func(ctx context.Context) CheckResult {
    r := checkPlaybookRunnerServiceActive(ctx, listPlaybook, playbookSQS)
    if r.Status == CheckError { r.Status = CheckWarn }
    return r
})
// ... etc
```

New fields on `DoctorDeps` struct: `PlaybookSQS awspkg.SQSClient`, `PlaybookListSandboxesWithPlaybook func(...)`.

---

## 9. S3 Content-Addressed Storage

No SHA-256 content-addressing helper exists in the codebase. The SPEC calls for:
```
s3://{artifacts}/playbooks/{name}/{sha256-of-yaml}.yaml
```

Implement in `pkg/playbook/apply.go`:

```go
import "crypto/sha256"

func ContentKey(name string, yamlBytes []byte) string {
    sum := sha256.Sum256(yamlBytes)
    return fmt.Sprintf("playbooks/%s/%x.yaml", name, sum)
}
```

This is a net-new, 3-line helper. Confidence HIGH that `crypto/sha256` is already imported elsewhere in the codebase (`pkg/aws/rotation.go:22`). No new dependencies needed.

Idempotency: `km playbook apply` should use `s3:HeadObject` to check if the key exists before uploading — if present, skip the PUT and only update DDB `applied_at`. The existing S3 PutObject is not idempotent in cost terms.

---

## 10. `claude -p` JSON Output Capture — Verified Pattern

From `pkg/compiler/userdata.go` (the slack-inbound-poller, lines 1310–1323):

```bash
sudo -u sandbox bash -c "
  set -a; for f in /etc/profile.d/*.sh; do source \"\$f\" 2>/dev/null || true; done; set +a
  claude -p \"\$(cat '$PROMPT_FILE')\" --output-format json \
    --dangerously-skip-permissions $RESUME_ARG \
    > '$RUN_DIR/output.json' 2>'$RUN_DIR/stderr.log'
  echo \$? > '$RUN_DIR/exit_code'
" || true

NEW_SESSION=$(jq -r '.session_id // empty' "$RUN_DIR/output.json" 2>/dev/null || true)
```

For playbook steps:
- Step 1 (first run): no `$RESUME_ARG` → claude creates a new session → `output.json` contains `.session_id` → capture it
- Step 1 (subsequent runs): `RESUME_ARG="--resume $CLAUDE_SESSION"` from DDB lookup → same `.session_id` returned (session continuity confirmed)
- Steps 2..N (same run): use the session_id captured from step 1

The `--output-format json` field `.session_id` is present in every non-error output regardless of whether `--resume` was passed. Confirmed by the poller's working implementation.

**Important:** The runner must NOT use `--bare`. The `--bare` flag (used by handleAgentRun in the TTL Lambda) suppresses the system prompt output but also suppresses some fields needed for session tracking. The slack-inbound-poller (the reference implementation) does not use `--bare`.

**Prompt delivery:** The slack-inbound-poller writes the prompt to a tempfile (`mktemp`) and passes `"$(cat '$PROMPT_FILE')"` because shell escaping of multiline prompts via environment or inline expansion is unreliable. The playbook runner must use the same tempfile pattern, especially since playbook prompts may contain YAML special characters.

---

## 11. SCP Workaround — `KM_PLAYBOOK_QUEUE_URL` env injection is blocked

**CONFIRMED:** An org-level SCP blocks SSM SendCommand for the application account. This is documented in:
- `internal/app/cmd/create_slack_inbound.go:17` — "SSM SendCommand is intentionally avoided because an org-level SCP denies it for the application account"
- `pkg/compiler/userdata.go:1175–1180` — "org-level SCP blocks SSM SendCommand, so the value cannot be injected directly"

The playbook runner **must not** rely on env var injection via SSM SendCommand for `KM_PLAYBOOK_QUEUE_URL`. The pattern:

1. `km create` writes queue URL to SSM Parameter Store: `/sandbox/{id}/playbook-queue-url`
2. `km create` writes the same URL to the sandbox env file (`/etc/profile.d/km-notify-env.sh` and `/etc/km/notify.env`) as a placeholder or omits it if SSM SendCommand is blocked
3. The runner reads from the env var at startup; if empty, falls back to SSM Parameter Store with 10-attempt, 30s-sleep retry

The DynamoDB `sandbox_url` attribute approach (also used) is not applicable here because the runner cannot DDB-query without knowing the sandbox-id (which it does know from `$KM_SANDBOX_ID` env var). The SSM path is canonical.

---

## 12. pkg/playbook Package — New Package

No `pkg/playbook` package exists. Must be created. Mirrors `pkg/profile` shape:

```
pkg/playbook/
  types.go     — Playbook, PlaybookSpec, Step structs
  parse.go     — Parse([]byte) (*Playbook, error)
  validate.go  — Validate(*Playbook) error  (table-driven, pure Go)
  apply.go     — Apply(*Playbook, s3, dynamo) (s3Key, error)
  schema/playbook.schema.json  (optional, if JSON schema validation desired)
```

Pure Go, no AWS deps in types/parse/validate. AWS deps isolated to apply.go. Unit-tested with golden valid + invalid YAML files in `pkg/playbook/testdata/`.

No external YAML library addition needed — `github.com/goccy/go-yaml v1.19.2` is already in go.mod (used by `pkg/profile`).

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead |
|---|---|---|
| YAML parsing | custom lexer | `github.com/goccy/go-yaml` (already in go.mod) |
| SHA-256 hash | custom hash | `crypto/sha256` (stdlib) |
| FIFO queue creation | raw SQS calls | extend `pkg/aws/sqs.go` pattern |
| Run ID generation | ULID library (not in go.mod) | `time.Now().UTC().Format("20060102T150405Z")` — same as existing agent-run |
| Systemd unit file | complex template | verbatim heredoc like SLACKINBOUNDUNIT pattern |
| JSON schema validation | custom validator | `github.com/santhosh-tekuri/jsonschema/v6` (already in go.mod, used by profile) |

---

## Common Pitfalls

### Pitfall 1: systemd EnvironmentFile rejects `export VAR=val` syntax
**What goes wrong:** Writing `export KM_PLAYBOOK_QUEUE_URL=...` to `/etc/profile.d/km-notify-env.sh` and pointing the systemd unit's `EnvironmentFile=` at it. Systemd silently ignores lines with the `export` prefix.
**How to avoid:** The compiler writes `/etc/km/notify.env` in systemd-native format (no `export`, just `VAR=val`) — same as the slack-inbound unit. The playbook runner systemd unit must use `EnvironmentFile=-/etc/km/notify.env`, not `/etc/profile.d/km-notify-env.sh`. This is documented in CLAUDE.md and in userdata.go:1665–1670.
**Warning signs:** Runner starts but `$QUEUE_URL` is empty even though the env file was written.

### Pitfall 2: AWS_REGION not exported to subprocesses
**What goes wrong:** The runner's main loop can resolve `AWS_REGION` from `${AWS_DEFAULT_REGION:-us-east-1}` but subprocess binaries (aws CLI, km-send) do not inherit it unless explicitly exported.
**How to avoid:** Add `export AWS_REGION="$REGION"` early in the runner script, matching userdata.go:1171.

### Pitfall 3: SQS message visibility expires during long claude steps
**What goes wrong:** A single claude step runs > 30s (the default VisibilityTimeout). SQS re-delivers the message while the step is still running, causing the runner to start a duplicate run.
**How to avoid:** Extend visibility in a background loop while claude is running, not just once before. The slack-inbound-poller extends to 300s once — sufficient for Slack turns but not guaranteed for multi-minute playbook steps. Implement a background extension loop: `while kill -0 $CLAUDE_PID 2>/dev/null; do aws sqs change-message-visibility ... --visibility-timeout 60; sleep 45; done &`.

### Pitfall 4: DLQ for playbook queue (new, not in Phase 67)
**What goes wrong:** The SPEC calls for a DLQ with `maxReceiveCount=5`. The existing `CreateSlackInboundQueue` helper in `pkg/aws/sqs.go` does not create a DLQ or set a redrive policy. Building the DLQ requires: (1) create DLQ first, (2) get its ARN, (3) create main queue with `RedrivePolicy` JSON attribute referencing DLQ ARN. The SQS SDK's `CreateQueue` doesn't return ARN — need `GetQueueAttributes` after creation to fetch it.
**How to avoid:** Create a `CreatePlaybookQueues(ctx, sqsClient, mainName, dlqName) (mainURL, dlqURL, error)` helper that handles the two-step flow atomically.

### Pitfall 5: km destroy must delete both FIFO and DLQ
**What goes wrong:** Only deleting the main queue leaves the DLQ as an orphan. `km doctor`'s `playbook_dlq_depth` check will find it.
**How to avoid:** Read both queue URLs from DDB metadata at destroy time (store both in sandbox row). Extend the `destroyPlaybookQueue` function to delete both.

### Pitfall 6: ULID not in go.mod — use timestamp run IDs
**What goes wrong:** The SPEC says "mints a fresh ULID" for run_id. `github.com/oklog/ulid` is not in go.mod.
**How to avoid:** Use `time.Now().UTC().Format("20060102T150405Z")` with a 4-digit nanosecond suffix for uniqueness (`fmt.Sprintf("pr-%s%04d", t.Format("20060102T150405Z"), t.Nanosecond()/100000)`) or use `github.com/google/uuid` which IS in go.mod. Recommend UUID v4 prefixed with "pr-" for run IDs.

### Pitfall 7: km-notify-hook's event routing doesn't handle playbook-run-completed
**What goes wrong:** The runner calls `km-notify-hook playbook-run-completed` but the hook's case statement only handles "Notification", "Stop", "PostToolUse" — the new event exits immediately (catch-all `*) exit 0`).
**How to avoid:** Add `playbook-run-completed` case to km-notify-hook's event routing (userdata.go around line 417). This requires regenerating userdata for existing sandboxes (km destroy + km create) to pick up the new hook logic.

### Pitfall 8: Runner reads playbook YAML from S3 every message
**What goes wrong:** The runner fetches the playbook YAML from S3 on every SQS message to get the list of steps. If the playbook is updated between runs (new `km playbook apply`), the in-flight run uses the new YAML. The SPEC says "the Lambda enqueues `s3_uri`" (content-addressed, so the URI changes when YAML changes) — but only if the run's SQS message carries the specific s3_uri. The planner must clarify: does the SQS message body carry the exact s3_uri minted at enqueue time (reproducible), or does the runner re-query DDB for the current s3_uri (uses latest)?
**How to avoid:** Include `s3_uri` in the SQS message body (as the SPEC specifies). The runner uses `body.s3_uri`, not a fresh DDB query. This ensures per-run reproducibility.

---

## Architecture Patterns

### New package: `pkg/playbook/`
Pure Go, mirrors `pkg/profile`. Dependencies: `goccy/go-yaml`, `crypto/sha256` (stdlib). No AWS.

### New command group: `internal/app/cmd/playbook.go`
Cobra command group. DI follows `at_test.go` pattern: `NewPlaybookCmdWithDeps(cfg, sched, dynamo, s3, lambda)`.

### New sidecar (bash, embedded in userdata.go)
Heredoc at the bottom of the conditional sidecar-install blocks, gated by `{{- if .PlaybookEnabled }}`.

### New Terraform modules (three DDB + IAM additions)
```
infra/modules/dynamodb-playbooks/v1.0.0/
infra/modules/dynamodb-playbook-sessions/v1.0.0/
infra/modules/dynamodb-playbook-runs/v1.0.0/
infra/live/use1/dynamodb-playbooks/terragrunt.hcl
infra/live/use1/dynamodb-playbook-sessions/terragrunt.hcl
infra/live/use1/dynamodb-playbook-runs/terragrunt.hcl
```

Plus IAM additions to `infra/modules/ttl-handler/v1.0.0/main.tf`.

---

## Validation Architecture

Nyquist validation is enabled in `.planning/config.json` (`"nyquist_validation": true`).

### Test Framework
| Property | Value |
|---|---|
| Framework | Go test (`go test ./...`) — same as all existing tests |
| Config file | none (standard go test) |
| Quick run command | `go test ./pkg/playbook/... ./internal/app/cmd/... -run TestPlaybook -v` |
| Full suite command | `go test ./...` |

### Success Criteria → Test Map

| SC# | Criterion Summary | Test Type | Automated Command | Notes |
|---|---|---|---|---|
| SC-1 | `km playbook validate` OK on valid file, fails on each invalid case | unit | `go test ./pkg/playbook/... -run TestValidate` | Table-driven; test each invalid rule variant |
| SC-2 | `km playbook apply` writes to S3 + DDB, idempotent on same content | unit (mock S3+DDB) | `go test ./internal/app/cmd/... -run TestPlaybookApply` | Inject fake S3 + DDB; verify content-addressed key |
| SC-3 | Manual `km playbook run` on running sandbox: Lambda invoked, SQS enqueued, runner walks all steps, DDB end-state correct | E2E (real sandbox) | manual UAT | Requires real sandbox with playbookEnabled:true |
| SC-4 | Session continuity: second run resumes prior session; model has memory | E2E (real sandbox) | manual UAT | Verify via step prompt referencing prior content |
| SC-5 | `km at` creates EventBridge schedule with playbook-run input; fires correctly | unit (mock scheduler) + E2E | `go test ./internal/app/cmd/... -run TestAt.*Playbook` | Unit verifies Input JSON shape; E2E verifies fire |
| SC-6 | Sandbox readiness: stopped sandbox auto-starts, run status transitions queued→running | unit (mock EC2+DDB+SQS) | `go test ./cmd/ttl-handler/... -run TestHandlePlaybookRun.*Stopped` | Table-driven: running/stopped/missing states |
| SC-7 | Step-failure abort: mid-run failure leaves status=failed, correct step pinned, notify fires | unit (runner shim) | `go test ./pkg/playbook/... -run TestRunnerStepFailure` | Use claude shim that exits non-zero on step N |
| SC-8 | Concurrent serialization: same playbook serializes, different playbooks parallelize | integration (localstack or real SQS) | `go test ./... -run TestConcurrentPlaybook -tags integration` | Verify DDB timestamps ordering |
| SC-9 | Crash-mid-step idempotency: SIGKILL + restart replays step, no duplicate row | integration | `go test ./... -run TestPlaybookRunnerCrashRecovery -tags integration` | SIGKILL runner between receive and step complete |
| SC-10 | `km doctor` three new checks green on healthy install, red on broken | unit (mock SQS+DDB+SSM) | `go test ./internal/app/cmd/... -run TestDoctorPlaybook` | Inject missing queue URL, assert CheckError/CheckWarn |
| SC-11 | `km destroy` deletes FIFO + DLQ + SSM param; DDB runs/sessions preserved | unit (mock SQS+SSM+DDB) | `go test ./internal/app/cmd/... -run TestDestroyPlaybook` | Verify delete calls; verify DDB rows NOT deleted |
| SC-12 | `playbook-run-completed` notify hook payload routes to email + Slack | unit (hook script invocation) | `bash -c 'echo "$JSON" | km-notify-hook playbook-run-completed'` in CI | Also: `go test ./pkg/compiler/... -run TestUserDataPlaybookHook` |

### Sampling Rate
- **Per task commit:** `go test ./pkg/playbook/... ./internal/app/cmd/... -run TestPlaybook -count=1`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps (files that don't exist yet)
- [ ] `pkg/playbook/` — entire package (parse, validate, apply, types)
- [ ] `pkg/playbook/testdata/valid.yaml`, `pkg/playbook/testdata/invalid-*.yaml`
- [ ] `internal/app/cmd/playbook.go` — CLI command group
- [ ] `internal/app/cmd/create_playbook.go` — provisioning helper
- [ ] `internal/app/cmd/destroy_playbook.go` — destroy helper
- [ ] `internal/app/cmd/doctor_playbook.go` — doctor checks
- [ ] `cmd/ttl-handler/main_test.go` additions for `playbook-run` event cases
- [ ] `infra/modules/dynamodb-playbooks/v1.0.0/`
- [ ] `infra/modules/dynamodb-playbook-sessions/v1.0.0/`
- [ ] `infra/modules/dynamodb-playbook-runs/v1.0.0/`
- [ ] `infra/live/use1/dynamodb-playbooks/terragrunt.hcl`
- [ ] `infra/live/use1/dynamodb-playbook-sessions/terragrunt.hcl`
- [ ] `infra/live/use1/dynamodb-playbook-runs/terragrunt.hcl`

---

## Open Questions

1. **Bash heredoc vs Go binary for the runner.** The SPEC says `sidecars/playbook-runner/` (implying a Go binary) but Phase 67's precedent is a bash heredoc in userdata.go. Go binary has better testability (fake claude shim, unit tests without real sandbox) but requires a Makefile sidecar target, S3 upload, and km init --sidecars deployment. Bash heredoc is simpler and matches Phase 67 exactly. **Recommendation:** bash heredoc for v1 (lower risk, proven pattern), Go binary as Phase 71.1.

2. **`playbook_runner_service_active` check implementation.** Systemd service state is not observable from operator-side without SSM. The SQS queue depth is a better proxy (if queue exists and depth is 0 with recent message, runner is processing). Recommend renaming this check to `playbook_queue_healthy` and using SQS queue attributes as the liveness probe, matching the `checkSlackInboundQueueExists` pattern.

3. **ULID run IDs.** The SPEC specifies ULID but go.mod has no ULID library. Use UUID v4 from `github.com/google/uuid` (already in go.mod) prefixed with "pr-" for human-readable sortable run IDs. Or use the existing timestamp format `20060102T150405Z`.

4. **CMK vs AWS-owned SSE for DDB tables.** SPEC says "platform CMK" but Phase 67 precedent uses AWS-owned SSE (simpler, no additional IAM surface). Confirm intent.

5. **S3 URI in SQS message vs DDB lookup.** The SPEC says the Lambda includes `s3_uri` in the SQS message body. This is the recommended approach (reproducibility). The planner should specify that the Lambda queries DDB `{prefix}-playbooks` for the current `s3_uri` when `km playbook run` does not pass `--s3-uri` explicitly, and includes the resolved URI in the SQS message body.

---

## Sources

### Primary (HIGH confidence)
- `pkg/compiler/userdata.go:1150–1391` — km-slack-inbound-poller (Phase 67 reference implementation)
- `pkg/compiler/userdata.go:1658–1679` — systemd unit pattern for inbound poller
- `internal/app/cmd/at.go:40–510` — km at schedulableCommands, two-word merge, buildTargetInput
- `internal/app/cmd/create_slack_inbound.go` — Phase 67 SQS provisioning with SSM workaround
- `pkg/aws/sqs.go` — SQS queue creation/deletion helpers
- `cmd/ttl-handler/main.go:199–655` — TTL Lambda event switch, handleAgentRun, handleStop patterns
- `infra/modules/ttl-handler/v1.0.0/main.tf` — existing IAM policies (no SQS, no playbook DDB)
- `infra/modules/dynamodb-slack-threads/v1.0.0/main.tf` — DDB module template
- `infra/live/use1/dynamodb-slack-threads/terragrunt.hcl` — live Terragrunt pattern
- `internal/app/cmd/init.go:85–183` — initModules slice (where to add three new entries)
- `internal/app/cmd/doctor.go:62–68, 2263–2322` — CheckResult type, doctor check assembly pattern
- `internal/app/cmd/doctor_slack.go:208–265` — checkSlackInboundQueueExists (check function template)
- `pkg/profile/types.go:354–447` — CLISpec struct with NotifySlackInboundEnabled at line 436
- `pkg/profile/schemas/sandbox_profile.schema.json:534–543` — JSON schema field pattern

### Secondary (MEDIUM confidence)
- CLAUDE.md §Slack inbound — systemd EnvironmentFile gotcha (confirmed against source at userdata.go:1665–1670)
- SPEC.md — architecture decisions (locked, treated as authoritative)

---

## Metadata

**Confidence breakdown:**
- Phase 67 poller patterns: HIGH — read from source
- km at extension surface: HIGH — read from source, exact line numbers
- TTL Lambda event switch: HIGH — read from source
- Profile schema additions: HIGH — read from source
- IAM gaps: HIGH — confirmed SQS/playbook-DDB absent from main.tf
- S3 content addressing: HIGH — stdlib crypto/sha256, no new deps
- ULID absence: HIGH — confirmed go.mod has no ULID library
- systemd EnvironmentFile gotcha: HIGH — documented in CLAUDE.md + source comment

**Research date:** 2026-05-05
**Valid until:** 2026-06-05 (stable infrastructure patterns; Go AWS SDK versions evolve slowly)
