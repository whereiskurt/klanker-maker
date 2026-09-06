# Phase 135 (proposed) — Interactive sessions land outside the eBPF enforcement cgroup

**Status:** finding written up and empirically closed, no code written.
Discovered 2026-09-06 while answering an unrelated question about herdr pane
shells; scope widened the same day by direct measurement (§1.2).

**One line:** **every** interactive entry point — `km shell`, `km herdr`,
`km vscode`, direct ssh — runs *outside* the per-sandbox cgroup the eBPF network
programs are attached to, so the BPF allow-trie does not apply to anything an
operator does interactively. DNS and HTTP proxy enforcement still do. The
Phase 132 agent-dispatch fix works and is the *only* thing ever inside the
cgroup.

---

## 1. The evidence

All measured on `herdr-7090c850` (AL2023, `enforcement: both`,
`km-ebpf-enforcer` active).

### 1.1 The ssh path

From inside a real ssh session opened the way `km herdr start` and
`km vscode start` open one — SSM port-forward to sshd, then ssh as `sandbox`:

```
cgroup on arrival : 0::/user.slice/user-1001.slice/session-4.scope
join write        : FAILED -> /bin/bash: line 3: echo: write error: Permission denied
cgroup after join : 0::/user.slice/user-1001.slice/session-4.scope
```

Live corroboration from the running herdr session, read as root:

```
pid 43743 herdr -> 0::/user.slice/user-1001.slice/session-6.scope
pid 43754 bash  -> 0::/user.slice/user-1001.slice/session-6.scope
pid 43757 bash  -> 0::/user.slice/user-1001.slice/session-6.scope
pid 44186 bash  -> 0::/user.slice/user-1001.slice/session-6.scope
```

### 1.2 The `km shell` path — also outside (this was the open question)

`km shell` was *assumed* safe on the strength of an indirect probe plus the
Phase 132 claim in CLAUDE.md. It is not. Measured through the real path — the
`km-Sandbox-Session` SSM document, `runAsDefaultUser: sandbox`, `shellProfile`
→ `km-session-entry` → `km-sandbox-shell`:

```
id -un            : sandbox
cgroup            : 0::/system.slice/amazon-ssm-agent.service
join write        : /bin/bash: line 1: echo: write error: Permission denied
scope cgroup.procs: (empty)
```

Different source cgroup from the ssh case (`system.slice`, because SSM's
`runAs` does not open a PAM session, so logind never creates a
`user-1001.slice`), **same EPERM, same outcome.**

**Consequence: CLAUDE.md's Phase 132 statement is false.** It says "only an
interactive `km shell` session (which joins via `km-session-entry` →
`km-sandbox-shell`, never `sudo`) was ever actually inside the cgroup." No
interactive session has ever been inside the cgroup.

### 1.3 The Phase 132 dispatch fix works

Running the exact `dispatch_as_sandbox` body from `pkg/compiler/userdata.go`
(root, forked subshell, `$BASHPID`, `runuser`):

```
uid=sandbox
0::/km.slice/km-herdr-7090c850.scope
```

So the enforcement cgroup is not broken — it is simply reachable only from a
root-side join. Agent turns are enrolled. Interactive sessions are not.

### 1.4 Summary of every entry path

| entry path | lands in | in km.slice? |
|---|---|---|
| agent dispatch (Phase 132, `runuser` after root join) | `km.slice/km-<id>.scope` | **yes** |
| `km shell` (SSM doc, runAs sandbox) | `system.slice/amazon-ssm-agent.service` | no |
| `km herdr` / `km vscode` / direct ssh | `user.slice/user-1001.slice/session-N.scope` | no |
| `sudo -u sandbox` | `user.slice/user-1001.slice/session-N.scope` | no |

## 2. Why it fails

`pkg/compiler/userdata.go` writes `/usr/local/bin/km-sandbox-shell`:

```bash
CGROUP_PROCS="/sys/fs/cgroup/km.slice/km-{{ .SandboxID }}.scope/cgroup.procs"
{ echo $$ > "$CGROUP_PROCS"; } 2>/dev/null || true
exec /bin/bash --login "$@"
```

`/etc/profile.d/km-cgroup.sh` makes the same write for any sandbox login shell.
Both fail, everywhere, for one reason:

