# Research: Slack `markdown` block + native tables for Claude output

**Date:** 2026-06-14
**Author:** Claude (research session, autonomous)
**Status:** Pre-decision research / design proposal — not a committed phase
**Trigger:** Operator asked whether the new Slack `markdown` block could replace our
CommonMark→mrkdwn reflow, with a specific focus on **getting real tables to render**.

---

## TL;DR / recommendation

1. **Yes, this is worth doing.** Slack shipped two GA-quality primitives that directly
   target "render an LLM's markdown nicely": the **`markdown` block** (Feb 2025) and a
   **dedicated `table` block** (Aug 2025). Both are usable from `chat.postMessage` today.
2. **The single most important finding for the table question:** the *reliable* way to get a
   real visual table is **NOT** a pipe table inside a `markdown` block — it's the **dedicated
   `table` block** with structured rows/cells. The markdown block *claims* to render pipe
   tables, but multiple real-world AI-agent integrations found it inconsistent enough that
   they parse pipe tables out and emit `table` blocks (or fall back to code blocks). So our
   table path should **parse Claude's GFM pipe tables into `table` blocks.**
3. **Recommended shape:** a new render tier (`blocks-rich`) in `pkg/slack/blocks.go` that emits
   **`markdown` blocks for prose + `table` blocks for tables + our existing `context` footer**,
   with automatic fallback to today's Tier-2 (mrkdwn sections) and Tier-1 (mrkdwn text). This
   slots into our existing tiered architecture and lets us *delete* most of the mrkdwn-reflow
   machinery on the happy path while keeping the golden-tested fallbacks intact.
4. **Do NOT use the `markdown_text` top-level param.** It's the simplest path (send raw markdown,
   no block-building) but it is **mutually exclusive with `blocks` and `text`** — which kills our
   header + AI-disclaimer-context-footer pattern and our required plain-text fallback. It's a
   non-starter for our multi-block messages.

---

## 1. What Slack actually offers now (verified against docs.slack.dev, June 2026)

There are **four** distinct mechanisms. They are easy to conflate; they are not the same thing.

| Mechanism | What it is | Char limit | Tables? | Mutually exclusive w/ blocks? |
|---|---|---|---|---|
| **legacy `mrkdwn`** (section block text) | Slack's proprietary subset (what we emit today) | 3,000 / section | **No** — pipes render as raw text | n/a (it *is* a block) |
| **`markdown` block** (`type:"markdown"`) | GFM-ish markdown rendered by Slack | **12,000 cumulative** across all markdown blocks in the payload | Claimed yes, **unreliable in practice** | No — it's a block, composes with others |
| **`table` block** (`type:"table"`) | Structured rows/cells, real grid | per-cell limits; ≤100 rows × ≤20 cols | **Yes (reliable)** | No — it's a block |
| **`markdown_text` param** (chat.postMessage / streaming) | Send raw markdown, Slack builds blocks for you | 12,000 | Same as markdown block | **YES — conflicts with `blocks` AND `text`** (`markdown_text_conflict` error) |

### 1a. The `markdown` block (GA Feb 3 2025)

Schema (`docs.slack.dev/reference/block-kit/blocks/markdown-block/`):

```json
{ "type": "markdown", "text": "## Heading\n\n**bold**, _italic_, `code`, [link](https://x)" }
```

- `text` is required; `block_id` is **ignored** (not retained).
- **Cumulative 12,000-char limit** across *all* markdown blocks in one payload (vs 3,000/section).
- Supported: bold, italic, strikethrough, links, ordered + unordered lists, **headers**, inline
  code, fenced code blocks **with syntax highlighting**, blockquotes, horizontal rules, task lists
  (`- [ ]` / `- [x]`), and "tables (renders as a formatted table)".
- **Headers render all at the same size** — `#`, `##`, `###` are visually identical. (Important: we
  lose heading hierarchy. Mitigation below.)
- **Images become hyperlink text**, not embedded images.
- "Passing a single block may result in **multiple** blocks after translation" — Slack re-splits
  internally, so block-count math is fuzzy.
- Explicitly designed "for apps that use platform AI features … expecting LLM-generated markdown."
  This is *exactly* our use case.

### 1b. The `table` block (GA Aug 14 2025; "data table" variant May 20 2026)

Schema (`docs.slack.dev/reference/block-kit/blocks/table-block/`):

```json
{
  "type": "table",
  "column_settings": [ {"align": "left"}, {"align": "left"}, {"align": "left"} ],
  "rows": [
    [ {"type":"rich_text", ...}, {"type":"raw_text","text":"Agency"}, {"type":"raw_text","text":"Role"} ],
    [ {"type":"raw_text","text":"Reid Wiseman"}, {"type":"raw_text","text":"NASA"}, {"type":"raw_text","text":"Commander"} ]
  ]
}
```

