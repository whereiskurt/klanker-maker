# BURNT.md Generation Process

How to regenerate or update `docs/BURNT.md` from local Claude Code session data.

## Data Sources

All token usage comes from **local JSONL session files**:

```
~/.claude/projects/-Users-khundeck-working-klankrmkr/*.jsonl
```

Each `.jsonl` file is one session. Each line is a JSON object. Messages with `usage` fields contain token counts.

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

The `input_tokens` field alone is tiny — it's only the non-cached portion. The real volume is in `cache_read_input_tokens`.

## Key Extraction Patterns

### Per-session totals

```python
import json, os, glob

for f in glob.glob("~/.claude/projects/-Users-khundeck-working-klankrmkr/*.jsonl"):
    for line in open(f):
        d = json.loads(line)
        if "message" in d and isinstance(d["message"], dict) and "usage" in d["message"]:
            u = d["message"]["usage"]
            # Sum: input_tokens + cache_creation_input_tokens + cache_read_input_tokens
            # And: output_tokens
```

### Timestamps

Each JSONL entry has a `timestamp` field (ISO 8601, UTC). Convert to EDT (UTC-4) for the heat map.

### Filtering by date range

Check `timestamp` against your target range. Sessions can span multiple days — a session is "active this week" if any of its timestamps fall in the range.

### Turns vs API calls

- **Turns**: entries where `d.get("type") == "user"` — actual human interactions
- **API calls**: entries with a `usage` field — includes tool calls, subagent messages, etc.

## Git Stats

```bash
# Commits in range
git log --since="2026-03-21" --until="2026-03-29" --oneline --all | wc -l

# Lines changed
git log --since="2026-03-21" --until="2026-03-29" --shortstat --all | \
  awk '/files? changed/ {f+=$1; i+=$4; d+=$6} END {print f, i, d}'
```

## Heat Map Generation

1. Parse all sessions, bucket tokens by `(day, hour)` in EDT
2. Scale: 1 block = 4M tokens
3. Use `🟧` for normal hours, `🔥` for 80M+ hours
4. Collapse gaps (sleep, low-activity) with `· · ·` separator rows

## Fun Facts Conversion

| Metric | Tokens per unit |
|---|---|
| Page of text | ~1,333 |
| Novel | ~400,000 |
| English Wikipedia | ~330,000,000 |
| Lord of the Rings | ~1,500,000 |
