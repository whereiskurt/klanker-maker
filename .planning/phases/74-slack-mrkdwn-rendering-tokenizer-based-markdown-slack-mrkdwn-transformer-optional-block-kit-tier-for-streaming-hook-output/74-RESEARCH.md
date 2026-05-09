# Phase 74: Slack mrkdwn Rendering — Research

**Researched:** 2026-05-09
**Domain:** Go text transformation, Slack mrkdwn/Block Kit, streaming hook integration
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- Tokenizer-based pipeline, not regex-on-whole-text. Walk input once, split into segments: `text` / `code-span` (single-backtick) / `code-fence` (triple-backtick). Transforms run on `text` segments only.
- Idempotent. `Mrkdwnify(Mrkdwnify(x)) == Mrkdwnify(x)`.
- Fail-soft. Whole pipeline wrapped in `recover()`. Never crashes the streaming hook.
- `--render=plain|mrkdwn|blocks` flag on `km-slack post`. Default `plain`. Streaming hook flips to `--render=blocks`.
- `KM_SLACK_RENDER` env safety valve.
- Two-PR phasing: PR1 = tokenizer + Tier 1 + `--render=mrkdwn` flag + tests. PR2 = Tier 2 Block Kit + `--render=blocks` flag + hook flip.
- Tier 1 transforms: `**x**` → `*x*`, `# h` / `## h` / `### h` → `*h*`, `[label](url)` → `<url|label>`, `~~x~~` → `~x~`, HTML-escape `&`/`<`/`>` in text only, drop `---`/`***`/`___` horizontal rules, wrap pipe-table runs in triple-backtick fences. Italic explicitly skipped.
- Tier 2 Block Kit: `# h` → `header` block (150-char plain-text cap), `## h`/`### h` → bold `*h*` in `section`, tool one-liner prefix `^🔧 \w+: ` → `context` block, `---` → `divider` block, section text capped at 3000 chars, 50-block message cap with Tier 1 fallback, fallback `text:` plain-text field required.
- Body overflow: 35KB trigger threshold, hard-truncate + `_…truncated; see full transcript at Stop_` footer. Never split into multiple replies.
- Layout: `pkg/slack/mrkdwn.go`, `pkg/slack/blocks.go`, `pkg/slack/testdata/`, `cmd/km-slack/main.go`, `pkg/compiler/userdata.go`.

### Claude's Discretion

- Exact tokenizer state-machine implementation (regex pipeline vs hand-rolled scanner)
- Specific Go fuzz seed corpus
- Whether to memoize plain-text rendering for the `text:` fallback or recompute
- Internal naming of helper functions and intermediate types
- Whether to expose the renderer as exported API (`slack.Render`) or keep internal
- Bridge integration test sandbox provisioning (use existing test fixtures vs new)

### Deferred Ideas (OUT OF SCOPE)

- Italic markdown handling (`*x*` → `_x_`)
- Retroactive re-rendering of historical Slack messages
- `rich_text` blocks instead of `section`+mrkdwn
- Tool-line normalization beyond the hook's existing format
- `chat.update` for live-streaming a single growing message
- Rate-limit aware multi-post for very long turns
</user_constraints>

---

## Summary

Phase 74 adds a tokenizer-based markdown-to-Slack transformer as a new `pkg/slack` subpackage, wired to `km-slack post` via a new `--render` flag. The integration surface is narrow and additive: the `runWith` function in `cmd/km-slack/main.go` reads the body file, runs it through the renderer, and passes the result to the existing `BuildEnvelope` + `SignEnvelope` + `PostToBridge` chain unchanged.

The bridge Lambda (`cmd/km-slack-bridge`) does **not** need changes for Tier 1 (mrkdwn). For Tier 2 (Block Kit), the bridge's `SlackPoster` interface and `SlackPosterAdapter.PostMessage` currently accept `(channel, subject, body, threadTS string)` — no `blocks` parameter. The Tier 2 path requires adding a `blocks`-aware post method to the `SlackPoster` interface and `SlackPosterAdapter`, and extending `SlackEnvelope` with a `Blocks` field. This is an additive envelope schema change (zero value is backward-compatible). The bridge handler dispatch path (`case slack.ActionPost`) must read the `Blocks` field and pass it to `chat.postMessage` when non-empty.

The test infrastructure follows project convention: table-driven unit tests, in-memory fake injection, fixture corpus at `pkg/slack/testdata/`. No existing fuzz targets exist in the repo — this phase introduces the first `testing.F` usage. `go test ./...` is the project standard (no `make test` target exists). Fuzz targets run in normal `go test ./...` mode against seed corpus; active fuzzing is `go test -fuzz=FuzzMrkdwnify ./pkg/slack/...`.

