/* SPDX-License-Identifier: GPL-2.0-only */
/* vmlinux_extra.h — kernel type declarations exec.c needs that neither the
 * real system libbpf headers nor pkg/ebpf/headers/vmlinux.h provide.
 *
 * pkg/ebpf/headers/vmlinux.h is a stub scoped to the cgroup-BPF enforcer's
 * own needs (bpf_sock_addr, __sk_buff, bpf_sock_ops, iphdr) — it has no
 * task_struct or tracepoint context structs, and worse, including it
 * alongside the real <linux/bpf.h> (required here for BPF_MAP_TYPE_* /
 * BPF_ANY) is a hard compile error: both declare struct __sk_buff,
 * bpf_sock_addr, and bpf_sock_ops. So this package does not include it —
 * see gen.go for why this package is deliberately split out with its own
 * object rather than sharing pkg/ebpf's.
 *
 * struct task_struct is read via BPF_CORE_READ, so only field NAMES and
 * types matter here — CO-RE relocates the actual offset against the
 * target kernel's own BTF at load time. The layout below never needs to
 * match a real kernel; only the field names "real_parent" and "tgid" do.
 *
 * struct trace_event_raw_sys_{enter,exit} are read via a plain (non-CO-RE)
 * pointer dereference, so — unlike task_struct — their field OFFSETS are
 * load-bearing: they must match the real ftrace-generated layout. That
 * layout is stable across kernel versions because every syscall's
 * sys_enter/sys_exit tracepoint shares one common event class; this is
 * the same struct bpftool emits into a full vmlinux.h on any modern kernel.
 */
#pragma once

struct task_struct {
    struct task_struct *real_parent;
    int tgid;
};

struct trace_entry {
    unsigned short type;
    unsigned char flags;
    unsigned char preempt_count;
    int pid;
};

struct trace_event_raw_sys_enter {
    struct trace_entry ent;
    long id;
    unsigned long args[6];
    char __data[0];
};

struct trace_event_raw_sys_exit {
    struct trace_entry ent;
    long id;
    long ret;
    char __data[0];
};
