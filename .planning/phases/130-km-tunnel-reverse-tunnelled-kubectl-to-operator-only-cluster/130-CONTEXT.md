# Phase 130 — `km tunnel`: reverse-tunnelled kubectl to an operator-only cluster

Design spec: `docs/superpowers/specs/2026-08-26-k8s-reverse-tunnel-design.md`

## The ask (locked)

The operator's cluster `k8s1` is reachable only from their workstation — the OpenVPN
route lives there and the VPN credential cannot leave the laptop. The cluster is
self-managed; `kubectl` authenticates via an OIDC exec plugin (`kubelogin` /
`oidc-login`) federated to AWS Identity Center.

`km tunnel <sandbox-id> --context <ctx>` gives them an interactive shell on a sandbox
with `kubectl` working against `k8s1`, while **no VPN credential, SSO refresh token, or
AWS credential ever reaches the sandbox**.

## Decisions taken with the operator (do not re-litigate)

| Question | Answer |
|---|---|
| Why is k8s1 laptop-only? | Both routing and a policy constraint — VPN creds can't leave. Reverse tunnel is **mandatory**, not one option. |
| Cluster auth | Self-managed k8s, OIDC → Identity Center. **Not EKS.** Plugin is `kubelogin`. |
| How much credential reaches the box | **k8s token only.** No AWS credentials, ever. |
| Tunnel lifetime | **Interactive only** — dies with the shell. No `-N`, no daemon, deliberately. |
| Release | **v0.8.6** (v0.8.5 is already the MITM-intercepts release). |

## Shape (locked)

Three nested transports; SSM has no reverse-forward primitive, so `-R` rides inside
SSH which rides inside the SSM forward:

```
laptop:2223 ──SSM port-forward──▶ sandbox:22 (sshd)
   └─ ssh -t sandbox@127.0.0.1 -p 2223
        -R 6443:k8s1.corp:6443
        -R /home/sandbox/.km/kubetoken.sock:<laptop-broker.sock>
        -o ExitOnForwardFailure=yes -o StreamLocalBindUnlink=yes
```

`ssh -R` resolves and dials its target **client-side**, so `k8s1.corp` goes over the
laptop's VPN and the sandbox needs no route, no DNS, and never touches km's
NXDOMAIN-by-default resolver.

Credentials proxy Kubernetes' own **ExecCredential protocol** — neither side
reimplements OIDC. Box kubeconfig sets `exec.command: km-kubetoken`; that shim is a
`curl --unix-socket` one-liner; the laptop broker runs the operator's *existing* exec
plugin and returns its stdout verbatim.

## Why the deploy surface is only `make build`

Verified during design, and the reason this phase is small:

- `IsVSCodeEnabled` (`pkg/profile/types.go:973`) **defaults to true** — nil block or
  nil `Enabled` returns true. Every sandbox already has sshd and a keypair at
  `~/.km/keys/<id>` unless a profile explicitly opted out.
- userdata never writes an `sshd_config` — stock defaults apply, and
  `AllowTcpForwarding yes` / `AllowStreamLocalForwarding yes` / `GatewayPorts no` are
  exactly what this needs.
- The box-side shim and kubeconfig are written at **connect time over SSH**, not baked
  into userdata.

⇒ No profile schema field. No sidecar. No `sidecarBuilds()` entry. No userdata change.
No Lambda rebuild. No `km init`. **No `km destroy && km create`** — it works on boxes
already running.

## Three details that are load-bearing, not incidental

1. **`ExitOnForwardFailure=yes`.** A failed `-R` bind is a *warning* by default — the
   operator would get a working shell with a dead tunnel and a baffling `connection
   refused`. Two operators on one sandbox is exactly how this gets hit. Regression-guard
   it: its absence is silent.
2. **Broker on a unix socket, not TCP.** Filesystem perms (0600, `sandbox`) genuinely
   gate access; no port to collide; no bearer-token scheme needed.
   `StreamLocalBindUnlink=yes` or a stale socket blocks rebinding.
3. **No reconnect on the SSH leg.** The SSM forward auto-reconnects, but that kills the
   SSH riding on it. Under an interactive shell, reconnecting loses shell state anyway —
   print `tunnel dropped, re-run km tunnel` and exit non-zero. Do not fake resilience.

## Constraint that shapes the whole phase: this cannot be tested here

The development machine has **no VPN, no cluster, no kubectl**. Every debug cycle costs
a tagged release plus a context switch to another machine. Consequences:

- `--dry-run`, `--print-ssh`, `--verbose` are **permanent interface**, not bring-up
  scaffolding. They are the only way to localise a failure on a machine with no debugger.
- The broker stays **dumb**: stdout verbatim, exit code propagated, stderr untouched.
  The less it assumes about `kubelogin`, the less can be wrong on first contact.
- Unit tests must cover everything up to "do the sockets carry traffic" — kubeconfig
  resolution, argv construction, generated file text, broker mint behaviour.
- The UAT doc is written **as part of the phase**, so the bring-up sequence is fixed
  before the operator is standing on the other machine.

## Prohibitions

- Do NOT add a profile schema field. An earlier draft had
  `spec.network.reverseTunnel`; it was dropped because connect-time setup removed its
  only real job, and keeping it would imply an authorization control that does not
  exist (the operator holds the key and can `ssh -R` by hand regardless).
- Do NOT add `k8s.io/client-go`. Hand-roll the kubeconfig structs on
  `gopkg.in/yaml.v3` (already a direct dep).
- Do NOT add a `-N` / detached / daemon mode.
- Do NOT touch `km shell`, `km vscode`, userdata, Terraform, DynamoDB, IAM, or any
  Lambda.
- Do NOT forward `KUBERNETES_EXEC_INFO` from the box — its cluster info describes the
  fake `127.0.0.1` endpoint and forwarding it would be actively misleading.
- `--local-port` defaults to **2223**, not 2222 — `km vscode start` owns 2222 and both
  being open at once is plausible.
