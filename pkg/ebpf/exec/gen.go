//go:build linux

// Package exec contains the kernel-side BPF programs and userspace loader for
// klanker-maker process-execution tracing.
//
// It is deliberately a separate package with its own compiled object from
// pkg/ebpf: the enforcer's programs are cgroup-attached and gated on
// enforcement mode, while these are tracepoints that run on every sandbox. One
// object serving both would couple observation to enforcement, which is exactly
// the coupling that made Phase 131's flow recording dead under the default mode.
//
// To regenerate (requires clang WITH a BPF target — Apple clang has none):
//
//	CLANG=/opt/homebrew/opt/llvm/bin/clang go generate ./pkg/ebpf/exec/
package exec

// -mllvm -disable-loop-idiom-all is required: without it, LLVM's loop-idiom
// pass recognizes the zero_words() store loop in exec.c and rewrites it back
// into an llvm.memset call, which this BPF backend rejects outright ("A call
// to built-in function 'memset' is not supported") — the same wall that
// __builtin_memset hit directly on the ~2.6 KB scratch struct.
//
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags linux -target amd64 execbpf exec.c -- -I../headers -O2 -g -Wall -Werror -mllvm -disable-loop-idiom-all
