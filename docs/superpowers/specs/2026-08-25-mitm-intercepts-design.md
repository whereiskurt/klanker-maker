# Design spec — Declarative MITM intercepts (`spec.network.mitm.intercepts`)

**Phase:** 127
**Date:** 2026-08-25
**Status:** design locked (operator decisions made up front; intended for an autonomous run)

## Problem

The http-proxy sidecar MITMs `google.com` and answers with a 301 to a Rickroll
(`sidecars/http-proxy/httpproxy/proxy.go:725-746`). The behaviour is hardcoded and
unconditional: no env var, no `proxyConfig` field, no profile knob gates it. An operator
who wants it off today has only indirect levers — deny-list `google.com`
(`spec.network.egress.deniedHosts`, which blocks rather than passes it through), or run
`enforcement: ebpf` so nothing DNATs to the proxy in the first place.

More generally, every host-matched behaviour in the proxy is compiled in. There is no way
for a profile to say "when this sandbox reaches host X, answer Y" — which is useful well
beyond the easter egg: stubbing a third-party API a sandbox must not really call, serving
a canned 503 to exercise an agent's error path, redirecting a documentation domain to an
internal mirror.

## Operator decisions (locked — do NOT re-litigate during execution)

1. **A declarative rule list, not a boolean.** The block is
   `spec.network.mitm.intercepts:` — a list of named host→action rules. The Rickroll
   becomes one such rule, expressed in YAML like any other.
2. **Opt-in, off by default.** A profile that says nothing about `mitm:` gets **no
   interception at all** — the Rickroll stops firing fleet-wide on the next
   `km create`. This is a deliberate behaviour change, accepted. It is the one place
   this phase departs from the repo's usual "dormant ⇒ byte-identical" rule: dormancy
   here means *no rules*, and the built-in rule is not grandfathered in.
3. **Operator rules never shadow platform handlers.** Order is: deny gate →
   Bedrock/Anthropic/OpenAI metering + GitHub repo filter → operator intercepts →
   general allowlist. A profile author must not be able to disable AI budget metering
   or the GitHub repo allowlist by declaring an intercept for those hosts.
4. **Two actions only: `redirect` and `respond`.** There is deliberately **no `block`
   action** — `spec.network.egress.deniedHosts` already blocks, with strictly stronger
   semantics (runs ahead of everything, broader subdomain matching, runtime-appendable
   via `km-netpolicy`). A second, weaker way to block would be a footgun.
5. **The metering interceptors stay hardcoded Go.** Bedrock, Anthropic, OpenAI and the
   GitHub repo filter carry real logic (token accounting, DynamoDB writes, repo
   allowlisting), not configuration. They are not part of this extraction.

## Design

### Profile surface

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

New Go types in `pkg/profile` (`NetworkMITMSpec`, `MITMIntercept`, `MITMAction`,
`MITMRespond`) plus JSON-schema entries with `additionalProperties: false`, matching the
rest of the schema. **No `apiVersion` bump** — the block is purely additive.

### Host matching

Matching reuses the semantics of `IsHostAllowed` (`proxy.go:153`): case-insensitive, port
stripped, exact host by default, and a leading `.` matches the apex **and** any
subdomain — `.google.com` matches `google.com` and `www.google.com`. Not regex.

This is consistent with every other host list in the platform, is greppable, and cannot
be a ReDoS vector. It also fixes a live bug: today's `^(www\.)?google\.com` is unanchored
at the end, so `google.com.evil.example` is currently MITM'd and Rickrolled too. The new
matcher rejects it, and a unit test pins that.

### `enabled` and inheritance

Phase 117 merges profile lists by **union**, so a leaf cannot shrink a base's list. The
compiler therefore resolves intercepts **by `name`, last-wins** — the child applies last
in `extends:` order, so:

```yaml
extends: [base/mitm-rickroll]
spec:
  network:
    mitm:
      intercepts:
        - name: rickroll
          enabled: false
```

