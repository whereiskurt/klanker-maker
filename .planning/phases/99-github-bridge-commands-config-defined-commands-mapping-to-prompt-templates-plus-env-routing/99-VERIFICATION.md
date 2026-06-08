---
phase: 99-github-bridge-commands-config-defined-commands-mapping-to-prompt-templates-plus-env-routing
verified: 2026-06-07T23:55:00Z
status: passed
score: 9/9 success criteria verified
re_verification:
  previous_status: gaps_found
  previous_score: 8/9
  gaps_closed:
    - "install-wide github.default_command dispatches when no /verb in comment (SC3a)"
  gaps_remaining: []
  regressions: []
human_verification:
  - test: "End-to-end live command dispatch via GitHub PR comment"
    expected: "Posting @bot /review <args> on a configured PR triggers bridge 👀 ACK, agent receives expanded template, PR review posted"
    why_human: "Requires a live GitHub App install, real SSM params, and a running sandbox — cannot test in go test. Documented in 99-VALIDATION.md Manual-Only section."
  - test: "km doctor GitHub command health with live SSM param"
    expected: "km doctor shows command checks pass and SSM param present check is green"
    why_human: "SSM param present check reads live AWS SSM — cannot fake in automated tests"
---

# Phase 99: GitHub Bridge Commands Verification Report

**Phase Goal:** Give operators a `github.commands:` abstraction for the GitHub comment-trigger bridge (Phases 97/98): named commands bundling a prompt template, optional routing override, and optional per-command allowlist. Bridge gains a second resolution pass: strips code, scans for `/name` tokens, expands template (`{{args}}`), and routes via command-overrides-repo. Auth: deny-by-default outer (repo.allow) + inner narrowing (command.allow). Multi-command = error; unknown = lenient; built-in `/help`. Configurable `default_command`. Commands published to SSM; `km doctor` validates; `km github status` lists. No SandboxProfile change. Absent `github.commands` => byte-identical to Phase 98.

**Verified:** 2026-06-07T23:55:00Z (initial) / 2026-06-07T20:00:00Z (re-verification)
**Status:** passed
**Re-verification:** Yes — after SC3a gap closure (commits 95978591 + a895f098)

