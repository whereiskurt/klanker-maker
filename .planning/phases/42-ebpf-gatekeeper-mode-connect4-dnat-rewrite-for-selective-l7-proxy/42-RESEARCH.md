# Phase 42: eBPF Gatekeeper Mode — connect4 DNAT Rewrite for Selective L7 Proxy

**Researched:** 2026-04-02
**Domain:** Linux eBPF cgroup enforcement, connect4 sockaddr rewrite, BPF map coordination, Go userspace orchestration
**Confidence:** HIGH — all findings are from direct code inspection of the existing codebase

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **Architecture:** eBPF runs in `block` mode as kernel-level gatekeeper; proxy handles only L7 inspection hosts
- **connect4 DNAT mechanism:** `connect4` BPF program rewrites `dst_addr`/`dst_port` to `127.0.0.1:3128` for L7-required hosts — replaces iptables DNAT entirely in `both` mode
- **BPF maps for original dest:** socket cookie keyed maps `sock_to_original_ip` / `sock_to_original_port` already exist in `bpf.c`; these convey original destination to proxy
- **L7 hosts (default set):** `github.com`, `api.github.com`, `raw.githubusercontent.com`, `codeload.githubusercontent.com`, `bedrock-runtime.*.amazonaws.com`, `api.anthropic.com`
- **Test profile:** `profiles/goose-ebpf-gatekeeper.yaml` with `enforcement: both` — all Phase 42 development uses this profile
- **Verification:** Full E2E lifecycle loops until monotonously reliable; cold start, multiple shells, idle/resume cycles, different `allowedRepos` configs
- **Disk hygiene:** `--remote` on all `km create` calls; monitor `df -h /`, `~/go/pkg/mod/cache/`, `~/.cache/go-build/`

### Claude's Discretion

- Whether `both` mode still sets `HTTP_PROXY`/`HTTPS_PROXY` env vars (or removes them since connect4 handles routing)
- Original destination retrieval method: SO_ORIGINAL_DST getsockopt, PROXY protocol, or BPF map lookup from proxy
- Exact `--proxy-hosts` flag semantics: repurpose existing flag vs. derive from profile `sourceAccess.github` + Bedrock config
- How to derive L7 host list from profile fields (explicit field, or infer from `sourceAccess.github` + `useBedrock`)

### Deferred Ideas (OUT OF SCOPE)

None specified — all ideas in CONTEXT.md are in scope.
</user_constraints>

---

## Summary

Phase 42 promotes the eBPF enforcer from passive observer to active gatekeeper in `both` enforcement mode. The codebase already contains most of the required infrastructure — the BPF C code in `pkg/ebpf/bpf.c` already has `connect4` L7 rewrite logic, the `http_proxy_ips` map, and the `sock_to_original_ip`/`sock_to_original_port` maps. The resolver already supports `ProxyHosts` population. The critical gap is that `both` mode in `pkg/compiler/userdata.go` still passes `--firewall-mode log` and `--dns-port 0`, keeping eBPF passive while iptables DNAT handles everything. The phase primarily requires a precise set of changes to flip `both` mode from "eBPF passive + iptables active" to "eBPF active + no iptables DNAT" — and then rigorous E2E validation.

The proxy's original-destination question is already solved by the architecture: goproxy (elazarl/goproxy) is an HTTP CONNECT proxy, and the application sends `CONNECT github.com:443 HTTP/1.1`. The proxy reads the `Host` header, not a BPF map. The `sock_to_original_ip`/`sock_to_original_port` maps are already populated by connect4 and are available for future use (e.g., audit logging enrichment), but the proxy does not need them for routing.

**Primary recommendation:** The implementation work is focused and bounded. The BPF C changes are already done. The effort is (1) changing `both` mode constants in userdata.go, (2) removing `km-dns-proxy` sidecar + iptables DNAT for `both` mode, (3) wiring `--proxy-hosts` from L7 host list (not just GitHub repos), and (4) E2E validation loops.

---

## Standard Stack

### Core (already in use — no new dependencies needed)

