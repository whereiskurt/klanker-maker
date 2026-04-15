# eBPF Network Enforcement & TLS Observability

Deep-dive into the eBPF implementation: cgroup-attached BPF programs for kernel-level network enforcement (Phase 40) and SSL uprobe plaintext capture for observability (Phase 41).

## Table of Contents

- [Enforcement Modes](#enforcement-modes)
- [Architecture Overview](#architecture-overview)
- [BPF Programs](#bpf-programs)
  - [cgroup/connect4 — TCP Connect Hook](#cgroupconnect4--tcp-connect-hook)
  - [cgroup/sendmsg4 — UDP/DNS Redirect](#cgroupsendmsg4--udpdns-redirect)
  - [sockops — TCP State Tracking](#sockops--tcp-state-tracking)
  - [cgroup_skb/egress — Packet-Level Backstop](#cgroup_skbegress--packet-level-backstop)
- [BPF Maps](#bpf-maps)
- [DNS Resolver](#dns-resolver)
- [Gatekeeper Mode — connect4 DNAT Rewrite (Phase 42)](#gatekeeper-mode--connect4-dnat-rewrite-phase-42)
- [Ring Buffer Audit Pipeline](#ring-buffer-audit-pipeline)
- [Volatile Constants](#volatile-constants)
- [Cgroup & Process Management](#cgroup--process-management)
- [Pin Paths & Persistence](#pin-paths--persistence)
- [Cleanup & Teardown](#cleanup--teardown)
- [SSL Uprobe Observability (Phase 41)](#ssl-uprobe-observability-phase-41)
- [Request Flow Examples](#request-flow-examples)
- [Build & Compilation](#build--compilation)
- [Troubleshooting](#troubleshooting)
- [Diagrams](#diagrams)

---

## Enforcement Modes

Set via `spec.network.enforcement` in the profile YAML:

```yaml
spec:
  network:
    enforcement: "both"   # "proxy" | "ebpf" | "both"
```

| Mode | iptables DNAT | DNS Proxy Sidecar | eBPF Enforcer | L7 MITM Proxy | SSL Uprobes | Use Case |
|------|---------------|-------------------|---------------|---------------|-------------|----------|
| `proxy` | Yes | Yes | No | Yes | No | Default. Backward compatible. All substrates. |
| `ebpf` | No | No | Yes | Optional | Optional | Maximum kernel-level security. EC2 only. |
| `both` | Yes | Yes | Yes | Yes | Optional | Belt-and-suspenders. eBPF primary + proxy for L7 inspection. EC2 only. |

**`both` mode** is the recommended production configuration. It provides:
- Kernel-level allow/deny via eBPF (cannot be bypassed by root)
- DNS allowlist enforcement via both the eBPF resolver AND the proxy DNS sidecar
- L7 inspection via MITM proxy (Bedrock token metering, GitHub repo filtering, Anthropic API metering)
- The eBPF layer acts as the primary enforcement; the proxy provides deep inspection on allowed traffic

---

## Architecture Overview

```
                    ┌─────────────────────────────────┐
                    │  Profile YAML                    │
                    │  enforcement: "both"             │
                    │  allowedDNSSuffixes: [...]        │
                    │  allowedHosts: [...]              │
                    └──────────┬──────────────────────┘
                               │
                               ▼
                    ┌─────────────────────────────────┐
                    │  km create → userdata.sh         │
                    │  Section 6b: eBPF bootstrap      │
                    └──────────┬──────────────────────┘
                               │
              ┌────────────────┼────────────────┐
              ▼                ▼                 ▼
   ┌──────────────────┐ ┌───────────┐ ┌──────────────────┐
   │ km ebpf-attach   │ │ iptables  │ │ Proxy Sidecars   │
   │ (systemd unit)   │ │ DNAT      │ │ (systemd units)  │
   │                  │ │ rules     │ │                  │
   │ 1. Load BPF      │ │ (both     │ │ km-dns-proxy     │
   │ 2. Create cgroup │ │  mode     │ │ km-http-proxy    │
   │ 3. Start resolver│ │  only)    │ │ km-audit-log     │
   │ 4. Start audit   │ │           │ │ km-tracing       │
   └────────┬─────────┘ └───────────┘ └──────────────────┘
            │
            ▼
   ┌──────────────────────────────────────────────────┐
   │  Kernel — Cgroup BPF Programs                     │
   │  /sys/fs/cgroup/km.slice/km-{sandboxID}.scope     │
   │                                                    │
   │  ┌──────────┐ ┌──────────┐ ┌────────┐ ┌────────┐ │
   │  │connect4  │ │sendmsg4  │ │sockops │ │egress  │ │
   │  │TCP hook  │ │UDP/DNS   │ │state   │ │L3 drop │ │
   │  │allow/    │ │redirect  │ │tracking│ │backstop│ │
   │  │deny/     │ │to :53    │ │port→   │ │        │ │
   │  │redirect  │ │resolver  │ │cookie  │ │        │ │
   │  └──────────┘ └──────────┘ └────────┘ └────────┘ │
   │                                                    │
   │  ┌──────────────────────────────────────────────┐ │
   │  │ BPF Maps (pinned to /sys/fs/bpf/km/{id}/)   │ │
   │  │ allowed_cidrs | http_proxy_ips | events | .. │ │
   │  └──────────────────────────────────────────────┘ │
   └──────────────────────────────────────────────────┘
```

---

## BPF Programs

All four programs are compiled from `pkg/ebpf/bpf.c` via `bpf2go` and embedded in the `km` binary. They attach to the sandbox cgroup at load time.

### cgroup/connect4 — TCP Connect Hook

**Hook point:** Every `connect()` syscall from processes in the cgroup.

**Logic flow:**
1. **Exempt the enforcer and proxy processes** — check PID against `const_proxy_pid` (the enforcer's own PID) and `const_http_proxy_pid` (the MITM proxy PID in gatekeeper mode). Both PIDs are passed via `--proxy-pid` flags. This prevents the DNS resolver, enforcer, and L7 proxy from being blocked by their own enforcement.
2. **Exempt IMDS** — allow 169.254.169.254 unconditionally (EC2 instance metadata).
3. **Exempt localhost** — allow 127.0.0.0/8 unconditionally.
4. **Record socket → PID mapping** — store in `socket_pid_map` for audit logging.
5. **LPM trie lookup** — check destination IP against `allowed_cidrs` map.
6. **If denied:**
   - Emit a deny event to the ring buffer (action=DENY, layer=CONNECT4)
   - Return 0 (EPERM) if `const_firewall_mode == MODE_BLOCK`
   - Return 1 (allow but log) if `const_firewall_mode == MODE_LOG`
7. **If allowed AND in `http_proxy_ips`:**
   - Stash original destination in `sock_to_original_ip` and `sock_to_original_port`
   - Rewrite `ctx->user_ip4` to `const_mitm_proxy_address` (127.0.0.1)
   - Rewrite `ctx->user_port` to `const_http_proxy_port` (3128) or `const_https_proxy_port` (3129) based on original port
   - Emit a redirect event to the ring buffer
8. **Return 1** (allow)

**Key subtlety:** The proxy redirect is transparent to the application. `curl https://api.github.com` thinks it's connecting to GitHub, but the BPF program silently rewrites the destination to the local MITM proxy. The proxy then recovers the real destination via the `sock_to_original_ip/port` maps.

### cgroup/sendmsg4 — UDP/DNS Redirect

**Hook point:** Every `sendmsg()` syscall for UDP from processes in the cgroup.

**Logic flow:**
1. Exempt the enforcer process (`const_proxy_pid`) and HTTP proxy process (`const_http_proxy_pid`) — same dual-PID check as connect4.
2. Only intercept port 53 (DNS) — non-DNS UDP passes through.
3. Stash original destination IP/port.
4. Rewrite destination to 127.0.0.1:`const_dns_proxy_port` (53 or 5353).
5. Emit a redirect event.

**Key subtlety:** This means ALL DNS queries from sandbox processes go through the enforcer's resolver, regardless of what `/etc/resolv.conf` says. Even if a process hardcodes `8.8.8.8:53`, the BPF program redirects it.

### sockops — TCP State Tracking

**Hook point:** TCP connection state transitions (specifically `BPF_SOCK_OPS_ACTIVE_ESTABLISHED_CB`).

**Logic flow:**
1. When a TCP connection enters ESTABLISHED state, map the local source port to the socket cookie in `src_port_to_sock`.

**Why this exists:** The MITM proxy needs to recover the real destination IP/port after `connect4` rewrites it. The proxy accepts a connection from the sandbox process on port 3128, reads the peer's source port (e.g., 50123), looks up `src_port_to_sock[50123]` to get the socket cookie, then looks up `sock_to_original_ip[cookie]` and `sock_to_original_port[cookie]` to find where the connection was originally headed.

### cgroup_skb/egress — Packet-Level Backstop

**Hook point:** Every outbound IP packet leaving the cgroup.

**Logic flow:**
1. Parse IPv4 header.
2. Exempt IMDS (169.254.169.254) and localhost (127.0.0.0/8).
3. LPM trie lookup on destination IP.
4. If not in allowlist AND `MODE_BLOCK` → drop packet (return 0), emit deny event.
5. Otherwise pass (return 1).

**Why this exists as a backstop:** `connect4` only hooks TCP `connect()` calls. Raw sockets, ICMP, and some edge cases might bypass the connect hook. The egress SKB filter catches these at the packet level — defense in depth.

---

## BPF Maps

All maps are pinned to `/sys/fs/bpf/km/{sandboxID}/` and survive process restarts.

| Map | Type | Key | Value | Max Entries | Purpose |
|-----|------|-----|-------|-------------|---------|
| `allowed_cidrs` | `LPM_TRIE` | `{prefixlen u32, addr [4]u8}` | `u32` | 65535 | CIDR allowlist. Populated by DNS resolver + pre-seed. |
| `http_proxy_ips` | `HASH` | `u32` (dest IP) | `u32` | 262144 | IPs needing L7 proxy inspection (GitHub, Bedrock, Anthropic). |
| `sock_to_original_ip` | `HASH` | `u64` (socket cookie) | `u32` (original IP) | 262144 | Real dest IP before BPF rewrite. |
| `sock_to_original_port` | `HASH` | `u64` (socket cookie) | `u16` (original port) | 262144 | Real dest port before BPF rewrite. |
| `src_port_to_sock` | `HASH` | `u16` (local src port) | `u64` (socket cookie) | 262144 | Proxy looks up socket by peer's source port. |
| `socket_pid_map` | `HASH` | `u64` (socket cookie) | `u32` (PID) | 10000 | PID attribution for audit events. |
| `events` | `RINGBUF` | — | `struct event` (64 bytes) | 16MB | Deny/redirect events to userspace. |

### LPM Trie — How CIDR Lookups Work

The `allowed_cidrs` map uses an LPM (Longest Prefix Match) trie, which is the same data structure used in IP routing tables. A single map lookup can match any CIDR prefix:

```
Key: {prefixlen: 8, addr: [10, 0, 0, 0]}   → matches 10.0.0.0/8 (entire VPC)
Key: {prefixlen: 32, addr: [140, 82, 112, 35]} → matches 140.82.112.35/32 (single host)
```

When the DNS resolver resolves `api.github.com` to `140.82.112.35`, it inserts a `/32` entry. When pre-seeding VPC CIDRs, a `/8` covers the entire range. The trie finds the most specific match.

---

## DNS Resolver

The enforcer runs a DNS resolver at 127.0.0.1:53 (configurable) that:

1. **Listens** on UDP and TCP port 53.
2. **Checks every query** against the profile's `allowedDNSSuffixes` list. Matching is case-insensitive with suffix matching (`.github.com` allows `api.github.com`).
3. **Denied queries** → return NXDOMAIN immediately.
4. **Allowed queries** → forward to upstream VPC DNS at 169.254.169.253:53.
5. **Extract A records** from the response.
6. **For each resolved IP:**
   - Call `enforcer.AllowIP(ip)` → inserts `/32` into `allowed_cidrs` LPM trie.
   - If the domain is in `ProxyHosts` (needs L7 inspection): call `enforcer.MarkForProxy(ip)` → inserts into `http_proxy_ips` hash map.
7. **Cache** resolved entries with DNS TTL.
8. **TTL sweep goroutine** periodically evicts expired entries from the BPF map.

**Dynamic allowlist model:** The `allowed_cidrs` map starts nearly empty (just VPC CIDRs, IMDS, link-local). As the agent resolves domains, IPs are added. As TTLs expire, IPs are removed. The allowlist is always the minimum set of IPs needed for currently-active connections.

---

## Gatekeeper Mode — connect4 DNAT Rewrite + Transparent Proxy (Phase 42)

When `enforcement: "both"`, the eBPF layer acts as a **gatekeeper** that selectively redirects traffic to the L7 MITM proxy via kernel-level DNAT. This is the recommended production configuration — it combines kernel-level IP enforcement with userspace L7 inspection for hosts that need deep filtering (Bedrock token metering, GitHub repo-level allowlisting).

**E2E verified on AL2023 kernel 6.18:** allowed repos clone successfully, blocked repos receive 403 from the transparent proxy, non-allowlisted domains are blocked by BPF with EPERM, non-proxy hosts connect directly without touching the proxy.

### How connect4 DNAT Works

The `connect4` BPF program performs transparent destination NAT at the syscall level, before the TCP handshake begins:

1. **Dual-PID exemption** — check PID against `const_proxy_pid` (eBPF enforcer) and `const_http_proxy_pid` (HTTP proxy sidecar). Both are passed via `--proxy-pid` flags. Exempted processes bypass all enforcement to prevent redirect loops.
2. **IMDS + localhost exemption** — always allow 169.254.169.254 and 127.0.0.0/8.
3. **IP allowlist check** — destination IP is looked up in the `allowed_cidrs` LPM trie. If denied, emit DENY event and return `EPERM` (block mode) or allow with log (log mode).
4. **L7 proxy check** — for allowed IPs, check the `http_proxy_ips` hash map. If the IP is **not** proxy-marked, the connection goes direct (no DNAT).
5. **DNAT rewrite** — if the IP is proxy-marked:
   - Stash the original destination IP and port in `sock_to_original_ip` / `sock_to_original_port` (keyed by socket cookie)
   - Rewrite `ctx->user_ip4` to `const_mitm_proxy_address` (127.0.0.1 in network byte order)
   - Select proxy port based on original port: 443 → `const_https_proxy_port`, others → `const_http_proxy_port`
   - Emit a `REDIRECT` event to the ring buffer

The application never sees the rewrite — `curl https://api.github.com` thinks it's connecting to GitHub, but the kernel silently routes the `connect()` syscall to 127.0.0.1:3128.

### Transparent Proxy — BPF Map Lookup Chain

The key innovation in Phase 42 is the **transparent proxy** (`sidecars/http-proxy/httpproxy/transparent.go`). Because BPF rewrites the destination at the syscall level, the HTTP proxy receives raw TLS (not HTTP CONNECT). The proxy recovers the original destination via a three-step BPF map lookup:

```
                        ┌──────────────────────────────────────┐
                        │  Sandbox process: curl github.com    │
                        └──────────┬───────────────────────────┘
                                   │ connect(140.82.112.4:443)
                                   ▼
                        ┌──────────────────────────────────────┐
                        │  BPF connect4 hook                    │
                        │  1. allowed_cidrs[140.82.112.4] ✓     │
                        │  2. http_proxy_ips[140.82.112.4] ✓    │
                        │  3. Stash: cookie→140.82.112.4:443    │
                        │  4. Rewrite → 127.0.0.1:3128          │
                        └──────────┬───────────────────────────┘
                                   │ connect(127.0.0.1:3128)
                                   ▼
                        ┌──────────────────────────────────────┐
                        │  BPF sockops hook                     │
                        │  ACTIVE_ESTABLISHED: src_port→cookie  │
                        └──────────┬───────────────────────────┘
                                   │
                                   ▼
                        ┌──────────────────────────────────────┐
                        │  HTTP Proxy (TransparentListener)     │
                        │  1. Accept conn from sandbox process  │
                        │  2. Peek first byte: 0x16 (TLS) →    │
                        │     transparent mode                  │
                        │  3. peer_port = conn.RemoteAddr().Port│
                        │  4. cookie = src_port_to_sock[port]   │
                        │  5. orig_ip = sock_to_original_ip[ck] │
                        │  6. orig_port = sock_to_original_port │
                        │     → 140.82.112.4:443 (recovered!)  │
                        │  7. TLS-terminate with platform CA    │
                        │  8. Apply GitHub repo filter          │
                        │  9. Forward to real destination       │
                        └──────────────────────────────────────┘
```

**Protocol detection:** The proxy peeks at the first byte. `0x16` (TLS ClientHello) triggers transparent mode with BPF map lookup. Non-TLS traffic (regular HTTP) is passed to goproxy's standard CONNECT handler.

**Lazy map loading:** BPF maps are loaded from pinned bpffs paths (`/sys/fs/bpf/km/{sandboxID}/`) on first transparent connection, not at startup. This avoids startup ordering issues between the enforcer (which pins the maps) and the proxy.

**Byte order handling:** A critical operational lesson from Phase 42 — BPF stores IPs and ports in network byte order. The proxy must use `binary.NativeEndian.PutUint32` and manual byte-swap for ports (`(raw >> 8) | (raw << 8)`) to correctly recover the original destination. Multiple E2E bug fixes addressed NBO/HBO mismatches.

### Proxy IP Map Population

The `http_proxy_ips` map is populated by the DNS resolver at two points:

1. **At startup** — the enforcer calls `MarkForProxy()` for pre-seeded proxy host IPs (resolved via `net.LookupHost()` on each `--proxy-hosts` domain).
2. **On DNS resolution** — when a domain matches the `ProxyHosts` list (configured from profile fields that need L7 inspection: `sourceAccess.github.allowedRepos`, Bedrock endpoint, Anthropic API), the resolver calls `enforcer.MarkForProxy(ip)` to insert the resolved IP into the hash map.

L7-required hosts are derived automatically from the profile:
- `github.com`, `api.github.com`, `*.githubusercontent.com` — from `sourceAccess.github`
- `bedrock-runtime.*.amazonaws.com` — from `useBedrock: true`
- `api.anthropic.com` — from `allowedHosts` containing Anthropic endpoints

### Bootstrap Ordering (both mode)

The `both` mode bootstrap in userdata has a specific ordering to ensure BPF maps are pinned before the proxy starts:

1. **eBPF enforcer starts** (`km-ebpf-enforcer.service`) — loads BPF, creates cgroup, pins maps to bpffs, starts DNS resolver
2. **chmod BPF pin directory** — `ExecStartPost` sets `/sys/fs/bpf/km/{id}/` readable by `km-sidecar` user
3. **HTTP proxy restarts** — `ExecStartPost` restarts `km-http-proxy.service` so it can find the pinned maps
4. **Proxy loads maps lazily** — on first transparent TLS connection, `TransparentListener.loadMaps()` opens pinned maps from bpffs

### Gatekeeper vs Pure eBPF

| Behavior | Pure eBPF (`enforcement: "ebpf"`) | Gatekeeper (`enforcement: "both"`) |
|----------|-----------------------------------|------------------------------------|
| Firewall mode | `--firewall-mode block` (strict deny) | `--firewall-mode block` (strict deny) |
| DNS handling | eBPF enforcer's built-in resolver on `:53` | eBPF enforcer's built-in resolver on `:53` |
| Proxy sidecars | Skipped (no km-dns-proxy, no km-http-proxy) | Active (HTTP proxy with transparent mode, audit, tracing) |
| L7 filtering | None (IP-level only) | GitHub repo filtering, Bedrock token metering, Anthropic API metering |
| Non-L7 traffic | Direct (kernel-only path) | Direct (connect4 skips DNAT for non-proxy IPs) |
| Proxy env vars | Not set | Set (`HTTP_PROXY`, `HTTPS_PROXY`, etc.) |
| iptables DNAT | None | DNS only (`:53` → `:5353`); HTTP/HTTPS handled by BPF connect4 |
| PID exemption | Single PID (enforcer) | Dual PID (enforcer + HTTP proxy sidecar) |

### E2E Verification Results

Verified on `goose-ebpf-gatekeeper` profile (EC2, AL2023 kernel 6.18, `enforcement: both`):

| Scenario | Expected | Result |
|----------|----------|--------|
| `git clone` allowed repo | Clone succeeds | **PASS** — transparent proxy forwards to GitHub |
| `git clone` blocked repo | 403 from proxy | **PASS** — proxy intercepts, returns GitHub 403 response |
| `curl evil.com` | EPERM (connection refused) | **PASS** — BPF connect4 blocks at kernel level |
| `pip install requests` (non-L7 host) | Direct connection, no proxy | **PASS** — connect4 skips DNAT, traffic flows direct |
| Bedrock API call | Token metered | **PASS** — transparent proxy captures response, meters tokens |
| `iptables -F -t nat` then retry | Still enforced | **PASS** — BPF enforcement independent of iptables |

### Built-in Profiles

- **`goose-ebpf-gatekeeper`** — gatekeeper mode with eBPF + proxy enforcement, GitHub repo filtering, and Bedrock token metering.
- **`goose-ebpf`** — pure eBPF enforcement, no proxy. Maximum security, lowest overhead.

### Operational Lessons from Phase 42

1. **Byte order is the #1 source of bugs.** BPF stores IPs in network byte order (NBO) as `__u32`. cilium/ebpf's `Map.Lookup/Put` use `binary.NativeEndian` for marshaling. The proxy must convert NBO ports via manual byte-swap, not `binary.BigEndian`.

2. **Bootstrap ordering matters.** The enforcer must pin BPF maps before the proxy starts. Solution: `ExecStartPost` in the enforcer's systemd unit runs `chmod` on the bpffs directory, then restarts the proxy.

3. **Transparent TLS requires platform CA trust.** The proxy generates leaf certificates signed by the platform CA (same CA used in proxy mode). Sandbox processes must trust the CA via the system cert bundle.

4. **Dual-PID exemption is essential.** Without exempting the proxy's PID, the proxy's own outbound connections to real destinations would be intercepted by connect4, creating an infinite redirect loop.

---

## Ring Buffer Audit Pipeline

BPF programs emit structured events to a ring buffer. A userspace consumer reads and logs them.

**Event structure (64 bytes):**
```c
struct event {
    __u64 timestamp;    // nanoseconds since boot
    __u32 pid;          // process ID
    __u32 src_ip;       // network byte order
    __u32 dst_ip;       // network byte order
    __u16 dst_port;     // host byte order
    __u8  action;       // 0=deny, 1=allow, 2=redirect
    __u8  layer;        // 1=connect4, 2=sendmsg4, 3=egress, 4=sockops
    __u8  comm[16];     // process name (from bpf_get_current_comm)
    __u8  _pad[7];      // padding to 64 bytes
};
```

**Actions:** `ACTION_DENY (0)`, `ACTION_ALLOW (1)`, `ACTION_REDIRECT (2)`

**Layers:** `LAYER_CONNECT4 (1)`, `LAYER_SENDMSG4 (2)`, `LAYER_EGRESS_SKB (3)`, `LAYER_SOCKOPS (4)`

**Output (structured JSON):**
```json
{
  "event_type": "ebpf_network_deny",
  "sandbox_id": "claude-e6c7d024",
  "pid": 1234,
  "src_ip": "10.0.1.5",
  "dst_ip": "203.0.113.45",
  "dst_port": 443,
  "action": "deny",
  "layer": "connect4",
  "comm": "curl"
}
```

---

## Volatile Constants

BPF programs use `volatile const` variables that are set at load time per-sandbox, without recompiling:

```c
volatile const __u32 const_dns_proxy_port = 5353;      // resolver listen port
volatile const __u32 const_proxy_pid = 0;               // enforcer PID (exempt from rules)
volatile const __u32 const_http_proxy_pid = 0;          // HTTP proxy PID (exempt from BPF interception in gatekeeper mode)
volatile const __u32 const_http_proxy_port = 3128;      // L7 proxy HTTP port
volatile const __u32 const_https_proxy_port = 3129;     // L7 proxy HTTPS port
volatile const __u16 const_firewall_mode = MODE_LOG;    // MODE_LOG=0, MODE_ALLOW=1, MODE_BLOCK=2
volatile const __u32 const_mitm_proxy_address = 0x0100007f;  // 127.0.0.1 in network byte order
```

Set via `spec.Variables[name].Set(value)` in Go before loading programs into the kernel. The `volatile` keyword prevents the BPF verifier from constant-folding them away.

---

## Cgroup & Process Management

**Cgroup v2 path:** `/sys/fs/cgroup/km.slice/km-{sandboxID}.scope`

**Creation (userdata.sh):**
```bash
mkdir -p /sys/fs/cgroup/km.slice/km-{sandboxID}.scope
chown root:sandbox /sys/fs/cgroup/km.slice/km-{sandboxID}.scope/cgroup.procs
chmod 664 /sys/fs/cgroup/km.slice/km-{sandboxID}.scope/cgroup.procs
```

**Process placement — two mechanisms:**

1. **Shell wrapper** — the sandbox user's login shell is replaced with `km-sandbox-shell`:
   ```bash
   #!/bin/bash
   echo $$ > /sys/fs/cgroup/km.slice/km-{sandboxID}.scope/cgroup.procs 2>/dev/null
   exec /bin/bash "$@"
   ```
   Every SSH session and `km shell` invocation places itself into the cgroup.

2. **profile.d hook** — a fallback for processes started by other means:
   ```bash
   if [ "$(whoami)" = "sandbox" ]; then
     echo $$ > /sys/fs/cgroup/km.slice/km-{sandboxID}.scope/cgroup.procs 2>/dev/null
   fi
   ```

**What's NOT in the cgroup:** The enforcer process (`km ebpf-attach`), SSM agent, proxy sidecars, and tracing collector. These run in the root cgroup and are unaffected by BPF enforcement.

---

## Pin Paths & Persistence

All BPF programs and maps are pinned to the BPF filesystem:

```
/sys/fs/bpf/km/{sandboxID}/
├── connect4_link       # program link
├── sendmsg4_link       # program link
├── sockops_link        # program link
├── egress_link         # program link
├── allowed_cidrs       # LPM trie map
├── http_proxy_ips      # hash map
├── sock_to_original_ip # hash map
├── sock_to_original_port # hash map
├── src_port_to_sock    # hash map
├── socket_pid_map      # hash map
└── events              # ring buffer
```

Pinning means the BPF programs continue enforcing even if the `km ebpf-attach` process crashes or is restarted. The resolver stops (DNS goes stale), but existing connections and the allowlist map remain active.

**IsPinned check:** Verifies all 4 link files exist. Used by `km destroy` to determine if eBPF cleanup is needed.

---

## Cleanup & Teardown

`km destroy` calls `cleanupEBPF(sandboxID)`:

1. `ebpfpkg.IsPinned(sandboxID)` — check if BPF programs are pinned.
2. If not pinned → skip (sandbox used proxy mode).
3. `ebpfpkg.Cleanup(sandboxID)`:
   - `os.RemoveAll("/sys/fs/bpf/km/{sandboxID}/")` — removes all pinned programs and maps.
   - Best-effort: remove `/sys/fs/bpf/km/` if empty.
   - `RemoveSandboxCgroup(sandboxID)` — remove cgroup directory.
   - Best-effort: remove `km.slice` parent if empty.

**Remote destroy (Lambda):** The Lambda triggers EC2 instance termination. Since bpffs is an in-memory filesystem, all pinned programs and maps are automatically cleaned up when the instance is terminated. The `cleanupEBPF` call is a no-op in this case (running on the operator's laptop, not the EC2 instance).

---

## SSL Uprobe Observability (Phase 41)

An `ebpf-observer` sidecar attaches uprobes to TLS library functions for passive plaintext capture. **E2E verified on AL2023 kernel 6.18** with 8 probes attaching successfully to OpenSSL 3.2.2.

### TLS Library Coverage

| Library | Binary | Uprobe Target | Attach Method | Status | Limitations |
|---------|--------|---------------|---------------|--------|-------------|
| OpenSSL 3.x | `/usr/lib64/libssl.so.3` | `SSL_write`, `SSL_read`, `SSL_write_ex`, `SSL_read_ex` | Standard dynamic symbol | **E2E verified** | None — most reliable |
| Go crypto/tls | Per-binary (e.g., goose) | `writeRecordLocked` / `Read` | Symbol scan + per-RET offsets | Schema-ready | Binary must be unstripped; uretprobe crashes Go |
| BoringSSL | Per-binary (e.g., Bun/Claude Code) | `SSL_write` | Byte-pattern offset discovery | Schema-ready | Offsets break per Bun version |
| rustls | Per-binary (future) | `rustls_connection_write_tls` | Reverse-correlation | Schema-ready | Experimental |

**Probe count (OpenSSL):** 8 probes attach — `SSL_write` entry, `SSL_read` entry+return, `SSL_write_ex` entry, `SSL_read_ex` entry+return, plus `__sys_connect` and `accept4` kprobes for connection correlation.

### What Uprobes See

- Function entry: plaintext buffer, buffer length, SSL context pointer
- HTTP/1.1: full request/response in plaintext (verified: git-smart-HTTP via `git clone`)
- HTTP/2: HPACK-compressed binary frames — **not parseable** by the HTTP/1.1 parser (GitHub API uses HTTP/2)
- Connection metadata: PID, TID, FD, remote IP/port (via `conn_map` from `__sys_connect` kprobe)

### What Uprobes Cannot Do

- **Block requests** — uprobes fire at function entry but cannot prevent `SSL_write` from completing. The only enforcement action is `bpf_send_signal(SIGKILL)` which kills the entire process.
- **Meter token usage** — Bedrock streaming responses are chunked HTTP/2 data frames; uprobe tooling can only capture HTTP/1.1 headers. Token counting stays in the MITM proxy.
- **Replace the proxy** — the proxy provides active enforcement (GitHub repo filtering, budget 403s). Uprobes provide parallel observability.
- **Parse HTTP/2** — the GitHub API uses HTTP/2 over TLS. Captured plaintext contains HPACK-compressed binary, not `GET /repos/... HTTP/1.1`. Git-smart-HTTP (clone/push) uses HTTP/1.1 and IS captured correctly.

### Operational Lessons from Phase 41

1. **BPF verifier on AL2023 kernel 6.18 is strict about `bpf_probe_read_user` length.** The verifier rejects calls where the length parameter might be negative (signed taint). The fix is a bitwise AND at the assignment point: `copy_len = len & 0x3FFF` — this statically bounds the value. If-clamping (`if (len > MAX) len = MAX`) is NOT sufficient because it doesn't remove the signed taint.

2. **cilium/ebpf map type marshaling is strict.** If BPF declares `__type(key, __u8)`, Go must use `uint8` not `uint32`. Mismatches fail silently with a logged warning.

3. **Uprobes are global, not cgroup-scoped.** They fire for ALL processes system-wide (SSM agent, yum, etc.), creating noise. Filter by PID/cgroup in the userspace consumer.

4. **Library discovery uses /proc/pid/maps.** The scanner finds `/usr/lib64/libssl.so.3` matching `ldd` output. On AL2023, `/lib64` is a symlink to `/usr/lib64`.

### Go crypto/tls — The uretprobe Problem

Go uses dynamic stack resizing and a non-standard calling convention. A standard `uretprobe` (which rewrites the return address on the stack) corrupts Go's stack frame and crashes the process.

**Workaround:** Instead of one uretprobe, attach multiple uprobes — one at each `RET` instruction offset within the target function. This requires disassembling the binary at deployment time to find all return sites. ecapture implements this, but it must be done per-binary per-Go-version.

### Claude Code / Bun — The BoringSSL Problem

Claude Code ships as a Bun binary with BoringSSL statically linked. The symbols are stripped — `SSL_write` doesn't appear in the dynamic symbol table.

**Workaround:** Byte-pattern matching against known BoringSSL function prologues (instruction sequences unique to `SSL_write`). These offsets are fragile and break on every Bun update. A calibration step runs at deployment time to discover current offsets.

---

## Request Flow Examples

### Example 1: Allowed HTTPS to api.github.com (both mode)

```
1. Agent: curl https://api.github.com/repos/org/repo
   ↓
2. DNS query: api.github.com
   → sendmsg4: intercept port 53, redirect to 127.0.0.1:53
   → Resolver: "github.com" matches allowedDNSSuffixes ✓
   → Forward to 169.254.169.253 → resolve to 140.82.112.35
   → AllowIP(140.82.112.35) → insert /32 into allowed_cidrs
   → MarkForProxy(140.82.112.35) → insert into http_proxy_ips
   ↓
3. TCP connect: connect(140.82.112.35:443)
   → connect4: LPM lookup 140.82.112.35 → FOUND ✓
   → http_proxy_ips lookup → FOUND ✓ (needs L7 inspection)
   → Stash original: sock_to_original_ip[cookie] = 140.82.112.35
   → Rewrite: dest = 127.0.0.1:3128
   → Emit: event(action=REDIRECT, layer=CONNECT4)
   ↓
4. Transparent proxy (TransparentListener): accept connection on :3128
   → Peek first byte: 0x16 (TLS ClientHello) → transparent mode
   → Read peer source port (50123)
   → BPF map lookup chain:
     src_port_to_sock[50123] → cookie (12345)
     sock_to_original_ip[12345] → 140.82.112.35 (NBO → net.IP)
     sock_to_original_port[12345] → 443 (NBO → host byte order)
   → Recovered original dest: 140.82.112.35:443
   → TLS-terminate with platform CA (generate leaf cert for SNI host)
   → Read decrypted HTTP/1.1 request
   → Inspect: is /repos/org/repo in allowedRepos? ✓
   → Connect to real 140.82.112.35:443, forward request and response
   ↓
5. SSL uprobe (if Phase 41): observe SSL_write plaintext
   → Log: GET /repos/org/repo, Host: api.github.com
```

### Example 2: Denied connection to unauthorized host

```
1. Agent: curl https://evil.com
   ↓
2. DNS query: evil.com
   → sendmsg4: redirect to resolver
   → Resolver: "evil.com" does NOT match allowedDNSSuffixes ✗
   → Return NXDOMAIN
   ↓
3. Agent sees: "Could not resolve host: evil.com"
   (Connection never attempted — denied at DNS layer)
```

### Example 3: Denied connection to hardcoded IP (bypassing DNS)

```
1. Agent: curl https://203.0.113.45
   ↓
2. No DNS query (IP used directly)
   ↓
3. TCP connect: connect(203.0.113.45:443)
   → connect4: LPM lookup 203.0.113.45 → NOT FOUND ✗
   → Emit: event(action=DENY, layer=CONNECT4)
   → Return 0 (EPERM)
   ↓
4. Agent sees: "Connection refused"
   ↓
5. If packet somehow escapes connect4:
   → egress_skb: parse IPv4, LPM lookup → NOT FOUND ✗
   → Drop packet (return 0)
   (Belt and suspenders — connect4 + egress_skb)
```

### Example 4: Root user attempting yum install (proxy mode vs eBPF mode)

```
Proxy mode:
  root# yum install -y nmap
  → yum resolves mirror.centos.org via /etc/resolv.conf
  → Root is exempt from iptables DNAT (uid 0 not redirected)
  → Connects directly to mirror — BYPASSES PROXY ✗
  → Package installs successfully

eBPF mode:
  root# yum install -y nmap
  → yum resolves mirror.centos.org
  → sendmsg4: redirect DNS to resolver (PID check: not enforcer PID)
  → Resolver: "centos.org" not in allowedDNSSuffixes
  → Return NXDOMAIN
  → yum: "Error: Failed to download metadata"
  ↓
  Even if DNS is somehow resolved:
  → connect4: LPM lookup on mirror IP → NOT FOUND ✗
  → Return EPERM
  → yum: "Connection refused"
```

---

## Build & Compilation

**Generate eBPF bytecode:**
```bash
make generate-ebpf
```

This runs:
1. `docker build -f Dockerfile.ebpf-generate` — creates a build container with clang, libbpf, and system headers
2. `bpf2go -target amd64` compiles `pkg/ebpf/bpf.c` → generates `bpf_x86_bpfel.go` (182-line loader + embedded bytecode) and `bpf_x86_bpfel.o` (19,736 bytes)
3. For SSL uprobes: generates `pkg/ebpf/tls/opensslbpf_x86_bpfel.go` (16KB) and `pkg/ebpf/tls/connectbpf_x86_bpfel.go` (5KB)
4. Generated files are committed to git — the bytecode is linked into the `km` binary at compile time, no runtime clang dependency on sandbox instances

**Go build tags:**
- `//go:build linux` — enforcer.go (real implementation)
- `//go:build !linux` — enforcer_stub.go (no-op stubs for macOS dev)

**E2E verification pipeline:** `make generate-ebpf` → `make build` → `make sidecars` (uploads km binary to S3) → `km create --remote` → SSM into instance → test enforcement → `km destroy`. Each cycle takes ~3-5 minutes. Phase 40 required 14 E2E iterations; Phase 41 required 3; Phase 42 required 8+ with byte-order bug fixes.

---

## Troubleshooting

### Checking if eBPF is active

```bash
# On the sandbox instance:
ls /sys/fs/bpf/km/{sandboxID}/
# Should show: connect4_link, sendmsg4_link, sockops_link, egress_link, plus maps

# Check cgroup membership:
cat /sys/fs/cgroup/km.slice/km-{sandboxID}.scope/cgroup.procs
# Should list sandbox user's PIDs

# Check enforcer service:
systemctl status km-ebpf-enforcer
```

### Checking the audit log

```bash
journalctl -u km-ebpf-enforcer -f
# Shows structured JSON events: deny, redirect, allow
```

### Checking the DNS resolver

```bash
# From inside the sandbox:
dig api.github.com @127.0.0.1
# Should resolve (if github.com is in allowedDNSSuffixes)

dig evil.com @127.0.0.1
# Should return NXDOMAIN (not in allowlist)
```

### Common issues

| Symptom | Cause | Fix |
|---------|-------|-----|
| All connections refused | BPF loaded but resolver not running | Check `systemctl status km-ebpf-enforcer` |
| DNS works but TCP fails | IP not in allowed_cidrs (stale or missing) | Check if domain was resolved before connect |
| Root can bypass (proxy mode) | iptables DNAT exempts uid 0 | Switch to `enforcement: "ebpf"` |
| Enforcer won't start | Kernel too old or cgroup v1 | Requires kernel 5.15+, cgroup v2 |
| Maps full | Too many unique IPs resolved | Increase map max_entries or tighten DNS allowlist |

---

## Diagrams

- **eBPF enforcement architecture:** [`docs/diagrams/ebpf-architecture.excalidraw`](diagrams/ebpf-architecture.excalidraw)
- **"both" mode detailed flow:** [`docs/diagrams/ebpf-both-mode.excalidraw`](diagrams/ebpf-both-mode.excalidraw)
- **General sandbox architecture:** [`docs/sandbox-architecture.excalidraw`](sandbox-architecture.excalidraw)
