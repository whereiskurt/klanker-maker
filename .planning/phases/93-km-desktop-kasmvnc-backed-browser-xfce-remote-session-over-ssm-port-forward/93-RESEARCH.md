# Phase 93: km desktop — KasmVNC-backed browser/XFCE remote session - Research

**Researched:** 2026-06-02
**Domain:** KasmVNC provisioning, SSM port-forward CLI mirroring `km vscode`, profile schema extension
**Confidence:** HIGH (code patterns verified by direct file reads; KasmVNC external facts verified against GitHub API + official defaults YAML)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- **Engine:** KasmVNC (web-native VNC, built-in HTML5 client + seamless clipboard). Not Apache Guacamole, not TigerVNC+noVNC.
- **Modes:** `spec.runtime.desktop.mode: kiosk | full` (default `kiosk`). GNOME/KDE deferred.
- **Profile schema:** `RuntimeDesktopSpec` in `pkg/profile/types.go` as sibling to `RuntimeVSCodeSpec`. Fields: `enabled *bool` (default FALSE when block absent), `mode string` (kiosk|full, default kiosk), `browsers []string` (subset of {firefox, chromium, brave}, default [firefox]), `geometry string` (optional, default 1920x1080). Helper `IsDesktopEnabled(*RuntimeDesktopSpec) bool` defaults **false** (opposite of vscode).
- **Validation:** `mode` enum, `browsers` membership, browsers non-empty for kiosk, geometry regex, Ubuntu-only guard (hard ERROR when enabled and non-Ubuntu AMI).
- **Provisioning:** idempotent userdata gated by `{{- if .DesktopEnabled }}`. Installs if absent, skips if baked. Always reseeds credential+config.
- **Credential:** per-sandbox KasmVNC username+password at `~/.km/desktop/<sandbox-id>`. Generated at `km create`. Threaded as `DesktopKasmCredential` in compiler. Never baked.
- **CLI:** `km desktop start <id> [--local-port 8444]` + `km desktop status <id>`. New file `internal/app/cmd/desktop.go`. Mirrors `vscode.go` exactly. Registered next to `NewVSCodeCmd`.
- **Security:** KasmVNC + session bind 127.0.0.1 only. SSL disabled (loopback + SSM tunnel justification). Per-sandbox credential is defense-in-depth.
- **Target distro:** Ubuntu 24.04 / 22.04 only in v1.
- **Deliverables:** `profiles/desktop.yaml` (kiosk-Firefox), added to `scripts/validate-all-profiles.sh`. `klanker:desktop` skill. Bump `plugin.json` + `marketplace.json`. `docs/desktop.md`. `CLAUDE.md` row. `OPERATOR-GUIDE.md` section.
- **Rollout:** `make build && km init --sidecars`. Existing sandboxes: `km destroy && km create`.

### Claude's Discretion
- Exact KasmVNC release version + `.deb` fetch URL/checksum.
- systemd unit form (user service vs system service) for KasmVNC, matching sandbox-user model and surviving `km resume`.
- Exact `kasmvnc.yaml` keys to disable SSL / enable clipboard / set geometry.
- matchbox-window-manager vs comparable minimal kiosk WM if matchbox unavailable on Ubuntu 24.04.
- Wave breakdown and plan granularity.
- Whether non-Ubuntu-AMI guard is WARN or hard ERROR.

### Deferred Ideas (OUT OF SCOPE)
- GNOME / KDE full-desktop modes.
- Amazon Linux 2023 support.
- Audio, multi-monitor, session recording.
- Web-based file transfer as a headline feature.
- Dedicated single `desktop.browser` field for kiosk.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| DSK-01-SCHEMA | Add `RuntimeDesktopSpec` + `IsDesktopEnabled` to `pkg/profile/types.go`; update JSON schema + `schema_export.go` | `RuntimeVSCodeSpec` at line 176 is the exact sibling pattern; JSON schema vscode block at line 273 is the template |
| DSK-02-HELPER | `IsDesktopEnabled(*RuntimeDesktopSpec) bool` defaults false | `IsVSCodeEnabled` at types.go:619 is the mirror; just invert the nil-pointer default |
| DSK-03-VALIDATE | `km validate` semantic rules: mode enum, browsers membership, browsers non-empty for kiosk, geometry regex, Ubuntu-only ERROR guard | `ValidateSemantic` in validate.go; `IsWarning bool` field on `ValidationError` already exists; AMI slug check via `strings.HasPrefix(p.Spec.Runtime.AMI, "ubuntu")` for slug path or `isRawAMIID` for raw IDs |
| DSK-04-SCHEMA-EXPORT | JSON schema update + `schema_export.go` re-export | `pkg/profile/schemas/sandbox_profile.schema.json` — add `desktop` block under `runtime.properties` mirroring the `vscode` block (line 273); `schema_export.go` is a one-line accessor with no code change needed |
| DSK-05-COMPILER-THREAD | Thread `DesktopEnabled`, `DesktopMode`, `DesktopBrowsers`, `DesktopGeometry`, `DesktopKasmCredential` through `NetworkConfig` and `userDataParams` | `NetworkConfig` struct in `service_hcl.go:683`; `userDataParams` struct in `userdata.go:3620`; VSCode fields at lines 3636-3642 are the exact pattern to mirror |
| DSK-06-USERDATA-INSTALL | Idempotent KasmVNC install block in `pkg/compiler/userdata.go` gated by `{{- if .DesktopEnabled }}` | VSCode block at userdata.go:2484-2497 is the template; km-queue.service systemd unit at lines 2464-2477 is the unit pattern |
| DSK-07-USERDATA-SESSION | xstartup for kiosk (matchbox + browser) and full (exec startxfce4); kasmvnc.yaml seed; systemd unit | KasmVNC defaults.yaml verified; matchbox-window-manager confirmed available Ubuntu 24.04 noble universe |
| DSK-08-CREDENTIAL | Generate username+password at `km create`, store at `~/.km/desktop/<sandbox-id>`, thread as `DesktopKasmCredential` | Pattern from create.go:616-638 (VSCode keypair); replace `sshkey.GenerateAndWrite` with `crypto/rand` password generation; store as `user:password` two-field file |
| DSK-09-CLI-START | `km desktop start <id> [--local-port 8444]` in new `desktop.go` | `runVSCodeStart` in vscode.go:125-194 is the complete template; replace SSH bits with credential read + URL print |
| DSK-10-CLI-STATUS | `km desktop status <id>` one-round-trip SSM probe | `runVSCodeStatus` in vscode.go:198-217 is the template; `vsCodeStatusScript` at line 32 is the pattern for `desktopStatusScript` |
| DSK-11-SECURITY | KasmVNC binds 127.0.0.1 only; SSL disabled; per-sandbox credential | Confirmed via `network.interface` in kasmvnc.yaml and `-localhost` flag; SSL disabled = `network.ssl.require_ssl: false` (default is true in the defaults YAML) |
| DSK-12-PROFILE-EXAMPLE | `profiles/desktop.yaml` kiosk-Firefox example; add to `scripts/validate-all-profiles.sh` | scripts/validate-all-profiles.sh pattern confirmed; inventory is a plain bash array |
| DSK-13-SKILL | `skills/desktop/SKILL.md`; bump `plugin.json` + `marketplace.json` | Plugin at `.claude-plugin/plugin.json` (version "0.3.0"); skill directory pattern from `skills/vscode/SKILL.md` |
| DSK-14-DOCS | `docs/desktop.md`, `CLAUDE.md` row, `OPERATOR-GUIDE.md` section | Standard project doc pattern |
| DSK-15-TESTS | `desktop_test.go` + `userdata_test.go` desktop suite + `validate_test.go` additions | `vscode_test.go` (945 lines) and `TestUserDataVSCode*` in `userdata_test.go:1909-1987` are exact templates |
</phase_requirements>

