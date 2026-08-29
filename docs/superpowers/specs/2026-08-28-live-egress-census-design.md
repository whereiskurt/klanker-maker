# Design spec — Live egress census, allowlist pinning, on-demand packet capture

Phase 130. New on-box verbs: `km-netpolicy observed | flows | profile | pin | capture`.

## Problem

A sandbox knows where it has been. It just has no way to say so.

Three producers already observe every egress decision, and all three throw the
answer away or ship it somewhere the box cannot reach:

- the eBPF ring buffer emits a record per connection — timestamp, pid, `comm`,
  src/dst IPv4, dst port, verdict (`allow`/`deny`/`redirect`) — which the audit
  consumer streams to CloudWatch and never retains locally;
- the DNS proxy logs every query and its allow/deny verdict to journald;
- the HTTP proxy logs every host and its verdict the same way.

Learn mode goes one step further and accumulates a deduplicated
`allowlistgen.Recorder` — but it lives in the eBPF enforcer's memory, flushes
only on `SIGUSR1` or shutdown, and the signal is sent from the operator's
laptop by `km shell --learn` at session exit. So the census exists, exactly
once, at the moment the session ends, on a machine that is not the sandbox.

The operator workflow this blocks is concrete: spin up a wide-open box, install
packages, run real work, and then — mid-session, before deciding anything — ask
"what has actually left this machine?" and turn that answer into the policy the
box runs under for the rest of its life. Today the only way to get the answer is
to end the session, and the only way to apply it is to author a profile and
create a *different* sandbox.

The narrowing half of this already shipped. `km-netpolicy deny` (v0.8.8, on
every sandbox) lets a box block destinations from user-land, effective in ~1s.
What is missing is the other direction: a box cannot enumerate what it has
reached, and cannot collapse a wide-open allowlist down to only that.

## Operator decisions (locked — do NOT re-litigate during execution)

1. **Observation is on for every sandbox, unconditionally.** Not gated on
   `learnMode`, not gated on a new profile field. The question "where has this
   box been" is worth *more* on a locked-down box during an incident than on a
   box someone pre-designated for learning.
2. **Flow coverage spans both enforcement paths.** The eBPF consumer feeds the
   store under `ebpf`/`both`; the DNS and HTTP proxies feed it under
   `proxy`/`both`. `proxy` is the default mode and has no eBPF enforcer at all,
   so an eBPF-only design would exclude most of the fleet.
3. **`pin` enforces live and is irreversible.** It intersects the effective
   allowlist with the observed set, effective in ~1s. No un-pin verb exists.
4. **Retained L7 plaintext is OUT of scope.** Deliberately deferred. It is the
   highest-value-per-byte data and the highest risk — a file of live bearer
   tokens on a box whose premise is running untrusted agents. Revisit only after
   flows + pcap have been used in anger.
5. **Packet capture is opt-in, bounded, and explicitly triggered.** Never
   always-on. Mandatory duration and size caps.
6. **Autonomy: code, tests, docs, PR. No deploy, no live UAT, no merge.**

## Design

### Flow store — one append-only file per producer

```
/var/lib/km/flows/flows.ebpf.jsonl
/var/lib/km/flows/flows.dns.jsonl
/var/lib/km/flows/flows.http.jsonl
```

Each producer owns exactly one file, appends JSONL, and rotates its own file by
size. `km-netpolicy` merges them at read time.

A single shared file was rejected: three writers racing on one rotation is a
corruption bug waiting to happen. A Unix-socket daemon was rejected too — it
adds an always-on process whose death silently loses data, to solve a problem
per-producer files do not have. Per-producer files also degrade honestly: a dead
producer means "that source is missing", never a corrupt store.

This mirrors what the repo already does. `netpolicy.Store` is a lazily-reloading
file reader consulted per egress decision; the flow store is the same shape in
the opposite direction.

`pkg/flowlog` holds the shared record schema, exactly as `pkg/netpolicy` holds
the shared deny vocabulary. One writer type, used by all three producers.

### Record schema

```json
{"ts":"2026-08-28T14:02:11Z","src":"dns","verdict":"allow","host":"api.github.com",
 "addr":"140.82.113.6","port":443,"proto":"tcp","pid":4211,"comm":"git"}
```

Producers fill what they know and omit what they do not. The DNS proxy knows the
name and verdict but not the eventual peer; the HTTP proxy knows host, port and
verdict; the eBPF path knows address, port, pid, `comm` and verdict but not the
name. **No producer fabricates a field it did not observe.**

