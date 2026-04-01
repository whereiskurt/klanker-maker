//go:build linux

// Package sni contains the TC egress classifier BPF program and Go attachment
// code for TLS SNI-based filtering. The classifier parses TLS ClientHello
// packets on port 443 and drops connections to disallowed SNI hostnames.
//
// This is a best-effort layer: fragmented ClientHellos (e.g. large Chrome
// hellos that exceed 1500 bytes) will pass through since TC cannot reassemble
// TCP segments.
//
// To regenerate the loader code and embedded bytecode (requires clang + Linux):
//
//	go generate ./pkg/ebpf/sni/
package sni

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags linux sni sni.c -- -I../headers -O2 -g -Wall -Werror
