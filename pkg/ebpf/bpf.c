// SPDX-License-Identifier: GPL-2.0-only
/*
 * bpf.c — Kernel-side eBPF network enforcement programs for klankrmkr.
 *
 * Programs:
 *   1. cgroup/connect4    — Intercepts TCP connect(), enforces CIDR allowlist,
 *                           redirects to MITM proxy when needed (EBPF-NET-02, -03)
 *   2. cgroup/sendmsg4    — Intercepts UDP sendmsg(), redirects port 53 to
 *                           local DNS proxy (EBPF-NET-04)
 *   3. sockops            — Maps local_port → socket_cookie for transparent
 *                           proxy origin lookups
 *   4. cgroup_skb/egress  — Packet-level defense-in-depth: drops egress to
 *                           IPs not in CIDR allowlist in block mode (EBPF-NET-06)
 *
 * All volatile constants are set by the Go loader at program load time
 * via cilium/ebpf CollectionSpec.RewriteConstants().
 *
 * Build: go generate ./pkg/ebpf/ (requires clang + Linux BPF headers)
 */

#include <linux/bpf.h>
#include <linux/in.h>
#include <linux/ip.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#include "headers/common.h"

/* ════════════════════════════════════════════════════════════════════
 * BPF MAP DEFINITIONS
 * All maps are in SEC(".maps") so bpf2go discovers them automatically.
 * ════════════════════════════════════════════════════════════════════ */

/* events: ring buffer for structured deny/redirect events streamed to userspace.
 * 16 MB = 1<<24. Ring buffer is lock-free, high-throughput event delivery. */
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 1 << 24); /* 16 MB */
} events SEC(".maps");

/* allowed_cidrs: LPM_TRIE of permitted destination IPv4 CIDRs.
 * Key: struct ip4_trie_key (prefixlen + 4-byte addr in network byte order).
 * Value: u32 (unused, conventionally 1).
 * BPF_F_NO_PREALLOC is MANDATORY for LPM_TRIE maps. */
struct {
    __uint(type, BPF_MAP_TYPE_LPM_TRIE);
    __type(key, struct ip4_trie_key);
    __type(value, __u32);
    __uint(max_entries, 65535);
    __uint(map_flags, BPF_F_NO_PREALLOC);
} allowed_cidrs SEC(".maps");

/* http_proxy_ips: HASH of destination IPs that need L7 proxy inspection.
 * Key: u32 destination IP (network byte order).
 * Value: u32 (unused, conventionally 1).
 * Populated by Go loader from the profile's AllowedHosts that have HTTPS/HTTP. */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u32);
    __type(value, __u32);
    __uint(max_entries, 262144); /* 256K */
} http_proxy_ips SEC(".maps");

/* sock_to_original_ip: HASH keyed by socket cookie → original dest IP.
 * Written by connect4/sendmsg4 before rewriting ctx->user_ip4.
 * Read by the userspace MITM proxy via SO_COOKIE to determine real target. */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);   /* socket cookie */
    __type(value, __u32); /* original dest IP in network byte order */
    __uint(max_entries, 262144);
} sock_to_original_ip SEC(".maps");

/* sock_to_original_port: HASH keyed by socket cookie → original dest port.
 * Stored in host byte order (matching how the proxy reads it). */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);   /* socket cookie */
    __type(value, __u16); /* original dest port in host byte order */
    __uint(max_entries, 262144);
} sock_to_original_port SEC(".maps");

/* src_port_to_sock: HASH keyed by local TCP source port → socket cookie.
 * Written by sockops on ACTIVE_ESTABLISHED_CB.
 * Allows the proxy to look up socket metadata by the source port it sees
 * on the accepted connection. */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u16);   /* local source port in host byte order */
    __type(value, __u64); /* socket cookie */
    __uint(max_entries, 262144);
} src_port_to_sock SEC(".maps");

/* socket_pid_map: HASH keyed by socket cookie → PID that created the socket.
 * Written by connect4 on first use. Allows userspace to correlate sockets to
 * the sandbox process tree for audit logging. */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __type(key, __u64);   /* socket cookie */
    __type(value, __u32); /* PID (tgid) */
    __uint(max_entries, 10000);
} socket_pid_map SEC(".maps");

/* ════════════════════════════════════════════════════════════════════
 * HELPER: emit_event
 * Emits a structured event to the ring buffer.
 * Callers set src_ip, dst_ip, dst_port, action, layer before calling.
 * ════════════════════════════════════════════════════════════════════ */
static __always_inline void emit_event(__u32 src_ip, __u32 dst_ip,
                                        __u16 dst_port, __u8 action,
                                        __u8 layer)
{
    struct event *e = bpf_ringbuf_reserve(&events, sizeof(struct event), 0);
    if (!e)
        return;

    e->timestamp = bpf_ktime_get_ns();
    e->pid       = (__u32)(bpf_get_current_pid_tgid() >> 32);
    e->src_ip    = src_ip;
    e->dst_ip    = dst_ip;
    e->dst_port  = dst_port;
    e->action    = action;
    e->layer     = layer;
    bpf_get_current_comm(e->comm, sizeof(e->comm));

    bpf_ringbuf_submit(e, 0);
}

