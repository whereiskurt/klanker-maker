# Phase 42: eBPF Gatekeeper Mode — connect4 DNAT Rewrite for Selective L7 Proxy

## Problem

Current `both` enforcement mode runs eBPF in `--firewall-mode log` (passive observer) while the proxy does all enforcement. eBPF and proxy are layered independently — eBPF doesn't gate the proxy:

```
Traffic --+-- iptables DNAT --> Proxy (blocks + L7 inspect)
          +-- eBPF cgroup hooks (--firewall-mode log, observes only)
```

This means:
- eBPF adds no enforcement value in `both` mode
- All traffic hits the proxy even when L7 inspection isn't needed
- No defense-in-depth — proxy is the single enforcement point

## Target Architecture

eBPF runs in `block` mode as the kernel-level gatekeeper. Only traffic eBPF allows reaches userspace. The proxy only sees hosts that need L7 inspection (GitHub repo filtering, Bedrock token metering).

```
Traffic --> eBPF cgroup (--firewall-mode block)
               |
               |-- denied domain? --> EPERM (kernel drops, proxy never sees it)
               |
               +-- allowed domain, needs L7? --> connect4 rewrites dst to 127.0.0.1:3128 --> Proxy
               |                                    |
               |                                    |-- MITM github.com, extract repo, check allowlist
               |                                    +-- Meter Bedrock tokens
               |
               +-- allowed domain, no L7 needed? --> direct connection (no proxy overhead)
```

## Key Design Decision: connect4 DNAT Rewrite

Rather than coordinating iptables ipsets with eBPF DNS resolution, the `connect4` BPF program performs the DNAT itself:

- `connect4` already intercepts every TCP `connect()` call and has the destination sockaddr
- For hosts flagged as needing L7 inspection, rewrite `dst_addr` to `127.0.0.1` and `dst_port` to `3128`
- This replaces iptables DNAT for `both` mode entirely — no iptables rules needed for proxy routing
- The original destination must be conveyed to the proxy (via PROXY protocol, SO_ORIGINAL_DST, or a BPF map lookup)

## Changes Required

### 1. eBPF enforcer (`pkg/ebpf/`)

- `connect4` hook: add L7-host detection and sockaddr rewrite logic
- New BPF map: `proxy_hosts` LPM trie or hash map of IPs that need L7 proxy
- DNS resolver: when resolving an L7-required host (github.com, bedrock endpoints), populate `proxy_hosts` map with resolved IPs
- Original destination tracking: BPF map keyed by `(src_port, pid)` storing original `(dst_ip, dst_port)` for proxy to retrieve

### 2. Userdata bootstrap (`pkg/compiler/userdata.go`)

- `both` mode: switch to `--firewall-mode block` (currently `log`)
- `both` mode: switch to `--dns-port 53` (currently `0`, defers to proxy DNS sidecar)
- `both` mode: remove iptables DNAT rules (connect4 handles routing to proxy)
- `both` mode: remove `km-dns-proxy` sidecar (eBPF resolver handles DNS)
- Keep `HTTP_PROXY`/`HTTPS_PROXY` env vars for application-level proxy awareness, or remove if connect4 rewrite makes them unnecessary

### 3. Proxy sidecar (`sidecars/http-proxy/`)

- Accept connections that were rewritten by connect4
- Retrieve original destination from BPF map or SO_ORIGINAL_DST
- Only needs to handle L7-required hosts (GitHub, Bedrock, Anthropic API)
- Reduced scope: no longer MITMs all HTTPS traffic

### 4. Profile/config

- New profile field or implicit behavior: list of hosts requiring L7 proxy inspection
- Default L7 hosts: `github.com`, `api.github.com`, `raw.githubusercontent.com`, `codeload.githubusercontent.com`, `bedrock-runtime.*.amazonaws.com`, `api.anthropic.com`
- Derive from existing `sourceAccess.github` and Bedrock config

### 5. `--proxy-hosts` flag (partially exists)

- Already passed on line 570 of userdata.go: `--proxy-hosts "{{ .GitHubAllowedRepos }}"`
- Repurpose or extend to mean "hosts that need L7 proxy routing via connect4 rewrite"

## Benefits

- **Defense in depth** — eBPF blocks at kernel level, proxy handles L7 policy
- **Reduced proxy load** — proxy only MITMs hosts needing L7 inspection
- **Faster non-L7 traffic** — allowed domains without repo filtering go direct
- **Simpler iptables** — no DNAT rules needed for `both` mode
- **Root bypass resistant** — kernel cgroup BPF can't be bypassed by sandbox user

## Complexity: HIGH

This is one of the more complex phases — it touches the BPF C code (kernel space), the Go eBPF userspace orchestrator, the userdata bootstrap template, and the proxy sidecar. Changes span three layers (kernel BPF, Go userspace, shell bootstrap) and a bug in any layer can silently break enforcement or brick the sandbox network entirely.

Key risk areas:
- **BPF connect4 sockaddr rewrite** — getting this wrong means connections either fail or bypass the proxy silently
- **Original destination tracking** — proxy must know where the connection was originally headed; BPF map coordination between connect4 and userspace is fiddly
- **DNS resolver / proxy_hosts map population** — race between DNS resolution and connect4 lookup; resolved IPs must be in the map before connect4 sees them
- **Bootstrap ordering** — eBPF must be loaded and DNS resolver ready before any sandbox process tries to connect

## Test Profile

`profiles/goose-ebpf-gatekeeper.yaml` — copy of `goose-ebpf.yaml` with `enforcement: both` for iterating on the gatekeeper design. Use this profile for all Phase 42 development and testing.

## Verification: Full E2E Loops

This phase MUST be validated with repeated full E2E lifecycle loops until provably working. Not a single happy-path pass — run the loop until it's boring:

```
km create profiles/goose-ebpf-gatekeeper.yaml --remote
km list                          # confirm running
# shell in via SSM, verify:
#   - DNS resolution works (allowed domains resolve, blocked don't)
#   - github.com access works but only for allowedRepos
#   - blocked repos get 403 from proxy (not timeout/EPERM)
#   - non-L7 hosts (pypi, npm) connect directly (no proxy in path)
#   - bedrock API calls succeed and are metered
#   - curl to a non-allowed domain fails (EPERM from eBPF)
km destroy <id> --remote --yes
# repeat
```

Run this loop across:
- Fresh create (cold start)
- Multiple shells in same sandbox
- After idle timeout / resume cycles
- With different allowedRepos configurations

Do not consider the phase complete until the loop is monotonously reliable.

### Disk Hygiene

Use `--remote` on all `km create` calls. Monitor local disk space throughout development — repeated builds, BPF compilation artifacts, and Go module caches add up. Periodically:
```bash
df -h /
du -sh ~/go/pkg/mod/cache/ ~/.cache/go-build/ /tmp/km-* 2>/dev/null
go clean -cache      # if build cache grows large
```
Clean caches proactively rather than waiting for disk pressure.

## Open Questions

- Original destination retrieval: `SO_ORIGINAL_DST` via getsockopt, PROXY protocol header, or BPF map lookup from proxy?
- Should `both` mode still set `HTTP_PROXY` env vars, or rely entirely on connect4 transparent rewrite?
- How to handle DNS for L7 hosts — eBPF resolver needs to know which resolved IPs map to L7-required hostnames to populate `proxy_hosts` BPF map
