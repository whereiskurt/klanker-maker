# Phase 79: km-presence daemon — Research

**Researched:** 2026-05-10
**Domain:** Go systemd sidecar, sandbox userdata template, doctor checks, CloudWatch FilterLogEvents
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **Service name:** `km-presence.service`
- **Process owner:** root (needs utmp read, cross-user pgrep, ss)
- **Implementation:** Go binary at `cmd/km-presence/main.go`, statically linked, ~150 LoC
- **Distribution:** Built into `sidecars/dist/km-presence`, uploaded by `km init --sidecars`
- **systemd unit:** `Type=simple`, `Restart=always`, `RestartSec=5`, `User=root`
- **Tick cadence:** 60 seconds, not configurable
- **Audit pipe output:** same JSON shape as old bash heartbeat, `source:"presence"` (not `"shell"`)
- **Write semantics:** `timeout 0.1 tee /run/km/audit-pipe > /dev/null 2>/dev/null || true` (100ms bound)
- **Five signals (all required):** (1) `who` non-empty, (2) tmux clients >0, (3) newer email file, (4) newer slack stamp, (5) `pgrep -af` agent process
- **Emit logic:** boolean OR, no weighting
- **Stamp file:** `/run/km/.presence-last-tick`, touched unconditionally at end of every tick
- **Remove from userdata.go:** lines 1056-1080 (entire `_km_heartbeat` block and EXIT trap)
- **Keep:** lines 1027-1054 (`_KM_AUDIT_INSTALLED` guard, `_km_audit`, PROMPT_COMMAND assignment)
- **touch addition:** `touch /run/km/last-slack-inbound` after successful SQS dispatch in km-slack-inbound-poller
- **New doctor check:** `presence_daemon_healthy`, 5-minute staleness threshold, WARN-not-ERROR
- **Migration:** `make build && km init --sidecars`; existing sandboxes unaffected until destroy+create
- **CLAUDE.md update:** required

### Claude's Discretion

- Go module structure: inline single file vs. split signal files (prefer testability)
- Signal check abstraction: `func() bool` or interface
- Logging library: zerolog (match existing sidecars)
- Test framework: standard `testing` package; table-driven with injectable runner
- Exact systemd unit text: mirror `km-mail-poller.service` / `km-slack-inbound-poller.service`
- EnvironmentFile: NOT needed unless tests reveal otherwise

### Deferred Ideas (OUT OF SCOPE)

1. Operator-side enrichment of `km list` to show which signal is keeping sandbox awake
2. Per-signal weighting / decay
3. Replacing IdleDetector CloudWatch poll with local IPC
4. `--older 7200` filter on signal 5 to exclude long-lived agents
5. Backfilling existing AMIs
6. Configurable tick cadence
</user_constraints>

---

## Summary

Phase 79 replaces `_km_heartbeat` — a bash function forked by every login shell — with a single root-owned systemd daemon (`km-presence.service`) that checks five concrete signals once per 60-second tick. The change is entirely within the existing sidecar pipeline: a new Go binary, a new userdata.go heredoc, and deletion of lines 1056-1080. Nothing downstream (IdleDetector, EventBridge, ttl-handler Lambda, `computeIdleRemaining`) changes.

The codebase research confirms all code patterns needed. The Makefile `sidecars` target compiles Go binaries and uploads them with `aws s3 cp`; `km-slack` is the closest model for `km-presence`. The systemd unit heredoc pattern is verbatim-documented at lines 1775-1812 of `pkg/compiler/userdata.go`. The `_km_heartbeat` function exists in exactly **two** files: `userdata.go` (EC2 path) and `compose.go` (Docker path). Only `userdata.go` is in scope; the PRD decision is to not touch the Docker path yet.

The `touch /run/km/last-slack-inbound` insert point in `km-slack-inbound-poller` is line 1516 of `userdata.go` — after the `echo "[km-slack-inbound-poller] Turn complete…"` line inside the `if [ -n "$NEW_SESSION" ]` block, meaning it only fires on a successful agent run with a non-empty session_id.

