---
phase: 99-github-bridge-commands-config-defined-commands-mapping-to-prompt-templates-plus-env-routing
plan: "04"
subsystem: github-bridge
tags: [github, bridge, lambda, command-dispatch, handler-wiring, tdd, go]

requires:
  - phase: 99-plan-02
    provides: "RunCommandPass, CommandEntry, CommandPassResult{Action,Alias,Profile,Prompt,ReplyText}, CommandAction enum (Dispatch/Reply/Deny/Passthrough)"
  - phase: 98-github-bridge-phase98
    provides: "WebhookHandler, warm/resume/cold dispatch (lines 265-388), InstallationReactor pattern"

provides:
  - "WebhookHandler.Commands (map[string]CommandEntry): command map populated at cold start from SSM"
  - "WebhookHandler.DefaultCommand (string): install-wide default command name"
  - "WebhookHandler.Commenter (CommentPoster): posts reply comments for multi-command, deny, /help"
  - "CommentPoster interface: PostComment(ctx, installID, owner, repo string, issueNumber int, body string) error"
  - "CommandsFetcher interface: Fetch(ctx) (map[string]CommandEntry, error)"
  - "SSMCommandsFetcher: 15-min cachedValue cache; ParameterNotFound → empty map (dormant, not error)"
  - "InstallationCommenter: JWT mint → installation token → POST /repos/{owner}/{repo}/issues/{number}/comments"
  - "lookupRepoDefaultCommand: helper to read RepoEntry.DefaultCommand by fullName scan for per-repo default"
  - "Dormancy invariant: len(h.Commands)==0 AND h.DefaultCommand='' → byte-identical Phase 98 behavior"

affects:
  - "99-05 (km init SSM write): sets {prefix}/config/github/commands SSM doc + KM_GITHUB_DEFAULT_COMMAND env"
  - "docs/github-bridge.md: Phase 99 operator runbook (command config, /help, auth)"

tech-stack:
  added: []
  patterns:
    - "Dormant-guarded command pass: if len(h.Commands)>0 || h.DefaultCommand!='' block; else = Phase 98 exact path"
    - "Switch on CommandAction: Reply/Deny → PostComment + return 200; Dispatch → override alias/profile/prompt; Passthrough → ExtractMentionBody"
    - "SSMCommandsFetcher mirrors SSMSecretFetcher cachedValue pattern; ParameterNotFound intercepted via errors.As"
    - "InstallationCommenter mirrors InstallationReactor.AddReaction JWT mint pattern exactly"
    - "lookupRepoDefaultCommand: minimal two-pass scan (exact then glob) mirrors Resolve() resolution order"

key-files:
  created:
    - pkg/github/bridge/webhook_handler_phase99_test.go
  modified:
    - pkg/github/bridge/webhook_handler.go
    - pkg/github/bridge/interfaces.go
    - pkg/github/bridge/aws_adapters.go
    - cmd/km-github-bridge/main.go

key-decisions:
  - "KM_GITHUB_DEFAULT_COMMAND env var for install-wide default: written by km init (Plan 03/05); bridge reads os.Getenv at cold start — no new SSM param needed for the default name"
  - "Dormant path uses strict else branch (not just len check): guarantees zero commenter calls and zero command-pass allocations when unconfigured — the byte-identity invariant is structural"
  - "InstallationCommenter requests issues:write + pull_requests:write (same as reactor): PR comment creation requires pull_requests:write in some GitHub App permission models"
  - "lookupRepoDefaultCommand placed in webhook_handler.go (not resolve.go): avoids widening Resolve() return signature; minimal scan is acceptable (entries list is small)"
  - "path import added to webhook_handler.go for glob matching in lookupRepoDefaultCommand; isGlob() from resolve.go is in same package, so reused without duplication"

requirements-completed: [GH-CMD-AUTH, GH-CMD-HELP, GH-CMD-E2E]

duration: 323s
completed: 2026-06-07
---

# Phase 99 Plan 04: Handler Wiring + Command Pass Integration Summary

**Command pass wired into WebhookHandler: dormant-guarded RunCommandPass slot between dedupe gate and envelope construction; SSMCommandsFetcher (15-min cache, ParameterNotFound → empty map); InstallationCommenter (JWT mint → issues/comments POST); three reply paths (multi-command, deny, /help) and the dormancy byte-identity invariant proven by TestHandle_CommandsDormant**

## Performance

- **Duration:** 323s (~5 min)
- **Started:** 2026-06-07T23:06:50Z
- **Completed:** 2026-06-07T23:12:13Z
- **Tasks:** 3 (TDD RED test file → interface+adapter additions → handler wiring GREEN)
- **Files modified:** 5

## Accomplishments

