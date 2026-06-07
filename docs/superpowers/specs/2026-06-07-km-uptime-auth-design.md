# km status / km list — uptime + agent-auth visibility

**Date:** 2026-06-07
**Scope:** small, self-contained CLI improvement. Not a GSD phase — execute via `/gsd:quick`.

## Goal

Make two operator commands surface a little more at-a-glance sandbox state:

1. `km list` — print a version + timestamp banner at the top.
2. Per-sandbox **uptime** (how long the sandbox has been up) in both `km status` and `km list`.
3. Per-sandbox **agent auth** (is the box logged in to claude / codex) in `km status` always, and in `km list` behind an opt-in `--auth` flag.

"The system" = each sandbox (confirmed). No operator-workstation or platform-level checks.

## Non-goals (YAGNI)

- No sandbox-side changes, no DynamoDB schema change, no heartbeat, no caching, no new Lambda.
- No auth check on non-running sandboxes (can't SSM a stopped/paused/killed box).
- Uptime is **age since creation** (`CreatedAt`), not EC2 instance `LaunchTime`. A pause/resume will not reset it. True LaunchTime is a possible cheap follow-up (the list path already calls `DescribeInstances` per EC2 row) but is explicitly out of scope here.

## Design

### 1. `km list` banner — reuse existing `fprintBanner`

`fprintBanner(w, cmd, context)` (`internal/app/cmd/root.go:164`) already renders
`km list — <context> [<version>] <timestamp>` plus a rule line, using `version.Number`.
`km status` already calls it.

- Add one `fprintBanner(cmd.OutOrStdout(), "km list", summary)` call at the top of `runList`
  (`internal/app/cmd/list.go`), where `summary` is e.g. `"3 sandboxes"`.
- **Suppress the banner in `--json` mode** so it never corrupts the JSON array.
- Empty-list path (`"No running sandboxes."`) still prints the banner (it's informative there too).

### 2. Uptime — derived from `CreatedAt`, no new AWS calls

- New pure helper `formatUptime(createdAt time.Time) string` → compact form:
  `8m`, `3h12m`, `2d4h`. Unit-tested directly.
- `km status` (`printSandboxStatus`): new `Uptime:` line directly under the `Created At:` line,
  printed only when `rec.Status == "running"`.
- `km list` (`printSandboxTable`): new compact `UP` column. Running rows show the uptime string;
  all other rows show `-`. Column appears in both narrow and `--wide` layouts (small enough to fit narrow).

### 3. Agent auth — one SSM round-trip per box, reusing `agent_auth.go` machinery

New helper, modeled on the existing verification code in `internal/app/cmd/agent_auth.go`
(`sendSSMAndWait`, the `claude auth status` → `loggedIn` JSON parse, the `~/.codex/auth.json`
file test):

```go
// checkAgentAuth runs ONE SSM command on the box that performs both the claude
// and codex checks, to minimize round-trips. Returns (claudeLoggedIn, codexLoggedIn, err).
func checkAgentAuth(ctx context.Context, ssm SSMSendAPI, instanceID string) (bool, bool, error)
```

The single remote command does both at once, e.g.:

```
sudo -u sandbox bash -lc 'claude auth status 2>/dev/null'
test -f /home/sandbox/.codex/auth.json && echo KM_CODEX_OK || echo KM_CODEX_MISSING
```

Parse `"loggedIn": true` (tolerant of spacing, as the existing code does) for claude and the
`KM_CODEX_OK` sentinel for codex.

Instance-ID resolution reuses the same lookup `km agent auth` already uses (tag
`km:sandbox-id` → instance ID).

- **`km status <id>`**: always run `checkAgentAuth` for a running sandbox (single box, cheap).
  New section:
  ```
  Auth:
    claude:  ✓ logged in
    codex:   ✗ not logged in
  ```
  Skipped entirely for non-running sandboxes. On SSM error, print a soft
  `Auth: <unavailable: ...>` line rather than failing the command.

- **`km list --auth`**: new standalone bool flag. Does **not** auto-enable on `--wide`
  (keeps `--wide` fast). When set, fan out `checkAgentAuth` **concurrently** across running
  sandboxes (bounded goroutine pool, mirroring the existing per-row EC2 status loop), then
  render a compact `AUTH` column: `cl✓ cx✗` (running rows), `-` otherwise.
  Without `--auth`, `km list` makes **zero** SSM calls — behaviorally identical to today
  except for the new banner line and `UP` column.

### 4. Testability

Both commands already use dependency-injected fetcher/lister overloads with `*_test.go`
coverage (`status_test.go`, `list_test.go`). Introduce an `AgentAuthChecker` interface
(real implementation wraps SSM) injected through the same DI seams so tests stub auth
results with no AWS. Add direct unit tests for `formatUptime`.

## Files touched

- `internal/app/cmd/list.go` — banner call, `UP` column, `--auth` flag + concurrent fan-out, `AUTH` column.
- `internal/app/cmd/status.go` — `Uptime:` line, `Auth:` section.
- `internal/app/cmd/agent_auth.go` (or a new small `agent_auth_check.go`) — `checkAgentAuth` helper + `AgentAuthChecker` interface.
- `internal/app/cmd/list_test.go`, `status_test.go`, plus a new `formatUptime` test.

## Verification

- `make build` (ldflags-stamped binary).
- `go test ./internal/app/cmd/...`.
- Manual: `./km list`, `./km list --auth`, `./km status <id>` against a live running sandbox.
