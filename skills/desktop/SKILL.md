---
name: desktop
description: Launch a KasmVNC graphical browser session (kiosk or full XFCE) inside a sandbox EC2 and tunnel it to the operator's local browser over SSM port-forward
---

# km desktop — Remote Browser Session over SSM

Operator-side workflow for opening a graphical session inside a sandbox EC2 and viewing it in your local browser. `km` generates a per-sandbox KasmVNC credential at create time and port-forwards the session over an encrypted SSM tunnel. The operator never exposes any port publicly — the only ingress is `AWS-StartPortForwardingSession`.

**Audience:** the operator running `km` on their workstation. The sandbox must have `spec.runtime.desktop.enabled: true` and `spec.runtime.ami: ubuntu-24.04` (or `ubuntu-22.04`) in its profile.

## Cross-references

- `klanker:init` — run `make build && km init --sidecars` once before desktop support works for new sandboxes
- `klanker:user` — `km create`, `km list`, `km destroy` lifecycle

## Profile field

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
| `desktop.enabled` | bool* | **false** | Provision KasmVNC + DE at sandbox boot. Must be set true explicitly. |
| `desktop.mode` | string | `kiosk` | `kiosk` = matchbox-wm + `browsers[0]` maximized. `full` = XFCE4 desktop. |
| `desktop.browsers` | []string | `[firefox]` | Browsers to install. `chrome` = Google Chrome; `chromium` = open-source build. |
| `desktop.geometry` | string | `1920x1080` | VNC display geometry (`WIDTHxHEIGHT`). |

`bool*` = pointer-bool with profile-inheritance semantics; omit to keep default.

**Ubuntu-only (v1):** `km validate` hard-errors when `desktop.enabled: true` and the profile's AMI is not an Ubuntu slug/family. Amazon Linux 2023 support is deferred.

## Operator state

| Path | Purpose |
|---|---|
| `~/.km/desktop/<sandbox-id>` | Per-sandbox KasmVNC credential (`user:password`); generated at `km create`, never baked |

The credential file is written by `km create` when `desktop.enabled: true` and read by `km desktop start` to print the login URL. It is deleted by `km destroy`.

## One-time setup

After enabling `spec.runtime.desktop` in a profile for the first time, refresh the management Lambdas so the new schema fields are understood remotely:

```bash
make build
km init --sidecars
```

Without this step, `km create --remote` against a desktop-enabled profile will fail to thread `DesktopKasmCredential` into userdata.

## Per-sandbox workflow

```bash
# 1. Create — KasmVNC credential is generated locally and threaded into userdata
km create profiles/desktop.yaml --alias my-desktop

# 2. Resolve the sandbox ID
SB=$(km list | awk '/my-desktop/ {print $1}')

# 3. Open the SSM tunnel (blocking — Ctrl-C to close the tunnel; session keeps running)
km desktop start $SB
# Prints: https://localhost:8444/   user: sandbox   password: <random>
# Open that URL in your local browser while the tunnel is active.

# 4. (Optional) check KasmVNC unit state
km desktop status $SB

# 5. Teardown also removes the local credential file
km destroy $SB --remote --yes
```

`km desktop start` and `km desktop status` accept the same identifier formats as other `km` subcommands: full sandbox ID (`desk-ee9499b5`), alias (`my-desktop`), or row number from `km list`.

`--local-port <N>` overrides the default 8444 if it is already in use.

## Modes

| Mode | What launches | Window manager |
|---|---|---|
| `kiosk` (default) | `browsers[0]` maximized, full screen | `matchbox-window-manager` (lightweight, kiosk-oriented) |
| `full` | `exec startxfce4` (XFCE4 desktop environment) | XFCE4 |

In **kiosk mode** the browser is the entire session — tabs, URL bar, and developer tools are all available; only the WM chrome is stripped. Pick `full` when you need a file manager, terminal, or multiple applications simultaneously.

## Clipboard

KasmVNC includes seamless bidirectional clipboard. Text copied in the remote browser is available in the operator's local clipboard and vice versa via the KasmVNC toolbar in the browser UI (the small tab on the left edge of the window).

## Security model

- KasmVNC binds `127.0.0.1` on the sandbox — no LAN/VPC exposure.
- The only access path is the operator's SSM `AWS-StartPortForwardingSession` (authenticated, encrypted) — same as `km vscode`.
- SSL is disabled at the KasmVNC layer because the SSM tunnel already encrypts the transport and the loopback-only bind removes the network exposure. This avoids a self-signed-cert browser warning.
- The per-sandbox credential is defense-in-depth so another local process on the operator's machine cannot ride the forwarded port.

## First-boot network caveat

A fresh non-AMI boot must reach the following endpoints to install the desktop stack during userdata:

- Ubuntu package mirrors (`archive.ubuntu.com`, `security.ubuntu.com`)
- KasmVNC release tarball (`github.com`, `objects.githubusercontent.com`)
- Firefox PPA (`ppa.launchpad.net`, `keyserver.ubuntu.com`) for `browsers: [firefox]`
- Google APT repo (`dl.google.com`) for `browsers: [chrome]`
- Brave APT repo (`brave-browser-apt-release.s3.brave.com`) for `browsers: [brave]`

Under a locked-down `spec.network`, either allowlist those domains for first boot, or bake the AMI first:

```bash
# 1. Create under an open profile (allowedDNSSuffixes: "*") to let packages install
km create profiles/desktop.yaml --alias bake-session

# 2. Wait for boot, then bake
km ami bake <sandbox-id>

# 3. Destroy the bake sandbox
km destroy <sandbox-id> --remote --yes

# 4. In your production profile, set the baked AMI ID:
#    spec.runtime.ami: ami-xxxxxxxxxxxxxxxxx
#    Now packages are pre-installed; first-boot is fast even under a locked spec.network.
km create profiles/my-locked-desktop.yaml
```

## km resume behavior

The KasmVNC systemd unit is enabled for every boot. `km resume` restores the session — run `km desktop start` again after resuming to re-open the tunnel.

## Operator notes

- **Existing sandboxes** provisioned without `desktop.enabled: true` do NOT get KasmVNC retroactively. `km destroy && km create` to re-provision.
- **AMI bake recommended** for routine use — the first boot installs KasmVNC + WM + browsers, which is slow. Baking an AMI skips all package installation on subsequent boots.
- **`km destroy` cleans up** the local `~/.km/desktop/<id>` credential file. Manual cleanup is only needed when a sandbox is wiped out-of-band.

See `docs/desktop.md` for the full operator runbook and troubleshooting matrix.