**Primary recommendation:** Implement `pkg/slack/mrkdwn.go` tokenizer + Tier 1 first; add `--render=mrkdwn` flag to `cmd/km-slack/main.go`; soak in production (PR1). Then add `pkg/slack/blocks.go`, extend the `SlackEnvelope` + bridge `SlackPosterAdapter` for `blocks`, and flip the streaming hook (PR2).

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `encoding/json` | stdlib | Block Kit JSON marshaling | Already used throughout; no external dep needed |
| `strings`, `regexp`, `unicode` | stdlib | Text segment manipulation | Sufficient for the tokenizer; no external Markdown parser needed |
| `testing` | stdlib | Unit + fuzz tests (`testing.F`) | Go's built-in test infra; fuzz targets run with `go test ./...` |
| `testing/quick` | stdlib | Property tests (idempotence, code-preservation) | Project convention documented in CONTEXT.md |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| None external | — | — | All needed functionality is in stdlib |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| stdlib tokenizer | `github.com/yuin/goldmark` | goldmark parses full CommonMark AST — overkill, adds a dependency, and the tokenizer only needs 3 segment types |
| stdlib tokenizer | `gitlab.com/golang-commonmark/markdown` | Same tradeoff; external dep with maintenance risk |

**Installation:** No new `go get` commands. All code uses stdlib.

---

## Architecture Patterns

### Recommended Project Structure
```
pkg/slack/
  mrkdwn.go              # Tokenizer + segment types + Tier 1 transforms
  mrkdwn_test.go         # Unit + property + fuzz
  blocks.go              # Tier 2 Block Kit builder (uses mrkdwn.go)
  blocks_test.go         # Unit + property tests
  payload.go             # Add MaxRenderedBytes = 35000 constant here
  testdata/              # Corpus fixtures
    bold-collapse.md     # .md input → .expected-mrkdwn.txt / .expected-blocks.json
    code-fence-passthrough.md
    html-escape.md
    link-conversion.md
    pipe-table.md
    heading-map.md
    tool-lines.md
    overflow-truncation.md
    idempotent-already-mrkdwn.md
    testdata/fuzz/FuzzMrkdwnify/  # Go fuzz seed corpus (auto-managed)
cmd/km-slack/main.go     # Add --render flag to runPost; call renderer in runWith
pkg/compiler/userdata.go # Flip _km_stream_drain km-slack post call: append --render=blocks (PR2)
```

### Pattern 1: Three-Segment Tokenizer
**What:** Walk input once, classify each character position into one of three segment types. Code segments are emitted byte-for-byte; transforms run only on text segments.
**When to use:** Any input that might contain code containing markdown-special characters (`**`, `<`, `|`).
**Example:**
```go
// pkg/slack/mrkdwn.go
type segKind int
const (
    segText      segKind = iota
    segCodeSpan                    // single-backtick delimited
    segCodeFence                   // triple-backtick delimited
)

type segment struct {
    kind segKind
    text string
}

func tokenize(input string) []segment {
    // Walk input; detect ``` and ` delimiters; emit segments.
    // Code-fence takes priority (check triple-backtick before single).
}
```

### Pattern 2: Fail-Soft Wrapper
**What:** `recover()` around the entire render path. Return original input unchanged on any panic.
**When to use:** All renderer entry points that are called from the streaming hook.
```go
// pkg/slack/mrkdwn.go
func Mrkdwnify(input string) (out string) {
    out = input // default: pass through unchanged
    defer func() {
        if r := recover(); r != nil {
            out = input // panic fallback
        }
    }()
    // ... real transform
    return
}
```

### Pattern 3: Overflow Check Before Envelope
**What:** Measure rendered body; if > `MaxRenderedBytes` (35000), truncate at that boundary and append truncation footer.
**When to use:** `runWith` in `cmd/km-slack/main.go`, after rendering, before `BuildEnvelope`.
```go
// cmd/km-slack/main.go runWith (after render call)
if len(rendered) > slack.MaxRenderedBytes {
    rendered = rendered[:slack.MaxRenderedBytes]
    rendered += "\n_…truncated; see full transcript at Stop_"
}
```

### Pattern 4: Blocks Envelope Extension (Tier 2 / PR2)
**What:** Add `Blocks string` (raw pre-serialized JSON array) to `SlackEnvelope`. Zero value is backward-compatible — existing post/archive/test callers serialize `""` which is ignored bridge-side.
**When to use:** PR2 only. Envelope struct field must maintain alphabetical JSON tag ordering for canonical JSON determinism.
```go
// pkg/slack/payload.go — new field insertion (alphabetical: "blocks" comes before "body")
type SlackEnvelope struct {
    Action      string `json:"action"`
    Blocks      string `json:"blocks"`      // NEW: pre-serialized Block Kit JSON array; "" = text-only
    Body        string `json:"body"`
    // ... rest unchanged
}
```

