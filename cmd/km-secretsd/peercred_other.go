//go:build !linux

package main

import "net"

// peerCred has no portable equivalent. The daemon only ever runs on the sandbox
// (Linux); this exists so the package compiles and tests on a dev machine.
func peerCred(net.Conn) (uid, pid uint32) { return 0, 0 }
