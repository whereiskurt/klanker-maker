---
phase: 103-hackerone-comment-trigger-bridge
plan: 02
subsystem: infra
tags: [hackerone, config, bridge, routing, viper, go]

# Dependency graph
requires:
  - phase: 103-01
    provides: pinned HackerOne webhook field paths + synthetic webhook bodies
provides:
  - "h1: config surface (H1Config/H1ProgramEntry/H1Target/H1EventEntry/H1CommandEntry) with merge-list wiring"
  - "GetH1Config/GetH1BotHandle/GetH1DefaultProfile/GetH1Programs/GetH1ProgramBotHandle getters"
  - "pkg/h1/bridge.Resolve(handle) -> ([]Target, allow, events, commands, ok) multi-target routing"
  - "pkg/h1/bridge.ContainsHandle / ExtractBody literal-handle comment matching"
  - "pkg/h1/bridge forked interfaces (H1ThreadStore keyed reportID+target, H1Commenter with internal bool)"
affects: [103-04, 103-06, 103-07, 103-08]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Multi-target fanout: Resolve returns []Target (N sandboxes) vs GitHub's single alias"
    - "Literal-handle comment trigger (no bot @-mention; HackerOne has no bot user)"
    - "Forked interfaces drop federation/reactor/bot-login; add internal-by-default reply gate"

key-files:
  created:
    - internal/app/config/config_h1_test.go
    - pkg/h1/bridge/resolve.go
    - pkg/h1/bridge/resolve_test.go
    - pkg/h1/bridge/interfaces.go
  modified:
    - internal/app/config/config.go

key-decisions:
  - "CommandEntry declared once in commands.go (sibling Plan 103-03/05); resolve.go references it — converged shared-package ownership"
  - "H1ThreadStore keyed by (reportID, target) so multi-target fanout rows do not collide"
  - "H1Commenter carries internal bool; internal-by-default is the documented contract at the interface layer"

patterns-established:
  - "Pattern: h1: block decoded atomically via single merge-list entry + UnmarshalKey (github precedent; closes project_config_key_merge_list footgun)"
  - "Pattern: bridge-local ProgramEntry/Target decouple pkg/h1/bridge from internal/app/config"

requirements-completed: [H1-RESOLVE-PROGRAM, H1-EVENT-PROMPT-MAP, H1-DEPLOY-WIRING]

# Metrics
duration: 8min
completed: 2026-06-10
---

# Phase 103 Plan 02: H1 routing foundation (config + resolve) Summary

**The `h1:` config surface (programs/targets/allow/events/commands, merge-list-wired so it is not silently dropped) plus `pkg/h1/bridge.Resolve(handle)` that maps a report's program handle to its multi-target dispatch config, and the forked bridge interfaces (federation/reactor/bot-login dropped; H1ThreadStore + internal-by-default H1Commenter added).**

## Performance