- `rows`: **max 100 rows**, each row **≤20 cells**.
- `column_settings` (≤20): per-column `align` (`left`/`center`/`right`, default left) + `is_wrapped`
  (bool, default false).
- Cell types: **`raw_text`** (plain), **`raw_number`** (numeric — aligns nicely), **`rich_text`**
  (bold, emoji, mentions, hyperlinks — but *not* full markdown / no code fences / no nested lists).
- Works in `chat.postMessage` via `blocks` (or `attachments`). You must still supply a top-level
  `blocks` or `text` value.
- A newer **"data table" block** (May 2026) is a richer/interactive variant — note for later, not
  needed for v1.

### 1c. `markdown_text` param + streaming API (Oct 2025) — context, not our path (yet)

- `chat.postMessage` accepts `markdown_text` to send raw markdown with **no block-building**, but it
  **cannot be combined with `blocks` or `text`** (returns `markdown_text_conflict`). Because we need a
  header, a context footer, and a required plain-text fallback, this is unusable for us.
- `chat.startStream` / `chat.appendStream` / `chat.stopStream` (Oct 2025) are the "proper" agent
  streaming primitives: append `markdown_text` chunks (or `blocks` chunks) into a single growing
  message, plus interactive `feedback_buttons` / `icon_button` / `context_actions`. SDK `streamer`
  helper exists. **This is a much bigger architectural change** than our current "post one discrete
  message per turn" model — flagged as a future direction in §6, not part of the v1 proposal.

---

## 2. The table question, answered

**Q: Can we make Claude's markdown tables render as real tables in Slack?**

**A: Yes — but via the `table` block, not via pipe-tables-in-a-markdown-block.**

Two paths exist:

- **Path A — pipe table inside a `markdown` block.** The official block reference says it "renders as
  a formatted table." But real-world AI-agent harnesses (OpenClaw, hermes-agent — see Sources) report
  the markdown-block table path is inconsistent across surfaces and rollouts; their config options for
  tables are "bullets / code / off," and they actively request/use the dedicated `table` block instead.
  Pipe tables also **never** render in email notifications and may differ on mobile. **Verdict: not
  reliable enough to depend on.**

- **Path B — dedicated `table` block.** Deterministic visual grid, column alignment, mobile-aware
  wrapping. **Verdict: this is the path.** Cost: we must *parse* Claude's GFM pipe tables into
  structured row/cell objects ourselves (we already detect & reflow them today, so the detection half
  is done — we swap the monospace-reflow output for a `table` block).

This is the crux of the work: **a GFM-pipe-table → Slack-`table`-block transformer.**

### 2a. Sketch of the transformer

Input (what Claude emits):

```
| Astronaut     | Agency | Role              |
|---------------|:------:|-------------------|
| Reid Wiseman  | NASA   | Commander         |
| Victor Glover | NASA   | Pilot             |
```

Mapping rules:
- **Delimiter row** (`|---|:--:|--:|`) → drives `column_settings[].align` (`:--:`=center, `--:`=right,
  else left). Drop the delimiter row itself.
- **Header row** → first `rows[]` entry; render cells as `rich_text` **bold** so headers stand out
  (the table block has no explicit "header row" flag in the v1 schema).
- **Body rows** → one `rows[]` entry each. Per cell:
  - pure number → `raw_number` (right-aligns cleanly),
  - inline formatting present (bold / link / code) → `rich_text`,
  - else → `raw_text`.
- **Guards:** ≤20 columns (truncate extra cols + log), ≤100 rows (paginate into multiple table blocks,
  or fall back to monospace reflow for giant tables), pad ragged rows to column count, cells get no
  multi-block content (code fence / nested list inside a cell → degrade to `raw_text`).

---

## 3. How this maps onto OUR pipeline

### 3a. Today (verified in `pkg/slack/blocks.go`, `pkg/slack/payload.go`)

- `RenderBlocks(input) -> (blocksJSON, fallbackText, ok)` walks CommonMark-ish lines and emits four
  block types: `header` (H1), `section` (mrkdwn, H2/H3 as bold prefix), `context` (🔧 tool lines),
  `divider` (hr). Tables are **reflowed to monospace text inside mrkdwn** via `fencePipeTables`
  (`mrkdwn.go`), not rendered as tables.
- Hard caps in code: **50 blocks** (`maxBlocks`), **3,000 chars/section** (`maxSectionChars`), with
  `splitSection` + `rebalanceFences` doing the chunking. `ok==false` → caller falls back to Tier-1
  `Mrkdwnify`.
