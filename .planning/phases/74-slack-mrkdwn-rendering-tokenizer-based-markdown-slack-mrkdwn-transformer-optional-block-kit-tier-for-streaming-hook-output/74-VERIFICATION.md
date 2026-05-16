---
phase: 74-slack-mrkdwn-rendering-tokenizer-based-markdown-slack-mrkdwn-transformer-optional-block-kit-tier-for-streaming-hook-output
verified: 2026-05-15T23:10:00Z
status: passed
score: 15/15 must-haves verified
re_verification:
  previous_status: passed
  previous_score: 14/14
  previous_verified: 2026-05-10T14:43:49Z
  gaps_closed: []
  gaps_remaining: []
  regressions: []
  scope_expansion:
    - "Task 6 (commit 8f983db, 2026-05-15) extended HOOK-01 to also cover the Slack-inbound poller reply post at pkg/compiler/userdata.go:1539 — original Plan 02 Task 4 only flipped _km_stream_drain. The poller was the dominant Phase-67 path operators actually used, so the Block-Kit flip was effectively no-op for the UAT chat flow until Task 6."
    - "New regression test TestSlackInboundPoller_ReplyPost_RenderFlag added to pkg/compiler/userdata_slack_inbound_test.go (passes)."
    - "Plan 02 SUMMARY and deferred-items updated in commit 903ef89 to reflect Task 6."
  uat_evidence:
    - "End-to-end Slack-inbound chat to sandbox learn-14f484c7 produced Block Kit rendered replies (header / section / context blocks)"
    - 'Operator confirmation: "I see the blocks coming back nicely formatted, seems to be working."'
    - "Bridge Lambda redeployed 2026-05-16T02:35 UTC; sandbox sidecars refreshed"
human_verification:
  - test: "End-to-end Slack rendering in a live sandbox with notifySlackTranscriptEnabled: true"
    expected: "Transcript turns appear as structured Block Kit messages (header blocks for # headings, context blocks for tool lines, mrkdwn section blocks for prose)"
    why_human: "Requires live AWS environment, real Slack workspace, and actual Claude agent output to verify visual rendering"
    status: "SATISFIED — operator confirmed UAT against sandbox learn-14f484c7 on 2026-05-15"
  - test: "KM_SLACK_RENDER=plain operator safety valve downgrade"
    expected: "Setting KM_SLACK_RENDER=plain in /etc/km/notify.env produces raw markdown in Slack on the next turn, without redeploying km-slack"
    why_human: "Requires live sandbox with systemd access + Slack observation"
    status: "NOT YET EXERCISED — fall-through default verified via TestRunWith_EnvOverride; live downgrade not part of this UAT"
---

# Phase 74: Slack Mrkdwn Rendering — Verification Report

**Phase Goal:** Eliminate literal `***heading***` asterisks, dropped `# headings`, and broken pipe-tables by adding a tokenizer-based renderer that converts Claude's CommonMark-ish output into valid Slack mrkdwn (Tier 1) and structured Block Kit (Tier 2). Two-PR phasing: PR1 ships Tier 1 + `--render=mrkdwn` flag with the streaming hook unchanged; PR2 ships Tier 2 Block Kit + flips the Phase 68 streaming hook in `pkg/compiler/userdata.go _km_stream_drain` to `--render=blocks`. Existing Phase 62/63/67 callers stay on default `plain`. Robustness moat: tokenizer preserves code blocks byte-for-byte, idempotent + fail-soft properties, fuzz target, corpus fixtures.

**Verified:** 2026-05-15T23:10:00Z
**Status:** PASSED — all 14 original must-haves still verified + 1 new must-have (HOOK-01 inbound coverage) added and verified.
**Re-verification:** Yes — initial verification 2026-05-10 passed 14/14; this pass re-verifies after the Task 6 scope expansion (commit `8f983db`) and the UAT operator sign-off.

## Scope expansion context

Plan 02 Task 4 flipped the Phase 68 transcript-streaming hook (`_km_stream_drain`) to `--render "${KM_SLACK_RENDER:-blocks}"`. UAT against sandbox `learn-14f484c7` revealed that the *Slack-inbound* path (Phase 67 poller, the path operators actually chat through) had its OWN `km-slack post` call at `pkg/compiler/userdata.go:1535` that was missing the flag — defaulting to plain, even though the streaming hook was correctly flipped. The result was operators chatting in `#sb-<id>` saw literal markdown for the poller's final reply, even though per-turn streaming rendered correctly.