---

## Summary

Phase 93 adds `km desktop` — a KasmVNC-backed remote graphical session that the operator views in their local browser over an SSM port-forward. The feature is a deliberate structural mirror of `km vscode` (Phase 73) and its entire implementation derives from that precedent: same DI pattern, same credential lifecycle, same SSM pre-flight check shape, same userdata gate. The primary new complexity is KasmVNC-specific: multi-step idempotent install (packages + credential + config + systemd unit), two xstartup modes (kiosk vs XFCE), browser package install with Ubuntu-specific snap-avoidance, and the non-interactive credential seeding via `kasmvncpasswd`.

Research confirms: KasmVNC 1.4.0 is the current stable release with explicit Ubuntu 24.04 (noble) and 22.04 (jammy) `.deb` packages on GitHub releases. The default `websocket_port: auto` resolves to `displayNumber + 8443`, so display `:1` → port `8444` — the design's chosen default. SSL enforcement is controlled by `network.ssl.require_ssl` (defaults `true` in the shipped YAML, must be set `false`). Loopback-only binding is `network.interface: 127.0.0.1`. Non-interactive password setup uses `echo -e "pass\npass\n" | kasmvncpasswd -u <user> -w -r ~/.kasmpasswd`. `matchbox-window-manager` is confirmed available in Ubuntu 24.04 noble universe (version 1.2.2+git20200512-1build1). Firefox in Ubuntu 24.04 is snap-by-default and requires the Mozilla Team PPA for a DEB install inside VNC. Chromium requires the xtradeb PPA (also snap-by-default in Ubuntu 24.04). Brave uses its official APT repo.

**Primary recommendation:** Mirror `km vscode` exactly file-by-file; the only genuinely new engineering is the KasmVNC userdata block and its credential seeding. Implement in five waves: schema/validate → compiler/userdata → CLI/create-credential → profile example/docs/skill → test suite completion.

---

## Standard Stack

### Core
| Library/Tool | Version | Purpose | Why Standard |
|---|---|---|---|
| KasmVNC | 1.4.0 | Web-native VNC server with HTML5 client + bidirectional clipboard | Only single-component solution with proper clipboard; official Ubuntu noble + jammy debs |
| matchbox-window-manager | 1.2.2+git20200512-1build1 | Minimal kiosk WM for single-app sessions | Available Ubuntu 24.04 noble universe; purpose-built for kiosk (fullscreen, no decorations) |
| xfce4 + xfce4-goodies | distro latest | Full desktop DE for `mode: full` | Lightest full DE; verified pattern in KasmVNC docs |
| Firefox (Mozilla PPA DEB) | latest | Default kiosk browser | Non-snap, runs cleanly under VNC; snap-confined Firefox breaks in VNC sessions |
| Chromium (xtradeb PPA) | latest | Optional kiosk/full browser | Non-snap DEB path; Ubuntu 24.04 snap default requires PPA override |
| Brave | latest from Brave APT repo | Optional kiosk/full browser | Official Brave APT repo for Debian/Ubuntu |
| dbus-x11 | distro latest | D-Bus session for desktop environments | Required to prevent grey/black screen in all VNC DE sessions |
| fonts-dejavu, fonts-liberation | distro latest | Font packages | Prevents font-missing grey screen in kiosk mode |

### Supporting
| Tool | Purpose | When to Use |
|---|---|---|
| `pkg/sshkey` (existing) | keypair generation | VSCode credential path; desktop uses `crypto/rand` instead |
| `kasmvncpasswd` | Non-interactive KasmVNC credential seeding | At boot, via userdata; `-u <user> -w -r ~/.kasmpasswd` |
| `systemd --user` vs system service | KasmVNC process management | Use system service (root-launched, `su -s /bin/bash - sandbox` in ExecStart) to survive `km resume` without user session |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|---|---|---|
| KasmVNC | TigerVNC + noVNC | Three-component stack; clipboard is one-directional sidebar paste |
| KasmVNC | Apache Guacamole | Gateway/auth value duplicated by SSM; heavyweight Java server |
| matchbox-wm | openbox | Also available on Ubuntu 24.04; more config surface but heavier; matchbox is purpose-built kiosk |
| Mozilla PPA DEB firefox | snap firefox | Snap isolation breaks under VNC (no dbus session, file system namespacing); snap-confined Firefox is a known VNC failure mode |

