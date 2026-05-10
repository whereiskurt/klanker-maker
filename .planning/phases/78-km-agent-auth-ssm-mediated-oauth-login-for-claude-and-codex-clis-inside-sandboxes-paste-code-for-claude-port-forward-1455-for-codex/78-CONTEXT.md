# Phase 78: km agent auth — Context

**Gathered:** 2026-05-10
**Status:** Ready for planning
**Source:** Synthesized from in-session design discussion (operator + Claude, 2026-05-10) — not /gsd:discuss-phase. Design conclusions captured below as locked decisions.

<domain>
## Phase Boundary

**Phase delivers:** A new `km agent auth <sandbox> [--claude | --codex]` Cobra subcommand that mediates the OAuth login flow for the Claude and Codex CLIs *inside* the sandbox, using SSM as the operator-laptop ↔ sandbox channel. After running this command, the targeted CLI is authenticated on the sandbox (token persisted to disk on the sandbox), and subsequent `km shell --no-bedrock` / `km agent run --no-bedrock` invocations Just Work without manual login.

**Operator pain solved:** Today, operators must SSM into a sandbox via `km shell` and run `claude auth login` interactively after every `km create` if they want `--no-bedrock`. There is no equivalent path for Codex. VS Code Remote-SSH's Claude extension stores tokens in VS Code's SecretStorage (machine-local, encrypted, not in `~/.claude/`), so even an authenticated Remote-SSH session does not give the CLI a usable token — the two storage backends never overlap.

**Phase does NOT:**
- Install or update the Claude or Codex CLIs (already baked into AMI/userdata; out of scope here).
- Modify or migrate tokens between operator workstation and sandbox.
- Implement token rotation, refresh-on-expiry orchestration, or revocation flows.
- Add multi-user concurrent auth on the same sandbox (single operator per sandbox in v1, matching Phase 73 model).
- Touch sandbox provisioning (no userdata, DDB, SSM-parameter, or infra/modules changes).

</domain>

<decisions>
## Implementation Decisions

### CLI surface (locked)
- New subcommand: `km agent auth <sandbox-id> [flags]`
- Default flag: `--claude` (no flag = `--claude`)
- Alternate: `--codex`
- Sandbox identifier accepts the same formats as `km shell` and `km vscode start`: full ID (`learn-14853201`), alias, or list-row number.
- Pass-through flags for `--claude` path: `--console`, `--sso`, `--claudeai` (default), `--email <addr>` — these forward to the underlying `claude auth login` invocation in the sandbox.
- **Skip codex `--api-key` shortcut for v1.** Codex's `--api-key` is a different auth mode (Console/API-billing) than the OAuth ChatGPT flow this phase targets; conflating them in v1 surface is scope creep.

### Auth flow — `--claude` (paste-the-code) (locked)
- Confirmed by inspecting the claude CLI binary: it uses a **manual redirect URI** flow, **NO localhost callback server**.
  - Binary contains: `MANUAL_REDIRECT_URL: https://platform.claude.com/oauth/code/callback` (literal) plus two templated variants with base URL interpolation (claude.ai and console.anthropic.com).
- Implementation:
  1. SSM-exec `claude auth login [--console|--sso|--claudeai] [--email <addr>]` interactively in the sandbox (via SSM Session Manager, same plumbing as `km shell` interactive mode).
  2. CLI prints an OAuth URL.
  3. Operator opens URL in their laptop browser, completes OAuth at claude.ai (or Anthropic Console).
  4. Hosted page displays a code; operator copies it into the SSM-attached terminal.
  5. CLI exchanges code for tokens via API and writes `~/.claude/.credentials.json` on the sandbox.
- Success signal: `~/.claude/.credentials.json` exists (or its mtime advanced) on the sandbox AND CLI exits 0.
- This path is small enough to be essentially a thin wrapper around `km shell <sb> -- claude auth login`. Value-add over a raw shell is: discoverability, flag pass-through, and a clean error when credentials don't materialize.

