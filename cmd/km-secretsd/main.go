package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: km-secretsd serve|selftest")
		os.Exit(2)
	}

	grants := secrets.Grants{}
	if raw := os.Getenv("KM_SECRETS_GRANTS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &grants); err != nil {
			fmt.Fprintf(os.Stderr, "km-secretsd: bad KM_SECRETS_GRANTS: %v\n", err)
			os.Exit(1)
		}
	}
	srv := &Server{
		CiphertextPath: secrets.CiphertextPath,
		Grants:         grants,
		Audit:          &PipeAudit{Path: secrets.AuditPipePath, SandboxID: os.Getenv("KM_SANDBOX_ID")},
	}

	switch os.Args[1] {
	case "serve":
		os.Exit(runServe(srv))
	case "selftest":
		os.Exit(runSelftest(srv, secrets.SocketPath))
	default:
		fmt.Fprintf(os.Stderr, "km-secretsd: unknown verb %q\n", os.Args[1])
		os.Exit(2)
	}
}

func runServe(srv *Server) int {
	// A second daemon starting while one is already live (a manual invocation
	// racing the unit, or a restart overlapping the old process's shutdown)
	// must not unlink the live socket out from under it — that produces two
	// root daemons both holding decrypted material with systemctl only
	// showing one. Probe before removing.
	if socketLive(secrets.SocketPath) {
		fmt.Fprintf(os.Stderr, "km-secretsd: %s already answers; refusing to steal it from a live daemon\n", secrets.SocketPath)
		return 1
	}
	// Remove a stale socket from an unclean shutdown; systemd restarts us and a
	// leftover inode would make every bind fail. socketLive above already
	// confirmed nothing is listening on it.
	_ = os.Remove(secrets.SocketPath)

	// The socket must never be even briefly world-connectable between Listen
	// and the Chmod below, regardless of what UMask= the unit sets (or a
	// hand-run `km-secretsd serve` inherits from its shell).
	oldMask := syscall.Umask(0o177) // socket is born 0600
	ln, err := net.Listen("unix", secrets.SocketPath)
	syscall.Umask(oldMask)
	if err != nil {
		fmt.Fprintf(os.Stderr, "km-secretsd: listen: %v\n", err)
		return 1
	}
	// 0660 root:sandbox — the sandbox user must reach it; km-sidecar and world
	// must not. Group ownership is set by the systemd unit's ExecStartPost.
	if err := os.Chmod(secrets.SocketPath, 0o660); err != nil {
		fmt.Fprintf(os.Stderr, "km-secretsd: chmod socket: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := srv.Serve(ctx, ln); err != nil {
		fmt.Fprintf(os.Stderr, "km-secretsd: serve: %v\n", err)
		return 1
	}
	return 0
}

// socketLive reports whether something is already answering at path. A short
// dial timeout keeps this from hanging on a genuinely stale inode (a socket
// file left behind by an unclean shutdown, with nothing behind it).
func socketLive(path string) bool {
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
