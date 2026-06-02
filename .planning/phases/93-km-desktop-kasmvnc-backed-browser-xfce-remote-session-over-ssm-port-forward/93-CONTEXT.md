# Phase 93: km desktop — KasmVNC-backed browser/XFCE remote session - Context

**Gathered:** 2026-06-02
**Status:** Ready for planning
**Source:** Brainstormed design spec (`docs/superpowers/specs/2026-06-02-km-desktop-remote-browser-design.md`) — treated as locked-decision context (PRD-equivalent path)

<domain>
## Phase Boundary

Deliver a `km desktop` capability that gives the operator a graphical session —
default a single maximized **browser** (kiosk), optionally a full **XFCE**
desktop — rendered in their **local browser** over an SSM port-forward. The
purpose is *web-browser-based interactions running remotely*: Chrome/Firefox/Brave
executing inside the sandbox EC2, driven from the operator's laptop browser. The
feature mirrors the existing `km vscode` trust model and CLI shape and reuses its
SSM/port-forward helpers.

**In scope:** profile schema (`spec.runtime.desktop`), `km validate` rules,
compiler config threading + idempotent AMI-bakeable userdata, per-sandbox KasmVNC
credential lifecycle, `km desktop start/status` CLI, `profiles/desktop.yaml`
example, `klanker:desktop` skill, docs, and the full test suite.

**Out of scope (v1):** GNOME/KDE, audio, multi-monitor, session recording,
Amazon Linux 2023, web-based file transfer as a headline feature.
</domain>

<decisions>
## Implementation Decisions

### Engine (locked)
- **KasmVNC** — web-native VNC server with a built-in HTML5 client + seamless
  bidirectional clipboard. One component replacing TigerVNC + websockify + noVNC.
  Chosen specifically because *proper clipboard* is a hard requirement. Not
  Apache Guacamole (its gateway/auth value duplicates the SSM tunnel) and not
  TigerVNC+noVNC (manual sidebar-paste clipboard is too weak).

### Scope / modes (locked)
- Two modes via `spec.runtime.desktop.mode`: **`kiosk`** (default) = matchbox-wm +
  `browsers[0]` maximized (the browser *is* the session, browser keeps its normal
  UI/tabs/URL bar); **`full`** = `exec startxfce4`. GNOME/KDE deferred.

