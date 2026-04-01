# Phase 40 Research: eBPF Cgroup Network Enforcement

**Date:** 2026-03-31
**Status:** Pre-planning research complete
**Sources:** Web search + codebase exploration across 6 research agents

---

## Reference Implementation: lawrencegripper/ebpf-cgroup-firewall

**Repo:** https://github.com/lawrencegripper/ebpf-cgroup-firewall
**Stack:** Go 1.23 + cilium/ebpf v0.16 + bpf2go + miekg/dns + goproxy + containerd/cgroups/v3
**License:** MIT

This project implements a DNS-based domain allowlist firewall using eBPF cgroup hooks. It is the closest existing implementation to what Phase 40 needs.

### Architecture: Three BPF Programs (Not Two)

| Program | Section | Attach Type | Purpose |
|---------|---------|-------------|---------|
| `connect4` | `SEC("cgroup/connect4")` | `AttachCGroupInet4Connect` | Intercepts `connect()`, rewrites dest to proxy, stores original dest in maps, records PID via socket cookie |
| `cgroup_skb/egress` | `SEC("cgroup_skb/egress")` | `AttachCGroupInetEgress` | Packet-level IP allowlist — the actual firewall. Returns 0=drop, 1=allow |
| `sockops` | `SEC("sockops")` | `AttachCGroupSockOps` | On `BPF_SOCK_OPS_ACTIVE_ESTABLISHED_CB`, maps src_port → socket_cookie. Bridges the gap so userspace proxy can recover original destination. |

**Critical insight:** `connect4` alone cannot filter packets — it only intercepts the syscall. You NEED `cgroup_skb/egress` for actual packet dropping. And `sockops` is the glue that lets the transparent proxy recover the original destination from the source port.

### Seven BPF Maps

| Map | Type | Key → Value | Max Entries | Purpose |
|-----|------|------------|-------------|---------|
| `events` | RINGBUF | — | 16 MB (1 << 24) | Deny/allow events to userspace |
| `sock_client_to_original_ip` | HASH | u64 (socket_cookie) → __be32 (IP) | 256K | Proxy recovers real destination IP |
| `sock_client_to_original_port` | HASH | u64 (socket_cookie) → u16 (port) | 256K | Proxy recovers real destination port |
| `src_port_to_sock_client` | HASH | u16 (src_port) → u64 (socket_cookie) | 256K | sockops populates; proxy uses to find cookie from connection |
| `socket_pid_map` | HASH | u64 (socket_cookie) → u32 (PID) | 10K | connect4 populates for audit/per-PID policy |
| `firewall_allowed_ips_map` | HASH | u32 (IP) → u32 (IP) | 256K | Any-port allowlist (BPF egress checks) |
| `firewall_allowed_http_ips_map` | HASH | u32 (IP) → u32 (IP) | 256K | HTTP-proxy-only allowlist |

### DNS → IP Correlation Flow (Transaction ID Bridge)

The DNS transaction ID bridges BPF (which knows PID) and the DNS proxy (which knows domain):

1. Process calls `connect()` to any IP on port 53
2. BPF `connect4` rewrites dest to `127.0.0.1:$dns_proxy_port`, records PID via `bpf_get_socket_cookie()`
3. BPF `cgroup_skb/egress` reads first 2 bytes of DNS payload = transaction ID, emits `{txn_id, PID}` via ring buffer
4. Go ring buffer consumer stores `txn_id → PID` in `sync.Map`
5. DNS proxy receives query, looks up PID from transaction ID
6. If domain allowed: resolves, pushes resolved IPs into `firewall_allowed_ips_map`
7. Subsequent `connect()` to that IP passes BPF egress check

### Compile-Time Constants (volatile const pattern)

```c
volatile const __u32 const_dns_proxy_port;
volatile const __u32 const_proxy_pid;
volatile const __u32 const_http_proxy_port;
volatile const __u32 const_https_proxy_port;
volatile const __u16 const_firewall_mode;        // 0=log, 1=allow, 2=block
volatile const __u32 const_mitm_proxy_address;   // 127.0.0.1, or Docker bridge IP
```

Set from Go before loading:
```go
spec.Variables["const_dns_proxy_port"].Set(uint32(5353))
spec.Variables["const_proxy_pid"].Set(uint32(os.Getpid()))
```

