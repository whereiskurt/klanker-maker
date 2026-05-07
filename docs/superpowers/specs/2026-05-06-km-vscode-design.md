# km vscode — remote VS Code session via SSM port-forward

**Status:** Draft (brainstorm output, pending phase planning)
**Date:** 2026-05-06
**Target phase:** 73

## Summary

Add `km vscode start | stop | status` so an operator can run a VS Code Web
session inside an existing sandbox, accessed locally over an SSM port-forward.
Use case: reviewing/editing what `km agent run` produced, or any
companion-style session lasting "a couple of hours" alongside a Claude run.

km owns the runtime contract (systemd unit, launch wrapper, token rotation,
directory layout). The operator's only job is installing the `code` binary
in the sandbox profile's `initCommands`. A new profile flag
`spec.cli.vscodeEnabled` (default `true`) gates whether the unit is
provisioned at sandbox boot.

## Non-goals

- Not a primary work surface — `km shell` and `km agent` remain the main
  surfaces. This is a companion command.
- No persistent operator-side state, no DDB schema, no SSM Parameter Store
  entries, no Lambda, no sidecar binary.
- No installation of the `code` binary itself — the operator picks the flavor
  (Microsoft `code serve-web`, Coder `code-server`, Gitpod `openvscode-server`)
  and installs it via `initCommands`. We document the Microsoft `code` CLI as
  the tested default.
- No auto-stop on idle. `--duration` time-boxing is deferred — the operator
  uses `km vscode stop` explicitly, or composes with `km at` if they want a
  scheduled stop.
- No integration with Slack/email notifications.
- No changes to `km destroy` or `km pause` — the systemd unit dies with the
  EC2 instance; there are no network resources to clean up.

## Architecture

```
operator laptop                  sandbox EC2 (via SSM)
─────────────                    ─────────────────────
km vscode start <sb>  ───SSM──▶  systemctl restart km-vscode
                                    └─ ExecStart=/opt/km/bin/km-vscode-launch
                                       (rotates token, execs `code serve-web`)
                      ◀──SSM───  cat /etc/km/vscode/token
                      ───SSM──▶  AWS-StartPortForwardingSession
                                    localhost:8443 ──▶ remote 127.0.0.1:8443
browser → http://localhost:8443/?tkn=<token>
```

### Ownership split

| Layer | What it owns |
|-------|--------------|
| Operator profile (`initCommands`) | Install the `code` binary on the sandbox |
| Profile schema (`spec.cli.vscodeEnabled`) | Opt-out switch (default `true`) |
| `userdata.go` template (gated on flag) | Systemd unit, launch wrapper, token directory |
| `internal/app/cmd/vscode.go` (new) | `start` / `stop` / `status` SSM wrappers + port-forward |
| `docs/vscode.md` (new) | Operator setup guide with sample `initCommands` snippet |

## Sandbox-side contract

These paths and names are what km expects when issuing SSM commands. They
are written into the sandbox by the userdata template, not authored by the
operator.

| Path | Purpose | Mode | Owner |
|------|---------|------|-------|
| `/etc/systemd/system/km-vscode.service` | Systemd unit (not enabled — explicit start only) | 0644 | root |
| `/opt/km/bin/km-vscode-launch` | Launch wrapper: rotates token, execs `code serve-web` | 0755 | root |
| `/etc/km/vscode/` | Token directory | 0700 | sandbox |
| `/etc/km/vscode/token` | 64-hex-char token, regenerated on every `start` | 0600 | sandbox |

### Systemd unit

```ini
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
```

### Launch wrapper

```bash
#!/usr/bin/env bash
# /opt/km/bin/km-vscode-launch — rotate token + exec `code serve-web`
set -euo pipefail
umask 077
TOKEN=$(openssl rand -hex 32)
printf '%s' "$TOKEN" > /etc/km/vscode/token
chown sandbox:sandbox /etc/km/vscode/token
chmod 0600 /etc/km/vscode/token
# Bind 127.0.0.1 only — SSM port-forward is the security boundary.
exec /usr/local/bin/code serve-web \
    --host 127.0.0.1 \
    --port 8443 \
    --connection-token-file /etc/km/vscode/token \
    --without-connection-token=false \
    --accept-server-license-terms
```

### Userdata gate

```go
{{- if .VSCodeEnabled }}
# write systemd unit, wrapper, token dir
# do NOT systemctl enable — start is explicit
{{- end }}
```

`VSCodeEnabled` is a new field on the userdata template input, populated
from `spec.cli.vscodeEnabled` in the profile (default `true`).

## CLI surface

```
km vscode start  <sandbox-id> [--local-port N] [--no-forward]
km vscode stop   <sandbox-id>
km vscode status <sandbox-id>
```

### `start`

1. SSM `systemctl restart km-vscode` (restart, not start — token rotates).
2. Poll `systemctl is-active km-vscode` for up to 10 s. On failure, surface
   the last 20 lines of `journalctl -u km-vscode --no-pager` and exit
   non-zero with a hint pointing at `docs/vscode.md`.
