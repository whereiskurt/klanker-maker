---
phase: 74-slack-mrkdwn-rendering-tokenizer-based-markdown-slack-mrkdwn-transformer-optional-block-kit-tier-for-streaming-hook-output
plan: 02
subsystem: slack-rendering
tags: [slack, block-kit, blocks, bridge, rendering, phase74]
dependency_graph:
  requires:
    - pkg/slack/Mrkdwnify (Plan 74-01)
    - pkg/slack/MaxRenderedBytes (Plan 74-01)
    - pkg/slack/payload.go SlackEnvelope (Phase 63)
    - pkg/slack/bridge/handler.go ActionPost dispatch (Phase 63)
    - pkg/compiler/userdata.go _km_stream_drain (Phase 68)
    - pkg/compiler/userdata.go inbound poller reply (Phase 67)
  provides:
    - pkg/slack/RenderBlocks (Tier 2 Block Kit builder)
    - pkg/slack/SlackEnvelope.Blocks (additive envelope field)
    - pkg/slack/bridge/BlockPoster (optional interface)
    - pkg/slack/bridge/SlackPosterAdapter.PostMessageBlocks
    - cmd/km-slack --render=blocks execution path
    - streaming hook + inbound poller reply on --render=blocks default
  affects:
    - pkg/compiler/userdata.go (sandbox runtime — new sandboxes only)
    - pkg/slack/bridge (Lambda redeploy required)
    - cmd/km-slack (sidecar binary redeploy required)
tech_stack:
  added: [encoding/json typed Block Kit structs, testing/quick property tests]
  patterns: [optional-interface-via-type-assertion, additive-envelope-field, fail-soft-recover, paragraph-sentence-char-split, two-pass-render]
key_files:
  created:
    - pkg/slack/blocks.go (~330 lines — RenderBlocks + 4 typed block structs + 3000-char split + 50-block cap + plain-text fallback)
    - pkg/slack/blocks_test.go (~420 lines — BLK-01..BLK-10 + testing/quick property + corpus walker)
    - pkg/slack/testdata/blocks-h1-header.md + .expected-blocks.json
    - pkg/slack/testdata/blocks-h2h3-section.md + .expected-blocks.json
    - pkg/slack/testdata/blocks-tool-line.md + .expected-blocks.json
    - pkg/slack/testdata/blocks-divider.md + .expected-blocks.json
    - pkg/slack/testdata/blocks-section-overflow.md + .expected-blocks.json
    - pkg/slack/testdata/blocks-50block-fallback.md (no expected-blocks — ok=false assertion)
    - pkg/slack/testdata/blocks-header-strip.md + .expected-blocks.json
    - pkg/slack/testdata/blocks-header-truncate.md + .expected-blocks.json
    - pkg/slack/testdata/blocks-plain-text-fallback.md + .expected-blocks.json
    - pkg/compiler/userdata_slack_inbound_test.go (Task 6 — asserts --render=blocks on inbound poller reply)
  modified:
    - pkg/slack/payload.go (SlackEnvelope.Blocks string field in alphabetical position)
    - pkg/slack/payload_test.go (canonical-ordering assertion update)
    - pkg/slack/payload_transcript_test.go (TestCanonicalJSON_BlocksOrdering — BRDG-03)
    - pkg/slack/bridge/interfaces.go (BlockPoster optional interface)
    - pkg/slack/bridge/aws_adapters.go (SlackPosterAdapter.PostMessageBlocks real impl with chat.postMessage carrying text + blocks)
    - pkg/slack/bridge/handler.go (ActionPost dispatch — type-assert to BlockPoster when env.Blocks != "")
    - pkg/slack/bridge/handler_test.go (TestHandler_Post_WithBlocks — BRDG-02; fake satisfying both interfaces)
    - cmd/km-slack/main.go (runWith "blocks" branch — RenderBlocks → env.Blocks + fallbackText body; Mrkdwnify on ok=false)
    - cmd/km-slack/main_test.go (TestRunWith_Blocks)
    - pkg/compiler/userdata.go (_km_stream_drain --render flag; inbound poller reply --render flag)
    - pkg/compiler/userdata_test.go (TestStreamDrain_RenderFlag — HOOK-01 streaming)