### Auth flow — `--codex` (port-forward 1455) (locked)
- Confirmed from `openai/codex` source `codex-rs/login/src/server.rs`:
  - Default port: **1455**, fallback: **1457**
  - Bind address: `127.0.0.1:1455` (or 1457)
  - Redirect URI: `http://localhost:1455/auth/callback`
  - **No CLI flag or env var to override the port** in current Codex CLI.
- Implementation:
  1. Operator-side: spin up SSM port-forward `localhost:1455 ↔ sandbox:1455` (reuse `km shell --ports` SSM primitive — exact same `AWS-StartPortForwardingSession` document call).
  2. SSM-exec `codex login` in the sandbox. On a headless EC2, codex's `xdg-open` of the OAuth URL fails; the CLI falls back to printing the URL to stdout.
  3. Capture/relay the URL to the operator (print or auto-`open` on laptop).
  4. Operator clicks → browser hits laptop:1455 → SSM tunnel → sandbox:1455 → codex callback server completes OAuth.
  5. Codex writes `~/.codex/auth.json` on the sandbox (file structure observed: `{"OPENAI_API_KEY", "tokens", "last_refresh"}`) and exits cleanly.
  6. Operator-side teardown: kill SSM port-forward session, kill the SSM-exec wrapper.
- Success signal: codex CLI exits 0 AND `~/.codex/auth.json` mtime advanced on the sandbox.
- Port-collision handling: if `localhost:1455` is in use on the operator's laptop, try `1457` (codex's own fallback); if both are taken, fail fast with a clear error pointing the operator at killing the local listener.

### Wave phasing (locked)
- **Wave 1: `--claude` path.** Ship as standalone PR. Trivial enough that it could be a single tight Cobra command + interactive SSM session + success-signal check. Unblocks the immediate operator pain right away.
- **Wave 2: `--codex` path.** Adds port-forward lifecycle management (start tunnel → exec → capture URL → wait for completion → teardown). More state, more failure modes, more tests.
- Both waves share helpers: success-signal polling on file mtime, sandbox-identifier resolution (already exists), interactive SSM-exec wrapper.

### Auto-trigger from km shell / km agent run (locked)
- When `km shell --no-bedrock` or `km agent run --no-bedrock` is invoked AND the corresponding credentials file is missing on the sandbox: **print a clear hint** ("Run `km agent auth <sandbox>` first") and **exit non-zero**. Do **NOT** silently auto-bootstrap or interactively prompt.
- Rationale (locked in discussion): clear, deterministic, predictable in scripted/CI contexts.

### Conflict handling (locked)
- If a `km agent run` tmux session is in flight on the sandbox, `km agent auth` should refuse with a clear error pointing at `km agent attach` / wait for completion. Do not collide with the user's running agent session.
- Use a dedicated tmux session name (e.g. `km-auth-<random>`) distinct from the `km-agent-*` naming used by `km agent run`, so accidental collision is impossible.

### Reuse / no new infra (locked)
- Reuse: `pkg/aws/ssm` session helpers (interactive exec + `AWS-StartPortForwardingSession`), sandbox-identifier resolution helper from `km shell`/`km vscode start`, `km list` lookup.
- New code: a single Cobra subcommand file (`internal/app/cmd/agent_auth.go` or similar) plus possibly small helper for "watch for file mtime change on sandbox via SSM".
- **No new IAM**, no new SSM parameters, no new DDB schema, no new infra/modules, no userdata template changes, no Lambda changes.

### Claude's Discretion
- **Exact filename and command path within `internal/app/cmd/`** — could be `agent_auth.go`, could be a folder, depending on existing `agent.go` structure.
- **How the operator-side URL relay works for codex** — auto-open via `os/exec` on `open`/`xdg-open`/`start` per platform vs. just printing the URL. Either is acceptable; auto-open is friendlier.
- **Test layout** — TDD per project convention; specific harness (real SSM mock vs unit tests around helpers) is the planner's call within established `pkg/aws/ssm/` test patterns.
- **Polling interval and timeout** for credentials-file mtime watch. Reasonable defaults: poll every 1s, total timeout 10 minutes. Planner can tune.
- **Error message wording** for the missing-credentials hint in `km shell --no-bedrock` / `km agent run --no-bedrock`.
- **Whether to add a corresponding `km agent auth status <sandbox>` subcommand** that reports whether `.claude/.credentials.json` and `.codex/auth.json` exist on the sandbox. Useful, low cost, but not strictly required by phase goal — defer unless trivial.