**Installation (in userdata):**
```bash
# Detect Ubuntu version for package name
UBUNTU_CODENAME=$(lsb_release -sc 2>/dev/null || . /etc/os-release && echo $VERSION_CODENAME)
KASMVNC_DEB="kasmvncserver_${UBUNTU_CODENAME}_1.4.0_amd64.deb"
KASMVNC_URL="https://github.com/kasmtech/KasmVNC/releases/download/v1.4.0/${KASMVNC_DEB}"

apt-get install -y dbus-x11 fonts-dejavu fonts-liberation
wget -q -O /tmp/${KASMVNC_DEB} "${KASMVNC_URL}"
apt-get install -y /tmp/${KASMVNC_DEB}
rm -f /tmp/${KASMVNC_DEB}

# kiosk: matchbox-window-manager
apt-get install -y matchbox-window-manager

# full: xfce4
apt-get install -y xfce4 xfce4-goodies

# Firefox (Mozilla PPA DEB, non-snap)
add-apt-repository -y ppa:mozillateam/ppa
apt-get update -q
apt-get install -y -t 'o=LP-PPA-mozillateam' firefox

# Chromium (xtradeb PPA DEB, non-snap)
add-apt-repository -y ppa:xtradeb/apps
apt-get update -q
apt-get install -y -t 'o=LP-PPA-xtradeb' chromium

# Google Chrome (official Google APT repo; always a DEB, never a snap)
curl -fsSL https://dl.google.com/linux/linux_signing_key.pub \
  | gpg --dearmor -o /usr/share/keyrings/google-chrome.gpg
echo "deb [arch=amd64 signed-by=/usr/share/keyrings/google-chrome.gpg] \
  http://dl.google.com/linux/chrome/deb/ stable main" \
  > /etc/apt/sources.list.d/google-chrome.list
apt-get update -q && apt-get install -y google-chrome-stable

# Brave (official APT repo)
curl -fsSL https://brave-browser-apt-release.s3.brave.com/brave-browser-archive-keyring.gpg \
  | gpg --dearmor -o /usr/share/keyrings/brave-browser-archive-keyring.gpg
echo "deb [arch=amd64 signed-by=/usr/share/keyrings/brave-browser-archive-keyring.gpg] \
  https://brave-browser-apt-release.s3.brave.com/ stable main" \
  > /etc/apt/sources.list.d/brave-browser-release.list
apt-get update -q && apt-get install -y brave-browser
```

**Browser keyword → launch binary mapping** (the profile `browsers` keyword is
NOT always the executable name — the compiler must map keyword→binary for the
kiosk `browsers[0]` xstartup launch and for any per-browser install switch):

| `browsers` keyword | apt package | launch binary (kiosk xstartup) |
|--------------------|-------------|--------------------------------|
| `firefox` | `firefox` (Mozilla PPA) | `firefox` |
| `chromium` | `chromium` (xtradeb PPA) | `chromium` |
| `chrome` | `google-chrome-stable` (Google APT) | `google-chrome-stable` |
| `brave` | `brave-browser` (Brave APT) | `brave-browser` |

Note `chrome` ≠ `chromium`: the keyword `chrome` installs **Google Chrome**
(proprietary, with Widevine DRM + proprietary codecs) and `chromium` installs
the open-source build. Both are first-class enum members; an operator may select
either or both. The kiosk launch uses the mapped binary, not the raw keyword.

---

## Architecture Patterns

### Recommended Project Structure
```
internal/app/cmd/
├── desktop.go            # new — mirrors vscode.go exactly
├── desktop_test.go       # new — mirrors vscode_test.go
pkg/profile/
├── types.go              # add RuntimeDesktopSpec, IsDesktopEnabled
├── validate.go           # add desktop semantic rules
├── schemas/sandbox_profile.schema.json  # add desktop block
pkg/compiler/
├── service_hcl.go        # add DesktopKasmCredential to NetworkConfig
├── userdata.go           # add DesktopEnabled+friends to params; add template block
├── userdata_test.go      # add TestUserDataDesktop* suite
profiles/
├── desktop.yaml          # new kiosk-Firefox example
skills/
├── desktop/
│   └── SKILL.md          # new klanker:desktop skill
docs/
├── desktop.md            # new runbook
```

### Pattern 1: RuntimeDesktopSpec in types.go (sibling to RuntimeVSCodeSpec)
**What:** New struct + helper + RuntimeSpec field, exact mirror of vscode
**When to use:** Always — this is the locked design
```go
// Source: pkg/profile/types.go (mirror of RuntimeVSCodeSpec at line 176)
type RuntimeDesktopSpec struct {
    Enabled   *bool    `json:"enabled,omitempty" yaml:"enabled,omitempty"`
    Mode      string   `json:"mode,omitempty" yaml:"mode,omitempty"`
    Browsers  []string `json:"browsers,omitempty" yaml:"browsers,omitempty"`
    Geometry  string   `json:"geometry,omitempty" yaml:"geometry,omitempty"`
}

// IsDesktopEnabled returns false when block absent or Enabled nil — OPT-IN (heavy install).
// Deliberate opposite of IsVSCodeEnabled (which defaults true).
func IsDesktopEnabled(desktop *RuntimeDesktopSpec) bool {
    if desktop == nil || desktop.Enabled == nil {
        return false
    }
    return *desktop.Enabled
}
```

Add to `RuntimeSpec`:
```go
// Desktop gates KasmVNC graphical session provisioning. nil = disabled (opt-in; heavy install).
Desktop *RuntimeDesktopSpec `yaml:"desktop,omitempty" json:"desktop,omitempty"`
```

### Pattern 2: kasmvnc.yaml configuration (verified from kasmtech/KasmVNC defaults)
**What:** Per-sandbox config seeded at every boot (never baked)
**When to use:** Always in the userdata desktop block
```yaml
# Source: https://raw.githubusercontent.com/kasmtech/KasmVNC/master/unix/kasmvnc_defaults.yaml
desktop:
  resolution:
    width: 1920     # from DesktopGeometry parsed
    height: 1080
network:
  interface: 127.0.0.1   # loopback-only binding (security gate)
  websocket_port: auto    # auto = displayNumber + 8443; display :1 → port 8444
  ssl:
    require_ssl: false    # disable SSL (acceptable: loopback + SSM tunnel)
data_loss_prevention:
  clipboard:
    server_to_client:
      enabled: true       # sandbox → operator clipboard
    client_to_server:
      enabled: true       # operator → sandbox clipboard
```

Note: `require_ssl: true` is the shipped default. Must explicitly set `false`.

### Pattern 3: xstartup for kiosk mode
**What:** `~/.vnc/xstartup` content for matchbox + browser maximized
**When to use:** When `DesktopMode == "kiosk"`
```bash
#!/bin/bash
unset SESSION_MANAGER
unset DBUS_SESSION_BUS_ADDRESS
export XDG_RUNTIME_DIR=/run/user/$(id -u sandbox)
mkdir -p "${XDG_RUNTIME_DIR}"
chown sandbox:sandbox "${XDG_RUNTIME_DIR}"
chmod 700 "${XDG_RUNTIME_DIR}"
exec dbus-launch --exit-with-session matchbox-window-manager -use_titlebar no &
WM_PID=$!
sleep 1
# Launch browsers[0] maximized (firefox/chromium/brave-browser)
{{ .DesktopBrowser0 }} &
wait $WM_PID
```