---

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| SC1 | Absent `github.commands` => byte-identical Phase 98 (no SSM param written, no command pass) | VERIFIED | `TestHandle_CommandsDormant` + `TestHandle_CommandsDormant_EmptyMap` pass; `init.go:1195` gates on `len(cfg.Github.Commands) == 0`; SSMCommandsFetcher returns empty map (not error) on ParameterNotFound |
| SC2 | `github.commands:` round-trips through config; `@file` resolved at `km init` (missing => hard error); assembled set published to SSM; read by bridge at cold start | VERIFIED | `TestGithubConfigCommands` (3 subtests pass); `TestResolveCommandPrompts` (5 subtests pass); `TestInitGitHubCommands_WritesAssembledJSON` passes; `SSMCommandsFetcher` in `aws_adapters.go:914` reads `{prefix}/config/github/commands` at cold start |
| SC3 | Exactly one known `/command` expands template (`{{args}}` substituted) and dispatches with `command.alias \|\| repo.alias` / `command.profile \|\| repo.profile \|\| default_profile` | VERIFIED | `TestHandle_KnownCommand_HappyPath` + `TestHandle_KnownCommand_ColdPath` pass; `ResolveCommandRouting` function verified; dispatch uses `res.Alias`, `res.Profile`, `res.Prompt` from `CommandPassResult` |
| SC3a | Per-repo `default_command` dispatches on command-less comment; install-wide `github.default_command` also dispatches; `km init`/`km doctor` ERROR when undefined | VERIFIED | Per-repo path confirmed unchanged. Install-wide gap CLOSED: `default_command` now travels in the CommandSet SSM envelope; `main.go` sources `defaultCommand` from `commandsFetcher.Fetch()` return (not env); `KM_GITHUB_DEFAULT_COMMAND` env read REMOVED. Regression guard: `TestInitGitHubCommands_DefaultCommandRoundTrip` + `TestSSMCommandsFetcher_ParsesEnvelope` both pass. |
| SC4 | Token found anywhere; fenced/inline code suppressed; embedded-slash tokens ignored (`/usr/bin/patch`); dedup repeats; multi-distinct-command => error + reply | VERIFIED | `TestCommandParse` (13 subtests pass): fenced code suppression, inline backtick suppression, embedded slash rejection, dedup, multi-command error all verified |
| SC5 | Unknown `/token` => plain text (free-form dispatch); `/help` posts command listing | VERIFIED | `TestHandle_UnknownToken` (passthrough, no reply, dispatch proceeds); `TestHandle_Help` (commenter called with help text); `CommandActionPassthrough` path verified |
| SC6 | `repo.allow` gate: silent drop; `command.allow` gate: polite reply + no dispatch | VERIFIED | `TestHandle_CommandNotAuthorized` verifies deny reply; `TestCommandAuth` (5 subtests pass); `CommandAllowed` function in `commands.go:347` |
| SC7 | Routing override feeds Phase 98 warm/stopped-resume/cold-create pipeline unchanged | VERIFIED | `webhook_handler.go:299-301` sets `alias = res.Alias`, `profile = res.Profile`, `promptBody = res.Prompt` before existing Phase 98 dispatch; `TestHandle_KnownCommand_ColdPath` verifies cold-create path with command routing |
| SC8 | `km doctor` reports command health; `km github status` lists configured commands | VERIFIED | `TestDoctorGitHubCommands*` (12 tests pass): @file-missing WARN, profile-unresolvable WARN, help-shadow WARN, alias-overlap WARN, undefined default_command ERROR; `TestGitHubStatusCommands*` (3 tests pass) |
| SC9 | All Phase 97/98 success criteria hold (no regression) | VERIFIED | Full `go test ./pkg/github/bridge/... -count=1` is green; all Phase 97/98 handle tests (`TestHandle_AutoResume`, `TestHandle_ThreadBypass`, etc.) pass with no modification |

**Score:** 9/9 success criteria verified

---

## Re-Verification: SC3a Gap Closure Detail

### What was wrong (initial verification)

`main.go` read install-wide `default_command` from `os.Getenv("KM_GITHUB_DEFAULT_COMMAND")`. No code path ever wrote that env var to the Lambda — not TF module v1.0.0 or v1.1.0, not terragrunt.hcl, not `ExportTerragruntEnvVars()`. `WebhookHandler.DefaultCommand` was always `""` at runtime.

### Fix applied (commit 95978591)

Approach: fold `default_command` into the existing SSM document via a `CommandSet` envelope `{"commands": {...}, "default_command": "..."}`. No new env var, no TF change.

**Verified changes:**

1. `pkg/github/bridge/commands.go` — `CommandSet` struct added with `Commands map[string]CommandEntry` and `DefaultCommand string` (json:"default_command,omitempty").

2. `pkg/github/bridge/interfaces.go` — `CommandsFetcher.Fetch` signature updated to `(map[string]CommandEntry, string, error)` (returns default_command as second return).

3. `pkg/github/bridge/aws_adapters.go` — `SSMCommandsFetcher.Fetch` unmarshals the envelope; cache struct holds `defaultCommand string`; `ParameterNotFound` returns `(empty map, "", nil)` (dormancy preserved); envelope returns both `cs.Commands` and `cs.DefaultCommand`.

4. `internal/app/cmd/init.go` — `PublishGitHubCommandsToSSM` marshals a `commandSetEnvelope{Commands: resolved, DefaultCommand: cfg.Github.DefaultCommand}` instead of a bare map.

5. `cmd/km-github-bridge/main.go` — `KM_GITHUB_DEFAULT_COMMAND` env read REMOVED. `commands, defaultCommand, cmdErr := commandsFetcher.Fetch(ctx)` sources `defaultCommand` directly from fetcher return. Both values passed to `WebhookHandler{Commands: commands, DefaultCommand: defaultCommand, ...}`.

6. No TF or terragrunt files changed — confirmed by `git show 95978591 -- infra/**` returning empty.