### Anti-Patterns to Avoid
- **Regex on whole text:** Running `strings.ReplaceAll` or `regexp.ReplaceAll` across the full input corrupts code samples containing `**`, `<`, or `|`. The tokenizer prevents this entirely.
- **Splitting into multiple thread replies for overflow:** Produces ordering hazards under Slack's rate limits. Hard-truncate + footer is the spec'd approach.
- **Extending `SlackPoster` interface in PR1:** The `SlackPoster` interface (`PostMessage(ctx, channel, subject, body, threadTS)`) only needs to be extended for `blocks` in PR2. PR1 leaves the interface untouched.
- **Re-using `body` field for pre-serialized blocks JSON:** Tempting but conflates the signing surface. The separate `Blocks` field keeps the envelope semantically clean.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Block Kit JSON schema validation | Custom type checker | `encoding/json` round-trip + struct validation | A `blocks` struct with typed fields catches invalid `type` values at compile time; json.Marshal produces valid JSON |
| CommonMark full parser | Full AST parser | Three-segment tokenizer | Only 3 segment types are needed; a full parser adds ~100KB of deps for no benefit |
| Canonical JSON serialization | Custom field-order encoder | `SlackEnvelope` struct with alphabetical JSON tags | Already implemented in `pkg/slack/payload.go:CanonicalJSON`; just add the `Blocks` field in alphabetical position |

**Key insight:** The renderer lives entirely in `pkg/slack` and needs no network access, no AWS SDK, and no external dependencies. The bridge's `SlackPosterAdapter` is the only component that needs a blocks-aware `chat.postMessage` call, and that's a localized addition to one method.

---

## Common Pitfalls

### Pitfall 1: Code Samples Containing Markdown-Special Characters
**What goes wrong:** A Go code snippet like `**p = nil` or a pipe-table inside a fenced block gets transformed, producing garbled output.
**Why it happens:** Regex-on-whole-text transforms don't respect code boundaries.
**How to avoid:** The tokenizer pattern is the fix. Transforms must only run on `segText` segments. Validated by the `code-fence-passthrough.md` corpus fixture and the code-preservation property test.
**Warning signs:** Any test input containing a code fence with `**` that produces output with `*` changed.

### Pitfall 2: `<channel-id>` or `<url>` in Prose Interpreted as Slack Links
**What goes wrong:** Claude output like `<C1234ABCD>` or `<https://example.com>` is interpreted by Slack as a channel mention or hyperlink with no label.
**Why it happens:** Slack mrkdwn interprets `<...>` as link/mention/special syntax.
**How to avoid:** HTML-escape `<` → `&lt;` and `>` → `&gt;` and `&` → `&amp;` in text segments **before** any other transform. Link conversion (`[label](url)` → `<url|label>`) must apply after escaping but only to the original link syntax, not to already-escaped text.
**Warning signs:** Slack renders a user mention where prose text was intended.

### Pitfall 3: `header` Block Plain-Text-Only Restriction
**What goes wrong:** A heading like `# The \`foo\` function` placed in a `header` block produces a Slack API error or renders with literal backticks/asterisks.
**Why it happens:** Slack's `header` block type requires a `plain_text` text object. Inline code, bold, links, and all other formatting are rejected or ignored.
**How to avoid:** Strip backticks, asterisks, and underscores from header text before placing it in a `header` block. Heading text > 150 chars gets hard-truncated to 147 chars + `…`.
**Warning signs:** Slack API returns `invalid_payload` or the block renders literally.

### Pitfall 4: Canonical JSON Field Ordering Breaks After `Blocks` Field Addition
**What goes wrong:** Adding `Blocks string` to `SlackEnvelope` in non-alphabetical position breaks `CanonicalJSON` determinism. The bridge verifies the Ed25519 signature over the canonical bytes — if field order differs between sender (km-slack) and verifier (bridge), every Tier 2 message fails signature verification with `bad_signature`.
**Why it happens:** `encoding/json` serializes struct fields in struct declaration order, not alphabetically.
**How to avoid:** Insert `Blocks string \`json:"blocks"\`` between `Action` and `Body` (alphabetical: `a` < `bl` < `bo`). The existing `payload_transcript_test.go` tests canonical ordering — add a corresponding assertion for the `blocks` field position.
**Warning signs:** Bridge returns `bad_signature 401` for all Tier 2 posts but Tier 1 posts work fine.