### Name↔address correlation is best-effort, and says so

The eBPF resolver already maintains a name→IP map to populate the LPM trie.
Merge-time correlation consults that map and nothing else. Flows whose address
is not in it are reported IP-only. There is no reverse-DNS lookup and no
heuristic inference — a wrong hostname on a flow record is worse than an absent
one, because the operator would pin against a destination the box never had.

### Verbs

- **`observed`** — the deduplicated destination set in allowlist vocabulary,
  with **denied attempts listed separately**. Denied destinations are never
  silently folded into the allowed census; they are the most interesting rows
  on the page.
- **`flows [--since 10m] [--denied] [--json]`** — raw per-connection records.
- **`profile`** — emits a SandboxProfile YAML from the observed set. Nearly
  free: `allowlistgen.Recorder` and `GenerateAnnotatedYAML` already exist and
  already produce the annotated `learned.*.yaml` shape operators know.
- **`pin [--dry-run] [--exact] [--yes]`** — see below.
- **`capture start|stop|status|list`** — see below.

### `pin` — narrowing an allowlist, provably

The existing deny contract is a union:

```
effective deny = union(profile denies, runtime deny file)
```

Union is monotone, so a deny can only shrink what is reachable. `pin` is the
mirror image, and monotone for the same reason:

```
effective allow = profile allow ∩ pin₁ ∩ pin₂ ∩ …
```

It gets its own kernel-append-only file, `/var/lib/km/netpolicy/allow.pins`
(`chattr +a`), alongside the deny list. Same guarantee, same enforcement, same
absence of a removal verb. "Never widens" stays a property of the data structure
and the filesystem, not a rule anyone has to trust.

Precedence is unchanged and pin slots in without disturbing it:

```
deny  →  pins narrow the allow  →  profile allowlist
```

A pin can never re-allow something a deny blocked.

**Allow-all is the motivating case and needs no special handling.**
`profiles/base/network/safenetwork.yaml` is literally `allowedDNSSuffixes:
["*"]` / `allowedHosts: ["*"]`, and both the resolver and the http-proxy honor
`*` as allow-all. Since `* ∩ observed = observed`, pinning from a wide-open box
yields precisely the set it touched. The intersection does the work; there is no
"if allow-all then …" branch.

**Both lists are narrowed, and that is load-bearing.** `allowedDNSSuffixes`
gates resolution at the resolver; `allowedHosts` gates HTTP/CONNECT at the
proxy. Narrowing one and not the other leaves the box reachable through the one
that was missed.

**Only allowed observations are pinnable.** A destination that was denied was
never in the allow set, so it cannot be in an intersection of allow sets.
Denied rows appear in `observed` for the operator's benefit and are excluded
from the pin by construction.

#### Collapsed by default, `--exact` opt-in

`CollapseToDNSSuffixes` already exists — it maps observed FQDNs to eTLD+1
suffixes (`api.github.com` → `.github.com`) and skips bare TLDs so a pin can
never generate `.com`.

- DNS suffixes are **always** collapsed. Names under a domain vary per request;
  an exact-match DNS allowlist stops resolving almost immediately.
- Hosts are collapsed **by default**, with `--exact` pinning the literal
  observed hostnames.

The default sits on the loose side deliberately, because **the risk is
asymmetric and pin is irreversible.** Too loose costs a follow-up `pin`, which
narrows further and is always available. Too tight has no recovery path at all
— there is no un-pin, so the fix is `km destroy && km create` and redoing the
session. A CDN or mirror hostname the session happened not to touch is exactly
the kind of thing `--exact` silently strands.

`--dry-run` prints both candidate lists side by side so the cost of `--exact` is
visible before committing. `pin` without `--dry-run` requires `--yes`.

#### Two sharp edges, surfaced rather than hidden

- **`pin` on a box that has touched nothing yields the empty set, which is
  deny-all.** Correct per the math, surprising in practice. `pin` prints the
  resulting set and refuses to proceed without `--yes`.
- **`pin` is a snapshot, not a mode.** It freezes the census as of that instant;
  anything genuinely new afterwards is denied. The workflow is *finish setup,
  then pin* — not *pin early and keep working*.

### `capture` — packet capture

Pure-Go `AF_PACKET` via `golang.org/x/sys/unix`, with the pcap file format
written by hand (24-byte global header, 16-byte per-packet header). Both
`golang.org/x/sys` and `golang.org/x/net` are already direct dependencies, so
this adds **no new dependency and no cgo** — which is required, not merely
tidy: sidecars cross-compile to `linux/amd64` from macOS with `CGO_ENABLED=0`,
and libpcap would break that build.

