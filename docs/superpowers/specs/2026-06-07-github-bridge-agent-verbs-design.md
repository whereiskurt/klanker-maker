# GitHub bridge agent verbs — `/claude` & `/codex` per-thread agent selection (Phase 102)

> **Status:** design approved 2026-06-07. Lands as **Phase 102**. Depends on
> Phase 98 (`km-github-threads`) and Phase 99 (comment-verb parser).
>
> **Direct ancestor:** Slack agent routing (Phase 70, `docs/codex-parity.md`). This
> is that capability ported to the GitHub bridge, simplified by GitHub's thread model.

## Problem

GitHub comment dispatch always runs the sandbox's **profile-default** agent
(`KM_AGENT` / `spec.agent.default`) — `userdata.go:2248` hardwires
`EFFECTIVE_AGENT="$AGENT"`. There is no way to pick Claude vs Codex from a comment,
unlike Slack's `claude:` / `codex:` prefix routing (Phase 70). The operator wants to
say `@klanker-maker /codex …` and have that PR/issue thread run Codex.

## What already exists (so this is a small lift)

- The GitHub poller **already has the Claude/Codex dispatch fork** and **already
  captures both session types** (Claude `.session_id`; Codex `thread.started`
  `thread_id`) — `userdata.go:2253-2317`. It simply never varies `EFFECTIVE_AGENT`.
- Missing: a verb parser (bridge), an `agent` field on `GitHubEnvelope`
  (`payload.go:75`), and an `agent_type` column on `km-github-threads`
  (`interfaces.go:98` — today only `sandbox_id` + `agent_session_id`).

## Goal

Reserved **`/claude`** and **`/codex`** verbs in a PR/issue comment select the agent
for that **thread** (persistent). The verb writes `agent_type` onto the
`(repo, number)` row; follow-up comments with no verb continue with that agent.
No verb → today's profile-default behavior, byte-identical.

## Decisions (resolved during brainstorming)

| # | Decision | Choice |
|---|---|---|
| D1 | Syntax | Slash verbs **`/claude` / `/codex`**, reserved, parsed anywhere (consistent with Phase 99 `/command` tokens) — *not* Slack's `codex:` colon prefix |
| D2 | Axis | **Separate axis** from Phase 99 template commands; composes (`/codex /patch …`) |
| D3 | Persistence | **Per-thread** — writes `agent_type`; follow-ups continue with it |
| D4 | Precedence | **verb > thread `agent_type` > profile default** (`KM_AGENT`) |
| D5 | Cross-agent switch | **Single `agent_session_id` column, reset on switch** — switching agent starts a fresh session and overwrites; switching back = fresh. No Slack-style new-top-level handoff (the PR *is* the thread). |
| D6 | Codex availability | `/codex` requires a **Codex-capable profile**; precondition + helpful error, not silent failure |
| D7 | Phase | **Phase 102**, depends on 98 + 99 |

## Non-goals

- Slack's 8-step cross-agent handoff (spawn a new top-level message). GitHub has no
  equivalent — a PR/issue is a single durable thread; switching just resets the
  session and flips `agent_type` in place.
- Per-agent session retention across switches (rejected D5 — single column, like
  `km-slack-threads`).
- Agents beyond `claude` / `codex` (future agents slot in as new `agent_type` values
  with no further parser work, mirroring the Slack hangover note in Phase 70).