### Pitfall 5: Single Pipe in Tool-Line Filenames Triggering Table Fence
**What goes wrong:** A tool line like `🔧 Edit: a|b.go` gets wrapped in triple-backtick fences by the pipe-table heuristic.
**Why it happens:** The table-fence heuristic matches `^\s*\|.*\|\s*$` per line; a filename `a|b.go` could match if the line starts or ends with a pipe.
**How to avoid:** Two protections in the spec: (1) solo single-line matches are excluded (only runs of ≥2 contiguous pipe-table lines are fenced); (2) tool lines (`^🔧`) are processed via the Tier 2 context-block path which bypasses table-fence entirely. The `tool-lines.md` corpus fixture validates this.
**Warning signs:** Slack renders tool output in a monospace block instead of gray context text.

### Pitfall 6: 50-Block Cap Exceeded Without Fallback
**What goes wrong:** A long Claude turn with many headings and tool lines produces > 50 Block Kit blocks; the `chat.postMessage` call fails with `too_many_blocks`.
**Why it happens:** Slack's API hard limit is 50 blocks per message.
**How to avoid:** Count blocks during Tier 2 build; if count would exceed 50, abort Block Kit construction and fall back to Tier 1 mrkdwn for the entire post.
**Warning signs:** Bridge logs `chat.postMessage: too_many_blocks 400`.

---

## Code Examples

### Integration Point: `runWith` in `cmd/km-slack/main.go`

The renderer slots in at line ~288, between `os.ReadFile(bodyPath)` and `slack.BuildEnvelope(...)`. Current code (lines 288-296):

```go
// Current (no renderer):
body, err := os.ReadFile(bodyPath)
if err != nil { return fmt.Errorf("read body file: %w", err) }
if len(body) > slack.MaxBodyBytes {
    return fmt.Errorf("body file %s exceeds %d bytes (40KB Slack limit)", bodyPath, slack.MaxBodyBytes)
}
env, err := slack.BuildEnvelope(slack.ActionPost, sandboxID, channel, subject, string(body), thread)
```

After Phase 74 (PR1, mrkdwn mode):
```go
// New: read render mode from --render flag (default "plain") + env fallback
body, err := os.ReadFile(bodyPath)
if err != nil { return fmt.Errorf("read body file: %w", err) }

// Renderer call (fail-soft: Mrkdwnify wraps recover() internally)
rendered := slack.RenderMrkdwn(string(body), renderMode) // renderMode = "plain"|"mrkdwn"

// Overflow check
if len(rendered) > slack.MaxRenderedBytes {
    rendered = rendered[:slack.MaxRenderedBytes] + "\n_…truncated; see full transcript at Stop_"
}
if len(rendered) > slack.MaxBodyBytes {
    return fmt.Errorf("rendered body exceeds 40KB Slack limit")
}

env, err := slack.BuildEnvelope(slack.ActionPost, sandboxID, channel, subject, rendered, thread)
```

### Streaming Hook Call Site: `pkg/compiler/userdata.go` `_km_stream_drain` (line ~631)

Current call (PR1 leaves unchanged; PR2 flips to `--render=blocks`):
```bash
# PR1: no change needed; --render flag defaults to plain
post_resp=$(/opt/km/bin/km-slack post \
  --channel "$KM_SLACK_CHANNEL_ID" \
  --thread "$ts" \
  --body "$body_file" 2>&1 || echo "")

# PR2: single-line bash change:
post_resp=$(/opt/km/bin/km-slack post \
  --channel "$KM_SLACK_CHANNEL_ID" \
  --thread "$ts" \
  --body "$body_file" \
  --render "${KM_SLACK_RENDER:-blocks}" 2>&1 || echo "")
```

### Slack mrkdwn Quirks Reference

```
Input markdown    → Slack mrkdwn output
**bold**          → *bold*
*italic*          → (unchanged — already Slack mrkdwn; not a transform target)
_italic_          → (unchanged — pass-through)
~~strike~~        → ~strike~
[label](url)      → <url|label>
# Heading         → *Heading*   (Tier 1) / header block "Heading" (Tier 2)
---               → (dropped)   (Tier 1) / divider block (Tier 2, explicit only)
<channel>         → &lt;channel&gt;  (HTML-escape prevents Slack link interpretation)
&amp;             → &amp;amp;        (double-escape &)
| col | col |     → ```\n| col | col |\n```  (two+ contiguous pipe lines)
```

### Block Kit JSON Structure (Tier 2)

```json
{
  "channel": "C123ABC",
  "text": "plain text fallback for notifications/search",
  "blocks": [
    {"type": "header", "text": {"type": "plain_text", "text": "Heading text (max 150 chars)"}},
    {"type": "section", "text": {"type": "mrkdwn", "text": "*subheading* text (max 3000 chars)"}},
    {"type": "context", "elements": [{"type": "mrkdwn", "text": "🔧 Edit: /path/to/file.go"}]},
    {"type": "divider"}
  ],
  "unfurl_links": false,
  "unfurl_media": false,
  "mrkdwn": true,
  "thread_ts": "1234567890.000100"
}
```

