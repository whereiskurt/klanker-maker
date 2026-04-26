# Phase 62: Claude Code operator-notify hook for permission and idle events — Context

**Gathered:** 2026-04-26
**Status:** Ready for planning
**Source:** PRD Express Path (`docs/superpowers/specs/2026-04-26-operator-notify-hook-design.md`)

<domain>
## Phase Boundary

This phase delivers a one-way notification mechanism for Claude Code agents
running on km sandboxes. When the agent hits a permission prompt
(`Notification` hook event) or finishes a turn / goes idle (`Stop` hook
event), a signed email is sent to the operator (or a profile-overridden
recipient).

**v1 scope:** notification-only. Hook script is wired into
`~/.claude/settings.json` at sandbox compile time. Behavior is gated by
four new `spec.cli` profile fields, optionally overridden per invocation
by CLI flags on `km shell` and `km agent run`.

**Out of scope (v2 forward-compatible):** closed-loop reply ingestion
(operator emails back, agent resumes). The v1 design must NOT close any
doors here — subject line conventions and the single-recipient field are
chosen so v2 can layer on without breaking changes.

**Dependencies (all complete):**
- Phase 45 — `km-send`/`km-recv` sandbox scripts and operator-side
  `km email send/read` CLI (signed email infrastructure already wired).
- Phase 50/51 — `km agent run` and tmux session support (hook fires
  inside Claude processes launched via these flows).
- `spec.execution.configFiles` — already supports dropping
  `~/.claude/settings.json` onto sandboxes; the merge logic this phase
  introduces sits next to it.

</domain>

<decisions>
## Implementation Decisions (LOCKED — from spec)

### Profile Schema (under `spec.cli`)

The following four fields are added. All optional. Defaults are
implementation-defined as below:

| Field | Type | Default | Effect |
|---|---|---|---|
| `notifyOnPermission` | bool | `false` | Enable email on `Notification` hook events |
| `notifyOnIdle` | bool | `false` | Enable email on `Stop` hook events |
| `notifyCooldownSeconds` | int | `0` (no cooldown) | Suppress emails within N seconds of the last send (per-sandbox, shared across both event types) |
| `notificationEmailAddress` | string | unset → operator default | Override recipient (e.g., team inbox) |

- Schema additions go in `pkg/profile/types.go` and
  `pkg/profile/schemas/sandbox_profile.schema.json`.
- All fields are independently togglable.

### CLI Overrides

`km shell` and `km agent run` gain four flags:
- `--notify-on-permission` / `--no-notify-on-permission`
- `--notify-on-idle` / `--no-notify-on-idle`

Cooldown and recipient address are **profile-only** in v1 (no CLI flag).

### Mechanics — Compile-Time

The compiler (`pkg/compiler/userdata.go`) **always** writes the hook
script and `settings.json` entries during sandbox provisioning,
regardless of profile settings:

1. Drops `/opt/km/bin/km-notify-hook` (a bash script).
2. Merges hook entries into `~/.claude/settings.json` for the sandbox user.
   - **Merge semantics:** parse existing user-supplied `settings.json`
     (if any from `spec.execution.configFiles`) as JSON; *append* the
     km-notify-hook command to existing `hooks.Notification` and
     `hooks.Stop` arrays (creating them if absent). User-supplied hooks
     are preserved; km hook runs alongside. **Fail fast** if user JSON
     is invalid.
3. Writes profile-derived defaults into `/etc/environment`:
   - `KM_NOTIFY_ON_PERMISSION=1` (or 0)
   - `KM_NOTIFY_ON_IDLE=1` (or 0)
   - `KM_NOTIFY_COOLDOWN_SECONDS=60`
   - `KM_NOTIFY_EMAIL=team@example.com` (only if `notificationEmailAddress` set)
   - **Each variable is written iff its corresponding profile field is
     set (true OR false).** Unset profile fields produce no env var.

### Mechanics — Run-Time

The operator-side CLI (`internal/app/cmd/shell.go`,
`internal/app/cmd/agent.go`) computes effective values:

```
effective_on_permission = cli_flag ?? profile.notifyOnPermission ?? false
effective_on_idle       = cli_flag ?? profile.notifyOnIdle       ?? false
```

It passes them as env vars over SSM when launching Claude:
```
KM_NOTIFY_ON_PERMISSION=1 KM_NOTIFY_ON_IDLE=0 claude ...
```

Env vars set on the Claude process propagate to its hook subprocesses.
SSM-launched env vars take precedence over `/etc/environment`. If neither
flag is given, `/etc/environment` defaults take effect.

### Hook Script (`/opt/km/bin/km-notify-hook`)

Bash script, ~60 lines. Behavior:

