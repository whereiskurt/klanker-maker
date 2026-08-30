# Live egress census, allowlist pinning, and packet capture

`km-netpolicy observed | flows | profile | pin | capture` let a sandbox report
where it has actually been, and collapse its own allowlist down to exactly
that — from inside the box, while it is running, with no operator round-trip.

**Provisioned on every sandbox, unconditionally.** Unlike most features in this
repo, this is not gated on a profile field and there is no dormant case. The
question "what has left this machine" is worth more on a locked-down box
during an incident than on a box someone pre-designated for learning, so
observation is always on. See `docs/egress-deny-lists.md` for the deny-list
half of the `km-netpolicy` binary this shares a home with.

## The problem

A sandbox already makes an egress decision on every connection, three times
over — the eBPF ring buffer, the DNS proxy, and the HTTP proxy all know the
verdict the instant they make it. Until this phase, all three threw the answer
away or shipped it somewhere the box itself could not read back: CloudWatch,
journald. Learn mode came closer — it accumulates a real census — but it lives
in the eBPF enforcer's memory, flushes only on `SIGUSR1` or shutdown, and that
signal is sent from the operator's laptop by `km shell --learn` at session
exit. The census existed, exactly once, at the moment the session ended, on a
machine that was not the sandbox.

`km-netpolicy observed` and `flows` answer "where have I been" while the
session is still running. `pin` turns that answer into policy without leaving
the box.

## `observed` and `flows`

`observed` is the deduplicated destination set — what `pin` will act on.
Allowed and denied are printed under separate headings and never merged; a
denied destination is one of the most interesting rows on the page, not noise
to fold away.

```console
$ km-netpolicy observed
allowed (3):
  api.github.com (140.82.113.6)                   214 conns   last just now
  registry.npmjs.org (104.16.0.35)                  38 conns   last 4m ago
  pypi.org (151.101.0.223)                           6 conns   last 12m ago

denied (1):
  evil.example.com                                   3 conns   last 2m ago
```

`flows` prints the raw per-connection records behind that census —
timestamp, producer, verdict, destination, and (where the producer observed
one) the originating pid/comm:

```console
$ km-netpolicy flows --since 10m
2026-08-28T14:00:01Z  dns      allow    api.github.com
2026-08-28T14:00:02Z  ebpf     allow    140.82.113.6:443                        git[4211]
2026-08-28T14:00:02Z  http     allow    api.github.com:443
2026-08-28T14:02:11Z  dns      deny     evil.example.com
2026-08-28T14:02:11Z  ebpf     deny     93.184.216.34:443                       curl[4299]

$ km-netpolicy flows --denied --json
{"ts":"2026-08-28T14:02:11Z","src":"dns","verdict":"deny","host":"evil.example.com"}
{"ts":"2026-08-28T14:02:11Z","src":"ebpf","verdict":"deny","addr":"93.184.216.34","port":443,"proto":"tcp","pid":4299,"comm":"curl"}
```

`--since <duration>` filters by age (e.g. `10m`, `1h`); `--denied` shows only
blocked attempts; `--json` emits one record per line instead of the table.

Every producer fills only what it actually observed and leaves the rest
absent — the DNS proxy knows a name but not the eventual peer; the HTTP proxy
knows host, port, and verdict; the eBPF path knows address, port, pid, and
`comm` but not the name unless the eBPF resolver's own name→IP map happens to
have one for that address at write time. **No producer fabricates a field it
did not observe**, and there is deliberately no reverse-DNS lookup filling the
gap — a wrong hostname on a flow record is worse than an absent one, because
it is exactly what an operator would `pin` against.

`km-netpolicy profile` emits a SandboxProfile YAML from the allowed census,
reusing the same `learned.*.yaml` shape `km shell --learn` produces — read one
learned profile and you can read this one.

## The pin model

`pin` is the mirror image of `deny`, and the shape is deliberate:

```
effective deny  = union(profile denies, runtime deny file)
effective allow = profile allowlist ∩ pin generation 1 ∩ pin generation 2 ∩ …
```

Deny is a union, so it can only ever grow what is blocked. Pin is an
intersection, so it can only ever shrink what is allowed. Both are monotone in
the direction that matters — **"never widens" is a property of the data
structure, not a rule anyone has to trust or a check anyone has to remember to
run.**

