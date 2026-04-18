# Phase 55: Learn Mode Command Capture — Research

**Researched:** 2026-04-18
**Domain:** Shell command capture, eBPF observe flow, allowlistgen recorder extension
**Confidence:** HIGH (all findings from direct codebase inspection)

## Summary

Phase 55 extends the learn mode pipeline to record shell commands typed during `km shell --learn` sessions. The goal is to surface those commands in the generated `SandboxProfile` as `spec.execution.initCommands` suggestions, helping operators turn an exploratory session into a reproducible profile.

The existing audit infrastructure already captures every shell command via a PROMPT_COMMAND hook in `/etc/profile.d/km-audit.sh` (written by the userdata compiler). This hook fires on every interactive bash prompt for both root and the sandbox user, emits a JSON line to `/run/km/audit-pipe`, and writes the user name. The learn mode extension can leverage this exact hook for EC2, writing commands to a separate file during `--observe` mode and flushing them alongside the existing DNS/host/repo state.

For Docker, the path is different: commands must be collected from the audit-log container's stdout logs (already captured), or from an additional PROMPT_COMMAND file that gets written into a log accessible via `docker logs`. The cleanest approach for Docker is writing commands to a second named file (`/run/km/learn-commands.log`) in the container, readable via `docker exec` on post-exit.

**Primary recommendation:** Extend the `learnObservedState` struct with a `Commands []string` field, capture from the existing PROMPT_COMMAND audit pipe on EC2 via a companion file in `--observe` mode, read from audit-log logs on Docker, flush/fetch alongside the network data, and emit as a YAML comment block and optional `initCommands` slice in `GenerateAnnotatedYAML`.

## The Existing Infrastructure (Critical Context)

Understanding what already exists prevents duplicate work and shows the minimal delta needed.

### Already in Place

**PROMPT_COMMAND audit hook** (`/etc/profile.d/km-audit.sh`, written by `pkg/compiler/userdata.go` lines 466-495):
- Fires `_km_audit()` on every bash prompt for all login shells (SSM sessions)
- Captures last command via `HISTTIMEFORMAT= history 1 | sed 's/^ *[0-9]* *//'`
- Emits JSON line: `{"timestamp":"...","sandbox_id":"...","event_type":"command","source":"shell","detail":{"command":"...","user":"..."}}`
- Writes to `/run/km/audit-pipe` (a named FIFO, world-writable 0666)
- Covers both `root` (when `km shell --root`) and `sandbox` user (default session)
- Installed for ALL login shells — not learn-mode-specific

**eBPF enforcer observe mode** (`internal/app/cmd/ebpf_attach.go`):
- `--observe` flag creates an `allowlistgen.Recorder`
- `flushObservedState()` serializes `{dns, hosts, repos, refs}` to JSON, writes atomically to `/tmp/km-observed.json`, uploads to `s3://KM_ARTIFACTS_BUCKET/learn/{sandboxID}/{timestamp}.json`
- SIGUSR1 triggers snapshot flush without shutdown; SIGTERM triggers final flush
- The SIGUSR1 path is called from `km shell --learn` post-exit via `flushEC2Observations()` (SSM SendCommand)

**learnObservedState struct** (`internal/app/cmd/shell.go` line 434):
```go
type learnObservedState struct {
    DNS   []string `json:"dns"`
    Hosts []string `json:"hosts"`
    Repos []string `json:"repos"`
    Refs  []string `json:"refs,omitempty"`
}
```

**observedState struct** (`internal/app/cmd/ebpf_attach.go` line 129):
```go
type observedState struct {
    DNS   []string `json:"dns"`
    Hosts []string `json:"hosts"`
    Repos []string `json:"repos"`
    Refs  []string `json:"refs,omitempty"`
}
```

Both structs are duplicates of each other — one on the sandbox side (ebpf_attach.go) and one on the operator side (shell.go). Both need `Commands []string` added.

**allowlistgen.Recorder** (`pkg/allowlistgen/recorder.go`):
- Map-based deduplication for dns, hosts, repos, refs
- All methods are mutex-protected, safe for concurrent use
- Has `RecordDNSQuery`, `RecordHost`, `RecordRepo`, `RecordRef` — needs `RecordCommand`