**Primary recommendation:** Follow the `km-slack` Makefile/userdata/test pattern exactly; no new patterns needed.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go stdlib (`os/exec`, `os`, `time`) | go 1.21+ | Signal checks via subprocesses + file stat | No external dep needed for the 150-LoC binary |
| `github.com/rs/zerolog` | matches go.mod | Structured logging (per-tick log line) | Used in `sidecars/audit-log/cmd/main.go` — project standard |
| `encoding/json` (stdlib) | — | Emit heartbeat JSON to audit pipe | Same as existing bash `printf` pattern |
| `testing` (stdlib) | — | Unit tests | Project standard |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `os/exec.Command` | stdlib | Invoke `who`, `runuser`, `pgrep`, `find` | All five signal checks |
| `os.Stat` | stdlib | Mtime comparison for signals 3 + 4 | Avoids spawning `find` for signal 4 |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `os/exec` for `who` | `golang.org/x/sys/unix` utmp bindings | exec is simpler, 1ms overhead is acceptable at 60s tick |
| subprocess `pgrep` | `/proc` walk | pgrep is the project's existing pattern; /proc walk requires root logic already available |

**Installation:** No new dependencies. The binary uses only packages already in `go.mod`.

---

## Architecture Patterns

### Recommended Project Structure
```
cmd/km-presence/
├── main.go          # daemon loop, signal checks, emit logic (~150 LoC)
└── main_test.go     # table-driven tests with injectable commandRunner
```

No sub-packages. The binary is simple enough for a flat layout.

### Pattern 1: Sidecar Build Pipeline (from Makefile)

**What:** Cross-compile `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`, output to `build/`, then `aws s3 cp` to `s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-presence`.

**Exact Makefile lines to add (mirror `km-slack` pattern at lines 90-95):**
```makefile
GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -trimpath -ldflags '-s -w' -o build/km-presence ./cmd/km-presence/
# ...in the aws s3 cp block:
aws s3 cp build/km-presence s3://$(KM_ARTIFACTS_BUCKET)/sidecars/km-presence
```

Also add to `build-sidecars` target (local-only, no S3 upload):
```makefile
GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -trimpath -ldflags '-s -w' -o build/km-presence ./cmd/km-presence/
```

**Userdata.go S3 fetch line** — add to the sidecar download block (lines ~894-900), alongside `km-slack`:
```bash
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-presence" /opt/km/bin/km-presence
```
The existing `chmod +x /opt/km/bin/km-*` wildcard at line 900 covers the new binary.

### Pattern 2: Systemd Unit Heredoc in userdata.go

**What:** `cat > /etc/systemd/system/km-presence.service << 'UNIT' ... UNIT` — the exact pattern used for `km-mail-poller.service` (lines 1775-1789) and `km-slack-inbound-poller.service` (lines 1792-1811).

**Key learnings from existing units:**
- `km-mail-poller.service` uses `User=root` and `Environment=` for sandbox-specific vars — correct model for km-presence.
- `km-slack-inbound-poller.service` adds `EnvironmentFile=-/etc/km/notify.env` — km-presence does NOT need this (no notify env vars; no Slack/email vars needed).
- No `EnvironmentFile` needed for km-presence since the daemon reads env from its own logic (sandboxID via `/etc/profile.d/km-identity.sh` or hardcoded at compile time via userdata template variable `{{ .SandboxID }}`).

**Exact unit text (verbatim model, follows km-mail-poller pattern):**
```
cat > /etc/systemd/system/km-presence.service << 'UNIT'
[Unit]
Description=Klankrmkr presence daemon — sandbox liveness heartbeat
After=network.target km-audit-log.service
[Service]
User=root
Environment=SANDBOX_ID={{ .SandboxID }}
ExecStart=/opt/km/bin/km-presence
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT
```

**Insertion point:** After the `km-slack-inbound-poller.service` block (around line 1813), before the `{{- if .VSCodeEnabled }}` block (line 1814). The systemd `daemon-reload` and `enable --now` calls for the unit happen in the existing `systemctl daemon-reload` + enable-all block that follows.

**Alternative insertion:** Before `km-mail-poller.service` (line 1775) — both are valid since ordering within the heredoc section is cosmetic. Prefer after the Slack inbound block for phase-number ordering.

**Note on `After=km-audit-log.service`:** The daemon writes to `/run/km/audit-pipe`, which is created by km-audit-log. Adding the ordering dependency ensures the FIFO exists before the first tick. However, the FIFO is also created during userdata init (line 972), so the daemon's 100ms-timeout-tee write is safe even without this dep. Include it for clarity.

### Pattern 3: Injectable commandRunner for Tests (from km-slack)