Precedence is unchanged by pins slotting in:

```
deny  →  pins narrow the allow  →  profile allowlist
```

A pin can never re-allow something a deny already blocked, because a denied
destination was never in the allow set to begin with — `observed`'s denied
section exists for the operator's benefit and is excluded from every pin
candidate by construction.

### The wide-open walkthrough

`profiles/base/network/safenetwork.yaml` and most learn-mode profiles declare
`allowedDNSSuffixes: ["*"]` / `allowedHosts: ["*"]` — genuinely wide open, no
special case anywhere in the code for what happens when you pin one:

```
effective allow (before) = "*"
observed                 = { api.github.com, registry.npmjs.org, pypi.org }
effective allow (after)  = "*" ∩ observed = observed
```

The intersection does the entire job. Pinning a wide-open box collapses it to
precisely what it touched — no `if allowlist == "*"` branch, because `*`
behaves as allow-everything in the intersection the same way it does
everywhere else in the enforcement stack.

**Both lists narrow, and narrowing only one is a real hole.**
`allowedDNSSuffixes` gates name resolution at the resolver; `allowedHosts`
gates HTTP/CONNECT at the proxy. A pin writes to both — narrowing DNS while
leaving HTTP wide (or vice versa) would leave the box reachable through
whichever layer was missed. The pin file tags each entry with the matcher it
belongs to (`dns:` / `host:`), because under `--exact` the two lists genuinely
differ; an untagged entry applies to both.

### The one thing a pin can never seal: `.amazonaws.com`

A compiled-in carve-out always allows `.amazonaws.com`, in both scopes,
regardless of how many pins have been taken. It is not configurable and there
is no flag to disable it.

This is a fuse, not a convenience. Under `ebpf`/`both` enforcement
`/etc/resolv.conf` points at the on-box resolver, and **DNS is neither uid- nor
cgroup-scoped** — so that resolver answers for root services too, above all
`amazon-ssm-agent`. Root's destinations barely reach the census (the eBPF audit
consumer only watches the sandbox cgroup), so a pin would deny them by
construction. A pin that NXDOMAINs `.amazonaws.com` kills the SSM control
plane, and with no un-pin verb, no `km shell` and no remote destroy, the only
recovery left is terminating the instance.

So: **a pin can seal everything a sandbox reaches except the channel through
which a mistake can still be undone.** `km-netpolicy pin` prints the floor
before it writes, and `km-netpolicy list` prints it beside the generations.

The floor covers pins only. `km-netpolicy deny .amazonaws.com` still blocks
exactly as it always did — a deny is an explicit, named act, where a pin denies
everything nobody named.

## Collapsed by default, `--exact` opt-in

DNS suffixes are **always** collapsed to eTLD+1 (`api.github.com` →
`.github.com`) — names under a domain vary per request, so an exact-match DNS
allowlist stops resolving almost immediately. Hosts collapse **by default**
too; `--exact` pins the literal observed hostnames instead.

That asymmetry is why the pin file scopes its entries. Under `--exact` a name
like `gist.github.com` still *resolves* (the DNS scope holds `.github.com`) but
the HTTP proxy refuses the *connection* (the host scope holds only
`api.github.com`). Narrowing lives at the layer that can express it.

```console
$ km-netpolicy pin --dry-run
pinned DNS suffixes (2):
  .github.com
  .npmjs.org

pinned hosts (2):
  .github.com
  .npmjs.org

mode: collapsed. With --exact you would pin instead:
  suffixes (2): [.github.com .npmjs.org]
  hosts (2): [api.github.com registry.npmjs.org]

nothing written. Pinning is IRREVERSIBLE — there is no un-pin verb,
and recovering from a too-tight pin means km destroy && km create.
Re-run with --yes to apply.
```

**The default sits on the loose side deliberately, because the risk is
asymmetric and pin has no reverse gear.** Pin too loose and the fix is a
follow-up `pin` — narrowing further is always available and always cheap.
Pin too tight with `--exact` and there is no recovery at all: no un-pin verb
exists, so the only fix is `km destroy && km create` and redoing the session.
A CDN edge hostname or a mirror the session happened not to hit that day is
exactly the kind of thing `--exact` silently strands. Reach for `--exact` only
once you're confident the census is genuinely complete, and always look at
`--dry-run` first — it prints both candidate lists side by side so the cost of
`--exact` is visible before you commit to it.

