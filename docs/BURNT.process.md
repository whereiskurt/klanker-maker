# BURNT.md Generation Process

How to regenerate or extend `docs/BURNT.md` from local Claude Code session data.

## Conventions

- **Latest-first ordering.** New sprint chapter goes directly under the top header. Older chapters cascade below in reverse-chronological order (VIII → VII → … → I).
- **Top-of-file dashboard.** The header carries cumulative numbers, the GitHub-style daily heatmap (Sun→Sat rows × week columns), the per-sprint volume bar chart, and the chapters TOC. Update all four when shipping a new chapter.
- **The framing.** Every line of code is written by Claude Code on Opus 4.7. The operator does profile design, UAT, and ship/no-ship calls. Keep that voice in the framing copy at the top.
- **Snapshot semantics.** Each chapter is frozen at the snapshot date stamped in its `*Generated...*` footer. Per-chapter "Running Total" tables reflect what was true at that moment — do not retro-edit them. Cumulative truth lives at the top of the file only.

## Data Sources

All token usage comes from **local JSONL session files**:

```
~/.claude/projects/-Users-khundeck-working-klankrmkr/*.jsonl
```

Each `.jsonl` file is one session. Each line is a JSON object. Messages with `usage` fields contain token counts.

> **Note on the path.** The directory name is `klankrmkr` (current working dir basename, slashes flattened to dashes). Older snapshots reference the historical `klanker-maker` path; only the current path holds active session data.

## Usage Structure

Each API response message has a `usage` object:

```json
{
  "input_tokens": 3,
  "cache_creation_input_tokens": 10706,
  "cache_read_input_tokens": 9824,
  "output_tokens": 10,
  "service_tier": "standard"
}
```

**Total input = `input_tokens` + `cache_creation_input_tokens` + `cache_read_input_tokens`**

The `input_tokens` field alone is tiny — it's only the non-cached portion. The real volume is in `cache_read_input_tokens` (typically 96–99% of total input across this project).

## Key Extraction Patterns

### Per-session totals

```python
import json, os, glob

for f in glob.glob(os.path.expanduser("~/.claude/projects/-Users-khundeck-working-klankrmkr/*.jsonl")):
    for line in open(f):
        d = json.loads(line)
        msg = d.get("message")
        if isinstance(msg, dict) and isinstance(msg.get("usage"), dict):
            u = msg["usage"]
            total_in = (u.get("input_tokens", 0) or 0) \
                     + (u.get("cache_creation_input_tokens", 0) or 0) \
                     + (u.get("cache_read_input_tokens", 0) or 0)
            out = u.get("output_tokens", 0) or 0
```

### Timestamps

Each JSONL entry has a `timestamp` field (ISO 8601, UTC). Convert to EDT (UTC-4) for the heat map and daily-bucket dashboard.

### Filtering by date range

Check `timestamp` against your target range. Sessions can span multiple days — a session is "active this week" if any of its timestamps fall in the range. The chapter cut-off is at the snapshot timestamp; the next chapter picks up from there (note any partial-day overlap explicitly in the chapter intro).

### Turns vs API calls

- **Turns**: entries where `d.get("type") == "user"` — actual human interactions
- **API calls**: entries with a `usage` field — includes tool calls, subagent messages, etc.

## Git Stats (per chapter)

```bash
# Commits in chapter window
git log --since="2026-05-04 08:00" --until="2026-05-09" --oneline --all | wc -l

# Lines changed (added / deleted / net)
git log --since="2026-05-04 08:00" --until="2026-05-09" --shortstat --all | \
  awk '/files? changed/ {f+=$1; for(i=1;i<=NF;i++) {if ($i ~ /insertion/) ins+=$(i-1); if ($i ~ /deletion/) del+=$(i-1)}} END {print f, ins, del, ins-del}'

# Phases touched (look for .planning/phases/NN-* directories)
git log --since=... --name-only --pretty=format: | grep -oE '\.planning/phases/[0-9]+[^/]*' | sort -u
```