### New regression-guard tests (all pass)

- `TestInitGitHubCommands_DefaultCommandRoundTrip` — publishes config with `DefaultCommand: "triage"`, asserts the written SSM JSON envelope contains `"default_command":"triage"`.
- `TestSSMCommandsFetcher_ParsesEnvelope` — feeds full envelope JSON, asserts `defaultCmd == "review"` (the gap: previously this would have been `""`).
- `TestSSMCommandsFetcher_Dormant` — ParameterNotFound → `("", nil)` default (dormancy invariant).
- `TestSSMCommandsFetcher_SSMError` — real SSM error propagated (not swallowed as dormant).
- `TestSSMCommandsFetcher_NoDefaultCommand` — envelope without `default_command` key → `""` default, no error.

### Key link updated

| From | To | Via | Status |
|------|----|-----|--------|
| `cfg.Github.DefaultCommand` | `h.DefaultCommand` | CommandSet SSM envelope → `commandsFetcher.Fetch()` second return | WIRED (was NOT_WIRED) |

---

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/config/config.go` | GithubCommandEntry struct, GithubConfig.Commands + DefaultCommand, GithubRepoEntry.DefaultCommand, ConfigFilePath | VERIFIED | All fields present with correct mapstructure/yaml/json tags |
| `internal/app/config/config_github_commands_test.go` | Round-trip test for Commands map | VERIFIED | TestGithubConfigCommands 3 subtests pass |
| `pkg/github/bridge/commands.go` | Pure functions: StripCode, ParseCommands, ExtractArgs, ExpandTemplate, EffectiveDefault, ResolveCommandRouting, CommandAllowed, RunCommandPass; CommandSet struct | VERIFIED | CommandSet struct added at lines 9-19; all 8 exported functions present; no `internal/app/config` import |
| `pkg/github/bridge/commands_test.go` | Table-driven tests for all pure functions | VERIFIED | 46 test cases across 7 test functions; all pass |
| `pkg/github/bridge/webhook_handler.go` | Commands/DefaultCommand/Commenter fields; dormant guard; command pass slot; lookupRepoDefaultCommand | VERIFIED | Lines 119-133 (fields); 262-309 (command pass); 508-526 (helper) |
| `pkg/github/bridge/webhook_handler_phase99_test.go` | Handler integration tests for all reply paths + dormancy | VERIFIED | 9 tests (TestHandle_CommandsDormant through TestHandle_KnownCommand_ColdPath); all pass |
| `pkg/github/bridge/interfaces.go` | CommandsFetcher.Fetch → (map, string, error) | VERIFIED | Updated signature at line 116; compile-time assertion present |
| `pkg/github/bridge/aws_adapters.go` | SSMCommandsFetcher: envelope unmarshal, cache holds defaultCommand, ParameterNotFound→empty map + "" default | VERIFIED | Lines 883-972; cache struct has `defaultCommand string` field; all four SSMCommandsFetcher tests pass |
| `pkg/github/bridge/aws_adapters_test.go` | SSMCommandsFetcher tests: ParsesEnvelope, Dormant, SSMError, NoDefaultCommand | VERIFIED | 4 new tests; all pass |
| `cmd/km-github-bridge/main.go` | Cold-start wiring: commandsFetcher.Fetch() → (commands, defaultCommand); NO KM_GITHUB_DEFAULT_COMMAND env read | VERIFIED | Lines 158-169; env read removed; defaultCommand from Fetch() second return; passed to WebhookHandler |
| `internal/app/cmd/init.go` | PublishGitHubCommandsToSSM marshals CommandSet envelope with DefaultCommand; wiring in runInit | VERIFIED | Lines 1216-1230 (envelope construction); cfg.Github.DefaultCommand in envelope |
| `internal/app/cmd/init_github_commands_test.go` | DefaultCommandRoundTrip test + envelope shape check in WritesAssembledJSON | VERIFIED | TestInitGitHubCommands_DefaultCommandRoundTrip + TestInitGitHubCommands_WritesAssembledJSON both pass |
| `internal/app/cmd/doctor.go` | checkGitHubCommandsValid (5 sub-checks) + checkGitHubCommandsSSMParam + DoctorConfigProvider extension | VERIFIED | Lines 268-277 (interface), 323-327 (adapter), 1352-1540 (check functions), 3526-3540 (registration) |
| `internal/app/cmd/doctor_github_commands_test.go` | All 5 doctor sub-checks + SSM presence + status listing tests | VERIFIED | 15 tests; all pass |
| `internal/app/cmd/github.go` | RunGitHubStatus extended with printGitHubCommandsStatus | VERIFIED | Lines 302-312; reads SSM (not cfg) for live published state |
| `docs/github-bridge.md` | Phase 99 operator section with config surface, dispatch rules, /help, doctor checks, deploy sequence | VERIFIED | Lines 835-1065 (~230 lines added); deploy sequence cross-checked against Plans 03/04 |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `km-config.yaml github.commands:` | `cfg.Github.Commands` | UnmarshalKey("github", ...) in config.Load() | WIRED | Single "github" merge-list entry at init.go:551 covers the full block including new Commands subkey |
| `cfg.Github.Commands` | SSM `{prefix}/config/github/commands` | PublishGitHubCommandsToSSM in runInit | WIRED | init.go:1195 gates on len > 0 then calls PublishGitHubCommandsToSSM |
| SSM `{prefix}/config/github/commands` | `h.Commands map[string]CommandEntry` + `h.DefaultCommand string` | SSMCommandsFetcher.Fetch at cold start | WIRED | main.go:158; envelope unmarshaled; both command map and defaultCommand returned and wired to WebhookHandler |
| `cfg.Github.DefaultCommand` | `h.DefaultCommand` | CommandSet SSM envelope → `commandsFetcher.Fetch()` second return | WIRED | Gap closed: no env var; envelope carries both fields; `TestSSMCommandsFetcher_ParsesEnvelope` + `TestInitGitHubCommands_DefaultCommandRoundTrip` prove round-trip |
| `repos[].default_command` | `lookupRepoDefaultCommand` | KM_GITHUB_REPOS JSON (RepoEntry.DefaultCommand json tag) | WIRED | bridge.RepoEntry.DefaultCommand (json:"default_command,omitempty") travels inside KM_GITHUB_REPOS; unchanged |
| `h.Commands` + `h.DefaultCommand` | RunCommandPass | dormant guard `len(h.Commands) > 0 \|\| h.DefaultCommand != ""` | WIRED | webhook_handler.go:270; strict else branch preserves Phase 98 exact path |
| CommandPassResult.Dispatch | Phase 98 warm/resume/cold dispatch | alias/profile/promptBody override before existing dispatch logic | WIRED | webhook_handler.go:297-301 sets alias, profile, promptBody; existing dispatch at 343-439 unchanged |
| CommandPassResult.Reply/Deny | GitHub issue comment POST | h.Commenter.PostComment | WIRED | webhook_handler.go:284-292; InstallationCommenter in aws_adapters.go |

---

## Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| GH-CMD-CONFIG | 99-01, 99-05 | Config struct: GithubCommandEntry, Commands map, DefaultCommand | SATISFIED | config.go:106-167; TestGithubConfigCommands passes |
| GH-CMD-FILEREF | 99-03 | @file prompt resolution relative to km-config.yaml dir | SATISFIED | ResolveCommandPrompts in init.go:1138; @@ escape; missing @file => hard error |
| GH-CMD-SSM | 99-03 | Commands published to SSM {prefix}/config/github/commands; read by bridge at cold start; envelope carries both map and default_command | SATISFIED | PublishGitHubCommandsToSSM (CommandSet envelope) + SSMCommandsFetcher; gap closure verified |
| GH-CMD-PARSE | 99-02 | Code strip, token scan, dedup, multi-error, /help intercept | SATISFIED | TestCommandParse 13 subtests; StripCode + ParseCommands |
| GH-CMD-ROUTE | 99-02 | command.alias\|\|repo.alias; command.profile\|\|repo.profile\|\|default_profile | SATISFIED | ResolveCommandRouting; TestCommandRouting 5 subtests |
| GH-CMD-AUTH | 99-02, 99-04 | Dual gate: outer repo.allow (silent drop) + inner command.allow (deny reply) | SATISFIED | CommandAllowed; TestHandle_CommandNotAuthorized |
| GH-CMD-HELP | 99-04, 99-05 | /help built-in posts command listing; reserved (cannot shadow) | SATISFIED | TestHandle_Help; checkGitHubCommandsHelpShadow |
| GH-CMD-E2E | 99-04, 99-05 | Handler wiring; dormancy byte-identity; dispatch unchanged; doctor + status; install-wide default_command delivered to Lambda | SATISFIED (automated) / MANUAL (live E2E) | All automated handler tests pass including SC3a gap-closure tests; install-wide default_command confirmed delivered via SSM envelope; live E2E is manual-only per 99-VALIDATION.md |

---

## Anti-Patterns Found

No new anti-patterns. The misleading comment from the initial verification (`main.go` line 167: "Written to Lambda env by km init") has been removed along with the `os.Getenv("KM_GITHUB_DEFAULT_COMMAND")` call.

No placeholder returns, no TODO/FIXME blockers, no stub patterns in Phase 99 production code.

---

## Human Verification Required

These items remain manual-only per 99-VALIDATION.md. They are not gaps — they are live-AWS verifications that cannot be automated with `go test`.

### 1. End-to-End Command Dispatch

**Test:** Post `@klanker-maker /review check the auth PR` on an open PR in a configured repo after `make build-lambdas && km init --dry-run=false`

**Expected:** Bridge emits 👀 ACK, agent receives expanded prompt ("Please review the PR: check the auth PR"), PR review is posted via `km-github review`

**Why human:** Requires live GitHub App webhook, real SSM params `{prefix}/config/github/{webhook-secret,bot-login,app-client-id,private-key,installation-id}`, and a running sandbox

### 2. Install-Wide Default Command (Live)

**Test:** Post `@klanker-maker please check this` (no `/verb`) on a repo configured with install-wide `github.default_command: triage`

**Expected:** Bridge dispatches using the triage command prompt template, not the raw comment body; confirms the envelope round-trip works against real SSM

**Why human:** Automated `TestSSMCommandsFetcher_ParsesEnvelope` + `TestInitGitHubCommands_DefaultCommandRoundTrip` verify the round-trip in Go; live confirmation requires real Lambda cold start reading real SSM

### 3. Per-Repo Default Command (Live)

**Test:** Post `@klanker-maker please check this` (no `/verb`) on a repo configured with `repos[].default_command: triage`

**Expected:** Bridge dispatches using the triage command prompt template

**Why human:** Per-repo path involves live KM_GITHUB_REPOS JSON in Lambda env; need real webhook delivery

### 4. km doctor with Live SSM

**Test:** Run `km doctor` after `km init --dry-run=false` with `github.commands` configured

**Expected:** All command health checks pass; `github commands SSM param` shows as present

**Why human:** SSM param present check (`checkGitHubCommandsSSMParam`) reads live AWS SSM

---

## Summary

Phase 99 goal is achieved. All 9 success criteria are verified. The SC3a gap (install-wide `github.default_command` silently dropped at Lambda runtime) is closed by the CommandSet SSM envelope approach: both the command map and `default_command` now travel together in the single SSM parameter (`{prefix}/config/github/commands`), eliminating the need for a new env var or TF change. The `KM_GITHUB_DEFAULT_COMMAND` env read is fully removed from `main.go`. Four regression-guard tests (two in `aws_adapters_test.go`, two in `init_github_commands_test.go`) lock in the fix. All Phase 97/98 invariants hold. Dormancy (absent `github.commands`) remains byte-identical to pre-Phase-99 behavior.

Live E2E remains a manual operator verification per 99-VALIDATION.md — this is the documented human-verification item, not a gap.

---

_Initial verified: 2026-06-07T23:55:00Z_
_Re-verified: 2026-06-07T20:00:00Z_
_Verifier: Claude (gsd-verifier)_
