<!--
  MAINTAINER NOTE — update this file BEFORE tagging each release.

  Its contents are injected VERBATIM into every GitHub release's notes,
  between the install header and goreleaser's auto-generated changelog.
  Wiring: .github/workflows/release.yml ("Load release highlights" step) →
  $KM_RELEASE_HIGHLIGHTS → .goreleaser.yaml `release.header` template.

  Keep it to the few MAJOR, human-curated additions for THIS release (the
  auto-changelog already lists every commit). HTML comments like this one
  are hidden in GitHub's rendered view. If this file is empty/absent the
  section is omitted gracefully.
-->
## ✨ Major additions highlighted

Some clusters you can only reach from your own laptop. The VPN route lives there, the
credential can't leave, and that's the end of it — which has meant a sandbox agent could
never touch them.

### 🔌 `km tunnel` — kubectl in a sandbox, against a cluster only your laptop can reach

```bash
km tunnel k8s my-sandbox --context k8s1
```

You land in a shell on the sandbox. `kubectl get ns` works. Exit the shell and the access
is gone.

**Nothing sensitive reaches the box.** Not your VPN credential, not your SSO refresh
token, not an AWS credential. `ssh -R` dials the cluster from *your* side, over *your*
VPN — the sandbox never gets a route or even a DNS entry for it.

Credentials work by proxying Kubernetes' own ExecCredential protocol rather than
reimplementing OIDC. The box runs a three-line `curl --unix-socket` shim; a broker on your
laptop runs **the exec plugin your real kubeconfig already uses** and hands back its output
untouched. Identity Center, the issuer, the refresh token, and the browser all stay on your
machine. When a token expires mid-`kubectl`, the SSO tab opens on your laptop and the
sandbox never prompts for anything.

**It works on sandboxes you already have running.** No profile change, no sidecar, no
`km init`, no recreate — `make build` and you're done. Every box already has sshd and a
keypair, and everything the tunnel needs is written at connect time.

**Three flags are permanent interface, not scaffolding.** `--dry-run` resolves your
kubeconfig and prints exactly what it would do, touching no AWS and opening no connection —
run it first, with or without the VPN. `--print-ssh` hands you the raw ssh command so you
can bypass km entirely. `--verbose` announces each leg so a failure localises without a
debugger.

**And the part worth reading twice:** a reverse tunnel is a hole straight through km's
egress enforcement. The MITM proxy, the eBPF allowlist, and `deniedHosts` do not see this
traffic. While the tunnel is up, anything on that box that can reach the forwarded port is
talking to your cluster as you. The control is the lifetime — it dies with your shell, and
there is deliberately no daemon mode, no `-N`, and no flag to leave it running for an
unattended agent.

### 🧦 `km tunnel socks` — the same trick, pointed at everything else

```bash
km tunnel socks my-sandbox
# on the box:  export ALL_PROXY=socks5h://127.0.0.1:1080
```

Same transport, no cluster. The box gets a SOCKS5 proxy on loopback that egresses through
your workstation, so it reaches whatever your VPN reaches. Nothing is written on the box
and there's no broker — `ssh -R <port>` with no destination makes ssh itself the proxy.

This one is deliberately wide. `k8s` forwards two sockets to one cluster; `socks` lets
anything on the box reach anything you can. That's the point of it, and it's why it dies
with your shell like everything else here.

One thing worth knowing before you reach for `--set-proxy-env`: km meters Bedrock,
Anthropic and OpenAI spend by intercepting those endpoints in its own proxy, and traffic
sent through SOCKS never gets there. Blanket-proxy the shell and **AI spend quietly stops
being metered**. That's why it's opt-in rather than the default, and why the banner prints
the export line instead — so you can scope it to the command that actually needs it.

Live UAT is pending. See `docs/k8s-reverse-tunnel.md` for the full runbook.