### Pattern 4: xstartup for full XFCE mode
**What:** `~/.vnc/xstartup` content for full XFCE desktop
**When to use:** When `DesktopMode == "full"`
```bash
#!/bin/bash
unset SESSION_MANAGER
unset DBUS_SESSION_BUS_ADDRESS
export XDG_RUNTIME_DIR=/run/user/$(id -u sandbox)
mkdir -p "${XDG_RUNTIME_DIR}"
chown sandbox:sandbox "${XDG_RUNTIME_DIR}"
chmod 700 "${XDG_RUNTIME_DIR}"
exec dbus-launch --exit-with-session startxfce4
```

### Pattern 5: KasmVNC systemd unit (system service, not user service)
**What:** `/etc/systemd/system/kasmvnc.service` — starts on boot, survives `km resume`
**Why system vs user:** The sandbox user session doesn't exist as a loginctl session at boot (no PAM login); `systemd --user` requires a user session. System service with `User=sandbox` works identically to the `km-queue.service` pattern.
```ini
# Source: mirrors /etc/systemd/system/km-queue.service pattern from userdata.go:2464
[Unit]
Description=KasmVNC desktop session for km desktop
After=network.target km-bootstrap.service
[Service]
User=sandbox
Environment=DISPLAY=:1
ExecStartPre=/bin/mkdir -p /run/user/1001
ExecStartPre=/bin/chown sandbox:sandbox /run/user/1001
ExecStart=/usr/bin/vncserver :1 -fg -noxstartup
ExecStop=/usr/bin/vncserver -kill :1
Restart=on-failure
RestartSec=5
[Install]
WantedBy=multi-user.target
```

Note: `vncserver :1 -fg -noxstartup` runs the server in foreground (systemd tracks PID) without re-running xstartup on every systemd restart. The `xstartup` script is sourced once per display start.

### Pattern 6: Non-interactive kasmvncpasswd credential seeding
**What:** Sets KasmVNC username+password without interactive prompt
**When to use:** Always in userdata desktop block (credential seeded fresh at every boot)
```bash
# Source: verified from KasmVNC issue #273 + vncpasswd(1) man page
# DesktopKasmUser and DesktopKasmPass are template variables from NetworkConfig
mkdir -p /home/sandbox/.vnc
printf '%s\n%s\n' '{{ .DesktopKasmPass }}' '{{ .DesktopKasmPass }}' \
  | kasmvncpasswd -u '{{ .DesktopKasmUser }}' -w -r /home/sandbox/.kasmpasswd
chmod 600 /home/sandbox/.kasmpasswd
chown sandbox:sandbox /home/sandbox/.kasmpasswd
```

### Pattern 7: Credential generation + storage at km create (mirrors VSCode keypair)
**What:** Generate random password, store at `~/.km/desktop/<id>`, thread to compiler
**When to use:** At `km create` when `IsDesktopEnabled`
```go
// Source: mirrors create.go:616-638 (VSCode keypair)
// File: internal/app/cmd/create.go  (two call sites, same as vscode)
if profile.IsDesktopEnabled(resolvedProfile.Spec.Runtime.Desktop) {
    homeDir, err := os.UserHomeDir()
    if err != nil { return fmt.Errorf("locate home dir for desktop credential: %w", err) }
    desktopDir := filepath.Join(homeDir, ".km", "desktop")
    if err := os.MkdirAll(desktopDir, 0o700); err != nil {
        return fmt.Errorf("create desktop dir: %w", err)
    }
    credPath := filepath.Join(desktopDir, sandboxID)
    user := "kasm"
    pass := randomPassword(16) // crypto/rand base62
    if err := os.WriteFile(credPath, []byte(user+":"+pass), 0o600); err != nil {
        return fmt.Errorf("write desktop credential: %w", err)
    }
    network.DesktopKasmUser = user
    network.DesktopKasmPass = pass
    fmt.Fprintf(os.Stderr, "  + Desktop credential written to %s\n", credPath)
}
```

### Pattern 8: CLI desktop.go — resolveDesktopDeps + runDesktopStart
**What:** Mirrors vscode.go exactly; replaces SSH concepts with credential read + URL print
**Key differences from vscode.go:**
- No `ssh-config` upsert (KasmVNC uses a browser, not SSH)
- No private key file check (credential file `~/.km/desktop/<id>` instead of `~/.km/keys/<id>`)
- Print `http://localhost:<port>/` (not SSH instruction)
- Print `user: kasm  pass: <password>` from credential file
- Remote port is `8444` (not `22`)
- `desktopStatusScript` checks KasmVNC systemd unit (`systemctl is-active kasmvnc`)
- `parseDesktopStatus` returns descriptive errors per failure mode

```go
// Source: mirrors vscode.go:32-37 (vsCodeStatusScript pattern)
const desktopStatusScript = `echo "=== kasmvnc ==="
systemctl is-active kasmvnc 2>&1 || true
echo "=== kasmpasswd ==="
test -f /home/sandbox/.kasmpasswd && echo yes || echo no`
```

### Anti-Patterns to Avoid
- **Baking the credential into an AMI**: The credential at `~/.kasmpasswd` must be seeded fresh at every boot from `DesktopKasmCredential`. If baked, all sandboxes launched from that AMI share a credential.
- **Omitting the Ubuntu-only guard at validate time**: KasmVNC does not ship RHEL/Fedora packages. Let `km validate` catch it before `km create` so the operator gets a clear error, not a mid-boot failure.
- **Using `systemd --user` for the KasmVNC service**: The sandbox user has no loginctl session at boot. Use system service with `User=sandbox`.
- **`chromium-browser` apt package**: On Ubuntu 24.04, this installs a snap transition package. Use the xtradeb PPA.
- **`firefox` apt package without PPA**: On Ubuntu 24.04, the default `firefox` apt package installs the snap version. Snap-confined Firefox breaks under VNC (no dbus session, restricted file system). Always use the Mozilla Team PPA DEB.
- **`vncserver :1` without `-fg`**: Without foreground mode, systemd cannot track the PID and the service will appear to exit immediately.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---|---|---|---|
| VNC server with web client | Custom websockify+noVNC pipeline | KasmVNC 1.4.0 | Single binary; built-in HTML5 client; **seamless bidirectional clipboard** |
| Port-forward to sandbox | Custom SSH/WebSocket proxy | `buildPortForwardCmd` + `sendSSMAndWait` | Already in cmd package; reused by vscode.go |
| Credential storage | Custom encryption | `crypto/rand` + plain file at `~/.km/desktop/<id>` | Threat model: operator controls `~/.km`; credential is defense-in-depth for local port only |
| DI for SSM + fetcher | Inline AWS setup | `resolveVSCodeDeps` pattern → `resolveDesktopDeps` | Already tested; mock-friendly |