/* ════════════════════════════════════════════════════════════════════
 * PROGRAM 1: cgroup/connect4
 *
 * Intercepts every TCP connect() syscall inside the cgroup.
 * Logic:
 *   1. Exempt the sidecar proxy process itself (by PID).
 *   2. Exempt IMDS (169.254.169.254) and localhost (127.0.0.0/8).
 *   3. Record socket → PID mapping in socket_pid_map.
 *   4. Look up dest IP in allowed_cidrs LPM_TRIE.
 *   5. On deny: in block mode return 0 (EPERM); in log/allow mode emit event
 *      and return 1.
 *   6. On allow: check if IP needs L7 inspection (http_proxy_ips).
 *      If yes, stash original IP/port and rewrite ctx to MITM proxy.
 *   7. Return 1 (allow connection to proceed).
 * ════════════════════════════════════════════════════════════════════ */
SEC("cgroup/connect4")
int connect4(struct bpf_sock_addr *ctx)
{
    __u32 pid = (__u32)(bpf_get_current_pid_tgid() >> 32);

    /* 1. Exempt proxy process */
    if (const_proxy_pid != 0 && pid == const_proxy_pid)
        return 1;

    __u32 dst_ip   = ctx->user_ip4; /* network byte order */
    /* user_port is in network byte order, high 16 bits contain the port */
    __u16 dst_port = bpf_ntohs((__u16)(ctx->user_port >> 16));

    /* 2a. Exempt IMDS (169.254.169.254 = 0xfea9fea9 in network byte order) */
    if (dst_ip == bpf_htonl(0xa9fea9fe)) /* 169.254.169.254 */
        return 1;

    /* 2b. Exempt localhost 127.0.0.0/8 (0x7f000000 in host byte order) */
    if ((bpf_ntohl(dst_ip) & 0xff000000) == 0x7f000000)
        return 1;

    /* 3. Record PID → socket cookie mapping */
    __u64 cookie = bpf_get_socket_cookie(ctx);
    bpf_map_update_elem(&socket_pid_map, &cookie, &pid, 0 /* BPF_ANY */);

    /* 4. LPM_TRIE lookup */
    struct ip4_trie_key trie_key = {};
    trie_key.prefixlen = 32;
    trie_key.addr[0] = ((__u8 *)&dst_ip)[0];
    trie_key.addr[1] = ((__u8 *)&dst_ip)[1];
    trie_key.addr[2] = ((__u8 *)&dst_ip)[2];
    trie_key.addr[3] = ((__u8 *)&dst_ip)[3];

    void *match = bpf_map_lookup_elem(&allowed_cidrs, &trie_key);

    /* 5. Handle deny */
    if (!match) {
        emit_event(0, dst_ip, dst_port, ACTION_DENY, LAYER_CONNECT4);
        if (const_firewall_mode == MODE_BLOCK)
            return 0; /* EPERM */
        return 1;     /* log-only or allow mode */
    }

    /* 6. Check for L7 proxy redirect */
    void *proxy_match = bpf_map_lookup_elem(&http_proxy_ips, &dst_ip);
    if (proxy_match) {
        /* Stash original destination */
        bpf_map_update_elem(&sock_to_original_ip, &cookie, &dst_ip, 0);
        bpf_map_update_elem(&sock_to_original_port, &cookie, &dst_port, 0);

        /* Rewrite destination to MITM proxy address */
        ctx->user_ip4 = const_mitm_proxy_address;

        /* Select proxy port based on original destination port */
        __u16 proxy_port;
        if (dst_port == 443)
            proxy_port = (__u16)const_https_proxy_port;
        else
            proxy_port = (__u16)const_http_proxy_port;

        ctx->user_port = bpf_htons(proxy_port) << 16;

        emit_event(0, dst_ip, dst_port, ACTION_REDIRECT, LAYER_CONNECT4);
    }

    /* 7. Allow */
    return 1;
}

/* ════════════════════════════════════════════════════════════════════
 * PROGRAM 2: cgroup/sendmsg4
 *
 * Intercepts UDP sendmsg() calls to redirect DNS (port 53) to the
 * local DNS proxy listening on const_dns_proxy_port.
 * Logic:
 *   1. Exempt the proxy process.
 *   2. Only act on port 53 (DNS).
 *   3. Stash original dest IP/port, rewrite to 127.0.0.1:dns_proxy_port.
 * ════════════════════════════════════════════════════════════════════ */
