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
volatile const __u32 const_http_proxy_port = 3128;
volatile const __u32 const_https_proxy_port = 3129;
volatile const __u16 const_firewall_mode = MODE_LOG;
volatile const __u32 const_mitm_proxy_address = 0x0100007f; /* 127.0.0.1 in network byte order on LE */
