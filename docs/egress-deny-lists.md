# Egress deny lists

`spec.network.egress.deniedDNSSuffixes` and `spec.network.egress.deniedHosts`
block destinations outright, ahead of the allowlist and ahead of every proxy
carve-out.

Both are **dormant by default**. A profile that declares neither renders
byte-identical user-data, compose, and task-definition output to before this
feature existed — nothing is emitted, and no deny gate is registered.

## Why a deny list exists at all

The egress policy was allowlist-only, with `"*"` as an allow-everything
wildcard. That makes the natural authoring loop impossible to express:

1. Run wide open with `learnMode: true` so the workload's real destinations get
   recorded.
2. Subtract the known-bad as you find it.
3. Replace `"*"` with the generated allowlist once the recording is
   representative.

Step 2 has no allowlist-shaped answer. Narrowing `"*"` means enumerating the
allowlist — which is exactly what step 1 is being run to discover. A deny list
lets you tighten the box before you know what it needs.

```yaml
spec:
  network:
    egress:
      allowedDNSSuffixes: ["*"]
      allowedHosts: ["*"]
      deniedDNSSuffixes:
        - "evil.example.com"
        - ".tracker.example.net"
      deniedHosts:
        - "evil.example.com"
```

See `profiles/deny-layered.yaml` for a complete worked profile.

> A `"*"` allowlist with denies layered on is an **open** box with holes
> plugged, not a closed one. A destination nobody thought to deny is still
> reachable. Denies are scaffolding for producing a real allowlist, not a
> resting state.

## Precedence

A deny beats every allow. Specifically it beats:

- an explicit matching entry in `allowedDNSSuffixes` / `allowedHosts`
- the `"*"` allow-all wildcard
- the GitHub repo filter, which implicitly allows GitHub hosts regardless of
  `allowedHosts`
- the OpenAI budget path, which returns before the host allowlist check
- the Bedrock / SES / Anthropic MITM interceptors

The last four matter because goproxy dispatches handlers first-match. The deny
gate is registered ahead of all of them; a deny evaluated later would be
silently bypassable depending on registration order.

## Matching

Entries are matched case-insensitively with the trailing DNS dot stripped. An
entry covers the apex **and** its subdomains whether or not it carries a leading
dot — `evil.example.com` and `.evil.example.com` are equivalent, and both block
`api.evil.example.com`.

This is deliberately broader than `allowedHosts`, where a bare entry matches
exactly. Strictness on an allowlist permits less; the same strictness on a
denylist would fail open.

`"*"` in a deny list denies everything, sealing the sandbox. That is legal but
`km validate` WARNs, because it is usually someone reaching for a wildcard
placeholder rather than a kill switch.

## Where it is enforced

| Layer | Component | Mechanism |
|---|---|---|
| DNS | `km-dns-proxy` | `DENIED_SUFFIXES` env; denied names get NXDOMAIN |
| DNS (eBPF mode) | `km ebpf-attach` resolver | `--denied-dns`; denied names never resolve, so their IPs never enter the BPF trie |
| HTTP/HTTPS | `km-http-proxy` | `DENIED_HOSTS` env; deny gate registered first, returns 403 / RejectConnect |
| BPF seeding | `km ebpf-attach` | `--denied-hosts`; denied hosts are skipped during allowlist pre-resolve |

Every env var and flag is emitted **only when the profile sets the
corresponding list**, so a sandbox without denies is unchanged.

### Known limitation: `"*"` allowlist under eBPF

With `allowedHosts: ["*"]`, `km ebpf-attach` seeds `0.0.0.0/0` into the BPF
allowlist, so the IP layer permits everything and enforcement rests entirely on
the resolver refusing to answer for denied names. A workload that connects to a
**literal IP address** it obtained some other way is therefore not blocked by a
deny in that configuration.

With a non-wildcard allowlist the trie only ever contains IPs resolved for
allowed names, and a denied host's IPs are never seeded, so the deny holds at
the IP layer too. This is another reason `"*"` plus denies is a staging
posture rather than a destination.

## Observability

- DNS proxy query logs carry a `denied` boolean alongside `allowed`, so an
  explicit deny is distinguishable from a name that simply never matched the
  allowlist.