**Generator output** (`pkg/allowlistgen/generator.go`):
- `Generate()` builds a `profile.SandboxProfile` — does NOT populate `InitCommands`
- `GenerateAnnotatedYAML()` adds header comments with counts
- GitHub access section is conditionally appended when repos/refs observed

**ExecutionSpec.InitCommands** (`pkg/profile/types.go` line 182):
```go
InitCommands []string `yaml:"initCommands,omitempty"`
```
Already in schema. Generator just needs to populate it.

## Standard Stack (No New Dependencies)

All required functionality is achievable with existing packages. No new libraries needed.

| Component | Mechanism | Why |
|-----------|-----------|-----|
| EC2 command capture | File sidecar to `/run/km/audit-pipe` FIFO + companion log file | FIFO already exists, hook already fires |
| EC2 flush | Extend `flushObservedState` to include commands from Recorder | Already called on SIGUSR1 and shutdown |
| Docker command capture | Read audit-log container stdout, parse JSON lines | Already done for DNS/HTTP proxy logs |
| Recorder extension | Add `commandObserved []string` slice (ordered) to Recorder | Slice, not map, to preserve meaningful order |
| Generator extension | Populate `p.Spec.Execution.InitCommands` in Generate() | Field already in schema |
| Annotated YAML | Add `# Commands observed:` comment block | Mirrors existing DNS suffix summary pattern |

## Architecture Patterns

### Capture Mechanism Decision: PROMPT_COMMAND File Tap

**EC2 path (HIGH confidence):**

The existing `/etc/profile.d/km-audit.sh` hook already captures every command for both users. When `--observe` is enabled on the eBPF enforcer, the userdata template should additionally write commands to a dedicated learn log file (`/run/km/learn-commands.log`), not just the audit FIFO. The eBPF enforcer process can tail this file and feed commands into the Recorder.

Alternative approach (simpler, preferred): Do NOT modify the eBPF enforcer's hot path. Instead, at SIGUSR1 flush time, use SSM SendCommand to read `/run/km/learn-commands.log` from the instance and include it in the flushed JSON. This avoids any goroutine overhead in the enforcer.

Even simpler approach (recommended): The PROMPT_COMMAND hook already writes to the FIFO. In `--observe` mode, the userdata template should install a second hook that appends to `/run/km/learn-commands.log` (a plain file, not FIFO), written directly by the shell. The eBPF enforcer reads this file at flush time.

**Docker path:**

The audit-log container receives all PROMPT_COMMAND events on its stdin (the FIFO). These appear in `docker logs km-{sandboxID}-audit-log`. The existing `CollectDockerObservations` reads DNS and HTTP proxy logs but not audit-log. Add audit-log log reading alongside the others.

### Recommended Project Structure Changes

```
pkg/allowlistgen/
├── recorder.go          # add commandObserved []string, RecordCommand(), Commands()
├── recorder_tls.go      # unchanged
└── generator.go         # populate InitCommands, add comment block

internal/app/cmd/
├── shell.go             # add Commands field to learnObservedState and both struct bodies
└── ebpf_attach.go       # add Commands field to observedState, read from log file at flush

pkg/compiler/
└── userdata.go          # add learn-commands.log append to PROMPT_COMMAND hook when LearnMode
```

### Pattern 1: Recorder Extension

**What:** Add ordered slice (not deduplicating map) to Recorder for commands.
**When to use:** Commands are ordered — deduplication yes, but order of first-seen matters.
**Example:**
```go
// Source: codebase inspection of recorder.go
type Recorder struct {
    mu              sync.Mutex
    dnsObserved     map[string]struct{}
    hostObserved    map[string]struct{}
    repoObserved    map[string]struct{}
    refObserved     map[string]struct{}
    commandSeen     map[string]struct{} // dedup set
    commandOrdered  []string            // ordered first-seen slice
}

func (r *Recorder) RecordCommand(cmd string) {
    cmd = strings.TrimSpace(cmd)
    if cmd == "" {
        return
    }
    r.mu.Lock()
    defer r.mu.Unlock()
    if _, ok := r.commandSeen[cmd]; !ok {
        r.commandSeen[cmd] = struct{}{}
        r.commandOrdered = append(r.commandOrdered, cmd)
    }
}

func (r *Recorder) Commands() []string {
    r.mu.Lock()
    out := make([]string, len(r.commandOrdered))
    copy(out, r.commandOrdered)
    r.mu.Unlock()
    return out
}
```