1. Receives event name as first arg (`Notification` or `Stop`).
2. **Gate check:** exits 0 if the corresponding `KM_NOTIFY_ON_*` env var
   is not `1`.
3. **Cooldown check:** if `KM_NOTIFY_COOLDOWN_SECONDS > 0` and
   `/tmp/km-notify.last` is younger than cooldown, exit 0.
4. **Read hook payload from stdin** (Claude Code passes JSON).
5. **Build subject + body file:**
   - **Notification event:** subject `[<sandbox-id>] needs permission`;
     body uses `message` field from payload.
   - **Stop event:** subject `[<sandbox-id>] idle`; body extracts last
     assistant message text from `transcript_path` JSONL via `jq`
     (`select(.type=="assistant") | .message.content[]? | select(.type=="text") | .text`).
   - Both: append footer with `Attach: km agent attach <sandbox-id>` and
     `Results: km agent results <sandbox-id>`.
6. **Send via km-send:** uses `--body <file>` (NOT stdin — required for
   OpenSSL 3.5+ signing per CLAUDE.md). If `KM_NOTIFY_EMAIL` is set,
   pass `--to` arg; otherwise km-send defaults to operator.
7. **Failure handling:** km-send failure does NOT block Claude — hook
   always exits 0.
8. **Update cooldown:** write `date +%s` to `/tmp/km-notify.last` on
   successful send.

### Hook Invariants

- **Never blocks Claude.** Hook always exits 0 even on send failure.
- **Body via file**, not stdin. (CLAUDE.md note: OpenSSL 3.5+ signing
  reliability requires `--body <file>`.)
- **Cooldown is per-sandbox, not per-event.** A Notification followed by
  a Stop within the cooldown window emits one email, by design.
- **Default recipient** falls back to km-send's operator default when
  `KM_NOTIFY_EMAIL` is unset.

### Email Format

Plain text, signed via existing km-send protocol.

**Permission event:**
```
Subject: [sb-abc123] needs permission
From: sb-abc123@sandboxes.klankermaker.ai

Claude needs your permission to use Bash

---
Attach:  km agent attach sb-abc123
Results: km agent results sb-abc123
```

**Idle event:**
```
Subject: [sb-abc123] idle
From: sb-abc123@sandboxes.klankermaker.ai

I've finished refactoring the auth middleware. Let me know what you'd
like to test next.

---
Attach:  km agent attach sb-abc123
Results: km agent results sb-abc123
```

Subject line format `[<sandbox-id>] <event-keyword>` is **intentional** —
v2's closed-loop receiver will match operator replies back to the
originating sandbox. v2 will primarily use `In-Reply-To`/`References`
headers; the subject prefix is the human-readable secondary key.

### Test Surface

**Compiler unit tests (`pkg/compiler/compiler_test.go`):**
- Profile with both bools `false` → no `KM_NOTIFY_ON_*` lines in
  `/etc/environment`, but hook entries still in `settings.json`.
- Profile with `notifyOnPermission: true` only → `KM_NOTIFY_ON_PERMISSION=1`
  written, idle var absent.
- Profile with `notifyCooldownSeconds: 30` → variable present.
- Profile with `notificationEmailAddress: "x@y"` → `KM_NOTIFY_EMAIL=x@y`
  written.
- `spec.execution.configFiles` user-supplied `settings.json` → hook
  entries merged in alongside, not clobbered.

**Hook script tests (bash/bats or Go test that shells out):**
- Notification with `KM_NOTIFY_ON_PERMISSION=0` → exits 0, no `km-send` call.
- Notification with `KM_NOTIFY_ON_PERMISSION=1` → `km-send` invoked with
  expected subject and body file containing message text.
- Stop with synthetic transcript JSONL → body contains last assistant
  text.
- Cooldown: two back-to-back invocations with
  `KM_NOTIFY_COOLDOWN_SECONDS=10` → second call exits 0 without calling
  km-send.
- `km-send` failure → hook still exits 0.
- km-send is stubbed (PATH override) so no real email sent.

**CLI flag tests (`internal/app/cmd/`):**
- `km shell --notify-on-idle` overrides profile default `false` → env
  var set in SSM session.
- `km agent run --no-notify-on-permission` overrides profile default
  `true` → env var set to 0.

**E2E (manual / opt-in CI):**
- Create a sandbox from a profile with `notifyOnPermission: true`, run
  `km agent run --prompt "..."` that triggers a permission prompt,
  confirm operator inbox receives a signed email.
- Same with `notifyOnIdle: true` and a benign prompt; confirm idle email
  arrives after Claude finishes.
- Confirm `notificationEmailAddress: "alt@example.com"` routes to the
  override address.

### Implementation Footprint (Files Touched)

