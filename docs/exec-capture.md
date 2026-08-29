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
$ km-netpolicy who api.github.com
14:02:11  allow    ebpf     api.github.com  ← pid=4299 curl -s https://api.github.com/repos/capitalone/VulnHunter
14:05:40  allow    resolver api.github.com  ← pid=5012 git fetch origin main
```

**`who` returns nothing under `proxy` enforcement — by design, and this is an
accepted limitation, not a bug to work around.** `flowlog.Record.PID` is
populated only on the eBPF enforcement path; the DNS and HTTP proxies observe
a connection, not the pid that opened it, and recovering one would mean a
racy `/proc/net/tcp` source-port → inode → fd walk on the egress hot path.
So on a `proxy`-enforcement box (the schema default), `who` has nothing to
join a flow against and says so explicitly rather than printing `(none)`:

```console
$ km-netpolicy who api.github.com
None of these flows could be attributed to a process.
Only the eBPF enforcement path records a pid alongside a flow, so pid
attribution needs spec.network.enforcement: ebpf or both. The exec trace
itself is complete regardless — see `km-netpolicy execs`.
```

This is the same shape of honesty as Phase 131's `transparent.go` gap: the
capability has a hole, and the tool names it instead of hiding it behind
silence. **The operator accepted this gap on 2026-08-29** on the grounds that
`proxy` enforcement is effectively the Docker-substrate path, and Docker
sandboxes are not in use — every sandbox that actually runs today is
`ebpf`/`both`, where `who` is fully live. The exec trace itself (`execs`) is
complete in **every** enforcement mode; only the correlation is
mode-dependent.

Attribution can also come back partial even under `ebpf`/`both` — a flow with
no matching exec record (rotation discarded it, or the pid's lifetime window
doesn't cover the flow's timestamp) prints its own reason rather than being
silently dropped from the listing:

```
14:07:02  allow    ebpf     140.82.113.6:443  ← (no exec recorded for that pid)
```

### `execs save`

Uploads the live trace to S3 and prints the URI:

```console
$ km-netpolicy execs save
saved: s3://example-artifacts-123456789012/execs/sb-abc123/execs-20260829T140500Z.jsonl (48213 bytes)
```

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

## Why its own daemon, not the eBPF enforcer

The entire eBPF section of userdata is wrapped in the `enforcement: ebpf` /
`both` conditional. Under `proxy` — the schema default — there is no BPF
loaded, no `km-ebpf-enforcer.service`, and no cgroup at all. Building exec
capture inside `km ebpf-attach` would therefore be **dead in the default mode,
silently** — the same failure shape Phase 131 caught when flow recording was
dead under `proxy` because of a permissions bug nobody could see without
looking.

So capture runs in its own root daemon, `km-execlog.service`, loading only the
four execve/exit tracepoints — no cgroup programs, no resolver, no
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
`km-netpolicy execs save` yourself.

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
- **`who <host>` says it can't attribute anything** — read the message it
  prints. It names whether the box is missing eBPF enforcement entirely
  (`proxy` mode) or whether specific flows just have no matching exec record
  (pid-lifetime window miss, or rotation discarded it) — those are different
  problems with different fixes, and the verb distinguishes them rather than
  collapsing both into silence.
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

See `.planning/phases/132-exec-capture/132-UAT.md` for the full live-UAT
record (VulnHunter vs. `defcon.run.34`) and
`docs/superpowers/specs/2026-08-29-exec-capture-design.md` for the design
rationale behind every decision above.