**What:** Tests in `cmd/km-slack/main_test.go` use `httptest.NewServer` and pass real clients to helper functions. For km-presence, the cleaner model from the problem domain is an injectable `commandRunner` interface:

```go
type commandRunner interface {
    Output(name string, args ...string) ([]byte, error)
}

type realRunner struct{}
func (r realRunner) Output(name string, args ...string) ([]byte, error) {
    return exec.Command(name, args...).Output()
}
```

Tests inject a `fakeRunner` that returns controlled stdout. File stat operations (signals 3+4) use path parameters so tests can pass `t.TempDir()` paths.

### Anti-Patterns to Avoid

- **Do not use `os.Stat` for `who` check** — `who` is needed for its interpretation (non-empty = active session), not just file existence.
- **Do not write to `/run/km/audit-pipe` with a bare redirect** — Phase 56.1 Bug 2: bare `>` blocks indefinitely when no reader is connected. Always use `timeout 0.1 tee /run/km/audit-pipe > /dev/null 2>/dev/null || true` (the same pattern as `_km_audit` at line 1041).
- **Do not touch `.presence-last-tick` only on emit** — touch must be unconditional at end of tick; conditional touch breaks newer-than comparison for next tick.
- **Do not run the daemon as `km-sidecar` user** — the daemon needs to `pgrep -af` across all users and `runuser -u sandbox -- tmux list-clients`, which requires root.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Mtime comparison for signals 3+4 | Custom inotify or polling loop | `os.Stat().ModTime()` vs stamp file | One line; no race; daemon already ticks at 60s interval |
| Process detection | Walk `/proc` | `pgrep -af` subprocess | pgrep handles edge cases (zombie entries, permission); already proven on AL2023 |
| utmp reading | Parse `/var/run/utmp` binary | `who` subprocess | `who` output is portable and simple to test |
| FIFO write with timeout | Custom goroutine + channel | `exec.Command("timeout", "0.1", "tee", "/run/km/audit-pipe")` | Matches existing bash pattern exactly; avoids reimplementing Phase 56.1 Bug 2 fix |
| Structured logging | `fmt.Printf` | zerolog | Project already uses zerolog in all sidecars |

**Key insight:** The daemon is intentionally simple — 150 LoC Go wrapping five shell invocations. Complexity belongs in the signal checks' test coverage, not in the implementation.

---

## Common Pitfalls

### Pitfall 1: `_km_heartbeat` Also Exists in compose.go
**What goes wrong:** Removing only from `userdata.go` leaves the Docker substrate (`compose.go:436-446`) still installing the heartbeat for Docker sandboxes.
**Why it happens:** Two separate code paths compile the audit hook; the PRD only cites `userdata.go`.
**How to avoid:** The Docker path is explicitly out of scope for Phase 79 (Docker sandboxes cannot run systemd). Document in CLAUDE.md that Docker sandboxes retain bash heartbeat. Do NOT touch `compose.go`.
**Warning signs:** grep for `_km_heartbeat` across both files — `compose.go:436` and `userdata.go:1063`.

### Pitfall 2: systemd Unit NOT Enabled After Installing
**What goes wrong:** Writing the unit file is not enough — `systemctl enable --now km-presence` must be called in the userdata script, or the daemon won't start on first boot.
**Why it happens:** Looking at the existing pattern, `km-mail-poller` and `km-slack-inbound-poller` are enabled via a block that comes AFTER all unit file writes. Confirm that block is unconditional (not inside a conditional).
**How to avoid:** Verify that `systemctl enable --now km-presence` is added to the unconditional enable block in the userdata template.

**Verification:** Search userdata.go for the existing `systemctl enable --now km-mail-poller` to find the exact enable block. At time of research this appears within `{{- if .SandboxEmail }}` — if so, km-presence enable must be OUTSIDE that conditional (presence daemon is unconditional).

### Pitfall 3: Phase 56.1 Bug 2 — FIFO Blocking
**What goes wrong:** Writing to `/run/km/audit-pipe` without the timeout-tee pattern blocks the daemon indefinitely if km-audit-log hasn't opened the read end yet.
**Why it happens:** Named pipes block on write until a reader opens. This is well-documented in the codebase comment at line 1036-1038.
**How to avoid:** Use `exec.Command("bash", "-c", `printf '...' | timeout 0.1 tee /run/km/audit-pipe > /dev/null 2>/dev/null || true`)` — or equivalently implement the timeout-tee logic natively in Go via goroutines. The bash wrapper is simpler and matches the existing pattern.