### Fuzz Target Scaffold

```go
// pkg/slack/mrkdwn_test.go
func FuzzMrkdwnify(f *testing.F) {
    // Seed corpus from production failure modes
    f.Add("**bold** and *already-mrkdwn*")
    f.Add("# heading\n\nparagraph with <html> tag")
    f.Add("```go\nfunc foo(**p *T) {}\n```\n\nprose")
    f.Add(strings.Repeat("| col |", 3) + "\n" + strings.Repeat("| val |", 3))
    f.Add("🔧 Edit: a|b.go (line 42)")

    f.Fuzz(func(t *testing.T, input string) {
        // Property 1: must not panic (fail-soft handles, but fuzz still catches panics before recover)
        out := slack.Mrkdwnify(input)
        // Property 2: idempotent
        out2 := slack.Mrkdwnify(out)
        if out != out2 {
            t.Errorf("not idempotent:\nfirst:  %q\nsecond: %q", out, out2)
        }
    })
}
```

### Property Test: Code-Block Byte Preservation

```go
// pkg/slack/mrkdwn_test.go
func TestCodeFencePreservation(t *testing.T) {
    // quick.Check: any input inside code fences survives unchanged
    f := func(inner string) bool {
        input := "```\n" + inner + "\n```"
        out := slack.Mrkdwnify(input)
        return strings.Contains(out, inner)
    }
    if err := quick.Check(f, nil); err != nil {
        t.Error(err)
    }
}
```

---

## Integration Surface Details

### Current `runWith` signature (no changes needed for PR1 except render mode param):

```go
func runWith(ctx context.Context, priv ed25519.PrivateKey, sandboxID, bridgeURL, channel, subject, bodyPath, thread string) error
```

The `--render` flag is parsed in `runPost` and passed down as a new `renderMode string` parameter. `runWith` signature gains one parameter; the testable inner entry point stays injectable.

### Bridge Lambda: No change needed for PR1 (mrkdwn)

The bridge `handler.go` dispatch for `ActionPost` (line 256-270) calls `h.Slack.PostMessage(ctx, env.Channel, env.Subject, env.Body, env.ThreadTS)`. For PR1, the renderer transforms the body before it enters the envelope; the bridge sees already-rendered mrkdwn text in `env.Body` and forwards it as-is. No bridge changes.

### Bridge Lambda: Additive changes required for PR2 (Block Kit)

**Three additive changes for PR2:**

1. **`SlackEnvelope.Blocks string`** — new field in `pkg/slack/payload.go`, alphabetical position `"blocks"` (between `action` and `body`). Canonical JSON ordering: `action`, `blocks`, `body`, `channel`, ...

2. **`SlackPoster` interface extension** — new method `PostMessageWithBlocks(ctx, channel, subject, body, blocksJSON, threadTS string) (string, error)` OR extend existing `PostMessage` signature. Cleaner: keep `PostMessage` unchanged, add `PostMessageBlocks`. Bridge handler checks `env.Blocks != ""` and routes to the new method.

3. **`SlackPosterAdapter.PostMessageBlocks`** — builds `chat.postMessage` payload with both `text` (plain-text fallback) and `blocks` (unmarshaled from `env.Blocks`). Rate-limit and error handling identical to `PostMessage`.

**Blast radius of Tier 2 bridge changes:** limited to `pkg/slack/payload.go` (new field), `pkg/slack/bridge/interfaces.go` (new interface method), `pkg/slack/bridge/aws_adapters.go` (new adapter method), `pkg/slack/bridge/handler.go` (dispatch case for non-empty `Blocks` field). All existing `ActionPost` tests remain valid because `Blocks == ""` routes through the original path.

### Bridge Lambda: `text:` fallback field (required by Slack)

Slack requires `text` when `blocks` is present, for push notifications and search. The `text` field is already the `env.Body` field. For Tier 2, `env.Body` contains the plain-text rendering of the full body (markdown stripped), and `env.Blocks` contains the Block Kit JSON. The bridge `PostMessageBlocks` method uses `env.Body` as `text` and the decoded `env.Blocks` as `blocks`.

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `files.upload` (deprecated) | `files.getUploadURLExternal` + PUT + `completeUploadExternal` | 2024 | Phase 68 already uses the new flow |
| attachments (`legacy_attachments`) | Block Kit `blocks` field | 2019–present | Phase 74 uses Block Kit, not legacy attachments |
| `mrkdwn_in` on attachments | Native `section` block with `"type":"mrkdwn"` | 2019 | No `mrkdwn_in` needed in Block Kit |