| Library | Version | Purpose | Notes |
|---------|---------|---------|-------|
| `github.com/cilium/ebpf` | existing | BPF program load, map ops, link management | `CollectionSpec.Variables` for volatile constants |
| `github.com/miekg/dns` | existing | DNS resolver daemon in resolver package | Already wired in `runEbpfAttach` |
| `github.com/elazarl/goproxy` | existing | HTTP CONNECT proxy — reads Host header for L7 routing | No BPF map needed for original dest |
| `bpf2go` (cilium) | existing | BPF C → Go glue code generation | `make generate-ebpf` via Docker |

### No New Dependencies

All required libraries are already in use. Phase 42 is purely configuration and code changes within the existing stack.

---

## Architecture Patterns

### Current State: `both` Mode (Before Phase 42)

```
enforcement: both
  ↓
userdata.go emits:
  km-ebpf-enforcer: --firewall-mode log  (passive)
                    --dns-port 0          (no DNS resolver)
  km-dns-proxy sidecar                   (handles DNS via iptables DNAT)
  iptables DNAT: UDP/TCP :53 → :5353
                 TCP :80/:443 → :3128
```

Result: eBPF observes but does not enforce. iptables + proxy enforce.

### Target State: `both` Mode (After Phase 42)

```
enforcement: both
  ↓
userdata.go emits:
  km-ebpf-enforcer: --firewall-mode block  (active gatekeeper)
                    --dns-port 53           (eBPF DNS resolver)
                    --proxy-hosts [L7 hosts list]
  NO km-dns-proxy sidecar
  NO iptables DNAT (connect4 handles proxy routing)
  km-http-proxy still runs (for L7 inspection of redirected traffic)
```

```
Traffic → eBPF cgroup/connect4 (block mode)
            │
            ├── denied domain? → EPERM (proxy never sees it)
            │
            ├── allowed, needs L7?
            │    → connect4 rewrites dst → 127.0.0.1:3128
            │    → proxy receives CONNECT github.com:443
            │    → proxy reads Host header (no BPF map needed)
            │    → proxy enforces repo allowlist / Bedrock metering
            │
            └── allowed, no L7? → direct connection
```

### Pattern: BPF Volatile Constants

`bpf.c` uses `volatile const` globals set at load time via `CollectionSpec.Variables` in `enforcer.go`. These are already wired:

```go
// pkg/ebpf/enforcer.go — already exists
spec.Variables["const_firewall_mode"].Set(cfg.FirewallMode)
spec.Variables["const_dns_proxy_port"].Set(cfg.DNSProxyPort)
spec.Variables["const_mitm_proxy_address"].Set(cfg.MITMProxyAddr)
```

Changing `both` mode to block just requires passing `ModeBlock` instead of `ModeLog` in the Config.

### Pattern: Original Destination Retrieval (RESOLVED)

The open question from CONTEXT.md has a definitive answer: **goproxy resolves destination from the HTTP CONNECT Host header, not from a BPF map.**

When connect4 rewrites `dst → 127.0.0.1:3128`, the application's TLS stack sends:
```
CONNECT github.com:443 HTTP/1.1
Host: github.com:443
```

goproxy's `HandleConnect` callback receives `host = "github.com:443"` directly. This is how `IsHostAllowed` and `WithGitHubRepoFilter` already work. No SO_ORIGINAL_DST getsockopt, no PROXY protocol, no BPF map lookup needed in the proxy.

The `sock_to_original_ip` / `sock_to_original_port` maps (already in bpf.c) are populated by connect4 and can be used for audit log enrichment (correlating proxy events back to original BPF events by socket cookie), but are not required for proxy routing.

### Pattern: L7 Proxy Host Derivation

Currently `--proxy-hosts` is passed as `{{ .GitHubAllowedRepos }}` (line 570 of userdata.go) — this is the wrong value. It should be the set of hostname suffixes whose resolved IPs need proxy routing. The correct derivation:

```go
// L7 proxy hosts = GitHub API hosts (when sourceAccess.github is configured)
//                + Bedrock endpoints (when useBedrock: true)
//                + Anthropic API (when useBedrock: true or explicitly allowed)
l7Hosts := []string{
    "github.com", "api.github.com", "raw.githubusercontent.com", "codeload.githubusercontent.com",
}
if profile.Spec.Execution.UseBedrock {
    l7Hosts = append(l7Hosts, ".amazonaws.com")   // covers bedrock-runtime.*
    l7Hosts = append(l7Hosts, "api.anthropic.com")
}
```