### Pitfall 4: `runuser -u sandbox -- tmux list-clients` Requires tmux Server Running
**What goes wrong:** If no tmux server is running for the sandbox user, `tmux list-clients` exits non-zero with an error message. This is not a signal-2 positive result but must not crash the daemon.
**Why it happens:** `tmux list-clients` returns exit code 1 + "no server running" when the socket doesn't exist.
**How to avoid:** Check exit code explicitly; non-zero means no clients. `wc -l` on non-empty output = attached clients. The daemon should treat `err != nil` from the command as "0 clients" (negative signal), not as a fatal error.

### Pitfall 5: systemd `EnvironmentFile=` vs Shell `export` Format Mismatch
**What goes wrong:** If km-presence ever needs env vars from a file, adding `EnvironmentFile=/etc/profile.d/km-notify-env.sh` silently drops all vars (the shell file uses `export VAR=val` which systemd rejects).
**Why it happens:** Documented in Phase 67-11 and in the code comment at userdata.go:1797-1803.
**How to avoid:** km-presence uses `Environment=SANDBOX_ID={{ .SandboxID }}` directly in the unit (template substitution at compile time). No EnvironmentFile needed.

### Pitfall 6: `touch /run/km/last-slack-inbound` Insert Point
**What goes wrong:** Placing `touch` BEFORE `aws sqs delete-message` means the stamp updates even on polling errors. Placing it AFTER the `else` branch means it fires on agent failures.
**Why it happens:** The km-slack-inbound-poller has an outer `if [ -n "$NEW_SESSION" ]` / `else` conditional. The stamp should only fire on success.
**How to avoid:** Insert immediately after line 1516 (`echo "[km-slack-inbound-poller] Turn complete…"`), still inside the `if [ -n "$NEW_SESSION" ]` block, before the closing `else`.

**Exact surrounding context (verified from codebase):**
```bash
    echo "[km-slack-inbound-poller] Turn complete — session=$NEW_SESSION thread=$THREAD_TS"
    # INSERT HERE: touch /run/km/last-slack-inbound
  else
    echo "[km-slack-inbound-poller] WARN: agent run failed (exit $RUN_EXIT), message returns to queue"
  fi
```

---

## Code Examples

Verified patterns from the codebase:

### Audit Pipe Write Pattern (from userdata.go:1039-1041)
```bash
# Source: pkg/compiler/userdata.go:1039-1041
printf '{"timestamp":"%s","sandbox_id":"%s","event_type":"heartbeat","source":"shell","detail":{}}\n' \
  "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "{{ .SandboxID }}" \
  | timeout 0.1 tee /run/km/audit-pipe > /dev/null 2>/dev/null || true
```

Go equivalent for km-presence (inline bash via exec.Command):
```go
// Source: pattern from pkg/compiler/userdata.go:1067-1071
payload := fmt.Sprintf(`{"timestamp":"%s","sandbox_id":"%s","event_type":"heartbeat","source":"presence","detail":{}}`+"\n",
    time.Now().UTC().Format(time.RFC3339),
    sandboxID,
)
cmd := exec.Command("bash", "-c",
    fmt.Sprintf("printf '%%s' '%s' | timeout 0.1 tee /run/km/audit-pipe > /dev/null 2>/dev/null || true",
        strings.ReplaceAll(payload, "'", "'\\''")))
_ = cmd.Run()
```

### Existing systemd Unit Format (from userdata.go:1775-1789)
```
# Source: pkg/compiler/userdata.go:1775-1789
cat > /etc/systemd/system/km-mail-poller.service << 'UNIT'
[Unit]
Description=Klankrmkr mail poller — syncs inbound email from S3
After=network.target
[Service]
User=root
Environment=SANDBOX_ID={{ .SandboxID }}
Environment=KM_ARTIFACTS_BUCKET={{ .KMArtifactsBucket }}
{{ if .AllowedSenders }}Environment=KM_ALLOWED_SENDERS={{ .AllowedSenders }}{{ end }}
ExecStart=/opt/km/bin/km-mail-poller
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT
```

