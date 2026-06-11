# Phase 106: Session-resume hint on GitHub + HackerOne bridge replies (post-on-mint) - Context

**Gathered:** 2026-06-11
**Status:** Ready for planning
**Source:** Brainstorming session (operator, 2026-06-11) — fully settled design

<domain>
## Phase Boundary

**Delivers:** After a bridge agent turn, each relevant poller posts ONE extra collapsed
GitHub-Markdown `<details>` comment carrying the operator-facing resume handle — the
run-from directory + the agent-correct resume command + the sandbox id + the freshly
extracted session id — so an operator can re-attach to the exact Claude/Codex session
without querying DynamoDB.

**In scope:**
- GitHub inbound poller (`pkg/compiler/userdata.go`, GitHub block ~2382).
- HackerOne inbound poller (`pkg/compiler/userdata.go`, H1 block ~2660).

**Out of scope (explicit):**
- **Slack poller** (`userdata.go` ~1535–2085) — deliberately EXCLUDED and MUST stay
  byte-identical. Rationale: the operator can ask for the handle interactively in the
  Slack chat; no value pushing it into every Slack reply.
- No change to the `km-github` / `km-h1` Go helper binaries — the pollers call them as-is
  with a constructed body.
- No SandboxProfile schema change, no new TF resource, no new DDB column (reuses existing
  `agent_session_id` / `agent_type`).
</domain>

<decisions>
## Implementation Decisions

### Hint content & format (locked)
- A GitHub-flavored collapsible `<details>`/`<summary>` fold, posted as a standalone comment:
  ```markdown
  <details>
  <summary>🔧 Resume this agent session</summary>

  On sandbox `sb-1a2b3c4d`, from the `/workspace` folder use `claude --resume 9f8e7d6c-…`
  </details>
  ```
- Agent-correct command, branch on the poller's already-computed `EFFECTIVE_AGENT`:
  - Claude → `claude --resume <id>`
  - Codex → `codex exec resume <id>`
- The `<id>` is the post-run handle: `NEW_GITHUB_SESSION` / `NEW_H1_SESSION`.
- Include the sandbox id so the line is self-contained (operator knows which box to attach to).
  The poller already knows its sandbox id (written to the threads tables) — confirm the exact
  env/metadata var at plan time.

### Run-from directory = `/workspace` (NOT `/home/sandbox`) — VERIFIED
- Every agent dispatch does `cd /workspace` (`userdata.go:2329/2305` GitHub; `2616/2627/2639` H1)
  but runs with `HOME=/home/sandbox` (`:3208`, `SANDBOX_HOME` `:3089`).
- Session transcript FILES live at `/home/sandbox/.claude/projects/-workspace/<id>.jsonl`,
  but Claude derives the resumable-session project bucket from the **current working directory**.
- ⇒ `claude --resume` MUST be invoked from `/workspace`, or it reports "No conversation found."
  The hint text MUST say `/workspace`. (The operator's "/home/sandbox" hunch is the storage
  location, not the run-from location.)

### Injection point = the POLLER, not the agent (locked)
- The agent posts its own reply MID-RUN via `km-github comment` / `km-h1`, BEFORE the session id
  exists. The authoritative handle is only extracted AFTER the run
  (`userdata.go:2375–2380` GitHub; H1 analog ~2660), at the DDB write-back site (~2391 GitHub).
- ⇒ The poller posts the hint itself, right after extraction / DDB write-back. This is a SECOND,
  collapsed comment per qualifying turn.
- Rejected alternatives:
  - Pre-mint `claude --session-id <uuid>` + env-var footer in `km-github` — Codex cannot
    pre-set its `thread_id` for fresh threads ⇒ inconsistent; adds a flag to the hot path.
  - PATCH the agent's own comment — requires capturing the comment id the agent created,
    not currently surfaced.

### Frequency = POST-ON-MINT (locked; not every-turn, not strictly turn-1)
- Post the fold ONLY when the session id is newly minted:
  - no prior stored session (true first turn), OR
  - `NEW_*_SESSION` differs from the pre-run stored value (a Gap-E / cross-box re-mint;
    Gap-E retry-without-resume at `userdata.go:2336–2347`).
- Stable common case (Claude keeps its id on `--resume`; Codex `thread_id` stable) ⇒ fires
  EXACTLY ONCE per thread (the operator's noise goal). If the session ever rotates, it self-heals
  by re-posting the live handle.
- The poller holds both old (`GITHUB_SESSION`) and new (`NEW_GITHUB_SESSION`) values at the
  write-back site ⇒ implement as a one-line `if`.

### HackerOne safety property (locked)
- `km-h1` posts INTERNAL by default ⇒ the resume hint lands on the internal/team comment,
  NEVER visible to the external researcher. Preserve this — do NOT post the hint externally.
- GitHub PR comments ARE visible to all repo collaborators; the collapsed fold is the agreed
  mitigation. The ids are not independently exploitable without AWS/SSM access.

### Robustness (locked)
- Best-effort, non-blocking: the hint post call is `|| true`. A failed hint post MUST NOT block
  the SQS message ack or the turn completion.
- Skip the hint when no session id was extracted (the rare empty-id case).

### Claude's Discretion
- Exact helper/shell-function factoring inside the poller heredocs (a shared snippet vs inline
  per-poller), so long as the Slack poller stays byte-identical.
- Exact `<summary>` wording and emoji, within the spirit of the locked example.
- Exact env var used to source the sandbox id (confirm what's already in poller scope).
- Whether the GitHub/H1 userdata byte-identity golden tests need a deliberate golden refresh
  (likely yes for GH+H1 goldens; the Slack golden MUST remain unchanged) — decide at plan time.
</decisions>

<specifics>
## Specific Ideas

- Source references to anchor the plan:
  - GitHub poller dispatch + post-run extraction + DDB write-back:
    `pkg/compiler/userdata.go:2255` (RUN_DIR), `:2264-2265` (RESUME_ARG), `:2305/2329` (cd /workspace),
    `:2336-2347` (Gap-E retry), `:2375-2390` (NEW_GITHUB_SESSION extract + update-item).
  - H1 poller analog: `:2571` (RUN_DIR), `:2580-2581` (RESUME_ARG), `:2616/2627/2639` (cd /workspace),
    `:2646-2654` (Gap-E retry), and the H1 post-run extract/write-back ~2660.
  - Home/cwd split: `:3089` (SANDBOX_HOME), `:3208` (export HOME), `:3212` (cd /workspace).
  - Slack poller (MUST stay byte-identical): `:1535-2085`.
- Reply helpers the poller calls: `km-github comment --repo <owner/repo> --number <N> --body "…"`
  (`cmd/km-github/main.go`), and `km-h1` INTERNAL-by-default comment (`cmd/km-h1/…`, Phase 103).
- Deploy: poller is compiled into userdata by the create-handler Lambda ⇒ `make build-lambdas`
  (clean) so the create-handler embeds the new userdata; existing sandboxes need
  `km destroy && km create` to pick it up. Bridge Lambdas / IAM / TF UNAFFECTED.
</specifics>

<deferred>
## Deferred Ideas

- Slack resume-hint parity — intentionally not built (operator asks interactively in chat).
- Single-comment (appended-to-agent-reply) delivery — rejected for this phase due to the
  pre-mint/Codex inconsistency and comment-id-capture complexity.
- Empirical confirmation of Claude `-p --resume` id stability — not required; the post-on-mint
  design is correct whether the id is stable or rotates.
</deferred>

---

*Phase: 106-session-resume-hint-on-github-hackerone-bridge-replies-post-on-mint*
*Context gathered: 2026-06-11 via brainstorming session*
