# `km desktop` — Browser-based Remote Session over SSM

**Status:** Design approved (brainstorm complete)
**Date:** 2026-06-02
**Author:** KPH + Claude
**Supersedes:** initial Guacamole/noVNC draft (this conversation)

## Summary

Add a `km desktop` capability that gives an operator a full graphical session —
by default a single maximized **browser**, optionally a full **XFCE** desktop —
rendered in their **local browser** over an SSM port-forward. Mirrors the
existing `km vscode` feature's trust model and CLI shape. The engine is
**KasmVNC** (web-native VNC server with a built-in HTML5 client and seamless
clipboard). The primary goal is *web-browser-based interactions running
remotely* (Chrome/Firefox/Brave executing inside the sandbox EC2).

## Goals

- One command, `km desktop start <id>`, that port-forwards a remote graphical
  session and prints a `localhost` URL the operator opens in their browser.
- **Kiosk browser** session (no desktop environment) as the default, lightest mode.
- **Full XFCE** desktop as an opt-in mode for when a real desktop is wanted.
- **Proper bidirectional clipboard** (a hard requirement) between the operator's
  browser and the remote session.
- Posture-agnostic networking: the desktop inherits whatever `spec.network` the
  profile already declares (no change to the egress-enforcement model).
- Same security envelope as `km vscode`: SSM port-forward is the only access
  path; all services bind loopback; no public ingress.
- AMI-bakeable so launches are fast after a one-time bake.

## Non-goals (v1)

- GNOME / KDE desktop environments (XFCE only for the full mode).
- Audio.
- Multi-monitor, session recording.
- Amazon Linux 2023 support (Ubuntu only in v1; AL2023 is a possible follow-up).
- Web-based file transfer as a headline feature (KasmVNC provides it; not a v1
  deliverable — `km vscode`/SSM already cover file movement).

## Decisions (from brainstorm)

| Decision | Choice | Rationale |
|---|---|---|
| Network posture | Operator-driven, per-sandbox | Desktop inherits existing `spec.network`; no new egress model. |
| Stack | KasmVNC (web-native VNC) | Single component; replaces TigerVNC+websockify+noVNC; **seamless clipboard** for the browser-in-browser case. |
| Scope | Kiosk **and** full XFCE | `spec.runtime.desktop.mode: kiosk\|full`, default `kiosk`. GNOME/KDE deferred. |
| Provisioning | Idempotent userdata, AMI-bakeable | Installs at boot if absent, skips if baked. One code path. |
| CLI surface | `km desktop start/status` | Single command; mode/DE chosen in the profile (mirrors `km vscode`). |
| Default | `enabled` defaults **false** | Heavy install; opt-in (opposite of `vscode` default-on). |
| Target distro | Ubuntu 24.04 / 22.04 only | KasmVNC's official, best-tested builds. |
| Deliverables | `profiles/desktop.yaml` + `klanker:desktop` skill | Ship example + operator skill in v1. |

## Architecture & data flow

Same trust model as `km vscode`: the SSM port-forward is the only access path,
every service binds loopback, no public ingress.

```
operator browser ──https──> localhost:8444
       │ (km desktop start: SSM AWS-StartPortForwardingSession 8444→8444)
       ▼
  sandbox EC2 (loopback only):
     KasmVNC server :8444   (built-in HTML5 client + websocket + seamless clipboard)
        └─ session (~/.vnc/xstartup):
             kiosk → matchbox-window-manager + <browser> maximized
             full  → exec startxfce4
                 └─ Firefox / Chromium / Brave
                        └─ outbound ──> profile's spec.network enforcement (unchanged)
```

- **KasmVNC** is one component (vs three) and serves the HTML5 client over its
  own port. SSL is disabled in `kasmvnc.yaml` because the SSM tunnel already
  encrypts and loopback-only binding removes the network exposure — this avoids
  a self-signed-cert browser warning. (Revisit if KasmVNC refuses plain HTTP;
  fallback is self-signed + documented "accept the warning".)
- A systemd unit auto-starts KasmVNC on every boot (pattern from `km-queue`), so
  `km resume` restores the session.
- Browser egress flows through whatever `spec.network` the profile declares.

## Components & boundaries

### 1. Profile schema — `spec.runtime.desktop`

New `RuntimeDesktopSpec` in `pkg/profile/types.go`, sibling to the existing
`runtime.vscode` block.