This should be computed in `pkg/compiler/userdata.go` as a new template field `L7ProxyHosts` (or repurpose/extend `GitHubAllowedRepos`).

### Pattern: `both` Mode DNS

In current `both` mode, `--dns-port 0` skips the eBPF resolver. In target gatekeeper mode, DNS must be handled by the eBPF resolver (`--dns-port 53`) because iptables DNAT is removed. The resolver then populates both `allowed_cidrs` and `http_proxy_ips` maps as DNS responses arrive.

The key: `isProxyHost()` in `pkg/ebpf/resolver/resolver.go` is already wired — when a domain matches `ProxyHosts`, `MarkForProxy(ip)` is called, populating `http_proxy_ips`. connect4 then redirects that IP to the proxy.

### Pattern: `km-dns-proxy` Sidecar

In pure `ebpf` mode, `km-dns-proxy` is already skipped (line 472-475 of userdata.go). In `both` mode it is still started. For gatekeeper mode, it must be omitted for `both` mode too:

```
{{- if eq .Enforcement "ebpf" }}
  # skip km-dns-proxy
{{- else }}
  systemctl enable km-dns-proxy ...
{{- end }}
```

Change to:
```
{{- if or (eq .Enforcement "ebpf") (eq .Enforcement "both") }}
  # skip km-dns-proxy
{{- else }}
  systemctl enable km-dns-proxy ...
{{- end }}
```

### Pattern: iptables DNAT Removal for `both` Mode

Currently `{{ if or (eq .Enforcement "proxy") (eq .Enforcement "both") }}` guards the iptables DNAT block. Change to `{{ if eq .Enforcement "proxy" }}` to remove iptables rules for `both` mode.

### Pattern: resolv.conf Override

Currently only done for `ebpf` mode (line 617-623). For `both` gatekeeper mode, `resolv.conf` must also point to `127.0.0.1` since the eBPF resolver listens on `:53`.

### Pattern: HTTP_PROXY Env Vars

Currently proxy env vars are set for `proxy` and `both` modes. In gatekeeper mode, connect4 transparently redirects traffic to the proxy, so `HTTP_PROXY`/`HTTPS_PROXY` env vars are redundant for kernel-redirected traffic. However, removing them risks breaking applications that check `$HTTPS_PROXY` before connecting (not all applications respect transparent proxy). Recommendation: **keep proxy env vars for `both` mode** as belt-and-suspenders — they don't cause harm when connect4 is also active (both paths lead to the proxy for L7 hosts).

### Recommended Change Set

```
Files to modify:
  pkg/compiler/userdata.go       — primary changes (DNS, iptables, sidecar, mode flags)
  pkg/compiler/userdata.go       — add L7ProxyHosts template field derivation
  profiles/goose-ebpf-gatekeeper.yaml — already correct (enforcement: both)

Files unchanged:
  pkg/ebpf/bpf.c                 — connect4 rewrite logic already correct
  pkg/ebpf/enforcer.go           — no changes needed
  pkg/ebpf/resolver/resolver.go  — ProxyHosts support already implemented
  internal/app/cmd/ebpf_attach.go — no changes needed
  sidecars/http-proxy/           — no changes needed (goproxy reads Host header)
```

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Original dest retrieval | Custom BPF map lookup in proxy | goproxy CONNECT Host header | Already works; goproxy passes `host` string to HandleConnect callbacks |
| DNS filtering | Custom DNS parser | `github.com/miekg/dns` (already in resolver) | Handles wire format, NXDOMAIN, TTL correctly |
| BPF map updates | Custom kernel interface | `cilium/ebpf` `Map.Put()` | Handles endianness, struct padding, error reporting |
| iptables replacement | Custom netfilter rules | connect4 sockaddr rewrite | Already implemented in bpf.c; cleaner than maintaining iptables |

**Key insight:** The proxy does not need to know the "original" destination because it's already an HTTP CONNECT proxy — the application always tells it where to go via the CONNECT Host header. The BPF rewrite works precisely because HTTPS clients send CONNECT before any TLS handshake.

---

## Common Pitfalls

### Pitfall 1: Proxy PID Exemption in Gatekeeper Mode

