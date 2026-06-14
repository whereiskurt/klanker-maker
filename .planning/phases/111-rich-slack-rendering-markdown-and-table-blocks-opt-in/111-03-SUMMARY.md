---
phase: 111-rich-slack-rendering-markdown-and-table-blocks-opt-in
plan: 03
subsystem: slack
tags: [slack, block-kit, km-slack, blocks-rich, render-mode, ai-footer, tdd]

# Dependency graph
requires:
  - phase: 111-01
    provides: RenderRich(input string, aiFooter bool) (blocksJSON, fallbackText string, ok bool)
  - phase: 111-02
    provides: buildTableBlock wired into renderRich — table segments produce table blocks
provides:
  - "cmd/km-slack/main.go: blocks-rich in runPost switch (line ~126) + runReply switch (line ~692)"
  - "cmd/km-slack/main.go runWith blocks-rich case: slack.RenderRich dispatch + RenderBlocks + Mrkdwnify fallback chain"
  - "cmd/km-slack/main.go: KM_SLACK_AI_FOOTER env read in runWith blocks-rich case (os.Getenv, sandbox-side only)"
  - "cmd/km-slack/main_rich_test.go: RICH-14/15/16 integration tests (5 test functions, 7 sub-tests)"
affects:
  - "111-04 (corpus+docs): blocks-rich now reachable end-to-end from the sidecar; ready for docs + plugin bump"
  - "112 (flip default): blocks-rich opt-in soak period complete; Phase 112 will flip default to blocks-rich"

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Pitfall-6 guard: BOTH runPost and runReply switches updated — never update one without the other"
    - "KM_SLACK_AI_FOOTER read sandbox-side in runWith via os.Getenv — NOT compiler-emitted (RICH-20 protected)"
    - "blocks-rich dispatch case sits BEFORE blocks case in the renderMode switch (order matters for readability)"
    - "Fallback chain: RenderRich ok=false → RenderBlocks ok=false → Mrkdwnify (same Tier-3→Tier-2→Tier-1 degradation)"

key-files:
  created:
    - cmd/km-slack/main_rich_test.go
  modified:
    - cmd/km-slack/main.go

key-decisions:
  - "KM_SLACK_AI_FOOTER is sandbox-side only — operator sets it in /etc/km/notify.env; compiler does NOT emit it (Pitfall-7 / RICH-20 protected)"
  - "blocks-rich case added BEFORE the blocks case in runWith to keep Tier-3 logically distinct from Tier-2"
  - "Full fallback chain implemented: RenderRich→RenderBlocks→Mrkdwnify — no partial fallback (if RenderRich fails, entire message falls back to Tier-2, not partial Tier-3)"

requirements-completed: [RICH-14, RICH-15, RICH-16]

# Metrics
duration: 2min
completed: 2026-06-14
---

# Phase 111 Plan 03: km-slack blocks-rich Wiring Summary

**blocks-rich wired into km-slack runPost + runReply validation switches and runWith dispatch: RenderRich with KM_SLACK_AI_FOOTER opt-in, RenderRich→RenderBlocks→Mrkdwnify fallback chain, RICH-14/15/16 tests all green**

## Performance

- **Duration:** ~2 min
- **Started:** 2026-06-14T15:41:10Z
- **Completed:** 2026-06-14T15:43:33Z
- **Tasks:** 1
- **Files modified:** 2

## Accomplishments

- `cmd/km-slack/main.go` runPost switch (~line 126): `"blocks-rich"` added to valid set; `--render` flag help string updated to mention Phase 111 Tier-3.
- `cmd/km-slack/main.go` runReply switch (~line 692): `"blocks-rich"` added to valid set (Pitfall-6 guard — both switches now in sync).
- `cmd/km-slack/main.go` runWith (~line 365): new `case "blocks-rich":` added BEFORE the existing `"blocks"` case. Reads `KM_SLACK_AI_FOOTER` via `os.Getenv` (sandbox-side, NOT compiler-emitted). Calls `slack.RenderRich(string(body), aiFooter)`. On `ok=false`: falls back to `slack.RenderBlocks` → on its `ok=false`: falls back to `slack.Mrkdwnify`. The existing `"blocks"` case and downstream `env.Blocks` population at line ~397 are unchanged.
- `cmd/km-slack/main_rich_test.go` (429 lines added): 5 test functions covering RICH-14/15/16:
  - `TestRunPost_BlocksRich` — blocks-rich accepted by runPost, no error
  - `TestRunPost_BlocksRich_SwitchAccepted` — envelope Body differs from raw input (mode not stripped); Blocks field non-empty
  - `TestRunReply_BlocksRich` — Pitfall-6 guard: runReply switch also accepts blocks-rich
  - `TestRunWith_BlocksRich` — prose+table body produces non-empty Blocks JSON with header block and table block
  - `TestRunWith_FallbackChain` — 13K prose input trips 12K cap, RenderRich ok=false, chain completes without error
  - `TestRunWith_AIFooter` (3 sub-tests) — `true`: last block is context with AI/verify text; `false`/unset: no disclaimer block

