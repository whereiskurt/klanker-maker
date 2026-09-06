# Phase 135 (proposed) — SSH sessions land outside the eBPF enforcement cgroup

**Status:** finding written up, no code written. Discovered 2026-09-06 while
answering an unrelated question about herdr pane shells.

**One line:** `km herdr` and `km vscode` Remote-SSH sessions run *outside* the
per-sandbox cgroup that the eBPF network programs are attached to, so the BPF
allow-trie does not apply to anything an operator does in them. DNS and HTTP
proxy enforcement still do.

---

## 1. The evidence

Measured on `herdr-7090c850` (AL2023, `enforcement: both`, km-ebpf-enforcer
active), from inside a real ssh session opened the way `km herdr start` and
`km vscode start` open one — SSM port-forward to sshd, then ssh as `sandbox`:

```
cgroup on arrival : 0::/user.slice/user-1001.slice/session-4.scope
join write        : FAILED -> /bin/bash: line 3: echo: write error: Permission denied
cgroup after join : 0::/user.slice/user-1001.slice/session-4.scope
```

Corroborating, on the same box:

```
km enforcement scope exists: yes
procs currently inside it:   0
```

The scope is created and empty. Nothing is being enforced by the cgroup-attached
programs because nothing is in the cgroup.

## 2. Why it fails

`pkg/compiler/userdata.go` writes `/usr/local/bin/km-sandbox-shell`:

```bash
CGROUP_PROCS="/sys/fs/cgroup/km.slice/km-{{ .SandboxID }}.scope/cgroup.procs"
{ echo $$ > "$CGROUP_PROCS"; } 2>/dev/null || true
exec /bin/bash --login "$@"
```

Two things combine:

1. **cgroup v2 requires write access to the common ancestor** of the source and
   destination cgroups, not just the destination. An ssh session starts in
   `/user.slice/user-1001.slice/session-N.scope`; the destination is
   `/km.slice/...`. Their common ancestor is the root cgroup, which is
   root-owned. Hence `EPERM` for uid `sandbox`.
2. **PAM/`pam_systemd` places the session in `user.slice` before the login shell
   runs**, so the shell is already in the wrong subtree by the time the wrapper
   executes.

The failure is invisible: `2>/dev/null || true` swallows it by design (that
guard exists for a *different* reason — a missing cgroup dir on resume, Phase
56.2 — and it also hides this).