Task 6 (commit `8f983db`, 2026-05-15) closed the gap by adding `--render "${KM_SLACK_RENDER:-blocks}"` to the poller's reply post, plus a regression test `TestSlackInboundPoller_ReplyPost_RenderFlag`. HOOK-01 now covers BOTH end-to-end paths:

1. Per-turn streaming via `_km_stream_drain` (line 641, original Task 4)
2. Final reply via `km-slack-inbound-poller` (line 1539, new Task 6)

This expansion is consistent with the phase goal — the goal text mentioned `_km_stream_drain` because that was the only Block-Kit-emitting path identified at planning time. Discovering the second path during UAT did not change the goal; it expanded the surface that HOOK-01 must cover.

## Goal Achievement

### Observable Truths — Plan 74-01 (Tier 1)

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | Tier 1 transforms produce valid Slack mrkdwn for the locked transform set | VERIFIED | `TestMrkdwnify_Bold/Heading/Link/Strike/HTMLEscape/HRule/PipeTable` PASS; `TestMrkdwnifyCorpus` 10 mrkdwn fixtures PASS |
| 2 | Tokenizer preserves code-fence and code-span content byte-for-byte | VERIFIED | `TestCodeFencePreservation` and `TestCodeSpanPreservation` PASS |
| 3 | Mrkdwnify is idempotent: running it twice yields the same output | VERIFIED | `TestMrkdwnifyIdempotent` PASS |
| 4 | Mrkdwnify is fail-soft: any panic returns original input unchanged | VERIFIED | `TestMrkdwnify_FailSoft` PASS; `recover()` confirmed in `mrkdwn.go` line 47 |
| 5 | `km-slack post --render=plain` (default) is a no-op | VERIFIED | `TestRunWith_Plain` PASS (subtest of pkg/slack tests) |
| 6 | `km-slack post --render=mrkdwn` passes body through Mrkdwnify; >35 KB truncated | VERIFIED | `TestRunWith_Overflow` PASS; `MaxRenderedBytes = 35 * 1024` in payload.go:28 |
| 7 | `KM_SLACK_RENDER` env var overrides `--render` flag | VERIFIED | `TestRunWith_EnvOverride` PASS |

### Observable Truths — Plan 74-02 (Tier 2 + Bridge + Hook)

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 8 | Tier 2 RenderBlocks produces valid Block Kit JSON within Slack structural limits | VERIFIED | `TestBlocks_StructuralValidity` (quick.Check, 200 iterations) PASS; BLK-01..BLK-10 PASS |
| 9 | When block count > 50, RenderBlocks returns ok=false (Tier 1 fallback) | VERIFIED | `TestBlocks_50BlockFallback` PASS |
| 10 | Bridge changes additive: `SlackEnvelope.Blocks=""` routes to existing PostMessage path | VERIFIED | `TestHandler_Post_NoBlocks` PASS (BRDG-01) |
| 11 | `SlackEnvelope.Blocks!=""` routes via BlockPoster.PostMessageBlocks | VERIFIED | `TestHandler_Post_WithBlocks` PASS (BRDG-02) |
| 12 | Canonical JSON ordering: `"blocks"` between `"action"` and `"body"` | VERIFIED | `TestCanonicalJSON_BlocksOrdering` PASS (BRDG-03) |
| 13 | Streaming hook `_km_stream_drain` calls `km-slack post --render "${KM_SLACK_RENDER:-blocks}"` | VERIFIED | `TestStreamDrain_RenderFlag` PASS (HOOK-01); confirmed at `pkg/compiler/userdata.go:641` |
| 14 | Existing Phase 62/63/67 callers see no behavior change (no `--render` flag = default plain) | VERIFIED | All non-streaming-hook callsites unchanged |
| 15 | Slack-inbound poller reply post also renders as Block Kit (HOOK-01 inbound coverage) | VERIFIED | `TestSlackInboundPoller_ReplyPost_RenderFlag` PASS; confirmed at `pkg/compiler/userdata.go:1539`; UAT operator confirmation |

