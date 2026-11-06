# `km hermes` — run Hermes Agent + Hermes WebUI in a sandbox, in the background

**Date:** 2026-11-05
**Status:** Design — operator asked for "hermes and hermes-ui, like t3"; builder-checked live in UAT
**Model:** `km t3` (`docs/superpowers/specs/2026-10-17-t3code-sandbox-design.md`). Read it
first: this spec only states where Hermes differs. Every t3 rule not contradicted
here applies verbatim (system unit `User=sandbox`, `bash -lc`, loopback only, log
opened by the sandbox user's shell, no root file op on sandbox-controlled paths,
detect the use never the server, fail idle, `runReconnectingPortForward`).

## 1. Summary

[Hermes Agent](https://github.com/NousResearch/hermes-agent) (Nous Research) is a
Python agent with persistent memory, skills, cron and subagents.
[Hermes WebUI](https://github.com/nesquena/hermes-webui) is a browser UI that runs
the agent **in-process** (no separate agent server), streaming over SSE.

```
km hermes start  <sandbox-id>   install agent + webui if absent/stale, start km-hermes.service
km hermes stop   <sandbox-id>   disable --now (stays down across resume)
km hermes status <sandbox-id>   installed? versions? running? provider? signal 10?
km hermes        <sandbox-id>   reconnecting SSM forward localhost:8788 → box:8787 + print the login password
```

The `hermes` CLI itself (TUI, `hermes model`, `hermes setup`) is installed by
`start` and reachable from `km shell <id>` as the sandbox user — no extra verb.

Three things ship together, as with t3:

1. `km hermes …` — operator binary, over SSM. Works on running boxes; no userdata,
   schema, Lambda, IAM or recreate.
2. `km-hermes.service` — km-owned system unit, `User=sandbox`.
3. km-presence **signal 10 ("hermes in use")** + extending the **signal 5 cgroup
   exclusion** to `km-hermes.service` (Hermes can spawn claude/codex as subagents).

## 2. Facts (source-read 2026-11-05; **[uat]** = builder must confirm live)

- **H1 Version pair.** WebUI and agent must be pinned together ("treat as a release
  pair", `docs/docker.md` § Compatibility). Pin **webui `v0.52.544`** (2026-10-22, the
  latest non-`exp-` tag) with **agent `v0.22.4`** (2026-10-21, the last agent release
  before it). Go consts `hermesAgentVersion`, `hermesWebUIVersion`; `start --agent-version`
  / `--webui-version` override per run. **[uat]** confirm the pair boots and chats.
- **H2 Agent install.** `scripts/install.sh` (fetch it from the **pinned tag** on
  raw.githubusercontent.com, never `hermes-agent.nousresearch.com/install.sh`):
  `HERMES_HOME=~/.hermes`, checkout `~/.hermes/hermes-agent`, pins its own uv 0.13.5
  from GitHub releases (SHA-checked), boots a Python, then its `pm` builds the
  runtime + TUI/web frontend (may fetch Node + PyPI). Flags used:
  `--non-interactive --skip-browser --skip-computer-use` and a version selector —
  `--branch`/`--commit <SHA>`, or `--channel stable` (NOT pinned; don't use).
  **[uat]** pick the form that lands exactly on tag `v0.22.4` (likely `--commit` of the
  tag's SHA, resolved by `git ls-remote` inside the script or a Go const SHA) and that
  `hermes --version` reports it. It edits `~/.bashrc`/`~/.profile` to add
  `~/.local/bin` — harmless (the Phase 133 PATH hooks re-assert `/opt/km/shims`).
- **H3 libatomic.** Same as t3 F2: PM installs `libatomic1` via `sudo -n`, which an
  unprivileged sandbox cannot do. `start` does the root step itself first (reuse t3's
  script fragment/marker → same named error on locked egress).
- **H4 WebUI install.** `git clone --depth 1 --branch v0.52.544
  https://github.com/nesquena/hermes-webui.git ~/.hermes/hermes-webui` (re-clone when
  the checked-out tag differs). Its deps are only `pyyaml` + `cryptography`; it must run
  under the **agent's** Python (`HERMES_WEBUI_PYTHON`, auto-discovered from the agent
  dir). **[uat]** whether the agent env already has both (if not:
  `HERMES_WEBUI_AUTO_INSTALL=1` or a pip into that env, as the sandbox user).
- **H5 Server shape.** `bootstrap.py --foreground --no-browser --host 127.0.0.1 8787`
  (foreground also auto-detected under systemd via `INVOCATION_ID`; it `execv`s into
  `server.py`, so the unit tracks the server). Default bind 127.0.0.1:8787. Upstream's
  own unit uses `KillMode=mixed` and `TimeoutStopSec=120` (90 s run drain + margin) —
  adopt both. State in `~/.hermes/webui/`.
- **H6 `/health` is public and reports work.** `api/auth.py` `PUBLIC_PATHS` includes
  `/health`; `_handle_health` returns `{status, active_streams, active_runs, runs[],
  last_run_finished_at, …}` and **503** when degraded. So signal 10 needs **no token**
  (simpler than t3 §5.5).
- **H7 Auth.** Off by default. `HERMES_WEBUI_PASSWORD` enables it (signed HttpOnly
  cookie, 24 h). Everything on the operator's laptop can reach `localhost:8788` while the
  forward is up and the UI drives an agent with shell access on the box, so **km always
  sets a password** (§3.3).
- **H8 Providers.** Bedrock uses the boto3 chain → the instance role, keyless:
  ```yaml
  model: {provider: bedrock, default: us.anthropic.claude-sonnet-4-6}
  bedrock: {region: <box region>}
  ```
  Traffic goes to `bedrock-runtime.*.amazonaws.com`, which km's http-proxy already
  MITM-meters under `useBedrock: true`, so spend lands in `km status`. Anthropic can
  borrow Claude Code's credential files if present; Codex OAuth imports `~/.codex/auth.json`.
- **H9 Egress.** Install needs github.com, objects/raw.githubusercontent.com,
  api.github.com, plus whatever `pm` fetches (PyPI `pypi.org`/`files.pythonhosted.org`,
  uv's Python downloads, possibly `nodejs.org`/npm). **Not** inside `base/network/locked`.
  **[uat]** record the real host list from `km-netpolicy observed` after one install and
  put it in the doc as the allowlist recipe. A locked box fails `start` with a named error
  pointing at that recipe (no S3 mirror — non-goal, like t3).
- **H10 Telemetry.** **[uat]** grep both repos for telemetry/analytics and disable via env
  if any exists; record the answer either way.

## 3. Design deltas from t3

### 3.1 Command surface (`internal/app/cmd/hermes.go`)

Mirror `t3.go` (parent RunE = connect; `start|stop|status`; `SandboxFetcher`,
`ShellExecFunc`, `SSMSendAPI` seams via `resolveVSCodeDeps`; `ResolveSandboxID`;
`SilenceUsage`; one positional; own `hermesConnectPrep` — no SSH key, no ssh-config).
Register in `root.go` beside `NewT3Cmd`.

| Const | Value | Why |
|---|---|---|
| `hermesRemotePort` | 8787 | upstream default; pinned across unit, forward, km-presence by test |
| `hermesDefaultLocalPort` | 8788 | 8787 may host a local webui; avoids 2222/2223/2224/3774/8001/8444/1455/1457 |
| `hermesUnitName` | `km-hermes.service` | |

Add 8788 wherever 3774 was added for the operator container (`EXPOSE`, the
`KM_PUBLISH_PORTS` default) — grep for `3774`.

### 3.2 The unit

```ini
[Unit]
Description=Hermes WebUI + Agent (km hermes)
After=network-online.target

[Service]
User=sandbox
Group=sandbox
WorkingDirectory=/workspace
EnvironmentFile=/etc/km/hermes/webui.env
Environment=HERMES_HOME=/home/sandbox/.hermes
Environment=HERMES_WEBUI_HOST=127.0.0.1
Environment=HERMES_WEBUI_PORT=8787
Environment=HERMES_WEBUI_DEFAULT_WORKSPACE=/workspace
UnsetEnvironment=XDG_RUNTIME_DIR
ExecStart=/bin/bash -lc 'umask 077; L=/home/sandbox/.hermes/km-hermes.log; …same symlink-safe log dance as t3…; cd /home/sandbox/.hermes/hermes-webui && exec python3 bootstrap.py --foreground --no-browser --host 127.0.0.1 8787 >>"$$L" 2>&1'
KillMode=mixed
TimeoutStopSec=120
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

- `EnvironmentFile` is read by systemd **as root** before dropping to `sandbox`, so the
  file stays `root:root 0600` in a root-owned dir `/etc/km/hermes` (0700) — the agent
  cannot read the password from disk. (The running server's environ is readable by uid
  `sandbox`; that is not a boundary and the doc says so — same posture as t3's token.)
- **[uat]** which python `bootstrap.py` must run under (may need the agent venv's python,
  or `HERMES_WEBUI_PYTHON=`); fix the ExecStart accordingly and pin it in the unit test.
- Same pinned unit-text test as t3: contains `User=sandbox`, `bash -lc`, `127.0.0.1`,
  `UnsetEnvironment=XDG_RUNTIME_DIR`, `Restart=on-failure`, `KillMode=mixed`,
  `EnvironmentFile=/etc/km/hermes/webui.env`; never `0.0.0.0`, never `StandardOutput=journal`.

### 3.3 Password

`start` creates `/etc/km/hermes/webui.env` **only if absent** (stable across restarts
and analysts): `HERMES_WEBUI_PASSWORD=<32 url-safe random chars from /dev/urandom>` plus
`HERMES_WEBUI_COOKIE_NAME=km_hermes_session` (distinct from a local webui's cookie on
`localhost`). Created with `umask 077`, `install -d -m 0700 -o root -g root`, written via
temp + `mv` inside that root dir — no fixed `/tmp` name. `start --rotate-password`
rewrites it and restarts the unit. **The password is never printed by `start`**; only
`km hermes <id>` prints it (one SSM round trip, like the t3 pairing link), and `--no-password`
suppresses it. The doc states it crosses SSM command output (visible to anyone with
`ssm:GetCommandInvocation`, i.e. operators who can already `km shell --root`).

### 3.4 Provider seeding

After install, as the sandbox user, **only when `~/.hermes/config.yaml` has no
`model.provider`** (never clobber an operator's choice): if
`/opt/km/.km-profile.yaml` shows `useBedrock: true` (grep the `execution` block with awk
block-scoping, not a bare grep — see memory: skill bash), run `hermes config set
model.provider bedrock`, `model.default us.anthropic.claude-sonnet-4-6`, `bedrock.region
<IMDS region>`. Otherwise leave it; the WebUI onboarding wizard (or `hermes model` in
`km shell`) handles it, and `status` says "no provider configured". `start --model <id>`
overrides the default model. **[uat]** `hermes config set` key syntax.

### 3.5 Connect (`km hermes <id>`)

One SSM round trip: unit `active` (else error naming `km hermes start <id>`), `/health`
ok, and (unless `--no-password`) the password under a marker. Banner:
```
✓ Forwarding localhost:8788 → sandbox:8787
Open:      http://localhost:8788/
Password:  <pw>          (km hermes start <id> --rotate-password to change)
Terminal:  km shell <id>, then `hermes`
The tunnel auto-reconnects; Ctrl-C closes it. The server keeps running — km hermes stop <id>.
```
then `runReconnectingPortForward(… buildPortForwardCmd(…, "8788", "8787"), httpTunnelProbe(lp), …)`.

### 3.6 Status

Markers parsed into `hermesBoxState`: agent path + version, webui tag (`git -C … describe
--tags`), unit active/enabled, `/health` (status, active_runs), provider (`hermes config
get model.provider`), password file present (yes/no), libatomic, signal 10 present
(`strings /opt/km/bin/km-presence | grep -q checkHermesInUse` — the symbol). Warnings
mirror t3's (no signal 10 → recreate; no provider → name `hermes model` / the wizard).
Non-zero exit when not installed or not running.

### 3.7 km-presence signal 10 (`cmd/km-presence/hermes.go`)

```go
// checkHermesInUse — signal 10: a browser is connected OR a run/stream is active.
func checkHermesInUse(r commandRunner, get httpGetter) bool
```

1. `ss -tnH state established '( sport = :8787 )'` non-empty → true (no HTTP made).
2. `GET http://127.0.0.1:8787/health`, 2 s timeout, keep-alives off; true iff
   `active_runs > 0 || active_streams > 0`. Parse the body even on 503 (degraded but
   busy is still busy). **[uat]** confirm an SSE-open-but-idle tab is caught by step 1
   and that `active_streams` drops to 0 when a turn ends.
3. Fails idle on every error.

Reuse t3's `httpGetter` seam. `tick()` gains `s10`; comments say "ten signals".
**Signal 5 exclusion** generalises from one unit to a list
(`km-t3code.service`, `km-hermes.service`) — same `/proc/<pid>/cgroup` test, same
conservative unreadable-file rule; add a test per unit.

Known, documented gap: Hermes **cron jobs** do not hold the box awake; an idle-reaped box
misses them (`km extend --idle`, or keep a tab open). Same for the messaging gateway,
which km does not start.

## 4. Non-goals

The Hermes messaging gateway (`hermes gateway`; km has its own Slack/GitHub/H1
bridges), Nous Portal / Tool Gateway login, a profile fragment
(`profiles/base/tools/hermes.yaml`, cheap follow-up), an S3 mirror, the Hermes desktop
app, passkeys/OIDC, Phase 133 brokered secrets for the in-process agent (the server is
Python, not a shimmed binary; documented limitation — Bedrock needs no secret).

## 5. Deploy surface

- `km hermes …`: **`make build`** — works on every running box over SSM.
- Signal 10 + exclusion: **`make build` + `km init --sidecars`**; existing boxes on
  recreate or a hot swap of `/opt/km/bin/km-presence`.
- No Terraform, IAM, DynamoDB, Lambda, userdata, schema or plugin change.

## 6. Testing

Unit (no AWS): status parser (all present / absent / chatty versions / not running /
no provider); port pinned across the three sites; unit text; start script (password
never echoed, created only-if-absent, installer URLs are the pinned tags, provider seeding
gated on absent `model.provider`); Cobra routing + flags; connect refuses inactive unit,
`--no-password` omits the password command; signal 10 — socket ⇒ true w/o HTTP;
`active_runs>0` ⇒ true; `active_streams>0` ⇒ true; 503-with-busy-body ⇒ true;
all-zero ⇒ false (**the load-bearing negative**); HTTP error / bad JSON / timeout ⇒ false;
signal 5 excludes both units, still counts others.

Live UAT (recorded in `docs/hermes.md` § UAT): fresh box `start` (incl. libatomic) →
`connect` + browser login → one Bedrock turn appears in `km status` AI spend →
signal 10 true with tab open / during a run, false after close → pause/resume keeps it
up, `stop` keeps it down → `status` on an old km-presence warns → `km-netpolicy observed`
host list captured.

## 7. Docs

`docs/hermes.md` runbook; CLAUDE.md block + "Where to look" row + CLI list entries;
no skill change → no plugin bump.
