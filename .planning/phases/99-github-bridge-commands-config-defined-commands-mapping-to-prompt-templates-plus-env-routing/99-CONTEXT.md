# Phase 99: GitHub bridge commands — Context

**Gathered:** 2026-06-07
**Status:** Ready for planning
**Source:** Approved design spec — `docs/superpowers/specs/2026-06-07-github-bridge-commands-design.md` (design approved 2026-06-07)

<domain>
## Phase Boundary

Give operators a **command** abstraction for the GitHub comment-trigger bridge (built in
Phases 97/98). A `github.commands:` block in `km-config.yaml` defines named commands; each
bundles a **prompt template** (inline or `@file`), an optional **routing override**
(`alias`/`profile`), and an optional **per-command user allowlist**. A user invokes a command
with `@klanker-maker … /name …` placed **anywhere** in a PR/issue comment.

**Delivers:** a second resolution pass in the bridge (after the existing repo `Resolve()`) that
parses `/command` tokens, expands the matched command's template, applies command-overrides-repo
routing + auth narrowing, and feeds the resulting `{alias, profile, prompt}` into Phase 98's
**unchanged** warm / resume / cold-create dispatch. Plus config plumbing, SSM publication,
`km init` `@file` resolution, `km doctor` validation, and `km github status` listing.

**Out of scope (YAGNI):** per-command guardrails (tools/budget/model/agent) — the target
profile already controls read-only vs write-capable; named-environment indirection; template
variables beyond `{{args}}`; per-repo command *definitions* (the command set is install-global;
a repo may only *select* its default command).

**Critical invariant:** Absent `github.commands` ⇒ byte-identical to Phase 98 (no SSM command
param written, no command pass active, free-form dispatch only). Additive and opt-in.

**No SandboxProfile schema change → no `km init --sidecars`, no sandbox recreate.** Deploy is
bridge-code + config only: `make build-lambdas` + `km init --dry-run=false`.
</domain>

<decisions>
## Implementation Decisions (locked — from design spec Decisions table D1–D10)

### D1 — What a command controls
Prompt template + routing override only. **No** guardrails.

### D2 — Routing model
**Command overrides repo.** Repo is the fallback + the outer auth gate.
- `alias` = `command.alias || repo.alias`
- `profile` = `command.profile || repo.profile || default_profile`

### D3 — Command location in comment
**Anywhere** in the comment body (not anchored after the mention).

### D4 — Prompt source
Inline text **OR** `@<path>` file reference (mirrors `km create --prompt @file`). The `@file`
is read **at `km init` time** on the operator workstation and inlined into the published JSON —
**the bridge Lambda never reads a filesystem.** Path resolved relative to the `km-config.yaml`
directory. Missing/unreadable `@file` = hard `km init` error (+ `km doctor` check).

### D5 — Multiple commands in one comment
**Hard error** — one-at-a-time reply, no dispatch. Repeats of the *same* command are deduped
and allowed.

### D6 — Unknown / typo'd command
**Lenient** — an unknown `/token` is treated as plain text → free-form dispatch (or via the
effective default command). No unknown-command/help-spam reply. `/help` is the discovery path.
(Paired deliberately with D3: parse-anywhere would otherwise flag casual standalone `/token`s.)

