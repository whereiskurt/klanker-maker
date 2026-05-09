# Phase 74: Slack mrkdwn rendering - Context

**Gathered:** 2026-05-08
**Status:** Ready for planning

<domain>
## Phase Boundary

A robust markdown→Slack renderer used by the Phase 68 transcript streaming hook so Claude's assistant prose looks intentional in Slack threads instead of leaking literal `***heading***`, dropped `# headings`, and broken pipe-tables. Two tiers:

- **Tier 1 (mrkdwn)** — text-mode transformer that converts CommonMark-ish input into valid Slack mrkdwn. Used for `--render=mrkdwn` and as fallback when Tier 2 fails.
- **Tier 2 (Block Kit)** — structured `header` / `section` / `context` / `divider` blocks for the streaming hook. Used for `--render=blocks`.

Backward compatibility is preserved: existing Phase 62/63 idle-ping callers stay on `--render=plain` (default) and see no behavior change. Only the Phase 68 transcript streaming hook in `pkg/compiler/userdata.go` flips to `--render=blocks`.

**In scope:** the renderer (`pkg/slack/mrkdwn.go`, `pkg/slack/blocks.go`), the `--render` flag wiring on `km-slack post`, the `KM_SLACK_RENDER` env safety valve, the streaming-hook integration, and the test moat (unit + property + fuzz + corpus fixtures + bridge integration).

**Out of scope:** any change to `chat.update` edit-in-place semantics, retroactive re-rendering of historical Slack messages, the gzipped JSONL transcript upload at Stop (separate file API path, unaffected), tool-output normalization beyond the existing hook formatting, italic markdown handling (`*x*` → `_x_` explicitly skipped — see Decisions).

</domain>

<decisions>
## Implementation Decisions

### Architecture (locked from conversation)

- **Tokenizer-based pipeline, not regex-on-whole-text.** Walk input once, split into segments: `text` / `code-span` (single-backtick) / `code-fence` (triple-backtick). Transforms run on `text` segments only. Code samples that contain `**`, `|...|`, or `<html>` are returned byte-for-byte. This is the single biggest correctness investment.
- **Idempotent.** `Mrkdwnify(Mrkdwnify(x)) == Mrkdwnify(x)`. Already-mrkdwn input (`*x*`, `_x_`, `~x~`) passes through unchanged. Run-twice safety is a property test.
- **Fail-soft.** Whole pipeline wrapped in `recover()`. If the transformer panics or any phase errors, fall back to writing the original body unchanged. Never crash the streaming hook.
- **`--render=plain|mrkdwn|blocks` flag** on `km-slack post`. Default `plain` so existing Phase 62/63 callers are unchanged. Streaming hook in `pkg/compiler/userdata.go` flips to `--render=blocks`.
- **`KM_SLACK_RENDER` env safety valve** — operator can downgrade in production without redeploy if a regression turns up.
- **Two-PR phasing.** PR1: tokenizer + Tier 1 mrkdwn + tests + `--render=mrkdwn` flag, hook flips to `mrkdwn`. Soak in production. PR2: Tier 2 Block Kit on top, hook flips to `blocks`.

### Tier 1 mrkdwn transforms

Applied to `text` segments only (code-span and code-fence segments pass through untouched):

- `**x**` → `*x*` (markdown bold → Slack bold)
- `# h` / `## h` / `### h` → `*h*` (ATX headings → bold line)
- `[label](url)` → `<url|label>` (markdown link → Slack link)
- `~~x~~` → `~x~` (strikethrough)
- HTML-escape `&`, `<`, `>` in text segments (NOT inside code) before any other transform — required because `<foo>` in plain text gets interpreted as a Slack link/mention.
- Drop `---` / `***` / `___` horizontal rules in plain text mode (Slack ignores them; they look like noise).
- Wrap pipe-table runs (≥2 contiguous lines matching `^\s*\|.*\|\s*$`) in triple-backtick fences for monospace alignment. Solo `|...|` lines are left alone (no false positives on bullet text containing pipes).
- **Italic explicitly skipped.** No `*x*` → `_x_` conversion. Distinguishing italic from leftover bold artifacts requires lookarounds Go regex doesn't support, and a state machine adds complexity for marginal upside — Claude usually emits `_x_` natively which already passes through Slack mrkdwn.

### Tier 2 Block Kit transforms

Built on top of Tier 1 — section text uses Tier 1 mrkdwn output; structural elements get dedicated blocks.