- HTTP proxy blocks from the deny gate log `event_type=http_denied`, distinct
  from the allowlist's `http_blocked`.
- `km ebpf-attach` logs `event_type=ebpf_host_denied` when it skips pre-resolving
  a denied host.

## Inheritance

Deny lists participate in the normal Phase 117 `extends:` deep-merge, where
lists union. For denies the union is the safe direction: a child can only ever
add denies to what a base declared, never shrink them. The v1 "a child cannot
narrow a base's list" limitation that constrains allowlists works in your favour
here.

## Deploy surface

The schema field, the rendered user-data, and both sidecar binaries all change:

```bash
make build            # operator binary — carries the ebpf-attach deny flags
make build-lambdas    # create-handler renders the new user-data
km init --dry-run=false
```

`km init --dry-run=false` also rebuilds and uploads the `km-dns-proxy` and
`km-http-proxy` sidecars via `buildAndUploadSidecars`. `--sidecars` alone is NOT
sufficient — it does not refresh the create-handler zip that renders user-data.

Existing sandboxes keep their baked-in configuration until
`km destroy && km create`.

## Runtime narrowing from inside the sandbox

Every sandbox can add denies to itself while **running**, from user-land, without
an operator round-trip and without a restart. There is nothing to enable.

> **Changed in v0.8.8.** This used to be gated on
> `spec.network.egress.runtimeDeny: true`. It no longer is — `km-netpolicy`, the
> append-only deny file, the `KM_NETPOLICY_FILE` env on both proxies, and the eBPF
> enforcer's `--netpolicy-file` are provisioned on **every** sandbox.
>
> The gate was backwards. `km-netpolicy` can only ever *add* denies — there is no
> removal verb and the file is append-only — so its presence can never widen a
> policy. Meanwhile the boxes where narrowing matters most are exactly the
> wide-open ones (`allowedDNSSuffixes: ["*"]`, learn mode) that no profile would
> have thought to opt in. Gating it meant the tool for locking a box down was
> missing precisely where locking down was most valuable, and gaining it cost a
> `km destroy && km create`.
>
> `runtimeDeny` is still accepted so existing profiles keep validating, and is now
> a no-op. You can delete it or leave it.

```yaml
spec:
  network:
    egress:
      allowedDNSSuffixes: ["*"]   # wide open — and still narrowable at runtime
```

On the box:

```console
$ km-netpolicy deny telemetry.example.com
denied telemetry.example.com
1 runtime deny entry now in force (takes effect within ~1s)

$ km-netpolicy list
profile-baked denies (fixed at create time):
  evil.example.com
runtime denies (appended from inside this sandbox):
  telemetry.example.com
```

Off by default. When unset, no file is provisioned, the helper is not installed,
and the rendered user-data is byte-identical.

### Why this is safe to expose to an agent

Narrowing is one-way, and that is enforced in two places rather than promised:

- **The data structure.** The only operation is *append to a deny list*.
  Appending can only ever shrink the reachable set. There is no verb that
  removes an entry — `km-netpolicy` has exactly `deny` and `list`, and a test
  fails if anyone adds `allow`, `undeny`, `remove`, `clear`, or `reset`.
- **The kernel.** `/var/lib/km/netpolicy/deny.list` is created with `chattr +a`
  during bootstrap. An append-only file cannot be truncated, unlinked, renamed,
  or rewritten, and the attribute itself cannot be cleared without
  `CAP_LINUX_IMMUTABLE`. The sandbox user has none of those.

Widening still requires what it always did: edit the profile, then
`km destroy && km create`.

The file lives under `/var/lib`, not `/run`. `/run` is a tmpfs cleared on boot,
so a reboot would silently discard accumulated denies — which would *widen* the
policy. Denies survive reboot and stop/resume.

### The limit of the guarantee

On a profile with `spec.execution.privileged: true` the sandbox user has sudo
and can clear the append-only attribute. That is a real hole, but not a
meaningful one: the same user can stop the proxies, rewrite `/etc/km`, or detach
the eBPF programs. The guarantee is meaningful precisely on unprivileged
sandboxes, which is where an untrusted agent should be running anyway.

