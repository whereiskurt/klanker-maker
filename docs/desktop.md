# km desktop — Remote Browser Session via KasmVNC + SSM

Klanker supports `km desktop start | status` so operators can view and interact with a graphical browser session running inside a sandbox EC2 from their **local browser** over an SSM port-forward.

The engine is **KasmVNC** — a web-native VNC server with a built-in HTML5 client and seamless bidirectional clipboard. Two modes are available: **kiosk** (default — a single maximized browser, lightest) and **full** (XFCE4 desktop environment). This feature mirrors the `km vscode` trust model: SSM port-forward is the only access path, every service binds loopback, no public ingress.

## Table of Contents

1. [How it works](#how-it-works)
2. [Profile configuration](#profile-configuration)
3. [Engine — KasmVNC](#engine--kasmvnc)
4. [Modes: kiosk vs full](#modes-kiosk-vs-full)
5. [One-time setup](#one-time-setup)
6. [Per-sandbox workflow](#per-sandbox-workflow)
7. [Clipboard usage](#clipboard-usage)
8. [Credential lifecycle](#credential-lifecycle)
9. [Security model](#security-model)
10. [First-boot network caveat + AMI-bake workflow](#first-boot-network-caveat--ami-bake-workflow)
11. [km resume behavior](#km-resume-behavior)
12. [CLI commands](#cli-commands)
13. [Troubleshooting](#troubleshooting)
14. [Limitations](#limitations)

---

## How it works

```
operator browser ──https──> localhost:8444
       │ (km desktop start: SSM AWS-StartPortForwardingSession 8444→8444)
       ▼
  sandbox EC2 (loopback-only bind):
     KasmVNC server :8444   (built-in HTML5 client + websocket + seamless clipboard)
        └─ session (~/.vnc/xstartup):
             kiosk → matchbox-window-manager + <browser> maximized
             full  → exec startxfce4
                 └─ Firefox / Chromium / Chrome / Brave
                        └─ outbound ──> profile's spec.network enforcement (unchanged)
```

- **KasmVNC** is one component (vs three for TigerVNC + websockify + noVNC) and serves the HTML5 client over its own port. Seamless bidirectional clipboard is built in — a hard requirement for the browser-in-browser use case.
- **SSM tunnel = security boundary.** IAM-authenticated, encrypted, no public port exposed.
- A **systemd unit** auto-starts KasmVNC on every boot so `km resume` restores the session.
- Browser **egress** flows through whatever `spec.network` the profile declares. The desktop inherits the profile's existing network posture with no changes to the egress-enforcement model.

---

## Profile configuration

```yaml
spec:
  runtime:
    ami: ubuntu-24.04         # Ubuntu 24.04 or 22.04 required (KasmVNC v1 constraint)
    desktop:
      enabled: true           # default FALSE; opt-in (heavy install)
      mode: kiosk             # kiosk | full   (default kiosk)
      browsers:               # subset of: firefox, chromium, chrome, brave
        - firefox             # kiosk launches browsers[0]; full installs all, none auto-launched
      geometry: 1920x1080     # optional, default 1920x1080
```

| Field | Type | Default | Purpose |
|---|---|---|---|
| `desktop.enabled` | bool* | **false** | Provision KasmVNC + DE at sandbox boot. Must be set `true` explicitly. |
| `desktop.mode` | string | `kiosk` | `kiosk` = matchbox-wm + `browsers[0]` maximized. `full` = XFCE4 desktop. |
| `desktop.browsers` | []string | `[firefox]` | Browsers to install. `chrome` = Google Chrome; `chromium` = open-source build. Both are first-class enum values. |
| `desktop.geometry` | string | `1920x1080` | VNC display geometry (`WIDTHxHEIGHT`). |

`bool*` = pointer-bool with profile-inheritance semantics.

**Ubuntu-only (v1):** `km validate` returns a hard error when `desktop.enabled: true` and the profile's AMI resolves to a non-Ubuntu family (Amazon Linux 2023, custom AMI, etc.). AL2023 support is deferred.

**Browser enum:** `{firefox, chromium, chrome, brave}`.
- `firefox` — installed from the Mozilla PPA.
- `chromium` — open-source build from Ubuntu PPAs.
- `chrome` — Google Chrome from the Google APT repo (`google-chrome-stable`).
- `brave` — Brave Browser from the Brave APT repo.

The minimal example profile lives at `profiles/desktop.yaml`.

---

## Engine — KasmVNC

KasmVNC was chosen over alternatives for two specific reasons:

1. **One component** — replaces TigerVNC + websockify + noVNC. All three services in a single binary.
2. **Seamless bidirectional clipboard** — a hard requirement for the browser-in-browser case. Both keyboard shortcuts (Ctrl+C / Ctrl+V) and the toolbar clipboard pass text without the paste-from-sidebar friction of the noVNC alternative.

**SSL is disabled at the KasmVNC layer** because the SSM tunnel already encrypts the transport and the loopback-only bind removes the network exposure — this avoids a self-signed-cert browser warning in the operator's local browser. If a future KasmVNC release refuses plain HTTP, the fallback is a self-signed cert with documented "accept the warning" instructions.

---

## Modes: kiosk vs full

| Mode | What launches | Window manager | Use when |
|---|---|---|---|
| `kiosk` (default) | `browsers[0]` maximized, full-screen | `matchbox-window-manager` (minimal, kiosk-oriented) | You need a remote browser; nothing else |
| `full` | XFCE4 desktop environment | XFCE4 | You need a file manager, terminal, multiple apps |

**Kiosk mode** keeps all browser UI (tabs, URL bar, developer tools) available — only the desktop environment overhead is eliminated. The browser IS the session.

**Full mode** installs all listed browsers but auto-launches none — the XFCE session manager starts, and the operator opens applications from the desktop.

---

## One-time setup

After enabling `spec.runtime.desktop` in a profile for the first time, refresh the management Lambdas so the new schema fields are understood remotely:

```bash
make build && km init --sidecars
```

Without `km init --sidecars`, `km create --remote` against a desktop-enabled profile will fail to thread the KasmVNC credential (`DesktopKasmCredential`) into userdata.

---

## Per-sandbox workflow

```bash
# 1. Create — a per-sandbox KasmVNC credential is generated locally and threaded into userdata
km create profiles/desktop.yaml --alias my-desktop

# 2. Get the sandbox ID
SB=$(km list | awk '/my-desktop/ {print $1}')

# 3. Open the SSM tunnel (blocking — Ctrl-C to close the tunnel; session keeps running)
km desktop start $SB
# Output:
#   KasmVNC session ready
#   URL:      https://localhost:8444/
#   Username: sandbox
#   Password: <random-per-sandbox>
#   Press Ctrl-C to close the tunnel (KasmVNC keeps running on the sandbox).

# 4. Open https://localhost:8444/ in your local browser while the tunnel is active.
#    Log in with the username + password printed by km desktop start.

# 5. Detach — Ctrl-C the terminal running km desktop start.
#    KasmVNC stays running; reconnect any time:
km desktop start $SB

# 6. (Optional) check KasmVNC unit state
km desktop status $SB

# 7. Destroy — cleans up the local credential file
km destroy $SB --remote --yes
```

`--local-port <N>` overrides the default `8444` if it is already in use:

```bash
km desktop start $SB --local-port 18444
```

`km desktop start` and `km desktop status` accept the same identifier formats as other `km` subcommands: full sandbox ID (e.g., `desk-ee9499b5`), alias (`my-desktop`), or row number from `km list`.

---

## Clipboard usage

KasmVNC includes seamless bidirectional clipboard:

- **Local → remote:** copy text on your operator machine; in the remote browser, paste with Ctrl+V.
- **Remote → local:** copy text in the remote browser (Ctrl+C); paste on your operator machine.

The KasmVNC toolbar (a small tab on the left edge of the browser window) also provides an explicit clipboard panel for transferring content when keyboard shortcuts are intercepted by the browser.

---

## Credential lifecycle

At `km create` time (when `desktop.enabled: true`):

1. A per-sandbox KasmVNC credential (username + random password) is generated.
2. It is stored locally at `~/.km/desktop/<sandbox-id>`.
3. It is threaded into the compiler config and seeded into `~/.kasmpasswd` on the sandbox at boot.

The credential is **never baked into an AMI** — it is always seeded fresh at boot — so one desktop AMI can serve many sandboxes with different credentials.

`km destroy` removes the `~/.km/desktop/<sandbox-id>` file. Manual cleanup is only needed when a sandbox is wiped out-of-band.

---

## Security model

| Layer | Mechanism |
|---|---|
| **Transport** | SSM port-forward: IAM-authenticated, encrypted, no public port exposed |
| **Authentication** | Per-sandbox KasmVNC credential (random password, seeded at boot) |
| **Blast radius** | One credential per sandbox; compromise of one credential affects only that sandbox |
| **Credential storage** | `~/.km/desktop/<sandbox-id>` on the operator laptop (mode 0600) |
| **Port exposure** | KasmVNC binds `127.0.0.1` on the sandbox — no LAN/VPC exposure |
| **SSL** | Disabled at KasmVNC layer (loopback bind + SSM encryption justify it) |

The SSM tunnel is the real security boundary. KasmVNC authentication is defense-in-depth so another local process on the operator's machine cannot ride the forwarded port.

---

## First-boot network caveat + AMI-bake workflow

A fresh non-AMI boot must reach the following endpoints to install the desktop stack during userdata:

| Endpoint | Purpose |
|---|---|
| `archive.ubuntu.com`, `security.ubuntu.com` | Ubuntu package mirrors |
| `github.com`, `objects.githubusercontent.com` | KasmVNC release tarball |
| `ppa.launchpad.net`, `keyserver.ubuntu.com` | Firefox PPA |
| `dl.google.com` | Google APT repo (for `browsers: [chrome]`) |
| `brave-browser-apt-release.s3.brave.com` | Brave APT repo (for `browsers: [brave]`) |

Under a **locked-down `spec.network`**, either allowlist these domains for first boot, OR use the AMI-bake workflow (recommended for routine use):

```bash
# Step 1 — Create with an open network profile (allowedDNSSuffixes: ["*"])
#           so the userdata install can reach package repos
km create profiles/desktop.yaml --alias bake-session

# Step 2 — Wait for the sandbox to fully boot, then bake an AMI
km ami bake bake-session
# Output includes: ami-xxxxxxxxxxxxxxxxx

# Step 3 — Destroy the bake sandbox
km destroy bake-session --remote --yes

# Step 4 — Add the baked AMI to your production profile
#   spec:
#     runtime:
#       ami: ami-xxxxxxxxxxxxxxxxx   # <-- the baked AMI ID
#       desktop:
#         enabled: true             # packages already installed; fast boot
#         mode: kiosk
#         browsers: [firefox]
#
# Credentials are NOT baked — they are seeded fresh from ~/.km/desktop/<id> at each boot.

km validate profiles/my-production-desktop.yaml
km create profiles/my-production-desktop.yaml
```

With a baked AMI, subsequent sandbox launches skip all package installation and boot directly into KasmVNC.

---

## km resume behavior

The KasmVNC systemd unit is enabled at boot and restarts automatically. After `km resume <sandbox-id>`, run `km desktop start <sandbox-id>` again to re-open the tunnel. The session's browser state (tabs, history, local storage) is preserved from before the pause.

---

## CLI commands

```bash
km desktop start <sandbox-id> [--local-port 8444]
km desktop status <sandbox-id>
```

**`km desktop start <sandbox-id>`**
1. Probes the local port (pre-bind check; fails fast if in use).
2. Fetches the DDB record → extracts instance ID + region.
3. SSM pre-flight: is the KasmVNC systemd unit active? Descriptive errors for: not installed, unit inactive, desktop not enabled in profile.
4. Prints `https://localhost:<port>/` + KasmVNC user/password (from `~/.km/desktop/<id>`).
5. Opens a blocking SSM port-forward (`AWS-StartPortForwardingSession`); Ctrl-C closes the tunnel, the session keeps running.

**`km desktop status <sandbox-id>`**
- One-round-trip SSM probe to check KasmVNC unit state.
- Prints a one-line health summary.
- Exits non-zero when unhealthy.

---

## Troubleshooting

**"local port 8444 is already in use — pick a different one with --local-port"**
Another process holds the port. Use `--local-port <N>`:
```bash
km desktop start $SB --local-port 18444
```

**"desktop not enabled in this profile"**
The sandbox was created with `desktop.enabled: false` (or the field absent). Recreate with `spec.runtime.desktop.enabled: true`.

**"KasmVNC is not running" / unit inactive**
The sandbox may not have finished booting, or the systemd unit failed. Check:
```bash
km shell $SB -- journalctl --user -u kasmvnc -n 50
```
Common causes: missing fonts/dbus (first-boot package failure), bad geometry string.

**Black screen after connecting**
The xstartup script failed. Triage:
```bash
km shell $SB -- journalctl --user -u kasmvnc -n 50
km shell $SB -- cat ~/.vnc/*.log
```
Check that dbus-launch and matchbox-window-manager (kiosk) or startxfce4 (full) are installed.

**Slow first boot**
Expected if using a non-AMI launch — the desktop stack (KasmVNC + WM + browser) must install from scratch. Use the AMI-bake workflow for routine use. See [First-boot network caveat](#first-boot-network-caveat--ami-bake-workflow).

**Credential file missing (`~/.km/desktop/<id>` not found)**
The sandbox was created on a different machine, or the file was deleted manually. Options:
- Use `km shell $SB` to SSM into the sandbox and read `~/.kasmpasswd`.
- Recreate the sandbox with `km destroy $SB --remote --yes && km create <profile>`.

**`km validate` error: "desktop requires an Ubuntu AMI"**
Set `spec.runtime.ami: ubuntu-24.04` (or `ubuntu-22.04`). Amazon Linux 2023 is not supported in v1.

---

## Limitations

- **Ubuntu 24.04 / 22.04 only (v1).** Amazon Linux 2023 support (KasmVNC RHEL-family build) is deferred.
- **No `km desktop stop` command.** Ctrl-C ends the foreground tunnel; KasmVNC keeps running. Scheduled stops compose with `km at`.
- **Existing sandboxes need reprovisioning.** Sandboxes created without `desktop.enabled: true` do NOT get KasmVNC installed retroactively. `km destroy` + `km create` to re-provision.
- **GNOME / KDE deferred.** XFCE4 is the only supported full-desktop environment.
- **Audio deferred.** KasmVNC has audio support; it is not wired up in v1.
- **Multi-monitor deferred.** Single display only in v1.
- **Per-machine credential (cross-machine gap).** `~/.km/desktop/<id>` is written on the creation machine only. To connect from a different laptop, read the credential from the sandbox via `km shell <id> -- cat ~/.kasmpasswd` and use it to log in manually.

---

See also:
- `docs/superpowers/specs/2026-06-02-km-desktop-remote-browser-design.md` — full design spec
- `skills/desktop/SKILL.md` — `klanker:desktop` skill quick reference
- `profiles/desktop.yaml` — minimal kiosk-Firefox example profile
- `CLAUDE.md` — Where to look table