### CGroup Management

Uses `containerd/cgroups/v3/cgroup2`:
```go
cgroupMan, err := cgroup2.NewManager(path, "/ebpf-cgroup-firewall", &cgroup2.Resources{})
```

Two modes:
- **Run mode:** Creates child cgroup, runs command in it via `SysProcAttr.UseCgroupFD`
- **Attach mode:** Attaches to existing cgroup (Docker: `/sys/fs/cgroup/system.slice/docker-{ID}.scope`)

---

## Current Enforcement Stack (What Phase 40 Replaces)

### iptables DNAT (EC2 only)

**File:** `pkg/compiler/userdata.go:456-486`

```bash
# IMDS exemption — MUST be first (-I inserts at top)
iptables -t nat -I OUTPUT -d 169.254.169.254 -j RETURN

# Root user exempt — SSM agent, systemd, AWS CLI run as root
iptables -t nat -A OUTPUT -m owner --uid-owner 0 -j RETURN

# DNS redirect to local proxy on :5353
iptables -t nat -A OUTPUT -p udp --dport 53 -m owner ! --uid-owner km-sidecar -j REDIRECT --to-ports 5353
iptables -t nat -A OUTPUT -p tcp --dport 53 -m owner ! --uid-owner km-sidecar -j REDIRECT --to-ports 5353

# HTTP/HTTPS redirect to proxy on :3128
iptables -t nat -A OUTPUT -p tcp --dport 80  -m owner ! --uid-owner km-sidecar -j REDIRECT --to-ports 3128
iptables -t nat -A OUTPUT -p tcp --dport 443 -m owner ! --uid-owner km-sidecar -j REDIRECT --to-ports 3128
```

**Weaknesses:**
- Root user exempt (can reach any endpoint)
- `iptables -F` flushes all rules if sandbox gains root
- km-sidecar UID exemption is another bypass vector
- No defense-in-depth: if DNAT bypassed, traffic goes direct

### DNS Proxy Sidecar

**File:** `sidecars/dns-proxy/dnsproxy/proxy.go:18-30`

```go
func IsAllowed(name string, suffixes []string) bool {
    name = strings.TrimSuffix(name, ".")
    name = strings.ToLower(name)
    for _, s := range suffixes {
        s = strings.ToLower(strings.TrimSuffix(s, "."))
        s = strings.TrimPrefix(s, ".")
        if name == s || strings.HasSuffix(name, "."+s) {
            return true
        }
    }
    return false
}
```

Allowed → forward to upstream `169.254.169.253` (VPC DNS). Denied → NXDOMAIN.

### HTTP Proxy Sidecar

**File:** `sidecars/http-proxy/httpproxy/proxy.go:117-136, 506-556`

- `IsHostAllowed()`: case-insensitive, supports suffix matching (`.amazonaws.com`)
- CONNECT handler: `goproxy.RejectConnect` for blocked hosts
- Bedrock/Anthropic MITM: intercepts responses for token metering
- GitHub repo filtering: extracts `owner/repo` from URL path

### Profile Flow: YAML → Compiler → Sidecar Config

```
spec.network.egress.allowedDNSSuffixes  → ALLOWED_SUFFIXES env var → DNS proxy
spec.network.egress.allowedHosts        → ALLOWED_HOSTS env var   → HTTP proxy
```

**EC2:** `pkg/compiler/userdata.go:688-695` — systemd unit files for sidecars
**ECS:** `pkg/compiler/service_hcl.go:687-695` — container env vars in task definition
**Docker:** `pkg/compiler/compose.go:287-295` — compose env vars, HTTP_PROXY/HTTPS_PROXY (no iptables)

### Security Groups (L3/L4 baseline)

**File:** `pkg/compiler/security.go:14-31`

Only TCP 443 and UDP 53 allowed. No TCP 80. Everything else dropped before reaching application.

### Critical Gap: No Sandbox Cgroup

Currently **no sandbox-level cgroup** exists. All processes run in default system cgroup. Phase 40 must:
1. Create sandbox-specific cgroup (systemd slice or `cgroup2.NewManager`)
2. Place sandbox user's processes in it
3. Attach BPF programs to that cgroup
4. Pin for persistence

