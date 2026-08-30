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
// To regenerate: make generate-ebpf (runs inside a Docker image carrying a
// real Linux clang + system libbpf headers — see containers/Dockerfile.ebpf-generate).
// A native `go generate` on this package does not work: on darwin, gen.go's
// own `//go:build linux` tag excludes it from package matching entirely (a
// silent no-op), and even a `GOOS=linux`-forced native run fails since
// bpf2go's own binary then gets built for linux and can't execute on the
// darwin host. Homebrew clang also cannot substitute for the Docker image
// here regardless: `<bpf/bpf_helpers.h>` and `<bpf/bpf_core_read.h>` resolve
// only against a real system libbpf install, which this repo's own
// pkg/ebpf/headers/ (a flat, hand-rolled minimal set with no bpf/
// subdirectory) does not provide.
package exec

// -mllvm -disable-loop-idiom-all is required: without it, LLVM's loop-idiom
// pass recognizes the zero_words() store loop in exec.c and rewrites it back
// into an llvm.memset call, which this BPF backend rejects outright ("A call
// to built-in function 'memset' is not supported") — the same wall that
// __builtin_memset hit directly on the ~2.6 KB scratch struct.
//
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags linux -target amd64 execbpf exec.c -- -O2 -g -Wall -Werror -mllvm -disable-loop-idiom-all
