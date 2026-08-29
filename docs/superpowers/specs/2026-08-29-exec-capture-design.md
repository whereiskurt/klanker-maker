# Phase 132 — Exec capture: every command a sandbox ran, and which one reached that host

**Status:** design approved, not yet planned
**Branches from:** `feat/live-egress-census-pin-capture` (PR #89, open)
**Design date:** 2026-08-29

---

## 1. The problem

A km sandbox can say where it has been. It cannot say what ran.

Command capture today is `_km_audit`, a `PROMPT_COMMAND` hook installed into
`/etc/profile.d/km-audit.sh` (`pkg/compiler/userdata.go`). It records exactly one
class of thing: a command a human typed at an interactive shell prompt. Every
process an agent spawns — `git`, `python`, `curl`, `rg`, anything a tool calls, and
everything those call in turn — is invisible. On an autonomous box, which is what
km exists to run, that is nearly all of it.

Phase 131 shipped the mirror-image capability for the network: four producers append
every egress decision to `/var/lib/km/flows/`, and `km-netpolicy observed|flows`
reports it. So a sandbox can now answer *"where did you go"* and cannot answer
*"what were you running when you went there"* — which is the version of the question
an operator actually has after an agentic run.

Half of that join already exists. `flowlog.Record` carries `PID` and `Comm`
(`pkg/flowlog/record.go:70-71`), populated on the eBPF path. What is missing is the
other side: a record of what each pid *was*.

## 2. Non-goals

- **Not a control.** Exec capture never blocks, delays, or denies an exec. It is
  observation only, in the same sense the flow census is.
- **Not a redactor.** See §4.3.
- **Not an agent-facing feature.** The sandbox user never reads this store (§4.2).
- **No operator-side CLI verb.** Retrieval is `aws s3 cp`, exactly as it is for
  Phase 131 packet captures. Adding an operator verb here and not there would be an
  inconsistency, not an improvement.
- **No syscall coverage beyond exec.** File opens, network syscalls, and ptrace are
  out of scope. `km-audit-log` and the flow census own their own domains.

## 3. Decisions

Each of these was taken deliberately; the rationale matters more than the choice.

### 3.1 eBPF, not auditd

`auditd` would be less code and is mature. It is rejected because it stands up a
second, parallel audit system beside km's own, with its own daemon, config
dialect, and noise profile — and because it would give pid/comm in a format
disconnected from the flow store rather than one that joins to it.

The eBPF path lands next to `pkg/ebpf`, reuses the `pkg/flowlog` producer/writer
pattern Phase 131 proved, and yields `pid`/`ppid`/`uid`/`cgroup_id` in the same
vocabulary the flow records already speak. The join is the point of the phase, and
only one of the two mechanisms produces data that joins.

### 3.2 Its own daemon, provisioned unconditionally

**The trap this avoids:** the entire eBPF section of userdata is wrapped in
`{{- if or (eq .Enforcement "ebpf") (eq .Enforcement "both") }}`
(`pkg/compiler/userdata.go:4827`). Under `proxy` enforcement — the schema default —
there is no BPF loaded, no `km-ebpf-enforcer.service`, and no `km.slice` cgroup at
all. Exec capture implemented inside `km ebpf-attach` would therefore be **dead in
the default mode, silently** — the identical failure Phase 131's whole-branch review
caught, where flow recording was dead under `proxy` because a root-owned `0755`
directory `EACCES`ed every write from the unprivileged proxies.

So capture runs in its own root daemon, `km-execlog.service`, loading *only* the
execve tracepoints: no cgroup programs, no resolver, no enforcement. It is
provisioned outside the `Enforcement` conditional.

Two alternatives were considered and rejected:

- **Un-gate `km ebpf-attach` so the enforcer runs in all three modes.** Least new
  code, and the unit template even carries a dormant `--dns-port 0` else-branch
  suggesting someone once intended it. That dormancy is what makes it dangerous —
  nobody has run it. It would load cgroup BPF onto every default-mode sandbox to
  gain a feature that needs no enforcement machinery.
- **A new `km-execlog` sidecar binary.** The honest name and the cleanest module
  boundary, but it adds a sidecar `s3 cp` at boot that 404s during a partial
  rollout — under `set -euo pipefail` that aborts the whole bootstrap. Phase 131
  designed around exactly this by folding packet capture into `km-netpolicy`
  instead of shipping a fourth binary.

**Unconditional provisioning is justified the same way the census is:** a facility
that only records what already happened cannot widen a policy by being present.
Userdata goldens change for every profile again. There is no byte-identical case.

### 3.3 Capture everything, filter at read

Every exec on the box is captured, stamped with `uid`, `pid`, `ppid`, and
`cgroup_id`; the read verbs filter.

The decisive case is `privileged: true`, which `profiles/dc34.yaml` sets. The moment
a privileged agent `sudo`s, its most interesting activity *becomes root activity* —
a uid filter would blind the trace to precisely the escalation worth seeing. Scoping
to the sandbox cgroup instead would reproduce the §3.2 blindness, since that cgroup
does not exist under `proxy`.

Capturing everything also makes "something ran outside the sandbox user" a visible
signal rather than a blind spot. Noise is a read-time filter; absence is permanent.

### 3.4 Bounded argv, no redaction

argv is recorded up to the BPF ceiling with an explicit `truncated` flag. There is
no redaction pass.

`captures/{sandbox_id}/` in the same artifacts bucket already receives full packet
captures, and a pcap carries strictly more secret material than argv ever will.
Adding a redactor here would apply a control the more-sensitive neighbouring
artifact does not have, and a redactor that silently under-matches is worse than
none because it implies a guarantee. `curl https://x` is the entire value of the
feature; a trace of bare executable names answers nothing.

This decision has a direct consequence on store permissions — see §4.2.

## 4. Architecture

### 4.1 Capture

A new `pkg/ebpf/exec/` subpackage carrying its own `exec.c`, its own `gen.go`
bpf2go directive, and its own compiled object — so `pkg/ebpf/bpf.c`, its generated
loader, and the enforcer that depends on both are untouched by this phase.

It follows the same build-tag split `pkg/ebpf` already uses (`//go:build linux`
implementation plus a stub), because `cmd/km-netpolicy` imports it and must keep
building and testing on darwin. `cilium/ebpf` is pure Go, so the loader
cross-compiles under `CGO_ENABLED=0` exactly as the AF_PACKET capture does.

Four tracepoints:

| Tracepoint | Role |
|---|---|
| `syscalls/sys_enter_execve` | read argv, stash in-flight record in a hash keyed by `pid_tgid` |
| `syscalls/sys_enter_execveat` | same, for the `execveat` entry point |
| `syscalls/sys_exit_execve` | emit the record, stamped with the return code |
| `sched/sched_process_exit` | record process end — see §5 |

`sys_enter_execve` is the attach point rather than `sched/sched_process_exec`
because argv arrives there directly as a `char *const *`, read with
`bpf_probe_read_user_str` in a bounded loop. Reading argv at `sched_process_exec`
instead means walking `current->mm->arg_start` via CO-RE, which is more fragile for
no gain.

**Failed execs are recorded, not dropped.** An agent reaching for a binary that is
not installed, or one it is not permitted to run, is a finding. The `ret` field
distinguishes them.

Bounds: `MAX_ARGS = 20`, `ARGSIZE = 128`. Overflow sets `truncated` rather than
silently clipping.

### 4.2 Store

`/var/lib/km/execs/execs.jsonl`, JSONL, one record per line.

There is exactly one producer — the root daemon — so none of the `1777`
multi-writer reasoning behind the flow directory applies here.

**The directory is `0700` root-only, and the read verbs require root.** This falls
directly out of §3.3 and §3.4 in combination: root's unredacted argv, readable by
the sandbox user, is an information leak on exactly the *unprivileged* profiles that
are supposed to be the tighter ones. The exec log is an operator forensics artifact;
the agent has no reason to read it. The operator reads it via `km shell --root`, or
from S3 after a `save`.

Rotation semantics are copied from `flowlog.Writer`: 16 MiB cap, one retained
generation, a `.rotations` sidecar counter, and a **one-shot write-failure warning**.
That last one is not optional given how Phase 131's silent `EACCES` went — a store
that fails silently is worse than no store, because it reads as "nothing happened."

Under `/var/lib` rather than `/run`, for the same reason the deny list and flow
store are: a trace silently emptied by a reboot would misrepresent what a box did.

### 4.3 Read surface

New verbs on `km-netpolicy`. This continues that binary's drift from "network policy
tool" toward "on-box forensics tool" — it already owns packet capture. The drift is
accepted deliberately: the alternative is a new sidecar binary that can 404 at boot
(§3.2). A rename is explicitly out of scope for this phase.

```
km-netpolicy execs [--since 10m] [--uid N] [--failed] [--json]
km-netpolicy who <host>
km-netpolicy execs save
km-netpolicy execs-daemon          # hidden; driven by the unit
```

`who <host>` is the verb that justifies the phase. Everything else is plumbing
around it.

`execs save` uploads the current trace to `s3://<artifacts>/execs/{sandbox_id}/execs-<ts>.jsonl`
using the AWS SDK and the instance role, modelled on `uploadCapture`
(`cmd/km-netpolicy/capture_daemon_linux.go:399`) — a Go verb, not a bash script.
Best-effort: on failure the file stays local and the verb says so. It is named
`save` because km already runs that direction under that name (`km rsync save`);
`upload` and `push` would be new vocabulary for an existing idea.

### 4.4 Provisioning

`km-execlog.service` — root, unconditional, provisioned beside the flow store and
outside the `Enforcement` conditional:

```
ExecStart=/opt/km/bin/km-netpolicy execs-daemon
ExecStop=/opt/km/bin/km-netpolicy execs save
Environment=AWS_REGION={{ .AWSRegion }}
Environment=KM_ARTIFACTS_BUCKET={{ .KMArtifactsBucket }}
Environment=KM_SANDBOX_ID={{ .SandboxID }}
Environment=KM_EXEC_DIR={{ .ExecLogDir }}
```

`AWS_REGION` is present from the first commit. Its absence is the bug `216d4664`
just fixed on `km-capture.service`, where the SDK failed with *"Invalid region:
region was not a valid DNS name"* — which reads as a bucket problem rather than a
config one, and degraded silently because the upload is best-effort.

`ExecStop` means a graceful shutdown saves without anyone remembering: `km stop`,
`km pause`, and the ACPI shutdown preceding an instance terminate all run it. Never
blocking; a hard power-off still loses the trace, and the docs say so rather than
implying a durability guarantee that does not exist.

The directory is created alongside the flow store, at `0700`.

### 4.5 IAM

`infra/modules/ec2spot/v1.6.0` — a **new immutable directory**, never an edit to
`v1.5.0` — adding `execs/${var.sandbox_id}/*` to the existing `s3:PutObject`
statement that already covers `transcripts/`, `captures/`, and `learn/`
(`infra/modules/ec2spot/v1.5.0/main.tf:564-573`). Plus the pin bump in
`locals.substrate_module_versions`.

## 5. The join, and the two things it must be honest about

`who <host>` reads the flow store and the exec store and correlates on pid.

**PID reuse.** Joining on pid alone is wrong after wraparound: a flow could be
attributed to a process that merely inherited the number. `sched_process_exit` gives
exact process lifetimes, so the join is *"the exec for this pid whose lifetime
contains the flow record's timestamp"* — a fact rather than a heuristic. That is the
entire reason for the fourth tracepoint, and the join must not be implemented
without it.

**The join only works under `ebpf`/`both`.** `flowlog.Record.PID` is populated on the
eBPF path only. The DNS and HTTP proxies observe a connection, not an originating
pid, and recovering one would require a `/proc/net/tcp` source-port → inode → fd walk
on the egress hot path — racy and expensive. So under `proxy` enforcement, the
default, `who` has nothing to join and returns empty.

This is a **stated limitation, documented in the same voice as Phase 131's
`transparent.go` gap** — not something for an operator to discover by running `who`
on a proxy box and getting silence. `who` must say *why* it is empty when the box is
in `proxy` mode, naming the mode, rather than printing `(none)`.

The exec trace itself is complete in all three modes. Only the correlation is
mode-dependent.

**Operator decision (2026-08-29): this gap is accepted and is not to be
re-litigated.** In practice `ebpf`/`both` on EC2 is the deployment; `proxy` is
effectively the Docker-substrate path, and Docker/local sandboxes are not in use.
So `who` is live on every box that actually runs. This also means §3.2's
unconditional daemon earns its keep for **mode-independence and separation of
observation from enforcement** — not for rescuing a default anyone runs. That is
still the right build: it costs one systemd unit, and tying observation to an
enforcement mode is what created the Phase 131 defect in the first place.

## 6. Record schema

```go
type Record struct {
    TS        time.Time `json:"ts"`
    PID       int       `json:"pid"`
    PPID      int       `json:"ppid"`
    UID       int       `json:"uid"`
    Comm      string    `json:"comm"`
    Exe       string    `json:"exe"`
    Args      []string  `json:"args,omitempty"`
    Truncated bool      `json:"truncated,omitempty"`
    Ret       int       `json:"ret"`
    CgroupID  uint64    `json:"cgroup_id,omitempty"`
}
```

`Ret` is not `omitempty`: zero is success, and a field absent because it was zero
would be indistinguishable from one absent because it was never recorded. The same
reasoning `flowlog.Record` applies to its optional fields, pointed the other way.

## 7. Testing

- **Pure Go, host machine:** record marshalling, writer rotation and the
  `.rotations` counter, reader tolerance of corrupt lines, and the pid/lifetime join
  including the wraparound case and the empty-under-`proxy` case.
- **BPF programs:** no `bpf.NewVM` equivalent exists for tracepoints, so unlike the
  capture filter (`dc6d746e`) the semantics cannot be proven in-process. These
  follow the established cross-compile path: `go test -c` for `linux/amd64`, run
  under Docker (`project_linux_only_test_via_crosscompiled_binary`; `CGO_ENABLED=0`,
  never compile inside qemu).
- **Open risk:** whether tracepoint attach works inside that container is a genuine
  unknown. It is resolved in wave 1, not discovered in the last wave.
- **Live UAT:** §8. Nothing below substitutes for it.

## 8. UAT — VulnHunter against defcon.run.34

New `profiles/vulnhunt.yaml`: `dc34`'s base fragment set, `enforcement: both` so the
join is live, cloning `capitalone/VulnHunter` and installing its `vulnhunt/` skill
into `~/.claude/skills/`, with `capitalone/VulnHunter` added to
`sourceAccess.github.allowedRepos`. The scan target is `whereiskurt/defcon.run.34`,
already cloned into `/workspace` by the `dc34` lineage and already allowlisted.

`useBedrock: true`. Besides matching `dc34`, this sidesteps VulnHunter's own
cyber-safeguard warning, which concerns Anthropic's first-party platforms — Bedrock
is not one.

VulnHunter is a prompt-only Claude Code skill driving Opus through recon → parallel
hunt → adversarial disprove → capability filter. It is chosen as the UAT precisely
because it is subprocess-heavy, genuinely agentic, and authorized (the target is the
operator's own repository) — a workload no synthetic test reproduces.

The run proves what unit tests cannot:

1. A real agentic workload produces a non-empty trace.
2. `who <host>` names the actual process behind a flow.
3. `sudo`'d execs land as `uid 0` — the §3.3 case.
4. `execs save` lands an object in `s3://<artifacts>/execs/{id}/`.
5. `ExecStop` saves on a `km stop` without an explicit `save`.
6. Rotation counters behave under thousands of execs, and `--since` stays usable at
   that volume.
7. `who` on a `proxy`-enforcement box explains itself rather than printing `(none)`.

## 9. Deploy surface

`make build` + `make build-lambdas` + `km init --dry-run=false`.

**Not `--sidecars`.** The `ec2spot` IAM change needs a full terragrunt apply, and
the userdata carrying the new unit rides in the create-handler zip, which
`--sidecars` does not rebuild.

**Do not split the deploy.** The `km-netpolicy` binary is uploaded by
`buildAndUploadSidecars`, while the unit whose `ExecStart` invokes its new verb is
rendered into userdata by the create-handler. Ship one half without the other and
`km-execlog.service` crash-loops on an unknown verb — the same lockstep that Phase
131 documented for `km ebpf-attach`'s two required flags. The full `km init` does
both halves.

Existing sandboxes gain nothing until `km destroy && km create`.

## 10. Known gaps and accepted risks

- **`who` is empty under `proxy` enforcement** (§5). Accepted by the operator
  2026-08-29 on the grounds that `proxy` is the Docker path and Docker is not in
  use; documented; the verb explains itself rather than printing `(none)`.
- **argv is bounded at 20 × 128 bytes.** A long command line is truncated with the
  flag set. Accepted — BPF loop and stack limits make unbounded argv impractical.
- **A hard power-off loses the unsaved tail** (§4.4). Accepted; no honest mitigation
  short of streaming, which §3 rejected on volume grounds.
- **On a `privileged: true` box the agent can stop `km-execlog.service`.** Identical
  in kind to the Wiz-sensor limitation: the real control is `privileged: false`, and
  detection belongs off-box. Not tamper-proof, deliberately.
- **Exec records are not enrolled in any quota or budget.** Volume is bounded by
  rotation alone.

## 11. Recorded debt

`pkg/execlog` duplicates `flowlog.Writer` — rotation, the `.rotations` counter, and
the one-shot warning — rather than sharing it. This is deliberate: PR #89 is open and
under review, and `pin` correctness depends on those rotation-counter semantics, so
churning them inside a reviewed-but-unmerged PR trades a real risk for a cosmetic
gain.

**Follow-up:** once #89 merges, extract a shared `pkg/jsonlstore` and move both
`flowlog` and `execlog` onto it. Recorded here so it is a decision with an owner
rather than drift nobody named.
