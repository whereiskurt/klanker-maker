//go:build linux

package main

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerCred reads SO_PEERCRED. The values are recorded for ATTRIBUTION only and
// are NOT an authorization check: every legitimate caller is uid sandbox, and so
// is any malware on the box. Naming the asker is the product here, not gating it.
func peerCred(conn net.Conn) (uid, pid uint32) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, 0
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, 0
	}
	_ = raw.Control(func(fd uintptr) {
		if cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil {
			uid, pid = cred.Uid, uint32(cred.Pid)
		}
	})
	return uid, pid
}