**Heading mapping:**
- `# h` → `header` block (Slack's large bold sans, 150-char cap, plain text only — no inline formatting inside)
- `## h` and `### h` → bold `*h*` line inside a `section` block (preserves inline formatting, no stacked-banner visual chaos)
- Heading > 150 chars → hard-truncate at 147 chars, append `…` (matches Slack's own truncation semantics)

**Tool one-liner handling:**
- Detection: prefix regex `^🔧 \w+: ` matches lines the Phase 68 hook already writes. No hook changes needed; the hook stays the source of truth.
- Render in Block Kit mode: each tool line becomes a Slack `context` block (smaller gray text — semantically the right fit for "metadata" lines, gives clear visual separation between Claude's thinking and Claude's actions).
- Transforms: HTML-escape `<` / `>` / `&` only. No other transforms (no bold collapse, no pipe-table fence, no link conversion). Hook already pre-formatted these; further transforms only risk corruption.

**Dividers:**
- Insert `divider` block ONLY on explicit `---` markdown rules. Honors author intent.
- No auto-dividers between header/section/context blocks — produces visual chaos when Claude alternates types rapidly.

**Section sizing:**
- Per-section text capped at 3000 chars (Slack limit). Long paragraphs auto-split into multiple section blocks at the nearest paragraph boundary, then sentence boundary, then char boundary as last resort.
- Whole-message capped at 50 blocks (Slack limit). If exceeded, fall back to Tier 1 mrkdwn for that post.

**Fallback `text:` field (required by Slack alongside `blocks:`):**
- Plain-text rendering of the whole body — strip all markdown/mrkdwn syntax (backticks, asterisks, headings, links collapsed to label text). Best search experience: a search for `vol-stale1` finds the message regardless of how it was formatted in blocks.

### Body-overflow strategy

- Trigger threshold: **35KB on the rendered body** (5KB headroom under Slack's 40KB cap for Block Kit JSON envelope, the truncation footer itself, and signing-envelope wrapping).
- Behavior: hard-truncate to threshold, append `_…truncated; see full transcript at Stop_` as the last block (Block Kit) or last line (mrkdwn). User always sees something in Slack; full data is preserved in the gzipped JSONL transcript that Stop uploads via the file API.
- Never silently drop a chunk; never split into multiple thread replies (rate-limit + ordering risk).

### Test strategy (the robustness moat)

Robustness is the priority — the test investment is non-negotiable.

- **Unit tests** for each transform (bold collapse, heading map, link conversion, table-fence, HTML escape, etc.).
- **Fixture corpus**: `pkg/slack/testdata/*.md` with paired `*.expected-mrkdwn.txt` and `*.expected-blocks.json`. Every Claude quirk we trip over in production becomes a fixture.
- **Property tests:**
  - Idempotence: `Mrkdwnify(Mrkdwnify(x)) == Mrkdwnify(x)`
  - Code-block byte-preservation: text inside ` ``` ` and `` ` `` survives every transform unchanged
  - Block Kit output validates against the public Slack block-kit JSON schema (no invalid `type` fields, no exceeded limits)
- **Go fuzz target** on `Mrkdwnify` — `testing.F`, runs on every PR.
- **Bridge integration test** — post each render mode through a real bridge to a sandbox channel, assert no `chat.postMessage` API errors and visible block structure roughly matches expectation.

### Layout

```
pkg/slack/
  mrkdwn.go              # Tokenizer + Tier 1 mrkdwn transformer
  mrkdwn_test.go
  blocks.go              # Tier 2 Block Kit builder (uses mrkdwn.go for section text)
  blocks_test.go
  testdata/              # Corpus fixtures (.md + expected outputs)
cmd/km-slack/main.go     # --render=plain|mrkdwn|blocks flag
pkg/compiler/userdata.go # Streaming hook flips --render=blocks
```

### Claude's Discretion

- Exact tokenizer state-machine implementation (regex pipeline vs hand-rolled scanner)
- Specific Go fuzz seed corpus
- Whether to memoize plain-text rendering for the `text:` fallback or recompute
- Internal naming of helper functions and intermediate types
- Whether to expose the renderer as exported API (`slack.Render`) or keep internal
- Bridge integration test sandbox provisioning (use existing test fixtures vs new)

</decisions>

<specifics>
## Specific Ideas

- **Today's failure mode in production:** `***heading***` (literal asterisks visible because Slack mrkdwn renders the inner `*x*` as bold but treats outer asterisks as literal), `# heading` lines render as plain text with the `#` visible, pipe-tables render in proportional font with column drift.
- **Reference behaviors that should "just work":**
  - Code samples in `pkg/...go` style that contain `**` (Go pointer dereference) — must survive byte-for-byte
  - Filenames like `a|b.go` in tool one-liners — must NOT trigger pipe-table fence (single-line heuristic exclusion + tool-line bypass both protect)
  - Claude output like `<html>` or `<channel-id>` in prose — must be HTML-escaped to render as visible text, not interpreted as Slack link/mention
  - `_italic_` and `*bold*` already-mrkdwn input — passes through unchanged (idempotence)
- **`text:` field semantics:** Slack uses `text` for push notifications (mobile preview), email digests, screen readers, and full-text search. Plain-text rendering of the whole body wins on search; mobile gets auto-truncated by Slack regardless.
- **The `header` block oddity:** Slack's `header` block is plain-text only — no inline `code`, no links, no `*bold*` inside. So a heading like `# The \`foo\` function` either drops the backticks (rendering "The foo function" big-bold) or falls back to a `*The \`foo\` function*` section. We hard-strip backticks/asterisks/underscores from `header` block text to keep behavior predictable.

</specifics>

<code_context>
## Existing Code Insights

### Reusable Assets

- **`pkg/slack/client.go:121` `PostMessage`** — already builds a payload with `text:` and `mrkdwn: true`. Easy extension point: add optional `Blocks []any` field on a new `PostMessageOpts` (or just accept JSON pre-built by the new renderer).
- **`pkg/slack/payload.go`** — existing envelope/signing flow handles `ActionPost` cleanly. Renderer slots in upstream of envelope construction; signing path doesn't change.
- **`cmd/km-slack/main.go runWith` (line 280)** — the natural insertion point. Read body file, then run through renderer based on `--render` flag, then build envelope. The fail-soft `recover()` wraps just the renderer call.
- **`pkg/compiler/userdata.go` `_km_stream_drain` (line 595)** — single hook function that calls `km-slack post`. Switching it to `--render=blocks` is a one-line bash change.

### Established Patterns

- **`pkg/slack` keeps wire-format helpers; `cmd/km-slack` is the thin CLI shim.** Renderer belongs in `pkg/slack` so the bridge tests can exercise it directly.
- **Project test conventions** (`AGENTS.md`, `CLAUDE.md`): table-driven tests with explicit fixtures, property tests via `testing/quick`, fuzz targets via `testing.F`. The corpus pattern (`testdata/*.md` + `*.expected.txt`) is already used in `pkg/profile/testdata` for profile validation.
- **Pre-existing 40KB cap** is enforced via `slack.MaxBodyBytes` constant (referenced at `cmd/km-slack/main.go:292`). New overflow logic uses the same constant; renderer adds a `MaxRenderedBytes = 35000` sibling.
- **Phase 62/63/68 hook callers** all use `km-slack post --body file --thread ts`. The `--render` flag is purely additive; default `plain` is a no-op for them.

### Integration Points

- `cmd/km-slack/main.go runPost` — add `--render` flag wiring + env fallback to `KM_SLACK_RENDER`
- `cmd/km-slack/main.go runWith` — call renderer with mode, recover on panic, swap result for original body, then build envelope
- `pkg/compiler/userdata.go _km_stream_drain` — append `--render=blocks` to the `km-slack post` argv
- `pkg/slack/payload.go BuildEnvelope` — accept optional pre-built blocks JSON OR keep current text-only envelope and have the bridge translate at post time (decision deferred to research/planning — both work, latter is bigger blast radius)

</code_context>

<deferred>
## Deferred Ideas

- **Italic markdown handling (`*x*` → `_x_`).** Explicitly out of scope for this phase. Re-visit only if Claude's output style shifts to single-asterisk italic (currently rare).
- **Retroactive re-rendering of historical Slack messages.** Out of scope — no `chat.update` rewrite path.
- **`rich_text` blocks instead of `section`+mrkdwn.** Slack's newer `rich_text` block supports proper inline code, ordered/unordered lists, and code blocks as first-class elements. More fidelity than `section`+mrkdwn but significantly harder to construct (deep nested JSON structure, no public schema doc). Punt to a future phase if `section`+mrkdwn proves limiting.
- **Tool-line normalization beyond the hook's existing format.** The transformer treats `🔧 ToolName: input` as opaque. Future phase could parse tool input deeper and render structured (e.g., `Edit /path/to/file (line 42)` instead of raw JSON).
- **Slack message-edit (`chat.update`) for live-streaming a single growing message.** Currently each PostToolUse fire creates a new message in the thread. Future phase could update a single "in-flight" message instead.
- **Rate-limit aware multi-post for very long turns.** Today's truncation strategy preserves data in the S3 transcript. If users complain about losing inline visibility of long turns, a multi-post path could split at clean boundaries with rate-limit backoff.

</deferred>

---

*Phase: 74-slack-mrkdwn-rendering-…*
*Context gathered: 2026-05-08*
