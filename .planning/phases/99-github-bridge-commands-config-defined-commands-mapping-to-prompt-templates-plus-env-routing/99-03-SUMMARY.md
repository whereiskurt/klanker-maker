---
phase: 99-github-bridge-commands-config-defined-commands-mapping-to-prompt-templates-plus-env-routing
plan: "03"
subsystem: km-init-github-commands
tags: [github, km-init, ssm, @file-resolution, tdd, operator-side]

requires:
  - phase: 99-plan-01
    provides: GithubCommandEntry struct + GithubConfig.Commands + GithubConfig.DefaultCommand + GithubRepoEntry.DefaultCommand

provides:
  - ResolveCommandPrompts(commands, configDir) — @file/@@ resolution relative to km-config.yaml dir
  - PublishGitHubCommandsToSSM(ctx, ssmClient, cfg, stderr) — assembles + publishes {prefix}/config/github/commands
  - Config.ConfigFilePath field — v2.ConfigFileUsed() surfaced from Load() for @file base-dir derivation
  - init_github_commands_test.go — 9 tests covering all resolution cases + SSM write/dormancy/drift

affects:
  - 99-04 (bridge cold-start reads {prefix}/config/github/commands — operator-side half complete)
  - km-github-bridge Lambda (receives assembled JSON at cold start via SSMCommandsFetcher)

tech-stack:
  added: []
  patterns:
    - "@file resolution relative to km-config.yaml dir (NOT CWD) — Pitfall 6 explicitly handled"
    - "SSMReadWriteAPI for drift-check GetParameter + PutParameter in single function"
    - "Dormancy gate: len(commands)==0 → no SSM write (byte-identical to pre-99)"
    - "ConfigFilePath threaded through config.Load() via v2.ConfigFileUsed()"

key-files:
  created:
    - internal/app/cmd/init_github_commands_test.go
  modified:
    - internal/app/cmd/init.go
    - internal/app/config/config.go

key-decisions:
  - "Open-Q-1 resolved: add Config.ConfigFilePath populated from v2.ConfigFileUsed() in Load(). This is the cleanest approach — no viper handle threading, no global, just a string field on the struct. Downstream callers use filepath.Dir(cfg.ConfigFilePath)."
  - "PublishGitHubCommandsToSSM is exported (not unexported + wrapper) for direct injection in cmd_test — mirrors the PreStageGitHubProfiles pattern in the same package."
  - "SSMReadWriteAPI (already defined in configure_github.go) reused for the drift GetParameter + PutParameter — no new interface needed."
  - "Drift WARN writes the yaml-derived value unconditionally (SSM has no 'env' path; yaml always wins). Unlike KM_GITHUB_REPOS which preserves env-wins, SSM commands have no operator export mechanism so the new value should always be written."
  - "runInit wires PublishGitHubCommandsToSSM inside a len(commands)>0 gate so the SSM client is not even constructed for dormant installs."

patterns-established:
  - "@file base dir = filepath.Dir(cfg.ConfigFilePath); fallback to '.' when ConfigFilePath is empty"
  - "fakeCommandsSSM in test: pre-seeded map for GetParameter + recorded slice for PutParameter assertions"

requirements-completed: [GH-CMD-FILEREF, GH-CMD-SSM]

duration: 11min
completed: 2026-06-07
---

# Phase 99 Plan 03: GitHub Commands @file Resolution + SSM Publication Summary

**km init resolves @file command prompt templates relative to km-config.yaml and publishes the assembled command map to SSM {prefix}/config/github/commands as a plain String, gated on dormancy and with drift WARN.**

## Performance

- **Duration:** ~11 min
- **Started:** 2026-06-07T23:07:51Z
- **Completed:** 2026-06-07T23:18:34Z
- **Tasks:** 2 (RED → GREEN TDD)
- **Files modified:** 3

## Accomplishments

- Added `Config.ConfigFilePath string` to `config.go`, populated from `v2.ConfigFileUsed()` after `ReadInConfig` succeeds — resolves Open-Q-1 cleanly without threading a viper handle
- Implemented `ResolveCommandPrompts(commands map[string]config.GithubCommandEntry, configDir string)` in `init.go` — mirrors `resolvePrompts()` in `create_prompt.go` but resolves relative to `configDir` (NOT `os.Getwd()`)
- Implemented `PublishGitHubCommandsToSSM(ctx, ssmClient SSMReadWriteAPI, cfg, stderr)` — resolves @file, marshals JSON, checks current SSM value for drift WARN, writes as `ParameterTypeString` with `Overwrite=true`
- Wired `PublishGitHubCommandsToSSM` into `runInit()` after `EnsureSlackBotUserIDFromSSM`; hard-fails on @file resolution or SSM write errors
- Wrote 9 tests: 5 for `ResolveCommandPrompts` (inline/@@/config-dir-relative/@file-read/missing-error/CWD-isolation) + 4 for `PublishGitHubCommandsToSSM` (write+inline/dormancy/drift-WARN/idempotent)

