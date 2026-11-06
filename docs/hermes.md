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
(`km ami bake`). The UAT host list goes here once it is recorded.

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
- `start` does every operation that touches `~/.hermes` as `sandbox` (including
  `git`, which would otherwise run a repo's hooks as root). It writes as root
  only inside root-owned `/etc/km/hermes`, never on a fixed `/tmp` name.

## Deploy surface

| Piece | Ships with | Reaches existing sandboxes |
|---|---|---|
| `km hermes start/stop/status/<connect>` | `make build` | Immediately, over SSM |
| Signal 10 and the signal 5 exclusion | `make build` + `km init --sidecars` | On `km destroy && km create`, or a hot swap of `/opt/km/bin/km-presence` |

No Terraform, IAM, DynamoDB, Lambda, userdata, schema or plugin change. On a box
whose km-presence predates signal 10, `km hermes status` warns.

## Troubleshooting

| Symptom | Look at |
|---|---|
| `start` says install failed | Egress (above). `km shell <sb>` → `tail ~/.hermes/logs/install.log` |
| Not answering `/health` | `km shell <sb> --root` → `tail /home/sandbox/.hermes/km-hermes.log`, `systemctl status km-hermes` |
| Connect says the unit is not running | `km hermes start <sb>` (after `stop` it stays disabled) |
| "no model provider configured" | The WebUI onboarding wizard, or `km shell <sb>` → `hermes model` |
| Login rejected | You copied an old password. Re-run `km hermes <sb>`, or `start --rotate-password` |
| Box idled out during a cron job | Expected (above) |

## UAT

Pending. The live checklist is in the design spec, § 6. These assumptions are
marked `UAT:` in `internal/app/cmd/hermes.go`:

- `--branch main --commit <tag sha>` lands exactly on the tag.
- The agent's binary and venv paths.
- `git describe --tags` on a shallow clone.
- The WebUI dependencies are present in the agent's Python.
- The `hermes config get/set` key syntax.
- Telemetry.

Also confirm live:

- `/health` stays unauthenticated once `HERMES_WEBUI_PASSWORD` is set. Signal 10
  and the start-time readiness wait both depend on it.
- The field names are still `active_runs` / `active_streams`. If a release
  renames them, signal 10 can no longer see turns, and only an open tab keeps the
  box awake.
