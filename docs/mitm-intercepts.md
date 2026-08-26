# Declarative MITM intercepts

`spec.network.mitm.intercepts` lets a profile declare named host → action
rules for the http-proxy sidecar to answer directly, instead of dialing the
real destination: `redirect` (301 + `Location`) or `respond` (a canned
status/body). This is the mechanism behind the platform's old built-in
`google.com` → Rickroll easter egg, now expressed the same way any operator
rule would be.

**Default: opt-in, off.** A profile that declares no `mitm:` block gets **zero
interception** — nothing is redirected, nothing answers with a canned
response, and `curl` reaches the real destination (or is blocked by the
allowlist, same as any other host). This is deliberate and it is the **one**
place this repo departs from its usual "dormant ⇒ byte-identical" rule:
dormancy here means *no rules*, not *the old built-in rule, unconditionally*.
The previously-unconditional Rickroll stops firing fleet-wide on the next
`km create` — anyone relying on it will see it vanish, and that is accepted.

```yaml
spec:
  network:
    mitm:
      intercepts:
        - name: rickroll                       # required, unique, ^[a-z0-9][a-z0-9-]*$
          enabled: true                        # *bool, default true
          hosts: [".google.com"]               # required, >= 1 entry
          action:                              # exactly one of redirect | respond
            redirect: https://www.youtube.com/watch?v=dQw4w9WgXcQ

        - name: chaos
          hosts: ["api.example.com"]
          action:
            respond:
              status: 503
              contentType: text/plain          # optional, default text/plain
              body: "maintenance window"
```

## Getting the old behaviour back

The egg is not gone from the repo — it survives as an abstract fragment,
`profiles/base/mitm-rickroll.yaml`, that any profile opts into with one
`extends:` line:

```yaml
extends:
  - base/mitm-rickroll
```

See `profiles/mitm-demo.yaml` for the complete worked example: it extends the
fragment (reproducing the Rickroll redirect exactly), adds a second `respond`
rule of its own, and carries a commented-out block showing the override
below.

## The override rule: by name, last-wins, whole entry

Intercepts resolve by `name`. When the same name appears more than once across
a profile's `extends:` chain, the **last** occurrence wins and it replaces the
**entire entry** — not a field-level merge.

```yaml
extends:
  - base/mitm-rickroll
spec:
  network:
    mitm:
      intercepts:
        - name: rickroll
          enabled: false
```

`name` plus `enabled: false` switches an inherited rule off. That is the only
field an override needs when the intent is "turn this off" — there is nothing
left to inherit once it is disabled, and a disabled rule is **dropped at
compile time**: it never reaches the box, and no `mitm.conf` drop-in is
written for a profile whose only rule is disabled.

An override that keeps a rule *enabled* must restate `hosts` and `action` in
full, even if only one of them is actually changing. There is no partial
inheritance for an enabled rule — the new body replaces the old one entirely.

**Why whole-entry replacement, not a field merge:** Phase 117's inheritance
merges lists by **union** — a child's list is added to a base's list, never
subtracted from it. If intercept fields merged the same way, a leaf's `hosts`
would union with the base's `hosts` instead of replacing it, and a leaf could
never narrow (or redirect) an inherited rule's host list. Whole-entry,
by-name, last-wins is what makes `enabled: false` — and any other override —
actually take effect.

## Host matching

Matching reuses the platform's existing allowlist semantics
(`IsHostAllowed`): case-insensitive, port stripped, exact host by default, and
a leading `.` matches the apex **and** any subdomain (`.google.com` matches
both `google.com` and `www.google.com`). **Not regex, no `*` wildcard.**

This is a deliberate bug fix, not just a stylistic choice. The old hardcoded
handler matched with `^(www\.)?google\.com`, a regex unanchored at the end —
so `google.com.evil.example` was also MITM'd and Rickrolled, because the regex
only pinned the *start* of the string. The new matcher compares whole labels
rather than a substring, so a host formed by appending an attacker-controlled
suffix to a declared host is correctly rejected.

## Precedence

Registration order, from first-checked to last:

1. the deny gate (`spec.network.egress.deniedHosts` / `deniedDNSSuffixes`)
2. the Bedrock / Anthropic / OpenAI metering handlers and the GitHub repo filter
3. **operator intercepts**
4. the general allowlist

Two consequences follow directly from this order:

- **An intercept naming a metering or GitHub host is dead.** Declaring a rule
  for `api.anthropic.com`, `bedrock-runtime.*.amazonaws.com`, `api.openai.com`,
  or a GitHub host does not — and cannot — disable AI budget metering or the
  GitHub repo allowlist. Those handlers run first and answer the request
  before an intercept is ever consulted; declaring one anyway is silently
  inert. `km validate` warns about this at author time (see below).
- **An intercept for a host that is NOT on the allowlist still fires.** This
  is the useful case, and it is exactly how the old built-in behaved:
  `google.com` was never on any profile's `allowedHosts`, and the Rickroll
  fired anyway because the intercept handler was registered ahead of the
  general "is this host allowed" check. Use this to serve a canned error for a
  host the sandbox is not otherwise permitted to reach.

## Why there is no `block` action

Only two actions exist: `redirect` and `respond`. There is deliberately no
third, `block`. `spec.network.egress.deniedHosts` (and `deniedDNSSuffixes`)
already blocks, with strictly stronger semantics:

- it runs **ahead of everything**, including intercepts and the metering
  handlers, not after them
- its subdomain matching is broader (an entry covers the apex and subdomains
  whether or not it carries a leading dot)
- it is runtime-appendable from inside a running sandbox via `km-netpolicy`,
  with no restart required

