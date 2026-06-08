# Phase 102: GitHub bridge agent verbs — Research

**Researched:** 2026-06-08
**Domain:** GitHub bridge (pkg/github/bridge/), userdata.go GitHub poller, km doctor
**Confidence:** HIGH — all findings verified against live code

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**D1 — Syntax:** Slash verbs `/claude` / `/codex`, reserved, parsed **anywhere** in the comment
(consistent with Phase 99 `/command` tokens). NOT Slack's `codex:` colon prefix.
Recognized by the same anywhere-scan + code-strip the Phase 99 parser uses
(`^/[A-Za-z][A-Za-z0-9_-]*$`, fenced/inline code stripped first).

**D2 — Axis:** Agent verbs are a **separate axis** from Phase 99 template commands; they
**compose**. A comment may carry **0–1 agent verb + 0–1 template command**; both stripped
from `{{args}}`. Composition: `@bot /codex /patch fix the flaky test` → agent=Codex,
template=/patch, `{{args}}`="fix the flaky test".

**D3 — Persistence:** Per-thread. The verb writes `agent_type` onto the `(repo, number)` row;
follow-up comments with no verb continue with that agent.

**D4 — Precedence:** `EFFECTIVE_AGENT = AGENT_OVERRIDE (verb) | THREAD_AGENT_TYPE | $AGENT (profile default)`.

**D5 — Cross-agent switch:** Single `agent_session_id` column, reset on switch. If
`AGENT_OVERRIDE` is set, differs from `THREAD_AGENT_TYPE`, and a session exists → clear
`RESUME_ARG`. New agent starts fresh, captures new session, overwrites `agent_session_id` +
`agent_type`. Switching back = fresh again. No Slack-style new-top-level handoff.

**D6 — Codex availability:** `/codex` requires a Codex-capable profile. Runtime helpful-error
comment when `EFFECTIVE_AGENT=codex` and `codex` binary is absent. Not a hard gate.

**Verb-count rule:** ≤ 1 agent verb per comment. Two distinct agent verbs → error reply
("🤖 Specify one agent — found /claude and /codex."), no dispatch. Repeats deduped.

**Reserved tokens:** `claude`, `codex`, `help` are reserved: a `github.commands` entry with
any of these names is ignored with a `km doctor` WARN.

**`/help` extension:** Phase 99 built-in `/help` is extended to list `/claude`, `/codex`
and note the thread's current agent.

**Dormancy / back-compat:** No verb → `EFFECTIVE_AGENT` falls to profile default; byte-identical
to today (`userdata.go:2248` hardwires `EFFECTIVE_AGENT="$AGENT"`). Old `km-github-threads`
rows without `agent_type` → treated as profile default. Additive, no migration.

### Claude's Discretion
- Exact Go function/file layout for the verb partitioner (extend the existing Phase 99 parser
  function vs. a sibling — follow existing parser structure).
- Whether `LookupSandbox` is extended to also return `agent_type` or a sibling accessor is added.
- Exact bash for the codex-binary-absent guard in `userdata.go`.
- Test file organization (extend existing parser/poller test files vs. new).

### Deferred Ideas (OUT OF SCOPE)
- Slack-style 8-step cross-agent handoff — non-goal (GitHub thread model makes it unnecessary).
- Per-agent session retention across switches — rejected (D5).
- Agents beyond claude/codex — future, slot in as new `agent_type` values.
- `km-config.yaml` surface for the verbs — non-goal (built-in reserved tokens).
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| GH-AGENT-VERB | Bridge-side agent verb parser: `/claude` + `/codex` recognized anywhere, code-stripped, deduped, partitioned from template commands, stripped from `{{args}}`; two-distinct-verb error reply | `ParseCommands` in `commands.go` is the extension point; `StripCode` + `isCommandCandidate` already handle the scanner mechanics |
| GH-AGENT-PERSIST | `agent_type` attribute on `km-github-threads` (schema-on-write); `LookupSandbox` extended to return it; write-back on successful turn | `DynamoGitHubThreadStore.LookupSandbox` (aws_adapters.go:712) today projects only `sandbox_id, agent_session_id`; `UpdateSession` (line 776) needs a companion that also writes `agent_type` |
| GH-AGENT-SWITCH | Cross-agent switch clears `RESUME_ARG` and `agent_session_id` when verb differs from stored `agent_type`; new agent starts fresh | Exact bash pattern verified in Slack poller (userdata.go:1863-1871); GitHub poller needs same 3-line reset block |
| GH-AGENT-POLLER | Poller computes `EFFECTIVE_AGENT` via D4 precedence; reads `agent_type` from DDB get-item; writes `agent_type` back on success; codex-binary-absent guard posts comment via `km-github comment` | Currently line 2248: `EFFECTIVE_AGENT="$AGENT"` (hardwire); DDB get-item at line 2182 only projects `agent_session_id`; write-back at line 2323 only writes `agent_session_id` |
| GH-AGENT-PROFILE | `/codex` on Claude-only profile posts helpful-error comment; not a hard gate | `km-github comment --repo $REPO --number $NUMBER --body '...'` is already wired in the poller's preamble; `command -v codex >/dev/null 2>&1` is the guard pattern used throughout userdata.go |
| GH-AGENT-E2E | End-to-end: `/codex /patch …` dispatches Codex, writes `agent_type=codex` to DDB; follow-up without verb continues Codex; `/claude` resets to Claude with fresh session | No existing automated E2E — poller bash is tested manually/via `km create`; bridge parser tests are pure-function Go |
</phase_requirements>