- `pkg/profile/types.go` — add four `cli` fields.
- `pkg/profile/schemas/sandbox_profile.schema.json` — schema additions.
- `pkg/compiler/userdata.go` — write `/etc/environment` entries, drop
  `/opt/km/bin/km-notify-hook`, merge `settings.json` hook entries.
- `pkg/compiler/compiler_test.go` — coverage per matrix above.
- `internal/app/cmd/shell.go` — `--notify-on-{permission,idle}` flags +
  env var injection over SSM.
- `internal/app/cmd/agent.go` (or `agent_run.go`) — same flags +
  injection.
- New: `pkg/compiler/assets/km-notify-hook.sh` (or inlined string in
  `userdata.go`, matching the pattern used for the pre-push hook in
  Phase 25).
- New: hook script test (`pkg/compiler/assets/km-notify-hook_test.sh`
  bats/shell, or Go test that shells out).

**No** new infra modules, **no** new sidecars, **no** SES policy changes
(km-send is already wired). Existing sandboxes will **not** gain hook
support retroactively — hook script and `/etc/environment` entries are
written into user-data at provision time. To enable on a pre-existing
sandbox, `km destroy && km create` it from a profile with the new fields.

### Claude's Discretion

The spec leaves the following open for the planner/implementation:
- **Hook script storage:** inline string in `userdata.go` vs.
  `pkg/compiler/assets/km-notify-hook.sh` file embedded via Go embed.
  Spec notes the pre-push hook pattern as precedent — planner should
  follow whatever that phase chose.
- **Test framework:** bats vs. Go-shell-out for hook script tests.
  Either is acceptable; planner should pick whichever fits existing
  test infrastructure.
- **Plan breakdown / wave assignment:** planner decides task decomposition
  (e.g., schema + compiler in one wave, CLI flags in another, hook
  script + tests as third).
- **Requirement IDs:** roadmap currently has `Requirements: TBD`; the
  planner should derive specific requirement IDs from the spec
  decisions (likely under a CLI/UX category in REQUIREMENTS.md, or
  add new ones if needed).

</decisions>

<specifics>
## Specific References

- **Spec source:** `docs/superpowers/specs/2026-04-26-operator-notify-hook-design.md`
  (322 lines, committed in `4469fd9`).
- **Existing pattern — pre-push hook:** Phase 25 (or thereabouts) drops a
  `/opt/km/hooks/pre-push` script via `pkg/compiler/userdata.go`. The
  km-notify-hook should follow the same idiom (look for the
  `core.hooksPath` / `pre-push` pattern in `pkg/compiler/userdata.go`).
- **Existing pattern — `noBedrock` flag:** `spec.cli.noBedrock` is the
  precedent for "operator-side default that affects how km invokes
  Claude." New fields should follow its lead.
- **km-send protocol:** `docs/multi-agent-email.md` documents the Ed25519
  signing protocol. The hook must use `km-send --body <file>` (not
  stdin) per CLAUDE.md.
- **Claude Code hook payload schemas:**
  - `Notification`: `{session_id, transcript_path, hook_event_name, message}`
  - `Stop`: `{session_id, transcript_path, hook_event_name, stop_hook_active}`
- **Sandbox env vars available to the hook:** `KM_SANDBOX_ID`,
  `KM_OPERATOR_EMAIL`, `KM_EMAIL_ADDRESS` (see CLAUDE.md "Key environment
  variables" section).

</specifics>

<deferred>
## Deferred Ideas (v2+ — Out of Scope)

Explicitly out of scope for this phase, but the v1 design preserves
forward compatibility:

- **Closed-loop reply ingestion.** Operator emails the sandbox a reply;
  agent picks it up, resumes. v1 subject prefix `[<sandbox-id>] <event>`
  and single `notificationEmailAddress` field are chosen so v2 can layer
  on without breaking changes.
- **Slack / webhook delivery.** Email only in v1.
- **Filtering by tool name** (e.g., "only notify on Bash permissions").
  All Notification events fire if the gate is on.
- **Rich HTML email.** Plain text only.
- **Per-run correlation IDs.** Subject prefix is enough for v1.
- **CLI override for cooldown / recipient address.** Profile-only in v1.
- **Multiple recipients or separate `replyEmailAddress`.** v1 uses one
  field for both send and (implicit) reply target.
- **Retroactive hook installation on pre-existing sandboxes.** Operators
  must destroy and recreate.

</deferred>

---

*Phase: 62-claude-code-operator-notify-hook-for-permission-and-idle-events*
*Context gathered: 2026-04-26 via PRD Express Path*
*Source spec: docs/superpowers/specs/2026-04-26-operator-notify-hook-design.md (4469fd9)*
