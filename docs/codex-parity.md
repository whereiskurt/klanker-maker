# Codex parity & agent switching

Phase 70 brings the Codex CLI to feature parity with Claude Code for operator
notifications, Slack notifications, and bidirectional Slack chat. It also adds
per-message Slack prefix routing and cross-agent mid-thread switching.

This document is the operator guide. For the full design narrative, see
`.planning/phases/70-*/SPEC.md`. For the Slack-scoped quick reference, see
`docs/slack-notifications.md § Prefix routing & agent switching`.

## What changed

| Concern | Before Phase 70 | After Phase 70 |
|---|---|---|
| Operator-notify hook for Codex | Claude-only | Both agents fire the same `/opt/km/bin/km-notify-hook` script |
| Slack notify hook for Codex | Claude-only | Both agents post to `#sb-{id}` via the same bridge |
| Slack inbound chat for Codex | Claude-only | Poller dispatches `codex exec` when `KM_AGENT=codex` |
| Per-message agent selection | Profile-only (one agent per sandbox) | Slack message prefix `claude:` / `codex:` overrides per-turn |
| Mid-thread agent switch | Not supported | Cross-agent prefix in an existing thread spawns a new top-level + handoff post |
| DDB schema | Claude-only `claude_session_id` | Same column reused agent-agnostic; new attrs `agent_type` + `last_assistant_msg` |
| Profile field | `spec.cli` had Claude-specific args only | New `spec.cli.agent: claude \| codex` (default `claude`) |

## Profile schema

`spec.cli.agent` selects the default agent for **this sandbox**:

```yaml
spec:
  cli:
    agent: codex  # or "claude"; absence ≡ claude
    notifyEmailEnabled: true
    notifySlackEnabled: true
    notifySlackPerSandbox: true
    notifySlackInboundEnabled: true
    notifyOnPermission: true
    notifyOnIdle: true
```

Validation: `km validate <profile.yaml>` rejects any value other than `claude`
or `codex` with a path-pointing error. Absence is treated as `claude`.

There is no way to declare "no agent". Every sandbox gets a default because
`km shell` and `km agent run` still need a default when no `--claude` /
`--codex` flag is passed.

## Runtime behavior

After `km create`, the sandbox carries:

- `/etc/profile.d/km-notify-env.sh` — exports `KM_AGENT=claude` or
  `KM_AGENT=codex`. Interactive shells see it.
- `/etc/km/notify.env` — same line in systemd `EnvironmentFile=` form. The
  `km-slack-inbound-poller` systemd unit reads from here.
- `~/.claude/settings.json` — unchanged (already present from Phase 62/63).
- `~/.codex/config.toml` — NEW in Phase 70. Always written regardless of
  `spec.agent.default` value. Claude-default sandboxes never start Codex, so the
  file is an inert forward-compatibility artifact.

  **Phase 92 update:** the config.toml is no longer a hardcoded heredoc in the
  userdata template — it is **synthesized** by `synthesizeCodexConfig(spec.agent)`
  (`pkg/compiler/agent_codex.go`). The base hook block below is byte-identical to
  the Phase 70 heredoc (so the Phase 92 byte-identity contract holds), but when a
  profile populates `spec.agent.codex.args` or `spec.agent.codex.tools.*`, the
  synthesizer appends an args echo and an explicit asymmetry NOTE — see
  `docs/agent-tool-gating.md` for the full write-up. Inspect the rendered file on a
  live sandbox with `cat /home/sandbox/.codex/config.toml`.

### `~/.codex/config.toml` contents (base hook block)

```toml
[features]
hooks = true

[[hooks.PermissionRequest]]
matcher = ".*"

[[hooks.PermissionRequest.hooks]]
type = "command"
command = "/opt/km/bin/km-notify-hook PermissionRequest"
timeout = 30
statusMessage = "km: notifying operator"

[[hooks.Stop]]

[[hooks.Stop.hooks]]
type = "command"
command = "/opt/km/bin/km-notify-hook Stop"
timeout = 30
```

**Important:** Codex 0.121.0 and 0.133.0 do NOT fire these hook entries. The
documented `[features] hooks` / `codex_hooks` feature in shipping Codex versions
refers to a different, undocumented event lifecycle — Phase 70's spike (Plan
70-00) confirmed that `PermissionRequest` and `Stop` events are NOT fired by
`codex exec --dangerously-bypass-approvals-and-sandbox`. The config file is
written for forward-compatibility only: if Codex ever ships a Claude-Code-style
hook API, the file becomes active without any sandbox rebuild. Under current
Codex (0.121–0.133), it has no runtime effect.

### JSONL stream parsing (the actual mechanism)