**What goes wrong:** `km-http-proxy` process is inside the cgroup. If it is not in the exempt list, its own upstream connections (to github.com:443) get redirected to itself — infinite loop.

**Why it happens:** `const_proxy_pid` exempts exactly one PID. In current `both` mode this is set from `os.Getpid()` in `runEbpfAttach`. The proxy is a separate process with a different PID.

**How to avoid:** The proxy runs as `km-sidecar` user. The connect4 exemption by PID must cover the proxy process. The existing approach passes `const_proxy_pid = os.Getpid()` which is the enforcer's own PID, not the proxy's. Current code relies on iptables `! --uid-owner km-sidecar` to exempt the proxy. In gatekeeper mode (no iptables), the connect4 hook must exempt the proxy by PID or uid.

**Resolution options:**
- Write the proxy's PID to a well-known file (e.g. `/run/km/http-proxy.pid`) and pass it to `km ebpf-attach --proxy-pid`
- Use cgroup hierarchy: run proxy in a separate child cgroup excluded from BPF attachment scope
- Exempt `km-sidecar` uid in connect4 via a BPF map keyed on uid (requires adding uid exemption logic to bpf.c)

**Warning signs:** proxy log shows connections to itself; sandbox can't reach github.com; `km-http-proxy` restarts in a loop.

### Pitfall 2: DNS Race — connect4 Fires Before Resolver Populates Maps

**What goes wrong:** Application resolves a hostname, gets an IP back, immediately calls `connect()`. If the resolver hasn't yet called `AllowIP()` + `MarkForProxy()` for that IP, connect4 sees an unrecognized IP, denies it (in block mode), and the connection fails with EPERM.

**Why it happens:** The resolver is async — it processes the DNS response, calls `AllowIP`, then returns the DNS answer to the application. There is a tiny window where the DNS answer is in flight but the BPF map update hasn't completed.

**How to avoid:** The resolver in `pkg/ebpf/resolver/resolver.go` calls `MapUpdater.AllowIP()` synchronously before writing the DNS response (line 257-265). This means the BPF map is updated before the DNS answer reaches the application. This is already correct. Verify this flow is preserved.

**Warning signs:** Intermittent EPERM on first connection to a host; connection succeeds on retry; errors correlate with DNS resolution timing.

### Pitfall 3: `--proxy-hosts` Still Passes GitHub Repo Names (Not Domain Suffixes)

**What goes wrong:** Current userdata.go line 570 passes `GitHubAllowedRepos` (e.g. `whereiskurt/meshtk`) as `--proxy-hosts`. The resolver's `isProxyHost()` does domain suffix matching — repo names are not domain names.