- **Duration:** ~8 min
- **Started:** 2026-06-10T04:00:45Z
- **Completed:** 2026-06-10T04:08:00Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments
- `H1Config` and its sub-structs round-trip from `km-config.yaml` into `cfg.H1`, with the load-bearing `"h1"` merge-list entry + `v.UnmarshalKey("h1", &cfg.H1)` proven by a dedicated merge-list regression test (the `project_config_key_merge_list` footgun is closed).
- `Resolve(handle)` returns `([]Target, allow, events, commands, ok)` — multi-target fanout (N targets per program, vs GitHub's single alias), with per-target alias defaulting to `h1-{handle}` and profile defaulting to `defaultProfile`; unknown handle → `ok=false` (handler 200-drops); empty events map → auto-triage dormant.
- `ContainsHandle` / `ExtractBody` match the configured handle as a literal substring (HackerOne has no bot user to @-mention) with per-program handle override respected.
- `interfaces.go` forked from `pkg/github/bridge`: dropped `PeerRelayer` / `BotLoginFetcher` / `GitHubReactor`; forked `GitHubThreadStore` → `H1ThreadStore` (keyed by `reportID`+`target`) and `CommentPoster` → `H1Commenter` (adds the safety-critical `internal bool`); all thread/status writes documented as UpdateItem-shaped.

## Task Commits

Each task was committed atomically (TDD: test → feat):

1. **Task 1: H1 config structs + getters + merge-list wiring**
   - `ffaece5f` (test) — failing H1 config round-trip + merge-list regression
   - `c88b7934` (feat) — H1Config structs, Config.H1 field, merge-list entry, UnmarshalKey, getters
2. **Task 2: pkg/h1/bridge resolve + interfaces**
   - `d62b5fe1` (test) — failing resolve / ContainsHandle / ExtractBody tests
   - `ff2f6045` (feat) — resolve.go (multi-target Resolve) + forked interfaces.go
   - `17217e86` (fix) — CommandEntry ownership (shared-package coordination)
   - `f2b47871` (fix) — converged: CommandEntry single-owner (commands.go), resolve.go references it

_Note: TDD tasks have multiple commits (test → feat); Task 2 carries two coordination fix commits resolving a live shared-package symbol race with sibling plans 103-03/05._

## Files Created/Modified
- `internal/app/config/config.go` — H1Config / H1ProgramEntry / H1Target / H1EventEntry / H1CommandEntry structs; `H1` field on Config; `"h1"` merge-list entry; `UnmarshalKey("h1", …)`; five `GetH1*` getters.
- `internal/app/config/config_h1_test.go` — round-trip, absent-dormant, merge-list regression, bot_handle override, getters.
- `pkg/h1/bridge/resolve.go` — `ProgramEntry`/`Target`/`EventEntry` types; `Resolve` (multi-target); `defaultAlias`; `ContainsHandle`; `ExtractBody`.
- `pkg/h1/bridge/resolve_test.go` — Resolve (known/miss/dormant/alias-default/profile-default), ContainsHandle, ExtractBody.
- `pkg/h1/bridge/interfaces.go` — forked interfaces (KEEP/DROP/FORK per plan).

## Decisions Made
- **CommandEntry single-owner (converged).** `CommandEntry` is part of `ProgramEntry`'s shape but is also heavily consumed by the sibling's command engine (`commands.go`, Plan 103-03/05). The two parallel agents briefly raced on which file declares it. Converged state: `CommandEntry` is declared **once in `commands.go`**, and `resolve.go` references it via `ProgramEntry.Commands` and the `Resolve` return — exactly one declaration in the shared `bridge` package, no duplicate-symbol collision.
- **H1ThreadStore keyed by (reportID, target).** Multi-target fanout dispatches N targets on the same report; each needs its own continuity row to avoid collision (per 103-CONTEXT).
- **internal-by-default at the interface layer.** `H1Commenter.PostComment(..., internal bool)` documents that the public path must be explicit and must never default an absent flag to public.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Shared-package CommandEntry symbol race with sibling plans**
- **Found during:** Task 2 (pkg/h1/bridge resolve + interfaces)
- **Issue:** Running in parallel with plans 103-03/05, the sibling-owned `commands.go` (untracked, mid-edit) and my `resolve.go` both needed the `CommandEntry` type. The declaration flip-flopped between the two files in real time as both agents reconciled, repeatedly breaking the package build with `CommandEntry redeclared`.
- **Fix:** Converged on a single owner — `CommandEntry` declared once in `commands.go` (the command-parsing domain), `resolve.go` references it. My plan's artifact spec lists `ProgramEntry/Resolve/ContainsHandle/ExtractBody` (not `CommandEntry`) as resolve.go's provided symbols, so deferring the concrete type to the command engine is consistent with stated ownership.
- **Files modified:** `pkg/h1/bridge/resolve.go`
- **Verification:** `grep '^type CommandEntry struct'` returns exactly one match (commands.go); both plan verification commands green.
- **Committed in:** `17217e86`, `f2b47871`

---

**Total deviations:** 1 auto-fixed (1 blocking — shared-package coordination).
**Impact on plan:** No scope change. The coordination fix was required for the shared `bridge` package to compile; both my files and the sibling's converge cleanly. No production HackerOne program referenced.

## Issues Encountered
- A sibling plan had already created `pkg/h1/bridge/payload.go` (committed) and `payload_test.go`/`commands_test.go`/`commands.go` (untracked, mid-edit) in the shared worktree. I stayed strictly within my files (`config.go`, `config_h1_test.go`, `resolve.go`, `resolve_test.go`, `interfaces.go`) and never touched payload.go/commands.go/cmd/km-h1/webhook_handler.go. The only cross-file interaction was the `CommandEntry` symbol ownership, resolved above.

## User Setup Required
None - no external service configuration required (config surface + pure routing functions only; no deploy wiring executed in this plan).

## Next Phase Readiness
- Routing foundation ready: handlers (Plan 04) can call `Resolve(programHandle)` for multi-target dispatch and read the events→prompt / commands→prompt maps.
- Config getters ready for `km h1 init`/`status` (Plan 06/07) and the Lambda env wiring (Plan 07/08).
- Forked interfaces ready to back AWS adapters (Plan 04) — H1ThreadStore + H1Commenter contracts fixed.
- Note: full `go test ./pkg/h1/bridge` for the whole package depends on the sibling plans' `commands.go`/`payload.go` being committed; the two plan-scoped verification commands for 103-02 are green.

## Self-Check: PASSED

- All 4 created files present on disk (config_h1_test.go, resolve.go, resolve_test.go, interfaces.go).
- All 6 task commits found in git history (ffaece5f, c88b7934, d62b5fe1, ff2f6045, 17217e86, f2b47871).
- Both plan verification commands green:
  - `go test ./internal/app/config -run H1 -count=1` → ok
  - `go test ./pkg/h1/bridge -run "TestResolve|TestContainsHandle|TestExtractBody" -count=1` → ok

---
*Phase: 103-hackerone-comment-trigger-bridge*
*Completed: 2026-06-10*