- `WebhookHandler` gains three Phase 99 fields: `Commands map[string]CommandEntry`, `DefaultCommand string`, `Commenter CommentPoster`
- Command pass slotted at the exact insertion point (between dedupe gate line ~241 and envelope construction line ~244) with a strict dormant guard — when `len(h.Commands)==0 && h.DefaultCommand==""` the else branch runs `ExtractMentionBody` directly, byte-identical to Phase 98
- `CommentPoster` and `CommandsFetcher` interfaces added to `interfaces.go` with compile-time assertions
- `SSMCommandsFetcher` mirrors `SSMSecretFetcher` cachedValue pattern exactly; `ParameterNotFound` intercepted via `errors.As(&ssmtypes.ParameterNotFound{})` — returns empty map (not nil, not error)
- `InstallationCommenter` mirrors `InstallationReactor.AddReaction` JWT mint pattern; POSTs to `/repos/{owner}/{repo}/issues/{number}/comments`
- `main.go` cold-start wires `SSMCommandsFetcher` + eager `Fetch` + `InstallationCommenter` + `KM_GITHUB_DEFAULT_COMMAND` env → `WebhookHandler` new fields
- All 9 Phase 99 handler tests GREEN: `TestHandle_CommandsDormant`, `CommandsDormant_EmptyMap`, `MultiCommand`, `CommandNotAuthorized`, `Help`, `UnknownToken`, `DefaultCommand`, `KnownCommand_HappyPath`, `KnownCommand_ColdPath`
- Full Phase 97/98 test suite unaffected (0 regressions)

## Task Commits

Each task was committed atomically:

1. **Task 1: TDD RED — failing handler tests** - `0919a0a6` (test)
2. **Task 2: Interfaces + adapters (CommentPoster, SSMCommandsFetcher, InstallationCommenter)** - `5835e980` (feat)
3. **Task 3: Handle() command pass + cold-start wiring (GREEN)** - `b0cbdf1b` (feat)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/pkg/github/bridge/webhook_handler_phase99_test.go` — 9 handler tests with mockCommenter; tests all reply paths + dormancy invariant
- `/Users/khundeck/working/klankrmkr/pkg/github/bridge/webhook_handler.go` — Commands/DefaultCommand/Commenter fields; dormant-guarded command pass; lookupRepoDefaultCommand helper; path import
- `/Users/khundeck/working/klankrmkr/pkg/github/bridge/interfaces.go` — CommentPoster + CommandsFetcher interfaces
- `/Users/khundeck/working/klankrmkr/pkg/github/bridge/aws_adapters.go` — SSMCommandsFetcher (15-min cache); InstallationCommenter (JWT→token→POST); ssmtypes import
- `/Users/khundeck/working/klankrmkr/cmd/km-github-bridge/main.go` — SSMCommandsFetcher cold-start wiring; eager Fetch; InstallationCommenter; KM_GITHUB_DEFAULT_COMMAND env; WebhookHandler new fields

## Decisions Made

- **KM_GITHUB_DEFAULT_COMMAND env for install-wide default**: The bridge reads `os.Getenv("KM_GITHUB_DEFAULT_COMMAND")` at cold start. Plan 03/05 writes this to the Lambda env block at `km init` time. This keeps the SSM doc (`{prefix}/config/github/commands`) as command-only JSON; the default name is a short string safe for Lambda env.
- **Strict `else` branch for dormancy**: Rather than checking `len(h.Commands)==0` in the switch, the outer `if`/`else` ensures the dormant path cannot accidentally invoke the commenter or touch command state. The structural separation is the byte-identity guarantee.
- **`lookupRepoDefaultCommand` in webhook_handler.go**: Avoids widening `Resolve()` return to 5 values. The scan is O(n) over a small entries list (same as Resolve itself). Placed in webhook_handler.go not resolve.go to keep resolve.go focused on pure resolution.

## Deviations from Plan

None — plan executed exactly as written.

## User Setup Required

None for this plan. Operator-side SSM write + `KM_GITHUB_DEFAULT_COMMAND` Lambda env wiring is done by Plan 03 (`km init` plumbing). The bridge Lambda reads the SSM doc at cold start; when the param is absent, it runs dormant (Phase 98 behavior).

## Next Phase Readiness

- Plan 03 (`km init` SSM write): must write `{prefix}/config/github/commands` JSON and set `KM_GITHUB_DEFAULT_COMMAND` Lambda env for the bridge to use live commands. The bridge already reads them.
- Deploy: `make build-lambdas` + `km init --dry-run=false` (NOT `--sidecars` — Lambda env block change requires full terragrunt apply). No new Terraform modules, no sandbox recreate.
- `go build ./...` is clean; `go test ./pkg/github/bridge/... -count=1` is green (all Phase 97/98/99 tests pass).

## Self-Check

- `pkg/github/bridge/webhook_handler_phase99_test.go` — FOUND
- `pkg/github/bridge/webhook_handler.go` — FOUND (Commands/DefaultCommand/Commenter fields present)
- `pkg/github/bridge/interfaces.go` — FOUND (CommentPoster + CommandsFetcher present)
- `pkg/github/bridge/aws_adapters.go` — FOUND (SSMCommandsFetcher + InstallationCommenter present)
- `cmd/km-github-bridge/main.go` — FOUND (SSMCommandsFetcher + commenter wiring present)
- RED commit `0919a0a6` — FOUND
- Interfaces+adapters commit `5835e980` — FOUND
- GREEN commit `b0cbdf1b` — FOUND

## Self-Check: PASSED

---
*Phase: 99-github-bridge-commands-config-defined-commands-mapping-to-prompt-templates-plus-env-routing*
*Completed: 2026-06-07*
