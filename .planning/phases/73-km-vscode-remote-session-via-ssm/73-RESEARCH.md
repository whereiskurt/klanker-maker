# Phase 73: km vscode remote session via SSM — Research

**Researched:** 2026-05-07
**Domain:** Go CLI extension + systemd unit injection via userdata template + SSM RunCommand + SSM port-forward
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **Use case (Q1: B):** Companion to `km agent run`. On-demand, time-boxed sessions alongside Claude runs. Not a primary work surface.
- **Lifecycle (Q2: B):** `km vscode start` brings up server + tunnel; runs until `km vscode stop`. No `--duration`. No auto-stop on idle.
- **Architecture (Q3: A):** Thin systemd wrapper. Unit name `km-vscode.service` (NOT `WantedBy=multi-user.target` — explicit start only). Launch wrapper at `/opt/km/bin/km-vscode-launch` (rotates token, execs `code serve-web`). Token dir `/etc/km/vscode/` mode 0700, owned by `sandbox`. Token `/etc/km/vscode/token` mode 0600. Server binds `127.0.0.1:8443` only.
- **Profile gate (Q4):** New field `spec.cli.vscodeEnabled` (`bool*` pointer, omit = default `true`). `true` = userdata writes unit/wrapper/dir. `false` = userdata skips entirely; `km vscode start` returns "VS Code not enabled in this sandbox's profile" on unit-not-found.
- **Ownership split:** Operator installs `code` binary in `initCommands`. km installs the systemd unit, launch wrapper, and token directory via `userdata.go`.
- **CLI surface:** `km vscode start <sandbox-id> [--local-port N] [--no-forward]`, `stop`, `status`.
- **Auth model:** SSM tunnel is the real security boundary. Token in URL is defense-in-depth. Server binds 127.0.0.1 only. Token file 0600 owned by `sandbox`, read by km via SSM RunCommand running as root.

### Claude's Discretion

- Exact version pinning vs `latest` for the `code` binary download URL in `docs/vscode.md`. CONTEXT.md leans toward pinning for reproducibility but acknowledges `latest` is simpler.
- Download URL architecture: `cli-alpine-x64` vs `cli-linux-x64` — depends on which glibc-free binary works best on Amazon Linux 2023.
- `km doctor` checks for VS Code — deferred, skip unless evidence of operator confusion.

### Deferred Ideas (OUT OF SCOPE)

- `--duration <D>` flag and auto-stop integration.
- `km doctor` checks for VS Code.
- Multiple concurrent VS Code sessions per sandbox on different ports.
- Session persistence across `km destroy + km create`.
- `code-server` and `openvscode-server` first-class support (document Microsoft `code` only).
- Version pinning decision (deferred to planning).
</user_constraints>

---

## Summary

Phase 73 adds three SSM wrapper subcommands (`km vscode start | stop | status`) plus a userdata template injection that writes a non-autostarting systemd unit, a bash launch wrapper, and a token directory into every new sandbox where `spec.cli.vscodeEnabled` is `true` (the default).

The implementation is deliberately thin: no new Lambda, no DDB schema, no SSM Parameter Store entries, no sidecar binary. Every primitive needed already exists in the codebase: `sendSSMAndWait` (agent.go line 1067) handles all SSM RunCommand work; `buildPortForwardCmd` + `runPortForward` (shell.go lines 577/473) handle the port tunnel; `SlackInboundEnabled` / `SandboxEmail` gates in `userdata.go` show exactly how to add a conditional block; and `CLISpec`'s `*bool` fields show how to add a default-true boolean profile flag.