A second, weaker way to block traffic would be a footgun — see
`docs/egress-deny-lists.md` for the full deny-list reference.

## `km validate` warnings and errors

**Errors** (fail `km validate`, and `km create`):

| Condition | Fix |
|---|---|
| `hosts` is empty on an enabled rule | add at least one host |
| zero or two keys under `action:` | declare exactly one of `redirect` / `respond` |
| `redirect` is not an absolute `http://` or `https://` URL | use a full URL, not a bare host or relative path |
| `respond.status` is outside 100–599 | use a valid HTTP status code |
| `name` is empty, malformed, or duplicated with conflicting content in the same file | names match `^[a-z0-9][a-z0-9-]*$`; a same-file duplicate can only be a typo |

A disable-only override (`name` + `enabled: false`) is exempt from the
hosts/action/redirect/status checks — there is nothing to check on a rule that
never reaches the box.

**Warnings** (never fail the exit code):

1. **Host overlaps a platform-reserved host** (`api.anthropic.com`,
   `bedrock-runtime.*.amazonaws.com`, `api.openai.com`, the four GitHub
   hosts). The platform handler always wins; the rule is dead. Fix: remove or
   retarget the rule.
2. **Host also matches `deniedHosts` / `deniedDNSSuffixes`.** The deny gate
   wins; the rule is dead. Fix: either the deny or the intercept is the
   author's actual intent — remove whichever one is not.
3. **`enforcement: ebpf` (or `both`) and the host is not covered by
   `allowedDNSSuffixes`.** See the eBPF section below — the host will never
   resolve, so the rule will never fire. Fix: add the host's suffix to
   `allowedDNSSuffixes`, or accept that the rule is inert under eBPF
   enforcement.

## eBPF and `both` enforcement

Under `enforcement: ebpf` or `enforcement: both`, intercept hosts are threaded
into the same `--proxy-hosts` list that seeds `connect4`'s DNAT rules — so
traffic to a declared intercept host is redirected to the proxy the same way
any other proxied host is.

**DNS is deliberately not widened.** Declaring an intercept does not add its
host to `allowedDNSSuffixes`. Under `ebpf`/`both` the enforcer's own resolver
still refuses to resolve (NXDOMAIN) a name outside `allowedDNSSuffixes` — so a
rule for a host the profile hasn't otherwise allowed to resolve will silently
never fire, because the sandbox can never get an IP to connect to in the first
place. This is exactly warning 3 above, not a bug: widening DNS as a side
effect of an intercept would quietly expand the sandbox's egress policy behind
the operator's back.

There is also a real asymmetry between the two proxy paths worth knowing
about. On the explicit-proxy (goproxy) path, the intercept handler answers a
CONNECT before any TLS handshake to the real destination happens. On the
transparent (DNAT) path used by `ebpf`/`both`, the relay dials and completes a
TLS handshake with the **real** destination before it inspects the request
enough to match an intercept rule. Practically: an intercept for a host that
is reachable and resolvable will fire identically on both paths, but an
intercept for a host that cannot actually be dialed (unreachable, wrong port,
TLS failure) can never fire under DNAT even though the explicit-proxy path
doesn't need to reach the real destination at all to answer.

## Not available

Three things are explicitly deferred and not part of this feature:

- **Runtime mutation.** There is no `km-mitm add`-style verb analogous to
  `km-netpolicy deny`. Intercepts are create-time only, baked into the
  profile. Runtime *narrowing* (denies) is safe because it can only restrict;
  a runtime *redirect* would let a compromised sandbox rewrite its own egress.
- **`respond.fromFile`.** Every `respond.body` is an inline string in the
  profile; there is no mechanism to serve a body from a file on the box.
- **Request/response rewriting, header injection, upstream proxying.** An
  intercept always terminates the request at the proxy with the declared
  action — it never modifies and forwards.

## Deploy surface

```bash
make build              # rebuilds the km-http-proxy sidecar binary
make build-lambdas      # create-handler renders the mitm.conf drop-in
km init --dry-run=false # full terragrunt apply
```

**All three commands are required, in this order.** Three things land
together: the sidecar binary that knows how to parse `KM_MITM_INTERCEPTS`, the
profile schema field that the create-handler's bundled `toolchain/km`
validates, and the user-data drop-in that the create-handler zip renders.

**`km init --sidecars` alone is not merely insufficient — it is actively
harmful mid-rollout.** It rebuilds and uploads the sidecar binaries but does
NOT refresh the create-handler zip. If you run `km init --sidecars` before the
full sequence above, a running sandbox picks up the new `km-http-proxy`
binary — which has no `KM_MITM_INTERCEPTS` env var to read yet, because
nothing has rendered it — and the built-in Rickroll (if that sandbox relied on
it) drops immediately, while the profile is still unable to declare the rule
back until the full sequence completes. Always run all three commands
together.

No Terraform module, DynamoDB table, IAM policy, Lambda bridge, or SES
resource changes for this feature. User-data goldens are untouched in the
dormant case — the drop-in is fully conditional on at least one enabled
intercept surviving the merge. Existing sandboxes keep today's behaviour
(including a live Rickroll, if their profile happened to extend the fragment)
until `km destroy && km create`.

## See also

- `docs/egress-deny-lists.md` — why blocking is `deniedHosts`'/
  `deniedDNSSuffixes`' job, not an intercept action
- `OPERATOR-GUIDE.md` § Composable inheritance — the Phase 117 list-union
  merge semantics that make by-name whole-entry override necessary
- `profiles/base/mitm-rickroll.yaml` / `profiles/mitm-demo.yaml` — the shipped
  fragment and worked demo leaf