</decisions>

<specifics>
## Specific Ideas

### Verification facts (already confirmed in-session — do not re-research)
- **Claude CLI binary inspection** (`/Users/khundeck/.local/bin/claude` v2.1.138, Mach-O arm64):
  - `strings | grep MANUAL_REDIRECT_URL` ⇒ `MANUAL_REDIRECT_URL: https://platform.claude.com/oauth/code/callback` (literal) plus two templated variants `${q}/oauth/code/callback` and `${K}/oauth/code/callback`.
  - `claude auth login --help` exposes `--claudeai` (default), `--console`, `--sso`, `--email <addr>`. **No `--port`, no localhost callback flag.**
- **Codex CLI source** (`openai/codex` `codex-rs/login/src/server.rs`):
  - Constants: `DEFAULT_PORT = 1455`, `FALLBACK_PORT = 1457`.
  - `bind_server` uses `format!("127.0.0.1:{port}")`.
  - Redirect URI: `http://localhost:{port}/auth/callback`.
  - `ServerOptions { port: u16 }` exists internally but is not surfaced as a CLI flag in `codex login`.
- **Token paths on sandbox** (and operator workstation, same conventions):
  - Claude: `~/.claude/.credentials.json` (per existing CLAUDE.md note in this repo).
  - Codex: `~/.codex/auth.json` (keys: `OPENAI_API_KEY`, `tokens`, `last_refresh` — observed locally).

### Existing primitive to reuse
- `km shell --ports 1455:1455` already does SSM port-forwarding via `AWS-StartPortForwardingSession`. Wave 2 should literally call the same code path or factor a helper out of it.
- `km vscode start` is the closest existing precedent — long-running SSM port-forward + cleanup on exit. Pattern to mimic, not duplicate.

### Diff against Phase 70
- Phase 70 ("codex parity for operator-notify, slack-notify, slack-inbound dispatcher") is about hook + dispatcher parity, **not** OAuth login. Scope does not overlap with Phase 78. Planner should confirm via a quick scan but no merge is expected.

### Diff against Phase 50/51 (km agent run)
- `km agent run --no-bedrock` is the consumer that benefits most from this phase. It currently silently fails when `~/.claude/.credentials.json` is missing on the sandbox. Phase 78 adds the missing-credentials hint there.

</specifics>

<deferred>
## Deferred Ideas

- **`codex login --api-key <KEY>` mode** — different auth path (Console / API-billing), not OAuth ChatGPT. Out of v1 scope.
- **Token rotation / refresh-on-expiry orchestration** — both CLIs handle their own refresh internally; only worry about it if production data shows refresh failures requiring operator intervention.
- **Operator-laptop ↔ sandbox token migration** — explicitly NOT in scope. Each sandbox holds its own token.
- **Multi-user / concurrent auth on the same sandbox** — single operator per sandbox in v1, matches Phase 73 keypair model.
- **`km agent auth status <sandbox>`** — informational subcommand reporting which CLIs are authenticated. Trivial to add but not strictly required by phase goal. Implement only if a test/UAT scenario actually needs it.
- **Auto-bootstrap (silent re-auth) inside `km shell --no-bedrock`** — explicit no in this phase. Always print a hint and exit non-zero on missing credentials.
- **Web/UI integration** (e.g. ConfigUI button to launch auth) — out of v1 scope.

</deferred>

---

*Phase: 78-km-agent-auth-ssm-mediated-oauth-login-for-claude-and-codex-clis-inside-sandboxes-paste-code-for-claude-port-forward-1455-for-codex*
*Context gathered: 2026-05-10 via in-session design synthesis (CLI binary + source inspection performed live, decisions locked by operator)*
