# Phase 110: Session-aware Slack Reply + Thread/Channel Repair - Research

**Researched:** 2026-06-12
**Domain:** Slack bridge extension, DynamoDB GSI, sandbox-side helper, operator CLI, km doctor
**Confidence:** HIGH

---

## Summary

Phase 110 extends the existing Slack machinery in three directions: (1) a new
`session-index` GSI on `km-slack-threads` so threads can be looked up by
`claude_session_id`; (2) a new `lookup-thread` action on the bridge Lambda so
sandboxes can resolve a session id → `(channel_id, thread_ts)` without ever
touching DynamoDB directly; and (3) a new `km-slack reply` subcommand that
chains through explicit `--thread`, `$KM_SLACK_THREAD_TS`, session-id
auto-detect, and channel-root fallback to always post to the right thread.

On the operator side, four repair commands (`km slack threads`, `km slack
forget-thread`, `km slack prune-threads`, `km slack forget-channel`) let
operators clean up stale/incorrect mappings, and two new `km doctor` WARN
checks flag thread rows pointing at dead channels.

All changes are brownfield extensions of well-established patterns in this
codebase. No new DDB tables, no new Lambda functions, no new SQS queues.
The GSI is an in-place schema change to the existing `dynamodb-slack-threads`
module, bumped from `v1.0.0` to `v1.1.0`. The bridge module stays at `v1.0.0`
(additive var + IAM statement change — same pattern used in Phase 104 and 109
for other in-place additions). Existing sandboxes must be `km destroy && km
create` to gain the `km-slack reply` subcommand.

**Primary recommendation:** Follow the bridge `lookup-thread` action pattern
established by `ActionPermalink` and `ActionUpdate` in Phase 70. The sandbox
posts a signed envelope; the bridge queries the GSI and returns the result.
No new envelope version required.

---

## Scope Item Breakdown

### Scope 1: GSI `session-index` on `km-slack-threads`

**What exists:**

The table is defined in `infra/modules/dynamodb-slack-threads/v1.0.0/main.tf`.
Current schema:
- Hash key: `channel_id` (S)
- Sort key: `thread_ts` (S)
- Attributes declared: `channel_id`, `thread_ts`
- TTL: `ttl_expiry` (N)
- No GSI today

The poller writes these attributes per row (confirmed at
`pkg/compiler/userdata.go:2018-2030`):
```
channel_id, thread_ts, claude_session_id, agent_type,
last_assistant_msg, sandbox_id, last_turn_ts, ttl_expiry
```

The bridge `Upsert` writes (confirmed at
`pkg/slack/bridge/aws_adapters.go:923-934`):
```
channel_id, thread_ts, sandbox_id, created_at, last_turn_ts,
turn_count, ttl_expiry
```
Note: bridge Upsert omits `claude_session_id` (phase scope spec: "Initial
bridge upsert omits `claude_session_id`, so only poller-written rows enter
the index — no empty-string key collisions").

**What to change:**

Create `infra/modules/dynamodb-slack-threads/v1.1.0/main.tf` (copy v1.0.0 +
add the GSI block below). Also copy `outputs.tf` and `variables.tf` unchanged.
Update the live terragrunt.hcl to source `v1.1.0`.

The GSI must be `KEYS_ONLY` projection (scope spec). DynamoDB requires each
GSI attribute to appear in `attribute {}` blocks at the table level:

```hcl
attribute {
  name = "claude_session_id"
  type = "S"
}

global_secondary_index {
  name            = "session-index"
  hash_key        = "claude_session_id"
  projection_type = "KEYS_ONLY"
}
```

**Files:**
- `infra/modules/dynamodb-slack-threads/v1.0.0/main.tf` (read-only source)
- Create: `infra/modules/dynamodb-slack-threads/v1.1.0/main.tf`
- Create: `infra/modules/dynamodb-slack-threads/v1.1.0/outputs.tf` (copy)
- Create: `infra/modules/dynamodb-slack-threads/v1.1.0/variables.tf` (copy)
- Edit: `infra/live/use1/dynamodb-slack-threads/terragrunt.hcl` line 32:
  `source = ".../v1.0.0"` → `".../v1.1.0"`

**No new `regionalModules()` entry** — the `dynamodb-slack-threads` entry at
`internal/app/cmd/init.go:564` already covers this module. The version bump
is an in-place change (same module name, new source path) that re-applies
cleanly via `km init --dry-run=false`.

**IAM note:** The bridge already has `dynamodb:Query` on
`table/${var.slack_threads_table_name}/index/*` at
`infra/modules/lambda-slack-bridge/v1.0.0/main.tf:166-172`. No IAM change
needed for the GSI query itself.

---

### Scope 2: Bridge `lookup-thread` action

**What exists:**

The bridge handler dispatch lives in
`pkg/slack/bridge/handler.go:255-460`.
All existing actions are defined as constants in `pkg/slack/payload.go:36-42`:
```go
ActionPost      = "post"
ActionArchive   = "archive"
ActionTest      = "test"
ActionUpload    = "upload"
ActionPermalink = "permalink"
ActionUpdate    = "update"
```

The handler validates the action at line 96:
```go
if env.Action != slack.ActionPost && env.Action != slack.ActionArchive && ...
```
This must be extended to include `ActionLookupThread`.

The action authorization block (step 6, line 210) distinguishes operator from
sandbox senders. `ActionPermalink` and `ActionUpdate` are sandbox-allowed but
still require the channel-ownership check (step 6, lines 220-241). For
`lookup-thread`, the sandbox supplies a `session_id` in a new envelope field,
NOT a `channel`. The channel-ownership check cannot apply in the normal way —
the purpose is to discover which channel a session maps to.

**Exact sandbox-never-reads-DDB boundary enforcement:** The scope spec says
"filtered to rows whose `sandbox_id` == requesting sandbox (preserves
sandbox-never-reads-DDB boundary)". This means after the GSI Query returns
rows, the bridge must assert that `row.sandbox_id == env.SenderID` before
returning the `(channel_id, thread_ts)`. This is the application-layer check
analogous to the channel-ownership check for `post`.