## Task Commits

1. **Task 1: blocks-rich wiring + RICH-14/15/16 tests** — `c164c023` (feat)

## Files Created/Modified

- `/Users/khundeck/working/klankrmkr/cmd/km-slack/main.go` — runPost switch + runReply switch + runWith blocks-rich case + KM_SLACK_AI_FOOTER reading
- `/Users/khundeck/working/klankrmkr/cmd/km-slack/main_rich_test.go` — RICH-14/15/16 integration tests (5 functions, 7 sub-tests)

## Decisions Made

- `KM_SLACK_AI_FOOTER` is read sandbox-side in `runWith` via `os.Getenv` — NOT compiler-emitted. The compiler (pkg/compiler/userdata.go) does not emit this env var, so compiler golden tests (TestUserdataAdditionalVolumeOnly_Golden, TestH1ByteIdentity) are unaffected (RICH-20 protected).
- `blocks-rich` dispatch case placed BEFORE `blocks` in the runWith switch for logical Tier-3-before-Tier-2 ordering. The `default` case remains the same ("plain" and any unknown values already normalised before reaching runWith).
- Full fallback chain: if `RenderRich` returns `ok=false` (12K cap, 50-block cap, panic), the entire message is re-rendered via `RenderBlocks` (Tier-2), then `Mrkdwnify` (Tier-1). No partial Tier-3 output is emitted on failure.

## Deviations from Plan

None - plan executed exactly as written. The TDD RED/GREEN cycle was clean: tests failed (blocks-rich normalised to plain), then all passed immediately after the two switch edits + the runWith case addition. No auto-fixes required.

## Verification

- `go test ./cmd/km-slack/... -run 'TestRunPost_BlocksRich|TestRunReply_BlocksRich|TestRunWith_BlocksRich|TestRunWith_FallbackChain|TestRunWith_AIFooter' -count=1 -timeout 120s`: PASS
- `go test ./cmd/km-slack/... -count=1 -timeout 120s`: PASS (full suite including existing dispatch/reply tests)
- `go test ./pkg/slack/... -run TestRich -count=1 -timeout 60s`: PASS (Plan 01/02 regressions clean)
- `go test ./pkg/compiler/... -run 'TestUserdataAdditionalVolumeOnly_Golden|TestH1ByteIdentity' -count=1 -timeout 120s`: PASS (RICH-20 protected)
- `go build ./cmd/km-slack/...`: PASS

## Self-Check: PASSED

- `cmd/km-slack/main.go` contains "blocks-rich": FOUND (3 locations: runPost switch, runReply switch, runWith case)
- `cmd/km-slack/main_rich_test.go`: FOUND
- `cmd/km-slack/main_rich_test.go` contains "func TestRunPost_BlocksRich": FOUND
- Commit `c164c023`: FOUND
- `go test ./cmd/km-slack/...`: PASS
- `go test ./pkg/compiler/... -run TestUserdataAdditionalVolumeOnly_Golden`: PASS (RICH-20)

## Next Phase Readiness

- Plan 04 (`111-04`): corpus+docs+plugin bump. `blocks-rich` is now end-to-end reachable from the sidecar CLI. `TestRichCorpus` is ready for extension to `rich-mixed.md` (H1 + prose + table + tool line). `docs/slack-notifications.md` § Phase 111 and `skills/slack/SKILL.md` render-mode table still need updating + plugin version bump.

---
*Phase: 111-rich-slack-rendering-markdown-and-table-blocks-opt-in*
*Completed: 2026-06-14*
