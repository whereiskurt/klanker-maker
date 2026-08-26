# Phase 127: Declarative MITM intercepts — Context

**Gathered:** 2026-08-25
**Status:** Ready for planning
**Source:** Design-spec express path (`docs/superpowers/specs/2026-08-25-mitm-intercepts-design.md`, committed c8747b2b)

<domain>
## Phase Boundary

Replace the hardcoded `google.com` -> Rickroll handler in the http-proxy sidecar
(`sidecars/http-proxy/httpproxy/proxy.go:725-746`) with a declarative SandboxProfile
block, `spec.network.mitm.intercepts` — a list of named host -> action rules
(`redirect` | `respond`) that an operator authors, disables by name, and inherits
through `extends:`.

**In this phase:** the profile schema + Go types; the merge-by-name override; the
compiler drop-in that carries the rules to the box; the sidecar matcher and handler
registration; `buildL7ProxyHosts` threading; `km validate` errors and warnings; an
abstract `profiles/base/mitm-rickroll.yaml` fragment; `docs/mitm-intercepts.md`; a
CLAUDE.md "Where to look" row; unit + compiler + validation tests; a 6-step live UAT.

**Not in this phase:** anything listed under Scope Fence below.
</domain>

<decisions>
## Implementation Decisions

### Shape of the config block (locked)
- The block is a **declarative rule list**, not a boolean. `spec.network.mitm.intercepts[]`
  with `name` (required, unique, `^[a-z0-9][a-z0-9-]*$`), `enabled` (`*bool`, default
  `true`), `hosts` (required, >= 1), and `action` (exactly one of `redirect` / `respond`).
- `respond` carries `status`, optional `contentType` (default `text/plain`), and `body`.
- Go types live in `pkg/profile`: `NetworkMITMSpec`, `MITMIntercept`, `MITMAction`,
  `MITMRespond`. JSON schema entries use `additionalProperties: false`, matching the rest
  of the schema. **No `apiVersion` bump** — the block is purely additive.

### Default state (locked)
- **Opt-in, off by default.** A profile that says nothing about `mitm:` intercepts
  nothing. The Rickroll stops firing fleet-wide on the next `km create`. This is a
  deliberate, accepted behaviour change and the one place this phase departs from the
  repo's usual "dormant => byte-identical" rule.
- The egg stays discoverable as `profiles/base/mitm-rickroll.yaml`, an abstract fragment
  (`metadata.abstract: true`) any profile opts into with one `extends:` line.

### Precedence (locked)
- Registration order is: deny gate (`proxy.go:237`) -> Bedrock/Anthropic/OpenAI metering
  + GitHub repo filter -> **operator intercepts** -> general allowlist.
- The registration point is exactly where the google block sits today: after the GitHub
  filter (`proxy.go:687`, `:699`), before the general CONNECT handler (`proxy.go:753`).
- Rationale: a profile author must never be able to disable AI budget metering or the
  GitHub repo allowlist by declaring an intercept for those hosts. Containment logic wins.
- Intercepts still fire for hosts **absent** from `ALLOWED_HOSTS` — that is today's
  google.com behaviour and the useful case for a canned error on a host you don't allow.

### Actions (locked)
- Exactly two: `redirect` (301 + `Location`) and `respond` (canned status/body).
- **No `block` action.** `spec.network.egress.deniedHosts` already blocks with strictly
  stronger semantics — it runs ahead of everything, matches subdomains more broadly, and
  is runtime-appendable via `km-netpolicy`. A weaker second way to block is a footgun.

### Host matching (locked)
- Reuse `IsHostAllowed` semantics (`proxy.go:153`): case-insensitive, port stripped,
  exact host by default, leading `.` matches apex AND subdomains. **Not regex.**
- This fixes a live bug: today's `^(www\.)?google\.com` is unanchored at the end, so
  `google.com.evil.example` is currently MITM'd and Rickrolled. A unit test pins the
  negative.

### Inheritance override (locked)
- The compiler resolves intercepts **by `name`, last-wins** (child applies last in
  `extends:` order). Phase 117 unions lists, so without this an inherited rule could
  never be turned off and `enabled: false` would be dead weight.