### Pattern 2: userdata.go Learn-Mode PROMPT_COMMAND Hook

**What:** When `LearnMode` is true, add a second append to the PROMPT_COMMAND that writes to a plain file.
**When to use:** EC2 sandboxes with `observability.learnMode: true`.
**Example (addition to km-audit.sh section in userdata template):**
```bash
{{- if .LearnMode }}
# Learn mode: also write commands to /run/km/learn-commands.log
# This file is read at flush time by the eBPF enforcer (or SSM) to build initCommands.
_km_learn() {
  local cmd
  cmd=$(HISTTIMEFORMAT= history 1 | sed 's/^ *[0-9]* *//')
  [ -z "$cmd" ] && return 0
  printf '%s\t%s\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$(whoami)" "$cmd" \
    >> /run/km/learn-commands.log 2>/dev/null
}
PROMPT_COMMAND="_km_audit;_km_learn;${PROMPT_COMMAND}"
{{- else }}
PROMPT_COMMAND="_km_audit;${PROMPT_COMMAND}"
{{- end }}
```
The learn-commands.log format: `timestamp TAB user TAB command` — simple, no JSON parsing needed at read time.

### Pattern 3: eBPF Enforcer Reads learn-commands.log at Flush

**What:** In `flushObservedState`, after marshaling dns/hosts/repos, also read `/run/km/learn-commands.log` if it exists, parse lines, add to observedState.Commands.
**When to use:** EC2 path only.
**Example:**
```go
// In flushObservedState or a new readLearnCommands() helper:
func readLearnCommands(path string) []string {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil
    }
    seen := make(map[string]struct{})
    var cmds []string
    for _, line := range strings.Split(string(data), "\n") {
        parts := strings.SplitN(line, "\t", 3)
        if len(parts) < 3 {
            continue
        }
        cmd := strings.TrimSpace(parts[2])
        if cmd == "" {
            continue
        }
        if _, ok := seen[cmd]; !ok {
            seen[cmd] = struct{}{}
            cmds = append(cmds, cmd)
        }
    }
    return cmds
}
```

### Pattern 4: Docker Command Collection

**What:** In `CollectDockerObservations`, add audit-log container log parsing alongside DNS and HTTP proxy logs.
**When to use:** Docker substrate learn sessions.
**Example:**
```go
// In shell.go, CollectDockerObservations:
auditContainer := fmt.Sprintf("km-%s-audit-log", sandboxID)
if auditOut, auditErr := exec.CommandContext(ctx, "docker", "logs", auditContainer).Output(); auditErr == nil {
    // Parse JSON lines for event_type=="command"
    parseAuditLogCommands(bytes.NewBuffer(auditOut), rec)
}
```
```go
func parseAuditLogCommands(r io.Reader, rec *allowlistgen.Recorder) {
    scanner := bufio.NewScanner(r)
    for scanner.Scan() {
        var event struct {
            EventType string `json:"event_type"`
            Detail    struct {
                Command string `json:"command"`
            } `json:"detail"`
        }
        if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
            continue
        }
        if event.EventType == "command" && event.Detail.Command != "" {
            rec.RecordCommand(event.Detail.Command)
        }
    }
}
```

### Pattern 5: Generator Output

**What:** Populate `InitCommands` in generated profile and add annotation comment.
**Example (generator.go changes):**
```go
// In Generate():
cmds := r.Commands()
if len(cmds) > 0 {
    p.Spec.Execution.InitCommands = cmds
}

// In GenerateAnnotatedYAML():
if len(cmds) > 0 {
    out.WriteString("# Commands observed (as initCommands suggestions):\n")
    for _, cmd := range cmds {
        fmt.Fprintf(&out, "#   %s\n", cmd)
    }
    out.WriteString("#\n")
}
```

### Anti-Patterns to Avoid

