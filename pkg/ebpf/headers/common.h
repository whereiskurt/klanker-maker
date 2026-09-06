/* SPDX-License-Identifier: GPL-2.0-only */
/* common.h — App-specific BPF types and volatile configuration constants.
 *
 * Included by bpf.c after the system BPF headers. Contains only application
 * types (event struct, trie key, action/layer constants) and volatile consts.
 */
#pragma once

#include <linux/types.h>

/* ── Ring-buffer event struct ──────────────────────────────────────── */
struct event {
    __u64 timestamp;   /* bpf_ktime_get_ns() */
    __u32 pid;         /* tgid from bpf_get_current_pid_tgid() >> 32 */
    __u32 src_ip;      /* network byte order */
    __u32 dst_ip;      /* network byte order */
    __u16 dst_port;    /* host byte order */
    __u8  action;      /* 0=deny, 1=allow, 2=redirect */
    __u8  layer;       /* 1=connect4, 2=sendmsg4, 3=egress_skb, 4=sockops */
    __u8  comm[16];    /* task name from bpf_get_current_comm() */
    __u8  _pad[7];     /* explicit padding to 64-byte alignment */
};

/* ── LPM_TRIE key for IPv4 CIDR lookups ───────────────────────────── */
struct ip4_trie_key {
    __u32 prefixlen; /* 0-32 */
    __u8  addr[4];   /* network byte order */
};

/* ── Action constants (must match types.go) ────────────────────────── */
#define ACTION_DENY     0
#define ACTION_ALLOW    1
#define ACTION_REDIRECT 2

/* ── Layer constants (must match types.go) ─────────────────────────── */
#define LAYER_CONNECT4   1
#define LAYER_SENDMSG4   2
#define LAYER_EGRESS_SKB 3
#define LAYER_SOCKOPS    4

/* ── Firewall mode constants (must match types.go) ─────────────────── */
#define MODE_LOG   0
#define MODE_ALLOW 1
#define MODE_BLOCK 2

/* ── Volatile constants — set at load time by the Go loader ────────── */
volatile const __u32 const_dns_proxy_port = 5353;
volatile const __u32 const_proxy_pid = 0;
volatile const __u32 const_http_proxy_pid = 0; /* HTTP proxy PID — exempt from BPF interception (gatekeeper mode) */
volatile const __u32 const_http_proxy_port = 3128;
volatile const __u32 const_https_proxy_port = 3129;
volatile const __u16 const_firewall_mode = MODE_LOG;
volatile const __u32 const_mitm_proxy_address = 0x0100007f; /* 127.0.0.1 in network byte order on LE */

/* ── Phase 135: the enforcement predicate ────────────────────────────
 * The programs attach at the ROOT cgroup, not the per-sandbox scope, because
 * no interactive session ever reached that scope. cgroup v2 permits a
 * migration only if the caller can write the cgroup.procs of the COMMON
 * ANCESTOR of the source and destination cgroups; for any interactive session
 * that ancestor is the root cgroup (root:root 0644), so km-sandbox-shell's
 * join failed EPERM every time and the error was swallowed by 2>/dev/null.
 *
 * Enforcement therefore selects on identity rather than placement:
 *
 *     enforced = (uid == const_sandbox_uid) || (cgroup_id == const_km_cgid)
 *
 * The uid clause covers interactive sessions wherever logind or the SSM agent
 * puts them. The cgroup clause keeps a root process that was DELIBERATELY
 * placed in the scope enforced, which is what preserves EBPF-NET-12 — with a
 * bare uid filter, sudo would become a way OUT of enforcement.
 *
 * Both defaults are sentinels meaning "unset", so a loader that fails to set
 * them enforces NOTHING. That disposition is load-bearing: these programs now
 * sit in the path of every packet on the box, and the failure to prefer is an
 * unenforced sandbox, never an unreachable instance.
 */
#define KM_UID_UNSET 0xFFFFFFFF
volatile const __u32 const_sandbox_uid = KM_UID_UNSET;
volatile const __u64 const_km_cgid = 0;
