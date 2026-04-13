# Phase 53: Persistent Local Sandbox Numbering - Research

**Researched:** 2026-04-13
**Domain:** Go local file I/O, JSON state, CLI UX, sandbox lifecycle hooks
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Storage: local JSON file at `~/.config/km/local-numbers.json`
- Schema: `{"next": int, "map": {"sandbox-id": number}}`
- Atomic write via tmp file + rename
- Missing file treated as empty state
- Numbers assigned at create time, counting up from 1
- `next` field tracks the next number to assign
- Numbers never reuse while any sandbox exists
- Counter resets to 1 only when map is completely empty (zero sandboxes)
- `km list` shows persistent numbers instead of positional index
- `km create` prints the assigned number
- Resolution priority: alias first, sandbox ID pattern second, local number third
- `km list` prunes entries for sandboxes no longer in DynamoDB
- Sandboxes without a local number (remote creates) get assigned next available number on first `km list`
- Destroy removes sandbox entry from map; if map becomes empty, reset `next` to 1

### Claude's Discretion
- Package naming and internal structure of `pkg/localnumber/`
- Exact error handling for file I/O edge cases
- Test strategy

### Deferred Ideas (OUT OF SCOPE)
None — design spec covers phase scope
</user_constraints>

---

## Summary

Phase 53 is a pure Go CLI enhancement with no AWS dependencies. It introduces a small local-state package (`pkg/localnumber/`) that manages a JSON file at `~/.config/km/local-numbers.json`. The package is integrated into four existing touchpoints: `create.go` (assign number), `list.go` (display + reconcile), `sandbox_ref.go` (resolve numeric ref), and the destroy path (remove entry).

The codebase already has a clear precedent for atomic JSON file writes: `ebpf_attach.go` uses the write-tmp-then-rename pattern (`os.WriteFile` to `path.tmp`, then `os.Rename`). The config package uses `os.UserHomeDir()` and `~/.km/` for local state. The design spec specifies `~/.config/km/` — this is XDG-compliant via `os.UserConfigDir()` on macOS/Linux (returns `~/.config`). The two are distinct directories; the new package should use `os.UserConfigDir()` to get the correct base.

The key behavioral subtleties are: (1) `Reconcile` must assign numbers to sandboxes appearing in DynamoDB that have no local number — this handles remote creates from other machines; (2) `Remove` must reset `next` to 1 only when the map is completely empty after removal; (3) `ResolveSandboxID` currently falls through to a full AWS list call for numeric refs — the new path reads the local file instead, making numeric resolution instant and offline-capable.

**Primary recommendation:** Implement `pkg/localnumber/` as a self-contained package with five pure functions plus a `StateFilePath()` helper, then wire all four touchpoints in sequence.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `encoding/json` | stdlib | JSON marshal/unmarshal of state file | Already used throughout codebase |
| `os` | stdlib | File I/O, UserConfigDir, Rename | Already used throughout codebase |
| `path/filepath` | stdlib | Path construction | Already used throughout codebase |

No new dependencies are needed.

**Installation:**
```bash
# No new packages — stdlib only
```

---

## Architecture Patterns

### Recommended Project Structure
```
pkg/localnumber/
├── localnumber.go       # State type + Load/Save/Assign/Remove/Resolve/Reconcile
└── localnumber_test.go  # Unit tests using t.TempDir()
```

### Pattern 1: Atomic Write (established in codebase)
**What:** Write to a `.tmp` file then `os.Rename` to target path — prevents partial writes corrupting state.
**When to use:** All `Save` calls.
**Example:**
```go
// Source: internal/app/cmd/ebpf_attach.go (flushObservedState)
tmpPath := outputPath + ".tmp"
if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
    return err
}
return os.Rename(tmpPath, outputPath)
```

### Pattern 2: UserConfigDir for XDG-compliant local state
**What:** `os.UserConfigDir()` returns `~/.config` on Linux/macOS (XDG_CONFIG_HOME), `%AppData%` on Windows.
**When to use:** Constructing the path to `local-numbers.json`.
**Example:**
```go
// Source: Go stdlib docs
dir, err := os.UserConfigDir()
if err != nil {
    // fallback to UserHomeDir + ".config"
    home, _ := os.UserHomeDir()
    dir = filepath.Join(home, ".config")
}
return filepath.Join(dir, "km", "local-numbers.json")
```

Note: The existing codebase uses `~/.km/` (via `os.UserHomeDir()`) for configs, but the design spec locks the path to `~/.config/km/local-numbers.json`. Use `os.UserConfigDir()` to be XDG-correct.

