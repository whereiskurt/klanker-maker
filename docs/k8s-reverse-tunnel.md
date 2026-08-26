# `km tunnel k8s` — kubectl in a sandbox, against a cluster only your laptop can reach

**Phase 130 · v0.8.6 · code-complete, live UAT pending**

> **`km tunnel` is a family, not a single command.** Every mode shares one transport —
> an SSM port-forward to sshd, with reverse forwards riding inside the SSH session — and
> differs only in what it carries and what it sets up on the box. Today there is one
> mode, `k8s`. See [The tunnel family](#the-tunnel-family) for why it is a subcommand
> rather than a `--k8s` flag, and what is likely to join it.

You have a Kubernetes cluster that is reachable only from your own workstation: the
OpenVPN route lives there, and the VPN credential cannot leave it. You want to work in
a km sandbox — with an agent — and have `kubectl` work against that cluster from
inside the box.

`km tunnel` does that without the sandbox ever holding your VPN credential, your SSO
refresh token, or any AWS credential.

```bash
km tunnel k8s my-sandbox --context k8s1
```

You land in a shell on the sandbox. `kubectl get ns` works. Exit the shell and the
access is gone.

---

## The tunnel family

`km tunnel` names a transport, not a destination. The reusable part is:

- an SSM port-forward from your laptop to the sandbox's `sshd`, and
- an SSH session on top of it carrying `-R` reverse forwards.

What varies by mode is **what those forwards carry** and **what has to be written on the
box** to make them usable. That split is why modes are subcommands rather than
mutually-exclusive `--k8s` / `--socks` booleans: a flag-based design gives you one help
page that is the union of two unrelated option sets, with nothing to tell you that
`--context` is meaningless under `--socks`. `km tunnel k8s --help` shows exactly the
seven flags that apply to it.

### `k8s` — today

Forwards the Kubernetes API plus a credential-broker socket, and writes a kubeconfig and
a shim on the box. The rest of this document describes it.

### `socks` — the likely next mode, not yet built

`ssh -R <port>` **with no destination** makes the ssh *client* act as a SOCKS 4/5 proxy
for connections arriving from the remote side. So the sandbox would get a SOCKS proxy on
loopback that egresses via your workstation:

```bash
# not implemented yet
km tunnel socks my-sandbox --bind-port 1080
# then, on the box:  HTTPS_PROXY=socks5h://127.0.0.1:1080
```

Two things worth stating before anyone builds it.

It needs **no new machinery** — the transport, the port pre-bind, the teardown, and the
diagnostic flags are all already here, and it needs no broker and nothing written on the
box. It is a genuinely small addition.

But it is a **far bigger hole** than `k8s`. The k8s mode forwards exactly two sockets to
exactly one cluster; a reverse SOCKS proxy gives anything on that box arbitrary network
access through your machine, to everything your VPN can reach. That does not merely slip
past km's egress enforcement, it makes the enforcement irrelevant for as long as the
tunnel is open. `socks5h` (resolve-at-proxy) is also the right form, for the same reason
`-R host:port` works today: names resolve on your side, over your VPN.

None of which is an argument against building it — just an argument that it deserves its
own decision, and probably louder warnings than this mode carries.

---

## How it works

Three nested transports. AWS Session Manager has no reverse-forward primitive — both
`AWS-StartPortForwardingSession` variants are strictly local→remote — so the reverse
leg rides inside SSH, which rides inside the SSM forward km already uses for
`km vscode`.

```
your laptop:2223 ──SSM port-forward──▶ sandbox:22 (sshd)
   └─ ssh -t sandbox@127.0.0.1 -p 2223
        -R 6443:k8s1.corp:6443            ← the Kubernetes API
        -R /home/sandbox/.km/kubetoken.sock:<broker>   ← the credential broker
```

The load-bearing detail: **`ssh -R` resolves and dials its target on the client side.**
`k8s1.corp` is resolved by *your laptop's* resolver and dialled over *your* VPN. The
sandbox needs no route, no DNS entry, and never consults km's
NXDOMAIN-by-default resolver. That is what makes this work at all when the cluster is
genuinely unreachable from anywhere else.

### Credentials: your plugin, run on your machine

km does not reimplement OIDC. It proxies Kubernetes' own **ExecCredential protocol**:

- The kubeconfig written on the box sets `exec.command: /home/sandbox/.km/km-kubetoken`.
- That shim is three lines of shell: `curl --unix-socket …` against the reverse-forwarded
  socket.
- A broker on your laptop runs **your existing exec plugin** — the same `kubelogin`
  invocation your real kubeconfig already uses — and returns its stdout unchanged.

Identity Center, the issuer URL, the refresh token, and the browser all stay on your
laptop inside the tool that already handles them.

Two consequences worth knowing before you see them:

- `kubectl` caches on `status.expirationTimestamp`, so the box asks for a credential
  only when one expires. Each mint prints a line on your terminal.
- **A cold token pops a browser on your laptop mid-`kubectl`.** You run `kubectl get pods`
  on the sandbox, your laptop opens an SSO tab, you complete it, and kubectl proceeds.
  That is correct behaviour, but it is startling the first time.

---

## Prerequisites

1. **Your VPN is up** and `kubectl --context k8s1 get ns` already works on your laptop.
   `km tunnel` is not a way to fix a broken kubectl — it forwards a working one.
2. **A running sandbox** whose profile did not set `spec.runtime.vscode.enabled: false`.
   That default is `true`, so unless you deliberately opted out, every sandbox qualifies.
3. **The sandbox's private key** at `~/.km/keys/<sandbox-id>`. If you created the sandbox
   on another machine, copy the `~/.km/keys/<sandbox-id>*` files across.
4. **`curl` on the sandbox.** Every stock km AMI has it; `km tunnel` checks and fails
   with a clear message if it is somehow absent.

---

## Deploy surface

**`make build`. That is the entire list.**

No profile schema field, no sidecar binary, no userdata change, no Lambda rebuild, no
`km init`, and **no `km destroy && km create`**. It works against sandboxes that are
already running.

That is unusual for this repo, so here is why:

- `IsVSCodeEnabled` defaults to **true**, so every sandbox already has `sshd` and a
  per-sandbox ed25519 keypair.
- km's userdata never writes an `sshd_config`, so stock OpenSSH defaults apply —
  `AllowTcpForwarding yes`, `AllowStreamLocalForwarding yes`, `GatewayPorts no` — which
  is exactly what this design needs.
- The box-side kubeconfig and shim are written at **connect time over SSH**, not baked
  into userdata at create time. That is also what lets you point at a different cluster
  without recreating anything.

---

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--context` | *(required)* | Kube context to tunnel |
| `--kubeconfig` | `$KUBECONFIG`, else `~/.kube/config` | Override the kubeconfig |
| `--local-port` | `2223` | Laptop port for the SSM forward to sshd |
| `--bind-port` | `6443` | Sandbox loopback port for the Kubernetes API |
| `--dry-run` | off | Resolve and print everything; connect to nothing |
| `--print-ssh` | off | Print the ssh command and exit |
| `--verbose` | off | Announce each leg as it comes up |

`--local-port` is 2223 rather than 2222 because `km vscode start` owns 2222, and having
VS Code attached to a box while tunnelling from it is a reasonable thing to do.

`--context` is required on purpose. km deliberately ignores `current-context`: silently
tunnelling whatever you last used is a poor failure mode when the whole premise is that
the sandbox cannot reach the cluster on its own — a wrong guess produces a puzzling
timeout instead of an obvious error.

### The three diagnostic flags are permanent, not scaffolding

This feature cannot be exercised on the machine it was written on — that machine has no
VPN, no cluster, and no kubectl. So the escape hatches are part of the interface:

- **`--dry-run`** resolves your kubeconfig and prints the cluster address, the plugin km
  found, the full ssh command, and the kubeconfig it would write. It makes no AWS call
  and opens no connection. Run this first, always. It catches a wrong context or a
  misread cluster address in one second, with no VPN and no sandbox.
- **`--print-ssh`** emits a copy-pasteable ssh command. If km's orchestration is wrong,
  you can run the ssh by hand and keep working.
- **`--verbose`** announces each leg separately — SSM up, broker listening, box
  provisioned — so a failure localises to one leg without a debugger.

---

## What gets written on the sandbox

| Path | Mode | What |
|---|---|---|
| `/home/sandbox/.km/kubeconfig` | 0600 | Points at `https://127.0.0.1:<bind-port>` with `tls-server-name` carrying your real cluster name |
| `/home/sandbox/.km/km-kubetoken` | 0700 | The `curl --unix-socket` credential shim |
| `/home/sandbox/.km/kubetoken.sock` | — | The reverse-forwarded broker socket, created by sshd and removed on exit |

The tunnel shell runs with `KUBECONFIG` already pointing at that file, so a bare
`kubectl` works. km does **not** write `~/.kube/config` — clobbering whatever was there
would be rude, and a persistent export would outlive the tunnel and point a later shell
at a dead socket.

A process on the box *outside* your tunnel shell needs
`export KUBECONFIG=/home/sandbox/.km/kubeconfig` to see it.

### Why the loopback address instead of an `/etc/hosts` entry

Mapping `127.0.0.1 k8s1.corp` in the box's `/etc/hosts` would let the kubeconfig keep its
real URL. It was rejected: it forces the loopback bind port to equal the cluster's real
port, which needs root on the box whenever that port is 443. Using `tls-server-name`
achieves the same certificate verification with no privilege at all, and frees
`--bind-port` to be anything.

### Why not full AWS credentials

Reverse-tunnelling a credential server and setting `AWS_CONTAINER_CREDENTIALS_FULL_URI`
would make every AWS SDK on the box work, auto-refreshing, with nothing at rest. It was
rejected for v1 because it hands the sandbox your entire SSO role when the stated need
is kubectl against one cluster.

---

## What this does not protect

**A reverse tunnel is a hole straight through km's egress enforcement.** The MITM proxy,
the eBPF allowlist, and `deniedHosts` do not see this traffic — it leaves through an
already-established SSH channel. While the tunnel is up, anything on the box that can
reach `127.0.0.1:<bind-port>` is talking to your cluster with your identity.

**The honest control is the lifetime, not a policy field.** The tunnel dies when you exit
the shell, and there is deliberately no flag to detach it, no daemon mode, and no way to
leave it running for an unattended agent. That would be a different feature with a
materially different risk profile, and it should be decided on its own merits rather than
arrive as a convenience flag.

An earlier draft of this design had a `spec.network.reverseTunnel` profile field. It was
dropped: connect-time provisioning removed its only real job, and keeping it would have
implied an authorization control that does not exist — you hold the private key and can
run `ssh -R` by hand regardless.

---

## Troubleshooting

**`connection refused` from kubectl on the box.**
Most likely the reverse bind collided — another `km tunnel` session already holds
`6443` on that sandbox. km passes `ExitOnForwardFailure=yes`, which is what turns this
from silent into loud: ssh refuses to open the session rather than handing you a working
shell attached to a dead tunnel. Use a different `--bind-port`, or find the other session.

**`interactiveMode must be specified`.**
You are on a km older than v0.8.6. That field is required under
`client.authentication.k8s.io/v1` and older builds omitted it. Upgrade.

**The browser never opens when a token expires.**
The plugin runs on your laptop with your inherited environment. Check that
`kubectl --context k8s1 get ns` still works directly on the laptop — if your plugin's
cache has expired in a way it cannot recover from non-interactively, fix it there first.

**`no SSH banner on 127.0.0.1:<port>`.**
The SSM forward never came up. Check `km status <id>`, and that
`aws ssm start-session` works for you at all. `km vscode status <id>` reports whether
sshd is actually running on the box.

**`private key for <id> not found`.**
The sandbox was created on a different machine. Copy `~/.km/keys/<id>*` across.

**Mint failures.**
Every credential mint logs one line on your terminal, and a failure carries your
plugin's own stderr. That text is from your plugin, not from km — read it as you would
if you had run the plugin yourself.

---

## Known limits

- **`provideClusterInfo: true` is unsupported.** km does not forward the box's
  `KUBERNETES_EXEC_INFO` to your plugin, because the box's cluster info describes the
  fake `127.0.0.1` endpoint and passing it to a plugin expecting the real cluster would
  be actively misleading. `kubelogin` does not need it.
- **One context per invocation.** Tunnelling two clusters at once means two `km tunnel`
  sessions, with different `--bind-port` values.
- **Exec-based users only.** A kubeconfig user with a static token or client certificate
  is rejected — the whole design is built on minting credentials on your side.
- **The SSH leg does not reconnect.** The SSM forward underneath it does, but a dropped
  SSH ends the session with `tunnel dropped, re-run km tunnel`. Reconnecting would lose
  your shell state anyway.