If `chattr` fails at boot (an exotic filesystem without append-only support),
bootstrap logs a `WARNING` and continues. Runtime denies still apply — they are
simply no longer protected against removal by the sandbox.

### Live UAT

Verified end to end on a real EC2 sandbox (`profiles/deny-layered.yaml`,
`enforcement: both`, AL2023, us-east-1a):

| Assertion | Result |
|---|---|
| `lsattr` shows the append-only bit on the deny file | `-----a----------------` |
| Sandbox user truncate / unlink / rename / `chattr -a` | all refused |
| Host reachable before the deny | yes |
| `km-netpolicy deny <host>` then reachable? | blocked, no restart |
| Unrelated host after the deny | still reachable — narrowing, not sealing |
| Denies survive a reboot | yes, file and attribute both |
| Still enforced after reboot | yes |
| eBPF resolver honours the runtime file | yes — with `km-dns-proxy` inactive, a runtime-denied name returned NXDOMAIN and a freshly appended deny took effect live |

That last row is the one worth knowing about. In `ebpf`/`both` mode the
bootstrap deliberately does not enable `km-dns-proxy` — the eBPF resolver
serves DNS instead. So on those profiles the resolver is the *only* thing
enforcing a DNS-level deny, which is why it reads the runtime file too.

### Operational notes

- A deny takes effect within about a second. Both proxies re-stat the file
  behind a short cache rather than holding a snapshot, so nothing restarts and
  no connections drop.
- Malformed lines are skipped and counted, never fatal. A bad append cannot
  blind the rest of the list. `km-netpolicy list` reports the count.
- `km-netpolicy deny` validates every pattern **before** writing anything, so a
  bad argument in a multi-pattern call cannot leave the file half-updated — the
  append is irreversible.
- It reads the file back after writing and reports failure if the entry is not
  actually in force, rather than assuming the write landed.
- The runtime file feeds the DNS proxy, the HTTP proxy, and the eBPF resolver.
  There is one list, not one per layer.
- `/etc/km/netpolicy.env` mirrors the profile-baked lists so `km-netpolicy list`
  can show them. Without it the helper would report `(none)` for profile-baked
  denies on a box that has them, since those values otherwise live only inside
  the proxy units' `Environment` blocks.

### All three enforcement modes

Runtime denies reach every layer, not just the proxies:

| Enforcement | What enforces a runtime deny |
|---|---|
| `proxy` (default) | `km-dns-proxy` (NXDOMAIN) and `km-http-proxy` (403) |
| `ebpf` | the resolver in `km ebpf-attach` — it *is* the DNS server here, so it must watch the file or a deny would report success while the name stayed resolvable |
| `both` | all three |

`km ebpf-attach` also re-checks the runtime file when deciding which allowed
hosts to pre-resolve into the BPF trie, so a runtime-denied host never has its
IPs seeded and cannot be reached by address.

The one caveat inherited from the static case still applies: with
`allowedHosts: ["*"]` under eBPF the trie is seeded `0.0.0.0/0`, so enforcement
rests on name resolution and a connection to a literal IP is not blocked.

## Allow pins: the mirror image of a deny

`km-netpolicy` also ships `pin`, which narrows from the **allow** side instead
of the deny side:

```
effective deny  = union(profile denies, runtime deny file)     # can only grow
effective allow = profile allowlist ∩ pin generation 1 ∩ …      # can only shrink
```

Deny is a union and can only ever block more; pin is an intersection and can
only ever allow less. Both are monotone in the narrowing direction, and both
get the identical treatment for the identical reason: their own append-only
file under `/var/lib`, `chattr +a` at boot, and no removal verb anywhere in
`km-netpolicy`'s surface.

**A deny still beats a pin.** Precedence is deny → pins narrow the allow →
profile allowlist, so a pin can never re-allow a host a deny already blocked —
a denied destination was never in the allow set to begin with, so it cannot
appear in an intersection of allow sets. The two narrow independently and
compose safely: denying more never widens what's pinned, and pinning tighter
never re-opens what's denied.

See `docs/egress-census.md` for the full pin workflow — the live census pin
narrows against, the collapsed-vs-`--exact` tradeoff, and why pin has no
un-pin verb.
