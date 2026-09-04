package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

const (
	// defaultRequestTimeout bounds the whole one-shot request/response
	// exchange in Handle, including the wait for a decrypt slot below. A few
	// seconds is plenty for a local RPC; it exists so a peer that stalls
	// (never writes, or never reads its response) cannot pin a goroutine, an
	// fd, and a decrypted bundle in memory indefinitely.
	defaultRequestTimeout = 5 * time.Second

	// defaultMaxConcurrentDecrypts bounds how many LoadBundle calls — each a
	// live kms:Decrypt — may run at once. This is NOT a cache: decrypt-per-
	// request is the deliberate property that gives every unseal its own
	// CloudTrail record. The bound only caps blast radius, so a local
	// process spinning up connections cannot exhaust the account's shared
	// KMS quota and self-DoS every other consumer of the secrets CMK.
	defaultMaxConcurrentDecrypts = 4

	minAcceptBackoff = 5 * time.Millisecond
	maxAcceptBackoff = 1 * time.Second

	// refusalWriteGrace is a small, fixed extra write budget given to every
	// refusal response, independent of the request's original deadline. A
	// refusal can legitimately be decided AT the moment that deadline
	// expires (the concurrency-timeout case below), so reusing the same
	// already-elapsed deadline to send the refusal would make the write
	// fail too — the caller would see a dropped connection instead of a
	// clean {"error":...} and have no way to tell "refused" from "broker
	// crashed". Kept small (not a fresh multi-second window) so a genuinely
	// silent or unresponsive peer still cannot stall Handle materially past
	// its own RequestTimeout — this only covers the boundary race, not a
	// second full request timeout.
	refusalWriteGrace = 250 * time.Millisecond
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

	// RequestTimeout overrides defaultRequestTimeout. Zero means the default;
	// tests set it short to prove the bound actually fires.
	RequestTimeout time.Duration

	// MaxConcurrentDecrypts overrides defaultMaxConcurrentDecrypts. Zero
	// means the default.
	MaxConcurrentDecrypts int

	decryptSlots     chan struct{}
	decryptSlotsOnce sync.Once
}

func (s *Server) requestTimeout() time.Duration {
	if s.RequestTimeout > 0 {
		return s.RequestTimeout
	}
	return defaultRequestTimeout
}

// decryptSemaphore lazily sizes and returns the bound on concurrent
// LoadBundle calls. sync.Once makes this race-free without needing a
// constructor, since Server is built as a plain struct literal.
func (s *Server) decryptSemaphore() chan struct{} {
	s.decryptSlotsOnce.Do(func() {
		n := s.MaxConcurrentDecrypts
		if n <= 0 {
			n = defaultMaxConcurrentDecrypts
		}
		s.decryptSlots = make(chan struct{}, n)
	})
	return s.decryptSlots
}

// Serve accepts connections until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	backoff := minAcceptBackoff
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				// The listener died out from under us without ctx
				// cancellation: something is genuinely wrong. Return the
				// error so systemd restarts the daemon instead of the loop
				// spinning silently forever.
				return err
			}
			// Anything else (EMFILE/ENFILE and similar) is likely a
			// persistent local condition, not a one-off blip. Back off
			// instead of spinning at 100% CPU, and say so: a broker that has
			// gone silent because it cannot accept is otherwise
			// indistinguishable from a healthy, idle one.
			log.Printf("km-secretsd: accept: %v", err)
			time.Sleep(backoff)
			backoff *= 2
			if backoff > maxAcceptBackoff {
				backoff = maxAcceptBackoff
			}
			continue
		}
		backoff = minAcceptBackoff
		uid, pid := peerCred(conn)
		go func() {
			defer conn.Close()
			s.Handle(conn, uid, pid)
		}()
	}
}

// Handle serves exactly one request, then returns. Decryption happens here and
// nowhere else, and the bundle is zeroed before this function returns on every
// path that reaches it — including a peer that never writes a request and one
// that never reads its response, both bounded by RequestTimeout.
func (s *Server) Handle(conn net.Conn, uid, pid uint32) {
	deadline := time.Now().Add(s.requestTimeout())
	// One deadline for the whole one-shot exchange (decode below, and encode
	// further down) is simpler than separate read/write deadlines and
	// equally correct given Handle serves exactly one request per call.
	_ = conn.SetDeadline(deadline)

	var req secrets.UnsealRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		s.refuse(conn, uid, pid, req, fmt.Sprintf("malformed request: %v", err))
		return
	}

	// Bound concurrent decrypts (see defaultMaxConcurrentDecrypts). The wait
	// is capped by the same deadline as the I/O above; past that point the
	// request is refused — and audited, which is more signal than less, since
	// a uid hammering the broker is exactly the event an operator wants to see.
	sem := s.decryptSemaphore()
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-time.After(time.Until(deadline)):
		s.refuse(conn, uid, pid, req, "too many concurrent unseal requests")
		return
	}

	bundle, err := LoadBundle(s.CiphertextPath)
	if err != nil {
		// Fail closed. An empty success here would hand the agent a confusing
		// 401 instead of a diagnosable error.
		//
		// decryptFile's own error text is third-party (sops/KMS), not ours —
		// reviewed and confirmed to carry key-service/MAC-mismatch text, never
		// plaintext, so wrapping it here is safe. The YAML-parse branch inside
		// LoadBundle is sanitized separately; see bundle.go.
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
	// json.Encoder writes through its own internal bytes.Buffer, which ends
	// up holding a base64 copy of every value on the wire; that buffer is GC
	// garbage of indeterminate lifetime that this loop cannot reach. Same
	// unavoidable class of gap as the yaml.Unmarshal buffer bundle.go
	// documents for itself — noted here so the zeroing claim's boundary is
	// stated once, not partially in two places.
	for _, v := range resp.Values {
		zero(v)
	}
}

func (s *Server) refuse(conn net.Conn, uid, pid uint32, req secrets.UnsealRequest, msg string) {
	// A refusal is the more interesting security event, not the less.
	_ = s.Audit.Emit("secret_unseal_refused", map[string]any{
		"as": req.As, "uid": uid, "pid": pid, "exe": exeOf(pid), "reason": msg,
	})
	// Refresh the write deadline: the request's original deadline may
	// already be spent by the time we get here (see refusalWriteGrace).
	_ = conn.SetWriteDeadline(time.Now().Add(refusalWriteGrace))
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
