---
phase: 93-km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward
plan: "03"
subsystem: compiler
tags: [desktop, kasmvnc, userdata, kiosk, xfce, tdd]
dependency_graph:
  requires: [93-01]
  provides: [DSK-05-COMPILER-THREAD, DSK-06-USERDATA-INSTALL, DSK-07-USERDATA-SESSION, DSK-11-SECURITY]
  affects: [pkg/compiler/service_hcl.go, pkg/compiler/userdata.go]
tech_stack:
  added: []
  patterns: [template-gate, tdd-red-green, browser-keyword-binary-map, idempotent-boot-block]
key_files:
  created: []
  modified:
    - pkg/compiler/service_hcl.go
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go
decisions:
  - "Pre-parse DesktopGeometryWidth/DesktopGeometryHeight in generateUserData instead of adding a template FuncMap — keeps parseUserDataTemplate() zero-dependency"
  - "DesktopBrowser0Binary field computed at generateUserData time via desktopBrowserBinary() — chrome→google-chrome-stable, brave→brave-browser, others match keyword"
  - "System service (User=sandbox) for kasmvnc.service mirrors km-queue.service pattern — no loginctl session needed at boot"
metrics:
  duration: 297s
  completed: "2026-06-02"
  tasks: 2
  files: 3
---

# Phase 93 Plan 03: Compiler Threading + KasmVNC Userdata Block Summary

**One-liner:** KasmVNC userdata block with idempotent install, per-sandbox credential seed, loopback-bound kasmvnc.yaml, kiosk/full xstartup, and systemd unit threaded through NetworkConfig + userDataParams.

## What Was Built

### Task 1: Thread desktop fields through NetworkConfig + userDataParams (feat — 58178fe8)

**service_hcl.go:** Added `DesktopKasmUser string` and `DesktopKasmPass string` to `NetworkConfig` after `VSCodeSSHPubKey`. Doc comment notes they are per-sandbox and never baked.

**userdata.go:** Added 8 new fields to `userDataParams`:
- `DesktopEnabled bool` — gate for the template block
- `DesktopMode string` — "kiosk" or "full"
- `DesktopBrowsers []string` — browser keyword list
- `DesktopGeometry string` — raw "WxH" string
- `DesktopKasmUser/Pass string` — threaded from NetworkConfig
- `DesktopBrowser0Binary string` — pre-mapped launch binary (chrome→google-chrome-stable)
- `DesktopGeometryWidth/Height string` — pre-parsed to avoid template FuncMap

Added `desktopBrowserBinary(keyword string) string` helper with the keyword→binary mapping table.

In `generateUserData`: set all `params.Desktop*` from `profile.IsDesktopEnabled()` + profile fields + NetworkConfig, with defaults (mode="kiosk", browsers=["firefox"], geometry="1920x1080").

### Task 2: KasmVNC userdata template block (TDD RED→GREEN — 6443352f + bbf0f785)

**RED (test/93-03, 6443352f):** Replaced 6 `t.Skip()` Wave 0 stubs with golden assertions. All 5 tests failed correctly (TestUserDataDesktopDisabled was already PASS since no block existed).

**GREEN (feat/93-03, bbf0f785):** Added `{{- if .DesktopEnabled }}...{{- end }}` block after the VSCode block:

1. **Idempotent install guard** (`if ! command -v vncserver`): installs dbus-x11, fonts, x11-xserver-utils; fetches KasmVNC 1.4.0 deb by `$VERSION_CODENAME`; installs matchbox-window-manager (kiosk) or xfce4+xfce4-goodies (full); loops over browsers installing firefox (Mozilla PPA), chromium (xtradeb PPA), google-chrome-stable (Google APT repo), or brave-browser (Brave APT repo) with snap-avoidance on each.

2. **Credential reseed** (always, even on baked AMI): `printf '%s\n%s\n' PASS PASS | kasmvncpasswd -u USER -w -r /home/sandbox/.kasmpasswd`; chmod 600; chown sandbox.

3. **kasmvnc.yaml** (always): `interface: 127.0.0.1`, `require_ssl: false`, `websocket_port: auto`, resolution from DesktopGeometryWidth/Height, clipboard both directions enabled.

4. **xstartup** (always): kiosk mode = unset SESSION_MANAGER/DBUS, set XDG_RUNTIME_DIR, `dbus-launch matchbox-window-manager` + `DesktopBrowser0Binary`; full mode = `dbus-launch startxfce4`; chmod +x; chown -R sandbox.

5. **kasmvnc.service** (always): system service, `User=sandbox`, `vncserver :1 -fg -geometry ... -interface 127.0.0.1`; ExecStop; daemon-reload; enable; start.

## Test Results

```
=== RUN   TestUserDataDesktopEnabled      PASS
=== RUN   TestUserDataDesktopDisabled     PASS
=== RUN   TestUserDataDesktopKiosk        PASS (incl. DSK-11-SECURITY: 127.0.0.1 + require_ssl: false)
=== RUN   TestUserDataDesktopFull         PASS
=== RUN   TestUserDataDesktopCredentialSeed PASS
=== RUN   TestUserDataDesktopChromeBinary PASS
ok  github.com/whereiskurt/klanker-maker/pkg/compiler  4.826s
```

Full compiler suite: GREEN. Full `go build ./...`: GREEN.

## Decisions Made

1. **Pre-parse geometry in generateUserData** — `DesktopGeometryWidth` and `DesktopGeometryHeight` are pre-split with `strings.SplitN(geometry, "x", 2)` so the template uses simple field references (`{{ .DesktopGeometryWidth }}`). This avoids registering a custom FuncMap with `template.New("userdata").Funcs(...)`, which would require changes to both `parseUserDataTemplate()` and `generateUserData()` — two call sites.

2. **DesktopBrowser0Binary computed field** — Rather than using `index .DesktopBrowsers 0` in the template and a custom map function, `desktopBrowserBinary()` maps keyword→binary at generateUserData time. The template sees `{{ .DesktopBrowser0Binary }}` which is already `google-chrome-stable` for `chrome`. This is the pattern called out in the plan ("Prefer a computed DesktopBrowser0Binary").

3. **System service with User=sandbox** — Mirrors km-queue.service; the sandbox user has no loginctl session at boot so `systemd --user` would require a PAM session. System service with `User=sandbox` works identically and survives `km resume`.

4. **TestUserDataDesktopDisabled uses baseProfile()** — The disabled test uses the base profile with no desktop block (IsDesktopEnabled → false). No need to explicitly set `enabled: false`; the nil-pointer default is the real-world path.

## Deviations from Plan

None — plan executed exactly as written. The initial compile failure (`pkg/profile/validate.go: undefined: validateDesktop`) was a stale cache artifact that resolved on a clean second build; `validateDesktop` was already present in validate.go from prior 93-02 work.

## Self-Check: PASSED

- FOUND: pkg/compiler/service_hcl.go
- FOUND: pkg/compiler/userdata.go
- FOUND: pkg/compiler/userdata_test.go
- FOUND: 93-03-SUMMARY.md
- FOUND commit 58178fe8 (feat: thread desktop fields)
- FOUND commit 6443352f (test: RED phase)
- FOUND commit bbf0f785 (feat: GREEN phase)