- The result rides in `SlackEnvelope.Blocks` (pre-serialized JSON string, signed). Bridge dispatch
  type-asserts `BlockPoster` and calls `PostMessageBlocks(ctx, channel, subject, body, blocks, threadTS)`
  which sends **both** `text` (fallback) **and** `blocks`. If `Blocks==""` or the adapter isn't a
  `BlockPoster`, it degrades to plain `PostMessage` (BRDG-01).
- **All Slack output flows through `RenderBlocks`:** the `km-slack post` helper, the Phase-68
  transcript-streaming hook (`--render "${KM_SLACK_RENDER:-blocks}"`), and the bridge inbound-poller
  reply. **Upgrading `blocks.go` upgrades every path at once.** That's the leverage point.

### 3b. Proposed (new Tier-3 `blocks-rich`)

Add a third renderer alongside `RenderBlocks`, selected by `KM_SLACK_RENDER=blocks-rich` (and
eventually the default once proven):

```
renderRich(input) -> (blocksJSON, fallbackText, ok)
  - split input into segments at table boundaries (reuse existing pipe-table detection)
  - prose segment            -> { "type": "markdown", "text": <verbatim GFM segment> }
                                 (chunk at 12,000-char cumulative cap; multiple markdown blocks)
  - GFM table segment        -> { "type": "table", ... }  (§2a transformer; guards on rows/cols)
  - leading H1 (optional)    -> keep promoting to a real `header` block for visual hierarchy,
                                 since markdown-block headings are all one size
  - 🔧 tool lines            -> keep as `context` blocks (unchanged)
  - AI-disclaimer footer     -> `context` block (as in the operator's Block-Kit-Builder sample)
  - fallbackText             -> unchanged stripForFallback logic (still required for email/search/push)
  - ok=false  -> caller falls back to Tier-2 RenderBlocks, then Tier-1 Mrkdwnify
```

