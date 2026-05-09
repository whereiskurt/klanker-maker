---
phase: 74-slack-mrkdwn-rendering-tokenizer-based-markdown-slack-mrkdwn-transformer-optional-block-kit-tier-for-streaming-hook-output
plan: 01
subsystem: slack-rendering
tags: [slack, mrkdwn, tokenizer, markdown, rendering, phase74]
dependency_graph:
  requires: [pkg/slack/payload.go, cmd/km-slack/main.go]
  provides: [pkg/slack/Mrkdwnify, pkg/slack/MaxRenderedBytes, cmd/km-slack/--render-flag]
  affects: [cmd/km-slack/main.go, pkg/slack/mrkdwn.go]
tech_stack:
  added: [regexp, testing/quick, testing.F (fuzz)]
  patterns: [three-segment-tokenizer, fail-soft-recover, idempotent-transforms, placeholder-extraction]
key_files:
  created:
    - pkg/slack/mrkdwn.go (298 lines)
    - pkg/slack/mrkdwn_test.go (320 lines)
    - pkg/slack/testdata/bold-collapse.md + .expected-mrkdwn.txt
    - pkg/slack/testdata/code-fence-passthrough.md + .expected-mrkdwn.txt
    - pkg/slack/testdata/heading-map.md + .expected-mrkdwn.txt
    - pkg/slack/testdata/hrule-drop.md + .expected-mrkdwn.txt
    - pkg/slack/testdata/html-escape.md + .expected-mrkdwn.txt
    - pkg/slack/testdata/idempotent-already-mrkdwn.md + .expected-mrkdwn.txt
    - pkg/slack/testdata/link-conversion.md + .expected-mrkdwn.txt
    - pkg/slack/testdata/pipe-table.md + .expected-mrkdwn.txt
    - pkg/slack/testdata/strike.md + .expected-mrkdwn.txt
    - pkg/slack/testdata/tool-lines.md + .expected-mrkdwn.txt
    - pkg/slack/testdata/fuzz/FuzzMrkdwnify/seed001
  modified:
    - pkg/slack/payload.go (added MaxRenderedBytes = 35*1024)
    - cmd/km-slack/main.go (--render flag, renderMode threading, overflow logic)
    - cmd/km-slack/main_test.go (updated runWith callers + REND-14..16 real tests)
decisions:
  - "Transform order changed from plan spec: mapHeadings runs BEFORE collapseBold for idempotence. Reason: '# *foo*' creates '**foo**' which must then be collapsed in the same pass."
  - "Placeholder strategy uses long NUL-delimited tokens (KMHTML_, KMBOLD_, etc.) to prevent accidental formation by adjacent text. Short \\x00p%d\\x00 format caused collisions."
  - "Triple *** and ~~~ protected before bold/strike collapse via reBoldTriple/reStrikeTriple regexes, then restored, preventing partial matching that breaks idempotence."
  - "Existing Slack links extracted at applyText level (not just htmlEscape) to prevent convertLinks from re-matching <url|label> on second pass."
  - "TestKmSlackPost_BodyTooLarge_ExitsBeforeHttp renamed to TestKmSlackPost_BodyTooLarge_TruncatedAndSent to reflect new overflow behavior (truncate, not reject)."
metrics:
  duration: "28 minutes"
  completed_date: "2026-05-09"
  tasks_completed: 3
  files_changed: 27
---

# Phase 74 Plan 01: Mrkdwnify Tokenizer + Tier 1 Transforms Summary

**One-liner:** Tokenizer-based markdown-to-Slack-mrkdwn transformer with fail-soft recover(), idempotent Tier 1 transforms, corpus/property/fuzz test moat, and --render flag wiring on km-slack post.

## What Was Built

### pkg/slack/mrkdwn.go — Core Renderer

Three-segment tokenizer + seven Tier 1 transforms + fail-soft entry point.

