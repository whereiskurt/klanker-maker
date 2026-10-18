# `km t3` — run T3 Code in a sandbox, in the background

**Date:** 2026-10-17
**Status:** Design — approved in brainstorm (operator); builder-checked
**Phase:** TBD (cross-check `grep "Phase NNN" CLAUDE.md docs/` before `gsd phase add`)

## 1. Summary

[T3 Code](https://github.com/pingdotgg/t3code) is an "agent harness control
surface": a server (`t3 serve`) that drives Claude Code, Codex and others on the
machine it runs on, and a web / desktop / mobile client that talks to it. Agents
keep working while no client is connected.

Add `km t3`, a sibling of `km vscode`, `km desktop` and `km herdr`:

```
km t3 start  <sandbox-id>   install t3 if absent/stale, start the server in the background
km t3 stop   <sandbox-id>   stop the server (and keep it stopped across resume)
km t3 status <sandbox-id>   installed? version? running? providers? presence token? signal 9?
km t3        <sandbox-id>   open the reconnecting SSM forward, print the URL + a fresh pairing link
```

Three things ship together:

1. **`km t3 …`** — operator-side, over SSM. Works on sandboxes that are already
   running: no userdata, no profile schema, no Lambda, no recreate.
2. **`km-t3code.service`** — a km-owned systemd *system* unit, `User=sandbox`,
   written, enabled and stopped by `km t3 start|stop` over SSM.
3. **km-presence signal 9 ("t3 in use")** — keeps the box awake while a client is
   connected OR an agent turn is running, and only then; plus a matching
   **signal 5 exclusion** so t3's long-lived agent children do not hold the box
   awake by name.

## 2. Intent (the operator's words, paraphrased)

- "Just like we have for VS Code and the desktop concept" — a first-class verb family.
- "Go one step further: start it in the background … I could start and stop it."
- "When it's started … prevent the system from shutting down … create a signal for it",
  refined in brainstorm to: **"awake as long as a client is connected or agent turns
  are firing within the idle period."** A started-but-unused server must NOT hold the
  box awake.
- Clients: **laptop browser and the t3 desktop app only.** "There is no ingress
  without SSM, so [phone / an exposed tunnel] is kinda not possible by design."

## 3. Goals, non-goals, anti-goals

**Goals**

- One command starts a background t3 server on a chosen sandbox; one stops it.
- One command connects a laptop browser (or the desktop app) to it through SSM.
- A started server survives the operator's terminal closing, laptop sleep, and
  `km pause`/`km stop` → `km resume` (the unit is *enabled*). A stopped one stays
  stopped across resume (the unit is *disabled*).
- The idle reaper sees real t3 use and nothing else.
- Agents launched by t3 run the way every other sandbox agent runs: as uid
  `sandbox`, under a login shell, so the Phase 133 consumer shims (brokered secrets),
  the nvm PATH, and Phase 135 eBPF enforcement (uid predicate) all apply.

**Non-goals**

- **Phone / mobile app, T3 Connect, LAN or Tailscale pairing, the hosted web app
  (app.t3.codes).** All need ingress that is not SSM. Out by design.
- No profile schema field and no `profiles/base/tools/t3code.yaml` fragment in this
  phase (on-demand install takes ~20 s; a fragment is a cheap follow-up).
- No S3 mirror of the t3 release (see §4 F2 — GitHub is already reachable).
- No `km t3 rekey`: browser sessions are t3's own (`t3 auth session list|revoke`).
- t3's headless-browser tabs (`t3 browser setup`, ~120 MB Chrome) — not wired.
- `km t3 uninstall` — `km t3 stop` leaves `~/.t3` intact; removal is `t3 uninstall`
  as the sandbox user, or the sandbox's own lifecycle.

**Anti-goals (a failure even if it "works")**

- **A started server that pins the box awake.** The VS Code / herdr lesson: detect
  the *use*, never the server. §4 F5 shows signal 5 would do exactly this today.
- **A listener on anything but 127.0.0.1** on the box.
- **A long-lived credential in a log, the audit stream, or SSM command output.**
  The only credential that ever crosses SSM output is a 5-minute, single-use pairing
  link (§5.4). The presence token is written to a root-only file on the box by the
  same command that mints it and is never echoed.
- **t3 agents escaping km's controls** — running as root, outside a login shell
  (no shims), or under `systemd-run --user` scopes outside the unit's cgroup.

## 4. Facts that shaped the design

**[live]** = measured on a real sandbox during the design spike
(`herdr-ca3c8843`, AL2023, profile `herdr`); **[src]** = t3code source
(`pingdotgg/t3code@main`, 2026-10-17); **[doc]** = t3's user docs.

- **F1 [src][live] Single binary, no Node.** `t3` is a Node single-executable;
  the installer (`scripts/install.sh`) needs only `sh`, `tar`, `sha256sum`, `curl`.
  It downloads `t3-<ver>-linux-x64.tar.gz` + `SHA256SUMS` from GitHub release assets,
  verifies, unpacks to `~/.t3/runtime/versions/<ver>/` and symlinks
  `~/.local/bin/t3`. `T3CODE_VERSION=<x.y.z>` pins it (and skips the
  `api.github.com` release lookup). Latest stable today: **0.1.0** (2026-10-15).
- **F2 [live] AL2023 lacks `libatomic`.** The pinned 0.1.0 binary fails its own
  post-install smoke test (`libatomic.so.1: cannot open shared object file`) until
  `dnf install -y libatomic` runs as root. Ubuntu package: `libatomic1`. So `start`
  has a root step before the sandbox-user install. **The package repos are NOT in
  `base/network/locked`** (`cdn.amazonlinux.com` / the Ubuntu archive), and the DNS
  allowlist binds root too, so on a locked box that step fails (builder check, Q1).
- **F3 [src] Egress.** Install needs `github.com` + `objects.githubusercontent.com`
  (+ `raw.githubusercontent.com` for the pinned installer script, §5.2). All are
  inside `base/network/locked`'s `.github.com` / `.githubusercontent.com`. At runtime
  t3 fetches a model manifest from `raw.githubusercontent.com` (bundled fallback, works
  offline) and PostHog telemetry (`T3CODE_TELEMETRY_ENABLED=false` turns it off).
  T3 Connect stays off unless `t3 connect` is run — km never runs it.
- **F4 [src][live] Server shape.** `t3 serve --host 127.0.0.1 --port 3773
  --no-browser <cwd>` binds loopback. The process's `comm` is **`node-MainThread`**,
  not `t3` (live `ss -p` and `systemctl status`) — so matching by process name is
  useless; match by port, which km owns. It spawns `t3-resource-monitor`. It writes
  `~/.t3/userdata/server-runtime.json` (`{pid, host, port, origin, serverVersion…}`).
  **At startup it prints a pairing QR code + token to stdout** — so stdout must not go
  to the journal (§5.3).
- **F5 [src] Agent children are long-lived.** Claude runs via
  `@anthropic-ai/claude-agent-sdk` with a streaming prompt → one long-lived `claude`
  child per provider session; Codex runs one shared `codex app-server`. A session is
  released only after **30 min idle**, and is *pinned* up to **4 h** while the adapter
  reports pending background work (`ProviderSessionManager.ts`). km-presence signal 5
  (`pgrep -af '(^|/)claude( |$)|(^|/)codex( |$)|…'`) matches both by name, so **any t3
  turn holds the box awake ≥30 min afterwards and up to ~4.5 h** — the anti-goal.
- **F6 [src] Agent scopes.** On Linux, when `XDG_RUNTIME_DIR` is set and a user
  manager is reachable, t3 wraps each agent child in
  `systemd-run --user --scope --slice=app-t3code-agents-….slice`. That would move
  agents out of the unit's cgroup (breaking the signal 5 exclusion) and into a user
  manager km does not manage. **[live]** `runuser -u sandbox -- bash -lc` has
  `XDG_RUNTIME_DIR` empty and `sandbox` is not lingering; the unit also sets
  `UnsetEnvironment=XDG_RUNTIME_DIR` so this stays true. Children then spawn
  directly, inside `system.slice/km-t3code.service`.
- **F7 [src][live] "Is a turn running?"** `GET /api/orchestration/shell` with
  `Authorization: Bearer <tok>` and `x-t3-orchestration-protocol: 2`, scope
  `orchestration:read`. Live: `200 {"schemaVersion":2,"threads":[],…}` with the
  token, `401` without. Each thread carries `activeRunId` (non-null while a run is
  active) and `pendingBackgroundTasks[]`. The token is minted offline (no server
  call) by `t3 auth session issue --scope orchestration:read --label km-presence
  --ttl <dur> --token-only` — 312 bytes on stdout; it lists as
  `client: km-presence | bot`, `scopes: orchestration:read`.
- **F8 [live] Client detection by socket.** With the server up and an **idle** SSM
  port-forward open on the laptop: `ss -tnH state established '( sport = :3773 )'`
  → **0**. With a paired headless browser attached through that forward → **1**
  (`127.0.0.1:3773 ↔ 127.0.0.1:44308`, the SSM agent's leg). A one-shot `curl` to the
  shell endpoint leaves **0** behind. So "a client is connected" = "an established
  socket whose local port is 3773".
- **F9 [src][live] Pairing.** `t3 auth pairing create --base-url
  http://localhost:<lp> --json --ttl 5m --label …` does not need the server running
  and returns `{credential, pairUrl, expiresAt…}`; `pairUrl` is
  `http://localhost:<lp>/pair#token=…`. Live: opening it in headless Chromium through
  the forward paired and showed the environment as **Connected** at
  `http://localhost:3774/`. Pairing links are single-use and expire.
- **F11 [src] Providers are resolved from PATH.** Claude's `binaryPath` setting
  defaults to `claude`, resolved from `PATH` (`packages/contracts/src/settings.ts:621`,
  `ClaudeAdapterV2.ts:1151`); Codex runs `codex app-server` the same way. That is what
  makes the unit's `bash -lc` (→ `/opt/km/shims` first) route agents through the
  Phase 133 shims. Verified live in UAT step 4 by reading the child's
  `/proc/<pid>/exe` chain; if t3 is found to use a bundled binary instead, that ships
  as a documented limitation (shims bypassed on sops profiles) with the fix named
  (set t3's provider `binaryPath` to `/opt/km/shims/claude`), not as a blocker.
- **F10 [doc]** The desktop app's **"Add environment"** takes the same pairing link.
  Its built-in **SSH** mode downloads its own server to `~/.t3/runtime` and "starts or
  reuses" one; whether it reuses km's unit is unverified, so it is documented as
  unsupported-but-may-work.

## 5. Design

### 5.1 Command surface (`internal/app/cmd/t3.go`)

Mirrors `herdr.go`: one parent `km t3 <sandbox-id>` whose RunE is the daily
**connect** verb, with `start`, `stop`, `status` subcommands. Same dependency seams
(`SandboxFetcher`, `ShellExecFunc`, `SSMSendAPI` via `resolveVSCodeDeps`), the same
`ResolveSandboxID` alias resolution, `SilenceUsage`, exactly-one positional.

| Verb | Flags | Does |
|---|---|---|
| `km t3 start <id>` | `--version` (default = pinned) | §5.2 ensure + §5.3 unit + §5.5 token, one SSM round trip; prints version, `active`, and "next: `km t3 <id>`" |
| `km t3 stop <id>` | — | `systemctl disable --now km-t3code.service`; prints the result. Idempotent. |
| `km t3 status <id>` | — | §5.6 report; non-zero exit if not installed/not running |
| `km t3 <id>` | `--local-port` (default **3774**), `--no-pair` | §5.4 |

Default local port **3774**: 3773 is t3's own default, which the operator's laptop
may well be using for a local t3; 2222/2223/2224/8444 are taken by
vscode/tunnel/herdr/desktop. One local port = one environment in the browser's saved
list, so connecting two sandboxes at once needs two `--local-port` values (document).

Remote port is a km-owned constant **3773**, shared by the unit, the connect
forward, and signal 9 — pinned across sites by a test (the `herdrS3Key` pattern).

`connect` does **not** use `connectPrep`: that requires the per-sandbox SSH key and
writes an ssh-config entry, and t3 needs neither (the forward goes straight to 3773).
A small `t3ConnectPrep` does the local-port probe (same error shape), fetch, instance
id, region.

### 5.2 Install (inside the `start` SSM script, POSIX sh — `sendSSMAndWait` runs dash)

1. As root: if `ldconfig -p | grep -q libatomic.so.1` fails, `dnf install -y libatomic`
   or `apt-get install -y libatomic1` (whichever exists). (F2) **If that fails**, stop
   with a marker the Go side turns into a named error: "libatomic is missing and the
   package repos are unreachable from this sandbox (a `base/network/locked`-style
   egress allowlist). Add `cdn.amazonlinux.com` (AL2023) / the Ubuntu archive to
   `allowedDNSSuffixes` and recreate, or use an AMI that already has it
   (`km ami bake`)." (`initCommands` is NOT a workaround: section 7.5 runs after the
   section 6 network setup.) **Decided (builder check Q1):** locked profiles are
   supported only via that recipe — no S3 mirror of the package; that stays a
   non-goal.
2. As `sandbox` (`runuser -u sandbox -- bash -lc`): if
   `~/.local/bin/t3 --version` ≠ the pinned version, fetch the installer **from the
   pinned tag on GitHub**, not `t3.codes` (outside `locked`'s allowlist):
   `https://raw.githubusercontent.com/pingdotgg/t3code/v<ver>/scripts/install.sh`,
   run with `T3CODE_VERSION=<ver>`. The installer does its own SHA256 verification.
3. Print `=== t3 path ===` / `=== t3 version ===` markers (parsed like herdr's).

Pinned version: a Go const `t3Version = "0.1.0"`; `--version` overrides it per run
(the only way to move without a km release). The installer is idempotent for an
already-installed version.

### 5.3 The unit (`km-t3code.service`, written by `start`, content is a Go const)

```ini
[Unit]
Description=T3 Code server (km t3)
After=network-online.target

[Service]
User=sandbox
Group=sandbox
WorkingDirectory=/workspace
Environment=T3CODE_TELEMETRY_ENABLED=false
UnsetEnvironment=XDG_RUNTIME_DIR
ExecStart=/bin/bash -lc 'umask 077; L=/home/sandbox/.t3/km-t3code.log; mkdir -p /home/sandbox/.t3; if [ -e "$$L" ] && [ ! -O "$$L" ]; then rm -f "$$L"; fi; touch "$$L" && chmod 600 "$$L"; exec t3 serve --host 127.0.0.1 --port 3773 --no-browser /workspace >>/home/sandbox/.t3/km-t3code.log 2>&1'
KillMode=mixed
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Each line is load-bearing:

- **System unit, `User=sandbox`** — not `t3 service install` (a *user* unit that
  needs `loginctl enable-linger`, i.e. sudo, and lives outside km's control).
- **`bash -lc`** — a login shell sources `/etc/profile.d`, which puts
  `/opt/km/shims` first (Phase 133: claude/codex get their brokered secrets) and nvm on
  PATH, and resolves `~/.local/bin/t3`. Same reason every poller dispatches through
  `bash -lc`.
- **`UnsetEnvironment=XDG_RUNTIME_DIR`** — keeps agents in this unit's cgroup (F6).
- **`--host 127.0.0.1`** — the anti-goal; also t3's default, stated explicitly so a
  future default change cannot widen it.
- **Log to `~/.t3/km-t3code.log`, not the journal** — t3 prints a pairing token at
  startup (F4). `~/.t3` is t3's own store, already holding every credential it has;
  the journal is readable by more than the sandbox user and is shipped nowhere, but
  keeping the token out of it is free. **Amended after review/UAT:** the redirect is
  opened by the sandbox user's shell, NOT systemd's `StandardOutput=append:` — that
  opens as root, and `~/.t3` is sandbox-controlled, so a planted symlink aims a root
  open at any file (the first build also had root touch/chown the log there: a
  sandbox→root escalation). `$$L` is systemd's escape for a literal `$`. The
  start script does no root file operation inside `~/.t3` or on a fixed `/tmp` name.
- **`WorkingDirectory=/workspace`** + positional `/workspace` — provider sessions
  start where every km agent works. **Not** `--auto-bootstrap-project-from-cwd`
  (builder's call: the onboarding wizard picks projects; avoids creating a project the
  operator did not ask for).
- **`Restart=on-failure`** (not `always`) — `km t3 stop` must stick.
- **enable --now / disable --now** — the started/stopped choice survives pause, stop
  and resume. Userdata never re-runs; the unit file lives in `/etc/systemd/system` on
  the root volume, so it does.

`start` then waits (≤ 30 s, 1 s poll) for
`curl -fsS http://127.0.0.1:3773/.well-known/t3/environment` before minting the
presence token, and reports `active` / `not ready` with the log tail **with tokens
redacted** (`sed -E 's/(token=|Token: )[^ ]+/\1REDACTED/g'`).

### 5.4 Connect (`km t3 <id>`)

1. `t3ConnectPrep` (local port free, sandbox → instance id + region).
2. One SSM round trip: confirm the unit is `active` (else: error naming
   `km t3 start <id>`); unless `--no-pair`, mint a pairing link
   `t3 auth pairing create --base-url http://localhost:<lp> --json --ttl 5m --label km-t3-<id>`
   and print only `pairUrl` under a marker.
3. Banner: `✓ Forwarding localhost:<lp> → sandbox:3773`, `Open: http://localhost:<lp>/`,
   `First time on this browser: <pairUrl>  (single use, expires in 5 minutes)`,
   desktop-app hint ("Add environment → paste the pairing link"), and the status hint.
4. `runReconnectingPortForward(ctx, execFn, buildPortForwardCmd(…, "<lp>", "3773"),
   httpTunnelProbe(lp), true, os.Stdout)` — the same wrapper every forward uses
   (memory: every new forward must go through it; it handles plugin hangs and the
   `KM_FORWARD_BIND` container relay).

**On the pairing link crossing SSM output:** `GetCommandInvocation` retains output,
so the link is visible to anyone holding `ssm:GetCommandInvocation` on the account
(i.e. operators, who can already `km shell --root`). It is single-use and expires in 5
minutes, so this is accepted and stated in the docs. It is never logged by km.

### 5.5 The presence token

Minted by `start` after the server is ready, as the sandbox user, straight into a
root-only file — the SSM output never contains it:

```sh
install -d -m 0700 /etc/km/t3
runuser -u sandbox -- bash -lc 't3 auth session list --json' → revoke prior label=km-presence sessions
runuser -u sandbox -- bash -lc 't3 auth session issue --scope orchestration:read --label km-presence --ttl 365d --token-only' > /etc/km/t3/presence.token.tmp
chmod 0600 …; mv -f …tmp /etc/km/t3/presence.token
```

- **Read-only scope only.** It cannot start, stop or steer a thread.
- **Revoke-then-issue** so repeated `start`s do not accumulate sessions.
- **365 d** so it outlives any sandbox TTL; if t3 rejects that duration, fall back to
  t3's 30 d default and `status` shows expiry (builder verifies the accepted format).
- **Not a privilege boundary**: the sandbox user owns `~/.t3`'s auth store and can
  mint any token itself. The file is root-only so the token stays out of the agent's
  reach by default, not because it protects anything the agent couldn't otherwise get.

### 5.6 Status (`km t3 status <id>`)

One SSM script, marker sections (herdr pattern), parsed into a `t3BoxState`:

- `t3 path` / `t3 version` (semver regex, `herdrSemverRe` style)
- `unit active` (`systemctl is-active`) / `unit enabled` (`is-enabled`)
- `ready` (the `.well-known` curl)
- `providers` — `runuser -u sandbox -- bash -lc 'command -v claude; command -v codex'`
  (t3 is useless without one; `km agent auth` is the fix to name)
- `presence token` — file exists and non-empty (yes/no; never contents)
- `presence signal9` — `strings /opt/km/bin/km-presence | grep -q checkT3InUse`
  (the symbol, never a bare word — the herdr worktree-name false positive)

Report: `✓ t3 v0.1.0 running on <id> (127.0.0.1:3773)`, providers line, then
warnings: no signal 9 → "this box's km-presence predates signal 9: an open browser
does not keep it awake, and t3's agents keep it awake ≥30 min after every turn via
signal 5 — `km destroy && km create` to fix"; no providers → name `km agent auth`;
no presence token → "run `km t3 start` again".

### 5.7 km-presence signal 9 + signal 5 exclusion (`cmd/km-presence`)

```go
// checkT3InUse — signal 9: a client is connected OR an agent turn is running.
func checkT3InUse(r commandRunner, get httpGetter, tokenPath string) bool
```

1. **Client** (runs FIRST): `ss -tnH state established '( sport = :3773 )'`;
   non-empty → true. Port, not process name (F4: `node-MainThread`); km owns 3773.
2. **Turn**: read `tokenPath` (`/etc/km/t3/presence.token`); absent/empty → false.
   `GET http://127.0.0.1:3773/api/orchestration/shell` with the two headers,
   **2 s timeout, keep-alives disabled** (no lingering socket for step 1 to see on the
   next tick). Busy iff any `threads[i].activeRunId != null` **or**
   `len(threads[i].pendingBackgroundTasks) > 0` (background subagents are agent work
   the operator asked for; builder's call). Archived threads ignored.
3. **Fails idle** on every error path: no `ss`, server down, non-200, timeout,
   unparseable JSON. A reaped box costs `km resume`; a stuck-true signal leaks
   instances fleet-wide.

No server running ⇒ step 1 finds no socket and step 2 fails to connect ⇒ false, so
a box without t3 pays one `ss` and one refused loopback connect per tick (~ms).

**Signal 5 exclusion.** `checkAgentProcess` keeps its pgrep, then drops every pid
whose `/proc/<pid>/cgroup` contains `/km-t3code.service` (procfs root injectable for
tests). A t3-launched claude/codex is then judged *only* by signal 9, which knows
whether it is mid-turn. Without this, F5 makes signal 9's negative case meaningless.
A claude the operator runs in `km shell` or a poller dispatches is outside that cgroup
and is still caught by signal 5 exactly as today.

`tick()` gains `s9`; the doc comments/count ("eight signals") update to nine.

### 5.8 What the operator sees, end to end

```
$ km t3 start mybox
t3 not found on sb-1234 — installing v0.1.0 from GitHub releases
✓ Installed t3 v0.1.0
✓ km-t3code.service active (127.0.0.1:3773)
Next: km t3 mybox
$ km t3 mybox
✓ Forwarding localhost:3774 → sandbox:3773
Open:           http://localhost:3774/
First pairing:  http://localhost:3774/pair#token=…   (single use, expires in 5 min)
Desktop app:    Settings → Connections → Add environment → paste the pairing link
The tunnel auto-reconnects; Ctrl-C closes it. The server keeps running — km t3 stop mybox.
```

## 6. Deploy surface

- **`km t3 …`**: operator binary only — **`make build`**. Works on every running
  sandbox immediately (install + unit over SSM).
- **Signal 9 + signal 5 exclusion**: `km-presence` sidecar — **`make build` +
  `km init --sidecars`**; existing sandboxes get it only on `km destroy && km create`
  (fetched at boot) or a hot swap of `/opt/km/bin/km-presence`. Until then: an open
  browser alone does not keep the box awake, and t3's agents keep it awake ≥30 min
  after each turn via signal 5. `km t3 status` says which situation a box is in.
- No Terraform, IAM, DynamoDB, Lambda, userdata, schema, or plugin change.

## 7. Testing

**Unit (Go, both packages, fast, no AWS):**
- `parseT3Status` across all-present / absent / chatty version / not-running.
- Port constant pinned across unit text, connect forward, and km-presence (`3773`).
- Unit text: contains `User=sandbox`, `bash -lc`, `--host 127.0.0.1`,
  `UnsetEnvironment=XDG_RUNTIME_DIR`, `T3CODE_TELEMETRY_ENABLED=false`,
  `Restart=on-failure`, and does NOT contain `StandardOutput=journal` / `0.0.0.0`.
- Start script: token minted to a file and never echoed (no `cat …token`, no
  `--token-only` piped to stdout); installer URL is the pinned raw.githubusercontent tag.
- Cobra routing (parent RunE = connect; start/stop/status dispatch; flags reach the flow).
- Connect: refuses when the unit is not active; `--no-pair` sends no pairing command;
  pairing URL parsed from its marker only.
- Signal 9: socket present ⇒ true (and no HTTP call made); no socket + busy thread ⇒
  true; no socket + idle threads ⇒ false; **no socket + only idle long-lived
  sessions ⇒ false** (the load-bearing negative); token absent ⇒ false; HTTP error /
  401 / bad JSON / timeout ⇒ false; `pendingBackgroundTasks` non-empty ⇒ true.
- Signal 5 exclusion: claude pid in `km-t3code.service` cgroup ⇒ excluded; claude pid
  elsewhere ⇒ still counted; unreadable cgroup file ⇒ still counted (conservative:
  an unknown process keeps today's behaviour).

**Live UAT (real sandbox, recorded in `docs/t3code.md` § UAT):**
1. `km t3 start` on a fresh box: installs (incl. libatomic), unit active+enabled.
2. `km t3 <id>` + headless browser pairing through the forward → Connected.
3. km-presence (hot-swapped on the test box only) logs active with the browser
   attached; inactive after it closes with the server still running.
4. Drive one real agent turn from the browser: signal 9 true while `activeRunId` is
   set; false within one tick after the turn ends **while the claude child is still
   alive** (and signal 5 does not fire for it). Confirm the child's cgroup is
   `km-t3code.service`.
5. `km pause` → `km resume`: server back on its own. `km t3 stop` → resume: stays down.
6. `km t3 status` on a box with the old km-presence prints the signal 9 warning.

## 8. Left to the builder

- Exact banner wording, file split inside `cmd/km-presence` (a `t3.go` beside
  `runner.go` is fine), and test names.
- Whether `status`'s providers check also reports t3's own provider view (nice to have).
- Doc placement: `docs/t3code.md` runbook + a CLAUDE.md block + a "Where to look" row +
  the CLI list entry; no skill change, so no plugin bump.