3. SSM read `/etc/km/vscode/token`.
4. Print connection block:
   ```
   VS Code ready for sb-abc123:
     URL: http://localhost:8443/?tkn=<token>
     Forwarding localhost:8443 → sandbox:8443 (Ctrl-C to disconnect; server keeps running)
   ```
5. Open foreground SSM port-forward via `buildPortForwardCmd`
   (existing helper at `internal/app/cmd/shell.go:577`).

Flags:
- `--local-port N` overrides the local-side port if 8443 is taken on the
  operator's laptop.
- `--no-forward` prints URL/token and exits without starting a tunnel
  (useful when reconnecting from a fresh terminal).

### `stop`

SSM `systemctl stop km-vscode`. Token file is left in place; the next `start`
overwrites it.

### `status`

SSM `systemctl is-active km-vscode` plus the last 20 lines of
`journalctl -u km-vscode`. Returns non-zero if inactive.

## Profile schema

New field under `spec.cli`:

| Field | Type | Default | Effect |
|-------|------|---------|--------|
| `vscodeEnabled` | `bool*` (pointer-to-bool, omit ⇒ default `true`) | `true` | Provision systemd unit, launch wrapper, token dir at sandbox boot |

Inheritance follows the existing `spec.cli.*` pattern (`pkg/profile/`).

## Auth & security model

- **SSM tunnel is the real security boundary.** IAM-authenticated, encrypted,
  per-session.
- **Token in URL is defense-in-depth.** Prevents other processes on the
  operator's laptop from connecting via `http://localhost:8443/`. Rotated
  on every `start`.
- **Server binds 127.0.0.1 only.** Nothing else on the EC2 instance can
  reach it (no public exposure, no security-group changes needed).
- **Token file is `0600` owned by `sandbox`.** km reads it via SSM
  RunCommand (running as root via the SSM agent), then prints to stdout
  on the operator's terminal.

## Edge cases

| Case | Behavior |
|------|----------|
| `code` binary not installed on sandbox | `systemctl restart` fails → we surface `journalctl` excerpt with hint pointing at `docs/vscode.md` |
| Local port 8443 already in use | SSM port-forward exits with bind error → operator uses `--local-port` |
| Re-running `start` while another tunnel is open | Second port-forward fails on bind; systemd restart silently rotates the token, breaking the first session. Document the recommendation: stop first, or use `--no-forward` for a fresh URL |
| `vscodeEnabled: false` in profile | Userdata skips the unit/wrapper/dir entirely. `km vscode start` against such a sandbox fails with "VS Code not enabled in this sandbox's profile" (detected by `systemctl status` returning unit-not-found) |
| `km destroy` / `km pause` | No-op for vscode; systemd unit dies with the box |

## Deployment requirements

- Userdata template change → `make build` (embeds template in km binary).
- Lambda needs the new km binary → `km init --sidecars`.
- Existing sandboxes that want VS Code → `km destroy` + `km create` (profile
  schema addition is the pattern documented in
  `memory/project_schema_change_requires_km_init.md`).
- AMI bakes pick up the unit + wrapper automatically on next boot via
  cloud-init (the unit lives in userdata, not in the baked image — this is
  the same pattern as every other systemd unit km manages).

## Testing

| Layer | Test |
|-------|------|
| `internal/app/cmd/vscode_test.go` (new) | Flag parsing, error messages, SSM command construction (mock the SSM client like `agent_test.go` / `shell_test.go`) |
| `pkg/profile/` | `vscodeEnabled` default-`true` and inheritance |
| `pkg/compiler/` | Userdata conditional block — emits unit/wrapper when `vscodeEnabled: true`, omits when `false` |
| Manual smoke | Spin up sandbox with default profile + Microsoft `code` install in `initCommands`, run `km vscode start`, connect from browser, edit a file in `/workspace`, verify edits land via `km shell` |

No live-EC2 integration tests in CI — manual smoke is captured in the
phase plan's verification steps.

## Open questions (deferred to phase planning)

- Do we want `km doctor` checks for vscode (e.g., "code binary present in
  any sandbox profile"), or skip until there's evidence of operator
  confusion?
- Sample `initCommands` snippet for installing the Microsoft `code` CLI —
  pin a version, or always pull `latest`? (Lean toward pinning.)
- Do we expose a way to run multiple VS Code sessions on different ports
  per sandbox? (Default answer: no — one per sandbox is enough; operators
  who want more can use `code serve-web` directly via `km shell`.)

## File touch list

New:
- `internal/app/cmd/vscode.go`
- `internal/app/cmd/vscode_test.go`
- `docs/vscode.md`

Modified:
- `pkg/profile/` — schema + inheritance for `vscodeEnabled`
- `pkg/compiler/userdata.go` — conditional block writing unit + wrapper + dir
- `pkg/compiler/userdata_test.go` — coverage for the new block
- `internal/app/cmd/root.go` (or wherever cobra commands register) — wire up `vscode` subcommand
- `CLAUDE.md` — add `km vscode` to the CLI command list
- `.planning/ROADMAP.md` — add Phase 73 entry (handled via `gsd:add-phase`)
