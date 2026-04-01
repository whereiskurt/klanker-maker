/* SPDX-License-Identifier: GPL-2.0-only */
/* bpf_helpers.h — Minimal BPF helper function declarations.
 *
 * Only declares the helpers actually used by bpf.c. This avoids pulling in
 * the full libbpf bpf_helpers.h which may not be available in all toolchains.
 * All helpers follow the Linux BPF helper ABI.
 */
#pragma once

/* ── Section macro ─────────────────────────────────────────────────── */
#ifndef SEC
#define SEC(name) __attribute__((section(name), used))
#endif

/* ── Compiler hints ────────────────────────────────────────────────── */
#ifndef __always_inline
#define __always_inline inline __attribute__((always_inline))
#endif

#ifndef likely
#define likely(x)   __builtin_expect(!!(x), 1)
#endif
#ifndef unlikely
#define unlikely(x) __builtin_expect(!!(x), 0)
#endif

/* ── Integer types (from linux/types.h / vmlinux) ──────────────────── */
typedef unsigned char  __u8;
typedef unsigned short __u16;
typedef unsigned int   __u32;
typedef unsigned long long __u64;

/* ── BPF map type constants ────────────────────────────────────────── */
#define BPF_MAP_TYPE_HASH       1
#define BPF_MAP_TYPE_LPM_TRIE   11
#define BPF_MAP_TYPE_RINGBUF    27

/* ── BPF map flag ──────────────────────────────────────────────────── */
/* BPF_F_NO_PREALLOC: MANDATORY for LPM_TRIE maps */
#define BPF_F_NO_PREALLOC (1U << 0)

/* ── BPF_FUNC numbers ──────────────────────────────────────────────── */
/* Helper prototypes — the BPF verifier resolves these to kernel functions */

/* bpf_map_lookup_elem: returns pointer to value, or NULL if not found */
static void *(*bpf_map_lookup_elem)(void *map, const void *key) =
    (void *) 1;

/* bpf_map_update_elem: inserts or updates map entry */
static int (*bpf_map_update_elem)(void *map, const void *key,
                                   const void *value, __u64 flags) =
    (void *) 2;

/* bpf_map_delete_elem */
static int (*bpf_map_delete_elem)(void *map, const void *key) =
    (void *) 3;

/* bpf_probe_read_kernel: read from kernel memory */
static int (*bpf_probe_read_kernel)(void *dst, __u32 size,
                                     const void *src) =
    (void *) 113;

/* bpf_ktime_get_ns: monotonic clock nanoseconds */
static __u64 (*bpf_ktime_get_ns)(void) = (void *) 5;

/* bpf_get_current_pid_tgid: returns (tgid << 32 | pid) */
static __u64 (*bpf_get_current_pid_tgid)(void) = (void *) 14;

/* bpf_get_current_comm: writes process name into buf[size] */
static int (*bpf_get_current_comm)(void *buf, __u32 buf_size) =
    (void *) 16;

/* bpf_get_socket_cookie: returns per-socket unique 64-bit cookie */
static __u64 (*bpf_get_socket_cookie)(void *ctx) = (void *) 46;

/* bpf_ringbuf_reserve: reserves space in ring buffer map */
static void *(*bpf_ringbuf_reserve)(void *ringbuf, __u64 size,
                                     __u64 flags) =
    (void *) 160;

/* bpf_ringbuf_submit: submits reserved ring buffer entry */
static void (*bpf_ringbuf_submit)(void *data, __u64 flags) =
    (void *) 161;

/* bpf_ringbuf_discard: discards reserved ring buffer entry */
static void (*bpf_ringbuf_discard)(void *data, __u64 flags) =
    (void *) 162;

/* bpf_skb_load_bytes: copies bytes from sk_buff to dest */
static int (*bpf_skb_load_bytes)(const void *skb, __u32 offset,
                                  void *to, __u32 len) =
    (void *) 26;

/* ── BPF sockops op codes ───────────────────────────────────────────── */
#define BPF_SOCK_OPS_ACTIVE_ESTABLISHED_CB  1

/* ── BTF-typed map attribute macros (required by bpf2go map definitions) ── */
/* These match the libbpf convention used in bpf.c and sni.c BTF map structs. */
#ifndef __uint
#define __uint(name, val) int (*name)[val]
#endif
#ifndef __type
#define __type(name, val) typeof(val) *name
#endif
#ifndef __array
#define __array(name, val) typeof(val) *name[]
#endif

/* ── BPF map definition macro ──────────────────────────────────────── */
/* Used to define BTF-typed maps compatible with bpf2go */
#define BPF_MAP_DEF(name) struct { \
    int (*type)[0]; \
    int (*key)[0]; \
    int (*value)[0]; \
    __u32 max_entries; \
    __u32 map_flags; \
} name SEC(".maps")