---

## Common Pitfalls

### Pitfall 1: KasmVNC default port is 6800 via `-websocketPort`, but `websocket_port: auto` gives 8444 for display :1
**What goes wrong:** If you set `websocket_port: 8444` explicitly in kasmvnc.yaml, it works. But if you leave it as `auto` and start on display `:1`, the port is `1 + 8443 = 8444`. If you start on `:2`, it's `8445`. Always start on `:1`.
**How to avoid:** Set `websocket_port: auto` and always use `vncserver :1`. Document that `--local-port 8444` is the default because `auto` on `:1` = `8444`.
**Confidence:** HIGH (verified from `kasmvnc_defaults.yaml` raw file fetch; deepwiki cross-reference)

### Pitfall 2: `network.ssl.require_ssl` defaults TRUE in the shipped kasmvnc.yaml
**What goes wrong:** KasmVNC ships with `require_ssl: true` by default. If you write a kasmvnc.yaml that omits the ssl block, the operator gets a browser TLS warning. The CONTEXT.md says "SSL disabled at KasmVNC layer is acceptable because of loopback bind + SSM tunnel."
**How to avoid:** Explicitly set `network.ssl.require_ssl: false` in the seeded kasmvnc.yaml. Confirmed from the raw defaults file: `require_ssl: true` is the shipped default.
**Confidence:** HIGH (verified from raw `kasmvnc_defaults.yaml`)

### Pitfall 3: Snap-confined Firefox/Chromium breaks under VNC
**What goes wrong:** Ubuntu 24.04 installs Firefox and Chromium as snaps by default. Snap-confined apps use strict filesystem namespacing and require a PAM user session with a running snap daemon — neither of which exists in a VNC session launched from a systemd service. The browser simply won't start, with errors about dbus or snap confinement.
**How to avoid:**
- Firefox: add Mozilla Team PPA (`ppa:mozillateam/ppa`) and pin to install the DEB, not the snap.
- Chromium: add xtradeb PPA (`ppa:xtradeb/apps`) for native DEB.
- Brave: official Brave APT repo always ships DEB.
**Warning signs:** Browser starts then immediately exits; VNC session shows empty screen; `journalctl -u kasmvnc` shows `snap-confine` or `dbus` errors.
**Confidence:** HIGH (multiple sources; established pattern in VNC Docker images)

### Pitfall 4: Grey/black screen — missing dbus or XDG_RUNTIME_DIR
**What goes wrong:** XFCE and kiosk browsers require a D-Bus session. When launched from a system service without a loginctl session, `$XDG_RUNTIME_DIR` is not set and `$DBUS_SESSION_BUS_ADDRESS` may point to a stale/wrong socket. Result: grey screen or browser crash.
**How to avoid:** In xstartup:
1. `unset SESSION_MANAGER` and `unset DBUS_SESSION_BUS_ADDRESS`
2. `export XDG_RUNTIME_DIR=/run/user/$(id -u sandbox)` + `mkdir -p` + chown + chmod 700
3. Launch everything via `dbus-launch --exit-with-session <command>`
4. Install `dbus-x11` package
**Confirmed from:** KasmVNC issue #264, KasmVNC FAQ (GNOME dbus issue), Coder help article (blocked but title confirmed)
**Confidence:** HIGH (multiple GitHub issues + KasmVNC FAQ confirm this pattern)

