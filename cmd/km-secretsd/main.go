package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: km-secretsd serve")
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
	// The selftest verb is added in Task 7, once Selftest exists.
	default:
		fmt.Fprintf(os.Stderr, "km-secretsd: unknown verb %q\n", os.Args[1])
		os.Exit(2)
	}
}

func runServe(srv *Server) int {
	// Remove a stale socket from an unclean shutdown; systemd restarts us and a
	// leftover inode would make every bind fail.
	_ = os.Remove(secrets.SocketPath)

	ln, err := net.Listen("unix", secrets.SocketPath)
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