### Pattern 3: Reconcile — prune + gap-fill in one pass
**What:** Given the live sandbox ID set from DynamoDB, remove map entries whose IDs are not in the set, then assign numbers to IDs in the set that have no map entry.
**When to use:** Called from `list.go` after fetching DynamoDB records.
```go
func Reconcile(s *State, liveSandboxIDs []string) {
    live := make(map[string]struct{}, len(liveSandboxIDs))
    for _, id := range liveSandboxIDs {
        live[id] = struct{}{}
    }
    // Prune stale entries
    for id := range s.Map {
        if _, ok := live[id]; !ok {
            delete(s.Map, id)
        }
    }
    // Assign numbers to unknown live sandboxes
    for _, id := range liveSandboxIDs {
        if _, ok := s.Map[id]; !ok {
            Assign(s, id)
        }
    }
    // Reset if now empty
    if len(s.Map) == 0 {
        s.Next = 1
    }
}
```

### Pattern 4: Destroy hook — call Remove after DynamoDB delete succeeds
**What:** After Step 12 (delete metadata from DynamoDB) in `destroy.go`, call `localnumber.Remove`. Non-fatal.
**When to use:** Both the main destroy path and the Docker destroy path.

### Anti-Patterns to Avoid
- **Storing numbers in DynamoDB:** Design spec explicitly says local-only. Never sync to DynamoDB.
- **Using positional index from `records[num-1]`:** Current `sandbox_ref.go` does this — it must be replaced with `localnumber.Resolve`.
- **Calling AWS list just to resolve a number:** New resolve path reads local file — no network call needed.
- **Non-atomic saves:** Direct `os.WriteFile` to the real path risks corruption on crash mid-write.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Atomic file write | Custom lock file or in-place overwrite | write-tmp + `os.Rename` | Rename is atomic on POSIX; already proven in codebase |
| XDG config dir | Hardcoded `~/.config` | `os.UserConfigDir()` | Handles macOS, Linux, Windows correctly |

---

## Common Pitfalls

### Pitfall 1: `os.UserConfigDir()` may require parent dir creation
**What goes wrong:** `os.WriteFile` fails if `~/.config/km/` doesn't exist yet.
**Why it happens:** First run on a fresh machine has no `~/.config/km/` directory.
**How to avoid:** Call `os.MkdirAll(filepath.Dir(stateFilePath), 0o700)` in `Save` before writing.
**Warning signs:** `no such file or directory` error on first `km create`.

### Pitfall 2: Reconcile must happen before display, not after
**What goes wrong:** If reconcile runs after building the display table, newly assigned numbers won't be shown.
**Why it happens:** Reconcile mutates state and must be saved before `printSandboxTable` is called.
**How to avoid:** In `runList`: Load → Reconcile(records) → Save → printSandboxTable(records with numbers).

### Pitfall 3: Number lookup in `sandbox_ref.go` — load failure should fall back gracefully
**What goes wrong:** If `Load` fails (e.g., disk full), numeric resolution returns an unhelpful error.
**Why it happens:** Local file I/O can fail on degraded systems.
**How to avoid:** If `Load` returns an error for a numeric ref, return a clear error: "could not load local sandbox numbers: %w — use sandbox ID directly".

### Pitfall 4: Docker destroy path also needs Remove
**What goes wrong:** Docker sandboxes destroyed via `runDestroyDocker` leave stale entries in the local map.
**Why it happens:** `destroy.go` has two destroy paths — the main EC2/ECS path (lines ~390-530) and the Docker path (lines ~528+). Both must call `localnumber.Remove`.
**How to avoid:** Factor out a `removeLocalNumber(sandboxID string)` helper called from both paths.

### Pitfall 5: list.go currently uses `i+1` for the row number
**What goes wrong:** After wiring, if the old `i+1` positional number is left in place, the table will show wrong numbers.
**Why it happens:** `printSandboxTable` currently uses `num := bw(fmt.Sprintf("%-3d", i+1))` on line 229 of list.go.
**How to avoid:** Pass a `numbers map[string]int` (or embed the number in SandboxRecord) to `printSandboxTable`. Look up `numbers[r.SandboxID]` instead of `i+1`.

---

## Code Examples

