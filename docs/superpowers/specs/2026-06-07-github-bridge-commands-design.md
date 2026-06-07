# GitHub bridge commands — templated prompts + env routing (Phase 99)

> **Status:** design approved 2026-06-07. Lands as **Phase 99**, after Phase 98
> (GitHub bridge expansion) closes. Builds directly on the Phase 97/98 bridge
> resolve→dispatch pipeline and shared-alias work.
>
> **Predecessor spec:** `2026-06-06-github-app-bridge-pr-review-design.md` (Phases 97/98).

## Problem

Today the GitHub comment-trigger bridge dispatches the comment **verbatim**:
`ExtractMentionBody()` (`pkg/github/bridge/resolve.go`) strips `@klanker-maker`
and ships the rest as the agent prompt. Routing is purely `owner/repo →
{alias, profile}` via `Resolve()`. There is no way for an operator to:

1. give a short verb a rich, reusable instruction template (`/patch` → "apply the
   smallest diff, run tests, open a PR"), or
2. route different *kinds* of request to different sandboxes/envs from the same repo
   (`/patch` → a write-capable dev box, `/review` → a read-only box).

The Phase 97/98 design explicitly chose "free-form prompt" as the command grammar
(predecessor spec line 63) and left structured commands as a future addition. This
spec is that addition.

## Goal

Let operators define **commands** in `km-config.yaml` that a user invokes in a
PR/issue comment (`@klanker-maker … /name …`). A command bundles:

- a **prompt template** (inline or loaded from a file),
- an optional **routing override** (which sandbox alias / profile handles it), and
- an optional **per-command user allowlist** (narrowing who may run it).

No command in a comment → today's free-form behavior, byte-identical.

## Non-goals (YAGNI)

- Per-command guardrails (tools / budget / model / agent selection). The target
  profile's `spec.agent.tools` already makes a box read-only vs write-capable.
- Named-environment indirection (commands referencing a separate `environments:`
  block) and per-repo-per-command box derivation. The chosen routing model is
  command-overrides-repo; these alternatives were considered and rejected.
- Template variables beyond `{{args}}`. The poller already prepends a repo/PR/branch
  context preamble, so templates need not restate PR metadata.
- Per-repo command overrides. Commands are install-global for v1.

## Decisions (all resolved during brainstorming)

| # | Decision | Choice |
|---|---|---|
| D1 | What a command controls | **Prompt template + routing override** (no guardrails) |
| D2 | Routing model | **Command overrides repo**; repo is the fallback + auth gate |
| D3 | Command location in the comment | **Anywhere** (not anchored after the mention) |
| D4 | Prompt source | **Inline text OR `@file`** reference (mirrors `km create --prompt @file`) |
| D5 | Multiple commands in one comment | **Hard error** (use one at a time) |
| D6 | Unknown / typo'd command | **Lenient** — unknown `/token` treated as plain text → free-form dispatch; `/help` is the discovery path |
| D7 | Per-command allowlist | **Yes**, intersection/narrowing with `repo.allow` |
| D8 | Where the command set is published | **SSM** `{prefix}/config/github/commands` (not a Lambda env var) |
| D9 | Phase | **Phase 99**, separate from the in-flight Phase 98 |

## Config surface

```yaml
github:
  default_profile: profiles/github-review.yaml
  repos:
    - match: myorg/*
      alias: gh-myorg
      allow: [alice, bob, carol]      # engagement gate: who may trigger the bot here
  commands:
    patch:
      description: apply the smallest fix and open a PR   # shown in /help
      alias: gh-myorg-dev                                 # routing OVERRIDE (write-capable box)
      profile: profiles/gh-patch.yaml
      prompt: |                                           # inline template
        You are in patch mode. {{args}}
        Apply the smallest diff that fixes it, run the test suite,
        and if green push a branch and open a PR via km-github.
    review:
      description: read-only review, inline findings
      prompt: "@prompts/gh-review.txt"                    # template loaded from file
      # no alias/profile → uses the repo's gh-myorg box
    deploy:
      description: cut a staging deploy
      alias: gh-staging-deployer
      profile: profiles/gh-deploy.yaml
      allow: [alice]                                      # per-command allowlist (narrower)
      prompt: "@prompts/gh-deploy.txt"
```

### `GithubCommandEntry` fields

| YAML key | Type | Meaning |
|---|---|---|
| `description` | string | One-liner shown by `/help` and `km github status`. |
| `alias` | string (optional) | Sandbox alias override. Empty → repo's alias. |
| `profile` | string (optional) | SandboxProfile path override. Empty → repo's profile → `default_profile`. |
| `allow` | []string (optional) | Per-command login allowlist; intersected with `repo.allow`. Empty → only `repo.allow` applies. |
| `prompt` | string | Inline template, OR `@<path>` to load the template from a file at `km init` time. |

`commands` is a map keyed by command name (the `/name` token, without the slash).
Reserved name: `help` (built-in; a user-defined `help` is ignored with a
`km doctor` WARN).

## `@file` prompt references (D4)

- A `prompt:` value beginning with `@` is a **file reference**; otherwise it is
  literal inline text.
- The `@file` is read **at `km init` time** on the operator workstation; its
  contents are inlined into the published command JSON. **The bridge Lambda never
  reads a filesystem.**
- Path is resolved relative to the `km-config.yaml` directory.
- A missing/unreadable `@file` is a **hard `km init` error** (and a `km doctor`
  check), never a silent empty prompt.
- Mirrors the existing `km create --prompt <text-or-@file>` convention.

## Storage & publication (D8)

Inlined `@file` templates can be several KB; the Lambda env-var ceiling is **4 KB
total across all variables**, already partly consumed by `KM_GITHUB_REPOS`. So the
command set is published to **SSM**, not an env var:

- `km init` assembles the command map (resolving `@file` → inline text) and writes
  one JSON document to **`{prefix}/config/github/commands`**.
- The bridge reads it at cold start, alongside the existing
  `{prefix}/config/github/{webhook-secret,bot-login,bridge-url}` parameters.
- Env-wins drift WARN if a stale SSM value differs from the local config (mirrors
  the `KM_GITHUB_REPOS` drift handling).

This deliberately diverges from the repos-in-env convention to remove the size
cliff; the github SSM-config pattern already exists, so it adds no new concept.

## Parsing rules (D3, D5, D6)

Applied in the bridge after repo resolution and the repo-allow gate:

1. **Strip code** — remove fenced ```` ``` ```` blocks and `` `inline code` `` from
   the comment body before scanning, to avoid false positives (e.g. a literal
   `` `/patch` `` reference in prose).
2. **Scan anywhere** for whitespace-bounded tokens matching exactly
   `^/[A-Za-z][A-Za-z0-9_-]*$`. Embedded-slash tokens like `/usr/bin/patch` are
   **not** candidates (they fail the single-segment pattern).
3. **Match** candidates against defined command names → the set of **distinct known
   commands**. Candidate tokens that are *not* defined commands are treated as plain
   text (lenient, D6):
   - **> 1 distinct known command → error reply** ("Use one command at a time —
     found `/patch` and `/review`."), no dispatch. Repeats of the *same* command
     are deduped and allowed.
   - **exactly 1 known command** → that command (continue to auth + dispatch).
   - **0 known commands** → free-form, dispatch to the repo box (today's path) —
     regardless of any unknown `/token`s, which ride along as part of the prompt.
4. **`{{args}}` extraction** — the comment body with the `@mention` token and the
   matched `/command` token removed, whitespace-normalized. Example:
   `@bot please /patch the login bug` → `{{args}}` = `please the login bug`.
5. **Template expansion** — substitute `{{args}}` in the (possibly `@file`-loaded)
   template. Simple string replacement only (no logic/conditionals). If the
   template omits `{{args}}`, the args are appended on a new line.

### Rationale: parse-anywhere (D3) × lenient-unknown (D6)

These two choices are paired deliberately. Parsing **anywhere** means a stray
standalone `/token` in an otherwise free-form comment — e.g.
`@bot can you check the /api endpoint` — is a candidate token but not a defined
command. **Lenient** handling (D6) treats such unknown tokens as plain text and
dispatches the comment free-form: casual mentions stay frictionless and read the way
people actually write. The trade-off is that a *typo'd real command* (`/patdh`)
silently runs free-form on the default box rather than being flagged; `/help` is the
discovery path for the correct spelling. (A strict variant — unknown `/token` →
help reply — was considered and rejected for the parse-anywhere false-positive
friction it would cause.)

## Resolution & auth pipeline (D2, D7)

```
parse owner/repo  → repo Resolve() → {alias, profile, repo.allow, matched}
  ├─ not matched                          → 200 drop
  ├─ sender ∉ repo.allow                   → 200 drop (silent; deny-by-default, today)
  ├─ parse commands (Parsing rules)
  │    ├─ > 1 known command     → error reply, no dispatch
  │    ├─ exactly 1 known cmd C
  │    │    ├─ C.allow set AND sender ∉ C.allow → "not authorized for /deploy" reply, no dispatch
  │    │    └─ else:
  │    │         prompt  = expand(C.prompt, {{args}})
  │    │         alias   = C.alias   || repo.alias
  │    │         profile = C.profile || repo.profile || default_profile
  │    └─ no command            → prompt = free-form body; target = repo's {alias, profile}
  └─ feed {alias, profile, prompt} into Phase 98's warm / resume / cold-create dispatch
```

**Auth model (D7 — intersection / narrowing):**
`repo.allow` is the **outer** gate: a sender not in it is dropped silently
(deny-by-default, exactly as today), whether or not the comment names a command.
`command.allow`, when set, is an **inner narrowing** gate applied *after* the repo
gate — effective access = `repo.allow ∩ command.allow`. A command can only
*restrict*, never *widen*. A sender who clears `repo.allow` but fails
`command.allow` gets a polite "not authorized for `/deploy`" reply (they are a known
user, so a silent drop would be confusing). A sender who fails `repo.allow` is
dropped silently regardless of any command.

**Downstream reuse:** the command pass only changes which `{alias, profile, prompt}`
tuple is produced. Phase 98's existing warm-enqueue / stopped-box auto-resume /
cold-create-with-S3-staged-profile dispatch consumes that tuple unchanged. No new
dispatch path is introduced.

## Reply paths

The bridge already mints an installation token and posts the 👀 ACK reaction
(`issues:write` scope landed in Phase 97). The three new replies reuse that token:

- **Multi-command error** — `🤖 Use one command at a time — found /patch and /review.`
- **Command not authorized** — `🤖 You're not authorized to run /deploy.`
- **`/help`** (built-in) — lists each command's name + `description`.

(There is no unknown-command reply — unknown `/token`s dispatch free-form per D6.)

## Plumbing & deploy

- **Config struct:** `GithubConfig.Commands map[string]GithubCommandEntry`
  (`Description, Alias, Profile, Allow, Prompt`) + getter + **the v2→v merge-list
  entry** (the three-edit config rule — struct, construction line, merge-list).
- **`km init`:** resolve `@file` prompts → write assembled JSON to SSM
  `{prefix}/config/github/commands`; env-wins drift WARN on stale value.
- **`km doctor`:** every `@file` exists/readable; every command profile resolvable
  (and cold-createable when a command sets its own alias); `help` not shadowed;
  command-alias ↔ repo-alias overlap WARN (extends the 98-03 alias-collision check);
  SSM command param present when `github.commands` is configured.
- **`km github status`:** list configured commands (name + description + target).
- **Bridge:** built-in `/help`; read commands from SSM at cold start.
- **Deploy:** `make build-lambdas` + `km init --dry-run=false` (bridge code +
  SSM/config). **No SandboxProfile schema change → no `km init --sidecars`, no
  sandbox recreate.** Bridge-side + config only.

## Dormancy / back-compat

Absent `github.commands` → the bridge behaves byte-identically to Phase 98 (no SSM
command param, no command pass, free-form dispatch only). The feature is additive
and opt-in, consistent with `github.repos` dormancy.

## Test strategy

Pure-function tests (no AWS, table-driven, in the style of `Resolve()`):
- code-strip + token scan (including `/usr/bin/patch` and code-fenced false-positive
  cases),
- single-vs-multi-command detection (incl. same-command repeats),
- `{{args}}` extraction with the command token anywhere in the body,
- template expansion (with and without `{{args}}` placeholder),
- `@file` resolution at init (present / missing),
- resolution precedence (override / fallback to repo / default_profile),
- auth intersection (in both / fails command / fails repo).

Bridge-handler tests for the reply paths (multi-command error,
command-not-authorized) and the `/help` listing, plus a lenient-unknown test
(unknown `/token` dispatches free-form, no reply).

`km doctor` tests for `@file`-missing, profile-unresolvable, `help`-shadow, and
alias-overlap WARNs.

## Open questions

None — all resolved during brainstorming (see Decisions table).
