//go:build linux

// Package tls contains the BPF programs and Go types for TLS uprobe
// observability. The BPF bytecode is compiled from openssl.bpf.c and
// connect.bpf.c via bpf2go and embedded in the binary; no runtime clang
// dependency is required.
//
// To regenerate the loader code and embedded bytecode (requires clang):
//
//	go generate ./pkg/ebpf/tls/
package tls

// Two separate bpf2go invocations for the two BPF C programs:
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags linux -target amd64 opensslBpf bpf/openssl.bpf.c -- -I../headers -O2 -g -Wall -Werror
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags linux -target amd64 connectBpf bpf/connect.bpf.c -- -I../headers -O2 -g -Wall -Werror
