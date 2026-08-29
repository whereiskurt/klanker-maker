//go:build !linux

package main

import "fmt"

// runCaptureDaemon is a stub on non-Linux platforms. AF_PACKET raw sockets and
// SO_ATTACH_FILTER are Linux-only, so this keeps the command building and
// testable on a macOS dev machine without pulling the real capture code into
// the build. Every sandbox is Linux, so this path never runs in production.
func runCaptureDaemon(o opts) int {
	fmt.Fprintln(o.stderr, "packet capture requires Linux")
	return 1
}
