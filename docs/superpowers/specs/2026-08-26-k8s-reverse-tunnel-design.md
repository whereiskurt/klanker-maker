# `km tunnel` — reverse-tunnelled kubectl access to an operator-only cluster

**Phase 130** · 2026-08-26 · status: design approved, not yet implemented

## Problem

An operator has a Kubernetes cluster (`k8s1`) reachable only from their own
workstation: the OpenVPN route lives on that laptop, and the VPN credential cannot
leave it. The cluster is self-managed, and `kubectl` authenticates through an OIDC
exec plugin (`kubelogin` / `oidc-login`) federated to AWS Identity Center.

They want to `km shell` into a sandbox and bring cluster access *with* them — so an
agent on the box can run `kubectl` against `k8s1` — without the VPN credential, the
SSO refresh token, or any AWS credential ever reaching the sandbox.

## Non-goals

- **Persistence.** The tunnel dies with the interactive session. No `-N` / detached
  mode, no daemon. An unattended agent holding production cluster access is a
  separate decision with a different risk profile; it is deliberately not a flag
  away.
- **Egress policy enforcement over the tunnel.** See "What this does not protect".
- **Multi-cluster.** One `km tunnel` invocation serves exactly one context.
- **Replacing `km shell`.** `km shell` keeps its SSM semantics untouched.

## Architecture

Three nested transports. SSM Session Manager has no reverse-forward primitive —
both `AWS-StartPortForwardingSession` variants are strictly local→remote — so the
reverse leg rides inside SSH, which rides inside the SSM forward.

```
laptop:<p> ──SSM port-forward──▶ sandbox:22 (sshd)
   └─ ssh -t sandbox@127.0.0.1 -p <p>
        -R 6443:k8s1.corp:6443                    ← k8s API
        -R /home/sandbox/.km/kubetoken.sock:/tmp/km-broker-XXXX/b.sock   ← cred broker
        -o ExitOnForwardFailure=yes
        -o StreamLocalBindUnlink=yes
```

The load-bearing property: **`ssh -R` resolves and dials its target on the client
side.** `k8s1.corp` is resolved by the laptop's resolver and dialled over the laptop's
VPN tun. The sandbox needs no route, no DNS entry, and never consults km's
NXDOMAIN-by-default resolver.

### Credentials: proxy the ExecCredential protocol, not OIDC

Neither side reimplements OIDC. The design proxies Kubernetes' own exec-plugin
protocol:

- The box's kubeconfig sets `exec.command: km-kubetoken`.
- `km-kubetoken` on the box is a three-line shell shim: `curl --unix-socket
  /home/sandbox/.km/kubetoken.sock http://localhost/token`, printing the response to
  stdout verbatim.
- The broker on the laptop runs **the operator's existing exec plugin**, resolved
  from their real kubeconfig, and returns its stdout unchanged.

Identity Center, the issuer URL, the refresh token, and the browser stay on the
laptop inside the tool that already handles them. If the cluster later moves to EKS,
the shim needs zero changes.

Two consequences to document:

- `kubectl` caches on `status.expirationTimestamp`, so the box hits the broker only
  on expiry — a handful of calls per session, each logged on the operator's terminal.
- **A cold token pops a browser on the laptop mid-`kubectl`.** The operator runs
  `kubectl get pods` on the sandbox; the laptop opens an SSO tab; on completion the
  token flows back and `kubectl` proceeds. Correct, but surprising the first time.

### Resolution happens at execution time

The kubeconfig exists only on the operator's real workstation, which is not
necessarily the machine that ran `km create`. Nothing about the cluster may be baked
in at create time. `km tunnel` therefore:

1. Reads `~/.kube/config` (honouring `$KUBECONFIG`, including multi-file
   colon-separated form) and resolves the named context to: `cluster.server`
   host+port, `cluster.tls-server-name` (falling back to the server hostname), and
   the user's `exec` stanza.
2. Pre-binds the local SSH port and creates the broker socket dir, failing fast
   before any AWS call.