- **Tailing the FIFO from the eBPF enforcer:** The audit-pipe FIFO is a write-once sink — the audit-log sidecar is the reader. Opening a second reader on a FIFO causes blocking or missed events. Use the companion log FILE approach instead.
- **Parsing bash history file directly:** `~/.bash_history` is only flushed on shell exit and doesn't capture root's history (different home). The PROMPT_COMMAND approach captures in-session, both users.
- **eBPF execve tracepoints:** Powerful but requires root + CAP_BPF in the enforcer, complex buffering, and picks up all processes (sidecar internals, SSM agent). Much higher complexity than tapping the existing PROMPT_COMMAND hook.
- **auditd:** Available on Amazon Linux 2023 but requires additional setup, generates verbose output for all syscalls, and the existing PROMPT_COMMAND hook is already more targeted. Adds operational complexity with no benefit for this use case.
- **Including ALL commands verbatim in initCommands:** Many commands are noise (e.g., `ls`, `pwd`, `cd`, `history`). The generator should filter or annotate, not blindly emit everything into `initCommands`. Use a comment block for the full list; only emit into `initCommands` what looks like setup commands (installs, config changes) — or emit ALL as comments, let operator curate.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Command attribution to user | Custom UID tracking | Existing `$(whoami)` in PROMPT_COMMAND hook | Already emits user field |
| Ordered deduplication | Custom data structure | Slice + map pair (standard Go idiom) | Simple, sufficient |
| Atomic file write | Custom temp-rename | Existing pattern in `flushObservedState` (write to `.tmp`, rename) | Already proven, atomic |
| Log parsing | JSON parser from scratch | `encoding/json` + `bufio.Scanner` | Standard library, already used |

## Common Pitfalls

### Pitfall 1: Writing to FIFO from Two Goroutines
**What goes wrong:** The audit-pipe is a named FIFO consumed by km-audit-log sidecar. If the eBPF enforcer tries to read from it (second reader on FIFO), it will either block or starve the sidecar of events.
**Why it happens:** FIFOs have exactly one reader. Attempts to open a FIFO for reading block until a writer opens it, and with a second reader, events are split non-deterministically.
**How to avoid:** Use a separate plain log file (`/run/km/learn-commands.log`) for learn mode capture. The PROMPT_COMMAND hook writes to both the FIFO (for audit-log sidecar) and the log file (for learn mode).
**Warning signs:** audit-log stops receiving events during a learn session.

### Pitfall 2: Commands Captured Before Root Prompt is Configured
**What goes wrong:** When connecting as root (`km shell --root`), the SSM session starts with `/bin/sh` or a non-login shell, so `/etc/profile.d/km-audit.sh` may not be sourced.
**Why it happens:** SSM starts-session (root path) does not use `--document-name AWS-StartInteractiveCommand`; it uses a raw session. Non-login shells don't source `/etc/profile.d/`.
**How to avoid:** The learn-commands.log file should be world-writable (0666). For root sessions, the `km-sandbox-shell` wrapper may need to source the profile explicitly. Check the userdata template's `km-sandbox-shell` section.
**Warning signs:** Zero commands captured for root sessions despite activity.

### Pitfall 3: Noise Commands in initCommands
**What goes wrong:** Emitting `ls`, `pwd`, `cd /workspace`, `history`, etc. as `initCommands` produces invalid or useless profile output.
**Why it happens:** PROMPT_COMMAND captures everything — navigation, inspection, retries.
**How to avoid:** Two strategies:
  - Strategy A (simpler): Emit ALL commands as YAML comments (not in `initCommands`). Let operator select and uncomment what they want. This is a documentation/suggestion pattern, not auto-configuration.
  - Strategy B: Apply a blocklist of known noise commands (`ls`, `pwd`, `cd`, `cat`, `less`, `man`, `history`, `exit`, `clear`). Emit only non-blocked commands into `initCommands`.
  - Recommended: Strategy A (comments only) for v1. Operator is the right filter.
**Warning signs:** Generated profile fails `km validate` because initCommands contains interactive commands that don't work as one-shot setup.

### Pitfall 4: learn-commands.log Missing on S3-Fetched Observed JSON
**What goes wrong:** The eBPF enforcer reads `learn-commands.log` at flush time on the sandbox, but the `observedState` JSON written to S3 doesn't include commands because the field was not added to the struct.
**Why it happens:** Two parallel structs (`observedState` in ebpf_attach.go and `learnObservedState` in shell.go) — both need `Commands` field added.
**How to avoid:** Add `Commands []string` to both structs with same JSON tag `"commands,omitempty"`. Ensure `flushObservedState` populates it. Ensure `GenerateProfileFromJSON` reads it and feeds `rec.RecordCommand`.
**Warning signs:** Generated profile has no `initCommands` section despite commands being typed.

