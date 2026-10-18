# T3 Code on a sandbox — `km t3`

[T3 Code](https://github.com/pingdotgg/t3code) is a control surface for coding
agents: a server (`t3 serve`) that drives Claude Code, Codex and others on the
machine it runs on, and a web / desktop client that talks to it. `km t3` runs that
server on a sandbox, in the background, and connects your browser to it over SSM.

Design: `docs/superpowers/specs/2026-10-17-t3code-sandbox-design.md`.

## The four verbs

```
km t3 start  <sandbox>   install t3 if needed and start the server in the background
km t3        <sandbox>   forward localhost:3774 to it and print a pairing link
km t3 status <sandbox>   installed, running, providers, what the idle timer sees
km t3 stop   <sandbox>   stop the server and keep it stopped across resume
```

`<sandbox>` is an id, an alias, or a `km list` number, as everywhere else.

### `km t3 start`

1. Installs `libatomic` as root if it is missing. The t3 binary will not run
   without it on Amazon Linux 2023.
2. Installs the pinned t3 release (`0.1.0`) as the `sandbox` user into
   `~/.t3/runtime` and `~/.local/bin/t3`. It uses t3's own installer, fetched from
   the release tag on GitHub, and that installer verifies the release checksums.
   `--version 1.2.3` installs a different release.
3. Writes and enables `km-t3code.service`: `t3 serve --host 127.0.0.1 --port 3773`,
   running as `sandbox` under a login shell, with telemetry off.
4. Waits for the server to answer, then mints the read-only token km-presence uses
   (see [Keeping the box awake](#keeping-the-box-awake)).

`start` is idempotent. On a box already running the same version it only makes
sure the server is up. Installing takes about 30 s the first time.

### `km t3 <sandbox>` (connect)

```
✓ t3 v0.1.0 running on sb-1234
✓ Forwarding localhost:3774 → sandbox:3773

Open:            http://localhost:3774/
First time here: http://localhost:3774/pair#token=…
                 (single use, expires in 5 minutes)
Desktop app:     Settings → Connections → Add environment → paste the pairing link
```

- **First time in a browser:** open the pairing link. On later visits, open the
  plain URL, or pass `--no-pair` so no link is minted.
- **The desktop app:** use **Add environment** with the same pairing link while
  `km t3 <sandbox>` is running. The app's built-in **SSH** mode is not supported.
  It downloads and may start its own server rather than km's. If you point it at
  the `km-<id>` host that `km vscode` writes, it may work, but it runs outside
  km-t3code.service and outside the idle signal.
- **One local port is one environment.** To connect two sandboxes at once, give
  the second one `--local-port 3775`.
- **The forward reconnects** if it drops. Ctrl-C closes it. **The server keeps
  running** after Ctrl-C, and t3's agents keep working with nobody attached.

Only SSM reaches the server, because it listens on the sandbox's `127.0.0.1`. So
the phone app, T3 Connect, LAN or Tailscale pairing and app.t3.codes are not
supported, by design. km never runs `t3 connect`.

### `km t3 stop`

Runs `systemctl disable --now km-t3code.service`. The server stays down after a
`km pause`/`km stop` → `km resume`. Threads, settings and paired sessions in
`~/.t3` are kept, and the next `km t3 start` picks them up.

## Keeping the box awake

km-presence **signal 9 ("t3 in use")** fires on a 60 s tick when either of these
is true:

1. **A client is connected:** there is an established socket on the sandbox's
   port 3773. An idle SSM forward holds none, and an open browser tab holds at
   least one (both measured live). The match is by port, not by process name,
   because t3's server shows up as `node-MainThread`.
2. **A turn is running:** km-presence calls `GET /api/orchestration/shell` with
   the read-only token, and the box is busy if any thread has an active run or
   pending background tasks (subagents).

A **started server that nobody is using does not keep the box awake.** That is
deliberate. If it did, a forgotten `km t3 start` would disable idle teardown for
the box's whole TTL. The same reasoning made VS Code and herdr detect the use,
never the server.

**Signal 5 ignores t3's agents.** t3 keeps a `claude` child per provider session,
and one shared `codex app-server`, alive for at least 30 minutes after a turn. It
holds them for up to ~4.5 h while it thinks background work is pending. Signal 5
used to count any process named claude or codex, which would have held the box
awake long after the work ended. It now skips processes inside
`km-t3code.service`'s cgroup, and signal 9 decides for those. A `claude` you run
in `km shell`, or one a poller dispatches, is counted exactly as before.

Two consequences, the same tradeoff VS Code and the desktop make:

- A browser tab left open keeps the box awake. `spec.lifecycle.ttl` is still the
  backstop.
- A turn running with nobody watching keeps the box awake until it finishes.

### The presence token

- **How it's minted:** `start` runs
  `t3 auth session issue --scope orchestration:read --label km-presence --ttl 365d`
  and writes the token straight into `/etc/km/t3/presence.token` (root, 0600). It
  is never printed, so it never appears in SSM command output.
- **Rotation:** each `start` issues a new token and then revokes the older
  `km-presence` sessions.
- **What it can do:** read the thread list. It cannot start, stop or steer a
  thread.
- **Why the root-only file is not a boundary:** the sandbox user owns t3's auth
  store and could mint any token itself. Keeping the file root-only only keeps the
  token out of the agent's way by default.

## Agents, secrets and enforcement

t3 finds `claude` and `codex` on `PATH`. The unit starts `t3 serve` through
`bash -lc`, so `/etc/profile.d` applies:

- On a `spec.secrets.sopsFile` profile, `/opt/km/shims` is first on PATH, so t3's
  agents get their brokered secrets like every other agent.
- The server and its agents run as uid `sandbox`, so the Phase 135 eBPF predicate
  enforces the profile's egress allowlist on them.

The unit also unsets `XDG_RUNTIME_DIR`. Otherwise t3 wraps each agent in a
`systemd-run --user` scope, outside both this unit and km's view.

**t3's onboarding offers to install its own managed Codex** into `~/.t3`. That
copy is not on PATH and so skips the shims. Choose "Use existing CLI" instead.

The agents must be logged in, which t3 does not do for you. Use
`km agent auth <sandbox> --claude` (or `--codex`), or a profile with Bedrock or
brokered API keys. `km t3 status` lists the agents it finds on PATH.

## Locked egress profiles

On a profile with an allowlist like `base/network/locked`:

- **Installing t3** works. It needs only `github.com` and
  `objects.githubusercontent.com` / `raw.githubusercontent.com`, which `locked`
  allows.
- **Installing `libatomic`** fails. The OS package repos (`cdn.amazonlinux.com`,
  the Ubuntu archive) are not in `locked`, and the DNS allowlist applies to root
  too. `start` stops with:

  > libatomic is missing on sb-… and the OS package repos are unreachable from it …

  **Fix:** add the repo host to `spec.network.egress.allowedDNSSuffixes` and
  recreate, or use an AMI that already has `libatomic` (`km ami bake`).
  `initCommands` is not a workaround, because it runs after network enforcement
  is set up.

At runtime t3 also fetches a model list from `raw.githubusercontent.com`, and
falls back to a bundled copy when it can't reach it.

## Security notes

- The server listens on `127.0.0.1` only, and nothing new becomes reachable from
  the box.
- **The pairing link crosses SSM.** `km t3 <sandbox>` mints the link in an SSM
  command, and SSM keeps command output, so anyone who can call
  `ssm:GetCommandInvocation` on the account can read it. Those are operators, who
  can already `km shell --root`. The link is single-use and expires in 5 minutes.
  km never logs it.
- **The server log** is `/home/sandbox/.t3/km-t3code.log`, owned by `sandbox` with
  mode 0600. It does not go to the journal, because t3 prints a pairing token at
  startup.
  - The redirect is opened in the sandbox user's own shell, not by systemd's
    `StandardOutput=append:`, which opens as root. `~/.t3` is sandbox-controlled,
    so a planted symlink would aim a root open at any file on the box.
  - For the same reason, `km t3 start` never does a root file operation inside
    `~/.t3` or on a fixed `/tmp` name.
- **Telemetry is off** (`T3CODE_TELEMETRY_ENABLED=false`) on the server side. The
  web client's own analytics run in your browser, not on the sandbox.

## Deploy surface

| Piece | Ships with | Reaches existing sandboxes |
|---|---|---|
| `km t3 start/stop/status/<connect>` | `make build` (operator binary) | Immediately: install and unit go over SSM |
| Signal 9 and the signal 5 exclusion | `make build` + `km init --sidecars` (km-presence sidecar) | Only on `km destroy && km create`, because km-presence is fetched at boot |

No Terraform, IAM, DynamoDB, Lambda, userdata, profile schema or plugin change.

On a sandbox whose km-presence predates signal 9, `km t3 status` warns. On such
a box:

- An open browser alone does not keep it awake.
- t3's agents keep it awake for at least 30 minutes after each turn, through the
  old signal 5.

## Troubleshooting

| Symptom | Look at |
|---|---|
| `start` reports "not answering yet" | `km shell <sb> --root` → `tail /home/sandbox/.t3/km-t3code.log`, `systemctl status km-t3code` |
| Connect says the unit is not running | `km t3 start <sb>`. After `km t3 stop` the unit stays disabled, even across resume. |
| Browser shows the wrong sandbox | One local port is one environment. Use `--local-port` per sandbox. |
| "local port 3774 is already in use" | Another `km t3` or forward holds it. Pass `--local-port`. |
| Turns fail at once | The agent is not logged in. `km agent auth <sb> --claude` |
| Box idled out while a turn ran with no browser | `km t3 status`: check for the "without signal 9" or "no presence token" warning |

Revoke browser sessions on the box with `t3 auth session list` and
`t3 auth session revoke <id>`, run as `sandbox`.

## UAT (2026-10-17)

Live, on AL2023 sandboxes from `profiles/herdr.yaml`, plus a Bedrock-enabled
variant. km-presence was hot-swapped onto the test boxes only; nothing was
uploaded to `sidecars/`.

| # | Check | Result |
|---|---|---|
| 1 | `km t3 status` on a box that never ran t3 | ✓ "not installed — run `km t3 start`" |
| 2 | `km t3 start` on a fresh box (installs `libatomic`, then t3 0.1.0, then the unit) | ✓ running, enabled, providers listed, presence token minted |
| 3 | `km t3 start` re-run on an up-to-date box | ✓ no reinstall; one `km-presence` session kept, the older one revoked |
| 4 | `km t3 <id>`: banner, pairing link, headless Chromium pairs through the forward | ✓ Connected at `http://localhost:3774/` |
| 5 | Signal 9 with a browser attached (no turn) | ✓ `active=true` on consecutive ticks |
| 6 | Signal 9 after the browser closes, forward and server still up | ✓ `active=false` within one tick |
| 7 | A real Claude turn through t3. The `claude` child is `/usr/bin/claude` from PATH, in `0::/system.slice/km-t3code.service` | ✓ |
| 8 | **The load-bearing negative.** The turn has finished, t3's `claude` child is still alive, no client is attached | ✓ `active=false` on two ticks (signal 5 skips it, signal 9 reads idle) |
| 9 | `km stop` → `km resume` (cold boot) | ✓ server back on its own |
| 10 | `km t3 stop`, then `km stop` → `km resume` | ✓ stays stopped |
| 11 | Presence token from the `--json` issue path against the live endpoint | ✓ HTTP 200 |
| 12 | Upgrade from a root-owned 0644 log left by an earlier build | ✓ replaced by `sandbox` 0600, unit active |

**Not observed live:** a turn still running with no client attached, which is
signal 9's HTTP path reading busy. On the test install, every t3 turn through
Claude-on-Bedrock failed at once: Bedrock rejects t3's model ids with
`400 The provided model identifier is invalid` for both `claude-sonnet-5` and
`claude-sonnet-4-6`. Plain `claude -p` with the default model worked on the same
box. What is covered instead:

- the live endpoint (200 with the real token, and the real response shape parsed)
- unit tests for the busy shapes (`activeRunId` set, pending background tasks)

To close the gap, run a long turn with a subscription-logged-in claude
(`km agent auth`), close the browser, and watch for `active=true`.

**Found and fixed during UAT:**

- **The server log was world-readable.** systemd's `append:` created it as root
  0644, and it contains t3's startup token. The redirect is now opened by the
  sandbox user's own shell.
- **The review found two privilege escalations** in that first version. Root
  touched or chowned a path inside sandbox-owned `~/.t3`, and staged the unit
  under a fixed `/tmp` name. Both are fixed and pinned by tests.

**Learned:**

- **t3's onboarding wizard offers to install its own managed Codex** into
  `~/.t3`. That copy does not come from PATH, so on a `sopsFile` profile it
  bypasses the brokered-secrets shim. Use the Codex on PATH ("Use existing CLI").
- **t3's model ids are Anthropic API ids.** On a Bedrock-only profile, pick a
  model your Bedrock account maps, or log claude in with a subscription.
