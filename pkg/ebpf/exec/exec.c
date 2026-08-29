//go:build ignore

// SPDX-License-Identifier: GPL-2.0-only
/*
 * exec.c — Kernel-side process-execution tracing for klanker-maker.
 *
 * Programs:
 *   1. tracepoint/syscalls/sys_enter_execve    — capture argv into an in-flight slot
 *   2. tracepoint/syscalls/sys_enter_execveat  — same, for the execveat entry point
 *   3. tracepoint/syscalls/sys_exit_execve     — stamp the return code and emit
 *   4. tracepoint/sched/sched_process_exit     — emit a process-end marker
 *
 * The enter/exit split exists so FAILED execs are recorded: an agent reaching
 * for a binary that is absent or not permitted is a finding, and
 * sched_process_exec fires only on success.
 *
 * Program 4 exists so the userspace join can bound a process's lifetime. Without
 * it, pid reuse makes the flow-to-process join a confident lie.
 *
 * Build: make generate-ebpf (see pkg/ebpf/exec/gen.go for why a native
 * `go generate` on this package does not work).
 */

/* pkg/ebpf/headers/vmlinux.h is NOT included here — see vmlinux_extra.h for
 * why it conflicts outright with <linux/bpf.h>, which this file needs for
 * the BPF_MAP_TYPE_ constants and BPF_ANY. */
#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_core_read.h>
#include "vmlinux_extra.h"

#define MAX_ARGS      20
#define ARGSIZE       128
#define TASK_COMM_LEN 16

#define KIND_EXEC 0
#define KIND_EXIT 1

/* Split into a header and an argv tail so an exit record can be emitted as just
 * the header. The ring buffer carries each record's own length, so userspace
 * tells the two apart by size — an exit record costs 56 bytes instead of 2.6 KB,
 * and exits are as frequent as execs. */
struct exec_hdr {
    __u64 ts_ns;
    __u64 cgroup_id;
    __u32 pid;
    __u32 ppid;
    __u32 uid;
    __s32 ret;
    __u8  kind;
    __u8  truncated;
    __u8  nargs;
    __u8  _pad;
    char  comm[TASK_COMM_LEN];
    char  _pad2[4];   /* Explicit: C pads this struct to 56 for its 8-byte
                       * alignment, but Go's encoding/binary packs without
                       * padding. Making it a real field is what keeps the two
                       * decoders agreeing on where argv starts. */
};

struct exec_event {
    struct exec_hdr h;
    char args[MAX_ARGS][ARGSIZE];
};

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 1 << 22); /* 4 MiB */
} exec_events SEC(".maps");

/* In-flight execve between enter and exit, keyed by pid_tgid.
 *
 * LRU, not a plain hash: when a non-leader thread execs, the kernel's
 * de_thread() reassigns its pid to the tgid before sys_exit_execve runs, so
 * the exit-side lookup (keyed on the CURRENT pid_tgid) misses the entry this
 * thread's enter inserted and it is never deleted. That is narrow — it needs
 * a thread exec'ing without ever forking — but on a plain hash the orphaned
 * entries only accumulate, and once max_entries is reached
 * bpf_map_update_elem starts returning E2BIG and silently drops every NEW
 * exec thereafter: capture goes quiet with no error anywhere. An LRU evicts
 * one stale entry to make room instead of refusing the insert. */
struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);
    __uint(max_entries, 4096);
    __type(key, __u64);
    __type(value, struct exec_event);
} inflight SEC(".maps");

/* struct exec_event is ~2.6 KB and the BPF stack is 512 bytes, so the event is
 * assembled in a per-CPU scratch slot and copied from there. Building it on the
 * stack does not merely warn — the verifier rejects the program outright. */
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, struct exec_event);
} scratch SEC(".maps");

/* __builtin_memset over ~2.6 KB is rejected by this BPF backend outright
 * ("A call to built-in function 'memset' is not supported") rather than
 * inlined — LLVM lowers a memset above its inlining threshold to a libcall,
 * and there is no libc on this target. `nwords` is a compile-time constant
 * at every call site below (327 for the full event, 7 for the header alone),
 * but `#pragma unroll` does not turn that into straight-line code with no
 * runtime loop here: the compiled object still contains bounded runtime
 * loops (an unroll-by-8 pass over the 327-word case plus a 7-word
 * remainder), each storing through the scratch map-value pointer at a
 * loop-carried offset. That is ordinary bounded-loop verifier territory
 * (kernel >= 5.3, which this platform already requires elsewhere) rather
 * than the fully-unrolled straight-line form the pragma name suggests —
 * Task 4's live load against a real kernel is what actually confirms the
 * verifier accepts it, not this comment. Both call sites pass an exact
 * multiple of 8 (struct exec_event is 2616 bytes, struct exec_hdr is 56), so
 * there is no byte remainder to handle by hand. */