**What we get to delete on the happy path:** the mrkdwn reflow for headings/bold/links and the
monospace pipe-table reflow — Slack now does all of that natively. We *keep* those functions as the
Tier-2 fallback (don't rip them out; demote them).

**Caps change:** markdown blocks raise the effective text budget from 3,000/section to
12,000-cumulative, so most single-turn transcripts become **one** markdown block (+ table blocks)
instead of many split sections. Simpler, fewer blocks, less chance of hitting the 50-block cap.

---

## 4. Caveats & gotchas (read before committing)

1. **Heading hierarchy is flattened** in the markdown block (all `#` levels same size). Mitigation:
   promote a leading H1/H2 to a real `header` block; accept flattening for deeper levels.
2. **No inline images** — markdown-block images become link text. Same limitation we have today; no
   regression. (If we ever want hero images per the operator's `carousel` sample, that's `image`/`card`
   blocks — separate, structured, future work.)
3. **Table cells are not markdown.** Only `raw_text` / `raw_number` / `rich_text`. Code spans, nested
   lists, multi-line content inside a cell degrade to plain text. Fine for Claude's typical tables.
4. **Email notifications & search index** still see only the `text` fallback — keep populating it
   exactly as today. Tables won't render in email; that's acceptable (they don't today either).
5. **Surface variance / rollout.** markdown + table blocks are GA in mid-2026 but render slightly
   differently on mobile. Because we always send a `text` fallback and degrade `BlockPoster`→`PostMessage`,
   worst case is graceful. Still worth a real-device spot check during UAT.
6. **12,000-char cumulative cap** across markdown blocks — long transcript turns need chunking or a
   fall-through to Tier-2. The `MaxRenderedBytes` (35KB) / `MaxBodyBytes` (40KB) envelope caps still apply.
7. **Golden-test churn.** We have byte-identity golden tests on rendered output
   (`pkg/compiler/testdata/*golden*`). A new tier is additive (gate behind `blocks-rich`), so the
   default-path goldens don't move until we flip the default. Plan the flip as its own step.
8. **`table` block needs `BlockPoster` to pass blocks through verbatim.** It already does (it forwards
   the pre-serialized array), so no adapter change — but confirm the bridge doesn't re-validate/strip
   unknown block types.

---

## 5. Proposed phased rollout (GSD-shaped)

- **Phase A — renderer (pure `pkg/slack`).** Add `renderRich` + the GFM-table→`table`-block transformer
  + unit tests + a `--render blocks-rich` opt-in in `cmd/km-slack`. No deploy-surface change; sandbox
  helper is shipped as a sidecar binary. Golden tests added for the new tier only.
- **Phase B — opt-in wiring.** Honor `KM_SLACK_RENDER=blocks-rich` from `/etc/km/notify.env`; let an
  operator set it per-profile. UAT on a real workspace (desktop + mobile + email fallback). Document in
  `docs/slack-notifications.md` + the `klanker:slack` skill.
- **Phase C — flip default (optional, after soak).** Make `blocks-rich` the default render; demote
  Tier-2 to fallback; update default-path goldens in one reviewed commit.

Deploy notes: renderer lives in the **sandbox-side `km-slack` sidecar binary** + the bridge's
`PostMessageBlocks` (already block-agnostic). Expect **`make build-lambdas` is NOT needed** for the
renderer itself, but the `km-slack` sidecar binary ships via `km init --sidecars` /
`buildAndUploadSidecars`; existing sandboxes pick up the new binary on recreate (or sidecar refresh).
No SandboxProfile schema change, no new TF/DDB. (Verify against the Phase-92/108 sidecar-delivery
mechanics before finalizing.)

---

## 6. Future directions (out of scope for v1)

- **Native streaming** (`chat.startStream`/`appendStream`) to replace per-turn discrete posts — turns
  the transcript hook into a single growing message + `feedback_buttons`. Big change; revisit after the
  renderer lands.
- **`card`/`carousel`/hero-image** output (the operator's Block-Kit-Builder sample) for structured
  "sources" footers — only pays off for structured-output flows like the `deep-research` skill, which
  can emit citation data. Generic transcripts have no structured source list to render.
- **`raw_number` analytics tables** — if we ever post budget/usage tables, `raw_number` cells give clean
  right-aligned numeric columns for free.

---

## 7. Decisions (resolved 2026-06-14)

1. **Link anchors:** *Native links only.* The `markdown` block renders `[label](url)` as clickable
   anchors natively; bare URLs use Slack's default auto-link. No bare-URL auto-anchoring, no Sources
   footer in v1. (The operator's "clickable anchors" ask is satisfied for free by adopting the block.)
2. **Rollout:** *Opt-in first, flip later.* Phase 111 ships `blocks-rich` behind
   `KM_SLACK_RENDER=blocks-rich` (default stays `blocks` → no golden churn); Phase 112 flips the default
   after a real-workspace soak (desktop + mobile + email).
3. **Table-size policy:** cap at **one** `table` block; tables exceeding 100 rows / 20 cols fall back to
   today's monospace reflow. (Claude tables are almost always tiny.)
4. **AI-disclaimer footer:** *No footer by default; opt-in via `KM_SLACK_AI_FOOTER`* (per-profile in
   `/etc/km/notify.env`). Not on by default for anyone.
5. **Streaming:** *Deferred.* `chat.startStream`/feedback-buttons is a separate future phase; v1 keeps the
   per-turn discrete-post model and only upgrades rendering.

### Roadmap mapping
- **Phase 111** — Rich renderer (`renderRich`) + GFM-table→`table`-block transformer + `markdown`-block
  prose + `KM_SLACK_AI_FOOTER` flag + opt-in `KM_SLACK_RENDER=blocks-rich` + tests + docs. Existing
  Tier-2/Tier-1 demoted to fallback, not deleted. No TF/DDB/schema change.
- **Phase 112** — Flip default to `blocks-rich`, demote Tier-2, update default-path goldens (one reviewed
  commit). Gated on Phase 111 UAT.

---

## Sources

- [Markdown block — Slack docs](https://docs.slack.dev/reference/block-kit/blocks/markdown-block/)
- [Table block — Slack docs](https://docs.slack.dev/reference/block-kit/blocks/table-block/)
- [chat.postMessage (`markdown_text`) — Slack docs](https://docs.slack.dev/reference/methods/chat.postMessage/)
- [New Block Kit blocks + streaming updates (Apr 16 2026) — Slack changelog](https://docs.slack.dev/changelog/2026/04/16/block-kit-new-blocks/)
- [Chat streaming for AI agents (Oct 7 2025) — Slack changelog](https://docs.slack.dev/changelog/2025/10/7/chat-streaming/)
- [chat.appendStream — Slack docs](https://docs.slack.dev/reference/methods/chat.appendStream/)
- [How to render tables in Slack markdown — Knock](https://knock.app/blog/how-to-render-tables-in-slack-markdown)
- [OpenClaw issue #26660 — markdown.tables regression removes table-block path](https://github.com/openclaw/openclaw/issues/26660)
- [OpenClaw issue #4554 — convert markdown tables to Block Kit table blocks](https://github.com/openclaw/openclaw/issues/4554)
- [hermes-agent issue #18918 — render markdown pipe tables as Block Kit tables](https://github.com/NousResearch/hermes-agent/issues/18918)
- [tryfabric/mack — Markdown→Slack Block Kit (reference impl)](https://github.com/tryfabric/mack)
- Internal: `pkg/slack/blocks.go`, `pkg/slack/mrkdwn.go`, `pkg/slack/payload.go`,
  `pkg/slack/bridge/{handler,aws_adapters,interfaces}.go`, `skills/slack/SKILL.md`.
</content>
</invoke>