The sandbox shell runs as dedicated `sandbox` user (`useradd -m -s /bin/bash sandbox` in user-data:261).

---

## cilium/ebpf Patterns (From Actual Source Code)

### bpf2go Directive

```go
//go:generate go tool bpf2go -tags linux -type event bpf bpf.c -- -I../headers
```

The `-type event` flag generates a Go struct matching the C `struct event` for type-safe ring buffer deserialization.

### Cgroup Path Detection

```go
func detectCgroupPath() (string, error) {
    f, _ := os.Open("/proc/mounts")
    scanner := bufio.NewScanner(f)
    for scanner.Scan() {
        fields := strings.Split(scanner.Text(), " ")
        if len(fields) >= 3 && fields[2] == "cgroup2" {
            return fields[1], nil  // typically /sys/fs/cgroup
        }
    }
    return "", errors.New("cgroup2 not mounted")
}
```

Source: `github.com/cilium/ebpf/blob/main/examples/cgroup_skb/main.go`

### Cgroup Attachment

```go
l, err := link.AttachCgroup(link.CgroupOptions{
    Path:    cgroupPath,
    Attach:  ebpf.AttachCGroupInet4Connect,
    Program: objs.Connect4,
})
defer l.Close()  // detaches program
```

Source: `github.com/cilium/ebpf/blob/main/examples/cgroup_skb/main.go`

### Ring Buffer (C side)

```c
struct { __uint(type, BPF_MAP_TYPE_RINGBUF); __uint(max_entries, 1 << 24); } events SEC(".maps");

struct event { u32 pid; u8 comm[16]; };

// Zero-copy: reserve slot, write, submit
struct event *e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
if (!e) return 0;
e->pid = bpf_get_current_pid_tgid() >> 32;
bpf_ringbuf_submit(e, 0);
```

Source: `github.com/cilium/ebpf/blob/main/examples/ringbuffer/ringbuffer.c`

### Ring Buffer (Go side)

```go
rd, _ := ringbuf.NewReader(objs.Events)
for {
    record, err := rd.Read()  // blocking
    if errors.Is(err, ringbuf.ErrClosed) { return }
    var event bpfEvent
    binary.Read(bytes.NewBuffer(record.RawSample), binary.LittleEndian, &event)
}
```

Source: `github.com/cilium/ebpf/blob/main/examples/ringbuffer/main.go`

### Pinning for Persistence

```go
pinPath := "/sys/fs/bpf/km/" + sandboxID
os.MkdirAll(pinPath, os.ModePerm)

loadBpfObjects(&objs, &ebpf.CollectionOptions{
    Maps: ebpf.MapOptions{PinPath: pinPath},
})
// Maps survive process exit. Reuse on next load if pin exists.

// To pin links (requires kernel 5.7+):
l.Pin("/sys/fs/bpf/km/" + sandboxID + "/connect4_link")
// To recover:
l, _ = link.LoadPinnedLink("/sys/fs/bpf/km/" + sandboxID + "/connect4_link", nil)
```

Source: `github.com/cilium/ebpf/blob/main/examples/kprobepin/main.go`

### LPM Trie Key Structure

```c
struct ip4_trie_key {
    __u32 prefixlen;   // 0-32 for IPv4
    __u8 addr[4];      // network byte order
};

struct {
    __uint(type, BPF_MAP_TYPE_LPM_TRIE);
    __uint(max_entries, 65535);
    __type(key, struct ip4_trie_key);
    __type(value, __u32);
    __uint(map_flags, BPF_F_NO_PREALLOC);  // MANDATORY
} allowed_cidrs SEC(".maps");
```

Go side:
```go
type lpmKey struct {
    PrefixLen uint32
    Addr      [4]byte
}
key := lpmKey{PrefixLen: 24, Addr: [4]byte{10, 0, 0, 0}}  // 10.0.0.0/24
objs.AllowedCidrs.Put(key, uint32(1))
```

Source: `github.com/cilium/ebpf/discussions/945`, kernel docs `BPF_MAP_TYPE_LPM_TRIE`

### connect4 Context Struct