**Why it happens:** The flag was added with `GitHubAllowedRepos` as a placeholder. The resolver correctly ignores non-matching strings (repos don't match DNS domain names), so no IPs get marked for proxy → connect4 allows direct connections to github.com without proxy → GitHub repo enforcement is bypassed silently.

**How to avoid:** Change `--proxy-hosts` to pass the L7 hostname list (e.g. `github.com,api.github.com,raw.githubusercontent.com,api.anthropic.com,.amazonaws.com`). Derive this from profile fields in userdata.go, not from `GitHubAllowedRepos`.

**Warning signs:** Proxy log shows zero MITM events for GitHub; `km otel` shows no Bedrock metering; allowed-repo enforcement appears to not work in `ebpf` mode.

### Pitfall 4: resolv.conf Not Overridden in `both` Mode

**What goes wrong:** After removing iptables DNAT, the system's DNS stub resolver still forwards to `169.254.169.253` (VPC DNS) directly. The eBPF resolver on `:53` is listening but nothing sends to it. DNS works (VPC DNS responds) but the resolver never populates BPF maps → no IPs in `allowed_cidrs` → all connections blocked by connect4.

**Why it happens:** resolv.conf override currently only happens for `ebpf` mode (line 617-623 of userdata.go). In `both` mode, DNS was handled by iptables DNAT → km-dns-proxy. With iptables removed, direct DNS works but bypasses BPF map population.

**How to avoid:** Add resolv.conf override for `both` mode when `--dns-port 53` is used.

**Warning signs:** Sandbox appears completely offline; all connections fail with EPERM; DNS resolution via `dig` succeeds (VPC DNS responds) but nothing reaches the eBPF resolver.

### Pitfall 5: BPF Program Regeneration After bpf.c Changes

**What goes wrong:** If bpf.c is modified, the generated Go files (`bpf_x86_bpfel.go`, `bpf_x86_bpfel.o`) are out of date. `make build` embeds the stale `.o` file. Changes to BPF logic appear to have no effect.

**Why it happens:** Generated files are committed. The Go build embeds the `.o` at compile time.

**How to avoid:** Run `make generate-ebpf` after any change to `bpf.c` or `headers/common.h`. Requires Docker (the Dockerfile.ebpf-generate handles clang/kernel headers). Commit the regenerated `.go` and `.o` files.

**Warning signs:** BPF logic changes have no observable effect on running sandbox; verifier shows unexpected program behavior.

### Pitfall 6: `km-dns-proxy` Still Started in `both` Mode

**What goes wrong:** If `km-dns-proxy` starts alongside the eBPF resolver both listening on `:5353` (or `:53`), the second service fails to bind. Or worse, iptables DNAT is removed but `km-dns-proxy` is still started — it binds fine but never receives queries (nothing redirects to it), wasting a process.

**How to avoid:** Update the sidecar startup block to skip `km-dns-proxy` for `both` mode (same logic as `ebpf` mode).

---

## Code Examples

All examples reference the existing codebase files.

### connect4 L7 Rewrite (already implemented in bpf.c)

```c
// pkg/ebpf/bpf.c — connect4, step 6 (lines 187-206)
// Already handles: check http_proxy_ips → stash original → rewrite to proxy
void *proxy_match = bpf_map_lookup_elem(&http_proxy_ips, &dst_ip);
if (proxy_match) {
    bpf_map_update_elem(&sock_to_original_ip, &cookie, &dst_ip, 0);
    bpf_map_update_elem(&sock_to_original_port, &cookie, &dst_port, 0);
    ctx->user_ip4 = const_mitm_proxy_address;
    __u16 proxy_port = (dst_port == 443) ?
        (__u16)const_https_proxy_port : (__u16)const_http_proxy_port;
    ctx->user_port = bpf_htons(proxy_port) << 16;
    emit_event(0, dst_ip, dst_port, ACTION_REDIRECT, LAYER_CONNECT4);
}
```

No changes needed to bpf.c for Phase 42.

### userdata.go: `both` Mode Gatekeeper Changes (target state)

```go
// In the ebpf-attach systemd unit template (around line 563-567 of userdata.go)
// Change from:
{{- if eq .Enforcement "ebpf" }}
  --dns-port 53 \
{{- else }}
  --dns-port 0 \
{{- end }}
  ...
{{- if eq .Enforcement "ebpf" }}
  --firewall-mode block \
{{- else }}
  --firewall-mode log \
{{- end }}

// Change to:
{{- if or (eq .Enforcement "ebpf") (eq .Enforcement "both") }}
  --dns-port 53 \
{{- else }}
  --dns-port 0 \
{{- end }}
  ...
{{- if or (eq .Enforcement "ebpf") (eq .Enforcement "both") }}
  --firewall-mode block \
{{- else }}
  --firewall-mode log \
{{- end }}
```

### userdata.go: L7ProxyHosts Template Field

```go
// pkg/compiler/userdata.go — in the UserdataParams struct (around line 827)
L7ProxyHosts string  // comma-separated domain suffixes for connect4 L7 redirect

// In buildUserdataParams (around line 939)
params.L7ProxyHosts = buildL7ProxyHosts(p)

// New helper function
func buildL7ProxyHosts(p *profile.SandboxProfile) string {
    hosts := []string{"github.com", "api.github.com", "raw.githubusercontent.com", "codeload.githubusercontent.com"}
    if p.Spec.Execution.UseBedrock {
        hosts = append(hosts, ".amazonaws.com", "api.anthropic.com")
    }
    return strings.Join(hosts, ",")
}

// In systemd unit template line 570, change:
//   --proxy-hosts "{{ .GitHubAllowedRepos }}" \
// to:
//   --proxy-hosts "{{ .L7ProxyHosts }}" \
```

### Resolver ProxyHosts Population (already implemented)

```go
// pkg/ebpf/resolver/resolver.go — isProxyHost + MarkForProxy (lines 268-277)
// Already correctly calls MarkForProxy for matching domains.
// No changes needed — just needs correct proxy hosts passed via --proxy-hosts.
if r.isProxyHost(domain) {
    r.cfg.MapUpdater.MarkForProxy(ip)
}
```

---

## State of the Art

| Old Approach | Current Approach | Phase 42 Change |
|--------------|------------------|-----------------|
| `both` = eBPF passive observer | eBPF logs only, iptables enforces | eBPF enforces, iptables removed |
| All traffic hits proxy | All HTTP/HTTPS → proxy | Only L7 hosts → proxy, others direct |
| km-dns-proxy handles `both` DNS | iptables DNAT → km-dns-proxy | eBPF resolver handles all DNS |
| --proxy-hosts = GitHub repo names | Non-functional (repos ≠ domains) | --proxy-hosts = L7 domain suffixes |
| connect4 in log mode for `both` | Observes, doesn't block | connect4 in block mode, primary enforcer |

---

## Open Questions

1. **Proxy PID Exemption in Gatekeeper Mode**
   - What we know: connect4 exempts `const_proxy_pid` (enforcer's own PID). iptables currently exempts proxy via `! --uid-owner km-sidecar`. With iptables removed, the proxy needs a different exemption.
   - What's unclear: Which mechanism is cleanest — PID file, UID check in BPF, or separate cgroup?
   - Recommendation: Write proxy PID to `/run/km/http-proxy.pid` from a systemd `ExecStartPost` script; pass to `km ebpf-attach --proxy-pid`. Alternatively, add a UID-based exemption check to connect4 using `bpf_get_current_uid_gid()` — simpler than PID file coordination. **UID approach is preferred** (no file coordination, survives proxy restart).

2. **HTTP_PROXY Env Vars in `both` Gatekeeper Mode**
   - What we know: connect4 transparently redirects L7 hosts to the proxy. Proxy env vars are redundant for kernel-redirected traffic but harmless.
   - Recommendation: **Keep proxy env vars for `both` mode.** Applications that check `$HTTPS_PROXY` before connecting (e.g. pip, npm, curl) will still connect to the proxy, which will either service the request (L7 host) or proxy the request directly (non-L7 allowed host). Belt-and-suspenders; removes a source of "it worked with proxy env vars but not without."

3. **`bedrock-runtime.*.amazonaws.com` Resolution in eBPF Resolver**
   - What we know: `--proxy-hosts .amazonaws.com` would mark all `*.amazonaws.com` IPs for proxy. The allowlist check in the resolver uses suffix matching.
   - What's unclear: If `.amazonaws.com` suffix is in ProxyHosts, every resolved amazonaws.com IP (VPC endpoints, S3, etc.) gets marked for proxy, adding unnecessary proxy overhead.
   - Recommendation: Use specific Bedrock endpoint suffix `.bedrock-runtime.*.amazonaws.com` is not possible with suffix matching alone. Alternative: only mark `bedrock-runtime.` prefix entries. Or accept the overhead for `both` mode — proxy will pass non-Bedrock amazonaws.com traffic through as `OkConnect` without MITM.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go test (`go test ./...`), E2E loops via `km create`/`km destroy` |
| Config file | None (standard Go testing) |
| Quick run command | `go test ./pkg/ebpf/... ./pkg/ebpf/resolver/... -run TestPinPath -v` |
| Full suite command | `go test ./pkg/ebpf/... ./pkg/compiler/... ./sidecars/http-proxy/...` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command | Notes |
|----------|-----------|-------------------|-------|
| `both` mode userdata uses `--firewall-mode block` | unit | `go test ./pkg/compiler/... -run TestUserdata` | Verify template output |
| `both` mode userdata uses `--dns-port 53` | unit | `go test ./pkg/compiler/... -run TestUserdata` | Verify template output |
| `both` mode omits `km-dns-proxy` sidecar start | unit | `go test ./pkg/compiler/... -run TestUserdata` | Check no `km-dns-proxy` in emitted script |
| `both` mode omits iptables DNAT rules | unit | `go test ./pkg/compiler/... -run TestUserdata` | Check no `iptables -t nat` in emitted script |
| L7 proxy hosts derived correctly from profile | unit | `go test ./pkg/compiler/... -run TestL7ProxyHosts` | New test needed |
| Resolver marks L7 IPs in http_proxy_ips map | unit | `go test ./pkg/ebpf/resolver/... -run TestProxyHosts` | Existing test infrastructure |
| DNS resolution → BPF map update → connect4 redirect | E2E | `km create profiles/goose-ebpf-gatekeeper.yaml --remote` | Manual SSM verification |
| Blocked domains return EPERM | E2E | `km create` + SSM: `curl blocked.com` | Manual verification |
| GitHub allowed repos enforced (403 on blocked repo) | E2E | `km create` + SSM: `git clone blocked-repo` | Manual verification |
| Non-L7 allowed hosts (pypi, npm) connect directly | E2E | `km create` + SSM: `pip install requests` | Manual, verify no proxy in path |
| Bedrock calls succeed and are metered | E2E | `km create` + `km otel` | Manual AI spend check |

### Sampling Rate

- **Per task commit:** `go test ./pkg/compiler/... -run TestUserdata`
- **Per wave merge:** `go test ./pkg/ebpf/... ./pkg/compiler/... ./sidecars/http-proxy/...`
- **Phase gate:** Full suite green + successful E2E loop (cold start + idle/resume) before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/compiler/userdata_test.go` — add test cases for `both` mode: assert `--firewall-mode block`, `--dns-port 53`, no iptables DNAT, no km-dns-proxy in emitted script
- [ ] `pkg/compiler/userdata_test.go` — add `TestL7ProxyHosts` verifying correct host derivation from profile fields

*(Existing `pkg/ebpf/resolver/resolver_test.go` and `pkg/ebpf/enforcer_test.go` already cover the BPF map path — no new test files needed for those.)*

---

## Sources

### Primary (HIGH confidence — direct code inspection)

- `/Users/khundeck/working/klankrmkr/pkg/ebpf/bpf.c` — BPF C programs, existing connect4 rewrite logic, map definitions
- `/Users/khundeck/working/klankrmkr/pkg/ebpf/enforcer.go` — Go BPF loader, Config struct, volatile constant wiring
- `/Users/khundeck/working/klankrmkr/pkg/ebpf/types.go` — ModeLog/ModeBlock constants, Config definition
- `/Users/khundeck/working/klankrmkr/pkg/ebpf/resolver/resolver.go` — DNS resolver, ProxyHosts support, isProxyHost + MarkForProxy flow
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go` — EC2 bootstrap template, current `both` mode config (firewall-mode log, dns-port 0, iptables DNAT)
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/ebpf_attach.go` — `km ebpf-attach` command, runEbpfAttach, how dnsPort=0 skips resolver
- `/Users/khundeck/working/klankrmkr/sidecars/http-proxy/httpproxy/proxy.go` — goproxy usage, CONNECT handling via Host header
- `/Users/khundeck/working/klankrmkr/profiles/goose-ebpf-gatekeeper.yaml` — test profile, enforcement: both confirmed

### Secondary (HIGH confidence — project design docs)

- `.planning/phases/42-ebpf-gatekeeper-mode-connect4-dnat-rewrite-for-selective-l7-proxy/CONTEXT.md` — design decisions, open questions
- `.planning/REQUIREMENTS.md` — EBPF-NET-03 (connect4 DNAT) requirement definition

---

## Metadata

**Confidence breakdown:**
- BPF C layer: HIGH — bpf.c already implements the connect4 rewrite; no BPF changes needed
- userdata.go changes: HIGH — specific line-level changes identified from code inspection
- Original destination mechanism: HIGH — goproxy architecture definitively resolves this (Host header, not BPF map)
- Proxy PID exemption: MEDIUM — two viable approaches identified; UID-based is cleanest but not yet confirmed against bpf_get_current_uid_gid() availability in cgroup/connect4 context
- E2E validation loops: no confidence level applicable (manual operational work)

**Research date:** 2026-04-02
**Valid until:** 2026-05-02 (stable codebase; only invalidated by concurrent Phase 40/41 changes to pkg/ebpf/)