SEC("cgroup/sendmsg4")
int sendmsg4(struct bpf_sock_addr *ctx)
{
    __u32 pid = (__u32)(bpf_get_current_pid_tgid() >> 32);

    /* 1. Exempt proxy process */
    if (const_proxy_pid != 0 && pid == const_proxy_pid)
        return 1;

    /* 2. Only intercept DNS (port 53) */
    __u16 dst_port = bpf_ntohs((__u16)(ctx->user_port >> 16));
    if (dst_port != 53)
        return 1;

    __u32 dst_ip = ctx->user_ip4;

    /* 3. Stash original destination */
    __u64 cookie = bpf_get_socket_cookie(ctx);
    bpf_map_update_elem(&sock_to_original_ip, &cookie, &dst_ip, 0);
    bpf_map_update_elem(&sock_to_original_port, &cookie, &dst_port, 0);

    /* Rewrite to 127.0.0.1 and configured DNS proxy port */
    ctx->user_ip4  = bpf_htonl(0x7f000001); /* 127.0.0.1 */
    ctx->user_port = bpf_htons((__u16)const_dns_proxy_port) << 16;

    emit_event(0, dst_ip, dst_port, ACTION_REDIRECT, LAYER_SENDMSG4);
    return 1;
}

/* ════════════════════════════════════════════════════════════════════
 * PROGRAM 3: sockops
 *
 * On ACTIVE_ESTABLISHED_CB (TCP connection established as initiator),
 * maps local_port → socket_cookie in src_port_to_sock.
 *
 * The MITM proxy accepts the redirected connection and reads the source port
 * of its peer (the sandboxed process). It can then look up src_port_to_sock
 * to get the cookie, then look up sock_to_original_ip / sock_to_original_port
 * to determine the real intended destination.
 * ════════════════════════════════════════════════════════════════════ */
SEC("sockops")
int bpf_sockops(struct bpf_sock_ops *skops)
{
    if (skops->op != BPF_SOCK_OPS_ACTIVE_ESTABLISHED_CB)
        return 1;

    __u16 local_port = (__u16)skops->local_port;
    __u64 cookie = bpf_get_socket_cookie(skops);
    bpf_map_update_elem(&src_port_to_sock, &local_port, &cookie, 0);

    return 1;
}

/* ════════════════════════════════════════════════════════════════════
 * PROGRAM 4: cgroup_skb/egress
 *
 * Packet-level defense-in-depth. Inspects every outbound IP packet.
 * In block mode (const_firewall_mode == 2), drops packets to IPs not
 * in allowed_cidrs. This catches raw socket / bypass attempts that
 * evade the connect4 hook (e.g., root processes using raw sockets).
 *
 * Returns: 1 (pass) or 0 (drop).
 * ════════════════════════════════════════════════════════════════════ */
SEC("cgroup_skb/egress")
int egress_filter(struct __sk_buff *skb)
{
    /* Parse IP header — offset 0 for cgroup_skb (no ethernet header) */
    struct iphdr iph = {};
    if (bpf_skb_load_bytes(skb, 0, &iph, sizeof(iph)) < 0)
        return 1; /* parse failure: pass (don't break non-IP traffic) */

    /* Only process IPv4 (version == 4) */
    if (iph.version != 4)
        return 1;

    __u32 dst_ip = iph.daddr; /* network byte order */

    /* Exempt IMDS (169.254.169.254) */
    if (dst_ip == bpf_htonl(0xa9fea9fe))
        return 1;

    /* Exempt localhost 127.0.0.0/8 */
    if ((bpf_ntohl(dst_ip) & 0xff000000) == 0x7f000000)
        return 1;

    /* LPM_TRIE lookup */
    struct ip4_trie_key trie_key = {};
    trie_key.prefixlen = 32;
    trie_key.addr[0] = ((__u8 *)&dst_ip)[0];
    trie_key.addr[1] = ((__u8 *)&dst_ip)[1];
    trie_key.addr[2] = ((__u8 *)&dst_ip)[2];
    trie_key.addr[3] = ((__u8 *)&dst_ip)[3];

    void *match = bpf_map_lookup_elem(&allowed_cidrs, &trie_key);
    if (!match) {
        /* Derive destination port from IP payload for the event */
        __u16 dst_port = 0;
        __u8 protocol  = iph.protocol;
        if (protocol == 6 || protocol == 17) { /* TCP=6, UDP=17 */
            /* TCP/UDP: dest port is at offset ihl*4 + 2 */
            __u8 ihl_bytes = (iph.ihl & 0x0f) * 4;
            __u16 port_be = 0;
            bpf_skb_load_bytes(skb, ihl_bytes + 2, &port_be, sizeof(port_be));
            dst_port = bpf_ntohs(port_be);
        }

        emit_event(iph.saddr, dst_ip, dst_port, ACTION_DENY, LAYER_EGRESS_SKB);

        if (const_firewall_mode == MODE_BLOCK)
            return 0; /* drop packet */
    }

    return 1; /* pass */
}

/* ── License: required by BPF verifier for GPL-licensed helpers ──── */
char _license[] SEC("license") = "GPL";