### CheckResult Structure (from doctor.go:63-69)
```go
// Source: internal/app/cmd/doctor.go:63-69
type CheckResult struct {
    Name        string      `json:"name"`
    Status      CheckStatus `json:"status"`
    Message     string      `json:"message"`
    Remediation string      `json:"remediation,omitempty"`
}
// CheckStatus values: CheckOK, CheckWarn, CheckError, CheckSkipped
```

### Doctor Check Registration Pattern (from doctor.go:2610-2618)
```go
// Source: internal/app/cmd/doctor.go:2610-2618 (Slack inbound check registration)
checks = append(checks, func(ctx context.Context) CheckResult {
    r := checkSlackInboundQueueExists(ctx, listInbound, inboundSQS)
    // Demote ERROR to WARN so Slack issues never fail km doctor.
    if r.Status == CheckError {
        r.Status = CheckWarn
    }
    return r
})
```

### FilterLogEvents Pattern (from cmd/configui/handlers.go:239-243)
```go
// Source: cmd/configui/handlers.go:239-243
output, err := h.cwClient.FilterLogEvents(r.Context(), &cloudwatchlogs.FilterLogEventsInput{
    LogGroupName:  ptrStr(kmPrefix + "sandboxes"),
    FilterPattern: ptrStr(sandboxID),
    Limit:         ptrInt32(20),
})
```

For `presence_daemon_healthy`, the doctor check needs `FilterLogEvents` with:
- `LogGroupName`: `/km/sandboxes/{sandbox_id}/` (same path as IdleDetector uses for `GetLogEvents`)
- `FilterPattern`: `"\"source\":\"presence\""` — JSON key-value filter matching events with source field = presence
- `StartTime`: `time.Now().UTC().Add(-5 * time.Minute).UnixMilli()`
- `Limit`: `1`

**Note:** `FilterLogEvents` is NOT currently in `pkg/aws/cloudwatch.go` — it only has `GetLogEvents`. The doctor check must either:
1. Add `FilterLogEvents` to the `CWLogsAPI` interface + pkg/aws/cloudwatch.go helper, OR
2. Define a separate narrow interface `CWLogsFilterAPI` (the pattern used in `cmd/configui/handlers.go:60-62`)

Option 2 is the existing pattern. A new `doctor_presence.go` file should define its own `CWLogsFilterAPI` interface.

### Sidecar Makefile Build Lines (from Makefile:90-95)
```makefile
# Source: Makefile:90-95
GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -trimpath -ldflags '-s -w' -o build/km-slack ./cmd/km-slack/
aws s3 cp build/km-slack   s3://$(KM_ARTIFACTS_BUCKET)/sidecars/km-slack
```

### Injectable commandRunner Pattern (for km-presence tests)
```go
// Modeled on km-slack dispatch pattern (cmd/km-slack/main.go)
type commandRunner interface {
    Output(name string, args ...string) ([]byte, error)
}
type realRunner struct{}
func (realRunner) Output(name string, args ...string) ([]byte, error) {
    return exec.Command(name, args...).Output()
}
// In tests:
type fakeRunner struct {
    responses map[string][]byte
    errors    map[string]error
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `_km_heartbeat` in every login shell | `km-presence.service` singleton | Phase 79 | Eliminates orphan multiplication; N→1 heartbeat source |
| `source:"shell"` heartbeat | `source:"presence"` heartbeat | Phase 79 | Allows CloudWatch greps to distinguish old vs. new |
| EXIT trap for cleanup | Systemd `Restart=always` | Phase 79 | Daemon survives bash death; no orphan on SSM disconnect |

**Deprecated/outdated after Phase 79:**
- `_km_heartbeat()` function in `userdata.go:1063-1072` and its background invocation at `1077-1079`
- `_KM_HEARTBEAT_PID` variable
- `trap 'kill -9 $_KM_HEARTBEAT_PID 2>/dev/null' EXIT` at `userdata.go:1079`
- Note: `compose.go:436-446` retains the old pattern for Docker sandboxes (intentionally out of scope)

---

## Open Questions

1. **Enable block for km-presence.service**
   - What we know: `km-mail-poller` enable call is inside `{{- if .SandboxEmail }}` block (not yet verified for the unconditional enable pattern)
   - What's unclear: Whether `systemctl enable --now km-presence` must be added unconditionally or if there's an unconditional enable block
   - Recommendation: Read lines 1150-1160 and the full userdata.go end section to find the enable block. If km-mail-poller enable is conditional, km-presence needs its own unconditional `systemctl daemon-reload && systemctl enable --now km-presence` after the unit file write.

2. **pgrep word-boundary regex in Go string context**
   - What we know: CONTEXT.md specifies `pgrep -af '(^|/)claude( |$)|(^|/)codex( |$)|km-agent-run\.sh'`
   - What's unclear: Whether ERE `|` syntax works with pgrep on AL2023 (pgrep uses POSIX BRE by default; `-E` may be needed for `|`)
   - Recommendation: Use `pgrep -afE '(^|/)claude( |$)|(^|/)codex( |$)|km-agent-run\.sh'` — the `-E` flag enables extended regex. Alternatively use a simpler approach: three separate `pgrep -af` calls (one each for `claude`, `codex`, `km-agent-run`).

3. **runuser vs sudo for tmux list-clients**
   - What we know: CONTEXT.md specifies `runuser -u sandbox -- tmux list-clients -t '' 2>/dev/null | wc -l`
   - What's unclear: Whether `-t ''` (empty target = list all sessions) works or whether `tmux list-clients 2>/dev/null` (no -t flag) is the right invocation to list all attached clients
   - Recommendation: Use `runuser -u sandbox -- tmux list-clients 2>/dev/null` (no `-t` flag) — `list-clients` without `-t` lists all clients across all sessions.

---

## Validation Architecture

> `nyquist_validation` is true in `.planning/config.json` — section required.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` package (stdlib) |
| Config file | none — `go test ./cmd/km-presence/...` |
| Quick run command | `go test ./cmd/km-presence/... -v -count=1` |
| Full suite command | `go test ./cmd/km-presence/... ./internal/app/cmd/... -v -count=1` |