**Envelope field:** The `SlackEnvelope` struct at `pkg/slack/payload.go:69-93`
already carries many optional fields (`MessageTS`, `Text`, `Blocks`). The
session_id can be conveyed in the existing `Body` field (set to the session id,
as a plain string) or a new dedicated field. The scope says "payload
`{session_id}`". The least-invasive pattern is to add a `SessionID` field with
a JSON tag in alphabetical order — this keeps `CanonicalJSON` deterministic.
Alternatively, reuse `Body` with a convention. A new field is cleaner;
`EnvelopeVersion` stays at 1 (additive change, same as Phase 68).

**DDB Query:** The bridge's `DDBThreadStore` at
`pkg/slack/bridge/aws_adapters.go:860-946` already implements `DDBQueryGetPutAPI`
with a `Query` method. The GSI query would be:
```go
&dynamodb.QueryInput{
    TableName:              aws.String(tableName),
    IndexName:              aws.String("session-index"),
    KeyConditionExpression: aws.String("claude_session_id = :sid"),
    ExpressionAttributeValues: map[string]types.AttributeValue{
        ":sid": &types.AttributeValueMemberS{Value: sessionID},
    },
}
```
Then filter results by `sandbox_id == requestingSandboxID` before returning.

**Response shape:** The bridge returns JSON. The `lookup-thread` response can
extend the existing `{"ok":true,"ts":...}` pattern:
```json
{"ok":true,"found":true,"channel_id":"Cxxx","thread_ts":"1234.5678","agent_type":"claude"}
```
or `{"ok":true,"found":false}` when no matching row.

**New interface method on `SlackThreadStore`:**
```go
// LookupBySession returns the (channelID, threadTS, agentType) for a given
// claude_session_id, filtered to rows owned by sandboxID. Returns empty
// strings when not found.
LookupBySession(ctx context.Context, sessionID, sandboxID string) (channelID, threadTS, agentType string, err error)
```
This extends `events_interfaces.go`'s `SlackThreadStore`.

**Files:**
- `pkg/slack/payload.go` — add `ActionLookupThread = "lookup-thread"` constant;
  add `SessionID string` field to `SlackEnvelope` struct (alphabetical: after `S3Key`,
  before `SenderID`)
- `pkg/slack/bridge/events_interfaces.go` — extend `SlackThreadStore` interface
- `pkg/slack/bridge/aws_adapters.go` — implement `LookupBySession` on `DDBThreadStore`
- `pkg/slack/bridge/handler.go` — add `ActionLookupThread` to action allow-list
  (line 96); add dispatch case; add `LookupThread SlackThreadLookup` field to
  `Handler` struct or reuse `Threads SlackThreadStore`
- `pkg/slack/bridge/interfaces.go` — update `SlackThreadStore` (or create a new
  narrower interface for the new method)

**Note:** `pkg/slack/payload.go` canonical JSON is alphabetical by struct tag.
New `session_id` tag would sort after `s3_key` and before `sender_id` —
insert accordingly to maintain the canonical ordering.

---

### Scope 3: Sandbox-side `km-slack reply`

**What exists:**

`cmd/km-slack/main.go` — the sandbox-side helper binary. Current subcommands
(dispatch table at line 56-74): `post`, `upload`, `record-mapping`, `permalink`,
`update`, `help`.

The `post` subcommand (`runPost`, line 94) accepts:
- `--channel` (required)
- `--body` (required, file path)
- `--subject` (optional)
- `--thread` (optional thread parent ts)
- `--render` (plain/mrkdwn/blocks)
- `--new-message` (force top-level)

