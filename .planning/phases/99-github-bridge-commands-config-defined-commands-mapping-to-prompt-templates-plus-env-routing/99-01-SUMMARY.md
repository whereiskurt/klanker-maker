---
phase: 99-github-bridge-commands-config-defined-commands-mapping-to-prompt-templates-plus-env-routing
plan: "01"
subsystem: config
tags: [github, config, viper, mapstructure, tdd]

requires:
  - phase: 97-github-comment-trigger-bridge
    provides: GithubRepoEntry + GithubConfig + single "github" merge-list entry + UnmarshalKey("github", ...)
  - phase: 99-github-bridge-commands (RESEARCH.md)
    provides: finding that single "github" merge-list entry covers whole block via UnmarshalKey

provides:
  - GithubCommandEntry struct with Description/Alias/Profile/Allow/Prompt + mapstructure tags
  - GithubConfig.Commands map[string]GithubCommandEntry
  - GithubConfig.DefaultCommand (install-wide fallback)
  - GithubRepoEntry.DefaultCommand (per-repo fallback override)
  - Round-trip regression test (TestGithubConfigCommands) proving dormancy + full decode

affects:
  - 99-02 (km init env export — reads cfg.Github.Commands to set KM_GITHUB_COMMANDS)
  - 99-03 (km github status — prints cfg.Github.Commands table)
  - 99-04 (bridge dispatch — decodes GithubCommandEntry from env to build prompt)

tech-stack:
  added: []
  patterns:
    - "Config struct extension via mapstructure tags only — no new merge-list entry when parent key already in list"
    - "TDD RED (compile failure on missing fields) → GREEN (struct + fields added)"

key-files:
  created:
    - internal/app/config/config_github_commands_test.go
  modified:
    - internal/app/config/config.go

key-decisions:
  - "No new merge-list entry for github.commands or github.default_command — the single 'github' entry covers the whole block via UnmarshalKey; adding a sibling entry would be a no-op or break parsing (documented in GithubCommandEntry godoc)"
  - "GithubCommandEntry is a new named type (not anonymous struct) to allow downstream Plans 02-04 to reference it by name in function signatures and JSON marshal"
  - "Commands field is map[string]GithubCommandEntry (keyed by verb) not a slice — O(1) lookup in bridge dispatch, clean YAML syntax, matches operator mental model"

patterns-established:
  - "GithubCommandEntry mapstructure tags: all five fields tagged; Prompt uses bare yaml tag (required field)"
  - "Dormancy via nil/empty map: absent github.commands => Commands is nil, no panic in downstream callers"

requirements-completed: [GH-CMD-CONFIG]

duration: 2min
completed: 2026-06-07
---

# Phase 99 Plan 01: GitHub Commands Config Layer Summary

**GithubCommandEntry struct + Commands/DefaultCommand config fields with TDD round-trip proof, enabling operator-declared @bot commands in km-config.yaml**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-06-07T22:58:27Z
- **Completed:** 2026-06-07T23:00:20Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- Added `GithubCommandEntry` struct with five fields (Description, Alias, Profile, Allow, Prompt) and mandatory mapstructure tags
- Extended `GithubConfig` with `Commands map[string]GithubCommandEntry` and `DefaultCommand string`
- Extended `GithubRepoEntry` with `DefaultCommand string` for per-repo command override
- Wrote and passed `TestGithubConfigCommands` with three sub-tests (full round-trip, dormant commands, absent github block)
- Documented the merge-list non-entry decision inline in godoc to prevent future "fix" regressions

## Task Commits

Each task was committed atomically:

1. **Task 1: Write failing config round-trip test (RED)** - `798c5056` (test)
2. **Task 2: Add GithubCommandEntry + command fields + getters (GREEN)** - `4539ace4` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified

- `internal/app/config/config_github_commands_test.go` - TestGithubConfigCommands (3 sub-tests) + TestGithubCommandEntryFields compile-time anchor
- `internal/app/config/config.go` - GithubCommandEntry struct, GithubRepoEntry.DefaultCommand, GithubConfig.Commands + DefaultCommand

## Decisions Made

- **No new merge-list entry:** The single `"github"` entry at config.go ~line 484 already causes `UnmarshalKey("github", &cfg.Github)` to decode the complete github: block including the new Commands map. Adding `"github.commands"` would be a no-op or parse-order hazard. Documented in `GithubCommandEntry` godoc.
- **map[string]GithubCommandEntry not []struct:** Map keyed by verb gives O(1) dispatch lookup in the bridge Lambda, cleaner YAML (`commands: review: ...`), and no redundant name field. Consistent with how operators think about "the review command."
- **Named type for GithubCommandEntry:** Makes Plans 02-04 cleaner (function signatures, JSON marshal helpers).

## Deviations from Plan

None — plan executed exactly as written. The merge-list non-edit was explicit in the plan; the three struct edits (GithubCommandEntry, GithubRepoEntry.DefaultCommand, GithubConfig fields) are exactly what was specified.

## Issues Encountered

None.

## User Setup Required

None — config-layer only, no external service configuration required.

## Next Phase Readiness

- `GithubCommandEntry`, `GithubConfig.Commands`, and `GithubConfig.DefaultCommand` are ready for Plan 02 (km init env export: `KM_GITHUB_COMMANDS`, `KM_GITHUB_DEFAULT_COMMAND`)
- `GithubRepoEntry.DefaultCommand` is ready for per-repo dispatch routing in Plan 04 (bridge dispatch)
- All existing config tests pass; build is clean

---
*Phase: 99-github-bridge-commands-config-defined-commands-mapping-to-prompt-templates-plus-env-routing*
*Completed: 2026-06-07*