**cgroup v2 requires write access to the `cgroup.procs` of the *common
ancestor* of the source and destination cgroups, not just the destination.**
km-bootstrap chowns the destination to `root:sandbox 0664`, which is necessary
but not sufficient:

```
-rw-r--r--. 1 root root    0 /sys/fs/cgroup/cgroup.procs                          <- common ancestor
-rw-rw-r--. 1 root sandbox 0 /sys/fs/cgroup/km.slice/km-<id>.scope/cgroup.procs   <- destination
```

Every interactive source cgroup (`user.slice/…` for ssh and sudo,
`system.slice/amazon-ssm-agent.service` for SSM) has the **root cgroup** as its
common ancestor with `km.slice/…`. That file is `root:root 0644`. Hence EPERM
for uid `sandbox`, from every direction.

For the ssh path specifically there is a second, independent blocker:
`pam_systemd` places the session in `user.slice` *before* the login shell runs,
and would re-migrate it back even if the write succeeded.

The failure is invisible: `2>/dev/null || true` swallows it by design. That
guard exists for a *different* reason — a missing cgroup dir on resume, Phase
56.2 — and hides this as a side effect.

**Same family as the Phase 132 finding** ("cgroup v2 only permits self-migration
by a process that can write the *common ancestor's* `cgroup.procs`… PAM/logind
migrates the process straight back"), which was fixed for the 15 agent dispatch
sites by joining as root and dropping with `runuser`. That fix cannot reach
sshd or the SSM session document, because in both cases km does not own the
process placement at the moment the uid drops.

## 3. Scope

**Affected (all verified in §1):** `km shell`, `km herdr start`,
`km vscode start`, any direct `ssh` using the km-managed `~/.ssh/config` block,
and anything reached via `sudo -u sandbox`.

**Not affected (verified §1.3):** the Phase 132 agent dispatch sites.

## 4. What still applies — do not over-read this

| layer | applies to an interactive session? | why |
|---|---|---|
| DNS resolver allow/deny | **yes** | `/etc/resolv.conf` → `127.0.0.1`; the resolver is neither uid- nor cgroup-scoped |
| HTTP/S proxy (metering, GitHub filter, MITM intercepts) | **yes** | `HTTPS_PROXY=http://127.0.0.1:3128` is exported into the sandbox user's env |
| eBPF `connect4`/`sendmsg4`/`sockops`/`egress` allow-trie | **no** | attached to the cgroup |

This is a **defence-in-depth** loss, not an open door. What the BPF layer
uniquely catches is traffic that bypasses both DNS and the proxy: a direct
connection to a literal IP, or a process that ignores the proxy environment.
An operator in a herdr pane or a `km shell` can do both.

**On a wide-open profile it makes no practical difference.**
`profiles/herdr.yaml` inherits `base/network/safenetwork`
(`allowedDNSSuffixes: ["*"]`, `allowedHosts: ["*"]`), so nothing is denied at
any layer. The gap bites on `base/network/locked`-style profiles, and on
anything relying on `km-netpolicy deny` or `pin` to hold against an interactive
user.

**It also means `km-netpolicy pin`'s boot pre-seed of the BPF allow-trie
(Phase 131) has never constrained an interactive session** — worth restating in
`docs/egress-census.md`, which does not currently distinguish.

## 4a. Spike result (2026-09-06) — the fix is verified feasible

Run live on a purpose-built on-demand sandbox (`cgprobe-2d2eb1cf`, AL2023,
kernel `6.1.182-227.379.amzn2023`, `enforcement: both`, km's own enforcer
active), then torn down. The probe code was throwaway and has been deleted;
this section is its entire output of record.

**The predicate under test** — the shape the fix would use:

```
deny = (uid == const_sandbox_uid) || (cgroup_id == const_km_cgid)
```

The uid clause covers interactive sessions, which never reach `km.slice`. The
cgroup clause preserves EBPF-NET-12 (`pkg/ebpf/enforcer_integration_test.go`) —
a root process deliberately placed in the scope stays enforced despite uid 0.
A bare uid filter would have silently repealed that guarantee, since `sudo`
would become a way out of enforcement.

### Q0 — can the programs attach at the ROOT cgroup? Yes.

All four (`cgroup/connect4` plus three separate `cgroup_skb/egress` links)
loaded and attached at `/sys/fs/cgroup` simultaneously. Multiple `bpf_link`
cgroup attachments of the same type coexist without `BPF_F_ALLOW_MULTI`
bookkeeping.

### Q1 — is `bpf_get_socket_uid(skb)` usable in `cgroup_skb/egress`? Yes.

It passes the verifier there — where `bpf_get_current_uid_gid()` is forbidden,
per the comment at `bpf.c:118` — and returns the true socket owner:

| trigger | `bpf_get_socket_uid` |
|---|---|
| root, outside `km.slice` | 0 |
| sandbox, outside `km.slice` | 1001 |
| sandbox, inside `km.slice` | 1001 |
| root, inside `km.slice` | 0 |

### Q2 — is `bpf_skb_cgroup_id` usable there? Yes.

`bpf_skb_cgroup_id` and `bpf_skb_ancestor_cgroup_id` both verify and resolve
correctly (a cgroup id is the cgroup directory's inode):

| trigger | `bpf_skb_cgroup_id` | resolves to |
|---|---|---|
| outside `km.slice` | 3672 | `system.slice/amazon-ssm-agent.service` |
| inside `km.slice` | 4710 | `km.slice/km-cgprobe-2d2eb1cf.scope` |

A `connect4` control confirmed `bpf_get_current_uid_gid()` and
`bpf_get_current_cgroup_id()` agree with those values in process context.

### The enforcement run — 8/8 as predicted, and the box survived

A second probe applied the predicate for real at the root cgroup, bounded to
two marker IPs so nothing else on the box could be affected:

| trigger | expected | observed |
|---|---|---|
| root, outside, to marked IP | allow | connected |
| sandbox, outside, to marked IP | deny (uid clause) | `EPERM` immediately |
| **root, INSIDE `km.slice`, to marked IP** | **deny (cgroup clause)** | **`EPERM`** |
| sandbox, inside, to marked IP | deny (both) | `EPERM` |
| sandbox, outside, to egress-only marked IP | deny at egress | packet dropped (timeout) |
| root, outside, to egress-only marked IP | allow | connected |
| sandbox, outside, to unmarked control | allow | connected |
| root, outside, to unmarked control | allow | connected |

Row 3 is the one that matters most: the cgroup clause still binds a root
process, so **EBPF-NET-12 survives the change**. Row 5 proves socket-uid
enforcement works in softirq, where the obvious helper is unavailable — that
was the single riskiest assumption in the design.

`amazon-ssm-agent` stayed up throughout — the SSM session carrying this output
was itself live traffic through a root-attached egress program — and the marked
IP was reachable again immediately after detach, so nothing leaked past the
probe process.

### What the spike MISSED, found by the live UAT

The spike probed `cgroup/connect4` and `cgroup_skb/egress` and generalised the
result to all four programs. That was wrong. **`BPF_PROG_TYPE_SOCK_OPS` cannot
call `bpf_get_current_uid_gid` at all** — the helper is absent from
`sock_ops_func_proto` and the verifier rejects the program with
`unknown func bpf_get_current_uid_gid#15`. The `bpf.c:118` comment naming
sockops alongside connect4 and sendmsg4 is about `bpf_get_current_pid_tgid`, and
was over-read.

Under a root-cgroup attach the consequence is worse than a missing gate: one
rejected program means the enforcer fails to load entirely, so the box runs with
**no** enforcement. Nothing catches it short of a kernel — clang compiles the
call, `make generate-ebpf` succeeds, and every Go test passes.

The lesson generalises past this phase: a spike that establishes a helper is
available in *some* program types has established nothing about the others, and
BPF program types differ in exactly this way. Probe every program type you
intend to change.

### Three implementation notes the spike surfaced

- **The cgroup id must be read at attach time, never persisted.** It is the
  scope directory's inode, so it changes across reboots. The enforcer creates
  that directory itself (`CreateSandboxCgroup`), so it can `stat` it and set the
  constant in the same breath — but any stored copy would go stale silently.
- **`volatile const` defaults must fail OPEN.** The probe used
  `const_sandbox_uid = 0xffffffff` and marker IP `0`, so a loader that forgot to
  set a constant enforced nothing rather than denying everything. The real fix
  needs the same disposition: once the whole box's network path runs through
  these programs, a mis-set constant is the difference between a working sandbox
  and an unreachable one.
- **Deny at `connect4` surfaces as `EPERM` ("Operation not permitted"), deny at
  `egress` as a silent drop and a connect timeout.** Worth knowing before
  reading a UAT transcript: the same policy produces two very different-looking
  failures depending on which layer catches it.

## 5. Options

Note that §1.2 removes the option of scoping a fix to sshd alone. Anything less
than a fix covering both sshd *and* the SSM session document leaves half the
interactive surface unenforced.

### A. `pam_exec` hook in `/etc/pam.d/sshd` (root-side placement)

Place the session in `km.slice` from PAM, which runs as root before the shell.

- **Pro:** fixes the real cause for ssh; covers every ssh entry point at once.
- **Con:** does **not** cover `km shell` — SSM `runAs` opens no PAM session
  (proven by the absence of a `user-1001.slice` in §1.2), so this fixes at most
  half. Edits a file where a mistake locks everyone out of the box. Ordering
  against `pam_systemd` matters (must run after, or logind moves it back).

### B. `sshd_config` `ForceCommand` → a root-side wrapper

km currently writes no `sshd_config` (Phase 130 relies on that deliberately —
stock `AllowTcpForwarding`/`GatewayPorts` are exactly right). `ForceCommand`
runs as the *session user*, so it hits the same EPERM.

- **Con:** needs a privileged component to do the move anyway; interferes with
  the `-R` reverse forwards `km tunnel` depends on; and like A, does nothing for
  `km shell`.

### C. Attach enforcement where the sessions already are

Rather than moving processes into `km.slice`, attach the BPF programs to the
cgroups sessions actually land in — `user.slice/user-1001.slice` for ssh/sudo,
and `system.slice/amazon-ssm-agent.service` (or a narrower descendant) for SSM.

- **Pro:** no privileged migration at all; works with logind instead of against
  it; the only option that covers both halves of §1.4.
- **Con:** `user-1001.slice` is created on first login and may not exist at
  enforcer start — needs late attach or a watch. Attaching to
  `amazon-ssm-agent.service` also enrolls the agent's own traffic, which would
  put SSM's control-plane calls under the sandbox allowlist; a narrower target
  is needed there (this is the open design question, see §6). Changes what "the
  sandbox cgroup" means, which several components assume.

### D. A privileged join helper invoked from the wrapper

Give `km-sandbox-shell` a setuid or socket-activated root helper that performs
the move on behalf of the calling pid. Covers every path uniformly because it
sits in the one place all of them already funnel through.

- **Pro:** one code path, covers ssh + SSM + sudo; no PAM edits; no change to
  what the enforcement cgroup means.
- **Con:** introduces a setuid binary onto the box whose whole job is moving
  arbitrary pids between cgroups — it must refuse any pid it does not own, or it
  is itself an escalation primitive. For ssh it must also run *after*
  `pam_systemd`, which it does (the wrapper is the login shell).

### E. Accept and document

State plainly in `docs/vscode.md`, `docs/herdr-remote-attach.md` and the
`km shell` docs that interactive sessions are covered by DNS + proxy but not the
BPF layer.

- **Pro:** honest, zero risk, matches how the repo already treats the
  `privileged: true` limitation.
- **Con:** leaves a real gap on locked profiles, and the current code *looks*
  like it handles this — the wrapper appears to join and silently does not.

### Recommendation (revised by the §4a spike)

**C, in the specific form the spike validated: attach the four programs at the
root cgroup and gate each on `uid == sandbox_uid || cgroup_id == km_scope_id`.**

The spike answered the objection that previously demoted C. There is no need to
decide "which cgroup an SSM session should be enrolled into" — nothing is
enrolled into anything. The programs sit above every slice and the predicate,
not the attach point, decides who is enforced. `system.slice` traffic from the
SSM agent itself is uid 0 and falls through the first comparison.

Three properties settle it against D:

1. **No entry-point coverage gap at all.** D intercepts where the box's config
   is consulted, so it covers login shells and misses a non-login
   `sudo -u sandbox bash -c`. A uid predicate has nothing to miss: any process
   running as uid 1001 is enforced however it was started.
2. **It is testable off a live box.** The predicate is a pure function of two
   integers, exercisable in the existing `enforcer_integration_test.go` harness
   as uid 1001 vs 0 vs `km-sidecar`. D's correctness depends on sshd, PAM,
   logind and the SSM agent, none of which a container reproduces — which is
   exactly the blind spot that hid this bug for months.
3. **It deletes the failure mode rather than repairing it.** There is no
   migration left to silently fail, so there is no silent-failure to assert
   against.

D remains the fallback if root-cgroup attach later proves unacceptable for a
reason the spike did not surface — performance under real packet volume being
the obvious candidate, since `cgroup_skb/egress` at the root cgroup runs for
every packet on the box rather than only the sandbox's.

**E ships immediately regardless** — done 2026-09-06: `docs/vscode.md`,
`docs/herdr-remote-attach.md` (which asserted the opposite outright),
`docs/egress-census.md`, `docs/egress-deny-lists.md`, a canonical section in
`docs/operational-gotchas.md`, and the CLAUDE.md Phase 132 correction.

**Whatever is chosen, make the failure loud.** The wrapper's `2>/dev/null ||
true` should distinguish "cgroup dir missing" (the Phase 56.2 case it was
written for, still fine to swallow) from `EPERM`. Under approach C the wrapper's
join stops being load-bearing entirely and could simply be deleted — but a boot
and resume assertion that enforcement is actually live becomes *more* important,
not less, because a mis-set constant now fails silently open across the whole
box (see §4a's fail-open note).

## 6. Still unverified

- **Performance of `cgroup_skb/egress` at the root cgroup under real traffic.**
  The program runs for every packet on the box, not just the sandbox's. The
  predicate is two loads and two compares before the early return, so the
  expected cost is negligible — but it is unmeasured, and it is the one
  plausible reason to fall back to D.
- **Interaction with `--docker` profiles.** A container process running as
  uid 1001 inside a user namespace may map to a different host uid, so the
  predicate's uid clause could miss it. The cgroup clause would not help either
  unless the container's cgroup is a descendant of the scope. Unmeasured.
- **How long this has been true.** The wrapper has looked like this since the
  cgroup work landed; there is no evidence it ever worked for any interactive
  path. `km vscode` predates `km herdr` by many phases, so the exposure is not
  new.
- **Whether any shipped profile is actually affected in practice** — i.e. one
  that is both `enforcement: ebpf`/`both` AND has a narrow allowlist AND is used
  interactively.
- **Whether `km-execlog` / Phase 132 `who` attribution is affected.** Exec
  capture attaches tracepoints, not cgroup programs, so it should be unaffected;
  the HTTP-proxy pid resolution should likewise still work. Unmeasured.

## 7. How to reproduce

**ssh path:**

```bash
km create profiles/herdr.yaml herdrbox          # enforcement: both
km herdr start herdrbox --no-attach --local-port 2299 &
ssh -i ~/.km/keys/<sandbox-id> -p 2299 sandbox@localhost \
  'cat /proc/self/cgroup; echo $$ > /sys/fs/cgroup/km.slice/km-<id>.scope/cgroup.procs'
```

**`km shell` path** — drive the real SSM document non-interactively (`km shell`
itself has no `-- <cmd>` form). Escape `$$` as `\\$\\$`: the shellProfile
interpolates the parameter inside double quotes, so an unescaped `$$` is eaten
by the outer shell and you get a misleading `EINVAL` instead of `EPERM`:

```bash
aws ssm start-session --target <instance-id> --document-name km-Sandbox-Session \
  --parameters 'command=["id -un; cat /proc/self/cgroup; echo \\$\\$ > /sys/fs/cgroup/km.slice/km-<id>.scope/cgroup.procs"]'
```

**Positive control** — the Phase 132 pattern, as root over `AWS-RunShellScript`.
Must use `$BASHPID`, not `$$`, inside the subshell:

```bash
( { echo "$BASHPID" > /sys/fs/cgroup/km.slice/km-<id>.scope/cgroup.procs; } 2>&1
  exec runuser -u sandbox -- bash -lc 'cat /proc/self/cgroup' )
```

Expect `km.slice/km-<id>.scope`. If this one fails too, the problem is the
scope, not the entry path.