Because hooks do not fire, Phase 70 uses `codex exec --json` and parses the
JSONL stdout stream directly:

- **Session ID:** extracted from the `thread_id` field on the first
  `{"type":"thread.started"}` event.
- **Assistant message:** extracted from `item.text` on the LAST
  `{"type":"item.completed","item":{"type":"agent_message",...}}` event.
- **Done signal:** presence of `{"type":"turn.completed"}` event, or process
  exit on success. No hook event needed.

Required Codex version: `>= 0.121.0` (recommended `0.133.0+`).

### SC-3 is dropped

Codex under `--dangerously-bypass-approvals-and-sandbox` does NOT emit
`PermissionRequest` events; tools execute without an approval gate. Phase 70
success criterion SC-3 (PermissionRequest hook fires and notifies) is dropped.

Operators who require a Codex permission gate should run Codex without the
bypass flag in a separate workflow (out of scope for Phase 70).

### km-notify-hook

`/opt/km/bin/km-notify-hook` (Phase 62) gains two small branches for
forward-compat with the hook config:

- `PermissionRequest` event: synonym of Claude's `Notification`. Same gate var
  (`KM_NOTIFY_ON_PERMISSION`), same email/Slack body shape (tool name extracted
  from `.tool_name`). Under shipping Codex this branch is dead code (hooks don't
  fire), but it is correct and harmless.
- `Stop` event: prefers `.last_assistant_message` (Codex payload shape) when
  present; falls back to tailing `transcript_path` JSONL (Claude payload) when
  absent.

The hook script is **shared** — there is no per-agent fork. Cooldown logic, env-var
gates (`KM_NOTIFY_ON_PERMISSION`, `KM_NOTIFY_ON_IDLE`, `KM_NOTIFY_COOLDOWN_SECONDS`),
and downstream `km-send` / `km-slack post` calls are agent-agnostic.

## Slack inbound dispatch

When an operator posts in `#sb-{id}` against an inbound-enabled sandbox:

1. Bridge Lambda receives the Slack Events API webhook, verifies signature,
   dedupes, and enqueues to the sandbox's SQS FIFO queue (Phase 67 path).
2. `km-slack-inbound-poller` (systemd unit on the sandbox) picks up the
   message, looks up the DDB row, applies the **dispatch fork**:
   - Boot-time read: `AGENT=${KM_AGENT:-claude}` (from `/etc/km/notify.env`)
   - DDB lookup: existing row carries `agent_type` (defaults to `claude` when
     absent for Phase 67 rows)
   - **Prefix parser** (Phase 70): see § Slack prefix routing
   - Routing decision: if prefix matched and differs from row's `agent_type` AND
     a session exists → cross-agent switch; otherwise `EFFECTIVE_AGENT` may be
     overridden in place
3. Dispatch executes:
   - Claude: `claude -p "$PROMPT" --output-format json --dangerously-skip-permissions [--resume $SESSION]` (unchanged from Phase 67)
   - Codex first turn: `codex exec --json --dangerously-bypass-approvals-and-sandbox "$PROMPT"`
   - Codex resume: `codex exec resume $SESSION "$PROMPT" --json --dangerously-bypass-approvals-and-sandbox` (subcommand form, not `--resume` flag)
4. Session ID captured:
   - Claude: from `output.json` `.session_id` field (unchanged)
   - Codex: JSONL stream parser reads `thread_id` from the first
     `thread.started` event in the output file
5. DDB write-back: every successful turn updates `claude_session_id`,
   `agent_type`, `last_assistant_msg` (truncated to 2000 chars; JSON-escaped
   via `jq -Rs .` for safety).

## Slack prefix routing

A Slack message starting with `claude:` or `codex:` selects the agent for
that turn.

### Grammar

Regex: `^([Cc][Ll][Aa][Uu][Dd][Ee]|[Cc][Oo][Dd][Ee][Xx]):[[:space:]]?`

(CONTEXT.md and the plan frontmatter state the regex as
`^([Cc]laude|[Cc]odex|CLAUDE|CODEX):[[:space:]]?` for readability; the production
implementation uses per-character case classes for bash ERE `[[ =~ ]]`
compatibility on all targets.)

- Case-insensitive on the agent name (`claude`, `Claude`, `CLAUDE` all match)
- Exactly **zero or one** space after the colon
- **Anchored at message start** (`^`) — no leading whitespace tolerated
- No tolerance for spaces before the colon (`claude :` does not match)
- Mid-sentence occurrences are ignored (`what does claude: mean?` does not
  trigger routing)

### Semantics

**Case 1 — no prefix:** dispatch the row's `agent_type` (or profile default
for a fresh thread). Existing Phase 67 behavior, unchanged.