```c
struct bpf_sock_addr {
    __u32 user_ip4;    // dest IP, readable+writable, network byte order
    __u32 user_port;   // dest port, readable+writable, network byte order
    __u32 family;      // AF_INET, read-only
    __u32 type;        // SOCK_STREAM/SOCK_DGRAM, read-only
    __u32 protocol;    // IPPROTO_TCP/UDP, read-only
    struct bpf_sock *sk;
};
// Return 1 = allow, 0 = reject (EPERM)
// Writing user_ip4/user_port = transparent DNAT
```

Source: `docs.ebpf.io/linux/program-type/BPF_PROG_TYPE_CGROUP_SOCK_ADDR/`

### Section Name → Attach Type Mapping

```
"cgroup/connect4"    → BPF_CGROUP_INET4_CONNECT
"cgroup/connect6"    → BPF_CGROUP_INET6_CONNECT
"cgroup/sendmsg4"    → BPF_CGROUP_UDP4_SENDMSG
"cgroup/sendmsg6"    → BPF_CGROUP_UDP6_SENDMSG
"cgroup_skb/egress"  → BPF_CGROUP_INET_EGRESS
"cgroup_skb/ingress" → BPF_CGROUP_INET_INGRESS
"sockops"            → BPF_CGROUP_SOCK_OPS
```

Source: `github.com/cilium/ebpf/blob/main/elf_sections.go`

---

## Implementation Gotchas

### 1. Link References Must Stay Alive
If Go GC collects the `link.Link`, the BPF program detaches silently. Store in a long-lived struct:
```go
type Enforcer struct {
    ConnectLink *link.Link  // DO NOT let this get GC'd
    EgressLink  *link.Link
    SockOpsLink *link.Link
}
```

### 2. IPv6 Must Be Blocked
The reference blocks all IPv6 (AAAA DNS refused, AF_INET6 packets dropped). Do the same — simplifies BPF code significantly.

### 3. IP Byte Ordering
BPF uses network byte order (big-endian). The reference has a bug using `binary.LittleEndian.Uint32()`. We should use `binary.BigEndian` consistently, and `bpf_htonl`/`bpf_ntohl` in C.

### 4. Map Cleanup / TTL Eviction
The reference has no map cleanup — IPs accumulate unboundedly. For long-running sandboxes, implement TTL-based eviction matching DNS TTL values.

### 5. Three Programs Are Required
`connect4` intercepts syscalls but cannot drop packets. `cgroup_skb/egress` drops packets but doesn't know the PID. `sockops` bridges src_port to socket_cookie so the proxy can recover original destination. All three are needed for the full enforcement + proxy pattern.

### 6. rlimit.RemoveMemlock()
Required before loading BPF programs on kernels < 5.11. AL2023 kernel 6.1 uses memcg accounting, but call it anyway for safety.

### 7. Proxy PID Exemption
The BPF connect4 program must exempt its own proxy's PID from redirection (or the proxy's outbound connections get redirected in a loop). Set via `volatile const __u32 const_proxy_pid`.

### 8. IMDS Exemption
169.254.169.254 (EC2 metadata) must be exempt from filtering. SSM agent and credential refresh depend on it.

### 9. Pin Path Lifecycle
- `km create` → load BPF, attach to cgroup, pin to `/sys/fs/bpf/km/{sandbox-id}/`
- CLI exits → enforcement persists (pinned)
- `km destroy` → `os.RemoveAll("/sys/fs/bpf/km/{sandbox-id}/")` to unpin, kernel cleans up
- `km` restarts → `LoadPinnedLink()` + `LoadPinnedMap()` to recover handles

### 10. TC SNI Filtering Caveat
Chrome sends ClientHello packets >1500 bytes that get TCP-segmented. SNI may land in the 2nd segment. TC programs can't do TCP reassembly. SNI filtering is best-effort defense-in-depth only.

Source: https://community.ipfire.org/t/ebpf-xdp-monitor-and-block-tls-ssl-encrypted-website-access/13002

---

## Suggested Plan Breakdown

### Plan 40-01: Scaffold + BPF Programs
- `pkg/ebpf/` package with `bpf2go` pipeline
- `headers/common.h` + `bpf_helpers.h`
- Three BPF C programs: connect4, cgroup_skb/egress, sockops
- Seven BPF maps
- `volatile const` pattern for runtime config
- Ring buffer event struct
- Makefile `go generate` target
- Verify compilation on AL2023 kernel 6.1