The inner `runWith` at line 340 does: read body → render → build envelope →
sign → POST to bridge → return ts.

The sandbox env vars available:
- `KM_SANDBOX_ID` — required by all subcommands
- `KM_SLACK_BRIDGE_URL` — required
- `KM_SLACK_CHANNEL_ID` — sandbox's bound channel
- `KM_SLACK_THREAD_TS` — set by the inbound poller when a poller-driven turn is running
- `KM_AGENT` — "claude" or "codex" (from `/etc/profile.d/km-notify-env.sh`)

**`km-slack reply` resolution chain** (scope spec, first-hit wins):
1. Explicit `--thread <ts>` + requires `--channel`
2. `$KM_SLACK_THREAD_TS` env var (set by inbound poller)
3. Session id — `--session <id>` or auto-detect newest `.jsonl` file:
   - Claude: `~/.claude/projects/**/<id>.jsonl` (newest by mtime)
   - Codex: `~/.codex/...` (to verify path)
   → POST `lookup-thread` to bridge → returns `(channel_id, thread_ts)`
4. Fallback: top-level post to `$KM_SLACK_CHANNEL_ID`

**Claude session auto-detect:** Claude Code writes session files to
`~/.claude/projects/<project-path-encoded>/<session-uuid>.jsonl`. The
newest file by mtime (across all subdirectories) represents the current
session. The session id is the UUID filename stem (without `.jsonl`).

**Codex session:** Codex session IDs are written to
`~/.codex/store/<session-id>.json` (based on Phase 70 hangover note:
`claude_session_id` column is reused for Codex session IDs).

**`KM_AGENT` branching:** `cmd/km-slack/main.go` already reads env vars.
The `reply` subcommand must branch on `$KM_AGENT` to pick the right
auto-detect path.

**Sidecar delivery:** `km-slack` is built and uploaded by
`buildAndUploadSidecars` at `internal/app/cmd/init.go:3027`. The binary is
downloaded by userdata at `pkg/compiler/userdata.go:1104`:
```bash
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-slack" /opt/km/bin/km-slack
```
No new sidecar entry needed — the existing `km-slack` binary gains the new
`reply` subcommand. But existing sandboxes need `km destroy && km create`
because the binary at `/opt/km/bin/km-slack` was baked in at create time.

**Files:**
- `cmd/km-slack/main.go` — add `case "reply"` to dispatch; implement `runReply`
- `cmd/km-slack/main_dispatch_test.go` — add reply dispatch test

---

### Scope 4: Operator-side `km slack reply`

**What exists:**

`internal/app/cmd/slack.go` — the operator CLI. The `newSlackCmdInternal`
function at line 128 assembles subcommands via `slackCmd.AddCommand(...)`.
Currently registered: `init`, `test`, `status`, `rotate-token`,
`rotate-signing-secret`, `manifest`, `invite`, `adopt`.

The operator sends envelopes as `SenderOperator = "operator"` (defined in
`pkg/slack/payload.go:46`). The operator key is loaded by
`loadSlackOperatorKey` at `slack.go:864` — reads SSM
`/{prefix}/sandbox/operator/signing-key`.

The bot token (for direct `chat.postMessage`) is available via
`buildSlackCmdDeps` → `SlackCmdDeps.Slack` field (a `*kmslack.Client`
satisfying `SlackAPI`). This client is wired at `slack.go:228-230` from the
bot token read from SSM. The `chat.postMessage` method is `SlackAPI.PostMessage`.

The DDB client for direct operator queries is not yet in `SlackCmdDeps` — the
operator doesn't currently query `km-slack-threads` directly. But the pattern
for DDB access exists in `doctor.go` (uses `DoctorDeps.DDB`). For `km slack
reply --session`, the operator can query DDB directly (GSI Query).

**Operator `km slack reply` flags:**
- `--session <id>` → query GSI directly (operator has creds, no bridge needed)
- `--sandbox <id>` or `--alias <name>` → resolve channel for root fallback
- `--thread <ts>` + `--channel <id>` → post verbatim
- `--body <file>` + `--render plain|mrkdwn|blocks` (same as sandbox-side)

**Files:**
- `internal/app/cmd/slack.go` — add `newSlackReplyCmd`; add to
  `newSlackCmdInternal`; add DDB client to `SlackCmdDeps`
- `internal/app/cmd/slack.go` — add `RunSlackReply` exported function (testable)

---

### Scope 5: Cleanup/repair operator commands

**What exists:**

`km slack adopt` (in `internal/app/cmd/slack_adopt.go`) is the existing pattern
for a targeted DDB write operator command — it seeds the `km-slack-channels`
table directly with operator creds. The repair commands follow this same
pattern.