**Score:** 15/15 truths verified (up from 14/14 with the Task 6 expansion).

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/slack/payload.go` | `MaxRenderedBytes = 35000` + `Blocks string` field, alphabetical position | VERIFIED | Line 28 (const) + Line 76 (field). `Blocks` declared as `json:"blocks"` between `Action` and `Body`. |
| `pkg/slack/mrkdwn.go` | Three-segment tokenizer + Tier 1 transforms + `Mrkdwnify()` w/ recover | VERIFIED | `func Mrkdwnify` at line 47 with `defer recover()` for fail-soft |
| `pkg/slack/mrkdwn_test.go` | Unit tests REND-01..13, FuzzMrkdwnify, corpus walker | VERIFIED | All present; `FuzzMrkdwnify` at line 281; corpus auto-discovers 10 `.md`/`.expected-mrkdwn.txt` pairs |
| `pkg/slack/testdata/` (Tier 1) | 10 fixture pairs + fuzz seed dir | VERIFIED | `bold-collapse`, `code-fence-passthrough`, `heading-map`, `hrule-drop`, `html-escape`, `idempotent-already-mrkdwn`, `link-conversion`, `pipe-table`, `strike`, `tool-lines` + `fuzz/FuzzMrkdwnify/.gitkeep` |
| `pkg/slack/blocks.go` | `RenderBlocks` Tier 2 builder | VERIFIED | `func RenderBlocks` at line 83 — header/section/context/divider mappers + 50-block cap + section overflow splitter + plain-text fallback + recover() |
| `pkg/slack/blocks_test.go` | BLK-01..10 + structural validity + corpus | VERIFIED | `TestBlocks_StructuralValidity` PASS; all 10 BLK-* PASS |
| `pkg/slack/testdata/blocks-*` | 9 fixture pairs for Tier 2 | VERIFIED | `blocks-divider`, `blocks-h1-header`, `blocks-h2h3-section`, `blocks-header-strip`, `blocks-header-truncate`, `blocks-plain-text-fallback`, `blocks-section-overflow`, `blocks-tool-line` (8 pairs); `blocks-50block-fallback.md` (input only — tested directly) |
| `pkg/slack/bridge/interfaces.go` | `BlockPoster` optional interface | VERIFIED | `type BlockPoster interface` at line 72 with `PostMessageBlocks(ctx, channel, subject, body, blocksJSON, threadTS) (string, error)` |
| `pkg/slack/bridge/aws_adapters.go` | `SlackPosterAdapter.PostMessageBlocks` | VERIFIED | Method at line 385; calls `chat.postMessage` with `text` + `blocks` JSON |
| `pkg/slack/bridge/handler.go` | Dispatch type assertion when `env.Blocks != ""` | VERIFIED | Lines 257-271; `if env.Blocks != ""` branch + `h.Slack.(BlockPoster)` type assertion + degrade to text-only on missing BP |
| `pkg/slack/bridge/handler_test.go` | BRDG-01 + BRDG-02 tests | VERIFIED | `TestHandler_Post_NoBlocks` + `TestHandler_Post_WithBlocks` both PASS |
| `pkg/slack/payload_transcript_test.go` | BRDG-03 canonical JSON ordering | VERIFIED | `TestCanonicalJSON_BlocksOrdering` PASS |
| `cmd/km-slack/main.go` | `--render=plain\|mrkdwn\|blocks` flag + KM_SLACK_RENDER fallback + overflow logic | VERIFIED | Line 97 flag; line 102-114 fallback + validation; line 336 RenderBlocks call; line 344 Mrkdwnify call |
| `cmd/km-slack/main_test.go` | REND-14..16 against fake bridge + TestRunWith_Blocks | VERIFIED | All 4 tests PASS (one pre-existing failure unrelated, see Anti-Patterns) |
| `pkg/compiler/userdata.go` (`_km_stream_drain`) | Line 641 carries `--render "${KM_SLACK_RENDER:-blocks}"` | VERIFIED | Confirmed at line 641 inside `_km_stream_drain` body |
| `pkg/compiler/userdata.go` (inbound poller) | Line 1539 carries `--render "${KM_SLACK_RENDER:-blocks}"` (Task 6 expansion) | VERIFIED | Confirmed at line 1539 inside `km-slack-inbound-poller` script; comment at lines 1529-1534 explains rationale |
| `pkg/compiler/userdata_test.go` | `TestStreamDrain_RenderFlag` (HOOK-01) | VERIFIED | PASS |
| `pkg/compiler/userdata_slack_inbound_test.go` | `TestSlackInboundPoller_ReplyPost_RenderFlag` (HOOK-01 inbound) — NEW | VERIFIED | PASS — asserts `--render "${KM_SLACK_RENDER:-blocks}"` appears inside the `if /opt/km/bin/km-slack post` shell statement and BEFORE `--body "$POST_FILE"` |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `cmd/km-slack/main.go runPost` | `cmd/km-slack/main.go runWith` | `renderMode string` parameter | WIRED | `renderMode` threaded through `runPost → run → runWith`; KM_SLACK_RENDER env fallback resolved before threading |
| `cmd/km-slack/main.go runWith` | `pkg/slack.Mrkdwnify` | renderMode=="mrkdwn" branch | WIRED | `slack.Mrkdwnify(string(body))` at line 344 |
| `cmd/km-slack/main.go runWith` | `pkg/slack.MaxRenderedBytes` | overflow truncation | WIRED | `len(rendered) > slack.MaxRenderedBytes` check present |
| `cmd/km-slack/main.go runWith` | `pkg/slack.RenderBlocks` | renderMode=="blocks" branch | WIRED | `slack.RenderBlocks(string(body))` at line 336; ok==false falls back to Mrkdwnify |
| `cmd/km-slack/main.go runWith` | `pkg/slack.SlackEnvelope.Blocks` | post-render assignment | WIRED | `env.Blocks = bj` after RenderBlocks success |
| `pkg/slack/bridge/handler.go ActionPost` | `BlockPoster.PostMessageBlocks` | type assertion `h.Slack.(BlockPoster)` | WIRED | Lines 263-265; conditional on `env.Blocks != ""` |
| `pkg/compiler/userdata.go _km_stream_drain` | `/opt/km/bin/km-slack post` | `--render "${KM_SLACK_RENDER:-blocks}"` | WIRED | Line 641; verified by `TestStreamDrain_RenderFlag` |
| `pkg/compiler/userdata.go km-slack-inbound-poller` | `/opt/km/bin/km-slack post` | `--render "${KM_SLACK_RENDER:-blocks}"` | WIRED | Line 1539; verified by `TestSlackInboundPoller_ReplyPost_RenderFlag` (NEW) |

### Requirements Coverage

Phase 74 requirements are LOCAL to the phase (defined in `74-VALIDATION.md` § Per-Task Verification Map, not in the global `.planning/REQUIREMENTS.md`).

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|---------|
| REND-01 | 74-01 | Bold collapse `**x**` → `*x*` | SATISFIED | `TestMrkdwnify_Bold` PASS |
| REND-02 | 74-01 | Heading `# h` etc → bold | SATISFIED | `TestMrkdwnify_Heading` PASS |
| REND-03 | 74-01 | Markdown link → `<url\|label>` | SATISFIED | `TestMrkdwnify_Link` PASS |
| REND-04 | 74-01 | Strikethrough `~~x~~` → `~x~` | SATISFIED | `TestMrkdwnify_Strike` PASS |
| REND-05 | 74-01 | HTML-escape `<>` and `&` outside code | SATISFIED | `TestMrkdwnify_HTMLEscape` PASS |
| REND-06 | 74-01 | Horizontal rule dropped | SATISFIED | `TestMrkdwnify_HRule` PASS |
| REND-07 | 74-01 | Pipe-table → code fence (2+ lines only) | SATISFIED | `TestMrkdwnify_PipeTable` PASS |
| REND-08 | 74-01 | Code-fence preservation | SATISFIED | `TestCodeFencePreservation` PASS |
| REND-09 | 74-01 | Code-span preservation | SATISFIED | `TestCodeSpanPreservation` PASS |
| REND-10 | 74-01 | Idempotence | SATISFIED | `TestMrkdwnifyIdempotent` PASS |
| REND-11 | 74-01 | Fuzz no-panic + idempotent | SATISFIED | `FuzzMrkdwnify` target present at `mrkdwn_test.go:281` with seed corpus directory |
| REND-12 | 74-01 | Corpus fixtures | SATISFIED | `TestMrkdwnifyCorpus` PASS for 10 fixtures |
| REND-13 | 74-01 | Fail-soft recover() | SATISFIED | `TestMrkdwnify_FailSoft` PASS |
| REND-14 | 74-01 | Body overflow truncation > 35 KB | SATISFIED | `TestRunWith_Overflow` PASS |
| REND-15 | 74-01 | `--render=plain` no-op default | SATISFIED | `TestRunWith_Plain` PASS |
| REND-16 | 74-01 | `KM_SLACK_RENDER` env override | SATISFIED | `TestRunWith_EnvOverride` PASS |
| BLK-01 | 74-02 | `# h` → header block | SATISFIED | `TestBlocks_H1Header` PASS |
| BLK-02 | 74-02 | `## h`/`### h` → bold section | SATISFIED | `TestBlocks_H2H3Section` PASS |
| BLK-03 | 74-02 | tool-line → context block | SATISFIED | `TestBlocks_ToolLine` PASS |
| BLK-04 | 74-02 | `---` → divider, no auto-dividers | SATISFIED | `TestBlocks_Divider` PASS |
| BLK-05 | 74-02 | section text 3000-char split | SATISFIED | `TestBlocks_SectionOverflow` PASS |
| BLK-06 | 74-02 | 50-block cap → Tier 1 fallback | SATISFIED | `TestBlocks_50BlockFallback` PASS |
| BLK-07 | 74-02 | `text:` plain-text fallback | SATISFIED | `TestBlocks_PlainTextFallback` PASS |
| BLK-08 | 74-02 | Block Kit structural validity | SATISFIED | `TestBlocks_StructuralValidity` PASS (200-iter property test) |
| BLK-09 | 74-02 | Header strip backtick/asterisk/underscore | SATISFIED | `TestBlocks_HeaderStrip` PASS |
| BLK-10 | 74-02 | Header > 150 chars truncated | SATISFIED | `TestBlocks_HeaderTruncate` PASS |
| BRDG-01 | 74-02 | `Blocks=""` → text-only post (backward compat) | SATISFIED | `TestHandler_Post_NoBlocks` PASS |
| BRDG-02 | 74-02 | `Blocks!=""` → `PostMessageBlocks` dispatch | SATISFIED | `TestHandler_Post_WithBlocks` PASS + live UAT (sandbox `learn-14f484c7`) |
| BRDG-03 | 74-02 | Canonical JSON `"blocks"` between `"action"`/`"body"` | SATISFIED | `TestCanonicalJSON_BlocksOrdering` PASS |
| HOOK-01 | 74-02 | `_km_stream_drain` argv includes `--render` + (Task 6 expansion) inbound poller reply also includes `--render` | SATISFIED | `TestStreamDrain_RenderFlag` PASS; `TestSlackInboundPoller_ReplyPost_RenderFlag` PASS (NEW) |