3. Opens the SSM forward in the background; waits for `sshBannerTunnelProbe`.
4. Starts the broker on a unix socket in a 0700 temp dir.
5. Over one non-interactive SSH call, creates `/home/sandbox/.km`, writes the box
   kubeconfig, and writes the `km-kubetoken` shim (verifying `curl` exists).
6. Opens the interactive SSH session with both `-R` forwards.
7. On exit, tears down the broker and cancels the SSM forward.

The box kubeconfig points at `https://127.0.0.1:<bindport>` with `tls-server-name`
carrying the real cluster name. **The local bind port need not match the remote
port** — `-R 16443:k8s1.corp:443` is fine — so no privileged bind is ever required,
whatever port the API server actually uses.

### Rejected alternative: `/etc/hosts` on the box

Mapping `127.0.0.1 k8s1.corp` in the box's `/etc/hosts` would let the kubeconfig keep
its real URL. Rejected: it forces the local bind port to equal the real port, which
needs root on the box (`spec.execution.privileged: true`) whenever the API server is
on 443. `tls-server-name` achieves the same with no privilege.

### Rejected alternative: full AWS credentials via container-credentials endpoint

Reverse-tunnelling a credential server and setting
`AWS_CONTAINER_CREDENTIALS_FULL_URI` would make every AWS SDK on the box work.
Rejected for v1: it hands the sandbox the operator's whole SSO role, when the stated
need is kubectl against one cluster.

## Three details that are load-bearing, not incidental

1. **`ExitOnForwardFailure=yes` is mandatory.** By default a failed `-R` bind is a
   *warning*, not fatal — the operator would get a working shell with a dead tunnel
   and a baffling `connection refused` from kubectl. Two operators tunnelling to one
   sandbox is exactly how this gets hit.
2. **The broker rides a unix socket, not a TCP port.** Filesystem permissions (0600,
   owned by `sandbox`) then genuinely gate access, there is no port to collide, and
   no bearer-token scheme is needed. `StreamLocalBindUnlink=yes` because a stale
   socket otherwise blocks rebinding.
3. **No reconnect theatre on the SSH leg.** The SSM forward auto-reconnects today,
   but that kills the SSH session riding on it. Rather than fake resilience under an
   interactive shell — where reconnecting would lose shell state anyway — if the SSH
   dies we print `tunnel dropped, re-run km tunnel` and exit non-zero.

## Deploy surface

**`make build`. That is the whole list.**

This is smaller than it first appears, and the reason is worth recording:

- `IsVSCodeEnabled` (`pkg/profile/types.go:973`) **defaults to true** — a nil block
  or nil `Enabled` returns true. Every sandbox already has sshd and a per-sandbox
  ed25519 keypair at `~/.km/keys/<id>` unless a profile explicitly opts out.
- userdata never writes an `sshd_config`, so stock defaults apply:
  `AllowTcpForwarding yes`, `AllowStreamLocalForwarding yes`, `GatewayPorts no`,
  `PermitOpen any` — exactly what this design needs.
- The box-side shim and kubeconfig are written at connect time over SSH, not baked
  into userdata.

Therefore: no profile schema field, no sidecar binary, no `sidecarBuilds()` entry, no
userdata change, no Lambda rebuild, no `km init`, and **no `km destroy && km create`**.
It works against sandboxes that are already running.

The one prerequisite is an existing knob: the target profile must not have set
`spec.runtime.vscode.enabled: false`.

## What this does not protect

A reverse tunnel is a hole straight through km's egress enforcement. The MITM proxy,
the eBPF allowlist, and `deniedHosts` do not see this traffic — it leaves through an
already-established SSH channel. While the tunnel is up, anything on the box that can
reach `127.0.0.1:<bindport>` is talking to the cluster with the operator's identity.

The honest control is not a policy field; it is the lifetime. **The tunnel dies with
the shell**, and there is deliberately no flag to detach it.

An earlier draft of this design proposed a `spec.network.reverseTunnel` profile field.
It is dropped. Its only real job would have been provisioning, and connect-time setup
removed that job; retaining it would have implied an authorization control that does
not exist, since the operator holds the private key and can run `ssh -R` by hand
regardless.

## Interface