## Hourly Heat Map

1. Parse all sessions in the chapter window, bucket tokens by `(date, hour)` in EDT.
2. Scale: 1 block = 4M tokens (`max(1, round(v / 4_000_000))`).
3. Symbol: `🟧` for normal hours, `🔥` for 80M+ hours (the visual "inferno" tier).
4. Mark the day's peak hour with `⬅️ DAY PEAK` and the sprint's peak hour with `⬅️ SPRINT PEAK HOUR`.
5. Collapse multi-hour quiet stretches with `<sub>· · · sleep 3am–7am · · ·</sub>` (or generic `· · ·`) divider rows.

## Top-of-File Daily Heatmap (GitHub-style)

Build the calendar grid from the per-day totals:

| Bucket (M tokens) | Symbol |
|---|---|
| 0 | ⬜ |
| 1–50 | 🟩 |
| 50–150 | 🟨 |
| 150–300 | 🟧 |
| 300+ | 🟥 |

Layout: rows = days of week (Sun → Sat), columns = ISO weeks (Mon-anchored, but display as "Wk 3/22" with the Sunday date). Add a one-line ASCII sparkline below the table (`▁▃▅▆█` glyphs scaled to the same daily-M buckets).

## Per-Sprint Volume Bar Chart

Sort all chapters by token volume (descending), render as a fixed-width bar chart in a fenced code block. One block per ~50M tokens (i.e. `width = round(tokens_in_M / 50)`). Annotate each bar with token count, date range, and sprint name. Mark the latest chapter with `← latest`.

## Fun Facts Conversion

| Metric | Tokens per unit |
|---|---|
| Page of text | ~1,333 |
| Novel | ~400,000 |
| English Wikipedia | ~330,000,000 |
| Lord of the Rings | ~1,500,000 |

## Chapter Template

```markdown
# BURNT N: <Sprint Title>

> **<Start> – <End>, 2026** | Claude Code on **5x MAX** (Opus 4.7) | klanker-maker <one-line theme>

---

## The Numbers
| | |
|---|---|
| **Tokens consumed** | **X,XXX,XXX,XXX+** |
| Input | ... |
| Output | ... |
| Input:Output ratio | N : 1 |
| Cache read | ... |
| **Sessions** | N |
| **API calls** | N |
| **Turns** | N |
| **Plan cost** | **$200/mo** |

---

## Heat Map (EDT)
<table> ... </table>

---

## Monster Sessions
| Tokens | Output | API calls | Span | Window |
|---:|---:|---:|---:|---|
| ... | ... | ... | ... | ... |

---

## What Got Built
| | |
|---|---|
| Commits | N |
| Files changed | N |
| Lines added | N |
| Lines deleted | N |
| Net lines | +N |
| Phases shipped | ... |
| Phases planned end-to-end | ... |
| Features shipped | ... |

---

## What Does X Billion Tokens Even Mean?
| | |
|---|---|
| Pages of text | ~N |
| Full-length novels | ~N |
| English Wikipedias | ~N |
| Lord of the Rings cover-to-covers | ~N |

...to write back **N tokens** and produce **N commits** / **N lines** across **N shipped phases** — every line authored by Claude Code on Opus 4.7.

---

## Running Total
(Append the new row, do not edit prior rows.)

---

*Generated by Claude Code · snapshot YYYY-MM-DD EDT*
```

## When Shipping a New Chapter

1. Add the new chapter file directly under the top header (above the previous chapter).
2. Update the top **TL;DR** numbers (cumulative tokens / commits / net lines / chapters count / latest snapshot date).
3. Append a row to the **Per-sprint token volume** bar chart, then re-sort by volume and re-mark the latest sprint with `← latest`.
4. Add the new chapter to the **Chapters (latest first)** TOC at the top.
5. Extend the **Daily token burn** heatmap table with the new days (a new column when crossing a Sunday).
6. Extend the **Sparkline** strip with one bar per new day.
7. Update the snapshot footer in the chapter and the top-of-file `Latest snapshot` field.
