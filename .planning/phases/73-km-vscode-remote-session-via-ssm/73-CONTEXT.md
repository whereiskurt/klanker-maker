# Phase 73: km vscode remote session via SSM — Context

**Gathered:** 2026-05-06
**Status:** Ready for planning
**Source:** Brainstorming dialogue (Q1–Q4 + Approach A user-approved). Full spec: `docs/superpowers/specs/2026-05-06-km-vscode-design.md`.

<domain>
## Phase Boundary

Add `km vscode start | stop | status` so an operator can launch a VS Code Web session inside an
existing sandbox, accessed locally over an SSM port-forward. The goal is companion-style usage:
the operator runs `km agent run` (Claude rips on a task), then opens VS Code on the same sandbox
to review and edit what the agent produced — sessions lasting "a couple of hours."

km owns the runtime contract on the sandbox side (systemd unit, launch wrapper, token rotation,
directory layout). The operator's only job is installing the `code` binary in their profile's
`initCommands`. A new profile flag `spec.cli.vscodeEnabled` (default `true`) gates whether the
unit/wrapper/dir are provisioned at sandbox boot.

**Out of scope:**
- Installing the `code` binary itself (Microsoft `code serve-web`, Coder `code-server`,
  openvscode-server are all valid choices — operator picks via `initCommands`; we document the
  Microsoft `code` CLI as the tested default).
- Auto-stop on idle, `--duration` time-boxing (operator can compose with existing `km at` if
  they want a scheduled stop — explicit deferral).
- Slack/email notification integration.
- DDB schema changes, new SSM Parameter Store entries, new Lambda, new sidecar binary.
- Changes to `km destroy` / `km pause` (systemd unit dies with the EC2 instance; nothing to
  clean up).
- Multiple concurrent VS Code sessions on different ports per sandbox (one per sandbox is enough;
  power users can run `code serve-web` directly via `km shell`).

</domain>

<decisions>
## Implementation Decisions

### Use case (Q1: B — companion to `km agent run`)

- Companion-style, on-demand, time-boxed sessions ("next couple of hours").
- Not a primary work surface — `km shell` and `km agent` remain the main surfaces.
- Operator handles the `code` binary install in their profile's `initCommands`; km does not
  install the binary.

### Lifecycle (Q2: B — manual stop, `--duration` deferred)

- `km vscode start` brings up the server and tunnel; runs until `km vscode stop`.
- No `--duration` flag in this phase. Operator can compose with `km at` for scheduled stops if
  needed (no special-case wiring in km vscode).
- No auto-stop on idle.

### Architecture (Q3: A — thin systemd wrapper, recommended option approved)

- Unit name: `km-vscode.service` (NOT `WantedBy=multi-user.target` — explicit start only).
- Launch wrapper: `/opt/km/bin/km-vscode-launch`
  - Generates fresh token: `TOKEN=$(openssl rand -hex 32)`
  - Writes `/etc/km/vscode/token` mode 0600, owned by `sandbox`
  - Execs `code serve-web --host 127.0.0.1 --port 8443 --connection-token-file /etc/km/vscode/token --without-connection-token=false --accept-server-license-terms`
- Token directory: `/etc/km/vscode/` mode 0700, owned by `sandbox`.
- Token rotates on every `systemctl restart km-vscode` (which is what `km vscode start` issues —
  restart, not start, so each call rotates).
- Server binds 127.0.0.1 only — SSM tunnel is the security boundary, URL token is
  defense-in-depth against other processes on the operator's laptop.
- Matches existing systemd-everywhere idiom (km-http-proxy, km-mail-poller,
  km-slack-inbound-poller). Survives reboots, logs land in journald.

### Ownership split (km installs more, operator installs less — user-clarified)

- Operator's profile (`initCommands`): only installs the `code` binary.
- km's `userdata.go`: writes the systemd unit, the launch wrapper, and the token directory at
  cloud-init time, conditional on the profile flag.
