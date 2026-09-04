package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

// Server answers unseal requests on a unix socket.
//
// It is a LOGGED DOOR, not a wall. Uid sandbox must reach the socket for any of
// this to work, so anything running as that uid can speak this protocol
// directly rather than going through a shim. What changes is the character of
// the theft: it becomes an active, authenticated, pid-attributed request with a
// CloudWatch record and a discrete CloudTrail kms:Decrypt, instead of a silent
// read of a file that was sitting there anyway.
type Server struct {
	CiphertextPath string
	Grants         secrets.Grants
	Audit          AuditWriter
}

// Serve accepts connections until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue // a bad peer must never take the daemon down
		}
		uid, pid := peerCred(conn)
		go func() {
			defer conn.Close()
			s.Handle(conn, uid, pid)
		}()
	}
}

// Handle serves exactly one request, then returns. Decryption happens here and
// nowhere else, and the bundle is zeroed before this function returns.
func (s *Server) Handle(conn net.Conn, uid, pid uint32) {
	var req secrets.UnsealRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		s.refuse(conn, uid, pid, req, fmt.Sprintf("malformed request: %v", err))
		return
	}

	bundle, err := LoadBundle(s.CiphertextPath)
	if err != nil {
		// Fail closed. An empty success here would hand the agent a confusing
		// 401 instead of a diagnosable error.
		s.refuse(conn, uid, pid, req, fmt.Sprintf("decrypt failed: %v", err))
		return
	}
	defer bundle.Zero()

	keys, err := secrets.Resolve(bundle.Keys(), s.Grants, req.As, req.Only)
	if err != nil {
		s.refuse(conn, uid, pid, req, err.Error())
		return
	}

	resp := secrets.UnsealResponse{Keys: keys, Values: make(map[string][]byte, len(keys))}
	for _, k := range keys {
		v := bundle.Get(k)
		cp := make([]byte, len(v))
		copy(cp, v)
		resp.Values[k] = cp
	}

	_ = s.Audit.Emit("secret_unseal", map[string]any{
		"as": req.As, "keys": keys, "uid": uid, "pid": pid, "exe": exeOf(pid),
	})
	_ = json.NewEncoder(conn).Encode(resp)
	for _, v := range resp.Values {
		zero(v)
	}
}

func (s *Server) refuse(conn net.Conn, uid, pid uint32, req secrets.UnsealRequest, msg string) {
	// A refusal is the more interesting security event, not the less.
	_ = s.Audit.Emit("secret_unseal_refused", map[string]any{
		"as": req.As, "uid": uid, "pid": pid, "exe": exeOf(pid), "reason": msg,
	})
	_ = json.NewEncoder(conn).Encode(secrets.UnsealResponse{Error: msg})
}

// exeOf resolves the caller's binary for the audit record. Advisory only: uid
// sandbox can exec any binary it likes, so this names the asker, it does not
// authenticate them.
func exeOf(pid uint32) string {
	if pid == 0 {
		return ""
	}
	p, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return ""
	}
	return p
}
