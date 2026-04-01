//go:build linux && !amd64

// Stub for non-amd64 platforms (e.g., arm64 Lambda).
// The TLS uprobe observer only runs on EC2 x86_64 instances.
// This stub allows the package to compile on arm64 without BPF objects.
package tls