### Pitfall 5: Non-interactive kasmvncpasswd — password must be piped TWICE (confirm prompt)
**What goes wrong:** `kasmvncpasswd` prompts for password and confirmation. Piping just the password once fails. Must pipe `password\npassword\n` (password + newline + same password + newline).
**How to avoid:** `printf '%s\n%s\n' "$PASS" "$PASS" | kasmvncpasswd -u "$USER" -w -r ~/.kasmpasswd`
**Confidence:** MEDIUM (GitHub issue #273 shows the pattern; confirmed from vncpasswd man page about confirmation prompt behavior)

### Pitfall 6: Ubuntu-only guard — raw AMI IDs are opaque at validate time
**What goes wrong:** `km validate` can inspect `spec.runtime.ami` slugs (`amazon-linux-2023`, `ubuntu-24.04`, `ubuntu-22.04`) but raw AMI IDs (`ami-xxxxxxxx`) are region-specific lookups unavailable at validate time.
**How to avoid:** The Ubuntu guard logic at validate time:
```go
ami := p.Spec.Runtime.AMI
isUbuntu := strings.HasPrefix(ami, "ubuntu-")
isRawID  := compiler.IsRawAMIID(ami)  // ami-[0-9a-f]{8,17}
// For raw IDs we cannot know at validate time — emit a WARN (not ERROR)
// For slugs we can know — emit ERROR for non-ubuntu slugs
```
The CONTEXT.md says "lean ERROR when desktop enabled." Research recommendation: ERROR for known non-Ubuntu slugs (e.g., `amazon-linux-2023`); WARN for raw AMI IDs since we can't inspect them without an AWS call.
**Confidence:** HIGH (validate.go code examined; `isRawAMIID` function confirmed in service_hcl.go)

### Pitfall 7: Credential file from Lambda path (remote create) needs env var pass-through
**What goes wrong:** At `km create --remote`, the operator's laptop generates the credential and stores it at `~/.km/desktop/<id>`. The Lambda subprocess then calls `km create` again on the remote side, which doesn't have access to `~/.km/desktop/`. The VSCode pattern uses `KM_VSCODE_SSH_PUBKEY` env var for this. Desktop needs the same pattern.
**How to avoid:** At the local `km create --remote` call site (create.go), after generating the credential, set `KM_DESKTOP_KASM_USER` and `KM_DESKTOP_KASM_PASS` env vars (analogous to `KM_VSCODE_SSH_PUBKEY`). In the Lambda subprocess path, check for these vars before regenerating.
**Confirmed from:** create.go:621-624 (the `KM_VSCODE_SSH_PUBKEY` pattern).
**Confidence:** HIGH (code read)

---

## Code Examples

### VSCode → Desktop mapping (exact file-by-file)

**File 1: `pkg/profile/types.go`**
- `RuntimeVSCodeSpec` (line 176) → add `RuntimeDesktopSpec` as sibling
- `IsVSCodeEnabled` (line 619) → add `IsDesktopEnabled` (same body, nil returns `false` not `true`)
- `RuntimeSpec.VSCode` (line 316) → add `RuntimeSpec.Desktop *RuntimeDesktopSpec`

**File 2: `pkg/profile/schemas/sandbox_profile.schema.json`**
- vscode block (lines 273-283) → add desktop block under `runtime.properties`
- Add `mode` (enum kiosk|full), `browsers` (array with items enum), `geometry` (string with pattern)

**File 3: `pkg/profile/validate.go`**
- `ValidateSemantic` function → add desktop rules after existing vscode/slack checks
- AMI guard uses: `strings.HasPrefix(p.Spec.Runtime.AMI, "ubuntu-")` for slug check
- Use `compiler.IsRawAMIID` for raw ID detection (cross-package call or copy the regex)

**File 4: `pkg/compiler/service_hcl.go`**
- `NetworkConfig` (line 683) → add `DesktopKasmUser string` + `DesktopKasmPass string`

**File 5: `pkg/compiler/userdata.go`**
- `userDataParams` struct (line 3620) → add `DesktopEnabled bool`, `DesktopMode string`, `DesktopBrowsers []string`, `DesktopGeometry string`, `DesktopKasmUser string`, `DesktopKasmPass string`
- `generateUserData` (line 4315 region) → set `params.DesktopEnabled = profile.IsDesktopEnabled(p.Spec.Runtime.Desktop)` etc.
- Template: add `{{- if .DesktopEnabled }}` block after the VSCode block (line 2497)

**File 6: `internal/app/cmd/desktop.go`** (new)
- Copy `vscode.go` structure verbatim
- Remove: `newVSCodeRekeyCmd`, SSH key handling, UpsertHost, ssh-config
- Replace: `vsCodeStatusScript` → `desktopStatusScript`; `parseVSCodeStatus` → `parseDesktopStatus`
- Replace: private key path (`~/.km/keys/<id>`) → credential path (`~/.km/desktop/<id>`)
- Replace: port `22` → port `8444`; print URL + credential instead of SSH instructions
- `resolveVSCodeDeps` → `resolveDesktopDeps` (same body)

**File 7: `internal/app/cmd/create.go`**
- Two call sites (local create + remote create) — add desktop credential generation after VSCode keypair block (lines 616-639 and 2057-2073)

**File 8: `internal/app/cmd/root.go`**
- Line 88: `root.AddCommand(NewVSCodeCmd(cfg))` → add `root.AddCommand(NewDesktopCmd(cfg))` after

### generateUserData desktop template block (verified pattern)
```go
// Source: pkg/compiler/userdata.go — mirrors VSCodeEnabled block at line 2484
// Add after the {{- end }} of the VSCode block (line 2497)
{{- if .DesktopEnabled }}
# Phase 93: KasmVNC desktop (kiosk or full XFCE)
# -------------------------------------------------------
# Step 1: Install packages if not already present (idempotent — AMI-bakeable)
if ! command -v vncserver >/dev/null 2>&1; then
  apt-get install -y dbus-x11 fonts-dejavu fonts-liberation x11-xserver-utils
  UBUNTU_CODENAME=$(. /etc/os-release && echo $VERSION_CODENAME)
  KASMVNC_DEB="kasmvncserver_${UBUNTU_CODENAME}_1.4.0_amd64.deb"
  wget -q -O /tmp/${KASMVNC_DEB} \
    "https://github.com/kasmtech/KasmVNC/releases/download/v1.4.0/${KASMVNC_DEB}"
  apt-get install -y /tmp/${KASMVNC_DEB}
  rm -f /tmp/${KASMVNC_DEB}
  {{- if eq .DesktopMode "kiosk" }}
  apt-get install -y matchbox-window-manager
  {{- end }}
  {{- if eq .DesktopMode "full" }}
  apt-get install -y xfce4 xfce4-goodies
  {{- end }}
  # Browser install (selected browsers only)
  {{- range .DesktopBrowsers }}
  {{- if eq . "firefox" }}
  add-apt-repository -y ppa:mozillateam/ppa && apt-get update -q
  apt-get install -y -t 'o=LP-PPA-mozillateam' firefox
  {{- end }}
  {{- if eq . "chromium" }}
  add-apt-repository -y ppa:xtradeb/apps && apt-get update -q
  apt-get install -y -t 'o=LP-PPA-xtradeb' chromium
  {{- end }}
  {{- if eq . "chrome" }}
  # ... Google Chrome APT repo setup (dl.google.com) ... → google-chrome-stable
  {{- end }}
  {{- if eq . "brave" }}
  # ... Brave APT repo setup ...
  {{- end }}
  {{- end }}
fi

# Step 2: Always (re)seed per-sandbox credential + config
sudo -u sandbox mkdir -p /home/sandbox/.vnc /home/sandbox/.config
printf '%s\n%s\n' '{{ .DesktopKasmPass }}' '{{ .DesktopKasmPass }}' \
  | sudo -u sandbox kasmvncpasswd -u '{{ .DesktopKasmUser }}' -w -r /home/sandbox/.kasmpasswd
chmod 600 /home/sandbox/.kasmpasswd
chown sandbox:sandbox /home/sandbox/.kasmpasswd

# Step 3: Write kasmvnc.yaml (geometry parsed from {{ .DesktopGeometry }})
# ... write /home/sandbox/.vnc/kasmvnc.yaml ...

# Step 4: Write xstartup
# ... write /home/sandbox/.vnc/xstartup ...
chmod +x /home/sandbox/.vnc/xstartup
chown -R sandbox:sandbox /home/sandbox/.vnc

# Step 5: Install + enable kasmvnc systemd service
# ... write /etc/systemd/system/kasmvnc.service ...
systemctl daemon-reload
systemctl enable kasmvnc
systemctl start kasmvnc
echo "[km-bootstrap] KasmVNC desktop configured ({{ .DesktopMode }} mode)"
{{- end }}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|---|---|---|---|
| TigerVNC + websockify + noVNC | KasmVNC (single binary) | KasmVNC ~2020 | Built-in HTML5 client; real bidirectional clipboard |
| Firefox snap (Ubuntu 22.04+) | Mozilla PPA DEB | Ubuntu 22.04 release | Snap Firefox doesn't run under VNC; DEB works cleanly |
| Chromium snap (Ubuntu 20.04+) | xtradeb PPA DEB | Ubuntu 20.04 release | Same snap-in-VNC problem |
| `cli.vscodeEnabled` | `spec.runtime.vscode.enabled` | Phase 92 | Schema cleanup; desktop follows this pattern from day one |

**Deprecated/outdated:**
- `noVNC` + `websockify`: superseded by KasmVNC's built-in web client for new installs
- `vncpasswd` (TigerVNC password tool): replaced by `kasmvncpasswd` in KasmVNC

---

## Open Questions

1. **`vncserver :1` vs `Xvnc :1` direct invocation**
   - What we know: `vncserver` is a wrapper script; `Xvnc` is the actual process. `vncserver -fg` runs the Xvnc process in foreground but also runs xstartup.
   - What's unclear: Whether `-noxstartup` + `Xvnc :1 -interface 127.0.0.1` directly (skipping the wrapper) gives cleaner systemd integration.
   - Recommendation: Start with `vncserver :1 -fg -geometry {{ .DesktopGeometry }} -interface 127.0.0.1`; fall back to direct Xvnc if systemd tracking is flaky.

2. **Remote create credential env var naming**
   - What we know: VSCode uses `KM_VSCODE_SSH_PUBKEY`; desktop needs a similar pattern.
   - Recommendation: Use `KM_DESKTOP_KASM_USER` + `KM_DESKTOP_KASM_PASS`. Check for these in the Lambda subprocess path before regenerating.

3. **Ubuntu AMI detection for non-slug raw IDs at validate time**
   - What we know: Raw AMI IDs cannot be inspected without an AWS call.
   - Recommendation: WARN (not ERROR) for raw AMI IDs with desktop enabled; ERROR for known non-Ubuntu slugs (`amazon-linux-2023`, or absent AMI which defaults to AL2023).

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` standard library (no external framework) |
| Config file | none — `go test ./...` |
| Quick run command | `go test ./pkg/profile/... ./pkg/compiler/... ./internal/app/cmd/... -run Desktop -v` |
| Full suite command | `go test ./pkg/profile/... ./pkg/compiler/... ./internal/app/cmd/... -count=1` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| DSK-01-SCHEMA | `RuntimeDesktopSpec` struct compiles; `RuntimeSpec.Desktop` field present | unit (compile) | `go build ./pkg/profile/...` | ❌ Wave 0 |
| DSK-02-HELPER | `IsDesktopEnabled(nil)` returns false; `IsDesktopEnabled(&{Enabled: &true})` returns true | unit | `go test ./pkg/profile/... -run TestIsDesktopEnabled -v` | ❌ Wave 0 |
| DSK-03-VALIDATE | mode enum, browsers membership, browsers non-empty for kiosk, geometry regex, Ubuntu-only ERROR guard | unit | `go test ./pkg/profile/... -run TestDesktopValidate -v` | ❌ Wave 0 |
| DSK-04-SCHEMA-EXPORT | JSON schema contains desktop block; `km validate profiles/desktop.yaml` passes | unit + inventory gate | `go test ./pkg/profile/... -run TestSchema` + `./km validate profiles/desktop.yaml` | ❌ Wave 0 |
| DSK-05-COMPILER-THREAD | `NetworkConfig.DesktopKasm*` fields set; `userDataParams.DesktopEnabled` set from profile | unit | `go test ./pkg/compiler/... -run TestUserDataDesktop -v` | ❌ Wave 0 |
| DSK-06-USERDATA-INSTALL | `DesktopEnabled=true` emits KasmVNC install block, kasmpasswd seed; `DesktopEnabled=false` emits nothing | unit (golden) | `go test ./pkg/compiler/... -run TestUserDataDesktopEnabled -v` | ❌ Wave 0 |
| DSK-07-USERDATA-SESSION | kiosk xstartup contains `matchbox-window-manager`; full xstartup contains `startxfce4`; kasmvnc.yaml contains `require_ssl: false` and `interface: 127.0.0.1` | unit (golden) | `go test ./pkg/compiler/... -run TestUserDataDesktopKiosk -v` | ❌ Wave 0 |
| DSK-08-CREDENTIAL | `km create` generates `~/.km/desktop/<id>` with `user:pass` format (mode 0600); credential threaded into NetworkConfig | unit | `go test ./internal/app/cmd/... -run TestDesktopCredential -v` | ❌ Wave 0 |
| DSK-09-CLI-START | port-in-use returns error; missing credential file returns error; healthy path prints URL + credential; port-forward args contain `AWS-StartPortForwardingSession` and port `8444` | unit | `go test ./internal/app/cmd/... -run TestDesktopStart -v` | ❌ Wave 0 |
| DSK-10-CLI-STATUS | healthy path prints "KasmVNC ready"; kasmvnc inactive → error with `desktop.enabled` hint | unit | `go test ./internal/app/cmd/... -run TestDesktopStatus -v` | ❌ Wave 0 |
| DSK-11-SECURITY | `network.interface: 127.0.0.1` appears in generated kasmvnc.yaml; `require_ssl: false` appears | unit (golden, part of DSK-07) | (covered by DSK-07 test) | ❌ Wave 0 |
| DSK-12-PROFILE-EXAMPLE | `profiles/desktop.yaml` passes `km validate` + appears in `scripts/validate-all-profiles.sh` inventory | inventory gate | `bash scripts/validate-all-profiles.sh` | ❌ Wave 0 |
| DSK-13-SKILL | `skills/desktop/SKILL.md` exists; plugin version bumped | manual check | `ls skills/desktop/SKILL.md && grep version .claude-plugin/plugin.json` | ❌ Wave 0 |
| DSK-14-DOCS | `docs/desktop.md` exists; `CLAUDE.md` has desktop row | manual check | `ls docs/desktop.md` | ❌ Wave 0 |
| DSK-15-TESTS | All test functions named per mapping above exist and pass | full suite | `go test ./... -count=1` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/profile/... ./pkg/compiler/... ./internal/app/cmd/... -run Desktop -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green + `bash scripts/validate-all-profiles.sh` before `/gsd:verify-work`

### Autonomous vs. Operator UAT Split

**Autonomously verifiable (CI-green before operator involvement):**
- All schema compilation, `IsDesktopEnabled` logic, validate semantic rules
- Compiler userdata golden tests (kiosk xstartup, full xstartup, kasmvnc.yaml content, credential seed, disabled emits nothing)
- CLI unit tests with mocked SSM (port-in-use, pre-flight parse states, credential file read, URL format)
- Profile example `km validate` gate
- `scripts/validate-all-profiles.sh` inventory gate

**Requires live operator UAT checkpoint (human, real sandbox):**
- Actual KasmVNC startup on a real Ubuntu 24.04 EC2 sandbox (packages install, service starts)
- Browser-in-browser rendering (Firefox kiosk visible in operator browser)
- Bidirectional clipboard working (text from operator browser to sandbox Firefox and back)
- `km resume` → KasmVNC session restores (systemd unit auto-starts)
- AMI bake → fresh create from baked AMI → packages already installed, credential seeded fresh
- Full XFCE mode rendering (desktop visible, taskbar, window manager)
- Network-allowlist caveat: first boot with locked `spec.network` fails gracefully with clear error

### Wave 0 Gaps (test stubs to create before implementation)
- [ ] `pkg/profile/validate_test.go` — `TestDesktopValidate*` stub functions (behind `t.Skip`)
- [ ] `pkg/compiler/userdata_test.go` — `TestUserDataDesktopEnabled`, `TestUserDataDesktopDisabled`, `TestUserDataDesktopKiosk`, `TestUserDataDesktopFull`, `TestUserDataDesktopCredentialSeed` stubs
- [ ] `internal/app/cmd/desktop_test.go` — full test file mirroring `vscode_test.go` structure, stubs behind `t.Skip`
- [ ] `pkg/profile/types_test.go` additions — `TestIsDesktopEnabled` stub
- No framework install needed — Go standard library test framework already in use

---

## Sources

### Primary (HIGH confidence)
- `internal/app/cmd/vscode.go` — direct file read; complete VSCode implementation as mirror template
- `internal/app/cmd/vscode_test.go` — direct file read; 945-line test suite as desktop_test.go template
- `pkg/profile/types.go` — direct file read; `RuntimeVSCodeSpec` + `IsVSCodeEnabled` at lines 176, 619
- `pkg/profile/validate.go` — direct file read; `ValidationError.IsWarning` pattern confirmed
- `pkg/profile/schemas/sandbox_profile.schema.json` — direct file read; vscode schema block at line 273
- `pkg/compiler/service_hcl.go` — direct file read; `NetworkConfig.VSCodeSSHPubKey` at line 704
- `pkg/compiler/userdata.go` — direct file read; VSCode template block at lines 2484-2497; `userDataParams.VSCodeEnabled` at line 3638
- `pkg/compiler/userdata_test.go` — direct file read; `TestUserDataVSCode*` suite at lines 1909-1987
- `internal/app/cmd/create.go` — direct file read; VSCode keypair generation at lines 616-638 and 2057-2073
- `internal/app/cmd/root.go` — direct file read; `NewVSCodeCmd` registration at line 88
- `scripts/validate-all-profiles.sh` — direct file read; inventory bash array pattern confirmed (20 profiles)
- `skills/vscode/SKILL.md` — direct file read; skill structure for desktop skill
- `.claude-plugin/plugin.json` — direct file read; version "0.3.0" to bump
- GitHub API (`/repos/kasmtech/KasmVNC/releases/latest`) — confirmed v1.4.0 stable with `kasmvncserver_noble_1.4.0_amd64.deb` + `kasmvncserver_jammy_1.4.0_amd64.deb`
- `https://raw.githubusercontent.com/kasmtech/KasmVNC/master/unix/kasmvnc_defaults.yaml` — confirmed exact YAML keys: `network.interface`, `network.ssl.require_ssl` (default `true`), `network.websocket_port` (default `auto`), `desktop.resolution.width/height`, clipboard keys
- Ubuntu packages.ubuntu.com — `matchbox-window-manager` confirmed in noble universe (1.2.2+git20200512-1build1)

### Secondary (MEDIUM confidence)
- DeepWiki KasmVNC config docs — `network.interface: 127.0.0.1` for localhost binding; `network.websocket_port` syntax
- KasmVNC FAQ — `dbus-x11` required; GNOME grey screen root cause (XDG_RUNTIME_DIR + non-root user)
- KasmVNC Xvnc man page — `websocket_port: auto = displayNumber + 8443`; `-localhost` flag exists; `-sslOnly` is off by default
- KasmVNC vncserver(1) man page — `-select-de`, `-geometry`, `-fg`, display number syntax
- vncpasswd(1) man page — `kasmvncpasswd -u <user> -w -r ~/.kasmpasswd` syntax

### Tertiary (LOW confidence — flag for UAT)
- KasmVNC issue #273 — non-interactive credential seeding pattern; has an unresolved thread but the `printf 'pass\npass\n' | kasmvncpasswd ...` form is the documented approach
- xtradeb PPA for non-snap chromium — described in multiple blog posts; not verified against Ubuntu 24.04 package directly
- Firefox Mozilla PPA DEB behavior in VNC — confirmed as working approach in multiple Docker image projects; snap confirmed broken in VNC by multiple sources

---

## Metadata

**Confidence breakdown:**
- Standard stack (KasmVNC version + .deb URLs): HIGH — GitHub API confirmed
- kasmvnc.yaml exact keys: HIGH — raw defaults file from GitHub
- Architecture (mirror vscode.go): HIGH — code read; exact line numbers
- Browser install (snap-avoidance): HIGH — multiple authoritative sources
- matchbox-wm availability Ubuntu 24.04: HIGH — packages.ubuntu.com confirmed
- kasmvncpasswd non-interactive: MEDIUM — confirmed pattern but issue #273 shows edge cases
- systemd unit form (system vs user): MEDIUM — reasoning correct but not tested on live EC2
- Pitfall completeness: MEDIUM — research-identified; all require live UAT to confirm resolution

**Research date:** 2026-06-02
**Valid until:** 2026-09-01 (KasmVNC stable track; Ubuntu distro packages stable; 90 days)