- A disabled rule is dropped at compile time and never reaches the box.
- Two entries sharing a `name` with conflicting bodies **in the same file** is a
  `km validate` error (it can only be a typo). The same `name` arriving from different
  levels of the `extends:` DAG is the override path and is legal.

### Transport to the box (locked)
- Compiler marshals enabled intercepts to JSON, base64-encodes, and writes a
  **conditional** drop-in `/etc/systemd/system/km-http-proxy.service.d/mitm.conf`
  carrying `Environment=KM_MITM_INTERCEPTS=<base64>`.
- **Base64 is load-bearing.** systemd splits an unquoted `Environment=KEY=value` on
  whitespace; the `KM_ACTION_LIMITS` precedent (`userdata.go:4891-4901`) survives only
  because marshalled maps have no spaces, and a `respond.body` will have them.
  `KM_PROXY_CA_CERT` (`main.go:129`) already sets the base64 precedent.
- The sidecar **fails toward zero intercepts**: a decode/parse failure registers nothing,
  logs an error, and the proxy still starts with working egress. A half-parsed rule set
  could redirect traffic the operator never asked to redirect.

### eBPF / both enforcement (locked)
- `buildL7ProxyHosts` (`userdata.go:5588`) gains the intercept hosts so `connect4` DNATs
  them under `enforcement: ebpf` / `both` (`--proxy-hosts`, `userdata.go:4640`).
- DNS is deliberately **not** widened. Under `ebpf` the enforcer's resolver still
  NXDOMAINs a host outside `allowedDNSSuffixes`, so a rule for an unresolvable host
  silently never fires — `km validate` warns instead of the feature quietly expanding
  egress policy.

### Validation (locked)
- **Errors:** empty `hosts`; zero or two actions under `action:`; `redirect` that is not
  an absolute http(s) URL; `respond.status` outside 100-599; a `name` that is empty,
  malformed, or duplicated with conflicting content inside one file.
- **Warns:** host overlaps a platform-reserved host (`api.anthropic.com`,
  `bedrock-runtime.*`, `api.openai.com`, the four GitHub hosts) — platform wins, rule
  dead; host also matched by `deniedHosts`/`deniedDNSSuffixes` — deny wins, rule dead;
  `enforcement: ebpf` and host not covered by `allowedDNSSuffixes` — never resolves.

### Logging
- `event_type=rickroll` generalises to
  `event_type=mitm_intercept, intercept=<name>, host=<host>, action=redirect|respond`.
  Nothing consumes the old event type (it appears only in `proxy.go` and `docs/BURNT.md`).

### Claude's Discretion
- Exact file split inside `sidecars/http-proxy/httpproxy/` (the spec proposes
  `intercept.go` + `intercept_test.go`).
- Whether the matcher is a thin wrapper over `IsHostAllowed` or a shared helper both call
  — as long as semantics are identical and tested.
- Wave/plan decomposition and ordering.
- Exact wording of validate messages and doc prose.
</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Authoritative design
- `docs/superpowers/specs/2026-08-25-mitm-intercepts-design.md` — locked decisions, full
  rationale, deploy surface, testing matrix, 6-step live UAT. Source of truth for this phase.

### Code the phase edits
- `sidecars/http-proxy/httpproxy/proxy.go` — `:153` `IsHostAllowed`; `:215` `NewProxy`;
  `:237` deny gate; `:278`/`:299` Bedrock; `:417`/`:435` Anthropic; `:535`/`:553` OpenAI;
  `:687`/`:699` GitHub; `:725-746` the google/Rickroll block being replaced; `:753`
  general CONNECT.
- `sidecars/http-proxy/main.go` — `:30-42` env reads, `:129` base64 CA precedent,
  `:211-215` quota env precedent.
- `pkg/compiler/userdata.go` — `:1281` proxy systemd unit; `:4640` `--proxy-hosts`;
  `:4891-4901` the `KM_ACTION_LIMITS` drop-in this mirrors; `:5588` `buildL7ProxyHosts`.

### Prior art / constraints
- `docs/egress-deny-lists.md` — the deny gate that stays ahead of every intercept and owns
  blocking (`pkg/netpolicy` is the canonical deny matcher).