---

## Summary

Phase 102 is a **small, surgical extension** of shipped Phases 97/98/99. The design spec's
characterization is exactly right: the dispatch fork, session capture, and DDB round-trip
all exist today; what's missing is (1) a verb partitioner in the bridge, (2) an `Agent`
field on `GitHubEnvelope`, (3) `agent_type` DDB reads and writes, and (4) `EFFECTIVE_AGENT`
computation in the GitHub poller.

The Slack Phase 70 analog is directly parallel in the poller section of `userdata.go`
(Slack poller at ~line 1670-1871; GitHub poller at ~line 2177-2338). The Slack poller
already has the full precedence + cross-agent switch logic; the GitHub poller needs an
analogous block, but simpler (no 8-step handoff, no new top-level message — just reset
and continue in the same PR thread).

The bridge parser (`commands.go`) is a clean pure-function module with excellent existing
tests (`commands_test.go`, `webhook_handler_phase99_test.go`). Adding agent-verb parsing
follows the exact same pattern as the Phase 99 `/help` reserved built-in.

**Primary recommendation:** Treat this as three self-contained work units — (A) bridge
parser extension + envelope field, (B) DDB interface + implementation extension, (C)
poller bash block. Unit A and B are pure Go; Unit C is bash inside a Go heredoc. All
three land in the same `make build-lambdas` + `km init --dry-run=false` deploy.

---

## Standard Stack

### Core — no new dependencies
| Component | Version/Location | Purpose |
|-----------|-----------------|---------|
| `pkg/github/bridge/commands.go` | Shipped Phase 99 | Verb parser extension point |
| `pkg/github/bridge/payload.go` | Line 75 | `GitHubEnvelope` struct to extend |
| `pkg/github/bridge/interfaces.go` | Line 155 | `GitHubThreadStore` interface to extend |
| `pkg/github/bridge/aws_adapters.go` | Line 705 | `DynamoGitHubThreadStore` impl to extend |
| `pkg/compiler/userdata.go` | Line 2079-2342 | GitHub inbound poller bash heredoc |
| `internal/app/cmd/doctor.go` | Line 1502-1504 | `help`-shadow check to extend |

No new libraries. No new Lambda, no new SQS queue, no new DDB table, no new TF module.

---

## Architecture Patterns

### Current State — Bridge Layer

