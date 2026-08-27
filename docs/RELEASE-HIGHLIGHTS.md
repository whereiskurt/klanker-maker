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
## 🔧 `km tunnel k8s` now works against a real cluster

v0.8.6 shipped `km tunnel` with its live UAT still pending. It has now been run against an
actual cluster, and it failed twice before it worked. Both were rendering bugs on the
operator's side — nothing on the sandbox, nothing in AWS.

**If you installed v0.8.6, `km tunnel k8s` could not have worked for you. Take this one.**

### The exec `apiVersion` is now mirrored, not chosen

```
exec plugin is configured to use API version client.authentication.k8s.io/v1,
plugin returned version client.authentication.k8s.io/v1beta1
```

km hardcoded `v1` into the kubeconfig it writes on the box. But the broker deliberately
never sends `KUBERNETES_EXEC_INFO` — the box's cluster info describes a fake loopback
endpoint, and forwarding it would be misleading — so your plugin can't know which version
was asked for and returns its own default. `kubectl oidc-login` returns `v1beta1`, kubectl
demands an exact match, and nothing connects.

km now mirrors whatever your own kubeconfig declares. Your local kubectl already works
against that plugin, so your declared version is the only one known to agree with it.
Hardcoding `v1beta1` instead would have been the same bug pointed the other way, and
changing kubectl on the sandbox can't help — every version enforces the match.

### The cluster CA now travels with the tunnel

```
x509: certificate signed by unknown authority
```

The box dials `127.0.0.1` but verifies your **real** cluster certificate via
`tls-server-name`. That needs the cluster's CA, and km wasn't sending one — so the sandbox
fell back to its system trust store, which has never heard of a private corporate root or
an EKS-managed CA. `tls-server-name` says which *name* to check; the CA says which
*certificate* to trust. km was emitting one half of a pair.

The CA now travels, read off your machine and inlined if your kubeconfig points at a file
(that path doesn't exist on the sandbox). `insecure-skip-tls-verify` is mirrored if you
already set it and **never invented** — km will not quietly stop verifying a certificate to
make an error go away.

**This doesn't weaken anything.** A CA certificate is public, verification-only, and mints
nothing — it can't be replayed and grants no route to your cluster. Your VPN profile, SSO
refresh token, and AWS credentials still never leave your workstation.

### Checking it without a VPN

`--dry-run` exercises the whole fix and touches no network:

```bash
km tunnel k8s <sandbox> --context <your-context> --dry-run
```

The exec `apiVersion` in the printed kubeconfig should match your own `~/.kube/config`, and
you should see a `Cluster CA:` line reporting an inlined CA.

Deploy is `make build` — no `km init`, no sandbox recreate.
