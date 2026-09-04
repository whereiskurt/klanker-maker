# `km herdr` — persistent remote attach for sandbox agent panes

**Phase 134 · 2026-09-04**

> **`km herdr` is a sibling of `km vscode`, not a `km tunnel` mode.** `km tunnel`
> carries a network path from your workstation *into* the sandbox, and its security
> note — km's egress enforcement does not see traffic crossing the tunnel — follows
> from that direction. `km herdr` points the other way: you reach *in* to a box
> that is already yours, over the same SSM+SSH transport `km vscode` uses. Nothing
> new becomes reachable from the sandbox, so filing it under `tunnel` would attach
> a caveat that is false here. See `docs/k8s-reverse-tunnel.md` if you actually
> want the tunnel family.

[Herdr](https://herdr.dev) is a terminal multiplexer built for coding agents:
named panes, workspace organization, and — the property this document is about —
**panes that keep running after you detach.** `km herdr start` makes sure a
sandbox is ready to accept a Herdr remote attach, holds the SSM+SSH transport open
in one terminal, and prints the line to run in another.

```bash
km herdr start my-sandbox
```

```
✓ Updated ~/.ssh/config (Host: km-my-sandbox)
✓ herdr v0.8.2 present at /usr/local/bin/herdr
✓ Forwarding localhost:2224 → sandbox:22

In another terminal:

    herdr --remote km-my-sandbox

  Named session:  herdr --remote km-my-sandbox --session agents
  Detach:         ctrl+b q          (panes keep running)
  Reattach:       rerun the same command
  ...
```

Run the printed `herdr --remote` command in a second terminal to actually attach.
`km herdr start` itself does not attach anything — it only proves the box is
reachable and holds the tunnel open, exactly like `km vscode start`.

---

## Prerequisites

1. **`spec.runtime.vscode.enabled`** must not be explicitly `false`. It **defaults
   to `true`**, so most sandboxes already have `sshd` running and a per-sandbox
   ed25519 keypair at `~/.km/keys/<id>` — the identical transport `km vscode`
   depends on. If you never touched this field, you already qualify.
2. **The Herdr binary on the box.** This is where the redundancy below matters —
   read it before assuming you need to do anything.

### The binary is probably already there

`profiles/base/userinit.yaml` — which most profiles extend — already installs
Herdr via `curl -fsSL https://herdr.dev/install.sh | sh`, landing it at
`/home/sandbox/.local/bin/herdr`, **owned by the sandbox user and NOT on root's
`PATH`**. On any profile that extends `base/userinit`, Herdr is present the
moment cloud-init finishes, and `profiles/base/tools/herdr.yaml` (below) is
redundant.

That fragment's real purpose is narrower: an **egress-free** install for
`base/network/locked`-style profiles that cannot reach `herdr.dev` at all. It
pulls the binary from S3 over the instance role instead:

```yaml
extends: [..., base/tools/herdr]
```

Deliberately absent from that fragment: any `spec.network` allowlist entry (S3
needs none), a systemd unit for the Herdr server (see [Signal 8](#signal-8-a-detached-busy-pane-keeps-the-sandbox-awake)
for why that would be actively harmful), and `pane_history` (see
[below](#pane-history-stays-off)).

Either way, `km herdr start` also **re-ensures the binary on demand** over SSM —
so the verb works today on any already-running sandbox, fragment or no fragment,
with no recreate. `--no-install` skips that ensure step if you want to see the
failure yourself.

---

## Lifecycle — read this before you rely on a detached session

**Herdr sessions do not survive a reboot.** This is stated in Herdr's own docs,
and it interacts with km's lifecycle commands in a way that is easy to get wrong:

| Command | What happens to Herdr panes |
|---|---|
| `km pause` (hibernate) | **Survive.** RAM is preserved, so panes and their processes keep running. |
| `km resume` (from pause) | Panes are exactly as you left them. |
| `km stop` | **Every pane's process dies.** Herdr's `pane_history`/`resume_agents_on_restore` can restore terminal *scrollback* and re-attach *recognized* agents — the running work itself is gone. |
| `km resume` (from stop) | A fresh Herdr server; nothing to reattach unless the above restore features were separately opted into upstream. |

The sentence that should worry you: **`teardownPolicy: stop` on an idle timeout is
the destructive path.** If km's idle reaper decides a sandbox is unused and its
teardown policy is `stop` (not `pause`), a detached-but-working Herdr pane gets
killed along with everything else on the box — silently, from the pane's point of
view. That is the entire reason signal 8 exists.

---

## Signal 8: a detached, busy pane keeps the sandbox awake

`km-presence`'s idle detector now ORs eight signals instead of seven. Before
signal 8, a detached Herdr pane was covered only in the cases that were already
covered some other way:

| Case | Already covered by | Verdict without signal 8 |
|---|---|---|
| An attached Herdr client | Signal 7 (established SSH session) | Fine |
| A detached pane running `claude`/`codex` by name | Signal 5 (`pgrep`) | Fine |
| A detached pane running anything else — a build, a test suite, a future agent | — | **Reaped mid-run** |
| Detached, panes idle | — | Reaped, correctly |

Signal 5 is a name allowlist: every new agent costs a km-presence edit and a
fleet recreate to take effect. Signal 8 instead asks Herdr **whether a pane is
doing work**, which makes it agent-agnostic by construction — swapping which
agent (or tool) runs in a pane needs no km change at all.

**It detects the work, never the server.** `herdr server stop` is the only thing
that ends a Herdr server, so "a server process exists" would latch the sandbox
awake forever — the same trap `pgrep vscode-server` would be for VS Code. A
detached session with nothing running in its panes **is still correctly reaped**
— that is by design, not a gap. `km-presence` runs `herdr pane list` to enumerate
panes, then `herdr pane process-info --pane <id>` per pane per tick (as the
`sandbox` user, over `runuser -u sandbox -- bash -lc '...'`, matching the
login-shell requirement below), ORing the result across every pane on every
discovered socket.

### The trap: `foreground_processes` is non-empty even when idle

The obvious-looking check is wrong, and it was actually written once before
being caught. `process-info`'s response carries a `foreground_processes` list —
and that list is **non-empty even on a completely idle pane**, because it always
contains the pane's own shell. A `len(foreground_processes) > 0` check is
therefore **always true**, which would make signal 8 permanently positive and
silently disable idle teardown fleet-wide. It was caught only by capturing a
real idle pane and comparing it against a busy one, not by reading Herdr's docs.

The correct discriminator, measured the same way: `process_info` also carries
`foreground_process_group_id` and `shell_pid`. On an idle pane both are the
shell's own pid (equal); on a pane running a foreground job,
`foreground_process_group_id` is the job's process group, which differs from
`shell_pid`. **Busy ⟺ `foreground_process_group_id != shell_pid`, both
non-zero** — the zero check matters too, since a response missing both fields
decodes to `0 == 0`, which must read as idle, not busy.

### Working with the Herdr CLI — the published docs are wrong three ways

All three were found by running the documented form first, on a live sandbox,
and watching it fail (`cmd/km-presence/testdata/README.md` has the transcripts):

- **There is no `--json` flag.** `herdr pane list --json` fails with
  `unknown option: --json` — JSON is the default output, not something you opt
  into.
- **Responses are wrapped**, not bare arrays or objects:
  `{"id":…,"result":{…},"type":…}`. The fields described above live under
  `result`, not at the top level.
- **`pane process-info` takes `--pane <ID>`, not a positional.**
  `herdr pane process-info w1:p1` fails with `unknown option: w1:p1`; the
  working form is `herdr pane process-info --pane w1:p1`. Pane ids look like
  `w1:p1` and come from `pane_id` in the `pane list` response (not `id`).

Herdr also creates a `herdr-client.sock` beside `herdr.sock` in the same config
directory — only `herdr.sock` speaks this API, which is why socket discovery
matches the literal filename `herdr.sock` (and its named-session equivalent)
rather than a `*.sock` glob that would also pick up the client socket.

**A pre-signal-8 sandbox needs `km destroy && km create` to gain it** —
`km-presence` is fetched at boot, so an existing box keeps its current daemon
until recreated. `km herdr status` tells you which situation you're in: it warns
explicitly when the sandbox's `km-presence` binary predates signal 8, so a
detached session's exposure to the destructive `stop` path is visible before it
bites, not after.

---

## The `~/.ssh/config` conflict

Herdr manages `~/.ssh/config` on your workstation by default (adding keepalive
fallbacks); km's `UpsertHost` owns the `Host km-<id>` block in that same file. Two
writers, one file.

`km herdr start` prints the fix on every run, and `km doctor` warns
(`Herdr ssh-config conflict`) when it detects both are active — Herdr is
managing the file (not opted out) *and* it already contains a `Host km-` block.
The fix is one line in Herdr's own config:

```toml
[remote]
manage_ssh_config = false
```

**km does not write this for you.** `~/.config/herdr/config.toml` is a
workstation-global file km does not own; silently editing it to win a conflict
with a tool it doesn't control is how you end up with a bug report about km
breaking an unrelated remote host. `km doctor` names the conflict and the exact
fix; you apply it.

---

## `pane_history` stays off

Herdr can persist pane scrollback to `session-history.json` on the sandbox disk
(`pane_history`, plus `resume_agents_on_restore`) so a restart can restore
terminal contents. It is **off by default upstream, and km's fragment does not
turn it on.**

Pane output is exactly the kind of thing that should not sit at rest on the box:
secrets, tokens, and prompts routinely pass through an agent's terminal. Enabling
persistence trades restart convenience for a new place those can leak from.
Nothing prevents an operator from turning it on themselves for a specific box —
km just doesn't make that choice for you.

---

## Security posture

- **No new credential path.** Same per-sandbox ed25519 keypair, same
  `authorized_keys` entry, same SSM forward, same `sandbox` user as `km vscode`.
  An operator who can already `km vscode start` a box could already `ssh -tt`
  into it and run anything Herdr can.
- **No new egress.** The S3-mirror path (`profiles/base/tools/herdr.yaml`) adds
  no `allowedDNSSuffixes` or `allowedHosts` entry — deliberately unlike
  `profiles/base/security/wiz.yaml`, which needs a `.wiz.io` suffix because its
  installer downloads over the public internet. Fetching the binary via the
  instance role keeps this feature free of any egress change at all.
- **The Herdr socket is the sandbox user's.** `~/.config/herdr/herdr.sock` is
  reachable by anything running as `sandbox` — on a `privileged: true` box, that's
  everything. Herdr's API can read pane output and drive input, so a compromised
  agent can inspect and steer sibling panes on the same box. This is not a new
  weakness (those panes were already the same uid on the same box), but it is
  another reason the real control is `spec.execution.privileged: false`.
- **Unlike `km tunnel`, nothing here bypasses km's egress enforcement.** No
  sandbox traffic routes through your workstation. The MITM proxy still meters
  AI spend, the eBPF allowlist still holds, and the flow/exec census
  (`docs/egress-census.md`, `docs/exec-capture.md`) still records what the panes
  do.

---

## Deploy surface

Three independent pieces — do not conflate them:

| What | Command | Reaches existing sandboxes? |
|---|---|---|
| `km herdr start\|status` + the S3 mirror (`s3://<bucket>/binaries/herdr`) | `make build` + `km init --sidecars` | **Yes** — the ensure-install step installs the binary over SSM on demand |
| `profiles/base/tools/herdr.yaml` | none (profile file only; needs the mirror seeded first) | New sandboxes only |
| `km-presence` signal 8 | `make build` + `km init --sidecars` | **No** — `km destroy && km create` |

**NOT `km init --dry-run=false`.** Nothing here touches a Terraform module, an
IAM policy, a DynamoDB table, a Lambda, or the userdata *template*. The fragment
is pure `initCommandsAppend` — profile *data*, not a template or schema change —
so the create-handler zip is untouched and userdata goldens for existing
profiles do not move.

**One caveat worth stating precisely, because it's easy to get backwards:**
adding an `initCommandsAppend` line *does* change one thing in rendered
userdata — Phase 113 `yaml.Marshal`s the entire resolved profile (including
`InitCommands`) into the `/opt/km/.km-profile.yaml` on-box dump, by reflection,
so no template token names it. That delta is confined to that one dump and never
reaches executable bootstrap logic, which is why the deploy story above still
holds — but "no rendered-output change at all" would be the wrong way to say it.

**Order matters:** run `km init --sidecars` before any profile extends the
fragment, so `binaries/herdr` exists before a boot tries to fetch it. A box that
boots the fragment against a missing S3 object fails soft (see
[Troubleshooting](#troubleshooting)) and is recoverable with `km herdr start`.

**`km init --sidecars` in a worktree that has never run `make build-lambdas`
exits non-zero — and that's expected here, not a failure of this feature.** Its
last step tars `build/*.zip` into `toolchain/infra.tar.gz`; those zips come from
`make build-lambdas`, and a fresh worktree hasn't produced them. Every sidecar,
including `binaries/herdr`, uploads successfully **before** that step runs, and
the previous `infra.tar.gz` is left intact rather than replaced with a partial
one. So the check that matters is `grep 'Uploaded herdr'` in the command's
output, not its exit code. Run `make build-lambdas` first if you want a clean
exit.

---

## Flags

### `km herdr start <sandbox-id>`

| Flag | Default | Purpose |
|---|---|---|
| `--local-port` | `2224` | Laptop port for the SSM forward to sshd |
| `--no-install` | off | Fail instead of installing herdr when it's absent, so you can see the failure |

`--local-port` defaults to `2224` because `km vscode start` owns `2222` and both
`km tunnel` modes own `2223` — having VS Code, a tunnel, and a Herdr attach all
open on the same sandbox at once is a plausible combination.

### `km herdr status <sandbox-id>`

No forward is opened; one SSM round trip reports sshd state, `authorized_keys`
presence, the herdr binary path and version, and — because this is the
persistence story — whether the sandbox's `km-presence` daemon has signal 8 at
all. Exits non-zero when the sandbox isn't ready for an attach.

---

## Verification status

This branch has had several defects found, fixed, reviewed, and still wrong at
each step, so this section is deliberately precise about what has and hasn't
actually been watched happen, rather than implying broader coverage than exists.

**Verified live, on a real sandbox, on 2026-09-04:**

- Signal 8's discriminator, by A/B/A: idle pane → `active=false`; the same pane
  running `sleep 900` → `active=true`; the job killed → `active=false` again.
- `km herdr status` against a real box.
- The corrected fragment command (source `/etc/profile.d/km-identity.sh` when
  `KM_ARTIFACTS_BUCKET` is unset), executed under `env -u KM_ARTIFACTS_BUCKET
  bash -c '...'` — cloud-init's exact plain-child-shell environment — extracted
  verbatim from the shipped YAML rather than retyped.
- `herdr server` starting headlessly under `setsid`/`nohup`, with no TTY.

**Not yet observed:**

- A fresh `km create` actually running the fixed fragment during real
  cloud-init (rather than the command replayed standalone, above).
- `km herdr status`'s quiet branch — a box created *after* signal 8 shipped,
  where the warning should correctly stay silent.
- An interactive `herdr --remote` attach → `ctrl+b q` detach → reattach round
  trip. Everything above drove Herdr headlessly.
- The fragment's egress-free path exercised on an actual `base/network/locked`
  profile.

---

## Troubleshooting

**`herdr: command not found` after `km herdr start` reports success.**
You're likely running `herdr` on your own workstation, not the sandbox — the
printed command (`herdr --remote km-<id>`) needs the Herdr client **installed
locally**, separately from the sandbox-side binary this document is about.
Install it from [herdr.dev](https://herdr.dev) on your workstation.

**`km herdr start` reports "herdr is not installed" and `--no-install` was set.**
Expected — that's what the flag is for. Drop it, or run `km herdr start` again
without it to trigger the ensure-install.

**`km herdr start` says it installed herdr but the binary is still absent.**
The error names the exact check: confirm `km init --sidecars` has actually
seeded `s3://<artifacts-bucket>/binaries/herdr` (see
[Deploy surface](#deploy-surface) for why its exit code alone isn't proof).

**A box that extends `base/tools/herdr` came up without the binary, but boot
otherwise looked healthy.**
The fragment fails soft by design — a fetch failure surfaces only as
`[km] herdr fetch failed; run km herdr start to install` in
`/var/log/cloud-init-output.log`, never as a boot failure. Grep for that line,
then run `km herdr start <id>` to install on demand; check that `binaries/herdr`
exists in S3 if it recurs.

**A missing binary right after `km create` looks like the fragment failed.**
`km list` shows a sandbox as green before cloud-init finishes. Check
`cloud-init status` on the box (or just wait) before concluding the fragment is
broken — see the grep above once boot is actually done.

**Port collision on `--local-port` (default 2224).**
`km vscode` owns `2222`, both `km tunnel` modes own `2223`. If you're already
using one of those on the same sandbox, `km herdr start` can coexist on `2224`;
if two `km herdr start` sessions collide with each other, pick a fourth free
port with `--local-port`.

**A box got reaped while a Herdr session was detached.**
Check `km herdr status <id>` first — if it warns about missing signal 8, that's
the cause: this sandbox predates the fix and needs `km destroy && km create`.
If signal 8 is present and this still happened, confirm the pane was actually
running something (an idle detached pane is *correctly* reaped — see
[Signal 8](#signal-8-a-detached-busy-pane-keeps-the-sandbox-awake)), and check
whether `teardownPolicy` was `stop` rather than `pause` — pausing preserves
panes across an idle reap, stopping does not.
