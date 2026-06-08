# Phase 102: GitHub bridge agent verbs — /claude and /codex per-thread agent selection - Context

**Gathered:** 2026-06-08
**Status:** Ready for planning
**Source:** Approved design spec (`docs/superpowers/specs/2026-06-07-github-bridge-agent-verbs-design.md`, design approved 2026-06-07)

<domain>
## Phase Boundary

Reserved `/claude` and `/codex` verbs in a PR/issue comment select the agent for that
**thread** (persistent). The GitHub analog of Slack's Phase 70 `claude:`/`codex:` prefix
routing, simplified by GitHub's single-durable-thread model.

**What this phase delivers:**
- Bridge-side verb parser that recognizes `/claude` and `/codex` anywhere in a comment
  (extends the Phase 99 scanner), partitions agent verbs from template commands, and
  strips the agent verb from `{{args}}`.
- `GitHubEnvelope` gains an `Agent` field carrying the parsed verb.
- `km-github-threads` gains an `agent_type` attribute (schema-on-write, no TF/migration).
- Poller (`userdata.go`) computes `EFFECTIVE_AGENT` via precedence and persists `agent_type`
  on write-back; cross-agent switch resets the session.
- `claude`/`codex`/`help` become reserved tokens; a `github.commands` entry shadowing them
  is ignored with a `km doctor` WARN.

**Out of scope (non-goals):**
- Slack's 8-step cross-agent handoff (spawn new top-level message) — GitHub has no equivalent.
- Per-agent session retention across switches (rejected — single `agent_session_id` column).
- Agents beyond `claude`/`codex` (future agents slot in as new `agent_type` values).
- A `km-config.yaml` surface — verbs are built-in reserved tokens, not user-defined commands.
- SandboxProfile schema change.

**Depends on:** Phase 99 (comment-verb parser) + Phase 98 (`km-github-threads`).
Independent of Phases 100/101.

</domain>

<decisions>
## Implementation Decisions (all locked — from design spec Decisions table)

### D1 — Syntax
- Slash verbs `/claude` / `/codex`, reserved, parsed **anywhere** in the comment
  (consistent with Phase 99 `/command` tokens). **NOT** Slack's `codex:` colon prefix.
- Recognized by the same anywhere-scan + code-strip the Phase 99 parser uses
  (`^/[A-Za-z][A-Za-z0-9_-]*$`, fenced/inline code stripped first).

### D2 — Axis
- Agent verbs are a **separate axis** from Phase 99 template commands; they **compose**.
- A comment may carry **0–1 agent verb + 0–1 template command**; both stripped from `{{args}}`.
- Composition example: `@bot /codex /patch fix the flaky test` → agent = Codex,
  template = `/patch`, `{{args}}` = `fix the flaky test`.

### D3 — Persistence
- **Per-thread.** The verb writes `agent_type` onto the `(repo, number)` row; follow-up
  comments with no verb continue with that agent.

### D4 — Precedence
- `EFFECTIVE_AGENT = AGENT_OVERRIDE (verb) | THREAD_AGENT_TYPE | $AGENT (profile default)`.
- Verb > thread `agent_type` > profile default.

### D5 — Cross-agent switch
- **Single `agent_session_id` column, reset on switch.** If `AGENT_OVERRIDE` is set, differs
  from `THREAD_AGENT_TYPE`, and a session exists → clear `RESUME_ARG` (stored session belongs
  to the other agent, cannot be resumed). New agent starts fresh, captures a new session,
  overwrites `agent_session_id` + `agent_type`. Switching back = fresh again.
- No Slack-style new-top-level handoff (the PR *is* the thread).

### D6 — Codex availability
- `/codex` requires a **Codex-capable profile**. The lean built-in `github-review` is Claude-only.
- Not a hard gate (`km doctor` cannot introspect installed binaries). Instead:
  documented precondition + **runtime helpful-error comment** when `EFFECTIVE_AGENT=codex` and
  the `codex` binary is absent — post a clear comment ("This sandbox's profile has no Codex;
  `/codex` is unavailable here") instead of a silent non-zero exit / stranded turn.

### Verb-count rule
- **≤ 1 agent verb** per comment. Two distinct agent verbs (`/claude` AND `/codex`) →
  **error reply** ("🤖 Specify one agent — found /claude and /codex."), no dispatch.
- Repeats of the same agent verb are deduped.

### Reserved tokens
- `claude`, `codex`, `help` are reserved: a `github.commands` entry with any of these names is
  ignored with a `km doctor` WARN (extends Phase 99's `help`-shadow check).

### `/help` extension
- Phase 99 built-in `/help` is extended to list available agents (`/claude`, `/codex`) and note
  the thread's current agent.

### Dormancy / back-compat
- No verb → `EFFECTIVE_AGENT` falls to profile default; byte-identical to today
  (`userdata.go:2248` hardwires `EFFECTIVE_AGENT="$AGENT"`).
- Old `km-github-threads` rows without `agent_type` → treated as profile default. Additive, no migration.

### Claude's Discretion
- Exact Go function/file layout for the verb partitioner (extend the existing Phase 99 parser
  function vs. a sibling — follow existing parser structure).
- Whether `LookupSandbox` is extended to also return `agent_type` or a sibling accessor is added.
- Exact bash for the codex-binary-absent guard in `userdata.go`.
- Test file organization (extend existing parser/poller test files vs. new).

</decisions>

<specifics>
## Specific Ideas / Concrete References

**Code touchpoints (from design spec):**
- `userdata.go:2248` — `EFFECTIVE_AGENT="$AGENT"` hardwire (the thing to change).
- `userdata.go:2253-2317` — existing Claude/Codex dispatch fork + session capture
  (Claude `.session_id`; Codex `thread.started` `thread_id`). Already present; never varies agent.
- `payload.go:75` — `GitHubEnvelope` (add `Agent string \`json:"agent,omitempty"\``).
- `interfaces.go:98` — `km-github-threads` schema (today: `sandbox_id` + `agent_session_id`;
  add `agent_type`). `GitHubThreadStore.LookupSandbox`.
- Phase 99 scanner — the `^/[A-Za-z][A-Za-z0-9_-]*$` token regex + fenced/inline code strip.

**Reply strings:**
- Two agent verbs: `🤖 Specify one agent — found /claude and /codex.`
- `/codex` on Claude-only box: `This sandbox's profile has no Codex; /codex is unavailable here`.

**Testing plan (from spec):**
- Pure-function (bridge parser): agent verb recognized anywhere; stripped from `{{args}}`;
  ≤1 agent verb (two-agent error); composition with template command; reserved-name shadow WARN;
  envelope carries `agent`.
- Poller (table/harness): precedence `override > thread > default`; cross-agent switch clears
  `RESUME_ARG` and starts fresh; `agent_type` write-back; codex-missing posts helpful comment;
  no-verb path unchanged.
- `km doctor`: `claude`/`codex`/`help` command-shadow WARN.

</specifics>

<deferred>
## Deferred Ideas

- Slack-style 8-step cross-agent handoff — non-goal (GitHub thread model makes it unnecessary).
- Per-agent session retention across switches — rejected (D5).
- Agents beyond claude/codex — future, slot in as new `agent_type` values with no parser work.
- `km-config.yaml` surface for the verbs — non-goal (built-in reserved tokens).

</deferred>

---

*Phase: 102-github-bridge-agent-verbs-claude-and-codex-select-the-per-thread-agent-in-a-pr-comment-slack-phase-70-analog*
*Context gathered: 2026-06-08 from approved design spec*
