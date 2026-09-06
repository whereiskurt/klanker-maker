<!--
  MAINTAINER NOTE — update this file BEFORE tagging each release.

  Its contents are injected VERBATIM into every GitHub release's notes,
  between the install header and goreleaser's auto-generated changelog.
  Wiring: .github/workflows/release.yml ("Load release highlights" step) →
  $KM_RELEASE_HIGHLIGHTS → .goreleaser.yaml `release.header` template.

  Keep it to the few MAJOR, human-curated additions for THIS release (the
  auto-changelog already lists every commit). HTML comments like this one
  are hidden in GitHub's rendered view. If this file is empty/absent the
  section is omitted gracefully.

  Drafted from CLAUDE.md phase blocks since v0.8.15 via
  scripts/draft-release-highlights.sh, then curated.
-->

## 🕳️ The eBPF allowlist never applied to anything you did by hand

No interactive session had **ever** been inside the cgroup the eBPF network programs are
attached to. Not `km shell`, not `km herdr`, not `km vscode`, not direct `ssh`, not
`sudo -u sandbox`. The BPF allow-trie only ever policed Phase 132 agent dispatch.

The cause is cgroup v2's common-ancestor rule: a process may be migrated only if the caller
can write the `cgroup.procs` of the **common ancestor** of the source and destination
cgroups. km chowned the *destination* `root:sandbox 0664`, which is necessary but not
sufficient — for every interactive session the ancestor is the root cgroup, `root:root 0644`.
The join failed `EPERM` every time, and `2>/dev/null || true` swallowed it, so the wrapper
looked like it worked. For `ssh` there is a second, independent blocker: `pam_systemd` places
the session in `user.slice` before the login shell ever runs.

This was **not an open door** — the DNS resolver and the HTTP proxy always applied, and both
are uid- and cgroup-agnostic. What was missing is the layer that catches traffic bypassing
*both*: a connection to a literal IP, or a process that ignores the proxy environment.

The fix stops migrating processes and attaches the programs where the processes already are —
the **root cgroup** — gating each on `uid == sandbox_uid || cgroup_id == km_scope_id`. The
second clause is not redundant: it keeps a root process deliberately placed in the scope
enforced, so `sudo` cannot become a way *out*. There is no entry-point coverage gap, because
there is no interception point to miss.

**On a locked profile an interactive session now hits the allow-trie for the first time.** A
profile whose allowlist was subtly too loose to be caught by DNS and the proxy alone may
behave differently in a `km shell`. **Existing sandboxes keep the old behaviour until
`km destroy && km create`.**

## 🔐 Secrets no longer sit in every shell's environment

SOPS secrets decrypted at boot into `/etc/sandbox-secrets.env` and were auto-exported into
every login shell. Agent turns dispatch through a login shell, so the whole bundle sat in the
agent's environment, in every `km shell`, and in the environ of anything either ever forked.
`cat /proc/*/environ` yielded everything.

`km-secretsd` now brokers each unseal over a unix socket, performing a live `kms:Decrypt` per
request and zeroing its buffers immediately after. Consumers are intercepted by root-owned
shims so `km-env` is innermost — wrapping the dispatch chokepoint instead was rejected, because
it would hand the whole turn's shell the bundle and merely shrink the problem being fixed.
`spec.secrets.grants` scopes which keys each consumer sees.

Stated plainly: **this is blast-radius hygiene and audit legibility, not containment.** Uid
`sandbox` must reach the socket for the feature to work at all, so peer credentials are
recorded for attribution, not authorization. What changes is the *character* of a theft — a
passive, traceless file read becomes an attributed request that writes `secret_unseal` to the
audit stream and a discrete `kms:Decrypt` to CloudTrail.

**The IMDS fence** (`spec.secrets.fenceIMDS`, opt-in, off by default) closes the remaining
unmediated path: the instance role permanently holds both halves of the decrypt, so anything
reaching IMDS could re-derive the bundle regardless of grants. It is code-complete with live
UAT still outstanding.

On `privileged: true` none of this holds — sudo can stop the daemon or read the ciphertext
directly. The real control remains `privileged: false`.

## 🐑 `km herdr` — agent panes you can walk away from

`km herdr start|status <sandbox-id>` attaches Herdr over the same SSM+SSH transport
`km vscode` already terminates, in one terminal, with the forward torn down when the session
ends. A new `km-presence` signal keeps a sandbox awake while a detached pane is doing real
work — and correctly reaps one that is merely sitting there, because it detects the *work*,
never the server.

Read the lifecycle note before relying on it: Herdr sessions do not survive a reboot.
`km pause` preserves them; `km stop` kills every pane's process outright.

## 📦 Every artifact fetched at boot or build is now pinned and verified

External downloads reached the box by version alone, or by piping an installer straight to a
shell. Each is now pinned and checked against a source-pinned digest, so a compromised or
silently re-tagged upstream fails the fetch rather than executing.

## 📋 Upgrading

`make build` + `make build-lambdas` + `km init --dry-run=false`. **Not `--sidecars`** — this
release changes userdata, IAM (`ec2spot/v1.7.0`), and adds new sidecar binaries, and the two
halves fail *quietly* if split. After deploying, the check worth running is:

```
journalctl -u km-ebpf-enforcer | grep 'enforcement predicate'
```

which prints the resolved uid and attach point.

**Existing sandboxes keep the previous behaviour until `km destroy && km create`** — the
enforcer, the secrets broker and the presence daemon are all fetched at boot.