turns an inherited rule off. This is what `enabled` is for; without it, an inherited rule
would be unreachable. A disabled rule is dropped at compile time and never reaches the
box. Two entries sharing a `name` with conflicting bodies in the *same* file is a
`km validate` error (it can only be a typo); the same `name` arriving from different
levels of the `extends:` DAG is the override path and is legal.

### Profile → box

The compiler marshals the enabled intercepts to JSON, base64-encodes them, and emits a
drop-in — written only when at least one enabled intercept survives the merge:

```
/etc/systemd/system/km-http-proxy.service.d/mitm.conf
[Service]
Environment=KM_MITM_INTERCEPTS=<base64>
```

**Base64 is load-bearing.** systemd splits an unquoted `Environment=KEY=value` on
whitespace; the `KM_ACTION_LIMITS` precedent (`userdata.go:4891-4901`) survives only
because a marshalled map has no spaces, and a `respond.body` will. `KM_PROXY_CA_CERT`
(`main.go:129`) already establishes base64 for this reason.

### Sidecar

New `sidecars/http-proxy/httpproxy/intercept.go`:

- `type Intercept struct { Name string; Hosts []string; Redirect string; Respond *Respond }`
- `ParseIntercepts(b64 string) ([]Intercept, error)`
- `WithIntercepts([]Intercept) ProxyOption` — appends to `proxyConfig`
- `registerInterceptHandlers(proxy, intercepts, sandboxID)` — one `HandleConnectFunc`
  (returns `MitmConnect` on match, `nil, host` to fall through) plus one `DoFunc`
  returning the redirect or canned response.

Registered at **exactly the point the google block occupies today** — `proxy.go:724`,
after the GitHub filter (`:687`, `:699`) and before the general CONNECT handler
(`:753`). The hardcoded google block is deleted. `main.go` reads `KM_MITM_INTERCEPTS`
alongside the existing env reads (`main.go:30-42`).

Logging generalises from `event_type=rickroll` to
`event_type=mitm_intercept, intercept=<name>, host=<host>, action=redirect|respond`.
Nothing consumes the old `rickroll` event type (`grep` finds it only in `proxy.go` and
`docs/BURNT.md`).

**Failure mode:** a `KM_MITM_INTERCEPTS` that fails to base64-decode or unmarshal
registers **nothing** and logs an error at startup. The proxy still starts and the
sandbox still has working egress. Failing toward no interception is the safe direction —
a half-parsed rule set could redirect traffic the operator never asked to redirect.

### eBPF / `both` enforcement

`buildL7ProxyHosts` (`userdata.go:5588`) gains the intercept hosts, so `connect4` DNATs
them to the proxy under `enforcement: ebpf` and `both` (the `--proxy-hosts` argument at
`userdata.go:4640`). This is a one-line addition to an existing list.

DNS is deliberately **not** widened: under `ebpf` the enforcer's resolver still NXDOMAINs
a host outside `allowedDNSSuffixes`, so a rule for a non-resolvable host silently never
fires. `km validate` warns about exactly that case rather than the feature quietly
expanding egress policy behind the operator's back.

### `km validate`

**Errors:** empty `hosts`; zero or two actions under `action:`; `redirect` that does not
parse as an absolute http(s) URL; `respond.status` outside 100–599; a `name` that is
empty, malformed, or duplicated with conflicting content inside a single file.

**Warns:**
- host overlaps a platform-reserved host (`api.anthropic.com`, `bedrock-runtime.*`,
  `api.openai.com`, the four GitHub hosts) — the platform handler wins, the rule is dead.
- host also matches `spec.network.egress.deniedHosts` / `deniedDNSSuffixes` — the deny
  gate wins, the rule is dead.
- `enforcement: ebpf` and the host is not covered by `allowedDNSSuffixes` — it will never
  resolve, so the rule will never fire. Names the fix.

## Explicitly NOT in scope (do not "fix" these)

- **Making Bedrock/Anthropic/OpenAI/GitHub interceptors configurable.** Decision 5.
- **A `block` action.** Decision 4 — `deniedHosts` owns blocking.
- **Runtime mutation.** There is no `km-mitm add` analogue to `km-netpolicy deny`.
  Intercepts are create-time only. Runtime narrowing is safe because it can only ever
  restrict; a runtime *redirect* would let a compromised sandbox rewrite its own egress.