decisions:
  - "Additive envelope field: SlackEnvelope.Blocks placed alphabetically between Action and Body. Empty-string default routes to existing PostMessage path — preserves Phase 63/67/68 backward compatibility without conditional flags."
  - "Optional interface via type assertion (RESEARCH.md § Open Question 1): BlockPoster declared as a separate interface; handler dispatch type-asserts h.Slack.(BlockPoster). SlackPoster interface is UNCHANGED, so existing fakes in Phase 63/67/68 tests still compile and pass."
  - "Typed Block Kit structs (RESEARCH.md § Don't Hand-Roll) instead of map[string]any — guarantees JSON output conforms to Slack's schema; defensive against typos."
  - "Section-overflow split is paragraph → sentence → character (in that order). Last-resort character split rare in practice but guarantees no section exceeds the 3000-char Slack hard limit."
  - "Fail-soft via deferred recover() wraps the whole RenderBlocks body. Panics return ok=false so the caller transparently falls back to Tier 1 Mrkdwnify — never breaks the streaming pipeline."
  - "Operator safety valve via KM_SLACK_RENDER env var with bash default ${KM_SLACK_RENDER:-blocks} — operator can downgrade a single sandbox to plain/mrkdwn by writing KM_SLACK_RENDER=plain into /etc/km/notify.env without a redeploy."
  - "Scope expansion mid-execution: Task 6 added --render=blocks to the inbound poller reply path. Original plan only flipped the streaming hook (_km_stream_drain); UAT exercised the Slack-inbound chat path which goes through the poller, not the streaming hook. The reply call was missing the flag."
patterns_established:
  - "Optional interface extension: declare a sibling interface (BlockPoster) + dispatch via type assertion. Backward-compatible alternative to widening the canonical interface."
  - "Additive JSON envelope field: alphabetical insertion + empty-string default = zero-cost backward compat for already-deployed callers."
  - "Two-tier render with auto-fallback: try Tier 2 (blocks), fall through to Tier 1 (mrkdwn) on cap or panic, never fail the post."
  - "Operator-overridable rendered defaults: bash ${VAR:-default} pattern in userdata heredoc with documented EnvironmentFile escape hatch."
requirements_completed:
  - BLK-01
  - BLK-02
  - BLK-03
  - BLK-04
  - BLK-05
  - BLK-06
  - BLK-07
  - BLK-08
  - BLK-09
  - BLK-10
  - BRDG-01
  - BRDG-02
  - BRDG-03
  - HOOK-01
metrics:
  duration: "~6 days elapsed (Tasks 1-4 on 2026-05-09; Task 6 + UAT on 2026-05-15/16)"
  completed_date: "2026-05-16"
  tasks_completed: 6
  files_changed: 27
---

# Phase 74 Plan 02: Block Kit Rendering Summary

**One-liner:** Tier 2 Block Kit renderer with header/section/context/divider mappers, 3000-char section split, 50-block cap auto-fallback to Tier 1, additive bridge envelope with BlockPoster optional interface + type-assertion dispatch, and `--render=blocks` flipped on BOTH the Phase 68 streaming hook AND the Phase 67 inbound poller reply path.

## Performance

- **Duration:** ~6 days elapsed (Tasks 1-4 landed 2026-05-09; Task 6 + UAT completed 2026-05-15/16)
- **Started:** 2026-05-09
- **Completed:** 2026-05-16
- **Tasks:** 6 (Plan was 5 tasks; Task 6 added during UAT as scope expansion)
- **Files modified:** 27

## What Was Built

### pkg/slack/blocks.go — Tier 2 Block Kit Builder