### State type and file path
```go
// pkg/localnumber/localnumber.go
package localnumber

import (
    "encoding/json"
    "os"
    "path/filepath"
)

type State struct {
    Next int            `json:"next"`
    Map  map[string]int `json:"map"`
}

func StateFilePath() (string, error) {
    dir, err := os.UserConfigDir()
    if err != nil {
        home, herr := os.UserHomeDir()
        if herr != nil {
            return "", herr
        }
        dir = filepath.Join(home, ".config")
    }
    return filepath.Join(dir, "km", "local-numbers.json"), nil
}

func Load() (*State, error) {
    path, err := StateFilePath()
    if err != nil {
        return &State{Next: 1, Map: map[string]int{}}, nil
    }
    data, err := os.ReadFile(path)
    if os.IsNotExist(err) {
        return &State{Next: 1, Map: map[string]int{}}, nil
    }
    if err != nil {
        return nil, err
    }
    var s State
    if err := json.Unmarshal(data, &s); err != nil {
        return &State{Next: 1, Map: map[string]int{}}, nil // corrupt file → fresh start
    }
    if s.Map == nil {
        s.Map = map[string]int{}
    }
    if s.Next < 1 {
        s.Next = 1
    }
    return &s, nil
}

func Save(s *State) error {
    path, err := StateFilePath()
    if err != nil {
        return err
    }
    if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
        return err
    }
    data, err := json.MarshalIndent(s, "", "  ")
    if err != nil {
        return err
    }
    tmp := path + ".tmp"
    if err := os.WriteFile(tmp, data, 0o600); err != nil {
        return err
    }
    return os.Rename(tmp, path)
}
```

### Assign and Remove
```go
// Assign assigns the next number to sandboxID and increments Next.
// Returns the assigned number.
func Assign(s *State, sandboxID string) int {
    if s.Map == nil {
        s.Map = map[string]int{}
    }
    if existing, ok := s.Map[sandboxID]; ok {
        return existing // idempotent
    }
    num := s.Next
    s.Map[sandboxID] = num
    s.Next++
    return num
}

// Remove removes sandboxID from the map. If the map becomes empty, resets Next to 1.
func Remove(s *State, sandboxID string) {
    delete(s.Map, sandboxID)
    if len(s.Map) == 0 {
        s.Next = 1
    }
}

// Resolve returns the sandbox ID for a given local number. Returns "", false if not found.
func Resolve(s *State, num int) (string, bool) {
    for id, n := range s.Map {
        if n == num {
            return id, true
        }
    }
    return "", false
}
```

### Wiring in create.go (after sandbox ID is generated)
```go
// After sandboxID := compiler.GenerateSandboxID(...)
state, err := localnumber.Load()
if err != nil {
    log.Warn().Err(err).Msg("could not load local sandbox numbers (non-fatal)")
    state = &localnumber.State{Next: 1, Map: map[string]int{}}
}
assignedNum := localnumber.Assign(state, sandboxID)
if saveErr := localnumber.Save(state); saveErr != nil {
    log.Warn().Err(saveErr).Msg("could not save local sandbox numbers (non-fatal)")
}
// Later in final output:
fmt.Printf("Sandbox #%d %s created successfully. (%s)\n", assignedNum, sandboxID, elapsed)
```

### Wiring in list.go (reconcile + display)
```go
// After records are fetched, before printSandboxTable
state, _ := localnumber.Load()
if state == nil {
    state = &localnumber.State{Next: 1, Map: map[string]int{}}
}
ids := make([]string, len(records))
for i, r := range records {
    ids[i] = r.SandboxID
}
localnumber.Reconcile(state, ids)
_ = localnumber.Save(state) // best-effort

// Pass numbers map to printSandboxTable
numbers := state.Map
return printSandboxTable(cmd, records, wide, numbers)
```

### Wiring in sandbox_ref.go (numeric resolution)
```go
// Replace the current positional lookup block:
num, err := strconv.Atoi(ref)
if err != nil || num < 1 {
    return "", fmt.Errorf("invalid sandbox reference %q: ...")
}
state, loadErr := localnumber.Load()
if loadErr != nil {
    return "", fmt.Errorf("could not load local sandbox numbers: %w — use sandbox ID directly", loadErr)
}
sandboxID, ok := localnumber.Resolve(state, num)
if !ok {
    return "", fmt.Errorf("sandbox #%d not found in local sandbox numbers", num)
}
fmt.Printf("Resolved #%d → %s\n", num, sandboxID)
return sandboxID, nil
```

### Wiring in destroy.go (after Step 12 DynamoDB delete)
```go
// After successful DynamoDB delete, non-fatal:
if state, loadErr := localnumber.Load(); loadErr == nil {
    localnumber.Remove(state, sandboxID)
    _ = localnumber.Save(state)
}
```

---

## Touchpoint Inventory

| File | Change | Notes |
|------|--------|-------|
| `pkg/localnumber/localnumber.go` | **New file** | State, Load, Save, Assign, Remove, Resolve, Reconcile |
| `pkg/localnumber/localnumber_test.go` | **New file** | Unit tests, no AWS |
| `internal/app/cmd/create.go` | **Modify** | After sandboxID assigned: Load, Assign, Save; print `#N` in final output |
| `internal/app/cmd/list.go` | **Modify** | After records fetched: Reconcile, Save; change `printSandboxTable` to accept numbers map |
| `internal/app/cmd/sandbox_ref.go` | **Modify** | Replace positional list-lookup with `localnumber.Resolve` |
| `internal/app/cmd/destroy.go` | **Modify** | After Step 12 DynamoDB delete: Load, Remove, Save (non-fatal) |

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) |
| Config file | none |
| Quick run command | `go test ./pkg/localnumber/... -v` |
| Full suite command | `go test ./internal/app/cmd/... ./pkg/localnumber/...` |