### Profile schema (locked)
- New `RuntimeDesktopSpec` in `pkg/profile/types.go`, sibling to `RuntimeVSCodeSpec`:
  - `enabled *bool` — **default false** when block absent (heavy install; opt-in,
    deliberately opposite of `vscode`'s default-on).
  - `mode string` — `kiosk` | `full`, default `kiosk`.
  - `browsers []string` — subset of `{firefox, chromium, brave}`, default
    `[firefox]`. Kiosk launches `browsers[0]`; full installs all, auto-launches none.
  - `geometry string` — optional, default `1920x1080`.
- `IsDesktopEnabled(*RuntimeDesktopSpec) bool` helper, defaulting **false**.
- JSON schema + `schema_export.go` updated.

### Validation (locked)
- `km validate`: `mode` ∈ {kiosk, full}; `browsers` ⊆ {firefox, chromium, brave};
  `browsers` non-empty when `mode: kiosk`; `geometry` matches `^[0-9]+x[0-9]+$`
  when set; WARN/ERROR when `desktop.enabled` is true and the resolved AMI is not
  an Ubuntu slug/family (v1 KasmVNC target constraint).

### Provisioning (locked)
- **Idempotent userdata** is the single code path, gated by `{{- if .DesktopEnabled }}`:
  install KasmVNC `.deb` + matchbox-window-manager (kiosk) / XFCE (full) +
  selected browsers + fonts/dbus **only if absent**; always (re)seed
  `~/.kasmpasswd`, `~/.vnc/kasmvnc.yaml` (SSL off, clipboard on, geometry),
  `~/.vnc/xstartup` (kiosk vs full); enable the systemd unit; bind loopback.
- **AMI-bakeable**: packages bake in; per-sandbox credential + config seeded fresh
  at boot (never baked) so one desktop AMI serves all sandboxes. Standard
  `km ami bake` + `spec.runtime.ami`.
- Browser install: `firefox`/`chromium` from distro repos; `brave` from the Brave
  APT repo.
- systemd unit auto-starts KasmVNC on every boot (pattern from `km-queue`) so
  `km resume` restores the session.

### Credential (locked)
- Per-sandbox KasmVNC credential (username + random password) generated at
  `km create`, stored locally at `~/.km/desktop/<sandbox-id>` (new dir mirroring
  `~/.km/keys/`), threaded into compiler config (`DesktopKasmCredential`), seeded
  into `~/.kasmpasswd` at boot. **Never baked.** Printed by `km desktop start`.

### CLI (locked)
- `km desktop start <id> [--local-port 8444]`: local-port pre-bind probe → fetch
  DDB record → `extractResourceID(:instance/)` + region → SSM pre-flight (KasmVNC
  unit active?) → print `https://localhost:<port>/` + credential → open blocking
  SSM port-forward (Ctrl-C closes tunnel, session keeps running).
- `km desktop status <id>`: one-round-trip SSM probe → one-line health summary;
  non-zero exit when unhealthy.
- Reuse: `resolveVSCodeDeps`-style DI, `sendSSMAndWait`, `buildPortForwardCmd`,
  `extractResourceID`, `ResolveSandboxID`, the port-probe helper. New file
  `internal/app/cmd/desktop.go`; registered next to `NewVSCodeCmd`.

### Security (locked)
- KasmVNC + session bind **127.0.0.1 only**; no LAN/VPC exposure. Only ingress is
  the operator's SSM `AWS-StartPortForwardingSession` (authenticated, encrypted).
- SSL disabled at the KasmVNC layer is acceptable *only because* of loopback bind +
  encrypted SSM tunnel (avoids self-signed-cert warning). Fallback: self-signed +
  documented "accept the warning" if KasmVNC refuses plain HTTP.

### Networking posture (locked)
- Posture-agnostic: the desktop inherits whatever `spec.network` the profile
  declares. No change to the egress-enforcement model. Browser runtime egress
  governed by the profile as usual.

### Target distro (locked)
- **Ubuntu 24.04 / 22.04 only** in v1 (KasmVNC's official, best-tested builds).

### Deliverables (locked)
- `profiles/desktop.yaml` kiosk-Firefox example, added to
  `scripts/validate-all-profiles.sh`.
- `klanker:desktop` skill, alongside `klanker:vscode`; bump plugin version in
  `plugin.json` + `marketplace.json` (per the cache-gate constraint).
- `docs/desktop.md` runbook + `CLAUDE.md` row + `OPERATOR-GUIDE.md` section.

### Rollout (locked)
- Schema addition → `make build && km init --sidecars` to refresh management
  Lambdas (per the schema-change constraint). Existing sandboxes don't pick up
  `desktop` retroactively — `km destroy && km create`.

### Claude's Discretion
- Exact KasmVNC release version + `.deb` fetch URL/checksum (research to confirm
  current stable; cache pattern like other bundled tools where sensible).
- systemd unit form (user service vs system service) for the KasmVNC server,
  matching the sandbox-user model and surviving `km resume`.
- Exact `kasmvnc.yaml` keys to disable SSL / enable clipboard / set geometry
  (confirm against the installed KasmVNC version's config schema).
- matchbox-window-manager vs a comparable minimal kiosk WM if matchbox is
  unavailable/unmaintained on Ubuntu 24.04.
- Wave breakdown and plan granularity (schema/validate → compiler/userdata →
  CLI → create-credential → docs/skill/example → tests).
- Whether the non-Ubuntu-AMI guard is WARN or hard ERROR (lean ERROR when
  desktop enabled, since KasmVNC won't install on the wrong family).
</decisions>

<specifics>
## Specific Ideas

- Default KasmVNC web port: `8444` (operator `--local-port` default).
- VNC display `:1` if a raw VNC port is referenced anywhere.
- `~/.km/desktop/<id>` credential file format: keep it simple and machine-readable
  (e.g. `user:password` or two lines) so `km desktop start` can print it.
- Pre-flight SSM script pattern: single combined script like `vsCodeStatusScript`
  with a `parseDesktopStatus` analog returning descriptive errors per failure mode
  ("desktop not enabled in this profile — set `spec.runtime.desktop.enabled: true`
  and recreate").
- Compiler tests mirror the `TestUserDataVSCode*` suite naming.
- Document the **first-boot network caveat**: a fresh non-AMI boot must reach
  distro mirrors + the KasmVNC release URL + browser vendor repos; under a
  locked-down `spec.network`, allowlist those for first boot OR bake the AMI on a
  permissive profile and run under a locked one.
</specifics>

<deferred>
## Deferred Ideas

- GNOME / KDE full-desktop modes.
- Amazon Linux 2023 support (KasmVNC RHEL/Fedora-family build).
- Audio, multi-monitor, session recording.
- Web-based file transfer as a headline feature (KasmVNC provides it; `km vscode`/
  SSM already cover file movement).
- Dedicated single-`desktop.browser` field for kiosk (current design reuses
  `browsers[0]`).
</deferred>

---

*Phase: 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward*
*Context gathered: 2026-06-02 from approved design spec (brainstorm complete)*