`RenderBlocks(input string) (blocksJSON, fallbackText string, ok bool)` walks input line-by-line, emits one of four typed block structs per logical chunk, and returns the serialized JSON array plus a plain-text fallback (for `chat.postMessage`'s `text:` field used in push notifications and search).

**Mapping rules (BLK-01..BLK-10):**

| Req | Pattern | Output |
|-----|---------|--------|
| BLK-01 | `^# X` | `header` block; strip backticks/asterisks/underscores from inner text (BLK-09); hard-truncate to 147 + `…` if > 150 chars (BLK-10) |
| BLK-02 | `^## X`, `^### X` | `section` block with `*X*` bold prefix; coalesces with adjacent paragraphs under 3000 chars |
| BLK-03 | `^🔧 \w+: ` | `context` block; HTML-escape only — no other transforms (hook pre-formats tool lines) |
| BLK-04 | `^\s*(---\|***\|___)\s*$` | `divider` block |
| BLK-05 | section text ≥ 3000 chars | paragraph → sentence → char-boundary split; one section block per chunk |
| BLK-06 | block count > 50 | return `("", "", false)` — caller falls back to Tier 1 |
| BLK-07 | (synthesized) | plain-text fallback strips ALL formatting: backticks, asterisks, underscores; `[label](url)` → `label`; drop heading `#`s; tool lines verbatim; hrules dropped |
| BLK-08 | testing/quick property | 200 random inputs — decoded JSON has only valid block types, header text ≤ 150, section text ≤ 3000, total ≤ 50 |
| BLK-09 | header inner formatting | strip backticks/asterisks/underscores |
| BLK-10 | header > 150 chars | hard-truncate to 147 + `…` |

**Inline formatting** inside section text reuses Plan 01's `Mrkdwnify` for `**x**` → `*x*`, links, escapes.

**Fail-soft** via `defer func() { if r := recover(); r != nil { ok = false; ... } }()` — panics return `(\"\", \"\", false)`, caller transparently degrades to Tier 1.

### pkg/slack/payload.go — Additive Blocks Field

Added `Blocks string` in alphabetical position between `Action` and `Body`:

```go
Action  string `json:"action"`
Blocks  string `json:"blocks"`  // NEW (Phase 74 Tier 2): pre-serialized Block Kit JSON array
Body    string `json:"body"`
```

Zero value (`""`) routes to the existing PostMessage path — Phase 63/67/68 callers unchanged.

### pkg/slack/bridge/{interfaces,aws_adapters,handler}.go — Bridge Dispatch

- **`BlockPoster` optional interface** with `PostMessageBlocks(ctx, channel, subject, body, blocksJSON, threadTS) (string, error)`. SlackPoster UNCHANGED so existing fakes still satisfy interface assertions.
- **`SlackPosterAdapter.PostMessageBlocks`** posts `chat.postMessage` with both `text` (fallback string) and `blocks` (pre-serialized JSON via `json.RawMessage`), mirroring the existing PostMessage's HTTP client + rate-limit + error-mapping pattern.
- **Handler dispatch wrap** (BRDG-02): `if env.Blocks != "" && h.Slack.(BlockPoster)` → route to PostMessageBlocks; else fall through to original PostMessage. BRDG-01 backward compat preserved — `env.Blocks == ""` is byte-identical to pre-Phase-74 dispatch.

### cmd/km-slack/main.go — runWith "blocks" Branch

```go
case "blocks":
    bj, fallback, okBK := slack.RenderBlocks(string(body))
    if okBK {
        rendered = fallback
        blocksJSON = bj
    } else {
        rendered = slack.Mrkdwnify(string(body))  // 50-block cap → Tier 1 fallback
    }
```

After envelope construction: `if blocksJSON != "" { env.Blocks = blocksJSON }`. Signing path unchanged because the additive field is alphabetically positioned.

### pkg/compiler/userdata.go — Hook Flip (HOOK-01)

Two callers flipped to `--render "${KM_SLACK_RENDER:-blocks}"`:

1. **`_km_stream_drain`** (Phase 68 per-turn transcript streaming, line ~631) — Task 4
2. **Inbound poller reply post** (Phase 67 Slack-inbound chat reply, line ~764) — Task 6 (scope expansion)

Operator override: `echo KM_SLACK_RENDER=plain | sudo tee -a /etc/km/notify.env && sudo systemctl daemon-reload` downgrades the entire sandbox without a redeploy.

## Task Commits

1. **Task 1: Wave 0 scaffolding** — `1a6fd21` (feat) — Blocks envelope field, BlockPoster interface, blocks.go skeleton, 9 corpus fixture pairs, skip-stubbed tests
2. **Task 2: Tier 2 Block Kit builder** — `e5f27fa` (feat) — RenderBlocks fully implemented; BLK-01..BLK-10 + testing/quick property green
3. **Task 3: Bridge dispatch + runWith for blocks** — `a0f9389` (feat) — BRDG-02 + TestRunWith_Blocks green; PostMessageBlocks calls chat.postMessage with text + blocks
4. **Task 4: Streaming hook --render=blocks** — `7711793` (feat) — HOOK-01 streaming path
5. **Task 5: UAT (manual)** — completed by user; sandbox `learn-14f484c7`
6. **Task 6: Inbound poller reply --render=blocks** — `8f983db` (feat) — HOOK-01 inbound path; scope expansion from UAT discovery

**Plan metadata:** (this closeout commit)

## Test Results

| Req | Test | Status |
|-----|------|--------|
| BLK-01 | TestBlocks_H1Header | PASS |
| BLK-02 | TestBlocks_H2H3Section | PASS |
| BLK-03 | TestBlocks_ToolLine | PASS |
| BLK-04 | TestBlocks_Divider | PASS |
| BLK-05 | TestBlocks_SectionOverflow | PASS |
| BLK-06 | TestBlocks_50BlockFallback | PASS |
| BLK-07 | TestBlocks_PlainTextFallback | PASS |
| BLK-08 | TestBlocks_StructuralValidity (testing/quick, 200 iter) | PASS |
| BLK-09 | TestBlocks_HeaderStrip | PASS |
| BLK-10 | TestBlocks_HeaderTruncate | PASS |
| (corpus) | TestBlocksCorpus (8 fixtures, skip 50block) | PASS |
| BRDG-01 | TestHandler_Post_NoBlocks (pre-existing, still green) | PASS |
| BRDG-02 | TestHandler_Post_WithBlocks | PASS |
| BRDG-03 | TestCanonicalJSON_BlocksOrdering | PASS |
| HOOK-01 (streaming) | TestStreamDrain_RenderFlag | PASS |
| HOOK-01 (inbound) | TestKmSlackInboundPollerRenderFlag | PASS |
| (km-slack) | TestRunWith_Blocks | PASS |

## UAT — End-to-End in Production

**Sandbox:** `learn-14f484c7` (created 2026-05-16T02:37 UTC from `profiles/learn.v2.yaml`, `noBedrock: true`)

**Deployment sequence (operator):**
1. `make build`
2. `km init --sidecars` — uploaded new km-slack binary + new userdata template (`--render=blocks` on streaming hook AND inbound poller reply)
3. `km init --dry-run=false` (note: NOT `--lambdas` — that builds the zip but does not deploy per CLAUDE.md memory `km_init_lambdas_doesnt_deploy`) — bridge Lambda redeployed 02:35 UTC with BlockPoster dispatch
4. `km create profiles/learn.v2.yaml --alias learn-14f484c7`

**Verification:** User chatted into `#sb-learn-14f484c7` from Slack. Reply landed as rendered Block Kit (header block, section blocks, context blocks). User signed off:

> "I see the blocks coming back nicely formatted, seems to be working."

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] HOOK-01 scope expansion — inbound poller reply was missing --render flag**

