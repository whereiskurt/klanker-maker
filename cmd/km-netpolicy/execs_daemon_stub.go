//go:build !linux || !amd64

package main

import "fmt"

// runExecsDaemon reports plainly that the platform cannot host the tracer,
// rather than failing in a way that reads like a configuration problem.
func runExecsDaemon(o opts) error {
	fmt.Fprintln(o.stderr, "exec tracing requires linux/amd64")
	return errUnsupportedPlatform
}