- Capture runs in a **root-owned daemon** (`km-capture.service`) exposing a Unix
  socket at `/run/km/capture.sock` (mode 0660, group `sandbox`);
  `km-netpolicy capture` is an unprivileged client of it. The sandbox user never
  holds `CAP_NET_RAW`, so this behaves identically on `privileged: false` boxes.

  **This is not a contradiction of the no-daemon decision for flows.** Flows are
  written *by* already-privileged producers and read by anyone, so a file is
  sufficient and a daemon would only add a failure mode. Capture is the reverse:
  it must be *started* by an unprivileged caller and performed with
  `CAP_NET_RAW`. A capability cannot be delegated through a file, so an
  arbitrating root process is the minimum mechanism, not a preference. It idles
  until asked and holds no state between captures.
- **Bounds are mandatory:** `--duration` (default 5m, hard ceiling 60m) and
  `--max-size` (default 250MB, ring rotation), plus a free-disk precondition.
  There is no unbounded capture mode.
- Files land at `/var/lib/km/capture/<ts>.pcap`. `capture stop` uploads to the
  artifacts bucket and prints the S3 URI.
- Filtering in v1 is `--port N` / `--host IP` only, assembled into a classic BPF
  program with `golang.org/x/net/bpf`. No tcpdump-expression parser.

**Honest scope note:** on a modern box most captured bytes are opaque TLS. The
pcap earns its place on what the other layers structurally cannot see — non-HTTP
protocols, unusual ports, raw sockets, malformed or blocked traffic, DNS
oddities — not as a general "see what the agent sent" tool. That question is
answered by flows today and would be answered by retained L7 if decision 4 is
ever revisited.

### Wiring — every site, or none

Commit `0c7f9880` removed the `runtimeDeny` gate and had to touch **five** sites
to do it: the sidecar `s3 cp` + symlink, `KM_NETPOLICY_FILE` on *both* proxy
units, the append-only file plus `chattr +a` plus `/etc/km/netpolicy.env`, and
the eBPF enforcer's `--netpolicy-file`. Its commit message records why a partial
change is worse than no change: ship the binary without the readers and
`km-netpolicy deny x` reports success while nothing enforces the result.

**The pin file has the identical shape and the identical failure mode.** Every
consumer of the deny store is also a consumer of the pin store:

| Site | Deny (shipped) | Pin (this phase) |
|---|---|---|
| `km-netpolicy` binary on box | ✅ v0.8.8 | reuses it |
| dns-proxy unit env | `KM_NETPOLICY_FILE` | `KM_NETPOLICY_PINS` |
| http-proxy unit env | `KM_NETPOLICY_FILE` | `KM_NETPOLICY_PINS` |
| eBPF enforcer flag | `--netpolicy-file` | `--netpolicy-pins` |
| append-only file + `chattr +a` + `netpolicy.env` | `deny.list` | `allow.pins` |

The eBPF site is the easiest to miss and the most load-bearing: under
`ebpf`/`both` the bootstrap deliberately leaves `km-dns-proxy` disabled and the
resolver serves DNS, so a pin that skipped `--netpolicy-pins` would report
success while every host stayed resolvable.

`km-netpolicy list` must report pins alongside denies for the same reason
`netpolicy.env` exists — otherwise a pinned box shows `(none)` and looks
unpinned.

## Explicitly NOT in scope (do not "fix" these)

- **Retained L7 plaintext.** Locked decision 4.
- **Laptop-side `km flows` / `km capture` verbs.** On-box first. Captures are
  retrieved from the artifacts bucket with the AWS CLI.
- **An un-pin or un-deny verb.** Its absence is the guarantee.
- **IPv6 flow records.** `LpmKey` and the BPF event struct are both `uint32`;
  the entire enforcement stack is IPv4-only today. The pcap captures frames and
  therefore *will* contain IPv6 traffic, so `flows` and a pcap will disagree on
  a v6-talking box. Documented, not fixed — v6 is its own phase.
- **A tcpdump-expression filter parser.**
- **Reverse DNS or inferred hostnames on flow records.**
- **Removing deprecated `spec.network.egress.runtimeDeny`.** Already a no-op;
  leave it accepted.

## Deploy surface

`make build` + `make build-lambdas` + `km init --dry-run=false`.

**Not `--sidecars`.** Userdata changes (new unit, new env vars, new
append-only file) ride in the create-handler zip, which `--sidecars` does not
rebuild. `make build` must precede `km init` per the standing rule.