### Plan 40-02: Go Loader + Cgroup Management
- `AttachEnforcerToCgroup()` function
- Cgroup creation for sandbox (systemd slice or cgroup2.NewManager)
- Link attachment with GC-safe reference storage
- `spec.Variables` population from profile config
- IMDS + proxy PID exemption logic
- Pin/unpin lifecycle (bpffs)

### Plan 40-03: DNS Resolver Daemon
- Userspace DNS proxy (miekg/dns based)
- DNS transaction ID → PID correlation via ring buffer
- Domain allowlist with wildcard suffix matching
- Resolved IP → BPF map population
- NXDOMAIN for denied domains
- IPv6 AAAA blocking

### Plan 40-04: Compiler + Profile Integration
- `spec.network.enforcement` schema field (`proxy | ebpf | both`)
- JSON schema + semantic validation
- Compiler emits eBPF setup in EC2 user-data
- Docker substrate cgroup attachment
- Backwards-compatible `proxy` default

### Plan 40-05: Verification + Root Bypass Testing
- Root-in-sandbox cannot bypass enforcement
- `iptables -F` is irrelevant with eBPF active
- Hardcoded IP blocked by cgroup_skb/egress
- DNS allowlist populates BPF map correctly
- Pin persistence across CLI exit
- Ring buffer audit events emitted

---

## Key Reference URLs

| Resource | URL |
|----------|-----|
| **lawrencegripper/ebpf-cgroup-firewall** | https://github.com/lawrencegripper/ebpf-cgroup-firewall |
| **cilium/ebpf examples** | https://github.com/cilium/ebpf/tree/main/examples |
| **cilium/ebpf cgroup_skb** | https://github.com/cilium/ebpf/blob/main/examples/cgroup_skb/main.go |
| **cilium/ebpf ringbuffer** | https://github.com/cilium/ebpf/blob/main/examples/ringbuffer/main.go |
| **cilium/ebpf kprobepin** | https://github.com/cilium/ebpf/blob/main/examples/kprobepin/main.go |
| **BPF_PROG_TYPE_CGROUP_SOCK_ADDR** | https://docs.ebpf.io/linux/program-type/BPF_PROG_TYPE_CGROUP_SOCK_ADDR/ |
| **BPF_MAP_TYPE_LPM_TRIE** | https://docs.ebpf.io/linux/map-type/BPF_MAP_TYPE_LPM_TRIE/ |
| **Cloudflare LPM trie deep dive** | https://blog.cloudflare.com/a-deep-dive-into-bpf-lpm-trie-performance-and-optimization/ |
| **Cloudflare connect4 example** | https://github.com/cloudflare/cloudflare-blog/tree/master/2022-02-connectx/ebpf_connect4 |
| **iximiuz transparent egress proxy** | https://labs.iximiuz.com/tutorials/ebpf-envoy-egress-dc77ccd7 |
| **CiliumCon FQDN DNS parsing** | https://tldrecap.tech/posts/2025/ciliumcon-na/cilium-ebpf-dns-parsing-fqdn-policies/ |
| **Cilium FQDN deep dive** | https://hackmd.io/@Echo-Live/B1UOe_yr5 |
| **nikofil eBPF firewall** | https://nfil.dev/coding/security/ebpf-firewall-with-cgroups/ |
| **IPFire TLS SNI segmentation caveat** | https://community.ipfire.org/t/ebpf-xdp-monitor-and-block-tls-ssl-encrypted-website-access/13002 |
| **Pixie TLS tracing** | https://blog.px.dev/ebpf-tls-tracing-past-present-future/ |
| **ecapture** | https://github.com/gojue/ecapture |
| **Coroot rustls instrumentation** | https://coroot.com/blog/instrumenting-rust-tls-with-ebpf |
| **Speedscale Go TLS eBPF** | https://speedscale.com/blog/ebpf-go-design-notes-1/ |
| **ebpf-go getting started** | https://ebpf-go.dev/guides/getting-started/ |
| **ebpf-go object lifecycle** | https://ebpf-go.dev/concepts/object-lifecycle/ |