- **Found during:** Task 5 UAT (user reported plain-text replies after deploying Tasks 1-4)
- **Issue:** Plan 02 frontmatter `key_links` and Task 4 only covered `pkg/compiler/userdata.go _km_stream_drain` (Phase 68 streaming hook). The Phase 67 Slack-inbound chat reply path goes through a separate `km-slack post` call (the inbound poller's reply, line ~764 in userdata.go), which was missing `--render`. UAT exercised the inbound chat path, not the streaming hook, so the user observed raw markdown despite Tasks 1-4 being correct.
- **Fix:** Added `--render "${KM_SLACK_RENDER:-blocks}"` to the inbound poller reply post in userdata.go. New `pkg/compiler/userdata_slack_inbound_test.go` asserts the flag presence specifically inside the inbound poller block.
- **Files modified:** `pkg/compiler/userdata.go`, `pkg/compiler/userdata_slack_inbound_test.go`
- **Verification:** End-to-end UAT on sandbox `learn-14f484c7` — Slack-inbound chat reply rendered as Block Kit. User approval logged above.
- **Committed in:** `8f983db` (Task 6, scope expansion)

---

**Total deviations:** 1 auto-fixed (1 blocking — Rule 3)
**Impact on plan:** The deviation extended scope by ~50 lines of code + one test file. No architectural change. The fix is symmetric with Task 4 (same `--render` flag, same env-var override semantics, same backward-compat behavior).

## Authentication Gates

None during planned task execution. One UAT side-find (not a Plan 74-02 defect):

- **Anthropic OAuth token stale.** The sandbox under `noBedrock: true` returned 401 from `api.anthropic.com` because the OAuth token in `~/.claude/.credentials.json` had expired. Operator resolved by running `claude login` on the sandbox. Pre-existing failure mode for any noBedrock profile — noted here for future operator runbook update (Phase 81 candidate).

## Issues Encountered

None during Plan 02 execution. UAT surfaced the inbound poller scope gap (Task 6) which was fixed inline, and the stale-OAuth gate (operator-resolved, out of scope).

## Pre-existing Failures (Out of Scope)

Documented in `deferred-items.md`:

- `TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0` (pre-Phase-74-01)
- `TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock` + 5 siblings in `userdata_notify_test.go` (last touched in Phase 63-04 per git log)

None caused by Plan 74-02 changes; confirmed pre-existing via Plan 01 closeout (commit `0e3dcf6`).

## Operator Runbook — KM_SLACK_RENDER Safety Valve

If a sandbox starts producing malformed Block Kit (e.g., a future Slack API change) and the operator wants to downgrade WITHOUT a redeploy:

```bash
# On the sandbox (km shell <sandbox-id>):
echo 'KM_SLACK_RENDER=plain' | sudo tee -a /etc/km/notify.env
sudo systemctl daemon-reload   # picks up EnvironmentFile change for next post
# OR for shell sessions:
echo 'export KM_SLACK_RENDER=plain' | sudo tee -a /etc/profile.d/km-notify-env.sh
```

Three valid values:
- `blocks` (default) — Tier 2 Block Kit with auto-fallback to Tier 1 on 50-block cap
- `mrkdwn` — Tier 1 only (Plan 74-01 transforms; no Block Kit assembly)
- `plain` — no transforms, raw text

## Confirmation: No Phase 62/63/67/68 Regression

- `km slack test` path unchanged (still routes through ActionTest → PostMessage, no Blocks field)
- Phase 62 email notifications: unchanged (`km-notify-hook` does not use `--render`)
- Phase 63 Slack notifications: unchanged (idle/permission pings use default `--render=plain`)
- Phase 67 Slack inbound dispatch (bridge `/events` → SQS): unchanged; only the REPLY post (Task 6) flipped
- Phase 68 transcript streaming: streaming hook flipped to blocks; final gzipped JSONL upload at Stop unchanged

## Deferred Items

- **Italic markdown rendering** — explicitly deferred per `74-CONTEXT.md § Deferred Ideas`
- **`chat.update` for live edit-in-place** — explicitly deferred
- **`rich_text` Block Kit** — explicitly deferred (Block Kit `section + mrkdwn` is sufficient)
- **`/clone` and Task-tool subagent fan-out producing one Slack thread per session_id** — Phase 68 known limitation, not regressed by Plan 02
- **Anthropic OAuth refresh runbook** — operator-runbook follow-up (not a code change)

## Next Phase Readiness

- Plan 02 closes Phase 74. Both plans (01 + 02) shipped end-to-end in production.
- Sandbox `learn-14f484c7` is live with full Block Kit rendering on streaming + inbound paths.
- ROADMAP advances to Phase 75 (already complete) → no carry-forward blockers.

---
*Phase: 74-slack-mrkdwn-rendering*
*Completed: 2026-05-16*

## Self-Check: PASSED

- pkg/slack/blocks.go: FOUND
- pkg/slack/blocks_test.go: FOUND
- pkg/compiler/userdata_slack_inbound_test.go: FOUND
- 74-02-SUMMARY.md: FOUND
- Task commits (1a6fd21, e5f27fa, a0f9389, 7711793, 8f983db): all 5 FOUND in git log