### Phase Requirements → Test Map
| ID | Behavior | Test Type | Automated Command | File Exists? |
|----|----------|-----------|-------------------|-------------|
| LOCAL-01 | Assign increments counter, is idempotent | unit | `go test ./pkg/localnumber/... -run TestAssign` | ❌ Wave 0 |
| LOCAL-02 | Remove deletes entry, resets counter when empty | unit | `go test ./pkg/localnumber/... -run TestRemove` | ❌ Wave 0 |
| LOCAL-03 | Resolve returns correct ID for number | unit | `go test ./pkg/localnumber/... -run TestResolve` | ❌ Wave 0 |
| LOCAL-04 | Reconcile prunes stale + assigns unknowns | unit | `go test ./pkg/localnumber/... -run TestReconcile` | ❌ Wave 0 |
| LOCAL-05 | Load returns empty state on missing file | unit | `go test ./pkg/localnumber/... -run TestLoad` | ❌ Wave 0 |
| LOCAL-06 | Save is atomic (tmp+rename) | unit | `go test ./pkg/localnumber/... -run TestSave` | ❌ Wave 0 |
| LOCAL-07 | list output shows persistent numbers not positional | unit | `go test ./internal/app/cmd/... -run TestListCmd` | ❌ Wave 0 |
| LOCAL-08 | sandbox_ref resolves numeric ref from local file | unit | `go test ./internal/app/cmd/... -run TestResolveSandboxID` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/localnumber/...`
- **Per wave merge:** `go test ./internal/app/cmd/... ./pkg/localnumber/...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/localnumber/localnumber.go` — new package (all tests above depend on it)
- [ ] `pkg/localnumber/localnumber_test.go` — unit tests for all functions using `t.TempDir()`

*(All gaps are Wave 0 — this is a new package with no existing test infrastructure)*

---

## Open Questions

1. **Does `printSandboxTable` receive a `map[string]int` or should number be embedded in `SandboxRecord`?**
   - What we know: `SandboxRecord` is in `pkg/aws` — adding a `LocalNumber int` field there crosses the layer boundary since local numbers are explicitly not synced to AWS.
   - What's unclear: Whether a separate `map[string]int` parameter to `printSandboxTable` is cleaner than a parallel slice.
   - Recommendation: Pass `numbers map[string]int` as an extra parameter to `printSandboxTable` and to `runList`. Keeps `SandboxRecord` AWS-only.

2. **Should `km create --remote` (Lambda path) assign a number?**
   - What we know: Remote creates run the Lambda, not local `create.go`. The number assignment in `create.go` only runs on the local operator machine.
   - What's unclear: Whether remote creates should proactively assign a number.
   - Recommendation: Don't assign during remote creates — the Reconcile call in `km list` will assign on next list. Consistent with the design spec's "remote creates get a number on first `km list`" statement.

---

## Sources

### Primary (HIGH confidence)
- Codebase review: `internal/app/cmd/list.go` — current positional numbering at line 229 (`i+1`)
- Codebase review: `internal/app/cmd/sandbox_ref.go` — current numeric resolution at line 63 (`records[num-1].SandboxID`)
- Codebase review: `internal/app/cmd/destroy.go` — Step 12 DynamoDB delete (line 447+); Docker destroy path (line 528+)
- Codebase review: `internal/app/cmd/ebpf_attach.go` lines 485-494 — atomic write pattern (tmp+rename)
- Codebase review: `internal/app/config/config.go` line 170 — `os.UserHomeDir()` pattern
- Design spec: `docs/superpowers/specs/2026-04-13-persistent-local-sandbox-numbering-design.md`
- Go stdlib: `os.UserConfigDir()` — XDG-compliant config directory resolution

### Secondary (MEDIUM confidence)
- Go stdlib docs: `os.Rename` is atomic on POSIX when source and dest are on the same filesystem (which `~/.config/km/local-numbers.json.tmp` and `local-numbers.json` always are)

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — stdlib only, no new dependencies
- Architecture: HIGH — all touchpoints identified, atomic write pattern established in codebase
- Pitfalls: HIGH — both destroy paths, reconcile ordering, and UserConfigDir dir-creation all verified from code review

**Research date:** 2026-04-13
**Valid until:** 2026-05-13 (stable Go stdlib, no external dependencies)