**`ParseCommands` function** (`commands.go:208`) — the Phase 99 scanner. It:
1. Calls `StripCode(body)` — removes fenced ``` blocks and `` `inline` `` spans.
2. Splits on `strings.Fields`.
3. Tests each token via `isCommandCandidate` — `^/[A-Za-z][A-Za-z0-9_-]*$`, no embedded slash.
4. Intercepts `"help"` as a reserved built-in before the command map lookup.
5. Looks up remaining tokens against the `commands map[string]CommandEntry`.
6. Returns `ParseResult{HelpRequested, Known []string, MultiError}`.

**Key insight:** The agent verb partitioner is logically a pre-pass on the same token list.
The cleanest approach (Claude's discretion) is to introduce a **`ParseAgentVerbs`** pure
function alongside `ParseCommands`, or to extend `ParseResult` with an `Agent string` field
and handle agent tokens inside `ParseCommands` before the command map lookup. The latter
avoids a double-scan and mirrors how `HelpRequested` is handled today.

**`ExtractArgs`** (`commands.go:255`) strips `@mention` + `/{commandToken}` from body.
The agent verb `/claude` or `/codex` also needs stripping from `{{args}}`. `ExtractArgs`
currently takes one `commandToken` string. For Phase 102, the agent verb token also needs
stripping — either `ExtractArgs` gains an `agentVerbToken string` parameter, or a wrapper
strips both in sequence.

**`RunCommandPass`** (`commands.go:415`) is the IO-free entry point. It calls
`ParseCommands` and `ExtractArgs`. The agent verb partitioner integrates here: before
building the prompt, strip the agent verb from args. The `CommandPassResult` does not need
to carry the agent verb — it is set on `GitHubEnvelope` by the caller (`Handle`).

**`GitHubEnvelope`** (`payload.go:75`) — add one field:
```go
Agent string `json:"agent,omitempty"`
```
This field is set by `Handle()` after `RunCommandPass` returns, before `json.Marshal(env)`.

**`Handle()` envelope construction** (`webhook_handler.go:398-415`):
```go
env := GitHubEnvelope{
    // existing fields ...
    Agent: parsed.AgentVerb, // new
}
```

### Current State — DDB Layer

**`GitHubThreadStore` interface** (`interfaces.go:155`):
```go
type GitHubThreadStore interface {
    LookupSandbox(ctx, repo, number) (sandboxID, sessionID string, err error)
    Upsert(ctx, repo, number, sandboxID) error
    UpdateSession(ctx, repo, number, sessionID) error
    InvalidateStaleSession(ctx, repo, number, newSandboxID) error
}
```

`LookupSandbox` returns `(sandboxID, sessionID string, err error)` — no `agent_type`.
The interface must be extended. Two options:

**Option A (recommended — simpler):** Change `LookupSandbox` return signature to return
`(sandboxID, sessionID, agentType string, err error)`. The fourth return "" when absent
(first dispatch or pre-Phase-102 row) — treated as profile default per D4. Update the
`ProjectionExpression` to include `agent_type`.

**Option B:** Add a sibling `LookupSandboxFull` accessor. More surgical but adds noise.

Option A is consistent with how `sessionID` was added — same return tuple, same
backward-compat treatment of absent attribute.

**`DynamoGitHubThreadStore.LookupSandbox`** (`aws_adapters.go:712`):
```go
ProjectionExpression: awssdk.String("sandbox_id, agent_session_id"),
```
Change to:
```go
ProjectionExpression: awssdk.String("sandbox_id, agent_session_id, agent_type"),
```

**Write-back — `UpdateSession`** (`aws_adapters.go:776`): Today writes only
`agent_session_id`. The update expression needs to include `agent_type`:
```go
UpdateExpression: awssdk.String("SET agent_session_id = :sid, agent_type = :at"),
```
This means `UpdateSession` gains an `agentType string` parameter (or a new
`UpdateSessionAndType` method is added — Claude's discretion, but extending the existing
method signature is cleaner than adding a fourth method).

### Current State — GitHub Poller (`userdata.go:2079-2342`)

The poller is a bash heredoc written at sandbox provisioning time. Key lines:

**Line 2096:** `AGENT="${KM_AGENT:-claude}"` — profile default, read once at boot.

**Lines 2147-2153:** Envelope field parsing:
```bash
REPO=$(echo "$BODY" | jq -r '.repo // empty')
NUMBER=$(echo "$BODY" | jq -r '.number // empty')
...
COMMENT_BODY=$(echo "$BODY" | jq -r '.body // empty')
HTML_URL=$(echo "$BODY" | jq -r '.html_url // empty')
```
**Add here:** `AGENT_OVERRIDE=$(echo "$BODY" | jq -r '.agent // empty')`

**Lines 2181-2188:** DDB get-item + session lookup:
```bash
DDB_THREAD=$(aws dynamodb get-item \
  --table-name "$GITHUB_THREADS_TABLE" \
  --key '{"repo":{"S":"$REPO"},"number":{"N":"$NUMBER"}}' \
  --projection-expression "agent_session_id" \
  ...
GITHUB_SESSION=$(echo "$DDB_THREAD" | jq -r '.Item.agent_session_id.S // empty' ...)
```
**Extend:** projection-expression adds `agent_type`; add `THREAD_AGENT_TYPE` extraction.

**Line 2248:** `EFFECTIVE_AGENT="$AGENT"` — the hardwire to replace:
```bash
THREAD_AGENT_TYPE=$(echo "$DDB_THREAD" | jq -r '.Item.agent_type.S // empty' 2>/dev/null || true)
[ -z "$THREAD_AGENT_TYPE" ] && THREAD_AGENT_TYPE="$AGENT"

if [ -n "$AGENT_OVERRIDE" ]; then
  if [ "$AGENT_OVERRIDE" != "$THREAD_AGENT_TYPE" ] && [ -n "$GITHUB_SESSION" ]; then
    # Cross-agent switch: clear stale session so new agent starts fresh.
    GITHUB_SESSION=""
    RESUME_ARG=""
  fi
  EFFECTIVE_AGENT="$AGENT_OVERRIDE"
else
  EFFECTIVE_AGENT="$THREAD_AGENT_TYPE"
fi
```

**Lines 2319-2326:** DDB write-back (success path):
```bash
aws dynamodb update-item \
  --update-expression "SET agent_session_id = :sid" \
  --expression-attribute-values '...'
```
**Extend:** `SET agent_session_id = :sid, agent_type = :at` with `:at` = `$EFFECTIVE_AGENT`.

**Codex-missing guard (D6):** Insert before the `if [ "$EFFECTIVE_AGENT" = "codex" ]; then`
dispatch fork:
```bash
if [ "$EFFECTIVE_AGENT" = "codex" ] && ! command -v codex >/dev/null 2>&1; then
  /opt/km/bin/km-github comment \
    --repo "$REPO" --number "$NUMBER" \
    --body "This sandbox's profile has no Codex; \`/codex\` is unavailable here."
  aws sqs delete-message --queue-url "$QUEUE_URL" --receipt-handle "$RECEIPT" \
    --region "$REGION" 2>/dev/null || true
  continue
fi
```

### Current State — km doctor (`doctor.go:1502-1504`)

```go
// 3. "help" not shadowed — reserved built-in.
if _, hasHelp := commands["help"]; hasHelp {
    warnings = append(warnings, `command "help" shadows the reserved built-in /help reply — rename to avoid unexpected behavior`)
}
```

**Extend** to also check `"claude"` and `"codex"`:
```go
reservedVerbs := []string{"help", "claude", "codex"}
for _, rv := range reservedVerbs {
    if _, has := commands[rv]; has {
        warnings = append(warnings, fmt.Sprintf(
            `command %q shadows a reserved verb (/help built-in or /claude//codex agent selector) — rename to avoid unexpected behavior`,
            rv,
        ))
    }
}
```

The remediation text at line 1567 should also reference agent verbs.

### Recommended File Layout for Phase 102 Changes

```
pkg/github/bridge/
├── commands.go            — ParseCommands: add AgentVerb field to ParseResult; intercept claude/codex
│                            ExtractArgs: strip agent verb token alongside command token
│                            RunCommandPass: strip agent verb from args; return agentVerb via ParseResult
├── payload.go             — GitHubEnvelope: add Agent string field
├── interfaces.go          — GitHubThreadStore: extend LookupSandbox signature; extend UpdateSession
├── aws_adapters.go        — DynamoGitHubThreadStore: update projection + write-back
├── commands_test.go       — Extend TestCommandParse with agent-verb cases
│                            (new tests: verb-recognized, stripped, two-verb error, compose)
└── webhook_handler_phase102_test.go  — bridge envelope carries Agent; two-verb error reply

pkg/compiler/
└── userdata.go            — GitHub poller: AGENT_OVERRIDE parse; precedence block; codex guard;
                             agent_type write-back

internal/app/cmd/
└── doctor.go              — checkGitHubCommandsValid: add claude/codex to reserved shadow check
    doctor_github_commands_test.go  — extend TestDoctorGitHubCommandsHelpShadow pattern
```

### Anti-Patterns to Avoid

- **Do NOT widen `DynamoGitHubThreadStore` with a PutItem path for `agent_type`**: use
  `UpdateItem` (the existing `update-item` pattern) to avoid the SandboxMetadata lossy
  round-trip footgun — this is why all continuity data lives in km-github-threads via
  UpdateItem, never PutItem on the full row.
- **Do NOT add `agent_type` to `km-sandboxes`**: it belongs only in km-github-threads (same
  reason the session ID is there, not in km-sandboxes).
- **Do NOT add a km-config.yaml surface** for the verbs: they are built-in reserved tokens.
- **Do NOT attempt an 8-step Slack handoff**: the PR is the thread; cross-agent switch just
  resets the session ID in place.
- **Do NOT call `km init --sidecars`** for this deploy: there is no SandboxProfile schema
  change; the deploy surface is `make build-lambdas` + `km init --dry-run=false`.
- **Do NOT rely on `LookupSandbox` returning a non-empty `agent_type`** on old rows: always
  treat "" as profile default (D4 backward compat).

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Markdown code stripping | Custom regex stripper | `StripCode()` (commands.go:117) | Already handles fenced ``` and `` `inline` `` spans; tested |
| Token candidacy check | Custom token validator | `isCommandCandidate()` (commands.go:170) | Same `^/[A-Za-z][A-Za-z0-9_-]*$` rule; already handles embedded-slash rejection |
| DDB UpdateItem for agent data | PutItem full-row replace | `UpdateItem` with expression | SandboxMetadata lossy round-trip footgun — UpdateItem is the established pattern |
| GitHub comment posting from poller | Custom HTTP client | `km-github comment` binary | Already downloaded to `/opt/km/bin/km-github`; used in preamble examples |
| Codex binary detection | Complex probe | `command -v codex >/dev/null 2>&1` | Same pattern used for claude, tmux, git throughout userdata.go |

---

## Common Pitfalls

### Pitfall 1: Forgetting that DDB projection-expression must include `agent_type`
**What goes wrong:** `LookupSandbox` returns `"" ` for `agentType` even after a verb was
written, because the DDB GetItem projection omits the attribute.
**How to avoid:** Update `ProjectionExpression: awssdk.String("sandbox_id, agent_session_id, agent_type")`.
**Warning signs:** Test shows `agentType=""` on rows that should carry the value.

### Pitfall 2: THREAD_AGENT_TYPE must default to $AGENT before EFFECTIVE_AGENT decision
**What goes wrong:** If `THREAD_AGENT_TYPE` is empty (pre-Phase-102 row), and `AGENT_OVERRIDE`
is also empty, then `EFFECTIVE_AGENT=""`, and the `if [ "$EFFECTIVE_AGENT" = "codex" ]`
fork falls to the `else` (claude) path. But a later log line shows empty agent. 
**How to avoid:** `[ -z "$THREAD_AGENT_TYPE" ] && THREAD_AGENT_TYPE="$AGENT"` before the
precedence block (mirrors the Slack poller pattern at line 1676).
**Warning signs:** `EFFECTIVE_AGENT` empty in logs.

### Pitfall 3: Cross-agent switch clears GITHUB_SESSION before RESUME_ARG is computed
**What goes wrong:** If `GITHUB_SESSION=""` is set before `RESUME_ARG` is computed, `RESUME_ARG`
correctly becomes `""`. But if RESUME_ARG was already set from the old session before the
switch block, the codex-path retry logic might still see a stale `RESUME_ARG`.
**How to avoid:** Clear both `GITHUB_SESSION` and `RESUME_ARG` together in the switch block,
exactly as the Slack poller does (lines 1869-1870).
**Warning signs:** Claude retry with `--resume <old-session>` on first codex turn.

### Pitfall 4: `ExtractArgs` must strip BOTH the agent verb AND the command token
**What goes wrong:** If only the command token is stripped from args, `/codex` appears in
`{{args}}` and is passed to the agent as literal prompt text.
**How to avoid:** Either add `agentVerbToken string` param to `ExtractArgs` and strip it
first, or strip the agent verb in a pre-pass before calling `ExtractArgs`.
**Warning signs:** Agent receives prompt containing `/codex` or `/claude` literally.

### Pitfall 5: agent_type write-back runs even when EFFECTIVE_AGENT = profile default
**What goes wrong:** On a fresh turn with no verb and no thread agent_type, `EFFECTIVE_AGENT`
= `$AGENT` (profile default). Writing `agent_type=$EFFECTIVE_AGENT` on every success is
fine and correct — it pins the thread to the current profile default, which is the right
behavior for follow-up turns.
**Note:** This is NOT a bug — it's intentional (D3 persistence). Old rows that never had
a verb still get `agent_type` written on their first post-Phase-102 turn.

### Pitfall 6: `km init --dry-run=false` is required (NOT `--sidecars`)
**What goes wrong:** Operators who try `km init --sidecars` to deploy this phase will get
the new binary but not the updated Lambda env block.
**How to avoid:** The bridge Lambda is redeployed via `make build-lambdas` + `km init
--dry-run=false`. Existing sandboxes need `km destroy && km create` to pick up the new
poller.
**Warning signs:** Agent verb parsed by bridge (envelope has `.agent`) but poller ignores
it because old poller script doesn't parse `.agent // empty`.

### Pitfall 7: `/help` extension must not dispatch to sandbox
**What goes wrong:** If `HelpRequested` is set AND an agent verb is present, the handler
must still short-circuit to the `/help` reply (no dispatch). The existing `Handle()` logic
intercepts `HelpRequested` before any command or agent logic; this ordering must be preserved
when the agent verb is added.
**How to avoid:** Agent verb parsing happens in `ParseCommands` but `HelpRequested` check
in `RunCommandPass` still runs first (current line 429); do not alter this ordering.

---

## Code Examples

Verified patterns from live code:

### Pattern: Extending ParseResult (follow HelpRequested model)
```go
// Source: pkg/github/bridge/commands.go:92
type ParseResult struct {
    HelpRequested bool
    Known         []string
    MultiError    bool
    // Phase 102: agent verb found in body (one of "claude", "codex", or "")
    AgentVerb     string
    // Phase 102: true when both /claude and /codex found (error path)
    AgentVerbConflict bool
}
```

### Pattern: Intercepting reserved tokens in ParseCommands (follow help model)
```go
// Source: pkg/github/bridge/commands.go:222 (help interception)
// /help is a reserved built-in — intercept before the defined-command lookup.
if name == "help" {
    result.HelpRequested = true
    continue
}

// Phase 102: agent verbs — intercept before command map lookup.
if name == "claude" || name == "codex" {
    if result.AgentVerb == "" || result.AgentVerb == name {
        result.AgentVerb = name // dedup: same verb twice = fine
    } else {
        result.AgentVerbConflict = true // /claude AND /codex = error
    }
    continue
}
```

### Pattern: GitHubEnvelope Agent field
```go
// Source: pkg/github/bridge/payload.go:75 (extend)
type GitHubEnvelope struct {
    Source        string `json:"source"`
    Repo          string `json:"repo"`
    Number        int    `json:"number"`
    Kind          string `json:"kind"`
    CommentID     int64  `json:"comment_id"`
    HTMLURL       string `json:"html_url"`
    Sender        string `json:"sender"`
    Body          string `json:"body"`
    InstallID     string `json:"install_id"`
    DefaultBranch string `json:"default_branch,omitempty"`
    Agent         string `json:"agent,omitempty"` // Phase 102: parsed agent verb ("claude"|"codex"|"")
}
```

### Pattern: DDB LookupSandbox with agent_type
```go
// Source: pkg/github/bridge/aws_adapters.go:712 (extend)
func (s *DynamoGitHubThreadStore) LookupSandbox(ctx context.Context, repo string, number int) (sandboxID, sessionID, agentType string, err error) {
    out, err := s.Client.GetItem(ctx, &dynamodb.GetItemInput{
        TableName: awssdk.String(s.TableName),
        Key: map[string]dynamodbtypes.AttributeValue{
            "repo":   &dynamodbtypes.AttributeValueMemberS{Value: repo},
            "number": &dynamodbtypes.AttributeValueMemberN{Value: strconv.Itoa(number)},
        },
        ProjectionExpression: awssdk.String("sandbox_id, agent_session_id, agent_type"),
    })
    // ... extract agentType from out.Item["agent_type"] ...
}
```

### Pattern: DDB write-back with agent_type (UpdateSession extension)
```go
// Source: pkg/github/bridge/aws_adapters.go:776 (extend)
func (s *DynamoGitHubThreadStore) UpdateSession(ctx context.Context, repo string, number int, sessionID, agentType string) error {
    _, err := s.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
        TableName: awssdk.String(s.TableName),
        Key: map[string]dynamodbtypes.AttributeValue{
            "repo":   &dynamodbtypes.AttributeValueMemberS{Value: repo},
            "number": &dynamodbtypes.AttributeValueMemberN{Value: strconv.Itoa(number)},
        },
        UpdateExpression: awssdk.String("SET agent_session_id = :sid, agent_type = :at"),
        ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{
            ":sid": &dynamodbtypes.AttributeValueMemberS{Value: sessionID},
            ":at":  &dynamodbtypes.AttributeValueMemberS{Value: agentType},
        },
    })
    // ...
}
```

### Pattern: Poller EFFECTIVE_AGENT precedence (mirrors Slack poller lines 1670-1709)
```bash
# Parse agent verb from envelope (Phase 102).
AGENT_OVERRIDE=$(echo "$BODY" | jq -r '.agent // empty' 2>/dev/null || true)

# Read thread's stored agent type from DDB (extended projection includes agent_type).
THREAD_AGENT_TYPE=$(echo "$DDB_THREAD" | jq -r '.Item.agent_type.S // empty' 2>/dev/null || true)
[ -z "$THREAD_AGENT_TYPE" ] && THREAD_AGENT_TYPE="$AGENT"  # default to profile

# D4 precedence: verb > thread agent_type > profile default.
# D5 cross-agent switch: clear session when switching agents mid-thread.
if [ -n "$AGENT_OVERRIDE" ]; then
  if [ "$AGENT_OVERRIDE" != "$THREAD_AGENT_TYPE" ] && [ -n "$GITHUB_SESSION" ]; then
    echo "[km-github-inbound-poller] cross-agent switch $THREAD_AGENT_TYPE→$AGENT_OVERRIDE; resetting session"
    GITHUB_SESSION=""
    RESUME_ARG=""
  fi
  EFFECTIVE_AGENT="$AGENT_OVERRIDE"
else
  EFFECTIVE_AGENT="$THREAD_AGENT_TYPE"
fi

# D6 codex-missing guard.
if [ "$EFFECTIVE_AGENT" = "codex" ] && ! command -v codex >/dev/null 2>&1; then
  /opt/km/bin/km-github comment \
    --repo "$REPO" --number "$NUMBER" \
    --body "This sandbox's profile has no Codex; \`/codex\` is unavailable here."
  aws sqs delete-message --queue-url "$QUEUE_URL" --receipt-handle "$RECEIPT" \
    --region "$REGION" 2>/dev/null || true
  continue
fi
```

### Pattern: km doctor reserved shadow check (extend existing lines 1502-1504)
```go
// Source: internal/app/cmd/doctor.go:1502 (extend)
for _, reserved := range []string{"help", "claude", "codex"} {
    if _, has := commands[reserved]; has {
        warnings = append(warnings, fmt.Sprintf(
            `command %q shadows a reserved verb — rename to avoid unexpected behavior`,
            reserved,
        ))
    }
}
```

### Pattern: two-verb error reply (in webhook_handler Handle())
```go
// Inserted in Handle() after RunCommandPass, before envelope construction.
if parsed.AgentVerbConflict {
    _ = h.Commenter.PostComment(ctx, installIDStr, owner, repo, payload.Issue.Number,
        "🤖 Specify one agent — found /claude and /codex.")
    return WebhookResponse{StatusCode: 200, Body: "ok"}
}
```

### Pattern: /help extension with agent listing
```go
// Source: pkg/github/bridge/commands.go:372 (buildHelpReply — extend)
func buildHelpReply(commands map[string]CommandEntry, effectiveDefaultCmd, currentAgentType string) string {
    var b strings.Builder
    b.WriteString("**Available agents:**\n\n")
    b.WriteString("- `/claude` — dispatch this thread to Claude\n")
    b.WriteString("- `/codex` — dispatch this thread to Codex\n")
    if currentAgentType != "" {
        b.WriteString(fmt.Sprintf("\n**Current thread agent:** `%s`\n\n", currentAgentType))
    }
    b.WriteString("\n**Available commands:**\n\n")
    // ... existing command listing ...
}
```
Note: `currentAgentType` is the `agentType` returned from `LookupSandbox`. Pass "" on fresh
threads (no row yet). The `buildHelpReply` signature change cascades to `RunCommandPass`.

---

## State of the Art

| Old / Current Approach | Phase 102 Approach | Impact |
|------------------------|-------------------|--------|
| `EFFECTIVE_AGENT="$AGENT"` hardwire (userdata.go:2248) | D4 precedence block (verb > thread_agent_type > profile default) | Enables per-thread agent selection |
| `agent_session_id` only in DDB projection | Project + read `agent_type` alongside session | Enables thread persistence |
| `UpdateSession` writes session ID only | Writes `agent_session_id + agent_type` together | Atomic per-turn write-back |
| `ParseCommands` returns `{HelpRequested, Known, MultiError}` | Adds `AgentVerb, AgentVerbConflict` | Bridge-side verb detection |
| `GitHubEnvelope` has no agent field | Gains `Agent string json:"agent,omitempty"` | Bridge → poller verb transport |
| `/help` lists commands only | Also lists `/claude`, `/codex`, current thread agent | Self-documenting UX |
| `km doctor` warns on `"help"` shadow only | Warns on `"help"`, `"claude"`, `"codex"` shadows | Prevents reserved name conflicts |

---

## Open Questions

None. All design decisions were resolved in the design spec and CONTEXT.md. The code
investigation confirms all stated locations are accurate (with minor line-drift
acknowledgement — line numbers shift as edits accumulate but the structures are unchanged).

**One implementation detail to confirm at plan time:** Whether `LookupSandbox` signature
change (adding a third return value `agentType string`) is preferable to a sibling method.
Given that `LookupSandbox` is called in exactly ONE place in `Handle()` (the webhook
handler), and the fake in `thread_store_test.go` wraps the interface, the signature change
is low-risk. The alternative (sibling method) avoids touching existing callers but adds
interface surface. Recommend extending the existing method signature.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./pkg/github/bridge/... -run TestCommandParse -count=1` |
| Full suite command | `go test ./pkg/github/bridge/... ./internal/app/cmd/... -count=1` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| GH-AGENT-VERB | `/claude` recognized anywhere in comment, stripped from `{{args}}` | unit | `go test ./pkg/github/bridge/... -run TestCommandParse -count=1` | ❌ Wave 0 |
| GH-AGENT-VERB | `/codex` recognized anywhere, code-block suppressed | unit | `go test ./pkg/github/bridge/... -run TestCommandParse -count=1` | ❌ Wave 0 |
| GH-AGENT-VERB | Two distinct agent verbs → error; same verb repeated → deduped | unit | `go test ./pkg/github/bridge/... -run TestCommandParse -count=1` | ❌ Wave 0 |
| GH-AGENT-VERB | `/codex /patch fix bug` → agent=Codex, cmd=patch, args="fix bug" | unit | `go test ./pkg/github/bridge/... -run TestCommandParse -count=1` | ❌ Wave 0 |
| GH-AGENT-VERB | `AgentVerbConflict=true` triggers reply "🤖 Specify one agent…" | unit | `go test ./pkg/github/bridge/... -run TestHandle_AgentVerbConflict -count=1` | ❌ Wave 0 |
| GH-AGENT-VERB | Envelope carries `Agent` field from parsed verb | unit | `go test ./pkg/github/bridge/... -run TestHandle_EnvelopeCarriesAgent -count=1` | ❌ Wave 0 |
| GH-AGENT-PERSIST | `LookupSandbox` returns `agent_type` from DDB row | unit | `go test ./pkg/github/bridge/... -run TestGitHubThreadStore -count=1` | ❌ Wave 0 (extend existing) |
| GH-AGENT-PERSIST | `UpdateSession` writes `agent_type` alongside session ID | unit | `go test ./pkg/github/bridge/... -run TestGitHubThreadStore -count=1` | ❌ Wave 0 (extend existing) |
| GH-AGENT-SWITCH | No verb + no stored `agent_type` → profile default (back-compat) | unit/poller | manual-only via `km create` + test comment | N/A — poller bash |
| GH-AGENT-SWITCH | Verb differs from stored agent_type + session exists → session cleared | unit/poller | manual-only | N/A — poller bash |
| GH-AGENT-POLLER | `EFFECTIVE_AGENT` precedence: verb > thread_agent_type > profile default | manual-only | `km create` + inject envelope variants | N/A — poller bash |
| GH-AGENT-PROFILE | `/codex` on Claude-only box → `km-github comment` helpful error | manual-only | `km create` (github-review profile) + `/codex` comment | N/A — poller bash |
| GH-AGENT-E2E | `/codex /patch fix bug` dispatches Codex; follow-up continues Codex | E2E | UAT runbook (`km create` + real PR comment) | N/A |
| GH-AGENT-E2E | `km doctor` WARNs on `claude`/`codex`/`help` in `github.commands` | unit | `go test ./internal/app/cmd/... -run TestDoctorGitHubCommands -count=1` | ❌ Wave 0 (extend existing) |

### Sampling Rate
- **Per task commit:** `go test ./pkg/github/bridge/... -run TestCommandParse -count=1`
- **Per wave merge:** `go test ./pkg/github/bridge/... ./internal/app/cmd/... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/github/bridge/commands_test.go` — extend `TestCommandParse` with: `/claude`
  recognized, `/codex` recognized, two-verb conflict, same-verb dedup, compose `/codex /patch`,
  code-block suppresses agent verb, agent verb stripped from `{{args}}`
- [ ] `pkg/github/bridge/webhook_handler_phase102_test.go` — new file: `TestHandle_AgentVerbConflict`
  (two verbs → PostComment with conflict message, no SQS), `TestHandle_EnvelopeCarriesAgent`
  (single `/claude` → envelope.Agent="claude"), `/help` with agent verb → help reply lists agents
- [ ] `pkg/github/bridge/thread_store_test.go` — extend: `TestGitHubThreadStore_LookupSandbox_Found`
  to assert returned `agentType`; `TestGitHubThreadStore_UpdateSession` to assert `agent_type`
  written in UpdateItem expression
- [ ] `internal/app/cmd/doctor_github_commands_test.go` — extend
  `TestDoctorGitHubCommandsHelpShadow` pattern for `"claude"` and `"codex"` reserved names

**Poller tests (bash — no automated harness):** The GitHub inbound poller is a bash heredoc
embedded in a Go string in `userdata.go`. There is no unit test harness for poller bash
(unlike the Go bridge code). Verification is E2E-only: `km create` a sandbox with
`notification.github.inbound.enabled: true`, post real comments with `/claude` / `/codex`
verbs, observe dispatch + DDB write-back via journald logs. This matches how Phases 97/98/99
were verified (see UAT runbooks).

---

## Sources

### Primary (HIGH confidence)
- Live code inspection — `pkg/github/bridge/commands.go` (verified `ParseCommands`,
  `StripCode`, `isCommandCandidate`, `ExtractArgs`, `RunCommandPass`, `buildHelpReply`)
- Live code inspection — `pkg/github/bridge/payload.go` (verified `GitHubEnvelope` exact fields)
- Live code inspection — `pkg/github/bridge/interfaces.go` (verified `GitHubThreadStore`
  interface: `LookupSandbox`, `UpdateSession` signatures)
- Live code inspection — `pkg/github/bridge/aws_adapters.go:700-820` (verified
  `DynamoGitHubThreadStore` implementation: projection expression, update expressions)
- Live code inspection — `pkg/compiler/userdata.go:2079-2342` (verified GitHub poller
  heredoc: AGENT init at 2096, DDB get-item at 2182-2188, EFFECTIVE_AGENT hardwire at 2248,
  dispatch fork at 2253, write-back at 2319-2326)
- Live code inspection — `internal/app/cmd/doctor.go:1502-1504` (verified `"help"` shadow
  check exact code)
- Live code inspection — `pkg/compiler/userdata.go:1670-1871` (verified Slack poller's
  EFFECTIVE_AGENT precedence + cross-agent switch as the exact analog to implement)
- Go test execution — `go test ./pkg/github/bridge/... -count=1` PASS (confirmed test
  infrastructure is healthy and fast)
- Go test execution — `go test ./internal/app/cmd/... -run TestDoctorGitHubCommands -count=1` PASS

### Secondary (MEDIUM confidence)
- `docs/codex-parity.md` — Slack Phase 70 analog design (agent_type, cross-agent switch
  precedence, DDB column hangover note) — cross-referenced with live Slack poller code
- `docs/superpowers/specs/2026-06-07-github-bridge-agent-verbs-design.md` — approved design
  spec (code location claims verified against live code)

---

## Metadata

**Confidence breakdown:**
- Bridge parser extension: HIGH — `ParseCommands` structure fully understood; exact extension
  point identified; test infrastructure verified running
- DDB interface/impl extension: HIGH — `LookupSandbox` and `UpdateSession` read from source;
  projection/update expressions verified
- Poller bash: HIGH — exact current lines documented; Slack analog verified as the template
- km doctor extension: HIGH — exact line location confirmed; test pattern established
- Deploy surface: HIGH — confirmed from CLAUDE.md + memory lessons (make build-lambdas + km
  init --dry-run=false; existing sandboxes need km destroy && km create)

**Research date:** 2026-06-08
**Valid until:** 2026-07-08 (code is stable; no fast-moving dependencies)