**This is the same family as the Phase 132 finding** ("cgroup v2 only permits
self-migration by a process that can write the *common ancestor's*
`cgroup.procs`… PAM/logind migrates the process straight back"), which was fixed
for the 15 agent dispatch sites by joining as root in a subshell and dropping
with `runuser`. That fix does not reach sshd, because km does not own the sshd
session's process placement.

## 3. Scope — what is and is not affected

**Affected (verified):**

- `km herdr start` — the whole point of the command is an interactive session
- `km vscode start` — VS Code Remote-SSH
- Any direct `ssh` to the box using the km-managed `~/.ssh/config` block

**Not affected (probed, not fully verified — see §6):**

- `km shell`, which reaches the same wrapper via SSM →
  `km-session-entry` → `km-sandbox-shell`. The identical join *succeeded* when
  probed from the SSM (root) context, consistent with CLAUDE.md's existing claim
  that "only an interactive `km shell` session … was ever actually inside the
  cgroup."
- Agent dispatch sites, fixed in Phase 132 (`0825b8ed`).

## 4. What still applies — do not over-read this

Verified on the same box in the same session:

| layer | applies to an ssh session? | why |
|---|---|---|
| DNS resolver allow/deny | **yes** | `/etc/resolv.conf` → `127.0.0.1`; the resolver is neither uid- nor cgroup-scoped |
| HTTP/S proxy (metering, GitHub filter, MITM intercepts) | **yes** | `HTTPS_PROXY=http://127.0.0.1:3128` is exported into the sandbox user's env |
| eBPF `connect4`/`sendmsg4`/`sockops`/`egress` allow-trie | **no** | attached to the cgroup |

So this is a **defence-in-depth** loss, not an open door. What the BPF layer
uniquely catches is traffic that bypasses both DNS and the proxy: a direct
connection to a literal IP, or a process that ignores the proxy environment.
An operator in a herdr pane can do both.

**On a wide-open profile it makes no practical difference.** `profiles/herdr.yaml`
inherits `base/network/safenetwork` (`allowedDNSSuffixes: ["*"]`,
`allowedHosts: ["*"]`), so nothing is denied at any layer. The gap bites on
`base/network/locked`-style profiles, and on anything relying on
`km-netpolicy deny` or `pin` to hold against an interactive user.

## 5. Options

### A. `pam_cgfs`-style PAM module / `pam_exec` hook (root-side placement)

Place the session in `km.slice` from PAM, which runs as root before the shell.
A `pam_exec.so` line in `/etc/pam.d/sshd` invoking a small root helper that
writes the session leader's pid into the scope.

- **Pro:** fixes the real cause, covers every ssh entry point at once.
- **Con:** edits `/etc/pam.d/sshd` — a file where a mistake locks everyone out
  of the box, including `km shell` if it shares a stack. Needs a fallback so a
  helper failure cannot deny the session. Ordering against `pam_systemd` matters
  (must run after, or logind moves it back).

### B. `sshd_config` `ForceCommand` → a root-side wrapper

km currently writes no `sshd_config` (Phase 130 relies on that deliberately —
stock `AllowTcpForwarding`/`GatewayPorts` are exactly right). A `ForceCommand`
runs as the *session user*, so it hits the same EPERM. Only viable with a
setuid helper or a socket-activated root helper the user asks to be moved.

- **Pro:** narrower blast radius than PAM.
- **Con:** needs a privileged component to do the actual move; `ForceCommand`
  also interferes with `-R` reverse forwards used by `km tunnel`.

### C. Systemd delegation: move enforcement to `user.slice`

Rather than moving processes into `km.slice`, attach the BPF programs to the
session's own cgroup, or to `user-1001.slice`, so anything the sandbox user runs
is covered wherever logind puts it.

- **Pro:** no privileged migration at all; works with logind instead of against
  it. Likely the smallest change to `km-ebpf-enforcer`.
- **Con:** `user-1001.slice` is created on first login and may not exist at
  enforcer start; needs the enforcer to attach late or watch for it. Changes
  what "the sandbox cgroup" means, which several components assume.

### D. Accept and document

State plainly in `docs/vscode.md` and `docs/herdr-remote-attach.md` that
interactive ssh sessions are covered by DNS + proxy but not the BPF layer.

- **Pro:** honest, zero risk, matches how the repo already treats the
  `privileged: true` limitation.
- **Con:** leaves a real gap on locked profiles, and the current code *looks*
  like it handles this — the wrapper appears to join and silently does not.

### Recommendation

**C, with D shipped immediately regardless.** The docs are wrong today by
omission and that costs nothing to fix. C is the only option that does not
require a privileged migration helper or edits to `/etc/pam.d/sshd`.

Whatever is chosen, **make the failure loud**: the wrapper's `2>/dev/null ||
true` should distinguish "cgroup dir missing" (the Phase 56.2 case it was
written for, still fine to swallow) from `EPERM` (this case, which should be
logged). A silent join that never worked is what let this sit.

## 6. Not verified

- **`km shell` is assumed unaffected** on the strength of a probe from the SSM
  context plus the existing CLAUDE.md claim. Nobody has read `/proc/self/cgroup`
  from inside a real `km shell` session. Do that first — if `km shell` is *also*
  outside, the scope of this phase is much larger and §4's "what still applies"
  is the only enforcement anywhere for interactive use.
- **How long this has been true.** The wrapper has looked like this since the
  cgroup work landed; there is no evidence it ever worked for ssh. `km vscode`
  predates `km herdr` by many phases, so the exposure is not new.
- **Whether any shipped profile is actually affected in practice** — i.e. one
  that is both `enforcement: ebpf`/`both` AND has a narrow allowlist AND is used
  interactively over ssh.

## 7. How to reproduce

```bash
km create profiles/herdr.yaml herdrbox          # enforcement: both
km herdr start herdrbox --no-attach --local-port 2299 &
ssh -i ~/.km/keys/<sandbox-id> -p 2299 sandbox@localhost \
  'cat /proc/self/cgroup; echo $$ > /sys/fs/cgroup/km.slice/km-<sandbox-id>.scope/cgroup.procs'
```

Expect `user.slice/...` and `Permission denied`. Compare against
`cat /sys/fs/cgroup/km.slice/km-<sandbox-id>.scope/cgroup.procs`, which will be
empty.