- km's CLI (`internal/app/cmd/vscode.go`): pure SSM wrapper.

### Profile gate (Q4: profile flag default-true)

- New field: `spec.cli.vscodeEnabled` (`bool*` pointer, omit ⇒ default `true`).
- `true` ⇒ userdata writes unit/wrapper/dir.
- `false` ⇒ userdata skips entirely; `km vscode start` against such a sandbox surfaces a clean
  "VS Code not enabled in this sandbox's profile" error (detected by `systemctl status` returning
  unit-not-found).
- Inheritance follows the existing `spec.cli.*` pattern.
- Schema change ⇒ `km init --sidecars` after rebuild (matches the project's documented pattern
  in `memory/project_schema_change_requires_km_init.md`).

### CLI surface

```
km vscode start  <sandbox-id> [--local-port N] [--no-forward]
km vscode stop   <sandbox-id>
km vscode status <sandbox-id>
```

**`start`:**
1. SSM `systemctl restart km-vscode` (token rotates).
2. Poll `systemctl is-active km-vscode` for up to 10 s. On failure, surface last 20 lines of
   `journalctl -u km-vscode --no-pager` and exit non-zero with hint pointing at `docs/vscode.md`.
3. SSM read `/etc/km/vscode/token`.
4. Print connection block:
   ```
   VS Code ready for sb-abc123:
     URL: http://localhost:8443/?tkn=<token>
     Forwarding localhost:8443 → sandbox:8443 (Ctrl-C to disconnect; server keeps running)
   ```
5. Open foreground SSM port-forward via existing `buildPortForwardCmd`
   (`internal/app/cmd/shell.go:577`).

**Flags:**
- `--local-port N` overrides the local-side port if 8443 is taken on the operator's laptop.
- `--no-forward` prints URL/token and exits without starting a tunnel (useful for reconnecting
  from a fresh terminal).

**`stop`:** SSM `systemctl stop km-vscode`. Token file remains; next start overwrites.

**`status`:** SSM `systemctl is-active` + last 20 lines of `journalctl -u km-vscode`. Returns
non-zero if inactive.

### Auth & security model (locked)

- **SSM tunnel = real security boundary.** IAM-authenticated, encrypted, per-session.
- **Token in URL = defense-in-depth.** Prevents other processes on the operator's laptop from
  connecting via `http://localhost:8443/`. Rotated on every `start`.
- **Server binds 127.0.0.1 only.** Nothing on the EC2 instance can reach it; no SG changes needed.
- **Token file is `0600` owned by `sandbox`.** km reads it via SSM RunCommand (running as root
  via the SSM agent), then prints to stdout on the operator's terminal.

</decisions>

<specifics>
## Specific Ideas

### File touch list (from spec)

**New:**
- `internal/app/cmd/vscode.go`
- `internal/app/cmd/vscode_test.go`
- `docs/vscode.md` (operator-facing setup guide with sample `initCommands` snippet for the
  Microsoft `code` CLI)

**Modified:**
- `pkg/profile/` — schema + inheritance for `vscodeEnabled` (default-true bool pointer)
- `pkg/compiler/userdata.go` — conditional block writing unit + wrapper + dir
- `pkg/compiler/userdata_test.go` — coverage for the new block
- `internal/app/cmd/root.go` (or wherever cobra commands register) — wire up `vscode` subcommand
- `CLAUDE.md` — add `km vscode start/stop/status` to the CLI command list

### Existing patterns to mirror

- **systemd unit conditional in userdata.go:** look at how `km-mail-poller`,
  `km-slack-inbound-poller`, `km-http-proxy` are written into `/etc/systemd/system/` from the
  template and gated on profile fields (`if .SandboxEmail`, `if .SlackInboundEnabled`, etc.).
- **SSM SendCommand wrapper:** look at `agent.go` for the pattern of issuing a command, polling
  for completion, capturing stdout. Reuse the same helper functions.