**Tokenizer design:** Hand-rolled byte scanner. Code-fence detection takes priority over code-span (scans for ``` lines first). Segments: `segText`, `segCodeSpan`, `segCodeFence`. Transforms run ONLY on text segments; code content passes through byte-for-byte.

**Transform order (actual, with deviation from plan):**
1. Extract existing Slack links as placeholders (idempotence protection)
2. `htmlEscape` — preserve existing `&lt;`/`&gt;`/`&amp;` and Slack links
3. `convertLinks` — `[label](url)` → `<url|label>`
4. `mapHeadings` — `# h` / `## h` / `### h` → `*h*` (BEFORE collapseBold)
5. `collapseBold` — `**x**` → `*x*` (after headings, so `***x***` partial matches collapse)
6. `collapseStrike` — `~~x~~` → `~x~`
7. `dropHRules` — removes `---` / `***` / `___` lines
8. `fencePipeTables` — wraps runs of ≥2 pipe lines in triple-backtick fences
9. Restore Slack link placeholders

**Idempotence:** Achieved through multi-layer placeholder extraction. Key insight: `reSlackLink` extracted at `applyText` entry, not just inside `htmlEscape`, prevents `convertLinks` from re-processing `<url|label>` on second pass.

### pkg/slack/payload.go — MaxRenderedBytes

Added `const MaxRenderedBytes = 35 * 1024` adjacent to `MaxBodyBytes`. No other changes.

### cmd/km-slack/main.go — --render Flag

- `runPost` parses `--render=plain|mrkdwn|blocks` with precedence: explicit flag > `KM_SLACK_RENDER` env > `"plain"`
- Unknown values fall back to `"plain"` with stderr warning
- `renderMode` threaded through `run` → `runWith` as new final string parameter
- `runWith` calls `slack.Mrkdwnify` when `renderMode == "mrkdwn"`, plain is no-op
- Overflow: `len(rendered) > MaxRenderedBytes` → truncate to MaxRenderedBytes + `"\n_…truncated; see full transcript at Stop_"` footer
- Existing `MaxBodyBytes` check retained as defense-in-depth after truncation
- `"blocks"` accepted but treated as `"plain"` until Plan 74-02

### Test Results: REND-01..REND-16

| Req | Test | Status |
|-----|------|--------|
| REND-01 | TestMrkdwnify_Bold | PASS |
| REND-02 | TestMrkdwnify_Heading | PASS |
| REND-03 | TestMrkdwnify_Link | PASS |
| REND-04 | TestMrkdwnify_Strike | PASS |
| REND-05 | TestMrkdwnify_HTMLEscape | PASS |
| REND-06 | TestMrkdwnify_HRule | PASS |
| REND-07 | TestMrkdwnify_PipeTable | PASS |
| REND-08 | TestCodeFencePreservation | PASS |
| REND-09 | TestCodeSpanPreservation | PASS |
| REND-10 | TestMrkdwnifyIdempotent | PASS |
| REND-11 | FuzzMrkdwnify (30s) | PASS — 0 new corpus entries |
| REND-12 | TestMrkdwnifyCorpus | PASS — 10 fixtures |
| REND-13 | TestMrkdwnify_FailSoft | PASS |
| REND-14 | TestRunWith_Overflow | PASS |
| REND-15 | TestRunWith_Plain | PASS |
| REND-16 | TestRunWith_EnvOverride | PASS (2 subtests) |

### Corpus Fixtures (10 pairs)

All in `pkg/slack/testdata/`:
- bold-collapse, code-fence-passthrough, heading-map, hrule-drop, html-escape
- idempotent-already-mrkdwn, link-conversion, pipe-table, strike, tool-lines

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Transform order changed: mapHeadings before collapseBold**
- **Found during:** Task 1/2 implementation (fuzz discovered)
- **Issue:** Plan specifies collapseBold before mapHeadings. Input `# *0*` produces `**0**` after heading transform (wrapping content `*0*` with `*`). Second pass then collapses `**0**` → `*0*`. Not idempotent.
- **Fix:** Moved mapHeadings before collapseBold so heading-created `**` is collapsed in the same pass.
- **Impact:** Same final output; only ordering changed. Plan's example fixtures unaffected.

**2. [Rule 1 - Bug] Long placeholder tokens to prevent collision**
- **Found during:** Fuzz run (input `&&p0&` triggered `\x00p0\x00` collision)
- **Issue:** Short placeholders like `\x00p0\x00` can be formed by adjacent placeholder suffixes + literal text (`\x00p1\x00` + `p0` + `\x00` creates fake `\x00p0\x00`).
- **Fix:** Changed to long unique tokens: `\x00KMHTML_0_KMHTML\x00`, `\x00KMBOLD_0_KMBOLD\x00`, `\x00KMLINK_0_KMLINK\x00`.

**3. [Rule 1 - Bug] Triple asterisk/tilde protection**
- **Found during:** Fuzz run (`***x***` → `**x**` → `*x*` across passes)
- **Issue:** `\*\*([^*]+?)\*\*` partially matches inner `**` of `***x***`, leaving one extra `*` on each side, creating a new `**` pattern on the next pass.
- **Fix:** Added `reBoldTriple` and `reStrikeTriple` to protect 3+ sequences before collapse.

**4. [Rule 1 - Bug] Slack link extraction at applyText level**
- **Found during:** Fuzz run (`[)]([\0](` case)
- **Issue:** `htmlEscape` extracted Slack links, but after restoring them, `convertLinks` still saw them and re-matched markdown inside the Slack link syntax.
- **Fix:** Extract Slack links at `applyText` entry before any transform runs; restore after all transforms.

**5. [Rule 1 - Bug] Updated BodyTooLarge test**
- **Found during:** Task 3 (test failure)
- **Issue:** Pre-existing test `TestKmSlackPost_BodyTooLarge_ExitsBeforeHttp` expected 40KB+1 to be rejected. New overflow path truncates instead of rejecting.
- **Fix:** Renamed and rewrote to `TestKmSlackPost_BodyTooLarge_TruncatedAndSent` verifying the new behavior.

### Pre-existing Issues (Out of Scope)

**TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0** was already failing before Plan 74-01. Documented in `deferred-items.md`. Root cause: test expects 5xx retry but `PostToBridge` fail-fasts on 5xx (by design, to prevent nonce-replay). Test expectation is wrong.

`pkg/compiler` tests also pre-exist as failures (confirmed via git stash).

## Confirmation: No Phase 62/63/67/68 Changes

- `pkg/compiler/userdata.go` — NOT touched (streaming hook flip is Plan 74-02's job)
- `pkg/slack/client.go` — NOT touched
- `pkg/slack/bridge/` — NOT touched
- Existing Phase 62/63/68 callers all use `km-slack post --body file` without `--render` → default `"plain"` → no behavior change

## PR1 Ready

All REND-01..REND-16 green. 30-second fuzz run clean. `go vet` clean. `make build` succeeds.

Soak `--render=mrkdwn` in production before Plan 74-02 (PR2) lands Block Kit.

## Self-Check: PASSED

- pkg/slack/mrkdwn.go: FOUND
- pkg/slack/mrkdwn_test.go: FOUND
- pkg/slack/payload.go MaxRenderedBytes: FOUND
- pkg/slack/testdata/: FOUND (10 fixture pairs)
- pkg/slack/testdata/fuzz/FuzzMrkdwnify/seed001: FOUND
- commit 913208b (Tasks 1+2): FOUND
- commit 9cef187 (Task 3): FOUND