```yaml
spec:
  runtime:
    desktop:
      enabled: true          # default FALSE when block absent (heavy; opt-in)
      mode: kiosk            # kiosk | full   (default kiosk)
      browsers: [firefox]    # subset of [firefox, chromium, brave]; default [firefox]
                             # kiosk launches browsers[0]; full installs all, none auto-launched
      geometry: 1920x1080    # optional, default 1920x1080
```

- `IsDesktopEnabled(*RuntimeDesktopSpec) bool` helper — **defaults false** (nil
  block or nil `enabled` → false). Deliberately the opposite of
  `IsVSCodeEnabled` (which defaults true) because the desktop install is heavy.
- JSON schema (`pkg/profile/schemas/…`) + `schema_export.go` updated.
- `km validate` rules:
  - `mode` ∈ {`kiosk`, `full`}.
  - `browsers` ⊆ {`firefox`, `chromium`, `brave`}.
  - `browsers` non-empty when `mode: kiosk`.
  - `geometry` matches `^[0-9]+x[0-9]+$` when set.
  - WARN/ERROR when `desktop.enabled` is true and the resolved AMI is not an
    Ubuntu slug/family (v1 KasmVNC target constraint).

**What it does:** declares whether/how a graphical session is provisioned.
**Interface:** consumed by the compiler (userdata generation) and `km validate`.
**Depends on:** nothing new.

### 2. Compiler — userdata + config threading

- New template data field `DesktopEnabled` (+ `DesktopMode`, `DesktopBrowsers`,
  `DesktopGeometry`, `DesktopKasmCredential`) wired through
  `pkg/compiler/service_hcl.go` the same way `VSCodeSSHPubKey` /
  `VSCodeEnabled` are.
- New **idempotent** userdata block in `pkg/compiler/userdata.go`, gated by
  `{{- if .DesktopEnabled }}`:
  1. If KasmVNC absent: install KasmVNC `.deb`, matchbox-window-manager (kiosk)
     or XFCE (`mode: full`), the selected browsers, fonts, dbus. If present
     (baked AMI), skip install.
  2. Always (re)seed per-sandbox config: `~/.kasmpasswd` (credential),
     `~/.vnc/kasmvnc.yaml` (SSL off, clipboard on, geometry), `~/.vnc/xstartup`
     (kiosk: matchbox + `browsers[0]` maximized; full: `exec startxfce4`).
  3. Enable + start the KasmVNC systemd unit; bind loopback.
  4. Ownership/permissions for the `sandbox` user; `restorecon` where relevant.
- Browser install specifics: `firefox`/`chromium` from distro repos; `brave`
  from the Brave APT repo. (Document the repo add for `brave`.)

**What it does:** turns profile fields into a self-contained, idempotent,
AMI-bakeable provisioning script.
**Interface:** `generateUserData` consumes the new fields; emits nothing when
`DesktopEnabled` is false.
**Depends on:** the per-sandbox credential supplied by `km create`.

### 3. `km create` — per-sandbox credential

- At create time (when `desktop.enabled`), generate a KasmVNC credential
  (username + random password), store locally at `~/.km/desktop/<sandbox-id>`
  (new dir, mirrors `~/.km/keys/`), and thread it into the compiler config
  (`DesktopKasmCredential`) so userdata seeds `~/.kasmpasswd` at boot.
- The credential is **never baked** into an AMI — always seeded fresh at boot —
  so a single desktop AMI serves all sandboxes.

### 4. CLI — `internal/app/cmd/desktop.go` (mirrors `vscode.go`)

- **`km desktop start <id>` `[--local-port 8444]`**
  1. Probe local port (reuse the `vscode start` pre-bind probe).
  2. Fetch DDB record → `extractResourceID(:instance/)` → region.
  3. SSM pre-flight: is the KasmVNC unit active? (combined single-round-trip
     script, analogous to `vsCodeStatusScript` + `parseVSCodeStatus`).
  4. Print `https://localhost:<port>/` and the KasmVNC user/password (read from
     `~/.km/desktop/<id>`).
  5. Open the blocking SSM port-forward (`buildPortForwardCmd`); Ctrl-C closes
     the tunnel, the session keeps running.
- **`km desktop status <id>`** — one-round-trip SSM probe → one-line health
  summary; non-zero exit when unhealthy.