- **SSM port-forward:** reuse `buildPortForwardCmd` from `shell.go:577` verbatim — same
  document name (`AWS-StartPortForwardingSession`), same parameter shape.
- **Profile flag inheritance:** follow the pattern of `notifySlackEnabled`,
  `notifySlackPerSandbox`, `notifySlackTranscriptEnabled` for the bool-pointer + default semantics.
- **Daemon-reload + restart for AMI bakes:** the recent fix (commit `4030fce`) added
  `systemctl daemon-reload` and `restart` (not `start`) before sidecar startup blocks for
  AMI-baked instances. The new vscode unit is NOT auto-started at boot (no `WantedBy`), so this
  particular fix doesn't apply directly — but the principle (write unit + daemon-reload before
  any operation) still holds. After cloud-init writes the unit, the userdata script should
  `systemctl daemon-reload` so the first `km vscode start` finds it.

### Edge cases (from spec)

| Case | Behavior |
|------|----------|
| `code` binary not installed on sandbox | `systemctl restart` fails → surface `journalctl` excerpt with hint pointing at `docs/vscode.md` |
| Local port 8443 already in use | SSM port-forward exits with bind error → operator uses `--local-port` |
| Re-running `start` while another tunnel is open | Second port-forward fails on bind; systemd restart silently rotates the token, breaking the first session. Document the recommendation: stop first, or use `--no-forward` for a fresh URL |
| `vscodeEnabled: false` in profile | Userdata skips the unit/wrapper/dir entirely. `km vscode start` against such a sandbox fails with "VS Code not enabled in this sandbox's profile" (detected via `systemctl status` exit code for "unit not found") |
| `km destroy` / `km pause` | No-op for vscode; systemd unit dies with the box |

### Sample operator setup (target for `docs/vscode.md`)

```yaml
# In a sandbox profile:
spec:
  cli:
    vscodeEnabled: true   # default; can omit
  execution:
    initCommands:
      - "curl -fsSL https://update.code.visualstudio.com/latest/cli-alpine-x64/stable -o /tmp/vscode-cli.tar.gz"
      - "tar -xzf /tmp/vscode-cli.tar.gz -C /usr/local/bin/"
      - "chmod +x /usr/local/bin/code"
```

(Exact URL / pinning strategy is an open question; phase planning may pin a specific version.)

### Deployment requirements (locked)

- Userdata template change ⇒ `make build` (embeds template in km binary).
- Schema change ⇒ `km init --sidecars` (refreshes management Lambda).
- Existing sandboxes that want VS Code ⇒ `km destroy` + `km create`.
- AMI bakes pick up the unit + wrapper automatically on next boot via cloud-init (the unit
  lives in userdata, not in the baked image — same pattern as every other systemd unit km
  manages).

</specifics>

<deferred>
## Deferred Ideas

### Explicitly deferred from this phase

- **`--duration <D>` flag and auto-stop integration.** Operator can compose with `km at` for
  scheduled stops if needed; no special wiring in `km vscode`.
- **`km doctor` checks for VS Code** (e.g., "code binary present in any sandbox profile"). Skip
  until there's evidence of operator confusion.
- **Multiple concurrent sessions per sandbox on different ports.** One per sandbox is enough.
- **Session persistence across `km destroy` + `km create`** (keeping `~/.vscode-server` data
  alive between sandbox lifetimes). Beyond phase scope; existing AMI-bake / additionalVolume
  patterns can carry user data if needed.
- **`code-server` and `openvscode-server` first-class support.** The systemd unit is written
  for `code serve-web` (Microsoft); operators using other flavors will need to tweak the
  wrapper. Documenting only one tested install path keeps the docs honest.
- **Pinning a specific `code` CLI version vs `latest`.** Phase planning to decide; lean toward
  pinning for reproducibility, but `latest` is operationally simpler.

</deferred>

---

*Phase: 73-km-vscode-remote-session-via-ssm*
*Context gathered: 2026-05-06 via brainstorming dialogue*