### Phase Requirements → Test Map

No formal REQ-IDs were assigned (this is a tactical bug fix). The functional behaviors map as follows:

| Behavior | Test Type | Automated Command | File Exists? |
|----------|-----------|-------------------|-------------|
| Signal 1 (who, non-empty) → positive | unit | `go test ./cmd/km-presence/... -run TestSignal_LoginShell` | No — Wave 0 |
| Signal 1 (who, empty) → negative | unit | `go test ./cmd/km-presence/... -run TestSignal_LoginShell` | No — Wave 0 |
| Signal 2 (tmux clients > 0) → positive | unit | `go test ./cmd/km-presence/... -run TestSignal_TmuxClients` | No — Wave 0 |
| Signal 2 (tmux exits non-zero) → negative | unit | `go test ./cmd/km-presence/... -run TestSignal_TmuxClients` | No — Wave 0 |
| Signal 3 (newer email file) → positive | unit | `go test ./cmd/km-presence/... -run TestSignal_Email` | No — Wave 0 |
| Signal 3 (no newer email file) → negative | unit | `go test ./cmd/km-presence/... -run TestSignal_Email` | No — Wave 0 |
| Signal 4 (newer slack stamp) → positive | unit | `go test ./cmd/km-presence/... -run TestSignal_Slack` | No — Wave 0 |
| Signal 4 (stamp missing) → negative | unit | `go test ./cmd/km-presence/... -run TestSignal_Slack` | No — Wave 0 |
| Signal 5 (pgrep returns agent PID) → positive | unit | `go test ./cmd/km-presence/... -run TestSignal_AgentProcess` | No — Wave 0 |
| Signal 5 (pgrep empty output) → negative | unit | `go test ./cmd/km-presence/... -run TestSignal_AgentProcess` | No — Wave 0 |
| All signals negative → no emit | unit | `go test ./cmd/km-presence/... -run TestTick_NoEmitWhenAllNegative` | No — Wave 0 |
| Any one signal positive → emit | unit | `go test ./cmd/km-presence/... -run TestTick_EmitWhenAnyPositive` | No — Wave 0 |
| Stamp file touched unconditionally | unit | `go test ./cmd/km-presence/... -run TestTick_StampAlwaysTouched` | No — Wave 0 |
| doctor check returns OK when recent event | unit | `go test ./internal/app/cmd/... -run TestDoctor_PresenceDaemonHealthy_OK` | No — Wave 0 |
| doctor check returns WARN when stale event | unit | `go test ./internal/app/cmd/... -run TestDoctor_PresenceDaemonHealthy_Stale` | No — Wave 0 |
| doctor check skipped when nil client | unit | `go test ./internal/app/cmd/... -run TestDoctor_PresenceDaemonHealthy_Skipped` | No — Wave 0 |
| No `_km_heartbeat` processes after Ctrl-D | regression | manual (`aws ssm send-command` smoke) | No — manual |