**No ORPHANED requirements.** All IDs listed in 74-VALIDATION.md appear in at least one plan's `requirements:` frontmatter.

### Anti-Patterns Found

| File | Pattern | Severity | Notes |
|------|---------|----------|-------|
| `cmd/km-slack/main_test.go:188` | `TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0` failing | Info (pre-existing) | Documented in `deferred-items.md`. `PostToBridge` fail-fasts on 5xx by design to prevent nonce replay; test expectation is wrong, not the implementation. Predates Phase 74. |
| `pkg/compiler/userdata_notify_test.go` (6 tests) | `TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock` etc. | Info (pre-existing) | Documented in `deferred-items.md`. Predate Phase 74 (last touched in Phase 63-04 per git log). |

No blocker anti-patterns. No stubs, no TODO/FIXME, no placeholder implementations in Phase 74 scope. The new Task 6 inbound poller change is substantive — actual `--render` flag added to bash with bound-to-context regression test asserting position relative to `--body`.

### Human Verification

The previous verification flagged two human-only items. UAT settled the primary one:

| # | Test | Status |
|---|------|--------|
| 1 | Live Slack Block Kit rendering in #sb-`<id>` channel | SATISFIED — operator confirmed against sandbox `learn-14f484c7` on 2026-05-15 |
| 2 | `KM_SLACK_RENDER=plain` downgrade without redeploy | NOT YET EXERCISED — code-path covered by `TestRunWith_EnvOverride`; live downgrade not part of this UAT. NOT BLOCKING — the env-var override mechanism is unit-tested; this is an operational safety valve, not a phase deliverable. |

### Gaps Summary

**No gaps.** Phase 74 is complete. The Task 6 scope expansion is a substantive HOOK-01 widening — it adds an artifact (test), modifies an artifact (userdata.go inbound poller bash), and is fully covered by a passing regression test plus operator UAT confirmation.

### Test Suite Status

```
go test ./pkg/slack/... ./cmd/km-slack/... ./pkg/compiler/...
- pkg/slack:           PASS  (all REND-* + BLK-* + BRDG-03 + corpus)
- pkg/slack/bridge:    PASS  (BRDG-01 + BRDG-02 + canonical signing)
- cmd/km-slack:        PASS except pre-existing TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0 (out of scope)
- pkg/compiler HOOK-01:
    TestStreamDrain_RenderFlag                          PASS
    TestSlackInboundPoller_ReplyPost_RenderFlag (NEW)   PASS
```

---

_Re-verified: 2026-05-15T23:10:00Z_
_Previous verification: 2026-05-10T14:43:49Z (14/14, passed)_
_Scope expansion: Task 6 / commit 8f983db (Slack-inbound poller HOOK-01 coverage)_
_UAT sign-off: 2026-05-15 against sandbox learn-14f484c7_
_Verifier: Claude (gsd-verifier)_