The `km-slack-threads` DDB client is `DDBQueryGetPutAPI` (supports GetItem,
PutItem, Query). For `forget-thread` and `prune-threads`, we also need
`DeleteItem`. The DDB IAM statement for the bridge already grants Query but
not DeleteItem on the threads table. The operator (running via local CLI) uses
the operator's AWS profile directly, not the bridge IAM — so no IAM change
is needed for operator repair commands.

For `prune-threads` (validate rows vs Slack API), we need `conversations.info`
to check channel existence. This is the same Slack client used by `km slack
adopt` for channel validation.

**New commands (all in `internal/app/cmd/slack.go` or new files):**

| Command | Action |
|---------|--------|
| `km slack threads <sandbox-id\|--alias>` | List `km-slack-threads` rows by sandbox_id (Scan with FilterExpression) |
| `km slack forget-thread (--session \| --thread+--channel)` | DeleteItem on km-slack-threads |
| `km slack prune-threads [sandbox] [--dry-run]` | Validate channel/thread existence via Slack API, delete dead rows |
| `km slack forget-channel <alias>` | DeleteItem on km-slack-channels (same table as `km slack adopt`) |

**`km slack threads` — scan pattern:** The threads table has `channel_id` as
hash key; no `sandbox_id` index today. To list by sandbox_id, we either:
(a) Scan with `FilterExpression = sandbox_id = :sid` — acceptable for a
    low-volume operator repair tool
