// SPDX-License-Identifier: GPL-2.0-only
/*
 * ssl_common.h — Shared BPF definitions for TLS uprobe observability.
 *
 * Defines the ring buffer event struct, connection correlation maps,
 * and constants shared between openssl.bpf.c and connect.bpf.c.
 */

#ifndef __SSL_COMMON_H__
#define __SSL_COMMON_H__

/* Define pt_regs before bpf includes — required for uprobe PT_REGS macros.
 * bpf_tracing.h needs the full struct definition, not just a forward decl.
 * Register names must match what bpf_tracing.h expects (rdi, rsi, rdx, etc).
 * This matches the x86_64 pt_regs layout from arch/x86/include/uapi/asm/ptrace.h */
struct pt_regs {
    unsigned long r15;
    unsigned long r14;
    unsigned long r13;
    unsigned long r12;
    unsigned long rbp;
    unsigned long rbx;
    unsigned long r11;
    unsigned long r10;
    unsigned long r9;
    unsigned long r8;
    unsigned long rax;
    unsigned long rcx;
    unsigned long rdx;
    unsigned long rsi;
    unsigned long rdi;
    unsigned long orig_rax;
    unsigned long rip;
    unsigned long cs;
    unsigned long eflags;
    unsigned long rsp;
    unsigned long ss;
};

#include <linux/bpf.h>
#include <linux/in.h>
#include <bpf/bpf_helpers.h>

/* ════════════════════════════════════════════════════════════════════
 * CONSTANTS
 * ════════════════════════════════════════════════════════════════════ */

#define MAX_PAYLOAD_LEN  16384  /* 16KB — TLS max fragment size */
#define MAX_CONN_ENTRIES 65536

/* Library type constants — which TLS library produced this event */
#define LIB_OPENSSL 1
#define LIB_GNUTLS  2
#define LIB_NSS     3
#define LIB_GO      4
#define LIB_RUSTLS  5

/* Direction constants */
#define DIR_WRITE 0
#define DIR_READ  1

/* ════════════════════════════════════════════════════════════════════
 * STRUCTS
 * ════════════════════════════════════════════════════════════════════ */

/* Ring buffer event — emitted for each SSL_write/SSL_read call */
struct ssl_event {
    __u64 timestamp_ns;
    __u32 pid;
    __u32 tid;
    __u32 fd;
    __u32 remote_ip;
    __u16 remote_port;
    __u8  direction;
    __u8  library_type;
    __u32 payload_len;
    __u8  payload[MAX_PAYLOAD_LEN];
};

/* Connection info for fd-to-endpoint correlation */
struct conn_info {
    __u32 remote_ip;
    __u16 remote_port;
    __u16 local_port;
};

/* Hash map key: identifies a socket by process + file descriptor */
struct pid_fd_key {
    __u32 pid;
    __u32 fd;
};

/* Stash SSL_read buf pointer between entry and return probes */
struct ssl_read_args {
    __u64 buf_ptr;
    __u32 fd;
};

/* ════════════════════════════════════════════════════════════════════
 * BPF MAPS
 * ════════════════════════════════════════════════════════════════════ */

/* Ring buffer for TLS events — 16MB */
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 16 * 1024 * 1024);
} tls_events SEC(".maps");

/* Connection correlation: pid+fd -> remote endpoint */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, MAX_CONN_ENTRIES);
    __type(key, struct pid_fd_key);
    __type(value, struct conn_info);
} conn_map SEC(".maps");

/* Per-library enable/disable toggle */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 16);
    __type(key, __u8);
    __type(value, __u8);
} lib_enabled SEC(".maps");

/* SSL_read args stash: pid_tgid -> buf pointer + fd */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 4096);
    __type(key, __u64);
    __type(value, struct ssl_read_args);
} ssl_read_args_map SEC(".maps");

#endif /* __SSL_COMMON_H__ */