## Two sharp edges

**An empty census pins to deny-all.** `pin` on a box that has touched nothing
yields an empty candidate set, and an empty allow set denies everything —
correct by the math, surprising in practice. `pin` refuses to proceed on an
empty census without an explicit `--allow-empty`:

```console
$ km-netpolicy pin --yes
km-netpolicy pin: the census is empty, so this pin would deny ALL egress.
  This is usually a sign the box has not done its work yet.
  Pass --allow-empty if sealing the box is genuinely what you want.
```

**Pin is a snapshot, not a mode.** It freezes the census as of the instant it
runs; anything genuinely new the box reaches afterwards is denied, same as any
other destination outside the allowlist. The workflow is *finish setup, then
pin* — never *pin early and keep working*. Pinning mid-session and continuing
to install things is how a legitimate destination ends up permanently denied
with no way back except redoing the box.

`pin` without `--dry-run` always requires `--yes` — there is no default-yes
path onto an irreversible operation.

```
km-netpolicy pin [--dry-run] [--exact] [--yes] [--allow-empty] [--accept-truncated]
```

**A census can be silently incomplete, and `pin` refuses when it can prove it
is.** Each producer's flow file rotates at 16 MiB and exactly one prior
generation is kept, so the *second* rotation discards the oldest records for
good — typically the package-install phase, which is exactly the traffic an
operator means to pin. `pin` notes any rotation, and refuses outright when a
producer has rotated more than once unless you pass `--accept-truncated`.
Destinations lost to rotation would otherwise be pinned out irreversibly with
no signal they ever existed.

## `capture` — on-demand packet capture

```
km-netpolicy capture start [--duration 5m] [--max-size 250MB] [--port N] [--host H]
km-netpolicy capture stop
km-netpolicy capture status
km-netpolicy capture list
```

Capture is opt-in and explicitly triggered per invocation — never always-on,
and never unbounded. `--duration` defaults to 5 minutes with a hard ceiling of
60 minutes; `--max-size` defaults to 250MB with a hard ceiling of 2GB, and
hitting it **stops** the capture — it does not ring-rotate. For a bounded
diagnostic that is the better trade: a ring would keep the last N bytes and
discard the beginning of the trace, which on a "why did this connection fail"
question is usually the part that matters. Stopping leaves a complete prefix,
and you can start another. There is no flag that raises either ceiling; a capture is a
diagnostic, not a recording studio, and an unbounded one on a sandbox with a
finite data volume can fill the disk and take the box down. `start` also
refuses unless the capture directory has at least 2× `--max-size` free.

`--port N` and `--host H` narrow the capture to that port and/or host (both
together is an AND — matching traffic must satisfy whichever of the two you
gave); omit both to capture everything reaching the interface.

A capture runs under a root-owned daemon (`km-capture.service`) that an
unprivileged `km-netpolicy capture` client talks to over a Unix socket. The
sandbox user never holds `CAP_NET_RAW` itself, so this behaves identically on
`privileged: false` boxes — the daemon is the only thing that needs the
capability, and it does nothing until asked.

```console
$ km-netpolicy capture start --duration 1m --host api.github.com
capturing to /var/lib/km/capture/20260828T140212Z.pcap (max 1m0s / 262144000 bytes)

$ km-netpolicy capture stop
stopped /var/lib/km/capture/20260828T140212Z.pcap
  /var/lib/km/capture/20260828T140212Z.pcap
uploaded: s3://example-artifacts-123456789012/captures/sb-abc123/20260828T140212Z.pcap
```

`capture stop` uploads the finished file to the artifacts bucket under
`captures/{sandbox_id}/` and prints the S3 URI. Download it with the AWS CLI
and open it in Wireshark or `tcpdump -r`. There is deliberately no laptop-side
`km capture` verb — captures are on-box first, retrieved from S3 after.

**Honest scope note: on a modern sandbox, most captured bytes are opaque
TLS.** A pcap does not substitute for `flows`, and it is not a general "see
what the agent sent" tool — that question already has an answer, and it's
`flows`. What a pcap earns its place on is what the other layers structurally
cannot see at all: non-HTTP protocols, traffic on unusual ports, raw sockets
that bypass the proxies entirely, malformed or blocked traffic, and DNS
oddities that never made it as far as a clean verdict. Reach for it when
`observed`/`flows` leave a genuine question the layered verdicts can't answer,
not as a first move.