**Deprecated/outdated:**
- Slack legacy `attachments` field: replaced by Block Kit; do not use in Phase 74.
- `files.upload`: already replaced in Phase 68; Phase 74 does not touch the file upload path.

---

## Open Questions

1. **`SlackPoster` interface extension strategy for Tier 2**
   - What we know: `SlackPoster.PostMessage(ctx, channel, subject, body, threadTS)` exists; all fakes and the bridge implement it. Adding a new method to the interface breaks all existing fakes.
   - What's unclear: Whether to add `PostMessageBlocks` as a new interface method (requires fake updates) or use an optional interface (type assertion to `BlockPoster` in the handler dispatch).
   - Recommendation: Optional interface via type assertion — `if bp, ok := h.Slack.(BlockPoster); ok { ... }`. Keeps existing fakes untouched. Define `BlockPoster` as a second interface with a single `PostMessageBlocks` method. The `fakeSlack` in tests only needs to implement `BlockPoster` in Block Kit-specific tests.

2. **`env.Blocks` transport size vs `MaxBodyBytes`**
   - What we know: `MaxBodyBytes = 40*1024` is enforced on `env.Body`. A Block Kit JSON array for a 50-block message can be significantly larger than the mrkdwn text.
   - What's unclear: Does Slack's 40KB total payload limit apply to the `blocks` JSON or just `text`? Slack's documented limit is "40KB total message size".
   - Recommendation: The 35KB `MaxRenderedBytes` threshold applies to the rendered body text; the Block Kit JSON overhead is accounted for by choosing 35KB (5KB headroom). Define `MaxBlocksBytes = 35 * 1024` as a separate constant for the serialized blocks JSON check. If `len(blocksJSON) > MaxBlocksBytes`, fall back to Tier 1.

---

## Validation Architecture