The main research finding that affects the design spec: `--without-connection-token=false` is NOT a valid flag invocation — the flag is a standalone boolean toggle (presence = disable token requirement). The correct launch wrapper must use `--connection-token-file /etc/km/vscode/token` WITHOUT `--without-connection-token`. The `--connection-token-file` flag had a bug (issue #215537, closed by PRs #219041 and #223524) that was fixed and released to Insiders; confidence is MEDIUM that it works in stable VS Code CLI today. As a belt-and-suspenders approach, the wrapper should inline the token value as a fallback if needed.

**Primary recommendation:** Implement exactly as the locked design specifies, but correct the launch wrapper to drop `--without-connection-token=false` (it is semantically inverted and likely unsupported as `=false` form). The wrapper should use `--connection-token-file` with a shell fallback `--connection-token "$(cat ...)"` guarded by a version check if the planning phase decides to defend against old VS Code CLI binaries.

---

## Standard Stack

### Core (all pre-existing in codebase)

| Library / Pattern | Location | Purpose |
|---|---|---|
| `sendSSMAndWait` | `internal/app/cmd/agent.go:1067` | Send SSM RunCommand + poll for completion, return stdout. Reuse verbatim. |
| `buildPortForwardCmd` | `internal/app/cmd/shell.go:577` | Construct `aws ssm start-session --document-name AWS-StartPortForwardingSession` exec.Cmd. Reuse verbatim. |
| `runPortForward` | `internal/app/cmd/shell.go:473` | Parse port specs, launch foreground SSM port tunnel. Reuse as pattern. |
| `SSMSendAPI` interface | `internal/app/cmd/agent.go:28-32` | `SendCommand` + `GetCommandInvocation` — reuse for test injection in vscode.go. |
| `SandboxFetcher` interface | `internal/app/cmd/` (shared across cmd files) | FetchSandbox(ctx, id) → SandboxRecord. Reuse for test injection. |
| `CLISpec` + `*bool` pattern | `pkg/profile/types.go:357-447` | Bool pointer = nil means "omit env var / use default"; explicit `&true`/`&false` overrides. Used by `NotifyEmailEnabled`, `NotifySlackEnabled`, `SlackArchiveOnDestroy`. |
| `generateUserData` template | `pkg/compiler/userdata.go:3031` | Accepts `userDataParams` struct; template gated blocks via `{{- if .Field }}`. |
| `userDataParams` struct | `pkg/compiler/userdata.go:2796-2885` | Add `VSCodeEnabled bool` field here. |

### Installation

No new dependencies. This phase adds only new files and modifications to existing files.

---

## Architecture Patterns

### Recommended Project Structure for new files

```
internal/app/cmd/
├── vscode.go           # NEW: km vscode start | stop | status command tree
├── vscode_test.go      # NEW: unit tests (SSM mock, flag parsing, error messages)
docs/
└── vscode.md           # NEW: operator setup guide
```

Modified files:
```
pkg/profile/types.go            # Add VSCodeEnabled *bool to CLISpec
pkg/profile/schemas/           # Update JSON schema (vscodeEnabled field)
pkg/compiler/userdata.go       # Add VSCodeEnabled bool to userDataParams struct + template block
pkg/compiler/userdata_test.go  # Add conditional rendering tests
internal/app/cmd/root.go       # Register NewVSCodeCmd(cfg)
CLAUDE.md                      # Add km vscode to CLI command list
```

### Pattern 1: Cobra subcommand registration — mirror `slack.go`

The `km slack` command registers in `root.go` at line 85 via `root.AddCommand(NewSlackCmd(cfg))`. The `NewSlackCmd` function builds a parent Cobra command and attaches `newSlack*Cmd` subcommands via `AddCommand`. Pattern for `vscode.go`:

```go
// Source: mirrors internal/app/cmd/slack.go:111-130
func NewVSCodeCmd(cfg *config.Config) *cobra.Command {
    return NewVSCodeCmdWithDeps(cfg, nil, nil, nil)
}

func NewVSCodeCmdWithDeps(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
    vscodeCmd := &cobra.Command{
        Use:          "vscode",
        Short:        "Manage VS Code Web sessions inside sandboxes",
        SilenceUsage: true,
    }
    vscodeCmd.AddCommand(newVSCodeStartCmd(cfg, fetcher, execFn, ssmClient))
    vscodeCmd.AddCommand(newVSCodeStopCmd(cfg, fetcher, ssmClient))
    vscodeCmd.AddCommand(newVSCodeStatusCmd(cfg, fetcher, ssmClient))
    return vscodeCmd
}
```

Register in `root.go` after the `NewSlackCmd` line:
```go
root.AddCommand(NewVSCodeCmd(cfg))
```

### Pattern 2: SSM RunCommand via `sendSSMAndWait` — reuse verbatim

The `sendSSMAndWait` helper (agent.go:1067) is the exact primitive needed for `start`, `stop`, and `status`:

```go
// Source: internal/app/cmd/agent.go:1067-1113
// sendSSMAndWait sends AWS-RunShellScript, polls GetCommandInvocation, returns stdout.
// Stdout is capped at 24,000 characters (AWS official limit per GetCommandInvocation docs).
// stderr is capped at 8,000 characters. Both fit token reads (64 chars) and journalctl 20 lines (~2KB).
func sendSSMAndWait(ctx context.Context, ssmClient SSMSendAPI, instanceID, shellCmd string) (string, error)
```

`vscode.go` calls this for:
- `systemctl restart km-vscode`
- `systemctl is-active km-vscode` (polling loop)
- `cat /etc/km/vscode/token`
- `journalctl -u km-vscode --no-pager -n 20`
- `systemctl stop km-vscode`

### Pattern 3: SSM port-forward — reuse `buildPortForwardCmd` verbatim

```go
// Source: internal/app/cmd/shell.go:577-584
func buildPortForwardCmd(ctx context.Context, instanceID, region, localPort, remotePort string) *exec.Cmd
```

For `km vscode start`, the port-forward step is:
```go
c := buildPortForwardCmd(ctx, instanceID, rec.Region, localPort, "8443")
c.Stdin = os.Stdin
c.Stdout = os.Stdout
c.Stderr = os.Stderr
return execFn(c)  // blocks until Ctrl-C
```

`localPort` defaults to `"8443"` unless `--local-port N` is set.

### Pattern 4: Bool pointer profile field — mirror `NotifySlackEnabled`

`CLISpec.NotifySlackEnabled` is `*bool` with `omitempty`. `nil` = not set = default behavior. `&false` = explicit disable. For `vscodeEnabled`, the default is `true` (not `false` like Slack), which is handled at the `generateUserData` call site, not in the type:

```go
// Source: pkg/profile/types.go:405-408
NotifySlackEnabled *bool `yaml:"notifySlackEnabled,omitempty"`

// For VSCodeEnabled, add to CLISpec:
// VSCodeEnabled controls whether the km-vscode systemd unit, launch wrapper,
// and /etc/km/vscode/ token directory are provisioned at sandbox boot.
// Pointer type: nil = omit = default true. &false = explicitly disable.
VSCodeEnabled *bool `yaml:"vscodeEnabled,omitempty"`
```

Default-true semantics are implemented in `generateUserData`:
```go
// In generateUserData (pkg/compiler/userdata.go), near the existing CLI section:
if p.Spec.CLI != nil && p.Spec.CLI.VSCodeEnabled != nil && !*p.Spec.CLI.VSCodeEnabled {
    params.VSCodeEnabled = false
} else {
    params.VSCodeEnabled = true  // nil means default true
}
```

### Pattern 5: Userdata template conditional block — mirror `SlackInboundEnabled`

The `km-slack-inbound-poller` block at `userdata.go:1207-1740` is the reference pattern. The new vscode block goes near the end of the file's "systemd unit writing" section, after the email poller block and before the `systemctl daemon-reload` lines.

The block writes:
1. The `/etc/systemd/system/km-vscode.service` unit file (heredoc, NOT `WantedBy=multi-user.target`)
2. The `/opt/km/bin/km-vscode-launch` wrapper script (heredoc, `chmod 0755`)
3. The `/etc/km/vscode/` directory (`mkdir -p`, `chown sandbox:sandbox`, `chmod 0700`)
4. `systemctl daemon-reload` (so the first `km vscode start` finds the unit)
5. Does NOT `systemctl enable` or `systemctl start` — explicit-start only

```
{{- if .VSCodeEnabled }}
# Phase 73: km-vscode — VS Code Web session unit + launch wrapper + token dir
cat > /etc/systemd/system/km-vscode.service << 'UNIT'
[Unit]
Description=Klanker Maker VS Code Web session
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=sandbox
Group=sandbox
ExecStart=/opt/km/bin/km-vscode-launch
Restart=on-failure
RestartSec=2
# Note: deliberately NOT WantedBy=multi-user.target — explicit start only
UNIT

cat > /opt/km/bin/km-vscode-launch << 'VSCODELAUNCH'
#!/usr/bin/env bash
set -euo pipefail
umask 077
TOKEN=$(openssl rand -hex 32)
printf '%s' "$TOKEN" > /etc/km/vscode/token
chown sandbox:sandbox /etc/km/vscode/token
chmod 0600 /etc/km/vscode/token
exec /usr/local/bin/code serve-web \
    --host 127.0.0.1 \
    --port 8443 \
    --connection-token-file /etc/km/vscode/token \
    --accept-server-license-terms
VSCODELAUNCH
chmod 0755 /opt/km/bin/km-vscode-launch

mkdir -p /etc/km/vscode
chown sandbox:sandbox /etc/km/vscode
chmod 0700 /etc/km/vscode

systemctl daemon-reload
echo "[km-bootstrap] km-vscode unit + launch wrapper installed (explicit start only)"
{{- end }}
```

**Key note:** `--without-connection-token=false` is dropped from the wrapper (see Pitfall 1 below). The design spec includes it but it is semantically wrong and unsupported as a `=false` form.

### Pattern 6: `start` command flow

```
1. FetchSandbox → get instanceID, region
2. sendSSMAndWait(ctx, ssmClient, instanceID, "systemctl restart km-vscode")
   → if error: distinguish "unit not found" (vscodeEnabled: false) vs other failures
3. Poll sendSSMAndWait(ctx, ssmClient, instanceID, "systemctl is-active km-vscode")
   for up to 10 attempts × 1s sleep = 10s total
   → if still inactive after 10s: fetch journalctl -n 20, print, exit non-zero
4. sendSSMAndWait(ctx, ssmClient, instanceID, "cat /etc/km/vscode/token") → strip whitespace
5. Print connection block to stdout
6. If !noForward: buildPortForwardCmd → execFn (blocks until Ctrl-C)
```

### Anti-Patterns to Avoid

- **Don't use `systemctl start` in `km vscode start`.** Use `systemctl restart` so a second `km vscode start` rotates the token even when the unit is already running.
- **Don't auto-enable the unit.** `WantedBy=multi-user.target` would start VS Code on every sandbox boot, consuming resources when not needed.
- **Don't omit `daemon-reload` in userdata.** Without it, `km vscode start` on an AMI-baked sandbox fails with "unit not found" because systemd hasn't seen the newly written unit file.
- **Don't read the token file before polling for `is-active`.** The file is written by the wrapper at startup; reading it before the unit is `active` risks an empty or partial token.
- **Don't block the SSM `restart` call for more than ~30s.** `sendSSMAndWait` uses a 2s poll with max 150 attempts (5 min). `systemctl restart` itself is quick — the long part is `code serve-web` initialization. Use `restart` to return immediately, then poll `is-active` separately.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---|---|---|---|
| SSM RunCommand + poll | Custom SendCommand loop | `sendSSMAndWait` (agent.go:1067) | Already has 1s initial delay, 2s poll, 150-attempt max, proper error parsing |
| SSM port-forward | Custom `aws ssm start-session` call | `buildPortForwardCmd` + `execFn` (shell.go:577) | Handles profile, region, document name, and parameter JSON |
| Sandbox lookup | Direct DynamoDB/S3 | `SandboxFetcher` interface + `newRealFetcher` | Consistent with all other commands; testable via interface |
| Profile flag default | Custom nil-check scattered everywhere | `generateUserData` nil-check pattern (already used for NotifySlack*, SlackArchiveOnDestroy) | Single source of truth for default semantics |

---

## Common Pitfalls

### Pitfall 1: `--without-connection-token=false` is not a valid flag

**What goes wrong:** The design spec's launch wrapper includes `--without-connection-token=false`. This flag is a standalone boolean toggle — its presence means "disable token requirement." Passing `=false` either (a) is parsed as a positional argument causing an error, or (b) is treated as "disable the token requirement and then treat 'false' as a positional arg." Neither is correct.

**Why it happens:** `--without-connection-token` was added to handle the case where operators want no token. The design intended to explicitly affirm "we DO want the token" — but that is the default behavior when `--connection-token-file` is provided without `--without-connection-token`.

**How to avoid:** Remove `--without-connection-token=false` from the launch wrapper. The correct invocation is:
```bash
exec /usr/local/bin/code serve-web \
    --host 127.0.0.1 \
    --port 8443 \
    --connection-token-file /etc/km/vscode/token \
    --accept-server-license-terms
```

**Warning signs:** `systemctl restart km-vscode` succeeds but `journalctl -u km-vscode` shows a startup failure referencing an unrecognized argument.

### Pitfall 2: `--connection-token-file` had a bug in older VS Code CLI versions

**What goes wrong:** In VS Code CLI versions before the fix in PRs #219041/#223524, `--connection-token-file` was accepted but ignored — the server generated a random UUID token instead. The operator would see a token in the file but the browser URL would need a different token.

**Why it happens:** The `serve_web()` function called `mint_connection_token()` without reading the file path argument.

**How to avoid:** The bug is fixed (labeled "insiders-released"). As of 2026, stable VS Code CLI should have the fix. If operators encounter this, `km vscode start` would print a token that doesn't match what the server expects — the connection block URL would 401. Document this in `docs/vscode.md` troubleshooting. Confidence: MEDIUM (fix confirmed merged, cannot verify stable release date without running the binary).

**Recommendation for the launch wrapper:** Keep `--connection-token-file` as the primary approach. If the planning phase decides to add a fallback, the wrapper could do:
```bash
TOKEN=$(openssl rand -hex 32)
printf '%s' "$TOKEN" > /etc/km/vscode/token
# ... chown/chmod ...
exec /usr/local/bin/code serve-web \
    --host 127.0.0.1 \
    --port 8443 \
    --connection-token "$TOKEN" \
    --accept-server-license-terms
```
This avoids the file-read entirely by injecting the token directly. The planner should choose one approach.

### Pitfall 3: `vscodeEnabled: false` detection from `systemctl restart` exit code

**What goes wrong:** When `spec.cli.vscodeEnabled: false`, userdata skips writing the unit entirely. `systemctl restart km-vscode` returns exit code 5 ("unit not found" / "not loaded") rather than a service-level failure. `sendSSMAndWait` returns this as an error. The error message from stderr is "Failed to restart km-vscode.service: Unit km-vscode.service not found." This must be detected and turned into a user-friendly message.

**How to avoid:** In `runVSCodeStart`, after the `restart` call:
```go
if err != nil {
    if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "Unit km-vscode") {
        return fmt.Errorf("VS Code not enabled for sandbox %s — set spec.cli.vscodeEnabled: true in the profile and reprovision", sandboxID)
    }
    return fmt.Errorf("start km-vscode: %w", err)
}
```

### Pitfall 4: Token file read races the unit becoming active

**What goes wrong:** Immediately after `systemctl restart` succeeds at the SSM level, the unit may still be in the "activating" state while `km-vscode-launch` runs. Reading `/etc/km/vscode/token` before the wrapper has written it returns empty or a stale value from a previous run.

**How to avoid:** The polling loop for `systemctl is-active` MUST complete (returning `active`) before the token read step. The wrapper writes the token early in its execution (before `exec`), so by the time the unit is `active`, the token file is present. The current design spec correctly orders: poll is-active → read token.

### Pitfall 5: Local port 8443 collision when re-running `start`

**What goes wrong:** If the operator runs `km vscode start` a second time without stopping first:
1. `systemctl restart` succeeds and rotates the token — the first tunnel is now stale (wrong token).
2. The port-forward step fails with `bind: address already in use` because the first tunnel is still holding local port 8443.

**How to avoid:** Document in `docs/vscode.md`: "Run `km vscode stop` before a second `start`, or use `--no-forward` to get the new URL/token and reconnect the browser without starting a new tunnel." The error from the failed bind is clear — no silent failure here.

### Pitfall 6: AMI-baked sandboxes and `daemon-reload`

**What goes wrong:** If a sandbox was created from an AMI baked before Phase 73 shipped, the unit file is NOT in the AMI image. Cloud-init writes it fresh at boot, but `systemctl daemon-reload` must run AFTER cloud-init writes the unit. The existing `daemon-reload` in userdata (around lines 2288-2297) runs at sidecar startup time. The new vscode block must include its own `daemon-reload` call immediately after writing the unit.

**How to avoid:** The vscode userdata block includes `systemctl daemon-reload` as documented in Pattern 5 above. This is consistent with the fix in commit `4030fce` for the mail-poller and sidecars.

---

## Code Examples

### SSM polling for is-active (in-shell, single SendCommand)

Rather than issuing 10 separate `GetCommandInvocation` round-trips for the `is-active` check, use a single SSM command that polls in-shell:

```bash
# Source: design pattern — single SSM call, polling in bash
for i in $(seq 1 10); do
  if systemctl is-active --quiet km-vscode; then
    exit 0
  fi
  sleep 1
done
exit 1
```

Issued as a single `sendSSMAndWait` call. This is simpler and faster than 10 × (SSM API round-trip + 2s poll). The command returns exit 0 (success → `sendSSMAndWait` returns `"", nil`) or exit 1 (failure → `sendSSMAndWait` returns an error).

**When the unit fails to start:** After the poll returns an error, issue a second `sendSSMAndWait` for `journalctl -u km-vscode --no-pager -n 20`. SSM stdout limit is 24,000 characters (HIGH confidence, [AWS API docs](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetCommandInvocation.html)); 20 journal lines are well under that limit.

### Profile field addition to CLISpec

```go
// Add to pkg/profile/types.go, inside CLISpec, after NotifySlackTranscriptEnabled:

// VSCodeEnabled controls whether the km-vscode systemd unit, launch wrapper,
// and /etc/km/vscode/ token directory are written by userdata at sandbox boot.
// Pointer type: nil (omit in YAML) = default true.
// Set false to provision sandboxes where VS Code is not needed.
// Schema change requires: make build && km init --sidecars.
VSCodeEnabled *bool `yaml:"vscodeEnabled,omitempty" json:"vscodeEnabled,omitempty"`
```

### userDataParams struct field addition

```go
// Add to userDataParams struct in pkg/compiler/userdata.go:
// VSCodeEnabled gates the km-vscode systemd unit, launch wrapper, and token
// directory. True when spec.cli.vscodeEnabled is nil (default) or &true.
VSCodeEnabled bool
```

### generateUserData population

```go
// In generateUserData, inside the `if p.Spec.CLI != nil` block:
// Default true: VSCodeEnabled is true unless explicitly set to false.
params.VSCodeEnabled = true
if p.Spec.CLI.VSCodeEnabled != nil && !*p.Spec.CLI.VSCodeEnabled {
    params.VSCodeEnabled = false
}
```

When `Spec.CLI` is nil (no cli section in profile), `VSCodeEnabled` should still be `true` (default). So the population must also handle the nil-CLI case:

```go
// After the CLI nil-check block, set the default if CLI is nil:
if p.Spec.CLI == nil {
    params.VSCodeEnabled = true
}
```

### Microsoft VS Code CLI download (Amazon Linux 2023)

The `cli-alpine-x64` build is a statically linked binary suitable for Amazon Linux 2023 (glibc-free). Download URL pattern confirmed via HTTP 302 redirect to versioned tar.gz:

```bash
# Tested download URL pattern (from CONTEXT.md sample initCommands):
curl -fsSL https://update.code.visualstudio.com/latest/cli-alpine-x64/stable \
  -o /tmp/vscode-cli.tar.gz
tar -xzf /tmp/vscode-cli.tar.gz -C /usr/local/bin/
chmod +x /usr/local/bin/code
```

The redirect resolves to a versioned URL like `vscode_cli_alpine_x64_cli.tar.gz` with a commit hash. For **version pinning** (recommended by CONTEXT.md), use a URL like:
```
https://update.code.visualstudio.com/commit:COMMIT_HASH/cli-alpine-x64/stable
```
The planning phase must decide which commit hash to pin, or accept `latest`.

**Architecture note:** `cli-alpine-x64` works on Amazon Linux 2023 (confirmed by the statically linked musl libc Alpine build avoiding glibc dependency issues). For Ubuntu-based sandboxes, `cli-linux-x64` would also work. Since Amazon Linux 2023 is the default AMI, `cli-alpine-x64` is the right recommendation.

---

## State of the Art

| Old Approach | Current Approach | Impact |
|---|---|---|
| `code-server` (Coder) for browser VS Code | `code serve-web` (official Microsoft CLI) | Operators get first-party support, same settings sync, no separate daemon project |
| `--connection-token` hardcoded at startup | `--connection-token-file` (fixed in post-1.90 stable) | Token is rotated without restart by writing a new value; wrapper reads at start |
| Manual `aws ssm start-session` + port-forward | `buildPortForwardCmd` wrapper in km | Consistent with `km shell --ports`, no raw AWS CLI knowledge needed |

---

## Open Questions

1. **`--connection-token` vs `--connection-token-file` in the launch wrapper**
   - What we know: `--connection-token-file` was fixed (PRs #219041/#223524, labeled insiders-released). Confidence MEDIUM on stable availability.
   - What's unclear: What version of stable VS Code CLI has the fix? Operators pinning to an older version may hit the bug.
   - Recommendation: Planning phase should choose between `--connection-token-file /etc/km/vscode/token` (simpler wrapper, depends on fix) vs `--connection-token "$(cat /etc/km/vscode/token)"` (avoids file-read bug, always works). The second form is more defensive.

2. **Version pinning for the `code` binary in `docs/vscode.md`**
   - What we know: `latest` redirect works; the redirect URL includes a commit hash that could be pinned.
   - What's unclear: How frequently does `latest` break? Is there a stable channel tag?
   - Recommendation: Document `latest` for ease of use but note in `docs/vscode.md` how to pin by commit hash if reproducibility is needed.

3. **`km doctor` check**
   - What we know: Deferred from this phase by CONTEXT.md.
   - Recommendation: Skip. Document as a future improvement in `docs/vscode.md`.

---

## Validation Architecture

> Nyquist validation is enabled (`workflow.nyquist_validation: true` in `.planning/config.json`).

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing (`testing` stdlib) |
| Config file | None — `go test ./...` convention |
| Quick run command | `go test ./internal/app/cmd/... ./pkg/compiler/... ./pkg/profile/... -run VSCode -count=1` |
| Full suite command | `go test ./internal/app/cmd/... ./pkg/compiler/... ./pkg/profile/... -count=1` |

**Pre-existing test state:** 4 compiler tests are currently failing (unrelated to Phase 73: `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`, `TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime`, `TestUserDataKMTracingServicectlStart`, `TestGitHubUserDataGITASKPASS`). Profile tests (`pkg/profile/...`) pass. Phase 73 tests must not regress the passing tests.

### Phase Requirements → Test Map

No formal REQUIREMENTS.md IDs. Behaviors to verify:

| Behavior | Test Type | Automated Command | File Exists? |
|---|---|---|---|
| `km vscode start` issues `systemctl restart km-vscode` via SSM | unit | `go test ./internal/app/cmd/... -run TestVSCodeStart -count=1` | Wave 0 |
| `km vscode start` polls `is-active` before reading token | unit | `go test ./internal/app/cmd/... -run TestVSCodeStart_PollsBeforeTokenRead -count=1` | Wave 0 |
| `km vscode start` prints connection block with URL + token | unit | `go test ./internal/app/cmd/... -run TestVSCodeStart_ConnectionBlock -count=1` | Wave 0 |
| `km vscode start` on disabled sandbox returns clean error | unit | `go test ./internal/app/cmd/... -run TestVSCodeStart_Disabled -count=1` | Wave 0 |
| `km vscode stop` issues `systemctl stop km-vscode` | unit | `go test ./internal/app/cmd/... -run TestVSCodeStop -count=1` | Wave 0 |
| `km vscode status` issues `systemctl is-active` + journalctl | unit | `go test ./internal/app/cmd/... -run TestVSCodeStatus -count=1` | Wave 0 |
| `--no-forward` skips port-forward | unit | `go test ./internal/app/cmd/... -run TestVSCodeStart_NoForward -count=1` | Wave 0 |
| `--local-port N` overrides local port in port-forward | unit | `go test ./internal/app/cmd/... -run TestVSCodeStart_LocalPort -count=1` | Wave 0 |
| `vscodeEnabled: true` → userdata contains unit + wrapper | unit | `go test ./pkg/compiler/... -run TestUserDataVSCodeEnabled -count=1` | Wave 0 |
| `vscodeEnabled: false` → userdata omits unit + wrapper | unit | `go test ./pkg/compiler/... -run TestUserDataVSCodeDisabled -count=1` | Wave 0 |
| `vscodeEnabled: nil` (omitted) → userdata contains unit (default true) | unit | `go test ./pkg/compiler/... -run TestUserDataVSCodeDefault -count=1` | Wave 0 |
| `daemon-reload` appears in userdata after unit write | unit | `go test ./pkg/compiler/... -run TestUserDataVSCodeDaemonReload -count=1` | Wave 0 |
| `VSCodeEnabled *bool` field added to CLISpec YAML | unit | `go test ./pkg/profile/... -run TestVSCodeEnabledField -count=1` | Wave 0 |
| JSON schema validates `vscodeEnabled` boolean | unit | (covered by existing schema validation tests + new profile test) | Wave 0 |
| Manual smoke: start → connect browser → edit file → verify via km shell | manual | N/A | N/A |

### Sampling Rate

- **Per task commit:** `go test ./internal/app/cmd/... -run TestVSCode -count=1 && go test ./pkg/compiler/... -run TestUserDataVSCode -count=1`
- **Per wave merge:** `go test ./internal/app/cmd/... ./pkg/compiler/... ./pkg/profile/... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `internal/app/cmd/vscode_test.go` — all vscode command tests (SSM mock injected via `NewVSCodeCmdWithDeps`)
- [ ] `pkg/compiler/userdata_test.go` additions — `TestUserDataVSCodeEnabled`, `TestUserDataVSCodeDisabled`, `TestUserDataVSCodeDefault`, `TestUserDataVSCodeDaemonReload`
- [ ] `pkg/profile/` test — `TestVSCodeEnabledField` (bool pointer default, YAML omit = true)

Framework install: None needed — Go stdlib `testing` already in use.

---

## Sources

### Primary (HIGH confidence)

- AWS SSM `GetCommandInvocation` API docs — `StandardOutputContent` max 24,000 chars, `StandardErrorContent` max 8,000 chars. [Link](https://docs.aws.amazon.com/systems-manager/latest/APIReference/API_GetCommandInvocation.html)
- `internal/app/cmd/agent.go` — `sendSSMAndWait` implementation (lines 1067-1113), `SSMSendAPI` interface (lines 28-32), `AgentPollInterval` exported for tests (line 46)
- `internal/app/cmd/shell.go` — `buildPortForwardCmd` (line 577), `runPortForward` (line 473), `parsePortSpecs` (line 556)
- `pkg/profile/types.go` — `CLISpec` struct (lines 354-447), `*bool` pattern for `NotifySlackEnabled`, `NotifyEmailEnabled`, `SlackArchiveOnDestroy`
- `pkg/compiler/userdata.go` — `userDataParams` struct (lines 2796-2885), `generateUserData` function (line 3031), `{{- if .SlackInboundEnabled }}` gate pattern (lines 1207, 1718, 2289-2297)
- `internal/app/cmd/root.go` — subcommand registration pattern, `NewSlackCmd` at line 85
- `internal/app/cmd/agent_test.go` — `mockAgentSSM` interface (lines 26-63), `NewAgentCmdWithDeps` test injection pattern
- VS Code CLI download: redirect from `https://update.code.visualstudio.com/latest/cli-alpine-x64/stable` → versioned tar.gz confirmed via HTTP 302

### Secondary (MEDIUM confidence)

- `--connection-token-file` bug (issue #215537) closed by PRs #219041 and #223524, labeled "insiders-released." Fix likely in stable VS Code CLI as of mid-2024. [Link](https://github.com/microsoft/vscode/issues/215537)
- `--without-connection-token` is a standalone boolean flag (presence = disable token). No `=false` form documented or found. Multiple community examples show it used without value. [Various GitHub issues]

### Tertiary (LOW confidence)

- Version number of stable VS Code CLI that includes the `--connection-token-file` fix — not independently verified. Use `--connection-token "$(cat ...)"` as fallback if planning phase prefers defense-in-depth.

---

## Metadata

**Confidence breakdown:**
- Standard stack (reuse existing helpers): HIGH — directly read from source code
- Architecture (systemd unit format, daemon-reload, no WantedBy): HIGH — mirrors existing units in codebase
- `--without-connection-token=false` flag concern: HIGH (it's invalid — drop it)
- `--connection-token-file` fix availability: MEDIUM — fix confirmed merged, stable release date unverified
- VS Code CLI download URL pattern: HIGH — confirmed via live HTTP redirect
- SSM stdout size limit: HIGH — official AWS API docs

**Research date:** 2026-05-07
**Valid until:** 2026-08-07 (VS Code CLI flags are stable; SSM limits do not change frequently)