**One IAM change is required**, and it is the easiest thing in this phase to get
wrong. The sandbox role's only S3 write grant today is
`transcripts/${sandbox_id}/*` (`ec2spot/v1.4.0/main.tf:562`, Sid
`S3PutTranscript`). `capture stop` uploads to a different prefix and will 403
without a grant. This needs a **new immutable module directory**
`infra/modules/ec2spot/v1.5.0` adding `s3:PutObject` on
`captures/${sandbox_id}/*`, plus the pin bump in
`locals.substrate_module_versions`. Editing v1.4.0 in place is wrong — the
directories are immutable by convention and existing sandboxes reference the
pinned version.

No new Terraform module beyond that version bump, no DynamoDB table, no Lambda
bridge change, no SandboxProfile schema change, no `apiVersion` bump.

**Userdata goldens change for every profile.** Observation is unconditional
(decision 1), so this is not a dormant feature and there is no byte-identical
case. This is a deliberate departure from the usual rule, on the same reasoning
`0c7f9880` used for `km-netpolicy` itself: a facility that can only narrow, and
that only reports what already happened, cannot widen a policy by being present.

Existing sandboxes gain nothing until `km destroy && km create`.

## Pre-existing defect found during design (adjacent, decide before execution)

**The learn-mode S3 upload has never worked.** `flushObservedState`
(`internal/app/cmd/ebpf_attach.go`) uploads the observed state to
`s3://{artifacts}/learn/{sandboxID}/{ts}.json` using the instance role. No
policy in `ec2spot` grants `s3:PutObject` on a `learn/` prefix — the only write
grant is `transcripts/`. So that `PutObject` returns 403 on every flush.

It is invisible for exactly the reasons this repo has been bitten by before: the
error is swallowed by `logger.Warn(...)` with `s3Key = "skipped"`, and
`fetchEC2ObservedJSON` falls back to SSM RunCommand, so `km shell --learn` still
produces a profile and nobody notices the primary path is dead. Code-vs-IAM
drift hidden by a soft logger — the same shape as the ttl-handler teardown gap.

This phase does not depend on it: nothing here reads `learn/` and the capture
grant is separate. But the v1.5.0 module version is being cut anyway, so adding
`learn/${sandbox_id}/*` alongside `captures/${sandbox_id}/*` costs one line and
one test. **Recommend fixing it here.** Flagged rather than assumed, because it
widens this phase's blast radius from "additive" to "also repairs a shipped
path", and that is the operator's call.

Verify with `aws iam simulate-principal-policy` against the live sandbox role
before and after — the technique that proved the ttl-handler fix without
recreating anything.

## Testing

- `pkg/flowlog`: record round-trip, rotation at the size cap, merge across
  producers, malformed-line tolerance (a corrupt line is skipped, never fatal).
- Pin algebra: `* ∩ observed = observed`; successive pins only shrink; a pin
  can never re-allow a denied host; denied observations are excluded; empty
  observed set yields deny-all.
- Collapse: `--exact` vs default produce the expected pair; bare TLDs never
  appear in a pinned suffix list.
- Wiring guard: a test that fails if any consumer of the deny store lacks a
  corresponding pin-store read — the five-site trap, made mechanical rather
  than remembered.
- pcap writer: byte-exact global and per-packet headers against a known-good
  fixture; `capture` refuses to start without bounds; ring rotation holds the
  size cap.
- Linux-only paths (`AF_PACKET`) build and run via `go test -c` cross-compile
  executed under bare Docker; compiling inside qemu crashes the Go compiler and
  `CGO_ENABLED` must be 0.

## UAT (live, in order) — operator-run, not part of this phase's autonomy

1. `km create profiles/learner.yaml` — wide open.
2. Install packages, clone a repo, hit an external API.
3. `km-netpolicy observed` lists them; a blocked host appears under denied.
4. `km-netpolicy flows --denied` shows the blocked attempts with pid/comm.
5. `km-netpolicy pin --dry-run` — inspect both candidate lists.
6. `km-netpolicy pin --yes` — confirm a previously-reachable, unpinned host now
   fails within ~1s, and a pinned host still works.
7. Confirm under `ebpf`/`both` that the pinned-out host also fails to *resolve*
   (the enforcer-flag trap).
8. `capture start --duration 1m`, generate traffic, `capture stop`, download the
   pcap from S3, open in Wireshark.
9. Repeat 3–6 on a `proxy`-enforcement profile to prove the non-eBPF path.