- Reuses: `resolveVSCodeDeps`-style DI, `sendSSMAndWait`,
  `buildPortForwardCmd`, `extractResourceID`, `ResolveSandboxID`,
  `HostOptions`/port-probe helpers.
- Registered in the root command tree next to `NewVSCodeCmd`.

**What it does:** the operator-side tunnel + connection-info UX.
**Interface:** `km desktop start|status <id>`.
**Depends on:** DDB record, SSM, local `~/.km/desktop/<id>`.

### 5. Docs & deliverables

- `docs/desktop.md` — runbook (enable in profile, create, `km desktop start`,
  clipboard usage, AMI-bake for speed, the network-allowlist caveat below).
- `CLAUDE.md` "Where to look" row + a short feature section.
- `OPERATOR-GUIDE.md` section.
- `profiles/desktop.yaml` — kiosk-Firefox example; added to
  `scripts/validate-all-profiles.sh`.
- `klanker:desktop` skill — user-invocable, alongside `klanker:vscode`; bump the
  plugin version in `plugin.json` + `marketplace.json` (per the cache-gate memory).

## Provisioning & AMI strategy

- **Idempotent userdata** is the single code path: installs at boot if absent,
  skips if a desktop AMI already baked the packages. Per-sandbox credential and
  config are always seeded fresh at boot.
- **AMI bake** (`km ami bake` + `spec.runtime.ami`) is the recommended path for
  routine use — a fresh non-AMI boot installs a heavy stack (KasmVNC + WM/DE +
  browsers), which is slow.
- **Network caveat (documented):** a fresh non-AMI boot must reach distro
  mirrors + the KasmVNC release URL + browser vendor repos. Under a locked-down
  `spec.network`, either allowlist those domains for the first boot or bake the
  AMI on a permissive profile and run it under a locked one. (Browser *runtime*
  egress is independent and governed by the profile as usual.)

## Security model

- KasmVNC + the session bind **127.0.0.1 only** on the sandbox; no LAN/VPC
  exposure.
- The only ingress is the operator's SSM `AWS-StartPortForwardingSession`
  (authenticated, encrypted) — same as `km vscode`.
- Per-sandbox KasmVNC credential is defense-in-depth so another local process on
  the operator's machine can't ride the forwarded port; the credential is
  per-sandbox, generated at create, never baked.
- SSL disabled at the KasmVNC layer is acceptable *only because* of loopback
  binding + the encrypted SSM tunnel.

## Failure modes

- **Session won't render (black screen):** xstartup/dbus/font issue → `km desktop
  status` reports unit state; runbook lists `km shell <id> -- journalctl
  --user -u kasmvnc` style triage.
- **Local port in use:** pre-bind probe fails fast with a `--local-port` hint
  (reused from `vscode start`).
- **KasmVNC not installed / disabled in profile:** pre-flight parse returns a
  descriptive error ("desktop not enabled in this profile — set
  `spec.runtime.desktop.enabled: true` and recreate").
- **Slow first boot (no AMI):** documented; nudge toward `km ami bake`.
- **Non-Ubuntu AMI:** `km validate` catches it before create.

## Testing

- **Profile:** validate tests — enabled/disabled, `mode` enum, browser-set
  membership, empty-browsers-in-kiosk, geometry format, non-Ubuntu-AMI guard.
- **Compiler:** userdata tests mirroring `TestUserDataVSCode*` — kiosk vs full
  xstartup content, credential seed present, loopback bind, disabled emits
  nothing, missing credential errors, idempotent-install guard present.
- **CLI:** `desktop_test.go` mirroring `vscode_test.go` — port-in-use,
  pre-flight parse states, status output, start prints URL + credential.
- **Inventory gate:** `profiles/desktop.yaml` passes
  `scripts/validate-all-profiles.sh`.

## Rollout

Schema addition → management Lambdas must refresh:
`make build && km init --sidecars` (per the schema-change memory). Existing
sandboxes don't pick up `desktop` retroactively — `km destroy && km create`.
Bump plugin version if the skill ships.

## Open questions / follow-ups

- AL2023 support (KasmVNC RHEL/Fedora-family build) — deferred.
- GNOME/KDE full-desktop modes — deferred.
- Whether to expose a dedicated `desktop.browser` (single) field for kiosk vs
  reusing `browsers[0]` — current design reuses `browsers[0]` for simplicity.