### Sampling Rate
- **Per task commit:** `go test ./cmd/km-presence/... -v -count=1`
- **Per wave merge:** `go test ./cmd/km-presence/... ./internal/app/cmd/... -v -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `cmd/km-presence/main.go` — daemon implementation (does not exist yet)
- [ ] `cmd/km-presence/main_test.go` — table-driven unit tests (does not exist yet)
- [ ] `internal/app/cmd/doctor_presence.go` — `checkPresenceDaemonHealthy` function
- [ ] `internal/app/cmd/doctor_presence_test.go` — unit tests for the new doctor check

### Layer 2 (Integration, live sandbox)
For each of the 5 signals, trigger in isolation on a sandbox with the new code:
1. Signal 1: Connect via `km shell`, verify CloudWatch event with `source:"presence"` within 90s, disconnect, verify no new events within 120s
2. Signal 2: Start tmux on sandbox (`tmux new-session -d`), attach client (`tmux attach`), verify emission
3. Signal 3: Drop a file into `/var/mail/km/new/`, verify emission within 90s
4. Signal 4: `touch /run/km/last-slack-inbound` manually, verify emission within 90s
5. Signal 5: Start a background `sleep 600` and rename it `claude` (or run actual claude), verify emission
- Success criterion: CloudWatch event with `{"source":"presence","event_type":"heartbeat"}` within 90s; `journalctl -u km-presence` shows correct signal label

### Layer 3 (km doctor)
- `km doctor` → `presence_daemon_healthy` returns OK on new sandbox
- Stop km-presence (`systemctl stop km-presence`, do NOT restart), wait 6 minutes, `km doctor` → WARN
- Success criterion: WARN message mentions sandbox ID and staleness

### Layer 4 (Regression for the bug)
```bash
# Provision new sandbox (post-Phase-79 code)
km create profiles/learn.yaml --alias ph79-test
SB=$(km list | awk '/ph79-test/{print $1}')

# Open a shell and exit
km shell $SB     # Ctrl-D immediately

# Wait 30s, then check for orphan heartbeat processes
aws ssm send-command \
  --instance-id $(aws ec2 describe-instances --filters "Name=tag:km:sandbox-id,Values=$SB" \
    --query "Reservations[0].Instances[0].InstanceId" --output text) \
  --document-name AWS-RunShellScript \
  --parameters 'commands=["pgrep -afc '"'"'_km_heartbeat|sleep 60'"'"' | tee /dev/stderr"]' \
  --output text --query "Command.CommandId"
# Success criterion: output is "0" (zero orphan processes)

# Verify exactly 1 km-presence process
aws ssm send-command ... --parameters 'commands=["pgrep -c km-presence"]'
# Success criterion: output is "1"
```

---

## Key Architecture Insights for Planning

### 1. Exact Lines to Remove from userdata.go
**File:** `pkg/compiler/userdata.go`
**Remove lines 1056-1080** (verified by reading the file):
- Line 1056: `# Background heartbeat: keeps the sandbox alive...` (comment block start)
- Lines 1063-1072: `_km_heartbeat()` function body
- Line 1077: `_km_heartbeat > /dev/null 2>&1 < /dev/null &`
- Line 1078: `_KM_HEARTBEAT_PID=$!`
- Line 1079: `trap 'kill -9 $_KM_HEARTBEAT_PID 2>/dev/null' EXIT`
- Line 1080: `HOOK` (closing heredoc marker for km-audit.sh)

After removal, the HOOK heredoc marker moves to immediately after line 1054 (the `{{- end }}` for LearnMode PROMPT_COMMAND).

### 2. touch Insert Point in km-slack-inbound-poller (exact)
**File:** `pkg/compiler/userdata.go`
**Current line 1516:**
```bash
    echo "[km-slack-inbound-poller] Turn complete — session=$NEW_SESSION thread=$THREAD_TS"
```
**Insert after line 1516, before the `else` at line 1517:**
```bash
    touch /run/km/last-slack-inbound
```
This is inside `if [ -n "$NEW_SESSION" ]` (success path only) and before `else` (failure path).

