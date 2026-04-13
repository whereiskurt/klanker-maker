# Persistent Local Sandbox Numbering

## Problem

`km list` currently assigns ephemeral positional numbers (1-N) that shift whenever sandboxes are created or destroyed. This makes numbers unreliable as references between commands.

## Design

Assign each sandbox a persistent local number at creation time. Numbers count up monotonically. A number stays with its sandbox for its entire lifetime. When all sandboxes are destroyed (zero remaining), the counter resets to 1.

### Behavior

```
km create → #1
km create → #2
km destroy 2
km create → #3
km create → #4
# live: 1, 3, 4 (gaps are expected)
km destroy 1; km destroy 3; km destroy 4
# zero sandboxes → counter resets
km create → #1
```

### Storage

Local JSON file at `~/.config/km/local-numbers.json`:

```json
{
  "next": 5,
  "map": {
    "sb-a1b2c3d4": 1,
    "sb-e5f6g7h8": 3,
    "sb-i9j0k1l2": 4
  }
}
```

- `next`: the next number to assign
- `map`: sandbox_id → number mapping

Numbers are local-only and not synced to DynamoDB. Aliases remain the global identifier.

### Touchpoints

1. **`create.go`** — After sandbox ID is generated, assign `next` number, increment `next`, write file. Print the assigned number to the user.

2. **`list.go`** — Display the persistent number from the local file instead of a positional index. Sandboxes without a local number (e.g., created remotely from another machine) get assigned the next available number on first list. Prune entries for sandboxes no longer in DynamoDB.

3. **`sandbox_ref.go`** — Resolve numeric references by reverse-looking up number → sandbox_id from the local file, replacing the current positional-list-based resolution.

4. **Destroy/cleanup path** — Remove the sandbox entry from the map on destroy. If the map becomes empty, reset `next` to 1.

### New package

A small `pkg/localnumber/` (or similar) package to encapsulate:

- `Load() (*State, error)` — read the JSON file (return empty state if missing)
- `Save(state *State) error` — atomic write (write tmp + rename)
- `Assign(state *State, sandboxID string) int` — assign next number, increment
- `Remove(state *State, sandboxID string)` — remove entry, reset if empty
- `Resolve(state *State, num int) (string, bool)` — reverse lookup number → sandbox_id
- `Reconcile(state *State, liveSandboxIDs []string)` — prune stale entries, assign numbers to unknown sandboxes

### Edge Cases

- **Remote creates**: Sandbox won't have a local number until the next `km list` or local interaction. `Reconcile` handles this by assigning numbers to unknown sandboxes.
- **Multiple machines**: Each machine has independent numbering. Aliases are the cross-machine identifier.
- **File missing**: Treated as empty state — all sandboxes get numbered on next interaction.
- **Concurrent creates**: Not a concern for single-user CLI. Atomic write (tmp + rename) prevents corruption.

### What doesn't change

- Sandbox IDs remain random UUIDs (`sb-xxxxxxxx`)
- Aliases remain global (DynamoDB GSI)
- DynamoDB metadata schema unchanged
- Resolution priority: alias first, then sandbox ID pattern, then local number