static __always_inline void zero_words(void *dst, int nwords)
{
    __u64 *p = (__u64 *)dst;
#pragma unroll
    for (int i = 0; i < nwords; i++)
        p[i] = 0;
}

static __always_inline int record_enter(const char *const *argv)
{
    __u32 zero = 0;
    struct exec_event *e = bpf_map_lookup_elem(&scratch, &zero);
    if (!e)
        return 0;

    zero_words(e, sizeof(*e) / 8);

    __u64 id = bpf_get_current_pid_tgid();
    e->h.kind      = KIND_EXEC;
    e->h.pid       = (__u32)(id >> 32);
    e->h.uid       = (__u32)bpf_get_current_uid_gid();
    e->h.cgroup_id = bpf_get_current_cgroup_id();

    struct task_struct *task = (struct task_struct *)bpf_get_current_task();
    e->h.ppid = BPF_CORE_READ(task, real_parent, tgid);

    int n = 0;
#pragma unroll
    for (int i = 0; i < MAX_ARGS; i++) {
        const char *p = NULL;
        if (bpf_probe_read_user(&p, sizeof(p), &argv[i]) != 0 || !p)
            goto done;
        if (bpf_probe_read_user_str(e->args[i], ARGSIZE, p) < 0)
            goto done;
        n = i + 1;
    }
    /* Every slot filled: check whether there was at least one more argument we
     * had no room for, so `truncated` means "we lost some" rather than "we
     * happened to use every slot". */
    {
        const char *p = NULL;
        if (bpf_probe_read_user(&p, sizeof(p), &argv[MAX_ARGS]) == 0 && p)
            e->h.truncated = 1;
    }
done:
    e->h.nargs = (__u8)n;
    bpf_map_update_elem(&inflight, &id, e, BPF_ANY);
    return 0;
}

SEC("tracepoint/syscalls/sys_enter_execve")
int tp_enter_execve(struct trace_event_raw_sys_enter *ctx)
{
    /* execve(const char *filename, char *const argv[], char *const envp[]) */
    return record_enter((const char *const *)ctx->args[1]);
}

SEC("tracepoint/syscalls/sys_enter_execveat")
int tp_enter_execveat(struct trace_event_raw_sys_enter *ctx)
{
    /* execveat(int dirfd, const char *pathname, char *const argv[], ...) */
    return record_enter((const char *const *)ctx->args[2]);
}

SEC("tracepoint/syscalls/sys_exit_execve")
int tp_exit_execve(struct trace_event_raw_sys_exit *ctx)
{
    __u64 id = bpf_get_current_pid_tgid();
    struct exec_event *e = bpf_map_lookup_elem(&inflight, &id);
    if (!e)
        return 0;

    e->h.ts_ns = bpf_ktime_get_boot_ns();
    e->h.ret   = (__s32)ctx->ret;
    /* On success this is the comm of the NEW image, which is what an operator
     * wants to see; on failure it is the caller's, which is also what they
     * want. Either way it is read here rather than at enter. */
    bpf_get_current_comm(&e->h.comm, sizeof(e->h.comm));

    bpf_ringbuf_output(&exec_events, e, sizeof(*e), 0);
    bpf_map_delete_elem(&inflight, &id);
    return 0;
}

SEC("tracepoint/sched/sched_process_exit")
int tp_process_exit(void *ctx)
{
    __u64 id   = bpf_get_current_pid_tgid();
    __u32 tgid = (__u32)(id >> 32);
    __u32 pid  = (__u32)id;

    /* This tracepoint fires for every TASK, threads included. Only the
     * thread-group leader's exit ends the process, and only that bounds the
     * lifetime the join cares about. Without this filter a threaded process
     * emits an exit record per thread and the join sees the process die early. */
    if (tgid != pid)
        return 0;

    __u32 zero = 0;
    struct exec_event *e = bpf_map_lookup_elem(&scratch, &zero);
    if (!e)
        return 0;

    zero_words(e, sizeof(e->h) / 8);
    e->h.kind  = KIND_EXIT;
    e->h.pid   = tgid;
    e->h.ts_ns = bpf_ktime_get_boot_ns();
    bpf_get_current_comm(&e->h.comm, sizeof(e->h.comm));

    /* Header only — see the struct comment. */
    bpf_ringbuf_output(&exec_events, e, sizeof(struct exec_hdr), 0);
    return 0;
}

char _license[] SEC("license") = "GPL";