### 3. S3 Fetch Block Location
**File:** `pkg/compiler/userdata.go`
**Lines 894-900** (the sidecar download block):
```bash
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/dns-proxy" /opt/km/bin/km-dns-proxy
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/http-proxy" /opt/km/bin/km-http-proxy
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/audit-log" /opt/km/bin/km-audit-log
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-slack" /opt/km/bin/km-slack
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/tracing/config.yaml" /etc/km/tracing/config.yaml
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/otelcol-contrib" /opt/km/bin/otelcol-contrib
chmod +x /opt/km/bin/km-*
```
Add after line 897 (km-slack line):
```bash
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-presence" /opt/km/bin/km-presence
```
The existing `chmod +x /opt/km/bin/km-*` wildcard covers the new binary.

### 4. /run/km Directory Ownership
`/run/km` is owned by `km-sidecar:km-sidecar` (line 971: `chown km-sidecar:km-sidecar /run/km`). The audit-pipe is mode 666 (line 974: `chmod 666 /run/km/audit-pipe`). The presence daemon runs as root and can create `.presence-last-tick` in this directory. No SELinux/AppArmor concerns documented for AL2023 in the existing codebase.

On every boot, `km-bootstrap.service` recreates `/run/km` via `mkdir -p /run/km` (km-bootstrap script line 1119). The stamp file `.presence-last-tick` is in `/run/km/` which is tmpfs — it is intentionally lost on reboot. The daemon treats missing stamp as zero time (first tick), which is the correct initial state.

### 5. doctor_presence.go Approach
Create `internal/app/cmd/doctor_presence.go` following `doctor_slack_transcript.go` as template:
- Define `CWLogsFilterAPI` interface (mirror `cmd/configui/handlers.go:60-62`) with `FilterLogEvents`
- Implement `checkPresenceDaemonHealthy(ctx, cwClient, lister, resourcePrefix, logGroup string) CheckResult`
- Add `CWFilterClient CWLogsFilterAPI` to `DoctorDeps`
- Register check in `runDoctor` via `checks = append(checks, ...)` closure
- Wire `cloudwatchlogs.NewFromConfig(awsCfg)` in `initRealDeps`

Check logic:
```
For each sandbox in lister.ListSandboxes() where status == "running":
    events = FilterLogEvents(logGroup=/km/sandboxes/{id}/, filterPattern='"source":"presence"', startTime=now-5min, limit=1)
    if len(events) == 0: append to stale list
if stale: return WARN with list of sandbox IDs + "presence daemon may have crashed or sandbox was created before Phase 79"
else: return OK
```

Demote to WARN (not ERROR) following the same pattern as Slack checks:
```go
if r.Status == CheckError {
    r.Status = CheckWarn
}
```

---

## Sources

### Primary (HIGH confidence)
- Direct codebase read: `pkg/compiler/userdata.go` — lines 860-1082, 1280-1524, 1760-1830
- Direct codebase read: `Makefile` — lines 1-120 (sidecar build targets)
- Direct codebase read: `internal/app/cmd/doctor.go` — DoctorDeps struct, CheckResult, check registration patterns
- Direct codebase read: `internal/app/cmd/doctor_slack.go` — `checkSlackInboundQueueExists` (lines 218-267)
- Direct codebase read: `internal/app/cmd/doctor_slack_transcript.go` — file-level doctor pattern
- Direct codebase read: `cmd/configui/handlers.go` — `FilterLogEvents` interface + call pattern (lines 58-62, 239-243)
- Direct codebase read: `cmd/km-slack/main.go` + `main_test.go` — sidecar pattern reference
- Direct codebase read: `pkg/aws/cloudwatch.go` — confirms `FilterLogEvents` NOT in CWLogsAPI
- Direct codebase read: `pkg/compiler/compose.go` — confirms second `_km_heartbeat` location (Docker path, out of scope)

### Secondary (MEDIUM confidence)
- CONTEXT.md (79-CONTEXT.md) — locked decisions
- PRD: `docs/superpowers/specs/2026-05-10-km-presence-daemon-design.md`

### Tertiary (LOW confidence)
- None

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — confirmed from go.mod and existing sidecar source
- Architecture: HIGH — all patterns read directly from codebase
- Pitfalls: HIGH — discovered from direct code inspection (compose.go, timeout-tee, FIFO, etc.)
- Validation Architecture: HIGH — test framework verified from existing test files

**Research date:** 2026-05-10
**Valid until:** 2026-06-10 (stable codebase; no fast-moving dependency)
