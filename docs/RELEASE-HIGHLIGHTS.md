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
## 🔒 Every sandbox can now lock itself down

`km-netpolicy` lets a **running** sandbox add egress denies to itself, from user-land, with
no operator round-trip and no restart. Until now it was gated behind
`spec.network.egress.runtimeDeny: true`.

**That gate was backwards, and it's gone.**

`km-netpolicy` can only ever *add* denies — there is no removal verb, and the file it appends
to is held append-only by the kernel. Its presence can never widen a policy. Meanwhile the
boxes where narrowing matters most are exactly the wide-open ones — `allowedDNSSuffixes:
["*"]`, learn mode — that nobody would have thought to opt in ahead of time. So the tool for
locking a box down was missing precisely where locking down was most valuable, and getting it
meant destroying and recreating the sandbox.

Now it's on every box:

```console
$ km-netpolicy deny telemetry.example.com
$ km-netpolicy list
```

Effective within about a second, no restart, and the kernel won't let the sandbox take it
back.

### All five pieces, not just the binary

Worth stating because a partial version would have been *worse* than the gate: shipping the
helper alone would let `km-netpolicy deny x` report success while nothing actually read the
result, and `list` would print `(none)`. Both were real bugs when this feature first shipped.

So the binary, the append-only deny file, `KM_NETPOLICY_FILE` on **both** proxies, the
`/etc/km/netpolicy.env` mirror, and the eBPF enforcer's `--netpolicy-file` are all now
unconditional. That last one is the easiest to forget and the most load-bearing — under
`ebpf`/`both` enforcement the resolver serves DNS, so without it a deny would report success
while the host stayed resolvable.

`spec.network.egress.runtimeDeny` is **deprecated and now a no-op**. It's still accepted, so
your existing profiles keep validating — delete it or leave it, either is fine.

### ⚠️ Deploy surface — heavier than v0.8.7

This one changes rendered user-data, so it is **not** a `make build`-only upgrade:

```bash
make build && make build-lambdas && km init --dry-run=false
```

Not `--sidecars` — that doesn't rebuild the create-handler zip that renders user-data.

**Existing sandboxes need `km destroy && km create` to gain it.** The binary is fetched at
boot, so a running box keeps whatever it was created with.

See `docs/egress-deny-lists.md`.