`km tunnel` is a **family**. Every mode shares the transport (SSM forward to sshd,
`-R` forwards inside the SSH session) and differs only in what it carries and what it
provisions on the box. Modes are subcommands, not mutually-exclusive `--k8s` / `--socks`
booleans: a flag-based design yields one help page that is the union of two unrelated
option sets, with nothing indicating that `--context` is meaningless under `--socks`.

The likely second mode is `socks`. `ssh -R <port>` **with no destination** makes the ssh
client act as a SOCKS 4/5 proxy for connections from the remote side, so the box would
get a proxy egressing via the operator's machine — no broker, nothing written on the box,
no new machinery. It is deliberately out of scope here, because it is a far bigger hole:
two sockets to one cluster versus arbitrary network access to everything the VPN reaches.

```
km tunnel k8s <sandbox-id> --context <kube-context> [flags]

  --context string       kube context to tunnel (required)
  --kubeconfig string    override $KUBECONFIG / ~/.kube/config
  --local-port int       laptop port for the SSM→sshd forward (default 2223)
  --bind-port int        sandbox loopback port for the k8s API (default 6443)
  --dry-run              resolve and print target, ssh argv, and generated
                         kubeconfig; connect to nothing
  --print-ssh            print the full ssh command and exit
  --verbose              announce each leg as it comes up
```

### Escape hatches are a first-class requirement

The operator cannot test this on the development machine — there is no VPN, no
cluster, and no kubectl there. Every debug cycle costs a tagged release plus a
context switch to another machine. The three diagnostic flags are therefore
permanent interface, not bring-up scaffolding:

- `--dry-run` catches kubeconfig-resolution mistakes with no VPN and no sandbox.
- `--print-ssh` lets the operator run the ssh by hand and keep working if km's
  orchestration is wrong on first contact.
- `--verbose` announces each leg separately (SSM up / SSH authenticated / `-R` API
  bound / `-R` socket bound / broker listening) so a failure localises to one leg on
  a machine with no debugger.

`--local-port` defaults to **2223**, not 2222, because `km vscode start` already
defaults to 2222 and having VS Code attached to a box while tunnelling from it is a
plausible combination. Both verbs pre-bind and fail fast with a `--local-port` hint,
so a collision is loud rather than mysterious.

### The broker stays dumb on purpose

The exec plugin's exact behaviour is unverifiable from the development machine. The
broker therefore passes stdout through verbatim, propagates the exit code, and lets
stderr reach the operator's terminal untouched. The less it assumes, the less can be
wrong on first contact.

**Known limitation:** `KUBERNETES_EXEC_INFO` is not forwarded from the box. The box's
cluster info describes the fake `127.0.0.1` endpoint, so forwarding it would be
actively misleading. Plugins requiring `provideClusterInfo: true` are unsupported in
v1; `kubelogin` does not need it.

## Dependencies

Kubeconfig parsing is hand-rolled against `gopkg.in/yaml.v3` (already a direct
dependency) using a minimal struct set. `k8s.io/client-go` is deliberately not
added — it is an enormous dependency for four structs.

## Testing

**Unit-testable despite having no cluster** — this is most of the logic:

- Kubeconfig parsing and context resolution against fixture YAML: multi-file
  `$KUBECONFIG`, absent `tls-server-name`, `exec` stanzas carrying `env:` and
  `args:`, missing context, context naming a missing cluster or user.
- ssh argv construction, including that `ExitOnForwardFailure=yes` and
  `StreamLocalBindUnlink=yes` are always present (regression-guarded — their absence
  is silent).
- The generated box kubeconfig and the `km-kubetoken` shim text.
- Broker mint behaviour against a stub plugin: stdout passthrough, non-zero exit
  propagation, stderr not swallowed.

**Not unit-testable, and therefore the entire point of the live UAT:** whether the
sockets actually carry traffic, whether `kubelogin` behaves under a mid-kubectl
browser hop, and whether token expiry refreshes cleanly.

A `docs/k8s-reverse-tunnel.md` runbook ships with the phase, including a numbered
live-UAT section fixed *before* the operator is standing on the other machine:
`--dry-run` → `--print-ssh` by hand → real run → `kubectl get ns` from the box →
sit through a token expiry to prove the browser hop.