## Task Commits

1. **Task 1: Write failing tests (RED)** — `85cd3463`
2. **Task 2: Implement @file resolution + SSM publication (GREEN)** — `62363afc`

## Files Created/Modified

- `internal/app/cmd/init_github_commands_test.go` — 9 tests covering all @file cases + SSM write/dormancy/drift
- `internal/app/cmd/init.go` — ResolveCommandPrompts + PublishGitHubCommandsToSSM + runInit wiring
- `internal/app/config/config.go` — ConfigFilePath field + Load() population from v2.ConfigFileUsed()

## Decisions Made

- **Open-Q-1 (config dir for @file):** Added `ConfigFilePath string` to `Config` struct, set from `v2.ConfigFileUsed()` inside `Load()` after `ReadInConfig` succeeds. Callers derive config dir via `filepath.Dir(cfg.ConfigFilePath)`. When `ConfigFilePath` is empty (km-config.yaml absent, Lambda env), falls back to `"."` — acceptable because @file prompts require an operator-side km-config.yaml and the Lambda never calls this function.

- **Exported functions vs unexported+wrapper:** `ResolveCommandPrompts` and `PublishGitHubCommandsToSSM` are exported directly (not via thin exported wrappers) — cleaner than the `resolvePrompts` / `ResolvePrompts` two-function pattern because the cmd_test package can inject a fake SSMReadWriteAPI directly.

- **Drift WARN semantics for SSM vs env vars:** For env vars (KM_GITHUB_REPOS), env wins — the yaml value is rejected when the env var is set. For SSM commands, there is no operator "export" path, so the yaml-derived value always wins and the WARN is purely informational. Both log a WARN on divergence.

## Deviations from Plan

**One minor Rule 2 addition (missing critical field, not a plan deviation):**

**[Rule 2 - Missing Critical Functionality] Added ConfigFilePath to Config struct**
- **Found during:** Task 2 implementation (resolving Open-Q-1)
- **Issue:** `v2.ConfigFileUsed()` is local to `config.Load()` and not accessible from `init.go`. Without surfacing this, @file paths must use `os.Getwd()` (Research Pitfall 6).
- **Fix:** Added `ConfigFilePath string` to `Config` struct, assigned from `v2.ConfigFileUsed()` in `Load()` after successful `ReadInConfig`.
- **Files modified:** `internal/app/config/config.go`
- **Impact:** Non-breaking; zero-value (empty string) means "use CWD as fallback" — harmless for existing callers.
- **Commit:** `85cd3463` (config.go hunk in the RED test commit)

## Issues Encountered

- Pre-existing `TestRunAgentAuthClaude_TeesAndCleans` times out at 120s — requires a real claude CLI binary and is unrelated to Phase 99 changes. Verified via `git log` that this test predates Phase 99.

## User Setup Required

None — operator-side `km init` code only. No new Lambda deployment step for this plan; Plan 04 will wire the bridge cold-start reader.

## Next Phase Readiness

- `PublishGitHubCommandsToSSM` is complete — SSM `{prefix}/config/github/commands` is populated by `km init` whenever `github.commands` is configured
- Plan 04 (bridge cold-start SSMCommandsFetcher) can now read this param; the JSON shape is `map[string]config.GithubCommandEntry` (description/alias/profile/allow/prompt)
- `ResolveCommandPrompts` is exported — Plan 04 tests can import it for integration scenarios if needed

## Self-Check: PASSED

- FOUND: internal/app/cmd/init_github_commands_test.go
- FOUND: internal/app/cmd/init.go (ResolveCommandPrompts + PublishGitHubCommandsToSSM)
- FOUND: internal/app/config/config.go (ConfigFilePath field)
- FOUND: .planning/phases/.../99-03-SUMMARY.md
- FOUND: commit 85cd3463 (RED test)
- FOUND: commit 62363afc (GREEN impl)

---
*Phase: 99-github-bridge-commands-config-defined-commands-mapping-to-prompt-templates-plus-env-routing*
*Completed: 2026-06-07*
