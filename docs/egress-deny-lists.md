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

## Not in scope

These lists are fixed at create time. Letting a **running** sandbox add denies
to itself from user-land — narrow-only, never widening — is the follow-up that
this feature is the prerequisite for. An append-only deny list is what makes
that safe: appending can only ever shrink the reachable set, so "never widens"
becomes a property of the data structure rather than a policy check you have to
trust.
