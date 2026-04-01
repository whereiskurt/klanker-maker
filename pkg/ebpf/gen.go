//go:build linux

// Package ebpf contains the kernel-side BPF programs and userspace loader for
// klankrmkr network enforcement. The BPF bytecode is compiled from bpf.c via
// bpf2go and embedded in the binary; no runtime clang dependency is required.
//
// To regenerate the loader code and embedded bytecode (requires clang):
//
//	go generate ./pkg/ebpf/
package ebpf

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags linux -target amd64 bpf bpf.c -- -I./headers -O2 -g -Wall -Werror