### Pitfall 5: Docker audit-log container name mismatch
**What goes wrong:** `km-{sandboxID}-audit-log` container name doesn't match actual Docker Compose container name.
**Why it happens:** Container naming convention defined in compose.go may use a different suffix.
**How to avoid:** Check `pkg/compiler/compose.go` for the exact `container_name` pattern used for the audit-log sidecar before hardcoding the name in `CollectDockerObservations`.
**Warning signs:** `docker logs` returns "No such container" error.

## Code Examples

### Struct Extension (Both Files)

```go
// Source: direct codebase inspection of shell.go:434 and ebpf_attach.go:129
// Both structs need the same Commands field.

// In shell.go:
type learnObservedState struct {
    DNS      []string `json:"dns"`
    Hosts    []string `json:"hosts"`
    Repos    []string `json:"repos"`
    Refs     []string `json:"refs,omitempty"`
    Commands []string `json:"commands,omitempty"` // NEW
}

// In ebpf_attach.go:
type observedState struct {
    DNS      []string `json:"dns"`
    Hosts    []string `json:"hosts"`
    Repos    []string `json:"repos"`
    Refs     []string `json:"refs,omitempty"`
    Commands []string `json:"commands,omitempty"` // NEW
}
```

### GenerateProfileFromJSON Extension

```go
// Source: shell.go:446 GenerateProfileFromJSON — existing loop pattern
for _, cmd := range state.Commands {
    rec.RecordCommand(cmd)
}
```

### CollectDockerObservations Extension

```go
// Source: shell.go:471 CollectDockerObservations — existing docker logs pattern
// The audit-log container name follows the same km-{sandboxID}-{role} pattern.
// Verify exact suffix in pkg/compiler/compose.go before implementing.
auditContainer := fmt.Sprintf("km-%s-audit-log", sandboxID)
if auditOut, auditErr := exec.CommandContext(ctx, "docker", "logs", auditContainer).Output(); auditErr == nil {
    auditBuf = bytes.NewBuffer(auditOut)
    allowlistgen.ParseAuditLogCommands(auditBuf, rec)
} else {
    log.Warn().Err(auditErr).Str("container", auditContainer).Msg("learn: failed to get audit-log container logs (non-fatal)")
}
```

## State of the Art

| Old Approach | Current Approach | Impact |
|--------------|------------------|--------|
| No command capture in learn mode | PROMPT_COMMAND hook fires but not captured for learn | Gap to close |
| All learn data from eBPF network events only | Extend with shell command events from existing hook | Richer initCommands suggestions |
| initCommands empty in generated profiles | Populate from observed commands (as comments or real values) | Faster operator workflow |

## Open Questions

1. **Should commands go into `initCommands` directly or only as YAML comments?**
   - What we know: initCommands runs sequentially as root at bootstrap. Interactive commands (cd, ls, pwd) will fail or be wrong.
   - What's unclear: Whether the operator benefit of auto-populated `initCommands` outweighs the noise/error risk.
   - Recommendation: Emit as YAML comments only (Strategy A above). Clean, safe, operator-curated.

2. **Does km shell --root capture commands via PROMPT_COMMAND?**
   - What we know: Root SSM sessions use `aws ssm start-session` without `--document-name AWS-StartInteractiveCommand`. The root session may not source `/etc/profile.d/`.
   - What's unclear: Whether the root SSM session runs a login shell that sources profile.d.
   - Recommendation: Check the userdata template's `km-sandbox-shell` section (line ~1357). If root sessions bypass profile.d, add a BASH_ENV hook or update the root session document.

3. **Exact Docker compose container name for audit-log**
   - What we know: DNS container is `km-{sandboxID}-dns-proxy`, HTTP is `km-{sandboxID}-http-proxy` (shell.go lines 529-530).
   - What's unclear: Exact audit-log container name in Docker Compose.
   - Recommendation: Read `pkg/compiler/compose.go` at plan time to confirm before hardcoding.