**Case 2 — prefix on a fresh thread:**

```
[#sb-x channel, profile has spec.cli.agent: claude]
Operator: codex: list workspace files
```

Poller strips the prefix, dispatches
`codex exec --json --dangerously-bypass-approvals-and-sandbox "list workspace files"`,
writes a new DDB row keyed on the new `thread_ts` with `agent_type=codex`. The
profile's compiled `KM_AGENT` on disk is **unchanged** — the override is
per-thread only. Follow-up replies in this thread (no prefix) resume Codex.

**Case 3 — same-agent prefix in existing thread:**

```
[Existing claude-rooted thread]
Operator: claude: do another thing
```

Poller strips the prefix and dispatches the **same agent**
(`claude -p --resume $SESSION "do another thing"`). No new thread. No new DDB
row. No handoff post. The prefix is a no-op continuation signal.

**Case 4 — cross-agent prefix in existing thread:** see § Cross-agent thread
switch.

## Cross-agent thread switch

When a prefix names the **other** agent inside an existing thread (DDB row's
`agent_type` differs from the requested agent), the poller orchestrates a clean
handoff.

The locked ordering fetches the OLD permalink FIRST — the OLD thread's `ts` is
already known from the inbound SQS event, so this call has no dependency on any
later step. The new top-level body is built with `$OLD_PERMALINK` already
substituted before the post, so no placeholder string is ever sent to Slack and
`chat.update` is not used in the critical path.

### Eight-step sequence

1. **Fetch the OLD-thread permalink** via
   `km-slack permalink --channel C --ts $OLD_THREAD_TS`. Falls back to
   `(unavailable)` on Slack API hiccup.
2. **Build the new top-level body** with `$OLD_PERMALINK` already substituted.
   The body leads with the NEW agent's name (capitalised) so the handoff target
   is obvious once the message posts out of the old thread. Finalised BEFORE the post:
   ```
   {Claude|Codex} will continue from {old_permalink}

   Previous assistant ({old_agent}) said:
   > {first 500 chars of last_assistant_msg}
   ```
3. **Post the NEW top-level message** in the same `#sb-x` channel via
   `km-slack post --new-message`. Capture the new message's `ts` from sidecar
   stdout into `NEW_TOP_TS`.
4. **Abort guard** — if `NEW_TOP_TS` is empty (post failed), post an error
   reply in the OLD thread, delete the SQS receipt, and skip dispatch. The OLD
   DDB row remains untouched; the operator can retry.
5. **Fetch the NEW-thread permalink** via
   `km-slack permalink --channel C --ts $NEW_TOP_TS`. Falls back to
   `(unavailable)` on failure.
6. **Post the handoff message** in the OLD thread via
   `km-slack post --thread $OLD_THREAD_TS`. Body:
   ```
   Switching to {new_agent} → continuing in this thread.
   {new_permalink}
   ```
7. **Compose the seeded prompt** by concatenating the stripped user prompt,
   a `--- Context from prior thread (agent: {old_agent}) ---` separator, and up
   to 2000 chars of the old `last_assistant_msg`.
8. **Dispatch the new agent** as a fresh first turn (no `--resume`) into the
   NEW thread. Plan 70-05's existing put-item path writes the new DDB row
   keyed on `(channel_id, NEW_TOP_TS)` with `agent_type={new_agent}` (the
   poller rewrites `THREAD_TS` to `NEW_TOP_TS` and `EFFECTIVE_AGENT` to
   `{new_agent}` before falling through to dispatch).

The OLD DDB row is **untouched** — no `aws dynamodb update-item` and no
`aws dynamodb delete-item` call references the OLD `thread_ts` inside the
cross-agent block. The OLD agent's session remains resumable: if the operator
replies in the old thread (with or without prefix), the original session
continues.

### Failure modes

| Failure | Behaviour |
|---|---|
| OLD permalink retrieval fails | Embed `(unavailable)` in new top-level body; journald log; both posts continue |
| NEW top-level post fails | Abort switch; post error reply in OLD thread; delete SQS message (no infinite retry) |
| NEW permalink retrieval fails | Embed `(unavailable)` in handoff post; journald log; switch continues |
| NEW DDB row write fails | Next reply in NEW thread is treated as a first turn (acceptable two-turn continuity loss) |

### Demo storyboard

See `.planning/phases/70-*/SPEC.md § Demo storyboard` for the 9-step
end-to-end flow used by Plan 70-09 UAT.

## km-slack sidecar additions (Phase 70-04)

Three new surfaces added to the existing `km-slack` binary:

- `km-slack post --new-message` — omits `thread_ts`, returns `ts=...` to
  stdout. Used in step 3 of the switch sequence.
- `km-slack permalink --channel C --ts T` — wraps `chat.getPermalink`. Used in
  steps 1 and 5.
- `km-slack update --channel C --ts T --text "..."` — wraps `chat.update`
  (subject to Slack's 10-minute bot edit window; not in the cross-agent critical
  path, present for completeness).

All three go through the bridge Lambda via signed Ed25519 envelopes; sandboxes
never touch the raw Slack bot token.

## km doctor

Phase 70 updates the doctor checks first planned as hook-based:

- **`codex_version_supports_jsonl`** (replaces the original
  `codex_hook_config_present`) — for each sandbox with
  `spec.cli.agent: codex`, SSM RunCommand probes `codex --version` and
  `codex exec --help | grep -- --json` to verify the installed binary supports
  the JSONL output format. WARN on mismatch. Skipped when no codex sandboxes
  exist.
- **`agent_type_consistency`** — for each `km-slack-threads` row with
  `agent_type` set, fetches the sandbox profile from S3 and confirms it still
  declares the same agent. WARN on drift (catches profiles flipped post-create
  without a `km destroy && km create`).

Run with `km doctor` or `km doctor --all-regions` for multi-region installs.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `km validate` rejects `agent: goose` | Schema enum only allows `claude`/`codex` | Use `claude` or `codex`; Goose parity is deferred |
| Codex notification never fires | Expected — hooks don't fire under `--dangerously-bypass-approvals-and-sandbox` | This is by design (SC-3 dropped). Notifications come from JSONL parse on turn completion |
| Codex session doesn't resume across turns | Session ID not captured in DDB | Check for `thread.started` in output JSONL; look for JSONL parse errors in `journalctl -u km-slack-inbound-poller` |
| `claude:` mid-sentence triggers routing | Should not happen with `^` anchor | File a bug — regex MUST anchor; see Plan 70-06 `TestPoller_PrefixParser_AnchoredAtStart` |
| Cross-agent switch shows `Continuing from (unavailable)` | Slack `chat.getPermalink` failed | Transient; retry. Check Slack API status or bridge Lambda logs |
| OLD thread doesn't resume after switch | OLD DDB row was unexpectedly modified | Should not happen — the cross-agent block never calls `update-item` or `delete-item` on the OLD `thread_ts` |
| `agent_type_consistency` WARN on `sb-x` | Profile flipped from `claude` to `codex` (or back) post-create | `km destroy sb-x && km create profile.yaml` to align profile and DDB row |
| Codex first turn works, resume fails | Wrong `codex exec resume` invocation | Resume form is `codex exec resume $SESSION "$PROMPT" --json ...` (subcommand, not `--resume` flag) |

## Hangover: `claude_session_id` column

The DynamoDB column `claude_session_id` now stores **either** a Claude session
ID or a Codex session ID, based on the row's `agent_type`. The column name is a
Phase 67 hangover — renaming requires a migration job we explicitly chose not
to run (cosmetic only; column semantics are correct as-is).

Future agents (Goose, etc.) reuse this column by adding a new `agent_type` enum
value and a new dispatch branch in the poller.

## Phase 70 deferrals

These are explicitly OUT of Phase 70 scope (see CONTEXT.md `<deferred>`):

- **Transcript streaming for Codex** (Tier 3, follow-up phase). When it lands,
  `~/.codex/config.toml` adds a `[[hooks.PostToolUse]]` entry; the JSONL
  parser gains a new event type.
- **Slack-driven approve/deny on Codex PermissionRequest** — requires Slack
  interactivity webhook into the bridge; a phase of its own.
- **Goose / other-agent parity** — `agent_type` extends cleanly; one new enum
  value + one new dispatch branch.
- **Auto-routing by content heuristics** — no "this looks like code → codex"
  classifier. Routing is explicit via prefix only.
- **Thread merge / rejoin after switch** — no mechanism. Two threads run in
  parallel after a switch; both remain independently resumable.
- **Carrying tool, file, or cwd state between agents** — only the last
  assistant message text travels in the seeded prompt.
- **Prefix in `km agent run --prompt`** — existing `--claude`/`--codex` flags
  handle CLI-side selection; the prefix parser is Slack-poller-only.

## See also

- `docs/slack-notifications.md` — Slack-side runbook (Phases 63/67/68/70)
- `OPERATOR-GUIDE.md` — full operator runbook
- `CLAUDE.md` — terse operator reference
- `.planning/phases/70-*/SPEC.md` — full design narrative + risk register
- `.planning/phases/70-*/70-VERIFY.md` — UAT results from Plan 70-09