- **`respond.fromFile`.** Considered and dropped — it adds a file-delivery problem and a
  boot-time failure mode for a v1 nobody has asked for yet.
- **Request/response rewriting, header injection, upstream proxying.** The action always
  terminates the request at the proxy.
- **ECS substrate.** Unused and unproven; this phase touches EC2 userdata only.

## Deploy surface

Three changes land together and **all three** are needed:

- sidecar binary (`km-http-proxy`) — `make build` + the `km init` sidecar upload
- profile schema field — the create-handler's bundled `toolchain/km` must be refreshed
- userdata drop-in — rendered by the create-handler zip

⇒ **`make build` + `make build-lambdas` + `km init --dry-run=false`.**

`km init --sidecars` alone is **not sufficient** and is actively harmful mid-rollout: the
new binary has no `KM_MITM_INTERCEPTS` to read, so a sidecar-only refresh on a live box
silently drops the Rickroll while leaving the profile unable to declare it back.

No Terraform module, DynamoDB table, IAM, Lambda-bridge or SES change. Userdata goldens
are untouched in the dormant case (the drop-in is conditional; deleting the google
handler is a pure sidecar change). Existing sandboxes keep today's behaviour until
`km destroy && km create`.

## Testing

**Sidecar unit tests** (`intercept_test.go`):
- matcher table: apex, subdomain, leading-dot, port-stripping, case-insensitivity, and
  the negative that today's regex gets wrong (`google.com.evil.example` must NOT match)
- `ParseIntercepts` round-trip; garbage base64 and malformed JSON ⇒ zero intercepts, no panic
- precedence: an intercept declaring `api.anthropic.com` does **not** shadow the metering
  handler; an intercept declaring a denied host is never reached
- a `respond` body containing spaces and newlines survives the base64 round-trip
- intercepts fire for hosts absent from `ALLOWED_HOSTS` (preserving today's google behaviour)

**Compiler tests:**
- dormancy: no `mitm:` block ⇒ no `KM_MITM_INTERCEPTS` line and no `mitm.conf` drop-in
- `enabled: false` entries are absent from the emitted JSON
- name-override across `extends:` resolves last-wins
- intercept hosts appear in `buildL7ProxyHosts` output

**Validation tests:** one per error and one per warning listed above.

**Profile inventory:** `scripts/validate-all-profiles.sh` stays green, and the new
fragment is skipped as abstract.

## UAT (live, in order)

1. `km create` a box from a profile with **no** `mitm:` block → `curl -sI https://google.com`
   returns Google's own response (or is blocked by the allowlist), **not** a 301 to YouTube.
   Confirms the default flipped.
2. `km create` a box from a profile that `extends: [base/mitm-rickroll]` →
   `curl -sI https://www.google.com` returns `301` with the YouTube `Location`.
   `km logs` shows `event_type=mitm_intercept intercept=rickroll`.
3. Same profile plus a leaf `- name: rickroll` / `enabled: false` → no 301, and
   `/etc/systemd/system/km-http-proxy.service.d/mitm.conf` is absent.
   Confirms the off-switch the phase exists for.
4. A `respond` rule with a multi-word body → `curl` returns the exact status and body.
   Confirms the systemd/base64 path.
5. An intercept for a denied host → deny wins (403 from the deny gate, not the rule), and
   `km validate` warned about it beforehand.
6. `enforcement: ebpf` box with an intercept whose host is in `allowedDNSSuffixes` → rule
   fires. Confirms the `--proxy-hosts` threading.

## Migration

The egg is off fleet-wide by default. To keep it discoverable, ship
`profiles/base/mitm-rickroll.yaml` as an abstract fragment (`metadata.abstract: true`) so
any profile opts in with one `extends:` line — it doubles as the worked example of
override-by-name. Add `docs/mitm-intercepts.md` and a CLAUDE.md "Where to look" row.
