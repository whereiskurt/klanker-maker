# `km herdr` — persistent remote attach for sandbox agent panes

**Date:** 2026-09-04
**Status:** Design — approved in brainstorm, not yet planned
**Phase:** TBD (next free number; cross-check `grep "Phase NNN" CLAUDE.md docs/` before `gsd phase add`)

## 1. Summary

Add `km herdr start|status <sandbox-id>`: an operator-side verb that guarantees a
sandbox is ready to accept a [Herdr](https://herdr.dev) remote attach, holds the
transport open in one terminal, and prints the `herdr --remote km-<id>` line to run
in another.

The job Herdr is doing here is **persistence for agent panes**, not a nicer terminal.
Agents keep working while the operator is detached — across hours, across a closed
laptop. That single requirement is what makes this more than a banner: km's idle
reaper cannot currently see a detached Herdr session, so a long non-agent run gets
the box stopped out from under it.

Three things ship together:

1. **Binary delivery** — `km init --sidecars` mirrors the pinned Herdr release to
   `s3://{bucket}/binaries/herdr`; a `profiles/base/tools/herdr.yaml` fragment pulls
   it at boot; `km herdr start` re-ensures it on demand for boxes that predate the
   fragment.
2. **`km herdr start|status`** — a thin sibling of `km vscode`, reusing its transport
   wholesale and differing only in pre-flight and banner.
3. **km-presence signal 8** — a pane-busy check so a detached-but-working Herdr
   session holds the box awake, while a detached-and-quiet one is still reaped.

Nothing here is a new transport, a new credential path, or a new egress hole.

## 2. Non-goals

- **No `km herdr rekey`.** Herdr uses the same per-sandbox ed25519 keypair as VS Code
  Remote-SSH (`~/.km/keys/<id>` ↔ the `sandbox` user's `authorized_keys`).
  `km vscode rekey` already rotates it; a second verb over one keypair is a footgun.
- **No Herdr systemd unit.** The remote server starts itself on first attach. A unit
  would start a server nobody is attached to and defeat signal 8's negative case.
- **No `km herdr stop`.** `herdr server stop` and `herdr session stop <name>` are
  Herdr's own verbs and work over the same attach. km wrapping them adds a name to
  learn and a way to be wrong.
- **No profile field.** Herdr presence is expressed by extending a fragment, the same
  way `base/security/wiz.yaml` works. No schema change, therefore no `apiVersion`
  bump and no create-handler toolchain refresh.
- **Windows local clients.** Herdr supports them; km's operator tooling does not, and
  nothing here changes that.

## 3. Why `km herdr`, not `km tunnel herdr`

`km tunnel` is a family with one defining property: it carries a network path from the
operator's workstation *into* the sandbox, so the box reaches something only that
workstation can reach. Both members do this — `k8s` forwards a cluster, `socks`
forwards general egress. The security note attached to the whole family follows from
that direction: while a tunnel is up, km's egress enforcement does not see the traffic
crossing it.

Herdr points the other way. The operator reaches *in* to a box that is already theirs,
over a transport km already terminates. Nothing new becomes reachable from the sandbox.
Filing it under `tunnel` would attach that family's security caveat to a command where
it is false, which is worse than a slightly larger command surface.

The correct sibling is `km vscode`: same direction, same transport, same keypair, same
`~/.ssh/config` entry. `km herdr` is that command with a different pre-flight.

## 4. Component 1 — binary delivery

### 4.1 Mirror (operator side)

Herdr ships bare per-platform binaries from `github.com/herdrdev/herdr` releases —
`herdr-linux-x86_64` for our sandboxes. This is a near-exact repeat of
`fetchAndUploadSops` (`internal/app/cmd/init.go`), which is the established pattern for
a third-party binary: download on the operator's machine, push to S3, let userdata pull
it with the instance role. `sidecarBuilds()` is the wrong home — it compiles Go from
this repo.

Add to `buildAndUploadSidecars`, beside the sops call:

```go
const herdrVersion = "0.8.2"

// fetchAndUploadHerdr downloads herdr v{herdrVersion} linux/amd64 (cached in
// build/) and uploads to s3://{bucket}/binaries/herdr.
//
// The release publishes bare per-platform binaries and NO checksum file, so
// there is no published digest to verify against. The version pin is the whole
// supply-chain control: bump it deliberately, like any other dependency.
func fetchAndUploadHerdr(buildDir, bucket string) error { ... }
```

Failure is **non-fatal at the call site** (`[warn]`, like `fetchAndUploadOtelcolContrib`),
not fatal like sops. A missing sops aborts boot because secrets are load-bearing; a
missing herdr costs one command that re-ensures it anyway.

S3 key `binaries/herdr` is the contract. Both the fragment and `km herdr start` read it,
and a test should pin the literal in all three places.

### 4.2 Fragment (boot side)

`profiles/base/tools/herdr.yaml` — a new `tools/` subdirectory under `profiles/base/`,
matching the existing `network/`, `os/`, `security/`, `gpu/` split.

Critically, this is **pure `initCommandsAppend`** — no `pkg/compiler/userdata.go` change,
therefore **no userdata golden churn, no create-handler rebuild, and no
`km init --dry-run=false`** for this half. `userdata.go:374` exports
`KM_ARTIFACTS_BUCKET` at the top of the bootstrap, and initCommands run as a child
process (`/tmp/km-init.sh`) that inherits it, so the fragment can reach S3 by name.

```yaml
spec:
  execution:
    initCommandsAppend:
      - "aws s3 cp \"s3://${KM_ARTIFACTS_BUCKET}/binaries/herdr\" /usr/local/bin/herdr && chmod 0755 /usr/local/bin/herdr || echo '[km] herdr fetch failed; run km herdr start to install' >&2"
```

The `|| echo` is deliberate and load-bearing. `km-init.sh` runs under `set -e`
(the trap `base/security/wiz.yaml` documents: a failure there aborts every *later*
initCommand and surfaces only as `No init script found in S3 (skipped)`). Because
`km herdr start` re-ensures the binary anyway, a hard failure here would trade a
recoverable gap for a broken boot. This redundancy is the whole reason approach C
beats either half alone.

`/usr/local/bin` rather than `~/.local/bin/herdr`: initCommands run as root, Herdr
checks the remote `PATH` first, and a system path avoids a chown dance. It also keeps
one copy for every user rather than one per home directory.

**No egress allowlist entry is needed** — the fetch is S3 via the instance role, not
`herdr.dev`. This is the decisive advantage over letting Herdr self-install: its
interactive installer downloads over the public internet, which on an `ebpf`/`both`
profile means a `.herdr.dev` (and GitHub CDN) `allowedDNSSuffixes` entry, and per
`base/security/wiz.yaml` DNS is the one layer root does *not* bypass.

### 4.3 Ensure on demand (`km herdr start`)

Before connecting, `km herdr start` runs one SSM script that reports whether
`/usr/local/bin/herdr` exists and what `herdr --version` says. On absent-or-stale it
runs the same `aws s3 cp` as the fragment, then re-reads. This makes the verb work
today on every already-running sandbox, with no recreate.

`--no-install` skips the ensure step for an operator who wants to see the failure.

## 5. Component 2 — `km herdr start|status`

### 5.1 `start`

`runHerdrStart` is `runVSCodeStart` (`internal/app/cmd/vscode.go`) with two deltas.
Reuse, do not fork: port probe → `FetchSandbox` → `extractResourceID(rec.Resources,
":instance/")` → key check at `~/.km/keys/<id>` → SSM pre-flight → `UpsertHost` →
`runReconnectingPortForward` with `sshBannerTunnelProbe`. Extract the shared body into a
helper both commands call rather than copy-pasting 60 lines; the vscode-specific part is
only the pre-flight script and the banner.

**Delta 1 — pre-flight.** Extend `vsCodeStatusScript`'s three checks (sshd active,
`authorized_keys` present, first key line) with a fourth: herdr binary path and version.
One SSM round trip, as today.

**Delta 2 — banner.** Print the attach line rather than the VS Code line:

```
✓ Updated ~/.ssh/config (Host: km-<id>)
✓ herdr v0.8.2 present at /usr/local/bin/herdr
✓ Forwarding localhost:2224 → sandbox:22

In another terminal:
    herdr --remote km-<id>

  Named session:   herdr --remote km-<id> --session agents
  Detach:          ctrl+b q          (panes keep running)
  Reattach:        rerun the same command

The tunnel auto-reconnects if it drops; Ctrl-C closes it. Detached panes
survive both — but see `km herdr status` for what the idle timer thinks.
```

**Default `--local-port 2224.`** 2222 is `km vscode`, 2223 is both `km tunnel` modes.
Having VS Code attached to a box you are also herdr-attached to is a plausible
combination, and the port probe's error message should suggest the next free port the
way the existing ones do.

### 5.2 `status`

Same one-shot SSM script, no forward. Reports sshd, `authorized_keys`, herdr version —
and, because this is the persistence story, **what the idle reaper currently sees**:
whether any Herdr session exists, how many panes are busy, and the sandbox's
`idleTimeout`. That last line is the difference between "the box vanished" and "I could
see it coming".

Non-zero exit when not ready, matching `runVSCodeStatus`.

## 6. Component 3 — km-presence signal 8 (pane busy)

### 6.1 The gap

Today's seven signals leave detached Herdr partly uncovered:

| Case | Covered by | Verdict |
|---|---|---|
| Attached Herdr client | Signal 7 (established sshd socket) | Already fine |
| Detached, pane running `claude`/`codex` | Signal 5 (`pgrep -afE`) | Already fine, **by name** |
| Detached, pane running pi / hermes / a build / a test suite | — | **Reaped mid-run** |
| Detached, panes idle | — | Reaped, and correctly so |

Signal 5 is a name allowlist. Every agent swap costs a km-presence edit *and* a fleet
recreate to take effect. Signal 8 asks Herdr whether a pane is doing work, so it is
agent-agnostic by construction and needs no edit when pi and hermes replace goose.

### 6.2 Design

```go
// checkHerdrPaneBusy returns true when any Herdr session has a pane doing work.
// Signal 8.
```

Rules it must obey, all inherited from signals 6 and 7:

- **Detect the work, never the server.** `herdr server stop` is the only thing that
  ends a Herdr server, so "a server is running" latches the box awake forever — the
  exact `pgrep vscode-server` trap. The negative case must be reachable by a real
  operator: detach, leave nothing running, and the box reaps on schedule.
- **Fail idle.** A missing binary, an absent socket, a malformed response, or any
  non-zero exit returns `false`. A reaped box costs one `km resume`; a signal that can
  never go negative silently disables idle teardown fleet-wide and leaks instances.
- **Independent.** Its own subprocess, not a share of another signal's output, matching
  the reasoning in `checkSSHSessions`.
- **`blocked` counts as busy.** Herdr's `agent.list` exposes `idle | working | blocked |
  done | unknown`. An agent parked on a permission prompt is *precisely* the case that
  must not be reaped — km already treats it as operator-relevant via
  `notification.events.onPermission`. Treat `working` and `blocked` as active; `idle`,
  `done`, and `unknown` as not.

### 6.3 Socket discovery

Herdr resolves its socket as: explicit `--session` → `HERDR_SOCKET_PATH` →
`HERDR_SESSION` → default. Sessions live at:

```
/home/sandbox/.config/herdr/herdr.sock              # default session
/home/sandbox/.config/herdr/sessions/<name>/herdr.sock   # named sessions
```

km-presence runs as root (`User=root` in `km-presence.service`, already required by
signals 6 and 7 for `ss -p`), so it can read a socket owned by `sandbox`.

Enumerate named sessions by **globbing that path** rather than shelling out to
`herdr session list` — filesystem access needs no seam and no subprocess, and signals 3
and 4 already read the filesystem directly. Query each discovered socket; OR the results.

`commandRunner` is `Output(name string, args ...string)` with no env support, so pass
the socket explicitly rather than widening the seam:

```go
r.Output("runuser", "-u", "sandbox", "--", "herdr", "pane", "list", "--json", ...)
```

`runuser`, not `sudo`/`su` — the same choice Phase 132 made across 15 dispatch sites.

### 6.4 Open question: one call or N?

Herdr's API exposes `pane.list` and a **separate** `pane.process_info` (shell pid,
foreground process group, foreground processes with pid/name/argv/cwd). If
`pane.process_info` is per-pane, a naive pane-busy check is one call per pane per 60s
tick, which is more subprocess churn than any existing signal.

**This must be settled by a live probe, not by reading docs.** On a real box:

```bash
herdr api schema --json > /tmp/herdr-api.json
herdr pane list --json
herdr agent list --json
```

Then pick, in preference order:

1. `pane list --json` already carries foreground-process or busy state → one call per
   session. Best case.
2. It does not, but `agent list --json` covers the realistic cases → one call per
   session, with a documented narrowing: a *non-agent* long job in a detached pane is
   still invisible.
3. Neither → speak newline-delimited JSON to the socket directly from Go (the protocol
   is one line in, one line out) and issue `pane.list` + `pane.process_info` over a
   single connection. This needs a new seam beside `commandRunner`, and it drops the
   dependency on the `herdr` CLI being on root's `PATH`.

Option 3 is the most robust and the most work. Do not choose between these from the
documentation — `pkg/ebpf`'s endianness bug is the standing reminder that a helper and
its test can agree with each other and both disagree with reality.

### 6.5 Wiring

`tick()` gains `s8 := checkHerdrPaneBusy(r)` and the `active` disjunction extends to
eight. Update the "seven signals" wording in `cmd/km-presence/main.go:4`,
`runner.go`, `docs/desktop.md`, `docs/vscode.md`, and CLAUDE.md.

Tests, mirroring the signal 6/7 set: positive (a busy pane), negative-quiet (sessions
exist, nothing running — **the load-bearing one**, since it is what keeps idle teardown
alive), negative-no-socket, negative-binary-absent, negative-malformed-JSON, and a
cross-independence test that signal 8 never fires from signal 6's or 7's inputs.

## 7. The `~/.ssh/config` conflict

Herdr manages `~/.ssh/config` itself by default, adding keepalive fallbacks; km's
`UpsertHost` owns a `Host km-<id>` block in the same file. Two writers, one file.

`km herdr start` prints the opt-out on first run and `km doctor` warns when Herdr's
config shows it is still managing:

```toml
[remote]
manage_ssh_config = false
```

**km should not write the operator's Herdr config for them.** It is a
workstation-global file km does not own; silently editing it to win a conflict is how
you produce a bug report about km breaking an unrelated remote host. Print it, explain
it, let them decide. (`--fix-ssh-config` writing the single key is a reasonable
follow-up once the conflict is observed in practice — not before.)

## 8. Lifecycle interactions

These belong in the runbook, loudly, because they are surprising and each one silently
destroys work:

- **Herdr sessions do not survive a reboot.** Its own docs say so. Therefore:
  - `km pause` (hibernate) — RAM is preserved, so panes and their processes survive.
    `profiles/learner.yaml` has just turned `hibernation: true` on, so this is the
    live path today.
  - `km stop` — the instance halts. **Every pane's process dies.** Herdr's
    `pane_history = true` and `resume_agents_on_restore` can restore terminal contents
    and re-attach recognized agents, but the running work is gone.
  - `teardownPolicy: stop` on idle means the idle reaper's failure mode is *exactly*
    the destructive one. That is why signal 8 is part of this phase and not a follow-up.
- **`pane_history` is off by default and should stay off.** It persists pane output —
  secrets, tokens, prompts — to `session-history.json` on the sandbox disk. Recommending
  it in a km profile would put agent transcript material at rest on the box for the
  convenience of scrollback after a restart. Operators can opt in per box; km does not.
- **`km herdr start` dies with Ctrl-C; the Herdr server does not.** Same relationship
  `km vscode start` has with sshd. Worth stating so nobody Ctrl-Cs expecting cleanup.

## 9. Security posture

- **No new credential path.** Same keypair, same `authorized_keys`, same SSM forward,
  same `sandbox` user as `km vscode`. An operator who can `km vscode start` could
  already `ssh -tt` and run anything.
- **No new egress.** The binary arrives from S3 over the instance role. The fragment
  adds no `allowedDNSSuffixes` and no `allowedHosts` entry — deliberately unlike
  `base/security/wiz.yaml`, which needs `.wiz.io` precisely because its installer
  downloads over the internet.
- **The Herdr socket is the sandbox user's.** `~/.config/herdr/herdr.sock` is
  reachable by anything running as `sandbox`, which on a `privileged: true` box is
  everything. Herdr's API can read pane output and drive input, so a compromised agent
  can inspect and steer its neighbours' panes. This is not weaker than the status quo —
  those panes are already the same uid on the same box — but it should be stated,
  and it is another reason the real control is `privileged: false`.
- **km's egress enforcement still applies in full.** Unlike `km tunnel`, nothing here
  routes sandbox traffic through the operator's workstation. The MITM proxy still meters
  AI spend, the eBPF allowlist still holds, and Phase 131/132's flow and exec census
  still record what the panes do.

## 10. Deploy surface

Three independent paths — do not conflate them:

| What | Command | Reaches existing sandboxes? |
|---|---|---|
| `km herdr start\|status` + the S3 mirror | `make build` + `km init --sidecars` | **Yes** — ensure-on-demand installs the binary over SSM |
| `profiles/base/tools/herdr.yaml` | none (profile file only; needs the mirror seeded) | New sandboxes only |
| km-presence signal 8 | `make build` + `km init --sidecars` | **No** — `km destroy && km create` |

**NOT `km init --dry-run=false`.** No Terraform module, IAM policy, DynamoDB table,
Lambda, or userdata template changes. The fragment is pure `initCommandsAppend`, so the
create-handler zip is untouched and userdata goldens do not move — verify that claim
with a golden run rather than assuming it.

**Order matters:** `km init --sidecars` seeds `binaries/herdr` *before* any profile
extends the fragment. A sandbox booting the fragment against a missing S3 object hits
the `|| echo` path and comes up herdr-less — recoverable via `km herdr start`, but
confusing.

**The honest caveat:** the connect half works on today's boxes; the idle protection does
not, because km-presence is fetched at boot. Until a box is recreated, a detached Herdr
session on it is still reapable. `km herdr status` should say so explicitly when it
detects a pre-signal-8 presence daemon.

## 11. Testing

- **Unit, operator side:** pre-flight parser (present / absent / stale version /
  malformed), banner rendering, port-probe collision message, `UpsertHost` reuse,
  `--no-install` short-circuit. Follow `vscode_test.go`'s shape and its injected
  `SandboxFetcher` / `SSMSendAPI` / `ShellExecFunc` seams.
- **Unit, km-presence:** the six signal-8 cases enumerated in §6.5.
- **Mirror contract:** one test pinning the `binaries/herdr` literal across
  `fetchAndUploadHerdr`, the fragment, and the ensure path — the same mechanical-pairing
  discipline as `TestTTLHandlerModule_EveryEnvTableHasAnIAMGrant`.
- **Golden guard:** assert the fragment changes no userdata golden, which is the claim
  §10 rests on.
- **Live UAT — not optional.** §6.4 is unresolvable without a real Herdr, and the
  negative-quiet case for signal 8 can only be trusted after watching a real detached
  session actually get reaped on schedule. Minimum script: attach; start a long
  non-agent job; detach; confirm the box outlives `idleTimeout`; stop the job; confirm
  it then reaps.

## 12. Rejected alternatives

- **`km tunnel herdr`** — wrong direction; would attach the tunnel family's
  egress-blindness caveat to a command where it is false. (§3)
- **Let Herdr self-install** — its interactive installer needs public egress, so every
  `ebpf`/`both` profile would need a `.herdr.dev` DNS suffix, and its own docs say
  non-interactive runs fail outright.
- **Ship herdr via `sidecarBuilds()`** — that pipeline compiles Go from this repo.
  `binaries/` is the existing home for third-party binaries.
- **Signal 8 = "herdr server is running"** — the `pgrep vscode-server` trap; the server
  never exits, so it would disable idle teardown on every box that ever ran Herdr.
- **Signal 8 = "a herdr client is attached"** — adds nothing, since a Herdr client *is*
  an SSH session and signal 7 already sees it. It would leave detached persistence, the
  entire point of the phase, unprotected.
- **Extend signal 5's `pgrep` list with pi/hermes** — treats the symptom. The list is
  the defect; every future agent costs an edit plus a fleet recreate.
- **A `spec.runtime.herdr` profile field** — a fragment already expresses this with no
  schema change, no `apiVersion` question, and no create-handler toolchain refresh.
  `base/security/wiz.yaml` is the precedent.
- **km writing `manage_ssh_config = false` into the operator's Herdr config** — km does
  not own that file. (§7)