- A `km-config.yaml` surface. The verbs are **built-in reserved tokens** (like
  Phase 99's `/help`), not user-defined commands.

## Syntax & parsing (extends the Phase 99 scanner)

Reserved verbs `/claude`, `/codex` are recognized by the same anywhere-scan +
code-strip the Phase 99 parser uses (`^/[A-Za-z][A-Za-z0-9_-]*$`, fenced/inline code
stripped first). After scanning, partition the candidate tokens into **agent verbs**
and **template commands**:

- **≤ 1 agent verb** allowed. Two distinct agent verbs (`/claude` *and* `/codex`) →
  **error reply** ("Specify one agent — found /claude and /codex."), no dispatch.
  Repeats of the same agent verb are deduped.
- The agent verb is **stripped from the prompt / `{{args}}`** exactly like a command
  token.
- The remaining tokens flow through Phase 99 command logic unchanged (≤ 1 template
  command, else its multi-command error). So a comment may carry **0–1 agent verb +
  0–1 template command**; both are stripped from `{{args}}`.
- Composition example: `@bot /codex /patch fix the flaky test` → agent = Codex,
  template = `/patch`, `{{args}}` = `fix the flaky test`.

`claude`, `codex`, and `help` are **reserved**: a `github.commands` entry with any of
these names is ignored with a `km doctor` WARN (extends Phase 99's `help`-shadow
check).

## Envelope & thread schema

- `GitHubEnvelope` gains `Agent string `json:"agent,omitempty"`` (`payload.go`). The
  bridge sets it from the parsed verb; empty when no verb.
- `km-github-threads` gains an **`agent_type`** attribute (DDB is schemaless for
  non-key attributes — no TF/migration change; the Phase 98 module is untouched).
  `GitHubThreadStore.LookupSandbox` is extended to also return `agent_type` (or a
  sibling accessor), and the poller's write-back includes it.

## Runtime: poller dispatch (`userdata.go`)

The dispatch fork already exists; the change is computing `EFFECTIVE_AGENT` and
persisting it:

1. Parse `AGENT_OVERRIDE=$(jq -r '.agent // empty')` from the envelope.
2. Read the thread's stored agent + session: extend the existing `km-github-threads`
   `get-item` projection to include `agent_type` alongside `agent_session_id`.
3. Compute precedence (D4):
   `EFFECTIVE_AGENT = AGENT_OVERRIDE | THREAD_AGENT_TYPE | $AGENT`.
4. **Cross-agent switch (D5):** if `AGENT_OVERRIDE` is set, differs from
   `THREAD_AGENT_TYPE`, and a session exists → clear `RESUME_ARG` (the stored
   session belongs to the *other* agent and cannot be resumed). The new agent starts
   fresh, captures a new session, and overwrites `agent_session_id`.
5. Dispatch via the existing fork (`EFFECTIVE_AGENT` = `codex` → `codex exec …`; else
   `claude -p …`).
6. **Write-back:** on success, the existing `update-item` that writes the new session
   id also writes `agent_type = EFFECTIVE_AGENT` so follow-up turns continue with it.
7. **Codex-missing guard (D6):** if `EFFECTIVE_AGENT=codex` and the `codex` binary is
   absent (Claude-only profile), post a clear comment via `km-github comment`
   ("This sandbox's profile has no Codex; `/codex` is unavailable here") instead of a
   silent non-zero exit. (Mirrors the Gap-E "don't strand the turn" lesson.)

## Codex-capable profile (D6)

The lean built-in `github-review` profile is **Claude-only**. For `/codex` to do
anything, the resolved profile (repo profile, or a Phase 99 command profile) must
ship Codex (`spec.agent` with codex installed). Options, documented in the runbook:

- Use a profile that installs both agents for repos where `/codex` is wanted; or
- Pair `/codex` with a Phase 99 command whose `profile:` is Codex-capable.

`km doctor` cannot introspect a profile's installed binaries, so this is a documented
precondition + the runtime helpful-error above, not a hard gate.

## Reply paths

- **Two agent verbs:** `🤖 Specify one agent — found /claude and /codex.`
- **`/codex` on a Claude-only box:** helpful comment (see D6 guard).
- **`/help`** (Phase 99 built-in) is extended to list the available agents
  (`/claude`, `/codex`) and note the thread's current agent.

## Dormancy / back-compat

- No verb → `EFFECTIVE_AGENT` falls to the profile default; byte-identical to today.
- Old `km-github-threads` rows without `agent_type` → treated as the profile default
  (same as Slack's "absent ⇒ claude" default). Additive, no migration.

## Deploy

- **Poller change lives in `userdata.go`** → compiled by the create-handler Lambda →
  `make build-lambdas` (clean) + `km init --dry-run=false` for remote create.
  **Existing sandboxes need `km destroy && km create`** to gain the new poller
  ([[project_schema_change_requires_km_init]] pattern).
- **Bridge verb-parse + envelope change** → bridge redeploy (same
  `make build-lambdas` + `km init`).
- `km-github-threads` `agent_type` is schema-on-write → no TF change, no migration.
- No SandboxProfile schema change.

## Testing

Pure-function (bridge parser): agent verb recognized anywhere; stripped from
`{{args}}`; ≤ 1 agent verb (two-agent error); composition with a template command;
reserved-name shadow WARN. Envelope carries `agent`.

Poller (table / harness): precedence `override > thread > default`; cross-agent
switch clears `RESUME_ARG` and starts fresh; `agent_type` write-back; codex-missing
posts a helpful comment rather than stranding the turn; no-verb path unchanged.

`km doctor`: `claude`/`codex`/`help` command-shadow WARN.

## Open questions

None — all resolved during brainstorming (see Decisions table).