> `workflow.nyquist_validation` is `true` in `.planning/config.json` — this section is required.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` package (stdlib), Go 1.25.5 |
| Fuzz support | `testing.F` — built into Go 1.18+ |
| Property tests | `testing/quick` (stdlib) |
| Config file | None — `go test ./...` discovers all `*_test.go` files |
| Quick run command | `go test ./pkg/slack/... ./cmd/km-slack/...` |
| Full suite command | `go test ./...` |
| Fuzz run command | `go test -fuzz=FuzzMrkdwnify -fuzztime=30s ./pkg/slack/...` |

### Phase Requirements to Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| REND-01 | `**x**` → `*x*` bold collapse in text segments | unit (table-driven) | `go test ./pkg/slack/... -run TestMrkdwnify_Bold` | ❌ Wave 0 |
| REND-02 | `# h`/`## h`/`### h` → `*h*` heading map | unit (table-driven) | `go test ./pkg/slack/... -run TestMrkdwnify_Heading` | ❌ Wave 0 |
| REND-03 | `[label](url)` → `<url\|label>` link conversion | unit (table-driven) | `go test ./pkg/slack/... -run TestMrkdwnify_Link` | ❌ Wave 0 |
| REND-04 | `~~x~~` → `~x~` strikethrough | unit (table-driven) | `go test ./pkg/slack/... -run TestMrkdwnify_Strike` | ❌ Wave 0 |
| REND-05 | HTML-escape `<`, `>`, `&` in text only | unit (table-driven) | `go test ./pkg/slack/... -run TestMrkdwnify_HTMLEscape` | ❌ Wave 0 |
| REND-06 | Drop `---`/`***`/`___` horizontal rules in text | unit (table-driven) | `go test ./pkg/slack/... -run TestMrkdwnify_HRule` | ❌ Wave 0 |
| REND-07 | Pipe-table runs (≥2 lines) → triple-backtick fence | unit (table-driven) | `go test ./pkg/slack/... -run TestMrkdwnify_PipeTable` | ❌ Wave 0 |
| REND-08 | Code-fence content passes through byte-for-byte | property (testing/quick) | `go test ./pkg/slack/... -run TestCodeFencePreservation` | ❌ Wave 0 |
| REND-09 | Code-span content passes through byte-for-byte | property (testing/quick) | `go test ./pkg/slack/... -run TestCodeSpanPreservation` | ❌ Wave 0 |
| REND-10 | Idempotence: `Mrkdwnify(Mrkdwnify(x)) == Mrkdwnify(x)` | property (testing/quick) | `go test ./pkg/slack/... -run TestMrkdwnifyIdempotent` | ❌ Wave 0 |
| REND-11 | Fuzz: no panic, idempotence on arbitrary input | fuzz | `go test -fuzz=FuzzMrkdwnify -fuzztime=30s ./pkg/slack/...` | ❌ Wave 0 |
| REND-12 | Corpus fixtures: known production failure modes | corpus fixture | `go test ./pkg/slack/... -run TestMrkdwnifyCorpus` | ❌ Wave 0 |
| REND-13 | Fail-soft: panic in renderer returns original input | unit | `go test ./pkg/slack/... -run TestMrkdwnify_FailSoft` | ❌ Wave 0 |
| REND-14 | Body overflow: >35KB truncated + footer appended | unit | `go test ./cmd/km-slack/... -run TestRunWith_Overflow` | ❌ Wave 0 |
| REND-15 | `--render=plain` default: body unchanged | unit | `go test ./cmd/km-slack/... -run TestRunWith_Plain` | ❌ Wave 0 |
| REND-16 | `KM_SLACK_RENDER` env overrides flag | unit | `go test ./cmd/km-slack/... -run TestRunWith_EnvOverride` | ❌ Wave 0 |
| BLK-01  | `# h` → `header` block (plain text, 150-char cap) | unit (table-driven) | `go test ./pkg/slack/... -run TestBlocks_H1Header` | ❌ Wave 0 |
| BLK-02  | `## h`/`### h` → bold `*h*` in `section` block | unit (table-driven) | `go test ./pkg/slack/... -run TestBlocks_H2H3Section` | ❌ Wave 0 |
| BLK-03  | Tool-line prefix `^🔧 \w+:` → `context` block | unit (table-driven) | `go test ./pkg/slack/... -run TestBlocks_ToolLine` | ❌ Wave 0 |
| BLK-04  | `---` → `divider` block; no auto-dividers | unit (table-driven) | `go test ./pkg/slack/... -run TestBlocks_Divider` | ❌ Wave 0 |
| BLK-05  | Section text capped at 3000 chars; splits at para/sent/char | unit | `go test ./pkg/slack/... -run TestBlocks_SectionOverflow` | ❌ Wave 0 |
| BLK-06  | 50-block cap triggers Tier 1 fallback | unit | `go test ./pkg/slack/... -run TestBlocks_50BlockFallback` | ❌ Wave 0 |
| BLK-07  | `text:` fallback field = plain-text rendering (no mrkdwn) | unit | `go test ./pkg/slack/... -run TestBlocks_PlainTextFallback` | ❌ Wave 0 |
| BLK-08  | Block Kit output validates against structure (no invalid types) | property (testing/quick) | `go test ./pkg/slack/... -run TestBlocks_StructuralValidity` | ❌ Wave 0 |
| BLK-09  | Header text stripped of backticks/asterisks/underscores | unit | `go test ./pkg/slack/... -run TestBlocks_HeaderStrip` | ❌ Wave 0 |
| BLK-10  | Header text >150 chars truncated at 147 + `…` | unit | `go test ./pkg/slack/... -run TestBlocks_HeaderTruncate` | ❌ Wave 0 |
| BRDG-01 | Envelope with `Blocks=""` routes to text-only PostMessage (backward compat) | unit (bridge) | `go test ./pkg/slack/bridge/... -run TestHandler_Post_NoBlocks` | ✅ existing test covers action=post |
| BRDG-02 | Envelope with `Blocks=<json>` routes to PostMessageBlocks | unit (bridge) | `go test ./pkg/slack/bridge/... -run TestHandler_Post_WithBlocks` | ❌ Wave 0 |
| BRDG-03 | Canonical JSON: `"blocks"` field appears between `"action"` and `"body"` | unit | `go test ./pkg/slack/... -run TestCanonicalJSON_BlocksOrdering` | ❌ Wave 0 |
| HOOK-01 | `_km_stream_drain` calls `km-slack post --render=blocks` after PR2 flip | integration (notify-hook tests) | `go test ./pkg/compiler/... -run TestStreamDrain_RenderFlag` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/slack/... ./cmd/km-slack/...`
- **Per wave merge:** `go test ./...`
- **Phase gate:** `go test ./...` green before `/gsd:verify-work`

### Wave 0 Gaps

PR1 (tokenizer + Tier 1):
- [ ] `pkg/slack/mrkdwn.go` — tokenizer + transforms (new file)
- [ ] `pkg/slack/mrkdwn_test.go` — unit + property + fuzz tests (new file)
- [ ] `pkg/slack/testdata/` — corpus fixture files (new directory)
- [ ] `pkg/slack/payload.go` — add `MaxRenderedBytes = 35000` constant
- [ ] `cmd/km-slack/main.go` — `--render` flag wiring in `runPost`/`runWith`

PR2 (Block Kit):
- [ ] `pkg/slack/blocks.go` — Tier 2 builder (new file)
- [ ] `pkg/slack/blocks_test.go` — unit + property tests (new file)
- [ ] `pkg/slack/payload.go` — add `Blocks string \`json:"blocks"\`` to `SlackEnvelope` (alphabetical position)
- [ ] `pkg/slack/bridge/interfaces.go` — optional `BlockPoster` interface
- [ ] `pkg/slack/bridge/aws_adapters.go` — `SlackPosterAdapter.PostMessageBlocks` method
- [ ] `pkg/slack/bridge/handler.go` — dispatch for `env.Blocks != ""` in ActionPost case
- [ ] `pkg/compiler/userdata.go` — append `--render "${KM_SLACK_RENDER:-blocks}"` to `_km_stream_drain` km-slack call

