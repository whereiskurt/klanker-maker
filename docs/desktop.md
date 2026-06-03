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
10. [First-boot install, network, and the AMI-bake workflow](#first-boot-install-network-and-the-ami-bake-workflow)
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
- **Enforcement-mode agnostic.** Both `spec.network.enforcement: proxy` and `ebpf`/`both` work on Ubuntu desktop sandboxes — the OS-aware bootstrap frees `:53` from Ubuntu's `systemd-resolved` for the eBPF resolver. The desktop install runs before enforcement is active (see [First-boot install, network…](#first-boot-install-network-and-the-ami-bake-workflow)).

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

**SSL is not *required* at the KasmVNC layer** (`require_ssl: false`) because the SSM tunnel already encrypts the transport and the loopback-only bind removes the network exposure. KasmVNC still serves HTTPS and presents a TLS cert; the bootstrap generates a **self-signed cert whose SAN includes `localhost` + `127.0.0.1`** so it matches the `https://localhost:8444/` URL reached through the port-forward (the packaged default snakeoil cert has the system hostname as CN, which mismatches). It is still self-signed, so the browser shows a one-time untrusted-CA warning that is safe to accept given the loopback + SSM model.

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

The default `km create` is **remote**: the create-handler Lambda runs `km create` as a subprocess and **compiles the userdata itself**, so the Lambda's bundled `km` must contain the desktop schema + OS-aware bootstrap. After pulling a build that changes either, redeploy the Lambdas:

```bash
make build-lambdas        # clean build — km init SKIPS already-present build/*.zip
km init --dry-run=false   # full apply; bundles the current km into the create-handler Lambda
```

> `km init --sidecars` rebuilds binaries + cold-starts the Lambda but is less reliable than the full apply for picking up compiler changes; prefer `--dry-run=false`. `km create --local` always uses your local `km` binary, so for iterating you can skip the Lambda redeploy entirely.

Without a current Lambda, a **remote** desktop create produces stale userdata (e.g. missing the Ubuntu OS-aware fixes or the KasmVNC credential threading).

---

## Per-sandbox workflow

```bash
# 1. Create — a per-sandbox KasmVNC credential is generated locally and threaded into userdata
km create profiles/desktop.yaml --alias my-desktop

# 2. Get the sandbox ID
SB=$(km list | awk '/my-desktop/ {print $1}')

# 3. Open the SSM tunnel (blocking; auto-reconnects if it drops — Ctrl-C to close it; session keeps running)
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
| **SSL** | Not required at KasmVNC layer (`require_ssl: false`); still serves HTTPS with a self-signed cert (SAN: `localhost`/`127.0.0.1`) matching the tunnel URL — untrusted-CA warning expected, safe to accept |

The SSM tunnel is the real security boundary. KasmVNC authentication is defense-in-depth so another local process on the operator's machine cannot ride the forwarded port.

---

## First-boot install, network, and the AMI-bake workflow

**The `spec.network` allowlist does NOT gate the desktop install.** The desktop
stack is installed in userdata **before** network enforcement (the proxy/iptables
DNAT and the eBPF cgroup attach) is configured, and the sandbox security group
permits HTTPS (443) egress. So a fresh boot reaches the package repos regardless
of the profile's `allowedDNSSuffixes`/`allowedHosts` — you do **not** need to
widen the allowlist for the install to succeed. (`apt` is pinned to **HTTPS** and
IPv4 on Ubuntu because the SG allows only 443/53 — port 80 is closed — and the
EC2 mirror's IPv6 is unroutable; this is handled automatically by the OS-aware
bootstrap.)

What the allowlist **does** govern is the **browser's runtime traffic** once
enforcement comes up — i.e. which sites the operator can actually reach inside the
session. That's the normal `spec.network` posture; tune it per profile.

The only real first-boot cost is **time**: the desktop stack (KasmVNC + WM +
browser, ~hundreds of MB) installs from scratch. For routine use, bake an AMI:

```bash
# Step 1 — Create a desktop sandbox (any network posture works — the install runs
#           pre-enforcement over HTTPS; no need to widen the allowlist)
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
km desktop rekey <sandbox-id> [--force] [--yes]
km desktop restart <sandbox-id> [--yes]
```

**`km desktop start <sandbox-id>`**
1. Probes the local port (pre-bind check; fails fast if in use).
2. Fetches the DDB record → extracts instance ID + region.
3. SSM pre-flight: is the KasmVNC systemd unit active? Descriptive errors for: not installed, unit inactive, desktop not enabled in profile.
4. Prints `https://localhost:<port>/` + KasmVNC user/password (from `~/.km/desktop/<id>`).
5. Opens a blocking SSM port-forward (`AWS-StartPortForwardingSession`) with **auto-reconnect**: the `session-manager-plugin` has no keep-alive, so on laptop sleep / Wi-Fi roam / NAT idle-timeout the tunnel is re-established automatically (an HTTPS liveness probe also recycles a silently-hung plugin). KasmVNC survives server-side, so you land back in the same session. Ctrl-C closes the tunnel for good; the session keeps running on the sandbox.

**`km desktop status <sandbox-id>`**
- One-round-trip SSM probe to check KasmVNC unit state.
- Prints a one-line health summary.
- Exits non-zero when unhealthy.

**`km desktop rekey <sandbox-id>`** — rotate the KasmVNC password on a running sandbox without `destroy && create` (parallels `km vscode rekey`).
1. Gates: EC2 running-state check → `km lock` check (`--force` to override) → SSM pre-flight.
2. Generates a fresh 16-char password, rewrites `~/.kasmpasswd` on the sandbox via `kasmvncpasswd` over SSM (with a readback check), then **atomically** replaces the local `~/.km/desktop/<id>` credential.
3. **No session interruption.** KasmVNC re-reads its password file per web-auth (verified: after a rekey the running server accepts the new password and rejects the old one, with no restart — same `MainPID`), so an already-connected session stays live and the new password applies on the next login. Re-open with `km desktop start`.
4. `--yes` skips the confirmation prompt; `--force` overrides a `km lock`. If the local `~/.km/desktop/<id>` is absent (cross-laptop), the username defaults to `kasm` (the only user `km` provisions) and the file is created fresh.

**`km desktop restart <sandbox-id>`** — force a server-side restart of the KasmVNC session when it's frozen, the window manager is wedged, or input handling is stuck (e.g. a latched modifier). Equivalent to logging out of XFCE and back in.
1. Pre-flight confirms the desktop is **provisioned** (the unit *file* exists) — deliberately NOT that it's currently active, since a hung/inactive session is exactly when you want to restart.
2. Over SSM (as root): `systemctl stop kasmvnc` → hard-kill a wedged `Xvnc` (`vncserver -kill :1`) → clear stale `/tmp/.X1-lock` + `/tmp/.X11-unix/X1` → `systemctl start kasmvnc`, then verifies the unit comes back **active**. Restarting the unit re-runs `~/.vnc/xstartup`, so the WM + browser come up fresh.
3. **Interrupts the live session** — the connected browser session is dropped (the sandbox, its files, and the KasmVNC credential are untouched). `--yes` skips the confirmation prompt. Reconnect with `km desktop start` afterward.

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
The sandbox may not have finished booting, or the systemd unit failed. `kasmvnc`
is a **system** service. Open a shell (`km shell $SB` is interactive — it does not
take a trailing `-- <cmd>`) and run:
```bash
sudo systemctl status kasmvnc
sudo journalctl -u kasmvnc -n 50
```
Common causes (all fixed in the OS-aware bootstrap, but useful when debugging a
hand-rolled profile): missing fonts/dbus, `:53` held by systemd-resolved (eBPF
mode), an unreadable TLS cert (sandbox not in the `ssl-cert` group), or a bad
geometry string.

**Black screen after connecting (browser exits instantly)**
The window manager came up but the browser failed to launch. The usual root cause
on Ubuntu 24.04 is the **unprivileged user-namespace restriction**
(`kernel.apparmor_restrict_unprivileged_userns=1`), which kills the browser's
content sandbox at startup. The OS-aware bootstrap sets that sysctl to `0`, so a
current build does not hit this — but a **baked AMI or create-handler Lambda that
predates the fix** will. Recreate from a current build (`make build-lambdas &&
km init --dry-run=false`, then `km destroy && km create`), or rebake the AMI. To
confirm the cause in `km shell $SB`:
```bash
sudo journalctl -u kasmvnc -n 50
cat ~/.vnc/*.log
sysctl kernel.apparmor_restrict_unprivileged_userns   # should be 0
```
Also verify `dbus-launch` and `matchbox-window-manager` (kiosk) / `startxfce4`
(full) are installed.

**Firefox: "Your profile cannot be loaded"**
The browser launched but could not create its profile under `~/.config/mozilla`
(Firefox uses the XDG path, not `~/.mozilla`). Root cause: `~/.config` left
root-owned by the bootstrap. Fixed in the current bootstrap (`~/.config` is
chowned to the sandbox user); seen only on stale AMIs/Lambdas. Recreate from a
current build, or as an immediate patch in `km shell $SB`:
```bash
sudo chown -R sandbox:sandbox /home/sandbox/.config
```

**XFCE (full mode): "Unable to load a failsafe session"**
XFCE could not find `/etc/xdg` because a bare VNC session leaves `XDG_CONFIG_DIRS`
unset. Fixed in the current bootstrap (xstartup exports `XDG_CONFIG_DIRS` /
`XDG_DATA_DIRS`). Seen only on AMIs/Lambdas built before the fix — recreate from a
current build.

**Slow first boot**
Expected if using a non-AMI launch — the desktop stack (KasmVNC + WM + browser) must install from scratch. Use the AMI-bake workflow for routine use. See [First-boot install, network…](#first-boot-install-network-and-the-ami-bake-workflow).

**Credential file missing (`~/.km/desktop/<id>` not found)**
The sandbox was created on a different machine, or the file was deleted manually. Options:
- Use `km shell $SB` to SSM into the sandbox and read `~/.kasmpasswd`.
- Recreate the sandbox with `km destroy $SB --remote --yes && km create <profile>`.

**`km validate` error: "desktop requires an Ubuntu AMI"**
Set `spec.runtime.ami: ubuntu-24.04` (or `ubuntu-22.04`). Amazon Linux 2023 is not supported in v1.

**Alt key / clicks "get messed up" over time (full XFCE)**
A stuck modifier. Web-VNC clients lose the modifier *key-up* event when focus
leaves the canvas (Cmd-Tab, clicking the KasmVNC toolbar, a browser shortcut) —
especially on macOS, where Option maps to X11 `Alt`. The remote X server then
latches Alt as "held", and xfwm4's default `Alt+click` = move/resize window turns
every click into a window drag and every keystroke into an Alt-shortcut. It's
fine at first and degrades as the latch accumulates.
- **Immediate fix:** open the KasmVNC toolbar (tab on the left edge) and tap the
  **Alt** (and Ctrl/Shift) key buttons to release the latch; or tap Option once
  in the desktop.
- **If the session is wedged:** `km desktop restart <id>` force-restarts Xvnc +
  the WM/browser (like logging out of XFCE and back in) — clears any latched
  state. Drops the connected session; reconnect with `km desktop start`.
- **Durable fix (shipped):** `full` mode pre-seeds xfwm4 with `easy_click=none`,
  so a latched Alt can no longer hijack the pointer. Sandboxes created before this
  build need `km destroy && km create` to pick it up.
- **Browser-only use:** prefer `mode: kiosk` — matchbox has no `Alt+click`
  window bindings, so a stuck modifier is nearly harmless.

Note: the `SSL alert number 46 / certificate unknown` lines in the KasmVNC log are
just the browser rejecting the self-signed cert (untrusted-CA warning) — cosmetic,
unrelated to input.

---

## Limitations

- **Ubuntu 24.04 / 22.04 only (v1).** Amazon Linux 2023 support (KasmVNC RHEL-family build) is deferred.
- **No `km desktop stop` command.** Ctrl-C ends the foreground tunnel; KasmVNC keeps running. Scheduled stops compose with `km at`.
- **Existing sandboxes need reprovisioning.** Sandboxes created without `desktop.enabled: true` do NOT get KasmVNC installed retroactively. `km destroy` + `km create` to re-provision.
- **GNOME / KDE deferred.** XFCE4 is the only supported full-desktop environment.
- **Audio deferred.** KasmVNC has audio support; it is not wired up in v1.
- **Multi-monitor deferred.** Single display only in v1.
- **Per-machine credential (cross-machine gap).** `~/.km/desktop/<id>` is written on the creation machine only. To connect from a different laptop, `km shell <id>` into the sandbox, `cat ~/.kasmpasswd`, and use it to log in manually (note: `km shell` is interactive — it does not accept a trailing `-- <cmd>`).

---

See also:
- `docs/superpowers/specs/2026-06-02-km-desktop-remote-browser-design.md` — full design spec
- `skills/desktop/SKILL.md` — `klanker:desktop` skill quick reference
- `profiles/desktop.yaml` — minimal kiosk-Firefox example profile
- `CLAUDE.md` — Where to look table