4. **learn-commands.log file permissions and location**
   - What we know: `/run/km/` is created with `chown km-sidecar:km-sidecar` and `audit-pipe` is 0666.
   - What's unclear: Whether `/run/km/learn-commands.log` needs explicit creation before the PROMPT_COMMAND hook appends to it.
   - Recommendation: Create the file in userdata with `touch /run/km/learn-commands.log && chmod 666 /run/km/learn-commands.log` when LearnMode is true.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) + testify assertions |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./pkg/allowlistgen/... ./internal/app/cmd/... -run TestLearn -v` |
| Full suite command | `make build && go test ./...` |

### Phase Requirements → Test Map
| ID | Behavior | Test Type | Automated Command | File Exists? |
|----|----------|-----------|-------------------|-------------|
| LEARN-CMD-01 | Recorder.RecordCommand deduplicates, preserves order | unit | `go test ./pkg/allowlistgen/... -run TestRecordCommand` | Wave 0 |
| LEARN-CMD-02 | Recorder.Commands() returns ordered slice | unit | `go test ./pkg/allowlistgen/... -run TestCommands` | Wave 0 |
| LEARN-CMD-03 | Generate() populates InitCommands (or comment block) | unit | `go test ./pkg/allowlistgen/... -run TestGenerateCommands` | Wave 0 |
| LEARN-CMD-04 | GenerateProfileFromJSON feeds Commands into Recorder | unit | `go test ./internal/app/cmd/... -run TestGenerateProfileFromJSON` | existing |
| LEARN-CMD-05 | learnObservedState JSON round-trips Commands field | unit | `go test ./internal/app/cmd/... -run TestLearnObservedState` | Wave 0 |
| LEARN-CMD-06 | parseAuditLogCommands parses JSON audit lines correctly | unit | `go test ./internal/app/cmd/... -run TestParseAuditLog` | Wave 0 |
| LEARN-CMD-07 | CollectDockerObservations includes commands from audit log | unit | `go test ./internal/app/cmd/... -run TestCollectDockerObservations` | existing |

### Sampling Rate
- **Per task commit:** `go test ./pkg/allowlistgen/... ./internal/app/cmd/...`
- **Per wave merge:** `make build && go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/allowlistgen/recorder_commands_test.go` — covers LEARN-CMD-01, LEARN-CMD-02
- [ ] `pkg/allowlistgen/generator_commands_test.go` — covers LEARN-CMD-03
- [ ] `internal/app/cmd/shell_commands_test.go` — covers LEARN-CMD-05, LEARN-CMD-06, LEARN-CMD-07
- [ ] `internal/app/cmd/ebpf_attach_commands_test.go` — build-tagged `linux && amd64`, covers observedState Commands field

## Sources

### Primary (HIGH confidence)
- Direct codebase inspection:
  - `pkg/allowlistgen/recorder.go` — Recorder struct and all existing RecordX methods
  - `pkg/allowlistgen/generator.go` — Generate() and GenerateAnnotatedYAML() output structure
  - `internal/app/cmd/shell.go` — learnObservedState, GenerateProfileFromJSON, CollectDockerObservations, runLearnPostExit
  - `internal/app/cmd/ebpf_attach.go` — observedState, flushObservedState, SIGUSR1 flow
  - `pkg/compiler/userdata.go` (lines 462-496) — PROMPT_COMMAND audit hook, km-audit.sh template
  - `pkg/compiler/userdata.go` (lines 1680-1710) — UserDataParams struct, LearnMode field
  - `pkg/profile/types.go` (lines 166-210) — ExecutionSpec.InitCommands field

### Secondary (MEDIUM confidence)
- Amazon Linux 2023 default shell behavior: bash login shells source `/etc/profile.d/` — standard Linux convention, HIGH confidence for sandbox user (SSM uses `AWS-StartInteractiveCommand` with `sudo -u sandbox -i` which is a login shell). Root SSM sessions need verification.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all existing code inspected directly
- Architecture: HIGH — pattern is clear extension of proven observe flow
- Pitfalls: HIGH — derived from direct inspection of FIFO, struct, and Docker log code
- Open questions: MEDIUM — root shell sourcing and Docker container naming need verification at plan time

**Research date:** 2026-04-18
**Valid until:** 2026-06-18 (codebase is the source of truth; no external dependency)
