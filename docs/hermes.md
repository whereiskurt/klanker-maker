# Hermes Agent + WebUI on a sandbox — `km hermes`

[Hermes Agent](https://github.com/NousResearch/hermes-agent) (Nous Research) is a
Python agent with persistent memory, skills, cron and subagents.
[Hermes WebUI](https://github.com/nesquena/hermes-webui) is a browser UI that runs
the agent **in-process** and streams its output over SSE. `km hermes` installs
both on a sandbox, runs the WebUI in the background, and connects your browser
to it over SSM. It is a sibling of `km t3`, `km vscode`, `km desktop` and
`km herdr`.

Design: `docs/superpowers/specs/2026-11-05-hermes-webui-sandbox-design.md`
(it builds on the `km t3` design, `docs/t3code.md`).

## The four verbs

```
km hermes start  <sandbox>   install agent + webui if needed and start km-hermes.service
km hermes        <sandbox>   forward localhost:8788 to it and print the login password
km hermes status <sandbox>   installed, versions, running, provider, what the idle timer sees
km hermes stop   <sandbox>   stop the server and keep it stopped across resume
```

`<sandbox>` is an id, an alias, or a `km list` number, as everywhere else.

### `km hermes start`

1. Installs `libatomic` as root if it is missing. Hermes's package manager
   needs it, and would otherwise try `sudo -n`, which an unprivileged sandbox
   cannot do.
2. Installs the pinned Hermes Agent (`v0.22.4`) as the `sandbox` user into
   `~/.hermes/hermes-agent`. It uses the agent's own `install.sh`, fetched from
   that release tag on GitHub, with `--non-interactive --skip-browser
   --skip-computer-use`, and pins the tag's commit.
3. Clones the pinned WebUI (`v0.52.544`) into `~/.hermes/hermes-webui`. Upstream
   tests these two releases as a pair, so move them together:
   `--agent-version` / `--webui-version` override either for one run.
4. Creates the login password, `/etc/km/hermes/webui.env` (root, 0600), **only if
   it is absent**. A re-run keeps it. `--rotate-password` replaces it and
   restarts the server.
5. Seeds a model provider **only if `~/.hermes/config.yaml` names none**. On a
   profile with `spec.execution.useBedrock: true` that is Bedrock, through the
   instance role (no key), with model `us.anthropic.claude-sonnet-4-6` in the
   box's region. `--model <id>` sets the default model, and is applied even
   when a provider already exists. Any other box is left for the WebUI's
   onboarding wizard, or for `hermes model` in `km shell`.
6. Writes and enables `km-hermes.service` (below), then waits for `/health`.

`start` is idempotent. Re-running it on an up-to-date box changes nothing and
does not restart the server. The first install takes a few minutes, and `start`
waits up to 20 minutes for it.

- **One `start` at a time.** A second concurrent `start` on the same box (yours
  or another analyst's) stops with "another km hermes start is running".
- **No restart over a live turn.** If `/health` shows a run or stream in
  progress, a provider seed or `--model` change is written but the restart is
  deferred. `start` says so, and the change applies on the next
  `km hermes stop && km hermes start`. `--rotate-password` is not written at all
  while busy, because the running server would reject the new password. Re-run
  it when the box is idle. An install or upgrade still restarts, because its
  files have already been replaced.
- `--model` is set only when it differs from the configured default.

### `km hermes <sandbox>` (connect)

```
✓ hermes-agent v0.22.4 + webui v0.52.544 running on sb-1234
✓ Forwarding localhost:8788 → sandbox:8787

Open:      http://localhost:8788/
Password:  …          (km hermes start sb-1234 --rotate-password to change)
Terminal:  km shell sb-1234, then `hermes`
The tunnel auto-reconnects; Ctrl-C closes it. The server keeps running — km hermes stop sb-1234.
```

- `--no-password` skips fetching the password. Use it once your browser holds
  the session cookie, which lasts 24 h.
- `--local-port` picks another local port. One local port can serve one sandbox
  at a time, because the cookie is scoped to `localhost`.
- **The server keeps running** after Ctrl-C, and a running turn keeps going.
- The `hermes` CLI (TUI, `hermes model`, `hermes setup`, `hermes config`) is
  installed for the `sandbox` user. Reach it with `km shell <sandbox>`.

### `km hermes stop`

Runs `systemctl disable --now km-hermes.service`. The server stays down across
`km pause`/`km stop` → `km resume`. Memory, skills, sessions and settings in
`~/.hermes` are kept.

## The unit

`km-hermes.service` is a system unit with `User=sandbox`, started through
`bash -lc`:

- It listens on `127.0.0.1:8787` only (`HERMES_WEBUI_HOST`, plus the explicit
  `--host`).
- `EnvironmentFile=/etc/km/hermes/webui.env` carries the password. systemd reads
  the file as root, so the agent cannot read it from disk.
- `UnsetEnvironment=XDG_RUNTIME_DIR` keeps child processes inside this unit's
  cgroup.
- `KillMode=mixed` + `TimeoutStopSec=120` let a stop drain running turns (90 s),
  as upstream's own unit does.
- `Restart=on-failure`, so `km hermes stop` sticks.
- The log is `/home/sandbox/.hermes/km-hermes.log` (sandbox, 0600), opened by the
  sandbox user's shell rather than by systemd as root.
- `bootstrap.py` runs under the WebUI's own venv
  (`~/.hermes/hermes-webui/.venv`) once it exists. Before that it runs under the
  agent's managed runtime, the first element of `hermes --print-runtime-command`
  (`~/.hermes/tools/python-3.x/bin/python3`). Never the system `python3`: AL2023's
  is 3.9 and the WebUI needs 3.10+. On its first run `bootstrap.py` builds the
  venv from PyPI and installs its own dependencies, so the first start can take a
  couple of minutes. `start` waits up to 180 s for `/health`. If no interpreter
  is found, the unit logs `km-hermes: no Python for the WebUI` and exits.

## Keeping the box awake

km-presence **signal 10 ("hermes in use")** fires on its 60 s tick when either is
true:

1. **A browser is connected:** an established socket on the sandbox's port 8787.
2. **A turn is running:** `GET http://127.0.0.1:8787/health` (public, no token)
   reports `active_runs > 0` or `active_streams > 0`. The body is read even on a
   503, because a degraded server in the middle of a run is still busy.

Every error reads as idle. A **started server nobody is using does not keep the
box awake**, for the same reason as t3, VS Code and herdr.

**Signal 5 ignores Hermes's subagents.** Hermes can start `claude` or `codex`.
Signal 5 now skips processes inside `km-hermes.service`'s cgroup, as it already
skipped `km-t3code.service`'s, and signal 10 decides for them.

**Not covered:** Hermes **cron jobs** and the messaging gateway do not keep the
box awake. An idle-reaped box misses its scheduled jobs. Use `km extend <sb>
--idle <dur>`, or keep a tab open.

## Providers, metering and secrets

- **Seeding.** The installer writes `model.provider: auto` (and an Anthropic
  default model). `start` treats `auto` as unconfigured. On a `useBedrock: true`
  profile it sets `model.provider: bedrock`, `model.default`
  (`us.anthropic.claude-sonnet-4-6`, or `--model`) and `bedrock.region`.
- **The Bedrock extra.** Bedrock needs the agent's `bedrock` provider extra (the
  `anthropic` package). The agent installs extras lazily at first use, but never
  inside the running WebUI, which holds the agent runtime's install lock. The
  first turn then fails with "The 'anthropic' package is required for the
  Bedrock provider … another Hermes process is installing dependencies". So
  whenever the provider is `bedrock` and `hermes pm status` does not list it,
  `start` stops the unit and runs `hermes pm install --extra bedrock` as
  `sandbox`, under `timeout 600`, then starts the unit. While a run is active
  this waits for the next restart, like a config change. `km hermes status`
  warns when the provider is `bedrock` and the extra is missing.
- **No update banner.** Upstream's WebUI checks for agent updates and offers
  "Update Now". That button would move the agent to `origin/main`, off the pinned
  release pair the WebUI was tested with. The WebUI writes all its defaults into
  `~/.hermes/webui/settings.json` on its first run, so the key is always there.
  `start` therefore decides once: if `~/.hermes/webui/.km-update-check-disabled`
  is absent, it sets `"check_for_updates": false` and then writes that marker.
  After that it never touches the key, so an operator who turns the check back
  on keeps it. Delete the marker to have `start` turn it off again. Upgrade with
  `km hermes start <sb> --agent-version X --webui-version Y`, a pair that
  upstream released together.
- **Bedrock** goes to `bedrock-runtime.*.amazonaws.com`. km's http-proxy already
  meters that host under `useBedrock: true`, so Hermes spend shows in
  `km status` / `km otel`.
- **Anthropic** can borrow Claude Code's login, if `km agent auth <sb> --claude`
  ran. **Codex/ChatGPT** can import `~/.codex/auth.json`. Both are chosen in
  `hermes model` or the onboarding wizard.
- **Brokered secrets (`spec.secrets`) do not reach the in-process agent.** The
  WebUI is a Python server, not a shimmed binary. Use Bedrock (no secret), or
  log the provider in on the box.
- The server and anything it runs are uid `sandbox`, so the profile's egress
  enforcement applies to them (Phase 135 predicate).

## Egress

Installing pulls from more than GitHub. The agent's installer and package
manager fetch uv, a Python runtime, PyPI packages and frontend build tools.
`base/network/locked` does not allow these, so `start` on a locked box fails with
"install failed … a locked egress allowlist blocks them".

**Recipe:** install once on an open box, then read the hosts it reached with
`km shell <sb> --root` → `km-netpolicy observed`. Add those suffixes to your
locked profile's `allowedDNSSuffixes`. Or bake an AMI from the installed box
(`km ami bake`).

Recorded on the UAT box (`km-netpolicy observed`, boot traffic filtered out):

| Host | Why |
|---|---|
| `github.com`, `api.github.com`, `raw.githubusercontent.com`, `release-assets.githubusercontent.com` | the installer, the agent and WebUI checkouts, uv and tool releases |
| `releases.astral.sh` | uv's Python downloads |
| `pypi.org`, `files.pythonhosted.org` | the agent's and the WebUI's Python packages |
| `nodejs.org`, `registry.npmjs.org` | the agent's TUI and web frontend build |
| the AL2023 package repos (`al2023-repos-*.s3.*.amazonaws.com`) | `libatomic` |

As suffixes: `.github.com`, `.githubusercontent.com`, `.astral.sh`,
`.pypi.org`, `.pythonhosted.org`, `.nodejs.org`, `.npmjs.org`, plus the OS repo.

`libatomic` on a locked box fails exactly as for t3 (`docs/t3code.md` § Locked
egress profiles).

## Security notes

- Loopback only: nothing new becomes reachable from the box.
- **A password is always set.** While the forward is up, anything on your laptop
  can reach `localhost:8788`, and the UI drives an agent with a shell on the
  box.
- **The password crosses SSM output** when `km hermes <sb>` prints it. Anyone
  with `ssm:GetCommandInvocation` can read it. Those are operators, who can
  already `km shell --root`. `start` and `status` never print it.
- The running server's environment holds the password and is readable by uid
  `sandbox`. That is not a boundary, the same posture as t3's presence token.
- **Telemetry is off by default upstream** (`telemetry.shared_metrics.enabled:
  false`, `send: false` in the installer's config). km sets nothing. An operator
  who turns it on in `~/.hermes/config.yaml` sends usage metrics to Nous Research.
- `start` does every operation that touches `~/.hermes` as `sandbox` (including
  `git`, which would otherwise run a repo's hooks as root). It writes as root
  only inside root-owned `/etc/km/hermes`, never on a fixed `/tmp` name.

## Deploy surface

| Piece | Ships with | Reaches existing sandboxes |
|---|---|---|
| `km hermes start/stop/status/<connect>` | `make build` | Immediately, over SSM |
| Signal 10 and the signal 5 exclusion | `make build` + `km init --sidecars` | On `km destroy && km create`, or a hot swap of `/opt/km/bin/km-presence` |
| The http-proxy SigV4 `Accept-Encoding` fix (needed for Bedrock) | `make build` + `km init --sidecars` | On `km destroy && km create`, or a hot swap of `/opt/km/bin/km-http-proxy` + `systemctl restart km-http-proxy` |

No Terraform, IAM, DynamoDB, Lambda, userdata, schema or plugin change. On a box
whose km-presence predates signal 10, `km hermes status` warns. On a box whose
http-proxy predates the fix, every Hermes turn on Bedrock fails with a 403
"signature … does not match".

## Troubleshooting

| Symptom | Look at |
|---|---|
| `start` says install failed | Egress (above). `km shell <sb>` → `tail ~/.hermes/logs/install.log` |
| Not answering `/health` | `km shell <sb> --root` → `tail /home/sandbox/.hermes/km-hermes.log`, `systemctl status km-hermes` |
| Connect says the unit is not running | `km hermes start <sb>` (after `stop` it stays disabled) |
| "failing to start (state: activating/failed)" | The error carries the log tail. `km hermes status <sb>` shows it again. `km-hermes: no Python` means no WebUI venv and no agent runtime |
| "no model provider configured" | The WebUI onboarding wizard, or `km shell <sb>` → `hermes model` |
| Login rejected | You copied an old password. Re-run `km hermes <sb>`, or `start --rotate-password` |
| Box idled out during a cron job | Expected (above) |

## UAT

First live run 2026-11-05 (AL2023, unprivileged; agent and WebUI installed in
3.5 min). Confirmed:

- `--branch main --commit <tag sha>` lands on the tag (`Hermes Agent v0.22.4`).
- Agent v0.22 has no venv. Its runtime is PM-managed under `~/.hermes/tools/`,
  and the WebUI builds its own venv (see § The unit).
- `hermes config get model.provider` prints `auto` on a fresh install.
- Telemetry is off by default.

Second run 2026-11-05 (upgrade in place): the unit came up, Bedrock was seeded
over `auto`, browser login worked, `/health` stays public with a password set,
and `/` redirects to `/login`. Two findings, both fixed in `start`:

- **The first Bedrock turn failed**: the `anthropic` package was missing, and the
  agent's lazy install always refuses inside the running server, which holds the
  runtime lock. `hermes pm install --extra bedrock` with the unit stopped fixes
  it in under a second. `start` now does this before starting the unit (§
  Providers).
- **The WebUI offered "5302 updates available — Update Now"** for the agent
  (`origin/main`). `start` now turns `check_for_updates` off (§ Providers).

Third run 2026-11-05: a real Bedrock turn works end to end (with the http-proxy
SigV4 fix). Confirmed: `hermes pm status` lists `"feature_list": ["all",
"bedrock"]`, and `git describe --tags` on the shallow clone returns `v0.52.544`.
One finding: the banner was still showing, because the WebUI persists
`"check_for_updates": true` among its defaults, so "set only when absent" never
fired. `start` now sets it once and records that with a marker (§ Providers).

Fourth run 2026-11-06: the update banner is gone. `check_for_updates: false`
and the marker survive a further server restart, so the WebUI does not write its
in-memory settings back over the edit.

**Live checklist, all passing:**

| # | Check | Result |
|---|---|---|
| 1 | `status` on a box that never ran Hermes | ✓ "not installed — run `km hermes start`", exit 1 |
| 2 | `start` on a fresh unprivileged AL2023 box (installs `libatomic`, agent, WebUI) | ✓ about 3.5 min; agent `v0.22.4`, WebUI `v0.52.544` |
| 3 | `start` re-run over a crash-looping unit (upgrade in place) | ✓ unit up, Bedrock seeded over `auto` |
| 4 | Connect, then log in in a headless browser through the forward | ✓ `/` redirects to `/login`, and the password logs in |
| 5 | A real turn on Bedrock (instance role, through km's proxy) | ✓ `PONG` in about 1 s |
| 6 | AI spend in `km status` | ✓ `bedrock_tokens_metered` per call. Prompt-cache tokens are not counted on the Bedrock path (pre-existing, all clients) |
| 7 | Signal 10, server running, idle forward, no tab | ✓ `active=false` |
| 8 | Signal 10 with a tab open | ✓ `active=true` on 2 ticks, then `false` one tick after closing |
| 9 | Signal 10 with a 150 s turn running and **no tab** | ✓ `active=true` for its whole run, `false` on the next tick |
| 10 | `km pause` → `km resume` (hibernate) | ✓ server up; the forward reconnected by itself |
| 11 | `km stop` → `km resume` (cold boot) | ✓ unit came back on its own |
| 12 | `km hermes stop` | ✓ `disabled` + `inactive`; `status` exits 1 and names `start` |
| 13 | `status` on a box whose km-presence predates signal 10 | ✓ warns, and the warning goes away after the hot swap |
| 14 | A second connect while one is running | ✓ "local port 8788 is already in use" |

**Found in km itself, not Hermes:** km's http-proxy dropped the client's
`Accept-Encoding` (goproxy's default), so any Python Anthropic SDK call to
Bedrock through the proxy failed SigV4 with a 403. That SDK signs
`accept-encoding`; botocore and Claude Code do not. Fixed in
`sidecars/http-proxy` (`accept_encoding.go`). The header is kept only when
SigV4 signs it, and metering decodes a compressed copy.

Still marked `UAT:` in `internal/app/cmd/hermes.go`:

- Whether the server re-reads `settings.json`. `start` restarts it after a
  change, in case it caches.

Also confirm live:

- `/health` stays unauthenticated once `HERMES_WEBUI_PASSWORD` is set. Signal 10
  and the start-time readiness wait both depend on it.
- The field names are still `active_runs` / `active_streams`. If a release
  renames them, signal 10 can no longer see turns, and only an open tab keeps the
  box awake.