- `OPERATOR-GUIDE.md` § Composable inheritance — Phase 117 list-union merge and the v1
  narrowing limitation that forces merge-by-name.
- `docs/operational-gotchas.md` — deploy-surface rules.
</canonical_refs>

<specifics>
## Specific Ideas

- Worked YAML the operator should be able to write:

```yaml
spec:
  network:
    mitm:
      intercepts:
        - name: rickroll
          hosts: [".google.com"]
          action:
            redirect: https://www.youtube.com/watch?v=dQw4w9WgXcQ
        - name: chaos
          hosts: ["api.example.com"]
          action:
            respond: { status: 503, contentType: text/plain, body: "maintenance window" }
```

- And the off-switch this phase exists for:

```yaml
extends: [base/mitm-rickroll]
spec:
  network:
    mitm:
      intercepts:
        - name: rickroll
          enabled: false
```

- Test cases called out explicitly in the spec: `google.com.evil.example` must NOT match;
  garbage base64 => zero intercepts, no panic; an intercept for `api.anthropic.com` does
  NOT shadow metering; a `respond` body with spaces and newlines survives the round trip;
  dormancy => no `KM_MITM_INTERCEPTS` line and no drop-in.
</specifics>

<deferred>
## Deferred Ideas

- `respond.fromFile` for large canned bodies — adds a file-delivery problem and a
  boot-time failure mode; not asked for yet.
- Runtime mutation (a `km-mitm add` analogue to `km-netpolicy deny`). Runtime *narrowing*
  is safe because it can only restrict; a runtime *redirect* would let a compromised
  sandbox rewrite its own egress.
- Request/response rewriting, header injection, upstream proxying.
</deferred>

<scope_fence>
## Scope Fence — do NOT do these

- Do **not** make the Bedrock / Anthropic / OpenAI / GitHub interceptors configurable or
  disableable. They carry real logic (token accounting, DynamoDB writes, repo
  allowlisting), not configuration.
- Do **not** add a `block` action.
- Do **not** widen DNS as a side effect of declaring an intercept.
- Do **not** bump `apiVersion`.
- Do **not** touch Terraform modules, DynamoDB tables, IAM, the Lambda bridges, or SES.
- Do **not** re-capture the frozen pre-92 byte-identity golden. Dormant-case userdata must
  stay byte-identical; if a golden appears to need changing, that is a signal the drop-in
  is not properly conditional.
- Do **not** work on the ECS substrate — unused and unproven; EC2 userdata only.
</scope_fence>

<success_criteria>
## Success Criteria

- A profile with no `mitm:` block produces no `KM_MITM_INTERCEPTS` and no drop-in, and
  `curl https://google.com` on that box is **not** redirected.
- A profile that `extends: [base/mitm-rickroll]` reproduces today's 301 exactly.
- Adding `- name: rickroll` / `enabled: false` to that leaf turns it off, with no drop-in
  written.
- A `respond` rule with a multi-word body returns the exact status and body.
- An intercept declaring a platform-reserved or denied host is warned about at validate
  time and provably never fires at runtime.
- `go test ./...` green, `scripts/validate-all-profiles.sh` green.
</success_criteria>

<risk_summary>
## Risk Summary

- **Fleet-wide behaviour change.** The egg goes off by default; anyone relying on it sees
  it vanish on the next `km create`. Accepted and documented.
- **Mid-rollout deploy hazard.** `km init --sidecars` alone ships a binary with no env var
  to read, dropping the Rickroll on a live box while the profile still cannot declare it
  back. The full sequence is `make build` + `make build-lambdas` +
  `km init --dry-run=false`.
- **systemd quoting.** A `respond.body` with spaces breaks an unquoted `Environment=`;
  base64 is the mitigation and needs a test that actually round-trips spaces/newlines.
- **Matcher narrowing.** Moving off the unanchored regex changes what matches. Treated as
  a bug fix, pinned by a test, and called out in the docs.
</risk_summary>

---

*Phase: 127-declarative-mitm-intercepts*
*Context gathered: 2026-08-25 via design-spec express path*