---

## Two-PR Sequencing

The natural seam is exactly what the CONTEXT specifies. Recommended PLAN.md split:

**PR1: `01-tokenizer-tier1.md`**
- Wave 0: add `MaxRenderedBytes` constant to `pkg/slack/payload.go`; create `pkg/slack/testdata/` directory with corpus fixtures; add `--render` flag scaffolding in `cmd/km-slack/main.go` (flag parsing + env fallback, no-op plain mode)
- Wave 1: implement tokenizer (`pkg/slack/mrkdwn.go`); implement Tier 1 transforms
- Wave 2: unit tests, property tests, fuzz target (`pkg/slack/mrkdwn_test.go`); overflow logic in `runWith`
- Wave 3: `--render=mrkdwn` integration in `runWith`; existing hook callers verified unchanged

**PR2: `02-tier2-blocks.md`**
- Wave 0: extend `SlackEnvelope` with `Blocks` field; add `BlockPoster` optional interface; create `pkg/slack/blocks.go` scaffold
- Wave 1: implement Tier 2 Block Kit builder (`pkg/slack/blocks.go`)
- Wave 2: unit + property tests (`pkg/slack/blocks_test.go`); bridge adapter + handler changes
- Wave 3: hook flip (`pkg/compiler/userdata.go`); bridge integration test; deploy path (`make build && km init --lambdas`)

---

## Sources

### Primary (HIGH confidence)
- Official Slack Block Kit reference — https://docs.slack.dev/reference/block-kit/blocks/header-block (header block: 150-char plain-text-only limit confirmed)
- Official Slack Block Kit reference — https://docs.slack.dev/reference/block-kit/blocks/section-block (section block: 3000-char text limit, 10 fields max confirmed)
- Official Slack Block Kit reference — https://docs.slack.dev/block-kit/ (50 blocks/message limit confirmed)
- Go fuzzing documentation — https://go.dev/doc/fuzz/ (testing.F, F.Add, corpus structure, go test -fuzz invocation confirmed)
- Codebase direct inspection:
  - `pkg/slack/payload.go` — `MaxBodyBytes = 40*1024`, `SlackEnvelope` struct, `BuildEnvelope`, `CanonicalJSON`
  - `pkg/slack/client.go` — `PostMessage` signature, `callJSON` pattern
  - `pkg/slack/bridge/interfaces.go` — `SlackPoster` interface definition
  - `pkg/slack/bridge/aws_adapters.go` — `SlackPosterAdapter.PostMessage` implementation
  - `pkg/slack/bridge/handler.go` — dispatch logic for `ActionPost`; `text`-only path confirmed; no `blocks` field today
  - `cmd/km-slack/main.go` — `runWith` at line 280, `runPost` flag parsing, `MaxBodyBytes` check at line 292
  - `pkg/compiler/userdata.go` lines 530-663 — `_km_stream_drain` function, exact `km-slack post` invocation at line 631

### Secondary (MEDIUM confidence)
- Slack mrkdwn formatting guide — https://docs.slack.dev/messaging/formatting-message-text/ (mrkdwn bold=`*x*`, italic=`_x_`, strikethrough=`~x~`, links=`<url|label>`, HTML special chars must be escaped; verified against `magicbell.com/blog/slack-text-formatting` and `markdownguide.org/tools/slack/`)

### Tertiary (LOW confidence — needs live bridge validation)
- Slack 40KB total payload limit applies to `blocks` JSON: stated by multiple community sources but Slack's official docs do not give a precise per-field breakdown; the 35KB `MaxRenderedBytes` threshold provides ample headroom.

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — stdlib only, verified in codebase
- Architecture: HIGH — direct codebase inspection of all integration points
- Pitfalls: HIGH — derived from codebase inspection + Slack official docs
- Bridge blocks extension: HIGH for approach; MEDIUM for exact interface extension pattern (optional interface vs new method — both viable, recommendation documented)
- Slack limits: HIGH — header 150 char, section 3000 char, 50 blocks/message all confirmed via official docs

**Research date:** 2026-05-09
**Valid until:** 2026-09-01 (Slack API limits are stable; Go fuzzing API stable since Go 1.18)