(b) Use the session-index GSI (only if sandbox_id appears in the GSI — it
    doesn't; GSI is KEYS_ONLY on `claude_session_id`)

Use option (a): Scan + FilterExpression is appropriate for operator tooling.

**Files:**
- `internal/app/cmd/slack.go` — add `newSlackThreadsCmd`, `newSlackForgetThreadCmd`,
  `newSlackPruneThreadsCmd`, `newSlackForgetChannelCmd`; register all four in
  `newSlackCmdInternal`
- May split into a new `internal/app/cmd/slack_repair.go` file for clarity

---

### Scope 6: `km doctor` WARN checks

**What exists:**

`internal/app/cmd/doctor_slack.go` — existing Slack doctor checks following
the `checkSlackXxx(ctx, deps...) CheckResult` pattern.

Checks are registered in `internal/app/cmd/doctor.go` around line 4081-4103
via closures appended to the `checks` slice.

**New checks:**

**Check A: Thread rows pointing at non-existent channels**
- Name: `slack_thread_dead_channels`
- Scan `km-slack-threads` (paginated), collect unique `channel_id` values,
  call `conversations.info` on each; WARN on `channel_not_found`.
- Mitigation: reference `km slack prune-threads` in remediation message.

**Check B: Alias rows in `km-slack-channels` whose channel is gone**
- Name: `slack_channel_dead_alias`
- Scan `km-slack-channels`, call `conversations.info` on each `channel_id`;
  WARN on `channel_not_found`.
- Mitigation: reference `km slack forget-channel <alias>` + `km slack adopt`.

**Pattern to follow:** `checkSlackBotUserIDCached` at
`internal/app/cmd/doctor_slack.go` (in a separate `doctor_slack_bot_user_id.go`
file per the file naming pattern) — each check in its own file with its
test file.

**DDB deps needed:**
- `DoctorDeps` (in `doctor.go:342`) needs a threads-table scan function and
  channels-table scan function added — or reuse the existing `DDB` client with
  table names from config.

**Files:**
- `internal/app/cmd/doctor_slack.go` (or new `doctor_slack_threads.go`) — add
  `checkSlackThreadDeadChannels` and `checkSlackChannelDeadAlias` functions
- `internal/app/cmd/doctor.go` — register the two checks in the checks slice
- Test files: `doctor_slack_threads_test.go`

---

### Scope 7: Skill doc + plugin bump

**What exists:**

- `skills/slack/SKILL.md` — current skill file (~130 lines), covers `km-slack post`,
  threading, render modes. No section on `km-slack reply` (because it doesn't
  exist yet).
- `.claude-plugin/plugin.json` — `"version": "0.4.7"`
- `.claude-plugin/marketplace.json` — `"version": "0.4.7"`

**What to change:**

Add a new `## Session-aware Reply (km-slack reply)` section to `skills/slack/SKILL.md`
covering: resolution order, `--session` override, auto-detect heuristic, channel-root
fallback, and operator-side cross-reference.

Bump both `plugin.json` and `marketplace.json` from `0.4.7` to `0.4.8`.

---

## Standard Stack

### Core
| Component | Version/Location | Purpose |
|-----------|-----------------|---------|
| DynamoDB GSI | AWS managed | Index `km-slack-threads` by `claude_session_id` |
| `pkg/slack/bridge/aws_adapters.go` | current | DDB adapter extended with `LookupBySession` |
| `pkg/slack/payload.go` | current | Envelope type extended with `SessionID` field |
| `cmd/km-slack/main.go` | current | New `reply` subcommand |
| `internal/app/cmd/slack.go` | current | New operator `reply` + 4 repair subcommands |
| `internal/app/cmd/doctor_slack.go` | current | Two new doctor WARNs |

### Supporting
| Library | Purpose |
|---------|---------|
| `pkg/slack/bridge/events_interfaces.go` | `SlackThreadStore` interface extended |
| `infra/modules/dynamodb-slack-threads/v1.1.0/` | New module version with GSI |
| `infra/live/use1/dynamodb-slack-threads/terragrunt.hcl` | Source pointer bumped to v1.1.0 |

---

## Architecture Patterns

### Pattern 1: Adding a New Signed Bridge Action

Follow `ActionPermalink` (Phase 70) exactly:
1. Add constant to `pkg/slack/payload.go`
2. Add it to the action allow-list in `handler.go:96`
3. Add a dispatch case in the switch at `handler.go:255`
4. Implement the handler logic using existing injected dependencies
5. Return `jsonResp(200, map[string]any{"ok": true, ...})`

The key difference for `lookup-thread`: the channel-ownership check (step 6 in
`Handle`) cannot apply because the sandbox is supplying a session_id, not a
channel. The security check is instead: `assert result.sandbox_id == env.SenderID`.

### Pattern 2: Module Version Bump (in-place schema change)

Phase 104 did this for `dynamodb-slack-channels`: created a new `v1.0.0/main.tf`
from scratch. For `km-slack-threads` v1.1.0, copy v1.0.0 and add the GSI block.
The live `terragrunt.hcl` source pointer is the only change in the live config.

### Pattern 3: Operator Subcommand

Follow `newSlackAdoptCmd` in `internal/app/cmd/slack_adopt.go` — it is the
cleanest example of: Cobra command + flag parsing + DDB call + SSM call + test.

### Pattern 4: Doctor Check

Follow `checkSlackBotUserIDCached` in
`internal/app/cmd/doctor_slack_bot_user_id.go` — function takes injected deps,
returns `CheckResult`, registered in `doctor.go` checks slice.

### Session ID Auto-detect (Claude)

Claude Code writes session JSONL files to:
```
~/.claude/projects/<url-encoded-path>/<session-uuid>.jsonl
```

The auto-detect strategy: `find ~/.claude/projects -name '*.jsonl' -type f`
then take the file with the highest mtime. The session id is the filename stem.
This is how Phase 106 implemented post-on-mint (detect new/changed session id
after agent completes turn).

### Session ID Auto-detect (Codex)

Codex stores sessions in `~/.codex/store/<session-id>.json` (or similar).
The DDB `claude_session_id` column is agent-agnostic per Phase 70 hangover note.
The `reply` subcommand branches on `$KM_AGENT` to pick the correct path.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead |
|---------|-------------|-------------|
| Channel existence check | Custom HTTP call to Slack | `pkg/slack/client.go` — `GetChannelInfo` or `conversations.info` via existing `*kmslack.Client` |
| DDB scan with filter | Raw `dynamodb:Scan` wrapper | Follow existing scan pattern in `DDBRunningChannelLister` at `pkg/slack/bridge/aws_adapters.go` |
| Envelope signing for operator reply | New signing path | Reuse `slack.SignEnvelope` + `slack.BuildEnvelope(ActionPost, SenderOperator, ...)` |

---

## Common Pitfalls

### Pitfall 1: Channel Ownership Check Bypass for `lookup-thread`

**What goes wrong:** The bridge step 6 enforces `owned == env.Channel`. For
`lookup-thread`, the sandbox doesn't supply a channel — it supplies a session id.
Naively skipping step 6 would let any sandbox query any thread row.

**How to avoid:** After the GSI query returns matching rows, filter to only
`sandbox_id == env.SenderID`. Return `found:false` (not an error) when the session
id exists but belongs to a different sandbox. This preserves the boundary without
breaking the channel-ownership step entirely — just replace it with a sandbox-id
ownership check.

### Pitfall 2: Empty-string GSI Key Collision

**What goes wrong:** If the bridge Upsert ever writes `claude_session_id: ""`
(an empty string), DynamoDB will happily index it in the GSI. All such rows
would appear together under the empty-string key and pollute queries.

**How to avoid:** The scope spec explicitly says the bridge Upsert omits
`claude_session_id`. Verify `pkg/slack/bridge/aws_adapters.go:Upsert` does
NOT write `claude_session_id`. It currently does not (confirmed — Upsert only
writes `channel_id`, `thread_ts`, `sandbox_id`, `created_at`, `last_turn_ts`,
`turn_count`, `ttl_expiry`). The no-write invariant must not be broken.

### Pitfall 3: Module Version Bump Requires `km init`, NOT `--sidecars`

**What goes wrong:** A DynamoDB GSI addition is a Terraform resource change.
Only a full `km init --dry-run=false` (terragrunt apply) adds the GSI. `km init
--sidecars` only rebuilds binaries. `km init --lambdas` only rebuilds Lambda zips.

**How to avoid:** Deploy notes must specify `make build-lambdas` + `km init
--dry-run=false`. The `dynamodb-slack-threads` module has no protection from
`--sidecars` skipping it.

### Pitfall 4: `EnvelopeVersion` Stays at 1

**What goes wrong:** Adding a new field to `SlackEnvelope` might tempt bumping
`EnvelopeVersion` to 2, which would break all existing sandboxes until they are
recreated.

**How to avoid:** Phase 68 established the precedent: additive zero-valued
fields in `SlackEnvelope` are backward-compatible. `EnvelopeVersion` stays at 1.
Old sandboxes that don't send `session_id` will simply have an empty `SessionID`
field — the bridge must treat empty `SessionID` in a `lookup-thread` envelope as
a validation error (400 `missing_session_id`), not a crash.

### Pitfall 5: `make build` Must Precede `km init`

Per `project_make_build_precedes_km_init.md` memory note: when a phase edits
an existing regionalModules entry (not adding a new one), `make build` is still
needed to compile the operator binary that will run `km init`. For this phase,
the module entry already exists; the version pointer changes in the
live terragrunt.hcl. `make build` is needed to get the latest binary; `make
build-lambdas` to rebuild the bridge zip with the new `lookup-thread` action.

### Pitfall 6: `km-slack reply` Session Auto-detect on Codex Sandboxes

**What goes wrong:** Codex session files may not follow the same path convention
as Claude. The `$KM_AGENT` env var must be consulted before auto-detecting.

**How to avoid:** Branch explicitly on `$KM_AGENT == "codex"` in `runReply`.
Document the Codex session path in the skill doc. If the codex path is
uncertain, fall back to the channel-root post and log a WARN.

---

## Code Examples

### Existing Action Dispatch Pattern (ActionPermalink)

```go
// Source: pkg/slack/bridge/handler.go:400-424
case slack.ActionPermalink:
    if env.MessageTS == "" {
        return errResp(400, "missing_message_ts")
    }
    permalink, err := h.Slack.GetPermalink(ctx, env.Channel, env.MessageTS)
    if err != nil {
        return slackResponse("", err)
    }
    return jsonResp(200, map[string]any{"ok": true, "permalink": permalink})
```

The `lookup-thread` case will follow the same structure, using `env.SessionID`
instead of `env.MessageTS` and querying `h.Threads.LookupBySession`.

### Existing `DDBThreadStore.LookupSandbox` (pattern for `LookupBySession`)

```go
// Source: pkg/slack/bridge/aws_adapters.go:895-912
func (s *DDBThreadStore) LookupSandbox(ctx context.Context, channelID, threadTS string) (string, error) {
    out, err := s.Client.GetItem(ctx, &dynamodb.GetItemInput{
        TableName: awssdk.String(s.TableName),
        Key: map[string]dynamodbtypes.AttributeValue{
            "channel_id": &dynamodbtypes.AttributeValueMemberS{Value: channelID},
            "thread_ts":  &dynamodbtypes.AttributeValueMemberS{Value: threadTS},
        },
    })
    // ...
}
```

`LookupBySession` replaces `GetItem` with `Query` on the `session-index` GSI.

### Poller DDB Write (source of truth for `claude_session_id`)

```go
// Source: pkg/compiler/userdata.go:2018-2030 (bash in userdata)
aws dynamodb put-item --table-name "$THREADS_TABLE" --item "{
    \"channel_id\":{\"S\":\"$CHANNEL\"},
    \"thread_ts\":{\"S\":\"$THREAD_TS\"},
    \"claude_session_id\":{\"S\":\"$NEW_SESSION\"},
    \"agent_type\":{\"S\":\"$EFFECTIVE_AGENT\"},
    \"last_assistant_msg\":{\"S\":$LAST_MSG_JSON},
    \"sandbox_id\":{\"S\":\"$SANDBOX_ID\"},
    \"last_turn_ts\":{\"S\":\"$NOW\"},
    \"ttl_expiry\":{\"N\":\"$TTL_EXPIRY\"}
}"
```

This is the full row shape the GSI will index.

### Doctor Check Registration Pattern

```go
// Source: internal/app/cmd/doctor.go:4081-4087
checks = append(checks, func(ctx context.Context) CheckResult {
    r := checkSlackBotUserIDCached(ctx, cfg.GetSsmPrefix(), getUID)
    if r.Status == CheckError {
        r.Status = CheckWarn
    }
    return r
})
```

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` package + `testify/assert` |
| Config file | none (project uses `go test ./...`) |
| Quick run command | `go test ./pkg/slack/... ./cmd/km-slack/... ./internal/app/cmd/... -count=1 -timeout 120s` |
| Full suite command | `go test ./... -count=1 -timeout 600s` |

### Phase Requirements → Test Map

| Scope | Behavior | Test Type | Automated Command |
|-------|----------|-----------|-------------------|
| 1 GSI | `claude_session_id` attribute added to v1.1.0 main.tf | manual/TF | `terraform plan` in module dir |
| 2 Bridge action | `lookup-thread` envelope dispatched, GSI queried, sandbox_id filter applied | unit | `go test ./pkg/slack/bridge/ -run TestHandler_LookupThread` |
| 2 Bridge action | Missing `SessionID` returns 400 | unit | `go test ./pkg/slack/bridge/ -run TestHandler_LookupThread_MissingSessionID` |
| 2 Bridge action | Cross-sandbox lookup returns `found:false` | unit | `go test ./pkg/slack/bridge/ -run TestHandler_LookupThread_WrongSandbox` |
| 3 km-slack reply | Resolution chain order: --thread > env > session > fallback | unit | `go test ./cmd/km-slack/ -run TestRunReply` |
| 3 km-slack reply | Auto-detect newest session file (Claude path) | unit | `go test ./cmd/km-slack/ -run TestAutoDetectClaudeSession` |
| 3 km-slack reply | Fallback to channel root when no session found | unit | `go test ./cmd/km-slack/ -run TestRunReply_FallbackToChannelRoot` |
| 4 operator reply | Session → DDB GSI → post | unit | `go test ./internal/app/cmd/ -run TestRunSlackReply` |
| 5 repair cmds | `forget-thread` deletes correct DDB row | unit | `go test ./internal/app/cmd/ -run TestRunSlackForgetThread` |
| 5 repair cmds | `prune-threads --dry-run` lists dead rows without deleting | unit | `go test ./internal/app/cmd/ -run TestRunSlackPruneThreads_DryRun` |
| 5 repair cmds | `forget-channel` deletes correct alias row | unit | `go test ./internal/app/cmd/ -run TestRunSlackForgetChannel` |
| 6 doctor | Dead channel WARN emitted when channel_not_found | unit | `go test ./internal/app/cmd/ -run TestCheckSlackThreadDeadChannels` |
| 6 doctor | Dead alias WARN emitted when channel_not_found | unit | `go test ./internal/app/cmd/ -run TestCheckSlackChannelDeadAlias` |

### Sampling Rate
- **Per task commit:** `go test ./pkg/slack/bridge/ ./cmd/km-slack/ -count=1 -timeout 60s`
- **Per wave merge:** `go test ./... -count=1 -timeout 600s`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

The test infrastructure exists (`go test` + existing test files per package).
New test files needed (Wave 0 scaffolding):

- [ ] `pkg/slack/bridge/lookup_thread_handler_test.go` — covers bridge lookup-thread action unit tests
- [ ] `cmd/km-slack/main_reply_test.go` — covers reply subcommand resolution chain
- [ ] `internal/app/cmd/slack_repair_test.go` — covers repair commands
- [ ] `internal/app/cmd/doctor_slack_threads_test.go` — covers new doctor checks

Framework install: already present (Go 1.21+, `testify` in go.mod). No new framework needed.

---

## Risks & Gotchas

### Risk 1: GSI Add May Require DDB Backfill Window

Adding a GSI to an existing DynamoDB table is an online operation; DynamoDB
builds the index in the background. For a low-volume table (km-slack-threads),
this takes seconds to minutes. No code change required — the GSI is available
as soon as DynamoDB marks it ACTIVE. Terragrunt apply waits for the resource
to reach ACTIVE state before returning. Existing rows with `claude_session_id`
set will be auto-indexed.

### Risk 2: `EnvelopeVersion` + JSON Canonical Ordering

The `SlackEnvelope` struct uses alphabetical JSON tags for canonical signing.
`session_id` (lowercase) sorts as: after `s3_key`, before `sender_id`.
The struct must insert `SessionID string \`json:"session_id"\`` at the correct
position in the struct definition. The `CanonicalJSON` function uses
`encoding/json` struct tag ordering — getting this wrong produces a signature
mismatch between sender and verifier. The existing tests in `pkg/slack/payload_test.go`
verify canonical JSON; add a test with a non-empty `SessionID`.

### Risk 3: Bridge IAM for `dynamodb:Query` on Index

The existing bridge IAM statement (`DDBSlackThreads` at
`infra/modules/lambda-slack-bridge/v1.0.0/main.tf:155-184`) already grants:
```hcl
Action = ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:Query"]
Resource = [
  "arn:...table/${var.slack_threads_table_name}",
  "arn:...table/${var.slack_threads_table_name}/index/*",
]
```
The `/index/*` wildcard covers the new `session-index` GSI. **No IAM change
to the bridge module is required.** Confirmed at `main.tf:166-172`.

### Risk 4: Module Lock Drift After Version Bump

Per `project_module_source_lock_drift.md`: after creating `v1.1.0/`, sweep
any stale `.terraform.lock.hcl` files from the module directory (these can
appear from bare `terraform validate` runs). The `v1.1.0/` directory should
start clean with no lock file; terragrunt manages locking from the live unit's
cache.

### Risk 5: `doctor_test.go` TestRunInitPlan_ModuleOrder

Per `project_module_order_test_count_debt.md`: `TestRunInitPlan_ModuleOrder`
hardcodes the `regionalModules()` count. The `dynamodb-slack-threads` module
already has an entry in `regionalModules()` at `init.go:564` — the v1.0.0→v1.1.0
bump is an in-place source path change, NOT a new module entry. Module count
stays unchanged. No test count update required.

### Risk 6: Dead Channel Detection Requires `conversations.info` Slack Call

`km doctor` runs offline against DDB; calling Slack's `conversations.info` per
row introduces a live API call. The check should be gated: only run when the
Slack client is available (bot token set in SSM). Follow `checkSlackTokenValidity`'s
SKIPPED pattern when the bot token is absent.

### Risk 7: `km-slack reply` on Non-Slack Sandboxes

If `KM_NOTIFY_SLACK_ENABLED != 1` or `KM_SLACK_CHANNEL_ID` is empty,
`km-slack reply` must exit with a clean error message (not panic). The fallback
to channel root requires `KM_SLACK_CHANNEL_ID` to be non-empty. If it is empty,
`reply` should exit non-zero with a helpful message: "Slack not configured for
this sandbox; re-create with notification.slack.enabled: true."

---

## Open Questions

1. **Codex session file path — exact location**
   - What we know: Phase 70 note says `claude_session_id` column stores Codex
     session IDs too; Codex installs `~/.codex/config.toml` per Phase 70 SC-1.
   - What's unclear: The exact path of Codex session JSONL/JSON files on the
     sandbox. Claude uses `~/.claude/projects/**/*.jsonl`; Codex may use
     `~/.codex/store/` or similar.
   - Recommendation: In the sandbox's `km-slack reply` auto-detect code, add
     a fallback that checks `~/.codex` subdirectories for the newest session
     file when `KM_AGENT=codex`. Document "unverified path" in skill doc
     and add a WARN log if no file found.

2. **`SlackEnvelope.SessionID` field name — new field vs. reuse `Body`**
   - The scope spec says payload `{session_id}`. A new field is cleaner but
     requires alphabetical insertion into the struct. Reusing `Body` avoids
     a struct change but is semantically confusing.
   - Recommendation: Use a new `SessionID string \`json:"session_id"\`` field.
     The planner can confirm if alphabetical ordering is a concern.

3. **`km slack threads` — does it need a `sandbox_id` GSI on `km-slack-threads`?**
   - The repair command lists rows by sandbox_id. A Scan with FilterExpression
     works for low-volume operator tooling; a GSI would be overkill.
   - Recommendation: Use Scan+Filter. Document the O(n) cost in command help.

---

## Sources

### Primary (HIGH confidence)
- `pkg/slack/bridge/handler.go` — action dispatch pattern
- `pkg/slack/bridge/aws_adapters.go` — DDBThreadStore implementation
- `pkg/slack/bridge/events_interfaces.go` — SlackThreadStore interface
- `pkg/slack/payload.go` — envelope structure and action constants
- `infra/modules/dynamodb-slack-threads/v1.0.0/main.tf` — current table schema
- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` — bridge IAM statements
- `infra/live/use1/dynamodb-slack-threads/terragrunt.hcl` — live config
- `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` — bridge live config
- `cmd/km-slack/main.go` — existing sidecar subcommands
- `internal/app/cmd/slack.go` — operator slack commands
- `internal/app/cmd/doctor_slack.go` — doctor check pattern
- `internal/app/cmd/doctor.go:4070-4103` — check registration pattern
- `pkg/compiler/userdata.go:2018-2030` — poller DDB write (full row shape)
- `pkg/compiler/userdata.go:1104` — sidecar download path
- `internal/app/cmd/init.go:3027,564` — sidecar build list + module entry
- `.claude-plugin/plugin.json` — version 0.4.7

### Metadata

**Confidence breakdown:**
- GSI schema: HIGH — exact main.tf pattern read
- Bridge action pattern: HIGH — ActionPermalink/Update are direct templates
- Sandbox sidecar: HIGH — buildAndUploadSidecars path confirmed
- Claude session path: MEDIUM — inferred from Phase 106 post-on-mint pattern;
  exact glob not directly verified in Go code
- Codex session path: LOW — Phase 70 hangover note confirms column reuse;
  exact file path unverified

**Research date:** 2026-06-12
**Valid until:** 2026-07-12 (stable infrastructure — 30-day window)