## Deploy surface

```bash
make build            # operator binary
make build-lambdas    # create-handler renders the new user-data
km init --dry-run=false
```

**Not `--sidecars`.** The new unit (`km-capture.service`), the new env vars on
both proxy units, and the new append-only pin file all ride in the user-data
the create-handler zip renders — `--sidecars` re-uploads sidecar binaries but
does not rebuild that zip, so it would leave the wiring absent even though the
`km-netpolicy` binary itself picked up the new verbs.

Observation is unconditional, so this is not a dormant feature and there is no
byte-identical case: **userdata goldens change for every profile.** Existing
sandboxes gain nothing until `km destroy && km create` — the binary and the
unit files are baked in at boot, not fetched fresh on an already-running box.

### The sidecar/userdata lockstep — tighter than any prior phase

`km ebpf-attach` gains two REQUIRED flags (`--netpolicy-pins`, `--flowlog-dir`)
that the create-handler-rendered userdata now always passes. The two halves come
from two different places:

- the enforcer binary is `s3://<bucket>/sidecars/km`, uploaded by
  `buildAndUploadSidecars` (`km init`'s sidecar step)
- the userdata that invokes it comes from the create-handler zip
  (`make build-lambdas` + `km init`)

Run `make build-lambdas` + `km init --lambdas` **without** the sidecar upload and
every `ebpf`/`both` sandbox boots with an enforcer that rejects an unknown flag
and crash-loops — i.e. **no network enforcement at all**, on a box that looks
otherwise healthy. `km init --dry-run=false` does both halves, which is why it is
the documented sequence; deviating from it here is unusually severe. Do not
split the deploy across two invocations, and do not use `--sidecars` alone
(which updates the binary but not the userdata that must pass the new flags).

## Troubleshooting

- **`km-netpolicy list` shows nothing under "allow pins"** — the box is
  genuinely unpinned. `list` reports pins alongside denies for the same reason
  `/etc/km/netpolicy.env` mirrors profile-baked denies: without it, a pinned
  box would print nothing about its pins and read as though it had none.
- **A pin appears to have no effect under `ebpf`/`both` enforcement** — check
  that the pinned host isn't already seeded into the BPF allow-trie from boot.
  The boot-time pre-seed loop (`internal/app/cmd/ebpf_attach.go`) now consults
  the pin store before seeding, but a box created before this phase shipped
  won't have that check; recreate it.
- **`capture: cannot reach the capture daemon`** — check
  `systemctl status km-capture`; the error message names this command
  directly.
- **`capture start` refuses with "only N bytes free"** — the capture
  directory doesn't have 2× `--max-size` free. Lower `--max-size` or free disk
  first; there is no override.
- **A flow producer's file is simply absent** — that source is missing, not
  corrupt. Each producer owns one file; a dead DNS proxy means `flows` shows
  no `dns` rows while `http`/`ebpf`/`resolver` rows keep appearing, not a
  broken command.
- **`observed` prints `(none)` on a box that has clearly reached things** —
  check the sidecar journals for `flowlog_write_failed`. The Writer logs its
  own first failure once per producer; the producers themselves discard the
  error on the egress hot path (an unwritable store must never stall a
  connection), so the journal is where a permission or disk problem surfaces.
  `/var/lib/km/flows` must be mode `1777`: km-dns-proxy and km-http-proxy run
  as `km-sidecar` while the eBPF enforcer and its resolver run as root, so a
  root-owned directory makes every proxy write EACCES.
- **`capture list` prints a path the agent cannot open** — captures are
  written `0640` and chowned to the `sandbox` group. If the profile has no
  such group the chown fails and the file stays `root:root`; S3 is the
  intended retrieval path either way (`capture stop` prints the `s3://` URI),
  and the local file is the fallback the operator reads over SSM as root.
- **Which producer answered DNS?** Under `proxy` enforcement it is
  `src=dns` (km-dns-proxy). Under `ebpf`/`both` the bootstrap leaves
  km-dns-proxy disabled and the on-box eBPF resolver serves every query
  instead, so the rows read `src=resolver`. Both carry names and verdicts;
  `src=ebpf` rows are the ring-buffer's address-level denies and redirects.