### D7 — Per-command allowlist
**Yes — intersection / narrowing.** `repo.allow` is the outer gate (fail ⇒ silent drop,
deny-by-default, as today). `command.allow`, when set, is an inner narrowing gate applied after:
effective = `repo.allow ∩ command.allow`. A command can only *restrict*, never *widen*. A sender
who clears `repo.allow` but fails `command.allow` gets a polite "not authorized for /cmd" reply
(they're known, so a silent drop would confuse).

### D8 — Where the command set is published
**SSM** `{prefix}/config/github/commands` — a single JSON doc, NOT a Lambda env var (dodges the
4 KB env ceiling; inlined `@file` templates can be several KB). Bridge reads it at cold start
alongside existing `{prefix}/config/github/{webhook-secret,bot-login,bridge-url}` params.
Env-wins drift WARN on stale SSM value (mirrors `KM_GITHUB_REPOS`).

### D9 — Phase
Phase 99, separate from (and after) Phase 98.

### D10 — Behavior when no `/command` is typed
**Configurable `default_command`.** `github.repos[].default_command` (per-repo) overrides
`github.default_command` (install-wide). Effective = `repo.default_command || github.default_command`.
Unset ⇒ free-form passthrough (today's behavior); the default is opt-in, never imposed. When a
default applies: template expanded with `{{args}}` = comment − mention (no command token to
strip); routing override + `allow` apply exactly as for an explicit invocation. Both
`default_command` values must name a defined command — `km init`/`km doctor` error otherwise.

### Parsing rules (D3 × D5 × D6) — applied after repo resolution + repo-allow gate
1. **Strip code** — remove fenced ``` blocks and `inline code` before scanning (avoids
   ``/patch`` prose false positives).
2. **Scan anywhere** for whitespace-bounded tokens matching `^/[A-Za-z][A-Za-z0-9_-]*$`.
   Embedded-slash tokens (`/usr/bin/patch`) fail the single-segment pattern → not candidates.
3. **Match** candidates → distinct known commands. >1 known ⇒ error reply, no dispatch.
   Exactly 1 ⇒ that command. 0 known ⇒ effective default command, else free-form (unknown
   tokens ride along in the prompt/`{{args}}`).
4. **`{{args}}` extraction** — comment body minus the `@mention` token and the matched
   `/command` token, whitespace-normalized.
5. **Template expansion** — simple string replacement of `{{args}}` only (no logic). If the
   template omits `{{args}}`, the args are appended on a new line.

### Auth + resolution pipeline
`parse owner/repo → repo Resolve() → {alias, profile, repo.allow, matched}`; not matched ⇒ 200
drop; sender ∉ repo.allow ⇒ silent 200 drop; then the command pass (parse → auth narrow →
expand → route). The command pass only changes which `{alias, profile, prompt}` tuple is
produced — **Phase 98's dispatch consumes it unchanged.** No new dispatch path.

### Reply paths (reuse the Phase-97 installation token + 👀 ACK)
- Multi-command error: `🤖 Use one command at a time — found /patch and /review.`
- Command not authorized: `🤖 You're not authorized to run /deploy.`
- `/help` (built-in): lists each command's name + `description`, notes the effective default
  command for that repo. No unknown-command reply.

### Plumbing (the three-edit config rule)
`GithubConfig.Commands map[string]GithubCommandEntry` (`Description, Alias, Profile, Allow,
Prompt`), `GithubConfig.DefaultCommand`, `GithubRepoEntry.DefaultCommand` — each needs struct +
construction line + **v2→v merge-list entry** (per `project_config_key_merge_list` memory) +
getters. `help` is a reserved built-in name; a user-defined `help` is ignored with a doctor WARN.

### Claude's Discretion
- Exact Go file/function layout within `pkg/github/bridge/` and the config package (follow the
  existing `Resolve()` / `ExtractMentionBody()` patterns and the Phase 97/98 SSM-config reader).
- Test file organization (table-driven, in the style of existing `Resolve()` tests).
- Wording/formatting details of the `/help` and error replies beyond the spec examples.
- `km doctor` check grouping/ordering within the existing GitHub doctor group.
</decisions>

<specifics>
## Specific Ideas

- Full worked config example, field tables, and the resolution/auth ASCII pipeline are in the
  design spec (`docs/superpowers/specs/2026-06-07-github-bridge-commands-design.md`) §§ "Config
  surface", "GithubCommandEntry fields", "Resolution & auth pipeline".
- Build directly on Phase 97/98 code: `pkg/github/bridge/resolve.go` (`Resolve()`,
  `ExtractMentionBody()`), the github SSM-config reader, `km init` SSM writes, the `km doctor`
  GitHub group (98-03 alias-collision check to be extended), and `km github status`.
- Test strategy (design spec § "Test strategy"): pure-function table tests for code-strip+scan,
  single-vs-multi detection, `{{args}}` extraction, template expansion, `@file` resolution,
  resolution precedence, effective-default resolution, auth intersection; bridge-handler tests
  for reply paths + `/help` + lenient-unknown + default-command; `km doctor` tests for each WARN.
</specifics>

<deferred>
## Deferred Ideas

- Per-command guardrails (tools / budget / model / agent selection) — target profile already
  controls capability.
- Named-environment indirection (`environments:` block) and per-repo-per-command box derivation.
- Template variables beyond `{{args}}` (poller already prepends repo/PR/branch preamble).
- Per-repo command *definitions* (command set is install-global for v1).
</deferred>

---

*Phase: 99-github-bridge-commands-config-defined-commands-mapping-to-prompt-templates-plus-env-routing*
*Context gathered: 2026-06-07 from approved design spec*
